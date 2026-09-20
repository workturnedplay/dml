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
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

// TestMain enables GraphActor's debug-only reentrancy tripwire
// (theorystate.md section 90) for this entire test binary, so any test
// anywhere in this file that reintroduces the misplaced-storage bug
// class the tripwire exists to catch -- a registry reading through a
// stored graph reference instead of through the tx/g value it was
// actually handed -- panics loudly and immediately instead of hanging
// forever.
func TestMain(m *testing.M) {
	EnableGraphActorReentrancyDetection()
	os.Exit(m.Run())
}

// sortedNodeIDs returns a sorted copy of ids, for comparing test results
// against results whose order is documented as unspecified (e.g.
// CapsuleRegistry.CapsulesWithValue, ListRegistry.OccurrencesOf).
func sortedNodeIDs(ids []NodeID) []NodeID {
	sorted := make([]NodeID, len(ids))
	copy(sorted, ids)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted
}

func TestCreateNode(t *testing.T) {
	var g Graph

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	if a != 0 {
		t.Fatalf("first CreateNode() returned %d, want 0", a)
	}

	if b != 1 {
		t.Fatalf("second CreateNode() returned %d, want 1", b)
	}

	if a == b {
		t.Fatalf("CreateNode() returned duplicate NodeID %d", a)
	}

	if !g.NodeExists(a) {
		t.Fatalf("created node %d does not exist", a)
	}

	if !g.NodeExists(b) {
		t.Fatalf("created node %d does not exist", b)
	}
}

func TestNodeCanExistWithoutRelationships(t *testing.T) {
	var g Graph

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	outgoing, err := g.FindOutgoing(a)
	if err != nil {
		t.Fatalf("FindOutgoing() returned error: %v", err)
	}

	if len(outgoing) != 0 {
		t.Fatalf("expected no outgoing relationships, got %v", outgoing)
	}

	incoming, err := g.FindIncoming(a)
	if err != nil {
		t.Fatalf("FindIncoming() returned error: %v", err)
	}

	if len(incoming) != 0 {
		t.Fatalf("expected no incoming relationships, got %v", incoming)
	}
}

func TestRelationshipIsDirected(t *testing.T) {
	var g Graph

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	created, err := g.AddRelationship(a, b)
	if err != nil {
		t.Fatalf("AddRelationship(%d, %d) returned error: %v", a, b, err)
	}

	if !created {
		t.Fatalf("AddRelationship(%d, %d) reported that nothing was created", a, b)
	}

	if !g.HasRelationship(a, b) {
		t.Fatalf("expected (%d,%d) to exist", a, b)
	}

	if g.HasRelationship(b, a) {
		t.Fatalf(
			"(%d,%d) incorrectly exists merely because (%d,%d) exists",
			b, a, a, b,
		)
	}
}

func TestRelationshipIsUnique(t *testing.T) {
	var g Graph

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	created, err := g.AddRelationship(a, b)
	if err != nil {
		t.Fatalf("first AddRelationship() returned error: %v", err)
	}

	if !created {
		t.Fatal("first AddRelationship() reported that nothing was created")
	}

	created, err = g.AddRelationship(a, b)
	if err != nil {
		t.Fatalf("second AddRelationship() returned error: %v", err)
	}

	if created {
		t.Fatal("second AddRelationship() reported that a new relationship was created")
	}

	relationship, exists, err := g.FindRelationship(a, b)
	if err != nil {
		t.Fatalf("FindRelationship(%d,%d) returned error: %v", a, b, err)
	}

	if !exists {
		t.Fatalf("FindRelationship(%d,%d) reported that the relationship does not exist", a, b)
	}

	expected := Relationship{
		From: a,
		To:   b,
	}

	if !reflect.DeepEqual(relationship, expected) {
		t.Fatalf(
			"FindRelationship(%d,%d) = %v, want %v",
			a, b, relationship, expected,
		)
	}
}

func TestSelfRelationshipIsAllowed(t *testing.T) {
	var g Graph

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	created, err := g.AddRelationship(a, a)
	if err != nil {
		t.Fatalf("AddRelationship(%d, %d) returned error: %v", a, a, err)
	}

	if !created {
		t.Fatal("self relationship was not created")
	}

	if !g.HasRelationship(a, a) {
		t.Fatalf("expected (%d,%d) to exist", a, a)
	}

	relationship, exists, err := g.FindRelationship(a, a)
	if err != nil {
		t.Fatalf("FindRelationship(%d,%d) returned error: %v", a, a, err)
	}

	if !exists {
		t.Fatalf("FindRelationship(%d,%d) reported that the relationship does not exist", a, a)
	}

	expected := Relationship{
		From: a,
		To:   a,
	}

	if !reflect.DeepEqual(relationship, expected) {
		t.Fatalf(
			"FindRelationship(%d,%d) = %v, want %v",
			a, a, relationship, expected,
		)
	}
}

func TestFindOutgoing(t *testing.T) {
	var g Graph

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	c, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	if _, err2 := g.AddRelationship(a, b); err2 != nil {
		t.Fatalf("AddRelationship(%d,%d): %v", a, b, err2)
	}

	if _, err3 := g.AddRelationship(a, c); err3 != nil {
		t.Fatalf("AddRelationship(%d,%d): %v", a, c, err3)
	}

	relationships, err := g.FindOutgoing(a)
	if err != nil {
		t.Fatalf("FindOutgoing(%d): %v", a, err)
	}

	expected := []Relationship{
		{From: a, To: b},
		{From: a, To: c},
	}

	if !reflect.DeepEqual(relationships, expected) {
		t.Fatalf(
			"FindOutgoing(%d) = %v, want %v",
			a, relationships, expected,
		)
	}
}

func TestFindIncoming(t *testing.T) {
	var g Graph

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	c, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	if _, err2 := g.AddRelationship(a, c); err2 != nil {
		t.Fatalf("AddRelationship(%d,%d): %v", a, c, err2)
	}

	if _, err3 := g.AddRelationship(b, c); err3 != nil {
		t.Fatalf("AddRelationship(%d,%d): %v", b, c, err3)
	}

	relationships, err := g.FindIncoming(c)
	if err != nil {
		t.Fatalf("FindIncoming(%d): %v", c, err)
	}

	expected := []Relationship{
		{From: a, To: c},
		{From: b, To: c},
	}

	if !reflect.DeepEqual(relationships, expected) {
		t.Fatalf(
			"FindIncoming(%d) = %v, want %v",
			c, relationships, expected,
		)
	}
}

func TestFindRelationship(t *testing.T) {
	var g Graph

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	if _, err2 := g.AddRelationship(a, b); err2 != nil {
		t.Fatalf("AddRelationship(%d,%d): %v", a, b, err2)
	}

	relationship, exists, err := g.FindRelationship(a, b)
	if err != nil {
		t.Fatalf("FindRelationship(%d,%d): %v", a, b, err)
	}

	if !exists {
		t.Fatalf("expected (%d,%d) to exist", a, b)
	}

	expected := Relationship{
		From: a,
		To:   b,
	}

	if !reflect.DeepEqual(relationship, expected) {
		t.Fatalf(
			"FindRelationship(%d,%d) = %v, want %v",
			a, b, relationship, expected,
		)
	}

	_, exists, err = g.FindRelationship(b, a)
	if err != nil {
		t.Fatalf("FindRelationship(%d,%d): %v", b, a, err)
	}

	if exists {
		t.Fatalf("did not expect (%d,%d) to exist", b, a)
	}
}

func TestFindRelationships(t *testing.T) {
	var g Graph

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	c, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	if _, err := g.AddRelationship(a, b); err != nil {
		t.Fatalf("AddRelationship(%d,%d): %v", a, b, err)
	}

	if _, err := g.AddRelationship(a, c); err != nil {
		t.Fatalf("AddRelationship(%d,%d): %v", a, c, err)
	}

	if _, err := g.AddRelationship(c, b); err != nil {
		t.Fatalf("AddRelationship(%d,%d): %v", c, b, err)
	}

	relationships := g.FindRelationships()

	expected := []Relationship{
		{From: a, To: b},
		{From: a, To: c},
		{From: c, To: b},
	}

	if !reflect.DeepEqual(relationships, expected) {
		t.Fatalf(
			"FindRelationships() = %v, want %v",
			relationships, expected,
		)
	}
}

func TestRemoveRelationship(t *testing.T) {
	var g Graph

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	if _, err2 := g.AddRelationship(a, b); err2 != nil {
		t.Fatalf("AddRelationship() returned error: %v", err2)
	}

	removed, err := g.RemoveRelationship(a, b)
	if err != nil {
		t.Fatalf("RemoveRelationship() returned error: %v", err)
	}

	if !removed {
		t.Fatal("RemoveRelationship() reported that nothing was removed")
	}

	if g.HasRelationship(a, b) {
		t.Fatalf("relationship (%d,%d) still exists", a, b)
	}

	outgoing, err := g.FindOutgoing(a)
	if err != nil {
		t.Fatalf("FindOutgoing() returned error: %v", err)
	}

	if len(outgoing) != 0 {
		t.Fatalf("expected no outgoing relationships, got %v", outgoing)
	}

	incoming, err := g.FindIncoming(b)
	if err != nil {
		t.Fatalf("FindIncoming() returned error: %v", err)
	}

	if len(incoming) != 0 {
		t.Fatalf("expected no incoming relationships, got %v", incoming)
	}
}

func TestDeleteNodeRequiresNoRelationships(t *testing.T) {
	var g Graph

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	if _, err2 := g.AddRelationship(a, b); err2 != nil {
		t.Fatalf("AddRelationship() returned error: %v", err2)
	}

	err = g.DeleteNode(a)
	if !errors.Is(err, ErrNodeNotEmpty) {
		t.Fatalf(
			"DeleteNode(%d) error = %v, want %v",
			a, err, ErrNodeNotEmpty,
		)
	}

	if !g.NodeExists(a) {
		t.Fatalf(
			"node %d disappeared even though deletion should have failed",
			a,
		)
	}

	_, err = g.RemoveRelationship(a, b)
	if err != nil {
		t.Fatalf("RemoveRelationship() returned error: %v", err)
	}

	if err3 := g.DeleteNode(a); err3 != nil {
		t.Fatalf(
			"DeleteNode(%d) after removing relationships returned error: %v",
			a, err3,
		)
	}

	if g.NodeExists(a) {
		t.Fatalf("node %d still exists after successful deletion", a)
	}
}

func TestDeleteNodeWithIncomingRelationshipAlsoFails(t *testing.T) {
	var g Graph

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	if _, err2 := g.AddRelationship(a, b); err2 != nil {
		t.Fatalf("AddRelationship() returned error: %v", err2)
	}

	err = g.DeleteNode(b)
	if !errors.Is(err, ErrNodeNotEmpty) {
		t.Fatalf(
			"DeleteNode(%d) error = %v, want %v",
			b, err, ErrNodeNotEmpty,
		)
	}

	if !g.NodeExists(b) {
		t.Fatalf(
			"node %d disappeared even though deletion should have failed",
			b,
		)
	}
}

func TestRelationshipRequiresExistingNodes(t *testing.T) {
	var g Graph

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	const nonexistent NodeID = 999999

	_, err = g.AddRelationship(a, nonexistent)
	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf(
			"AddRelationship(%d,%d) error = %v, want %v",
			a, nonexistent, err, ErrNodeNotFound,
		)
	}

	if g.HasRelationship(a, nonexistent) {
		t.Fatalf("relationship to nonexistent node unexpectedly exists")
	}
}

func TestRelationshipQueriesRequireExistingNodes(t *testing.T) {
	var g Graph

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	const nonexistent NodeID = 999999

	if _, err := g.FindOutgoing(nonexistent); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf(
			"FindOutgoing(%d) error = %v, want %v",
			nonexistent, err, ErrNodeNotFound,
		)
	}

	if _, err := g.FindIncoming(nonexistent); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf(
			"FindIncoming(%d) error = %v, want %v",
			nonexistent, err, ErrNodeNotFound,
		)
	}

	if _, _, err := g.FindRelationship(a, nonexistent); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf(
			"FindRelationship(%d,%d) error = %v, want %v",
			a, nonexistent, err, ErrNodeNotFound,
		)
	}

	if _, _, err := g.FindRelationship(nonexistent, a); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf(
			"FindRelationship(%d,%d) error = %v, want %v",
			nonexistent, a, err, ErrNodeNotFound,
		)
	}
}

func TestZeroValueGraphWorks(t *testing.T) {
	var g Graph

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() on zero-value Graph returned error: %v", err)
	}

	if a != 0 {
		t.Fatalf("first node in zero-value Graph = %d, want 0", a)
	}

	if !g.NodeExists(a) {
		t.Fatalf("created node %d does not exist", a)
	}
}

// TestConcurrentAccessGuardFiresOnDoubleAcquire exercises Graph's fail-
// fast concurrent-access guard (theorystate.md section 89b) directly and
// deterministically: acquiring it a second time before the first
// acquisition's release has run must panic immediately, exactly as
// documented on concurrentAccessGuard, rather than blocking or silently
// succeeding. This is tested against the guard type itself, not by
// racing real goroutines against a *Graph, since genuinely provoking two
// goroutines to overlap inside a guarded call is inherently
// timing-dependent and would make this test flaky; every other existing
// test in this file already exercises the "ordinary sequential and
// Transact-based usage never trips this guard" side of the contract,
// since none of them introduce genuine concurrent overlap and all
// continue to pass unmodified against the guarded implementation.
func TestConcurrentAccessGuardFiresOnDoubleAcquire(t *testing.T) {
	var g concurrentAccessGuard

	release := g.acquire()
	defer release()

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected second acquire() to panic while the guard is still held")
		}
	}()

	g.acquire()
}

// TestConcurrentAccessGuardAllowsSequentialReuse confirms the guard is
// not a one-shot latch: once released, it can be acquired again by a
// later, non-overlapping call without panicking.
func TestConcurrentAccessGuardAllowsSequentialReuse(_ *testing.T) {
	var g concurrentAccessGuard

	release := g.acquire()
	release()

	release = g.acquire()
	release()
}

func TestNameRegistryLookupMissing(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	if _, ok := names.Lookup("ROOT"); ok {
		t.Fatal("Lookup() found a name that was never bound")
	}
}

func TestNameRegistryCreateNamedNode(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	id, err := names.CreateNamedNode(&g, "ROOT")
	if err != nil {
		t.Fatalf("CreateNamedNode() returned error: %v", err)
	}

	if !g.NodeExists(id) {
		t.Fatalf("created node %d does not exist", id)
	}

	found, ok := names.Lookup("ROOT")
	if !ok {
		t.Fatal("Lookup(\"ROOT\") did not find the name")
	}

	if found != id {
		t.Fatalf("Lookup(\"ROOT\") = %d, want %d", found, id)
	}

	name, ok := names.NameForNode(id)
	if !ok {
		t.Fatalf("NameForNode(%d) did not find the name", id)
	}

	if name != "ROOT" {
		t.Fatalf("NameForNode(%d) = %q, want %q", id, name, "ROOT")
	}
}

func TestNameRegistryRequiresExistingNode(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	const nonexistent NodeID = 12345

	err := names.Bind(&g, "A", nonexistent)
	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Bind() error = %v, want %v", err, ErrNodeNotFound)
	}

	if _, ok := names.Lookup("A"); ok {
		t.Fatal("name was bound despite nonexistent NodeID")
	}
}

func TestNameRegistryNameIsUnique(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	if err2 := names.Bind(&g, "A", a); err2 != nil {
		t.Fatalf("first Bind() returned error: %v", err2)
	}

	err = names.Bind(&g, "A", b)
	if !errors.Is(err, ErrNameAlreadyBound) {
		t.Fatalf("second Bind() error = %v, want %v", err, ErrNameAlreadyBound)
	}

	found, ok := names.Lookup("A")
	if !ok || found != a {
		t.Fatalf("failed rebind changed the existing association")
	}
}

func TestNameRegistryNodeIDIsUnique(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	id, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	if err2 := names.Bind(&g, "A", id); err2 != nil {
		t.Fatalf("first Bind() returned error: %v", err2)
	}

	err = names.Bind(&g, "B", id)
	if !errors.Is(err, ErrNodeAlreadyNamed) {
		t.Fatalf("second Bind() error = %v, want %v", err, ErrNodeAlreadyNamed)
	}

	name, ok := names.NameForNode(id)
	if !ok || name != "A" {
		t.Fatalf("failed second binding changed the existing association")
	}
}

func TestNameRegistrySameBindingIsIdempotent(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	id, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	if err := names.Bind(&g, "A", id); err != nil {
		t.Fatalf("first Bind() returned error: %v", err)
	}

	if err := names.Bind(&g, "A", id); err != nil {
		t.Fatalf("identical second Bind() returned error: %v", err)
	}
}

func TestNameRegistryCreateNamedNodeDoesNotDuplicateName(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	first, err := names.CreateNamedNode(&g, "A")
	if err != nil {
		t.Fatalf("first CreateNamedNode() returned error: %v", err)
	}

	_, err = names.CreateNamedNode(&g, "A")
	if !errors.Is(err, ErrNameAlreadyBound) {
		t.Fatalf(
			"second CreateNamedNode() error = %v, want %v",
			err, ErrNameAlreadyBound,
		)
	}

	if !g.NodeExists(first) {
		t.Fatalf("original named node %d disappeared", first)
	}
}

func TestNameRegistryUnbindDoesNotDeleteNode(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	id, err := names.CreateNamedNode(&g, "A")
	if err != nil {
		t.Fatalf("CreateNamedNode() returned error: %v", err)
	}

	removed, err := names.Unbind("A")
	if err != nil {
		t.Fatalf("Unbind() returned error: %v", err)
	}

	if !removed {
		t.Fatal("Unbind() reported that nothing was removed")
	}

	if _, ok := names.Lookup("A"); ok {
		t.Fatal("name still exists after Unbind()")
	}

	if _, ok := names.NameForNode(id); ok {
		t.Fatal("NodeID still has a name after Unbind()")
	}

	if !g.NodeExists(id) {
		t.Fatalf("Unbind() incorrectly deleted NodeID %d", id)
	}
}

func TestNameRegistryUnbindMissing(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	removed, err := names.Unbind("missing")
	if !errors.Is(err, ErrNameNotFound) {
		t.Fatalf("Unbind() error = %v, want %v", err, ErrNameNotFound)
	}

	if removed {
		t.Fatal("Unbind() reported removal despite name not existing")
	}
}

func TestNameRegistryDeleteNodeRemovesNameAssociation(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	id, err := names.CreateNamedNode(&g, "A")
	if err != nil {
		t.Fatalf("CreateNamedNode(): %v", err)
	}

	if err := names.DeleteNode(&g, id); err != nil {
		t.Fatalf("DeleteNode(%d): %v", id, err)
	}

	if g.NodeExists(id) {
		t.Fatalf("node %d still exists after DeleteNode()", id)
	}

	if _, ok := names.Lookup("A"); ok {
		t.Fatal("name \"A\" still resolves after DeleteNode()")
	}

	if _, ok := names.NameForNode(id); ok {
		t.Fatalf("NodeID %d still has a name after DeleteNode()", id)
	}
}

func TestNameRegistryDeleteNodeFailsIfNotEmpty(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	a, err := names.CreateNamedNode(&g, "A")
	if err != nil {
		t.Fatalf("CreateNamedNode(): %v", err)
	}

	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	if _, err2 := g.AddRelationship(a, b); err2 != nil {
		t.Fatalf("AddRelationship(): %v", err2)
	}

	err = names.DeleteNode(&g, a)
	if !errors.Is(err, ErrNodeNotEmpty) {
		t.Fatalf("DeleteNode(%d) error = %v, want %v", a, err, ErrNodeNotEmpty)
	}

	if !g.NodeExists(a) {
		t.Fatalf("node %d disappeared even though deletion should have failed", a)
	}

	name, ok := names.NameForNode(a)
	if !ok || name != "A" {
		t.Fatalf("name association for %d was disturbed by failed DeleteNode()", a)
	}
}

func TestNameRegistryDeleteNodeWithoutNameWorks(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	id, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	if err := names.DeleteNode(&g, id); err != nil {
		t.Fatalf("DeleteNode(%d): %v", id, err)
	}

	if g.NodeExists(id) {
		t.Fatalf("node %d still exists after DeleteNode()", id)
	}
}

func TestNameRegistryDoesNotCreateRelationships(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	a, err := names.CreateNamedNode(&g, "A")
	if err != nil {
		t.Fatalf("CreateNamedNode(\"A\") returned error: %v", err)
	}

	b, err := names.CreateNamedNode(&g, "B")
	if err != nil {
		t.Fatalf("CreateNamedNode(\"B\") returned error: %v", err)
	}

	if g.HasRelationship(a, b) {
		t.Fatalf("name registry created unexpected relationship (%d,%d)", a, b)
	}

	if g.HasRelationship(b, a) {
		t.Fatalf("name registry created unexpected relationship (%d,%d)", b, a)
	}
}

func TestNameRegistryEnsureNamedNodeCreatesWhenMissing(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	id, err := names.EnsureNamedNode(&g, "A")
	if err != nil {
		t.Fatalf("EnsureNamedNode() returned error: %v", err)
	}

	if !g.NodeExists(id) {
		t.Fatalf("EnsureNamedNode() returned NodeID %d that does not exist", id)
	}

	found, ok := names.Lookup("A")
	if !ok || found != id {
		t.Fatalf("Lookup(\"A\") = (%d, %v), want (%d, true)", found, ok, id)
	}
}

