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
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// ---------------------------------------------------------------------
// BoltGraph tests (theorystate.md sections 100-110). Names are persisted
// (section 109), so a test that reopens a file either calls LoadNames on a
// fresh NameRegistry or, where names are not the point, carries the
// NodeIDs it already knows across the reopen.

func TestBoltGraphBasicOperations(t *testing.T) {
	g := newBoltTestGraph(t)

	a := mustCreateNode(t, g)
	b := mustCreateNode(t, g)
	if a != 0 || b != 1 {
		t.Fatalf("first two NodeIDs = %d, %d, want 0, 1", a, b)
	}

	if !mustNodeExists(t, g, a) || !mustNodeExists(t, g, b) {
		t.Fatal("created nodes do not both exist")
	}

	created, err := g.AddRelationship(a, b)
	if err != nil {
		t.Fatalf("AddRelationship(a,b): %v", err)
	}
	if !created {
		t.Fatal("AddRelationship(a,b) reported that nothing was created")
	}

	again, err := g.AddRelationship(a, b)
	if err != nil {
		t.Fatalf("second AddRelationship(a,b): %v", err)
	}
	if again {
		t.Fatal("second AddRelationship(a,b) reported creating a duplicate")
	}

	if !mustHasRelationship(t, g, a, b) || mustHasRelationship(t, g, b, a) {
		t.Fatal("relationship direction is wrong")
	}

	want := Relationship{From: a, To: b}

	relationship, exists, err := g.FindRelationship(a, b)
	if err != nil || !exists || relationship != want {
		t.Fatalf("FindRelationship(a,b) = (%v,%v,%v), want (%v,true,nil)", relationship, exists, err, want)
	}

	if _, missing, findErr := g.FindRelationship(b, a); findErr != nil || missing {
		t.Fatalf("FindRelationship(b,a) = (%v,%v), want (false,nil)", missing, findErr)
	}

	outgoing, err := g.FindOutgoing(a)
	if err != nil || !reflect.DeepEqual(outgoing, []Relationship{want}) {
		t.Fatalf("FindOutgoing(a) = (%v,%v), want (%v,nil)", outgoing, err, []Relationship{want})
	}

	incoming, err := g.FindIncoming(b)
	if err != nil || !reflect.DeepEqual(incoming, []Relationship{want}) {
		t.Fatalf("FindIncoming(b) = (%v,%v), want (%v,nil)", incoming, err, []Relationship{want})
	}

	if all := mustFindRelationships(t, g); !reflect.DeepEqual(all, []Relationship{want}) {
		t.Fatalf("FindRelationships() = %v, want %v", all, []Relationship{want})
	}

	const nonexistent NodeID = 999999

	if _, findErr := g.FindOutgoing(nonexistent); !errors.Is(findErr, ErrNodeNotFound) {
		t.Fatalf("FindOutgoing(nonexistent) error = %v, want %v", findErr, ErrNodeNotFound)
	}
	if _, addErr := g.AddRelationship(a, nonexistent); !errors.Is(addErr, ErrNodeNotFound) {
		t.Fatalf("AddRelationship(a, nonexistent) error = %v, want %v", addErr, ErrNodeNotFound)
	}

	if deleteErr := g.DeleteNode(a); !errors.Is(deleteErr, ErrNodeNotEmpty) {
		t.Fatalf("DeleteNode(a) error = %v, want %v", deleteErr, ErrNodeNotEmpty)
	}
	if deleteErr := g.DeleteNode(b); !errors.Is(deleteErr, ErrNodeNotEmpty) {
		t.Fatalf("DeleteNode(b) error = %v, want %v (incoming relationship)", deleteErr, ErrNodeNotEmpty)
	}

	removed, err := g.RemoveRelationship(a, b)
	if err != nil || !removed {
		t.Fatalf("RemoveRelationship(a,b) = (%v,%v), want (true,nil)", removed, err)
	}

	// A self-relationship counts as both outgoing and incoming.
	c := mustCreateNode(t, g)
	if _, selfErr := g.AddRelationship(c, c); selfErr != nil {
		t.Fatalf("AddRelationship(c,c): %v", selfErr)
	}
	if deleteErr := g.DeleteNode(c); !errors.Is(deleteErr, ErrNodeNotEmpty) {
		t.Fatalf("DeleteNode(c) error = %v, want %v", deleteErr, ErrNodeNotEmpty)
	}
	if _, selfErr := g.RemoveRelationship(c, c); selfErr != nil {
		t.Fatalf("RemoveRelationship(c,c): %v", selfErr)
	}
	if deleteErr := g.DeleteNode(c); deleteErr != nil {
		t.Fatalf("DeleteNode(c) after removing its relationship: %v", deleteErr)
	}

	if nodes := mustFindNodes(t, g); !reflect.DeepEqual(nodes, []NodeID{a, b}) {
		t.Fatalf("FindNodes() = %v, want %v", nodes, []NodeID{a, b})
	}
}

