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

// a CSP/actor-style wrapper making it safe for multiple goroutines to
// share one underlying *Graph, none of them ever touching it directly.

func TestGraphActorBasicOperations(t *testing.T) {
	actor := NewGraphActor(&Graph{})
	defer actor.Close()

	a, err := actor.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}

	b, err := actor.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for b: %v", err)
	}

	if !mustNodeExists(t, actor, a) || !mustNodeExists(t, actor, b) {
		t.Fatal("created nodes do not both exist")
	}

	created, err := actor.AddRelationship(a, b)
	if err != nil {
		t.Fatalf("AddRelationship(a,b): %v", err)
	}
	if !created {
		t.Fatal("AddRelationship(a,b) reported that nothing was created")
	}

	if !mustHasRelationship(t, actor, a, b) {
		t.Fatal("HasRelationship(a,b) = false, want true")
	}

	relationship, exists, err := actor.FindRelationship(a, b)
	if err != nil {
		t.Fatalf("FindRelationship(a,b): %v", err)
	}
	if !exists {
		t.Fatal("FindRelationship(a,b) reported the relationship does not exist")
	}
	want := Relationship{From: a, To: b}
	if !reflect.DeepEqual(relationship, want) {
		t.Fatalf("FindRelationship(a,b) = %v, want %v", relationship, want)
	}

	outgoing, err := actor.FindOutgoing(a)
	if err != nil {
		t.Fatalf("FindOutgoing(a): %v", err)
	}
	if !reflect.DeepEqual(outgoing, []Relationship{want}) {
		t.Fatalf("FindOutgoing(a) = %v, want %v", outgoing, []Relationship{want})
	}

	incoming, err := actor.FindIncoming(b)
	if err != nil {
		t.Fatalf("FindIncoming(b): %v", err)
	}
	if !reflect.DeepEqual(incoming, []Relationship{want}) {
		t.Fatalf("FindIncoming(b) = %v, want %v", incoming, []Relationship{want})
	}

	all := mustFindRelationships(t, actor)
	if !reflect.DeepEqual(all, []Relationship{want}) {
		t.Fatalf("FindRelationships() = %v, want %v", all, []Relationship{want})
	}

	removed, err := actor.RemoveRelationship(a, b)
	if err != nil {
		t.Fatalf("RemoveRelationship(a,b): %v", err)
	}
	if !removed {
		t.Fatal("RemoveRelationship(a,b) reported that nothing was removed")
	}

	if mustHasRelationship(t, actor, a, b) {
		t.Fatal("relationship still exists after removal")
	}

	if err := actor.DeleteNode(a); err != nil {
		t.Fatalf("DeleteNode(a): %v", err)
	}
	if mustNodeExists(t, actor, a) {
		t.Fatal("node a still exists after DeleteNode()")
	}
}

func TestGraphActorFindNodes(t *testing.T) {
	actor := NewGraphActor(&Graph{})
	defer actor.Close()

	a, err := actor.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}

	b, err := actor.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for b: %v", err)
	}

	if err2 := actor.DeleteNode(a); err2 != nil {
		t.Fatalf("DeleteNode(a): %v", err2)
	}

	got := mustFindNodes(t, actor)
	want := []NodeID{b}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FindNodes() = %v, want %v", got, want)
	}
}

func TestGraphActorTransactRollsBackOnFailure(t *testing.T) {
	actor := NewGraphActor(&Graph{})
	defer actor.Close()

	const nonexistent NodeID = 999999

	var id NodeID
	err := actor.Transact(func(tx Tx) error {
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

	if mustNodeExists(t, actor, id) {
		t.Fatalf("node %d still exists after its creating transaction rolled back", id)
	}
}

// TestGraphActorTransactPanicPropagatesAndActorSurvives covers the
// panic-handling behavior documented on GraphActor.do: a panic inside a
// Transact closure must still propagate to its original caller, exactly
// as a direct, non-actor Graph.Transact call already does, while
// leaving the actor's own dedicated goroutine alive to service every
// later, unrelated call.
func TestGraphActorTransactPanicPropagatesAndActorSurvives(t *testing.T) {
	actor := NewGraphActor(&Graph{})
	defer actor.Close()

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic to propagate out of GraphActor.Transact()")
			}
		}()

		//nolint:errcheck // because there's no error, it panics
		_ = actor.Transact(func(_ Tx) error {
			panic("boom")
		})
	}()

	id, err := actor.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() after a panicking Transact(): %v", err)
	}
	if !mustNodeExists(t, actor, id) {
		t.Fatalf("node %d does not exist after a panicking Transact()", id)
	}
}

