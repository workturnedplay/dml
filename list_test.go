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
	"reflect"
	"testing"
)

func newListTestFixture(t *testing.T) (testGraph, *CapsuleRegistry, *ListRegistry) {
	t.Helper()

	g := newTestGraph(t)
	names := NewNameRegistry(g)

	ids, err := names.BootstrapNames(g, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	capsules, err := NewCapsuleRegistry(
		g,
		ids[NameAllElementCapsules],
		ids[NameAllElementCapsulePrevSlot],
		ids[NameAllElementCapsuleValueSlot],
		ids[NameAllElementCapsuleNextSlot],
	)
	if err != nil {
		t.Fatalf("NewCapsuleRegistry(): %v", err)
	}

	lists, err := NewListRegistry(g, capsules, ids[NameAllLists], ids[NameAllHeads], ids[NameAllTails])
	if err != nil {
		t.Fatalf("NewListRegistry(): %v", err)
	}

	return g, capsules, lists
}

func TestFoundationalNamesIncludesListNames(t *testing.T) {
	want := []string{NameAllLists, NameAllHeads, NameAllTails}

	for _, name := range want {
		found := false
		for _, got := range FoundationalNames {
			if got == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("FoundationalNames %v does not include %q", FoundationalNames, name)
		}
	}
}

func TestNewListRegistryRequiresExistingTags(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	ids, err := names.BootstrapNames(&g, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	capsules, err := NewCapsuleRegistry(
		&g,
		ids[NameAllElementCapsules],
		ids[NameAllElementCapsulePrevSlot],
		ids[NameAllElementCapsuleValueSlot],
		ids[NameAllElementCapsuleNextSlot],
	)
	if err != nil {
		t.Fatalf("NewCapsuleRegistry(): %v", err)
	}

	const nonexistent NodeID = 999999
	existing := ids[NameAllLists]

	if _, err := NewListRegistry(&g, capsules, nonexistent, existing, existing); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("NewListRegistry() error = %v, want %v", err, ErrNodeNotFound)
	}

	if _, err := NewListRegistry(&g, capsules, existing, nonexistent, existing); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("NewListRegistry() error = %v, want %v", err, ErrNodeNotFound)
	}

	if _, err := NewListRegistry(&g, capsules, existing, existing, nonexistent); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("NewListRegistry() error = %v, want %v", err, ErrNodeNotFound)
	}
}

func TestNewListTagsListAndStartsEmpty(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	if !mustNodeExists(t, g, list) {
		t.Fatalf("NewList() returned NodeID %d that does not exist", list)
	}

	if !mustIsList(t, lists, g, list) {
		t.Fatalf("NewList() did not tag %d as a list", list)
	}

	if _, hasHead, err2 := lists.Head(g, list); err2 != nil {
		t.Fatalf("Head(%d): %v", list, err2)
	} else if hasHead {
		t.Fatalf("fresh list %d unexpectedly has a head", list)
	}

	if _, hasTail, err3 := lists.Tail(g, list); err3 != nil {
		t.Fatalf("Tail(%d): %v", list, err3)
	} else if hasTail {
		t.Fatalf("fresh list %d unexpectedly has a tail", list)
	}

	elements, err := lists.Elements(g, list)
	if err != nil {
		t.Fatalf("Elements(%d): %v", list, err)
	}
	if len(elements) != 0 {
		t.Fatalf("Elements(%d) = %v, want empty", list, elements)
	}
}

func TestListAppendSingleElementIsHeadAndTail(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	capsule, err := lists.Append(g, list, value)
	if err != nil {
		t.Fatalf("Append(): %v", err)
	}

	head, hasHead, err := lists.Head(g, list)
	if err != nil {
		t.Fatalf("Head(%d): %v", list, err)
	}
	if !hasHead || head != capsule {
		t.Fatalf("Head(%d) = (%d,%v), want (%d,true)", list, head, hasHead, capsule)
	}

	tail, hasTail, err := lists.Tail(g, list)
	if err != nil {
		t.Fatalf("Tail(%d): %v", list, err)
	}
	if !hasTail || tail != capsule {
		t.Fatalf("Tail(%d) = (%d,%v), want (%d,true)", list, tail, hasTail, capsule)
	}
}

func TestListAppendMultipleMaintainsOrder(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	var values []NodeID
	for i := 0; i < 3; i++ {
		v, err2 := g.CreateNode()
		if err2 != nil {
			t.Fatalf("CreateNode() for value %d: %v", i, err2)
		}
		values = append(values, v)

		if _, err3 := lists.Append(g, list, v); err3 != nil {
			t.Fatalf("Append(%d): %v", v, err3)
		}
	}

	got, err := lists.Elements(g, list)
	if err != nil {
		t.Fatalf("Elements(%d): %v", list, err)
	}

	if !reflect.DeepEqual(got, values) {
		t.Fatalf("Elements(%d) = %v, want %v", list, got, values)
	}

	tail, hasTail, err := lists.Tail(g, list)
	if err != nil {
		t.Fatalf("Tail(%d): %v", list, err)
	}
	lastValue, _, err := lists.capsules.Value(g, tail)
	if err != nil {
		t.Fatalf("Value(tail): %v", err)
	}
	if !hasTail || lastValue != values[len(values)-1] {
		t.Fatalf("tail capsule's value = %d, want %d", lastValue, values[len(values)-1])
	}
}

func TestListAppendManyAppendsInOrderAndReturnsTheCapsules(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	first := newTestNode(t, g)
	a := newTestNode(t, g)
	b := newTestNode(t, g)
	c := newTestNode(t, g)

	// A non-empty list first, so the tail being extended is a real one.
	if _, appendErr := lists.Append(g, list, first); appendErr != nil {
		t.Fatalf("Append(first): %v", appendErr)
	}

	capsules, err := lists.AppendMany(g, list, []NodeID{a, b, c})
	if err != nil {
		t.Fatalf("AppendMany(): %v", err)
	}
	if len(capsules) != 3 {
		t.Fatalf("AppendMany() returned %d capsules, want 3", len(capsules))
	}

	requireListElements(t, lists, g, list, []NodeID{first, a, b, c})

	for i, want := range []NodeID{a, b, c} {
		value, hasValue, valueErr := lists.capsules.Value(g, capsules[i])
		if valueErr != nil {
			t.Fatalf("Value(capsules[%d]): %v", i, valueErr)
		}
		if !hasValue || value != want {
			t.Fatalf("Value(capsules[%d]) = (%d,%v), want (%d,true)", i, value, hasValue, want)
		}
	}

	tail, hasTail, tailErr := lists.Tail(g, list)
	if tailErr != nil {
		t.Fatalf("Tail(): %v", tailErr)
	}
	if !hasTail || tail != capsules[2] {
		t.Fatalf("Tail() = (%d,%v), want (%d,true)", tail, hasTail, capsules[2])
	}
}

