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

	if mustHasRelationship(t, r, root, root) {
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

	if !mustHasRelationship(t, r, a, root) {
		t.Fatal("relationship (a, ROOT) is not visible")
	}

	removed, err := r.RemoveRelationship(a, root)
	if err != nil {
		t.Fatalf("RemoveRelationship(a, ROOT): %v", err)
	}

	if !removed {
		t.Fatal("RemoveRelationship(a, ROOT) reported that nothing was removed")
	}

	if mustHasRelationship(t, r, a, root) {
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

	if !mustNodeExists(t, &g, a) {
		t.Fatalf("new node %d does not exist in primitive graph", a)
	}

	if !mustHasRelationship(t, r, root, a) {
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

	if mustHasRelationship(t, &g, root, a) {
		t.Fatal("ROOT relationship was physically stored in Graph")
	}

	if !mustHasRelationship(t, r, root, a) {
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

	if mustHasRelationship(t, &g, root, a) {
		t.Fatal("AddRelationship(ROOT, a) physically stored the virtual relationship")
	}

	if !mustHasRelationship(t, r, root, a) {
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

	if !mustHasRelationship(t, r, root, a) {
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

	got := mustFindRelationships(t, r)

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

	if mustNodeExists(t, r, a) {
		t.Fatal("deleted node still exists")
	}

	if mustHasRelationship(t, r, root, a) {
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

	if !mustNodeExists(t, r, root) {
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

	got = mustFindRelationships(t, r)

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

	if !mustHasRelationship(t, &g, root, root) {
		t.Fatal("primitive graph does not contain (ROOT, ROOT)")
	}

	r, err := NewRootGraph(&g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	if mustHasRelationship(t, r, root, root) {
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

	got = mustFindRelationships(t, r)

	if len(got) != 0 {
		t.Fatalf("FindRelationships() = %v, want no relationships", got)
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
	if all := mustFindRelationships(t, actor); !reflect.DeepEqual(all, wantAll) {
		t.Fatalf("FindRelationships() = %v, want %v", all, wantAll)
	}

	c, err := actor.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for c: %v", err)
	}
	if !mustHasRelationship(t, actor, root, c) {
		t.Fatalf("new node %d is not visible as a ROOT child", c)
	}

	if err3 := actor.DeleteNode(root); !errors.Is(err3, ErrCannotDeleteRoot) {
		t.Fatalf("DeleteNode(ROOT) error = %v, want %v", err3, ErrCannotDeleteRoot)
	}

	// The overlay must also be in force inside a transaction.
	var sawVirtualInTx, createdVirtualInTx bool
	var deleteRootErr error

	err = actor.Transact(func(tx Tx) error {
		var hasErr error
		sawVirtualInTx, hasErr = tx.HasRelationship(root, a)
		if hasErr != nil {
			return wrapInterfaceErr(hasErr)
		}

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
		snapshot := mustFindRelationships(t, actor)

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
			has, hasErr := view.HasRelationship(root, x)
			if hasErr != nil {
				return wrapInterfaceErr(hasErr)
			}
			checkerSawVirtual = has
			return nil
		},
	})

	var sawVirtualInTx, createdVirtualInTx bool

	err = r.Transact(func(tx Tx) error {
		var hasErr error
		sawVirtualInTx, hasErr = tx.HasRelationship(root, x)
		if hasErr != nil {
			return wrapInterfaceErr(hasErr)
		}

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
	if mustHasRelationship(t, &g, root, x) {
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

	got := mustFindRelationships(t, r)
	want := []Relationship{{From: a, To: b}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FindRelationships() = %v, want %v", got, want)
	}

	if _, err4 := r.FindOutgoing(root); !errors.Is(err4, ErrNodeNotFound) {
		t.Fatalf("FindOutgoing(deleted ROOT) error = %v, want %v", err4, ErrNodeNotFound)
	}
}

// TestRootGraphOverStagedGraphBasicOperations confirms RootGraph
// (theorystate.md section 87b) also works unchanged over stagedGraph:
// NewRootGraph takes a GraphAPI and enumerates nodes via the interface's
// own FindNodes, with no assumption anywhere that the wrapped GraphAPI is
// *Graph specifically.
func TestRootGraphOverStagedGraphBasicOperations(t *testing.T) {
	g := newStagedGraph()

	root, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for ROOT: %v", err)
	}

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}

	r, err := NewRootGraph(g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	if !mustHasRelationship(t, r, root, a) {
		t.Fatal("a is not visible as a virtual ROOT child")
	}

	if mustHasRelationship(t, r, root, root) {
		t.Fatal("ROOT incorrectly has a relationship to itself")
	}
}

func TestRootGraphOverBoltGraphBasicOperations(t *testing.T) {
	g := newBoltTestGraph(t)

	root := mustCreateNode(t, g)
	a := mustCreateNode(t, g)

	r, err := NewRootGraph(g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	if !mustHasRelationship(t, r, root, a) {
		t.Fatal("a is not visible as a virtual ROOT child")
	}
	if mustHasRelationship(t, r, root, root) {
		t.Fatal("ROOT incorrectly has a relationship to itself")
	}
	if mustHasRelationship(t, g, root, a) {
		t.Fatal("the virtual ROOT relationship was physically stored")
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
			has, hasErr := inner.HasRelationship(root, x)
			if hasErr != nil {
				return wrapInterfaceErr(hasErr)
			}
			sawVirtual = has
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

func TestRootGraphTransactForwardsOnRollback(t *testing.T) {
	var g Graph

	root := newTestNode(t, &g)

	rootGraph, err := NewRootGraph(&g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	errBoom := errors.New("boom")
	ran := false

	err = rootGraph.Transact(func(tx Tx) error {
		tx.OnRollback(func() { ran = true })
		return errBoom
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("Transact() error = %v, want %v", err, errBoom)
	}
	if !ran {
		t.Fatal("a rollback hook registered through the ROOT overlay did not run")
	}
}

// TestRootReaderFindCommonChildrenForwardsOrReportsUnsupported: for ordinary
// nodes the overlay forwards to a backend that has the lookup and reports
// errPagingUnsupported over one that has not, and for ROOT (whose children are
// virtual) it always does; findUniqueTaggedChild gives the same answer either
// way.
func TestRootReaderFindCommonChildrenForwardsOrReportsUnsupported(t *testing.T) {
	scenarios := []struct {
		name   string
		api    testGraph
		native bool
	}{
		{name: "BoltGraph", api: newBoltTestGraph(t), native: true},
		{name: "stagedGraph", api: newStagedGraph(), native: false},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			root := mustCreateNode(t, sc.api)
			left := mustCreateNode(t, sc.api)
			right := mustCreateNode(t, sc.api)
			kid := mustCreateNode(t, sc.api)

			for _, parent := range []NodeID{left, right} {
				if _, linkErr := addRelationshipVia(sc.api, parent, kid); linkErr != nil {
					t.Fatalf("AddRelationship(%d, kid): %v", parent, linkErr)
				}
			}

			r, err := NewRootGraph(sc.api, root)
			if err != nil {
				t.Fatalf("NewRootGraph(): %v", err)
			}

			common, findErr := r.findCommonChildren(left, right, 2)

			switch {
			case sc.native && (findErr != nil || !reflect.DeepEqual(common, []NodeID{kid})):
				t.Fatalf("findCommonChildren() = (%v,%v), want ([%d],nil)", common, findErr, kid)
			case !sc.native && !errors.Is(findErr, errPagingUnsupported):
				t.Fatalf("findCommonChildren() error = %v, want %v", findErr, errPagingUnsupported)
			}

			if _, rootErr := r.findCommonChildren(root, left, 2); !errors.Is(rootErr, errPagingUnsupported) {
				t.Fatalf("findCommonChildren(ROOT, ...) error = %v, want %v", rootErr, errPagingUnsupported)
			}

			child, found, uniqueErr := findUniqueTaggedChild(r, left, right)
			if uniqueErr != nil || !found || child != kid {
				t.Fatalf("findUniqueTaggedChild() = (%d,%v,%v), want (%d,true,nil)", child, found, uniqueErr, kid)
			}
		})
	}
}