// TestBoltGraphPersistsGraphAndNeverReusesIDsAcrossReopen is the
// persistence guarantee this backend exists for: committed nodes and
// relationships survive a close and reopen, and the ID counter is
// persisted with them, so even the highest deleted ID is never reissued.
func TestBoltGraphPersistsGraphAndNeverReusesIDsAcrossReopen(t *testing.T) {
	path := boltTestPath(t)
	first := openBoltTestGraph(t, path)

	a := mustCreateNode(t, first)
	b := mustCreateNode(t, first)
	c := mustCreateNode(t, first)

	if _, err := first.AddRelationship(a, b); err != nil {
		t.Fatalf("AddRelationship(a,b): %v", err)
	}
	if err := first.DeleteNode(c); err != nil {
		t.Fatalf("DeleteNode(c): %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}

	second := openBoltTestGraph(t, path)

	if nodes := mustFindNodes(t, second); !reflect.DeepEqual(nodes, []NodeID{a, b}) {
		t.Fatalf("FindNodes() after reopen = %v, want %v", nodes, []NodeID{a, b})
	}
	if !mustHasRelationship(t, second, a, b) {
		t.Fatal("relationship (a,b) did not survive the reopen")
	}
	if incoming, err := second.FindIncoming(b); err != nil || len(incoming) != 1 {
		t.Fatalf("FindIncoming(b) after reopen = (%v,%v), want the one persisted relationship", incoming, err)
	}

	d := mustCreateNode(t, second)
	if d == c || d != c+1 {
		t.Fatalf("first NodeID after reopen = %d, want %d (deleted ID %d must never be reissued)", d, c+1, c)
	}
}

// TestBoltGraphExhaustedCounterPersists covers the edge of the ID space:
// the last ID is handed out once, the exhausted flag is persisted with it,
// and a reopened store still refuses to create another node.
func TestBoltGraphExhaustedCounterPersists(t *testing.T) {
	path := boltTestPath(t)
	first := openBoltTestGraph(t, path)

	err := first.db.Update(func(btx *bolt.Tx) error {
		txn, openErr := newBoltTxn(btx)
		if openErr != nil {
			return openErr
		}

		return txn.setCounter(^NodeID(0), false)
	})
	if err != nil {
		t.Fatalf("setting the counter: %v", err)
	}

	last := mustCreateNode(t, first)
	if last != ^NodeID(0) {
		t.Fatalf("CreateNode() = %d, want the last ID %d", last, ^NodeID(0))
	}

	if _, createErr := first.CreateNode(); !errors.Is(createErr, ErrNodeIDExhausted) {
		t.Fatalf("CreateNode() past the last ID error = %v, want %v", createErr, ErrNodeIDExhausted)
	}

	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("Close(): %v", closeErr)
	}

	second := openBoltTestGraph(t, path)

	if _, createErr := second.CreateNode(); !errors.Is(createErr, ErrNodeIDExhausted) {
		t.Fatalf("CreateNode() after reopen error = %v, want %v (exhausted flag must persist)", createErr, ErrNodeIDExhausted)
	}
	if !mustNodeExists(t, second, last) {
		t.Fatal("the last node did not survive the reopen")
	}
}

func TestBoltGraphFailedTransactLeavesNoTrace(t *testing.T) {
	g := newBoltTestGraph(t)
	errBoom := errors.New("boom")

	var id NodeID

	err := g.Transact(func(tx Tx) error {
		var createErr error
		id, createErr = createNodeTx(tx)
		if createErr != nil {
			return createErr
		}

		return errBoom
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("Transact() error = %v, want %v", err, errBoom)
	}

	if mustNodeExists(t, g, id) {
		t.Fatalf("node %d created by a failed transaction is visible", id)
	}
	if nodes := mustFindNodes(t, g); len(nodes) != 0 {
		t.Fatalf("FindNodes() = %v, want none", nodes)
	}
}

func TestBoltGraphPanicRollsBackRunsRollbackHookAndStaysUsable(t *testing.T) {
	g := newBoltTestGraph(t)

	var id NodeID

	rolledBack := false

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected the panic to propagate out of Transact()")
			}
		}()

		//nolint:errcheck // the closure panics before Transact can return
		_ = g.Transact(func(tx Tx) error {
			var createErr error
			id, createErr = createNodeTx(tx)
			if createErr != nil {
				t.Errorf("CreateNode(): %v", createErr)
			}

			tx.OnRollback(func() { rolledBack = true })

			panic("boom")
		})
	}()

	if mustNodeExists(t, g, id) {
		t.Fatalf("node %d survived a panicking transaction", id)
	}
	if !rolledBack {
		t.Fatal("the OnRollback hook did not run for a panicking transaction")
	}

	// The guard and the store are both usable again.
	mustCreateNode(t, g)
}

