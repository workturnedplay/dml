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

// CapsuleRegistry implements the ElementCapsule primitive of Ordered
// Lists (theorystate.md section 11 / 11a): each list-element
// *occurrence* gets its own freshly-minted
// NodeID (the capsule) rather than reusing the value's own NodeID, so the
// same value can occur multiple times in a list through different
// capsules (theorystate.md section 75's occurrence/role-identity
// pattern).
//
// A capsule's previous, value, and next roles are each represented by a
// dedicated intermediary slot node -- exactly Pointer Representation B
// (theorystate.md section 10 / 10b), applied three times under
// three different tags:
//
//	(AllElementCapsules, capsule)
//	capsule -> Uprev    (AllElementCapsulePrevSlot, Uprev)   Uprev -> prevCapsule
//	capsule -> Uvalue   (AllElementCapsuleValueSlot, Uvalue) Uvalue -> value
//	capsule -> Unext    (AllElementCapsuleNextSlot, Unext)   Unext -> nextCapsule
//
// Each slot's own target is enforced to be at most one using the exact
// same PointerRegistry type already used for Representations A and B;
// CapsuleRegistry embeds three PointerRegistry instances -- one per role,
// distinguished only by tag, per theorystate.md section 76's
// tag-parameterization discipline -- rather than reimplementing "at most
// one target" a third time.
//
// The three slot roles are discovered by their own tag
// (findUniqueTaggedChild), never by position or by exclusion, so a
// capsule remains free to carry additional, unrelated children later
// without disturbing role discovery -- the same discipline established
// for PointerMetadataRegistryD.
//
// CapsuleRegistry does not itself know about lists, heads, or tails, and
// does not itself decide when a capsule is linked into or unlinked from a
// list -- it only mints and wires individual capsules and their three
// roles. List-level operations (append/prepend/insert, head/tail
// bookkeeping) are a separate, higher layer to be built on top of this
// one; not implemented yet. Per discussion, head/tail is expected to be a
// plain (AllHEADs, capsule) / (AllTAILs, capsule) tag pair discovered via
// findUniqueTaggedChild, not a further Pointer-style indirection: unlike
// Representation C/D's subject/target collision risk, (AllHEADs, X) and
// (AllTAILs, X) are already two distinct relationships even when the same
// capsule X is simultaneously both head and tail (a single-element list).
type CapsuleRegistry struct {
	allElementCapsules NodeID
	prevSlots          *PointerRegistry
	valueSlots         *PointerRegistry
	nextSlots          *PointerRegistry
}

// NewCapsuleRegistry creates a CapsuleRegistry over graph.
// allElementCapsules tags capsule-kind nodes; allPrevSlot, allValueSlot,
// and allNextSlot each tag a capsule's respective role-slot node. All
// four must already exist -- typically via
// NameRegistry.BootstrapNames(FoundationalNames). allPrevSlot,
// allValueSlot, and allNextSlot's existence is checked by the embedded
// NewPointerRegistry calls; allElementCapsules is checked here.
func NewCapsuleRegistry(graph GraphAPI, allElementCapsules, allPrevSlot, allValueSlot, allNextSlot NodeID) (*CapsuleRegistry, error) {
	if existsErr := requireExists(graph, allElementCapsules); existsErr != nil {
		return nil, existsErr
	}

	prevSlots, err := NewPointerRegistry(graph, allPrevSlot)
	if err != nil {
		return nil, err
	}

	valueSlots, err := NewPointerRegistry(graph, allValueSlot)
	if err != nil {
		return nil, err
	}

	nextSlots, err := NewPointerRegistry(graph, allNextSlot)
	if err != nil {
		return nil, err
	}

	c := &CapsuleRegistry{
		allElementCapsules: allElementCapsules,
		prevSlots:          prevSlots,
		valueSlots:         valueSlots,
		nextSlots:          nextSlots,
	}

	// Registered against c itself (rather than against the individual
	// tag NodeIDs, the way the simpler registries above do), since this
	// Checker's Check closure needs c.wellFormed -- which needs the
	// fully assembled CapsuleRegistry, not just the three underlying
	// PointerRegistry instances, to also check per-slot ownership, not
	// merely per-slot cardinality. c is fully initialized above before
	// this closure is ever invoked; capturing it by reference here is
	// safe because Check only ever runs later, during some future
	// Graph.Transact call, never during this constructor itself.
	graph.RegisterChecker(Checker{
		Name: fmt.Sprintf("CapsuleRegistry(tag=%d)", allElementCapsules),
		Tags: []NodeID{allElementCapsules},
		Check: func(g GraphReader, touched map[NodeID]struct{}) error {
			for node := range touched {
				tagged, taggedErr := g.HasRelationship(allElementCapsules, node)
				if taggedErr != nil {
					return wrapInterfaceErr(taggedErr)
				}
				if !tagged {
					continue
				}

				if err2 := c.wellFormed(g, node); err2 != nil {
					return err2
				}
			}

			return nil
		},
	})

	return c, nil
}

