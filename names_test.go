package wtw

import (
	"errors"
	"testing"
)

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
