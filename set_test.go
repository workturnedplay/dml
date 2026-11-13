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
	"slices"
	"testing"
)

// newSetTestFixture creates a fresh Graph and SetRegistry with AllSets
// already bootstrapped, for use by SetRegistry tests.
func newSetTestFixture(t *testing.T) (testGraph, *SetRegistry) {
	t.Helper()

	g := newTestGraph(t)
	names := NewNameRegistry(g)

	ids, err := names.BootstrapNames(g, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	sets, err := NewSetRegistry(g, ids[NameAllSets], ids[NameAllCompositeSets], ids[NameAllCompositeSetLogs])
	if err != nil {
		t.Fatalf("NewSetRegistry(): %v", err)
	}

	return g, sets
}

func TestFoundationalNamesIncludesAllSets(t *testing.T) {
	for _, name := range FoundationalNames {
		if name == NameAllSets {
			return
		}
	}

	t.Fatalf("FoundationalNames %v does not include %q", FoundationalNames, NameAllSets)
}

func TestNewSetRegistryRequiresExistingAllSets(t *testing.T) {
	var g Graph

	const nonexistent NodeID = 999999

	_, err := NewSetRegistry(&g, nonexistent)
	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("NewSetRegistry() error = %v, want %v", err, ErrNodeNotFound)
	}
}

func TestNewSetTagsSetAndStartsEmpty(t *testing.T) {
	g, sets := newSetTestFixture(t)

	set, err := sets.NewSet(g)
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	if !mustNodeExists(t, g, set) {
		t.Fatalf("NewSet() returned NodeID %d that does not exist", set)
	}

	if !mustIsSet(t, sets, g, set) {
		t.Fatalf("NewSet() did not tag %d as a set", set)
	}

	members, err := sets.Members(g, set)
	if err != nil {
		t.Fatalf("Members(%d): %v", set, err)
	}
	if len(members) != 0 {
		t.Fatalf("Members(%d) = %v, want empty", set, members)
	}

	size, err := sets.Size(g, set)
	if err != nil {
		t.Fatalf("Size(%d): %v", set, err)
	}
	if size != 0 {
		t.Fatalf("Size(%d) = %d, want 0", set, size)
	}
}

func TestSetTagAsSetTagsFreshNode(t *testing.T) {
	g, sets := newSetTestFixture(t)

	id, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	if mustIsSet(t, sets, g, id) {
		t.Fatalf("node %d is unexpectedly already tagged Set-kind", id)
	}

	if err := sets.TagAsSet(g, id); err != nil {
		t.Fatalf("TagAsSet(%d): %v", id, err)
	}

	if !mustIsSet(t, sets, g, id) {
		t.Fatalf("TagAsSet(%d) did not tag the node", id)
	}
}

// TestSetTagAsSetAllowsExistingChildren pins down the contrast with
// PointerRegistry.TagAsPointer
// (TestPointerRegistryTagAsPointerRejectsMultipleExistingChildren): a Set
// imposes no cardinality constraint on its children at all, so tagging a
// node with any number of preexisting children as Set-kind always
// succeeds, and those children immediately become members.
func TestSetTagAsSetAllowsExistingChildren(t *testing.T) {
	g, sets := newSetTestFixture(t)

	id, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for x: %v", err)
	}

	y, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for y: %v", err)
	}

	if _, err2 := g.AddRelationship(id, x); err2 != nil {
		t.Fatalf("AddRelationship(id,x): %v", err2)
	}
	if _, err3 := g.AddRelationship(id, y); err3 != nil {
		t.Fatalf("AddRelationship(id,y): %v", err3)
	}

	if err4 := sets.TagAsSet(g, id); err4 != nil {
		t.Fatalf("TagAsSet(%d): %v", id, err4)
	}

	got, err := sets.Members(g, id)
	if err != nil {
		t.Fatalf("Members(%d): %v", id, err)
	}

	want := []NodeID{x, y}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Members(%d) = %v, want %v", id, got, want)
	}
}

func TestSetTagAsSetIsIdempotent(t *testing.T) {
	g, sets := newSetTestFixture(t)

	id, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	if err := sets.TagAsSet(g, id); err != nil {
		t.Fatalf("first TagAsSet(%d): %v", id, err)
	}

	if err := sets.TagAsSet(g, id); err != nil {
		t.Fatalf("second TagAsSet(%d): %v", id, err)
	}
}

func TestSetTagAsSetRequiresExistingNode(t *testing.T) {
	g, sets := newSetTestFixture(t)

	const nonexistent NodeID = 999999

	err := sets.TagAsSet(g, nonexistent)
	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("TagAsSet() error = %v, want %v", err, ErrNodeNotFound)
	}
}

func TestSetAddAddsMember(t *testing.T) {
	g, sets := newSetTestFixture(t)

	set, err := sets.NewSet(g)
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	member, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for member: %v", err)
	}

	added, err := sets.Add(g, set, member)
	if err != nil {
		t.Fatalf("Add(): %v", err)
	}
	if !added {
		t.Fatal("Add() reported that nothing was added")
	}

	found, err := sets.Contains(g, set, member)
	if err != nil {
		t.Fatalf("Contains(): %v", err)
	}
	if !found {
		t.Fatal("Contains() did not find the newly added member")
	}

	members, err := sets.Members(g, set)
	if err != nil {
		t.Fatalf("Members(): %v", err)
	}
	want := []NodeID{member}
	if !reflect.DeepEqual(members, want) {
		t.Fatalf("Members() = %v, want %v", members, want)
	}
}

func TestSetAddIsIdempotentForExistingMember(t *testing.T) {
	g, sets := newSetTestFixture(t)

	set, err := sets.NewSet(g)
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	member, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for member: %v", err)
	}

	if _, err2 := sets.Add(g, set, member); err2 != nil {
		t.Fatalf("first Add(): %v", err2)
	}

	added, err := sets.Add(g, set, member)
	if err != nil {
		t.Fatalf("second Add(): %v", err)
	}
	if added {
		t.Fatal("second Add() reported adding an already-present member")
	}
}

func TestSetAddAllowsSelfMembership(t *testing.T) {
	g, sets := newSetTestFixture(t)

	set, err := sets.NewSet(g)
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	added, err := sets.Add(g, set, set)
	if err != nil {
		t.Fatalf("Add(set, set): %v", err)
	}
	if !added {
		t.Fatal("self-membership was not created")
	}

	found, err := sets.Contains(g, set, set)
	if err != nil {
		t.Fatalf("Contains(set, set): %v", err)
	}
	if !found {
		t.Fatal("Contains(set, set) did not find self-membership")
	}

	members, err := sets.Members(g, set)
	if err != nil {
		t.Fatalf("Members(set): %v", err)
	}
	want := []NodeID{set}
	if !reflect.DeepEqual(members, want) {
		t.Fatalf("Members(set) = %v, want %v", members, want)
	}
}

func TestSetAddRequiresExistingMember(t *testing.T) {
	g, sets := newSetTestFixture(t)

	set, err := sets.NewSet(g)
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	const nonexistent NodeID = 999999

	if _, err := sets.Add(g, set, nonexistent); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Add() error = %v, want %v", err, ErrNodeNotFound)
	}
}

func TestSetContainsRequiresExistingMember(t *testing.T) {
	g, sets := newSetTestFixture(t)

	set, err := sets.NewSet(g)
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	const nonexistent NodeID = 999999

	if _, err := sets.Contains(g, set, nonexistent); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Contains() error = %v, want %v", err, ErrNodeNotFound)
	}
}

func TestSetRemoveRemovesExistingMember(t *testing.T) {
	g, sets := newSetTestFixture(t)

	set, err := sets.NewSet(g)
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	member, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for member: %v", err)
	}

	if _, err2 := sets.Add(g, set, member); err2 != nil {
		t.Fatalf("Add(): %v", err2)
	}

	removed, err := sets.Remove(g, set, member)
	if err != nil {
		t.Fatalf("Remove(): %v", err)
	}
	if !removed {
		t.Fatal("Remove() reported that nothing was removed")
	}

	found, err := sets.Contains(g, set, member)
	if err != nil {
		t.Fatalf("Contains(): %v", err)
	}
	if found {
		t.Fatal("member is still present after Remove()")
	}
}

func TestSetRemoveNoOpWhenAbsent(t *testing.T) {
	g, sets := newSetTestFixture(t)

	set, err := sets.NewSet(g)
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	member, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for member: %v", err)
	}

	removed, err := sets.Remove(g, set, member)
	if err != nil {
		t.Fatalf("Remove(): %v", err)
	}
	if removed {
		t.Fatal("Remove() reported removal of a member that was never added")
	}
}

func TestSetRemoveRequiresExistingMember(t *testing.T) {
	g, sets := newSetTestFixture(t)

	set, err := sets.NewSet(g)
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	const nonexistent NodeID = 999999

	if _, err := sets.Remove(g, set, nonexistent); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Remove() error = %v, want %v", err, ErrNodeNotFound)
	}
}

func TestSetContainsReflectsMembership(t *testing.T) {
	g, sets := newSetTestFixture(t)

	set, err := sets.NewSet(g)
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	member, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for member: %v", err)
	}

	found, err := sets.Contains(g, set, member)
	if err != nil {
		t.Fatalf("Contains() before Add: %v", err)
	}
	if found {
		t.Fatal("Contains() found a member that was never added")
	}

	if _, err2 := sets.Add(g, set, member); err2 != nil {
		t.Fatalf("Add(): %v", err2)
	}

	found, err = sets.Contains(g, set, member)
	if err != nil {
		t.Fatalf("Contains() after Add: %v", err)
	}
	if !found {
		t.Fatal("Contains() did not find a member that was added")
	}
}

