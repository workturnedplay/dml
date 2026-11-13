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

	if !mustNodeExists(t, &g, a) {
		t.Fatalf("created node %d does not exist", a)
	}

	if !mustNodeExists(t, &g, b) {
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

	if !mustHasRelationship(t, &g, a, b) {
		t.Fatalf("expected (%d,%d) to exist", a, b)
	}

	if mustHasRelationship(t, &g, b, a) {
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

	if !mustHasRelationship(t, &g, a, a) {
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

	if _, err2 := g.AddRelationship(a, b); err2 != nil {
		t.Fatalf("AddRelationship(%d,%d): %v", a, b, err2)
	}

	if _, err3 := g.AddRelationship(a, c); err3 != nil {
		t.Fatalf("AddRelationship(%d,%d): %v", a, c, err3)
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

	if _, err2 := g.AddRelationship(a, c); err2 != nil {
		t.Fatalf("AddRelationship(%d,%d): %v", a, c, err2)
	}

	if _, err3 := g.AddRelationship(b, c); err3 != nil {
		t.Fatalf("AddRelationship(%d,%d): %v", b, c, err3)
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

	if _, err2 := g.AddRelationship(a, b); err2 != nil {
		t.Fatalf("AddRelationship(%d,%d): %v", a, b, err2)
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

	relationships := mustFindRelationships(t, &g)

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

	if _, err2 := g.AddRelationship(a, b); err2 != nil {
		t.Fatalf("AddRelationship() returned error: %v", err2)
	}

	removed, err := g.RemoveRelationship(a, b)
	if err != nil {
		t.Fatalf("RemoveRelationship() returned error: %v", err)
	}

	if !removed {
		t.Fatal("RemoveRelationship() reported that nothing was removed")
	}

	if mustHasRelationship(t, &g, a, b) {
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

	if _, err2 := g.AddRelationship(a, b); err2 != nil {
		t.Fatalf("AddRelationship() returned error: %v", err2)
	}

	err = g.DeleteNode(a)
	if !errors.Is(err, ErrNodeNotEmpty) {
		t.Fatalf(
			"DeleteNode(%d) error = %v, want %v",
			a, err, ErrNodeNotEmpty,
		)
	}

	if !mustNodeExists(t, &g, a) {
		t.Fatalf(
			"node %d disappeared even though deletion should have failed",
			a,
		)
	}

	_, err = g.RemoveRelationship(a, b)
	if err != nil {
		t.Fatalf("RemoveRelationship() returned error: %v", err)
	}

	if err3 := g.DeleteNode(a); err3 != nil {
		t.Fatalf(
			"DeleteNode(%d) after removing relationships returned error: %v",
			a, err3,
		)
	}

	if mustNodeExists(t, &g, a) {
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

	if _, err2 := g.AddRelationship(a, b); err2 != nil {
		t.Fatalf("AddRelationship() returned error: %v", err2)
	}

	err = g.DeleteNode(b)
	if !errors.Is(err, ErrNodeNotEmpty) {
		t.Fatalf(
			"DeleteNode(%d) error = %v, want %v",
			b, err, ErrNodeNotEmpty,
		)
	}

	if !mustNodeExists(t, &g, b) {
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

	if mustHasRelationship(t, &g, a, nonexistent) {
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

	if !mustNodeExists(t, &g, a) {
		t.Fatalf("created node %d does not exist", a)
	}
}

// TestConcurrentAccessGuardFiresOnDoubleAcquire exercises Graph's fail-
// fast concurrent-access guard (theorystate.md section 89b) directly and
// deterministically: acquiring it a second time before the first
// acquisition's release has run must panic immediately, exactly as
// documented on concurrentAccessGuard, rather than blocking or silently
// succeeding. This is tested against the guard type itself, not by
// racing real goroutines against a *Graph, since genuinely provoking two
// goroutines to overlap inside a guarded call is inherently
// timing-dependent and would make this test flaky; every other existing
// test in this file already exercises the "ordinary sequential and
// Transact-based usage never trips this guard" side of the contract,
// since none of them introduce genuine concurrent overlap and all
// continue to pass unmodified against the guarded implementation.
func TestConcurrentAccessGuardFiresOnDoubleAcquire(t *testing.T) {
	var g concurrentAccessGuard

	release := g.acquire()
	defer release()

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected second acquire() to panic while the guard is still held")
		}
	}()

	g.acquire()
}

// TestConcurrentAccessGuardAllowsSequentialReuse confirms the guard is
// not a one-shot latch: once released, it can be acquired again by a
// later, non-overlapping call without panicking.
func TestConcurrentAccessGuardAllowsSequentialReuse(_ *testing.T) {
	var g concurrentAccessGuard

	release := g.acquire()
	release()

	release = g.acquire()
	release()
}

func TestFindNodesReturnsSortedExistingNodes(t *testing.T) {
	var g Graph

	if got := mustFindNodes(t, &g); len(got) != 0 {
		t.Fatalf("FindNodes() on an empty graph = %v, want empty", got)
	}

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}

	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for b: %v", err)
	}

	c, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for c: %v", err)
	}

	if err2 := g.DeleteNode(b); err2 != nil {
		t.Fatalf("DeleteNode(b): %v", err2)
	}

	got := mustFindNodes(t, &g)
	want := []NodeID{a, c}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FindNodes() = %v, want %v (sorted, deleted node excluded)", got, want)
	}
}

func TestTxnFindNodesReflectsUncommittedCreatesAndRollback(t *testing.T) {
	var g Graph

	existing, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	errForcedRollback := errors.New("forced rollback")

	var created NodeID
	var inside []NodeID

	err = g.Transact(func(tx Tx) error {
		var txErr error
		created, txErr = createNodeTx(tx)
		if txErr != nil {
			return txErr
		}

		var findErr error
		inside, findErr = tx.FindNodes()
		if findErr != nil {
			return wrapInterfaceErr(findErr)
		}

		return errForcedRollback
	})
	if !errors.Is(err, errForcedRollback) {
		t.Fatalf("Transact() error = %v, want %v", err, errForcedRollback)
	}

	wantInside := []NodeID{existing, created}
	if !reflect.DeepEqual(inside, wantInside) {
		t.Fatalf("tx.FindNodes() inside the transaction = %v, want %v", inside, wantInside)
	}

	wantAfter := []NodeID{existing}
	if got := mustFindNodes(t, &g); !reflect.DeepEqual(got, wantAfter) {
		t.Fatalf("FindNodes() after rollback = %v, want %v", got, wantAfter)
	}
}

func TestTransactCommitsMutationsOnSuccess(t *testing.T) {
	var g Graph

	var a, b NodeID
	err := g.Transact(func(tx Tx) error {
		var err error
		a, err = createNodeTx(tx)
		if err != nil {
			return err
		}

		b, err = createNodeTx(tx)
		if err != nil {
			return err
		}

		return addRelationshipTx(tx, a, b)
	})
	if err != nil {
		t.Fatalf("Transact() returned error: %v", err)
	}

	if !mustNodeExists(t, &g, a) || !mustNodeExists(t, &g, b) {
		t.Fatalf("nodes %d, %d do not both exist after successful Transact()", a, b)
	}

	if !mustHasRelationship(t, &g, a, b) {
		t.Fatalf("relationship (%d,%d) missing after successful Transact()", a, b)
	}
}

func TestTransactRollsBackCreateNodeOnLaterFailure(t *testing.T) {
	var g Graph

	const nonexistent NodeID = 999999

	var id NodeID
	err := g.Transact(func(tx Tx) error {
		var err error
		id, err = createNodeTx(tx)
		if err != nil {
			return err
		}

		err = addRelationshipTx(tx, id, nonexistent)
		return err
	})

	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Transact() error = %v, want %v", err, ErrNodeNotFound)
	}

	if mustNodeExists(t, &g, id) {
		t.Fatalf("node %d still exists after its creating transaction rolled back", id)
	}
}

func TestTransactRollsBackRelationshipsInLIFOOrder(t *testing.T) {
	var g Graph

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}

	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for b: %v", err)
	}

	c, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for c: %v", err)
	}

	const nonexistent NodeID = 999999

	err = g.Transact(func(tx Tx) error {
		if err2 := addRelationshipTx(tx, a, b); err2 != nil {
			return err2
		}

		if err3 := addRelationshipTx(tx, a, c); err3 != nil {
			return err3
		}

		return addRelationshipTx(tx, a, nonexistent)
	})

	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Transact() error = %v, want %v", err, ErrNodeNotFound)
	}

	if mustHasRelationship(t, &g, a, b) {
		t.Fatalf("relationship (%d,%d) survived a rolled-back transaction", a, b)
	}

	if mustHasRelationship(t, &g, a, c) {
		t.Fatalf("relationship (%d,%d) survived a rolled-back transaction", a, c)
	}
}

