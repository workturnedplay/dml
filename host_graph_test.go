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
	"testing"
	"time"
)

func TestHostGraphFailsWithErrHostClosedAfterClose(t *testing.T) {
	h := openTestHost(t, boltTestPath(t), nil)
	g := h.Graph()
	id := mustCreateNode(t, g)

	if closeErr := h.Close(); closeErr != nil {
		t.Fatalf("Close(): %v", closeErr)
	}

	if _, err := g.NodeExists(id); !errors.Is(err, ErrHostClosed) {
		t.Fatalf("NodeExists() after Close error = %v, want %v", err, ErrHostClosed)
	}

	if _, err := g.HasRelationship(id, id); !errors.Is(err, ErrHostClosed) {
		t.Fatalf("HasRelationship() after Close error = %v, want %v", err, ErrHostClosed)
	}

	if _, _, err := g.FindRelationship(id, id); !errors.Is(err, ErrHostClosed) {
		t.Fatalf("FindRelationship() after Close error = %v, want %v", err, ErrHostClosed)
	}

	if _, err := g.FindOutgoing(id); !errors.Is(err, ErrHostClosed) {
		t.Fatalf("FindOutgoing() after Close error = %v, want %v", err, ErrHostClosed)
	}

	if _, err := g.FindIncoming(id); !errors.Is(err, ErrHostClosed) {
		t.Fatalf("FindIncoming() after Close error = %v, want %v", err, ErrHostClosed)
	}

	if _, err := g.FindRelationships(); !errors.Is(err, ErrHostClosed) {
		t.Fatalf("FindRelationships() after Close error = %v, want %v", err, ErrHostClosed)
	}

	if _, err := g.FindNodes(); !errors.Is(err, ErrHostClosed) {
		t.Fatalf("FindNodes() after Close error = %v, want %v", err, ErrHostClosed)
	}

	if _, err := createNodeVia(g); !errors.Is(err, ErrHostClosed) {
		t.Fatalf("a write after Close error = %v, want %v", err, ErrHostClosed)
	}

	// Registering a Checker on a closed host does nothing, and must not panic.
	g.RegisterChecker(Checker{Name: "late"})
}

// TestHostGraphCloseWaitsForATransactionInFlight: Close must not shut the
// actor down under a transaction a direct user is still running.
func TestHostGraphCloseWaitsForATransactionInFlight(t *testing.T) {
	h := openTestHost(t, boltTestPath(t), nil)

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	txDone := make(chan error, 1)

	go func() {
		txDone <- h.Graph().Transact(func(Tx) error {
			select {
			case started <- struct{}{}:
			default:
			}

			<-release

			return nil
		})
	}()

	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the transaction did not start")
	}

	closed := make(chan error, 1)

	go func() { closed <- h.Close() }()

	select {
	case closeErr := <-closed:
		t.Fatalf("Close() returned (%v) while a transaction was still running", closeErr)
	case <-time.After(100 * time.Millisecond):
	}

	close(release)

	select {
	case txErr := <-txDone:
		if txErr != nil {
			t.Fatalf("the transaction in flight failed: %v", txErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the transaction did not finish")
	}

	select {
	case closeErr := <-closed:
		if closeErr != nil {
			t.Fatalf("Close(): %v", closeErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close() did not return after the transaction finished")
	}
}

// TestHostGraphWritesWakeTheReconciler: with the resync an hour away, only
// the dirty mark can make the reconciler apply an effect for a hold taken
// straight through Host.Graph(), whether the hold is taken in a plain
// transaction or in a nested one.
func TestHostGraphWritesWakeTheReconciler(t *testing.T) {
	cases := []struct {
		name   string
		nested bool
	}{
		{name: "plain"},
		{name: "nested", nested: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := openTestHost(t, boltTestPath(t), func(cfg *HostConfig) { cfg.ResyncInterval = time.Hour })
			fw := &fakeEffect{}
			registerTestResource(t, h, "example-resource", fw)

			eventually(t, "the resource to be looked at after registration", func() bool { return fw.presentCalls() >= 1 })

			node, ok := h.names.Lookup(resourcePrefix + "example-resource")
			if !ok {
				t.Fatal("the resource name is not bound")
			}

			leases := h.Registries().Leases

			session, err := leases.NewSession(h.Graph())
			if err != nil {
				t.Fatalf("NewSession(): %v", err)
			}

			acquire := func(graph Transactor) error {
				_, acquireErr := leases.Acquire(graph, node, session)

				return acquireErr
			}

			if tc.nested {
				err = h.Graph().Transact(func(tx Tx) error {
					return wrapInterfaceErr(tx.Transact(func(inner Tx) error { return acquire(inner) }))
				})
			} else {
				err = acquire(h.Graph())
			}

			if err != nil {
				t.Fatalf("taking the hold: %v", err)
			}

			eventually(t, "the effect to be applied", func() bool {
				applies, _, present := fw.counts()

				return applies == 1 && present
			})
		})
	}
}

func TestHostGraphWritesToUnrelatedNodesDoNotWakeTheReconciler(t *testing.T) {
	h := openTestHost(t, boltTestPath(t), func(cfg *HostConfig) { cfg.ResyncInterval = time.Hour })
	fw := &fakeEffect{}
	registerTestResource(t, h, "example-resource", fw)

	eventually(t, "the resource to be looked at after registration", func() bool { return fw.presentCalls() >= 1 })

	before := fw.presentCalls()

	a := mustCreateNode(t, h.Graph())
	b := mustCreateNode(t, h.Graph())

	if _, err := addRelationshipVia(h.Graph(), a, b); err != nil {
		t.Fatalf("AddRelationship(a, b): %v", err)
	}

	time.Sleep(150 * time.Millisecond)

	if got := fw.presentCalls(); got != before {
		t.Fatalf("the resource was looked at %d times, want still %d: unrelated writes must not wake the reconciler", got, before)
	}
}

// TestHostGraphNamesPersistAcrossARestart: the wrapper must forward the name
// store, or a name created through Host.Graph() would be lost on restart.
func TestHostGraphNamesPersistAcrossARestart(t *testing.T) {
	path := boltTestPath(t)
	first := openTestHost(t, path, nil)

	id, err := first.names.EnsureNamedNode(first.Graph(), "custom-name")
	if err != nil {
		t.Fatalf("EnsureNamedNode(): %v", err)
	}

	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("Close(): %v", closeErr)
	}

	second := openTestHost(t, path, nil)

	if found, ok := second.names.Lookup("custom-name"); !ok || found != id {
		t.Fatalf("Lookup() after restart = (%d,%v), want (%d,true)", found, ok, id)
	}
}