func TestSetMembersReturnsAllDirectChildren(t *testing.T) {
	g, sets := newSetTestFixture(t)

	set, err := sets.NewSet(g)
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	var want []NodeID
	for i := 0; i < 3; i++ {
		member, err2 := g.CreateNode()
		if err2 != nil {
			t.Fatalf("CreateNode() for member %d: %v", i, err2)
		}
		if _, err3 := sets.Add(g, set, member); err3 != nil {
			t.Fatalf("Add(member %d): %v", i, err3)
		}
		want = append(want, member)
	}

	got, err := sets.Members(g, set)
	if err != nil {
		t.Fatalf("Members(): %v", err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Members() = %v, want %v", got, want)
	}
}

// TestSetMembersDoesNotRecurseIntoNestedSet pins down
// theorystate.md section 9a/79: a Set containing another Set as a
// member does not, by itself, imply recursive membership expansion.
func TestSetMembersDoesNotRecurseIntoNestedSet(t *testing.T) {
	g, sets := newSetTestFixture(t)

	outer, err := sets.NewSet(g)
	if err != nil {
		t.Fatalf("NewSet() for outer: %v", err)
	}

	inner, err := sets.NewSet(g)
	if err != nil {
		t.Fatalf("NewSet() for inner: %v", err)
	}

	innerMember, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for innerMember: %v", err)
	}

	if _, err2 := sets.Add(g, inner, innerMember); err2 != nil {
		t.Fatalf("Add(inner, innerMember): %v", err2)
	}

	if _, err3 := sets.Add(g, outer, inner); err3 != nil {
		t.Fatalf("Add(outer, inner): %v", err3)
	}

	got, err := sets.Members(g, outer)
	if err != nil {
		t.Fatalf("Members(outer): %v", err)
	}

	want := []NodeID{inner}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Members(outer) = %v, want %v -- Members must not recurse into a nested Set's own members", got, want)
	}
}

func TestSetSizeMatchesMemberCount(t *testing.T) {
	g, sets := newSetTestFixture(t)

	set, err := sets.NewSet(g)
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	for i := 0; i < 3; i++ {
		member, err2 := g.CreateNode()
		if err2 != nil {
			t.Fatalf("CreateNode() for member %d: %v", i, err2)
		}
		if _, err3 := sets.Add(g, set, member); err3 != nil {
			t.Fatalf("Add(member %d): %v", i, err3)
		}
	}

	size, err := sets.Size(g, set)
	if err != nil {
		t.Fatalf("Size(): %v", err)
	}
	if size != 3 {
		t.Fatalf("Size() = %d, want 3", size)
	}

	members, err := sets.Members(g, set)
	if err != nil {
		t.Fatalf("Members(): %v", err)
	}
	if size != len(members) {
		t.Fatalf("Size() = %d, want len(Members()) = %d", size, len(members))
	}
}

func TestSetDeleteSetSucceedsWhenEmpty(t *testing.T) {
	g, sets := newSetTestFixture(t)

	set, err := sets.NewSet(g)
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	if err := sets.DeleteSet(g, set); err != nil {
		t.Fatalf("DeleteSet(): %v", err)
	}

	if mustNodeExists(t, g, set) {
		t.Fatalf("set %d still exists after successful DeleteSet()", set)
	}
}

func TestSetDeleteSetFailsIfNotEmpty(t *testing.T) {
	g, sets := newSetTestFixture(t)

	set, err := sets.NewSet(g)
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	member, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for member: %v", err)
	}

	if _, err2 := sets.Add(g, set, member); err2 != nil {
		t.Fatalf("Add(): %v", err2)
	}

	err = sets.DeleteSet(g, set)
	if !errors.Is(err, ErrNodeNotEmpty) {
		t.Fatalf("DeleteSet() error = %v, want %v", err, ErrNodeNotEmpty)
	}

	if !mustNodeExists(t, g, set) {
		t.Fatal("set disappeared despite a failed DeleteSet()")
	}
	if !mustIsSet(t, sets, g, set) {
		t.Fatal("set lost its AllSets tag despite a failed DeleteSet()")
	}

	found, err := sets.Contains(g, set, member)
	if err != nil {
		t.Fatalf("Contains(): %v", err)
	}
	if !found {
		t.Fatal("membership was disturbed by a failed DeleteSet()")
	}
}

// TestSetDeleteSetFailsIfReferencedElsewhere covers the case where set
// has no members of its own but is itself referenced by some other node
// -- Graph.DeleteNode requires both outgoing and incoming relationships
// to be empty (theorystate.md section 18), so an incoming reference
// alone is enough to block deletion.
func TestSetDeleteSetFailsIfReferencedElsewhere(t *testing.T) {
	g, sets := newSetTestFixture(t)

	set, err := sets.NewSet(g)
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	referrer, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for referrer: %v", err)
	}

	if _, err2 := g.AddRelationship(referrer, set); err2 != nil {
		t.Fatalf("AddRelationship(referrer, set): %v", err2)
	}

	err = sets.DeleteSet(g, set)
	if !errors.Is(err, ErrNodeNotEmpty) {
		t.Fatalf("DeleteSet() error = %v, want %v", err, ErrNodeNotEmpty)
	}

	if !mustNodeExists(t, g, set) {
		t.Fatal("set disappeared despite a failed DeleteSet()")
	}
	if !mustIsSet(t, sets, g, set) {
		t.Fatal("set lost its AllSets tag despite a failed DeleteSet()")
	}
	if !mustHasRelationship(t, g, referrer, set) {
		t.Fatal("referrer's relationship to set was disturbed by a failed DeleteSet()")
	}
}

func TestSetOperationsRequireSetTag(t *testing.T) {
	g, sets := newSetTestFixture(t)

	notASet, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	member, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for member: %v", err)
	}

	if _, err := sets.Add(g, notASet, member); !errors.Is(err, ErrNotSet) {
		t.Fatalf("Add() error = %v, want %v", err, ErrNotSet)
	}

	if _, err := sets.Remove(g, notASet, member); !errors.Is(err, ErrNotSet) {
		t.Fatalf("Remove() error = %v, want %v", err, ErrNotSet)
	}

	if _, err := sets.Contains(g, notASet, member); !errors.Is(err, ErrNotSet) {
		t.Fatalf("Contains() error = %v, want %v", err, ErrNotSet)
	}

	if _, err := sets.Members(g, notASet); !errors.Is(err, ErrNotSet) {
		t.Fatalf("Members() error = %v, want %v", err, ErrNotSet)
	}

	if _, err := sets.Size(g, notASet); !errors.Is(err, ErrNotSet) {
		t.Fatalf("Size() error = %v, want %v", err, ErrNotSet)
	}

	if err := sets.DeleteSet(g, notASet); !errors.Is(err, ErrNotSet) {
		t.Fatalf("DeleteSet() error = %v, want %v", err, ErrNotSet)
	}
}

func TestSetOperationsRequireExistingSetNode(t *testing.T) {
	g, sets := newSetTestFixture(t)

	member, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for member: %v", err)
	}

	const nonexistent NodeID = 999999

	if _, err := sets.Add(g, nonexistent, member); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Add() error = %v, want %v", err, ErrNodeNotFound)
	}

	if _, err := sets.Remove(g, nonexistent, member); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Remove() error = %v, want %v", err, ErrNodeNotFound)
	}

	if _, err := sets.Contains(g, nonexistent, member); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Contains() error = %v, want %v", err, ErrNodeNotFound)
	}

	if _, err := sets.Members(g, nonexistent); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Members() error = %v, want %v", err, ErrNodeNotFound)
	}

	if _, err := sets.Size(g, nonexistent); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Size() error = %v, want %v", err, ErrNodeNotFound)
	}

	if err := sets.DeleteSet(g, nonexistent); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("DeleteSet() error = %v, want %v", err, ErrNodeNotFound)
	}
}

// TestSetRegistryTagAsSetRejectsCompositeSetConflict pins down
// theorystate.md section 79's mutual-exclusivity rule, now enforced now
// that a second Set-representation tag (AllCompositeSets) exists:
// SetRegistry.TagAsSet must refuse to add the AllSets tag to a node
// already tagged AllCompositeSets.
func TestSetRegistryTagAsSetRejectsCompositeSetConflict(t *testing.T) {
	g, sets, composites := newCompositeSetTestFixture(t)

	composite, err := composites.NewCompositeSet(g)
	if err != nil {
		t.Fatalf("NewCompositeSet(): %v", err)
	}

	err = sets.TagAsSet(g, composite)
	if !errors.Is(err, ErrSetRepresentationConflict) {
		t.Fatalf("TagAsSet() error = %v, want %v", err, ErrSetRepresentationConflict)
	}

	if mustIsSet(t, sets, g, composite) {
		t.Fatal("node was tagged AllSets despite already being AllCompositeSets-tagged")
	}
}

// newCompositeSetTestFixture creates a fresh Graph, SetRegistry, and
// CompositeSetRegistry with every relevant name already bootstrapped, for
// use by CompositeSetRegistry tests.
func newCompositeSetTestFixture(t *testing.T) (testGraph, *SetRegistry, *CompositeSetRegistry) {
	t.Helper()

	g := newTestGraph(t)
	names := NewNameRegistry(g)

	ids, err := names.BootstrapNames(g, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	sets, err := NewSetRegistry(g, ids[NameAllSets], ids[NameAllCompositeSets], ids[NameAllCompositeSetLogs])
	if err != nil {
		t.Fatalf("NewSetRegistry(): %v", err)
	}

	composites, err := NewCompositeSetRegistry(
		g,
		sets,
		ids[NameAllCompositeSets],
		ids[NameAllAdditiveOp],
		ids[NameAllSubtractiveOp],
		ids[NameAllScalarOperand],
		ids[NameAllSetOperand],
	)
	if err != nil {
		t.Fatalf("NewCompositeSetRegistry(): %v", err)
	}

	return g, sets, composites
}

func TestFoundationalNamesIncludesCompositeSetNames(t *testing.T) {
	want := []string{
		NameAllCompositeSets,
		NameAllAdditiveOp,
		NameAllSubtractiveOp,
		NameAllScalarOperand,
		NameAllSetOperand,
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

func TestNewCompositeSetRegistryRequiresExistingTags(t *testing.T) {
	var g Graph

	existing, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	sets, err := NewSetRegistry(&g, existing)
	if err != nil {
		t.Fatalf("NewSetRegistry(): %v", err)
	}

	const nonexistent NodeID = 999999

	if _, err := NewCompositeSetRegistry(&g, sets, nonexistent, existing, existing, existing, existing); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("NewCompositeSetRegistry() error = %v, want %v", err, ErrNodeNotFound)
	}
	if _, err := NewCompositeSetRegistry(&g, sets, existing, nonexistent, existing, existing, existing); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("NewCompositeSetRegistry() error = %v, want %v", err, ErrNodeNotFound)
	}
	if _, err := NewCompositeSetRegistry(&g, sets, existing, existing, nonexistent, existing, existing); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("NewCompositeSetRegistry() error = %v, want %v", err, ErrNodeNotFound)
	}
	if _, err := NewCompositeSetRegistry(&g, sets, existing, existing, existing, nonexistent, existing); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("NewCompositeSetRegistry() error = %v, want %v", err, ErrNodeNotFound)
	}
	if _, err := NewCompositeSetRegistry(&g, sets, existing, existing, existing, existing, nonexistent); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("NewCompositeSetRegistry() error = %v, want %v", err, ErrNodeNotFound)
	}
}