func TestTransactRollsBackRemoveRelationshipOnLaterFailure(t *testing.T) {
	var g Graph

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}

	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for b: %v", err)
	}

	if _, err2 := g.AddRelationship(a, b); err2 != nil {
		t.Fatalf("AddRelationship(%d,%d): %v", a, b, err2)
	}

	const nonexistent NodeID = 999999

	err = g.Transact(func(tx Tx) error {
		if err3 := removeRelationshipTx(tx, a, b); err3 != nil {
			return err3
		}

		return addRelationshipTx(tx, a, nonexistent)
	})

	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Transact() error = %v, want %v", err, ErrNodeNotFound)
	}

	if !mustHasRelationship(t, &g, a, b) {
		t.Fatalf("relationship (%d,%d) was not restored after rollback", a, b)
	}
}

func TestTransactDoesNotUndoPreexistingRelationship(t *testing.T) {
	var g Graph

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}

	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for b: %v", err)
	}

	if _, err2 := g.AddRelationship(a, b); err2 != nil {
		t.Fatalf("AddRelationship(%d,%d): %v", a, b, err2)
	}

	const nonexistent NodeID = 999999

	err = g.Transact(func(tx Tx) error {
		// (a,b) already exists, so this call reports created == false and
		// must not schedule an undo step for a relationship this
		// transaction did not itself create.
		created, err3 := tx.AddRelationship(a, b)
		if err3 != nil {
			return wrapInterfaceErr(err3)
		}
		if created {
			t.Fatal("AddRelationship() reported creating an already-existing relationship")
		}

		return addRelationshipTx(tx, a, nonexistent)
	})

	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Transact() error = %v, want %v", err, ErrNodeNotFound)
	}

	if !mustHasRelationship(t, &g, a, b) {
		t.Fatalf("preexisting relationship (%d,%d) was incorrectly removed by rollback", a, b)
	}
}

