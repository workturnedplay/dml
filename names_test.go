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
	"strings"
	"sync"
	"testing"
)

func TestNameRegistryBindTxIsDiscardedOnRollbackAndAppliedOnCommit(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	errRollback := errors.New("rollback")

	var rolledBack NodeID

	err := g.Transact(func(tx Tx) error {
		var txErr error
		rolledBack, txErr = createNodeTx(tx)
		if txErr != nil {
			return txErr
		}
		if bindErr := names.Bind(tx, "A", rolledBack); bindErr != nil {
			return bindErr
		}

		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("Transact() error = %v, want %v", err, errRollback)
	}

	if _, ok := names.Lookup("A"); ok {
		t.Fatal("name \"A\" is bound after its transaction rolled back")
	}
	if _, ok := names.NameForNode(rolledBack); ok {
		t.Fatalf("node %d has a name after its transaction rolled back", rolledBack)
	}

	var committed NodeID

	err = g.Transact(func(tx Tx) error {
		var txErr error
		committed, txErr = createNodeTx(tx)
		if txErr != nil {
			return txErr
		}

		return names.Bind(tx, "A", committed)
	})
	if err != nil {
		t.Fatalf("Transact() error = %v", err)
	}

	if found, ok := names.Lookup("A"); !ok || found != committed {
		t.Fatalf("Lookup(\"A\") = (%d,%v), want (%d,true)", found, ok, committed)
	}
}

func TestNameRegistryCreateNamedNodeComposesAndRollsBackWithEnclosingTransaction(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)
	errOuter := errors.New("outer failure")

	var rolledBack NodeID

	err := g.Transact(func(tx Tx) error {
		var createErr error
		rolledBack, createErr = names.CreateNamedNode(tx, "A")
		if createErr != nil {
			return createErr
		}

		return errOuter
	})
	if !errors.Is(err, errOuter) {
		t.Fatalf("Transact(fail) error = %v, want %v", err, errOuter)
	}
	if _, ok := names.Lookup("A"); ok {
		t.Fatal("name \"A\" is bound after its enclosing transaction rolled back")
	}
	if mustNodeExists(t, &g, rolledBack) {
		t.Fatalf("node %d survived its enclosing transaction's rollback", rolledBack)
	}

	var committed NodeID

	err = g.Transact(func(tx Tx) error {
		var ensureErr error
		committed, ensureErr = names.EnsureNamedNode(tx, "A")
		return ensureErr
	})
	if err != nil {
		t.Fatalf("Transact(commit) error = %v", err)
	}
	if found, ok := names.Lookup("A"); !ok || found != committed {
		t.Fatalf("Lookup(\"A\") = (%d,%v), want (%d,true)", found, ok, committed)
	}
}

// TestNameRegistryLookupIsSafeWhileGraphActorBindsAndUnbinds runs readers
// hammering Lookup/NameForNode while writers create and unbind names
// through a GraphActor. Its value is under `go test -race`: before
// NameRegistry guarded its maps, this was a data race.
func TestNameRegistryLookupIsSafeWhileGraphActorBindsAndUnbinds(t *testing.T) {
	actor := NewGraphActor(&Graph{})
	defer actor.Close()

	names := NewNameRegistry(actor)

	const writers = 20

	nameFor := func(i int) string { return strings.Repeat("n", i+1) }

	stop := make(chan struct{})

	var readers sync.WaitGroup
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()

			for {
				select {
				case <-stop:
					return
				default:
				}

				for i := 0; i < writers; i++ {
					id, ok := names.Lookup(nameFor(i))
					if ok {
						names.NameForNode(id)
					}
				}
			}
		}()
	}

	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			name := nameFor(i)

			id, createErr := names.CreateNamedNode(actor, name)
			if createErr != nil {
				t.Errorf("CreateNamedNode(%q): %v", name, createErr)
				return
			}

			if found, ok := names.Lookup(name); !ok || found != id {
				t.Errorf("Lookup(%q) = (%d,%v), want (%d,true)", name, found, ok, id)
				return
			}

			if _, unbindErr := names.Unbind(actor, name); unbindErr != nil {
				t.Errorf("Unbind(%q): %v", name, unbindErr)
			}
		}()
	}

	wg.Wait()
	close(stop)
	readers.Wait()

	for i := 0; i < writers; i++ {
		if _, ok := names.Lookup(nameFor(i)); ok {
			t.Fatalf("name %q is still bound after Unbind()", nameFor(i))
		}
	}
}