// TestGraphActorReentrancyTripwireFiresAndActorSurvives exercises the
// debug-only reentrancy tripwire (theorystate.md section 90) directly: a
// Transact closure calls back into the very same GraphActor it is
// already running on, from the actor's own dedicated goroutine -- the
// exact shape of the original NameRegistry/GraphActor deadlock this
// tripwire exists to turn into a loud, immediate panic instead of a
// silent, permanent hang. Mirrors
// TestGraphActorTransactPanicPropagatesAndActorSurvives's structure: the
// panic must reach the original caller, and the actor must remain fully
// usable immediately afterward.
func TestGraphActorReentrancyTripwireFiresAndActorSurvives(t *testing.T) {
	actor := NewGraphActor(&Graph{})
	defer actor.Close()

	func() {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("expected the reentrancy tripwire to panic")
			}
			message, ok := r.(string)
			if !ok || !strings.Contains(message, "reentrancy") {
				t.Fatalf("panic value = %v, want a string mentioning reentrancy", r)
			}
		}()

		//nolint:errcheck // the tripwire panics before Transact can return
		_ = actor.Transact(func(_ Tx) error {
			// Reentrant: this closure is already running on actor's one
			// dedicated goroutine, so calling back into the actor here
			// is exactly the deadlock class the tripwire detects.
			actor.RegisterChecker(Checker{})
			return nil
		})
	}()

	id, err := actor.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() after the tripwire panicked: %v", err)
	}
	if !mustNodeExists(t, actor, id) {
		t.Fatalf("node %d does not exist after the tripwire panicked", id)
	}
}

func TestGraphActorCloseIsIdempotent(_ *testing.T) {
	actor := NewGraphActor(&Graph{})

	actor.Close()
	actor.Close()
}

// TestGraphActorConcurrentCreateNodeProducesUniqueIDs exercises
// GraphActor under real concurrent load: many goroutines each mint a
// fresh node and link it to a shared hub node, entirely through the
// actor, with no direct access to the underlying *Graph at all.
// Graph.CreateNode's own nextID counter increment is not atomic on its
// own -- this is exactly what go test -race, or a duplicate/missing
// NodeID, would be expected to catch if GraphActor's serialization
// guarantee did not actually hold.
func TestGraphActorConcurrentCreateNodeProducesUniqueIDs(t *testing.T) {
	actor := NewGraphActor(&Graph{})
	defer actor.Close()

	hub, err := actor.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for hub: %v", err)
	}

	const goroutines = 50

	var wg sync.WaitGroup
	ids := make([]NodeID, goroutines)
	errs := make([]error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			id, createErr := actor.CreateNode()
			if createErr != nil {
				errs[i] = createErr
				return
			}

			if _, addErr := actor.AddRelationship(hub, id); addErr != nil {
				errs[i] = addErr
				return
			}

			ids[i] = id
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

	outgoing, err := actor.FindOutgoing(hub)
	if err != nil {
		t.Fatalf("FindOutgoing(hub): %v", err)
	}
	if len(outgoing) != goroutines {
		t.Fatalf("FindOutgoing(hub) has %d relationships, want %d", len(outgoing), goroutines)
	}
}

