// Copyright 2026 workturnedplay
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package dml

import (
	"errors"
	"fmt"
)

// ListRegistry implements Ordered Lists (theorystate.md section 11
// / 11a) on top of CapsuleRegistry.
//
// A list is an ordinary node tagged (AllLists, list). List membership --
// which of a list's direct children are actual ElementCapsules, as
// opposed to unrelated metadata or comments a caller might attach later
// -- is identified through the ordinary (list, capsule) containment edge
// combined with the capsule's own (AllElementCapsules, capsule) tag,
// exactly per the discipline already established for CapsuleRegistry: a
// list's direct children are not assumed to all be capsules.
//
// Head and tail are each a plain tag on a capsule -- (AllHeads, capsule)
// and (AllTails, capsule) -- discovered as the single child of list
// tagged accordingly, via findUniqueTaggedChild. This deliberately does
// NOT use a further Pointer-style indirection (a dedicated head/tail
// slot node, the way Representation C/D uses slot nodes for subject/
// target): that indirection exists specifically to prevent two roles
// sharing the same *source* node from colliding into a single primitive
// relationship when their targets happen to be equal (section 1: (A,B)
// is a unique pair). Here the two roles have different sources --
// (AllHeads, X) and (AllTails, X) -- so they can never collide even when
// the same capsule X is simultaneously both head and tail, which is
// exactly the normal, expected state for a single-element list. Adding
// slot indirection here would be pure unneeded overhead.
//
// ListRegistry's mutating operations (NewList, Append, Prepend,
// InsertAfter, Remove, RemoveWithoutDeletingCapsule, DeleteList) each run
// as one transaction on the Transactor they are given (nested if it is a
// Tx), composing CapsuleRegistry's exported NewCapsule/SetPrev/SetNext/
// RemovePrev/RemoveNext/DeleteCapsule alongside direct tag
// (AddRelationship/RemoveRelationship) calls against the same tx. Any of
// them can therefore be called standalone or inside a larger
// transaction, where it shares that transaction's fate.
//
// As with every other registry in this file, list structure is
// re-derived fresh from the Graph on every call rather than cached.
//
// Cost limit (implementation_state.md items 88 and 92). Head and tail are tags
// on capsules, found as the child the list shares with the AllHeads/AllTails
// hub, and the hub has one member per non-empty list. That lookup is a
// leapfrog join whose cost is bounded by about twice the smaller of the two
// child sets and reaches the bound when the NodeIDs interleave (the long
// list's capsules alternating with the tails of other lists, which is what
// happens when lists grow over the same period). Measured for Tail and so for
// Append: about 0.5 microseconds per element, 57 ms at 100,000 elements. A
// commit touching only the list node validates the whole list. AppendMany
// looks the tail up once, so a bulk load does not pay it per element.
// SlottedListRegistry has none of these dependences: prefer it for a list that
// can grow long.
//
// Two removal paths are available: RemoveWithoutDeletingCapsule unlinks
// capsule from list only, leaving it standalone and intact (no cascading
// node deletion, consistent with theorystate.md section 18's
// rejection of automatic cascade delete); Remove does the same unlinking
// and then additionally reclaims capsule via CapsuleRegistry.DeleteCapsule
// whenever nothing else still references it. DeleteList removes a list
// itself once empty.
type ListRegistry struct {
	capsules *CapsuleRegistry
	allLists NodeID
	allHeads NodeID
	allTails NodeID
}

// NewListRegistry creates a ListRegistry over graph, using capsules for
// all per-capsule slot operations, allLists to tag list-kind nodes, and
// allHeads/allTails to tag a list's current head/tail capsule. All three
// tag NodeIDs must already exist -- typically via
// NameRegistry.BootstrapNames(FoundationalNames). capsules must already
// be constructed over the same graph.
func NewListRegistry(graph GraphAPI, capsules *CapsuleRegistry, allLists, allHeads, allTails NodeID) (*ListRegistry, error) {
	if existsErr := requireAllExist(graph, allLists, allHeads, allTails); existsErr != nil {
		return nil, existsErr
	}

	l := &ListRegistry{
		capsules: capsules,
		allLists: allLists,
		allHeads: allHeads,
		allTails: allTails,
	}

	// The Checker is local: it validates what the transaction touched
	// (checkTouched has the rules, and says what it deliberately leaves to
	// Elements and VerifyStructures) instead of every list the transaction
	// touched. Validating a whole list on every commit made adding the n-th
	// element cost O(n) (theorystate.md section 105, item 63 and 64 of
	// implementation_state.md).
	//
	// Tags: a list, a capsule, or a head/tail-tagged node being touched is
	// what can change a list's structure. Every legitimate ListRegistry
	// mutation touches the capsules it changes (a link change touches the
	// slot's target, a membership or boundary-tag change touches the
	// capsule), which is what makes a touched capsule a sufficient trigger.
	// This Checker only ever runs for mutations made through Transact (see
	// the Checker doc comment).
	// A raw, direct Graph.AddRelationship/RemoveRelationship call
	// bypassing Transact entirely -- exactly what every existing
	// out-of-band adversarial test in this file already does -- still
	// bypasses this Checker the same way it already bypasses every other
	// one; validateStructure's existing lazy, on-read enforcement (via
	// Elements) remains the backstop for that case, unaffected by this
	// addition.
	graph.RegisterChecker(Checker{
		Name:  fmt.Sprintf("ListRegistry(tag=%d)", allLists),
		Tags:  []NodeID{allLists, capsules.allElementCapsules, allHeads, allTails},
		Check: l.checkTouched,
	})

	return l, nil
}

