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
	"slices"
)

// SlottedListRegistry is the second ordered list type (OrderedList): the same
// behaviour as ListRegistry, with a structure in which no lookup ever reads a
// node whose number of children grows with the list.
//
//	(AllSlottedLists, list)
//	list -> M    (AllSlottedListMembers, M)   M  -> every capsule of the list
//	list -> HS   (AllSlottedListHeadSlot, HS) HS -> the head capsule
//	list -> TS   (AllSlottedListTailSlot, TS) TS -> the tail capsule
//
// ListRegistry keeps its capsules as direct children of the list and tags the
// head and tail capsules, so finding the tail means finding the one child of a
// list with n children that carries a tag. Here the list has three children,
// head and tail are the (at most one) targets of two PointerRegistry slots,
// membership is one relationship from M, and the list of a capsule is found
// from the capsule's few parents. Head, Tail, Append, Prepend, InsertAfter,
// Remove and Contains therefore cost the same however long the list is;
// Elements and the full validation are linear, as they must be.
//
// Its capsules are a CapsuleRegistry of their own, with their own tags, so
// ListRegistry's Checker never fires for them and this one never fires for
// ListRegistry's; a capsule linked into a list of the other type is plainly
// invalid here. It keeps the invariants of ListRegistry, including that a
// member capsule is a child of exactly one list (section 11b).
//
// The commit-time Checker is local, like ListRegistry's: it validates what
// the transaction touched (a member capsule against its neighbours, and the
// head and tail of a touched list), and leaves whole-list properties (a stray
// child of M, a chain unreachable from the head, a cycle) to Elements and
// VerifyStructures.
type SlottedListRegistry struct {
	capsules   *CapsuleRegistry
	headSlots  *PointerRegistry
	tailSlots  *PointerRegistry
	allLists   NodeID
	allMembers NodeID
}

// Compile-time assertion that *SlottedListRegistry satisfies OrderedList.
var _ OrderedList = (*SlottedListRegistry)(nil)

// NewSlottedListRegistry creates a SlottedListRegistry over graph. capsules
// makes the lists' capsules and must be a CapsuleRegistry of its own (not the
// one a ListRegistry uses); allLists tags lists, allMembers their members
// nodes, and allHeadSlot/allTailSlot their head and tail slots. All must
// already exist. This registers the Checkers of the two slot PointerRegistry
// instances and the list Checker.
func NewSlottedListRegistry(graph GraphAPI, capsules *CapsuleRegistry, allLists, allMembers, allHeadSlot, allTailSlot NodeID) (*SlottedListRegistry, error) {
	if existsErr := requireAllExist(graph, allLists, allMembers); existsErr != nil {
		return nil, existsErr
	}

	headSlots, err := NewPointerRegistry(graph, allHeadSlot)
	if err != nil {
		return nil, err
	}

	tailSlots, err := NewPointerRegistry(graph, allTailSlot)
	if err != nil {
		return nil, err
	}

	s := &SlottedListRegistry{
		capsules:   capsules,
		headSlots:  headSlots,
		tailSlots:  tailSlots,
		allLists:   allLists,
		allMembers: allMembers,
	}

	// Tags: a list, a members node, a slot or a capsule being touched is
	// what can change a list's structure.
	graph.RegisterChecker(Checker{
		Name:  fmt.Sprintf("SlottedListRegistry(tag=%d)", allLists),
		Tags:  []NodeID{allLists, allMembers, allHeadSlot, allTailSlot, capsules.allElementCapsules},
		Check: s.checkTouched,
	})

	return s, nil
}

// Capsules implements OrderedList.
func (s *SlottedListRegistry) Capsules() *CapsuleRegistry {
	return s.capsules
}

// IsList reports whether id is currently tagged (AllSlottedLists, id).
func (s *SlottedListRegistry) IsList(graph GraphReader, id NodeID) (bool, error) {
	has, err := graph.HasRelationship(s.allLists, id)

	return has, wrapInterfaceErr(err)
}

// requireList checks that list exists and is a slotted list.
func (s *SlottedListRegistry) requireList(graph GraphReader, list NodeID) error {
	return requireTagged(graph, list, s.allLists, ErrNotList)
}

// requireListValue is requireList plus a check that value exists.
func (s *SlottedListRegistry) requireListValue(graph GraphReader, list, value NodeID) error {
	if requireErr := s.requireList(graph, list); requireErr != nil {
		return requireErr
	}

	return requireExists(graph, value)
}

// listSlots are the three nodes a list owns besides its tag.
type listSlots struct {
	members, head, tail NodeID
}

