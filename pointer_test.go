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

// newPointerTestFixture creates a fresh graph (of the active test backend, see
// newTestGraph) and a PointerRegistry with AllPointers already bootstrapped,
// for use by PointerRegistry tests.
func newPointerTestFixture(t *testing.T) (testGraph, *PointerRegistry) {
	t.Helper()

	g := newTestGraph(t)
	names := NewNameRegistry(g)

	allPointers, err := names.EnsureNamedNode(g, NameAllPointers)
	if err != nil {
		t.Fatalf("EnsureNamedNode(%q): %v", NameAllPointers, err)
	}

	pointers, err := NewPointerRegistry(g, allPointers)
	if err != nil {
		t.Fatalf("NewPointerRegistry(): %v", err)
	}

	return g, pointers
}

func TestNewPointerRegistryRequiresExistingAllPointers(t *testing.T) {
	var g Graph

	const nonexistent NodeID = 999999

	_, err := NewPointerRegistry(&g, nonexistent)
	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("NewPointerRegistry() error = %v, want %v", err, ErrNodeNotFound)
	}
}

func TestPointerRegistryNewPointerStartsEmpty(t *testing.T) {
	g, pointers := newPointerTestFixture(t)

	p, err := pointers.NewPointer(g)
	if err != nil {
		t.Fatalf("NewPointer(): %v", err)
	}

	if !mustNodeExists(t, g, p) {
		t.Fatalf("NewPointer() returned NodeID %d that does not exist", p)
	}

	if !mustIsPointer(t, pointers, g, p) {
		t.Fatalf("NewPointer() did not tag %d as Pointer-kind", p)
	}

	_, hasTarget, err := pointers.Target(g, p)
	if err != nil {
		t.Fatalf("Target(%d): %v", p, err)
	}

	if hasTarget {
		t.Fatalf("freshly created pointer %d unexpectedly has a target", p)
	}
}

func TestPointerRegistrySetTargetAddsFirstTarget(t *testing.T) {
	g, pointers := newPointerTestFixture(t)

	p, err := pointers.NewPointer(g)
	if err != nil {
		t.Fatalf("NewPointer(): %v", err)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for x: %v", err)
	}

	if err2 := pointers.SetTarget(g, p, x); err2 != nil {
		t.Fatalf("SetTarget(%d,%d): %v", p, x, err2)
	}

	target, hasTarget, err := pointers.Target(g, p)
	if err != nil {
		t.Fatalf("Target(%d): %v", p, err)
	}

	if !hasTarget || target != x {
		t.Fatalf("Target(%d) = (%d,%v), want (%d,true)", p, target, hasTarget, x)
	}
}

func TestPointerRegistrySetTargetIsIdempotentForSameTarget(t *testing.T) {
	g, pointers := newPointerTestFixture(t)

	p, err := pointers.NewPointer(g)
	if err != nil {
		t.Fatalf("NewPointer(): %v", err)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for x: %v", err)
	}

	if err2 := pointers.SetTarget(g, p, x); err2 != nil {
		t.Fatalf("first SetTarget(%d,%d): %v", p, x, err2)
	}

	if err3 := pointers.SetTarget(g, p, x); err3 != nil {
		t.Fatalf("second SetTarget(%d,%d): %v", p, x, err3)
	}

	outgoing, err := g.FindOutgoing(p)
	if err != nil {
		t.Fatalf("FindOutgoing(%d): %v", p, err)
	}

	if len(outgoing) != 1 {
		t.Fatalf("FindOutgoing(%d) = %v, want exactly one relationship", p, outgoing)
	}
}

func TestPointerRegistrySetTargetReplacesExistingTarget(t *testing.T) {
	g, pointers := newPointerTestFixture(t)

	p, err := pointers.NewPointer(g)
	if err != nil {
		t.Fatalf("NewPointer(): %v", err)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for x: %v", err)
	}

	y, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for y: %v", err)
	}

	if err2 := pointers.SetTarget(g, p, x); err2 != nil {
		t.Fatalf("SetTarget(%d,%d): %v", p, x, err2)
	}

	if err3 := pointers.SetTarget(g, p, y); err3 != nil {
		t.Fatalf("SetTarget(%d,%d): %v", p, y, err3)
	}

	target, hasTarget, err := pointers.Target(g, p)
	if err != nil {
		t.Fatalf("Target(%d): %v", p, err)
	}

	if !hasTarget || target != y {
		t.Fatalf("Target(%d) = (%d,%v), want (%d,true)", p, target, hasTarget, y)
	}

	if mustHasRelationship(t, g, p, x) {
		t.Fatalf("old target relationship (%d,%d) was not removed", p, x)
	}

	outgoing, err := g.FindOutgoing(p)
	if err != nil {
		t.Fatalf("FindOutgoing(%d): %v", p, err)
	}

	if len(outgoing) != 1 {
		t.Fatalf("FindOutgoing(%d) = %v, want exactly one relationship after replacement", p, outgoing)
	}
}