// Capsules returns the CapsuleRegistry l makes its capsules with.
func (l *ListRegistry) Capsules() *CapsuleRegistry {
	return l.capsules
}

// IsMember implements OrderedList: capsule is an element of list when
// (list, capsule) exists.
func (l *ListRegistry) IsMember(graph GraphReader, list, capsule NodeID) (bool, error) {
	if requireErr := l.requireList(graph, list); requireErr != nil {
		return false, requireErr
	}

	has, err := graph.HasRelationship(list, capsule)

	return has, wrapInterfaceErr(err)
}

// IsList reports whether id is currently tagged (AllLists, id).
func (l *ListRegistry) IsList(graph GraphReader, id NodeID) (bool, error) {
	has, err := graph.HasRelationship(l.allLists, id)
	return has, wrapInterfaceErr(err)
}

// requireList checks that list exists and is tagged (AllLists, list),
// returning ErrNodeNotFound or ErrNotList otherwise. The mutating
// methods call it with their tx, so the check and the write see the same
// state. Delegates to the shared requireTagged helper (see its doc
// comment on PointerRegistry.requirePointer).
func (l *ListRegistry) requireList(graph GraphReader, list NodeID) error {
	return requireTagged(graph, list, l.allLists, ErrNotList)
}

// requireListValue is requireList plus a check that value exists.
func (l *ListRegistry) requireListValue(graph GraphReader, list, value NodeID) error {
	if requireErr := l.requireList(graph, list); requireErr != nil {
		return requireErr
	}

	return requireExists(graph, value)
}

// NewList creates a fresh NodeID and tags it (AllLists, id). The new list
// starts empty: no head, no tail, no element capsules.
func (l *ListRegistry) NewList(graph Transactor) (NodeID, error) {
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		return createTaggedNodeTx(tx, l.allLists)
	})
}

// Head returns list's current head capsule, if any. hasHead is false for
// an empty list.
func (l *ListRegistry) Head(graph GraphReader, list NodeID) (head NodeID, hasHead bool, err error) {
	return l.boundary(graph, list, l.allHeads)
}

// boundary returns the capsule of list tagged via (tag, capsule), if any:
// the shared body of Head (tag AllHeads) and Tail (tag AllTails). A tagged
// node that is not a capsule, or not linked into list, is
// ErrInvalidListStructure.
func (l *ListRegistry) boundary(graph GraphReader, list, tag NodeID) (capsule NodeID, found bool, err error) {
	if requireErr := l.requireList(graph, list); requireErr != nil {
		return 0, false, requireErr
	}

	capsule, found, err = findUniqueTaggedChild(graph, list, tag)
	if err != nil || !found {
		return capsule, found, err
	}

	isCapsule, capsuleErr := l.capsules.IsCapsule(graph, capsule)
	if capsuleErr != nil {
		return 0, false, capsuleErr
	}

	hasEdge, edgeErr := graph.HasRelationship(list, capsule)
	if edgeErr != nil {
		return 0, false, wrapInterfaceErr(edgeErr)
	}

	if !isCapsule || !hasEdge {
		return 0, false, ErrInvalidListStructure
	}

	return capsule, true, nil
}

// Tail returns list's current tail capsule, if any. hasTail is false for
// an empty list.
func (l *ListRegistry) Tail(graph GraphReader, list NodeID) (tail NodeID, hasTail bool, err error) {
	return l.boundary(graph, list, l.allTails)
}