// slotsOf finds list's members node and head and tail slots, each by its own
// tag and each verified to be owned by list. A list missing one is
// ErrInvalidListStructure; two are ErrAmbiguousPointerMetadata.
func (s *SlottedListRegistry) slotsOf(graph GraphReader, list NodeID) (listSlots, error) {
	var slots listSlots

	wanted := [...]struct {
		tag  NodeID
		dest *NodeID
	}{
		{tag: s.allMembers, dest: &slots.members},
		{tag: s.headSlots.allPointers, dest: &slots.head},
		{tag: s.tailSlots.allPointers, dest: &slots.tail},
	}

	for _, want := range wanted {
		slot, found, err := findOwnedSlot(graph, list, s.allLists, want.tag)
		if err != nil {
			return listSlots{}, err
		}
		if !found {
			return listSlots{}, ErrInvalidListStructure
		}

		*want.dest = slot
	}

	return slots, nil
}

// openList is requireList plus slotsOf: what every operation starts with.
func (s *SlottedListRegistry) openList(graph GraphReader, list NodeID) (listSlots, error) {
	if requireErr := s.requireList(graph, list); requireErr != nil {
		return listSlots{}, requireErr
	}

	return s.slotsOf(graph, list)
}

// NewList creates a list: the list node, its members node and its two slots,
// all in one transaction. The new list is empty.
func (s *SlottedListRegistry) NewList(graph Transactor) (NodeID, error) {
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		list, err := createTaggedNodeTx(tx, s.allLists)
		if err != nil {
			return 0, err
		}

		members, err := createTaggedNodeTx(tx, s.allMembers)
		if err != nil {
			return 0, err
		}

		if linkErr := addRelationshipTx(tx, list, members); linkErr != nil {
			return 0, linkErr
		}

		for _, slots := range []*PointerRegistry{s.headSlots, s.tailSlots} {
			slot, slotErr := slots.NewPointer(tx)
			if slotErr != nil {
				return 0, slotErr
			}

			if linkErr := addRelationshipTx(tx, list, slot); linkErr != nil {
				return 0, linkErr
			}
		}

		return list, nil
	})
}

// boundary returns the capsule a slot of list points at, if any, checking that
// it is a member capsule of list.
func (s *SlottedListRegistry) boundary(graph GraphReader, list NodeID, pointers *PointerRegistry, slotOf func(listSlots) NodeID) (NodeID, bool, error) {
	slots, err := s.openList(graph, list)
	if err != nil {
		return 0, false, err
	}

	capsule, found, targetErr := pointers.Target(graph, slotOf(slots))
	if targetErr != nil || !found {
		return 0, false, targetErr
	}

	isCapsule, capsuleErr := s.capsules.IsCapsule(graph, capsule)
	if capsuleErr != nil {
		return 0, false, capsuleErr
	}

	isMember, memberErr := graph.HasRelationship(slots.members, capsule)
	if memberErr != nil {
		return 0, false, wrapInterfaceErr(memberErr)
	}

	if !isCapsule || !isMember {
		return 0, false, ErrInvalidListStructure
	}

	return capsule, true, nil
}

// Head returns list's head capsule, if any.
func (s *SlottedListRegistry) Head(graph GraphReader, list NodeID) (head NodeID, hasHead bool, err error) {
	return s.boundary(graph, list, s.headSlots, func(slots listSlots) NodeID { return slots.head })
}

// Tail returns list's tail capsule, if any.
func (s *SlottedListRegistry) Tail(graph GraphReader, list NodeID) (tail NodeID, hasTail bool, err error) {
	return s.boundary(graph, list, s.tailSlots, func(slots listSlots) NodeID { return slots.tail })
}

// Append appends one value, as AppendMany does.
func (s *SlottedListRegistry) Append(graph Transactor, list, value NodeID) (NodeID, error) {
	capsules, err := s.AppendMany(graph, list, []NodeID{value})
	if err != nil {
		return 0, err
	}

	return capsules[0], nil
}

// AppendMany appends one capsule per value, in order, as the new tail of list,
// in one transaction, and returns the capsules. It is all-or-nothing.
func (s *SlottedListRegistry) AppendMany(graph Transactor, list NodeID, values []NodeID) ([]NodeID, error) {
	return transactValue(graph, func(tx Tx) ([]NodeID, error) {
		slots, err := s.openList(tx, list)
		if err != nil {
			return nil, err
		}

		tail, hasTail, tailErr := s.tailSlots.Target(tx, slots.tail)
		if tailErr != nil {
			return nil, tailErr
		}

		// Built fresh on every run of the closure (it may be re-run).
		capsules := make([]NodeID, 0, len(values))

		for _, value := range values {
			capsule, newErr := s.newMember(tx, slots, value)
			if newErr != nil {
				return nil, newErr
			}

			if hasTail {
				if prevErr := s.capsules.SetPrev(tx, capsule, tail); prevErr != nil {
					return nil, prevErr
				}
				if nextErr := s.capsules.SetNext(tx, tail, capsule); nextErr != nil {
					return nil, nextErr
				}
			} else if headErr := s.headSlots.SetTarget(tx, slots.head, capsule); headErr != nil {
				return nil, headErr
			}

			if tailSetErr := s.tailSlots.SetTarget(tx, slots.tail, capsule); tailSetErr != nil {
				return nil, tailSetErr
			}

			tail, hasTail = capsule, true
			capsules = append(capsules, capsule)
		}

		return capsules, nil
	})
}

