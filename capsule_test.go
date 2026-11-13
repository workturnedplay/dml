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

func TestFoundationalNamesIncludesElementCapsuleNames(t *testing.T) {
	want := []string{
		NameAllElementCapsules,
		NameAllElementCapsulePrevSlot,
		NameAllElementCapsuleValueSlot,
		NameAllElementCapsuleNextSlot,
	}

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

func newCapsuleTestFixture(t *testing.T) (testGraph, *CapsuleRegistry) {
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

	return g, capsules
}

func TestNewCapsuleRegistryRequiresExistingTags(t *testing.T) {
	var g Graph

	existing, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	const nonexistent NodeID = 999999

	if _, err := NewCapsuleRegistry(&g, nonexistent, existing, existing, existing); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("NewCapsuleRegistry() error = %v, want %v", err, ErrNodeNotFound)
	}

	if _, err := NewCapsuleRegistry(&g, existing, nonexistent, existing, existing); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("NewCapsuleRegistry() error = %v, want %v", err, ErrNodeNotFound)
	}

	if _, err := NewCapsuleRegistry(&g, existing, existing, nonexistent, existing); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("NewCapsuleRegistry() error = %v, want %v", err, ErrNodeNotFound)
	}

	if _, err := NewCapsuleRegistry(&g, existing, existing, existing, nonexistent); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("NewCapsuleRegistry() error = %v, want %v", err, ErrNodeNotFound)
	}
}

func TestNewCapsuleRequiresExistingValue(t *testing.T) {
	g, capsules := newCapsuleTestFixture(t)

	const nonexistent NodeID = 999999

	if _, err := capsules.NewCapsule(g, nonexistent); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("NewCapsule() error = %v, want %v", err, ErrNodeNotFound)
	}
}

func TestNewCapsuleTagsAndSetsValue(t *testing.T) {
	g, capsules := newCapsuleTestFixture(t)

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	capsule, err := capsules.NewCapsule(g, value)
	if err != nil {
		t.Fatalf("NewCapsule(): %v", err)
	}

	if !mustNodeExists(t, g, capsule) {
		t.Fatalf("NewCapsule() returned NodeID %d that does not exist", capsule)
	}

	if !mustIsCapsule(t, capsules, g, capsule) {
		t.Fatalf("NewCapsule() did not tag %d as an ElementCapsule", capsule)
	}

	got, hasValue, err := capsules.Value(g, capsule)
	if err != nil {
		t.Fatalf("Value(%d): %v", capsule, err)
	}
	if !hasValue || got != value {
		t.Fatalf("Value(%d) = (%d,%v), want (%d,true)", capsule, got, hasValue, value)
	}
}

func TestNewCapsuleStartsWithNoPrevOrNext(t *testing.T) {
	g, capsules := newCapsuleTestFixture(t)

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	capsule, err := capsules.NewCapsule(g, value)
	if err != nil {
		t.Fatalf("NewCapsule(): %v", err)
	}

	if _, hasPrev, err := capsules.Prev(g, capsule); err != nil {
		t.Fatalf("Prev(%d): %v", capsule, err)
	} else if hasPrev {
		t.Fatalf("freshly created capsule %d unexpectedly has a prev", capsule)
	}

	if _, hasNext, err := capsules.Next(g, capsule); err != nil {
		t.Fatalf("Next(%d): %v", capsule, err)
	} else if hasNext {
		t.Fatalf("freshly created capsule %d unexpectedly has a next", capsule)
	}
}

func TestCapsuleSetPrevAndNextLinkCapsules(t *testing.T) {
	g, capsules := newCapsuleTestFixture(t)

	v1, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for v1: %v", err)
	}

	v2, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for v2: %v", err)
	}

	c1, err := capsules.NewCapsule(g, v1)
	if err != nil {
		t.Fatalf("NewCapsule(v1): %v", err)
	}

	c2, err := capsules.NewCapsule(g, v2)
	if err != nil {
		t.Fatalf("NewCapsule(v2): %v", err)
	}

	if err2 := capsules.SetNext(g, c1, c2); err2 != nil {
		t.Fatalf("SetNext(c1, c2): %v", err2)
	}

	if err3 := capsules.SetPrev(g, c2, c1); err3 != nil {
		t.Fatalf("SetPrev(c2, c1): %v", err3)
	}

	next, hasNext, err := capsules.Next(g, c1)
	if err != nil {
		t.Fatalf("Next(c1): %v", err)
	}
	if !hasNext || next != c2 {
		t.Fatalf("Next(c1) = (%d,%v), want (%d,true)", next, hasNext, c2)
	}

	prev, hasPrev, err := capsules.Prev(g, c2)
	if err != nil {
		t.Fatalf("Prev(c2): %v", err)
	}
	if !hasPrev || prev != c1 {
		t.Fatalf("Prev(c2) = (%d,%v), want (%d,true)", prev, hasPrev, c1)
	}
}

func TestCapsuleRemovePrevAndNext(t *testing.T) {
	g, capsules := newCapsuleTestFixture(t)

	v1, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for v1: %v", err)
	}

	v2, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for v2: %v", err)
	}

	c1, err := capsules.NewCapsule(g, v1)
	if err != nil {
		t.Fatalf("NewCapsule(v1): %v", err)
	}

	c2, err := capsules.NewCapsule(g, v2)
	if err != nil {
		t.Fatalf("NewCapsule(v2): %v", err)
	}

	if err2 := capsules.SetNext(g, c1, c2); err2 != nil {
		t.Fatalf("SetNext(c1, c2): %v", err2)
	}

	removed, err := capsules.RemoveNext(g, c1)
	if err != nil {
		t.Fatalf("RemoveNext(c1): %v", err)
	}
	if !removed {
		t.Fatal("RemoveNext() reported that nothing was removed")
	}

	if _, hasNext, err := capsules.Next(g, c1); err != nil {
		t.Fatalf("Next(c1): %v", err)
	} else if hasNext {
		t.Fatal("c1 still has a next after RemoveNext()")
	}
}

