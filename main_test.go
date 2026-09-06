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
	"sort"
	"testing"
)

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

	if _, err := g.AddRelationship(a, b); err != nil {
		t.Fatalf("AddRelationship(%d,%d): %v", a, b, err)
	}

	if _, err := g.AddRelationship(a, c); err != nil {
		t.Fatalf("AddRelationship(%d,%d): %v", a, c, err)
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

	if _, err := g.AddRelationship(a, c); err != nil {
		t.Fatalf("AddRelationship(%d,%d): %v", a, c, err)
	}

	if _, err := g.AddRelationship(b, c); err != nil {
		t.Fatalf("AddRelationship(%d,%d): %v", b, c, err)
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

	if _, err := g.AddRelationship(a, b); err != nil {
		t.Fatalf("AddRelationship(%d,%d): %v", a, b, err)
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

	if _, err := g.AddRelationship(a, b); err != nil {
		t.Fatalf("AddRelationship() returned error: %v", err)
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

	if _, err := g.AddRelationship(a, b); err != nil {
		t.Fatalf("AddRelationship() returned error: %v", err)
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

	if err := g.DeleteNode(a); err != nil {
		t.Fatalf(
			"DeleteNode(%d) after removing relationships returned error: %v",
			a, err,
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

	if _, err := g.AddRelationship(a, b); err != nil {
		t.Fatalf("AddRelationship() returned error: %v", err)
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

	id, err := names.CreateNamedNode("ROOT")
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

	err := names.Bind("A", nonexistent)
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

	if err := names.Bind("A", a); err != nil {
		t.Fatalf("first Bind() returned error: %v", err)
	}

	err = names.Bind("A", b)
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

	if err := names.Bind("A", id); err != nil {
		t.Fatalf("first Bind() returned error: %v", err)
	}

	err = names.Bind("B", id)
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

	if err := names.Bind("A", id); err != nil {
		t.Fatalf("first Bind() returned error: %v", err)
	}

	if err := names.Bind("A", id); err != nil {
		t.Fatalf("identical second Bind() returned error: %v", err)
	}
}

func TestNameRegistryCreateNamedNodeDoesNotDuplicateName(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	first, err := names.CreateNamedNode("A")
	if err != nil {
		t.Fatalf("first CreateNamedNode() returned error: %v", err)
	}

	_, err = names.CreateNamedNode("A")
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

	id, err := names.CreateNamedNode("A")
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

	id, err := names.CreateNamedNode("A")
	if err != nil {
		t.Fatalf("CreateNamedNode(): %v", err)
	}

	if err := names.DeleteNode(id); err != nil {
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

	a, err := names.CreateNamedNode("A")
	if err != nil {
		t.Fatalf("CreateNamedNode(): %v", err)
	}

	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	if _, err := g.AddRelationship(a, b); err != nil {
		t.Fatalf("AddRelationship(): %v", err)
	}

	err = names.DeleteNode(a)
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

	if err := names.DeleteNode(id); err != nil {
		t.Fatalf("DeleteNode(%d): %v", id, err)
	}

	if g.NodeExists(id) {
		t.Fatalf("node %d still exists after DeleteNode()", id)
	}
}

func TestNameRegistryDoesNotCreateRelationships(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	a, err := names.CreateNamedNode("A")
	if err != nil {
		t.Fatalf("CreateNamedNode(\"A\") returned error: %v", err)
	}

	b, err := names.CreateNamedNode("B")
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

	id, err := names.EnsureNamedNode("A")
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

	first, err := names.EnsureNamedNode("A")
	if err != nil {
		t.Fatalf("first EnsureNamedNode() returned error: %v", err)
	}

	second, err := names.EnsureNamedNode("A")
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

	if err := names.Bind("A", id); err != nil {
		t.Fatalf("Bind() returned error: %v", err)
	}

	found, err := names.EnsureNamedNode("A")
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

	if err := names.Bind("A", id); err != nil {
		t.Fatalf("Bind(): %v", err)
	}

	// Bypass NameRegistry.DeleteNode on purpose, simulating a caller bug
	// that deletes the node without coordinating with the name registry.
	if err := g.DeleteNode(id); err != nil {
		t.Fatalf("DeleteNode(%d) via raw Graph: %v", id, err)
	}

	_, err = names.EnsureNamedNode("A")
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

	if err := names.Bind("A", id); err != nil {
		t.Fatalf("Bind(): %v", err)
	}

	if err := g.DeleteNode(id); err != nil {
		t.Fatalf("DeleteNode(%d) via raw Graph: %v", id, err)
	}

	_, err = names.CreateNamedNode("A")
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

	if err := names.Bind("A", stale); err != nil {
		t.Fatalf("Bind(): %v", err)
	}

	if err := g.DeleteNode(stale); err != nil {
		t.Fatalf("DeleteNode(%d) via raw Graph: %v", stale, err)
	}

	replacement, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for replacement: %v", err)
	}

	err = names.Bind("A", replacement)
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

	if err := names.Bind("A", id); err != nil {
		t.Fatalf("Bind(): %v", err)
	}

	if err := g.DeleteNode(id); err != nil {
		t.Fatalf("DeleteNode(%d) via raw Graph: %v", id, err)
	}

	_, err = names.BootstrapNames([]string{"A", "B"})
	if !errors.Is(err, ErrNameBoundToDeletedNode) {
		t.Fatalf("BootstrapNames() error = %v, want %v", err, ErrNameBoundToDeletedNode)
	}
}

func TestBootstrapNamesCreatesAllNames(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	ids, err := names.BootstrapNames([]string{"A", "B", "C"})
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

	first, err := names.BootstrapNames([]string{"A", "B"})
	if err != nil {
		t.Fatalf("first BootstrapNames() returned error: %v", err)
	}

	second, err := names.BootstrapNames([]string{"A", "B"})
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

	first, err := names.BootstrapNames([]string{"A", "B"})
	if err != nil {
		t.Fatalf("first BootstrapNames() returned error: %v", err)
	}

	second, err := names.BootstrapNames([]string{"B", "C"})
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

	ids, err := names.BootstrapNames([]string{"A", "A", "A"})
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

	ids, err := names.BootstrapNames(FoundationalNames)
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

	if _, err := g.AddRelationship(a, root); err != nil {
		t.Fatalf("AddRelationship(a, ROOT): %v", err)
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

	if _, err := r.AddRelationship(a, b); err != nil {
		t.Fatalf("AddRelationship(a, b): %v", err)
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

	if _, err := g.AddRelationship(a, b); err != nil {
		t.Fatalf("AddRelationship(a, b): %v", err)
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

	if _, err := g.AddRelationship(root, a); err != nil {
		t.Fatalf("AddRelationship(ROOT, a) in primitive graph: %v", err)
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

	if _, err := g.AddRelationship(root, root); err != nil {
		t.Fatalf("AddRelationship(ROOT, ROOT) in primitive graph: %v", err)
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

// newPointerTestFixture creates a fresh Graph and PointerRegistry with
// AllPointers already bootstrapped, for use by PointerRegistry tests.
func newPointerTestFixture(t *testing.T) (*Graph, *PointerRegistry) {
	t.Helper()

	var g Graph
	names := NewNameRegistry(&g)

	allPointers, err := names.EnsureNamedNode(NameAllPointers)
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

	p, err := pointers.NewPointer()
	if err != nil {
		t.Fatalf("NewPointer(): %v", err)
	}

	if !g.NodeExists(p) {
		t.Fatalf("NewPointer() returned NodeID %d that does not exist", p)
	}

	if !pointers.IsPointer(p) {
		t.Fatalf("NewPointer() did not tag %d as Pointer-kind", p)
	}

	_, hasTarget, err := pointers.Target(p)
	if err != nil {
		t.Fatalf("Target(%d): %v", p, err)
	}

	if hasTarget {
		t.Fatalf("freshly created pointer %d unexpectedly has a target", p)
	}
}

func TestPointerRegistrySetTargetAddsFirstTarget(t *testing.T) {
	g, pointers := newPointerTestFixture(t)

	p, err := pointers.NewPointer()
	if err != nil {
		t.Fatalf("NewPointer(): %v", err)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for x: %v", err)
	}

	if err := pointers.SetTarget(p, x); err != nil {
		t.Fatalf("SetTarget(%d,%d): %v", p, x, err)
	}

	target, hasTarget, err := pointers.Target(p)
	if err != nil {
		t.Fatalf("Target(%d): %v", p, err)
	}

	if !hasTarget || target != x {
		t.Fatalf("Target(%d) = (%d,%v), want (%d,true)", p, target, hasTarget, x)
	}
}

func TestPointerRegistrySetTargetIsIdempotentForSameTarget(t *testing.T) {
	g, pointers := newPointerTestFixture(t)

	p, err := pointers.NewPointer()
	if err != nil {
		t.Fatalf("NewPointer(): %v", err)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for x: %v", err)
	}

	if err := pointers.SetTarget(p, x); err != nil {
		t.Fatalf("first SetTarget(%d,%d): %v", p, x, err)
	}

	if err := pointers.SetTarget(p, x); err != nil {
		t.Fatalf("second SetTarget(%d,%d): %v", p, x, err)
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

	p, err := pointers.NewPointer()
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

	if err := pointers.SetTarget(p, x); err != nil {
		t.Fatalf("SetTarget(%d,%d): %v", p, x, err)
	}

	if err := pointers.SetTarget(p, y); err != nil {
		t.Fatalf("SetTarget(%d,%d): %v", p, y, err)
	}

	target, hasTarget, err := pointers.Target(p)
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
	_, pointers := newPointerTestFixture(t)

	p, err := pointers.NewPointer()
	if err != nil {
		t.Fatalf("NewPointer(): %v", err)
	}

	if err := pointers.SetTarget(p, p); err != nil {
		t.Fatalf("SetTarget(%d,%d) self-target: %v", p, p, err)
	}

	target, hasTarget, err := pointers.Target(p)
	if err != nil {
		t.Fatalf("Target(%d): %v", p, err)
	}

	if !hasTarget || target != p {
		t.Fatalf("Target(%d) = (%d,%v), want (%d,true)", p, target, hasTarget, p)
	}
}

func TestPointerRegistrySetTargetRequiresExistingTarget(t *testing.T) {
	_, pointers := newPointerTestFixture(t)

	p, err := pointers.NewPointer()
	if err != nil {
		t.Fatalf("NewPointer(): %v", err)
	}

	const nonexistent NodeID = 999999

	err = pointers.SetTarget(p, nonexistent)
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

	err = pointers.SetTarget(id, x)
	if !errors.Is(err, ErrNotPointer) {
		t.Fatalf("SetTarget() error = %v, want %v", err, ErrNotPointer)
	}
}

func TestPointerRegistryRemoveTargetRemovesExisting(t *testing.T) {
	g, pointers := newPointerTestFixture(t)

	p, err := pointers.NewPointer()
	if err != nil {
		t.Fatalf("NewPointer(): %v", err)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for x: %v", err)
	}

	if err := pointers.SetTarget(p, x); err != nil {
		t.Fatalf("SetTarget(%d,%d): %v", p, x, err)
	}

	removed, err := pointers.RemoveTarget(p)
	if err != nil {
		t.Fatalf("RemoveTarget(%d): %v", p, err)
	}

	if !removed {
		t.Fatal("RemoveTarget() reported that nothing was removed")
	}

	_, hasTarget, err := pointers.Target(p)
	if err != nil {
		t.Fatalf("Target(%d): %v", p, err)
	}

	if hasTarget {
		t.Fatalf("pointer %d still has a target after RemoveTarget()", p)
	}
}

func TestPointerRegistryRemoveTargetNoOpWhenEmpty(t *testing.T) {
	_, pointers := newPointerTestFixture(t)

	p, err := pointers.NewPointer()
	if err != nil {
		t.Fatalf("NewPointer(): %v", err)
	}

	removed, err := pointers.RemoveTarget(p)
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

	if pointers.IsPointer(id) {
		t.Fatalf("node %d is unexpectedly already tagged Pointer-kind", id)
	}

	if err := pointers.TagAsPointer(id); err != nil {
		t.Fatalf("TagAsPointer(%d): %v", id, err)
	}

	if !pointers.IsPointer(id) {
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

	if _, err := g.AddRelationship(id, x); err != nil {
		t.Fatalf("AddRelationship(%d,%d): %v", id, x, err)
	}

	if err := pointers.TagAsPointer(id); err != nil {
		t.Fatalf("TagAsPointer(%d): %v", id, err)
	}

	target, hasTarget, err := pointers.Target(id)
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

	if _, err := g.AddRelationship(id, x); err != nil {
		t.Fatalf("AddRelationship(%d,%d): %v", id, x, err)
	}

	if _, err := g.AddRelationship(id, y); err != nil {
		t.Fatalf("AddRelationship(%d,%d): %v", id, y, err)
	}

	err = pointers.TagAsPointer(id)
	if !errors.Is(err, ErrTooManyPointerTargets) {
		t.Fatalf("TagAsPointer() error = %v, want %v", err, ErrTooManyPointerTargets)
	}

	if pointers.IsPointer(id) {
		t.Fatalf("node %d was tagged despite violating the Pointer invariant", id)
	}
}

func TestPointerRegistryTagAsPointerIsIdempotent(t *testing.T) {
	g, pointers := newPointerTestFixture(t)

	id, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	if err := pointers.TagAsPointer(id); err != nil {
		t.Fatalf("first TagAsPointer(%d): %v", id, err)
	}

	if err := pointers.TagAsPointer(id); err != nil {
		t.Fatalf("second TagAsPointer(%d): %v", id, err)
	}
}

func TestPointerRegistryDetectsOutOfBandInvariantViolation(t *testing.T) {
	g, pointers := newPointerTestFixture(t)

	p, err := pointers.NewPointer()
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
	if _, err := g.AddRelationship(p, x); err != nil {
		t.Fatalf("AddRelationship(%d,%d) via raw Graph: %v", p, x, err)
	}

	if _, err := g.AddRelationship(p, y); err != nil {
		t.Fatalf("AddRelationship(%d,%d) via raw Graph: %v", p, y, err)
	}

	if _, _, err := pointers.Target(p); !errors.Is(err, ErrTooManyPointerTargets) {
		t.Fatalf("Target() error = %v, want %v", err, ErrTooManyPointerTargets)
	}

	z, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for z: %v", err)
	}

	if err := pointers.SetTarget(p, z); !errors.Is(err, ErrTooManyPointerTargets) {
		t.Fatalf("SetTarget() error = %v, want %v", err, ErrTooManyPointerTargets)
	}

	if _, err := pointers.RemoveTarget(p); !errors.Is(err, ErrTooManyPointerTargets) {
		t.Fatalf("RemoveTarget() error = %v, want %v", err, ErrTooManyPointerTargets)
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
	err := g.Transact(func(tx *Txn) error {
		var err error
		a, err = tx.CreateNode()
		if err != nil {
			return err
		}

		b, err = tx.CreateNode()
		if err != nil {
			return err
		}

		_, err = tx.AddRelationship(a, b)
		return err
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
	err := g.Transact(func(tx *Txn) error {
		var err error
		id, err = tx.CreateNode()
		if err != nil {
			return err
		}

		_, err = tx.AddRelationship(id, nonexistent)
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

	err = g.Transact(func(tx *Txn) error {
		if _, err := tx.AddRelationship(a, b); err != nil {
			return err
		}

		if _, err := tx.AddRelationship(a, c); err != nil {
			return err
		}

		_, err := tx.AddRelationship(a, nonexistent)
		return err
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

	if _, err := g.AddRelationship(a, b); err != nil {
		t.Fatalf("AddRelationship(%d,%d): %v", a, b, err)
	}

	const nonexistent NodeID = 999999

	err = g.Transact(func(tx *Txn) error {
		if _, err := tx.RemoveRelationship(a, b); err != nil {
			return err
		}

		_, err := tx.AddRelationship(a, nonexistent)
		return err
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

	if _, err := g.AddRelationship(a, b); err != nil {
		t.Fatalf("AddRelationship(%d,%d): %v", a, b, err)
	}

	const nonexistent NodeID = 999999

	err = g.Transact(func(tx *Txn) error {
		// (a,b) already exists, so this call reports created == false and
		// must not schedule an undo step for a relationship this
		// transaction did not itself create.
		created, err := tx.AddRelationship(a, b)
		if err != nil {
			return err
		}
		if created {
			t.Fatal("AddRelationship() reported creating an already-existing relationship")
		}

		_, err = tx.AddRelationship(a, nonexistent)
		return err
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

		_ = g.Transact(func(tx *Txn) error {
			var err error
			id, err = tx.CreateNode()
			if err != nil {
				t.Fatalf("CreateNode(): %v", err)
			}

			panic("boom")
		})
	}()

	if g.NodeExists(id) {
		t.Fatalf("node %d still exists after a panicking transaction", id)
	}
}

func TestSubPointerReusesPointerRegistryUnderDifferentTag(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	ids, err := names.BootstrapNames(FoundationalNames)
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

	u, err := subPointers.NewPointer()
	if err != nil {
		t.Fatalf("NewPointer() for u: %v", err)
	}

	if _, err := g.AddRelationship(p, u); err != nil {
		t.Fatalf("AddRelationship(p, u): %v", err)
	}

	other, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for other: %v", err)
	}

	if _, err := g.AddRelationship(p, other); err != nil {
		t.Fatalf("AddRelationship(p, other): %v", err)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for x: %v", err)
	}

	if err := subPointers.SetTarget(u, x); err != nil {
		t.Fatalf("SetTarget(u, x): %v", err)
	}

	target, hasTarget, err := subPointers.Target(u)
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

	ids, err := names.BootstrapNames(FoundationalNames)
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

	has, err := metadata.HasMetadata(subject)
	if err != nil {
		t.Fatalf("HasMetadata(): %v", err)
	}
	if has {
		t.Fatal("fresh subject unexpectedly already has metadata")
	}

	_, hasTarget, err := metadata.Target(subject)
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

	if _, err := g.AddRelationship(subject, preexisting); err != nil {
		t.Fatalf("AddRelationship(subject, preexisting): %v", err)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for x: %v", err)
	}

	if err := metadata.SetTarget(subject, x); err != nil {
		t.Fatalf("SetTarget(subject, x): %v", err)
	}

	outgoing, err := g.FindOutgoing(subject)
	if err != nil {
		t.Fatalf("FindOutgoing(subject): %v", err)
	}

	want := []Relationship{{From: subject, To: preexisting}}
	if !reflect.DeepEqual(outgoing, want) {
		t.Fatalf("FindOutgoing(subject) = %v, want %v (unchanged by the pointer representation)", outgoing, want)
	}

	target, hasTarget, err := metadata.Target(subject)
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

	if err := metadata.SetTarget(subject, subject); err != nil {
		t.Fatalf("SetTarget(subject, subject): %v", err)
	}

	target, hasTarget, err := metadata.Target(subject)
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

	if err := metadata.SetTarget(subject, x); err != nil {
		t.Fatalf("SetTarget(subject, x): %v", err)
	}

	if err := metadata.SetTarget(subject, y); err != nil {
		t.Fatalf("SetTarget(subject, y): %v", err)
	}

	target, hasTarget, err := metadata.Target(subject)
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

	if err := metadata.SetTarget(subject, x); err != nil {
		t.Fatalf("SetTarget(subject, x): %v", err)
	}

	removed, err := metadata.RemoveTarget(subject)
	if err != nil {
		t.Fatalf("RemoveTarget(subject): %v", err)
	}
	if !removed {
		t.Fatal("RemoveTarget() reported that nothing was removed")
	}

	has, err := metadata.HasMetadata(subject)
	if err != nil {
		t.Fatalf("HasMetadata(): %v", err)
	}
	if !has {
		t.Fatal("metadata node should still exist after RemoveTarget (no cascade delete)")
	}

	_, hasTarget, err := metadata.Target(subject)
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

	err = metadata.SetTarget(subject, nonexistent)
	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("SetTarget() error = %v, want %v", err, ErrNodeNotFound)
	}
}

func newPointerMetadataDTestFixture(t *testing.T) (*Graph, *PointerMetadataRegistryD) {
	t.Helper()

	var g Graph
	names := NewNameRegistry(&g)

	ids, err := names.BootstrapNames(FoundationalNames)
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

	has, err := metadata.HasMetadata(subject)
	if err != nil {
		t.Fatalf("HasMetadata(): %v", err)
	}
	if has {
		t.Fatal("fresh subject unexpectedly already has metadata")
	}

	_, hasTarget, err := metadata.Target(subject)
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

	if _, err := g.AddRelationship(subject, preexisting); err != nil {
		t.Fatalf("AddRelationship(subject, preexisting): %v", err)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for x: %v", err)
	}

	if err := metadata.SetTarget(subject, x); err != nil {
		t.Fatalf("SetTarget(subject, x): %v", err)
	}

	outgoing, err := g.FindOutgoing(subject)
	if err != nil {
		t.Fatalf("FindOutgoing(subject): %v", err)
	}

	want := []Relationship{{From: subject, To: preexisting}}
	if !reflect.DeepEqual(outgoing, want) {
		t.Fatalf("FindOutgoing(subject) = %v, want %v (unchanged by the pointer representation)", outgoing, want)
	}

	target, hasTarget, err := metadata.Target(subject)
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

	if err := metadata.SetTarget(subject, subject); err != nil {
		t.Fatalf("SetTarget(subject, subject): %v", err)
	}

	target, hasTarget, err := metadata.Target(subject)
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

	if err := metadata.SetTarget(subject, x); err != nil {
		t.Fatalf("SetTarget(subject, x): %v", err)
	}

	if err := metadata.SetTarget(subject, y); err != nil {
		t.Fatalf("SetTarget(subject, y): %v", err)
	}

	target, hasTarget, err := metadata.Target(subject)
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

	if err := metadata.SetTarget(subject, x); err != nil {
		t.Fatalf("SetTarget(subject, x): %v", err)
	}

	removed, err := metadata.RemoveTarget(subject)
	if err != nil {
		t.Fatalf("RemoveTarget(subject): %v", err)
	}
	if !removed {
		t.Fatal("RemoveTarget() reported that nothing was removed")
	}

	_, hasTarget, err := metadata.Target(subject)
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

	err = metadata.SetTarget(subject, nonexistent)
	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("SetTarget() error = %v, want %v", err, ErrNodeNotFound)
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

	if err := metadata.SetTarget(subject, x); err != nil {
		t.Fatalf("SetTarget(subject, x): %v", err)
	}

	m, err := metadata.EnsureMetadata(subject)
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
	if _, err := g.AddRelationship(m, unrelated); err != nil {
		t.Fatalf("AddRelationship(m, unrelated): %v", err)
	}

	target, hasTarget, err := metadata.Target(subject)
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

	ids, err := names.BootstrapNames(FoundationalNames)
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
	_, capsules := newCapsuleTestFixture(t)

	const nonexistent NodeID = 999999

	if _, err := capsules.NewCapsule(nonexistent); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("NewCapsule() error = %v, want %v", err, ErrNodeNotFound)
	}
}

func TestNewCapsuleTagsAndSetsValue(t *testing.T) {
	g, capsules := newCapsuleTestFixture(t)

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	capsule, err := capsules.NewCapsule(value)
	if err != nil {
		t.Fatalf("NewCapsule(): %v", err)
	}

	if !g.NodeExists(capsule) {
		t.Fatalf("NewCapsule() returned NodeID %d that does not exist", capsule)
	}

	if !capsules.IsCapsule(capsule) {
		t.Fatalf("NewCapsule() did not tag %d as an ElementCapsule", capsule)
	}

	got, hasValue, err := capsules.Value(capsule)
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

	capsule, err := capsules.NewCapsule(value)
	if err != nil {
		t.Fatalf("NewCapsule(): %v", err)
	}

	if _, hasPrev, err := capsules.Prev(capsule); err != nil {
		t.Fatalf("Prev(%d): %v", capsule, err)
	} else if hasPrev {
		t.Fatalf("freshly created capsule %d unexpectedly has a prev", capsule)
	}

	if _, hasNext, err := capsules.Next(capsule); err != nil {
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

	c1, err := capsules.NewCapsule(v1)
	if err != nil {
		t.Fatalf("NewCapsule(v1): %v", err)
	}

	c2, err := capsules.NewCapsule(v2)
	if err != nil {
		t.Fatalf("NewCapsule(v2): %v", err)
	}

	if err := capsules.SetNext(c1, c2); err != nil {
		t.Fatalf("SetNext(c1, c2): %v", err)
	}

	if err := capsules.SetPrev(c2, c1); err != nil {
		t.Fatalf("SetPrev(c2, c1): %v", err)
	}

	next, hasNext, err := capsules.Next(c1)
	if err != nil {
		t.Fatalf("Next(c1): %v", err)
	}
	if !hasNext || next != c2 {
		t.Fatalf("Next(c1) = (%d,%v), want (%d,true)", next, hasNext, c2)
	}

	prev, hasPrev, err := capsules.Prev(c2)
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

	c1, err := capsules.NewCapsule(v1)
	if err != nil {
		t.Fatalf("NewCapsule(v1): %v", err)
	}

	c2, err := capsules.NewCapsule(v2)
	if err != nil {
		t.Fatalf("NewCapsule(v2): %v", err)
	}

	if err := capsules.SetNext(c1, c2); err != nil {
		t.Fatalf("SetNext(c1, c2): %v", err)
	}

	removed, err := capsules.RemoveNext(c1)
	if err != nil {
		t.Fatalf("RemoveNext(c1): %v", err)
	}
	if !removed {
		t.Fatal("RemoveNext() reported that nothing was removed")
	}

	if _, hasNext, err := capsules.Next(c1); err != nil {
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

	if _, _, err := capsules.Value(id); !errors.Is(err, ErrNotCapsule) {
		t.Fatalf("Value() error = %v, want %v", err, ErrNotCapsule)
	}

	if err := capsules.SetValue(id, x); !errors.Is(err, ErrNotCapsule) {
		t.Fatalf("SetValue() error = %v, want %v", err, ErrNotCapsule)
	}

	if _, _, err := capsules.Prev(id); !errors.Is(err, ErrNotCapsule) {
		t.Fatalf("Prev() error = %v, want %v", err, ErrNotCapsule)
	}

	if _, _, err := capsules.Next(id); !errors.Is(err, ErrNotCapsule) {
		t.Fatalf("Next() error = %v, want %v", err, ErrNotCapsule)
	}
}

func TestCapsuleRegistryDeleteCapsuleDeletesCleanCapsule(t *testing.T) {
	g, capsules := newCapsuleTestFixture(t)

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	capsule, err := capsules.NewCapsule(value)
	if err != nil {
		t.Fatalf("NewCapsule(): %v", err)
	}

	prevSlot, found, err := capsules.slotFor(capsule, capsules.prevSlots.allPointers)
	if err != nil || !found {
		t.Fatalf("slotFor(prev): found=%v err=%v", found, err)
	}
	valueSlot, found, err := capsules.slotFor(capsule, capsules.valueSlots.allPointers)
	if err != nil || !found {
		t.Fatalf("slotFor(value): found=%v err=%v", found, err)
	}
	nextSlot, found, err := capsules.slotFor(capsule, capsules.nextSlots.allPointers)
	if err != nil || !found {
		t.Fatalf("slotFor(next): found=%v err=%v", found, err)
	}

	if err := capsules.DeleteCapsule(capsule); err != nil {
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

	list, err := lists.NewList()
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	capsule, err := lists.Append(list, value)
	if err != nil {
		t.Fatalf("Append(): %v", err)
	}

	err = capsules.DeleteCapsule(capsule)
	if !errors.Is(err, ErrCapsuleNotEmpty) {
		t.Fatalf("DeleteCapsule() error = %v, want %v", err, ErrCapsuleNotEmpty)
	}

	if !g.NodeExists(capsule) {
		t.Fatal("capsule disappeared despite a failed DeleteCapsule()")
	}
	if !capsules.IsCapsule(capsule) {
		t.Fatal("capsule lost its AllElementCapsules tag despite a failed DeleteCapsule()")
	}
	if !g.HasRelationship(list, capsule) {
		t.Fatal("capsule lost its list membership despite a failed DeleteCapsule()")
	}

	got, hasValue, err := capsules.Value(capsule)
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

	c1, err := capsules.NewCapsule(v1)
	if err != nil {
		t.Fatalf("NewCapsule(v1): %v", err)
	}
	c2, err := capsules.NewCapsule(v2)
	if err != nil {
		t.Fatalf("NewCapsule(v2): %v", err)
	}

	if err := capsules.SetNext(c1, c2); err != nil {
		t.Fatalf("SetNext(c1, c2): %v", err)
	}

	err = capsules.DeleteCapsule(c1)
	if !errors.Is(err, ErrCapsuleNotEmpty) {
		t.Fatalf("DeleteCapsule(c1) error = %v, want %v", err, ErrCapsuleNotEmpty)
	}

	if !g.NodeExists(c1) {
		t.Fatal("c1 disappeared despite a failed DeleteCapsule()")
	}

	next, hasNext, err := capsules.Next(c1)
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

	capsule, err := capsules.NewCapsule(value)
	if err != nil {
		t.Fatalf("NewCapsule(): %v", err)
	}

	valueSlot, found, err := capsules.slotFor(capsule, capsules.valueSlots.allPointers)
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
	if _, err := g.AddRelationship(metadata, valueSlot); err != nil {
		t.Fatalf("AddRelationship(metadata, valueSlot): %v", err)
	}

	err = capsules.DeleteCapsule(capsule)
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

	err = capsules.DeleteCapsule(id)
	if !errors.Is(err, ErrNotCapsule) {
		t.Fatalf("DeleteCapsule() error = %v, want %v", err, ErrNotCapsule)
	}
}

func TestCapsuleRegistryDeleteCapsuleRequiresExistingNode(t *testing.T) {
	_, capsules := newCapsuleTestFixture(t)

	const nonexistent NodeID = 999999

	err := capsules.DeleteCapsule(nonexistent)
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

	ids, err := names.BootstrapNames(FoundationalNames)
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

	capsule, err := capsules.NewCapsule(value)
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
		slot, found, err := capsules.slotFor(capsule, tc.tag)
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

	c1, err := capsules.NewCapsule(value)
	if err != nil {
		t.Fatalf("NewCapsule(value) for c1: %v", err)
	}

	c2, err := capsules.NewCapsule(value)
	if err != nil {
		t.Fatalf("NewCapsule(value) for c2: %v", err)
	}

	// A capsule holding an unrelated value must not show up.
	if _, err := capsules.NewCapsule(other); err != nil {
		t.Fatalf("NewCapsule(other): %v", err)
	}

	got, err := capsules.CapsulesWithValue(value)
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

	capsule, err := capsules.NewCapsule(value)
	if err != nil {
		t.Fatalf("NewCapsule(value): %v", err)
	}

	unrelated, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for unrelated: %v", err)
	}

	// A plain, non-value-slot relationship pointing at value from
	// elsewhere in the graph (e.g. some other structure's target).
	if _, err := g.AddRelationship(unrelated, value); err != nil {
		t.Fatalf("AddRelationship(unrelated, value): %v", err)
	}

	got, err := capsules.CapsulesWithValue(value)
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

	capsule, err := capsules.NewCapsule(value)
	if err != nil {
		t.Fatalf("NewCapsule(value): %v", err)
	}

	slot, found, err := capsules.slotFor(capsule, capsules.valueSlots.allPointers)
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
	if _, err := g.AddRelationship(metadata, slot); err != nil {
		t.Fatalf("AddRelationship(metadata, slot): %v", err)
	}

	got, err := capsules.CapsulesWithValue(value)
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

	capsule, err := capsules.NewCapsule(value)
	if err != nil {
		t.Fatalf("NewCapsule(value): %v", err)
	}

	slot, found, err := capsules.slotFor(capsule, capsules.valueSlots.allPointers)
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

	otherCapsule, err := capsules.NewCapsule(otherValue)
	if err != nil {
		t.Fatalf("NewCapsule(otherValue): %v", err)
	}

	// Bypass CapsuleRegistry entirely, simulating an out-of-band mutation
	// that wires a second, distinct capsule to the same value slot.
	if _, err := g.AddRelationship(otherCapsule, slot); err != nil {
		t.Fatalf("AddRelationship(otherCapsule, slot): %v", err)
	}

	_, err = capsules.CapsulesWithValue(value)
	if !errors.Is(err, ErrAmbiguousPointerMetadata) {
		t.Fatalf("CapsulesWithValue(value) error = %v, want %v", err, ErrAmbiguousPointerMetadata)
	}
}

func newListTestFixture(t *testing.T) (*Graph, *CapsuleRegistry, *ListRegistry) {
	t.Helper()

	var g Graph
	names := NewNameRegistry(&g)

	ids, err := names.BootstrapNames(FoundationalNames)
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

	ids, err := names.BootstrapNames(FoundationalNames)
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

	list, err := lists.NewList()
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	if !g.NodeExists(list) {
		t.Fatalf("NewList() returned NodeID %d that does not exist", list)
	}

	if !lists.IsList(list) {
		t.Fatalf("NewList() did not tag %d as a list", list)
	}

	if _, hasHead, err := lists.Head(list); err != nil {
		t.Fatalf("Head(%d): %v", list, err)
	} else if hasHead {
		t.Fatalf("fresh list %d unexpectedly has a head", list)
	}

	if _, hasTail, err := lists.Tail(list); err != nil {
		t.Fatalf("Tail(%d): %v", list, err)
	} else if hasTail {
		t.Fatalf("fresh list %d unexpectedly has a tail", list)
	}

	elements, err := lists.Elements(list)
	if err != nil {
		t.Fatalf("Elements(%d): %v", list, err)
	}
	if len(elements) != 0 {
		t.Fatalf("Elements(%d) = %v, want empty", list, elements)
	}
}

func TestListAppendSingleElementIsHeadAndTail(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList()
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	capsule, err := lists.Append(list, value)
	if err != nil {
		t.Fatalf("Append(): %v", err)
	}

	head, hasHead, err := lists.Head(list)
	if err != nil {
		t.Fatalf("Head(%d): %v", list, err)
	}
	if !hasHead || head != capsule {
		t.Fatalf("Head(%d) = (%d,%v), want (%d,true)", list, head, hasHead, capsule)
	}

	tail, hasTail, err := lists.Tail(list)
	if err != nil {
		t.Fatalf("Tail(%d): %v", list, err)
	}
	if !hasTail || tail != capsule {
		t.Fatalf("Tail(%d) = (%d,%v), want (%d,true)", list, tail, hasTail, capsule)
	}
}

func TestListAppendMultipleMaintainsOrder(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList()
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	var values []NodeID
	for i := 0; i < 3; i++ {
		v, err := g.CreateNode()
		if err != nil {
			t.Fatalf("CreateNode() for value %d: %v", i, err)
		}
		values = append(values, v)

		if _, err := lists.Append(list, v); err != nil {
			t.Fatalf("Append(%d): %v", v, err)
		}
	}

	got, err := lists.Elements(list)
	if err != nil {
		t.Fatalf("Elements(%d): %v", list, err)
	}

	if !reflect.DeepEqual(got, values) {
		t.Fatalf("Elements(%d) = %v, want %v", list, got, values)
	}

	tail, hasTail, err := lists.Tail(list)
	if err != nil {
		t.Fatalf("Tail(%d): %v", list, err)
	}
	lastValue, _, err := lists.capsules.Value(tail)
	if err != nil {
		t.Fatalf("Value(tail): %v", err)
	}
	if !hasTail || lastValue != values[len(values)-1] {
		t.Fatalf("tail capsule's value = %d, want %d", lastValue, values[len(values)-1])
	}
}

func TestListPrependAddsAtFront(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList()
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

	if _, err := lists.Append(list, a); err != nil {
		t.Fatalf("Append(a): %v", err)
	}
	if _, err := lists.Prepend(list, b); err != nil {
		t.Fatalf("Prepend(b): %v", err)
	}

	got, err := lists.Elements(list)
	if err != nil {
		t.Fatalf("Elements(%d): %v", list, err)
	}

	want := []NodeID{b, a}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Elements(%d) = %v, want %v", list, got, want)
	}

	head, hasHead, err := lists.Head(list)
	if err != nil {
		t.Fatalf("Head(%d): %v", list, err)
	}
	headValue, _, _ := lists.capsules.Value(head)
	if !hasHead || headValue != b {
		t.Fatalf("head value = %d, want %d", headValue, b)
	}
}

func TestListInsertAfterMiddle(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList()
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

	capsuleA, err := lists.Append(list, a)
	if err != nil {
		t.Fatalf("Append(a): %v", err)
	}
	if _, err := lists.Append(list, c); err != nil {
		t.Fatalf("Append(c): %v", err)
	}

	if _, err := lists.InsertAfter(list, capsuleA, b); err != nil {
		t.Fatalf("InsertAfter(capsuleA, b): %v", err)
	}

	got, err := lists.Elements(list)
	if err != nil {
		t.Fatalf("Elements(%d): %v", list, err)
	}

	want := []NodeID{a, b, c}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Elements(%d) = %v, want %v", list, got, want)
	}

	tail, hasTail, err := lists.Tail(list)
	if err != nil {
		t.Fatalf("Tail(%d): %v", list, err)
	}
	tailValue, _, _ := lists.capsules.Value(tail)
	if !hasTail || tailValue != c {
		t.Fatalf("tail value = %d, want %d (tail should be unaffected by a middle insert)", tailValue, c)
	}
}

func TestListInsertAfterTailUpdatesTail(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList()
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

	capsuleA, err := lists.Append(list, a)
	if err != nil {
		t.Fatalf("Append(a): %v", err)
	}

	capsuleB, err := lists.InsertAfter(list, capsuleA, b)
	if err != nil {
		t.Fatalf("InsertAfter(capsuleA, b): %v", err)
	}

	tail, hasTail, err := lists.Tail(list)
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

	head, hasHead, err := lists.Head(list)
	if err != nil {
		t.Fatalf("Head(%d): %v", list, err)
	}
	if !hasHead || head != capsuleA {
		t.Fatalf("Head(%d) = (%d,%v), want (%d,true) (head should be unaffected)", list, head, hasHead, capsuleA)
	}
}

func TestListInsertAfterRequiresCapsuleInList(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList()
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

	_, err = lists.InsertAfter(list, other, value)
	if !errors.Is(err, ErrCapsuleNotInList) {
		t.Fatalf("InsertAfter() error = %v, want %v", err, ErrCapsuleNotInList)
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

	if _, _, err := lists.Head(notAList); !errors.Is(err, ErrNotList) {
		t.Fatalf("Head() error = %v, want %v", err, ErrNotList)
	}

	if _, _, err := lists.Tail(notAList); !errors.Is(err, ErrNotList) {
		t.Fatalf("Tail() error = %v, want %v", err, ErrNotList)
	}

	if _, err := lists.Append(notAList, value); !errors.Is(err, ErrNotList) {
		t.Fatalf("Append() error = %v, want %v", err, ErrNotList)
	}

	if _, err := lists.Prepend(notAList, value); !errors.Is(err, ErrNotList) {
		t.Fatalf("Prepend() error = %v, want %v", err, ErrNotList)
	}

	if _, err := lists.Elements(notAList); !errors.Is(err, ErrNotList) {
		t.Fatalf("Elements() error = %v, want %v", err, ErrNotList)
	}
}

func TestListContainsFindsValue(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList()
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

	if _, err := lists.Append(list, a); err != nil {
		t.Fatalf("Append(a): %v", err)
	}
	capsuleB, err := lists.Append(list, b)
	if err != nil {
		t.Fatalf("Append(b): %v", err)
	}
	if _, err := lists.Append(list, c); err != nil {
		t.Fatalf("Append(c): %v", err)
	}

	got, found, err := lists.Contains(list, b)
	if err != nil {
		t.Fatalf("Contains(list, b): %v", err)
	}
	if !found || got != capsuleB {
		t.Fatalf("Contains(list, b) = (%d,%v), want (%d,true)", got, found, capsuleB)
	}
}

func TestListContainsFalseForAbsentValue(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList()
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

	if _, err := lists.Append(list, a); err != nil {
		t.Fatalf("Append(a): %v", err)
	}

	_, found, err := lists.Contains(list, absent)
	if err != nil {
		t.Fatalf("Contains(list, absent): %v", err)
	}
	if found {
		t.Fatal("Contains() reported a value that was never appended")
	}
}

func TestListContainsScopedToOwningList(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	listA, err := lists.NewList()
	if err != nil {
		t.Fatalf("NewList() for listA: %v", err)
	}

	listB, err := lists.NewList()
	if err != nil {
		t.Fatalf("NewList() for listB: %v", err)
	}

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	if _, err := lists.Append(listB, value); err != nil {
		t.Fatalf("Append(listB, value): %v", err)
	}

	_, found, err := lists.Contains(listA, value)
	if err != nil {
		t.Fatalf("Contains(listA, value): %v", err)
	}
	if found {
		t.Fatal("Contains(listA, value) incorrectly found a value that only exists in listB")
	}

	_, found, err = lists.Contains(listB, value)
	if err != nil {
		t.Fatalf("Contains(listB, value): %v", err)
	}
	if !found {
		t.Fatal("Contains(listB, value) did not find a value that was appended to listB")
	}
}

func TestListOccurrencesOfFindsDuplicates(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList()
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	c1, err := lists.Append(list, value)
	if err != nil {
		t.Fatalf("first Append(value): %v", err)
	}

	c2, err := lists.Append(list, value)
	if err != nil {
		t.Fatalf("second Append(value): %v", err)
	}

	got, err := lists.OccurrencesOf(list, value)
	if err != nil {
		t.Fatalf("OccurrencesOf(list, value): %v", err)
	}

	want := sortedNodeIDs([]NodeID{c1, c2})
	got = sortedNodeIDs(got)

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OccurrencesOf(list, value) = %v, want %v", got, want)
	}

	_, found, err := lists.Contains(list, value)
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

	if _, _, err := lists.Contains(notAList, value); !errors.Is(err, ErrNotList) {
		t.Fatalf("Contains() error = %v, want %v", err, ErrNotList)
	}

	if _, err := lists.OccurrencesOf(notAList, value); !errors.Is(err, ErrNotList) {
		t.Fatalf("OccurrencesOf() error = %v, want %v", err, ErrNotList)
	}
}

func TestListContainsRequiresExistingValue(t *testing.T) {
	_, _, lists := newListTestFixture(t)

	list, err := lists.NewList()
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	const nonexistent NodeID = 999999

	if _, _, err := lists.Contains(list, nonexistent); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Contains() error = %v, want %v", err, ErrNodeNotFound)
	}
}

func TestListRemoveWithoutDeletingCapsuleMiddleElement(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList()
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

	capsuleA, err := lists.Append(list, a)
	if err != nil {
		t.Fatalf("Append(a): %v", err)
	}
	capsuleB, err := lists.Append(list, b)
	if err != nil {
		t.Fatalf("Append(b): %v", err)
	}
	if _, err := lists.Append(list, c); err != nil {
		t.Fatalf("Append(c): %v", err)
	}

	if err := lists.RemoveWithoutDeletingCapsule(list, capsuleB); err != nil {
		t.Fatalf("RemoveWithoutDeletingCapsule(capsuleB): %v", err)
	}

	got, err := lists.Elements(list)
	if err != nil {
		t.Fatalf("Elements(%d): %v", list, err)
	}

	want := []NodeID{a, c}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Elements(%d) = %v, want %v", list, got, want)
	}

	next, hasNext, err := lists.capsules.Next(capsuleA)
	if err != nil {
		t.Fatalf("Next(capsuleA): %v", err)
	}
	if !hasNext {
		t.Fatal("capsuleA lost its next link after an unrelated middle removal")
	}
	nextValue, _, _ := lists.capsules.Value(next)
	if nextValue != c {
		t.Fatalf("capsuleA's next value = %d, want %d", nextValue, c)
	}
}

func TestListRemoveWithoutDeletingCapsuleHeadUpdatesHead(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList()
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

	capsuleA, err := lists.Append(list, a)
	if err != nil {
		t.Fatalf("Append(a): %v", err)
	}
	capsuleB, err := lists.Append(list, b)
	if err != nil {
		t.Fatalf("Append(b): %v", err)
	}

	if err := lists.RemoveWithoutDeletingCapsule(list, capsuleA); err != nil {
		t.Fatalf("RemoveWithoutDeletingCapsule(capsuleA): %v", err)
	}

	head, hasHead, err := lists.Head(list)
	if err != nil {
		t.Fatalf("Head(%d): %v", list, err)
	}
	if !hasHead || head != capsuleB {
		t.Fatalf("Head(%d) = (%d,%v), want (%d,true)", list, head, hasHead, capsuleB)
	}

	if _, hasPrev, err := lists.capsules.Prev(capsuleB); err != nil {
		t.Fatalf("Prev(capsuleB): %v", err)
	} else if hasPrev {
		t.Fatal("new head capsuleB unexpectedly still has a prev")
	}
}

func TestListRemoveWithoutDeletingCapsuleTailUpdatesTail(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList()
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

	capsuleA, err := lists.Append(list, a)
	if err != nil {
		t.Fatalf("Append(a): %v", err)
	}
	capsuleB, err := lists.Append(list, b)
	if err != nil {
		t.Fatalf("Append(b): %v", err)
	}

	if err := lists.RemoveWithoutDeletingCapsule(list, capsuleB); err != nil {
		t.Fatalf("RemoveWithoutDeletingCapsule(capsuleB): %v", err)
	}

	tail, hasTail, err := lists.Tail(list)
	if err != nil {
		t.Fatalf("Tail(%d): %v", list, err)
	}
	if !hasTail || tail != capsuleA {
		t.Fatalf("Tail(%d) = (%d,%v), want (%d,true)", list, tail, hasTail, capsuleA)
	}

	if _, hasNext, err := lists.capsules.Next(capsuleA); err != nil {
		t.Fatalf("Next(capsuleA): %v", err)
	} else if hasNext {
		t.Fatal("new tail capsuleA unexpectedly still has a next")
	}
}

func TestListRemoveWithoutDeletingCapsuleSoleElementEmptiesList(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList()
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}

	capsuleA, err := lists.Append(list, a)
	if err != nil {
		t.Fatalf("Append(a): %v", err)
	}

	if err := lists.RemoveWithoutDeletingCapsule(list, capsuleA); err != nil {
		t.Fatalf("RemoveWithoutDeletingCapsule(capsuleA): %v", err)
	}

	if _, hasHead, err := lists.Head(list); err != nil {
		t.Fatalf("Head(%d): %v", list, err)
	} else if hasHead {
		t.Fatal("list unexpectedly still has a head after removing its sole element")
	}

	if _, hasTail, err := lists.Tail(list); err != nil {
		t.Fatalf("Tail(%d): %v", list, err)
	} else if hasTail {
		t.Fatal("list unexpectedly still has a tail after removing its sole element")
	}

	elements, err := lists.Elements(list)
	if err != nil {
		t.Fatalf("Elements(%d): %v", list, err)
	}
	if len(elements) != 0 {
		t.Fatalf("Elements(%d) = %v, want empty", list, elements)
	}
}

func TestListRemoveWithoutDeletingCapsuleClearsCapsuleOwnLinks(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList()
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

	if _, err := lists.Append(list, a); err != nil {
		t.Fatalf("Append(a): %v", err)
	}
	capsuleB, err := lists.Append(list, b)
	if err != nil {
		t.Fatalf("Append(b): %v", err)
	}
	if _, err := lists.Append(list, c); err != nil {
		t.Fatalf("Append(c): %v", err)
	}

	if err := lists.RemoveWithoutDeletingCapsule(list, capsuleB); err != nil {
		t.Fatalf("RemoveWithoutDeletingCapsule(capsuleB): %v", err)
	}

	if _, hasPrev, err := lists.capsules.Prev(capsuleB); err != nil {
		t.Fatalf("Prev(capsuleB): %v", err)
	} else if hasPrev {
		t.Fatal("removed capsuleB still has a prev link into its old list")
	}

	if _, hasNext, err := lists.capsules.Next(capsuleB); err != nil {
		t.Fatalf("Next(capsuleB): %v", err)
	} else if hasNext {
		t.Fatal("removed capsuleB still has a next link into its old list")
	}

	// The capsule itself remains a valid, addressable ElementCapsule --
	// removal from a list does not delete or untag it.
	if !lists.capsules.IsCapsule(capsuleB) {
		t.Fatal("removed capsuleB lost its AllElementCapsules tag")
	}

	value, hasValue, err := lists.capsules.Value(capsuleB)
	if err != nil {
		t.Fatalf("Value(capsuleB): %v", err)
	}
	if !hasValue || value != b {
		t.Fatalf("Value(capsuleB) = (%d,%v), want (%d,true)", value, hasValue, b)
	}
}

func TestListRemoveWithoutDeletingCapsuleRequiresCapsuleInList(t *testing.T) {
	g, capsules, lists := newListTestFixture(t)

	list, err := lists.NewList()
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	// A capsule that exists but was never linked into this list.
	unrelated, err := capsules.NewCapsule(value)
	if err != nil {
		t.Fatalf("NewCapsule(): %v", err)
	}

	err = lists.RemoveWithoutDeletingCapsule(list, unrelated)
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

	capsule, err := capsules.NewCapsule(value)
	if err != nil {
		t.Fatalf("NewCapsule(): %v", err)
	}

	err = lists.RemoveWithoutDeletingCapsule(notAList, capsule)
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

	err = lists.DeleteList(notAList)
	if !errors.Is(err, ErrNotList) {
		t.Fatalf("DeleteList() error = %v, want %v", err, ErrNotList)
	}
}

func TestListDeleteListFailsIfNotEmpty(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList()
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	if _, err := lists.Append(list, value); err != nil {
		t.Fatalf("Append(): %v", err)
	}

	err = lists.DeleteList(list)
	if !errors.Is(err, ErrNodeNotEmpty) {
		t.Fatalf("DeleteList() error = %v, want %v", err, ErrNodeNotEmpty)
	}

	if !g.NodeExists(list) {
		t.Fatalf("list %d disappeared even though deletion should have failed", list)
	}

	if !lists.IsList(list) {
		t.Fatal("AllLists tag was not restored after a failed DeleteList()")
	}
}

func TestListDeleteListSucceedsWhenEmpty(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList()
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	if err := lists.DeleteList(list); err != nil {
		t.Fatalf("DeleteList(): %v", err)
	}

	if g.NodeExists(list) {
		t.Fatalf("list %d still exists after successful DeleteList()", list)
	}
}

func TestListRemoveWithoutDeletingCapsuleThenDeleteListSucceeds(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList()
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	capsule, err := lists.Append(list, value)
	if err != nil {
		t.Fatalf("Append(): %v", err)
	}

	if err := lists.RemoveWithoutDeletingCapsule(list, capsule); err != nil {
		t.Fatalf("RemoveWithoutDeletingCapsule(): %v", err)
	}

	if err := lists.DeleteList(list); err != nil {
		t.Fatalf("DeleteList() after RemoveWithoutDeletingCapsule(): %v", err)
	}

	if g.NodeExists(list) {
		t.Fatalf("list %d still exists after successful DeleteList()", list)
	}
}

func TestListRemoveDeletesUnreferencedCapsule(t *testing.T) {
	g, _, lists := newListTestFixture(t)

	list, err := lists.NewList()
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	capsule, err := lists.Append(list, value)
	if err != nil {
		t.Fatalf("Append(): %v", err)
	}

	deleted, err := lists.Remove(list, capsule)
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

	elements, err := lists.Elements(list)
	if err != nil {
		t.Fatalf("Elements(list): %v", err)
	}
	if len(elements) != 0 {
		t.Fatalf("Elements(list) = %v, want empty after Remove()", elements)
	}
}

func TestListRemoveKeepsCapsuleIfStillReferencedElsewhere(t *testing.T) {
	g, capsules, lists := newListTestFixture(t)

	list, err := lists.NewList()
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	capsule, err := lists.Append(list, value)
	if err != nil {
		t.Fatalf("Append(): %v", err)
	}

	valueSlot, found, err := capsules.slotFor(capsule, capsules.valueSlots.allPointers)
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
	if _, err := g.AddRelationship(metadata, valueSlot); err != nil {
		t.Fatalf("AddRelationship(metadata, valueSlot): %v", err)
	}

	deleted, err := lists.Remove(list, capsule)
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
	elements, err := lists.Elements(list)
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
	if !capsules.IsCapsule(capsule) {
		t.Fatal("capsule lost its AllElementCapsules tag despite deletion being refused")
	}
}

func TestListRemoveRequiresCapsuleInList(t *testing.T) {
	g, capsules, lists := newListTestFixture(t)

	list, err := lists.NewList()
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	unrelated, err := capsules.NewCapsule(value)
	if err != nil {
		t.Fatalf("NewCapsule(): %v", err)
	}

	_, err = lists.Remove(list, unrelated)
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

	capsule, err := capsules.NewCapsule(value)
	if err != nil {
		t.Fatalf("NewCapsule(): %v", err)
	}

	_, err = lists.Remove(notAList, capsule)
	if !errors.Is(err, ErrNotList) {
		t.Fatalf("Remove() error = %v, want %v", err, ErrNotList)
	}
}

// These tests deliberately exploit freedoms that the primitive Graph permits.
// They are not tests of "normal" construction; they ask whether higher-level
// discovery depends on accidental child/parent cardinality or child ordering.

func TestAdversarialListIgnoresUnrelatedListChild(t *testing.T) {
	g, _, lists := newListTestFixture(t)
	list, err := lists.NewList()
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
	if _, err := lists.Append(list, a); err != nil {
		t.Fatalf("Append(a): %v", err)
	}
	if _, err := lists.Append(list, b); err != nil {
		t.Fatalf("Append(b): %v", err)
	}

	unrelated, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(unrelated): %v", err)
	}
	if _, err := g.AddRelationship(list, unrelated); err != nil {
		t.Fatalf("AddRelationship(list, unrelated): %v", err)
	}

	got, err := lists.Elements(list)
	if err != nil {
		t.Fatalf("Elements(): %v", err)
	}
	if want := []NodeID{a, b}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Elements() = %v, want %v", got, want)
	}
	if _, _, err := lists.Head(list); err != nil {
		t.Fatalf("Head(): %v", err)
	}
	if _, _, err := lists.Tail(list); err != nil {
		t.Fatalf("Tail(): %v", err)
	}
}

func TestAdversarialCapsuleIgnoresUnrelatedChild(t *testing.T) {
	g, capsules := newCapsuleTestFixture(t)
	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(value): %v", err)
	}
	capsule, err := capsules.NewCapsule(value)
	if err != nil {
		t.Fatalf("NewCapsule(): %v", err)
	}

	unrelated, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(unrelated): %v", err)
	}
	if _, err := g.AddRelationship(capsule, unrelated); err != nil {
		t.Fatalf("AddRelationship(capsule, unrelated): %v", err)
	}

	got, hasValue, err := capsules.Value(capsule)
	if err != nil {
		t.Fatalf("Value(): %v", err)
	}
	if !hasValue || got != value {
		t.Fatalf("Value() = (%d,%v), want (%d,true)", got, hasValue, value)
	}
}

func TestAdversarialValueMayBeTheListItself(t *testing.T) {
	_, _, lists := newListTestFixture(t)
	list, err := lists.NewList()
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}
	capsule, err := lists.Append(list, list)
	if err != nil {
		t.Fatalf("Append(list as value): %v", err)
	}

	got, err := lists.Elements(list)
	if err != nil {
		t.Fatalf("Elements(): %v", err)
	}
	if want := []NodeID{list}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Elements() = %v, want %v", got, want)
	}
	value, hasValue, err := lists.capsules.Value(capsule)
	if err != nil {
		t.Fatalf("Value(): %v", err)
	}
	if !hasValue || value != list {
		t.Fatalf("Value() = (%d,%v), want (%d,true)", value, hasValue, list)
	}
}

func TestAdversarialSharedValueAcrossListsRemainsSeparateOccurrences(t *testing.T) {
	g, _, lists := newListTestFixture(t)
	listA, err := lists.NewList()
	if err != nil {
		t.Fatalf("NewList(A): %v", err)
	}
	listB, err := lists.NewList()
	if err != nil {
		t.Fatalf("NewList(B): %v", err)
	}
	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(value): %v", err)
	}

	cA, err := lists.Append(listA, value)
	if err != nil {
		t.Fatalf("Append(A): %v", err)
	}
	cB, err := lists.Append(listB, value)
	if err != nil {
		t.Fatalf("Append(B): %v", err)
	}
	if cA == cB {
		t.Fatal("two occurrences unexpectedly share one capsule")
	}

	occA, err := lists.OccurrencesOf(listA, value)
	if err != nil {
		t.Fatalf("OccurrencesOf(A): %v", err)
	}
	occB, err := lists.OccurrencesOf(listB, value)
	if err != nil {
		t.Fatalf("OccurrencesOf(B): %v", err)
	}
	if !reflect.DeepEqual(occA, []NodeID{cA}) || !reflect.DeepEqual(occB, []NodeID{cB}) {
		t.Fatalf("OccurrencesOf = A:%v B:%v, want A:%v B:%v", occA, occB, []NodeID{cA}, []NodeID{cB})
	}
}

func TestAdversarialDetachedCapsuleDoesNotBecomeListMember(t *testing.T) {
	g, capsules, lists := newListTestFixture(t)
	list, err := lists.NewList()
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}
	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(value): %v", err)
	}
	capsule, err := capsules.NewCapsule(value)
	if err != nil {
		t.Fatalf("NewCapsule(): %v", err)
	}

	if _, _, err := lists.Contains(list, value); err != nil {
		t.Fatalf("Contains(): %v", err)
	}
	if err := lists.RemoveWithoutDeletingCapsule(list, capsule); !errors.Is(err, ErrCapsuleNotInList) {
		t.Fatalf("RemoveWithoutDeletingCapsule() error = %v, want %v", err, ErrCapsuleNotInList)
	}
	if !capsules.IsCapsule(capsule) || !g.NodeExists(capsule) {
		t.Fatal("detached capsule was unexpectedly deleted or untagged")
	}
}

func TestAdversarialListHeadAmbiguityFailsLoudly(t *testing.T) {
	g, _, lists := newListTestFixture(t)
	list, err := lists.NewList()
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}
	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(value): %v", err)
	}
	c1, err := lists.Append(list, value)
	if err != nil {
		t.Fatalf("Append(): %v", err)
	}
	c2, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(c2): %v", err)
	}
	if _, err := g.AddRelationship(lists.allHeads, c2); err != nil {
		t.Fatalf("AddRelationship(AllHeads,c2): %v", err)
	}
	if _, err := g.AddRelationship(list, c2); err != nil {
		t.Fatalf("AddRelationship(list,c2): %v", err)
	}
	_ = c1

	_, _, err = lists.Head(list)
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
	capsule, err := capsules.NewCapsule(value)
	if err != nil {
		t.Fatalf("NewCapsule(): %v", err)
	}
	slot, found, err := capsules.slotFor(capsule, capsules.valueSlots.allPointers)
	if err != nil || !found {
		t.Fatalf("slotFor(value): found=%v err=%v", found, err)
	}
	if _, err := g.RemoveRelationship(slot, value); err != nil {
		t.Fatalf("RemoveRelationship(slot,value): %v", err)
	}

	_, hasValue, err := capsules.Value(capsule)
	if err != nil {
		t.Fatalf("Value(): %v", err)
	}
	if hasValue {
		t.Fatal("Value() reported a value after the value edge was removed")
	}
}

func TestAdversarialListNextCycleIsDetectedWithoutTimeout(t *testing.T) {
	g, capsules, lists := newListTestFixture(t)
	list, err := lists.NewList()
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
	c1, err := lists.Append(list, valueA)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := lists.Append(list, valueB)
	if err != nil {
		t.Fatal(err)
	}

	// Deliberately corrupt the Next/Prev chain into c1 <-> c2. We mutate
	// the primitive graph directly, bypassing CapsuleRegistry's normal
	// single-target replacement semantics.
	nextSlot2, found, err := capsules.slotFor(c2, capsules.nextSlots.allPointers)
	if err != nil || !found {
		t.Fatalf("slotFor(c2,next): found=%v err=%v", found, err)
	}
	if _, err := g.AddRelationship(nextSlot2, c1); err != nil {
		t.Fatal(err)
	}
	prevSlot1, found, err := capsules.slotFor(c1, capsules.prevSlots.allPointers)
	if err != nil || !found {
		t.Fatalf("slotFor(c1,prev): found=%v err=%v", found, err)
	}
	if _, err := g.AddRelationship(prevSlot1, c2); err != nil {
		t.Fatal(err)
	}

	got, err := lists.Elements(list)
	if !errors.Is(err, ErrListCycle) {
		t.Fatalf("Elements() error = %v, want %v (got values %v)", err, ErrListCycle, got)
	}
}

func TestAdversarialListPrevCycleIsDetectedViaNextChain(t *testing.T) {
	g, capsules, lists := newListTestFixture(t)
	list, err := lists.NewList()
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
	c1, err := lists.Append(list, valueA)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := lists.Append(list, valueB)
	if err != nil {
		t.Fatal(err)
	}

	// Corrupt only Prev: c1.Prev = c2. Next remains the valid c1 -> c2
	// chain. Elements currently follows Next, so this test intentionally
	// records whether reverse-link corruption is detected by traversal.
	prevSlot1, found, err := capsules.slotFor(c1, capsules.prevSlots.allPointers)
	if err != nil || !found {
		t.Fatalf("slotFor(c1,prev): found=%v err=%v", found, err)
	}
	if _, err := g.AddRelationship(prevSlot1, c2); err != nil {
		t.Fatal(err)
	}

	got, err := lists.Elements(list)
	if !errors.Is(err, ErrInvalidListStructure) {
		t.Fatalf("Elements() error = %v, want %v (values %v)", err, ErrInvalidListStructure, got)
	}
}

func TestAdversarialListChainCanContainCapsuleFromAnotherList(t *testing.T) {
	g, capsules, lists := newListTestFixture(t)
	listA, err := lists.NewList()
	if err != nil {
		t.Fatal(err)
	}
	listB, err := lists.NewList()
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
	c1, err := lists.Append(listA, valueA)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := lists.Append(listB, valueB)
	if err != nil {
		t.Fatal(err)
	}

	// Make listA's head chain point to listB's capsule. This is an explicit
	// out-of-band topology violation.
	nextSlot1, found, err := capsules.slotFor(c1, capsules.nextSlots.allPointers)
	if err != nil || !found {
		t.Fatalf("slotFor(c1,next): found=%v err=%v", found, err)
	}
	if _, err := g.AddRelationship(nextSlot1, c2); err != nil {
		t.Fatal(err)
	}

	got, err := lists.Elements(listA)
	if !errors.Is(err, ErrInvalidListStructure) {
		t.Fatalf("Elements() error = %v, want %v (values %v)", err, ErrInvalidListStructure, got)
	}
}

func TestAdversarialDisconnectedListMemberIsIgnoredByElements(t *testing.T) {
	g, capsules, lists := newListTestFixture(t)
	list, err := lists.NewList()
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
	_, err = lists.Append(list, valueA)
	if err != nil {
		t.Fatal(err)
	}
	disconnected, err := capsules.NewCapsule(valueB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddRelationship(list, disconnected); err != nil {
		t.Fatal(err)
	}

	got, err := lists.Elements(list)
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
	capsule, err := capsules.NewCapsule(value)
	if err != nil {
		t.Fatal(err)
	}
	otherSlot, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddRelationship(capsule, otherSlot); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddRelationship(capsules.valueSlots.allPointers, otherSlot); err != nil {
		t.Fatal(err)
	}

	_, _, err = capsules.Value(capsule)
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
	c1, err := capsules.NewCapsule(valueA)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := capsules.NewCapsule(valueB)
	if err != nil {
		t.Fatal(err)
	}
	slot, found, err := capsules.slotFor(c1, capsules.valueSlots.allPointers)
	if err != nil || !found {
		t.Fatalf("slotFor(c1,value): found=%v err=%v", found, err)
	}
	if _, err := g.AddRelationship(c2, slot); err != nil {
		t.Fatal(err)
	}

	_, _, err = capsules.Value(c2)
	if !errors.Is(err, ErrAmbiguousPointerMetadata) {
		t.Fatalf("Value(c2) error = %v, want %v", err, ErrAmbiguousPointerMetadata)
	}

	_, err = capsules.CapsulesWithValue(valueA)
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
	capsule, err := capsules.NewCapsule(value)
	if err != nil {
		t.Fatal(err)
	}
	slot, found, err := capsules.slotFor(capsule, capsules.valueSlots.allPointers)
	if err != nil || !found {
		t.Fatalf("slotFor(): found=%v err=%v", found, err)
	}
	if _, err := g.RemoveRelationship(capsules.valueSlots.allPointers, slot); err != nil {
		t.Fatal(err)
	}

	_, _, err = capsules.Value(capsule)
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
	capsule, err := capsules.NewCapsule(value)
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddRelationship(capsule, wrong); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddRelationship(capsules.valueSlots.allPointers, wrong); err != nil {
		t.Fatal(err)
	}

	_, _, err = capsules.Value(capsule)
	if !errors.Is(err, ErrAmbiguousPointerMetadata) {
		t.Fatalf("Value() error = %v, want %v", err, ErrAmbiguousPointerMetadata)
	}
}

func TestAdversarialHeadPointingAtNonCapsuleFailsWhenTraversed(t *testing.T) {
	g, _, lists := newListTestFixture(t)
	list, err := lists.NewList()
	if err != nil {
		t.Fatal(err)
	}
	bogus, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddRelationship(list, bogus); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddRelationship(lists.allHeads, bogus); err != nil {
		t.Fatal(err)
	}

	_, err = lists.Elements(list)
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
	capsule, err := capsules.NewCapsule(value)
	if err != nil {
		t.Fatal(err)
	}
	slot, found, err := capsules.slotFor(capsule, capsules.prevSlots.allPointers)
	if err != nil || !found {
		t.Fatalf("slotFor(prev): found=%v err=%v", found, err)
	}
	_ = slot
	other, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddRelationship(capsule, other); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddRelationship(capsules.prevSlots.allPointers, other); err != nil {
		t.Fatal(err)
	}
	_, _, err = capsules.Prev(capsule)
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
	capsule, err := capsules.NewCapsule(value)
	if err != nil {
		t.Fatal(err)
	}
	other, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddRelationship(capsule, other); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddRelationship(capsules.nextSlots.allPointers, other); err != nil {
		t.Fatal(err)
	}
	_, _, err = capsules.Next(capsule)
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
	capsule, err := capsules.NewCapsule(value)
	if err != nil {
		t.Fatal(err)
	}
	slot, found, err := capsules.slotFor(capsule, capsules.nextSlots.allPointers)
	if err != nil || !found {
		t.Fatalf("slotFor(next): found=%v err=%v", found, err)
	}
	target, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddRelationship(slot, target); err != nil {
		t.Fatal(err)
	}
	extra, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddRelationship(slot, extra); err != nil {
		t.Fatal(err)
	}
	_, _, err = capsules.Next(capsule)
	if !errors.Is(err, ErrTooManyPointerTargets) {
		t.Fatalf("Next() error = %v, want %v", err, ErrTooManyPointerTargets)
	}
}

func TestAdversarialMissingEachCapsuleRoleTagMakesThatRoleUndiscoverable(t *testing.T) {
	roles := []struct {
		name string
		tag  func(*CapsuleRegistry) NodeID
		get  func(*CapsuleRegistry, NodeID) (NodeID, bool, error)
	}{
		{"prev", func(c *CapsuleRegistry) NodeID { return c.prevSlots.allPointers }, func(c *CapsuleRegistry, id NodeID) (NodeID, bool, error) { return c.Prev(id) }},
		{"value", func(c *CapsuleRegistry) NodeID { return c.valueSlots.allPointers }, func(c *CapsuleRegistry, id NodeID) (NodeID, bool, error) { return c.Value(id) }},
		{"next", func(c *CapsuleRegistry) NodeID { return c.nextSlots.allPointers }, func(c *CapsuleRegistry, id NodeID) (NodeID, bool, error) { return c.Next(id) }},
	}
	for _, role := range roles {
		t.Run(role.name, func(t *testing.T) {
			g, capsules := newCapsuleTestFixture(t)
			value, err := g.CreateNode()
			if err != nil {
				t.Fatal(err)
			}
			capsule, err := capsules.NewCapsule(value)
			if err != nil {
				t.Fatal(err)
			}
			slot, found, err := capsules.slotFor(capsule, role.tag(capsules))
			if err != nil || !found {
				t.Fatalf("slotFor(): found=%v err=%v", found, err)
			}
			if _, err := g.RemoveRelationship(role.tag(capsules), slot); err != nil {
				t.Fatal(err)
			}
			_, _, err = role.get(capsules, capsule)
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
	created, err := capsules.NewCapsule(value)
	if err != nil {
		t.Fatal(err)
	}
	valueSlot, found, err := capsules.slotFor(created, capsules.valueSlots.allPointers)
	if err != nil || !found {
		t.Fatal(err)
	}
	if _, err := g.RemoveRelationship(valueSlot, value); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddRelationship(valueSlot, created); err != nil {
		t.Fatal(err)
	}
	got, hasValue, err := capsules.Value(created)
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
	listA, err := lists.NewList()
	if err != nil {
		t.Fatal(err)
	}
	listB, err := lists.NewList()
	if err != nil {
		t.Fatal(err)
	}
	value, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	capsule, err := capsules.NewCapsule(value)
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

	if _, err := lists.Elements(listA); !errors.Is(err, nil) {
		t.Fatalf("Elements(listA) error = %v, want nil", err)
	}
	if _, err := lists.Elements(listB); !errors.Is(err, nil) {
		t.Fatalf("Elements(listB) error = %v, want nil", err)
	}
}

func TestAdversarialMissingValueTargetInvalidatesList(t *testing.T) {
	g, capsules, lists := newListTestFixture(t)
	list, err := lists.NewList()
	if err != nil {
		t.Fatal(err)
	}
	value, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	capsule, err := lists.Append(list, value)
	if err != nil {
		t.Fatal(err)
	}
	slot, found, err := capsules.slotFor(capsule, capsules.valueSlots.allPointers)
	if err != nil || !found {
		t.Fatal(err)
	}
	if _, err := g.RemoveRelationship(slot, value); err != nil {
		t.Fatal(err)
	}
	got, err := lists.Elements(list)
	if !errors.Is(err, ErrInvalidListStructure) {
		t.Fatalf("Elements() error = %v, want %v (values %v)", err, ErrInvalidListStructure, got)
	}
}

func TestAdversarialNonEmptyListWithoutTailIsInvalid(t *testing.T) {
	g, _, lists := newListTestFixture(t)
	list, err := lists.NewList()
	if err != nil {
		t.Fatal(err)
	}
	value, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	capsule, err := lists.Append(list, value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.RemoveRelationship(lists.allTails, capsule); err != nil {
		t.Fatal(err)
	}
	_, err = lists.Elements(list)
	if !errors.Is(err, ErrInvalidListStructure) {
		t.Fatalf("Elements() error = %v, want %v", err, ErrInvalidListStructure)
	}
}

func TestAdversarialNonEmptyListWithoutHeadIsInvalid(t *testing.T) {
	g, _, lists := newListTestFixture(t)
	list, err := lists.NewList()
	if err != nil {
		t.Fatal(err)
	}
	value, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	capsule, err := lists.Append(list, value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.RemoveRelationship(lists.allHeads, capsule); err != nil {
		t.Fatal(err)
	}
	_, err = lists.Elements(list)
	if !errors.Is(err, ErrInvalidListStructure) {
		t.Fatalf("Elements() error = %v, want %v", err, ErrInvalidListStructure)
	}
}

func TestAdversarialEmptyListWithBoundaryTagIsInvalid(t *testing.T) {
	g, _, lists := newListTestFixture(t)
	list, err := lists.NewList()
	if err != nil {
		t.Fatal(err)
	}
	bogus, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddRelationship(lists.allTails, bogus); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddRelationship(list, bogus); err != nil {
		t.Fatal(err)
	}
	_, err = lists.Elements(list)
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

	ids, err := names.BootstrapNames(FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	sets, err := NewSetRegistry(&g, ids[NameAllSets], ids[NameAllCompositeSets])
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

	set, err := sets.NewSet()
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	if !g.NodeExists(set) {
		t.Fatalf("NewSet() returned NodeID %d that does not exist", set)
	}

	if !sets.IsSet(set) {
		t.Fatalf("NewSet() did not tag %d as a set", set)
	}

	members, err := sets.Members(set)
	if err != nil {
		t.Fatalf("Members(%d): %v", set, err)
	}
	if len(members) != 0 {
		t.Fatalf("Members(%d) = %v, want empty", set, members)
	}

	size, err := sets.Size(set)
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

	if sets.IsSet(id) {
		t.Fatalf("node %d is unexpectedly already tagged Set-kind", id)
	}

	if err := sets.TagAsSet(id); err != nil {
		t.Fatalf("TagAsSet(%d): %v", id, err)
	}

	if !sets.IsSet(id) {
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

	if _, err := g.AddRelationship(id, x); err != nil {
		t.Fatalf("AddRelationship(id,x): %v", err)
	}
	if _, err := g.AddRelationship(id, y); err != nil {
		t.Fatalf("AddRelationship(id,y): %v", err)
	}

	if err := sets.TagAsSet(id); err != nil {
		t.Fatalf("TagAsSet(%d): %v", id, err)
	}

	got, err := sets.Members(id)
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

	if err := sets.TagAsSet(id); err != nil {
		t.Fatalf("first TagAsSet(%d): %v", id, err)
	}

	if err := sets.TagAsSet(id); err != nil {
		t.Fatalf("second TagAsSet(%d): %v", id, err)
	}
}

func TestSetTagAsSetRequiresExistingNode(t *testing.T) {
	_, sets := newSetTestFixture(t)

	const nonexistent NodeID = 999999

	err := sets.TagAsSet(nonexistent)
	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("TagAsSet() error = %v, want %v", err, ErrNodeNotFound)
	}
}

func TestSetAddAddsMember(t *testing.T) {
	g, sets := newSetTestFixture(t)

	set, err := sets.NewSet()
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	member, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for member: %v", err)
	}

	added, err := sets.Add(set, member)
	if err != nil {
		t.Fatalf("Add(): %v", err)
	}
	if !added {
		t.Fatal("Add() reported that nothing was added")
	}

	found, err := sets.Contains(set, member)
	if err != nil {
		t.Fatalf("Contains(): %v", err)
	}
	if !found {
		t.Fatal("Contains() did not find the newly added member")
	}

	members, err := sets.Members(set)
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

	set, err := sets.NewSet()
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	member, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for member: %v", err)
	}

	if _, err := sets.Add(set, member); err != nil {
		t.Fatalf("first Add(): %v", err)
	}

	added, err := sets.Add(set, member)
	if err != nil {
		t.Fatalf("second Add(): %v", err)
	}
	if added {
		t.Fatal("second Add() reported adding an already-present member")
	}
}

func TestSetAddAllowsSelfMembership(t *testing.T) {
	_, sets := newSetTestFixture(t)

	set, err := sets.NewSet()
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	added, err := sets.Add(set, set)
	if err != nil {
		t.Fatalf("Add(set, set): %v", err)
	}
	if !added {
		t.Fatal("self-membership was not created")
	}

	found, err := sets.Contains(set, set)
	if err != nil {
		t.Fatalf("Contains(set, set): %v", err)
	}
	if !found {
		t.Fatal("Contains(set, set) did not find self-membership")
	}

	members, err := sets.Members(set)
	if err != nil {
		t.Fatalf("Members(set): %v", err)
	}
	want := []NodeID{set}
	if !reflect.DeepEqual(members, want) {
		t.Fatalf("Members(set) = %v, want %v", members, want)
	}
}

func TestSetAddRequiresExistingMember(t *testing.T) {
	_, sets := newSetTestFixture(t)

	set, err := sets.NewSet()
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	const nonexistent NodeID = 999999

	if _, err := sets.Add(set, nonexistent); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Add() error = %v, want %v", err, ErrNodeNotFound)
	}
}

func TestSetContainsRequiresExistingMember(t *testing.T) {
	_, sets := newSetTestFixture(t)

	set, err := sets.NewSet()
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	const nonexistent NodeID = 999999

	if _, err := sets.Contains(set, nonexistent); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Contains() error = %v, want %v", err, ErrNodeNotFound)
	}
}

func TestSetRemoveRemovesExistingMember(t *testing.T) {
	g, sets := newSetTestFixture(t)

	set, err := sets.NewSet()
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	member, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for member: %v", err)
	}

	if _, err := sets.Add(set, member); err != nil {
		t.Fatalf("Add(): %v", err)
	}

	removed, err := sets.Remove(set, member)
	if err != nil {
		t.Fatalf("Remove(): %v", err)
	}
	if !removed {
		t.Fatal("Remove() reported that nothing was removed")
	}

	found, err := sets.Contains(set, member)
	if err != nil {
		t.Fatalf("Contains(): %v", err)
	}
	if found {
		t.Fatal("member is still present after Remove()")
	}
}

func TestSetRemoveNoOpWhenAbsent(t *testing.T) {
	g, sets := newSetTestFixture(t)

	set, err := sets.NewSet()
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	member, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for member: %v", err)
	}

	removed, err := sets.Remove(set, member)
	if err != nil {
		t.Fatalf("Remove(): %v", err)
	}
	if removed {
		t.Fatal("Remove() reported removal of a member that was never added")
	}
}

func TestSetRemoveRequiresExistingMember(t *testing.T) {
	_, sets := newSetTestFixture(t)

	set, err := sets.NewSet()
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	const nonexistent NodeID = 999999

	if _, err := sets.Remove(set, nonexistent); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Remove() error = %v, want %v", err, ErrNodeNotFound)
	}
}

func TestSetContainsReflectsMembership(t *testing.T) {
	g, sets := newSetTestFixture(t)

	set, err := sets.NewSet()
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	member, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for member: %v", err)
	}

	found, err := sets.Contains(set, member)
	if err != nil {
		t.Fatalf("Contains() before Add: %v", err)
	}
	if found {
		t.Fatal("Contains() found a member that was never added")
	}

	if _, err := sets.Add(set, member); err != nil {
		t.Fatalf("Add(): %v", err)
	}

	found, err = sets.Contains(set, member)
	if err != nil {
		t.Fatalf("Contains() after Add: %v", err)
	}
	if !found {
		t.Fatal("Contains() did not find a member that was added")
	}
}

func TestSetMembersReturnsAllDirectChildren(t *testing.T) {
	g, sets := newSetTestFixture(t)

	set, err := sets.NewSet()
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	var want []NodeID
	for i := 0; i < 3; i++ {
		member, err := g.CreateNode()
		if err != nil {
			t.Fatalf("CreateNode() for member %d: %v", i, err)
		}
		if _, err := sets.Add(set, member); err != nil {
			t.Fatalf("Add(member %d): %v", i, err)
		}
		want = append(want, member)
	}

	got, err := sets.Members(set)
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

	outer, err := sets.NewSet()
	if err != nil {
		t.Fatalf("NewSet() for outer: %v", err)
	}

	inner, err := sets.NewSet()
	if err != nil {
		t.Fatalf("NewSet() for inner: %v", err)
	}

	innerMember, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for innerMember: %v", err)
	}

	if _, err := sets.Add(inner, innerMember); err != nil {
		t.Fatalf("Add(inner, innerMember): %v", err)
	}

	if _, err := sets.Add(outer, inner); err != nil {
		t.Fatalf("Add(outer, inner): %v", err)
	}

	got, err := sets.Members(outer)
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

	set, err := sets.NewSet()
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	for i := 0; i < 3; i++ {
		member, err := g.CreateNode()
		if err != nil {
			t.Fatalf("CreateNode() for member %d: %v", i, err)
		}
		if _, err := sets.Add(set, member); err != nil {
			t.Fatalf("Add(member %d): %v", i, err)
		}
	}

	size, err := sets.Size(set)
	if err != nil {
		t.Fatalf("Size(): %v", err)
	}
	if size != 3 {
		t.Fatalf("Size() = %d, want 3", size)
	}

	members, err := sets.Members(set)
	if err != nil {
		t.Fatalf("Members(): %v", err)
	}
	if size != len(members) {
		t.Fatalf("Size() = %d, want len(Members()) = %d", size, len(members))
	}
}

func TestSetDeleteSetSucceedsWhenEmpty(t *testing.T) {
	g, sets := newSetTestFixture(t)

	set, err := sets.NewSet()
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	if err := sets.DeleteSet(set); err != nil {
		t.Fatalf("DeleteSet(): %v", err)
	}

	if g.NodeExists(set) {
		t.Fatalf("set %d still exists after successful DeleteSet()", set)
	}
}

func TestSetDeleteSetFailsIfNotEmpty(t *testing.T) {
	g, sets := newSetTestFixture(t)

	set, err := sets.NewSet()
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	member, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for member: %v", err)
	}

	if _, err := sets.Add(set, member); err != nil {
		t.Fatalf("Add(): %v", err)
	}

	err = sets.DeleteSet(set)
	if !errors.Is(err, ErrNodeNotEmpty) {
		t.Fatalf("DeleteSet() error = %v, want %v", err, ErrNodeNotEmpty)
	}

	if !g.NodeExists(set) {
		t.Fatal("set disappeared despite a failed DeleteSet()")
	}
	if !sets.IsSet(set) {
		t.Fatal("set lost its AllSets tag despite a failed DeleteSet()")
	}

	found, err := sets.Contains(set, member)
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

	set, err := sets.NewSet()
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	referrer, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for referrer: %v", err)
	}

	if _, err := g.AddRelationship(referrer, set); err != nil {
		t.Fatalf("AddRelationship(referrer, set): %v", err)
	}

	err = sets.DeleteSet(set)
	if !errors.Is(err, ErrNodeNotEmpty) {
		t.Fatalf("DeleteSet() error = %v, want %v", err, ErrNodeNotEmpty)
	}

	if !g.NodeExists(set) {
		t.Fatal("set disappeared despite a failed DeleteSet()")
	}
	if !sets.IsSet(set) {
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

	if _, err := sets.Add(notASet, member); !errors.Is(err, ErrNotSet) {
		t.Fatalf("Add() error = %v, want %v", err, ErrNotSet)
	}

	if _, err := sets.Remove(notASet, member); !errors.Is(err, ErrNotSet) {
		t.Fatalf("Remove() error = %v, want %v", err, ErrNotSet)
	}

	if _, err := sets.Contains(notASet, member); !errors.Is(err, ErrNotSet) {
		t.Fatalf("Contains() error = %v, want %v", err, ErrNotSet)
	}

	if _, err := sets.Members(notASet); !errors.Is(err, ErrNotSet) {
		t.Fatalf("Members() error = %v, want %v", err, ErrNotSet)
	}

	if _, err := sets.Size(notASet); !errors.Is(err, ErrNotSet) {
		t.Fatalf("Size() error = %v, want %v", err, ErrNotSet)
	}

	if err := sets.DeleteSet(notASet); !errors.Is(err, ErrNotSet) {
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

	if _, err := sets.Add(nonexistent, member); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Add() error = %v, want %v", err, ErrNodeNotFound)
	}

	if _, err := sets.Remove(nonexistent, member); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Remove() error = %v, want %v", err, ErrNodeNotFound)
	}

	if _, err := sets.Contains(nonexistent, member); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Contains() error = %v, want %v", err, ErrNodeNotFound)
	}

	if _, err := sets.Members(nonexistent); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Members() error = %v, want %v", err, ErrNodeNotFound)
	}

	if _, err := sets.Size(nonexistent); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Size() error = %v, want %v", err, ErrNodeNotFound)
	}

	if err := sets.DeleteSet(nonexistent); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("DeleteSet() error = %v, want %v", err, ErrNodeNotFound)
	}
}

