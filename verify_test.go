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

	bolt "go.etcd.io/bbolt"
)

func TestVerifyAllTouchesEveryNodeInPagesOnEveryBackend(t *testing.T) {
	for _, backend := range testBackends() {
		t.Run(backend.name, func(t *testing.T) {
			api := backend.open(t)

			tag := mustCreateNode(t, api)

			const nodeCount = 10

			tagged := make([]NodeID, 0, nodeCount)

			for range nodeCount {
				id := mustCreateNode(t, api)

				if _, err := addRelationshipVia(api, tag, id); err != nil {
					t.Fatalf("AddRelationship(tag, %d): %v", id, err)
				}

				tagged = append(tagged, id)
			}

			seen := make(map[NodeID]int)

			api.RegisterChecker(Checker{
				Name: "recorder",
				Tags: []NodeID{tag},
				Check: func(_ GraphReader, touched map[NodeID]struct{}) error {
					for node := range touched {
						seen[node]++
					}

					return nil
				},
			})

			if err := VerifyAll(api, 3); err != nil {
				t.Fatalf("VerifyAll(): %v", err)
			}

			for _, id := range tagged {
				if seen[id] != 1 {
					t.Fatalf("node %d was checked %d times, want exactly once", id, seen[id])
				}
			}
		})
	}
}

func TestVerifyAllDefaultsPageSizeAndHandlesAnEmptyGraph(t *testing.T) {
	var g Graph

	if err := VerifyAll(&g, 0); err != nil {
		t.Fatalf("VerifyAll() on an empty graph: %v", err)
	}

	mustCreateNode(t, &g)

	if err := VerifyAll(&g, -3); err != nil {
		t.Fatalf("VerifyAll() with a negative page size: %v", err)
	}
}

// TestVerifyAllFindsViolationsThatBypassedCheckersOnBoltGraph writes an
// invariant violation straight into the store, as an older build or another
// tool could have, and checks that only the startup sweep finds it.
func TestVerifyAllFindsViolationsThatBypassedCheckersOnBoltGraph(t *testing.T) {
	g := newBoltTestGraph(t)

	allPointers := mustCreateNode(t, g)

	pointers, err := NewPointerRegistry(g, allPointers)
	if err != nil {
		t.Fatalf("NewPointerRegistry(): %v", err)
	}

	p, err := pointers.NewPointer(g)
	if err != nil {
		t.Fatalf("NewPointer(): %v", err)
	}

	x := mustCreateNode(t, g)
	y := mustCreateNode(t, g)

	for range 4 {
		mustCreateNode(t, g)
	}

	if setErr := pointers.SetTarget(g, p, x); setErr != nil {
		t.Fatalf("SetTarget(p, x): %v", setErr)
	}

	if verifyErr := VerifyAll(g, 2); verifyErr != nil {
		t.Fatalf("VerifyAll() on a valid store: %v", verifyErr)
	}

	rawErr := g.db.Update(func(btx *bolt.Tx) error {
		txn, openErr := newBoltTxn(btx)
		if openErr != nil {
			return openErr
		}

		// No Checker runs for a write made this way.
		_, addErr := txn.AddRelationship(p, y)

		return addErr
	})
	if rawErr != nil {
		t.Fatalf("raw write: %v", rawErr)
	}

	verifyErr := VerifyAll(g, 2)
	if !errors.Is(verifyErr, ErrLoadVerification) {
		t.Fatalf("VerifyAll() error = %v, want %v", verifyErr, ErrLoadVerification)
	}
	if !errors.Is(verifyErr, ErrTooManyPointerTargets) {
		t.Fatalf("VerifyAll() error = %v, want it to wrap %v", verifyErr, ErrTooManyPointerTargets)
	}
}