// Append creates a fresh capsule holding value and links it as the new
// tail of list, entirely inside one transaction (nested if graph is a
// Tx, so a larger operation such as CompositeSetLogRegistry.
// AppendOperation can append as one step of its own transaction).
//
// If list is currently empty, the new capsule becomes both head and
// tail. Otherwise the new capsule is wired in after the current tail
// (new capsule's prev -> old tail, old tail's next -> new capsule), the
// old tail loses its AllTails tag, and the new capsule gains it.
//
// list must already be tagged (AllLists, list); value must already
// exist. Both are checked against tx itself, so a caller composing this
// into a larger transaction cannot act on a stale pre-check.
func (l *ListRegistry) Append(graph Transactor, list, value NodeID) (NodeID, error) {
	capsules, err := l.AppendMany(graph, list, []NodeID{value})
	if err != nil {
		return 0, err
	}

	return capsules[0], nil
}

// AppendMany appends one fresh capsule per value, in order, as the new
// tail of list, entirely inside one transaction (nested if graph is a Tx),
// and returns the capsules in the same order. The result is exactly what
// calling Append once per value would produce, but the current tail is
// looked up once, not once per value: Append's tail lookup reads every
// child of the list, so n separate Appends cost O(n squared) reads and one
// AppendMany costs O(n). A bulk load should use this.
//
// It is all-or-nothing: if any value is missing (ErrNodeNotFound) or any
// step fails, nothing is appended. With no values it only checks that list
// is a list and returns an empty result.
//
// list must already be tagged (AllLists, list) and every value must
// already exist. Both are checked against tx itself, so a caller composing
// this into a larger transaction cannot act on a stale pre-check.
func (l *ListRegistry) AppendMany(graph Transactor, list NodeID, values []NodeID) ([]NodeID, error) {
	return transactValue(graph, func(tx Tx) ([]NodeID, error) {
		if requireErr := l.requireList(tx, list); requireErr != nil {
			return nil, requireErr
		}

		tail, hasTail, err := findUniqueTaggedChild(tx, list, l.allTails)
		if err != nil {
			return nil, err
		}

		// Built fresh on every run of the closure (it may be re-run).
		capsules := make([]NodeID, 0, len(values))

		for _, value := range values {
			if requireErr := requireExists(tx, value); requireErr != nil {
				return nil, requireErr
			}

			capsule, newErr := l.capsules.NewCapsule(tx, value)
			if newErr != nil {
				return nil, newErr
			}

			if linkErr := addRelationshipTx(tx, list, capsule); linkErr != nil {
				return nil, linkErr
			}

			if hasTail {
				if prevErr := l.capsules.SetPrev(tx, capsule, tail); prevErr != nil {
					return nil, prevErr
				}
				if nextErr := l.capsules.SetNext(tx, tail, capsule); nextErr != nil {
					return nil, nextErr
				}
				if untagErr := removeRelationshipTx(tx, l.allTails, tail); untagErr != nil {
					return nil, untagErr
				}
			} else {
				if headErr := addRelationshipTx(tx, l.allHeads, capsule); headErr != nil {
					return nil, headErr
				}
			}

			if tailErr := addRelationshipTx(tx, l.allTails, capsule); tailErr != nil {
				return nil, tailErr
			}

			tail, hasTail = capsule, true
			capsules = append(capsules, capsule)
		}

		return capsules, nil
	})
}

// Prepend creates a fresh capsule holding value and links it as the new
// head of list, entirely inside one transaction (nested if graph is a
// Tx). Exact mirror of Append, swapping head/tail and prev/next roles.
//
// list must already be tagged (AllLists, list); value must already
// exist.
func (l *ListRegistry) Prepend(graph Transactor, list, value NodeID) (NodeID, error) {
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		if requireErr := l.requireListValue(tx, list, value); requireErr != nil {
			return 0, requireErr
		}

		oldHead, hasHead, err := findUniqueTaggedChild(tx, list, l.allHeads)
		if err != nil {
			return 0, err
		}

		capsule, err := l.capsules.NewCapsule(tx, value)
		if err != nil {
			return 0, err
		}

		if linkErr := addRelationshipTx(tx, list, capsule); linkErr != nil {
			return 0, linkErr
		}

		if hasHead {
			if nextErr := l.capsules.SetNext(tx, capsule, oldHead); nextErr != nil {
				return 0, nextErr
			}
			if prevErr := l.capsules.SetPrev(tx, oldHead, capsule); prevErr != nil {
				return 0, prevErr
			}
			if untagErr := removeRelationshipTx(tx, l.allHeads, oldHead); untagErr != nil {
				return 0, untagErr
			}
		} else {
			if tailErr := addRelationshipTx(tx, l.allTails, capsule); tailErr != nil {
				return 0, tailErr
			}
		}

		if headErr := addRelationshipTx(tx, l.allHeads, capsule); headErr != nil {
			return 0, headErr
		}

		return capsule, nil
	})
}