func TestBoltGraphReadsInsideTransactSeeUncommittedWrites(t *testing.T) {
	g := newBoltTestGraph(t)
	existing := mustCreateNode(t, g)

	err := g.Transact(func(tx Tx) error {
		created, createErr := createNodeTx(tx)
		if createErr != nil {
			return createErr
		}
		if linkErr := addRelationshipTx(tx, existing, created); linkErr != nil {
			return linkErr
		}

		existsCreated, existsErr := tx.NodeExists(created)
		if existsErr != nil {
			return wrapInterfaceErr(existsErr)
		}
		hasExistingCreated, hasErr := tx.HasRelationship(existing, created)
		if hasErr != nil {
			return wrapInterfaceErr(hasErr)
		}
		if !existsCreated || !hasExistingCreated {
			t.Error("the transaction does not see its own uncommitted writes")
		}

		outgoing, outErr := tx.FindOutgoing(existing)
		if outErr != nil {
			return wrapInterfaceErr(outErr)
		}
		if want := []Relationship{{From: existing, To: created}}; !reflect.DeepEqual(outgoing, want) {
			t.Errorf("tx.FindOutgoing() = %v, want %v", outgoing, want)
		}

		incoming, inErr := tx.FindIncoming(created)
		if inErr != nil {
			return wrapInterfaceErr(inErr)
		}
		if want := []Relationship{{From: existing, To: created}}; !reflect.DeepEqual(incoming, want) {
			t.Errorf("tx.FindIncoming() = %v, want %v", incoming, want)
		}

		nodes, nodesErr := tx.FindNodes()
		if nodesErr != nil {
			return wrapInterfaceErr(nodesErr)
		}
		if !reflect.DeepEqual(nodes, []NodeID{existing, created}) {
			t.Errorf("tx.FindNodes() = %v, want %v", nodes, []NodeID{existing, created})
		}

		return nil
	})
	if err != nil {
		t.Fatalf("Transact(): %v", err)
	}
}

// TestBoltGraphNestedTransactRollsBackOnlyInnerSteps is the BoltGraph
// counterpart of TestNestedTransactFailureRollsBackOnlyTheInnerSteps,
// covering every kind of mutation the savepoint undo log must reverse:
// node creation, relationship add and remove, and node deletion.
func TestBoltGraphNestedTransactRollsBackOnlyInnerSteps(t *testing.T) {
	g := newBoltTestGraph(t)
	errInner := errors.New("inner failure")

	kept := mustCreateNode(t, g)
	victim := mustCreateNode(t, g)
	other := mustCreateNode(t, g)

	if _, err := g.AddRelationship(kept, other); err != nil {
		t.Fatalf("AddRelationship(kept, other): %v", err)
	}

	var dropped NodeID

	err := g.Transact(func(tx Tx) error {
		innerErr := tx.Transact(func(inner Tx) error {
			var createErr error
			dropped, createErr = createNodeTx(inner)
			if createErr != nil {
				return createErr
			}

			if linkErr := addRelationshipTx(inner, kept, dropped); linkErr != nil {
				return linkErr
			}
			if removeErr := removeRelationshipTx(inner, kept, other); removeErr != nil {
				return removeErr
			}
			if deleteErr := deleteNodeTx(inner, victim); deleteErr != nil {
				return deleteErr
			}

			return errInner
		})
		if !errors.Is(innerErr, errInner) {
			t.Errorf("nested Transact() error = %v, want %v", innerErr, errInner)
		}

		droppedExists, droppedErr := tx.NodeExists(dropped)
		if droppedErr != nil {
			return wrapInterfaceErr(droppedErr)
		}
		if droppedExists {
			t.Error("the nested node survived its own failed transaction")
		}

		victimExists, victimErr := tx.NodeExists(victim)
		if victimErr != nil {
			return wrapInterfaceErr(victimErr)
		}
		if !victimExists {
			t.Error("the node deleted inside the failed nested transaction was not restored")
		}

		keptOtherLinked, linkedErr := tx.HasRelationship(kept, other)
		if linkedErr != nil {
			return wrapInterfaceErr(linkedErr)
		}
		if !keptOtherLinked {
			t.Error("the relationship removed inside the failed nested transaction was not restored")
		}

		return nil
	})
	if err != nil {
		t.Fatalf("Transact() error = %v", err)
	}

	if mustNodeExists(t, g, dropped) || mustHasRelationship(t, g, kept, dropped) {
		t.Fatal("the nested transaction's steps were committed")
	}
	if !mustNodeExists(t, g, victim) || !mustHasRelationship(t, g, kept, other) {
		t.Fatal("the restored state was not committed")
	}
}