func TestCapsuleOperationsRequireCapsuleTag(t *testing.T) {
	g, capsules := newCapsuleTestFixture(t)

	id, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for x: %v", err)
	}

	if _, _, err := capsules.Value(g, id); !errors.Is(err, ErrNotCapsule) {
		t.Fatalf("Value() error = %v, want %v", err, ErrNotCapsule)
	}

	if err := capsules.SetValue(g, id, x); !errors.Is(err, ErrNotCapsule) {
		t.Fatalf("SetValue() error = %v, want %v", err, ErrNotCapsule)
	}

	if _, _, err := capsules.Prev(g, id); !errors.Is(err, ErrNotCapsule) {
		t.Fatalf("Prev() error = %v, want %v", err, ErrNotCapsule)
	}

	if _, _, err := capsules.Next(g, id); !errors.Is(err, ErrNotCapsule) {
		t.Fatalf("Next() error = %v, want %v", err, ErrNotCapsule)
	}
}

func TestCapsuleRegistryDeleteCapsuleDeletesCleanCapsule(t *testing.T) {
	g, capsules := newCapsuleTestFixture(t)

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	capsule, err := capsules.NewCapsule(g, value)
	if err != nil {
		t.Fatalf("NewCapsule(): %v", err)
	}

	prevSlot, found, err := capsules.slotFor(g, capsule, capsules.prevSlots.allPointers)
	if err != nil || !found {
		t.Fatalf("slotFor(prev): found=%v err=%v", found, err)
	}
	valueSlot, found, err := capsules.slotFor(g, capsule, capsules.valueSlots.allPointers)
	if err != nil || !found {
		t.Fatalf("slotFor(value): found=%v err=%v", found, err)
	}
	nextSlot, found, err := capsules.slotFor(g, capsule, capsules.nextSlots.allPointers)
	if err != nil || !found {
		t.Fatalf("slotFor(next): found=%v err=%v", found, err)
	}

	if err := capsules.DeleteCapsule(g, capsule); err != nil {
		t.Fatalf("DeleteCapsule(): %v", err)
	}

	if mustNodeExists(t, g, capsule) {
		t.Fatalf("capsule %d still exists after DeleteCapsule()", capsule)
	}
	if mustNodeExists(t, g, prevSlot) {
		t.Fatalf("prevSlot %d still exists after DeleteCapsule()", prevSlot)
	}
	if mustNodeExists(t, g, valueSlot) {
		t.Fatalf("valueSlot %d still exists after DeleteCapsule()", valueSlot)
	}
	if mustNodeExists(t, g, nextSlot) {
		t.Fatalf("nextSlot %d still exists after DeleteCapsule()", nextSlot)
	}

	if !mustNodeExists(t, g, value) {
		t.Fatal("DeleteCapsule() incorrectly deleted the capsule's value")
	}
}

func TestCapsuleRegistryDeleteCapsuleFailsIfStillListed(t *testing.T) {
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

	err = capsules.DeleteCapsule(g, capsule)
	if !errors.Is(err, ErrCapsuleNotEmpty) {
		t.Fatalf("DeleteCapsule() error = %v, want %v", err, ErrCapsuleNotEmpty)
	}

	if !mustNodeExists(t, g, capsule) {
		t.Fatal("capsule disappeared despite a failed DeleteCapsule()")
	}
	if !mustIsCapsule(t, capsules, g, capsule) {
		t.Fatal("capsule lost its AllElementCapsules tag despite a failed DeleteCapsule()")
	}
	if !mustHasRelationship(t, g, list, capsule) {
		t.Fatal("capsule lost its list membership despite a failed DeleteCapsule()")
	}

	got, hasValue, err := capsules.Value(g, capsule)
	if err != nil {
		t.Fatalf("Value(capsule): %v", err)
	}
	if !hasValue || got != value {
		t.Fatalf("Value(capsule) = (%d,%v), want (%d,true) -- unaffected by a failed DeleteCapsule()", got, hasValue, value)
	}
}

func TestCapsuleRegistryDeleteCapsuleFailsIfPrevOrNextSet(t *testing.T) {
	g, capsules := newCapsuleTestFixture(t)

	v1, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for v1: %v", err)
	}
	v2, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for v2: %v", err)
	}

	c1, err := capsules.NewCapsule(g, v1)
	if err != nil {
		t.Fatalf("NewCapsule(v1): %v", err)
	}
	c2, err := capsules.NewCapsule(g, v2)
	if err != nil {
		t.Fatalf("NewCapsule(v2): %v", err)
	}

	if err2 := capsules.SetNext(g, c1, c2); err2 != nil {
		t.Fatalf("SetNext(c1, c2): %v", err2)
	}

	err = capsules.DeleteCapsule(g, c1)
	if !errors.Is(err, ErrCapsuleNotEmpty) {
		t.Fatalf("DeleteCapsule(c1) error = %v, want %v", err, ErrCapsuleNotEmpty)
	}

	if !mustNodeExists(t, g, c1) {
		t.Fatal("c1 disappeared despite a failed DeleteCapsule()")
	}

	next, hasNext, err := capsules.Next(g, c1)
	if err != nil {
		t.Fatalf("Next(c1): %v", err)
	}
	if !hasNext || next != c2 {
		t.Fatalf("Next(c1) = (%d,%v), want (%d,true) -- unaffected by a failed DeleteCapsule()", next, hasNext, c2)
	}
}