func TestPointerRegistrySetTargetAllowsSelfTarget(t *testing.T) {
	g, pointers := newPointerTestFixture(t)

	p, err := pointers.NewPointer(g)
	if err != nil {
		t.Fatalf("NewPointer(): %v", err)
	}

	if err2 := pointers.SetTarget(g, p, p); err2 != nil {
		t.Fatalf("SetTarget(%d,%d) self-target: %v", p, p, err2)
	}

	target, hasTarget, err := pointers.Target(g, p)
	if err != nil {
		t.Fatalf("Target(%d): %v", p, err)
	}

	if !hasTarget || target != p {
		t.Fatalf("Target(%d) = (%d,%v), want (%d,true)", p, target, hasTarget, p)
	}
}

func TestPointerRegistrySetTargetRequiresExistingTarget(t *testing.T) {
	g, pointers := newPointerTestFixture(t)

	p, err := pointers.NewPointer(g)
	if err != nil {
		t.Fatalf("NewPointer(): %v", err)
	}

	const nonexistent NodeID = 999999

	err = pointers.SetTarget(g, p, nonexistent)
	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("SetTarget() error = %v, want %v", err, ErrNodeNotFound)
	}
}

func TestPointerRegistrySetTargetRequiresPointerTag(t *testing.T) {
	g, pointers := newPointerTestFixture(t)

	id, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for x: %v", err)
	}

	err = pointers.SetTarget(g, id, x)
	if !errors.Is(err, ErrNotPointer) {
		t.Fatalf("SetTarget() error = %v, want %v", err, ErrNotPointer)
	}
}

func TestPointerRegistryRemoveTargetRemovesExisting(t *testing.T) {
	g, pointers := newPointerTestFixture(t)

	p, err := pointers.NewPointer(g)
	if err != nil {
		t.Fatalf("NewPointer(): %v", err)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for x: %v", err)
	}

	if err2 := pointers.SetTarget(g, p, x); err2 != nil {
		t.Fatalf("SetTarget(%d,%d): %v", p, x, err2)
	}

	removed, err := pointers.RemoveTarget(g, p)
	if err != nil {
		t.Fatalf("RemoveTarget(%d): %v", p, err)
	}

	if !removed {
		t.Fatal("RemoveTarget() reported that nothing was removed")
	}

	_, hasTarget, err := pointers.Target(g, p)
	if err != nil {
		t.Fatalf("Target(%d): %v", p, err)
	}

	if hasTarget {
		t.Fatalf("pointer %d still has a target after RemoveTarget()", p)
	}
}

func TestPointerRegistryRemoveTargetNoOpWhenEmpty(t *testing.T) {
	g, pointers := newPointerTestFixture(t)

	p, err := pointers.NewPointer(g)
	if err != nil {
		t.Fatalf("NewPointer(): %v", err)
	}

	removed, err := pointers.RemoveTarget(g, p)
	if err != nil {
		t.Fatalf("RemoveTarget(%d): %v", p, err)
	}

	if removed {
		t.Fatal("RemoveTarget() reported removal of a nonexistent target")
	}
}

func TestPointerRegistryTagAsPointerTagsFreshNode(t *testing.T) {
	g, pointers := newPointerTestFixture(t)

	id, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	if mustIsPointer(t, pointers, g, id) {
		t.Fatalf("node %d is unexpectedly already tagged Pointer-kind", id)
	}

	if err := pointers.TagAsPointer(g, id); err != nil {
		t.Fatalf("TagAsPointer(%d): %v", id, err)
	}

	if !mustIsPointer(t, pointers, g, id) {
		t.Fatalf("TagAsPointer(%d) did not tag the node", id)
	}
}

func TestPointerRegistryTagAsPointerAllowsExistingSingleChild(t *testing.T) {
	g, pointers := newPointerTestFixture(t)

	id, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for x: %v", err)
	}

	if _, err2 := g.AddRelationship(id, x); err2 != nil {
		t.Fatalf("AddRelationship(%d,%d): %v", id, x, err2)
	}

	if err3 := pointers.TagAsPointer(g, id); err3 != nil {
		t.Fatalf("TagAsPointer(%d): %v", id, err3)
	}

	target, hasTarget, err := pointers.Target(g, id)
	if err != nil {
		t.Fatalf("Target(%d): %v", id, err)
	}

	if !hasTarget || target != x {
		t.Fatalf("Target(%d) = (%d,%v), want (%d,true)", id, target, hasTarget, x)
	}
}