func TestBoltGraphCheckerSeesWritesAndDeclineLeavesNoTrace(t *testing.T) {
	g := newBoltTestGraph(t)
	tag := mustCreateNode(t, g)

	errVeto := errors.New("veto")
	veto := false
	sawTagged := false

	g.RegisterChecker(Checker{
		Name: "veto",
		Tags: []NodeID{tag},
		Check: func(view GraphReader, touched map[NodeID]struct{}) error {
			for node := range touched {
				has, hasErr := view.HasRelationship(tag, node)
				if hasErr != nil {
					return wrapInterfaceErr(hasErr)
				}
				if has {
					sawTagged = true
				}
			}

			if veto {
				return errVeto
			}

			return nil
		},
	})

	// The Checker sees the state the transaction produced, before commit.
	first := mustCreateNode(t, g)
	if _, err := g.AddRelationship(tag, first); err != nil {
		t.Fatalf("AddRelationship(tag, first): %v", err)
	}
	if !sawTagged {
		t.Fatal("the Checker did not see the uncommitted tag relationship")
	}

	// A declined commit leaves nothing behind.
	veto = true

	var second NodeID

	err := g.Transact(func(tx Tx) error {
		var createErr error
		second, createErr = createNodeTx(tx)
		if createErr != nil {
			return createErr
		}

		return addRelationshipTx(tx, tag, second)
	})
	if !errors.Is(err, errVeto) {
		t.Fatalf("Transact() error = %v, want %v", err, errVeto)
	}
	if mustNodeExists(t, g, second) || mustHasRelationship(t, g, tag, second) {
		t.Fatal("a declined transaction left a node or relationship behind")
	}

	// A transaction that touches nothing tagged does not consult it.
	untagged := mustCreateNode(t, g)
	if !mustNodeExists(t, g, untagged) {
		t.Fatal("an irrelevant transaction was declined by a Checker it does not concern")
	}
}

func TestBoltGraphCommitAndRollbackHooks(t *testing.T) {
	g := newBoltTestGraph(t)
	tag := mustCreateNode(t, g)
	node := mustCreateNode(t, g)

	errBoom := errors.New("boom")
	errVeto := errors.New("veto")
	veto := false

	g.RegisterChecker(Checker{
		Name: "veto",
		Tags: []NodeID{tag},
		Check: func(_ GraphReader, _ map[NodeID]struct{}) error {
			if veto {
				return errVeto
			}

			return nil
		},
	})

	var events []string

	hooks := func(tx Tx) {
		tx.OnCommit(func() { events = append(events, "commit") })
		tx.OnRollback(func() { events = append(events, "rollback") })
	}

	if err := g.Transact(func(tx Tx) error {
		hooks(tx)
		return nil
	}); err != nil {
		t.Fatalf("Transact(commit): %v", err)
	}

	if err := g.Transact(func(tx Tx) error {
		hooks(tx)
		return errBoom
	}); !errors.Is(err, errBoom) {
		t.Fatalf("Transact(fail) error = %v, want %v", err, errBoom)
	}

	veto = true

	if err := g.Transact(func(tx Tx) error {
		hooks(tx)
		return addRelationshipTx(tx, tag, node)
	}); !errors.Is(err, errVeto) {
		t.Fatalf("Transact(veto) error = %v, want %v", err, errVeto)
	}

	if want := []string{"commit", "rollback", "rollback"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("hooks ran %v, want %v", events, want)
	}
}