func TestCapsuleRegistryDeleteCapsuleFailsIfSlotHasExtraParent(t *testing.T) {
	g, capsules := newCapsuleTestFixture(t)

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	capsule, err := capsules.NewCapsule(g, value)
	if err != nil {
		t.Fatalf("NewCapsule(): %v", err)
	}

	valueSlot, found, err := capsules.slotFor(g, capsule, capsules.valueSlots.allPointers)
	if err != nil || !found {
		t.Fatalf("slotFor(value): found=%v err=%v", found, err)
	}

	metadata, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for metadata: %v", err)
	}

	// An unrelated node referencing the value slot for its own reasons
	// -- ordinary permitted graph structure (a node may have any number
	// of parents, theorystate.md section 2.8) that
	// DeleteCapsule must not silently delete out from under.
	if _, err2 := g.AddRelationship(metadata, valueSlot); err2 != nil {
		t.Fatalf("AddRelationship(metadata, valueSlot): %v", err2)
	}

	err = capsules.DeleteCapsule(g, capsule)
	if !errors.Is(err, ErrCapsuleNotEmpty) {
		t.Fatalf("DeleteCapsule() error = %v, want %v", err, ErrCapsuleNotEmpty)
	}

	if !mustNodeExists(t, g, capsule) || !mustNodeExists(t, g, valueSlot) {
		t.Fatal("capsule or valueSlot disappeared despite a failed DeleteCapsule()")
	}
	if !mustHasRelationship(t, g, metadata, valueSlot) {
		t.Fatal("unrelated metadata relationship was disturbed by a failed DeleteCapsule()")
	}
}

func TestCapsuleRegistryDeleteCapsuleRequiresCapsuleTag(t *testing.T) {
	g, capsules := newCapsuleTestFixture(t)

	id, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	err = capsules.DeleteCapsule(g, id)
	if !errors.Is(err, ErrNotCapsule) {
		t.Fatalf("DeleteCapsule() error = %v, want %v", err, ErrNotCapsule)
	}
}

func TestCapsuleRegistryDeleteCapsuleRequiresExistingNode(t *testing.T) {
	g, capsules := newCapsuleTestFixture(t)

	const nonexistent NodeID = 999999

	err := capsules.DeleteCapsule(g, nonexistent)
	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("DeleteCapsule() error = %v, want %v", err, ErrNodeNotFound)
	}
}

// TestCapsuleRoleSlotsAreNotTaggedWithGenericAllPointers pins down a
// point raised in review: each of a capsule's three role slots is
// tagged only with its own specific role tag (AllElementCapsulePrevSlot
// / AllElementCapsuleValueSlot / AllElementCapsuleNextSlot), never with
// the separate, generic AllPointers tag, even though the underlying
// PointerRegistry type is literally named for that concept. If
// CapsuleRegistry were ever changed to construct its three slot
// registries against the shared generic AllPointers tag instead of
// their own distinct tags, slotFor's per-role, tag-based discovery
// (findUniqueTaggedChild) would no longer be able to tell a capsule's
// prev slot from its value slot from its next slot -- this test exists
// to catch exactly that regression.
func TestCapsuleRoleSlotsAreNotTaggedWithGenericAllPointers(t *testing.T) {
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

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	capsule, err := capsules.NewCapsule(&g, value)
	if err != nil {
		t.Fatalf("NewCapsule(): %v", err)
	}

	allPointers := ids[NameAllPointers]

	for _, tc := range []struct {
		name string
		tag  NodeID
	}{
		{"prev", capsules.prevSlots.allPointers},
		{"value", capsules.valueSlots.allPointers},
		{"next", capsules.nextSlots.allPointers},
	} {
		slot, found, err := capsules.slotFor(&g, capsule, tc.tag)
		if err != nil || !found {
			t.Fatalf("slotFor(%s): found=%v err=%v", tc.name, found, err)
		}

		if mustHasRelationship(t, &g, allPointers, slot) {
			t.Fatalf("%s slot %d is tagged with the generic AllPointers tag; it should only carry its own role tag", tc.name, slot)
		}

		incoming, err := g.FindIncoming(slot)
		if err != nil {
			t.Fatalf("FindIncoming(%s slot): %v", tc.name, err)
		}
		if len(incoming) != 2 {
			t.Fatalf("%s slot %d has %d incoming relationships, want exactly 2 (its owning capsule and its own role tag)", tc.name, slot, len(incoming))
		}
	}
}

func TestCapsulesWithValueFindsAllOccurrences(t *testing.T) {
	g, capsules := newCapsuleTestFixture(t)

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	other, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for other: %v", err)
	}

	c1, err := capsules.NewCapsule(g, value)
	if err != nil {
		t.Fatalf("NewCapsule(value) for c1: %v", err)
	}

	c2, err := capsules.NewCapsule(g, value)
	if err != nil {
		t.Fatalf("NewCapsule(value) for c2: %v", err)
	}

	// A capsule holding an unrelated value must not show up.
	if _, err2 := capsules.NewCapsule(g, other); err2 != nil {
		t.Fatalf("NewCapsule(other): %v", err2)
	}

	got, err := capsules.CapsulesWithValue(g, value)
	if err != nil {
		t.Fatalf("CapsulesWithValue(value): %v", err)
	}

	want := sortedNodeIDs([]NodeID{c1, c2})
	got = sortedNodeIDs(got)

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CapsulesWithValue(value) = %v, want %v", got, want)
	}
}