func TestPointerRegistryTagAsPointerRejectsMultipleExistingChildren(t *testing.T) {
	g, pointers := newPointerTestFixture(t)

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
		t.Fatalf("AddRelationship(%d,%d): %v", id, x, err2)
	}

	if _, err3 := g.AddRelationship(id, y); err3 != nil {
		t.Fatalf("AddRelationship(%d,%d): %v", id, y, err3)
	}

	err = pointers.TagAsPointer(g, id)
	if !errors.Is(err, ErrTooManyPointerTargets) {
		t.Fatalf("TagAsPointer() error = %v, want %v", err, ErrTooManyPointerTargets)
	}

	if mustIsPointer(t, pointers, g, id) {
		t.Fatalf("node %d was tagged despite violating the Pointer invariant", id)
	}
}

func TestPointerRegistryTagAsPointerIsIdempotent(t *testing.T) {
	g, pointers := newPointerTestFixture(t)

	id, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	if err := pointers.TagAsPointer(g, id); err != nil {
		t.Fatalf("first TagAsPointer(%d): %v", id, err)
	}

	if err := pointers.TagAsPointer(g, id); err != nil {
		t.Fatalf("second TagAsPointer(%d): %v", id, err)
	}
}

func TestPointerRegistryDetectsOutOfBandInvariantViolation(t *testing.T) {
	g, pointers := newPointerTestFixture(t)

	p, err := pointers.NewPointer(g)
	if err != nil {
		t.Fatalf("NewPointer(): %v", err)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for x: %v", err)
	}

	y, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for y: %v", err)
	}

	// Bypass PointerRegistry entirely, simulating a caller bug that
	// mutates a tagged Pointer node directly through the primitive Graph.
	if _, err2 := g.AddRelationship(p, x); err2 != nil {
		t.Fatalf("AddRelationship(%d,%d) via raw Graph: %v", p, x, err2)
	}

	if _, err3 := g.AddRelationship(p, y); err3 != nil {
		t.Fatalf("AddRelationship(%d,%d) via raw Graph: %v", p, y, err3)
	}

	if _, _, err4 := pointers.Target(g, p); !errors.Is(err4, ErrTooManyPointerTargets) {
		t.Fatalf("Target() error = %v, want %v", err4, ErrTooManyPointerTargets)
	}

	z, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for z: %v", err)
	}

	if err5 := pointers.SetTarget(g, p, z); !errors.Is(err5, ErrTooManyPointerTargets) {
		t.Fatalf("SetTarget() error = %v, want %v", err5, ErrTooManyPointerTargets)
	}

	if _, err6 := pointers.RemoveTarget(g, p); !errors.Is(err6, ErrTooManyPointerTargets) {
		t.Fatalf("RemoveTarget() error = %v, want %v", err6, ErrTooManyPointerTargets)
	}

	// Confirm none of the failed calls above mutated anything.
	outgoing, err := g.FindOutgoing(p)
	if err != nil {
		t.Fatalf("FindOutgoing(%d): %v", p, err)
	}

	if len(outgoing) != 2 {
		t.Fatalf("FindOutgoing(%d) = %v, want the original 2 relationships untouched", p, outgoing)
	}
}

func TestSubPointerReusesPointerRegistryUnderDifferentTag(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	ids, err := names.BootstrapNames(&g, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	subPointers, err := NewPointerRegistry(&g, ids[NameAllSubPointers])
	if err != nil {
		t.Fatalf("NewPointerRegistry(AllSubPointers): %v", err)
	}

	p, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for p: %v", err)
	}

	u, err := subPointers.NewPointer(&g)
	if err != nil {
		t.Fatalf("NewPointer() for u: %v", err)
	}

	if _, err2 := g.AddRelationship(p, u); err2 != nil {
		t.Fatalf("AddRelationship(p, u): %v", err2)
	}

	other, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for other: %v", err)
	}

	if _, err3 := g.AddRelationship(p, other); err3 != nil {
		t.Fatalf("AddRelationship(p, other): %v", err3)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for x: %v", err)
	}

	if err4 := subPointers.SetTarget(&g, u, x); err4 != nil {
		t.Fatalf("SetTarget(u, x): %v", err4)
	}

	target, hasTarget, err := subPointers.Target(&g, u)
	if err != nil {
		t.Fatalf("Target(u): %v", err)
	}
	if !hasTarget || target != x {
		t.Fatalf("Target(u) = (%d,%v), want (%d,true)", target, hasTarget, x)
	}

	outgoing, err := g.FindOutgoing(p)
	if err != nil {
		t.Fatalf("FindOutgoing(p): %v", err)
	}
	if len(outgoing) != 2 {
		t.Fatalf("FindOutgoing(p) = %v, want exactly {u, other}", outgoing)
	}
}