// IsCapsule reports whether id is currently tagged
// (AllElementCapsules, id).
func (c *CapsuleRegistry) IsCapsule(graph GraphReader, id NodeID) (bool, error) {
	has, err := graph.HasRelationship(c.allElementCapsules, id)
	return has, wrapInterfaceErr(err)
}

// requireCapsule checks that id exists and is tagged
// (AllElementCapsules, id), returning ErrNodeNotFound or ErrNotCapsule
// otherwise. Delegates to the shared requireTagged helper (see its doc
// comment on PointerRegistry.requirePointer).
func (c *CapsuleRegistry) requireCapsule(graph GraphReader, id NodeID) error {
	return requireTagged(graph, id, c.allElementCapsules, ErrNotCapsule)
}

// slotFor returns capsule's role-slot child tagged via (tag, slot) --
// e.g. its prev, value, or next slot -- found by tag rather than by
// position, so a capsule may carry additional, unrelated children later
// without disturbing role discovery. found is false if capsule has no
// such slot yet, which for a capsule created via NewCapsule should only
// happen due to an out-of-band mutation, since NewCapsule always creates
// all three slots up front.
//
// The discovered slot must also have capsule as its unique
// AllElementCapsules-tagged parent. This second check is important because
// the primitive Graph permits arbitrary additional parents: a raw mutation
// could otherwise make one capsule point at another capsule's role slot and
// silently alias that role. Unrelated non-capsule parents remain permitted;
// two distinct capsule-tagged parents produce ErrAmbiguousPointerMetadata.
// capsule's existence is checked implicitly by the underlying lookups.
func (c *CapsuleRegistry) slotFor(graph GraphReader, capsule, tag NodeID) (slot NodeID, found bool, err error) {
	return findOwnedSlot(graph, capsule, c.allElementCapsules, tag)
}

// wellFormed reports whether capsule currently has exactly the fixed
// shape buildCapsuleTx itself establishes: all three role slots (prev,
// value, next) present and uniquely owned by capsule (via slotFor), and
// each slot's own "at most one target" Pointer invariant intact (via the
// underlying PointerRegistry.Target for that role).
//
// This bundles into one comprehensive verdict what was previously only
// answerable by making three separate slotFor/Value/Prev/Next-style
// calls and noticing if any of them failed -- there was no single
// function to ask "is this capsule well-formed, full stop" before this.
// It exists primarily to back the Checker registered by
// NewCapsuleRegistry (see Graph.RegisterChecker) for eager, commit-time
// enforcement.
//
// DeleteCapsule deliberately does not call this: DeleteCapsule's own
// all-or-nothing teardown already gets an equivalent guarantee for free
// by attempting the real deletes directly and relying on Transact's
// existing rollback if any of them turns out to fail (see DeleteCapsule's
// own doc comment) -- a pre-check here would only be redundant work for
// that specific caller, not a missed reuse opportunity.
func (c *CapsuleRegistry) wellFormed(graph GraphReader, capsule NodeID) error {
	isCapsule, err := c.IsCapsule(graph, capsule)
	if err != nil {
		return err
	}
	if !isCapsule {
		return ErrNotCapsule
	}

	for _, slots := range []*PointerRegistry{c.prevSlots, c.valueSlots, c.nextSlots} {
		slot, found, slotErr := c.slotFor(graph, capsule, slots.allPointers)
		if slotErr != nil {
			return slotErr
		}
		if !found {
			return ErrNotCapsule
		}

		if _, _, targetErr := slots.Target(graph, slot); targetErr != nil {
			return targetErr
		}
	}

	return nil
}