// InsertAfter creates a fresh capsule holding value and links it into
// list immediately after afterCapsule, entirely inside one transaction
// (nested if graph is a Tx).
//
// If afterCapsule was the tail, the new capsule becomes the new tail.
// Otherwise the new capsule is spliced in between afterCapsule and
// afterCapsule's old next capsule.
//
// list must already be tagged (AllLists, list); afterCapsule must
// already be an element of list (checked via the (list, afterCapsule)
// containment edge, returning ErrCapsuleNotInList otherwise); value must
// already exist.
func (l *ListRegistry) InsertAfter(graph Transactor, list, afterCapsule, value NodeID) (NodeID, error) {
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		if requireErr := l.requireListValue(tx, list, value); requireErr != nil {
			return 0, requireErr
		}

		inList, err := tx.HasRelationship(list, afterCapsule)
		if err != nil {
			return 0, wrapInterfaceErr(err)
		}
		if !inList {
			return 0, ErrCapsuleNotInList
		}

		oldNext, hasNext, err := l.capsules.Next(tx, afterCapsule)
		if err != nil {
			return 0, err
		}

		capsule, err := l.capsules.NewCapsule(tx, value)
		if err != nil {
			return 0, err
		}

		if linkErr := addRelationshipTx(tx, list, capsule); linkErr != nil {
			return 0, linkErr
		}

		if prevErr := l.capsules.SetPrev(tx, capsule, afterCapsule); prevErr != nil {
			return 0, prevErr
		}
		if nextErr := l.capsules.SetNext(tx, afterCapsule, capsule); nextErr != nil {
			return 0, nextErr
		}

		if hasNext {
			if oldNextErr := l.capsules.SetNext(tx, capsule, oldNext); oldNextErr != nil {
				return 0, oldNextErr
			}
			if oldPrevErr := l.capsules.SetPrev(tx, oldNext, capsule); oldPrevErr != nil {
				return 0, oldPrevErr
			}

			return capsule, nil
		}

		if untagErr := removeRelationshipTx(tx, l.allTails, afterCapsule); untagErr != nil {
			return 0, untagErr
		}
		if tailErr := addRelationshipTx(tx, l.allTails, capsule); tailErr != nil {
			return 0, tailErr
		}

		return capsule, nil
	})
}

// validateStructure checks the ordered-list invariants that are meaningful
// at this layer without imposing any restriction on unrelated primitive
// graph relationships. In particular, direct list children only count as
// elements when they are tagged AllElementCapsules; arbitrary non-capsule
// children remain permitted.
//
// The check deliberately walks the Next chain with a visited set. The
// primitive Graph permits cycles, but an ordered list is interpreted as a
// finite head-to-tail sequence. The same pass also checks list membership,
// reciprocal Prev/Next links, and that every capsule-tagged list member is
// actually reachable from the head. Thus a corrupted graph is rejected
// rather than silently producing a plausible partial sequence.
func (l *ListRegistry) validateStructure(graph GraphReader, list NodeID) error {
	head, hasHead, err := findUniqueTaggedChild(graph, list, l.allHeads)
	if err != nil {
		return err
	}
	tail, hasTail, err := findUniqueTaggedChild(graph, list, l.allTails)
	if err != nil {
		return err
	}

	outgoing, err := graph.FindOutgoing(list)
	if err != nil {
		return wrapInterfaceErr(err)
	}

	members := make(map[NodeID]struct{})
	for _, rel := range outgoing {
		isMember, memberErr := l.capsules.IsCapsule(graph, rel.To)
		if memberErr != nil {
			return memberErr
		}
		if isMember {
			if soleErr := l.requireSoleList(graph, rel.To); soleErr != nil {
				return soleErr
			}

			members[rel.To] = struct{}{}
		}
	}

	if !hasHead || !hasTail {
		if hasHead || hasTail || len(members) != 0 {
			return ErrInvalidListStructure
		}
		return nil
	}

	return validateChain(graph, l.capsules, list, head, tail, members)
}