func newPointerMetadataTestFixture(t *testing.T) (testGraph, *PointerMetadataRegistry) {
	t.Helper()

	g := newTestGraph(t)
	names := NewNameRegistry(g)

	ids, err := names.BootstrapNames(g, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	metadata, err := NewPointerMetadataRegistry(g, ids[NameAllPointerMetadata], ids[NameAllPointerMetadataSubjectSlot])
	if err != nil {
		t.Fatalf("NewPointerMetadataRegistry(): %v", err)
	}

	return g, metadata
}

func TestNewPointerMetadataRegistryRequiresExistingTags(t *testing.T) {
	var g Graph

	existing, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	const nonexistent NodeID = 999999

	if _, err := NewPointerMetadataRegistry(&g, nonexistent, existing); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("NewPointerMetadataRegistry() error = %v, want %v", err, ErrNodeNotFound)
	}

	if _, err := NewPointerMetadataRegistry(&g, existing, nonexistent); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("NewPointerMetadataRegistry() error = %v, want %v", err, ErrNodeNotFound)
	}
}

func TestPointerMetadataRegistryHasMetadataFalseInitially(t *testing.T) {
	g, metadata := newPointerMetadataTestFixture(t)

	subject, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	has, err := metadata.HasMetadata(g, subject)
	if err != nil {
		t.Fatalf("HasMetadata(): %v", err)
	}
	if has {
		t.Fatal("fresh subject unexpectedly already has metadata")
	}

	_, hasTarget, err := metadata.Target(g, subject)
	if err != nil {
		t.Fatalf("Target(): %v", err)
	}
	if hasTarget {
		t.Fatal("fresh subject unexpectedly has a target")
	}
}

func TestPointerMetadataRegistrySetTargetLeavesSubjectChildrenUntouched(t *testing.T) {
	g, metadata := newPointerMetadataTestFixture(t)

	subject, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for subject: %v", err)
	}

	preexisting, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for preexisting: %v", err)
	}

	if _, err2 := g.AddRelationship(subject, preexisting); err2 != nil {
		t.Fatalf("AddRelationship(subject, preexisting): %v", err2)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for x: %v", err)
	}

	if err3 := metadata.SetTarget(g, subject, x); err3 != nil {
		t.Fatalf("SetTarget(subject, x): %v", err3)
	}

	outgoing, err := g.FindOutgoing(subject)
	if err != nil {
		t.Fatalf("FindOutgoing(subject): %v", err)
	}

	want := []Relationship{{From: subject, To: preexisting}}
	if !reflect.DeepEqual(outgoing, want) {
		t.Fatalf("FindOutgoing(subject) = %v, want %v (unchanged by the pointer representation)", outgoing, want)
	}

	target, hasTarget, err := metadata.Target(g, subject)
	if err != nil {
		t.Fatalf("Target(subject): %v", err)
	}
	if !hasTarget || target != x {
		t.Fatalf("Target(subject) = (%d,%v), want (%d,true)", target, hasTarget, x)
	}
}

func TestPointerMetadataRegistrySetTargetAllowsSelfTarget(t *testing.T) {
	g, metadata := newPointerMetadataTestFixture(t)

	subject, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	if err2 := metadata.SetTarget(g, subject, subject); err2 != nil {
		t.Fatalf("SetTarget(subject, subject): %v", err2)
	}

	target, hasTarget, err := metadata.Target(g, subject)
	if err != nil {
		t.Fatalf("Target(subject): %v", err)
	}
	if !hasTarget || target != subject {
		t.Fatalf("Target(subject) = (%d,%v), want (%d,true)", target, hasTarget, subject)
	}
}

func TestPointerMetadataRegistrySetTargetReplacesExistingTarget(t *testing.T) {
	g, metadata := newPointerMetadataTestFixture(t)

	subject, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for subject: %v", err)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for x: %v", err)
	}

	y, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for y: %v", err)
	}

	if err2 := metadata.SetTarget(g, subject, x); err2 != nil {
		t.Fatalf("SetTarget(subject, x): %v", err2)
	}

	if err3 := metadata.SetTarget(g, subject, y); err3 != nil {
		t.Fatalf("SetTarget(subject, y): %v", err3)
	}

	target, hasTarget, err := metadata.Target(g, subject)
	if err != nil {
		t.Fatalf("Target(subject): %v", err)
	}
	if !hasTarget || target != y {
		t.Fatalf("Target(subject) = (%d,%v), want (%d,true)", target, hasTarget, y)
	}
}

