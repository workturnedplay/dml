package wtw

import (
	"errors"
	"reflect"
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

	root, err := names.CreateNamedNode("ROOT")
	if err != nil {
		t.Fatalf("CreateNamedNode() returned error: %v", err)
	}

	if !g.NodeExists(root) {
		t.Fatalf("created node %d does not exist in graph", root)
	}

	found, ok := names.Lookup("ROOT")
	if !ok {
		t.Fatal("Lookup(\"ROOT\") did not find the newly created name")
	}

	if found != root {
		t.Fatalf(
			"Lookup(\"ROOT\") = %d, want %d",
			found, root,
		)
	}
}

func TestNameRegistryCreateNamedNodeDoesNotDuplicateName(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	first, err := names.CreateNamedNode("ROOT")
	if err != nil {
		t.Fatalf("first CreateNamedNode() returned error: %v", err)
	}

	_, err = names.CreateNamedNode("ROOT")
	if !errors.Is(err, ErrNameAlreadyBound) {
		t.Fatalf(
			"second CreateNamedNode() error = %v, want %v",
			err, ErrNameAlreadyBound,
		)
	}

	other, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	if other == first {
		t.Fatalf(
			"second node unexpectedly reused first node ID %d",
			first,
		)
	}
}

func TestNameRegistryBindExistingNode(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	if err := names.Bind("A", a); err != nil {
		t.Fatalf("Bind() returned error: %v", err)
	}

	found, ok := names.Lookup("A")
	if !ok {
		t.Fatal("Lookup(\"A\") did not find bound name")
	}

	if found != a {
		t.Fatalf(
			"Lookup(\"A\") = %d, want %d",
			found, a,
		)
	}
}

func TestNameRegistryCannotBindNonexistentNode(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	const nonexistent NodeID = 12345

	err := names.Bind("A", nonexistent)
	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf(
			"Bind() error = %v, want %v",
			err, ErrNodeNotFound,
		)
	}

	if _, ok := names.Lookup("A"); ok {
		t.Fatal("name was bound despite nonexistent NodeID")
	}
}

func TestNameRegistryCannotRebindNameToDifferentNode(t *testing.T) {
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
		t.Fatalf(
			"second Bind() error = %v, want %v",
			err, ErrNameAlreadyBound,
		)
	}

	found, ok := names.Lookup("A")
	if !ok {
		t.Fatal("name disappeared after failed rebind")
	}

	if found != a {
		t.Fatalf(
			"failed rebind changed name from %d to %d",
			a, found,
		)
	}
}

func TestNameRegistrySameBindingIsIdempotent(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	if err := names.Bind("A", a); err != nil {
		t.Fatalf("first Bind() returned error: %v", err)
	}

	if err := names.Bind("A", a); err != nil {
		t.Fatalf("second identical Bind() returned error: %v", err)
	}
}

func TestNameRegistryMultipleNamesCanReferToSameNode(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	if err := names.Bind("A", a); err != nil {
		t.Fatalf("Bind(\"A\") returned error: %v", err)
	}

	if err := names.Bind("Alpha", a); err != nil {
		t.Fatalf("Bind(\"Alpha\") returned error: %v", err)
	}

	got, err := names.NamesForNode(a)
	if err != nil {
		t.Fatalf("NamesForNode() returned error: %v", err)
	}

	want := []string{"A", "Alpha"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf(
			"NamesForNode(%d) = %v, want %v",
			a, got, want,
		)
	}
}

func TestNameRegistryUnbindDoesNotDeleteNode(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	a, err := names.CreateNamedNode("A")
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

	if !g.NodeExists(a) {
		t.Fatalf(
			"Unbind() incorrectly deleted NodeID %d from graph",
			a,
		)
	}
}

func TestNameRegistryUnbindMissingName(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	removed, err := names.Unbind("missing")
	if err != nil {
		t.Fatalf("Unbind() returned error: %v", err)
	}

	if removed {
		t.Fatal("Unbind() reported removal of a nonexistent name")
	}
}

func TestNameRegistryNamesForNodeWithoutNames(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() returned error: %v", err)
	}

	got, err := names.NamesForNode(a)
	if err != nil {
		t.Fatalf("NamesForNode() returned error: %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("NamesForNode(%d) = %v, want empty", a, got)
	}
}

func TestNameRegistryNamesForNonexistentNode(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	const nonexistent NodeID = 12345

	_, err := names.NamesForNode(nonexistent)
	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf(
			"NamesForNode() error = %v, want %v",
			err, ErrNodeNotFound,
		)
	}
}

func TestNameRegistryDoesNotCreateGraphRelationships(t *testing.T) {
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
		t.Fatalf(
			"name registry unexpectedly created primitive relationship (%d,%d)",
			a, b,
		)
	}

	if g.HasRelationship(b, a) {
		t.Fatalf(
			"name registry unexpectedly created primitive relationship (%d,%d)",
			b, a,
		)
	}
}