func TestListAppendManyOnAnEmptyListSetsHeadAndTail(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	a := newTestNode(t, g)
	b := newTestNode(t, g)

	capsules, err := lists.AppendMany(g, list, []NodeID{a, b})
	if err != nil {
		t.Fatalf("AppendMany(): %v", err)
	}

	requireListElements(t, lists, g, list, []NodeID{a, b})

	head, hasHead, headErr := lists.Head(g, list)
	if headErr != nil {
		t.Fatalf("Head(): %v", headErr)
	}
	if !hasHead || head != capsules[0] {
		t.Fatalf("Head() = (%d,%v), want (%d,true)", head, hasHead, capsules[0])
	}

	tail, hasTail, tailErr := lists.Tail(g, list)
	if tailErr != nil {
		t.Fatalf("Tail(): %v", tailErr)
	}
	if !hasTail || tail != capsules[1] {
		t.Fatalf("Tail() = (%d,%v), want (%d,true)", tail, hasTail, capsules[1])
	}
}

// TestListAppendManyIsAtomicWhenAValueIsMissing: the first value is fine and
// is wired in before the second one fails, so only a rollback of the whole
// call leaves the list as it was.
func TestListAppendManyIsAtomicWhenAValueIsMissing(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	existing := newTestNode(t, g)
	a := newTestNode(t, g)

	if _, appendErr := lists.Append(g, list, existing); appendErr != nil {
		t.Fatalf("Append(existing): %v", appendErr)
	}

	const nonexistent NodeID = 999999

	if _, manyErr := lists.AppendMany(g, list, []NodeID{a, nonexistent}); !errors.Is(manyErr, ErrNodeNotFound) {
		t.Fatalf("AppendMany() error = %v, want %v", manyErr, ErrNodeNotFound)
	}

	// Elements validates head/tail, links and reachability, so this also
	// proves the first value's capsule and the tail move were undone.
	requireListElements(t, lists, g, list, []NodeID{existing})
}

func TestListAppendManyOfNothingStillChecksTheList(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	capsules, err := lists.AppendMany(g, list, nil)
	if err != nil || len(capsules) != 0 {
		t.Fatalf("AppendMany(nothing) = (%v,%v), want (none,nil)", capsules, err)
	}

	requireListElements(t, lists, g, list, nil)

	notAList := newTestNode(t, g)

	if _, manyErr := lists.AppendMany(g, notAList, nil); !errors.Is(manyErr, ErrNotList) {
		t.Fatalf("AppendMany(not a list) error = %v, want %v", manyErr, ErrNotList)
	}
}

func TestListPrependAddsAtFront(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}
	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for b: %v", err)
	}

	if _, err2 := lists.Append(g, list, a); err2 != nil {
		t.Fatalf("Append(a): %v", err2)
	}
	if _, err3 := lists.Prepend(g, list, b); err3 != nil {
		t.Fatalf("Prepend(b): %v", err3)
	}

	got, err := lists.Elements(g, list)
	if err != nil {
		t.Fatalf("Elements(%d): %v", list, err)
	}

	want := []NodeID{b, a}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Elements(%d) = %v, want %v", list, got, want)
	}

	head, hasHead, err := lists.Head(g, list)
	if err != nil {
		t.Fatalf("Head(%d): %v", list, err)
	}
	headValue, _, err := lists.capsules.Value(g, head)
	if err != nil {
		t.Fatalf("Value(head): %v", err)
	}
	if !hasHead || headValue != b {
		t.Fatalf("head value = %d, want %d", headValue, b)
	}
}

func TestListInsertAfterMiddle(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}
	c, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for c: %v", err)
	}
	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for b: %v", err)
	}

	capsuleA, err := lists.Append(g, list, a)
	if err != nil {
		t.Fatalf("Append(a): %v", err)
	}
	if _, err2 := lists.Append(g, list, c); err2 != nil {
		t.Fatalf("Append(c): %v", err2)
	}

	if _, err3 := lists.InsertAfter(g, list, capsuleA, b); err3 != nil {
		t.Fatalf("InsertAfter(capsuleA, b): %v", err3)
	}

	got, err := lists.Elements(g, list)
	if err != nil {
		t.Fatalf("Elements(%d): %v", list, err)
	}

	want := []NodeID{a, b, c}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Elements(%d) = %v, want %v", list, got, want)
	}

	tail, hasTail, err := lists.Tail(g, list)
	if err != nil {
		t.Fatalf("Tail(%d): %v", list, err)
	}
	tailValue, _, err := lists.capsules.Value(g, tail)
	if err != nil {
		t.Fatalf("Value(tail): %v", err)
	}
	if !hasTail || tailValue != c {
		t.Fatalf("tail value = %d, want %d (tail should be unaffected by a middle insert)", tailValue, c)
	}
}

func TestListInsertAfterTailUpdatesTail(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}
	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for b: %v", err)
	}

	capsuleA, err := lists.Append(g, list, a)
	if err != nil {
		t.Fatalf("Append(a): %v", err)
	}

	capsuleB, err := lists.InsertAfter(g, list, capsuleA, b)
	if err != nil {
		t.Fatalf("InsertAfter(capsuleA, b): %v", err)
	}

	tail, hasTail, err := lists.Tail(g, list)
	if err != nil {
		t.Fatalf("Tail(%d): %v", list, err)
	}
	if !hasTail || tail != capsuleB {
		t.Fatalf("Tail(%d) = (%d,%v), want (%d,true)", list, tail, hasTail, capsuleB)
	}

	// The old tail (capsuleA) must have lost its AllTails tag.
	if mustHasRelationship(t, g, lists.allTails, capsuleA) {
		t.Fatalf("old tail capsule %d is still tagged AllTails after InsertAfter extended the list", capsuleA)
	}

	head, hasHead, err := lists.Head(g, list)
	if err != nil {
		t.Fatalf("Head(%d): %v", list, err)
	}
	if !hasHead || head != capsuleA {
		t.Fatalf("Head(%d) = (%d,%v), want (%d,true) (head should be unaffected)", list, head, hasHead, capsuleA)
	}
}

func TestListInsertAfterRequiresCapsuleInList(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	other, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for other: %v", err)
	}

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	_, err = lists.InsertAfter(g, list, other, value)
	if !errors.Is(err, ErrCapsuleNotInList) {
		t.Fatalf("InsertAfter() error = %v, want %v", err, ErrCapsuleNotInList)
	}
}