func TestPointerMetadataRegistryRemoveTargetRemovesExisting(t *testing.T) {
	g, metadata := newPointerMetadataTestFixture(t)

	subject, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for subject: %v", err)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for x: %v", err)
	}

	if err2 := metadata.SetTarget(g, subject, x); err2 != nil {
		t.Fatalf("SetTarget(subject, x): %v", err2)
	}

	removed, err := metadata.RemoveTarget(g, subject)
	if err != nil {
		t.Fatalf("RemoveTarget(subject): %v", err)
	}
	if !removed {
		t.Fatal("RemoveTarget() reported that nothing was removed")
	}

	has, err := metadata.HasMetadata(g, subject)
	if err != nil {
		t.Fatalf("HasMetadata(): %v", err)
	}
	if !has {
		t.Fatal("metadata node should still exist after RemoveTarget (no cascade delete)")
	}

	_, hasTarget, err := metadata.Target(g, subject)
	if err != nil {
		t.Fatalf("Target(subject): %v", err)
	}
	if hasTarget {
		t.Fatal("subject still has a target after RemoveTarget()")
	}
}

func TestPointerMetadataRegistrySetTargetRequiresExistingTarget(t *testing.T) {
	g, metadata := newPointerMetadataTestFixture(t)

	subject, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	const nonexistent NodeID = 999999

	err = metadata.SetTarget(g, subject, nonexistent)
	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("SetTarget() error = %v, want %v", err, ErrNodeNotFound)
	}
}

// TestPointerMetadataRegistryDetectsOutOfBandInvariantViolation covers
// Representation C's own "at most one target, found by excluding the
// subject-slot" invariant being violated out of band -- the counterpart
// to TestPointerRegistryDetectsOutOfBandInvariantViolation for
// Representation A/B, which this registry did not previously have.
func TestPointerMetadataRegistryDetectsOutOfBandInvariantViolation(t *testing.T) {
	g, metadata := newPointerMetadataTestFixture(t)

	subject, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for subject: %v", err)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for x: %v", err)
	}

	if err2 := metadata.SetTarget(g, subject, x); err2 != nil {
		t.Fatalf("SetTarget(subject, x): %v", err2)
	}

	m, err := metadata.EnsureMetadata(g, subject)
	if err != nil {
		t.Fatalf("EnsureMetadata(subject): %v", err)
	}

	y, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for y: %v", err)
	}

	// Bypass PointerMetadataRegistry entirely, simulating a caller bug
	// that gives M a second non-subject-slot child directly through the
	// primitive Graph. M now has two children besides its subject-slot:
	// its original target x and this new, unrelated y.
	if _, err3 := g.AddRelationship(m, y); err3 != nil {
		t.Fatalf("AddRelationship(m, y) via raw Graph: %v", err3)
	}

	if _, _, err4 := metadata.Target(g, subject); !errors.Is(err4, ErrTooManyPointerTargets) {
		t.Fatalf("Target() error = %v, want %v", err4, ErrTooManyPointerTargets)
	}

	z, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for z: %v", err)
	}

	if err5 := metadata.SetTarget(g, subject, z); !errors.Is(err5, ErrTooManyPointerTargets) {
		t.Fatalf("SetTarget() error = %v, want %v", err5, ErrTooManyPointerTargets)
	}

	if _, err6 := metadata.RemoveTarget(g, subject); !errors.Is(err6, ErrTooManyPointerTargets) {
		t.Fatalf("RemoveTarget() error = %v, want %v", err6, ErrTooManyPointerTargets)
	}

	// Confirm none of the failed calls above mutated anything: M should
	// still have exactly its subject-slot child, x, and y.
	outgoing, err := g.FindOutgoing(m)
	if err != nil {
		t.Fatalf("FindOutgoing(m): %v", err)
	}
	if len(outgoing) != 3 {
		t.Fatalf("FindOutgoing(m) = %v, want the original 3 relationships untouched", outgoing)
	}
}