// buildCapsuleTx creates a fresh capsule NodeID, tags it via
// (allElementCapsules, capsule), and wires all three of its role slots
// (prev, value, next) -- each via the shared newPointerTx create-and-tag
// sequence -- against tx. This is the body behind
// CapsuleRegistry.NewCapsule, factored out as a free function
// parameterized entirely over tag NodeIDs. Callers that mint a capsule as
// one step of a larger operation (ListRegistry.Append/Prepend/
// InsertAfter) call NewCapsule with their own tx, which nests.
//
// The value slot's target is set to value immediately, since a freshly
// created slot trivially satisfies the Pointer invariant (it starts
// childless), exactly like PointerRegistry.NewPointer. The prev and next
// slots are left empty: a capsule with no preceding or following
// neighbor is a normal, valid state -- e.g. a single-element list's sole
// capsule is simultaneously head and tail, with both slots empty.
func buildCapsuleTx(tx txOps, allElementCapsules, allPrevSlot, allValueSlot, allNextSlot, value NodeID) (NodeID, error) {
	capsule, err := createTaggedNodeTx(tx, allElementCapsules)
	if err != nil {
		return 0, err
	}

	prevSlot, err := newPointerTx(tx, allPrevSlot)
	if err != nil {
		return 0, err
	}
	if _, err2 := tx.AddRelationship(capsule, prevSlot); err2 != nil {
		return 0, wrapInterfaceErr(err2)
	}

	valueSlot, err := newPointerTx(tx, allValueSlot)
	if err != nil {
		return 0, err
	}
	if _, err3 := tx.AddRelationship(capsule, valueSlot); err3 != nil {
		return 0, wrapInterfaceErr(err3)
	}
	if _, err4 := tx.AddRelationship(valueSlot, value); err4 != nil {
		return 0, wrapInterfaceErr(err4)
	}

	nextSlot, err := newPointerTx(tx, allNextSlot)
	if err != nil {
		return 0, err
	}

	_, err = tx.AddRelationship(capsule, nextSlot)
	if err != nil {
		return 0, wrapInterfaceErr(err)
	}

	return capsule, nil
}

// setSlotTarget rewires capsule's role slot -- the one slots is
// parameterized on (prev, value or next) -- to target, as one
// transaction (nested if graph is a Tx). The slot is discovered through
// slotFor, so the ownership check the read accessors apply also guards
// every write; ErrNotCapsule is returned if capsule has no such slot.
// This is the shared body behind the exported SetPrev/SetNext/SetValue,
// so each of them reads the slot and writes it in one transaction.
func (c *CapsuleRegistry) setSlotTarget(graph Transactor, capsule NodeID, slots *PointerRegistry, target NodeID) error {
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		slot, found, err := c.slotFor(tx, capsule, slots.allPointers)
		if err != nil {
			return err
		}
		if !found {
			return ErrNotCapsule
		}

		return slots.SetTarget(tx, slot, target)
	}))
}

// removeSlotTarget clears capsule's role slot -- the one slots is
// parameterized on -- as one transaction (nested if graph is a Tx). The
// slot is discovered through slotFor (ownership-checked); ErrNotCapsule
// is returned if capsule has no such slot. This is the shared body behind
// the exported RemovePrev/RemoveNext, so a capsule's own links can be
// cleared as part of the same transaction that relinks its neighbors.
func (c *CapsuleRegistry) removeSlotTarget(graph Transactor, capsule NodeID, slots *PointerRegistry) (removed bool, err error) {
	return transactBool(graph, func(tx Tx) (bool, error) {
		slot, found, slotErr := c.slotFor(tx, capsule, slots.allPointers)
		if slotErr != nil {
			return false, slotErr
		}
		if !found {
			return false, ErrNotCapsule
		}

		return slots.RemoveTarget(tx, slot)
	})
}