// TestListRegistryCheckerCatchesInvalidStructureAtCommitTime demonstrates
// the ListRegistry Checker (registered by NewListRegistry) catching an
// invalid list structure immediately, at commit time, rather than only
// the next time something calls Elements(). Unlike the many existing
// out-of-band adversarial tests elsewhere in this file, this simulates
// the corruption through Graph.Transact directly (via raw tx calls),
// since only mutations made through Transact are visible to any
// Checker at all -- a raw, non-transactional Graph.AddRelationship call
// (what those other tests use) bypasses every Checker entirely, exactly
// as documented on the Checker type itself.
func TestListRegistryCheckerCatchesInvalidStructureAtCommitTime(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	bogus, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for bogus: %v", err)
	}

	// Simulate a hypothetical buggy composed operation that tags a
	// non-capsule node as list's head and links it in as a child,
	// entirely through one Graph.Transact call.
	err = g.Transact(func(tx Tx) error {
		if err2 := addRelationshipTx(tx, list, bogus); err2 != nil {
			return err2
		}
		return addRelationshipTx(tx, lists.allHeads, bogus)
	})

	if !errors.Is(err, ErrInvalidListStructure) {
		t.Fatalf("Transact() error = %v, want %v", err, ErrInvalidListStructure)
	}

	// Confirm the whole changeset was rolled back: bogus must not be
	// linked into list nor tagged as head.
	if mustHasRelationship(t, g, list, bogus) {
		t.Fatal("list still contains bogus after the Checker declined the commit")
	}
	if mustHasRelationship(t, g, lists.allHeads, bogus) {
		t.Fatal("bogus is still tagged AllHeads after the Checker declined the commit")
	}

	if _, hasHead, err3 := lists.Head(g, list); err3 != nil {
		t.Fatalf("Head(list): %v", err3)
	} else if hasHead {
		t.Fatal("list unexpectedly has a head after the Checker declined the commit")
	}
}

// listCorruptionRig is the list [a, b, c] (capsules[0..2], values[0..2]) plus
// one capsule, foreign, that is the sole element of another list.
type listCorruptionRig struct {
	graph    testGraph
	lists    *ListRegistry
	list     NodeID
	values   [3]NodeID
	capsules [3]NodeID
	foreign  NodeID
}

func newListCorruptionRig(t *testing.T) *listCorruptionRig {
	t.Helper()

	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	rig := &listCorruptionRig{graph: g, lists: lists, list: list}

	for i := range rig.values {
		rig.values[i] = newTestNode(t, g)

		capsule, appendErr := lists.Append(g, list, rig.values[i])
		if appendErr != nil {
			t.Fatalf("Append(%d): %v", i, appendErr)
		}

		rig.capsules[i] = capsule
	}

	otherList, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList() for the other list: %v", err)
	}

	foreign, err := lists.Append(g, otherList, newTestNode(t, g))
	if err != nil {
		t.Fatalf("Append() to the other list: %v", err)
	}

	rig.foreign = foreign

	return rig
}

// newLooseCapsuleTx makes a capsule, with a fresh value, that is linked into
// no list.
func newLooseCapsuleTx(tx Tx, lists *ListRegistry) (NodeID, error) {
	value, createErr := createNodeTx(tx)
	if createErr != nil {
		return 0, createErr
	}

	return lists.capsules.NewCapsule(tx, value)
}

// TestListCheckerDeclinesEachLocalViolationAtCommitTime corrupts a list in
// one transaction per case, through paths that touch only the capsules (or
// only the list) involved, and checks that the commit is declined and the
// list is left exactly as it was.
func TestListCheckerDeclinesEachLocalViolationAtCommitTime(t *testing.T) {
	cases := []struct {
		name    string
		corrupt func(tx Tx, r *listCorruptionRig) error
	}{
		{
			name: "a back link is missing",
			corrupt: func(tx Tx, r *listCorruptionRig) error {
				_, err := r.lists.capsules.RemovePrev(tx, r.capsules[2])
				return err
			},
		},
		{
			name: "a forward link skips a member",
			corrupt: func(tx Tx, r *listCorruptionRig) error {
				return r.lists.capsules.SetNext(tx, r.capsules[0], r.capsules[2])
			},
		},
		{
			name: "a second capsule is tagged head",
			corrupt: func(tx Tx, r *listCorruptionRig) error {
				return addRelationshipTx(tx, r.lists.allHeads, r.capsules[1])
			},
		},
		{
			name: "the tail loses its tag",
			corrupt: func(tx Tx, r *listCorruptionRig) error {
				return removeRelationshipTx(tx, r.lists.allTails, r.capsules[2])
			},
		},
		{
			name: "a capsule of another list is linked in from outside",
			corrupt: func(tx Tx, r *listCorruptionRig) error {
				return r.lists.capsules.SetNext(tx, r.capsules[2], r.foreign)
			},
		},
		{
			name: "a member is never wired into the chain",
			corrupt: func(tx Tx, r *listCorruptionRig) error {
				capsule, err := newLooseCapsuleTx(tx, r.lists)
				if err != nil {
					return err
				}

				return addRelationshipTx(tx, r.list, capsule)
			},
		},
		{
			name: "a member is unlinked without relinking its neighbours",
			corrupt: func(tx Tx, r *listCorruptionRig) error {
				return removeRelationshipTx(tx, r.list, r.capsules[1])
			},
		},
		{
			name: "a capsule outside every list is tagged tail",
			corrupt: func(tx Tx, r *listCorruptionRig) error {
				capsule, err := newLooseCapsuleTx(tx, r.lists)
				if err != nil {
					return err
				}

				return addRelationshipTx(tx, r.lists.allTails, capsule)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newListCorruptionRig(t)

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

// TestListCheckerLeavesGlobalStructureToReadsAndVerifyStructures pins down
// what the local Checker deliberately does not see: a second chain, locally
// consistent, with its own head and tail. The commit is accepted; Elements
// and VerifyStructures find the two heads.
func TestListCheckerLeavesGlobalStructureToReadsAndVerifyStructures(t *testing.T) {
	rig := newListCorruptionRig(t)

	if verifyErr := rig.lists.VerifyStructures(rig.graph); verifyErr != nil {
		t.Fatalf("VerifyStructures() on valid lists: %v", verifyErr)
	}

	err := rig.graph.Transact(func(tx Tx) error {
		first, firstErr := newLooseCapsuleTx(tx, rig.lists)
		if firstErr != nil {
			return firstErr
		}

		second, secondErr := newLooseCapsuleTx(tx, rig.lists)
		if secondErr != nil {
			return secondErr
		}

		for _, capsule := range []NodeID{first, second} {
			if linkErr := addRelationshipTx(tx, rig.list, capsule); linkErr != nil {
				return linkErr
			}
		}

		if nextErr := rig.lists.capsules.SetNext(tx, first, second); nextErr != nil {
			return nextErr
		}

		if prevErr := rig.lists.capsules.SetPrev(tx, second, first); prevErr != nil {
			return prevErr
		}

		if headErr := addRelationshipTx(tx, rig.lists.allHeads, first); headErr != nil {
			return headErr
		}

		return addRelationshipTx(tx, rig.lists.allTails, second)
	})
	if err != nil {
		t.Fatalf("Transact() error = %v, want the locally consistent second chain accepted", err)
	}

	if _, elementsErr := rig.lists.Elements(rig.graph, rig.list); !errors.Is(elementsErr, ErrAmbiguousPointerMetadata) {
		t.Fatalf("Elements() error = %v, want %v (two heads)", elementsErr, ErrAmbiguousPointerMetadata)
	}

	verifyErr := rig.lists.VerifyStructures(rig.graph)
	if !errors.Is(verifyErr, ErrLoadVerification) || !errors.Is(verifyErr, ErrAmbiguousPointerMetadata) {
		t.Fatalf("VerifyStructures() error = %v, want %v wrapping %v", verifyErr, ErrLoadVerification, ErrAmbiguousPointerMetadata)
	}
}

// TestListCheckerDeclinesACapsuleLinkedIntoASecondList: a member capsule
// belongs to exactly one list, so linking one into a second is declined at
// commit and rolled back.
func TestListCheckerDeclinesACapsuleLinkedIntoASecondList(t *testing.T) {
	rig := newListCorruptionRig(t)

	owners, err := rig.lists.ListsOf(rig.graph, rig.capsules[1])
	if err != nil || !reflect.DeepEqual(owners, []NodeID{rig.list}) {
		t.Fatalf("ListsOf() = (%v,%v), want ([%d],nil)", owners, err, rig.list)
	}

	other, err := rig.lists.NewList(rig.graph)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	err = rig.graph.Transact(func(tx Tx) error {
		return addRelationshipTx(tx, other, rig.capsules[1])
	})
	if !errors.Is(err, ErrInvalidListStructure) {
		t.Fatalf("Transact() error = %v, want %v", err, ErrInvalidListStructure)
	}

	requireListElements(t, rig.lists, rig.graph, rig.list, rig.values[:])
}

func TestListOperationsRequireListTag(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	notAList, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	if _, _, err := lists.Head(g, notAList); !errors.Is(err, ErrNotList) {
		t.Fatalf("Head() error = %v, want %v", err, ErrNotList)
	}

	if _, _, err := lists.Tail(g, notAList); !errors.Is(err, ErrNotList) {
		t.Fatalf("Tail() error = %v, want %v", err, ErrNotList)
	}

	if _, err := lists.Append(g, notAList, value); !errors.Is(err, ErrNotList) {
		t.Fatalf("Append() error = %v, want %v", err, ErrNotList)
	}

	if _, err := lists.Prepend(g, notAList, value); !errors.Is(err, ErrNotList) {
		t.Fatalf("Prepend() error = %v, want %v", err, ErrNotList)
	}

	if _, err := lists.Elements(g, notAList); !errors.Is(err, ErrNotList) {
		t.Fatalf("Elements() error = %v, want %v", err, ErrNotList)
	}
}

func TestListContainsFindsValue(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}
	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for b: %v", err)
	}
	c, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for c: %v", err)
	}

	if _, err2 := lists.Append(g, list, a); err2 != nil {
		t.Fatalf("Append(a): %v", err2)
	}
	capsuleB, err := lists.Append(g, list, b)
	if err != nil {
		t.Fatalf("Append(b): %v", err)
	}
	if _, err3 := lists.Append(g, list, c); err3 != nil {
		t.Fatalf("Append(c): %v", err3)
	}

	got, found, err := lists.Contains(g, list, b)
	if err != nil {
		t.Fatalf("Contains(list, b): %v", err)
	}
	if !found || got != capsuleB {
		t.Fatalf("Contains(list, b) = (%d,%v), want (%d,true)", got, found, capsuleB)
	}
}

func TestListContainsFalseForAbsentValue(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}

	absent, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for absent: %v", err)
	}

	if _, err2 := lists.Append(g, list, a); err2 != nil {
		t.Fatalf("Append(a): %v", err2)
	}

	_, found, err := lists.Contains(g, list, absent)
	if err != nil {
		t.Fatalf("Contains(list, absent): %v", err)
	}
	if found {
		t.Fatal("Contains() reported a value that was never appended")
	}
}

