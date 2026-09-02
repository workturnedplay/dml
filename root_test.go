package wtw

import (
	"errors"
	"reflect"
	"testing"
)

func TestNewRootViewRequiresExistingNode(t *testing.T) {
	var g Graph

	const root NodeID = 999

	_, err := NewRootView(&g, root)
	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("NewRootView() error = %v, want %v", err, ErrNodeNotFound)
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

	r, err := NewRootView(&g, root)
	if err != nil {
		t.Fatalf("NewRootView(): %v", err)
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

	r, err := NewRootView(&g, root)
	if err != nil {
		t.Fatalf("NewRootView(): %v", err)
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

	r, err := NewRootView(&g, root)
	if err != nil {
		t.Fatalf("NewRootView(): %v", err)
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

	r, err := NewRootView(&g, root)
	if err != nil {
		t.Fatalf("NewRootView(): %v", err)
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

	r, err := NewRootView(&g, root)
	if err != nil {
		t.Fatalf("NewRootView(): %v", err)
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

	r, err := NewRootView(&g, root)
	if err != nil {
		t.Fatalf("NewRootView(): %v", err)
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

	r, err := NewRootView(&g, root)
	if err != nil {
		t.Fatalf("NewRootView(): %v", err)
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

	r, err := NewRootView(&g, root)
	if err != nil {
		t.Fatalf("NewRootView(): %v", err)
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

	r, err := NewRootView(&g, root)
	if err != nil {
		t.Fatalf("NewRootView(): %v", err)
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

	r, err := NewRootView(&g, root)
	if err != nil {
		t.Fatalf("NewRootView(): %v", err)
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

	r, err := NewRootView(&g, root)
	if err != nil {
		t.Fatalf("NewRootView(): %v", err)
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

	r, err := NewRootView(&g, root)
	if err != nil {
		t.Fatalf("NewRootView(): %v", err)
	}

	err = r.DeleteNode(root)
	if !errors.Is(err, ErrNodeNotEmpty) {
		t.Fatalf("DeleteNode(ROOT) error = %v, want %v", err, ErrNodeNotEmpty)
	}

	if !r.NodeExists(root) {
		t.Fatal("ROOT disappeared after failed deletion")
	}
}
