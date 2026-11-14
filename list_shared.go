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
	"fmt"
)

// capsuleLinks is a capsule's own prev and next links, read once by
// checkCapsule.
type capsuleLinks struct {
	prev, next       NodeID
	hasPrev, hasNext bool
}

// requireNeighbour checks that neighbour is a capsule of list whose link in
// the opposite direction (backLink: Next for a previous neighbour, Prev for
// a next one) points back at capsule.
func requireNeighbour(g GraphReader, capsules *CapsuleRegistry, container, neighbour, capsule NodeID, backLink func(GraphReader, NodeID) (NodeID, bool, error)) error {
	isCapsule, capsuleErr := capsules.IsCapsule(g, neighbour)
	if capsuleErr != nil {
		return capsuleErr
	}

	inList, inListErr := g.HasRelationship(container, neighbour)
	if inListErr != nil {
		return wrapInterfaceErr(inListErr)
	}

	if !isCapsule || !inList {
		return ErrInvalidListStructure
	}

	back, hasBack, backErr := backLink(g, neighbour)
	if backErr != nil {
		return wrapInterfaceErr(backErr)
	}

	if !hasBack || back != capsule {
		return ErrInvalidListStructure
	}

	return nil
}

// checkStaleLink rejects a node that points at the capsule through a role
// slot the capsule does not agree with: a next-slot pointing at it must
// belong to the capsule's own prev, and a prev-slot to its next. This is
// what catches a stale pointer left by a neighbour the transaction did not
// touch.
func checkStaleLink(g GraphReader, capsules *CapsuleRegistry, source NodeID, links capsuleLinks) error {
	roles := [...]struct {
		slots *PointerRegistry
		other NodeID
		has   bool
	}{
		{slots: capsules.nextSlots, other: links.prev, has: links.hasPrev},
		{slots: capsules.prevSlots, other: links.next, has: links.hasNext},
	}

	for _, role := range roles {
		isSlot, slotErr := role.slots.IsPointer(g, source)
		if slotErr != nil {
			return slotErr
		}
		if !isSlot {
			continue
		}

		owner, found, ownerErr := findUniqueTaggedParent(g, source, capsules.allElementCapsules)
		if ownerErr != nil {
			return ownerErr
		}

		if found && (!role.has || owner != role.other) {
			return ErrInvalidListStructure
		}
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