func TestListContainsScopedToOwningList(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	listA, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList() for listA: %v", err)
	}

	listB, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList() for listB: %v", err)
	}

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	if _, err2 := lists.Append(g, listB, value); err2 != nil {
		t.Fatalf("Append(listB, value): %v", err2)
	}

	_, found, err := lists.Contains(g, listA, value)
	if err != nil {
		t.Fatalf("Contains(listA, value): %v", err)
	}
	if found {
		t.Fatal("Contains(listA, value) incorrectly found a value that only exists in listB")
	}

	_, found, err = lists.Contains(g, listB, value)
	if err != nil {
		t.Fatalf("Contains(listB, value): %v", err)
	}
	if !found {
		t.Fatal("Contains(listB, value) did not find a value that was appended to listB")
	}
}

func TestListOccurrencesOfFindsDuplicates(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	c1, err := lists.Append(g, list, value)
	if err != nil {
		t.Fatalf("first Append(value): %v", err)
	}

	c2, err := lists.Append(g, list, value)
	if err != nil {
		t.Fatalf("second Append(value): %v", err)
	}

	got, err := lists.OccurrencesOf(g, list, value)
	if err != nil {
		t.Fatalf("OccurrencesOf(list, value): %v", err)
	}

	want := sortedNodeIDs([]NodeID{c1, c2})
	got = sortedNodeIDs(got)

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OccurrencesOf(list, value) = %v, want %v", got, want)
	}

	_, found, err := lists.Contains(g, list, value)
	if err != nil {
		t.Fatalf("Contains(list, value): %v", err)
	}
	if !found {
		t.Fatal("Contains() did not find a value with duplicate occurrences")
	}
}

func TestListContainsRequiresListTag(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	notAList, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	if _, _, err := lists.Contains(g, notAList, value); !errors.Is(err, ErrNotList) {
		t.Fatalf("Contains() error = %v, want %v", err, ErrNotList)
	}

	if _, err := lists.OccurrencesOf(g, notAList, value); !errors.Is(err, ErrNotList) {
		t.Fatalf("OccurrencesOf() error = %v, want %v", err, ErrNotList)
	}
}

func TestListContainsRequiresExistingValue(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	const nonexistent NodeID = 999999

	if _, _, err := lists.Contains(g, list, nonexistent); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Contains() error = %v, want %v", err, ErrNodeNotFound)
	}
}