// newMember makes a capsule holding value and makes it a member of the list
// whose slots are given, without linking it to any neighbour.
func (s *SlottedListRegistry) newMember(tx Tx, slots listSlots, value NodeID) (NodeID, error) {
	if existsErr := requireExists(tx, value); existsErr != nil {
		return 0, existsErr
	}

	capsule, err := s.capsules.NewCapsule(tx, value)
	if err != nil {
		return 0, err
	}

	if linkErr := addRelationshipTx(tx, slots.members, capsule); linkErr != nil {
		return 0, linkErr
	}

	return capsule, nil
}

// Prepend makes a capsule holding value the new head of list.
func (s *SlottedListRegistry) Prepend(graph Transactor, list, value NodeID) (NodeID, error) {
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		if requireErr := s.requireListValue(tx, list, value); requireErr != nil {
			return 0, requireErr
		}

		slots, err := s.slotsOf(tx, list)
		if err != nil {
			return 0, err
		}

		oldHead, hasHead, headErr := s.headSlots.Target(tx, slots.head)
		if headErr != nil {
			return 0, headErr
		}

		capsule, newErr := s.newMember(tx, slots, value)
		if newErr != nil {
			return 0, newErr
		}

		if hasHead {
			if nextErr := s.capsules.SetNext(tx, capsule, oldHead); nextErr != nil {
				return 0, nextErr
			}
			if prevErr := s.capsules.SetPrev(tx, oldHead, capsule); prevErr != nil {
				return 0, prevErr
			}
		} else if tailErr := s.tailSlots.SetTarget(tx, slots.tail, capsule); tailErr != nil {
			return 0, tailErr
		}

		if headSetErr := s.headSlots.SetTarget(tx, slots.head, capsule); headSetErr != nil {
			return 0, headSetErr
		}

		return capsule, nil
	})
}

// InsertAfter makes a capsule holding value an element of list, right after
// afterCapsule, which must be an element of list (ErrCapsuleNotInList).
func (s *SlottedListRegistry) InsertAfter(graph Transactor, list, afterCapsule, value NodeID) (NodeID, error) {
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		if requireErr := s.requireListValue(tx, list, value); requireErr != nil {
			return 0, requireErr
		}

		slots, err := s.slotsOf(tx, list)
		if err != nil {
			return 0, err
		}

		inList, inListErr := tx.HasRelationship(slots.members, afterCapsule)
		if inListErr != nil {
			return 0, wrapInterfaceErr(inListErr)
		}
		if !inList {
			return 0, ErrCapsuleNotInList
		}

		oldNext, hasNext, nextErr := s.capsules.Next(tx, afterCapsule)
		if nextErr != nil {
			return 0, nextErr
		}

		capsule, newErr := s.newMember(tx, slots, value)
		if newErr != nil {
			return 0, newErr
		}

		if prevErr := s.capsules.SetPrev(tx, capsule, afterCapsule); prevErr != nil {
			return 0, prevErr
		}
		if afterErr := s.capsules.SetNext(tx, afterCapsule, capsule); afterErr != nil {
			return 0, afterErr
		}

		if !hasNext {
			if tailErr := s.tailSlots.SetTarget(tx, slots.tail, capsule); tailErr != nil {
				return 0, tailErr
			}

			return capsule, nil
		}

		if oldNextErr := s.capsules.SetNext(tx, capsule, oldNext); oldNextErr != nil {
			return 0, oldNextErr
		}

		if oldPrevErr := s.capsules.SetPrev(tx, oldNext, capsule); oldPrevErr != nil {
			return 0, oldPrevErr
		}

		return capsule, nil
	})
}