func TestNewCompositeSetStartsEmpty(t *testing.T) {
	g, _, composites := newCompositeSetTestFixture(t)

	set, err := composites.NewCompositeSet(g)
	if err != nil {
		t.Fatalf("NewCompositeSet(): %v", err)
	}

	if !mustNodeExists(t, g, set) {
		t.Fatalf("NewCompositeSet() returned NodeID %d that does not exist", set)
	}
	if !mustIsCompositeSet(t, composites, g, set) {
		t.Fatalf("NewCompositeSet() did not tag %d as a composite set", set)
	}

	operands, err := composites.Operands(g, set)
	if err != nil {
		t.Fatalf("Operands(): %v", err)
	}
	if len(operands) != 0 {
		t.Fatalf("Operands() = %v, want empty", operands)
	}

	members, err := composites.Evaluate(g, set)
	if err != nil {
		t.Fatalf("Evaluate(): %v", err)
	}
	if len(members) != 0 {
		t.Fatalf("Evaluate() = %v, want empty", members)
	}
}

func TestCompositeSetAddOperandScalarAdditive(t *testing.T) {
	g, _, composites := newCompositeSetTestFixture(t)

	set, err := composites.NewCompositeSet(g)
	if err != nil {
		t.Fatalf("NewCompositeSet(): %v", err)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for x: %v", err)
	}

	u, err := composites.AddOperand(g, set, x, true, false)
	if err != nil {
		t.Fatalf("AddOperand(x, additive, scalar): %v", err)
	}

	target, err := composites.OperandTarget(g, u)
	if err != nil {
		t.Fatalf("OperandTarget(u): %v", err)
	}
	if target != x {
		t.Fatalf("OperandTarget(u) = %d, want %d", target, x)
	}

	isAdditive, err := composites.OperandIsAdditive(g, u)
	if err != nil {
		t.Fatalf("OperandIsAdditive(u): %v", err)
	}
	if !isAdditive {
		t.Fatal("OperandIsAdditive(u) = false, want true")
	}

	isSetOperand, err := composites.OperandIsSetOperand(g, u)
	if err != nil {
		t.Fatalf("OperandIsSetOperand(u): %v", err)
	}
	if isSetOperand {
		t.Fatal("OperandIsSetOperand(u) = true, want false")
	}

	got, err := composites.Evaluate(g, set)
	if err != nil {
		t.Fatalf("Evaluate(): %v", err)
	}
	want := []NodeID{x}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Evaluate() = %v, want %v", got, want)
	}
}

func TestCompositeSetEvaluateUnionThenDifference(t *testing.T) {
	g, sets, composites := newCompositeSetTestFixture(t)

	setA, err := sets.NewSet(g)
	if err != nil {
		t.Fatalf("NewSet() for setA: %v", err)
	}
	a1, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	a2, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err2 := sets.Add(g, setA, a1); err2 != nil {
		t.Fatal(err2)
	}
	if _, err3 := sets.Add(g, setA, a2); err3 != nil {
		t.Fatal(err3)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	composite, err := composites.NewCompositeSet(g)
	if err != nil {
		t.Fatalf("NewCompositeSet(): %v", err)
	}

	// composite = (setA expanded ∪ {x}) \ {a2}
	if _, err4 := composites.AddOperand(g, composite, setA, true, true); err4 != nil {
		t.Fatalf("AddOperand(setA, additive, set): %v", err4)
	}
	if _, err5 := composites.AddOperand(g, composite, x, true, false); err5 != nil {
		t.Fatalf("AddOperand(x, additive, scalar): %v", err5)
	}
	if _, err6 := composites.AddOperand(g, composite, a2, false, false); err6 != nil {
		t.Fatalf("AddOperand(a2, subtractive, scalar): %v", err6)
	}

	got, err := composites.Evaluate(g, composite)
	if err != nil {
		t.Fatalf("Evaluate(): %v", err)
	}

	want := sortedNodeIDs([]NodeID{a1, x})
	got = sortedNodeIDs(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Evaluate() = %v, want %v", got, want)
	}
}

func TestCompositeSetAddOperandRequiresKnownSetTagWhenSetOperand(t *testing.T) {
	g, _, composites := newCompositeSetTestFixture(t)

	set, err := composites.NewCompositeSet(g)
	if err != nil {
		t.Fatalf("NewCompositeSet(): %v", err)
	}

	notASet, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	_, err = composites.AddOperand(g, set, notASet, true, true)
	if !errors.Is(err, ErrInvalidSetOperand) {
		t.Fatalf("AddOperand() error = %v, want %v", err, ErrInvalidSetOperand)
	}
}

func TestCompositeSetEvaluateResolvesNestedCompositeSetOperand(t *testing.T) {
	g, _, composites := newCompositeSetTestFixture(t)

	inner, err := composites.NewCompositeSet(g)
	if err != nil {
		t.Fatalf("NewCompositeSet() for inner: %v", err)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err2 := composites.AddOperand(g, inner, x, true, false); err2 != nil {
		t.Fatalf("AddOperand(inner, x): %v", err2)
	}

	outer, err := composites.NewCompositeSet(g)
	if err != nil {
		t.Fatalf("NewCompositeSet() for outer: %v", err)
	}
	if _, err3 := composites.AddOperand(g, outer, inner, true, true); err3 != nil {
		t.Fatalf("AddOperand(outer, inner, additive, set): %v", err3)
	}

	got, err := composites.Evaluate(g, outer)
	if err != nil {
		t.Fatalf("Evaluate(outer): %v", err)
	}
	want := []NodeID{x}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Evaluate(outer) = %v, want %v", got, want)
	}
}

func TestCompositeSetEvaluateDetectsCycle(t *testing.T) {
	g, _, composites := newCompositeSetTestFixture(t)

	c1, err := composites.NewCompositeSet(g)
	if err != nil {
		t.Fatalf("NewCompositeSet() for c1: %v", err)
	}
	c2, err := composites.NewCompositeSet(g)
	if err != nil {
		t.Fatalf("NewCompositeSet() for c2: %v", err)
	}

	if _, err2 := composites.AddOperand(g, c1, c2, true, true); err2 != nil {
		t.Fatalf("AddOperand(c1, c2): %v", err2)
	}
	if _, err3 := composites.AddOperand(g, c2, c1, true, true); err3 != nil {
		t.Fatalf("AddOperand(c2, c1): %v", err3)
	}

	_, err = composites.Evaluate(g, c1)
	if !errors.Is(err, ErrCompositeSetCycle) {
		t.Fatalf("Evaluate(c1) error = %v, want %v", err, ErrCompositeSetCycle)
	}
}

// TestCompositeSetEvaluateAllowsDiamondSharedOperand pins down that
// cycle-detection visited tracking is path-scoped (theorystate.md
// section 83), not a global ever-visited set: reaching the same
// composite-kind node via two different, non-cyclic branches must not be
// mistaken for a cycle.
//
//	top -> branchA -> shared -> x
//	top -> branchB -> shared -> x   (same "shared", not a cycle)
func TestCompositeSetEvaluateAllowsDiamondSharedOperand(t *testing.T) {
	g, _, composites := newCompositeSetTestFixture(t)

	shared, err := composites.NewCompositeSet(g)
	if err != nil {
		t.Fatalf("NewCompositeSet() for shared: %v", err)
	}
	x, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err2 := composites.AddOperand(g, shared, x, true, false); err2 != nil {
		t.Fatalf("AddOperand(shared, x): %v", err2)
	}

	branchA, err := composites.NewCompositeSet(g)
	if err != nil {
		t.Fatalf("NewCompositeSet() for branchA: %v", err)
	}
	if _, err3 := composites.AddOperand(g, branchA, shared, true, true); err3 != nil {
		t.Fatalf("AddOperand(branchA, shared): %v", err3)
	}

	branchB, err := composites.NewCompositeSet(g)
	if err != nil {
		t.Fatalf("NewCompositeSet() for branchB: %v", err)
	}
	if _, err4 := composites.AddOperand(g, branchB, shared, true, true); err4 != nil {
		t.Fatalf("AddOperand(branchB, shared): %v", err4)
	}

	top, err := composites.NewCompositeSet(g)
	if err != nil {
		t.Fatalf("NewCompositeSet() for top: %v", err)
	}
	if _, err5 := composites.AddOperand(g, top, branchA, true, true); err5 != nil {
		t.Fatalf("AddOperand(top, branchA): %v", err5)
	}
	if _, err6 := composites.AddOperand(g, top, branchB, true, true); err6 != nil {
		t.Fatalf("AddOperand(top, branchB): %v", err6)
	}

	got, err := composites.Evaluate(g, top)
	if err != nil {
		t.Fatalf("Evaluate(top): %v", err)
	}
	want := []NodeID{x}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Evaluate(top) = %v, want %v", got, want)
	}
}

func TestCompositeSetRemoveOperandDeletesDescriptor(t *testing.T) {
	g, _, composites := newCompositeSetTestFixture(t)

	set, err := composites.NewCompositeSet(g)
	if err != nil {
		t.Fatalf("NewCompositeSet(): %v", err)
	}
	x, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	u, err := composites.AddOperand(g, set, x, true, false)
	if err != nil {
		t.Fatalf("AddOperand(): %v", err)
	}

	if err2 := composites.RemoveOperand(g, set, u); err2 != nil {
		t.Fatalf("RemoveOperand(): %v", err2)
	}

	if mustNodeExists(t, g, u) {
		t.Fatalf("descriptor %d still exists after RemoveOperand()", u)
	}

	got, err := composites.Evaluate(g, set)
	if err != nil {
		t.Fatalf("Evaluate(): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Evaluate() = %v, want empty after RemoveOperand()", got)
	}

	if !mustNodeExists(t, g, x) {
		t.Fatal("RemoveOperand() incorrectly deleted the operand target")
	}
}