// TestSetRegistryTagAsSetRejectsCompositeSetConflict pins down
// theorystate.md section 79's mutual-exclusivity rule, now enforced now
// that a second Set-representation tag (AllCompositeSets) exists:
// SetRegistry.TagAsSet must refuse to add the AllSets tag to a node
// already tagged AllCompositeSets.
func TestSetRegistryTagAsSetRejectsCompositeSetConflict(t *testing.T) {
	_, sets, composites := newCompositeSetTestFixture(t)

	composite, err := composites.NewCompositeSet()
	if err != nil {
		t.Fatalf("NewCompositeSet(): %v", err)
	}

	err = sets.TagAsSet(composite)
	if !errors.Is(err, ErrSetRepresentationConflict) {
		t.Fatalf("TagAsSet() error = %v, want %v", err, ErrSetRepresentationConflict)
	}

	if sets.IsSet(composite) {
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

	ids, err := names.BootstrapNames(FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	sets, err := NewSetRegistry(&g, ids[NameAllSets], ids[NameAllCompositeSets])
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

	set, err := composites.NewCompositeSet()
	if err != nil {
		t.Fatalf("NewCompositeSet(): %v", err)
	}

	if !g.NodeExists(set) {
		t.Fatalf("NewCompositeSet() returned NodeID %d that does not exist", set)
	}
	if !composites.IsCompositeSet(set) {
		t.Fatalf("NewCompositeSet() did not tag %d as a composite set", set)
	}

	operands, err := composites.Operands(set)
	if err != nil {
		t.Fatalf("Operands(): %v", err)
	}
	if len(operands) != 0 {
		t.Fatalf("Operands() = %v, want empty", operands)
	}

	members, err := composites.Evaluate(set)
	if err != nil {
		t.Fatalf("Evaluate(): %v", err)
	}
	if len(members) != 0 {
		t.Fatalf("Evaluate() = %v, want empty", members)
	}
}

func TestCompositeSetAddOperandScalarAdditive(t *testing.T) {
	g, _, composites := newCompositeSetTestFixture(t)

	set, err := composites.NewCompositeSet()
	if err != nil {
		t.Fatalf("NewCompositeSet(): %v", err)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for x: %v", err)
	}

	u, err := composites.AddOperand(set, x, true, false)
	if err != nil {
		t.Fatalf("AddOperand(x, additive, scalar): %v", err)
	}

	target, err := composites.OperandTarget(u)
	if err != nil {
		t.Fatalf("OperandTarget(u): %v", err)
	}
	if target != x {
		t.Fatalf("OperandTarget(u) = %d, want %d", target, x)
	}

	isAdditive, err := composites.OperandIsAdditive(u)
	if err != nil {
		t.Fatalf("OperandIsAdditive(u): %v", err)
	}
	if !isAdditive {
		t.Fatal("OperandIsAdditive(u) = false, want true")
	}

	isSetOperand, err := composites.OperandIsSetOperand(u)
	if err != nil {
		t.Fatalf("OperandIsSetOperand(u): %v", err)
	}
	if isSetOperand {
		t.Fatal("OperandIsSetOperand(u) = true, want false")
	}

	got, err := composites.Evaluate(set)
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

	setA, err := sets.NewSet()
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
	if _, err := sets.Add(setA, a1); err != nil {
		t.Fatal(err)
	}
	if _, err := sets.Add(setA, a2); err != nil {
		t.Fatal(err)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	composite, err := composites.NewCompositeSet()
	if err != nil {
		t.Fatalf("NewCompositeSet(): %v", err)
	}

	// composite = (setA expanded ∪ {x}) \ {a2}
	if _, err := composites.AddOperand(composite, setA, true, true); err != nil {
		t.Fatalf("AddOperand(setA, additive, set): %v", err)
	}
	if _, err := composites.AddOperand(composite, x, true, false); err != nil {
		t.Fatalf("AddOperand(x, additive, scalar): %v", err)
	}
	if _, err := composites.AddOperand(composite, a2, false, false); err != nil {
		t.Fatalf("AddOperand(a2, subtractive, scalar): %v", err)
	}

	got, err := composites.Evaluate(composite)
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

	set, err := composites.NewCompositeSet()
	if err != nil {
		t.Fatalf("NewCompositeSet(): %v", err)
	}

	notASet, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	_, err = composites.AddOperand(set, notASet, true, true)
	if !errors.Is(err, ErrInvalidSetOperand) {
		t.Fatalf("AddOperand() error = %v, want %v", err, ErrInvalidSetOperand)
	}
}

func TestCompositeSetEvaluateResolvesNestedCompositeSetOperand(t *testing.T) {
	g, _, composites := newCompositeSetTestFixture(t)

	inner, err := composites.NewCompositeSet()
	if err != nil {
		t.Fatalf("NewCompositeSet() for inner: %v", err)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := composites.AddOperand(inner, x, true, false); err != nil {
		t.Fatalf("AddOperand(inner, x): %v", err)
	}

	outer, err := composites.NewCompositeSet()
	if err != nil {
		t.Fatalf("NewCompositeSet() for outer: %v", err)
	}
	if _, err := composites.AddOperand(outer, inner, true, true); err != nil {
		t.Fatalf("AddOperand(outer, inner, additive, set): %v", err)
	}

	got, err := composites.Evaluate(outer)
	if err != nil {
		t.Fatalf("Evaluate(outer): %v", err)
	}
	want := []NodeID{x}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Evaluate(outer) = %v, want %v", got, want)
	}
}

func TestCompositeSetEvaluateDetectsCycle(t *testing.T) {
	_, _, composites := newCompositeSetTestFixture(t)

	c1, err := composites.NewCompositeSet()
	if err != nil {
		t.Fatalf("NewCompositeSet() for c1: %v", err)
	}
	c2, err := composites.NewCompositeSet()
	if err != nil {
		t.Fatalf("NewCompositeSet() for c2: %v", err)
	}

	if _, err := composites.AddOperand(c1, c2, true, true); err != nil {
		t.Fatalf("AddOperand(c1, c2): %v", err)
	}
	if _, err := composites.AddOperand(c2, c1, true, true); err != nil {
		t.Fatalf("AddOperand(c2, c1): %v", err)
	}

	_, err = composites.Evaluate(c1)
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

	shared, err := composites.NewCompositeSet()
	if err != nil {
		t.Fatalf("NewCompositeSet() for shared: %v", err)
	}
	x, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := composites.AddOperand(shared, x, true, false); err != nil {
		t.Fatalf("AddOperand(shared, x): %v", err)
	}

	branchA, err := composites.NewCompositeSet()
	if err != nil {
		t.Fatalf("NewCompositeSet() for branchA: %v", err)
	}
	if _, err := composites.AddOperand(branchA, shared, true, true); err != nil {
		t.Fatalf("AddOperand(branchA, shared): %v", err)
	}

	branchB, err := composites.NewCompositeSet()
	if err != nil {
		t.Fatalf("NewCompositeSet() for branchB: %v", err)
	}
	if _, err := composites.AddOperand(branchB, shared, true, true); err != nil {
		t.Fatalf("AddOperand(branchB, shared): %v", err)
	}

	top, err := composites.NewCompositeSet()
	if err != nil {
		t.Fatalf("NewCompositeSet() for top: %v", err)
	}
	if _, err := composites.AddOperand(top, branchA, true, true); err != nil {
		t.Fatalf("AddOperand(top, branchA): %v", err)
	}
	if _, err := composites.AddOperand(top, branchB, true, true); err != nil {
		t.Fatalf("AddOperand(top, branchB): %v", err)
	}

	got, err := composites.Evaluate(top)
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

	set, err := composites.NewCompositeSet()
	if err != nil {
		t.Fatalf("NewCompositeSet(): %v", err)
	}
	x, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	u, err := composites.AddOperand(set, x, true, false)
	if err != nil {
		t.Fatalf("AddOperand(): %v", err)
	}

	if err := composites.RemoveOperand(set, u); err != nil {
		t.Fatalf("RemoveOperand(): %v", err)
	}

	if g.NodeExists(u) {
		t.Fatalf("descriptor %d still exists after RemoveOperand()", u)
	}

	got, err := composites.Evaluate(set)
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

	setA, err := composites.NewCompositeSet()
	if err != nil {
		t.Fatal(err)
	}
	setB, err := composites.NewCompositeSet()
	if err != nil {
		t.Fatal(err)
	}
	x, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	u, err := composites.AddOperand(setA, x, true, false)
	if err != nil {
		t.Fatal(err)
	}

	err = composites.RemoveOperand(setB, u)
	if !errors.Is(err, ErrOperandNotInCompositeSet) {
		t.Fatalf("RemoveOperand() error = %v, want %v", err, ErrOperandNotInCompositeSet)
	}
}

func TestCompositeSetDeleteCompositeSetSucceedsWhenEmpty(t *testing.T) {
	g, _, composites := newCompositeSetTestFixture(t)

	set, err := composites.NewCompositeSet()
	if err != nil {
		t.Fatal(err)
	}

	if err := composites.DeleteCompositeSet(set); err != nil {
		t.Fatalf("DeleteCompositeSet(): %v", err)
	}
	if g.NodeExists(set) {
		t.Fatalf("composite set %d still exists after successful DeleteCompositeSet()", set)
	}
}

func TestCompositeSetDeleteCompositeSetFailsIfNotEmpty(t *testing.T) {
	g, _, composites := newCompositeSetTestFixture(t)

	set, err := composites.NewCompositeSet()
	if err != nil {
		t.Fatal(err)
	}
	x, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := composites.AddOperand(set, x, true, false); err != nil {
		t.Fatal(err)
	}

	err = composites.DeleteCompositeSet(set)
	if !errors.Is(err, ErrNodeNotEmpty) {
		t.Fatalf("DeleteCompositeSet() error = %v, want %v", err, ErrNodeNotEmpty)
	}
	if !g.NodeExists(set) {
		t.Fatal("composite set disappeared despite a failed DeleteCompositeSet()")
	}
	if !composites.IsCompositeSet(set) {
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

	if _, err := composites.AddOperand(notAComposite, x, true, false); !errors.Is(err, ErrNotCompositeSet) {
		t.Fatalf("AddOperand() error = %v, want %v", err, ErrNotCompositeSet)
	}
	if _, err := composites.Operands(notAComposite); !errors.Is(err, ErrNotCompositeSet) {
		t.Fatalf("Operands() error = %v, want %v", err, ErrNotCompositeSet)
	}
	if _, err := composites.Evaluate(notAComposite); !errors.Is(err, ErrNotCompositeSet) {
		t.Fatalf("Evaluate() error = %v, want %v", err, ErrNotCompositeSet)
	}
	if err := composites.DeleteCompositeSet(notAComposite); !errors.Is(err, ErrNotCompositeSet) {
		t.Fatalf("DeleteCompositeSet() error = %v, want %v", err, ErrNotCompositeSet)
	}
	if err := composites.RemoveOperand(notAComposite, x); !errors.Is(err, ErrNotCompositeSet) {
		t.Fatalf("RemoveOperand() error = %v, want %v", err, ErrNotCompositeSet)
	}
}

// TestCompositeSetEvaluateDetectsMalformedDescriptor covers an
// out-of-band mutation giving a descriptor node both operation-kind tags
// at once, which exactlyOneTag must reject rather than guess.
func TestCompositeSetEvaluateDetectsMalformedDescriptor(t *testing.T) {
	g, _, composites := newCompositeSetTestFixture(t)

	set, err := composites.NewCompositeSet()
	if err != nil {
		t.Fatal(err)
	}
	x, err := g.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	u, err := composites.AddOperand(set, x, true, false)
	if err != nil {
		t.Fatal(err)
	}

	// Bypass AddOperand entirely, simulating an out-of-band mutation that
	// gives u a second, conflicting operation-kind tag.
	if _, err := g.AddRelationship(composites.allSubtractiveOp, u); err != nil {
		t.Fatal(err)
	}

	_, err = composites.Evaluate(set)
	if !errors.Is(err, ErrInvalidOperandDescriptor) {
		t.Fatalf("Evaluate() error = %v, want %v", err, ErrInvalidOperandDescriptor)
	}
}