// unlinkNeighbours relinks the neighbours of a capsule that is leaving its
// list around the gap, and moves the head or tail slot if it was an end.
func (s *SlottedListRegistry) unlinkNeighbours(tx Tx, slots listSlots, prev NodeID, hasPrev bool, next NodeID, hasNext bool) error {
	switch {
	case hasPrev && hasNext:
		if nextErr := s.capsules.SetNext(tx, prev, next); nextErr != nil {
			return nextErr
		}

		return s.capsules.SetPrev(tx, next, prev)
	case hasPrev:
		if _, removeErr := s.capsules.RemoveNext(tx, prev); removeErr != nil {
			return removeErr
		}

		return s.tailSlots.SetTarget(tx, slots.tail, prev)
	case hasNext:
		if _, removeErr := s.capsules.RemovePrev(tx, next); removeErr != nil {
			return removeErr
		}

		return s.headSlots.SetTarget(tx, slots.head, next)
	default:
		if _, headErr := s.headSlots.RemoveTarget(tx, slots.head); headErr != nil {
			return headErr
		}

		_, tailErr := s.tailSlots.RemoveTarget(tx, slots.tail)

		return tailErr
	}
}

// RemoveWithoutDeletingCapsule unlinks capsule from list, relinking its
// neighbours and moving head or tail as needed, and clears the capsule's own
// links. The capsule survives, in no list.
func (s *SlottedListRegistry) RemoveWithoutDeletingCapsule(graph Transactor, list, capsule NodeID) error {
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		slots, err := s.openList(tx, list)
		if err != nil {
			return err
		}

		inList, inListErr := tx.HasRelationship(slots.members, capsule)
		if inListErr != nil {
			return wrapInterfaceErr(inListErr)
		}
		if !inList {
			return ErrCapsuleNotInList
		}

		prev, hasPrev, prevErr := s.capsules.Prev(tx, capsule)
		if prevErr != nil {
			return prevErr
		}

		next, hasNext, nextErr := s.capsules.Next(tx, capsule)
		if nextErr != nil {
			return nextErr
		}

		if unlinkErr := s.unlinkNeighbours(tx, slots, prev, hasPrev, next, hasNext); unlinkErr != nil {
			return unlinkErr
		}

		if _, clearErr := s.capsules.RemovePrev(tx, capsule); clearErr != nil {
			return clearErr
		}
		if _, clearErr := s.capsules.RemoveNext(tx, capsule); clearErr != nil {
			return clearErr
		}

		return removeRelationshipTx(tx, slots.members, capsule)
	}))
}

// Remove unlinks capsule from list and reclaims it when nothing else refers to
// it (deleted reports whether it did), all in one transaction: the removal is
// this call's own work, and the deletion a nested savepoint.
func (s *SlottedListRegistry) Remove(graph Transactor, list, capsule NodeID) (deleted bool, err error) {
	return transactBool(graph, func(tx Tx) (bool, error) {
		if removeErr := s.RemoveWithoutDeletingCapsule(tx, list, capsule); removeErr != nil {
			return false, removeErr
		}

		deleteErr := s.capsules.DeleteCapsule(tx, capsule)
		if deleteErr != nil {
			if errors.Is(deleteErr, ErrCapsuleNotEmpty) {
				return false, nil
			}

			return false, deleteErr
		}

		return true, nil
	})
}

// DeleteList deletes an empty list together with its members node and slots,
// in one transaction (delete only if empty: ErrNodeNotEmpty rolls everything
// back).
func (s *SlottedListRegistry) DeleteList(graph Transactor, list NodeID) error {
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		slots, err := s.openList(tx, list)
		if err != nil {
			return err
		}

		owned := [...]struct{ node, tag NodeID }{
			{node: slots.members, tag: s.allMembers},
			{node: slots.head, tag: s.headSlots.allPointers},
			{node: slots.tail, tag: s.tailSlots.allPointers},
		}

		for _, part := range owned {
			if removeErr := removeRelationshipTx(tx, list, part.node); removeErr != nil {
				return removeErr
			}
			if deleteErr := untagAndDeleteNodeTx(tx, part.node, part.tag); deleteErr != nil {
				return deleteErr
			}
		}

		return untagAndDeleteNodeTx(tx, list, s.allLists)
	}))
}

// Elements returns list's values, head to tail, after validating the whole
// list (validateStructure).
func (s *SlottedListRegistry) Elements(graph GraphReader, list NodeID) ([]NodeID, error) {
	slots, err := s.openList(graph, list)
	if err != nil {
		return nil, err
	}

	if validateErr := s.validateStructure(graph, list); validateErr != nil {
		return nil, validateErr
	}

	current, hasCurrent, err := s.headSlots.Target(graph, slots.head)
	if err != nil {
		return nil, err
	}

	var values []NodeID

	visited := make(map[NodeID]struct{})

	for hasCurrent {
		if _, seen := visited[current]; seen {
			return nil, ErrListCycle
		}

		visited[current] = struct{}{}

		value, hasValue, valueErr := s.capsules.Value(graph, current)
		if valueErr != nil {
			return nil, valueErr
		}
		if hasValue {
			values = append(values, value)
		}

		current, hasCurrent, err = s.capsules.Next(graph, current)
		if err != nil {
			return nil, err
		}
	}

	return values, nil
}