// TestBoltGraphPointerRegistryPortability is the payoff of theorystate.md
// section 97 once more: PointerRegistry and NameRegistry run unchanged on
// a structurally different, disk-backed backend.
func TestBoltGraphPointerRegistryPortability(t *testing.T) {
	g := newBoltTestGraph(t)
	names := NewNameRegistry(g)

	ids, err := names.BootstrapNames(g, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	pointers, err := NewPointerRegistry(g, ids[NameAllPointers])
	if err != nil {
		t.Fatalf("NewPointerRegistry(): %v", err)
	}

	p, err := pointers.NewPointer(g)
	if err != nil {
		t.Fatalf("NewPointer(): %v", err)
	}

	x := mustCreateNode(t, g)
	y := mustCreateNode(t, g)

	if setErr := pointers.SetTarget(g, p, x); setErr != nil {
		t.Fatalf("SetTarget(p, x): %v", setErr)
	}
	if setErr := pointers.SetTarget(g, p, y); setErr != nil {
		t.Fatalf("SetTarget(p, y): %v", setErr)
	}

	if mustHasRelationship(t, g, p, x) {
		t.Fatal("old target relationship survived a replacement SetTarget()")
	}

	target, hasTarget, err := pointers.Target(g, p)
	if err != nil || !hasTarget || target != y {
		t.Fatalf("Target(p) = (%d,%v,%v), want (%d,true,nil)", target, hasTarget, err, y)
	}

	// The registry's commit-time Checker guards this backend too: a
	// second target added inside a transaction is declined.
	badErr := g.Transact(func(tx Tx) error {
		return addRelationshipTx(tx, p, x)
	})
	if !errors.Is(badErr, ErrTooManyPointerTargets) {
		t.Fatalf("adding a second target error = %v, want %v", badErr, ErrTooManyPointerTargets)
	}
}

// TestBoltGraphPointerStateSurvivesReopen checks that graph structure
// written through a registry is still there, and still guarded by a freshly
// constructed registry's Checker, after a reopen. NameRegistry bindings do
// persist (TestBoltGraphNamesPersistAcrossReopen), but names are not the
// point here, so the tag's NodeID is simply carried across by the test.
func TestBoltGraphPointerStateSurvivesReopen(t *testing.T) {
	path := boltTestPath(t)
	first := openBoltTestGraph(t, path)

	allPointers := mustCreateNode(t, first)

	pointers, err := NewPointerRegistry(first, allPointers)
	if err != nil {
		t.Fatalf("NewPointerRegistry(): %v", err)
	}

	p, err := pointers.NewPointer(first)
	if err != nil {
		t.Fatalf("NewPointer(): %v", err)
	}

	x := mustCreateNode(t, first)
	if setErr := pointers.SetTarget(first, p, x); setErr != nil {
		t.Fatalf("SetTarget(p, x): %v", setErr)
	}

	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("Close(): %v", closeErr)
	}

	second := openBoltTestGraph(t, path)

	reopened, err := NewPointerRegistry(second, allPointers)
	if err != nil {
		t.Fatalf("NewPointerRegistry() after reopen: %v", err)
	}

	target, hasTarget, err := reopened.Target(second, p)
	if err != nil || !hasTarget || target != x {
		t.Fatalf("Target(p) after reopen = (%d,%v,%v), want (%d,true,nil)", target, hasTarget, err, x)
	}

	y := mustCreateNode(t, second)
	if setErr := reopened.SetTarget(second, p, y); setErr != nil {
		t.Fatalf("SetTarget(p, y) after reopen: %v", setErr)
	}
}

