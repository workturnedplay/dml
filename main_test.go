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