// OccurrencesOf returns every capsule of list holding value, found from the
// value's side (CapsulesWithValue) and filtered by one membership check each.
func (s *SlottedListRegistry) OccurrencesOf(graph GraphReader, list, value NodeID) ([]NodeID, error) {
	slots, err := s.openList(graph, list)
	if err != nil {
		return nil, err
	}

	candidates, candidatesErr := s.capsules.CapsulesWithValue(graph, value)
	if candidatesErr != nil {
		return nil, candidatesErr
	}

	var occurrences []NodeID

	for _, capsule := range candidates {
		inList, inListErr := graph.HasRelationship(slots.members, capsule)
		if inListErr != nil {
			return nil, wrapInterfaceErr(inListErr)
		}
		if inList {
			occurrences = append(occurrences, capsule)
		}
	}

	return occurrences, nil
}

// Contains reports whether value occurs in list, returning one such capsule.
func (s *SlottedListRegistry) Contains(graph GraphReader, list, value NodeID) (capsule NodeID, found bool, err error) {
	occurrences, err := s.OccurrencesOf(graph, list, value)
	if err != nil || len(occurrences) == 0 {
		return 0, false, err
	}

	return occurrences[0], true, nil
}

// ListsOf returns the lists capsule is a member of, ascending: the lists
// owning the members nodes among the capsule's parents. Exactly one for a
// member capsule.
func (s *SlottedListRegistry) ListsOf(graph GraphReader, capsule NodeID) ([]NodeID, error) {
	incoming, err := graph.FindIncoming(capsule)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}

	lists := make([]NodeID, 0, 1)

	for _, rel := range incoming {
		isMembers, membersErr := graph.HasRelationship(s.allMembers, rel.From)
		if membersErr != nil {
			return nil, wrapInterfaceErr(membersErr)
		}
		if !isMembers {
			continue
		}

		list, found, ownerErr := findUniqueTaggedParent(graph, rel.From, s.allLists)
		if ownerErr != nil {
			return nil, ownerErr
		}
		if found {
			lists = append(lists, list)
		}
	}

	slices.Sort(lists)

	return lists, nil
}

// stepBack implements OrderedList.
func (s *SlottedListRegistry) stepBack(graph GraphReader, list, capsule NodeID) (NodeID, bool, error) {
	members, err := s.membersOf(graph, list)
	if err != nil {
		return 0, false, err
	}

	return stepBackWithin(graph, s.capsules, members, capsule)
}

// membersOf returns list's members node: one lookup, where slotsOf makes
// three, for the operations that need nothing else.
func (s *SlottedListRegistry) membersOf(graph GraphReader, list NodeID) (NodeID, error) {
	if requireErr := s.requireList(graph, list); requireErr != nil {
		return 0, requireErr
	}

	members, found, err := findOwnedSlot(graph, list, s.allLists, s.allMembers)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, ErrInvalidListStructure
	}

	return members, nil
}

// IsMember implements OrderedList: capsule is an element of list when it is a
// child of list's members node.
func (s *SlottedListRegistry) IsMember(graph GraphReader, list, capsule NodeID) (bool, error) {
	members, err := s.membersOf(graph, list)
	if err != nil {
		return false, err
	}

	has, hasErr := graph.HasRelationship(members, capsule)

	return has, wrapInterfaceErr(hasErr)
}

// VerifyStructures validates every list in full, fail-closed.
func (s *SlottedListRegistry) VerifyStructures(graph GraphAPI) error {
	return verifyLists(graph, s.allLists, s.validateStructure)
}

// validateStructure validates one list in full: its slots, every child of the
// members node being a capsule of this list alone, and the chain from head to
// tail covering exactly the members.
func (s *SlottedListRegistry) validateStructure(graph GraphReader, list NodeID) error {
	slots, err := s.slotsOf(graph, list)
	if err != nil {
		return err
	}

	head, hasHead, headErr := s.headSlots.Target(graph, slots.head)
	if headErr != nil {
		return headErr
	}

	tail, hasTail, tailErr := s.tailSlots.Target(graph, slots.tail)
	if tailErr != nil {
		return tailErr
	}

	outgoing, outgoingErr := graph.FindOutgoing(slots.members)
	if outgoingErr != nil {
		return wrapInterfaceErr(outgoingErr)
	}

	members := make(map[NodeID]struct{}, len(outgoing))

	for _, rel := range outgoing {
		isCapsule, capsuleErr := s.capsules.IsCapsule(graph, rel.To)
		if capsuleErr != nil {
			return capsuleErr
		}

		owners, ownersErr := s.ListsOf(graph, rel.To)
		if ownersErr != nil {
			return ownersErr
		}

		if !isCapsule || len(owners) != 1 || owners[0] != list {
			return ErrInvalidListStructure
		}

		members[rel.To] = struct{}{}
	}

	if !hasHead || !hasTail {
		if hasHead || hasTail || len(members) != 0 {
			return ErrInvalidListStructure
		}

		return nil
	}

	return validateChain(graph, s.capsules, slots.members, head, tail, members)
}