// TestGraphActorConcurrentSetTargetNeverProducesTooManyTargets exercises
// the write-skew hazard theorystate.md section 89c names directly: many
// goroutines each running a caller-composed read-then-write sequence on
// the very same pointer (the shape PointerRegistry.SetTarget itself had
// before implementation_state.md item 30 made it atomic; see
// staleReadThenSetTarget), entirely through one GraphActor, with no
// direct *Graph access from any of them. Each sequence's read (the
// current target) and its later, separate commit are two round trips,
// so two goroutines can legitimately interleave between them.
//
// This test does not assert which goroutine "wins": that depends on
// scheduling. It asserts the actual safety property instead: every
// returned error, if any, is specifically ErrTooManyPointerTargets --
// PointerRegistry's own existing Checker (registered once, at
// construction, exactly as it already is for single-threaded use)
// catching and rolling back a losing goroutine's stale commit, with no
// GraphActor-specific machinery required -- and exactly one target
// survives at the end regardless of how the goroutines happened to
// interleave.
func TestGraphActorConcurrentSetTargetNeverProducesTooManyTargets(t *testing.T) {
	actor := NewGraphActor(&Graph{})
	defer actor.Close()

	names := NewNameRegistry(actor)
	ids, err := names.BootstrapNames(actor, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	pointers, err := NewPointerRegistry(actor, ids[NameAllPointers])
	if err != nil {
		t.Fatalf("NewPointerRegistry(): %v", err)
	}

	p, err := pointers.NewPointer(actor)
	if err != nil {
		t.Fatalf("NewPointer(): %v", err)
	}

	const goroutines = 50

	candidates := make([]NodeID, goroutines)
	for i := range candidates {
		candidate, createErr := actor.CreateNode()
		if createErr != nil {
			t.Fatalf("CreateNode() for candidate %d: %v", i, createErr)
		}
		candidates[i] = candidate
	}

	var wg sync.WaitGroup
	errs := make([]error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = staleReadThenSetTarget(actor, pointers, p, candidates[i])
		}()
	}

	wg.Wait()

	for i, setErr := range errs {
		if setErr != nil && !errors.Is(setErr, ErrTooManyPointerTargets) {
			t.Fatalf("goroutine %d: SetTarget() error = %v, want nil or %v", i, setErr, ErrTooManyPointerTargets)
		}
	}

	outgoing, err := actor.FindOutgoing(p)
	if err != nil {
		t.Fatalf("FindOutgoing(p): %v", err)
	}
	if len(outgoing) != 1 {
		t.Fatalf("FindOutgoing(p) = %v, want exactly one surviving target regardless of interleaving", outgoing)
	}

	target, hasTarget, err := pointers.Target(actor, p)
	if err != nil {
		t.Fatalf("Target(p): %v", err)
	}
	if !hasTarget {
		t.Fatal("Target(p) reports no target after concurrent SetTarget calls")
	}

	found := false
	for _, candidate := range candidates {
		if candidate == target {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Target(p) = %d, want one of the attempted candidates %v", target, candidates)
	}
}

// staleReadThenSetTarget reproduces the shape PointerRegistry.SetTarget
// had before implementation_state.md item 30: read the pointer's current
// target through one round trip, then commit the replacement through a
// second, separate one. It exists to keep exercising the hazard a
// caller-composed sequence still has under GraphActor (and the Checker
// that catches it), now that SetTarget itself is atomic.
func staleReadThenSetTarget(api GraphAPI, pointers *PointerRegistry, p, target NodeID) error {
	current, hasTarget, err := pointers.Target(api, p)
	if err != nil {
		return err
	}

	return wrapInterfaceErr(api.Transact(func(tx Tx) error {
		return setPointerTargetTx(tx, p, current, hasTarget, target)
	}))
}

// newActorPointerFixture builds a GraphActor with a PointerRegistry over
// AllPointers and one fresh Pointer node. The actor is closed when the
// test ends.
func newActorPointerFixture(t *testing.T) (*GraphActor, *PointerRegistry, NodeID) {
	t.Helper()

	actor := NewGraphActor(&Graph{})
	t.Cleanup(actor.Close)

	names := NewNameRegistry(actor)
	ids, err := names.BootstrapNames(actor, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	pointers, err := NewPointerRegistry(actor, ids[NameAllPointers])
	if err != nil {
		t.Fatalf("NewPointerRegistry(): %v", err)
	}

	p, err := pointers.NewPointer(actor)
	if err != nil {
		t.Fatalf("NewPointer(): %v", err)
	}

	return actor, pointers, p
}

// TestGraphActorConcurrentSetTargetIsAtomic is the counterpart of
// TestGraphActorConcurrentSetTargetNeverProducesTooManyTargets: with
// SetTarget's reads inside its Transact, concurrent calls can no longer
// produce a stale commit, so every call must succeed (the last commit
// wins) and exactly one target must survive.
func TestGraphActorConcurrentSetTargetIsAtomic(t *testing.T) {
	actor, pointers, p := newActorPointerFixture(t)

	const goroutines = 50

	candidates := make([]NodeID, goroutines)
	for i := range candidates {
		candidate, createErr := actor.CreateNode()
		if createErr != nil {
			t.Fatalf("CreateNode() for candidate %d: %v", i, createErr)
		}
		candidates[i] = candidate
	}

	var wg sync.WaitGroup
	errs := make([]error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = pointers.SetTarget(actor, p, candidates[i])
		}()
	}

	wg.Wait()

	for i, setErr := range errs {
		if setErr != nil {
			t.Fatalf("goroutine %d: SetTarget() error = %v, want nil (SetTarget is atomic)", i, setErr)
		}
	}

	outgoing, err := actor.FindOutgoing(p)
	if err != nil {
		t.Fatalf("FindOutgoing(p): %v", err)
	}
	if len(outgoing) != 1 {
		t.Fatalf("FindOutgoing(p) = %v, want exactly one surviving target", outgoing)
	}

	survivor := outgoing[0].To
	found := false
	for _, candidate := range candidates {
		if candidate == survivor {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("surviving target %d is not one of the attempted candidates %v", survivor, candidates)
	}
}

// TestGraphActorConcurrentEnsureMetadataCreatesExactlyOneMetadataNode
// covers the check-then-create bug in ensureMetadata: before the lookup
// moved inside the Transact, concurrent callers could each conclude "no
// metadata yet" and each create one, after which every lookup failed
// with ErrAmbiguousPointerMetadata.
func TestGraphActorConcurrentEnsureMetadataCreatesExactlyOneMetadataNode(t *testing.T) {
	actor := NewGraphActor(&Graph{})
	defer actor.Close()

	names := NewNameRegistry(actor)
	ids, err := names.BootstrapNames(actor, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	metadata, err := NewPointerMetadataRegistryD(actor, ids[NameAllPointerMetadata], ids[NameAllPointerMetadataSubjectSlot], ids[NameAllPointerMetadataTargetSlot])
	if err != nil {
		t.Fatalf("NewPointerMetadataRegistryD(): %v", err)
	}

	subject, err := actor.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	const goroutines = 50

	var wg sync.WaitGroup
	got := make([]NodeID, goroutines)
	errs := make([]error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i], errs[i] = metadata.EnsureMetadata(actor, subject)
		}()
	}

	wg.Wait()

	for i := range got {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: EnsureMetadata() error = %v", i, errs[i])
		}
		if got[i] != got[0] {
			t.Fatalf("goroutine %d got metadata node %d, goroutine 0 got %d; want one shared node", i, got[i], got[0])
		}
	}

	has, hasErr := metadata.HasMetadata(actor, subject)
	if hasErr != nil {
		t.Fatalf("HasMetadata(): %v (a second metadata node makes lookups ambiguous)", hasErr)
	}
	if !has {
		t.Fatal("HasMetadata() = false after EnsureMetadata()")
	}
}