// ListsOf returns every list that capsule is a direct child of, ascending. A
// member capsule belongs to exactly one (see requireSoleList); more than one
// can only come from a hand-written transaction or foreign data. Code that
// needs to know which list a capsule is in should use this, not the raw edge.
func (l *ListRegistry) ListsOf(graph GraphReader, capsule NodeID) ([]NodeID, error) {
	incoming, err := graph.FindIncoming(capsule)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}

	return l.listsAmong(graph, incoming)
}

// listsAmong returns the sources of incoming that are lists.
func (l *ListRegistry) listsAmong(graph GraphReader, incoming []Relationship) ([]NodeID, error) {
	lists := make([]NodeID, 0, 1)

	for _, rel := range incoming {
		isList, listErr := l.IsList(graph, rel.From)
		if listErr != nil {
			return nil, listErr
		}
		if isList {
			lists = append(lists, rel.From)
		}
	}

	return lists, nil
}

// requireSoleList returns ErrInvalidListStructure unless capsule is a child of
// exactly one list: its Prev and Next links describe a position in one chain,
// so a second list would share, and so silently change, that position. (A
// list that must appear in two places holds a pointer to it, or is the value
// of a capsule, which is not sharing.)
func (l *ListRegistry) requireSoleList(graph GraphReader, capsule NodeID) error {
	owners, err := l.ListsOf(graph, capsule)
	if err != nil {
		return err
	}

	if len(owners) != 1 {
		return ErrInvalidListStructure
	}

	return nil
}

// rejectBoundaryTags returns ErrInvalidListStructure if node is tagged as a
// list's head or tail. It is asked about a node that is not a member capsule
// of any list (a non-capsule, or a capsule linked into no list), which has
// no business carrying a boundary tag.
func (l *ListRegistry) rejectBoundaryTags(g GraphReader, node NodeID) error {
	for _, tag := range []NodeID{l.allHeads, l.allTails} {
		tagged, err := g.HasRelationship(tag, node)
		if err != nil {
			return wrapInterfaceErr(err)
		}
		if tagged {
			return ErrInvalidListStructure
		}
	}

	return nil
}

// checkCapsule validates one capsule in the neighbourhood of its own links
// (see checkTouched) and records in verified every list it is a member of.
// A capsule that is a member of no list is only checked for boundary tags.
func (l *ListRegistry) checkCapsule(g GraphReader, capsule NodeID, verified map[NodeID]struct{}) error {
	incoming, err := g.FindIncoming(capsule)
	if err != nil {
		return wrapInterfaceErr(err)
	}

	lists, listsErr := l.listsAmong(g, incoming)
	if listsErr != nil {
		return listsErr
	}

	if len(lists) == 0 {
		return l.rejectBoundaryTags(g, capsule)
	}

	// A member capsule belongs to exactly one list (see requireSoleList).
	if len(lists) > 1 {
		return ErrInvalidListStructure
	}

	list := lists[0]
	verified[list] = struct{}{}

	isHead, headErr := g.HasRelationship(l.allHeads, capsule)
	if headErr != nil {
		return wrapInterfaceErr(headErr)
	}

	isTail, tailErr := g.HasRelationship(l.allTails, capsule)
	if tailErr != nil {
		return wrapInterfaceErr(tailErr)
	}

	return checkCapsuleInChain(g, l.capsules, list, capsule, isHead, isTail, incoming)
}

// checkTouched is the commit-time Checker of ListRegistry. It costs time
// proportional to what the transaction touched, not to the length of the
// lists involved (validateStructure, which it replaced here, reads a whole
// list, so adding the n-th element cost O(n); see item 63 of
// implementation_state.md for the measurements).
//
//   - Every touched capsule that is a member of a list is checked locally
//     (checkCapsule): its value, head/tail tags against its links, its
//     neighbours' back links, and stale pointers at it.
//   - A touched node that is not a member capsule must not carry a head or
//     tail tag.
//   - A touched list none of whose member capsules was touched is validated
//     in full (validateStructure): nothing local says what changed.
//
// What this does not check at commit time, because it cannot be checked
// without reading the whole list: that the list has a single head and a
// single tail, that no second chain is unreachable from the head, and that
// there is no cycle apart from the head. A history of valid operations
// cannot produce these; a hand-written transaction or foreign data could.
// Elements checks them on every read and VerifyStructures at startup.
func (l *ListRegistry) checkTouched(g GraphReader, touched map[NodeID]struct{}) error {
	var capsules, lists []NodeID

	for node := range touched {
		isCapsule, capsuleErr := l.capsules.IsCapsule(g, node)
		if capsuleErr != nil {
			return capsuleErr
		}

		if isCapsule {
			capsules = append(capsules, node)
		} else if tagErr := l.rejectBoundaryTags(g, node); tagErr != nil {
			return tagErr
		}

		isList, listErr := l.IsList(g, node)
		if listErr != nil {
			return listErr
		}
		if isList {
			lists = append(lists, node)
		}
	}

	verified := make(map[NodeID]struct{})

	for _, capsule := range capsules {
		if capsuleCheckErr := l.checkCapsule(g, capsule, verified); capsuleCheckErr != nil {
			return capsuleCheckErr
		}
	}

	for _, list := range lists {
		if _, done := verified[list]; done {
			continue
		}

		if structureErr := l.validateStructure(g, list); structureErr != nil {
			return structureErr
		}
	}

	return nil
}