func TestListRemoveWithoutDeletingCapsuleMiddleElement(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}
	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for b: %v", err)
	}
	c, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for c: %v", err)
	}

	capsuleA, err := lists.Append(g, list, a)
	if err != nil {
		t.Fatalf("Append(a): %v", err)
	}
	capsuleB, err := lists.Append(g, list, b)
	if err != nil {
		t.Fatalf("Append(b): %v", err)
	}
	if _, err2 := lists.Append(g, list, c); err2 != nil {
		t.Fatalf("Append(c): %v", err2)
	}

	if err2 := lists.RemoveWithoutDeletingCapsule(g, list, capsuleB); err2 != nil {
		t.Fatalf("RemoveWithoutDeletingCapsule(capsuleB): %v", err2)
	}

	got, err := lists.Elements(g, list)
	if err != nil {
		t.Fatalf("Elements(%d): %v", list, err)
	}

	want := []NodeID{a, c}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Elements(%d) = %v, want %v", list, got, want)
	}

	next, hasNext, err := lists.capsules.Next(g, capsuleA)
	if err != nil {
		t.Fatalf("Next(capsuleA): %v", err)
	}
	if !hasNext {
		t.Fatal("capsuleA lost its next link after an unrelated middle removal")
	}
	nextValue, _, err := lists.capsules.Value(g, next)
	if err != nil {
		t.Fatalf("Value(next): %v", err)
	}
	if nextValue != c {
		t.Fatalf("capsuleA's next value = %d, want %d", nextValue, c)
	}
}

func TestListRemoveWithoutDeletingCapsuleHeadUpdatesHead(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}
	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for b: %v", err)
	}

	capsuleA, err := lists.Append(g, list, a)
	if err != nil {
		t.Fatalf("Append(a): %v", err)
	}
	capsuleB, err := lists.Append(g, list, b)
	if err != nil {
		t.Fatalf("Append(b): %v", err)
	}

	if err2 := lists.RemoveWithoutDeletingCapsule(g, list, capsuleA); err2 != nil {
		t.Fatalf("RemoveWithoutDeletingCapsule(capsuleA): %v", err2)
	}

	head, hasHead, err := lists.Head(g, list)
	if err != nil {
		t.Fatalf("Head(%d): %v", list, err)
	}
	if !hasHead || head != capsuleB {
		t.Fatalf("Head(%d) = (%d,%v), want (%d,true)", list, head, hasHead, capsuleB)
	}

	if _, hasPrev, err := lists.capsules.Prev(g, capsuleB); err != nil {
		t.Fatalf("Prev(capsuleB): %v", err)
	} else if hasPrev {
		t.Fatal("new head capsuleB unexpectedly still has a prev")
	}
}

func TestListRemoveWithoutDeletingCapsuleTailUpdatesTail(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}
	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for b: %v", err)
	}

	capsuleA, err := lists.Append(g, list, a)
	if err != nil {
		t.Fatalf("Append(a): %v", err)
	}
	capsuleB, err := lists.Append(g, list, b)
	if err != nil {
		t.Fatalf("Append(b): %v", err)
	}

	if err2 := lists.RemoveWithoutDeletingCapsule(g, list, capsuleB); err2 != nil {
		t.Fatalf("RemoveWithoutDeletingCapsule(capsuleB): %v", err2)
	}

	tail, hasTail, err := lists.Tail(g, list)
	if err != nil {
		t.Fatalf("Tail(%d): %v", list, err)
	}
	if !hasTail || tail != capsuleA {
		t.Fatalf("Tail(%d) = (%d,%v), want (%d,true)", list, tail, hasTail, capsuleA)
	}

	if _, hasNext, err := lists.capsules.Next(g, capsuleA); err != nil {
		t.Fatalf("Next(capsuleA): %v", err)
	} else if hasNext {
		t.Fatal("new tail capsuleA unexpectedly still has a next")
	}
}

func TestListRemoveWithoutDeletingCapsuleSoleElementEmptiesList(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}

	capsuleA, err := lists.Append(g, list, a)
	if err != nil {
		t.Fatalf("Append(a): %v", err)
	}

	if err2 := lists.RemoveWithoutDeletingCapsule(g, list, capsuleA); err2 != nil {
		t.Fatalf("RemoveWithoutDeletingCapsule(capsuleA): %v", err2)
	}

	if _, hasHead, err2 := lists.Head(g, list); err2 != nil {
		t.Fatalf("Head(%d): %v", list, err2)
	} else if hasHead {
		t.Fatal("list unexpectedly still has a head after removing its sole element")
	}

	if _, hasTail, err3 := lists.Tail(g, list); err3 != nil {
		t.Fatalf("Tail(%d): %v", list, err3)
	} else if hasTail {
		t.Fatal("list unexpectedly still has a tail after removing its sole element")
	}

	elements, err := lists.Elements(g, list)
	if err != nil {
		t.Fatalf("Elements(%d): %v", list, err)
	}
	if len(elements) != 0 {
		t.Fatalf("Elements(%d) = %v, want empty", list, elements)
	}
}

func TestListRemoveWithoutDeletingCapsuleClearsCapsuleOwnLinks(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}
	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for b: %v", err)
	}
	c, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for c: %v", err)
	}

	if _, err2 := lists.Append(g, list, a); err2 != nil {
		t.Fatalf("Append(a): %v", err2)
	}
	capsuleB, err := lists.Append(g, list, b)
	if err != nil {
		t.Fatalf("Append(b): %v", err)
	}
	if _, err3 := lists.Append(g, list, c); err3 != nil {
		t.Fatalf("Append(c): %v", err3)
	}

	if err4 := lists.RemoveWithoutDeletingCapsule(g, list, capsuleB); err4 != nil {
		t.Fatalf("RemoveWithoutDeletingCapsule(capsuleB): %v", err4)
	}

	if _, hasPrev, err5 := lists.capsules.Prev(g, capsuleB); err5 != nil {
		t.Fatalf("Prev(capsuleB): %v", err5)
	} else if hasPrev {
		t.Fatal("removed capsuleB still has a prev link into its old list")
	}

	if _, hasNext, err6 := lists.capsules.Next(g, capsuleB); err6 != nil {
		t.Fatalf("Next(capsuleB): %v", err6)
	} else if hasNext {
		t.Fatal("removed capsuleB still has a next link into its old list")
	}

	// The capsule itself remains a valid, addressable ElementCapsule --
	// removal from a list does not delete or untag it.
	if !mustIsCapsule(t, lists.capsules, g, capsuleB) {
		t.Fatal("removed capsuleB lost its AllElementCapsules tag")
	}

	value, hasValue, err := lists.capsules.Value(g, capsuleB)
	if err != nil {
		t.Fatalf("Value(capsuleB): %v", err)
	}
	if !hasValue || value != b {
		t.Fatalf("Value(capsuleB) = (%d,%v), want (%d,true)", value, hasValue, b)
	}
}

func TestListRemoveWithoutDeletingCapsuleRequiresCapsuleInList(t *testing.T) {
	g, capsules, lists := newListTestFixture(t)

	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	// A capsule that exists but was never linked into this list.
	unrelated, err := capsules.NewCapsule(g, value)
	if err != nil {
		t.Fatalf("NewCapsule(): %v", err)
	}

	err = lists.RemoveWithoutDeletingCapsule(g, list, unrelated)
	if !errors.Is(err, ErrCapsuleNotInList) {
		t.Fatalf("RemoveWithoutDeletingCapsule() error = %v, want %v", err, ErrCapsuleNotInList)
	}
}