// TestGraphActorConcurrentNewDomainPointerCreatesExactlyOneSubPointer
// covers the same check-then-create bug in NewDomainPointer.
func TestGraphActorConcurrentNewDomainPointerCreatesExactlyOneSubPointer(t *testing.T) {
	actor := NewGraphActor(&Graph{})
	defer actor.Close()

	rig := newDomainBRig(t, actor)

	anchor, err := actor.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	const goroutines = 50

	var wg sync.WaitGroup
	errs := make([]error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = rig.domainB.NewDomainPointer(actor, anchor)
		}()
	}

	wg.Wait()

	for i, newErr := range errs {
		if newErr != nil {
			t.Fatalf("goroutine %d: NewDomainPointer() error = %v", i, newErr)
		}
	}

	outgoing, err := actor.FindOutgoing(anchor)
	if err != nil {
		t.Fatalf("FindOutgoing(anchor): %v", err)
	}
	if len(outgoing) != 1 {
		t.Fatalf("FindOutgoing(anchor) = %v, want exactly one sub-pointer", outgoing)
	}

	if _, _, targetErr := rig.domainB.Target(actor, anchor); targetErr != nil {
		t.Fatalf("Target(): %v", targetErr)
	}
}

func TestGraphActorNestedTransactRollsBackOnlyInnerSteps(t *testing.T) {
	actor := NewGraphActor(&Graph{})
	defer actor.Close()

	errInner := errors.New("inner failure")

	var kept, dropped NodeID

	err := actor.Transact(func(tx Tx) error {
		var txErr error
		kept, txErr = createNodeTx(tx)
		if txErr != nil {
			return txErr
		}

		if innerErr := tx.Transact(func(inner Tx) error {
			var createErr error
			dropped, createErr = createNodeTx(inner)
			if createErr != nil {
				return createErr
			}

			return errInner
		}); !errors.Is(innerErr, errInner) {
			t.Errorf("nested Transact() error = %v, want %v", innerErr, errInner)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("Transact() error = %v", err)
	}

	if !mustNodeExists(t, actor, kept) {
		t.Fatal("the outer step was lost although only the nested transaction failed")
	}
	if mustNodeExists(t, actor, dropped) {
		t.Fatal("the nested step survived its own failed transaction")
	}
}

// TestGraphActorConcurrentListAppendKeepsListValid appends from many
// goroutines to one list through a GraphActor. Append's existence and tag
// checks now run inside its transaction; Elements validates the resulting
// structure (head/tail, reciprocal links, reachability).
func TestGraphActorConcurrentListAppendKeepsListValid(t *testing.T) {
	actor := NewGraphActor(&Graph{})
	defer actor.Close()

	names := NewNameRegistry(actor)
	ids, err := names.BootstrapNames(actor, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	capsules, err := NewCapsuleRegistry(
		actor,
		ids[NameAllElementCapsules],
		ids[NameAllElementCapsulePrevSlot],
		ids[NameAllElementCapsuleValueSlot],
		ids[NameAllElementCapsuleNextSlot],
	)
	if err != nil {
		t.Fatalf("NewCapsuleRegistry(): %v", err)
	}

	lists, err := NewListRegistry(actor, capsules, ids[NameAllLists], ids[NameAllHeads], ids[NameAllTails])
	if err != nil {
		t.Fatalf("NewListRegistry(): %v", err)
	}

	list, err := lists.NewList(actor)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	const goroutines = 50

	values := make([]NodeID, goroutines)
	for i := range values {
		value, createErr := actor.CreateNode()
		if createErr != nil {
			t.Fatalf("CreateNode() for value %d: %v", i, createErr)
		}
		values[i] = value
	}

	var wg sync.WaitGroup
	errs := make([]error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = lists.Append(actor, list, values[i])
		}()
	}

	wg.Wait()

	for i, appendErr := range errs {
		if appendErr != nil {
			t.Fatalf("goroutine %d: Append() error = %v", i, appendErr)
		}
	}

	elements, err := lists.Elements(actor, list)
	if err != nil {
		t.Fatalf("Elements(): %v", err)
	}

	if !reflect.DeepEqual(sortedNodeIDs(elements), sortedNodeIDs(values)) {
		t.Fatalf("Elements() = %v, want the %d appended values in some order", elements, goroutines)
	}
}