func TestTransactRollsBackOnPanic(t *testing.T) {
	var g Graph
	var id NodeID

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic to propagate out of Transact()")
			}
		}()

		if err := g.Transact(func(tx Tx) error {
			var err error
			id, err = createNodeTx(tx)
			if err != nil {
				t.Fatalf("CreateNode(): %v", err)
			}

			panic("boom")
		}); err != nil {
			t.Fatalf("Transact() returned error: %v", err)
		}
	}()

	if mustNodeExists(t, &g, id) {
		t.Fatalf("node %d still exists after a panicking transaction", id)
	}
}

// TestCheckerPanicRollsBackAndPropagates covers a path no existing test
// does: Graph.Transact's defer/recover is registered once, before fn
// runs, so it already covers a panic from inside the commit-time Checker
// pass (runCheckers) exactly as it covers one from fn itself
// (TestTransactRollsBackOnPanic above) -- but nothing previously
// exercised that second path. A Checker whose Check function panics must
// still roll back every mutation fn made and propagate the panic to
// Transact's caller.
func TestCheckerPanicRollsBackAndPropagates(t *testing.T) {
	var g Graph

	tag, tagErr := g.CreateNode()
	if tagErr != nil {
		t.Fatalf("CreateNode() for tag: %v", tagErr)
	}

	g.RegisterChecker(Checker{
		Name: "panics",
		Tags: []NodeID{tag},
		Check: func(_ GraphReader, _ map[NodeID]struct{}) error {
			panic("checker boom")
		},
	})

	var id NodeID

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected the Checker's panic to propagate out of Transact()")
			}
		}()

		if err := g.Transact(func(tx Tx) error {
			var err error
			id, err = createNodeTx(tx)
			if err != nil {
				t.Fatalf("CreateNode(): %v", err)
			}

			return addRelationshipTx(tx, tag, id)
		}); err != nil {
			t.Fatalf("Transact() returned error: %v", err)
		}
	}()

	if mustNodeExists(t, &g, id) {
		t.Fatalf("node %d still exists after its transaction's Checker panicked", id)
	}
}