func TestCompositeSetRemoveOperandRequiresOperandInSet(t *testing.T) {
	g, _, composites := newCompositeSetTestFixture(t)

	setA, err := composites.NewCompositeSet(g)
	if err != nil {
		t.Fatal(err)
	}
	setB, err := composites.NewCompositeSet(g)
	if err != nil {
		t.Fatal(err)
	}
	x, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	u, err := composites.AddOperand(g, setA, x, true, false)
	if err != nil {
		t.Fatal(err)
	}

	err = composites.RemoveOperand(g, setB, u)
	if !errors.Is(err, ErrOperandNotInCompositeSet) {
		t.Fatalf("RemoveOperand() error = %v, want %v", err, ErrOperandNotInCompositeSet)
	}
}

func TestCompositeSetDeleteCompositeSetSucceedsWhenEmpty(t *testing.T) {
	g, _, composites := newCompositeSetTestFixture(t)

	set, err := composites.NewCompositeSet(g)
	if err != nil {
		t.Fatal(err)
	}

	if err := composites.DeleteCompositeSet(g, set); err != nil {
		t.Fatalf("DeleteCompositeSet(): %v", err)
	}
	if mustNodeExists(t, g, set) {
		t.Fatalf("composite set %d still exists after successful DeleteCompositeSet()", set)
	}
}

func TestCompositeSetDeleteCompositeSetFailsIfNotEmpty(t *testing.T) {
	g, _, composites := newCompositeSetTestFixture(t)

	set, err := composites.NewCompositeSet(g)
	if err != nil {
		t.Fatal(err)
	}
	x, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err2 := composites.AddOperand(g, set, x, true, false); err2 != nil {
		t.Fatal(err2)
	}

	err = composites.DeleteCompositeSet(g, set)
	if !errors.Is(err, ErrNodeNotEmpty) {
		t.Fatalf("DeleteCompositeSet() error = %v, want %v", err, ErrNodeNotEmpty)
	}
	if !mustNodeExists(t, g, set) {
		t.Fatal("composite set disappeared despite a failed DeleteCompositeSet()")
	}
	if !mustIsCompositeSet(t, composites, g, set) {
		t.Fatal("composite set lost its tag despite a failed DeleteCompositeSet()")
	}
}

func TestCompositeSetOperationsRequireCompositeSetTag(t *testing.T) {
	g, _, composites := newCompositeSetTestFixture(t)

	notAComposite, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	x, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := composites.AddOperand(g, notAComposite, x, true, false); !errors.Is(err, ErrNotCompositeSet) {
		t.Fatalf("AddOperand() error = %v, want %v", err, ErrNotCompositeSet)
	}
	if _, err := composites.Operands(g, notAComposite); !errors.Is(err, ErrNotCompositeSet) {
		t.Fatalf("Operands() error = %v, want %v", err, ErrNotCompositeSet)
	}
	if _, err := composites.Evaluate(g, notAComposite); !errors.Is(err, ErrNotCompositeSet) {
		t.Fatalf("Evaluate() error = %v, want %v", err, ErrNotCompositeSet)
	}
	if err := composites.DeleteCompositeSet(g, notAComposite); !errors.Is(err, ErrNotCompositeSet) {
		t.Fatalf("DeleteCompositeSet() error = %v, want %v", err, ErrNotCompositeSet)
	}
	if err := composites.RemoveOperand(g, notAComposite, x); !errors.Is(err, ErrNotCompositeSet) {
		t.Fatalf("RemoveOperand() error = %v, want %v", err, ErrNotCompositeSet)
	}
}

// TestCompositeSetRegistryCheckerCatchesMalformedDescriptorAtCommitTime
// demonstrates the CompositeSetRegistry operand-descriptor Checker
// (registered by NewCompositeSetRegistry) catching a malformed
// descriptor immediately, at commit time, rather than only the next time
// something calls Evaluate(). As with the analogous ListRegistry test
// above, this simulates the malformed wiring through Graph.Transact
// directly, since only Transact-mediated mutations are visible to any
// Checker.
func TestCompositeSetRegistryCheckerCatchesMalformedDescriptorAtCommitTime(t *testing.T) {
	g, _, composites := newCompositeSetTestFixture(t)

	set, err := composites.NewCompositeSet(g)
	if err != nil {
		t.Fatalf("NewCompositeSet(): %v", err)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for x: %v", err)
	}

	// Simulate a hypothetical buggy composed operation that wires a
	// descriptor with both operation-kind tags at once, entirely through
	// one Graph.Transact call.
	err = g.Transact(func(tx Tx) error {
		u, err2 := createNodeTx(tx)
		if err2 != nil {
			return err2
		}
		for _, tag := range []NodeID{composites.allAdditiveOp, composites.allSubtractiveOp, composites.allScalarOperand} {
			if err3 := addRelationshipTx(tx, tag, u); err3 != nil {
				return err3
			}
		}
		if err3 := addRelationshipTx(tx, u, x); err3 != nil {
			return err3
		}
		return addRelationshipTx(tx, set, u)
	})

	if !errors.Is(err, ErrInvalidOperandDescriptor) {
		t.Fatalf("Transact() error = %v, want %v", err, ErrInvalidOperandDescriptor)
	}

	operands, err := composites.Operands(g, set)
	if err != nil {
		t.Fatalf("Operands(set): %v", err)
	}
	if len(operands) != 0 {
		t.Fatalf("Operands(set) = %v, want empty after the Checker declined the commit", operands)
	}
}

// TestCompositeSetEvaluateDetectsMalformedDescriptor covers an
// out-of-band mutation giving a descriptor node both operation-kind tags
// at once, which exactlyOneTag must reject rather than guess.
func TestCompositeSetEvaluateDetectsMalformedDescriptor(t *testing.T) {
	g, _, composites := newCompositeSetTestFixture(t)

	set, err := composites.NewCompositeSet(g)
	if err != nil {
		t.Fatal(err)
	}
	x, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	u, err := composites.AddOperand(g, set, x, true, false)
	if err != nil {
		t.Fatal(err)
	}

	// Bypass AddOperand entirely, simulating an out-of-band mutation that
	// gives u a second, conflicting operation-kind tag.
	if _, err2 := g.AddRelationship(composites.allSubtractiveOp, u); err2 != nil {
		t.Fatal(err2)
	}

	_, err = composites.Evaluate(g, set)
	if !errors.Is(err, ErrInvalidOperandDescriptor) {
		t.Fatalf("Evaluate() error = %v, want %v", err, ErrInvalidOperandDescriptor)
	}
}

func TestCompositeSetContainsReflectsMembership(t *testing.T) {
	g, _, composites := newCompositeSetTestFixture(t)

	set, err := composites.NewCompositeSet(g)
	if err != nil {
		t.Fatalf("NewCompositeSet(): %v", err)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err2 := composites.AddOperand(g, set, x, true, false); err2 != nil {
		t.Fatalf("AddOperand(): %v", err2)
	}

	found, err := composites.Contains(g, set, x)
	if err != nil {
		t.Fatalf("Contains(set, x): %v", err)
	}
	if !found {
		t.Fatal("Contains(set, x) = false, want true")
	}

	y, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	found, err = composites.Contains(g, set, y)
	if err != nil {
		t.Fatalf("Contains(set, y): %v", err)
	}
	if found {
		t.Fatal("Contains(set, y) = true, want false")
	}
}

func TestCompositeSetContainsRequiresCompositeSetTag(t *testing.T) {
	g, _, composites := newCompositeSetTestFixture(t)

	notAComposite, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	x, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := composites.Contains(g, notAComposite, x); !errors.Is(err, ErrNotCompositeSet) {
		t.Fatalf("Contains() error = %v, want %v", err, ErrNotCompositeSet)
	}
}

func TestCompositeSetContainsRequiresExistingValue(t *testing.T) {
	g, _, composites := newCompositeSetTestFixture(t)

	set, err := composites.NewCompositeSet(g)
	if err != nil {
		t.Fatal(err)
	}

	const nonexistent NodeID = 999999
	if _, err := composites.Contains(g, set, nonexistent); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Contains() error = %v, want %v", err, ErrNodeNotFound)
	}
}

// newCompositeSetLogTestFixture creates a fresh Graph plus every registry
// needed to exercise CompositeSetLogRegistry, with full bidirectional
// cross-representation dispatch already wired via
// CompositeSetRegistry.SetLogs.
func newCompositeSetLogTestFixture(t *testing.T) (testGraph, *SetRegistry, *CompositeSetRegistry, *CompositeSetLogRegistry) {
	t.Helper()

	g := newTestGraph(t)
	names := NewNameRegistry(g)

	ids, err := names.BootstrapNames(g, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	sets, err := NewSetRegistry(g, ids[NameAllSets], ids[NameAllCompositeSets], ids[NameAllCompositeSetLogs])
	if err != nil {
		t.Fatalf("NewSetRegistry(): %v", err)
	}

	composites, err := NewCompositeSetRegistry(
		g,
		sets,
		ids[NameAllCompositeSets],
		ids[NameAllAdditiveOp],
		ids[NameAllSubtractiveOp],
		ids[NameAllScalarOperand],
		ids[NameAllSetOperand],
	)
	if err != nil {
		t.Fatalf("NewCompositeSetRegistry(): %v", err)
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

	logs, err := NewCompositeSetLogRegistry(
		g,
		lists,
		composites,
		ids[NameAllCompositeSetLogs],
		ids[NameAllAdditiveOp],
		ids[NameAllSubtractiveOp],
		ids[NameAllScalarOperand],
		ids[NameAllSetOperand],
	)
	if err != nil {
		t.Fatalf("NewCompositeSetLogRegistry(): %v", err)
	}

	composites.SetLogs(logs)

	return g, sets, composites, logs
}

func TestFoundationalNamesIncludesAllCompositeSetLogs(t *testing.T) {
	for _, name := range FoundationalNames {
		if name == NameAllCompositeSetLogs {
			return
		}
	}

	t.Fatalf("FoundationalNames %v does not include %q", FoundationalNames, NameAllCompositeSetLogs)
}

func TestNewCompositeSetLogRegistryRequiresExistingTags(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	ids, err := names.BootstrapNames(&g, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	sets, err := NewSetRegistry(&g, ids[NameAllSets], ids[NameAllCompositeSets], ids[NameAllCompositeSetLogs])
	if err != nil {
		t.Fatalf("NewSetRegistry(): %v", err)
	}

	composites, err := NewCompositeSetRegistry(
		&g,
		sets,
		ids[NameAllCompositeSets],
		ids[NameAllAdditiveOp],
		ids[NameAllSubtractiveOp],
		ids[NameAllScalarOperand],
		ids[NameAllSetOperand],
	)
	if err != nil {
		t.Fatalf("NewCompositeSetRegistry(): %v", err)
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

	lists, err := NewListRegistry(&g, capsules, ids[NameAllLists], ids[NameAllHeads], ids[NameAllTails])
	if err != nil {
		t.Fatalf("NewListRegistry(): %v", err)
	}

	const nonexistent NodeID = 999999
	existing := ids[NameAllCompositeSetLogs]

	if _, err := NewCompositeSetLogRegistry(&g, lists, composites, nonexistent, existing, existing, existing, existing); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("NewCompositeSetLogRegistry() error = %v, want %v", err, ErrNodeNotFound)
	}
	if _, err := NewCompositeSetLogRegistry(&g, lists, composites, existing, nonexistent, existing, existing, existing); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("NewCompositeSetLogRegistry() error = %v, want %v", err, ErrNodeNotFound)
	}
	if _, err := NewCompositeSetLogRegistry(&g, lists, composites, existing, existing, nonexistent, existing, existing); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("NewCompositeSetLogRegistry() error = %v, want %v", err, ErrNodeNotFound)
	}
	if _, err := NewCompositeSetLogRegistry(&g, lists, composites, existing, existing, existing, nonexistent, existing); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("NewCompositeSetLogRegistry() error = %v, want %v", err, ErrNodeNotFound)
	}
	if _, err := NewCompositeSetLogRegistry(&g, lists, composites, existing, existing, existing, existing, nonexistent); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("NewCompositeSetLogRegistry() error = %v, want %v", err, ErrNodeNotFound)
	}
}