// checkTouched is the commit-time Checker: every touched member capsule is
// checked against its neighbours, and every list touched (itself, or through
// its members node, a slot or a capsule) has its head and tail checked. All of
// it costs time that does not depend on the length of the lists.
func (s *SlottedListRegistry) checkTouched(g GraphReader, touched map[NodeID]struct{}) error {
	lists := make(map[NodeID]struct{})

	for node := range touched {
		isCapsule, capsuleErr := s.capsules.IsCapsule(g, node)
		if capsuleErr != nil {
			return capsuleErr
		}

		if isCapsule {
			if checkErr := s.checkCapsule(g, node, lists); checkErr != nil {
				return checkErr
			}

			continue
		}

		list, owned, ownerErr := s.owningList(g, node)
		if ownerErr != nil {
			return ownerErr
		}
		if owned {
			lists[list] = struct{}{}
		}
	}

	for list := range lists {
		if checkErr := s.checkList(g, list); checkErr != nil {
			return checkErr
		}
	}

	return nil
}

// owningList returns the list node belongs to if it is a list, a members node
// or a slot of one. A members node or slot with no list is invalid.
func (s *SlottedListRegistry) owningList(g GraphReader, node NodeID) (NodeID, bool, error) {
	isList, listErr := s.IsList(g, node)
	if listErr != nil {
		return 0, false, listErr
	}
	if isList {
		return node, true, nil
	}

	for _, tag := range []NodeID{s.allMembers, s.headSlots.allPointers, s.tailSlots.allPointers} {
		tagged, tagErr := g.HasRelationship(tag, node)
		if tagErr != nil {
			return 0, false, wrapInterfaceErr(tagErr)
		}
		if !tagged {
			continue
		}

		list, found, ownerErr := findUniqueTaggedParent(g, node, s.allLists)
		if ownerErr != nil {
			return 0, false, ownerErr
		}
		if !found {
			return 0, false, ErrInvalidListStructure
		}

		return list, true, nil
	}

	return 0, false, nil
}

// checkCapsule validates one touched capsule and records its list in lists. A
// capsule that is a member of no list is only checked to be loose (checkLoose).
func (s *SlottedListRegistry) checkCapsule(g GraphReader, capsule NodeID, lists map[NodeID]struct{}) error {
	incoming, err := g.FindIncoming(capsule)
	if err != nil {
		return wrapInterfaceErr(err)
	}

	memberNodes := make([]NodeID, 0, 1)

	for _, rel := range incoming {
		isMembers, membersErr := g.HasRelationship(s.allMembers, rel.From)
		if membersErr != nil {
			return wrapInterfaceErr(membersErr)
		}
		if isMembers {
			memberNodes = append(memberNodes, rel.From)
		}
	}

	if len(memberNodes) == 0 {
		return s.checkLoose(g, incoming)
	}

	// A member capsule belongs to exactly one list.
	if len(memberNodes) > 1 {
		return ErrInvalidListStructure
	}

	list, found, ownerErr := findUniqueTaggedParent(g, memberNodes[0], s.allLists)
	if ownerErr != nil {
		return ownerErr
	}
	if !found {
		return ErrInvalidListStructure
	}

	slots, slotsErr := s.slotsOf(g, list)
	if slotsErr != nil {
		return slotsErr
	}
	if slots.members != memberNodes[0] {
		return ErrInvalidListStructure
	}

	lists[list] = struct{}{}

	isHead, headErr := g.HasRelationship(slots.head, capsule)
	if headErr != nil {
		return wrapInterfaceErr(headErr)
	}

	isTail, tailErr := g.HasRelationship(slots.tail, capsule)
	if tailErr != nil {
		return wrapInterfaceErr(tailErr)
	}

	return checkCapsuleInChain(g, s.capsules, slots.members, capsule, isHead, isTail, incoming)
}

// checkLoose rejects a capsule in no list that something still points at: a
// head or tail slot, or the next or prev link of another capsule. Whatever
// removes a capsule from a list moves all of those away first.
func (s *SlottedListRegistry) checkLoose(g GraphReader, incoming []Relationship) error {
	pointers := [...]*PointerRegistry{s.headSlots, s.tailSlots, s.capsules.nextSlots, s.capsules.prevSlots}

	for _, rel := range incoming {
		for _, slots := range pointers {
			isSlot, slotErr := slots.IsPointer(g, rel.From)
			if slotErr != nil {
				return slotErr
			}
			if isSlot {
				return ErrInvalidListStructure
			}
		}
	}

	return nil
}