func TestCapsulesWithValueIgnoresUnrelatedEdges(t *testing.T) {
	g, capsules := newCapsuleTestFixture(t)

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	capsule, err := capsules.NewCapsule(g, value)
	if err != nil {
		t.Fatalf("NewCapsule(value): %v", err)
	}

	unrelated, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for unrelated: %v", err)
	}

	// A plain, non-value-slot relationship pointing at value from
	// elsewhere in the graph (e.g. some other structure's target).
	if _, err2 := g.AddRelationship(unrelated, value); err2 != nil {
		t.Fatalf("AddRelationship(unrelated, value): %v", err2)
	}

	got, err := capsules.CapsulesWithValue(g, value)
	if err != nil {
		t.Fatalf("CapsulesWithValue(value): %v", err)
	}

	want := []NodeID{capsule}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CapsulesWithValue(value) = %v, want %v", got, want)
	}
}

// TestCapsulesWithValueIgnoresUnrelatedParentsOfSlot is the regression
// test for the bug caught in review: a role-slot node acquiring an
// unrelated extra parent (e.g. some future metadata structure
// referencing the slot itself, for its own reasons) must not be confused
// with a second owning capsule, and must not make CapsulesWithValue
// fail. Only a parent that is itself tagged AllElementCapsules counts as
// an owner.
func TestCapsulesWithValueIgnoresUnrelatedParentsOfSlot(t *testing.T) {
	g, capsules := newCapsuleTestFixture(t)

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	capsule, err := capsules.NewCapsule(g, value)
	if err != nil {
		t.Fatalf("NewCapsule(value): %v", err)
	}

	slot, found, err := capsules.slotFor(g, capsule, capsules.valueSlots.allPointers)
	if err != nil {
		t.Fatalf("slotFor(capsule, valueSlot tag): %v", err)
	}
	if !found {
		t.Fatalf("capsule %d unexpectedly has no value slot", capsule)
	}

	metadata, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for metadata: %v", err)
	}

	// An unrelated node pointing at the slot itself, not tagged as a
	// capsule -- ordinary permitted graph structure that ownership
	// discovery must ignore rather than error on or mistake for the
	// owner.
	if _, err2 := g.AddRelationship(metadata, slot); err2 != nil {
		t.Fatalf("AddRelationship(metadata, slot): %v", err2)
	}

	got, err := capsules.CapsulesWithValue(g, value)
	if err != nil {
		t.Fatalf("CapsulesWithValue(value): %v", err)
	}

	want := []NodeID{capsule}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CapsulesWithValue(value) = %v, want %v -- an unrelated non-capsule parent of the value slot must not affect ownership discovery", got, want)
	}
}

// TestCapsulesWithValueDetectsAmbiguousCapsuleOwnership covers the
// genuine invariant violation that TestCapsulesWithValueIgnoresUnrelatedParentsOfSlot
// is deliberately distinguished from: two distinct capsule-tagged nodes
// both wired to the same value slot, which can only happen through an
// out-of-band mutation, since buildCapsuleTx wires each slot to exactly
// one owning capsule at creation.
func TestCapsulesWithValueDetectsAmbiguousCapsuleOwnership(t *testing.T) {
	g, capsules := newCapsuleTestFixture(t)

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	capsule, err := capsules.NewCapsule(g, value)
	if err != nil {
		t.Fatalf("NewCapsule(value): %v", err)
	}

	slot, found, err := capsules.slotFor(g, capsule, capsules.valueSlots.allPointers)
	if err != nil {
		t.Fatalf("slotFor(capsule, valueSlot tag): %v", err)
	}
	if !found {
		t.Fatalf("capsule %d unexpectedly has no value slot", capsule)
	}

	otherValue, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for otherValue: %v", err)
	}

	otherCapsule, err := capsules.NewCapsule(g, otherValue)
	if err != nil {
		t.Fatalf("NewCapsule(otherValue): %v", err)
	}

	// Bypass CapsuleRegistry entirely, simulating an out-of-band mutation
	// that wires a second, distinct capsule to the same value slot.
	if _, err2 := g.AddRelationship(otherCapsule, slot); err2 != nil {
		t.Fatalf("AddRelationship(otherCapsule, slot): %v", err2)
	}

	_, err = capsules.CapsulesWithValue(g, value)
	if !errors.Is(err, ErrAmbiguousPointerMetadata) {
		t.Fatalf("CapsulesWithValue(value) error = %v, want %v", err, ErrAmbiguousPointerMetadata)
	}
}

// TestCapsulesWithValuePagesAcrossMultipleFetches shrinks
// capsulesWithValuePageSize temporarily so this can exercise
// CapsulesWithValue's own multi-page loop (theorystate.md section 105)
// without needing thousands of capsules. This runs against a plain
// in-memory *Graph, which has no native incomingPager of its own -- what
// is under test here is CapsulesWithValue's own looping and cursor
// bookkeeping, not any particular backend's paging mechanism (that is
// covered separately by TestBoltGraphFindIncomingAfterMatchesFindIncoming).
func TestCapsulesWithValuePagesAcrossMultipleFetches(t *testing.T) {
	original := capsulesWithValuePageSize
	capsulesWithValuePageSize = 3
	t.Cleanup(func() { capsulesWithValuePageSize = original })

	g, capsules := newCapsuleTestFixture(t)

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	const occurrences = 7

	want := make([]NodeID, 0, occurrences)

	for range occurrences {
		capsule, err2 := capsules.NewCapsule(g, value)
		if err2 != nil {
			t.Fatalf("NewCapsule(): %v", err2)
		}
		want = append(want, capsule)
	}

	got, err := capsules.CapsulesWithValue(g, value)
	if err != nil {
		t.Fatalf("CapsulesWithValue(): %v", err)
	}

	if !reflect.DeepEqual(sortedNodeIDs(got), sortedNodeIDs(want)) {
		t.Fatalf("CapsulesWithValue() = %v, want %v (in some order)", got, want)
	}
}