func TestListRemoveWithoutDeletingCapsuleRequiresListTag(t *testing.T) {
	g, capsules, lists := newListTestFixture(t)

	notAList, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	capsule, err := capsules.NewCapsule(g, value)
	if err != nil {
		t.Fatalf("NewCapsule(): %v", err)
	}

	err = lists.RemoveWithoutDeletingCapsule(g, notAList, capsule)
	if !errors.Is(err, ErrNotList) {
		t.Fatalf("RemoveWithoutDeletingCapsule() error = %v, want %v", err, ErrNotList)
	}
}

func TestListDeleteListRequiresListTag(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	notAList, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	err = lists.DeleteList(g, notAList)
	if !errors.Is(err, ErrNotList) {
		t.Fatalf("DeleteList() error = %v, want %v", err, ErrNotList)
	}
}

func TestListDeleteListFailsIfNotEmpty(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	if _, err2 := lists.Append(g, list, value); err2 != nil {
		t.Fatalf("Append(): %v", err2)
	}

	err = lists.DeleteList(g, list)
	if !errors.Is(err, ErrNodeNotEmpty) {
		t.Fatalf("DeleteList() error = %v, want %v", err, ErrNodeNotEmpty)
	}

	if !mustNodeExists(t, g, list) {
		t.Fatalf("list %d disappeared even though deletion should have failed", list)
	}

	if !mustIsList(t, lists, g, list) {
		t.Fatal("AllLists tag was not restored after a failed DeleteList()")
	}
}

func TestListDeleteListSucceedsWhenEmpty(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	if err := lists.DeleteList(g, list); err != nil {
		t.Fatalf("DeleteList(): %v", err)
	}

	if mustNodeExists(t, g, list) {
		t.Fatalf("list %d still exists after successful DeleteList()", list)
	}
}

func TestListRemoveWithoutDeletingCapsuleThenDeleteListSucceeds(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	capsule, err := lists.Append(g, list, value)
	if err != nil {
		t.Fatalf("Append(): %v", err)
	}

	if err := lists.RemoveWithoutDeletingCapsule(g, list, capsule); err != nil {
		t.Fatalf("RemoveWithoutDeletingCapsule(): %v", err)
	}

	if err := lists.DeleteList(g, list); err != nil {
		t.Fatalf("DeleteList() after RemoveWithoutDeletingCapsule(): %v", err)
	}

	if mustNodeExists(t, g, list) {
		t.Fatalf("list %d still exists after successful DeleteList()", list)
	}
}

func TestListRemoveDeletesUnreferencedCapsule(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	capsule, err := lists.Append(g, list, value)
	if err != nil {
		t.Fatalf("Append(): %v", err)
	}

	deleted, err := lists.Remove(g, list, capsule)
	if err != nil {
		t.Fatalf("Remove(): %v", err)
	}
	if !deleted {
		t.Fatal("Remove() reported deleted=false for an unreferenced capsule")
	}

	if mustNodeExists(t, g, capsule) {
		t.Fatalf("capsule %d still exists after Remove()", capsule)
	}

	if mustHasRelationship(t, g, list, capsule) {
		t.Fatal("capsule is still linked into list after Remove()")
	}

	elements, err := lists.Elements(g, list)
	if err != nil {
		t.Fatalf("Elements(list): %v", err)
	}
	if len(elements) != 0 {
		t.Fatalf("Elements(list) = %v, want empty after Remove()", elements)
	}
}

func TestListRemoveKeepsCapsuleIfStillReferencedElsewhere(t *testing.T) {
	g, capsules, lists := newListTestFixture(t)

	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	capsule, err := lists.Append(g, list, value)
	if err != nil {
		t.Fatalf("Append(): %v", err)
	}

	valueSlot, found, err := capsules.slotFor(g, capsule, capsules.valueSlots.allPointers)
	if err != nil || !found {
		t.Fatalf("slotFor(value): found=%v err=%v", found, err)
	}

	metadata, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for metadata: %v", err)
	}

	// Something unrelated referencing one of the capsule's slots -- this
	// alone must be enough to make deletion unsafe, even though Remove
	// itself does not care about it.
	if _, err2 := g.AddRelationship(metadata, valueSlot); err2 != nil {
		t.Fatalf("AddRelationship(metadata, valueSlot): %v", err2)
	}

	deleted, err := lists.Remove(g, list, capsule)
	if err != nil {
		t.Fatalf("Remove(): %v", err)
	}
	if deleted {
		t.Fatal("Remove() reported deleted=true for a capsule still referenced elsewhere")
	}

	// The removal half must still have fully succeeded.
	if mustHasRelationship(t, g, list, capsule) {
		t.Fatal("capsule is still linked into list after Remove()")
	}
	elements, err := lists.Elements(g, list)
	if err != nil {
		t.Fatalf("Elements(list): %v", err)
	}
	if len(elements) != 0 {
		t.Fatalf("Elements(list) = %v, want empty after Remove()", elements)
	}

	// But the capsule itself, being undeletable, must remain intact.
	if !mustNodeExists(t, g, capsule) {
		t.Fatal("capsule was deleted despite still being referenced elsewhere")
	}
	if !mustIsCapsule(t, capsules, g, capsule) {
		t.Fatal("capsule lost its AllElementCapsules tag despite deletion being refused")
	}
}

func TestListRemoveRequiresCapsuleInList(t *testing.T) {
	g, capsules, lists := newListTestFixture(t)

	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	unrelated, err := capsules.NewCapsule(g, value)
	if err != nil {
		t.Fatalf("NewCapsule(): %v", err)
	}

	_, err = lists.Remove(g, list, unrelated)
	if !errors.Is(err, ErrCapsuleNotInList) {
		t.Fatalf("Remove() error = %v, want %v", err, ErrCapsuleNotInList)
	}
}

func TestListRemoveRequiresListTag(t *testing.T) {
	g, capsules, lists := newListTestFixture(t)

	notAList, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	capsule, err := capsules.NewCapsule(g, value)
	if err != nil {
		t.Fatalf("NewCapsule(): %v", err)
	}

	_, err = lists.Remove(g, notAList, capsule)
	if !errors.Is(err, ErrNotList) {
		t.Fatalf("Remove() error = %v, want %v", err, ErrNotList)
	}
}

// These tests deliberately exploit freedoms that the primitive Graph permits.
// They are not tests of "normal" construction; they ask whether higher-level
// discovery depends on accidental child/parent cardinality or child ordering.