func TestNewCompositeSetLogTagsBothAllListsAndAllCompositeSetLogs(t *testing.T) {
	g, _, _, logs := newCompositeSetLogTestFixture(t)

	log, err := logs.NewCompositeSetLog(g)
	if err != nil {
		t.Fatalf("NewCompositeSetLog(): %v", err)
	}

	if !mustNodeExists(t, g, log) {
		t.Fatalf("NewCompositeSetLog() returned NodeID %d that does not exist", log)
	}
	if !mustIsCompositeSetLog(t, logs, g, log) {
		t.Fatalf("NewCompositeSetLog() did not tag %d as a composite set log", log)
	}
	if !mustIsList(t, logs.lists, g, log) {
		t.Fatalf("NewCompositeSetLog() did not also tag %d as a list", log)
	}

	ops, err := logs.Operations(g, log)
	if err != nil {
		t.Fatalf("Operations(): %v", err)
	}
	if len(ops) != 0 {
		t.Fatalf("Operations() = %v, want empty", ops)
	}

	members, err := logs.Evaluate(g, log)
	if err != nil {
		t.Fatalf("Evaluate(): %v", err)
	}
	if len(members) != 0 {
		t.Fatalf("Evaluate() = %v, want empty", members)
	}
}

func TestCompositeSetLogAppendOperationScalarAdditive(t *testing.T) {
	g, _, _, logs := newCompositeSetLogTestFixture(t)

	log, err := logs.NewCompositeSetLog(g)
	if err != nil {
		t.Fatalf("NewCompositeSetLog(): %v", err)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for x: %v", err)
	}

	u, capsule, err := logs.AppendOperation(g, log, x, true, false)
	if err != nil {
		t.Fatalf("AppendOperation(x, additive, scalar): %v", err)
	}

	if !mustHasRelationship(t, g, log, capsule) {
		t.Fatalf("capsule %d is not linked into log", capsule)
	}

	target, err := logs.OperandTarget(g, u)
	if err != nil {
		t.Fatalf("OperandTarget(u): %v", err)
	}
	if target != x {
		t.Fatalf("OperandTarget(u) = %d, want %d", target, x)
	}

	isAdditive, err := logs.OperandIsAdditive(g, u)
	if err != nil {
		t.Fatalf("OperandIsAdditive(u): %v", err)
	}
	if !isAdditive {
		t.Fatal("OperandIsAdditive(u) = false, want true")
	}

	isSetOperand, err := logs.OperandIsSetOperand(g, u)
	if err != nil {
		t.Fatalf("OperandIsSetOperand(u): %v", err)
	}
	if isSetOperand {
		t.Fatal("OperandIsSetOperand(u) = true, want false")
	}

	got, err := logs.Evaluate(g, log)
	if err != nil {
		t.Fatalf("Evaluate(): %v", err)
	}
	want := []NodeID{x}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Evaluate() = %v, want %v", got, want)
	}

	ops, err := logs.Operations(g, log)
	if err != nil {
		t.Fatalf("Operations(): %v", err)
	}
	wantOps := []NodeID{u}
	if !reflect.DeepEqual(ops, wantOps) {
		t.Fatalf("Operations() = %v, want %v", ops, wantOps)
	}
}

// TestCompositeSetLogEvaluateOrderSensitiveFold pins down theorystate.md
// section 82's core distinguishing property from CompositeSetRegistry: a
// later operation mentioning a given element supersedes an earlier one,
// regardless of additive/subtractive grouping (unlike section 81's
// order-insensitive union-then-difference).
func TestCompositeSetLogEvaluateOrderSensitiveFold(t *testing.T) {
	g, _, _, logs := newCompositeSetLogTestFixture(t)

	log, err := logs.NewCompositeSetLog(g)
	if err != nil {
		t.Fatalf("NewCompositeSetLog(): %v", err)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	// Append x, remove it, then re-add it. If evaluation were order-
	// insensitive (union-then-difference, like CompositeSetRegistry),
	// the trailing additive mention would still need to win -- this
	// specifically checks the log respects append order rather than
	// grouping by operation kind first.
	if _, _, err2 := logs.AppendOperation(g, log, x, true, false); err2 != nil {
		t.Fatalf("AppendOperation(x, additive): %v", err2)
	}
	if _, _, err3 := logs.AppendOperation(g, log, x, false, false); err3 != nil {
		t.Fatalf("AppendOperation(x, subtractive): %v", err3)
	}

	got, err := logs.Evaluate(g, log)
	if err != nil {
		t.Fatalf("Evaluate() after add-then-remove: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Evaluate() = %v, want empty after add-then-remove", got)
	}

	if _, _, err4 := logs.AppendOperation(g, log, x, true, false); err4 != nil {
		t.Fatalf("AppendOperation(x, additive again): %v", err4)
	}

	got, err = logs.Evaluate(g, log)
	if err != nil {
		t.Fatalf("Evaluate() after re-add: %v", err)
	}
	want := []NodeID{x}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Evaluate() = %v, want %v after re-add", got, want)
	}
}

func TestCompositeSetLogAppendOperationRequiresKnownSetTagWhenExpand(t *testing.T) {
	g, _, _, logs := newCompositeSetLogTestFixture(t)

	log, err := logs.NewCompositeSetLog(g)
	if err != nil {
		t.Fatalf("NewCompositeSetLog(): %v", err)
	}

	notASet, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = logs.AppendOperation(g, log, notASet, true, true)
	if !errors.Is(err, ErrInvalidSetOperand) {
		t.Fatalf("AppendOperation() error = %v, want %v", err, ErrInvalidSetOperand)
	}
}

func TestCompositeSetLogEvaluateResolvesSetOperand(t *testing.T) {
	g, sets, _, logs := newCompositeSetLogTestFixture(t)

	set, err := sets.NewSet(g)
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}
	a, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err2 := sets.Add(g, set, a); err2 != nil {
		t.Fatal(err2)
	}

	log, err := logs.NewCompositeSetLog(g)
	if err != nil {
		t.Fatalf("NewCompositeSetLog(): %v", err)
	}

	if _, _, err3 := logs.AppendOperation(g, log, set, true, true); err3 != nil {
		t.Fatalf("AppendOperation(set, additive, expand): %v", err3)
	}

	got, err := logs.Evaluate(g, log)
	if err != nil {
		t.Fatalf("Evaluate(): %v", err)
	}
	want := []NodeID{a}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Evaluate() = %v, want %v", got, want)
	}
}

func TestCompositeSetLogEvaluateResolvesCompositeSetOperand(t *testing.T) {
	g, _, composites, logs := newCompositeSetLogTestFixture(t)

	composite, err := composites.NewCompositeSet(g)
	if err != nil {
		t.Fatalf("NewCompositeSet(): %v", err)
	}
	x, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err2 := composites.AddOperand(g, composite, x, true, false); err2 != nil {
		t.Fatal(err2)
	}

	log, err := logs.NewCompositeSetLog(g)
	if err != nil {
		t.Fatalf("NewCompositeSetLog(): %v", err)
	}

	if _, _, err3 := logs.AppendOperation(g, log, composite, true, true); err3 != nil {
		t.Fatalf("AppendOperation(composite, additive, expand): %v", err3)
	}

	got, err := logs.Evaluate(g, log)
	if err != nil {
		t.Fatalf("Evaluate(): %v", err)
	}
	want := []NodeID{x}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Evaluate() = %v, want %v", got, want)
	}
}

// TestCompositeSetLogEvaluateResolvesNestedCompositeSetLogOperand covers
// recursive self-type resolution: a CompositeSetLog operand nested inside
// another CompositeSetLog.
func TestCompositeSetLogEvaluateResolvesNestedCompositeSetLogOperand(t *testing.T) {
	g, _, _, logs := newCompositeSetLogTestFixture(t)

	inner, err := logs.NewCompositeSetLog(g)
	if err != nil {
		t.Fatalf("NewCompositeSetLog() for inner: %v", err)
	}
	x, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err2 := logs.AppendOperation(g, inner, x, true, false); err2 != nil {
		t.Fatal(err2)
	}

	outer, err := logs.NewCompositeSetLog(g)
	if err != nil {
		t.Fatalf("NewCompositeSetLog() for outer: %v", err)
	}
	if _, _, err3 := logs.AppendOperation(g, outer, inner, true, true); err3 != nil {
		t.Fatalf("AppendOperation(outer, inner, additive, expand): %v", err3)
	}

	got, err := logs.Evaluate(g, outer)
	if err != nil {
		t.Fatalf("Evaluate(outer): %v", err)
	}
	want := []NodeID{x}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Evaluate(outer) = %v, want %v", got, want)
	}
}