func TestVerifyAllThroughRootGraphInsideGraphActorOverBoltGraph(t *testing.T) {
	g := newBoltTestGraph(t)
	root := mustCreateNode(t, g)

	rootGraph, err := NewRootGraph(g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	actor := NewGraphActor(rootGraph)
	defer actor.Close()

	tag := mustCreateNode(t, actor)

	const nodeCount = 8

	tagged := make([]NodeID, 0, nodeCount)

	for range nodeCount {
		id := mustCreateNode(t, actor)

		if _, linkErr := addRelationshipVia(actor, tag, id); linkErr != nil {
			t.Fatalf("AddRelationship(tag, %d): %v", id, linkErr)
		}

		tagged = append(tagged, id)
	}

	seen := make(map[NodeID]int)

	actor.RegisterChecker(Checker{
		Name: "recorder",
		Tags: []NodeID{tag},
		Check: func(_ GraphReader, touched map[NodeID]struct{}) error {
			for node := range touched {
				seen[node]++
			}

			return nil
		},
	})

	if verifyErr := VerifyAll(actor, 2); verifyErr != nil {
		t.Fatalf("VerifyAll(): %v", verifyErr)
	}

	for _, id := range tagged {
		if seen[id] != 1 {
			t.Fatalf("node %d was checked %d times, want exactly once", id, seen[id])
		}
	}
}

func TestBoltGraphCheckStore(t *testing.T) {
	// putRaw writes one key straight into a bucket, bypassing everything.
	putRaw := func(bucket *bolt.Bucket, key []byte) error {
		return wrapBoltErr("test write", bucket.Put(key, boltPresent))
	}

	cases := []struct {
		name    string
		corrupt func(txn *boltTxn, a, c NodeID) error
	}{
		{
			name:    "outgoing entry without its incoming mirror",
			corrupt: func(txn *boltTxn, a, c NodeID) error { return putRaw(txn.out, boltKey16(a, c)) },
		},
		{
			name:    "incoming entry without its outgoing mirror",
			corrupt: func(txn *boltTxn, a, c NodeID) error { return putRaw(txn.in, boltKey16(a, c)) },
		},
		{
			name: "relationship to a node that does not exist",
			corrupt: func(txn *boltTxn, a, _ NodeID) error {
				const missing NodeID = 999

				if err := putRaw(txn.out, boltKey16(a, missing)); err != nil {
					return err
				}

				return putRaw(txn.in, boltKey16(missing, a))
			},
		},
		{
			name:    "ID counter behind an existing node",
			corrupt: func(txn *boltTxn, _, _ NodeID) error { return txn.setCounter(0, false) },
		},
	}

	healthy := newBoltTestGraph(t)
	if err := healthy.CheckStore(); err != nil {
		t.Fatalf("CheckStore() on an empty store: %v", err)
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newBoltTestGraph(t)

			a := mustCreateNode(t, g)
			b := mustCreateNode(t, g)
			c := mustCreateNode(t, g)

			if _, err := g.AddRelationship(a, b); err != nil {
				t.Fatalf("AddRelationship(a, b): %v", err)
			}

			if err := g.CheckStore(); err != nil {
				t.Fatalf("CheckStore() on a valid store: %v", err)
			}

			rawErr := g.db.Update(func(btx *bolt.Tx) error {
				txn, openErr := newBoltTxn(btx)
				if openErr != nil {
					return openErr
				}

				return tc.corrupt(txn, a, c)
			})
			if rawErr != nil {
				t.Fatalf("raw write: %v", rawErr)
			}

			if checkErr := g.CheckStore(); !errors.Is(checkErr, ErrStoreCorrupt) {
				t.Fatalf("CheckStore() error = %v, want %v", checkErr, ErrStoreCorrupt)
			}
		})
	}
}

// TestBoltGraphStartupSequence runs the startup order of theorystate.md
// section 104 against a store written by an earlier session: open, physical
// check, actor, LoadNames, BootstrapNames, registries, VerifyAll,
// VerifyBindings.
func TestBoltGraphStartupSequence(t *testing.T) {
	path := boltTestPath(t)

	first := openBoltTestGraph(t, path)
	firstNames := NewNameRegistry(first)

	ids, err := firstNames.BootstrapNames(first, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	firstPointers, err := NewPointerRegistry(first, ids[NameAllPointers])
	if err != nil {
		t.Fatalf("NewPointerRegistry(): %v", err)
	}

	p, err := firstPointers.NewPointer(first)
	if err != nil {
		t.Fatalf("NewPointer(): %v", err)
	}

	x := mustCreateNode(t, first)
	if setErr := firstPointers.SetTarget(first, p, x); setErr != nil {
		t.Fatalf("SetTarget(): %v", setErr)
	}

	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("Close(): %v", closeErr)
	}

	second := openBoltTestGraph(t, path)

	if checkErr := second.CheckStore(); checkErr != nil {
		t.Fatalf("CheckStore(): %v", checkErr)
	}

	actor := NewGraphActor(second)
	defer actor.Close()

	names := NewNameRegistry(actor)

	if loadErr := names.LoadNames(actor); loadErr != nil {
		t.Fatalf("LoadNames(): %v", loadErr)
	}

	again, err := names.BootstrapNames(actor, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames() after restart: %v", err)
	}
	if !reflect.DeepEqual(again, ids) {
		t.Fatalf("BootstrapNames() after restart = %v, want the original %v", again, ids)
	}

	pointers, err := NewPointerRegistry(actor, again[NameAllPointers])
	if err != nil {
		t.Fatalf("NewPointerRegistry() after restart: %v", err)
	}

	if verifyErr := VerifyAll(actor, 4); verifyErr != nil {
		t.Fatalf("VerifyAll(): %v", verifyErr)
	}
	if verifyErr := names.VerifyBindings(actor); verifyErr != nil {
		t.Fatalf("VerifyBindings(): %v", verifyErr)
	}

	target, hasTarget, err := pointers.Target(actor, p)
	if err != nil || !hasTarget || target != x {
		t.Fatalf("Target(p) after restart = (%d,%v,%v), want (%d,true,nil)", target, hasTarget, err, x)
	}
}