// VerifyStructures validates every list in full (validateStructure), the
// startup counterpart of the local commit-time Checker: it also checks what
// that cannot (a single head and tail, nothing unreachable from the head, no
// cycle). It is fail-closed: the first list that fails is returned, wrapped
// in ErrLoadVerification, and nothing is repaired. Each list is read in one
// transaction, so it is consistent, and the cost is linear in the size of
// the lists. A CompositeSetLog is a list too, so it is covered.
func (l *ListRegistry) VerifyStructures(graph GraphAPI) error {
	return verifyLists(graph, l.allLists, l.validateStructure)
}

// Elements returns list's current values, in head-to-tail order, by
// traversing the capsule chain via CapsuleRegistry.Next. It first validates
// the list structure so out-of-band mutations cannot turn a corrupted
// chain into a silently accepted partial or cross-list traversal.
func (l *ListRegistry) Elements(graph GraphReader, list NodeID) ([]NodeID, error) {
	if requireErr := l.requireList(graph, list); requireErr != nil {
		return nil, requireErr
	}

	err := l.validateStructure(graph, list)
	if err != nil {
		return nil, err
	}

	var values []NodeID

	// The primitive Graph intentionally permits arbitrary cycles. The list
	// layer, however, interprets Next as a finite ordered chain, so traversal
	// must detect a repeated capsule rather than relying on a timeout or
	// assuming that well-formed construction is the only possible state.
	visited := make(map[NodeID]struct{})

	current, hasCurrent, err := findUniqueTaggedChild(graph, list, l.allHeads)
	if err != nil {
		return nil, err
	}

	for hasCurrent {
		if _, seen := visited[current]; seen {
			return nil, ErrListCycle
		}
		visited[current] = struct{}{}

		value, hasValue, err := l.capsules.Value(graph, current)
		if err != nil {
			return nil, err
		}
		if hasValue {
			values = append(values, value)
		}

		current, hasCurrent, err = l.capsules.Next(graph, current)
		if err != nil {
			return nil, err
		}
	}

	return values, nil
}

// stepBack returns the capsule before capsule in list, checking only what one
// step needs: capsule is a member of list, and its previous capsule's next
// link points back at it. A walk from the tail with stepBack therefore costs
// per step, not per list; the whole-list properties are left to Elements and
// VerifyStructures. hasPrev is false for the head.
func (l *ListRegistry) stepBack(graph GraphReader, list, capsule NodeID) (NodeID, bool, error) {
	return stepBackWithin(graph, l.capsules, list, capsule)
}

// OccurrencesOf returns every capsule within list whose value equals
// value, in no particular semantic order (see the ordering note on
// CapsuleRegistry.CapsulesWithValue, which this is built directly on
// top of).
//
// This exists because a value may legitimately occur more than once in
// the same list, each occurrence via its own capsule
// (theorystate.md section 75's occurrence-identity distinction) --
// Contains below only
// needs to know whether at least one occurrence exists, but some callers
// legitimately need all of them (e.g. removing every occurrence of a
// value, or counting duplicates).
//
// list must already be tagged (AllLists, list). If value does not
// exist, this fails with ErrNodeNotFound, via
// CapsuleRegistry.CapsulesWithValue.
func (l *ListRegistry) OccurrencesOf(graph GraphReader, list, value NodeID) ([]NodeID, error) {
	if requireErr := l.requireList(graph, list); requireErr != nil {
		return nil, requireErr
	}

	candidates, err := l.capsules.CapsulesWithValue(graph, value)
	if err != nil {
		return nil, err
	}

	var occurrences []NodeID

	for _, capsule := range candidates {
		inList, inListErr := graph.HasRelationship(list, capsule)
		if inListErr != nil {
			return nil, wrapInterfaceErr(inListErr)
		}
		if inList {
			occurrences = append(occurrences, capsule)
		}
	}

	return occurrences, nil
}