// checkList validates a touched list's ends: both slots empty, or both
// pointing at member capsules, the head with no previous capsule and the tail
// with no next one.
func (s *SlottedListRegistry) checkList(g GraphReader, list NodeID) error {
	slots, err := s.slotsOf(g, list)
	if err != nil {
		return err
	}

	head, hasHead, headErr := s.headSlots.Target(g, slots.head)
	if headErr != nil {
		return headErr
	}

	tail, hasTail, tailErr := s.tailSlots.Target(g, slots.tail)
	if tailErr != nil {
		return tailErr
	}

	if hasHead != hasTail {
		return ErrInvalidListStructure
	}
	if !hasHead {
		return nil
	}

	if endErr := s.checkEnd(g, slots.members, head, s.capsules.Prev); endErr != nil {
		return endErr
	}

	return s.checkEnd(g, slots.members, tail, s.capsules.Next)
}

// checkEnd checks that end is a member capsule of members with no link in the
// direction link reads (the head has no previous capsule, the tail no next).
func (s *SlottedListRegistry) checkEnd(g GraphReader, members, end NodeID, link func(GraphReader, NodeID) (NodeID, bool, error)) error {
	isCapsule, capsuleErr := s.capsules.IsCapsule(g, end)
	if capsuleErr != nil {
		return capsuleErr
	}

	isMember, memberErr := g.HasRelationship(members, end)
	if memberErr != nil {
		return wrapInterfaceErr(memberErr)
	}

	if !isCapsule || !isMember {
		return ErrInvalidListStructure
	}

	_, hasLink, linkErr := link(g, end)
	if linkErr != nil {
		return linkErr
	}
	if hasLink {
		return ErrInvalidListStructure
	}

	return nil
}

// ---------------------------------------------------------------------
// Shared with ListRegistry.

// findOwnedSlot returns the child of owner tagged slotTag, verifying that
// owner is its unique ownerTag-tagged parent (other parents are allowed). A
// slot owned by someone else, or by two owners, is ErrAmbiguousPointerMetadata:
// otherwise a raw edge could make two nodes silently share a role. It is how a
// capsule finds its role slots and a slotted list its members node and slots.
func findOwnedSlot(g GraphReader, owner, ownerTag, slotTag NodeID) (slot NodeID, found bool, err error) {
	slot, found, err = findUniqueTaggedChild(g, owner, slotTag)
	if err != nil || !found {
		return slot, found, err
	}

	parent, foundParent, parentErr := findUniqueTaggedParent(g, slot, ownerTag)
	if parentErr != nil {
		return 0, false, parentErr
	}
	if !foundParent || parent != owner {
		return 0, false, ErrAmbiguousPointerMetadata
	}

	return slot, true, nil
}

// checkCapsuleInChain validates one member capsule of a list whose members hang
// off container (the list for ListRegistry, the members node for
// SlottedListRegistry), given whether it is the list's head or tail: it has a
// value, it is the head exactly when it has no previous capsule and the tail
// exactly when it has no next one, its neighbours are member capsules whose back
// links point at it, and no node points at it through a slot its own links do
// not agree with. incoming are the capsule's incoming relationships.
func checkCapsuleInChain(g GraphReader, capsules *CapsuleRegistry, container, capsule NodeID, isHead, isTail bool, incoming []Relationship) error {
	var (
		links   capsuleLinks
		linkErr error
	)

	links.prev, links.hasPrev, linkErr = capsules.Prev(g, capsule)
	if linkErr != nil {
		return linkErr
	}

	links.next, links.hasNext, linkErr = capsules.Next(g, capsule)
	if linkErr != nil {
		return linkErr
	}

	_, hasValue, valueErr := capsules.Value(g, capsule)
	if valueErr != nil {
		return valueErr
	}
	if !hasValue {
		return ErrInvalidListStructure
	}

	if isHead == links.hasPrev || isTail == links.hasNext {
		return ErrInvalidListStructure
	}

	if links.hasPrev {
		if prevErr := requireNeighbour(g, capsules, container, links.prev, capsule, capsules.Next); prevErr != nil {
			return prevErr
		}
	}

	if links.hasNext {
		if nextErr := requireNeighbour(g, capsules, container, links.next, capsule, capsules.Prev); nextErr != nil {
			return nextErr
		}
	}

	for _, rel := range incoming {
		if staleErr := checkStaleLink(g, capsules, rel.From, links); staleErr != nil {
			return staleErr
		}
	}

	return nil
}