func newPointerMetadataDTestFixture(t *testing.T) (testGraph, *PointerMetadataRegistryD) {
	t.Helper()

	g := newTestGraph(t)
	names := NewNameRegistry(g)

	ids, err := names.BootstrapNames(g, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	metadata, err := NewPointerMetadataRegistryD(g, ids[NameAllPointerMetadata], ids[NameAllPointerMetadataSubjectSlot], ids[NameAllPointerMetadataTargetSlot])
	if err != nil {
		t.Fatalf("NewPointerMetadataRegistryD(): %v", err)
	}

	return g, metadata
}

func TestFoundationalNamesIncludesAllPointerMetadataTargetSlot(t *testing.T) {
	for _, name := range FoundationalNames {
		if name == NameAllPointerMetadataTargetSlot {
			return
		}
	}

	t.Fatalf("FoundationalNames %v does not include %q", FoundationalNames, NameAllPointerMetadataTargetSlot)
}

func TestNewPointerMetadataRegistryDRequiresExistingTags(t *testing.T) {
	var g Graph

	existing, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	other, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	const nonexistent NodeID = 999999

	if _, err := NewPointerMetadataRegistryD(&g, nonexistent, existing, other); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("NewPointerMetadataRegistryD() error = %v, want %v", err, ErrNodeNotFound)
	}

	if _, err := NewPointerMetadataRegistryD(&g, existing, nonexistent, other); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("NewPointerMetadataRegistryD() error = %v, want %v", err, ErrNodeNotFound)
	}

	if _, err := NewPointerMetadataRegistryD(&g, existing, other, nonexistent); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("NewPointerMetadataRegistryD() error = %v, want %v", err, ErrNodeNotFound)
	}
}

func TestPointerMetadataRegistryDHasMetadataFalseInitially(t *testing.T) {
	g, metadata := newPointerMetadataDTestFixture(t)

	subject, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	has, err := metadata.HasMetadata(g, subject)
	if err != nil {
		t.Fatalf("HasMetadata(): %v", err)
	}
	if has {
		t.Fatal("fresh subject unexpectedly already has metadata")
	}

	_, hasTarget, err := metadata.Target(g, subject)
	if err != nil {
		t.Fatalf("Target(): %v", err)
	}
	if hasTarget {
		t.Fatal("fresh subject unexpectedly has a target")
	}
}

func TestPointerMetadataRegistryDSetTargetLeavesSubjectChildrenUntouched(t *testing.T) {
	g, metadata := newPointerMetadataDTestFixture(t)

	subject, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for subject: %v", err)
	}

	preexisting, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for preexisting: %v", err)
	}

	if _, err2 := g.AddRelationship(subject, preexisting); err2 != nil {
		t.Fatalf("AddRelationship(subject, preexisting): %v", err2)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for x: %v", err)
	}

	if err3 := metadata.SetTarget(g, subject, x); err3 != nil {
		t.Fatalf("SetTarget(subject, x): %v", err3)
	}

	outgoing, err := g.FindOutgoing(subject)
	if err != nil {
		t.Fatalf("FindOutgoing(subject): %v", err)
	}

	want := []Relationship{{From: subject, To: preexisting}}
	if !reflect.DeepEqual(outgoing, want) {
		t.Fatalf("FindOutgoing(subject) = %v, want %v (unchanged by the pointer representation)", outgoing, want)
	}

	target, hasTarget, err := metadata.Target(g, subject)
	if err != nil {
		t.Fatalf("Target(subject): %v", err)
	}
	if !hasTarget || target != x {
		t.Fatalf("Target(subject) = (%d,%v), want (%d,true)", target, hasTarget, x)
	}
}

func TestPointerMetadataRegistryDSetTargetAllowsSelfTarget(t *testing.T) {
	g, metadata := newPointerMetadataDTestFixture(t)

	subject, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	if err2 := metadata.SetTarget(g, subject, subject); err2 != nil {
		t.Fatalf("SetTarget(subject, subject): %v", err2)
	}

	target, hasTarget, err := metadata.Target(g, subject)
	if err != nil {
		t.Fatalf("Target(subject): %v", err)
	}
	if !hasTarget || target != subject {
		t.Fatalf("Target(subject) = (%d,%v), want (%d,true)", target, hasTarget, subject)
	}
}

func TestPointerMetadataRegistryDSetTargetReplacesExistingTarget(t *testing.T) {
	g, metadata := newPointerMetadataDTestFixture(t)

	subject, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for subject: %v", err)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for x: %v", err)
	}

	y, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for y: %v", err)
	}

	if err2 := metadata.SetTarget(g, subject, x); err2 != nil {
		t.Fatalf("SetTarget(subject, x): %v", err2)
	}

	if err3 := metadata.SetTarget(g, subject, y); err3 != nil {
		t.Fatalf("SetTarget(subject, y): %v", err3)
	}

	target, hasTarget, err := metadata.Target(g, subject)
	if err != nil {
		t.Fatalf("Target(subject): %v", err)
	}
	if !hasTarget || target != y {
		t.Fatalf("Target(subject) = (%d,%v), want (%d,true)", target, hasTarget, y)
	}
}