// Contains reports whether value currently occurs at least once in
// list, returning one such capsule if so. If value occurs more than
// once, which capsule is returned is unspecified -- see OccurrencesOf
// to find every occurrence.
//
// Built directly on OccurrencesOf: per theorystate.md section 11's
// note that a Set-like membership index "may be added later" for this
// exact query, no new node, tag, or index structure turned out to be
// necessary -- see the CapsuleRegistry.CapsulesWithValue doc comment for
// why the existing list/capsule/value-slot structure already supports
// this query in time proportional to how many places value is
// referenced, not to list's length.
//
// list must already be tagged (AllLists, list). If value does not
// exist, this fails with ErrNodeNotFound.
func (l *ListRegistry) Contains(graph GraphReader, list, value NodeID) (capsule NodeID, found bool, err error) {
	occurrences, err := l.OccurrencesOf(graph, list, value)
	if err != nil {
		return 0, false, err
	}

	if len(occurrences) == 0 {
		return 0, false, nil
	}

	return occurrences[0], true, nil
}

// RemoveWithoutDeletingCapsule unlinks capsule from list, relinking
// capsule's neighbors (if any) around the gap and updating head/tail
// tagging as needed, entirely inside one Graph.Transact call. capsule's
// own prev/next slots are cleared as part of the same transaction, since
// they described its position within the list it is now leaving -- this
// leaves capsule as a standalone, valid, empty-linked capsule rather
// than one carrying stale links into a list it is no longer part of.
//
// This does not delete capsule itself, or its role-slot nodes -- no
// cascade deletion, consistent with theorystate.md section 18's
// rejection of deleteNodeAndRelationships. capsule keeps its
// AllElementCapsules tag and its value: list membership is a separate
// concern from capsule-kind or value identity (theorystate.md
// section 10c -- the same node identity can participate in multiple
// interpretations without changing its primitive facts).
//
// This is the lower-level primitive Remove (below) builds on: Remove
// calls this method first and then attempts CapsuleRegistry.DeleteCapsule
// as a second, separate step. Call this method directly instead of
// Remove when capsule must unconditionally survive removal regardless of
// whether it happens to be otherwise unreferenced -- e.g. a caller
// planning to immediately re-link capsule into a different list or
// position.
//
// list must already be tagged (AllLists, list); capsule must currently
// be an element of list (checked via the (list, capsule) containment
// edge, returning ErrCapsuleNotInList otherwise).
func (l *ListRegistry) RemoveWithoutDeletingCapsule(graph Transactor, list, capsule NodeID) error {
	// Membership is checked against tx, so a caller composing this into
	// a larger transaction (CompositeSetLogRegistry.RemoveOperation)
	// cannot act on a stale check.
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		if requireErr := l.requireList(tx, list); requireErr != nil {
			return requireErr
		}

		inList, err := tx.HasRelationship(list, capsule)
		if err != nil {
			return wrapInterfaceErr(err)
		}
		if !inList {
			return ErrCapsuleNotInList
		}

		prev, hasPrev, err := l.capsules.Prev(tx, capsule)
		if err != nil {
			return err
		}

		next, hasNext, err := l.capsules.Next(tx, capsule)
		if err != nil {
			return err
		}

		switch {
		case hasPrev && hasNext:
			// Removing a middle element: splice prev and next together
			// directly. Head/tail are unaffected.
			if err2 := l.capsules.SetNext(tx, prev, next); err2 != nil {
				return err2
			}
			if err3 := l.capsules.SetPrev(tx, next, prev); err3 != nil {
				return err3
			}

		case hasPrev:
			// capsule was the tail: prev becomes the new tail.
			if _, err4 := l.capsules.RemoveNext(tx, prev); err4 != nil {
				return err4
			}
			if err5 := removeRelationshipTx(tx, l.allTails, capsule); err5 != nil {
				return err5
			}
			if err6 := addRelationshipTx(tx, l.allTails, prev); err6 != nil {
				return err6
			}

		case hasNext:
			// capsule was the head: next becomes the new head.
			if _, err7 := l.capsules.RemovePrev(tx, next); err7 != nil {
				return err7
			}
			if err8 := removeRelationshipTx(tx, l.allHeads, capsule); err8 != nil {
				return err8
			}
			if err9 := addRelationshipTx(tx, l.allHeads, next); err9 != nil {
				return err9
			}

		default:
			// capsule was the sole element: the list becomes empty.
			if err10 := removeRelationshipTx(tx, l.allHeads, capsule); err10 != nil {
				return err10
			}
			if err11 := removeRelationshipTx(tx, l.allTails, capsule); err11 != nil {
				return err11
			}
		}

		if _, err12 := l.capsules.RemovePrev(tx, capsule); err12 != nil {
			return err12
		}
		if _, err13 := l.capsules.RemoveNext(tx, capsule); err13 != nil {
			return err13
		}

		return removeRelationshipTx(tx, list, capsule)
	}))
}