func TestAdversarialListIgnoresUnrelatedListChild(t *testing.T) {
	g, _, lists := newListTestFixture(t)
	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}
	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(a): %v", err)
	}
	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(b): %v", err)
	}
	if _, err2 := lists.Append(g, list, a); err2 != nil {
		t.Fatalf("Append(a): %v", err2)
	}
	if _, err3 := lists.Append(g, list, b); err3 != nil {
		t.Fatalf("Append(b): %v", err3)
	}

	unrelated, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(unrelated): %v", err)
	}
	if _, err4 := g.AddRelationship(list, unrelated); err4 != nil {
		t.Fatalf("AddRelationship(list, unrelated): %v", err4)
	}

	got, err := lists.Elements(g, list)
	if err != nil {
		t.Fatalf("Elements(): %v", err)
	}
	if want := []NodeID{a, b}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Elements() = %v, want %v", got, want)
	}
	if _, _, err5 := lists.Head(g, list); err5 != nil {
		t.Fatalf("Head(): %v", err5)
	}
	if _, _, err6 := lists.Tail(g, list); err6 != nil {
		t.Fatalf("Tail(): %v", err6)
	}
}

func TestAdversarialValueMayBeTheListItself(t *testing.T) {
	g, _, lists := newListTestFixture(t)
	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}
	capsule, err := lists.Append(g, list, list)
	if err != nil {
		t.Fatalf("Append(list as value): %v", err)
	}

	got, err := lists.Elements(g, list)
	if err != nil {
		t.Fatalf("Elements(): %v", err)
	}
	if want := []NodeID{list}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Elements() = %v, want %v", got, want)
	}
	value, hasValue, err := lists.capsules.Value(g, capsule)
	if err != nil {
		t.Fatalf("Value(): %v", err)
	}
	if !hasValue || value != list {
		t.Fatalf("Value() = (%d,%v), want (%d,true)", value, hasValue, list)
	}
}

func TestAdversarialSharedValueAcrossListsRemainsSeparateOccurrences(t *testing.T) {
	g, _, lists := newListTestFixture(t)
	listA, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(A): %v", err)
	}
	listB, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(B): %v", err)
	}
	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(value): %v", err)
	}

	cA, err := lists.Append(g, listA, value)
	if err != nil {
		t.Fatalf("Append(A): %v", err)
	}
	cB, err := lists.Append(g, listB, value)
	if err != nil {
		t.Fatalf("Append(B): %v", err)
	}
	if cA == cB {
		t.Fatal("two occurrences unexpectedly share one capsule")
	}

	occA, err := lists.OccurrencesOf(g, listA, value)
	if err != nil {
		t.Fatalf("OccurrencesOf(A): %v", err)
	}
	occB, err := lists.OccurrencesOf(g, listB, value)
	if err != nil {
		t.Fatalf("OccurrencesOf(B): %v", err)
	}
	if !reflect.DeepEqual(occA, []NodeID{cA}) || !reflect.DeepEqual(occB, []NodeID{cB}) {
		t.Fatalf("OccurrencesOf = A:%v B:%v, want A:%v B:%v", occA, occB, []NodeID{cA}, []NodeID{cB})
	}
}

func TestAdversarialListHeadAmbiguityFailsLoudly(t *testing.T) {
	g, _, lists := newListTestFixture(t)
	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}
	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(value): %v", err)
	}
	c1, err := lists.Append(g, list, value)
	if err != nil {
		t.Fatalf("Append(): %v", err)
	}
	c2, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(c2): %v", err)
	}
	if _, err2 := g.AddRelationship(lists.allHeads, c2); err2 != nil {
		t.Fatalf("AddRelationship(AllHeads,c2): %v", err2)
	}
	if _, err3 := g.AddRelationship(list, c2); err3 != nil {
		t.Fatalf("AddRelationship(list,c2): %v", err3)
	}
	_ = c1

	_, _, err = lists.Head(g, list)
	if !errors.Is(err, ErrAmbiguousPointerMetadata) {
		t.Fatalf("Head() error = %v, want %v", err, ErrAmbiguousPointerMetadata)
	}
}

func TestAdversarialListNextCycleIsDetectedWithoutTimeout(t *testing.T) {
	g, capsules, lists := newListTestFixture(t)
	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}
	valueA, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	valueB, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	c1, err := lists.Append(g, list, valueA)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := lists.Append(g, list, valueB)
	if err != nil {
		t.Fatal(err)
	}

	// Deliberately corrupt the Next/Prev chain into c1 <-> c2. We mutate
	// the primitive graph directly, bypassing CapsuleRegistry's normal
	// single-target replacement semantics.
	nextSlot2, found, err := capsules.slotFor(g, c2, capsules.nextSlots.allPointers)
	if err != nil || !found {
		t.Fatalf("slotFor(c2,next): found=%v err=%v", found, err)
	}
	if _, err2 := g.AddRelationship(nextSlot2, c1); err2 != nil {
		t.Fatal(err2)
	}
	prevSlot1, found, err := capsules.slotFor(g, c1, capsules.prevSlots.allPointers)
	if err != nil || !found {
		t.Fatalf("slotFor(c1,prev): found=%v err=%v", found, err)
	}
	if _, err3 := g.AddRelationship(prevSlot1, c2); err3 != nil {
		t.Fatal(err3)
	}

	got, err := lists.Elements(g, list)
	if !errors.Is(err, ErrListCycle) {
		t.Fatalf("Elements() error = %v, want %v (got values %v)", err, ErrListCycle, got)
	}
}

func TestAdversarialListPrevCycleIsDetectedViaNextChain(t *testing.T) {
	g, capsules, lists := newListTestFixture(t)
	list, err := lists.NewList(g)
	if err != nil {
		t.Fatal(err)
	}
	valueA, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	valueB, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	c1, err := lists.Append(g, list, valueA)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := lists.Append(g, list, valueB)
	if err != nil {
		t.Fatal(err)
	}

	// Corrupt only Prev: c1.Prev = c2. Next remains the valid c1 -> c2
	// chain. Elements currently follows Next, so this test intentionally
	// records whether reverse-link corruption is detected by traversal.
	prevSlot1, found, err := capsules.slotFor(g, c1, capsules.prevSlots.allPointers)
	if err != nil || !found {
		t.Fatalf("slotFor(c1,prev): found=%v err=%v", found, err)
	}
	if _, err2 := g.AddRelationship(prevSlot1, c2); err2 != nil {
		t.Fatal(err2)
	}

	got, err := lists.Elements(g, list)
	if !errors.Is(err, ErrInvalidListStructure) {
		t.Fatalf("Elements() error = %v, want %v (values %v)", err, ErrInvalidListStructure, got)
	}
}