func TestPointerMetadataRegistryDRemoveTargetRemovesExisting(t *testing.T) {
	g, metadata := newPointerMetadataDTestFixture(t)

	subject, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for subject: %v", err)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for x: %v", err)
	}

	if err2 := metadata.SetTarget(g, subject, x); err2 != nil {
		t.Fatalf("SetTarget(subject, x): %v", err2)
	}

	removed, err := metadata.RemoveTarget(g, subject)
	if err != nil {
		t.Fatalf("RemoveTarget(subject): %v", err)
	}
	if !removed {
		t.Fatal("RemoveTarget() reported that nothing was removed")
	}

	_, hasTarget, err := metadata.Target(g, subject)
	if err != nil {
		t.Fatalf("Target(subject): %v", err)
	}
	if hasTarget {
		t.Fatal("subject still has a target after RemoveTarget()")
	}
}

func TestPointerMetadataRegistryDSetTargetRequiresExistingTarget(t *testing.T) {
	g, metadata := newPointerMetadataDTestFixture(t)

	subject, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	const nonexistent NodeID = 999999

	err = metadata.SetTarget(g, subject, nonexistent)
	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("SetTarget() error = %v, want %v", err, ErrNodeNotFound)
	}
}

// TestPointerMetadataRegistryDDetectsOutOfBandInvariantViolation covers
// Representation D's own "at most one target" invariant being violated
// out of band. Unlike Representation C, D's subject is discovered
// entirely by tag (never by exclusion), so the count-based invariant to
// violate here lives on the target-slot U2 itself, not on M directly.
func TestPointerMetadataRegistryDDetectsOutOfBandInvariantViolation(t *testing.T) {
	g, metadata := newPointerMetadataDTestFixture(t)

	subject, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for subject: %v", err)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for x: %v", err)
	}

	if err2 := metadata.SetTarget(g, subject, x); err2 != nil {
		t.Fatalf("SetTarget(subject, x): %v", err2)
	}

	m, err := metadata.EnsureMetadata(g, subject)
	if err != nil {
		t.Fatalf("EnsureMetadata(subject): %v", err)
	}

	slot, found, err := metadata.targetSlot(g, m)
	if err != nil || !found {
		t.Fatalf("targetSlot(m): found=%v err=%v", found, err)
	}

	y, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for y: %v", err)
	}

	// Bypass PointerMetadataRegistryD entirely, simulating a caller bug
	// that gives the target-slot U2 a second target directly through the
	// primitive Graph.
	if _, err3 := g.AddRelationship(slot, y); err3 != nil {
		t.Fatalf("AddRelationship(slot, y) via raw Graph: %v", err3)
	}

	if _, _, err4 := metadata.Target(g, subject); !errors.Is(err4, ErrTooManyPointerTargets) {
		t.Fatalf("Target() error = %v, want %v", err4, ErrTooManyPointerTargets)
	}

	z, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for z: %v", err)
	}

	if err5 := metadata.SetTarget(g, subject, z); !errors.Is(err5, ErrTooManyPointerTargets) {
		t.Fatalf("SetTarget() error = %v, want %v", err5, ErrTooManyPointerTargets)
	}

	if _, err6 := metadata.RemoveTarget(g, subject); !errors.Is(err6, ErrTooManyPointerTargets) {
		t.Fatalf("RemoveTarget() error = %v, want %v", err6, ErrTooManyPointerTargets)
	}

	// Confirm none of the failed calls above mutated anything: the
	// target-slot should still have exactly its original two children,
	// x and y.
	outgoing, err := g.FindOutgoing(slot)
	if err != nil {
		t.Fatalf("FindOutgoing(slot): %v", err)
	}
	if len(outgoing) != 2 {
		t.Fatalf("FindOutgoing(slot) = %v, want the original 2 relationships (x and y) untouched", outgoing)
	}
}

// TestPointerMetadataRegistryDAllowsUnrelatedMetadataChildren is the key
// test distinguishing Representation D from Representation C: M can carry
// an arbitrary, unrelated extra child without disturbing subject/target
// discovery, because both are found by their own tag rather than by
// exclusion. See the PointerMetadataRegistryD doc comment.
func TestPointerMetadataRegistryDAllowsUnrelatedMetadataChildren(t *testing.T) {
	g, metadata := newPointerMetadataDTestFixture(t)

	subject, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for subject: %v", err)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for x: %v", err)
	}

	if err2 := metadata.SetTarget(g, subject, x); err2 != nil {
		t.Fatalf("SetTarget(subject, x): %v", err2)
	}

	m, err := metadata.EnsureMetadata(g, subject)
	if err != nil {
		t.Fatalf("EnsureMetadata(subject): %v", err)
	}

	unrelated, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for unrelated: %v", err)
	}

	// Simulate a future extension adding an arbitrary, untagged child to
	// M. Representation C's exclusion-based target lookup would break
	// (ErrTooManyPointerTargets) if this were done to its metadata node;
	// Representation D must not be affected at all.
	if _, err3 := g.AddRelationship(m, unrelated); err3 != nil {
		t.Fatalf("AddRelationship(m, unrelated): %v", err3)
	}

	target, hasTarget, err := metadata.Target(g, subject)
	if err != nil {
		t.Fatalf("Target(subject) after adding an unrelated child to M: %v", err)
	}
	if !hasTarget || target != x {
		t.Fatalf("Target(subject) = (%d,%v), want (%d,true) -- unrelated child to M should not affect target discovery", target, hasTarget, x)
	}
}