func TestNameRegistryEnsureNamedNodeIsIdempotent(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	first, err := names.EnsureNamedNode(&g, "A")
	if err != nil {
		t.Fatalf("first EnsureNamedNode() returned error: %v", err)
	}

	second, err := names.EnsureNamedNode(&g, "A")
	if err != nil {
		t.Fatalf("second EnsureNamedNode() returned error: %v", err)
	}

	if first != second {
		t.Fatalf(
			"EnsureNamedNode() returned %d then %d, want the same NodeID both times",
			first, second,
		)
	}
}

func TestNameRegistryEnsureNamedNodeFindsExistingBinding(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	id, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	if err2 := names.Bind(&g, "A", id); err2 != nil {
		t.Fatalf("Bind() returned error: %v", err2)
	}

	found, err := names.EnsureNamedNode(&g, "A")
	if err != nil {
		t.Fatalf("EnsureNamedNode() returned error: %v", err)
	}

	if found != id {
		t.Fatalf(
			"EnsureNamedNode(\"A\") = %d, want %d (the manually bound node)",
			found, id,
		)
	}
}

func TestNameRegistryEnsureNamedNodeFailsOnStaleBinding(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	id, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	if err2 := names.Bind(&g, "A", id); err2 != nil {
		t.Fatalf("Bind(): %v", err2)
	}

	// Bypass NameRegistry.DeleteNode on purpose, simulating a caller bug
	// that deletes the node without coordinating with the name registry.
	if err3 := g.DeleteNode(id); err3 != nil {
		t.Fatalf("DeleteNode(%d) via raw Graph: %v", id, err3)
	}

	_, err = names.EnsureNamedNode(&g, "A")
	if !errors.Is(err, ErrNameBoundToDeletedNode) {
		t.Fatalf("EnsureNamedNode() error = %v, want %v", err, ErrNameBoundToDeletedNode)
	}
}

func TestNameRegistryCreateNamedNodeFailsOnStaleBinding(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	id, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	if err2 := names.Bind(&g, "A", id); err2 != nil {
		t.Fatalf("Bind(): %v", err2)
	}

	if err3 := g.DeleteNode(id); err3 != nil {
		t.Fatalf("DeleteNode(%d) via raw Graph: %v", id, err3)
	}

	_, err = names.CreateNamedNode(&g, "A")
	if !errors.Is(err, ErrNameBoundToDeletedNode) {
		t.Fatalf("CreateNamedNode() error = %v, want %v", err, ErrNameBoundToDeletedNode)
	}
}

func TestNameRegistryBindFailsOnStaleBindingToDifferentNode(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	stale, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for stale: %v", err)
	}

	if err2 := names.Bind(&g, "A", stale); err2 != nil {
		t.Fatalf("Bind(): %v", err2)
	}

	if err3 := g.DeleteNode(stale); err3 != nil {
		t.Fatalf("DeleteNode(%d) via raw Graph: %v", stale, err3)
	}

	replacement, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for replacement: %v", err)
	}

	err = names.Bind(&g, "A", replacement)
	if !errors.Is(err, ErrNameBoundToDeletedNode) {
		t.Fatalf("Bind() error = %v, want %v", err, ErrNameBoundToDeletedNode)
	}
}

func TestBootstrapNamesFailsOnStaleBinding(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	id, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	if err2 := names.Bind(&g, "A", id); err2 != nil {
		t.Fatalf("Bind(): %v", err2)
	}

	if err3 := g.DeleteNode(id); err3 != nil {
		t.Fatalf("DeleteNode(%d) via raw Graph: %v", id, err3)
	}

	_, err = names.BootstrapNames(&g, []string{"A", "B"})
	if !errors.Is(err, ErrNameBoundToDeletedNode) {
		t.Fatalf("BootstrapNames() error = %v, want %v", err, ErrNameBoundToDeletedNode)
	}
}

func TestBootstrapNamesCreatesAllNames(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	ids, err := names.BootstrapNames(&g, []string{"A", "B", "C"})
	if err != nil {
		t.Fatalf("BootstrapNames() returned error: %v", err)
	}

	if len(ids) != 3 {
		t.Fatalf("BootstrapNames() returned %d entries, want 3", len(ids))
	}

	for _, name := range []string{"A", "B", "C"} {
		id, ok := ids[name]
		if !ok {
			t.Fatalf("BootstrapNames() result missing entry for %q", name)
		}

		if !g.NodeExists(id) {
			t.Fatalf("BootstrapNames() returned nonexistent NodeID %d for %q", id, name)
		}

		found, ok := names.Lookup(name)
		if !ok || found != id {
			t.Fatalf("Lookup(%q) = (%d, %v), want (%d, true)", name, found, ok, id)
		}
	}
}

func TestBootstrapNamesIsIdempotent(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	first, err := names.BootstrapNames(&g, []string{"A", "B"})
	if err != nil {
		t.Fatalf("first BootstrapNames() returned error: %v", err)
	}

	second, err := names.BootstrapNames(&g, []string{"A", "B"})
	if err != nil {
		t.Fatalf("second BootstrapNames() returned error: %v", err)
	}

	if !reflect.DeepEqual(first, second) {
		t.Fatalf("BootstrapNames() = %v then %v, want identical results", first, second)
	}
}

func TestBootstrapNamesResumesAcrossOverlappingCalls(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	first, err := names.BootstrapNames(&g, []string{"A", "B"})
	if err != nil {
		t.Fatalf("first BootstrapNames() returned error: %v", err)
	}

	second, err := names.BootstrapNames(&g, []string{"B", "C"})
	if err != nil {
		t.Fatalf("second BootstrapNames() returned error: %v", err)
	}

	if second["B"] != first["B"] {
		t.Fatalf(
			"BootstrapNames() rebound %q to %d, want unchanged %d",
			"B", second["B"], first["B"],
		)
	}

	if _, ok := names.Lookup("A"); !ok {
		t.Fatal("\"A\" from the first call is no longer bound")
	}

	if _, ok := names.Lookup("C"); !ok {
		t.Fatal("\"C\" from the second call was not bound")
	}
}

func TestBootstrapNamesHandlesDuplicateNamesInList(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	ids, err := names.BootstrapNames(&g, []string{"A", "A", "A"})
	if err != nil {
		t.Fatalf("BootstrapNames() returned error: %v", err)
	}

	if len(ids) != 1 {
		t.Fatalf(
			"BootstrapNames() with duplicate names returned %d entries, want 1",
			len(ids),
		)
	}
}

func TestFoundationalNamesIncludesAllPointers(t *testing.T) {
	for _, name := range FoundationalNames {
		if name == NameAllPointers {
			return
		}
	}

	t.Fatalf("FoundationalNames %v does not include %q", FoundationalNames, NameAllPointers)
}

func TestAllPointersTagsPointerViaRelationship(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	ids, err := names.BootstrapNames(&g, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames() returned error: %v", err)
	}

	allPointers := ids[NameAllPointers]

	p, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for p: %v", err)
	}

	created, err := g.AddRelationship(allPointers, p)
	if err != nil {
		t.Fatalf("AddRelationship(AllPointers, p): %v", err)
	}

	if !created {
		t.Fatal("tagging relationship (AllPointers, p) was not created")
	}

	if !g.HasRelationship(allPointers, p) {
		t.Fatal("p is not tagged as Pointer-kind via (AllPointers, p)")
	}
}

func TestNewRootGraphRequiresExistingNode(t *testing.T) {
	var g Graph

	const root NodeID = 999

	_, err := NewRootGraph(&g, root)
	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("NewRootGraph() error = %v, want %v", err, ErrNodeNotFound)
	}
}

func TestRootExposesEveryOtherExistingNode(t *testing.T) {
	var g Graph

	root, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for ROOT: %v", err)
	}

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}

	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for b: %v", err)
	}

	r, err := NewRootGraph(&g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	got, err := r.FindOutgoing(root)
	if err != nil {
		t.Fatalf("FindOutgoing(ROOT): %v", err)
	}

	want := []Relationship{
		{From: root, To: a},
		{From: root, To: b},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FindOutgoing(ROOT) = %v, want %v", got, want)
	}
}

func TestRootDoesNotPointToItself(t *testing.T) {
	var g Graph

	root, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	r, err := NewRootGraph(&g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	if r.HasRelationship(root, root) {
		t.Fatal("ROOT incorrectly has a relationship to itself")
	}

	_, exists, err := r.FindRelationship(root, root)
	if err != nil {
		t.Fatalf("FindRelationship(ROOT, ROOT): %v", err)
	}

	if exists {
		t.Fatal("FindRelationship(ROOT, ROOT) found a relationship")
	}
}

func TestRootCanHaveParents(t *testing.T) {
	var g Graph

	root, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for ROOT: %v", err)
	}

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}

	if _, err2 := g.AddRelationship(a, root); err2 != nil {
		t.Fatalf("AddRelationship(a, ROOT): %v", err2)
	}

	r, err := NewRootGraph(&g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	got, err := r.FindIncoming(root)
	if err != nil {
		t.Fatalf("FindIncoming(ROOT): %v", err)
	}

	want := []Relationship{
		{From: a, To: root},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FindIncoming(ROOT) = %v, want %v", got, want)
	}
}

func TestRootCanBeTargetOfNormalRelationship(t *testing.T) {
	var g Graph

	root, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for ROOT: %v", err)
	}

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}

	r, err := NewRootGraph(&g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	created, err := r.AddRelationship(a, root)
	if err != nil {
		t.Fatalf("AddRelationship(a, ROOT): %v", err)
	}

	if !created {
		t.Fatal("AddRelationship(a, ROOT) reported that nothing was created")
	}

	if !r.HasRelationship(a, root) {
		t.Fatal("relationship (a, ROOT) is not visible")
	}

	removed, err := r.RemoveRelationship(a, root)
	if err != nil {
		t.Fatalf("RemoveRelationship(a, ROOT): %v", err)
	}

	if !removed {
		t.Fatal("RemoveRelationship(a, ROOT) reported that nothing was removed")
	}

	if r.HasRelationship(a, root) {
		t.Fatal("relationship (a, ROOT) still exists after removal")
	}
}

func TestRootCreateNodeGoesThroughRootLayer(t *testing.T) {
	var g Graph

	root, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for ROOT: %v", err)
	}

	r, err := NewRootGraph(&g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	a, err := r.CreateNode()
	if err != nil {
		t.Fatalf("RootView.CreateNode(): %v", err)
	}

	if !g.NodeExists(a) {
		t.Fatalf("new node %d does not exist in primitive graph", a)
	}

	if !r.HasRelationship(root, a) {
		t.Fatalf("new node %d is not visible as a ROOT child", a)
	}
}

func TestRootRelationshipIsVirtual(t *testing.T) {
	var g Graph

	root, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for ROOT: %v", err)
	}

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}

	r, err := NewRootGraph(&g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	if g.HasRelationship(root, a) {
		t.Fatal("ROOT relationship was physically stored in Graph")
	}

	if !r.HasRelationship(root, a) {
		t.Fatal("ROOT relationship is not visible through RootView")
	}
}

func TestRootAddRelationshipDoesNotPhysicallyStoreVirtualRelationship(t *testing.T) {
	var g Graph

	root, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for ROOT: %v", err)
	}

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}

	r, err := NewRootGraph(&g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	created, err := r.AddRelationship(root, a)
	if err != nil {
		t.Fatalf("AddRelationship(ROOT, a): %v", err)
	}

	if created {
		t.Fatal("AddRelationship(ROOT, a) reported a physical relationship was created")
	}

	if g.HasRelationship(root, a) {
		t.Fatal("AddRelationship(ROOT, a) physically stored the virtual relationship")
	}

	if !r.HasRelationship(root, a) {
		t.Fatal("virtual ROOT relationship is missing")
	}
}

func TestRootRemoveRelationshipCannotRemoveVirtualRelationship(t *testing.T) {
	var g Graph

	root, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for ROOT: %v", err)
	}

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}

	r, err := NewRootGraph(&g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	removed, err := r.RemoveRelationship(root, a)
	if err != nil {
		t.Fatalf("RemoveRelationship(ROOT, a): %v", err)
	}

	if removed {
		t.Fatal("RemoveRelationship(ROOT, a) reported removal of a virtual relationship")
	}

	if !r.HasRelationship(root, a) {
		t.Fatal("removing virtual ROOT relationship incorrectly removed it")
	}
}

func TestRootDelegatesNonRootRelationships(t *testing.T) {
	var g Graph

	root, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for ROOT: %v", err)
	}

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}

	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for b: %v", err)
	}

	r, err := NewRootGraph(&g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	if _, err2 := r.AddRelationship(a, b); err2 != nil {
		t.Fatalf("AddRelationship(a, b): %v", err2)
	}

	got, err := r.FindOutgoing(a)
	if err != nil {
		t.Fatalf("FindOutgoing(a): %v", err)
	}

	want := []Relationship{
		{From: a, To: b},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FindOutgoing(a) = %v, want %v", got, want)
	}
}

func TestRootFindRelationshipsIncludesVirtualRelationships(t *testing.T) {
	var g Graph

	root, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for ROOT: %v", err)
	}

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}

	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for b: %v", err)
	}

	if _, err2 := g.AddRelationship(a, b); err2 != nil {
		t.Fatalf("AddRelationship(a, b): %v", err2)
	}

	r, err := NewRootGraph(&g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	got := r.FindRelationships()

	want := []Relationship{
		{From: root, To: a},
		{From: root, To: b},
		{From: a, To: b},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FindRelationships() = %v, want %v", got, want)
	}
}

func TestRootDeleteNodeRemovesOrdinaryNode(t *testing.T) {
	var g Graph

	root, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for ROOT: %v", err)
	}

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}

	r, err := NewRootGraph(&g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	if err := r.DeleteNode(a); err != nil {
		t.Fatalf("DeleteNode(a): %v", err)
	}

	if r.NodeExists(a) {
		t.Fatal("deleted node still exists")
	}

	if r.HasRelationship(root, a) {
		t.Fatal("deleted node is still visible as a ROOT child")
	}
}

func TestRootCannotDeleteRoot(t *testing.T) {
	var g Graph

	root, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	r, err := NewRootGraph(&g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	err = r.DeleteNode(root)
	if !errors.Is(err, ErrCannotDeleteRoot) {
		t.Fatalf("DeleteNode(ROOT) error = %v, want %v", err, ErrCannotDeleteRoot)
	}

	if !r.NodeExists(root) {
		t.Fatal("ROOT disappeared after failed deletion")
	}
}

func TestRootPhysicalRelationshipDoesNotDuplicateVirtualRelationship(t *testing.T) {
	var g Graph

	root, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for ROOT: %v", err)
	}

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}

	if _, err2 := g.AddRelationship(root, a); err2 != nil {
		t.Fatalf("AddRelationship(ROOT, a) in primitive graph: %v", err2)
	}

	r, err := NewRootGraph(&g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	got, err := r.FindOutgoing(root)
	if err != nil {
		t.Fatalf("FindOutgoing(ROOT): %v", err)
	}

	want := []Relationship{
		{From: root, To: a},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FindOutgoing(ROOT) = %v, want %v", got, want)
	}

	got = r.FindRelationships()

	want = []Relationship{
		{From: root, To: a},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FindRelationships() = %v, want %v", got, want)
	}
}
func TestRootPhysicalSelfRelationshipIsHidden(t *testing.T) {
	var g Graph

	root, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for ROOT: %v", err)
	}

	if _, err2 := g.AddRelationship(root, root); err2 != nil {
		t.Fatalf("AddRelationship(ROOT, ROOT) in primitive graph: %v", err2)
	}

	if !g.HasRelationship(root, root) {
		t.Fatal("primitive graph does not contain (ROOT, ROOT)")
	}

	r, err := NewRootGraph(&g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	if r.HasRelationship(root, root) {
		t.Fatal("ROOT self-relationship is visible through RootGraph")
	}

	_, exists, err := r.FindRelationship(root, root)
	if err != nil {
		t.Fatalf("FindRelationship(ROOT, ROOT): %v", err)
	}

	if exists {
		t.Fatal("FindRelationship(ROOT, ROOT) found a relationship")
	}

	got, err := r.FindIncoming(root)
	if err != nil {
		t.Fatalf("FindIncoming(ROOT): %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("FindIncoming(ROOT) = %v, want no relationships", got)
	}

	got = r.FindRelationships()

	if len(got) != 0 {
		t.Fatalf("FindRelationships() = %v, want no relationships", got)
	}
}

func TestFindNodesReturnsSortedExistingNodes(t *testing.T) {
	var g Graph

	if got := g.FindNodes(); len(got) != 0 {
		t.Fatalf("FindNodes() on an empty graph = %v, want empty", got)
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

	if err2 := g.DeleteNode(b); err2 != nil {
		t.Fatalf("DeleteNode(b): %v", err2)
	}

	got := g.FindNodes()
	want := []NodeID{a, c}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FindNodes() = %v, want %v (sorted, deleted node excluded)", got, want)
	}
}

func TestTxnFindNodesReflectsUncommittedCreatesAndRollback(t *testing.T) {
	var g Graph

	existing, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	errForcedRollback := errors.New("forced rollback")

	var created NodeID
	var inside []NodeID

	err = g.Transact(func(tx Tx) error {
		var txErr error
		created, txErr = createNodeTx(tx)
		if txErr != nil {
			return txErr
		}

		inside = tx.FindNodes()

		return errForcedRollback
	})
	if !errors.Is(err, errForcedRollback) {
		t.Fatalf("Transact() error = %v, want %v", err, errForcedRollback)
	}

	wantInside := []NodeID{existing, created}
	if !reflect.DeepEqual(inside, wantInside) {
		t.Fatalf("tx.FindNodes() inside the transaction = %v, want %v", inside, wantInside)
	}

	wantAfter := []NodeID{existing}
	if got := g.FindNodes(); !reflect.DeepEqual(got, wantAfter) {
		t.Fatalf("FindNodes() after rollback = %v, want %v", got, wantAfter)
	}
}

// TestRootGraphInsideGraphActor confirms the stacking rule from
// theorystate.md section 87b: the GraphActor owns the whole stack, and
// the ROOT overlay is visible both outside and inside Transact.
func TestRootGraphInsideGraphActor(t *testing.T) {
	var g Graph

	root, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for ROOT: %v", err)
	}

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}

	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for b: %v", err)
	}

	rootGraph, err := NewRootGraph(&g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	// From here on, g must not be touched directly: the actor owns it.
	actor := NewGraphActor(rootGraph)
	defer actor.Close()

	gotOutgoing, err := actor.FindOutgoing(root)
	if err != nil {
		t.Fatalf("FindOutgoing(ROOT): %v", err)
	}
	wantOutgoing := []Relationship{{From: root, To: a}, {From: root, To: b}}
	if !reflect.DeepEqual(gotOutgoing, wantOutgoing) {
		t.Fatalf("FindOutgoing(ROOT) = %v, want %v", gotOutgoing, wantOutgoing)
	}

	if _, err2 := actor.AddRelationship(a, b); err2 != nil {
		t.Fatalf("AddRelationship(a, b): %v", err2)
	}

	wantAll := []Relationship{{From: root, To: a}, {From: root, To: b}, {From: a, To: b}}
	if all := actor.FindRelationships(); !reflect.DeepEqual(all, wantAll) {
		t.Fatalf("FindRelationships() = %v, want %v", all, wantAll)
	}

	c, err := actor.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for c: %v", err)
	}
	if !actor.HasRelationship(root, c) {
		t.Fatalf("new node %d is not visible as a ROOT child", c)
	}

	if err3 := actor.DeleteNode(root); !errors.Is(err3, ErrCannotDeleteRoot) {
		t.Fatalf("DeleteNode(ROOT) error = %v, want %v", err3, ErrCannotDeleteRoot)
	}

	// The overlay must also be in force inside a transaction.
	var sawVirtualInTx, createdVirtualInTx bool
	var deleteRootErr error

	err = actor.Transact(func(tx Tx) error {
		sawVirtualInTx = tx.HasRelationship(root, a)

		var txErr error
		createdVirtualInTx, txErr = tx.AddRelationship(root, a)
		if txErr != nil {
			return wrapInterfaceErr(txErr)
		}

		deleteRootErr = tx.DeleteNode(root)

		return nil
	})
	if err != nil {
		t.Fatalf("Transact(): %v", err)
	}

	if !sawVirtualInTx {
		t.Fatal("tx.HasRelationship(ROOT, a) = false inside Transact, want true")
	}
	if createdVirtualInTx {
		t.Fatal("tx.AddRelationship(ROOT, a) reported creating a virtual relationship")
	}
	if !errors.Is(deleteRootErr, ErrCannotDeleteRoot) {
		t.Fatalf("tx.DeleteNode(ROOT) error = %v, want %v", deleteRootErr, ErrCannotDeleteRoot)
	}
}