func TestGraphActorOverBoltGraphConcurrentCreateNodeProducesUniqueIDs(t *testing.T) {
	actor := NewGraphActor(newBoltTestGraph(t))
	defer actor.Close()

	const goroutines = 20

	var wg sync.WaitGroup
	ids := make([]NodeID, goroutines)
	errs := make([]error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ids[i], errs[i] = actor.CreateNode()
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

	if nodes := mustFindNodes(t, actor); len(nodes) != goroutines {
		t.Fatalf("FindNodes() has %d nodes, want %d", len(nodes), goroutines)
	}
}

// ---------------------------------------------------------------------
// Measurement: commit latency and file size (theorystate.md section 108).
// Run without -race, which distorts timing, and with -v to see the log:
//
//	go test -run TestBoltGraphReportCommitLatencyAndFileSize -v
//	go test -run "^$" -bench BoltGraphCommit -benchtime 300x

func TestBoltGraphReportCommitLatencyAndFileSize(t *testing.T) {
	if testing.Short() {
		t.Skip("measurement only; run without -short")
	}

	path := boltTestPath(t)
	g := openBoltTestGraph(t, path)

	logSize := func(label string) {
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatalf("Stat(): %v", statErr)
		}

		t.Logf("file size %-40s %10d bytes", label, info.Size())
	}

	logSize("empty store")

	const bulkNodes = 2000

	var ids []NodeID

	start := time.Now()

	err := g.Transact(func(tx Tx) error {
		ids = ids[:0] // fn may be re-run

		for i := 0; i < bulkNodes; i++ {
			id, createErr := createNodeTx(tx)
			if createErr != nil {
				return createErr
			}

			ids = append(ids, id)
		}

		for i := 1; i < len(ids); i++ {
			if linkErr := addRelationshipTx(tx, ids[i-1], ids[i]); linkErr != nil {
				return linkErr
			}

			if i >= 2 {
				if linkErr := addRelationshipTx(tx, ids[i-2], ids[i]); linkErr != nil {
					return linkErr
				}
			}
		}

		return nil
	})
	if err != nil {
		t.Fatalf("bulk Transact(): %v", err)
	}

	relationships := mustFindRelationships(t, g)
	t.Logf("one transaction: %d nodes and %d relationships in %v", len(ids), len(relationships), time.Since(start))
	logSize("after the bulk transaction")

	const commits = 200

	var total, worst time.Duration

	for i := 0; i < commits; i++ {
		begin := time.Now()

		if _, createErr := createNodeVia(g); createErr != nil {
			t.Fatalf("CreateNode(): %v", createErr)
		}

		elapsed := time.Since(begin)

		total += elapsed
		if elapsed > worst {
			worst = elapsed
		}
	}

	t.Logf("%d single-node commits: average %v, worst %v", commits, total/commits, worst)
	logSize("after the single-node commits")
}

func BenchmarkBoltGraphCommit(b *testing.B) {
	path := filepath.Join(b.TempDir(), "bench.db")

	g, openErr := OpenBoltGraph(path)
	if openErr != nil {
		b.Fatalf("OpenBoltGraph(): %v", openErr)
	}

	b.Cleanup(func() {
		if closeErr := g.Close(); closeErr != nil {
			b.Errorf("Close(): %v", closeErr)
		}
	})

	b.ReportAllocs()

	// b.Loop resets the timer on its first call and stops it when the loop
	// ends, so the setup above and the Stat below are not measured.
	for b.Loop() {
		if _, createErr := createNodeVia(g); createErr != nil {
			b.Fatalf("CreateNode(): %v", createErr)
		}
	}

	if info, statErr := os.Stat(path); statErr == nil {
		b.ReportMetric(float64(info.Size()), "file-bytes")
	}
}

// TestBoltGraphReadsAfterCloseReportStoreUnavailable checks that a failure
// of bolt's own read channel is reported as ErrGraphStoreUnavailable, as
// GraphReader documents, and not confused with a request-level error.
func TestBoltGraphReadsAfterCloseReportStoreUnavailable(t *testing.T) {
	g := newBoltTestGraph(t)
	id := mustCreateNode(t, g)

	if closeErr := g.Close(); closeErr != nil {
		t.Fatalf("Close(): %v", closeErr)
	}

	if _, err := g.NodeExists(id); !errors.Is(err, ErrGraphStoreUnavailable) {
		t.Fatalf("NodeExists() on a closed store error = %v, want %v", err, ErrGraphStoreUnavailable)
	}
	if _, err := g.FindNodes(); !errors.Is(err, ErrGraphStoreUnavailable) {
		t.Fatalf("FindNodes() on a closed store error = %v, want %v", err, ErrGraphStoreUnavailable)
	}
}

// ---------------------------------------------------------------------
// Store format version and single-open guard (theorystate.md section 112).