// TestCompositeSetRegistryResolvesCompositeSetLogOperand covers dispatch
// in the other direction: CompositeSetRegistry.Evaluate resolving a
// CompositeSetLog-kind operand, only possible once SetLogs has wired the
// two registries together.
func TestCompositeSetRegistryResolvesCompositeSetLogOperand(t *testing.T) {
	g, _, composites, logs := newCompositeSetLogTestFixture(t)

	log, err := logs.NewCompositeSetLog(g)
	if err != nil {
		t.Fatalf("NewCompositeSetLog(): %v", err)
	}
	x, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err2 := logs.AppendOperation(g, log, x, true, false); err2 != nil {
		t.Fatal(err2)
	}

	composite, err := composites.NewCompositeSet(g)
	if err != nil {
		t.Fatalf("NewCompositeSet(): %v", err)
	}
	if _, err3 := composites.AddOperand(g, composite, log, true, true); err3 != nil {
		t.Fatalf("AddOperand(composite, log, additive, expand): %v", err3)
	}

	got, err := composites.Evaluate(g, composite)
	if err != nil {
		t.Fatalf("Evaluate(composite): %v", err)
	}
	want := []NodeID{x}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Evaluate(composite) = %v, want %v", got, want)
	}
}

// TestCompositeSetRegistryWithoutSetLogsRejectsCompositeSetLogOperand
// pins down that SetLogs is required, not automatic: a
// CompositeSetRegistry that has never had SetLogs called must treat a
// CompositeSetLog-kind operand exactly like any other node carrying no
// recognized Set-representation tag.
func TestCompositeSetRegistryWithoutSetLogsRejectsCompositeSetLogOperand(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	ids, err := names.BootstrapNames(&g, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	sets, err := NewSetRegistry(&g, ids[NameAllSets], ids[NameAllCompositeSets], ids[NameAllCompositeSetLogs])
	if err != nil {
		t.Fatalf("NewSetRegistry(): %v", err)
	}

	composites, err := NewCompositeSetRegistry(
		&g,
		sets,
		ids[NameAllCompositeSets],
		ids[NameAllAdditiveOp],
		ids[NameAllSubtractiveOp],
		ids[NameAllScalarOperand],
		ids[NameAllSetOperand],
	)
	if err != nil {
		t.Fatalf("NewCompositeSetRegistry(): %v", err)
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

	lists, err := NewListRegistry(&g, capsules, ids[NameAllLists], ids[NameAllHeads], ids[NameAllTails])
	if err != nil {
		t.Fatalf("NewListRegistry(): %v", err)
	}

	logs, err := NewCompositeSetLogRegistry(
		&g,
		lists,
		composites,
		ids[NameAllCompositeSetLogs],
		ids[NameAllAdditiveOp],
		ids[NameAllSubtractiveOp],
		ids[NameAllScalarOperand],
		ids[NameAllSetOperand],
	)
	if err != nil {
		t.Fatalf("NewCompositeSetLogRegistry(): %v", err)
	}
	// Deliberately not calling composites.SetLogs(logs).

	log, err := logs.NewCompositeSetLog(&g)
	if err != nil {
		t.Fatalf("NewCompositeSetLog(): %v", err)
	}

	composite, err := composites.NewCompositeSet(&g)
	if err != nil {
		t.Fatalf("NewCompositeSet(): %v", err)
	}

	_, err = composites.AddOperand(&g, composite, log, true, true)
	if !errors.Is(err, ErrInvalidSetOperand) {
		t.Fatalf("AddOperand() error = %v, want %v", err, ErrInvalidSetOperand)
	}
}

func TestCompositeSetLogEvaluateDetectsCycle(t *testing.T) {
	g, _, _, logs := newCompositeSetLogTestFixture(t)

	l1, err := logs.NewCompositeSetLog(g)
	if err != nil {
		t.Fatalf("NewCompositeSetLog() for l1: %v", err)
	}
	l2, err := logs.NewCompositeSetLog(g)
	if err != nil {
		t.Fatalf("NewCompositeSetLog() for l2: %v", err)
	}

	if _, _, err2 := logs.AppendOperation(g, l1, l2, true, true); err2 != nil {
		t.Fatalf("AppendOperation(l1, l2): %v", err2)
	}
	if _, _, err3 := logs.AppendOperation(g, l2, l1, true, true); err3 != nil {
		t.Fatalf("AppendOperation(l2, l1): %v", err3)
	}

	_, err = logs.Evaluate(g, l1)
	if !errors.Is(err, ErrCompositeSetCycle) {
		t.Fatalf("Evaluate(l1) error = %v, want %v", err, ErrCompositeSetCycle)
	}
}

// TestCompositeSetLogEvaluateDetectsCrossRepresentationCycle covers a
// cycle crossing between CompositeSetLog and CompositeSet, per
// theorystate.md section 83's requirement that cycle tracking be shared
// across both composite-kind representations.
func TestCompositeSetLogEvaluateDetectsCrossRepresentationCycle(t *testing.T) {
	g, _, composites, logs := newCompositeSetLogTestFixture(t)

	log, err := logs.NewCompositeSetLog(g)
	if err != nil {
		t.Fatalf("NewCompositeSetLog(): %v", err)
	}
	composite, err := composites.NewCompositeSet(g)
	if err != nil {
		t.Fatalf("NewCompositeSet(): %v", err)
	}

	if _, _, err2 := logs.AppendOperation(g, log, composite, true, true); err2 != nil {
		t.Fatalf("AppendOperation(log, composite): %v", err2)
	}
	if _, err3 := composites.AddOperand(g, composite, log, true, true); err3 != nil {
		t.Fatalf("AddOperand(composite, log): %v", err3)
	}

	_, err = logs.Evaluate(g, log)
	if !errors.Is(err, ErrCompositeSetCycle) {
		t.Fatalf("Evaluate(log) error = %v, want %v", err, ErrCompositeSetCycle)
	}

	_, err = composites.Evaluate(g, composite)
	if !errors.Is(err, ErrCompositeSetCycle) {
		t.Fatalf("Evaluate(composite) error = %v, want %v", err, ErrCompositeSetCycle)
	}
}

func TestCompositeSetLogContainsMatchesEvaluate(t *testing.T) {
	g, _, _, logs := newCompositeSetLogTestFixture(t)

	log, err := logs.NewCompositeSetLog(g)
	if err != nil {
		t.Fatalf("NewCompositeSetLog(): %v", err)
	}

	a, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	b, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err2 := logs.AppendOperation(g, log, a, true, false); err2 != nil {
		t.Fatal(err2)
	}
	if _, _, err3 := logs.AppendOperation(g, log, b, true, false); err3 != nil {
		t.Fatal(err3)
	}
	if _, _, err4 := logs.AppendOperation(g, log, a, false, false); err4 != nil {
		t.Fatal(err4)
	}

	got, err := logs.Contains(g, log, a)
	if err != nil {
		t.Fatalf("Contains(log, a): %v", err)
	}
	if got {
		t.Fatal("Contains(log, a) = true, want false (a was subtracted last)")
	}

	got, err = logs.Contains(g, log, b)
	if err != nil {
		t.Fatalf("Contains(log, b): %v", err)
	}
	if !got {
		t.Fatal("Contains(log, b) = false, want true")
	}

	evaluated, err := logs.Evaluate(g, log)
	if err != nil {
		t.Fatalf("Evaluate(log): %v", err)
	}
	want := []NodeID{b}
	if !reflect.DeepEqual(evaluated, want) {
		t.Fatalf("Evaluate(log) = %v, want %v", evaluated, want)
	}
}

// outgoingReadCounter counts FindOutgoing calls for one watched node.
// Embedding the GraphReader interface hides any optional pager or
// common-children lookup the wrapped value has.
type outgoingReadCounter struct {
	GraphReader
	watched NodeID
	calls   int
}

func (r *outgoingReadCounter) FindOutgoing(from NodeID) ([]Relationship, error) {
	if from == r.watched {
		r.calls++
	}

	relationships, err := r.GraphReader.FindOutgoing(from)

	return relationships, wrapInterfaceErr(err)
}

// TestCompositeSetLogContainsAgreesWithEvaluateForEveryNode mixes scalar and
// set operands, a composite, subtractions and a re-add, and checks Contains
// against Evaluate for every node.
func TestCompositeSetLogContainsAgreesWithEvaluateForEveryNode(t *testing.T) {
	g, sets, composites, logs := newCompositeSetLogTestFixture(t)

	a := newTestNode(t, g)
	b := newTestNode(t, g)
	c := newTestNode(t, g)
	d := newTestNode(t, g)
	e := newTestNode(t, g)
	f := newTestNode(t, g)

	set, err := sets.NewSet(g)
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	for _, member := range []NodeID{b, d} {
		if _, addErr := sets.Add(g, set, member); addErr != nil {
			t.Fatalf("Add(set, %d): %v", member, addErr)
		}
	}

	// composite = {e} minus {d} = {e}
	composite, err := composites.NewCompositeSet(g)
	if err != nil {
		t.Fatalf("NewCompositeSet(): %v", err)
	}

	if _, operandErr := composites.AddOperand(g, composite, e, true, false); operandErr != nil {
		t.Fatalf("AddOperand(e): %v", operandErr)
	}

	if _, operandErr := composites.AddOperand(g, composite, d, false, false); operandErr != nil {
		t.Fatalf("AddOperand(d, subtractive): %v", operandErr)
	}

	log, err := logs.NewCompositeSetLog(g)
	if err != nil {
		t.Fatalf("NewCompositeSetLog(): %v", err)
	}

	// {a} -> {a,b,d} -> {a,d} -> {a,d,e} -> {a,e} -> {a,b,e}
	for _, op := range []struct {
		operand          NodeID
		additive, expand bool
	}{
		{operand: a, additive: true},
		{operand: set, additive: true, expand: true},
		{operand: b},
		{operand: composite, additive: true, expand: true},
		{operand: set, expand: true},
		{operand: b, additive: true},
	} {
		if _, _, appendErr := logs.AppendOperation(g, log, op.operand, op.additive, op.expand); appendErr != nil {
			t.Fatalf("AppendOperation(%d): %v", op.operand, appendErr)
		}
	}

	evaluated, err := logs.Evaluate(g, log)
	if err != nil {
		t.Fatalf("Evaluate(): %v", err)
	}

	if want := sortedNodeIDs([]NodeID{a, b, e}); !reflect.DeepEqual(sortedNodeIDs(evaluated), want) {
		t.Fatalf("Evaluate() = %v, want %v", evaluated, want)
	}

	for _, node := range []NodeID{a, b, c, d, e, f} {
		got, containsErr := logs.Contains(g, log, node)
		if containsErr != nil {
			t.Fatalf("Contains(%d): %v", node, containsErr)
		}

		if want := slices.Contains(evaluated, node); got != want {
			t.Fatalf("Contains(%d) = %v, want %v (Evaluate() = %v)", node, got, want, evaluated)
		}
	}
}

// TestCompositeSetLogContainsOfAnUnmentionedValueDoesNotReadTheLog: the
// candidates come from a reverse lookup from the value, so a value nothing
// mentions never makes Contains read the log's own children (Elements, which
// it used to call, does).
func TestCompositeSetLogContainsOfAnUnmentionedValueDoesNotReadTheLog(t *testing.T) {
	g, _, _, logs := newCompositeSetLogTestFixture(t)

	log, err := logs.NewCompositeSetLog(g)
	if err != nil {
		t.Fatalf("NewCompositeSetLog(): %v", err)
	}

	a := newTestNode(t, g)
	b := newTestNode(t, g)
	unmentioned := newTestNode(t, g)

	for _, operand := range []NodeID{a, b} {
		if _, _, appendErr := logs.AppendOperation(g, log, operand, true, false); appendErr != nil {
			t.Fatalf("AppendOperation(%d): %v", operand, appendErr)
		}
	}

	reads := &outgoingReadCounter{GraphReader: g, watched: log}

	got, err := logs.Contains(reads, log, unmentioned)
	if err != nil || got {
		t.Fatalf("Contains(unmentioned) = (%v,%v), want (false,nil)", got, err)
	}

	if reads.calls != 0 {
		t.Fatalf("Contains(unmentioned) read the children of the log %d times, want 0", reads.calls)
	}

	got, err = logs.Contains(reads, log, b)
	if err != nil || !got {
		t.Fatalf("Contains(b) = (%v,%v), want (true,nil)", got, err)
	}
}

func TestCompositeSetLogContainsRequiresExistingValue(t *testing.T) {
	g, _, _, logs := newCompositeSetLogTestFixture(t)

	log, err := logs.NewCompositeSetLog(g)
	if err != nil {
		t.Fatalf("NewCompositeSetLog(): %v", err)
	}

	const nonexistent NodeID = 999999
	if _, err := logs.Contains(g, log, nonexistent); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Contains() error = %v, want %v", err, ErrNodeNotFound)
	}
}

func TestCompositeSetLogRemoveOperationDeletesDescriptorAndCapsule(t *testing.T) {
	g, _, _, logs := newCompositeSetLogTestFixture(t)

	log, err := logs.NewCompositeSetLog(g)
	if err != nil {
		t.Fatalf("NewCompositeSetLog(): %v", err)
	}
	x, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	u, capsule, err := logs.AppendOperation(g, log, x, true, false)
	if err != nil {
		t.Fatalf("AppendOperation(): %v", err)
	}

	if err2 := logs.RemoveOperation(g, log, capsule); err2 != nil {
		t.Fatalf("RemoveOperation(): %v", err2)
	}

	if mustNodeExists(t, g, u) {
		t.Fatalf("descriptor %d still exists after RemoveOperation()", u)
	}
	if mustNodeExists(t, g, capsule) {
		t.Fatalf("capsule %d still exists after RemoveOperation()", capsule)
	}
	if !mustNodeExists(t, g, x) {
		t.Fatal("RemoveOperation() incorrectly deleted the operand target")
	}

	got, err := logs.Evaluate(g, log)
	if err != nil {
		t.Fatalf("Evaluate(): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Evaluate() = %v, want empty after RemoveOperation()", got)
	}
}

func TestCompositeSetLogRemoveOperationRequiresOperationInLog(t *testing.T) {
	g, _, _, logs := newCompositeSetLogTestFixture(t)

	logA, err := logs.NewCompositeSetLog(g)
	if err != nil {
		t.Fatal(err)
	}
	logB, err := logs.NewCompositeSetLog(g)
	if err != nil {
		t.Fatal(err)
	}
	x, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	_, capsule, err := logs.AppendOperation(g, logA, x, true, false)
	if err != nil {
		t.Fatal(err)
	}

	err = logs.RemoveOperation(g, logB, capsule)
	if !errors.Is(err, ErrCapsuleNotInList) {
		t.Fatalf("RemoveOperation() error = %v, want %v", err, ErrCapsuleNotInList)
	}
}

func TestCompositeSetLogDeleteCompositeSetLogSucceedsWhenEmpty(t *testing.T) {
	g, _, _, logs := newCompositeSetLogTestFixture(t)

	log, err := logs.NewCompositeSetLog(g)
	if err != nil {
		t.Fatal(err)
	}

	if err := logs.DeleteCompositeSetLog(g, log); err != nil {
		t.Fatalf("DeleteCompositeSetLog(): %v", err)
	}
	if mustNodeExists(t, g, log) {
		t.Fatalf("log %d still exists after successful DeleteCompositeSetLog()", log)
	}
}

func TestCompositeSetLogDeleteCompositeSetLogFailsIfNotEmpty(t *testing.T) {
	g, _, _, logs := newCompositeSetLogTestFixture(t)

	log, err := logs.NewCompositeSetLog(g)
	if err != nil {
		t.Fatal(err)
	}
	x, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err2 := logs.AppendOperation(g, log, x, true, false); err2 != nil {
		t.Fatal(err2)
	}

	err = logs.DeleteCompositeSetLog(g, log)
	if !errors.Is(err, ErrNodeNotEmpty) {
		t.Fatalf("DeleteCompositeSetLog() error = %v, want %v", err, ErrNodeNotEmpty)
	}
	if !mustNodeExists(t, g, log) {
		t.Fatal("log disappeared despite a failed DeleteCompositeSetLog()")
	}
	if !mustIsCompositeSetLog(t, logs, g, log) {
		t.Fatal("log lost its AllCompositeSetLogs tag despite a failed DeleteCompositeSetLog()")
	}
	if !mustIsList(t, logs.lists, g, log) {
		t.Fatal("log lost its AllLists tag despite a failed DeleteCompositeSetLog()")
	}
}

func TestCompositeSetLogOperationsRequireCompositeSetLogTag(t *testing.T) {
	g, _, _, logs := newCompositeSetLogTestFixture(t)

	notALog, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	x, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := logs.AppendOperation(g, notALog, x, true, false); !errors.Is(err, ErrNotCompositeSetLog) {
		t.Fatalf("AppendOperation() error = %v, want %v", err, ErrNotCompositeSetLog)
	}
	if _, err := logs.Operations(g, notALog); !errors.Is(err, ErrNotCompositeSetLog) {
		t.Fatalf("Operations() error = %v, want %v", err, ErrNotCompositeSetLog)
	}
	if _, err := logs.Evaluate(g, notALog); !errors.Is(err, ErrNotCompositeSetLog) {
		t.Fatalf("Evaluate() error = %v, want %v", err, ErrNotCompositeSetLog)
	}
	if _, err := logs.Contains(g, notALog, x); !errors.Is(err, ErrNotCompositeSetLog) {
		t.Fatalf("Contains() error = %v, want %v", err, ErrNotCompositeSetLog)
	}
	if err := logs.DeleteCompositeSetLog(g, notALog); !errors.Is(err, ErrNotCompositeSetLog) {
		t.Fatalf("DeleteCompositeSetLog() error = %v, want %v", err, ErrNotCompositeSetLog)
	}
	if err := logs.RemoveOperation(g, notALog, x); !errors.Is(err, ErrNotCompositeSetLog) {
		t.Fatalf("RemoveOperation() error = %v, want %v", err, ErrNotCompositeSetLog)
	}
}

// TestCompositeSetLogEvaluateDetectsMalformedDescriptor covers bypassing
// AppendOperation entirely by calling the underlying ListRegistry.Append
// directly, producing a capsule whose value carries no descriptor tags at
// all -- Evaluate must fail loudly rather than guess a default operation
// kind.
func TestCompositeSetLogEvaluateDetectsMalformedDescriptor(t *testing.T) {
	g, _, _, logs := newCompositeSetLogTestFixture(t)

	log, err := logs.NewCompositeSetLog(g)
	if err != nil {
		t.Fatal(err)
	}
	x, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	if _, err2 := logs.lists.Append(g, log, x); err2 != nil {
		t.Fatalf("Append() bypassing AppendOperation: %v", err2)
	}

	_, err = logs.Evaluate(g, log)
	if !errors.Is(err, ErrInvalidOperandDescriptor) {
		t.Fatalf("Evaluate() error = %v, want %v", err, ErrInvalidOperandDescriptor)
	}
}

// TestCompositeSetLogRegistrySharesListStructureChecker demonstrates that
// CompositeSetLogRegistry, despite registering no Checker of its own
// (see NewCompositeSetLogRegistry's doc comment), still gets its
// underlying List structure validated eagerly at commit time -- for
// free, via the ListRegistry Checker registered when this fixture's
// ListRegistry was itself constructed. A CompositeSetLog is dual-tagged
// (AllLists,node) and (AllCompositeSetLogs,node), and the ListRegistry
// Checker fires on any touched node carrying AllLists regardless of
// which registry happened to touch it.
func TestCompositeSetLogRegistrySharesListStructureChecker(t *testing.T) {
	g, _, _, logs := newCompositeSetLogTestFixture(t)

	log, err := logs.NewCompositeSetLog(g)
	if err != nil {
		t.Fatalf("NewCompositeSetLog(): %v", err)
	}

	bogus, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for bogus: %v", err)
	}

	listRegistry, isListRegistry := logs.lists.(*ListRegistry)
	if !isListRegistry {
		t.Fatalf("the fixture's log lists are a %T, want a *ListRegistry", logs.lists)
	}

	err = g.Transact(func(tx Tx) error {
		if err2 := addRelationshipTx(tx, log, bogus); err2 != nil {
			return err2
		}
		return addRelationshipTx(tx, listRegistry.allHeads, bogus)
	})

	if !errors.Is(err, ErrInvalidListStructure) {
		t.Fatalf("Transact() error = %v, want %v", err, ErrInvalidListStructure)
	}
}