// findDanglingRelationship returns the first relationship in snapshot
// that is not internally consistent for a ROOT view: every relationship
// not sourced at root must have both endpoints (other than root itself
// as a target) among that same snapshot's ROOT children.
func findDanglingRelationship(root NodeID, snapshot []Relationship) (Relationship, bool) {
	children := make(map[NodeID]struct{})
	for _, rel := range snapshot {
		if rel.From == root {
			children[rel.To] = struct{}{}
		}
	}

	for _, rel := range snapshot {
		if rel.From == root {
			continue
		}

		if _, ok := children[rel.From]; !ok {
			return rel, true
		}

		if rel.To == root {
			continue
		}

		if _, ok := children[rel.To]; !ok {
			return rel, true
		}
	}

	return Relationship{}, false
}

// TestGraphActorRootGraphFindRelationshipsIsAtomic checks the property
// the old outside-the-actor layering could not give: a multi-step
// RootGraph method returns one consistent snapshot even while other
// goroutines create and delete nodes.
func TestGraphActorRootGraphFindRelationshipsIsAtomic(t *testing.T) {
	var g Graph

	root, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for ROOT: %v", err)
	}

	rootGraph, err := NewRootGraph(&g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	actor := NewGraphActor(rootGraph)
	defer actor.Close()

	const iterations = 200

	churn := func() error {
		x, err2 := actor.CreateNode()
		if err2 != nil {
			return err2
		}

		y, err3 := actor.CreateNode()
		if err3 != nil {
			return err3
		}

		if _, err4 := actor.AddRelationship(x, y); err4 != nil {
			return err4
		}

		if _, err5 := actor.RemoveRelationship(x, y); err5 != nil {
			return err5
		}

		if err6 := actor.DeleteNode(x); err6 != nil {
			return err6
		}

		return actor.DeleteNode(y)
	}

	finished := make(chan struct{})
	var workerErr error

	go func() {
		defer close(finished)

		for i := 0; i < iterations; i++ {
			if churnErr := churn(); churnErr != nil {
				workerErr = churnErr
				return
			}
		}
	}()

	for running := true; running; {
		snapshot := actor.FindRelationships()

		if bad, found := findDanglingRelationship(root, snapshot); found {
			t.Errorf("FindRelationships() returned %v, whose endpoints are not all ROOT children of the same snapshot", bad)
			running = false
		}

		select {
		case <-finished:
			running = false
		default:
		}
	}

	<-finished

	if workerErr != nil {
		t.Fatalf("churn worker: %v", workerErr)
	}
}

func TestRootFindIncomingIncludesVirtualRootParent(t *testing.T) {
	var g Graph

	// Creation order makes root < a < b, so results sorted by From
	// list root first.
	root, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for ROOT: %v", err)
	}

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}

	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for b: %v", err)
	}

	// A physically stored (ROOT, a) must not produce a duplicate.
	if _, err2 := g.AddRelationship(root, a); err2 != nil {
		t.Fatalf("AddRelationship(ROOT, a): %v", err2)
	}
	if _, err3 := g.AddRelationship(a, b); err3 != nil {
		t.Fatalf("AddRelationship(a, b): %v", err3)
	}

	r, err := NewRootGraph(&g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	gotA, err := r.FindIncoming(a)
	if err != nil {
		t.Fatalf("FindIncoming(a): %v", err)
	}
	if want := []Relationship{{From: root, To: a}}; !reflect.DeepEqual(gotA, want) {
		t.Fatalf("FindIncoming(a) = %v, want %v", gotA, want)
	}

	gotB, err := r.FindIncoming(b)
	if err != nil {
		t.Fatalf("FindIncoming(b): %v", err)
	}
	if want := []Relationship{{From: root, To: b}, {From: a, To: b}}; !reflect.DeepEqual(gotB, want) {
		t.Fatalf("FindIncoming(b) = %v, want %v", gotB, want)
	}

	gotRoot, err := r.FindIncoming(root)
	if err != nil {
		t.Fatalf("FindIncoming(ROOT): %v", err)
	}
	if len(gotRoot) != 0 {
		t.Fatalf("FindIncoming(ROOT) = %v, want none (ROOT is not its own parent)", gotRoot)
	}
}

// TestRootGraphTransactAndCheckerSeeOverlay confirms the overlay is
// presented inside Transact and to registered Checkers, not only to
// direct callers.
func TestRootGraphTransactAndCheckerSeeOverlay(t *testing.T) {
	var g Graph

	root, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for ROOT: %v", err)
	}

	tag, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for tag: %v", err)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for x: %v", err)
	}

	r, err := NewRootGraph(&g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	var checkerSawVirtual bool

	r.RegisterChecker(Checker{
		Name: "overlay-probe",
		Tags: []NodeID{tag},
		Check: func(view GraphReader, _ map[NodeID]struct{}) error {
			checkerSawVirtual = view.HasRelationship(root, x)
			return nil
		},
	})

	var sawVirtualInTx, createdVirtualInTx bool

	err = r.Transact(func(tx Tx) error {
		sawVirtualInTx = tx.HasRelationship(root, x)

		var txErr error
		createdVirtualInTx, txErr = tx.AddRelationship(root, x)
		if txErr != nil {
			return wrapInterfaceErr(txErr)
		}

		return addRelationshipTx(tx, tag, x)
	})
	if err != nil {
		t.Fatalf("Transact(): %v", err)
	}

	if !sawVirtualInTx {
		t.Fatal("tx.HasRelationship(ROOT, x) = false inside Transact, want true")
	}
	if createdVirtualInTx {
		t.Fatal("tx.AddRelationship(ROOT, x) reported creating a virtual relationship")
	}
	if !checkerSawVirtual {
		t.Fatal("the Checker's reader did not present the virtual (ROOT, x) relationship")
	}
	if g.HasRelationship(root, x) {
		t.Fatal("the virtual (ROOT, x) relationship was physically stored")
	}
}

func TestNewRootGraphRejectsGraphActor(t *testing.T) {
	actor := NewGraphActor(&Graph{})
	defer actor.Close()

	root, err := actor.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	if _, err2 := NewRootGraph(actor, root); !errors.Is(err2, ErrRootGraphOverActor) {
		t.Fatalf("NewRootGraph(actor, root) error = %v, want %v", err2, ErrRootGraphOverActor)
	}
}

// TestRootFindRelationshipsWithoutRootNodeEmitsNoVirtualRelationships
// covers ROOT having been deleted through the raw graph, bypassing
// RootGraph.DeleteNode's protection. No virtual relationships may be
// reported, and nothing may panic.
func TestRootFindRelationshipsWithoutRootNodeEmitsNoVirtualRelationships(t *testing.T) {
	var g Graph

	root, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for ROOT: %v", err)
	}

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}

	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for b: %v", err)
	}

	if _, err2 := g.AddRelationship(a, b); err2 != nil {
		t.Fatalf("AddRelationship(a, b): %v", err2)
	}

	r, err := NewRootGraph(&g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	if err3 := g.DeleteNode(root); err3 != nil {
		t.Fatalf("raw DeleteNode(ROOT): %v", err3)
	}

	got := r.FindRelationships()
	want := []Relationship{{From: a, To: b}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FindRelationships() = %v, want %v", got, want)
	}

	if _, err4 := r.FindOutgoing(root); !errors.Is(err4, ErrNodeNotFound) {
		t.Fatalf("FindOutgoing(deleted ROOT) error = %v, want %v", err4, ErrNodeNotFound)
	}
}

// newPointerTestFixture creates a fresh Graph and PointerRegistry with
// AllPointers already bootstrapped, for use by PointerRegistry tests.
func newPointerTestFixture(t *testing.T) (*Graph, *PointerRegistry) {
	t.Helper()

	var g Graph
	names := NewNameRegistry(&g)

	allPointers, err := names.EnsureNamedNode(&g, NameAllPointers)
	if err != nil {
		t.Fatalf("EnsureNamedNode(%q): %v", NameAllPointers, err)
	}

	pointers, err := NewPointerRegistry(&g, allPointers)
	if err != nil {
		t.Fatalf("NewPointerRegistry(): %v", err)
	}

	return &g, pointers
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

	if !g.NodeExists(p) {
		t.Fatalf("NewPointer() returned NodeID %d that does not exist", p)
	}

	if !pointers.IsPointer(g, p) {
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

	if g.HasRelationship(p, x) {
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

	if pointers.IsPointer(g, id) {
		t.Fatalf("node %d is unexpectedly already tagged Pointer-kind", id)
	}

	if err := pointers.TagAsPointer(g, id); err != nil {
		t.Fatalf("TagAsPointer(%d): %v", id, err)
	}

	if !pointers.IsPointer(g, id) {
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

	if pointers.IsPointer(g, id) {
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

func TestTransactCommitsMutationsOnSuccess(t *testing.T) {
	var g Graph

	var a, b NodeID
	err := g.Transact(func(tx Tx) error {
		var err error
		a, err = createNodeTx(tx)
		if err != nil {
			return err
		}

		b, err = createNodeTx(tx)
		if err != nil {
			return err
		}

		return addRelationshipTx(tx, a, b)
	})
	if err != nil {
		t.Fatalf("Transact() returned error: %v", err)
	}

	if !g.NodeExists(a) || !g.NodeExists(b) {
		t.Fatalf("nodes %d, %d do not both exist after successful Transact()", a, b)
	}

	if !g.HasRelationship(a, b) {
		t.Fatalf("relationship (%d,%d) missing after successful Transact()", a, b)
	}
}

func TestTransactRollsBackCreateNodeOnLaterFailure(t *testing.T) {
	var g Graph

	const nonexistent NodeID = 999999

	var id NodeID
	err := g.Transact(func(tx Tx) error {
		var err error
		id, err = createNodeTx(tx)
		if err != nil {
			return err
		}

		err = addRelationshipTx(tx, id, nonexistent)
		return err
	})

	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Transact() error = %v, want %v", err, ErrNodeNotFound)
	}

	if g.NodeExists(id) {
		t.Fatalf("node %d still exists after its creating transaction rolled back", id)
	}
}

func TestTransactRollsBackRelationshipsInLIFOOrder(t *testing.T) {
	var g Graph

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

	const nonexistent NodeID = 999999

	err = g.Transact(func(tx Tx) error {
		if err2 := addRelationshipTx(tx, a, b); err2 != nil {
			return err2
		}

		if err3 := addRelationshipTx(tx, a, c); err3 != nil {
			return err3
		}

		return addRelationshipTx(tx, a, nonexistent)
	})

	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Transact() error = %v, want %v", err, ErrNodeNotFound)
	}

	if g.HasRelationship(a, b) {
		t.Fatalf("relationship (%d,%d) survived a rolled-back transaction", a, b)
	}

	if g.HasRelationship(a, c) {
		t.Fatalf("relationship (%d,%d) survived a rolled-back transaction", a, c)
	}
}

func TestTransactRollsBackRemoveRelationshipOnLaterFailure(t *testing.T) {
	var g Graph

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}

	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for b: %v", err)
	}

	if _, err2 := g.AddRelationship(a, b); err2 != nil {
		t.Fatalf("AddRelationship(%d,%d): %v", a, b, err2)
	}

	const nonexistent NodeID = 999999

	err = g.Transact(func(tx Tx) error {
		if err3 := removeRelationshipTx(tx, a, b); err3 != nil {
			return err3
		}

		return addRelationshipTx(tx, a, nonexistent)
	})

	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Transact() error = %v, want %v", err, ErrNodeNotFound)
	}

	if !g.HasRelationship(a, b) {
		t.Fatalf("relationship (%d,%d) was not restored after rollback", a, b)
	}
}

func TestTransactDoesNotUndoPreexistingRelationship(t *testing.T) {
	var g Graph

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}

	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for b: %v", err)
	}

	if _, err2 := g.AddRelationship(a, b); err2 != nil {
		t.Fatalf("AddRelationship(%d,%d): %v", a, b, err2)
	}

	const nonexistent NodeID = 999999

	err = g.Transact(func(tx Tx) error {
		// (a,b) already exists, so this call reports created == false and
		// must not schedule an undo step for a relationship this
		// transaction did not itself create.
		created, err3 := tx.AddRelationship(a, b)
		if err3 != nil {
			return wrapInterfaceErr(err3)
		}
		if created {
			t.Fatal("AddRelationship() reported creating an already-existing relationship")
		}

		return addRelationshipTx(tx, a, nonexistent)
	})

	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Transact() error = %v, want %v", err, ErrNodeNotFound)
	}

	if !g.HasRelationship(a, b) {
		t.Fatalf("preexisting relationship (%d,%d) was incorrectly removed by rollback", a, b)
	}
}