func TestCapsuleLinkAndDeleteComposeAndRollBackWithEnclosingTransaction(t *testing.T) {
	g, capsules := newCapsuleTestFixture(t)

	v1 := newTestNode(t, g)
	v2 := newTestNode(t, g)

	c1, err := capsules.NewCapsule(g, v1)
	if err != nil {
		t.Fatalf("NewCapsule(v1): %v", err)
	}

	c2, err := capsules.NewCapsule(g, v2)
	if err != nil {
		t.Fatalf("NewCapsule(v2): %v", err)
	}

	errOuter := errors.New("outer failure")

	err = g.Transact(func(tx Tx) error {
		if nextErr := capsules.SetNext(tx, c1, c2); nextErr != nil {
			return nextErr
		}
		if prevErr := capsules.SetPrev(tx, c2, c1); prevErr != nil {
			return prevErr
		}

		return errOuter
	})
	if !errors.Is(err, errOuter) {
		t.Fatalf("Transact(link, fail) error = %v, want %v", err, errOuter)
	}

	if _, hasNext, nextErr := capsules.Next(g, c1); nextErr != nil {
		t.Fatalf("Next(c1): %v", nextErr)
	} else if hasNext {
		t.Fatal("c1 kept a next link from a rolled-back transaction")
	}
	if _, hasPrev, prevErr := capsules.Prev(g, c2); prevErr != nil {
		t.Fatalf("Prev(c2): %v", prevErr)
	} else if hasPrev {
		t.Fatal("c2 kept a prev link from a rolled-back transaction")
	}

	err = g.Transact(func(tx Tx) error {
		if deleteErr := capsules.DeleteCapsule(tx, c2); deleteErr != nil {
			return deleteErr
		}

		return errOuter
	})
	if !errors.Is(err, errOuter) {
		t.Fatalf("Transact(delete, fail) error = %v, want %v", err, errOuter)
	}

	if !mustNodeExists(t, g, c2) || !mustIsCapsule(t, capsules, g, c2) {
		t.Fatal("c2 was not restored after its deleting transaction rolled back")
	}

	value, hasValue, valueErr := capsules.Value(g, c2)
	if valueErr != nil {
		t.Fatalf("Value(c2): %v", valueErr)
	}
	if !hasValue || value != v2 {
		t.Fatalf("Value(c2) = (%d,%v), want (%d,true)", value, hasValue, v2)
	}
}

func TestAdversarialCapsuleIgnoresUnrelatedChild(t *testing.T) {
	g, capsules := newCapsuleTestFixture(t)
	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(value): %v", err)
	}
	capsule, err := capsules.NewCapsule(g, value)
	if err != nil {
		t.Fatalf("NewCapsule(): %v", err)
	}

	unrelated, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(unrelated): %v", err)
	}
	if _, err2 := g.AddRelationship(capsule, unrelated); err2 != nil {
		t.Fatalf("AddRelationship(capsule, unrelated): %v", err2)
	}

	got, hasValue, err := capsules.Value(g, capsule)
	if err != nil {
		t.Fatalf("Value(): %v", err)
	}
	if !hasValue || got != value {
		t.Fatalf("Value() = (%d,%v), want (%d,true)", got, hasValue, value)
	}
}

func TestAdversarialDetachedCapsuleDoesNotBecomeListMember(t *testing.T) {
	g, capsules, lists := newListTestFixture(t)
	list, err := lists.NewList(g)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}
	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(value): %v", err)
	}
	capsule, err := capsules.NewCapsule(g, value)
	if err != nil {
		t.Fatalf("NewCapsule(): %v", err)
	}

	if _, _, err := lists.Contains(g, list, value); err != nil {
		t.Fatalf("Contains(): %v", err)
	}
	if err := lists.RemoveWithoutDeletingCapsule(g, list, capsule); !errors.Is(err, ErrCapsuleNotInList) {
		t.Fatalf("RemoveWithoutDeletingCapsule() error = %v, want %v", err, ErrCapsuleNotInList)
	}
	if !mustIsCapsule(t, capsules, g, capsule) || !mustNodeExists(t, g, capsule) {
		t.Fatal("detached capsule was unexpectedly deleted or untagged")
	}
}

func TestAdversarialCapsuleValueMissingIsDetected(t *testing.T) {
	g, capsules := newCapsuleTestFixture(t)
	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(value): %v", err)
	}
	capsule, err := capsules.NewCapsule(g, value)
	if err != nil {
		t.Fatalf("NewCapsule(): %v", err)
	}
	slot, found, err := capsules.slotFor(g, capsule, capsules.valueSlots.allPointers)
	if err != nil || !found {
		t.Fatalf("slotFor(value): found=%v err=%v", found, err)
	}
	if _, err2 := g.RemoveRelationship(slot, value); err2 != nil {
		t.Fatalf("RemoveRelationship(slot,value): %v", err2)
	}

	_, hasValue, err := capsules.Value(g, capsule)
	if err != nil {
		t.Fatalf("Value(): %v", err)
	}
	if hasValue {
		t.Fatal("Value() reported a value after the value edge was removed")
	}
}

