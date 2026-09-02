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
		t.Fatal("ROOT incorrectly has a virtual relationship to itself")
	}

	relationship, exists, err := r.FindRelationship(root, root)
	if err != nil {
		t.Fatalf("FindRelationship(ROOT, ROOT): %v", err)
	}

	if exists {
		t.Fatalf("FindRelationship(ROOT, ROOT) = %v, want no relationship", relationship)
	}
}

func TestRootHasNoParents(t *testing.T) {
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

	if len(got) != 0 {
		t.Fatalf("FindIncoming(ROOT) = %v, want empty", got)
	}
}

func TestRootSeesNodesCreatedAfterView(t *testing.T) {
	var g Graph

	root, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for ROOT: %v", err)
	}

	r, err := NewRootView(&g, root)
	if err != nil {
		t.Fatalf("NewRootView(): %v", err)
	}

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}

	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for b: %v", err)
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

func TestRootRelationshipsAreVirtual(t *testing.T) {
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

	if _, err := g.AddRelationship(a, b); err != nil {
		t.Fatalf("AddRelationship(a, b): %v", err)
	}

	r, err := NewRootView(&g, root)
	if err != nil {
		t.Fatalf("NewRootView(): %v", err)
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