func TestTransactRollsBackOnPanic(t *testing.T) {
	var g Graph
	var id NodeID

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic to propagate out of Transact()")
			}
		}()

		if err := g.Transact(func(tx Tx) error {
			var err error
			id, err = createNodeTx(tx)
			if err != nil {
				t.Fatalf("CreateNode(): %v", err)
			}

			panic("boom")
		}); err != nil {
			t.Fatalf("Transact() returned error: %v", err)
		}
	}()

	if g.NodeExists(id) {
		t.Fatalf("node %d still exists after a panicking transaction", id)
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

func newPointerMetadataTestFixture(t *testing.T) (*Graph, *PointerMetadataRegistry) {
	t.Helper()

	var g Graph
	names := NewNameRegistry(&g)

	ids, err := names.BootstrapNames(&g, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	metadata, err := NewPointerMetadataRegistry(&g, ids[NameAllPointerMetadata], ids[NameAllPointerMetadataSubjectSlot])
	if err != nil {
		t.Fatalf("NewPointerMetadataRegistry(): %v", err)
	}

	return &g, metadata
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

func newPointerMetadataDTestFixture(t *testing.T) (*Graph, *PointerMetadataRegistryD) {
	t.Helper()

	var g Graph
	names := NewNameRegistry(&g)

	ids, err := names.BootstrapNames(&g, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	metadata, err := NewPointerMetadataRegistryD(&g, ids[NameAllPointerMetadata], ids[NameAllPointerMetadataSubjectSlot], ids[NameAllPointerMetadataTargetSlot])
	if err != nil {
		t.Fatalf("NewPointerMetadataRegistryD(): %v", err)
	}

	return &g, metadata
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

func newCapsuleTestFixture(t *testing.T) (*Graph, *CapsuleRegistry) {
	t.Helper()

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

	return &g, capsules
}

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

	if !g.NodeExists(capsule) {
		t.Fatalf("NewCapsule() returned NodeID %d that does not exist", capsule)
	}

	if !capsules.IsCapsule(g, capsule) {
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

	if g.NodeExists(capsule) {
		t.Fatalf("capsule %d still exists after DeleteCapsule()", capsule)
	}
	if g.NodeExists(prevSlot) {
		t.Fatalf("prevSlot %d still exists after DeleteCapsule()", prevSlot)
	}
	if g.NodeExists(valueSlot) {
		t.Fatalf("valueSlot %d still exists after DeleteCapsule()", valueSlot)
	}
	if g.NodeExists(nextSlot) {
		t.Fatalf("nextSlot %d still exists after DeleteCapsule()", nextSlot)
	}

	if !g.NodeExists(value) {
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

	if !g.NodeExists(capsule) {
		t.Fatal("capsule disappeared despite a failed DeleteCapsule()")
	}
	if !capsules.IsCapsule(g, capsule) {
		t.Fatal("capsule lost its AllElementCapsules tag despite a failed DeleteCapsule()")
	}
	if !g.HasRelationship(list, capsule) {
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

	if !g.NodeExists(c1) {
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

	if !g.NodeExists(capsule) || !g.NodeExists(valueSlot) {
		t.Fatal("capsule or valueSlot disappeared despite a failed DeleteCapsule()")
	}
	if !g.HasRelationship(metadata, valueSlot) {
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

		if g.HasRelationship(allPointers, slot) {
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

func newListTestFixture(t *testing.T) (*Graph, *CapsuleRegistry, *ListRegistry) {
	t.Helper()

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

	lists, err := NewListRegistry(&g, capsules, ids[NameAllLists], ids[NameAllHeads], ids[NameAllTails])
	if err != nil {
		t.Fatalf("NewListRegistry(): %v", err)
	}

	return &g, capsules, lists
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

	if !g.NodeExists(list) {
		t.Fatalf("NewList() returned NodeID %d that does not exist", list)
	}

	if !lists.IsList(g, list) {
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
	if g.HasRelationship(lists.allTails, capsuleA) {
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
	if g.HasRelationship(list, bogus) {
		t.Fatal("list still contains bogus after the Checker declined the commit")
	}
	if g.HasRelationship(lists.allHeads, bogus) {
		t.Fatal("bogus is still tagged AllHeads after the Checker declined the commit")
	}

	if _, hasHead, err3 := lists.Head(g, list); err3 != nil {
		t.Fatalf("Head(list): %v", err3)
	} else if hasHead {
		t.Fatal("list unexpectedly has a head after the Checker declined the commit")
	}
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
	if !lists.capsules.IsCapsule(g, capsuleB) {
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

	if !g.NodeExists(list) {
		t.Fatalf("list %d disappeared even though deletion should have failed", list)
	}

	if !lists.IsList(g, list) {
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

	if g.NodeExists(list) {
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

	if g.NodeExists(list) {
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

	if g.NodeExists(capsule) {
		t.Fatalf("capsule %d still exists after Remove()", capsule)
	}

	if g.HasRelationship(list, capsule) {
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
	if g.HasRelationship(list, capsule) {
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
	if !g.NodeExists(capsule) {
		t.Fatal("capsule was deleted despite still being referenced elsewhere")
	}
	if !capsules.IsCapsule(g, capsule) {
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
	if !capsules.IsCapsule(g, capsule) || !g.NodeExists(capsule) {
		t.Fatal("detached capsule was unexpectedly deleted or untagged")
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
	if !g.NodeExists(capsule) {
		t.Fatal("unused test node unexpectedly absent")
	}
}

func TestAdversarialSameCapsuleMayBeReferencedByTwoListsOnlyIfTopologyAllowsIt(t *testing.T) {
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

	if _, err := lists.Elements(g, listA); !errors.Is(err, nil) {
		t.Fatalf("Elements(listA) error = %v, want nil", err)
	}
	if _, err := lists.Elements(g, listB); !errors.Is(err, nil) {
		t.Fatalf("Elements(listB) error = %v, want nil", err)
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

// newSetTestFixture creates a fresh Graph and SetRegistry with AllSets
// already bootstrapped, for use by SetRegistry tests.
func newSetTestFixture(t *testing.T) (*Graph, *SetRegistry) {
	t.Helper()

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

	return &g, sets
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

	if !g.NodeExists(set) {
		t.Fatalf("NewSet() returned NodeID %d that does not exist", set)
	}

	if !sets.IsSet(g, set) {
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

	if sets.IsSet(g, id) {
		t.Fatalf("node %d is unexpectedly already tagged Set-kind", id)
	}

	if err := sets.TagAsSet(g, id); err != nil {
		t.Fatalf("TagAsSet(%d): %v", id, err)
	}

	if !sets.IsSet(g, id) {
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

	if g.NodeExists(set) {
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

	if !g.NodeExists(set) {
		t.Fatal("set disappeared despite a failed DeleteSet()")
	}
	if !sets.IsSet(g, set) {
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

	if !g.NodeExists(set) {
		t.Fatal("set disappeared despite a failed DeleteSet()")
	}
	if !sets.IsSet(g, set) {
		t.Fatal("set lost its AllSets tag despite a failed DeleteSet()")
	}
	if !g.HasRelationship(referrer, set) {
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

	if sets.IsSet(g, composite) {
		t.Fatal("node was tagged AllSets despite already being AllCompositeSets-tagged")
	}
}

// newCompositeSetTestFixture creates a fresh Graph, SetRegistry, and
// CompositeSetRegistry with every relevant name already bootstrapped, for
// use by CompositeSetRegistry tests.
func newCompositeSetTestFixture(t *testing.T) (*Graph, *SetRegistry, *CompositeSetRegistry) {
	t.Helper()

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

	return &g, sets, composites
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

	if !g.NodeExists(set) {
		t.Fatalf("NewCompositeSet() returned NodeID %d that does not exist", set)
	}
	if !composites.IsCompositeSet(g, set) {
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

	if g.NodeExists(u) {
		t.Fatalf("descriptor %d still exists after RemoveOperand()", u)
	}

	got, err := composites.Evaluate(g, set)
	if err != nil {
		t.Fatalf("Evaluate(): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Evaluate() = %v, want empty after RemoveOperand()", got)
	}

	if !g.NodeExists(x) {
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
	if g.NodeExists(set) {
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
	if !g.NodeExists(set) {
		t.Fatal("composite set disappeared despite a failed DeleteCompositeSet()")
	}
	if !composites.IsCompositeSet(g, set) {
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
func newCompositeSetLogTestFixture(t *testing.T) (*Graph, *SetRegistry, *CompositeSetRegistry, *CompositeSetLogRegistry) {
	t.Helper()

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

	composites.SetLogs(logs)

	return &g, sets, composites, logs
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

	if !g.NodeExists(log) {
		t.Fatalf("NewCompositeSetLog() returned NodeID %d that does not exist", log)
	}
	if !logs.IsCompositeSetLog(g, log) {
		t.Fatalf("NewCompositeSetLog() did not tag %d as a composite set log", log)
	}
	if !logs.lists.IsList(g, log) {
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

	if !g.HasRelationship(log, capsule) {
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

	if g.NodeExists(u) {
		t.Fatalf("descriptor %d still exists after RemoveOperation()", u)
	}
	if g.NodeExists(capsule) {
		t.Fatalf("capsule %d still exists after RemoveOperation()", capsule)
	}
	if !g.NodeExists(x) {
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
	if g.NodeExists(log) {
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
	if !g.NodeExists(log) {
		t.Fatal("log disappeared despite a failed DeleteCompositeSetLog()")
	}
	if !logs.IsCompositeSetLog(g, log) {
		t.Fatal("log lost its AllCompositeSetLogs tag despite a failed DeleteCompositeSetLog()")
	}
	if !logs.lists.IsList(g, log) {
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

	err = g.Transact(func(tx Tx) error {
		if err2 := addRelationshipTx(tx, log, bogus); err2 != nil {
			return err2
		}
		return addRelationshipTx(tx, logs.lists.allHeads, bogus)
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

	if sets.IsSet(g, log) {
		t.Fatal("node was tagged AllSets despite already being AllCompositeSetLogs-tagged")
	}
}

func TestFoundationalNamesIncludesAllDomainSlot(t *testing.T) {
	for _, name := range FoundationalNames {
		if name == NameAllDomainSlot {
			return
		}
	}

	t.Fatalf("FoundationalNames %v does not include %q", FoundationalNames, NameAllDomainSlot)
}

// domainPointerTestFixture bundles every registry
// newDomainPointerTestFixture constructs. Returning one struct instead of
// six separate values keeps newDomainPointerTestFixture under gocritic's
// tooManyResultsChecker limit (max 5) without losing any of the
// individual registries call sites need -- each is still just a field
// access away.
type domainPointerTestFixture struct {
	graph      *Graph
	names      *NameRegistry
	sets       *SetRegistry
	composites *CompositeSetRegistry
	logs       *CompositeSetLogRegistry
	capsules   *CapsuleRegistry
	lists      *ListRegistry
	pointers   *PointerRegistry
	domainB    *DomainPointerRegistryB
	domainD    *DomainPointerRegistryD
}

// newDomainPointerTestFixture creates a fresh Graph plus every registry
// needed to exercise both DomainPointerRegistryB and
// DomainPointerRegistryD against one shared graph: SetRegistry,
// CompositeSetRegistry, CompositeSetLogRegistry (fully wired via
// SetLogs), a plain PointerRegistry for Representation B usage
// (AllSubPointers), PointerMetadataRegistryD, and a single shared
// domainSlots PointerRegistry (AllDomainSlot) passed to both domain
// wrappers, per domainConstraint's doc comment.
func newDomainPointerTestFixture(t *testing.T) *domainPointerTestFixture {
	t.Helper()

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
	composites.SetLogs(logs)

	pointers, err := NewPointerRegistry(&g, ids[NameAllPointers])
	if err != nil {
		t.Fatalf("NewPointerRegistry(AllPointers): %v", err)
	}

	subPointers, err := NewPointerRegistry(&g, ids[NameAllSubPointers])
	if err != nil {
		t.Fatalf("NewPointerRegistry(AllSubPointers): %v", err)
	}

	metadata, err := NewPointerMetadataRegistryD(&g, ids[NameAllPointerMetadata], ids[NameAllPointerMetadataSubjectSlot], ids[NameAllPointerMetadataTargetSlot])
	if err != nil {
		t.Fatalf("NewPointerMetadataRegistryD(): %v", err)
	}

	domainSlots, err := NewPointerRegistry(&g, ids[NameAllDomainSlot])
	if err != nil {
		t.Fatalf("NewPointerRegistry(AllDomainSlot): %v", err)
	}

	domainB := NewDomainPointerRegistryB(&g, subPointers, domainSlots, sets, composites, logs)
	domainD := NewDomainPointerRegistryD(&g, metadata, domainSlots, sets, composites, logs)

	return &domainPointerTestFixture{
		graph:      &g,
		names:      names,
		sets:       sets,
		composites: composites,
		logs:       logs,
		capsules:   capsules,
		lists:      lists,
		pointers:   pointers,
		domainB:    domainB,
		domainD:    domainD,
	}
}

func TestDomainPointerRegistryBNewDomainPointerAndTargetWithNoDomain(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	p, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if err2 := fx.domainB.NewDomainPointer(fx.graph, p); err2 != nil {
		t.Fatalf("NewDomainPointer(p): %v", err2)
	}

	x, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if err3 := fx.domainB.SetTarget(fx.graph, p, x); err3 != nil {
		t.Fatalf("SetTarget(p, x) with no domain set: %v", err3)
	}

	target, hasTarget, err := fx.domainB.Target(fx.graph, p)
	if err != nil {
		t.Fatalf("Target(p): %v", err)
	}
	if !hasTarget || target != x {
		t.Fatalf("Target(p) = (%d,%v), want (%d,true)", target, hasTarget, x)
	}
}

// TestDomainPointerRegistryBNewDomainPointerIsIdempotent covers a real
// gap found on review: NewDomainPointer previously minted a fresh
// sub-pointer node unconditionally on every call, so calling it twice
// for the same anchor silently gave that anchor two children both
// tagged via the underlying PointerRegistry's own tag, making every
// subsequent subPointer-based lookup (Target/SetTarget/RemoveTarget)
// fail with ErrAmbiguousPointerMetadata. NewDomainPointer must instead
// be idempotent: a second call for the same anchor is a no-op, and the
// pointer's existing target survives untouched.
func TestDomainPointerRegistryBNewDomainPointerIsIdempotent(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	p, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if err2 := fx.domainB.NewDomainPointer(fx.graph, p); err2 != nil {
		t.Fatalf("first NewDomainPointer(p): %v", err2)
	}

	x, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if err3 := fx.domainB.SetTarget(fx.graph, p, x); err3 != nil {
		t.Fatalf("SetTarget(p, x): %v", err3)
	}

	if err4 := fx.domainB.NewDomainPointer(fx.graph, p); err4 != nil {
		t.Fatalf("second NewDomainPointer(p): %v", err4)
	}

	target, hasTarget, err := fx.domainB.Target(fx.graph, p)
	if err != nil {
		t.Fatalf("Target(p) after second NewDomainPointer(): %v", err)
	}
	if !hasTarget || target != x {
		t.Fatalf("Target(p) = (%d,%v), want (%d,true) -- unaffected by the idempotent second NewDomainPointer() call", target, hasTarget, x)
	}
}

func TestDomainPointerRegistryBSetDomainEnforcesMembership(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	p, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if err2 := fx.domainB.NewDomainPointer(fx.graph, p); err2 != nil {
		t.Fatalf("NewDomainPointer(p): %v", err2)
	}

	domainSet, err := fx.sets.NewSet(fx.graph)
	if err != nil {
		t.Fatalf("NewSet() for domain: %v", err)
	}
	allowed, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err3 := fx.sets.Add(fx.graph, domainSet, allowed); err3 != nil {
		t.Fatal(err3)
	}

	if err4 := fx.domainB.SetDomain(fx.graph, p, domainSet); err4 != nil {
		t.Fatalf("SetDomain(p, domainSet): %v", err4)
	}

	if err5 := fx.domainB.SetTarget(fx.graph, p, allowed); err5 != nil {
		t.Fatalf("SetTarget(p, allowed) within domain: %v", err5)
	}

	disallowed, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	err = fx.domainB.SetTarget(fx.graph, p, disallowed)
	if !errors.Is(err, ErrTargetOutsideDomain) {
		t.Fatalf("SetTarget(p, disallowed) error = %v, want %v", err, ErrTargetOutsideDomain)
	}

	target, hasTarget, err := fx.domainB.Target(fx.graph, p)
	if err != nil {
		t.Fatalf("Target(p): %v", err)
	}
	if !hasTarget || target != allowed {
		t.Fatalf("Target(p) = (%d,%v), want (%d,true) -- unaffected by the rejected SetTarget", target, hasTarget, allowed)
	}
}

func TestDomainPointerRegistryBSetDomainRejectsNonSetTaggedNode(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	p, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if err2 := fx.domainB.NewDomainPointer(fx.graph, p); err2 != nil {
		t.Fatalf("NewDomainPointer(p): %v", err2)
	}

	notASet, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	err = fx.domainB.SetDomain(fx.graph, p, notASet)
	if !errors.Is(err, ErrInvalidSetOperand) {
		t.Fatalf("SetDomain(p, notASet) error = %v, want %v", err, ErrInvalidSetOperand)
	}
}

func TestDomainPointerRegistryBSetDomainRejectsWhenCurrentTargetOutsideNewDomain(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	p, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if err2 := fx.domainB.NewDomainPointer(fx.graph, p); err2 != nil {
		t.Fatalf("NewDomainPointer(p): %v", err2)
	}

	x, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if err3 := fx.domainB.SetTarget(fx.graph, p, x); err3 != nil {
		t.Fatalf("SetTarget(p, x): %v", err3)
	}

	domainSet, err := fx.sets.NewSet(fx.graph)
	if err != nil {
		t.Fatal(err)
	}
	// domainSet deliberately does not contain x.

	err = fx.domainB.SetDomain(fx.graph, p, domainSet)
	if !errors.Is(err, ErrTargetOutsideDomain) {
		t.Fatalf("SetDomain(p, domainSet) error = %v, want %v", err, ErrTargetOutsideDomain)
	}

	_, hasDomain, err := fx.domainB.Domain(fx.graph, p)
	if err != nil {
		t.Fatalf("Domain(p): %v", err)
	}
	if hasDomain {
		t.Fatal("Domain(p) reports a domain despite the rejected SetDomain")
	}
}

func TestDomainPointerRegistryBRemoveDomainAllowsAnyTargetAgain(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	p, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if err2 := fx.domainB.NewDomainPointer(fx.graph, p); err2 != nil {
		t.Fatalf("NewDomainPointer(p): %v", err2)
	}

	domainSet, err := fx.sets.NewSet(fx.graph)
	if err != nil {
		t.Fatal(err)
	}
	if err3 := fx.domainB.SetDomain(fx.graph, p, domainSet); err3 != nil {
		t.Fatalf("SetDomain(p, domainSet): %v", err3)
	}

	outside, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	if err4 := fx.domainB.SetTarget(fx.graph, p, outside); !errors.Is(err4, ErrTargetOutsideDomain) {
		t.Fatalf("SetTarget(p, outside) before RemoveDomain error = %v, want %v", err4, ErrTargetOutsideDomain)
	}

	removed, err := fx.domainB.RemoveDomain(fx.graph, p)
	if err != nil {
		t.Fatalf("RemoveDomain(p): %v", err)
	}
	if !removed {
		t.Fatal("RemoveDomain() reported that nothing was removed")
	}

	if err5 := fx.domainB.SetTarget(fx.graph, p, outside); err5 != nil {
		t.Fatalf("SetTarget(p, outside) after RemoveDomain: %v", err5)
	}
}

func TestDomainPointerRegistryBSetTargetRequiresExistingSubPointer(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	p, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately not calling NewDomainPointer(p).

	x, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	err = fx.domainB.SetTarget(fx.graph, p, x)
	if !errors.Is(err, ErrNotPointer) {
		t.Fatalf("SetTarget(p, x) without a sub-pointer error = %v, want %v", err, ErrNotPointer)
	}
}

func TestDomainPointerRegistryDTargetWithNoDomainAllowsAnyTarget(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	subject, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	x, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	if err2 := fx.domainD.SetTarget(fx.graph, subject, x); err2 != nil {
		t.Fatalf("SetTarget(subject, x) with no domain: %v", err2)
	}

	target, hasTarget, err := fx.domainD.Target(fx.graph, subject)
	if err != nil {
		t.Fatalf("Target(subject): %v", err)
	}
	if !hasTarget || target != x {
		t.Fatalf("Target(subject) = (%d,%v), want (%d,true)", target, hasTarget, x)
	}
}

func TestDomainPointerRegistryDSetDomainEnforcesMembership(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	subject, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	domainSet, err := fx.sets.NewSet(fx.graph)
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err2 := fx.sets.Add(fx.graph, domainSet, allowed); err2 != nil {
		t.Fatal(err2)
	}

	if err3 := fx.domainD.SetDomain(fx.graph, subject, domainSet); err3 != nil {
		t.Fatalf("SetDomain(subject, domainSet): %v", err3)
	}

	if err4 := fx.domainD.SetTarget(fx.graph, subject, allowed); err4 != nil {
		t.Fatalf("SetTarget(subject, allowed): %v", err4)
	}

	disallowed, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	err = fx.domainD.SetTarget(fx.graph, subject, disallowed)
	if !errors.Is(err, ErrTargetOutsideDomain) {
		t.Fatalf("SetTarget(subject, disallowed) error = %v, want %v", err, ErrTargetOutsideDomain)
	}
}

func TestDomainPointerRegistryDSetDomainRejectsNonSetTaggedNode(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	subject, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	notASet, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	err = fx.domainD.SetDomain(fx.graph, subject, notASet)
	if !errors.Is(err, ErrInvalidSetOperand) {
		t.Fatalf("SetDomain(subject, notASet) error = %v, want %v", err, ErrInvalidSetOperand)
	}
}

func TestDomainPointerRegistryDSetDomainRejectsWhenCurrentTargetOutsideNewDomain(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	subject, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	x, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if err2 := fx.domainD.SetTarget(fx.graph, subject, x); err2 != nil {
		t.Fatalf("SetTarget(subject, x): %v", err2)
	}

	domainSet, err := fx.sets.NewSet(fx.graph)
	if err != nil {
		t.Fatal(err)
	}
	// domainSet deliberately does not contain x.

	err = fx.domainD.SetDomain(fx.graph, subject, domainSet)
	if !errors.Is(err, ErrTargetOutsideDomain) {
		t.Fatalf("SetDomain(subject, domainSet) error = %v, want %v", err, ErrTargetOutsideDomain)
	}

	_, hasDomain, err := fx.domainD.Domain(fx.graph, subject)
	if err != nil {
		t.Fatalf("Domain(subject): %v", err)
	}
	if hasDomain {
		t.Fatal("Domain(subject) reports a domain despite the rejected SetDomain")
	}
}

func TestDomainPointerRegistryDRemoveDomainAllowsAnyTargetAgain(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	subject, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	domainSet, err := fx.sets.NewSet(fx.graph)
	if err != nil {
		t.Fatal(err)
	}
	if err2 := fx.domainD.SetDomain(fx.graph, subject, domainSet); err2 != nil {
		t.Fatalf("SetDomain(subject, domainSet): %v", err2)
	}

	outside, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if err3 := fx.domainD.SetTarget(fx.graph, subject, outside); !errors.Is(err3, ErrTargetOutsideDomain) {
		t.Fatalf("SetTarget(subject, outside) before RemoveDomain error = %v, want %v", err3, ErrTargetOutsideDomain)
	}

	removed, err := fx.domainD.RemoveDomain(fx.graph, subject)
	if err != nil {
		t.Fatalf("RemoveDomain(subject): %v", err)
	}
	if !removed {
		t.Fatal("RemoveDomain() reported that nothing was removed")
	}

	if err4 := fx.domainD.SetTarget(fx.graph, subject, outside); err4 != nil {
		t.Fatalf("SetTarget(subject, outside) after RemoveDomain: %v", err4)
	}
}

func TestDomainPointerRegistryDDomainViaCompositeSet(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	subject, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	domainComposite, err := fx.composites.NewCompositeSet(fx.graph)
	if err != nil {
		t.Fatalf("NewCompositeSet(): %v", err)
	}
	allowed, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err2 := fx.composites.AddOperand(fx.graph, domainComposite, allowed, true, false); err2 != nil {
		t.Fatalf("AddOperand(): %v", err2)
	}

	if err3 := fx.domainD.SetDomain(fx.graph, subject, domainComposite); err3 != nil {
		t.Fatalf("SetDomain(subject, domainComposite): %v", err3)
	}

	if err4 := fx.domainD.SetTarget(fx.graph, subject, allowed); err4 != nil {
		t.Fatalf("SetTarget(subject, allowed): %v", err4)
	}

	disallowed, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	err = fx.domainD.SetTarget(fx.graph, subject, disallowed)
	if !errors.Is(err, ErrTargetOutsideDomain) {
		t.Fatalf("SetTarget(subject, disallowed) error = %v, want %v", err, ErrTargetOutsideDomain)
	}
}

func TestDomainPointerRegistryDDomainViaCompositeSetLog(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	subject, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	domainLog, err := fx.logs.NewCompositeSetLog(fx.graph)
	if err != nil {
		t.Fatalf("NewCompositeSetLog(): %v", err)
	}
	allowed, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err2 := fx.logs.AppendOperation(fx.graph, domainLog, allowed, true, false); err2 != nil {
		t.Fatalf("AppendOperation(): %v", err2)
	}

	if err3 := fx.domainD.SetDomain(fx.graph, subject, domainLog); err3 != nil {
		t.Fatalf("SetDomain(subject, domainLog): %v", err3)
	}

	if err4 := fx.domainD.SetTarget(fx.graph, subject, allowed); err4 != nil {
		t.Fatalf("SetTarget(subject, allowed): %v", err4)
	}

	disallowed, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	err = fx.domainD.SetTarget(fx.graph, subject, disallowed)
	if !errors.Is(err, ErrTargetOutsideDomain) {
		t.Fatalf("SetTarget(subject, disallowed) error = %v, want %v", err, ErrTargetOutsideDomain)
	}
}

// TestDomainPointerRegistryDCheckerCatchesOutOfBandTargetChange
// demonstrates the Checker registered by NewDomainPointerRegistryD
// catching a domain violation introduced by bypassing
// DomainPointerRegistryD entirely and mutating the underlying
// PointerMetadataRegistryD directly. This still goes through
// Graph.Transact internally (see PointerMetadataRegistryD.SetTarget), so
// the Checker still sees and rejects it -- the counterpart to
// DomainPointerRegistryB's documented lack of an equivalent Checker.
func TestDomainPointerRegistryDCheckerCatchesOutOfBandTargetChange(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	subject, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	domainSet, err := fx.sets.NewSet(fx.graph)
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err2 := fx.sets.Add(fx.graph, domainSet, allowed); err2 != nil {
		t.Fatal(err2)
	}
	if err3 := fx.domainD.SetDomain(fx.graph, subject, domainSet); err3 != nil {
		t.Fatalf("SetDomain(subject, domainSet): %v", err3)
	}

	disallowed, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	err = fx.domainD.metadata.SetTarget(fx.graph, subject, disallowed)
	if !errors.Is(err, ErrTargetOutsideDomain) {
		t.Fatalf("bypassing SetTarget() error = %v, want %v", err, ErrTargetOutsideDomain)
	}

	target, hasTarget, err := fx.domainD.Target(fx.graph, subject)
	if err != nil {
		t.Fatalf("Target(subject): %v", err)
	}
	if hasTarget {
		t.Fatalf("Target(subject) = (%d,true), want no target after the Checker declined the bypassing commit", target)
	}
}

// TestDomainPointerRegistryDCheckerCatchesOutOfBandDomainChange covers
// the other trigger direction: bypassing DomainPointerRegistryD.SetDomain
// and reassigning the domain slot's target directly through the shared
// domainSlots PointerRegistry, to a domain that no longer contains the
// pointer's current target.
func TestDomainPointerRegistryDCheckerCatchesOutOfBandDomainChange(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	subject, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	firstDomain, err := fx.sets.NewSet(fx.graph)
	if err != nil {
		t.Fatal(err)
	}
	x, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err2 := fx.sets.Add(fx.graph, firstDomain, x); err2 != nil {
		t.Fatal(err2)
	}
	if err3 := fx.domainD.SetDomain(fx.graph, subject, firstDomain); err3 != nil {
		t.Fatalf("SetDomain(subject, firstDomain): %v", err3)
	}
	if err4 := fx.domainD.SetTarget(fx.graph, subject, x); err4 != nil {
		t.Fatalf("SetTarget(subject, x): %v", err4)
	}

	secondDomain, err := fx.sets.NewSet(fx.graph)
	if err != nil {
		t.Fatal(err)
	}
	// secondDomain deliberately does not contain x.

	m, _, found, err := fx.domainD.metadata.locate(fx.graph, subject)
	if err != nil || !found {
		t.Fatalf("locate(subject): found=%v err=%v", found, err)
	}

	slot, found, err := fx.domainD.domainSlotFor(fx.graph, m)
	if err != nil || !found {
		t.Fatalf("domainSlotFor(m): found=%v err=%v", found, err)
	}

	err = fx.domainD.domainSlots.SetTarget(fx.graph, slot, secondDomain)
	if !errors.Is(err, ErrTargetOutsideDomain) {
		t.Fatalf("bypassing SetDomain() error = %v, want %v", err, ErrTargetOutsideDomain)
	}

	domain, hasDomain, err := fx.domainD.Domain(fx.graph, subject)
	if err != nil {
		t.Fatalf("Domain(subject): %v", err)
	}
	if !hasDomain || domain != firstDomain {
		t.Fatalf("Domain(subject) = (%d,%v), want (%d,true) -- unaffected by the declined bypass", domain, hasDomain, firstDomain)
	}
}

// TestCrossRoleNodeParticipatesInMultipleStructuresSimultaneously is the
// first of a planned "cross-registry adversarial" test family: instead
// of testing one registry in isolation, it builds a small cluster of
// nodes that each simultaneously participate in several different
// higher-level interpretations at once, and checks that no registry's
// own assumptions leak into another registry's behavior merely because
// they happen to share a node. This is a direct exercise of
// theorystate.md section 7a's "a fact never acquires a universal
// semantic meaning merely because one processor gives it meaning"
// principle, and of section 9a/68's claim that the same node identity
// can participate in many roles without the primitive graph needing to
// know about any of them.
//
// The scenario built here:
//   - S is a plain Set with two ordinary members (m1, m2).
//   - L is a List; S itself (not S's members) is appended as L's sole
//     element value, so S is simultaneously a Set and a List element
//     value.
//   - C is a CompositeSet with one additive, set-expansion operand
//     targeting S, so S is simultaneously a Set, a List element value,
//     and a CompositeSet operand target.
//   - P is a Representation A Pointer whose target is L, so L is
//     simultaneously a List and a Pointer target.
//   - A Representation D Domain Pointer's domain is set to C, and its
//     target is validated against C's evaluated membership (which
//     resolves recursively through S), so C is simultaneously a
//     CompositeSet and a Domain Pointer's domain.
//
// Each higher-level operation below is checked to behave exactly as it
// would in isolation, and the final step confirms that adding a new
// member to S -- multiply-interpreted as it now is -- is immediately
// visible through every layer built on top of it, with nothing needing
// to be explicitly re-synced (every Evaluate/Members in this file is
// deliberately never cached; theorystate.md sections 9a/35/81).
func TestCrossRoleNodeParticipatesInMultipleStructuresSimultaneously(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	s, err := fx.sets.NewSet(fx.graph)
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}
	m1, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	m2, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err2 := fx.sets.Add(fx.graph, s, m1); err2 != nil {
		t.Fatalf("Add(s, m1): %v", err2)
	}
	if _, err3 := fx.sets.Add(fx.graph, s, m2); err3 != nil {
		t.Fatalf("Add(s, m2): %v", err3)
	}

	list, err := fx.lists.NewList(fx.graph)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}
	if _, err4 := fx.lists.Append(fx.graph, list, s); err4 != nil {
		t.Fatalf("Append(list, s): %v", err4)
	}

	// S must still behave as an ordinary Set: being a list element
	// value must not disturb its own membership.
	members, err := fx.sets.Members(fx.graph, s)
	if err != nil {
		t.Fatalf("Members(s) after s became a list value: %v", err)
	}
	wantMembers := sortedNodeIDs([]NodeID{m1, m2})
	if got := sortedNodeIDs(members); !reflect.DeepEqual(got, wantMembers) {
		t.Fatalf("Members(s) = %v, want %v", got, wantMembers)
	}

	// The list must correctly report S as its sole element's value.
	elements, err := fx.lists.Elements(fx.graph, list)
	if err != nil {
		t.Fatalf("Elements(list): %v", err)
	}
	if want := []NodeID{s}; !reflect.DeepEqual(elements, want) {
		t.Fatalf("Elements(list) = %v, want %v", elements, want)
	}

	composite, err := fx.composites.NewCompositeSet(fx.graph)
	if err != nil {
		t.Fatalf("NewCompositeSet(): %v", err)
	}
	if _, err5 := fx.composites.AddOperand(fx.graph, composite, s, true, true); err5 != nil {
		t.Fatalf("AddOperand(composite, s, additive, expand): %v", err5)
	}

	evaluated, err := fx.composites.Evaluate(fx.graph, composite)
	if err != nil {
		t.Fatalf("Evaluate(composite): %v", err)
	}
	if got := sortedNodeIDs(evaluated); !reflect.DeepEqual(got, wantMembers) {
		t.Fatalf("Evaluate(composite) = %v, want %v (S's own members, resolved through the operand)", got, wantMembers)
	}

	p, err := fx.pointers.NewPointer(fx.graph)
	if err != nil {
		t.Fatalf("NewPointer(): %v", err)
	}
	if err6 := fx.pointers.SetTarget(fx.graph, p, list); err6 != nil {
		t.Fatalf("SetTarget(p, list): %v", err6)
	}

	target, hasTarget, err := fx.pointers.Target(fx.graph, p)
	if err != nil {
		t.Fatalf("Target(p): %v", err)
	}
	if !hasTarget || target != list {
		t.Fatalf("Target(p) = (%d,%v), want (%d,true) -- Pointer target unaffected by list's other roles", target, hasTarget, list)
	}

	// The list must still behave as an ordinary list despite also being
	// a Pointer's target.
	if _, hasHead, err11 := fx.lists.Head(fx.graph, list); err11 != nil {
		t.Fatalf("Head(list) after list became a pointer target: %v", err11)
	} else if !hasHead {
		t.Fatal("Head(list) unexpectedly empty after list became a pointer target")
	}

	subject, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if err7 := fx.domainD.SetDomain(fx.graph, subject, composite); err7 != nil {
		t.Fatalf("SetDomain(subject, composite): %v", err7)
	}

	if err8 := fx.domainD.SetTarget(fx.graph, subject, m1); err8 != nil {
		t.Fatalf("SetTarget(subject, m1) within domain: %v", err8)
	}

	outside, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	err = fx.domainD.SetTarget(fx.graph, subject, outside)
	if !errors.Is(err, ErrTargetOutsideDomain) {
		t.Fatalf("SetTarget(subject, outside) error = %v, want %v", err, ErrTargetOutsideDomain)
	}

	// Adding a new member to S -- simultaneously a Set, a list value,
	// and a CompositeSet operand target -- must be immediately visible
	// through the CompositeSet's own Evaluate, and therefore through the
	// domain pointer's own membership check, live.
	m3, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err9 := fx.sets.Add(fx.graph, s, m3); err9 != nil {
		t.Fatalf("Add(s, m3): %v", err9)
	}
	if err10 := fx.domainD.SetTarget(fx.graph, subject, m3); err10 != nil {
		t.Fatalf("SetTarget(subject, m3) after S gained a new member live: %v", err10)
	}
}

// TestCrossRoleDomainPointerDetectsCycleIntroducedThroughDomainItself is
// the second cross-role test: it builds a Domain Pointer whose domain is
// a CompositeSet that resolves through a CompositeSetLog, and confirms
// that a cycle introduced later -- purely by mutating the domain
// structure itself, never the pointer, its metadata, or either slot
// directly -- surfaces through domain validation as ErrCompositeSetCycle
// specifically, not as ErrTargetOutsideDomain or a silent false negative.
// This exercises theorystate.md section 83's cross-representation cycle
// detection through an integration point that was not previously tested
// anywhere: a Domain Pointer's write-time validateMembership call
// dispatching into that same cycle-detecting resolution path
// (domainContainsGeneric -> CompositeSetRegistry.Contains -> evaluate ->
// resolveSetOperandGeneric -> CompositeSetLogRegistry.evaluate ->
// resolveSetOperandGeneric, back into the CompositeSet that started it).
//
// Introducing the cycle (via logs.AppendOperation(log1, composite, ...))
// touches only log1 and composite -- never subject, its metadata node,
// or either slot. Before theorystate.md section 86 was closed, that
// meant no Checker could fire and the cycle was only discovered by a
// later SetTarget. The shared domain Checker now reaches subject's
// pointer through a reverse lookup from the touched log/composite to the
// domain slot that references them, evaluates the domain, hits the
// cycle, and declines the commit: the append is rolled back and the
// domain stays evaluable.
func TestCrossRoleDomainPointerDetectsCycleIntroducedThroughDomainItself(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	s, err := fx.sets.NewSet(fx.graph)
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}
	x, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err2 := fx.sets.Add(fx.graph, s, x); err2 != nil {
		t.Fatalf("Add(s, x): %v", err2)
	}

	log1, err := fx.logs.NewCompositeSetLog(fx.graph)
	if err != nil {
		t.Fatalf("NewCompositeSetLog(): %v", err)
	}
	if _, _, err3 := fx.logs.AppendOperation(fx.graph, log1, s, true, true); err3 != nil {
		t.Fatalf("AppendOperation(log1, s, additive, expand): %v", err3)
	}

	composite, err := fx.composites.NewCompositeSet(fx.graph)
	if err != nil {
		t.Fatalf("NewCompositeSet(): %v", err)
	}
	if _, err4 := fx.composites.AddOperand(fx.graph, composite, log1, true, true); err4 != nil {
		t.Fatalf("AddOperand(composite, log1, additive, expand): %v", err4)
	}

	subject, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if err5 := fx.domainD.SetDomain(fx.graph, subject, composite); err5 != nil {
		t.Fatalf("SetDomain(subject, composite): %v", err5)
	}

	// Before the cycle exists, x is legitimately reachable through
	// composite -> log1 -> s, so this must succeed.
	if err6 := fx.domainD.SetTarget(fx.graph, subject, x); err6 != nil {
		t.Fatalf("SetTarget(subject, x) before cycle introduced: %v", err6)
	}

	// Attempt to introduce the cycle purely through the domain's own
	// structure: log1 would also expand composite, which itself expands
	// log1. The Checker must decline the commit with the cycle error,
	// specifically -- not ErrTargetOutsideDomain.
	_, _, cycleErr := fx.logs.AppendOperation(fx.graph, log1, composite, true, true)
	if !errors.Is(cycleErr, ErrCompositeSetCycle) {
		t.Fatalf("AppendOperation(log1, composite, additive, expand) error = %v, want %v", cycleErr, ErrCompositeSetCycle)
	}

	// The whole append was rolled back: log1 still has its one operation
	// and the composite still evaluates, without a cycle.
	operations, err := fx.logs.Operations(fx.graph, log1)
	if err != nil {
		t.Fatalf("Operations(log1) after the declined append: %v", err)
	}
	if len(operations) != 1 {
		t.Fatalf("Operations(log1) = %v, want exactly the original operation", operations)
	}

	evaluated, err := fx.composites.Evaluate(fx.graph, composite)
	if err != nil {
		t.Fatalf("Evaluate(composite) after the declined append: %v", err)
	}
	if want := []NodeID{x}; !reflect.DeepEqual(evaluated, want) {
		t.Fatalf("Evaluate(composite) = %v, want %v", evaluated, want)
	}

	// The pointer is fully usable and its domain is untouched.
	if err2 := fx.domainD.SetTarget(fx.graph, subject, x); err2 != nil {
		t.Fatalf("SetTarget(subject, x) after the declined append: %v", err2)
	}

	domain, hasDomain, err := fx.domainD.Domain(fx.graph, subject)
	if err != nil {
		t.Fatalf("Domain(subject) after the declined append: %v", err)
	}
	if !hasDomain || domain != composite {
		t.Fatalf("Domain(subject) = (%d,%v), want (%d,true)", domain, hasDomain, composite)
	}
}

// TestCrossRoleCorruptedLoggedOperandDoesNotCorruptSiblingStructures is
// the third cross-role test, and the first deliberately built around
// mid-composition corruption rather than composition alone: it appends
// two operations to a CompositeSetLog, corrupts the second operation's
// descriptor out-of-band (giving it two operand targets, violating the
// shared operand-descriptor's own "exactly one operand" shape), and
// confirms the resulting failure is scoped exactly to Evaluate/Contains
// -- the two operations that must resolve every descriptor -- and does
// not leak into or corrupt any of: the underlying List's own structural
// validity, a completely unrelated CompositeSet sharing no structure
// with the corrupted log, or the first (uncorrupted) logged operation.
//
// This is a direct exercise of theorystate.md section 7a's claim that a
// fact's meaning belongs entirely to whichever processor interprets it:
// corrupting a node in its role as an operand descriptor must not be
// visible to ListRegistry, which interprets the exact same capsule chain
// under a completely different, narrower contract (structural list
// validity only, never descriptor shape) -- ListRegistry.Elements/Head/
// Tail have no reason to know or care that a capsule's value happens to
// also be a CompositeSetLog operand descriptor.
func TestCrossRoleCorruptedLoggedOperandDoesNotCorruptSiblingStructures(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	log, err := fx.logs.NewCompositeSetLog(fx.graph)
	if err != nil {
		t.Fatalf("NewCompositeSetLog(): %v", err)
	}

	x1, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	x2, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	u1, capsule1, err := fx.logs.AppendOperation(fx.graph, log, x1, true, false)
	if err != nil {
		t.Fatalf("AppendOperation(log, x1): %v", err)
	}
	u2, capsule2, err := fx.logs.AppendOperation(fx.graph, log, x2, true, false)
	if err != nil {
		t.Fatalf("AppendOperation(log, x2): %v", err)
	}

	// Corrupt only u2, entirely out-of-band: give it a second outgoing
	// relationship, violating operandTargetGeneric/resolveOperandGeneric's
	// shared "exactly one operand target" assumption for u2 specifically.
	// u1, capsule1, capsule2, and log's own head/tail/next/prev structure
	// are all left completely untouched by this.
	extra, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err2 := fx.graph.AddRelationship(u2, extra); err2 != nil {
		t.Fatalf("AddRelationship(u2, extra) [out-of-band corruption]: %v", err2)
	}

	// Evaluate/Contains must fail specifically because of u2's now-
	// invalid shape, not because anything about the list itself is
	// wrong.
	if _, err3 := fx.logs.Evaluate(fx.graph, log); !errors.Is(err3, ErrTooManyPointerTargets) {
		t.Fatalf("Evaluate(log) after corrupting u2: error = %v, want %v", err3, ErrTooManyPointerTargets)
	}
	if _, err4 := fx.logs.Contains(fx.graph, log, x1); !errors.Is(err4, ErrTooManyPointerTargets) {
		t.Fatalf("Contains(log, x1) after corrupting u2: error = %v, want %v (backward scan reaches the corrupted u2 first)", err4, ErrTooManyPointerTargets)
	}

	// Operations() must still succeed: it is built on ListRegistry.Elements,
	// which only cares about capsule/value-slot structure, never about
	// what a value's own further relationships happen to mean under some
	// other registry's interpretation.
	ops, err := fx.logs.Operations(fx.graph, log)
	if err != nil {
		t.Fatalf("Operations(log) after corrupting u2: %v", err)
	}
	if want := []NodeID{u1, u2}; !reflect.DeepEqual(ops, want) {
		t.Fatalf("Operations(log) = %v, want %v -- corrupting u2's shape must not disturb list-level ordering", ops, want)
	}

	// The underlying List's own head/tail must likewise be completely
	// unaffected: ListRegistry never inspects a value's own outgoing
	// relationships at all.
	head, hasHead, err := fx.lists.Head(fx.graph, log)
	if err != nil {
		t.Fatalf("Head(log) after corrupting u2: %v", err)
	}
	if !hasHead || head != capsule1 {
		t.Fatalf("Head(log) = (%d,%v), want (%d,true)", head, hasHead, capsule1)
	}
	tail, hasTail, err := fx.lists.Tail(fx.graph, log)
	if err != nil {
		t.Fatalf("Tail(log) after corrupting u2: %v", err)
	}
	if !hasTail || tail != capsule2 {
		t.Fatalf("Tail(log) = (%d,%v), want (%d,true)", tail, hasTail, capsule2)
	}

	// u1's own shape is untouched, so a completely independent
	// CompositeSet built from scratch -- sharing no node with log at all
	// except reusing x1 as an ordinary scalar operand -- must evaluate
	// normally, confirming the corruption did not leak into shared
	// registry-level state (e.g. some cached axis lookup) rather than
	// staying scoped to u2 itself.
	unrelated, err := fx.composites.NewCompositeSet(fx.graph)
	if err != nil {
		t.Fatalf("NewCompositeSet(): %v", err)
	}
	if _, err5 := fx.composites.AddOperand(fx.graph, unrelated, x1, true, false); err5 != nil {
		t.Fatalf("AddOperand(unrelated, x1, additive, scalar): %v", err5)
	}
	got, err := fx.composites.Evaluate(fx.graph, unrelated)
	if err != nil {
		t.Fatalf("Evaluate(unrelated) after corrupting an unrelated log's operand: %v", err)
	}
	if want := []NodeID{x1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Evaluate(unrelated) = %v, want %v", got, want)
	}
}

// TestCrossRoleSetRegistryConflictCheckExercisedWhileSetIsDomainAndOperand
// is the fourth cross-role test: it deliberately attempts to corrupt a
// node that is simultaneously playing two other structural roles at
// once -- a Domain Pointer's domain (theorystate.md section 9c/10c) and
// a set-expansion operand of an unrelated CompositeSet (theorystate.md
// section 80/81) -- to confirm that theorystate.md section 79's
// mutual-exclusivity rule (a node may carry at most one of the three
// Set-representation tags) is still correctly enforced by
// SetRegistry.TagAsSet even while the node is load-bearing for two other
// registries at once, and that a declined TagAsSet call disturbs neither
// of those other two roles.
//
// The scenario: c is a CompositeSet (AllCompositeSets) holding one
// scalar additive operand, x. c is used, unmodified, in two independent
// roles simultaneously: as subject's domain (via DomainPointerRegistryD,
// so subject's target must belong to c's evaluated membership {x}), and
// as an additive, set-expansion operand of an entirely separate outer
// CompositeSet (so outer's own evaluated membership also resolves
// through c to {x}). Attempting sets.TagAsSet(c) at that point must be
// rejected with ErrSetRepresentationConflict (c already carries
// AllCompositeSets), must leave c untagged AllSets, and must leave both
// of c's other two roles working exactly as before: subject's domain
// pointer still validates against c, and outer's Evaluate still resolves
// through c, unaffected by the failed tagging attempt.
func TestCrossRoleSetRegistryConflictCheckExercisedWhileSetIsDomainAndOperand(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	c, err := fx.composites.NewCompositeSet(fx.graph)
	if err != nil {
		t.Fatalf("NewCompositeSet() for c: %v", err)
	}

	x, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err2 := fx.composites.AddOperand(fx.graph, c, x, true, false); err2 != nil {
		t.Fatalf("AddOperand(c, x, additive, scalar): %v", err2)
	}

	subject, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if err3 := fx.domainD.SetDomain(fx.graph, subject, c); err3 != nil {
		t.Fatalf("SetDomain(subject, c): %v", err3)
	}
	if err4 := fx.domainD.SetTarget(fx.graph, subject, x); err4 != nil {
		t.Fatalf("SetTarget(subject, x) within domain c: %v", err4)
	}

	outer, err := fx.composites.NewCompositeSet(fx.graph)
	if err != nil {
		t.Fatalf("NewCompositeSet() for outer: %v", err)
	}
	if _, err5 := fx.composites.AddOperand(fx.graph, outer, c, true, true); err5 != nil {
		t.Fatalf("AddOperand(outer, c, additive, expand): %v", err5)
	}

	evaluated, err := fx.composites.Evaluate(fx.graph, outer)
	if err != nil {
		t.Fatalf("Evaluate(outer) before corruption attempt: %v", err)
	}
	if want := []NodeID{x}; !reflect.DeepEqual(evaluated, want) {
		t.Fatalf("Evaluate(outer) before corruption attempt = %v, want %v", evaluated, want)
	}

	// The actual corruption attempt: try to also tag c as a plain Set
	// while it already carries AllCompositeSets and is simultaneously
	// load-bearing as both a Domain and a CompositeSet operand.
	err = fx.sets.TagAsSet(fx.graph, c)
	if !errors.Is(err, ErrSetRepresentationConflict) {
		t.Fatalf("TagAsSet(c) error = %v, want %v", err, ErrSetRepresentationConflict)
	}

	if fx.sets.IsSet(fx.graph, c) {
		t.Fatal("c was tagged AllSets despite already being AllCompositeSets-tagged")
	}

	// c's role as outer's set-expansion operand must be completely
	// unaffected by the declined TagAsSet call.
	evaluated, err = fx.composites.Evaluate(fx.graph, outer)
	if err != nil {
		t.Fatalf("Evaluate(outer) after declined TagAsSet: %v", err)
	}
	if want := []NodeID{x}; !reflect.DeepEqual(evaluated, want) {
		t.Fatalf("Evaluate(outer) after declined TagAsSet = %v, want %v", evaluated, want)
	}

	// c's role as subject's domain must likewise be completely
	// unaffected: subject's existing target is still valid, and a target
	// outside c's membership is still correctly rejected.
	target, hasTarget, err := fx.domainD.Target(fx.graph, subject)
	if err != nil {
		t.Fatalf("Target(subject) after declined TagAsSet: %v", err)
	}
	if !hasTarget || target != x {
		t.Fatalf("Target(subject) = (%d,%v), want (%d,true) after declined TagAsSet", target, hasTarget, x)
	}

	outside, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	err = fx.domainD.SetTarget(fx.graph, subject, outside)
	if !errors.Is(err, ErrTargetOutsideDomain) {
		t.Fatalf("SetTarget(subject, outside) after declined TagAsSet: error = %v, want %v", err, ErrTargetOutsideDomain)
	}
}

// The following tests cover theorystate.md section 86: a domain
// pointer's validity depends on its domain's membership, so the shared
// commit-time domain Checker must decline any transaction that would
// strand a pointer outside its domain, for both Representation B and D.

// newTestNode creates a fresh node in g, failing t on error.
func newTestNode(t *testing.T, g *Graph) NodeID {
	t.Helper()

	id, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	return id
}

// domainPointerHandle abstracts one domain-constrained pointer so the
// same staleness scenario can run against Representation B and D.
type domainPointerHandle struct {
	setDomain func(domain NodeID) error
	setTarget func(target NodeID) error
	target    func() (NodeID, bool, error)
}

var domainPointerKinds = []struct {
	name  string
	build func(t *testing.T, fx *domainPointerTestFixture) domainPointerHandle
}{
	{"B", func(t *testing.T, fx *domainPointerTestFixture) domainPointerHandle {
		t.Helper()

		anchor := newTestNode(t, fx.graph)
		if err := fx.domainB.NewDomainPointer(fx.graph, anchor); err != nil {
			t.Fatalf("NewDomainPointer(): %v", err)
		}

		return domainPointerHandle{
			setDomain: func(domain NodeID) error { return fx.domainB.SetDomain(fx.graph, anchor, domain) },
			setTarget: func(target NodeID) error { return fx.domainB.SetTarget(fx.graph, anchor, target) },
			target:    func() (NodeID, bool, error) { return fx.domainB.Target(fx.graph, anchor) },
		}
	}},
	{"D", func(t *testing.T, fx *domainPointerTestFixture) domainPointerHandle {
		t.Helper()

		subject := newTestNode(t, fx.graph)

		return domainPointerHandle{
			setDomain: func(domain NodeID) error { return fx.domainD.SetDomain(fx.graph, subject, domain) },
			setTarget: func(target NodeID) error { return fx.domainD.SetTarget(fx.graph, subject, target) },
			target:    func() (NodeID, bool, error) { return fx.domainD.Target(fx.graph, subject) },
		}
	}},
}

// forEachDomainPointerKind runs body once per representation, each in a
// fresh fixture.
func forEachDomainPointerKind(t *testing.T, body func(t *testing.T, fx *domainPointerTestFixture, handle domainPointerHandle)) {
	t.Helper()

	for _, kind := range domainPointerKinds {
		t.Run(kind.name, func(t *testing.T) {
			fx := newDomainPointerTestFixture(t)
			body(t, fx, kind.build(t, fx))
		})
	}
}

func requireSetContains(t *testing.T, fx *domainPointerTestFixture, set, member NodeID, want bool) {
	t.Helper()

	got, err := fx.sets.Contains(fx.graph, set, member)
	if err != nil {
		t.Fatalf("Contains(%d, %d): %v", set, member, err)
	}
	if got != want {
		t.Fatalf("Contains(%d, %d) = %v, want %v", set, member, got, want)
	}
}

func requireHandleTarget(t *testing.T, handle domainPointerHandle, want NodeID) {
	t.Helper()

	target, hasTarget, err := handle.target()
	if err != nil {
		t.Fatalf("Target(): %v", err)
	}
	if !hasTarget || target != want {
		t.Fatalf("Target() = (%d,%v), want (%d,true)", target, hasTarget, want)
	}
}

func requireCompositeOperandCount(t *testing.T, fx *domainPointerTestFixture, composite NodeID, want int) {
	t.Helper()

	operands, err := fx.composites.Operands(fx.graph, composite)
	if err != nil {
		t.Fatalf("Operands(%d): %v", composite, err)
	}
	if len(operands) != want {
		t.Fatalf("Operands(%d) = %v, want %d operands", composite, operands, want)
	}
}

func requireLogOperationCount(t *testing.T, fx *domainPointerTestFixture, log NodeID, want int) {
	t.Helper()

	operations, err := fx.logs.Operations(fx.graph, log)
	if err != nil {
		t.Fatalf("Operations(%d): %v", log, err)
	}
	if len(operations) != want {
		t.Fatalf("Operations(%d) = %v, want %d operations", log, operations, want)
	}
}

func TestDomainStalenessPlainSetRemoveOfCurrentTargetIsRejected(t *testing.T) {
	forEachDomainPointerKind(t, func(t *testing.T, fx *domainPointerTestFixture, handle domainPointerHandle) {
		domain, err := fx.sets.NewSet(fx.graph)
		if err != nil {
			t.Fatalf("NewSet(): %v", err)
		}

		target := newTestNode(t, fx.graph)
		spare := newTestNode(t, fx.graph)
		for _, member := range []NodeID{target, spare} {
			if _, addErr := fx.sets.Add(fx.graph, domain, member); addErr != nil {
				t.Fatalf("Add(domain, %d): %v", member, addErr)
			}
		}

		if setErr := handle.setDomain(domain); setErr != nil {
			t.Fatalf("setDomain(): %v", setErr)
		}
		if setErr := handle.setTarget(target); setErr != nil {
			t.Fatalf("setTarget(): %v", setErr)
		}

		if _, removeErr := fx.sets.Remove(fx.graph, domain, target); !errors.Is(removeErr, ErrTargetOutsideDomain) {
			t.Fatalf("Remove(domain, target) error = %v, want %v", removeErr, ErrTargetOutsideDomain)
		}
		requireSetContains(t, fx, domain, target, true)
		requireHandleTarget(t, handle, target)

		removed, err := fx.sets.Remove(fx.graph, domain, spare)
		if err != nil {
			t.Fatalf("Remove(domain, spare): %v", err)
		}
		if !removed {
			t.Fatal("Remove(domain, spare) reported nothing removed")
		}

		extra := newTestNode(t, fx.graph)
		if _, addErr := fx.sets.Add(fx.graph, domain, extra); addErr != nil {
			t.Fatalf("Add(domain, extra): %v", addErr)
		}
	})
}

func TestDomainStalenessCompositeDomainMutationsThatStrandTargetAreRejected(t *testing.T) {
	forEachDomainPointerKind(t, func(t *testing.T, fx *domainPointerTestFixture, handle domainPointerHandle) {
		composite, err := fx.composites.NewCompositeSet(fx.graph)
		if err != nil {
			t.Fatalf("NewCompositeSet(): %v", err)
		}

		target := newTestNode(t, fx.graph)
		spare := newTestNode(t, fx.graph)

		targetOperand, err := fx.composites.AddOperand(fx.graph, composite, target, true, false)
		if err != nil {
			t.Fatalf("AddOperand(target): %v", err)
		}
		spareOperand, err := fx.composites.AddOperand(fx.graph, composite, spare, true, false)
		if err != nil {
			t.Fatalf("AddOperand(spare): %v", err)
		}

		if setErr := handle.setDomain(composite); setErr != nil {
			t.Fatalf("setDomain(): %v", setErr)
		}
		if setErr := handle.setTarget(target); setErr != nil {
			t.Fatalf("setTarget(): %v", setErr)
		}

		if removeErr := fx.composites.RemoveOperand(fx.graph, composite, targetOperand); !errors.Is(removeErr, ErrTargetOutsideDomain) {
			t.Fatalf("RemoveOperand(target) error = %v, want %v", removeErr, ErrTargetOutsideDomain)
		}
		requireCompositeOperandCount(t, fx, composite, 2)

		if _, subtractErr := fx.composites.AddOperand(fx.graph, composite, target, false, false); !errors.Is(subtractErr, ErrTargetOutsideDomain) {
			t.Fatalf("AddOperand(target, subtractive) error = %v, want %v", subtractErr, ErrTargetOutsideDomain)
		}
		requireCompositeOperandCount(t, fx, composite, 2)
		requireHandleTarget(t, handle, target)

		if removeErr := fx.composites.RemoveOperand(fx.graph, composite, spareOperand); removeErr != nil {
			t.Fatalf("RemoveOperand(spare): %v", removeErr)
		}
		requireCompositeOperandCount(t, fx, composite, 1)
	})
}

func TestDomainStalenessLogDomainMutationsThatStrandTargetAreRejected(t *testing.T) {
	forEachDomainPointerKind(t, func(t *testing.T, fx *domainPointerTestFixture, handle domainPointerHandle) {
		log, err := fx.logs.NewCompositeSetLog(fx.graph)
		if err != nil {
			t.Fatalf("NewCompositeSetLog(): %v", err)
		}

		target := newTestNode(t, fx.graph)
		spare := newTestNode(t, fx.graph)

		_, targetCapsule, err := fx.logs.AppendOperation(fx.graph, log, target, true, false)
		if err != nil {
			t.Fatalf("AppendOperation(target): %v", err)
		}
		_, spareCapsule, err := fx.logs.AppendOperation(fx.graph, log, spare, true, false)
		if err != nil {
			t.Fatalf("AppendOperation(spare): %v", err)
		}

		if setErr := handle.setDomain(log); setErr != nil {
			t.Fatalf("setDomain(): %v", setErr)
		}
		if setErr := handle.setTarget(target); setErr != nil {
			t.Fatalf("setTarget(): %v", setErr)
		}

		if _, _, subtractErr := fx.logs.AppendOperation(fx.graph, log, target, false, false); !errors.Is(subtractErr, ErrTargetOutsideDomain) {
			t.Fatalf("AppendOperation(target, subtractive) error = %v, want %v", subtractErr, ErrTargetOutsideDomain)
		}
		requireLogOperationCount(t, fx, log, 2)

		if removeErr := fx.logs.RemoveOperation(fx.graph, log, targetCapsule); !errors.Is(removeErr, ErrTargetOutsideDomain) {
			t.Fatalf("RemoveOperation(target) error = %v, want %v", removeErr, ErrTargetOutsideDomain)
		}
		requireLogOperationCount(t, fx, log, 2)
		requireHandleTarget(t, handle, target)

		if removeErr := fx.logs.RemoveOperation(fx.graph, log, spareCapsule); removeErr != nil {
			t.Fatalf("RemoveOperation(spare): %v", removeErr)
		}
		requireLogOperationCount(t, fx, log, 1)
	})
}

// TestDomainStalenessNestedSetShrinkIsRejectedThroughDiamond covers a
// three-level chain (Set -> two composites -> log, with the Set reachable
// through both composites) whose innermost Set is mutated. The reverse
// walk must reach the log's domain slot, visit the shared Set once, and
// terminate.
func TestDomainStalenessNestedSetShrinkIsRejectedThroughDiamond(t *testing.T) {
	forEachDomainPointerKind(t, func(t *testing.T, fx *domainPointerTestFixture, handle domainPointerHandle) {
		inner, err := fx.sets.NewSet(fx.graph)
		if err != nil {
			t.Fatalf("NewSet(): %v", err)
		}

		target := newTestNode(t, fx.graph)
		spare := newTestNode(t, fx.graph)
		for _, member := range []NodeID{target, spare} {
			if _, addErr := fx.sets.Add(fx.graph, inner, member); addErr != nil {
				t.Fatalf("Add(inner, %d): %v", member, addErr)
			}
		}

		log, err := fx.logs.NewCompositeSetLog(fx.graph)
		if err != nil {
			t.Fatalf("NewCompositeSetLog(): %v", err)
		}

		for i := 0; i < 2; i++ {
			composite, compositeErr := fx.composites.NewCompositeSet(fx.graph)
			if compositeErr != nil {
				t.Fatalf("NewCompositeSet(): %v", compositeErr)
			}
			if _, operandErr := fx.composites.AddOperand(fx.graph, composite, inner, true, true); operandErr != nil {
				t.Fatalf("AddOperand(composite, inner): %v", operandErr)
			}
			if _, _, appendErr := fx.logs.AppendOperation(fx.graph, log, composite, true, true); appendErr != nil {
				t.Fatalf("AppendOperation(log, composite): %v", appendErr)
			}
		}

		if setErr := handle.setDomain(log); setErr != nil {
			t.Fatalf("setDomain(): %v", setErr)
		}
		if setErr := handle.setTarget(target); setErr != nil {
			t.Fatalf("setTarget(): %v", setErr)
		}

		if _, removeErr := fx.sets.Remove(fx.graph, inner, target); !errors.Is(removeErr, ErrTargetOutsideDomain) {
			t.Fatalf("Remove(inner, target) error = %v, want %v", removeErr, ErrTargetOutsideDomain)
		}
		requireSetContains(t, fx, inner, target, true)

		if _, removeErr := fx.sets.Remove(fx.graph, inner, spare); removeErr != nil {
			t.Fatalf("Remove(inner, spare): %v", removeErr)
		}
	})
}

// TestDomainStalenessOneStrandedPointerRejectsWholeTransactionAcrossRepresentations
// shares one domain between a B pointer and a D pointer. Removing either
// pointer's target is rejected, and a single transaction removing both is
// rejected as a whole, leaving both members in place.
func TestDomainStalenessOneStrandedPointerRejectsWholeTransactionAcrossRepresentations(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	domain, err := fx.sets.NewSet(fx.graph)
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	bTarget := newTestNode(t, fx.graph)
	dTarget := newTestNode(t, fx.graph)
	free := newTestNode(t, fx.graph)
	for _, member := range []NodeID{bTarget, dTarget, free} {
		if _, addErr := fx.sets.Add(fx.graph, domain, member); addErr != nil {
			t.Fatalf("Add(domain, %d): %v", member, addErr)
		}
	}

	anchor := newTestNode(t, fx.graph)
	if newErr := fx.domainB.NewDomainPointer(fx.graph, anchor); newErr != nil {
		t.Fatalf("NewDomainPointer(): %v", newErr)
	}
	if setErr := fx.domainB.SetDomain(fx.graph, anchor, domain); setErr != nil {
		t.Fatalf("domainB.SetDomain(): %v", setErr)
	}
	if setErr := fx.domainB.SetTarget(fx.graph, anchor, bTarget); setErr != nil {
		t.Fatalf("domainB.SetTarget(): %v", setErr)
	}

	subject := newTestNode(t, fx.graph)
	if setErr := fx.domainD.SetDomain(fx.graph, subject, domain); setErr != nil {
		t.Fatalf("domainD.SetDomain(): %v", setErr)
	}
	if setErr := fx.domainD.SetTarget(fx.graph, subject, dTarget); setErr != nil {
		t.Fatalf("domainD.SetTarget(): %v", setErr)
	}

	if _, removeErr := fx.sets.Remove(fx.graph, domain, free); removeErr != nil {
		t.Fatalf("Remove(domain, free): %v", removeErr)
	}
	if _, removeErr := fx.sets.Remove(fx.graph, domain, dTarget); !errors.Is(removeErr, ErrTargetOutsideDomain) {
		t.Fatalf("Remove(domain, dTarget) error = %v, want %v", removeErr, ErrTargetOutsideDomain)
	}
	if _, removeErr := fx.sets.Remove(fx.graph, domain, bTarget); !errors.Is(removeErr, ErrTargetOutsideDomain) {
		t.Fatalf("Remove(domain, bTarget) error = %v, want %v", removeErr, ErrTargetOutsideDomain)
	}

	err = fx.graph.Transact(func(tx Tx) error {
		if removeErr := removeRelationshipTx(tx, domain, bTarget); removeErr != nil {
			return removeErr
		}

		return removeRelationshipTx(tx, domain, dTarget)
	})
	if !errors.Is(err, ErrTargetOutsideDomain) {
		t.Fatalf("Transact(remove both) error = %v, want %v", err, ErrTargetOutsideDomain)
	}

	requireSetContains(t, fx, domain, bTarget, true)
	requireSetContains(t, fx, domain, dTarget, true)
}

// TestDomainStalenessShrinkThenRetargetInOneTransactionIsAccepted shows
// the Checker judges the final state of a transaction, not each step: the
// intermediate state (domain shrunk, pointer not yet retargeted) is
// stranded, the final state is valid.
func TestDomainStalenessShrinkThenRetargetInOneTransactionIsAccepted(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	domain, err := fx.sets.NewSet(fx.graph)
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	oldTarget := newTestNode(t, fx.graph)
	newTarget := newTestNode(t, fx.graph)
	for _, member := range []NodeID{oldTarget, newTarget} {
		if _, addErr := fx.sets.Add(fx.graph, domain, member); addErr != nil {
			t.Fatalf("Add(domain, %d): %v", member, addErr)
		}
	}

	anchor := newTestNode(t, fx.graph)
	if newErr := fx.domainB.NewDomainPointer(fx.graph, anchor); newErr != nil {
		t.Fatalf("NewDomainPointer(): %v", newErr)
	}
	if setErr := fx.domainB.SetDomain(fx.graph, anchor, domain); setErr != nil {
		t.Fatalf("SetDomain(): %v", setErr)
	}
	if setErr := fx.domainB.SetTarget(fx.graph, anchor, oldTarget); setErr != nil {
		t.Fatalf("SetTarget(oldTarget): %v", setErr)
	}

	u, found, err := fx.domainB.subPointer(fx.graph, anchor)
	if err != nil || !found {
		t.Fatalf("subPointer(): found=%v err=%v", found, err)
	}

	err = fx.graph.Transact(func(tx Tx) error {
		if removeErr := removeRelationshipTx(tx, domain, oldTarget); removeErr != nil {
			return removeErr
		}
		if swapErr := removeRelationshipTx(tx, u, oldTarget); swapErr != nil {
			return swapErr
		}

		return addRelationshipTx(tx, u, newTarget)
	})
	if err != nil {
		t.Fatalf("Transact(shrink then retarget) error = %v, want nil", err)
	}

	target, hasTarget, err := fx.domainB.Target(fx.graph, anchor)
	if err != nil {
		t.Fatalf("Target(): %v", err)
	}
	if !hasTarget || target != newTarget {
		t.Fatalf("Target() = (%d,%v), want (%d,true)", target, hasTarget, newTarget)
	}
	requireSetContains(t, fx, domain, oldTarget, false)
}

// TestDomainPointerRegistryBCheckerCatchesOutOfBandTargetChange is the
// Representation B counterpart of
// TestDomainPointerRegistryDCheckerCatchesOutOfBandTargetChange: bypass
// the wrapper and mutate the underlying sub-pointer registry directly.
func TestDomainPointerRegistryBCheckerCatchesOutOfBandTargetChange(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	anchor := newTestNode(t, fx.graph)
	if newErr := fx.domainB.NewDomainPointer(fx.graph, anchor); newErr != nil {
		t.Fatalf("NewDomainPointer(): %v", newErr)
	}

	domain, err := fx.sets.NewSet(fx.graph)
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}
	allowed := newTestNode(t, fx.graph)
	if _, addErr := fx.sets.Add(fx.graph, domain, allowed); addErr != nil {
		t.Fatalf("Add(): %v", addErr)
	}
	if setErr := fx.domainB.SetDomain(fx.graph, anchor, domain); setErr != nil {
		t.Fatalf("SetDomain(): %v", setErr)
	}

	u, found, err := fx.domainB.subPointer(fx.graph, anchor)
	if err != nil || !found {
		t.Fatalf("subPointer(): found=%v err=%v", found, err)
	}

	disallowed := newTestNode(t, fx.graph)
	err = fx.domainB.pointers.SetTarget(fx.graph, u, disallowed)
	if !errors.Is(err, ErrTargetOutsideDomain) {
		t.Fatalf("bypassing SetTarget() error = %v, want %v", err, ErrTargetOutsideDomain)
	}

	target, hasTarget, err := fx.domainB.Target(fx.graph, anchor)
	if err != nil {
		t.Fatalf("Target(): %v", err)
	}
	if hasTarget {
		t.Fatalf("Target() = (%d,true), want no target after the Checker declined the bypassing commit", target)
	}
}

// TestDomainPointerRegistryBCheckerCatchesOutOfBandDomainChange is the
// Representation B counterpart of
// TestDomainPointerRegistryDCheckerCatchesOutOfBandDomainChange.
func TestDomainPointerRegistryBCheckerCatchesOutOfBandDomainChange(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	anchor := newTestNode(t, fx.graph)
	if newErr := fx.domainB.NewDomainPointer(fx.graph, anchor); newErr != nil {
		t.Fatalf("NewDomainPointer(): %v", newErr)
	}

	firstDomain, err := fx.sets.NewSet(fx.graph)
	if err != nil {
		t.Fatalf("NewSet(first): %v", err)
	}
	x := newTestNode(t, fx.graph)
	if _, addErr := fx.sets.Add(fx.graph, firstDomain, x); addErr != nil {
		t.Fatalf("Add(): %v", addErr)
	}
	if setErr := fx.domainB.SetDomain(fx.graph, anchor, firstDomain); setErr != nil {
		t.Fatalf("SetDomain(): %v", setErr)
	}
	if setErr := fx.domainB.SetTarget(fx.graph, anchor, x); setErr != nil {
		t.Fatalf("SetTarget(): %v", setErr)
	}

	secondDomain, err := fx.sets.NewSet(fx.graph)
	if err != nil {
		t.Fatalf("NewSet(second): %v", err)
	}
	// secondDomain deliberately does not contain x.

	slot, found, err := fx.domainB.domainSlotFor(fx.graph, anchor)
	if err != nil || !found {
		t.Fatalf("domainSlotFor(): found=%v err=%v", found, err)
	}

	err = fx.domainB.domainSlots.SetTarget(fx.graph, slot, secondDomain)
	if !errors.Is(err, ErrTargetOutsideDomain) {
		t.Fatalf("bypassing SetDomain() error = %v, want %v", err, ErrTargetOutsideDomain)
	}

	domain, hasDomain, err := fx.domainB.Domain(fx.graph, anchor)
	if err != nil {
		t.Fatalf("Domain(): %v", err)
	}
	if !hasDomain || domain != firstDomain {
		t.Fatalf("Domain() = (%d,%v), want (%d,true) -- unaffected by the declined bypass", domain, hasDomain, firstDomain)
	}
}

// domainBRig holds the registries needed to exercise
// DomainPointerRegistryB over an arbitrary GraphAPI (a GraphActor, a
// RootGraph, ...), unlike domainPointerTestFixture, which is fixed to a
// bare *Graph.
type domainBRig struct {
	sets    *SetRegistry
	domainB *DomainPointerRegistryB
}

func newDomainBRig(t *testing.T, api GraphAPI) domainBRig {
	t.Helper()

	names := NewNameRegistry(api)
	ids, err := names.BootstrapNames(api, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	sets, err := NewSetRegistry(api, ids[NameAllSets], ids[NameAllCompositeSets], ids[NameAllCompositeSetLogs])
	if err != nil {
		t.Fatalf("NewSetRegistry(): %v", err)
	}

	composites, err := NewCompositeSetRegistry(
		api,
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

	subPointers, err := NewPointerRegistry(api, ids[NameAllSubPointers])
	if err != nil {
		t.Fatalf("NewPointerRegistry(AllSubPointers): %v", err)
	}

	domainSlots, err := NewPointerRegistry(api, ids[NameAllDomainSlot])
	if err != nil {
		t.Fatalf("NewPointerRegistry(AllDomainSlot): %v", err)
	}

	return domainBRig{
		sets:    sets,
		domainB: NewDomainPointerRegistryB(api, subPointers, domainSlots, sets, composites, nil),
	}
}

// TestDomainPointerRegistryBCheckerToleratesUniversalRootParent runs two
// domain-constrained B pointers under a RootGraph, where ROOT is a virtual
// parent of every slot. Without addSlotOwners' universal-parent skip, ROOT
// would be treated as an anchor and its forward lookups would see both
// slots and report ambiguity, rejecting unrelated transactions. Staleness
// must still be detected.
func TestDomainPointerRegistryBCheckerToleratesUniversalRootParent(t *testing.T) {
	var g Graph

	root := newTestNode(t, &g)
	rootGraph, err := NewRootGraph(&g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	rig := newDomainBRig(t, rootGraph)

	type pointerCase struct {
		domain, target, spare NodeID
	}

	cases := make([]pointerCase, 2)
	for i := range cases {
		anchor, createErr := rootGraph.CreateNode()
		if createErr != nil {
			t.Fatalf("CreateNode(anchor): %v", createErr)
		}
		if newErr := rig.domainB.NewDomainPointer(rootGraph, anchor); newErr != nil {
			t.Fatalf("NewDomainPointer(): %v", newErr)
		}

		domain, setErr := rig.sets.NewSet(rootGraph)
		if setErr != nil {
			t.Fatalf("NewSet(): %v", setErr)
		}

		target, targetErr := rootGraph.CreateNode()
		if targetErr != nil {
			t.Fatalf("CreateNode(target): %v", targetErr)
		}
		spare, spareErr := rootGraph.CreateNode()
		if spareErr != nil {
			t.Fatalf("CreateNode(spare): %v", spareErr)
		}

		for _, member := range []NodeID{target, spare} {
			if _, addErr := rig.sets.Add(rootGraph, domain, member); addErr != nil {
				t.Fatalf("Add(domain, %d): %v", member, addErr)
			}
		}
		if domainErr := rig.domainB.SetDomain(rootGraph, anchor, domain); domainErr != nil {
			t.Fatalf("SetDomain(): %v", domainErr)
		}
		if pointErr := rig.domainB.SetTarget(rootGraph, anchor, target); pointErr != nil {
			t.Fatalf("SetTarget(): %v", pointErr)
		}

		cases[i] = pointerCase{domain: domain, target: target, spare: spare}
	}

	for _, c := range cases {
		if _, removeErr := rig.sets.Remove(rootGraph, c.domain, c.spare); removeErr != nil {
			t.Fatalf("Remove(domain, spare) error = %v, want nil", removeErr)
		}
		if _, removeErr := rig.sets.Remove(rootGraph, c.domain, c.target); !errors.Is(removeErr, ErrTargetOutsideDomain) {
			t.Fatalf("Remove(domain, target) error = %v, want %v", removeErr, ErrTargetOutsideDomain)
		}
	}
}

// TestDomainPointerRegistryBSetTargetRacingDomainShrinkNeverStrandsPointerUnderGraphActor
// races SetTarget against removal of the same member from the domain,
// entirely through one GraphActor. Each is read-then-commit across
// separate round trips, so they can interleave; the property under test
// is that the pointer never ends up targeting a non-member -- the losing
// side is declined with ErrTargetOutsideDomain (write-time or by the
// Checker), never committed stale.
func TestDomainPointerRegistryBSetTargetRacingDomainShrinkNeverStrandsPointerUnderGraphActor(t *testing.T) {
	actor := NewGraphActor(&Graph{})
	defer actor.Close()

	rig := newDomainBRig(t, actor)

	anchor, err := actor.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(anchor): %v", err)
	}
	if newErr := rig.domainB.NewDomainPointer(actor, anchor); newErr != nil {
		t.Fatalf("NewDomainPointer(): %v", newErr)
	}

	domain, err := rig.sets.NewSet(actor)
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}
	if setErr := rig.domainB.SetDomain(actor, anchor, domain); setErr != nil {
		t.Fatalf("SetDomain(): %v", setErr)
	}

	const rounds = 100

	for round := 0; round < rounds; round++ {
		candidate, createErr := actor.CreateNode()
		if createErr != nil {
			t.Fatalf("round %d: CreateNode(): %v", round, createErr)
		}
		if _, addErr := rig.sets.Add(actor, domain, candidate); addErr != nil {
			t.Fatalf("round %d: Add(): %v", round, addErr)
		}

		var wg sync.WaitGroup
		var setErr, removeErr error

		wg.Add(2)
		go func() {
			defer wg.Done()
			setErr = rig.domainB.SetTarget(actor, anchor, candidate)
		}()
		go func() {
			defer wg.Done()
			_, removeErr = rig.sets.Remove(actor, domain, candidate)
		}()
		wg.Wait()

		for _, opErr := range []error{setErr, removeErr} {
			if opErr != nil && !errors.Is(opErr, ErrTargetOutsideDomain) {
				t.Fatalf("round %d: error = %v, want nil or %v", round, opErr, ErrTargetOutsideDomain)
			}
		}

		target, hasTarget, targetErr := rig.domainB.Target(actor, anchor)
		if targetErr != nil {
			t.Fatalf("round %d: Target(): %v", round, targetErr)
		}
		if !hasTarget {
			continue
		}

		member, memberErr := rig.sets.Contains(actor, domain, target)
		if memberErr != nil {
			t.Fatalf("round %d: Contains(): %v", round, memberErr)
		}
		if !member {
			t.Fatalf("round %d: pointer targets %d, which is no longer in its domain", round, target)
		}
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
	if g.HasRelationship(set, member) {
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
	if !g.HasRelationship(set, member) {
		t.Fatal("member is missing after a vetoed Remove()")
	}
}

// The following tests exercise GraphActor (theorystate.md section 89c):
// a CSP/actor-style wrapper making it safe for multiple goroutines to
// share one underlying *Graph, none of them ever touching it directly.

func TestGraphActorBasicOperations(t *testing.T) {
	actor := NewGraphActor(&Graph{})
	defer actor.Close()

	a, err := actor.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}

	b, err := actor.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for b: %v", err)
	}

	if !actor.NodeExists(a) || !actor.NodeExists(b) {
		t.Fatal("created nodes do not both exist")
	}

	created, err := actor.AddRelationship(a, b)
	if err != nil {
		t.Fatalf("AddRelationship(a,b): %v", err)
	}
	if !created {
		t.Fatal("AddRelationship(a,b) reported that nothing was created")
	}

	if !actor.HasRelationship(a, b) {
		t.Fatal("HasRelationship(a,b) = false, want true")
	}

	relationship, exists, err := actor.FindRelationship(a, b)
	if err != nil {
		t.Fatalf("FindRelationship(a,b): %v", err)
	}
	if !exists {
		t.Fatal("FindRelationship(a,b) reported the relationship does not exist")
	}
	want := Relationship{From: a, To: b}
	if !reflect.DeepEqual(relationship, want) {
		t.Fatalf("FindRelationship(a,b) = %v, want %v", relationship, want)
	}

	outgoing, err := actor.FindOutgoing(a)
	if err != nil {
		t.Fatalf("FindOutgoing(a): %v", err)
	}
	if !reflect.DeepEqual(outgoing, []Relationship{want}) {
		t.Fatalf("FindOutgoing(a) = %v, want %v", outgoing, []Relationship{want})
	}

	incoming, err := actor.FindIncoming(b)
	if err != nil {
		t.Fatalf("FindIncoming(b): %v", err)
	}
	if !reflect.DeepEqual(incoming, []Relationship{want}) {
		t.Fatalf("FindIncoming(b) = %v, want %v", incoming, []Relationship{want})
	}

	all := actor.FindRelationships()
	if !reflect.DeepEqual(all, []Relationship{want}) {
		t.Fatalf("FindRelationships() = %v, want %v", all, []Relationship{want})
	}

	removed, err := actor.RemoveRelationship(a, b)
	if err != nil {
		t.Fatalf("RemoveRelationship(a,b): %v", err)
	}
	if !removed {
		t.Fatal("RemoveRelationship(a,b) reported that nothing was removed")
	}

	if actor.HasRelationship(a, b) {
		t.Fatal("relationship still exists after removal")
	}

	if err := actor.DeleteNode(a); err != nil {
		t.Fatalf("DeleteNode(a): %v", err)
	}
	if actor.NodeExists(a) {
		t.Fatal("node a still exists after DeleteNode()")
	}
}

func TestGraphActorFindNodes(t *testing.T) {
	actor := NewGraphActor(&Graph{})
	defer actor.Close()

	a, err := actor.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}

	b, err := actor.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for b: %v", err)
	}

	if err2 := actor.DeleteNode(a); err2 != nil {
		t.Fatalf("DeleteNode(a): %v", err2)
	}

	got := actor.FindNodes()
	want := []NodeID{b}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FindNodes() = %v, want %v", got, want)
	}
}

func TestGraphActorTransactRollsBackOnFailure(t *testing.T) {
	actor := NewGraphActor(&Graph{})
	defer actor.Close()

	const nonexistent NodeID = 999999

	var id NodeID
	err := actor.Transact(func(tx Tx) error {
		var err error
		id, err = createNodeTx(tx)
		if err != nil {
			return err
		}

		err = addRelationshipTx(tx, id, nonexistent)
		return err
	})

	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Transact() error = %v, want %v", err, ErrNodeNotFound)
	}

	if actor.NodeExists(id) {
		t.Fatalf("node %d still exists after its creating transaction rolled back", id)
	}
}

// TestGraphActorTransactPanicPropagatesAndActorSurvives covers the
// panic-handling behavior documented on GraphActor.do: a panic inside a
// Transact closure must still propagate to its original caller, exactly
// as a direct, non-actor Graph.Transact call already does, while
// leaving the actor's own dedicated goroutine alive to service every
// later, unrelated call.
func TestGraphActorTransactPanicPropagatesAndActorSurvives(t *testing.T) {
	actor := NewGraphActor(&Graph{})
	defer actor.Close()

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic to propagate out of GraphActor.Transact()")
			}
		}()

		//nolint:errcheck // because there's no error, it panics
		_ = actor.Transact(func(_ Tx) error {
			panic("boom")
		})
	}()

	id, err := actor.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() after a panicking Transact(): %v", err)
	}
	if !actor.NodeExists(id) {
		t.Fatalf("node %d does not exist after a panicking Transact()", id)
	}
}

// TestGraphActorReentrancyTripwireFiresAndActorSurvives exercises the
// debug-only reentrancy tripwire (theorystate.md section 90) directly: a
// Transact closure calls back into the very same GraphActor it is
// already running on, from the actor's own dedicated goroutine -- the
// exact shape of the original NameRegistry/GraphActor deadlock this
// tripwire exists to turn into a loud, immediate panic instead of a
// silent, permanent hang. Mirrors
// TestGraphActorTransactPanicPropagatesAndActorSurvives's structure: the
// panic must reach the original caller, and the actor must remain fully
// usable immediately afterward.
func TestGraphActorReentrancyTripwireFiresAndActorSurvives(t *testing.T) {
	actor := NewGraphActor(&Graph{})
	defer actor.Close()

	func() {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("expected the reentrancy tripwire to panic")
			}
			message, ok := r.(string)
			if !ok || !strings.Contains(message, "reentrancy") {
				t.Fatalf("panic value = %v, want a string mentioning reentrancy", r)
			}
		}()

		//nolint:errcheck // the tripwire panics before Transact can return
		_ = actor.Transact(func(_ Tx) error {
			// Reentrant: this closure is already running on actor's one
			// dedicated goroutine, so calling back into the actor here
			// is exactly the deadlock class the tripwire detects.
			actor.RegisterChecker(Checker{})
			return nil
		})
	}()

	id, err := actor.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() after the tripwire panicked: %v", err)
	}
	if !actor.NodeExists(id) {
		t.Fatalf("node %d does not exist after the tripwire panicked", id)
	}
}

func TestGraphActorCloseIsIdempotent(_ *testing.T) {
	actor := NewGraphActor(&Graph{})

	actor.Close()
	actor.Close()
}

// TestGraphActorConcurrentCreateNodeProducesUniqueIDs exercises
// GraphActor under real concurrent load: many goroutines each mint a
// fresh node and link it to a shared hub node, entirely through the
// actor, with no direct access to the underlying *Graph at all.
// Graph.CreateNode's own nextID counter increment is not atomic on its
// own -- this is exactly what go test -race, or a duplicate/missing
// NodeID, would be expected to catch if GraphActor's serialization
// guarantee did not actually hold.
func TestGraphActorConcurrentCreateNodeProducesUniqueIDs(t *testing.T) {
	actor := NewGraphActor(&Graph{})
	defer actor.Close()

	hub, err := actor.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for hub: %v", err)
	}

	const goroutines = 50

	var wg sync.WaitGroup
	ids := make([]NodeID, goroutines)
	errs := make([]error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			id, createErr := actor.CreateNode()
			if createErr != nil {
				errs[i] = createErr
				return
			}

			if _, addErr := actor.AddRelationship(hub, id); addErr != nil {
				errs[i] = addErr
				return
			}

			ids[i] = id
		}()
	}

	wg.Wait()

	seen := make(map[NodeID]struct{}, goroutines)
	for i, id := range ids {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("CreateNode() returned duplicate NodeID %d", id)
		}
		seen[id] = struct{}{}
	}

	outgoing, err := actor.FindOutgoing(hub)
	if err != nil {
		t.Fatalf("FindOutgoing(hub): %v", err)
	}
	if len(outgoing) != goroutines {
		t.Fatalf("FindOutgoing(hub) has %d relationships, want %d", len(outgoing), goroutines)
	}
}

// TestGraphActorConcurrentSetTargetNeverProducesTooManyTargets exercises
// the write-skew hazard theorystate.md section 89c names directly: many
// goroutines each running a caller-composed read-then-write sequence on
// the very same pointer (the shape PointerRegistry.SetTarget itself had
// before implementation_state.md item 30 made it atomic; see
// staleReadThenSetTarget), entirely through one GraphActor, with no
// direct *Graph access from any of them. Each sequence's read (the
// current target) and its later, separate commit are two round trips,
// so two goroutines can legitimately interleave between them.
//
// This test does not assert which goroutine "wins": that depends on
// scheduling. It asserts the actual safety property instead: every
// returned error, if any, is specifically ErrTooManyPointerTargets --
// PointerRegistry's own existing Checker (registered once, at
// construction, exactly as it already is for single-threaded use)
// catching and rolling back a losing goroutine's stale commit, with no
// GraphActor-specific machinery required -- and exactly one target
// survives at the end regardless of how the goroutines happened to
// interleave.
func TestGraphActorConcurrentSetTargetNeverProducesTooManyTargets(t *testing.T) {
	actor := NewGraphActor(&Graph{})
	defer actor.Close()

	names := NewNameRegistry(actor)
	ids, err := names.BootstrapNames(actor, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	pointers, err := NewPointerRegistry(actor, ids[NameAllPointers])
	if err != nil {
		t.Fatalf("NewPointerRegistry(): %v", err)
	}

	p, err := pointers.NewPointer(actor)
	if err != nil {
		t.Fatalf("NewPointer(): %v", err)
	}

	const goroutines = 50

	candidates := make([]NodeID, goroutines)
	for i := range candidates {
		candidate, createErr := actor.CreateNode()
		if createErr != nil {
			t.Fatalf("CreateNode() for candidate %d: %v", i, createErr)
		}
		candidates[i] = candidate
	}

	var wg sync.WaitGroup
	errs := make([]error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = staleReadThenSetTarget(actor, pointers, p, candidates[i])
		}()
	}

	wg.Wait()

	for i, setErr := range errs {
		if setErr != nil && !errors.Is(setErr, ErrTooManyPointerTargets) {
			t.Fatalf("goroutine %d: SetTarget() error = %v, want nil or %v", i, setErr, ErrTooManyPointerTargets)
		}
	}

	outgoing, err := actor.FindOutgoing(p)
	if err != nil {
		t.Fatalf("FindOutgoing(p): %v", err)
	}
	if len(outgoing) != 1 {
		t.Fatalf("FindOutgoing(p) = %v, want exactly one surviving target regardless of interleaving", outgoing)
	}

	target, hasTarget, err := pointers.Target(actor, p)
	if err != nil {
		t.Fatalf("Target(p): %v", err)
	}
	if !hasTarget {
		t.Fatal("Target(p) reports no target after concurrent SetTarget calls")
	}

	found := false
	for _, candidate := range candidates {
		if candidate == target {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Target(p) = %d, want one of the attempted candidates %v", target, candidates)
	}
}

// staleReadThenSetTarget reproduces the shape PointerRegistry.SetTarget
// had before implementation_state.md item 30: read the pointer's current
// target through one round trip, then commit the replacement through a
// second, separate one. It exists to keep exercising the hazard a
// caller-composed sequence still has under GraphActor (and the Checker
// that catches it), now that SetTarget itself is atomic.
func staleReadThenSetTarget(api GraphAPI, pointers *PointerRegistry, p, target NodeID) error {
	current, hasTarget, err := pointers.Target(api, p)
	if err != nil {
		return err
	}

	return wrapInterfaceErr(api.Transact(func(tx Tx) error {
		return setPointerTargetTx(tx, p, current, hasTarget, target)
	}))
}

// newActorPointerFixture builds a GraphActor with a PointerRegistry over
// AllPointers and one fresh Pointer node. The actor is closed when the
// test ends.
func newActorPointerFixture(t *testing.T) (*GraphActor, *PointerRegistry, NodeID) {
	t.Helper()

	actor := NewGraphActor(&Graph{})
	t.Cleanup(actor.Close)

	names := NewNameRegistry(actor)
	ids, err := names.BootstrapNames(actor, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	pointers, err := NewPointerRegistry(actor, ids[NameAllPointers])
	if err != nil {
		t.Fatalf("NewPointerRegistry(): %v", err)
	}

	p, err := pointers.NewPointer(actor)
	if err != nil {
		t.Fatalf("NewPointer(): %v", err)
	}

	return actor, pointers, p
}

// TestGraphActorConcurrentSetTargetIsAtomic is the counterpart of
// TestGraphActorConcurrentSetTargetNeverProducesTooManyTargets: with
// SetTarget's reads inside its Transact, concurrent calls can no longer
// produce a stale commit, so every call must succeed (the last commit
// wins) and exactly one target must survive.
func TestGraphActorConcurrentSetTargetIsAtomic(t *testing.T) {
	actor, pointers, p := newActorPointerFixture(t)

	const goroutines = 50

	candidates := make([]NodeID, goroutines)
	for i := range candidates {
		candidate, createErr := actor.CreateNode()
		if createErr != nil {
			t.Fatalf("CreateNode() for candidate %d: %v", i, createErr)
		}
		candidates[i] = candidate
	}

	var wg sync.WaitGroup
	errs := make([]error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = pointers.SetTarget(actor, p, candidates[i])
		}()
	}

	wg.Wait()

	for i, setErr := range errs {
		if setErr != nil {
			t.Fatalf("goroutine %d: SetTarget() error = %v, want nil (SetTarget is atomic)", i, setErr)
		}
	}

	outgoing, err := actor.FindOutgoing(p)
	if err != nil {
		t.Fatalf("FindOutgoing(p): %v", err)
	}
	if len(outgoing) != 1 {
		t.Fatalf("FindOutgoing(p) = %v, want exactly one surviving target", outgoing)
	}

	survivor := outgoing[0].To
	found := false
	for _, candidate := range candidates {
		if candidate == survivor {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("surviving target %d is not one of the attempted candidates %v", survivor, candidates)
	}
}

// TestGraphActorConcurrentEnsureMetadataCreatesExactlyOneMetadataNode
// covers the check-then-create bug in ensureMetadata: before the lookup
// moved inside the Transact, concurrent callers could each conclude "no
// metadata yet" and each create one, after which every lookup failed
// with ErrAmbiguousPointerMetadata.
func TestGraphActorConcurrentEnsureMetadataCreatesExactlyOneMetadataNode(t *testing.T) {
	actor := NewGraphActor(&Graph{})
	defer actor.Close()

	names := NewNameRegistry(actor)
	ids, err := names.BootstrapNames(actor, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	metadata, err := NewPointerMetadataRegistryD(actor, ids[NameAllPointerMetadata], ids[NameAllPointerMetadataSubjectSlot], ids[NameAllPointerMetadataTargetSlot])
	if err != nil {
		t.Fatalf("NewPointerMetadataRegistryD(): %v", err)
	}

	subject, err := actor.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	const goroutines = 50

	var wg sync.WaitGroup
	got := make([]NodeID, goroutines)
	errs := make([]error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i], errs[i] = metadata.EnsureMetadata(actor, subject)
		}()
	}

	wg.Wait()

	for i := range got {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: EnsureMetadata() error = %v", i, errs[i])
		}
		if got[i] != got[0] {
			t.Fatalf("goroutine %d got metadata node %d, goroutine 0 got %d; want one shared node", i, got[i], got[0])
		}
	}

	has, hasErr := metadata.HasMetadata(actor, subject)
	if hasErr != nil {
		t.Fatalf("HasMetadata(): %v (a second metadata node makes lookups ambiguous)", hasErr)
	}
	if !has {
		t.Fatal("HasMetadata() = false after EnsureMetadata()")
	}
}

// TestGraphActorConcurrentNewDomainPointerCreatesExactlyOneSubPointer
// covers the same check-then-create bug in NewDomainPointer.
func TestGraphActorConcurrentNewDomainPointerCreatesExactlyOneSubPointer(t *testing.T) {
	actor := NewGraphActor(&Graph{})
	defer actor.Close()

	rig := newDomainBRig(t, actor)

	anchor, err := actor.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	const goroutines = 50

	var wg sync.WaitGroup
	errs := make([]error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = rig.domainB.NewDomainPointer(actor, anchor)
		}()
	}

	wg.Wait()

	for i, newErr := range errs {
		if newErr != nil {
			t.Fatalf("goroutine %d: NewDomainPointer() error = %v", i, newErr)
		}
	}

	outgoing, err := actor.FindOutgoing(anchor)
	if err != nil {
		t.Fatalf("FindOutgoing(anchor): %v", err)
	}
	if len(outgoing) != 1 {
		t.Fatalf("FindOutgoing(anchor) = %v, want exactly one sub-pointer", outgoing)
	}

	if _, _, targetErr := rig.domainB.Target(actor, anchor); targetErr != nil {
		t.Fatalf("Target(): %v", targetErr)
	}
}

// TestGraphActorConcurrentCreateNamedNodeSameNameBindsExactlyOnce checks
// that the lookup, node creation and binding are one atomic step: of many
// goroutines creating the same name, exactly one wins, the rest get
// ErrNameAlreadyBound, and only one node is left in the graph.
func TestGraphActorConcurrentCreateNamedNodeSameNameBindsExactlyOnce(t *testing.T) {
	actor := NewGraphActor(&Graph{})
	defer actor.Close()

	names := NewNameRegistry(actor)

	const goroutines = 50

	var wg sync.WaitGroup
	ids := make([]NodeID, goroutines)
	errs := make([]error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ids[i], errs[i] = names.CreateNamedNode(actor, "contended")
		}()
	}

	wg.Wait()

	winners := 0
	winner := NodeID(0)

	for i, createErr := range errs {
		switch {
		case createErr == nil:
			winners++
			winner = ids[i]
		case !errors.Is(createErr, ErrNameAlreadyBound):
			t.Fatalf("goroutine %d: CreateNamedNode() error = %v, want nil or %v", i, createErr, ErrNameAlreadyBound)
		}
	}

	if winners != 1 {
		t.Fatalf("%d goroutines created the name, want exactly 1", winners)
	}

	bound, ok := names.Lookup("contended")
	if !ok || bound != winner {
		t.Fatalf("Lookup(\"contended\") = (%d,%v), want (%d,true)", bound, ok, winner)
	}

	if nodes := actor.FindNodes(); len(nodes) != 1 {
		t.Fatalf("FindNodes() = %v, want exactly the winning node (losers must roll back)", nodes)
	}
}

func TestTxOnCommitRunsOnlyAfterSuccessfulCommit(t *testing.T) {
	var g Graph

	var order []string

	err := g.Transact(func(tx Tx) error {
		tx.OnCommit(func() { order = append(order, "first") })
		tx.OnCommit(func() { order = append(order, "second") })
		return nil
	})
	if err != nil {
		t.Fatalf("Transact() error = %v", err)
	}
	if want := []string{"first", "second"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("hooks ran %v, want %v (registration order)", order, want)
	}

	errBoom := errors.New("boom")

	err = g.Transact(func(tx Tx) error {
		tx.OnCommit(func() { order = append(order, "after-error") })
		return errBoom
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("Transact() error = %v, want %v", err, errBoom)
	}

	tag := newTestNode(t, &g)
	node := newTestNode(t, &g)
	errVeto := errors.New("veto")

	g.RegisterChecker(Checker{
		Name: "veto",
		Tags: []NodeID{tag},
		Check: func(_ GraphReader, _ map[NodeID]struct{}) error {
			return errVeto
		},
	})

	err = g.Transact(func(tx Tx) error {
		tx.OnCommit(func() { order = append(order, "after-veto") })
		return addRelationshipTx(tx, tag, node)
	})
	if !errors.Is(err, errVeto) {
		t.Fatalf("Transact() error = %v, want %v", err, errVeto)
	}

	if want := []string{"first", "second"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("hooks ran %v, want %v (hooks of a failed or vetoed transaction must not run)", order, want)
	}
}

func TestNestedTransactCommitsWithOuter(t *testing.T) {
	var g Graph

	var a, b NodeID

	err := g.Transact(func(tx Tx) error {
		var txErr error
		a, txErr = createNodeTx(tx)
		if txErr != nil {
			return txErr
		}

		return wrapInterfaceErr(tx.Transact(func(inner Tx) error {
			var innerErr error
			b, innerErr = createNodeTx(inner)
			if innerErr != nil {
				return innerErr
			}

			return addRelationshipTx(inner, a, b)
		}))
	})
	if err != nil {
		t.Fatalf("Transact() error = %v", err)
	}

	if !g.NodeExists(a) || !g.NodeExists(b) || !g.HasRelationship(a, b) {
		t.Fatalf("nodes %d, %d and relationship (%d,%d) must all exist after the outermost commit", a, b, a, b)
	}
}

func TestNestedTransactFailureRollsBackOnlyTheInnerSteps(t *testing.T) {
	var g Graph

	errInner := errors.New("inner failure")

	var kept, dropped NodeID
	var hooks []string

	err := g.Transact(func(tx Tx) error {
		var txErr error
		kept, txErr = createNodeTx(tx)
		if txErr != nil {
			return txErr
		}

		tx.OnCommit(func() { hooks = append(hooks, "outer") })

		innerErr := tx.Transact(func(inner Tx) error {
			var createErr error
			dropped, createErr = createNodeTx(inner)
			if createErr != nil {
				return createErr
			}

			if linkErr := addRelationshipTx(inner, kept, dropped); linkErr != nil {
				return linkErr
			}

			inner.OnCommit(func() { hooks = append(hooks, "inner") })

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

	if !g.NodeExists(kept) {
		t.Fatal("the outer step was lost although only the nested transaction failed")
	}
	if g.NodeExists(dropped) {
		t.Fatal("the nested step survived its own failed transaction")
	}
	if want := []string{"outer"}; !reflect.DeepEqual(hooks, want) {
		t.Fatalf("commit hooks ran %v, want %v (the failed nested transaction's hook must be discarded)", hooks, want)
	}
}

func TestNestedTransactSuccessIsRolledBackWithOuterFailure(t *testing.T) {
	var g Graph

	errOuter := errors.New("outer failure")

	var id NodeID

	err := g.Transact(func(tx Tx) error {
		if nestedErr := tx.Transact(func(inner Tx) error {
			var createErr error
			id, createErr = createNodeTx(inner)
			return createErr
		}); nestedErr != nil {
			return wrapInterfaceErr(nestedErr)
		}

		return errOuter
	})
	if !errors.Is(err, errOuter) {
		t.Fatalf("Transact() error = %v, want %v", err, errOuter)
	}

	if g.NodeExists(id) {
		t.Fatalf("node %d created by a successful nested transaction survived the outer failure", id)
	}
}

func TestNestedTransactRunsCheckersOnlyAtOutermostCommit(t *testing.T) {
	var g Graph

	flag := newTestNode(t, &g)
	x := newTestNode(t, &g)
	okNode := newTestNode(t, &g)

	errUnsatisfied := errors.New("flagged node has no ok edge")
	runs := 0

	g.RegisterChecker(Checker{
		Name: "flagged-needs-ok",
		Tags: []NodeID{flag},
		Check: func(view GraphReader, touched map[NodeID]struct{}) error {
			runs++

			for node := range touched {
				if view.HasRelationship(flag, node) && !view.HasRelationship(node, okNode) {
					return errUnsatisfied
				}
			}

			return nil
		},
	})

	// The nested step leaves the invariant violated and the outer step
	// repairs it before commit: the Checker must judge only the final
	// state, once.
	err := g.Transact(func(tx Tx) error {
		if innerErr := tx.Transact(func(inner Tx) error {
			return addRelationshipTx(inner, flag, x)
		}); innerErr != nil {
			return wrapInterfaceErr(innerErr)
		}

		return addRelationshipTx(tx, x, okNode)
	})
	if err != nil {
		t.Fatalf("Transact(repaired before commit) error = %v, want nil", err)
	}
	if runs != 1 {
		t.Fatalf("the Checker ran %d times, want exactly 1 (at the outermost commit)", runs)
	}

	// Nothing repairs this one: the nested call itself succeeds
	// (provisionally), and the outermost commit declines everything.
	y := newTestNode(t, &g)

	err = g.Transact(func(tx Tx) error {
		if innerErr := tx.Transact(func(inner Tx) error {
			return addRelationshipTx(inner, flag, y)
		}); innerErr != nil {
			return wrapInterfaceErr(innerErr)
		}

		return nil
	})
	if !errors.Is(err, errUnsatisfied) {
		t.Fatalf("Transact(unrepaired) error = %v, want %v", err, errUnsatisfied)
	}
	if g.HasRelationship(flag, y) {
		t.Fatal("a nested step that succeeded provisionally survived the declined outermost commit")
	}
}

func TestNestedTransactPanicRollsBackToSavepointAndPropagates(t *testing.T) {
	var g Graph

	var kept, dropped NodeID

	err := g.Transact(func(tx Tx) error {
		var txErr error
		kept, txErr = createNodeTx(tx)
		if txErr != nil {
			return txErr
		}

		func() {
			defer func() {
				if r := recover(); r == nil {
					t.Error("expected the nested panic to reach the enclosing closure")
				}
			}()

			//nolint:errcheck // the nested closure panics before Transact can return
			_ = tx.Transact(func(inner Tx) error {
				var createErr error
				dropped, createErr = createNodeTx(inner)
				if createErr != nil {
					return createErr
				}

				panic("boom")
			})
		}()

		return nil
	})
	if err != nil {
		t.Fatalf("Transact() error = %v", err)
	}

	if !g.NodeExists(kept) {
		t.Fatal("the outer step was lost although the enclosing closure recovered the nested panic")
	}
	if g.NodeExists(dropped) {
		t.Fatal("the panicking nested transaction's step survived")
	}
}

func TestRootGraphNestedTransactPresentsOverlayAndForwardsOnCommit(t *testing.T) {
	var g Graph

	root := newTestNode(t, &g)
	x := newTestNode(t, &g)

	rootGraph, err := NewRootGraph(&g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	var sawVirtual, ran bool
	var deleteRootErr error

	err = rootGraph.Transact(func(tx Tx) error {
		return wrapInterfaceErr(tx.Transact(func(inner Tx) error {
			sawVirtual = inner.HasRelationship(root, x)
			deleteRootErr = inner.DeleteNode(root)
			inner.OnCommit(func() { ran = true })

			return nil
		}))
	})
	if err != nil {
		t.Fatalf("Transact() error = %v", err)
	}

	if !sawVirtual {
		t.Fatal("the nested transaction did not present the virtual (ROOT, x) relationship")
	}
	if !errors.Is(deleteRootErr, ErrCannotDeleteRoot) {
		t.Fatalf("nested tx.DeleteNode(ROOT) error = %v, want %v", deleteRootErr, ErrCannotDeleteRoot)
	}
	if !ran {
		t.Fatal("a commit hook registered inside the nested transaction did not run")
	}
}

func TestGraphActorNestedTransactRollsBackOnlyInnerSteps(t *testing.T) {
	actor := NewGraphActor(&Graph{})
	defer actor.Close()

	errInner := errors.New("inner failure")

	var kept, dropped NodeID

	err := actor.Transact(func(tx Tx) error {
		var txErr error
		kept, txErr = createNodeTx(tx)
		if txErr != nil {
			return txErr
		}

		if innerErr := tx.Transact(func(inner Tx) error {
			var createErr error
			dropped, createErr = createNodeTx(inner)
			if createErr != nil {
				return createErr
			}

			return errInner
		}); !errors.Is(innerErr, errInner) {
			t.Errorf("nested Transact() error = %v, want %v", innerErr, errInner)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("Transact() error = %v", err)
	}

	if !actor.NodeExists(kept) {
		t.Fatal("the outer step was lost although only the nested transaction failed")
	}
	if actor.NodeExists(dropped) {
		t.Fatal("the nested step survived its own failed transaction")
	}
}

func TestRootGraphTransactForwardsOnCommit(t *testing.T) {
	var g Graph

	root := newTestNode(t, &g)

	rootGraph, err := NewRootGraph(&g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	ran := false

	err = rootGraph.Transact(func(tx Tx) error {
		tx.OnCommit(func() { ran = true })
		return nil
	})
	if err != nil {
		t.Fatalf("Transact() error = %v", err)
	}
	if !ran {
		t.Fatal("commit hook registered through the ROOT overlay did not run")
	}
}

func TestNameRegistryBindTxIsDiscardedOnRollbackAndAppliedOnCommit(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	errRollback := errors.New("rollback")

	var rolledBack NodeID

	err := g.Transact(func(tx Tx) error {
		var txErr error
		rolledBack, txErr = createNodeTx(tx)
		if txErr != nil {
			return txErr
		}
		if bindErr := names.Bind(tx, "A", rolledBack); bindErr != nil {
			return bindErr
		}

		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("Transact() error = %v, want %v", err, errRollback)
	}

	if _, ok := names.Lookup("A"); ok {
		t.Fatal("name \"A\" is bound after its transaction rolled back")
	}
	if _, ok := names.NameForNode(rolledBack); ok {
		t.Fatalf("node %d has a name after its transaction rolled back", rolledBack)
	}

	var committed NodeID

	err = g.Transact(func(tx Tx) error {
		var txErr error
		committed, txErr = createNodeTx(tx)
		if txErr != nil {
			return txErr
		}

		return names.Bind(tx, "A", committed)
	})
	if err != nil {
		t.Fatalf("Transact() error = %v", err)
	}

	if found, ok := names.Lookup("A"); !ok || found != committed {
		t.Fatalf("Lookup(\"A\") = (%d,%v), want (%d,true)", found, ok, committed)
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

func TestNameRegistryCreateNamedNodeComposesAndRollsBackWithEnclosingTransaction(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)
	errOuter := errors.New("outer failure")

	var rolledBack NodeID

	err := g.Transact(func(tx Tx) error {
		var createErr error
		rolledBack, createErr = names.CreateNamedNode(tx, "A")
		if createErr != nil {
			return createErr
		}

		return errOuter
	})
	if !errors.Is(err, errOuter) {
		t.Fatalf("Transact(fail) error = %v, want %v", err, errOuter)
	}
	if _, ok := names.Lookup("A"); ok {
		t.Fatal("name \"A\" is bound after its enclosing transaction rolled back")
	}
	if g.NodeExists(rolledBack) {
		t.Fatalf("node %d survived its enclosing transaction's rollback", rolledBack)
	}

	var committed NodeID

	err = g.Transact(func(tx Tx) error {
		var ensureErr error
		committed, ensureErr = names.EnsureNamedNode(tx, "A")
		return ensureErr
	})
	if err != nil {
		t.Fatalf("Transact(commit) error = %v", err)
	}
	if found, ok := names.Lookup("A"); !ok || found != committed {
		t.Fatalf("Lookup(\"A\") = (%d,%v), want (%d,true)", found, ok, committed)
	}
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

func TestDomainPointerRegistryDSetDomainFailureRollsBackMetadataCreation(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	subject := newTestNode(t, fx.graph)
	notASet := newTestNode(t, fx.graph)

	err := fx.domainD.SetDomain(fx.graph, subject, notASet)
	if !errors.Is(err, ErrInvalidSetOperand) {
		t.Fatalf("SetDomain(subject, notASet) error = %v, want %v", err, ErrInvalidSetOperand)
	}

	has, hasErr := fx.domainD.metadata.HasMetadata(fx.graph, subject)
	if hasErr != nil {
		t.Fatalf("HasMetadata(): %v", hasErr)
	}
	if has {
		t.Fatal("a rejected SetDomain left a metadata node behind; it should have rolled back with the rest")
	}
}

// TestNameRegistryLookupIsSafeWhileGraphActorBindsAndUnbinds runs readers
// hammering Lookup/NameForNode while writers create and unbind names
// through a GraphActor. Its value is under `go test -race`: before
// NameRegistry guarded its maps, this was a data race.
func TestNameRegistryLookupIsSafeWhileGraphActorBindsAndUnbinds(t *testing.T) {
	actor := NewGraphActor(&Graph{})
	defer actor.Close()

	names := NewNameRegistry(actor)

	const writers = 20

	nameFor := func(i int) string { return strings.Repeat("n", i+1) }

	stop := make(chan struct{})

	var readers sync.WaitGroup
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()

			for {
				select {
				case <-stop:
					return
				default:
				}

				for i := 0; i < writers; i++ {
					id, ok := names.Lookup(nameFor(i))
					if ok {
						names.NameForNode(id)
					}
				}
			}
		}()
	}

	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			name := nameFor(i)

			id, createErr := names.CreateNamedNode(actor, name)
			if createErr != nil {
				t.Errorf("CreateNamedNode(%q): %v", name, createErr)
				return
			}

			if found, ok := names.Lookup(name); !ok || found != id {
				t.Errorf("Lookup(%q) = (%d,%v), want (%d,true)", name, found, ok, id)
				return
			}

			if _, unbindErr := names.Unbind(name); unbindErr != nil {
				t.Errorf("Unbind(%q): %v", name, unbindErr)
			}
		}()
	}

	wg.Wait()
	close(stop)
	readers.Wait()

	for i := 0; i < writers; i++ {
		if _, ok := names.Lookup(nameFor(i)); ok {
			t.Fatalf("name %q is still bound after Unbind()", nameFor(i))
		}
	}
}

// TestGraphActorConcurrentListAppendKeepsListValid appends from many
// goroutines to one list through a GraphActor. Append's existence and tag
// checks now run inside its transaction; Elements validates the resulting
// structure (head/tail, reciprocal links, reachability).
func TestGraphActorConcurrentListAppendKeepsListValid(t *testing.T) {
	actor := NewGraphActor(&Graph{})
	defer actor.Close()

	names := NewNameRegistry(actor)
	ids, err := names.BootstrapNames(actor, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	capsules, err := NewCapsuleRegistry(
		actor,
		ids[NameAllElementCapsules],
		ids[NameAllElementCapsulePrevSlot],
		ids[NameAllElementCapsuleValueSlot],
		ids[NameAllElementCapsuleNextSlot],
	)
	if err != nil {
		t.Fatalf("NewCapsuleRegistry(): %v", err)
	}

	lists, err := NewListRegistry(actor, capsules, ids[NameAllLists], ids[NameAllHeads], ids[NameAllTails])
	if err != nil {
		t.Fatalf("NewListRegistry(): %v", err)
	}

	list, err := lists.NewList(actor)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	const goroutines = 50

	values := make([]NodeID, goroutines)
	for i := range values {
		value, createErr := actor.CreateNode()
		if createErr != nil {
			t.Fatalf("CreateNode() for value %d: %v", i, createErr)
		}
		values[i] = value
	}

	var wg sync.WaitGroup
	errs := make([]error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = lists.Append(actor, list, values[i])
		}()
	}

	wg.Wait()

	for i, appendErr := range errs {
		if appendErr != nil {
			t.Fatalf("goroutine %d: Append() error = %v", i, appendErr)
		}
	}

	elements, err := lists.Elements(actor, list)
	if err != nil {
		t.Fatalf("Elements(): %v", err)
	}

	if !reflect.DeepEqual(sortedNodeIDs(elements), sortedNodeIDs(values)) {
		t.Fatalf("Elements() = %v, want the %d appended values in some order", elements, goroutines)
	}
}

// TestCompositeSetLogRemoveOperationIsAtomicWhenCapsuleCannotBeDeleted
// covers RemoveOperation now being one transaction: when the capsule
// cannot be deleted (something else references its value slot), the
// operation must stay in the log completely intact instead of ending up
// unlinked from the log but still holding its descriptor.
func requireListElements(t *testing.T, lists *ListRegistry, graph GraphReader, list NodeID, want []NodeID) {
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
	if g.NodeExists(capsuleA) {
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
	if !capsules.IsCapsule(g, capsuleB) || !g.HasRelationship(list, capsuleB) {
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

	if !g.NodeExists(c2) || !capsules.IsCapsule(g, c2) {
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

	capsules := logs.lists.capsules

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

	if !g.HasRelationship(log, capsule) {
		t.Fatal("capsule was unlinked from the log despite the failed RemoveOperation()")
	}
	if !g.NodeExists(u) || !g.HasRelationship(u, x) {
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