func TestAdversarialListChainCanContainCapsuleFromAnotherList(t *testing.T) {
	g, capsules, lists := newListTestFixture(t)
	listA, err := lists.NewList(g)
	if err != nil {
		t.Fatal(err)
	}
	listB, err := lists.NewList(g)
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
	c1, err := lists.Append(g, listA, valueA)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := lists.Append(g, listB, valueB)
	if err != nil {
		t.Fatal(err)
	}

	// Make listA's head chain point to listB's capsule. This is an explicit
	// out-of-band topology violation.
	nextSlot1, found, err := capsules.slotFor(g, c1, capsules.nextSlots.allPointers)
	if err != nil || !found {
		t.Fatalf("slotFor(c1,next): found=%v err=%v", found, err)
	}
	if _, err2 := g.AddRelationship(nextSlot1, c2); err2 != nil {
		t.Fatal(err2)
	}

	got, err := lists.Elements(g, listA)
	if !errors.Is(err, ErrInvalidListStructure) {
		t.Fatalf("Elements() error = %v, want %v (values %v)", err, ErrInvalidListStructure, got)
	}
}

func TestAdversarialDuplicateValueSlotFailsLoudly(t *testing.T) {
	g, capsules := newCapsuleTestFixture(t)
	value, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	capsule, err := capsules.NewCapsule(g, value)
	if err != nil {
		t.Fatal(err)
	}
	otherSlot, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err2 := g.AddRelationship(capsule, otherSlot); err2 != nil {
		t.Fatal(err2)
	}
	if _, err3 := g.AddRelationship(capsules.valueSlots.allPointers, otherSlot); err3 != nil {
		t.Fatal(err3)
	}

	_, _, err = capsules.Value(g, capsule)
	if !errors.Is(err, ErrAmbiguousPointerMetadata) {
		t.Fatalf("Value() error = %v, want %v", err, ErrAmbiguousPointerMetadata)
	}
}

func TestAdversarialSharedRoleSlotFailsLoudly(t *testing.T) {
	g, capsules := newCapsuleTestFixture(t)
	valueA, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	valueB, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	c1, err := capsules.NewCapsule(g, valueA)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := capsules.NewCapsule(g, valueB)
	if err != nil {
		t.Fatal(err)
	}
	slot, found, err := capsules.slotFor(g, c1, capsules.valueSlots.allPointers)
	if err != nil || !found {
		t.Fatalf("slotFor(c1,value): found=%v err=%v", found, err)
	}
	if _, err2 := g.AddRelationship(c2, slot); err2 != nil {
		t.Fatal(err2)
	}

	_, _, err = capsules.Value(g, c2)
	if !errors.Is(err, ErrAmbiguousPointerMetadata) {
		t.Fatalf("Value(c2) error = %v, want %v", err, ErrAmbiguousPointerMetadata)
	}

	_, err = capsules.CapsulesWithValue(g, valueA)
	if !errors.Is(err, ErrAmbiguousPointerMetadata) {
		t.Fatalf("CapsulesWithValue(valueA) error = %v, want %v", err, ErrAmbiguousPointerMetadata)
	}
}

func TestAdversarialMissingRoleTagMakesCapsuleUndiscoverable(t *testing.T) {
	g, capsules := newCapsuleTestFixture(t)
	value, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	capsule, err := capsules.NewCapsule(g, value)
	if err != nil {
		t.Fatal(err)
	}
	slot, found, err := capsules.slotFor(g, capsule, capsules.valueSlots.allPointers)
	if err != nil || !found {
		t.Fatalf("slotFor(): found=%v err=%v", found, err)
	}
	if _, err2 := g.RemoveRelationship(capsules.valueSlots.allPointers, slot); err2 != nil {
		t.Fatal(err2)
	}

	_, _, err = capsules.Value(g, capsule)
	if !errors.Is(err, ErrNotCapsule) {
		t.Fatalf("Value() error = %v, want %v", err, ErrNotCapsule)
	}
}

func TestAdversarialWrongTaggedChildFailsLoudly(t *testing.T) {
	g, capsules := newCapsuleTestFixture(t)
	value, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	capsule, err := capsules.NewCapsule(g, value)
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err2 := g.AddRelationship(capsule, wrong); err2 != nil {
		t.Fatal(err2)
	}
	if _, err3 := g.AddRelationship(capsules.valueSlots.allPointers, wrong); err3 != nil {
		t.Fatal(err3)
	}

	_, _, err = capsules.Value(g, capsule)
	if !errors.Is(err, ErrAmbiguousPointerMetadata) {
		t.Fatalf("Value() error = %v, want %v", err, ErrAmbiguousPointerMetadata)
	}
}

func TestAdversarialHeadPointingAtNonCapsuleFailsWhenTraversed(t *testing.T) {
	g, _, lists := newListTestFixture(t)
	list, err := lists.NewList(g)
	if err != nil {
		t.Fatal(err)
	}
	bogus, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err2 := g.AddRelationship(list, bogus); err2 != nil {
		t.Fatal(err2)
	}
	if _, err3 := g.AddRelationship(lists.allHeads, bogus); err3 != nil {
		t.Fatal(err3)
	}

	_, err = lists.Elements(g, list)
	if !errors.Is(err, ErrInvalidListStructure) {
		t.Fatalf("Elements() error = %v, want %v", err, ErrInvalidListStructure)
	}
}