// TestCompositeSetLogRegistrySharesOperandDescriptorChecker demonstrates
// that CompositeSetLogRegistry's own logged-operation descriptors are
// also validated eagerly at commit time, again with no Checker of
// CompositeSetLogRegistry's own -- this time via the operand-descriptor
// Checker registered by NewCompositeSetRegistry, which is keyed on the
// shared axis tags themselves (theorystate.md section 80) rather than on
// AllCompositeSets, specifically so it also reaches descriptors that are
// never children of a composite-set node at all, as is always the case
// for a CompositeSetLog's descriptors (they are list-capsule values
// instead). Appending a pre-existing, already-malformed descriptor as an
// ordinary ListRegistry value -- not going through AppendOperation at
// all -- still touches that descriptor as part of wiring the new
// capsule's value slot, which is enough for the shared Checker to fire.
func TestCompositeSetLogRegistrySharesOperandDescriptorChecker(t *testing.T) {
	g, _, composites, logs := newCompositeSetLogTestFixture(t)

	log, err := logs.NewCompositeSetLog(g)
	if err != nil {
		t.Fatalf("NewCompositeSetLog(): %v", err)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for x: %v", err)
	}

	u, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for u: %v", err)
	}
	if _, err2 := g.AddRelationship(composites.allAdditiveOp, u); err2 != nil {
		t.Fatalf("AddRelationship(allAdditiveOp, u): %v", err2)
	}
	if _, err3 := g.AddRelationship(composites.allSubtractiveOp, u); err3 != nil {
		t.Fatalf("AddRelationship(allSubtractiveOp, u): %v", err3)
	}
	if _, err4 := g.AddRelationship(composites.allScalarOperand, u); err4 != nil {
		t.Fatalf("AddRelationship(allScalarOperand, u): %v", err4)
	}
	if _, err5 := g.AddRelationship(u, x); err5 != nil {
		t.Fatalf("AddRelationship(u, x): %v", err5)
	}

	_, err = logs.lists.Append(g, log, u)
	if !errors.Is(err, ErrInvalidOperandDescriptor) {
		t.Fatalf("Append() error = %v, want %v", err, ErrInvalidOperandDescriptor)
	}

	elements, err := logs.lists.Elements(g, log)
	if err != nil {
		t.Fatalf("Elements(log): %v", err)
	}
	if len(elements) != 0 {
		t.Fatalf("Elements(log) = %v, want empty after the Checker declined the commit", elements)
	}
}