// NewCapsule creates a fresh capsule NodeID, tags it
// (AllElementCapsules, capsule), and wires all three of its role slots
// (prev, value, next), entirely inside one transaction (nested if graph
// is a Tx), via buildCapsuleTx.
//
// value must already exist.
func (c *CapsuleRegistry) NewCapsule(graph Transactor, value NodeID) (NodeID, error) {
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		if existsErr := requireExists(tx, value); existsErr != nil {
			return 0, existsErr
		}

		return buildCapsuleTx(tx, c.allElementCapsules, c.prevSlots.allPointers, c.valueSlots.allPointers, c.nextSlots.allPointers, value)
	})
}

// Value returns capsule's current value, i.e. its value slot's target.
//
// hasValue is false only if capsule's value slot has no target set --
// which should not occur for any capsule created via NewCapsule, since
// NewCapsule always sets the value slot's target immediately. It can
// only arise from an out-of-band mutation (e.g. RemoveTarget called
// directly through the underlying value PointerRegistry).
func (c *CapsuleRegistry) Value(graph GraphReader, capsule NodeID) (value NodeID, hasValue bool, err error) {
	slot, found, err := c.slotFor(graph, capsule, c.valueSlots.allPointers)
	if err != nil {
		return 0, false, err
	}
	if !found {
		return 0, false, ErrNotCapsule
	}

	return c.valueSlots.Target(graph, slot)
}

// SetValue replaces capsule's value.
func (c *CapsuleRegistry) SetValue(graph Transactor, capsule, value NodeID) error {
	return c.setSlotTarget(graph, capsule, c.valueSlots, value)
}