func TestAdversarialDuplicatePrevSlotFailsLoudly(t *testing.T) {
	g, capsules := newCapsuleTestFixture(t)
	value, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	capsule, err := capsules.NewCapsule(g, value)
	if err != nil {
		t.Fatal(err)
	}
	slot, found, err := capsules.slotFor(g, capsule, capsules.prevSlots.allPointers)
	if err != nil || !found {
		t.Fatalf("slotFor(prev): found=%v err=%v", found, err)
	}
	_ = slot
	other, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err2 := g.AddRelationship(capsule, other); err2 != nil {
		t.Fatal(err2)
	}
	if _, err3 := g.AddRelationship(capsules.prevSlots.allPointers, other); err3 != nil {
		t.Fatal(err3)
	}
	_, _, err = capsules.Prev(g, capsule)
	if !errors.Is(err, ErrAmbiguousPointerMetadata) {
		t.Fatalf("Prev() error = %v, want %v", err, ErrAmbiguousPointerMetadata)
	}
}

func TestAdversarialDuplicateNextSlotFailsLoudly(t *testing.T) {
	g, capsules := newCapsuleTestFixture(t)
	value, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	capsule, err := capsules.NewCapsule(g, value)
	if err != nil {
		t.Fatal(err)
	}
	other, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err2 := g.AddRelationship(capsule, other); err2 != nil {
		t.Fatal(err2)
	}
	if _, err3 := g.AddRelationship(capsules.nextSlots.allPointers, other); err3 != nil {
		t.Fatal(err3)
	}
	_, _, err = capsules.Next(g, capsule)
	if !errors.Is(err, ErrAmbiguousPointerMetadata) {
		t.Fatalf("Next() error = %v, want %v", err, ErrAmbiguousPointerMetadata)
	}
}

func TestAdversarialRoleSlotWithExtraChildFailsLoudly(t *testing.T) {
	g, capsules := newCapsuleTestFixture(t)
	value, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	capsule, err := capsules.NewCapsule(g, value)
	if err != nil {
		t.Fatal(err)
	}
	slot, found, err := capsules.slotFor(g, capsule, capsules.nextSlots.allPointers)
	if err != nil || !found {
		t.Fatalf("slotFor(next): found=%v err=%v", found, err)
	}
	target, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err2 := g.AddRelationship(slot, target); err2 != nil {
		t.Fatal(err2)
	}
	extra, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err3 := g.AddRelationship(slot, extra); err3 != nil {
		t.Fatal(err3)
	}
	_, _, err = capsules.Next(g, capsule)
	if !errors.Is(err, ErrTooManyPointerTargets) {
		t.Fatalf("Next() error = %v, want %v", err, ErrTooManyPointerTargets)
	}
}

// TestAdversarialCapsuleMultipleRoleViolationsEachDetectedIndependently
// combines three different out-of-band single-role violations (already
// individually covered by TestAdversarialRoleSlotWithExtraChildFailsLoudly,
// TestAdversarialSharedRoleSlotFailsLoudly, and
// TestAdversarialMissingRoleTagMakesCapsuleUndiscoverable) onto the same
// capsule at once, confirming each role's own accessor still reports its
// own specific violation independently. This documents a real,
// deliberately-not-yet-closed gap: there is currently no single "is this
// capsule well-formed" check -- only three separate per-role queries,
// each of which must be called individually to discover a problem with
// that particular role.
func TestAdversarialCapsuleMultipleRoleViolationsEachDetectedIndependently(t *testing.T) {
	g, capsules := newCapsuleTestFixture(t)

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	capsule, err := capsules.NewCapsule(g, value)
	if err != nil {
		t.Fatalf("NewCapsule(): %v", err)
	}

	// Corrupt the prev slot: give it a second target directly through the
	// primitive Graph, violating the underlying PointerRegistry's "at
	// most one target" invariant.
	prevSlot, found, err := capsules.slotFor(g, capsule, capsules.prevSlots.allPointers)
	if err != nil || !found {
		t.Fatalf("slotFor(prev): found=%v err=%v", found, err)
	}
	prevTarget, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for prevTarget: %v", err)
	}
	prevExtra, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for prevExtra: %v", err)
	}
	if _, err2 := g.AddRelationship(prevSlot, prevTarget); err2 != nil {
		t.Fatalf("AddRelationship(prevSlot, prevTarget): %v", err2)
	}
	if _, err3 := g.AddRelationship(prevSlot, prevExtra); err3 != nil {
		t.Fatalf("AddRelationship(prevSlot, prevExtra): %v", err3)
	}

	// Corrupt the value slot: wire a second, distinct capsule to the same
	// value slot, making its owning capsule ambiguous.
	valueSlot, found, err := capsules.slotFor(g, capsule, capsules.valueSlots.allPointers)
	if err != nil || !found {
		t.Fatalf("slotFor(value): found=%v err=%v", found, err)
	}
	otherValue, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for otherValue: %v", err)
	}
	otherCapsule, err := capsules.NewCapsule(g, otherValue)
	if err != nil {
		t.Fatalf("NewCapsule(otherValue): %v", err)
	}
	if _, err4 := g.AddRelationship(otherCapsule, valueSlot); err4 != nil {
		t.Fatalf("AddRelationship(otherCapsule, valueSlot): %v", err4)
	}

	// Corrupt the next slot: remove its own role tag entirely, making it
	// undiscoverable as a role slot at all.
	nextSlot, found, err := capsules.slotFor(g, capsule, capsules.nextSlots.allPointers)
	if err != nil || !found {
		t.Fatalf("slotFor(next): found=%v err=%v", found, err)
	}
	if _, err5 := g.RemoveRelationship(capsules.nextSlots.allPointers, nextSlot); err5 != nil {
		t.Fatalf("RemoveRelationship(nextSlots tag, nextSlot): %v", err5)
	}

	// Each role's own accessor must independently report its own specific
	// violation -- there is no single call that reports all three at
	// once, which is exactly the gap this test documents.
	if _, _, err6 := capsules.Prev(g, capsule); !errors.Is(err6, ErrTooManyPointerTargets) {
		t.Fatalf("Prev() error = %v, want %v", err6, ErrTooManyPointerTargets)
	}

	if _, _, err7 := capsules.Value(g, capsule); !errors.Is(err7, ErrAmbiguousPointerMetadata) {
		t.Fatalf("Value() error = %v, want %v", err7, ErrAmbiguousPointerMetadata)
	}

	if _, _, err8 := capsules.Next(g, capsule); !errors.Is(err8, ErrNotCapsule) {
		t.Fatalf("Next() error = %v, want %v", err8, ErrNotCapsule)
	}
}