func TestNameRegistryStagedBindingsAreVisibleInsideTheTransactionOnly(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	var first NodeID

	err := g.Transact(func(tx Tx) error {
		var createErr error
		first, createErr = names.CreateNamedNode(tx, "A")
		if createErr != nil {
			return createErr
		}

		if _, ok := names.Lookup("A"); ok {
			t.Error("Lookup() reports a binding that is not committed yet")
		}

		if _, dupErr := names.CreateNamedNode(tx, "A"); !errors.Is(dupErr, ErrNameAlreadyBound) {
			t.Errorf("second CreateNamedNode() error = %v, want %v", dupErr, ErrNameAlreadyBound)
		}

		again, ensureErr := names.EnsureNamedNode(tx, "A")
		if ensureErr != nil {
			return ensureErr
		}
		if again != first {
			t.Errorf("EnsureNamedNode() = %d, want the staged node %d", again, first)
		}

		other, otherErr := createNodeTx(tx)
		if otherErr != nil {
			return otherErr
		}
		if bindErr := names.Bind(tx, "A", other); !errors.Is(bindErr, ErrNameAlreadyBound) {
			t.Errorf("Bind(A, other) error = %v, want %v", bindErr, ErrNameAlreadyBound)
		}
		if renameErr := names.Bind(tx, "B", first); !errors.Is(renameErr, ErrNodeAlreadyNamed) {
			t.Errorf("Bind(B, first) error = %v, want %v", renameErr, ErrNodeAlreadyNamed)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("Transact() error = %v", err)
	}

	if found, ok := names.Lookup("A"); !ok || found != first {
		t.Fatalf("Lookup(\"A\") = (%d,%v), want (%d,true)", found, ok, first)
	}
	if _, ok := names.Lookup("B"); ok {
		t.Fatal("the rejected Bind bound \"B\"")
	}
	requireNoStagedNames(t, names)
}

func TestNameRegistryNestedFailureUnstagesItsBinding(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	errInner := errors.New("inner failure")

	var committed NodeID

	err := g.Transact(func(tx Tx) error {
		innerErr := tx.Transact(func(inner Tx) error {
			if _, stageErr := names.CreateNamedNode(inner, "A"); stageErr != nil {
				return stageErr
			}

			return errInner
		})
		if !errors.Is(innerErr, errInner) {
			t.Errorf("nested Transact() error = %v, want %v", innerErr, errInner)
		}

		// The name is free again after the nested rollback.
		var createErr error
		committed, createErr = names.CreateNamedNode(tx, "A")

		return createErr
	})
	if err != nil {
		t.Fatalf("Transact() error = %v", err)
	}

	if found, ok := names.Lookup("A"); !ok || found != committed {
		t.Fatalf("Lookup(\"A\") = (%d,%v), want (%d,true)", found, ok, committed)
	}
	requireNoStagedNames(t, names)
}

func TestNameRegistryDeleteRetiresNameAndRebindNeedsPurge(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	old, err := names.CreateNamedNode(&g, "A")
	if err != nil {
		t.Fatalf("CreateNamedNode(): %v", err)
	}

	var rebindErr error

	err = g.Transact(func(tx Tx) error {
		if deleteErr := names.DeleteNode(tx, old); deleteErr != nil {
			return deleteErr
		}

		// Other goroutines' view is still the committed one.
		if found, ok := names.Lookup("A"); !ok || found != old {
			t.Errorf("Lookup(\"A\") = (%d,%v) inside the transaction, want the committed (%d,true)", found, ok, old)
		}

		// Inside the transaction the name is already retired: rebinding it
		// is refused instead of silently succeeding.
		_, rebindErr = names.CreateNamedNode(tx, "A")

		return nil
	})
	if err != nil {
		t.Fatalf("Transact() error = %v", err)
	}
	if !errors.Is(rebindErr, ErrNameRetired) {
		t.Fatalf("CreateNamedNode() of a name retired in the same transaction error = %v, want %v", rebindErr, ErrNameRetired)
	}

	if _, ok := names.Lookup("A"); ok {
		t.Fatal("a retired name still resolves")
	}
	if _, ok := names.NameForNode(old); ok {
		t.Fatalf("deleted node %d still has a name", old)
	}
	requireNoStagedNames(t, names)

	if _, ensureErr := names.EnsureNamedNode(&g, "A"); !errors.Is(ensureErr, ErrNameRetired) {
		t.Fatalf("EnsureNamedNode() of a retired name error = %v, want %v", ensureErr, ErrNameRetired)
	}

	if purgeErr := names.Purge(&g, "A"); purgeErr != nil {
		t.Fatalf("Purge(): %v", purgeErr)
	}

	replacement, err := names.EnsureNamedNode(&g, "A")
	if err != nil {
		t.Fatalf("EnsureNamedNode() after Purge(): %v", err)
	}
	if replacement == old {
		t.Fatalf("the recreated name got the old NodeID %d; IDs are never reused", old)
	}
	if found, ok := names.Lookup("A"); !ok || found != replacement {
		t.Fatalf("Lookup(\"A\") = (%d,%v), want (%d,true)", found, ok, replacement)
	}
	requireNoStagedNames(t, names)

	// A rolled-back delete leaves the binding, and the overlay, intact.
	errOuter := errors.New("outer failure")

	err = g.Transact(func(tx Tx) error {
		if deleteErr := names.DeleteNode(tx, replacement); deleteErr != nil {
			return deleteErr
		}

		return errOuter
	})
	if !errors.Is(err, errOuter) {
		t.Fatalf("Transact(fail) error = %v, want %v", err, errOuter)
	}
	requireNoStagedNames(t, names)

	kept, err := names.EnsureNamedNode(&g, "A")
	if err != nil {
		t.Fatalf("EnsureNamedNode() after the rolled-back delete: %v", err)
	}
	if kept != replacement {
		t.Fatalf("EnsureNamedNode() = %d, want the surviving %d", kept, replacement)
	}
}

func TestNameRegistryCreateThenDeleteInOneTransactionLeavesNoBinding(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	var id NodeID

	err := g.Transact(func(tx Tx) error {
		var createErr error
		id, createErr = names.CreateNamedNode(tx, "T")
		if createErr != nil {
			return createErr
		}

		return names.DeleteNode(tx, id)
	})
	if err != nil {
		t.Fatalf("Transact() error = %v", err)
	}

	if _, ok := names.Lookup("T"); ok {
		t.Fatal("name \"T\" is bound to a node deleted in the same transaction")
	}
	if mustNodeExists(t, &g, id) {
		t.Fatalf("node %d survived its own deletion", id)
	}
	requireNoStagedNames(t, names)

	if _, ensureErr := names.EnsureNamedNode(&g, "T"); !errors.Is(ensureErr, ErrNameRetired) {
		t.Fatalf("EnsureNamedNode() of a name created and deleted in one transaction error = %v, want %v", ensureErr, ErrNameRetired)
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

	id, err := names.CreateNamedNode(&g, "ROOT")
	if err != nil {
		t.Fatalf("CreateNamedNode() returned error: %v", err)
	}

	if !mustNodeExists(t, &g, id) {
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

	err := names.Bind(&g, "A", nonexistent)
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

	if err2 := names.Bind(&g, "A", a); err2 != nil {
		t.Fatalf("first Bind() returned error: %v", err2)
	}

	err = names.Bind(&g, "A", b)
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

	if err2 := names.Bind(&g, "A", id); err2 != nil {
		t.Fatalf("first Bind() returned error: %v", err2)
	}

	err = names.Bind(&g, "B", id)
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

	if err := names.Bind(&g, "A", id); err != nil {
		t.Fatalf("first Bind() returned error: %v", err)
	}

	if err := names.Bind(&g, "A", id); err != nil {
		t.Fatalf("identical second Bind() returned error: %v", err)
	}
}

func TestNameRegistryCreateNamedNodeDoesNotDuplicateName(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	first, err := names.CreateNamedNode(&g, "A")
	if err != nil {
		t.Fatalf("first CreateNamedNode() returned error: %v", err)
	}

	_, err = names.CreateNamedNode(&g, "A")
	if !errors.Is(err, ErrNameAlreadyBound) {
		t.Fatalf(
			"second CreateNamedNode() error = %v, want %v",
			err, ErrNameAlreadyBound,
		)
	}

	if !mustNodeExists(t, &g, first) {
		t.Fatalf("original named node %d disappeared", first)
	}
}

func TestNameRegistryUnbindDoesNotDeleteNode(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	id, err := names.CreateNamedNode(&g, "A")
	if err != nil {
		t.Fatalf("CreateNamedNode() returned error: %v", err)
	}

	removed, err := names.Unbind(&g, "A")
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

	if !mustNodeExists(t, &g, id) {
		t.Fatalf("Unbind() incorrectly deleted NodeID %d", id)
	}
}

func TestNameRegistryUnbindMissing(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	removed, err := names.Unbind(&g, "missing")
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

	id, err := names.CreateNamedNode(&g, "A")
	if err != nil {
		t.Fatalf("CreateNamedNode(): %v", err)
	}

	if err := names.DeleteNode(&g, id); err != nil {
		t.Fatalf("DeleteNode(%d): %v", id, err)
	}

	if mustNodeExists(t, &g, id) {
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

	a, err := names.CreateNamedNode(&g, "A")
	if err != nil {
		t.Fatalf("CreateNamedNode(): %v", err)
	}

	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	if _, err2 := g.AddRelationship(a, b); err2 != nil {
		t.Fatalf("AddRelationship(): %v", err2)
	}

	err = names.DeleteNode(&g, a)
	if !errors.Is(err, ErrNodeNotEmpty) {
		t.Fatalf("DeleteNode(%d) error = %v, want %v", a, err, ErrNodeNotEmpty)
	}

	if !mustNodeExists(t, &g, a) {
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

	if err := names.DeleteNode(&g, id); err != nil {
		t.Fatalf("DeleteNode(%d): %v", id, err)
	}

	if mustNodeExists(t, &g, id) {
		t.Fatalf("node %d still exists after DeleteNode()", id)
	}
}

func TestNameRegistryDoesNotCreateRelationships(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	a, err := names.CreateNamedNode(&g, "A")
	if err != nil {
		t.Fatalf("CreateNamedNode(\"A\") returned error: %v", err)
	}

	b, err := names.CreateNamedNode(&g, "B")
	if err != nil {
		t.Fatalf("CreateNamedNode(\"B\") returned error: %v", err)
	}

	if mustHasRelationship(t, &g, a, b) {
		t.Fatalf("name registry created unexpected relationship (%d,%d)", a, b)
	}

	if mustHasRelationship(t, &g, b, a) {
		t.Fatalf("name registry created unexpected relationship (%d,%d)", b, a)
	}
}

func TestNameRegistryEnsureNamedNodeCreatesWhenMissing(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	id, err := names.EnsureNamedNode(&g, "A")
	if err != nil {
		t.Fatalf("EnsureNamedNode() returned error: %v", err)
	}

	if !mustNodeExists(t, &g, id) {
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

	first, err := names.EnsureNamedNode(&g, "A")
	if err != nil {
		t.Fatalf("first EnsureNamedNode() returned error: %v", err)
	}

	second, err := names.EnsureNamedNode(&g, "A")
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

	if err2 := names.Bind(&g, "A", id); err2 != nil {
		t.Fatalf("Bind() returned error: %v", err2)
	}

	found, err := names.EnsureNamedNode(&g, "A")
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

// bindThenDeleteRaw binds name to a fresh node and then deletes that node
// through the graph's direct write (raw on every backend, see newTestGraph),
// bypassing NameRegistry.DeleteNode on purpose: it simulates a caller bug that
// deletes the node without coordinating with the name registry, leaving the
// binding stale.
func bindThenDeleteRaw(t *testing.T, g testGraph, names *NameRegistry, name string) {
	t.Helper()

	id := newTestNode(t, g)

	if bindErr := names.Bind(g, name, id); bindErr != nil {
		t.Fatalf("Bind(%q, %d): %v", name, id, bindErr)
	}

	if deleteErr := g.DeleteNode(id); deleteErr != nil {
		t.Fatalf("DeleteNode(%d) through the direct write: %v", id, deleteErr)
	}
}

func TestNameRegistryEnsureNamedNodeFailsOnStaleBinding(t *testing.T) {
	g := newTestGraph(t)
	names := NewNameRegistry(g)

	bindThenDeleteRaw(t, g, names, "A")

	_, err := names.EnsureNamedNode(g, "A")
	if !errors.Is(err, ErrNameBoundToDeletedNode) {
		t.Fatalf("EnsureNamedNode() error = %v, want %v", err, ErrNameBoundToDeletedNode)
	}
}

func TestNameRegistryCreateNamedNodeFailsOnStaleBinding(t *testing.T) {
	g := newTestGraph(t)
	names := NewNameRegistry(g)

	bindThenDeleteRaw(t, g, names, "A")

	_, err := names.CreateNamedNode(g, "A")
	if !errors.Is(err, ErrNameBoundToDeletedNode) {
		t.Fatalf("CreateNamedNode() error = %v, want %v", err, ErrNameBoundToDeletedNode)
	}
}

func TestNameRegistryBindFailsOnStaleBindingToDifferentNode(t *testing.T) {
	g := newTestGraph(t)
	names := NewNameRegistry(g)

	bindThenDeleteRaw(t, g, names, "A")

	replacement := newTestNode(t, g)

	err := names.Bind(g, "A", replacement)
	if !errors.Is(err, ErrNameBoundToDeletedNode) {
		t.Fatalf("Bind() error = %v, want %v", err, ErrNameBoundToDeletedNode)
	}
}

// ---------------------------------------------------------------------
// Name retirement, Purge and LoadNames (theorystate.md section 103).

func TestNameRegistryRetiredNameFailsEveryWayOfAskingForIt(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	id, err := names.CreateNamedNode(&g, "A")
	if err != nil {
		t.Fatalf("CreateNamedNode(): %v", err)
	}

	if delErr := names.DeleteNode(&g, id); delErr != nil {
		t.Fatalf("DeleteNode(): %v", delErr)
	}

	if _, ok := names.Lookup("A"); ok {
		t.Fatal("a retired name still resolves")
	}

	other := mustCreateNode(t, &g)

	if _, ensureErr := names.EnsureNamedNode(&g, "A"); !errors.Is(ensureErr, ErrNameRetired) {
		t.Fatalf("EnsureNamedNode() error = %v, want %v", ensureErr, ErrNameRetired)
	}
	if _, createErr := names.CreateNamedNode(&g, "A"); !errors.Is(createErr, ErrNameRetired) {
		t.Fatalf("CreateNamedNode() error = %v, want %v", createErr, ErrNameRetired)
	}
	if bindErr := names.Bind(&g, "A", other); !errors.Is(bindErr, ErrNameRetired) {
		t.Fatalf("Bind() error = %v, want %v", bindErr, ErrNameRetired)
	}
	if _, bootErr := names.BootstrapNames(&g, []string{"A"}); !errors.Is(bootErr, ErrNameRetired) {
		t.Fatalf("BootstrapNames() error = %v, want %v", bootErr, ErrNameRetired)
	}
}

func TestNameRegistryPurge(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	if purgeErr := names.Purge(&g, "missing"); !errors.Is(purgeErr, ErrNameNotFound) {
		t.Fatalf("Purge(missing) error = %v, want %v", purgeErr, ErrNameNotFound)
	}

	id, err := names.CreateNamedNode(&g, "A")
	if err != nil {
		t.Fatalf("CreateNamedNode(): %v", err)
	}

	if purgeErr := names.Purge(&g, "A"); !errors.Is(purgeErr, ErrNameNotRetired) {
		t.Fatalf("Purge(bound name) error = %v, want %v", purgeErr, ErrNameNotRetired)
	}

	if delErr := names.DeleteNode(&g, id); delErr != nil {
		t.Fatalf("DeleteNode(): %v", delErr)
	}
	if purgeErr := names.Purge(&g, "A"); purgeErr != nil {
		t.Fatalf("Purge(retired name): %v", purgeErr)
	}

	fresh, err := names.EnsureNamedNode(&g, "A")
	if err != nil {
		t.Fatalf("EnsureNamedNode() after Purge(): %v", err)
	}
	if fresh == id {
		t.Fatalf("the recreated name got the old NodeID %d", id)
	}
	if found, ok := names.Lookup("A"); !ok || found != fresh {
		t.Fatalf("Lookup(\"A\") = (%d,%v), want (%d,true)", found, ok, fresh)
	}
}

func TestNameRegistryUnbindRetiresNameButKeepsNode(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	id, err := names.CreateNamedNode(&g, "B")
	if err != nil {
		t.Fatalf("CreateNamedNode(): %v", err)
	}

	if removed, unbindErr := names.Unbind(&g, "B"); unbindErr != nil || !removed {
		t.Fatalf("Unbind() = (%v,%v), want (true,nil)", removed, unbindErr)
	}
	if !mustNodeExists(t, &g, id) {
		t.Fatal("Unbind() deleted the node")
	}

	if _, ensureErr := names.EnsureNamedNode(&g, "B"); !errors.Is(ensureErr, ErrNameRetired) {
		t.Fatalf("EnsureNamedNode() after Unbind() error = %v, want %v", ensureErr, ErrNameRetired)
	}
	if _, unbindErr := names.Unbind(&g, "B"); !errors.Is(unbindErr, ErrNameNotFound) {
		t.Fatalf("second Unbind() error = %v, want %v", unbindErr, ErrNameNotFound)
	}
}

func TestNameRegistryLoadNamesIsANoOpWithoutADurableStore(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	id, err := names.CreateNamedNode(&g, "A")
	if err != nil {
		t.Fatalf("CreateNamedNode(): %v", err)
	}

	if loadErr := names.LoadNames(&g); loadErr != nil {
		t.Fatalf("LoadNames(): %v", loadErr)
	}
	if found, ok := names.Lookup("A"); !ok || found != id {
		t.Fatalf("Lookup(\"A\") = (%d,%v) after LoadNames(), want (%d,true)", found, ok, id)
	}
}

func TestNameRegistryVerifyBindings(t *testing.T) {
	g := newTestGraph(t)
	names := NewNameRegistry(g)

	id, err := names.CreateNamedNode(g, "A")
	if err != nil {
		t.Fatalf("CreateNamedNode(): %v", err)
	}

	if verifyErr := names.VerifyBindings(g); verifyErr != nil {
		t.Fatalf("VerifyBindings() on consistent state: %v", verifyErr)
	}

	// A retired name is not checked: its node is meant to be gone.
	other, err := names.CreateNamedNode(g, "B")
	if err != nil {
		t.Fatalf("CreateNamedNode(B): %v", err)
	}

	if delErr := names.DeleteNode(g, other); delErr != nil {
		t.Fatalf("DeleteNode(B): %v", delErr)
	}

	if verifyErr := names.VerifyBindings(g); verifyErr != nil {
		t.Fatalf("VerifyBindings() with a retired name: %v", verifyErr)
	}

	// Bypass NameRegistry.DeleteNode, leaving "A" bound to a missing node.
	if delErr := g.DeleteNode(id); delErr != nil {
		t.Fatalf("raw DeleteNode(): %v", delErr)
	}

	if verifyErr := names.VerifyBindings(g); !errors.Is(verifyErr, ErrNameBoundToDeletedNode) {
		t.Fatalf("VerifyBindings() error = %v, want %v", verifyErr, ErrNameBoundToDeletedNode)
	}
}

func TestBootstrapNamesFailsOnStaleBinding(t *testing.T) {
	g := newTestGraph(t)
	names := NewNameRegistry(g)

	bindThenDeleteRaw(t, g, names, "A")

	_, err := names.BootstrapNames(g, []string{"A", "B"})
	if !errors.Is(err, ErrNameBoundToDeletedNode) {
		t.Fatalf("BootstrapNames() error = %v, want %v", err, ErrNameBoundToDeletedNode)
	}
}

func TestBootstrapNamesCreatesAllNames(t *testing.T) {
	var g Graph
	names := NewNameRegistry(&g)

	ids, err := names.BootstrapNames(&g, []string{"A", "B", "C"})
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

		if !mustNodeExists(t, &g, id) {
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

	first, err := names.BootstrapNames(&g, []string{"A", "B"})
	if err != nil {
		t.Fatalf("first BootstrapNames() returned error: %v", err)
	}

	second, err := names.BootstrapNames(&g, []string{"A", "B"})
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

	first, err := names.BootstrapNames(&g, []string{"A", "B"})
	if err != nil {
		t.Fatalf("first BootstrapNames() returned error: %v", err)
	}

	second, err := names.BootstrapNames(&g, []string{"B", "C"})
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

	ids, err := names.BootstrapNames(&g, []string{"A", "A", "A"})
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

	ids, err := names.BootstrapNames(&g, FoundationalNames)
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

	if !mustHasRelationship(t, &g, allPointers, p) {
		t.Fatal("p is not tagged as Pointer-kind via (AllPointers, p)")
	}
}

func TestBoltGraphNamesPersistAcrossReopen(t *testing.T) {
	path := boltTestPath(t)
	first := openBoltTestGraph(t, path)
	firstNames := NewNameRegistry(first)

	ids, err := firstNames.BootstrapNames(first, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	nodeCount := len(mustFindNodes(t, first))

	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("Close(): %v", closeErr)
	}

	second := openBoltTestGraph(t, path)
	secondNames := NewNameRegistry(second)

	if _, ok := secondNames.Lookup(NameAllPointers); ok {
		t.Fatal("a fresh registry knows a name before LoadNames()")
	}

	if loadErr := secondNames.LoadNames(second); loadErr != nil {
		t.Fatalf("LoadNames(): %v", loadErr)
	}

	for name, id := range ids {
		if found, ok := secondNames.Lookup(name); !ok || found != id {
			t.Fatalf("Lookup(%q) = (%d,%v), want (%d,true)", name, found, ok, id)
		}
		if got, ok := secondNames.NameForNode(id); !ok || got != name {
			t.Fatalf("NameForNode(%d) = (%q,%v), want (%q,true)", id, got, ok, name)
		}
	}

	again, err := secondNames.BootstrapNames(second, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames() after reopen: %v", err)
	}
	if !reflect.DeepEqual(again, ids) {
		t.Fatalf("BootstrapNames() after reopen = %v, want the original %v", again, ids)
	}
	if got := len(mustFindNodes(t, second)); got != nodeCount {
		t.Fatalf("BootstrapNames() after reopen changed the node count from %d to %d", nodeCount, got)
	}
}

func TestBoltGraphForgettingLoadNamesIsDetected(t *testing.T) {
	path := boltTestPath(t)
	first := openBoltTestGraph(t, path)

	if _, err := NewNameRegistry(first).EnsureNamedNode(first, "A"); err != nil {
		t.Fatalf("EnsureNamedNode(): %v", err)
	}

	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("Close(): %v", closeErr)
	}

	second := openBoltTestGraph(t, path)

	_, err := NewNameRegistry(second).EnsureNamedNode(second, "A")
	if !errors.Is(err, ErrNamesNotLoaded) {
		t.Fatalf("EnsureNamedNode() without LoadNames() error = %v, want %v", err, ErrNamesNotLoaded)
	}

	if nodes := mustFindNodes(t, second); len(nodes) != 1 {
		t.Fatalf("FindNodes() = %v, want only the original node (the failed Ensure must roll back)", nodes)
	}
}

// ---------------------------------------------------------------------
// Names on BoltGraph.

func TestBoltGraphRetiredNamePersistsAndPurgeAllowsRecreation(t *testing.T) {
	path := boltTestPath(t)
	first := openBoltTestGraph(t, path)
	firstNames := NewNameRegistry(first)

	old, err := firstNames.CreateNamedNode(first, "A")
	if err != nil {
		t.Fatalf("CreateNamedNode(): %v", err)
	}
	if delErr := firstNames.DeleteNode(first, old); delErr != nil {
		t.Fatalf("DeleteNode(): %v", delErr)
	}

	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("Close(): %v", closeErr)
	}

	second := openBoltTestGraph(t, path)
	secondNames := NewNameRegistry(second)

	if loadErr := secondNames.LoadNames(second); loadErr != nil {
		t.Fatalf("LoadNames(): %v", loadErr)
	}
	if _, ok := secondNames.Lookup("A"); ok {
		t.Fatal("a retired name resolves after a restart")
	}

	// This is the startup failure of theorystate.md section 103: the code
	// still asks for a name the store has retired.
	if _, bootErr := secondNames.BootstrapNames(second, []string{"A"}); !errors.Is(bootErr, ErrNameRetired) {
		t.Fatalf("BootstrapNames() error = %v, want %v", bootErr, ErrNameRetired)
	}

	if purgeErr := secondNames.Purge(second, "A"); purgeErr != nil {
		t.Fatalf("Purge(): %v", purgeErr)
	}

	fresh, err := secondNames.EnsureNamedNode(second, "A")
	if err != nil {
		t.Fatalf("EnsureNamedNode() after Purge(): %v", err)
	}
	if fresh <= old {
		t.Fatalf("recreated NodeID = %d, want greater than the old %d", fresh, old)
	}

	if closeErr := second.Close(); closeErr != nil {
		t.Fatalf("Close(): %v", closeErr)
	}

	third := openBoltTestGraph(t, path)
	thirdNames := NewNameRegistry(third)

	if loadErr := thirdNames.LoadNames(third); loadErr != nil {
		t.Fatalf("LoadNames() after the purge: %v", loadErr)
	}
	if found, ok := thirdNames.Lookup("A"); !ok || found != fresh {
		t.Fatalf("Lookup(\"A\") = (%d,%v), want (%d,true)", found, ok, fresh)
	}
}

// TestBoltGraphNameRecordsFollowTransactionAndSavepointOutcomes checks
// that a name record commits, or is undone, exactly with the node it names:
// an aborted transaction leaves none, and a failed nested transaction
// undoes its own record (the savepoint undo log) but not its parent's.
func TestBoltGraphNameRecordsFollowTransactionAndSavepointOutcomes(t *testing.T) {
	path := boltTestPath(t)
	first := openBoltTestGraph(t, path)
	names := NewNameRegistry(first)

	errOuter := errors.New("outer failure")
	errInner := errors.New("inner failure")

	err := first.Transact(func(tx Tx) error {
		if _, createErr := names.CreateNamedNode(tx, "aborted"); createErr != nil {
			return createErr
		}

		return errOuter
	})
	if !errors.Is(err, errOuter) {
		t.Fatalf("Transact(abort) error = %v, want %v", err, errOuter)
	}

	var kept NodeID

	err = first.Transact(func(tx Tx) error {
		var createErr error
		kept, createErr = names.CreateNamedNode(tx, "kept")
		if createErr != nil {
			return createErr
		}

		innerErr := tx.Transact(func(inner Tx) error {
			if _, nestedErr := names.CreateNamedNode(inner, "dropped"); nestedErr != nil {
				return nestedErr
			}

			return errInner
		})
		if !errors.Is(innerErr, errInner) {
			t.Errorf("nested Transact() error = %v, want %v", innerErr, errInner)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("Transact() error = %v", err)
	}

	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("Close(): %v", closeErr)
	}

	second := openBoltTestGraph(t, path)
	secondNames := NewNameRegistry(second)

	if loadErr := secondNames.LoadNames(second); loadErr != nil {
		t.Fatalf("LoadNames(): %v", loadErr)
	}

	if found, ok := secondNames.Lookup("kept"); !ok || found != kept {
		t.Fatalf("Lookup(\"kept\") = (%d,%v), want (%d,true)", found, ok, kept)
	}
	for _, name := range []string{"aborted", "dropped"} {
		if _, ok := secondNames.Lookup(name); ok {
			t.Fatalf("the store holds a record for %q, which was rolled back", name)
		}
	}
}

// TestBoltGraphNamesThroughRootGraphPersist checks that the ROOT layer
// forwards the name store: without rootTx.nameRecords, names created
// through a RootGraph would silently not be persisted.
func TestBoltGraphNamesThroughRootGraphPersist(t *testing.T) {
	path := boltTestPath(t)
	first := openBoltTestGraph(t, path)

	root := mustCreateNode(t, first)

	rootGraph, err := NewRootGraph(first, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	id, err := NewNameRegistry(rootGraph).CreateNamedNode(rootGraph, "X")
	if err != nil {
		t.Fatalf("CreateNamedNode() through the ROOT layer: %v", err)
	}

	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("Close(): %v", closeErr)
	}

	second := openBoltTestGraph(t, path)
	secondNames := NewNameRegistry(second)

	if loadErr := secondNames.LoadNames(second); loadErr != nil {
		t.Fatalf("LoadNames(): %v", loadErr)
	}
	if found, ok := secondNames.Lookup("X"); !ok || found != id {
		t.Fatalf("Lookup(\"X\") = (%d,%v), want (%d,true)", found, ok, id)
	}
}

// requireNoStagedNames checks that no transaction's staged overlay is
// left behind in names.
func requireNoStagedNames(t *testing.T, names *NameRegistry) {
	t.Helper()

	if len(names.pending) != 0 {
		t.Fatalf("staged name overlay not empty: %v", names.pending)
	}
}

// TestGraphActorConcurrentCreateNamedNodeSameNameBindsExactlyOnce checks
// that the lookup, node creation and binding are one atomic step: of many
// goroutines creating the same name, exactly one wins, the rest get
// ErrNameAlreadyBound, and only one node is left in the graph.
func TestGraphActorConcurrentCreateNamedNodeSameNameBindsExactlyOnce(t *testing.T) {
	actor := NewGraphActor(&Graph{})
	defer actor.Close()

	names := NewNameRegistry(actor)

	const goroutines = 50

	var wg sync.WaitGroup
	ids := make([]NodeID, goroutines)
	errs := make([]error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ids[i], errs[i] = names.CreateNamedNode(actor, "contended")
		}()
	}

	wg.Wait()

	winners := 0
	winner := NodeID(0)

	for i, createErr := range errs {
		switch {
		case createErr == nil:
			winners++
			winner = ids[i]
		case !errors.Is(createErr, ErrNameAlreadyBound):
			t.Fatalf("goroutine %d: CreateNamedNode() error = %v, want nil or %v", i, createErr, ErrNameAlreadyBound)
		}
	}

	if winners != 1 {
		t.Fatalf("%d goroutines created the name, want exactly 1", winners)
	}

	bound, ok := names.Lookup("contended")
	if !ok || bound != winner {
		t.Fatalf("Lookup(\"contended\") = (%d,%v), want (%d,true)", bound, ok, winner)
	}

	if nodes := mustFindNodes(t, actor); len(nodes) != 1 {
		t.Fatalf("FindNodes() = %v, want exactly the winning node (losers must roll back)", nodes)
	}
}
