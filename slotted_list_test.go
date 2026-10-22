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
	"testing"
	"time"
)

// Tests of SlottedListRegistry's own structure and Checker. The behaviour it
// shares with ListRegistry is in ordered_list_test.go.

// buildSlottedList constructs a SlottedListRegistry, with a capsule registry
// of its own, over graph.
func buildSlottedList(t *testing.T, graph testGraph) *SlottedListRegistry {
	t.Helper()

	names := NewNameRegistry(graph)

	ids, err := names.BootstrapNames(graph, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	capsules, err := NewCapsuleRegistry(
		graph,
		ids[NameAllSlottedCapsules],
		ids[NameAllSlottedCapsulePrevSlot],
		ids[NameAllSlottedCapsuleValueSlot],
		ids[NameAllSlottedCapsuleNextSlot],
	)
	if err != nil {
		t.Fatalf("NewCapsuleRegistry(): %v", err)
	}

	lists, err := NewSlottedListRegistry(
		graph,
		capsules,
		ids[NameAllSlottedLists],
		ids[NameAllSlottedListMembers],
		ids[NameAllSlottedListHeadSlot],
		ids[NameAllSlottedListTailSlot],
	)
	if err != nil {
		t.Fatalf("NewSlottedListRegistry(): %v", err)
	}

	return lists
}

// slottedListRig is the list [a, b, c] (capsules[0..2]) plus a capsule,
// foreign, that is the sole element of another list, and a loose capsule.
type slottedListRig struct {
	graph    testGraph
	lists    *SlottedListRegistry
	list     NodeID
	slots    listSlots
	values   [3]NodeID
	capsules [3]NodeID
	foreign  NodeID
	loose    NodeID
}

func newSlottedListRig(t *testing.T) *slottedListRig {
	t.Helper()

	g := newTestGraph(t)
	lists := buildSlottedList(t, g)
	list := mustNewOrderedList(t, lists, g)

	values, capsules := mustAppendValues(t, lists, g, list, 3)

	rig := &slottedListRig{graph: g, lists: lists, list: list}
	copy(rig.values[:], values)
	copy(rig.capsules[:], capsules)

	slots, err := lists.slotsOf(g, list)
	if err != nil {
		t.Fatalf("slotsOf(): %v", err)
	}

	rig.slots = slots

	other := mustNewOrderedList(t, lists, g)
	rig.foreign = mustAppendTo(t, lists, g, other, mustCreateNode(t, g))

	loose, err := lists.Capsules().NewCapsule(g, mustCreateNode(t, g))
	if err != nil {
		t.Fatalf("NewCapsule(): %v", err)
	}

	rig.loose = loose

	return rig
}

// TestSlottedListCheckerDeclinesEachLocalViolationAtCommitTime corrupts the
// list in one transaction per case and checks that the commit is declined and
// the list is left as it was.
func TestSlottedListCheckerDeclinesEachLocalViolationAtCommitTime(t *testing.T) {
	cases := []struct {
		name    string
		corrupt func(tx Tx, r *slottedListRig) error
	}{
		{
			name: "a back link is missing",
			corrupt: func(tx Tx, r *slottedListRig) error {
				_, err := r.lists.capsules.RemovePrev(tx, r.capsules[2])
				return err
			},
		},
		{
			name: "a forward link skips a member",
			corrupt: func(tx Tx, r *slottedListRig) error {
				return r.lists.capsules.SetNext(tx, r.capsules[0], r.capsules[2])
			},
		},
		{
			name: "the head slot points at a capsule that is not a member",
			corrupt: func(tx Tx, r *slottedListRig) error {
				return r.lists.headSlots.SetTarget(tx, r.slots.head, r.loose)
			},
		},
		{
			name: "the tail slot points at a capsule that is not a member",
			corrupt: func(tx Tx, r *slottedListRig) error {
				return r.lists.tailSlots.SetTarget(tx, r.slots.tail, r.loose)
			},
		},
		{
			name: "the tail slot loses its target",
			corrupt: func(tx Tx, r *slottedListRig) error {
				_, err := r.lists.tailSlots.RemoveTarget(tx, r.slots.tail)
				return err
			},
		},
		{
			name: "the head slot points at the middle of the chain",
			corrupt: func(tx Tx, r *slottedListRig) error {
				return r.lists.headSlots.SetTarget(tx, r.slots.head, r.capsules[1])
			},
		},
		{
			name: "a capsule of another list is linked in from outside",
			corrupt: func(tx Tx, r *slottedListRig) error {
				return r.lists.capsules.SetNext(tx, r.capsules[2], r.foreign)
			},
		},
		{
			name: "a capsule of another list becomes a member of this one too",
			corrupt: func(tx Tx, r *slottedListRig) error {
				return addRelationshipTx(tx, r.slots.members, r.foreign)
			},
		},
		{
			name: "a member is never wired into the chain",
			corrupt: func(tx Tx, r *slottedListRig) error {
				return addRelationshipTx(tx, r.slots.members, r.loose)
			},
		},
		{
			name: "a member is unlinked without relinking its neighbours",
			corrupt: func(tx Tx, r *slottedListRig) error {
				return removeRelationshipTx(tx, r.slots.members, r.capsules[1])
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newSlottedListRig(t)

			err := rig.graph.Transact(func(tx Tx) error {
				return tc.corrupt(tx, rig)
			})
			if !errors.Is(err, ErrInvalidListStructure) {
				t.Fatalf("Transact() error = %v, want %v", err, ErrInvalidListStructure)
			}

			// Elements validates the whole list, so this also proves the
			// declined commit was rolled back.
			requireListElements(t, rig.lists, rig.graph, rig.list, rig.values[:])
		})
	}
}

// A second target on a slot is the slot's own PointerRegistry Checker's.
func TestSlottedListSlotsHoldAtMostOneTarget(t *testing.T) {
	rig := newSlottedListRig(t)

	err := rig.graph.Transact(func(tx Tx) error {
		return addRelationshipTx(tx, rig.slots.head, rig.capsules[1])
	})
	if !errors.Is(err, ErrTooManyPointerTargets) {
		t.Fatalf("Transact() error = %v, want %v", err, ErrTooManyPointerTargets)
	}

	requireListElements(t, rig.lists, rig.graph, rig.list, rig.values[:])
}

// TestSlottedListLeavesGlobalStructureToReadsAndVerifyStructures: a stray
// child of the members node is not something the local Checker looks at, so
// the commit is accepted and Elements and VerifyStructures find it.
func TestSlottedListLeavesGlobalStructureToReadsAndVerifyStructures(t *testing.T) {
	rig := newSlottedListRig(t)

	if verifyErr := rig.lists.VerifyStructures(rig.graph); verifyErr != nil {
		t.Fatalf("VerifyStructures() on valid lists: %v", verifyErr)
	}

	stray := newTestNode(t, rig.graph)

	strayErr := rig.graph.Transact(func(tx Tx) error {
		return addRelationshipTx(tx, rig.slots.members, stray)
	})
	if strayErr != nil {
		t.Fatalf("Transact() error = %v, want the stray child accepted at commit", strayErr)
	}

	if _, elementsErr := rig.lists.Elements(rig.graph, rig.list); !errors.Is(elementsErr, ErrInvalidListStructure) {
		t.Fatalf("Elements() error = %v, want %v", elementsErr, ErrInvalidListStructure)
	}

	verifyErr := rig.lists.VerifyStructures(rig.graph)
	if !errors.Is(verifyErr, ErrLoadVerification) || !errors.Is(verifyErr, ErrInvalidListStructure) {
		t.Fatalf("VerifyStructures() error = %v, want %v wrapping %v", verifyErr, ErrLoadVerification, ErrInvalidListStructure)
	}
}

// TestSlottedListLookupsDoNotReadTheMembersNode: the point of the structure.
// Head, Tail and Contains never read the children of the members node, which
// has one per element.
func TestSlottedListLookupsDoNotReadTheMembersNode(t *testing.T) {
	g := newTestGraph(t)
	lists := buildSlottedList(t, g)
	list := mustNewOrderedList(t, lists, g)
	values, _ := mustAppendValues(t, lists, g, list, 30)

	slots, err := lists.slotsOf(g, list)
	if err != nil {
		t.Fatalf("slotsOf(): %v", err)
	}

	reads := &outgoingReadCounter{GraphReader: g, watched: slots.members}

	if _, hasHead, headErr := lists.Head(reads, list); headErr != nil || !hasHead {
		t.Fatalf("Head() = (%v,%v), want (true,nil)", hasHead, headErr)
	}

	if _, hasTail, tailErr := lists.Tail(reads, list); tailErr != nil || !hasTail {
		t.Fatalf("Tail() = (%v,%v), want (true,nil)", hasTail, tailErr)
	}

	if _, found, containsErr := lists.Contains(reads, list, values[10]); containsErr != nil || !found {
		t.Fatalf("Contains() = (%v,%v), want (true,nil)", found, containsErr)
	}

	if reads.calls != 0 {
		t.Fatalf("the members node's children were read %d times, want 0", reads.calls)
	}
}

// TestSlottedListDeleteListRemovesEveryNodeItMade: a list is four nodes, and
// deleting it leaves nothing behind.
func TestSlottedListDeleteListRemovesEveryNodeItMade(t *testing.T) {
	g := newTestGraph(t)
	lists := buildSlottedList(t, g)

	before := len(mustFindNodes(t, g))
	list := mustNewOrderedList(t, lists, g)

	if got := len(mustFindNodes(t, g)); got != before+4 {
		t.Fatalf("a new list made %d nodes, want 4", got-before)
	}

	if err := lists.DeleteList(g, list); err != nil {
		t.Fatalf("DeleteList(): %v", err)
	}

	if got := len(mustFindNodes(t, g)); got != before {
		t.Fatalf("%d nodes are left after DeleteList(), want %d", got, before)
	}
}

// TestHostRegistriesCarryBothListTypes: both list types work side by side in
// the production stack, each with its own Checker.
func TestHostRegistriesCarryBothListTypes(t *testing.T) {
	h := openTestHost(t, boltTestPath(t), nil)
	reg := h.Registries()

	for _, lists := range []OrderedList{reg.Lists, reg.SlottedLists} {
		list := mustNewOrderedList(t, lists, h.Graph())
		values, capsules := mustAppendValues(t, lists, h.Graph(), list, 3)

		requireListState(t, lists, h.Graph(), list, values, capsules)

		if verifyErr := lists.VerifyStructures(h.Graph()); verifyErr != nil {
			t.Fatalf("VerifyStructures(): %v", verifyErr)
		}
	}
}

// benchCommit times step as one transaction per iteration.
func benchCommit(b *testing.B, graph GraphAPI, step func(tx Tx) error) {
	b.Helper()
	b.ReportAllocs()

	for b.Loop() {
		if txErr := graph.Transact(step); txErr != nil {
			b.Fatalf("Transact(): %v", txErr)
		}
	}
}

// BenchmarkSlottedListCommitCost is BenchmarkListCommitCost for the slotted
// list. append should be flat, about the empty commit, whatever n is. Run
// without -race:
//
//	go test -run "^$" -bench SlottedListCommitCost -benchtime 30x -timeout 60m
func BenchmarkSlottedListCommitCost(b *testing.B) {
	for _, size := range listBenchSizes {
		b.Run(fmt.Sprintf("n=%d", size), func(b *testing.B) {
			if testing.Short() && size > listBenchShortLimit {
				b.Skipf("building a list of %d elements takes a long time; skipped under -short", size)
			}

			h := openListBenchHost(b)
			graph := h.Graph()
			lists := h.Registries().SlottedLists

			start := time.Now()
			list := buildBenchList(b, h, lists, size)
			b.Logf("built a list of %d elements in %v", size, time.Since(start))

			bystander, err := createNodeVia(graph)
			if err != nil {
				b.Fatalf("CreateNode() for the bystander: %v", err)
			}

			value, err := createNodeVia(graph)
			if err != nil {
				b.Fatalf("CreateNode() for the value: %v", err)
			}

			b.Run("empty-commit", func(b *testing.B) {
				benchCommit(b, graph, func(tx Tx) error {
					tx.Touch(bystander)

					return nil
				})
			})

			b.Run("tail-lookup", func(b *testing.B) {
				benchCommit(b, graph, func(tx Tx) error {
					_, _, tailErr := lists.Tail(tx, list)

					return tailErr
				})
			})

			b.Run("append", func(b *testing.B) {
				benchCommit(b, graph, func(tx Tx) error {
					_, appendErr := lists.Append(tx, list, value)

					return appendErr
				})
			})
		})
	}
}