// CapsulesWithValue returns every capsule, anywhere in the graph, whose
// value slot currently targets value -- not scoped to any particular
// list.
//
// This is the reverse-lookup counterpart to Value(capsule): Value walks
// capsule -> valueSlot -> value forward; CapsulesWithValue walks
// value -> valueSlot -> capsule backward, starting from
// Graph.FindIncoming(value) (an indexed map lookup, not a scan of any
// list). This is the realization behind ListRegistry.Contains/
// OccurrencesOf below: theorystate.md section 11's open question
// about adding "a Set-like index atop a List... for doesElementExist(X)"
// turns out not to need any new node, tag, or index structure at all --
// the ElementCapsule/value-slot wiring already built for ordinary list
// traversal already has everything a reverse lookup needs, exactly
// because each list-element occurrence already has its own
// freshly-minted, uniquely-tagged identity (theorystate.md section
// 75). The only thing missing was asking the question from the value's
// side instead of the list's side -- the same realization already
// implicit in how InsertAfter/Remove check list membership via a direct
// (list, capsule) relationship lookup instead of walking the list to
// find afterCapsule/capsule.
//
// Candidates are found via value's incoming relationships, filtered to
// those actually tagged as value-slots (IsPointer under this registry's
// value-slot tag) -- value may well have other, unrelated incoming
// relationships elsewhere in the graph (a Pointer target, a
// PointerMetadata target, etc.), which are silently skipped rather than
// mistaken for capsule occurrences.
//
// Naming note worth being explicit about, raised in review: valueSlots
// is a *PointerRegistry constructed with allValueSlot (i.e.
// AllElementCapsuleValueSlot) as its own tag -- not with the separate,
// generic AllPointers tag; see NewCapsuleRegistry. So
// c.valueSlots.IsPointer(slot) here checks exactly one relationship,
// (AllElementCapsuleValueSlot, slot), never a second, independent
// (AllPointers, slot) fact -- a value slot is never tagged both ways.
// IsPointer is still the method's name, inherited unmodified from
// PointerRegistry per theorystate.md section 76's
// tag-parameterization discipline (one type, no branching on which tag
// it holds), which makes it easy to misread this call as depending on
// two independent tags when only one ever exists.
// TestCapsuleRoleSlotsAreNotTaggedWithGenericAllPointers pins this down
// directly, so that constructing CapsuleRegistry's three slot registries
// against the shared generic AllPointers tag instead of their own
// distinct role tags -- which would silently break slotFor's per-role
// discovery -- would be caught immediately.
//
// Each qualifying slot's owning capsule is then found via
// findUniqueTaggedParent(slot, allElementCapsules) -- deliberately a
// *tagged* parent lookup, not a "the slot has exactly one parent, full
// stop" lookup. A role-slot node is free to acquire any number of
// additional, unrelated parents over time (some future metadata
// structure referencing the slot node itself, for its own reasons --
// nothing in this file prevents that, since a node may have any number
// of parents, theorystate.md section 2.8) without that
// being confused for a second owning capsule. ErrAmbiguousPointerMetadata
// (the same error findUniqueTaggedParent/findUniqueTaggedChild already
// return elsewhere in this file for an analogous ambiguity) is returned
// only if two distinct *capsule-tagged* nodes both claim the same slot --
// a genuine invariant violation, since buildCapsuleTx wires each slot to
// exactly one owning capsule at creation and nothing legitimate ever
// adds a second one. A slot with no capsule-tagged parent at all (found
// == false, e.g. after some out-of-band edit removed the owning edge) is
// silently skipped rather than fabricated. Like slotFor's other callers
// (Value, Prev, Next, ...), this does not separately re-verify that the
// discovered capsule's own value slot (via slotFor) is this exact slot --
// a well-formed graph only ever wires that edge to match, the same level
// of defensiveness already used elsewhere in this file.
//
// Running time is proportional to the number of incoming relationships
// value happens to have across the whole graph -- typically small and
// unrelated to the length of any list value occurs in -- not to the
// length of any particular list.
//
// value need not currently be tagged or used as a capsule value at all.
// If value does not exist, this fails with ErrNodeNotFound, via the
// underlying Graph.FindIncoming(value) call.
//
// The returned capsules are in no particular semantic order (they follow
// Graph.FindIncoming's own deterministic sort by slot NodeID, which does
// not necessarily correspond to capsule creation order).
//
// Reading value's incoming relationships is now paged internally
// (theorystate.md section 105), capsulesWithValuePageSize at a time, so a
// value referenced from a very large number of places -- the "very popular
// value node" case that section names directly -- does not require
// materializing its entire incoming edge list in memory just to filter it
// down to genuine value-slot owners. A backend that pages natively
// (BoltGraph, a transaction over it, and BoltGraph or GraphActor
// forwarding to it) reads one bounded page at a time. Any other reader is
// read in full exactly once and sliced (see pageIterator), so this never
// costs more than the single FindIncoming it replaced.
func (c *CapsuleRegistry) CapsulesWithValue(graph GraphReader, value NodeID) ([]NodeID, error) {
	return slotOwnersOf(graph, value, c.valueSlots.allPointers, c.allElementCapsules)
}