func TestTxOnCommitRunsOnlyAfterSuccessfulCommit(t *testing.T) {
	var g Graph

	var order []string

	err := g.Transact(func(tx Tx) error {
		tx.OnCommit(func() { order = append(order, "first") })
		tx.OnCommit(func() { order = append(order, "second") })
		return nil
	})
	if err != nil {
		t.Fatalf("Transact() error = %v", err)
	}
	if want := []string{"first", "second"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("hooks ran %v, want %v (registration order)", order, want)
	}

	errBoom := errors.New("boom")

	err = g.Transact(func(tx Tx) error {
		tx.OnCommit(func() { order = append(order, "after-error") })
		return errBoom
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("Transact() error = %v, want %v", err, errBoom)
	}

	tag := newTestNode(t, &g)
	node := newTestNode(t, &g)
	errVeto := errors.New("veto")

	g.RegisterChecker(Checker{
		Name: "veto",
		Tags: []NodeID{tag},
		Check: func(_ GraphReader, _ map[NodeID]struct{}) error {
			return errVeto
		},
	})

	err = g.Transact(func(tx Tx) error {
		tx.OnCommit(func() { order = append(order, "after-veto") })
		return addRelationshipTx(tx, tag, node)
	})
	if !errors.Is(err, errVeto) {
		t.Fatalf("Transact() error = %v, want %v", err, errVeto)
	}

	if want := []string{"first", "second"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("hooks ran %v, want %v (hooks of a failed or vetoed transaction must not run)", order, want)
	}
}

func TestTxOnRollbackRunsInReverseOrderAndOnlyOnRollback(t *testing.T) {
	var g Graph

	var order []string
	errBoom := errors.New("boom")

	err := g.Transact(func(tx Tx) error {
		tx.OnRollback(func() { order = append(order, "committed") })
		return nil
	})
	if err != nil {
		t.Fatalf("Transact(commit) error = %v", err)
	}
	if len(order) != 0 {
		t.Fatalf("an OnRollback hook ran for a committed transaction: %v", order)
	}

	err = g.Transact(func(tx Tx) error {
		tx.OnRollback(func() { order = append(order, "first") })

		nestedErr := tx.Transact(func(inner Tx) error {
			inner.OnRollback(func() { order = append(order, "nested") })
			return errBoom
		})
		if !errors.Is(nestedErr, errBoom) {
			t.Errorf("nested Transact() error = %v, want %v", nestedErr, errBoom)
		}

		tx.OnRollback(func() { order = append(order, "second") })

		return errBoom
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("Transact(fail) error = %v, want %v", err, errBoom)
	}

	// The nested hook runs when the nested transaction fails; the others
	// run, newest first, when the outermost one does.
	if want := []string{"nested", "second", "first"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("rollback hooks ran %v, want %v", order, want)
	}
}

func TestTxOnRollbackRunsWhenCheckerDeclinesCommit(t *testing.T) {
	var g Graph

	tag := newTestNode(t, &g)
	node := newTestNode(t, &g)
	errVeto := errors.New("veto")

	g.RegisterChecker(Checker{
		Name: "veto",
		Tags: []NodeID{tag},
		Check: func(_ GraphReader, _ map[NodeID]struct{}) error {
			return errVeto
		},
	})

	rolledBack, committed := false, false

	err := g.Transact(func(tx Tx) error {
		tx.OnRollback(func() { rolledBack = true })
		tx.OnCommit(func() { committed = true })

		return addRelationshipTx(tx, tag, node)
	})
	if !errors.Is(err, errVeto) {
		t.Fatalf("Transact() error = %v, want %v", err, errVeto)
	}
	if !rolledBack || committed {
		t.Fatalf("rolledBack=%v committed=%v, want true,false after a declined commit", rolledBack, committed)
	}
}

// TestTxTouchRunsRelevantCheckersWithoutMutation covers Tx.Touch on every
// backend that records touched sets: a touched node whose tag a Checker is
// keyed on makes that Checker run at commit, a touched node it is not keyed
// on does not, nothing is mutated, and a declining Checker fails the
// transaction (theorystate.md section 104).
func TestTxTouchRunsRelevantCheckersWithoutMutation(t *testing.T) {
	backends := testBackends()

	for _, backend := range backends {
		t.Run(backend.name, func(t *testing.T) {
			api := backend.open(t)

			tag := mustCreateNode(t, api)
			tagged := mustCreateNode(t, api)
			untagged := mustCreateNode(t, api)

			if _, err := addRelationshipVia(api, tag, tagged); err != nil {
				t.Fatalf("AddRelationship(tag, tagged): %v", err)
			}

			errVeto := errors.New("veto")
			veto := false
			runs := 0
			sawTagged := false

			api.RegisterChecker(Checker{
				Name: "probe",
				Tags: []NodeID{tag},
				Check: func(_ GraphReader, touched map[NodeID]struct{}) error {
					runs++
					_, sawTagged = touched[tagged]

					if veto {
						return errVeto
					}

					return nil
				},
			})

			relationshipsBefore := mustFindRelationships(t, api)
			nodesBefore := mustFindNodes(t, api)

			if err := api.Transact(func(tx Tx) error {
				tx.Touch(untagged)
				return nil
			}); err != nil {
				t.Fatalf("Transact(touch untagged): %v", err)
			}
			if runs != 0 {
				t.Fatalf("the Checker ran %d times for a node it is not keyed on, want 0", runs)
			}

			if err := api.Transact(func(tx Tx) error {
				tx.Touch(tagged)
				return nil
			}); err != nil {
				t.Fatalf("Transact(touch tagged): %v", err)
			}
			if runs != 1 || !sawTagged {
				t.Fatalf("runs=%d sawTagged=%v, want 1,true after touching a tagged node", runs, sawTagged)
			}

			if !reflect.DeepEqual(mustFindRelationships(t, api), relationshipsBefore) || !reflect.DeepEqual(mustFindNodes(t, api), nodesBefore) {
				t.Fatal("Touch changed the graph")
			}

			veto = true

			if err := api.Transact(func(tx Tx) error {
				tx.Touch(tagged)
				return nil
			}); !errors.Is(err, errVeto) {
				t.Fatalf("Transact(touch with veto) error = %v, want %v", err, errVeto)
			}
		})
	}
}

func TestNestedTransactCommitsWithOuter(t *testing.T) {
	var g Graph

	var a, b NodeID

	err := g.Transact(func(tx Tx) error {
		var txErr error
		a, txErr = createNodeTx(tx)
		if txErr != nil {
			return txErr
		}

		return wrapInterfaceErr(tx.Transact(func(inner Tx) error {
			var innerErr error
			b, innerErr = createNodeTx(inner)
			if innerErr != nil {
				return innerErr
			}

			return addRelationshipTx(inner, a, b)
		}))
	})
	if err != nil {
		t.Fatalf("Transact() error = %v", err)
	}

	if !mustNodeExists(t, &g, a) || !mustNodeExists(t, &g, b) || !mustHasRelationship(t, &g, a, b) {
		t.Fatalf("nodes %d, %d and relationship (%d,%d) must all exist after the outermost commit", a, b, a, b)
	}
}

func TestNestedTransactFailureRollsBackOnlyTheInnerSteps(t *testing.T) {
	var g Graph

	errInner := errors.New("inner failure")

	var kept, dropped NodeID
	var hooks []string

	err := g.Transact(func(tx Tx) error {
		var txErr error
		kept, txErr = createNodeTx(tx)
		if txErr != nil {
			return txErr
		}

		tx.OnCommit(func() { hooks = append(hooks, "outer") })

		innerErr := tx.Transact(func(inner Tx) error {
			var createErr error
			dropped, createErr = createNodeTx(inner)
			if createErr != nil {
				return createErr
			}

			if linkErr := addRelationshipTx(inner, kept, dropped); linkErr != nil {
				return linkErr
			}

			inner.OnCommit(func() { hooks = append(hooks, "inner") })

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

	if !mustNodeExists(t, &g, kept) {
		t.Fatal("the outer step was lost although only the nested transaction failed")
	}
	if mustNodeExists(t, &g, dropped) {
		t.Fatal("the nested step survived its own failed transaction")
	}
	if want := []string{"outer"}; !reflect.DeepEqual(hooks, want) {
		t.Fatalf("commit hooks ran %v, want %v (the failed nested transaction's hook must be discarded)", hooks, want)
	}
}

func TestNestedTransactSuccessIsRolledBackWithOuterFailure(t *testing.T) {
	var g Graph

	errOuter := errors.New("outer failure")

	var id NodeID

	err := g.Transact(func(tx Tx) error {
		if nestedErr := tx.Transact(func(inner Tx) error {
			var createErr error
			id, createErr = createNodeTx(inner)
			return createErr
		}); nestedErr != nil {
			return wrapInterfaceErr(nestedErr)
		}

		return errOuter
	})
	if !errors.Is(err, errOuter) {
		t.Fatalf("Transact() error = %v, want %v", err, errOuter)
	}

	if mustNodeExists(t, &g, id) {
		t.Fatalf("node %d created by a successful nested transaction survived the outer failure", id)
	}
}

func TestNestedTransactRunsCheckersOnlyAtOutermostCommit(t *testing.T) {
	var g Graph

	flag := newTestNode(t, &g)
	x := newTestNode(t, &g)
	okNode := newTestNode(t, &g)

	errUnsatisfied := errors.New("flagged node has no ok edge")
	runs := 0

	g.RegisterChecker(Checker{
		Name: "flagged-needs-ok",
		Tags: []NodeID{flag},
		Check: func(view GraphReader, touched map[NodeID]struct{}) error {
			runs++

			for node := range touched {
				hasFlag, err := view.HasRelationship(flag, node)
				if err != nil {
					return wrapInterfaceErr(err)
				}
				if !hasFlag {
					continue
				}

				hasOK, err := view.HasRelationship(node, okNode)
				if err != nil {
					return wrapInterfaceErr(err)
				}
				if !hasOK {
					return errUnsatisfied
				}
			}

			return nil
		},
	})

	// The nested step leaves the invariant violated and the outer step
	// repairs it before commit: the Checker must judge only the final
	// state, once.
	err := g.Transact(func(tx Tx) error {
		if innerErr := tx.Transact(func(inner Tx) error {
			return addRelationshipTx(inner, flag, x)
		}); innerErr != nil {
			return wrapInterfaceErr(innerErr)
		}

		return addRelationshipTx(tx, x, okNode)
	})
	if err != nil {
		t.Fatalf("Transact(repaired before commit) error = %v, want nil", err)
	}
	if runs != 1 {
		t.Fatalf("the Checker ran %d times, want exactly 1 (at the outermost commit)", runs)
	}

	// Nothing repairs this one: the nested call itself succeeds
	// (provisionally), and the outermost commit declines everything.
	y := newTestNode(t, &g)

	err = g.Transact(func(tx Tx) error {
		if innerErr := tx.Transact(func(inner Tx) error {
			return addRelationshipTx(inner, flag, y)
		}); innerErr != nil {
			return wrapInterfaceErr(innerErr)
		}

		return nil
	})
	if !errors.Is(err, errUnsatisfied) {
		t.Fatalf("Transact(unrepaired) error = %v, want %v", err, errUnsatisfied)
	}
	if mustHasRelationship(t, &g, flag, y) {
		t.Fatal("a nested step that succeeded provisionally survived the declined outermost commit")
	}
}

func TestNestedTransactPanicRollsBackToSavepointAndPropagates(t *testing.T) {
	var g Graph

	var kept, dropped NodeID

	err := g.Transact(func(tx Tx) error {
		var txErr error
		kept, txErr = createNodeTx(tx)
		if txErr != nil {
			return txErr
		}

		func() {
			defer func() {
				if r := recover(); r == nil {
					t.Error("expected the nested panic to reach the enclosing closure")
				}
			}()

			//nolint:errcheck // the nested closure panics before Transact can return
			_ = tx.Transact(func(inner Tx) error {
				var createErr error
				dropped, createErr = createNodeTx(inner)
				if createErr != nil {
					return createErr
				}

				panic("boom")
			})
		}()

		return nil
	})
	if err != nil {
		t.Fatalf("Transact() error = %v", err)
	}

	if !mustNodeExists(t, &g, kept) {
		t.Fatal("the outer step was lost although the enclosing closure recovered the nested panic")
	}
	if mustNodeExists(t, &g, dropped) {
		t.Fatal("the panicking nested transaction's step survived")
	}
}