// Remove unlinks capsule from list via RemoveWithoutDeletingCapsule, and
// then additionally attempts to delete capsule and its three role-slot
// nodes via CapsuleRegistry.DeleteCapsule -- this is the list structure
// cleaning up after itself: a capsule exists only to represent one
// occurrence of a value within a list (theorystate.md section 75),
// so once it is removed from its (only) list and nothing else has taken
// an interest in it, there is no reason to leave it behind as an orphan.
//
// deleted reports whether the capsule was actually deleted. Deletion is
// best-effort: the removal is this call's own work, and DeleteCapsule
// runs as a nested transaction (a savepoint). If capsule turns out not
// to be safely deletable -- some further reference to it or one of its
// role slots exists beyond what removal itself cleared, e.g. it was also
// (unusually) referenced by something outside this list -- the nested
// transaction undoes only itself, the removal still commits, and deleted
// is false with err nil: this is not a failure of Remove, it simply
// means capsule was left in place, standalone and still valid, exactly
// as RemoveWithoutDeletingCapsule already leaves it (see
// TestListRemoveWithoutDeletingCapsuleClearsCapsuleOwnLinks). err is
// reserved for genuine failures: list not tagged, capsule not currently
// an element of list, or an unexpected error from either underlying
// call.
//
// The whole call is one transaction, so no other goroutine can observe
// the intermediate "unlinked but not yet deleted" state, and if graph is
// a Tx the removal (and the deletion, if it happens) commit or roll back
// with the enclosing transaction.
//
// list must already be tagged (AllLists, list); capsule must currently
// be an element of list, exactly like RemoveWithoutDeletingCapsule.
func (l *ListRegistry) Remove(graph Transactor, list, capsule NodeID) (deleted bool, err error) {
	return transactBool(graph, func(tx Tx) (bool, error) {
		if removeErr := l.RemoveWithoutDeletingCapsule(tx, list, capsule); removeErr != nil {
			return false, removeErr
		}

		deleteErr := l.capsules.DeleteCapsule(tx, capsule)
		if deleteErr != nil {
			if errors.Is(deleteErr, ErrCapsuleNotEmpty) {
				return false, nil
			}

			return false, deleteErr
		}

		return true, nil
	})
}

// DeleteList deletes list from the underlying graph, additionally
// removing its (AllLists, list) tag as part of the same transaction.
//
// Unlike NameRegistry.DeleteNode's coordinated delete -- which deletes
// the primitive node first and only then cleans up name bookkeeping that
// lives entirely outside the graph -- the (AllLists, list) tag is itself
// an ordinary primitive relationship *into* list, and therefore itself
// counts toward list's relationship count. It must be removed *before*
// Graph.DeleteNode can succeed, not after. This method's own
// Graph.Transact call is what makes that safe: if list turns out not to
// be empty and the underlying DeleteNode call fails with
// ErrNodeNotEmpty, the tag removal that already happened is rolled back
// automatically, leaving list exactly as tagged and populated as before
// this call -- rather than leaving a list untagged but still present
// with orphaned element capsules.
//
// Per theorystate.md section 18, deletion is deliberately "delete
// only if empty," not cascade: DeleteList refuses (ErrNodeNotEmpty,
// resolvable by clearing and retrying -- unlike RootGraph's
// ErrCannotDeleteRoot, this is not structurally permanent) if list
// currently has any element capsules or any other relationship at all.
// Callers wanting to delete a non-empty list must first Remove every
// element capsule; DeleteList does not touch those now-detached capsule
// nodes at all.
//
// list must currently be tagged (AllLists, list).
func (l *ListRegistry) DeleteList(graph Transactor, list NodeID) error {
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		if requireErr := l.requireList(tx, list); requireErr != nil {
			return requireErr
		}
		// Tag removal and delete are steps of this one transaction: if
		// the delete fails (e.g. ErrNodeNotEmpty), Transact's rollback
		// restores the (AllLists, list) tag. See untagAndDeleteNodeTx and
		// theorystate.md section 78.
		return untagAndDeleteNodeTx(tx, list, l.allLists)
	}))
}