func requirePointerTarget(t *testing.T, pointers *PointerRegistry, graph GraphReader, p, want NodeID) {
	t.Helper()

	target, hasTarget, err := pointers.Target(graph, p)
	if err != nil {
		t.Fatalf("Target(%d): %v", p, err)
	}
	if !hasTarget || target != want {
		t.Fatalf("Target(%d) = (%d,%v), want (%d,true)", p, target, hasTarget, want)
	}
}

func TestPointerRegistrySetTargetComposesInsideOneTransaction(t *testing.T) {
	g, pointers := newPointerTestFixture(t)

	p, err := pointers.NewPointer(g)
	if err != nil {
		t.Fatalf("NewPointer(): %v", err)
	}

	x := newTestNode(t, g)
	y := newTestNode(t, g)
	errOuter := errors.New("outer failure")

	// Two exported calls composed in one transaction commit together.
	err = g.Transact(func(tx Tx) error {
		if setErr := pointers.SetTarget(tx, p, x); setErr != nil {
			return setErr
		}

		return pointers.SetTarget(tx, p, y)
	})
	if err != nil {
		t.Fatalf("Transact(compose): %v", err)
	}
	requirePointerTarget(t, pointers, g, p, y)

	// ...and roll back together when the enclosing transaction fails.
	err = g.Transact(func(tx Tx) error {
		if setErr := pointers.SetTarget(tx, p, x); setErr != nil {
			return setErr
		}

		return errOuter
	})
	if !errors.Is(err, errOuter) {
		t.Fatalf("Transact(fail) error = %v, want %v", err, errOuter)
	}
	requirePointerTarget(t, pointers, g, p, y)
}

func TestPointerRegistryComposedSetTargetIsUndoneByFailedNestedTransaction(t *testing.T) {
	g, pointers := newPointerTestFixture(t)

	p, err := pointers.NewPointer(g)
	if err != nil {
		t.Fatalf("NewPointer(): %v", err)
	}

	x := newTestNode(t, g)
	y := newTestNode(t, g)
	errInner := errors.New("inner failure")

	err = g.Transact(func(tx Tx) error {
		if setErr := pointers.SetTarget(tx, p, x); setErr != nil {
			return setErr
		}

		innerErr := tx.Transact(func(inner Tx) error {
			if setErr := pointers.SetTarget(inner, p, y); setErr != nil {
				return setErr
			}

			return errInner
		})
		if !errors.Is(innerErr, errInner) {
			t.Errorf("nested Transact() error = %v, want %v", innerErr, errInner)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("Transact(): %v", err)
	}

	requirePointerTarget(t, pointers, g, p, x)
}

func TestPointerMetadataRegistryDComposedSetTargetRollsBackMetadataCreation(t *testing.T) {
	g, metadata := newPointerMetadataDTestFixture(t)

	subject := newTestNode(t, g)
	x := newTestNode(t, g)
	errOuter := errors.New("outer failure")

	err := g.Transact(func(tx Tx) error {
		if setErr := metadata.SetTarget(tx, subject, x); setErr != nil {
			return setErr
		}

		return errOuter
	})
	if !errors.Is(err, errOuter) {
		t.Fatalf("Transact(fail) error = %v, want %v", err, errOuter)
	}

	has, hasErr := metadata.HasMetadata(g, subject)
	if hasErr != nil {
		t.Fatalf("HasMetadata(): %v", hasErr)
	}
	if has {
		t.Fatal("metadata created by a rolled-back composed SetTarget survived")
	}

	err = g.Transact(func(tx Tx) error {
		if setErr := metadata.SetTarget(tx, subject, x); setErr != nil {
			return setErr
		}

		removed, removeErr := metadata.RemoveTarget(tx, subject)
		if removeErr != nil {
			return removeErr
		}
		if !removed {
			t.Error("RemoveTarget() reported nothing removed right after SetTarget() in the same transaction")
		}

		return nil
	})
	if err != nil {
		t.Fatalf("Transact(compose) error = %v", err)
	}

	if _, hasTarget, targetErr := metadata.Target(g, subject); targetErr != nil {
		t.Fatalf("Target(): %v", targetErr)
	} else if hasTarget {
		t.Fatal("subject still has a target after the composed SetTarget+RemoveTarget")
	}
}
