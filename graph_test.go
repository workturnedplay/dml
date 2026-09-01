package wtw

import (
	"errors"
	"reflect"
	"testing"
)

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

	if a == 0 || b == 0 {
		t.Fatalf("CreateNode() returned reserved NodeID 0: a=%d b=%d", a, b)
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

	children, err := g.Children(a)
	if err != nil {
		t.Fatalf("Children() returned error: %v", err)
	}

	parents, err := g.Parents(a)
	if err != nil {
		t.Fatalf("Parents() returned error: %v", err)
	}

	if len(children) != 0 {
		t.Fatalf("expected no children, got %v", children)
	}

	if len(parents) != 0 {
		t.Fatalf("expected no parents, got %v", parents)
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
		t.Fatalf("(%d,%d) incorrectly exists merely because (%d,%d) exists", b, a, a, b)
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

	children, err := g.Children(a)
	if err != nil {
		t.Fatalf("Children() returned error: %v", err)
	}

	expected := []NodeID{b}
	if !reflect.DeepEqual(children, expected) {
		t.Fatalf("Children(%d) = %v, want %v", a, children, expected)
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

	children, err := g.Children(a)
	if err != nil {
		t.Fatalf("Children() returned error: %v", err)
	}

	parents, err := g.Parents(a)
	if err != nil {
		t.Fatalf("Parents() returned error: %v", err)
	}

	expected := []NodeID{a}

	if !reflect.DeepEqual(children, expected) {
		t.Fatalf("Children(%d) = %v, want %v", a, children, expected)
	}

	if !reflect.DeepEqual(parents, expected) {
		t.Fatalf("Parents(%d) = %v, want %v", a, parents, expected)
	}
}

func TestMultipleChildrenAndParents(t *testing.T) {
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

	d, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	if _, err := g.AddRelationship(a, b); err != nil {
		t.Fatalf("AddRelationship(%d,%d): %v", a, b, err)
	}
	if _, err := g.AddRelationship(a, c); err != nil {
		t.Fatalf("AddRelationship(%d,%d): %v", a, c, err)
	}
	if _, err := g.AddRelationship(d, b); err != nil {
		t.Fatalf("AddRelationship(%d,%d): %v", d, b, err)
	}
	if _, err := g.AddRelationship(d, c); err != nil {
		t.Fatalf("AddRelationship(%d,%d): %v", d, c, err)
	}

	childrenA, err := g.Children(a)
	if err != nil {
		t.Fatalf("Children(%d): %v", a, err)
	}

	expectedChildrenA := []NodeID{b, c}
	if !reflect.DeepEqual(childrenA, expectedChildrenA) {
		t.Fatalf("Children(%d) = %v, want %v", a, childrenA, expectedChildrenA)
	}

	parentsB, err := g.Parents(b)
	if err != nil {
		t.Fatalf("Parents(%d): %v", b, err)
	}

	expectedParentsB := []NodeID{a, d}
	if !reflect.DeepEqual(parentsB, expectedParentsB) {
		t.Fatalf("Parents(%d) = %v, want %v", b, parentsB, expectedParentsB)
	}
}

func TestParentsAndChildrenAreConsistent(t *testing.T) {
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
		t.Fatalf("AddRelationship() returned error: %v", err)
	}
	if !created {
		t.Fatal("relationship was not created")
	}

	children, err := g.Children(a)
	if err != nil {
		t.Fatalf("Children() returned error: %v", err)
	}

	parents, err := g.Parents(b)
	if err != nil {
		t.Fatalf("Parents() returned error: %v", err)
	}

	if !reflect.DeepEqual(children, []NodeID{b}) {
		t.Fatalf("Children(%d) = %v, want [%d]", a, children, b)
	}

	if !reflect.DeepEqual(parents, []NodeID{a}) {
		t.Fatalf("Parents(%d) = %v, want [%d]", b, parents, a)
	}

	if !g.HasRelationship(a, b) {
		t.Fatal("relationship disappeared despite both directional views reporting it")
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

	children, err := g.Children(a)
	if err != nil {
		t.Fatalf("Children() returned error: %v", err)
	}

	parents, err := g.Parents(b)
	if err != nil {
		t.Fatalf("Parents() returned error: %v", err)
	}

	if len(children) != 0 {
		t.Fatalf("expected no children after removal, got %v", children)
	}

	if len(parents) != 0 {
		t.Fatalf("expected no parents after removal, got %v", parents)
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
		t.Fatalf("DeleteNode(%d) error = %v, want %v", a, err, ErrNodeNotEmpty)
	}

	if !g.NodeExists(a) {
		t.Fatalf("node %d disappeared even though deletion should have failed", a)
	}

	if err := g.RemoveRelationship(a, b); err != nil {
		t.Fatalf("RemoveRelationship() returned error: %v", err)
	}

	if err := g.DeleteNode(a); err != nil {
		t.Fatalf("DeleteNode(%d) after removing relationships returned error: %v", a, err)
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
		t.Fatalf("DeleteNode(%d) error = %v, want %v", b, err, ErrNodeNotEmpty)
	}

	if !g.NodeExists(b) {
		t.Fatalf("node %d disappeared even though deletion should have failed", b)
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
			a,
			nonexistent,
			err,
			ErrNodeNotFound,
		)
	}

	if g.HasRelationship(a, nonexistent) {
		t.Fatalf("relationship to nonexistent node unexpectedly exists")
	}
}

func TestQueriesRequireExistingNode(t *testing.T) {
	var g Graph

	const nonexistent NodeID = 999999

	if _, err := g.Children(nonexistent); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Children() error = %v, want %v", err, ErrNodeNotFound)
	}

	if _, err := g.Parents(nonexistent); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Parents() error = %v, want %v", err, ErrNodeNotFound)
	}
}

func TestZeroValueGraphWorks(t *testing.T) {
	var g Graph

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() on zero-value Graph returned error: %v", err)
	}

	if !g.NodeExists(a) {
		t.Fatalf("created node %d does not exist", a)
	}
}