func TestAdversarialMissingEachCapsuleRoleTagMakesThatRoleUndiscoverable(t *testing.T) {
	roles := []struct {
		name string
		tag  func(*CapsuleRegistry) NodeID
		get  func(*CapsuleRegistry, GraphReader, NodeID) (NodeID, bool, error)
	}{
		{"prev", func(c *CapsuleRegistry) NodeID { return c.prevSlots.allPointers }, func(c *CapsuleRegistry, g GraphReader, id NodeID) (NodeID, bool, error) { return c.Prev(g, id) }},
		{"value", func(c *CapsuleRegistry) NodeID { return c.valueSlots.allPointers }, func(c *CapsuleRegistry, g GraphReader, id NodeID) (NodeID, bool, error) { return c.Value(g, id) }},
		{"next", func(c *CapsuleRegistry) NodeID { return c.nextSlots.allPointers }, func(c *CapsuleRegistry, g GraphReader, id NodeID) (NodeID, bool, error) { return c.Next(g, id) }},
	}
	for _, role := range roles {
		t.Run(role.name, func(t *testing.T) {
			g, capsules := newCapsuleTestFixture(t)
			value, err := g.CreateNode()
			if err != nil {
				t.Fatal(err)
			}
			capsule, err := capsules.NewCapsule(g, value)
			if err != nil {
				t.Fatal(err)
			}
			slot, found, err := capsules.slotFor(g, capsule, role.tag(capsules))
			if err != nil || !found {
				t.Fatalf("slotFor(): found=%v err=%v", found, err)
			}
			if _, err2 := g.RemoveRelationship(role.tag(capsules), slot); err2 != nil {
				t.Fatal(err2)
			}
			_, _, err = role.get(capsules, g, capsule)
			if !errors.Is(err, ErrNotCapsule) {
				t.Fatalf("role getter error = %v, want %v", err, ErrNotCapsule)
			}
		})
	}
}

func TestAdversarialSelfReferentialCapsuleValueIsAllowed(t *testing.T) {
	g, capsules := newCapsuleTestFixture(t)
	capsule, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	// Turn an ordinary node into a capsule-like structure only through the
	// public registry is impossible because NewCapsule requires a preexisting
	// value. Use a normal value node that happens to be the capsule itself
	// after construction by replacing the value target out-of-band.
	value, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	_ = value
	created, err := capsules.NewCapsule(g, value)
	if err != nil {
		t.Fatal(err)
	}
	valueSlot, found, err := capsules.slotFor(g, created, capsules.valueSlots.allPointers)
	if err != nil || !found {
		t.Fatal(err)
	}
	if _, err2 := g.RemoveRelationship(valueSlot, value); err2 != nil {
		t.Fatal(err2)
	}
	if _, err3 := g.AddRelationship(valueSlot, created); err3 != nil {
		t.Fatal(err3)
	}
	got, hasValue, err := capsules.Value(g, created)
	if err != nil {
		t.Fatal(err)
	}
	if !hasValue || got != created {
		t.Fatalf("Value() = (%d,%v), want (%d,true)", got, hasValue, created)
	}
	if !mustNodeExists(t, g, capsule) {
		t.Fatal("unused test node unexpectedly absent")
	}
}

// A capsule linked into two lists would share one position in two chains, so
// every read of either list rejects it.
func TestAdversarialCapsuleInTwoListsIsInvalid(t *testing.T) {
	g, capsules, lists := newListTestFixture(t)
	listA, err := lists.NewList(g)
	if err != nil {
		t.Fatal(err)
	}
	listB, err := lists.NewList(g)
	if err != nil {
		t.Fatal(err)
	}
	value, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	capsule, err := capsules.NewCapsule(g, value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddRelationship(listA, capsule); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddRelationship(listB, capsule); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddRelationship(lists.allHeads, capsule); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddRelationship(lists.allTails, capsule); err != nil {
		t.Fatal(err)
	}

	if _, err := lists.Elements(g, listA); !errors.Is(err, ErrInvalidListStructure) {
		t.Fatalf("Elements(listA) error = %v, want %v", err, ErrInvalidListStructure)
	}
	if _, err := lists.Elements(g, listB); !errors.Is(err, ErrInvalidListStructure) {
		t.Fatalf("Elements(listB) error = %v, want %v", err, ErrInvalidListStructure)
	}
	if verifyErr := lists.VerifyStructures(g); !errors.Is(verifyErr, ErrLoadVerification) || !errors.Is(verifyErr, ErrInvalidListStructure) {
		t.Fatalf("VerifyStructures() error = %v, want %v wrapping %v", verifyErr, ErrLoadVerification, ErrInvalidListStructure)
	}
}