func TestAdversarialDisconnectedListMemberIsIgnoredByElements(t *testing.T) {
	g, capsules, lists := newListTestFixture(t)
	list, err := lists.NewList(g)
	if err != nil {
		t.Fatal(err)
	}
	valueA, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	valueB, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	_, err = lists.Append(g, list, valueA)
	if err != nil {
		t.Fatal(err)
	}
	disconnected, err := capsules.NewCapsule(g, valueB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err2 := g.AddRelationship(list, disconnected); err2 != nil {
		t.Fatal(err2)
	}

	got, err := lists.Elements(g, list)
	if !errors.Is(err, ErrInvalidListStructure) {
		t.Fatalf("Elements() error = %v, want %v (values %v)", err, ErrInvalidListStructure, got)
	}
}

func TestAdversarialMissingValueTargetInvalidatesList(t *testing.T) {
	g, capsules, lists := newListTestFixture(t)
	list, err := lists.NewList(g)
	if err != nil {
		t.Fatal(err)
	}
	value, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	capsule, err := lists.Append(g, list, value)
	if err != nil {
		t.Fatal(err)
	}
	slot, found, err := capsules.slotFor(g, capsule, capsules.valueSlots.allPointers)
	if err != nil || !found {
		t.Fatal(err)
	}
	if _, err2 := g.RemoveRelationship(slot, value); err2 != nil {
		t.Fatal(err2)
	}
	got, err := lists.Elements(g, list)
	if !errors.Is(err, ErrInvalidListStructure) {
		t.Fatalf("Elements() error = %v, want %v (values %v)", err, ErrInvalidListStructure, got)
	}
}

func TestAdversarialNonEmptyListWithoutTailIsInvalid(t *testing.T) {
	g, _, lists := newListTestFixture(t)
	list, err := lists.NewList(g)
	if err != nil {
		t.Fatal(err)
	}
	value, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	capsule, err := lists.Append(g, list, value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err2 := g.RemoveRelationship(lists.allTails, capsule); err2 != nil {
		t.Fatal(err2)
	}
	_, err = lists.Elements(g, list)
	if !errors.Is(err, ErrInvalidListStructure) {
		t.Fatalf("Elements() error = %v, want %v", err, ErrInvalidListStructure)
	}
}

func TestAdversarialNonEmptyListWithoutHeadIsInvalid(t *testing.T) {
	g, _, lists := newListTestFixture(t)
	list, err := lists.NewList(g)
	if err != nil {
		t.Fatal(err)
	}
	value, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	capsule, err := lists.Append(g, list, value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err2 := g.RemoveRelationship(lists.allHeads, capsule); err2 != nil {
		t.Fatal(err2)
	}
	_, err = lists.Elements(g, list)
	if !errors.Is(err, ErrInvalidListStructure) {
		t.Fatalf("Elements() error = %v, want %v", err, ErrInvalidListStructure)
	}
}

func TestAdversarialEmptyListWithBoundaryTagIsInvalid(t *testing.T) {
	g, _, lists := newListTestFixture(t)
	list, err := lists.NewList(g)
	if err != nil {
		t.Fatal(err)
	}
	bogus, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err2 := g.AddRelationship(lists.allTails, bogus); err2 != nil {
		t.Fatal(err2)
	}
	if _, err3 := g.AddRelationship(list, bogus); err3 != nil {
		t.Fatal(err3)
	}
	_, err = lists.Elements(g, list)
	if !errors.Is(err, ErrInvalidListStructure) {
		t.Fatalf("Elements() error = %v, want %v", err, ErrInvalidListStructure)
	}
}

// TestCompositeSetLogRemoveOperationIsAtomicWhenCapsuleCannotBeDeleted
// covers RemoveOperation now being one transaction: when the capsule
// cannot be deleted (something else references its value slot), the
// operation must stay in the log completely intact instead of ending up
// unlinked from the log but still holding its descriptor.
func requireListElements(t *testing.T, lists OrderedList, graph GraphReader, list NodeID, want []NodeID) {
	t.Helper()

	got, err := lists.Elements(graph, list)
	if err != nil {
		t.Fatalf("Elements(%d): %v", list, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Elements(%d) = %v, want %v", list, got, want)
	}
}

func TestListMutatorsComposeInsideOneTransactionAndRollBackTogether(t *testing.T) {
	g, capsules, lists := newListTestFixture(t)

	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	a := newTestNode(t, g)
	b := newTestNode(t, g)
	c := newTestNode(t, g)

	capsuleA, err := lists.Append(g, list, a)
	if err != nil {
		t.Fatalf("Append(a): %v", err)
	}

	var capsuleB NodeID

	// Append, Prepend and Remove composed in one transaction commit
	// together: [a] -> [c, a, b] -> [c, b].
	err = g.Transact(func(tx Tx) error {
		var appendErr error
		capsuleB, appendErr = lists.Append(tx, list, b)
		if appendErr != nil {
			return appendErr
		}

		if _, prependErr := lists.Prepend(tx, list, c); prependErr != nil {
			return prependErr
		}

		deleted, removeErr := lists.Remove(tx, list, capsuleA)
		if removeErr != nil {
			return removeErr
		}
		if !deleted {
			t.Error("Remove() reported the capsule was not deleted")
		}

		return nil
	})
	if err != nil {
		t.Fatalf("Transact(compose) error = %v", err)
	}

	requireListElements(t, lists, g, list, []NodeID{c, b})
	if mustNodeExists(t, g, capsuleA) {
		t.Fatalf("capsule %d survived a Remove() that reported deleting it", capsuleA)
	}

	errOuter := errors.New("outer failure")

	// The same kind of composition rolls back together, including the
	// deletion of a capsule and its slots.
	err = g.Transact(func(tx Tx) error {
		deleted, removeErr := lists.Remove(tx, list, capsuleB)
		if removeErr != nil {
			return removeErr
		}
		if !deleted {
			t.Error("Remove() reported the capsule was not deleted")
		}

		if _, appendErr := lists.Append(tx, list, a); appendErr != nil {
			return appendErr
		}

		return errOuter
	})
	if !errors.Is(err, errOuter) {
		t.Fatalf("Transact(fail) error = %v, want %v", err, errOuter)
	}

	requireListElements(t, lists, g, list, []NodeID{c, b})
	if !mustIsCapsule(t, capsules, g, capsuleB) || !mustHasRelationship(t, g, list, capsuleB) {
		t.Fatal("capsuleB was not restored by the rollback of the enclosing transaction")
	}
}

func TestListAppendInsideFailedNestedTransactionLeavesListValid(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	a := newTestNode(t, g)
	b := newTestNode(t, g)
	errInner := errors.New("inner failure")

	err = g.Transact(func(tx Tx) error {
		if _, appendErr := lists.Append(tx, list, a); appendErr != nil {
			return appendErr
		}

		innerErr := tx.Transact(func(inner Tx) error {
			if _, innerAppendErr := lists.Append(inner, list, b); innerAppendErr != nil {
				return innerAppendErr
			}

			return errInner
		})
		if !errors.Is(innerErr, errInner) {
			t.Errorf("nested Transact() error = %v, want %v", innerErr, errInner)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("Transact() error = %v", err)
	}

	// Elements validates head/tail, reciprocal links and reachability, so
	// this also proves the nested rollback restored every tag and slot.
	requireListElements(t, lists, g, list, []NodeID{a})
}