// slotOwnersOf returns the owners of every slot that points at value: the
// reverse lookup shared by CapsuleRegistry.CapsulesWithValue (slots tagged
// as a capsule's value slot, owners tagged as capsules) and
// FieldRegistry.Owners (slots tagged as one field, owners tagged as
// records). It reads value's incoming relationships in pages, never the
// owners, and keeps only those parents that carry slotTag. Each slot's owner
// is found with findUniqueTaggedParent, deliberately a tagged lookup: a slot
// may acquire unrelated parents, which are ignored, and only two distinct
// ownerTag-tagged parents are ambiguous (ErrAmbiguousPointerMetadata). A slot
// with no owner is skipped. The result is in no particular order and nil if
// there is none. value must exist (ErrNodeNotFound).
func slotOwnersOf(graph GraphReader, value, slotTag, ownerTag NodeID) ([]NodeID, error) {
	var owners []NodeID

	walkErr := forEachIncoming(graph, value, func(rel Relationship) error {
		slot := rel.From

		isSlot, slotErr := graph.HasRelationship(slotTag, slot)
		if slotErr != nil {
			return wrapInterfaceErr(slotErr)
		}
		if !isSlot {
			return nil
		}

		owner, found, findErr := findUniqueTaggedParent(graph, slot, ownerTag)
		if findErr != nil {
			return findErr
		}
		if found {
			owners = append(owners, owner)
		}

		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}

	return owners, nil
}

// capsulesWithValuePageSize bounds each internal page CapsulesWithValue
// reads while walking a value's incoming relationships (theorystate.md
// section 105). A var, not a const, specifically so tests can shrink it to
// exercise the multi-page loop without needing thousands of capsules.
var capsulesWithValuePageSize = 1000

// forEachIncoming calls visit for every relationship whose target is to, in
// ascending source order, reading them capsulesWithValuePageSize at a time
// (see incomingPages), and stops at the first error visit returns.
func forEachIncoming(graph GraphReader, to NodeID, visit func(rel Relationship) error) error {
	pages := incomingPages(graph, to, capsulesWithValuePageSize)

	for {
		page, err := pages.next()
		if err != nil {
			return err
		}

		if len(page) == 0 {
			return nil
		}

		for _, rel := range page {
			if visitErr := visit(rel); visitErr != nil {
				return wrapInterfaceErr(visitErr)
			}
		}
	}
}

// Prev returns capsule's previous-capsule link, if any. hasPrev is false
// for a capsule currently at the head of its list.
func (c *CapsuleRegistry) Prev(graph GraphReader, capsule NodeID) (prev NodeID, hasPrev bool, err error) {
	slot, found, err := c.slotFor(graph, capsule, c.prevSlots.allPointers)
	if err != nil {
		return 0, false, err
	}
	if !found {
		return 0, false, ErrNotCapsule
	}

	return c.prevSlots.Target(graph, slot)
}

// SetPrev sets capsule's previous-capsule link.
func (c *CapsuleRegistry) SetPrev(graph Transactor, capsule, prev NodeID) error {
	return c.setSlotTarget(graph, capsule, c.prevSlots, prev)
}

// RemovePrev clears capsule's previous-capsule link, if any.
func (c *CapsuleRegistry) RemovePrev(graph Transactor, capsule NodeID) (removed bool, err error) {
	return c.removeSlotTarget(graph, capsule, c.prevSlots)
}

// Next returns capsule's next-capsule link, if any. hasNext is false for
// a capsule currently at the tail of its list.
func (c *CapsuleRegistry) Next(graph GraphReader, capsule NodeID) (next NodeID, hasNext bool, err error) {
	slot, found, err := c.slotFor(graph, capsule, c.nextSlots.allPointers)
	if err != nil {
		return 0, false, err
	}
	if !found {
		return 0, false, ErrNotCapsule
	}

	return c.nextSlots.Target(graph, slot)
}

// SetNext sets capsule's next-capsule link.
func (c *CapsuleRegistry) SetNext(graph Transactor, capsule, next NodeID) error {
	return c.setSlotTarget(graph, capsule, c.nextSlots, next)
}

// RemoveNext clears capsule's next-capsule link, if any.
func (c *CapsuleRegistry) RemoveNext(graph Transactor, capsule NodeID) (removed bool, err error) {
	return c.removeSlotTarget(graph, capsule, c.nextSlots)
}

// DeleteCapsule deletes capsule and all three of its role-slot nodes
// (prev, value, next) from the underlying graph, entirely inside one
// Graph.Transact call, but only if doing so cannot leave anything
// orphaned or partially torn down.
//
// A capsule minted via NewCapsule/newCapsuleTx has a fixed, known shape:
// its own AllElementCapsules tag, exactly three outgoing edges to its
// role slots, each slot's own role tag, and (usually) a target already
// set on the value slot. DeleteCapsule removes every relationship
// CapsuleRegistry itself is aware of through tx first (capsule's own
// three slot-edges and its AllElementCapsules tag, the value slot's
// target edge if set, and each slot's own role-tag edge), then deletes
// each of the four nodes via tx.DeleteNode. If capsule or any of its
// three slots still carries something beyond that fixed shape --
// ListRegistry still linking capsule into a list, a caller having
// called SetPrev/SetNext without a matching removal, or some future
// structure referencing a slot node for its own reasons -- the
// corresponding tx.DeleteNode call fails with the underlying Graph's own
// ErrNodeNotEmpty, which this method maps to the more specific
// ErrCapsuleNotEmpty.
//
// This is deliberately all-or-nothing, and getting that right requires
// no special care: tx.DeleteNode is itself fully undoable (see the Txn
// doc comment), so if a later delete in the sequence below fails,
// Graph.Transact's ordinary LIFO rollback automatically undoes every
// earlier step in this same call -- including any DeleteNode calls that
// had already succeeded earlier in the sequence -- exactly like it
// already does for every other multi-step operation in this file. No
// separate pre-verification pass is needed before deleting anything;
// see theorystate.md section 78 for why an earlier version of this
// method needed one; and why it no longer does.
//
// Every relationship this removes is one CapsuleRegistry itself is
// certain it created (via buildCapsuleTx). value itself is never
// deleted -- only the valueSlot -> value edge is removed -- since value
// is caller-owned data that may still be referenced elsewhere (e.g. by
// another capsule's own value slot). prevSlot and nextSlot's own target
// edges, if set, are deliberately never force-cleared here: doing so
// would risk leaving a *neighboring* capsule (whatever prevSlot/nextSlot
// currently points at) with a dangling reference into a node this call
// is about to delete. Refusing to delete when a prev/next target is
// still set (surfaced as ErrCapsuleNotEmpty via the corresponding slot's
// failed tx.DeleteNode) is the correct behavior, not a missing feature;
// see TestCapsuleRegistryDeleteCapsuleFailsIfPrevOrNextSet.
//
// capsule must currently be tagged (AllElementCapsules, capsule); a
// capsule somehow missing one of its three role slots -- only reachable
// through an out-of-band Graph mutation -- is treated the same as
// ErrCapsuleNotEmpty rather than guessed about.
//
// Every existence, tag and slot check runs against tx. If graph is a Tx
// the call is a nested transaction: if it fails, only what it did is
// undone (including any DeleteNode calls that had already succeeded),
// and the enclosing closure may carry on. That is what lets
// ListRegistry.Remove treat this as a best-effort step inside its own
// single transaction, and lets CompositeSetLogRegistry.RemoveOperation
// treat it as one all-or-nothing step of a larger one.
func (c *CapsuleRegistry) DeleteCapsule(graph Transactor, capsule NodeID) error {
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		if requireErr := c.requireCapsule(tx, capsule); requireErr != nil {
			return requireErr
		}

		prevSlot, hasPrevSlot, err := c.slotFor(tx, capsule, c.prevSlots.allPointers)
		if err != nil {
			return err
		}

		valueSlot, hasValueSlot, err := c.slotFor(tx, capsule, c.valueSlots.allPointers)
		if err != nil {
			return err
		}

		nextSlot, hasNextSlot, err := c.slotFor(tx, capsule, c.nextSlots.allPointers)
		if err != nil {
			return err
		}

		if !hasPrevSlot || !hasValueSlot || !hasNextSlot {
			return ErrCapsuleNotEmpty
		}

		value, hasValue, err := c.valueSlots.Target(tx, valueSlot)
		if err != nil {
			return err
		}

		// Every relationship here is one buildCapsuleTx itself created.
		// prevSlot/nextSlot's own targets are deliberately absent: see
		// the DeleteCapsule doc comment.
		edges := []Relationship{
			{From: capsule, To: prevSlot},
			{From: c.prevSlots.allPointers, To: prevSlot},
			{From: capsule, To: valueSlot},
			{From: c.valueSlots.allPointers, To: valueSlot},
			{From: capsule, To: nextSlot},
			{From: c.nextSlots.allPointers, To: nextSlot},
			{From: c.allElementCapsules, To: capsule},
		}
		if hasValue {
			edges = append(edges, Relationship{From: valueSlot, To: value})
		}

		for _, edge := range edges {
			if removeErr := removeRelationshipTx(tx, edge.From, edge.To); removeErr != nil {
				return removeErr
			}
		}

		for _, node := range []NodeID{prevSlot, valueSlot, nextSlot, capsule} {
			if deleteErr := deleteNodeTx(tx, node); deleteErr != nil {
				if errors.Is(deleteErr, ErrNodeNotEmpty) {
					return ErrCapsuleNotEmpty
				}
				return deleteErr
			}
		}

		return nil
	}))
}
