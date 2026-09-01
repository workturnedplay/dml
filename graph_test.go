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