// TestSetRegistryTagAsSetRejectsCompositeSetLogConflict pins down
// theorystate.md section 79's mutual-exclusivity rule for the third and
// final Set-representation tag: SetRegistry.TagAsSet must refuse to tag
// a node already tagged AllCompositeSetLogs.
func TestSetRegistryTagAsSetRejectsCompositeSetLogConflict(t *testing.T) {
	g, sets, _, logs := newCompositeSetLogTestFixture(t)

	log, err := logs.NewCompositeSetLog(g)
	if err != nil {
		t.Fatalf("NewCompositeSetLog(): %v", err)
	}

	err = sets.TagAsSet(g, log)
	if !errors.Is(err, ErrSetRepresentationConflict) {
		t.Fatalf("TagAsSet() error = %v, want %v", err, ErrSetRepresentationConflict)
	}

	if mustIsSet(t, sets, g, log) {
		t.Fatal("node was tagged AllSets despite already being AllCompositeSetLogs-tagged")
	}
}

// TestSetAddAndRemoveAreVisibleToCommitTimeCheckers pins down that
// SetRegistry.Add/Remove run through Graph.Transact: a Checker keyed on
// the AllSets tag can veto either, the change is rolled back, and the
// reported bool is false.
func TestSetAddAndRemoveAreVisibleToCommitTimeCheckers(t *testing.T) {
	g, sets := newSetTestFixture(t)

	set, err := sets.NewSet(g)
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}
	member := newTestNode(t, g)

	errVeto := errors.New("veto")
	veto := false

	g.RegisterChecker(Checker{
		Name: "veto",
		Tags: []NodeID{sets.allSets},
		Check: func(_ GraphReader, _ map[NodeID]struct{}) error {
			if veto {
				return errVeto
			}

			return nil
		},
	})

	veto = true
	added, err := sets.Add(g, set, member)
	if !errors.Is(err, errVeto) {
		t.Fatalf("Add() error = %v, want %v", err, errVeto)
	}
	if added {
		t.Fatal("Add() reported added=true for a vetoed commit")
	}
	if mustHasRelationship(t, g, set, member) {
		t.Fatal("member is present after a vetoed Add()")
	}

	veto = false
	if _, addErr := sets.Add(g, set, member); addErr != nil {
		t.Fatalf("Add() without veto: %v", addErr)
	}

	veto = true
	removed, err := sets.Remove(g, set, member)
	if !errors.Is(err, errVeto) {
		t.Fatalf("Remove() error = %v, want %v", err, errVeto)
	}
	if removed {
		t.Fatal("Remove() reported removed=true for a vetoed commit")
	}
	if !mustHasRelationship(t, g, set, member) {
		t.Fatal("member is missing after a vetoed Remove()")
	}
}

func TestSetMutatorsComposeInsideOneTransactionAndRollBackTogether(t *testing.T) {
	g, sets := newSetTestFixture(t)

	member := newTestNode(t, g)
	errOuter := errors.New("outer failure")

	var set NodeID

	err := g.Transact(func(tx Tx) error {
		var newErr error
		set, newErr = sets.NewSet(tx)
		if newErr != nil {
			return newErr
		}

		_, addErr := sets.Add(tx, set, member)

		return addErr
	})
	if err != nil {
		t.Fatalf("Transact(compose) error = %v", err)
	}

	if found, containsErr := sets.Contains(g, set, member); containsErr != nil || !found {
		t.Fatalf("Contains() = (%v,%v), want (true,nil)", found, containsErr)
	}

	err = g.Transact(func(tx Tx) error {
		if _, removeErr := sets.Remove(tx, set, member); removeErr != nil {
			return removeErr
		}
		if deleteErr := sets.DeleteSet(tx, set); deleteErr != nil {
			return deleteErr
		}

		return errOuter
	})
	if !errors.Is(err, errOuter) {
		t.Fatalf("Transact(fail) error = %v, want %v", err, errOuter)
	}

	if !mustNodeExists(t, g, set) || !mustIsSet(t, sets, g, set) {
		t.Fatal("the set was not restored after its deleting transaction rolled back")
	}
	if found, containsErr := sets.Contains(g, set, member); containsErr != nil || !found {
		t.Fatalf("Contains() = (%v,%v) after rollback, want (true,nil)", found, containsErr)
	}
}

func TestCompositeSetLogNestedRemoveOperationFailureLeavesLogIntactAndOuterContinues(t *testing.T) {
	g, _, _, logs := newCompositeSetLogTestFixture(t)

	log, err := logs.NewCompositeSetLog(g)
	if err != nil {
		t.Fatalf("NewCompositeSetLog(): %v", err)
	}

	x := newTestNode(t, g)
	y := newTestNode(t, g)
	extra := newTestNode(t, g)

	var u, uY NodeID

	err = g.Transact(func(tx Tx) error {
		var capsule NodeID

		var appendErr error
		u, capsule, appendErr = logs.AppendOperation(tx, log, x, true, false)
		if appendErr != nil {
			return appendErr
		}

		capsules := logs.lists.Capsules()

		valueSlot, found, slotErr := capsules.slotFor(tx, capsule, capsules.valueSlots.allPointers)
		if slotErr != nil {
			return slotErr
		}
		if !found {
			return ErrNotCapsule
		}

		// Something unrelated referencing the value slot makes deleting
		// the capsule unsafe, so the nested RemoveOperation must fail and
		// undo only itself.
		if linkErr := addRelationshipTx(tx, extra, valueSlot); linkErr != nil {
			return linkErr
		}

		if removeErr := logs.RemoveOperation(tx, log, capsule); !errors.Is(removeErr, ErrCapsuleNotEmpty) {
			t.Errorf("nested RemoveOperation() error = %v, want %v", removeErr, ErrCapsuleNotEmpty)
		}

		var appendYErr error
		uY, _, appendYErr = logs.AppendOperation(tx, log, y, true, false)

		return appendYErr
	})
	if err != nil {
		t.Fatalf("Transact() error = %v", err)
	}

	operations, err := logs.Operations(g, log)
	if err != nil {
		t.Fatalf("Operations(): %v", err)
	}
	if want := []NodeID{u, uY}; !reflect.DeepEqual(operations, want) {
		t.Fatalf("Operations() = %v, want %v", operations, want)
	}

	evaluated, err := logs.Evaluate(g, log)
	if err != nil {
		t.Fatalf("Evaluate(): %v", err)
	}
	if want := sortedNodeIDs([]NodeID{x, y}); !reflect.DeepEqual(sortedNodeIDs(evaluated), want) {
		t.Fatalf("Evaluate() = %v, want %v", evaluated, want)
	}
}
func TestCompositeSetLogRemoveOperationIsAtomicWhenCapsuleCannotBeDeleted(t *testing.T) {
	g, _, _, logs := newCompositeSetLogTestFixture(t)

	log, err := logs.NewCompositeSetLog(g)
	if err != nil {
		t.Fatalf("NewCompositeSetLog(): %v", err)
	}

	x := newTestNode(t, g)

	u, capsule, err := logs.AppendOperation(g, log, x, true, false)
	if err != nil {
		t.Fatalf("AppendOperation(): %v", err)
	}

	capsules := logs.lists.Capsules()

	valueSlot, found, err := capsules.slotFor(g, capsule, capsules.valueSlots.allPointers)
	if err != nil || !found {
		t.Fatalf("slotFor(value): found=%v err=%v", found, err)
	}

	// Something unrelated referencing the value slot makes deleting the
	// capsule unsafe.
	extra := newTestNode(t, g)
	if _, addErr := g.AddRelationship(extra, valueSlot); addErr != nil {
		t.Fatalf("AddRelationship(extra, valueSlot): %v", addErr)
	}

	err = logs.RemoveOperation(g, log, capsule)
	if !errors.Is(err, ErrCapsuleNotEmpty) {
		t.Fatalf("RemoveOperation() error = %v, want %v", err, ErrCapsuleNotEmpty)
	}

	if !mustHasRelationship(t, g, log, capsule) {
		t.Fatal("capsule was unlinked from the log despite the failed RemoveOperation()")
	}
	if !mustNodeExists(t, g, u) || !mustHasRelationship(t, g, u, x) {
		t.Fatal("descriptor was disturbed by the failed RemoveOperation()")
	}

	operations, err := logs.Operations(g, log)
	if err != nil {
		t.Fatalf("Operations(): %v", err)
	}
	if want := []NodeID{u}; !reflect.DeepEqual(operations, want) {
		t.Fatalf("Operations() = %v, want %v", operations, want)
	}
}