func readFormatForTest(t *testing.T, g *BoltGraph) (version uint64, present bool) {
	t.Helper()

	viewErr := g.db.View(func(btx *bolt.Tx) error {
		var readErr error
		version, present, readErr = readBoltFormat(btx.Bucket(boltBucketMeta))

		return readErr
	})
	if viewErr != nil {
		t.Fatalf("reading the format record: %v", viewErr)
	}

	return version, present
}

func putFormatForTest(t *testing.T, g *BoltGraph, version uint64) {
	t.Helper()

	err := g.db.Update(func(btx *bolt.Tx) error {
		return wrapBoltErr("put format", btx.Bucket(boltBucketMeta).Put(boltFormatKey, encodeBoltFormat(version)))
	})
	if err != nil {
		t.Fatalf("writing the format record: %v", err)
	}
}

func deleteFormatForTest(t *testing.T, g *BoltGraph) {
	t.Helper()

	err := g.db.Update(func(btx *bolt.Tx) error {
		return wrapBoltErr("delete format", btx.Bucket(boltBucketMeta).Delete(boltFormatKey))
	})
	if err != nil {
		t.Fatalf("deleting the format record: %v", err)
	}
}

func TestOpenBoltGraphWritesFormatVersionAndAdoptsUnversionedStore(t *testing.T) {
	path := boltTestPath(t)
	first := openBoltTestGraph(t, path)

	if version, present := readFormatForTest(t, first); !present || version != boltFormatVersion {
		t.Fatalf("format record = (%d,%v), want (%d,true) on a new store", version, present, boltFormatVersion)
	}

	node := mustCreateNode(t, first)

	// Simulate a store written before the record existed.
	deleteFormatForTest(t, first)

	if checkErr := first.CheckStore(); !errors.Is(checkErr, ErrStoreCorrupt) {
		t.Fatalf("CheckStore() without a format record error = %v, want %v", checkErr, ErrStoreCorrupt)
	}

	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("Close(): %v", closeErr)
	}

	second := openBoltTestGraph(t, path)

	if version, present := readFormatForTest(t, second); !present || version != boltFormatVersion {
		t.Fatalf("format record after adoption = (%d,%v), want (%d,true)", version, present, boltFormatVersion)
	}
	if !mustNodeExists(t, second, node) {
		t.Fatal("the adopted store lost its node")
	}
	if checkErr := second.CheckStore(); checkErr != nil {
		t.Fatalf("CheckStore() after adoption: %v", checkErr)
	}
}

func TestOpenBoltGraphRejectsUnknownFormatVersion(t *testing.T) {
	path := boltTestPath(t)
	first := openBoltTestGraph(t, path)

	putFormatForTest(t, first, boltFormatVersion+1)

	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("Close(): %v", closeErr)
	}

	if _, err := OpenBoltGraph(path); !errors.Is(err, ErrStoreFormat) {
		t.Fatalf("OpenBoltGraph() of a newer store error = %v, want %v", err, ErrStoreFormat)
	}

	// A record of the wrong size is just as loud.
	reopened, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatalf("bolt.Open(): %v", err)
	}

	rawErr := reopened.Update(func(btx *bolt.Tx) error {
		return wrapBoltErr("put short format", btx.Bucket(boltBucketMeta).Put(boltFormatKey, []byte{1}))
	})
	if closeErr := reopened.Close(); closeErr != nil {
		t.Fatalf("closing the raw handle: %v", closeErr)
	}
	if rawErr != nil {
		t.Fatalf("writing a short format record: %v", rawErr)
	}

	if _, openErr := OpenBoltGraph(path); !errors.Is(openErr, ErrStoreFormat) {
		t.Fatalf("OpenBoltGraph() with a malformed record error = %v, want %v", openErr, ErrStoreFormat)
	}
}

func TestOpenBoltGraphFailsFastWhenStoreIsAlreadyOpen(t *testing.T) {
	original := boltOpenTimeout
	boltOpenTimeout = 50 * time.Millisecond

	t.Cleanup(func() { boltOpenTimeout = original })

	path := boltTestPath(t)
	openBoltTestGraph(t, path)

	start := time.Now()

	if _, err := OpenBoltGraph(path); !errors.Is(err, ErrStoreLocked) {
		t.Fatalf("second OpenBoltGraph() error = %v, want %v", err, ErrStoreLocked)
	}

	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the second open took %v, want a fast failure", elapsed)
	}
}