// validateChain checks a non-empty list in full: head and tail are member
// capsules, the next chain from the head is finite, reaches the tail, has
// reciprocal links, a value everywhere and only members, the head has no
// previous capsule, and the chain covers exactly members. container is the node
// the members hang off (see checkCapsuleInChain).
func validateChain(graph GraphReader, capsules *CapsuleRegistry, container, head, tail NodeID, members map[NodeID]struct{}) error {
	headIsCapsule, err := capsules.IsCapsule(graph, head)
	if err != nil {
		return err
	}
	tailIsCapsule, err := capsules.IsCapsule(graph, tail)
	if err != nil {
		return err
	}
	if !headIsCapsule || !tailIsCapsule {
		return ErrInvalidListStructure
	}
	if _, ok := members[head]; !ok {
		return ErrInvalidListStructure
	}
	if _, ok := members[tail]; !ok {
		return ErrInvalidListStructure
	}

	visited := make(map[NodeID]struct{}, len(members))
	current := head
	for {
		if _, seen := visited[current]; seen {
			return ErrListCycle
		}
		visited[current] = struct{}{}

		currentIsCapsule, currentIsCapsuleErr := capsules.IsCapsule(graph, current)
		if currentIsCapsuleErr != nil {
			return currentIsCapsuleErr
		}
		if !currentIsCapsule {
			return ErrInvalidListStructure
		}

		currentInList, currentInListErr := graph.HasRelationship(container, current)
		if currentInListErr != nil {
			return wrapInterfaceErr(currentInListErr)
		}
		if !currentInList {
			return ErrInvalidListStructure
		}

		_, hasValue, valueErr := capsules.Value(graph, current)
		if valueErr != nil {
			return valueErr
		}
		if !hasValue {
			return ErrInvalidListStructure
		}

		next, hasNext, nextErr := capsules.Next(graph, current)
		if nextErr != nil {
			return nextErr
		}
		if !hasNext {
			if current != tail {
				return ErrInvalidListStructure
			}
			break
		}

		nextIsCapsule, nextIsCapsuleErr := capsules.IsCapsule(graph, next)
		if nextIsCapsuleErr != nil {
			return nextIsCapsuleErr
		}
		nextInList, nextInListErr := graph.HasRelationship(container, next)
		if nextInListErr != nil {
			return wrapInterfaceErr(nextInListErr)
		}
		if !nextIsCapsule || !nextInList {
			return ErrInvalidListStructure
		}

		prev, hasPrev, prevErr := capsules.Prev(graph, next)
		if prevErr != nil {
			return prevErr
		}
		if !hasPrev || prev != current {
			return ErrInvalidListStructure
		}

		current = next
	}

	_, headHasPrev, err := capsules.Prev(graph, head)
	if err != nil {
		return err
	}
	if headHasPrev {
		return ErrInvalidListStructure
	}

	if len(visited) != len(members) {
		return ErrInvalidListStructure
	}

	return nil
}

// stepBackWithin returns the capsule before capsule, checking only what one
// step needs: capsule is a member of container (the node its list's members
// hang off), and its previous capsule's next link points back at it. hasPrev
// is false for the head.
func stepBackWithin(graph GraphReader, capsules *CapsuleRegistry, container, capsule NodeID) (NodeID, bool, error) {
	inList, inListErr := graph.HasRelationship(container, capsule)
	if inListErr != nil {
		return 0, false, wrapInterfaceErr(inListErr)
	}
	if !inList {
		return 0, false, ErrInvalidListStructure
	}

	prev, hasPrev, prevErr := capsules.Prev(graph, capsule)
	if prevErr != nil || !hasPrev {
		return 0, false, prevErr
	}

	back, hasBack, backErr := capsules.Next(graph, prev)
	if backErr != nil {
		return 0, false, backErr
	}
	if !hasBack || back != capsule {
		return 0, false, ErrInvalidListStructure
	}

	return prev, true, nil
}

// verifyLists validates every list tagged allLists in full, one transaction
// per list so each read is consistent. It is fail-closed: the first list that
// fails is returned wrapped in ErrLoadVerification, and nothing is repaired.
func verifyLists(graph GraphAPI, allLists NodeID, validate func(g GraphReader, list NodeID) error) error {
	lists, err := graph.FindOutgoing(allLists)
	if err != nil {
		return wrapInterfaceErr(err)
	}

	for _, rel := range lists {
		list := rel.To

		verifyErr := graph.Transact(func(tx Tx) error {
			return wrapInterfaceErr(validate(tx, list))
		})
		if verifyErr != nil {
			return fmt.Errorf("%w: list %d: %w", ErrLoadVerification, list, verifyErr)
		}
	}

	return nil
}
