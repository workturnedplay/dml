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

// ---------------------------------------------------------------------
// LeaseRegistry: sessions and holds (theorystate.md section 111).

func newLeaseTestFixture(t *testing.T) (testGraph, *SetRegistry, *LeaseRegistry) {
	t.Helper()

	g := newTestGraph(t)
	names := NewNameRegistry(g)

	ids, err := names.BootstrapNames(g, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	sets, err := NewSetRegistry(g, ids[NameAllSets], ids[NameAllCompositeSets], ids[NameAllCompositeSetLogs])
	if err != nil {
		t.Fatalf("NewSetRegistry(): %v", err)
	}

	leases, err := NewLeaseRegistry(g, sets, ids[NameAllSessions])
	if err != nil {
		t.Fatalf("NewLeaseRegistry(): %v", err)
	}

	return g, sets, leases
}

func mustNewSession(t *testing.T, api Transactor, leases *LeaseRegistry) NodeID {
	t.Helper()

	session, err := leases.NewSession(api)
	if err != nil {
		t.Fatalf("NewSession(): %v", err)
	}

	return session
}

func mustNewResource(t *testing.T, api Transactor, sets *SetRegistry) NodeID {
	t.Helper()

	resource, err := sets.NewSet(api)
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	return resource
}

func mustAcquire(t *testing.T, api Transactor, leases *LeaseRegistry, resource, session NodeID) bool {
	t.Helper()

	first, err := leases.Acquire(api, resource, session)
	if err != nil {
		t.Fatalf("Acquire(%d, %d): %v", resource, session, err)
	}

	return first
}

func requireHolders(t *testing.T, graph GraphReader, leases *LeaseRegistry, resource NodeID, want []NodeID) {
	t.Helper()

	got, err := leases.Holders(graph, resource)
	if err != nil {
		t.Fatalf("Holders(%d): %v", resource, err)
	}
	if !reflect.DeepEqual(sortedNodeIDs(got), sortedNodeIDs(want)) {
		t.Fatalf("Holders(%d) = %v, want %v", resource, got, want)
	}
}

func TestFoundationalNamesIncludesAllSessions(t *testing.T) {
	for _, name := range FoundationalNames {
		if name == NameAllSessions {
			return
		}
	}

	t.Fatalf("FoundationalNames %v does not include %q", FoundationalNames, NameAllSessions)
}

func TestNewLeaseRegistryRequiresExistingAllSessions(t *testing.T) {
	var g Graph

	const nonexistent NodeID = 999999

	if _, err := NewLeaseRegistry(&g, nil, nonexistent); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("NewLeaseRegistry() error = %v, want %v", err, ErrNodeNotFound)
	}
}

func TestLeaseNewSessionIsTaggedAndListed(t *testing.T) {
	g, _, leases := newLeaseTestFixture(t)

	first := mustNewSession(t, g, leases)
	second := mustNewSession(t, g, leases)

	for _, session := range []NodeID{first, second} {
		is, err := leases.IsSession(g, session)
		if err != nil || !is {
			t.Fatalf("IsSession(%d) = (%v,%v), want (true,nil)", session, is, err)
		}
	}

	sessions, err := leases.Sessions(g)
	if err != nil {
		t.Fatalf("Sessions(): %v", err)
	}
	if want := []NodeID{first, second}; !reflect.DeepEqual(sessions, want) {
		t.Fatalf("Sessions() = %v, want %v", sessions, want)
	}
}

func TestLeaseAcquireReportsFirstHolderOnly(t *testing.T) {
	g, sets, leases := newLeaseTestFixture(t)

	resource := mustNewResource(t, g, sets)
	s1 := mustNewSession(t, g, leases)
	s2 := mustNewSession(t, g, leases)

	for _, tc := range []struct {
		name    string
		session NodeID
		want    bool
	}{
		{name: "first holder", session: s1, want: true},
		{name: "second holder", session: s2, want: false},
		{name: "repeat by the first holder", session: s1, want: false},
	} {
		if got := mustAcquire(t, g, leases, resource, tc.session); got != tc.want {
			t.Fatalf("%s: Acquire() first = %v, want %v", tc.name, got, tc.want)
		}
	}

	requireHolders(t, g, leases, resource, []NodeID{s1, s2})
}

func TestLeaseReleaseReportsLastHolderOnly(t *testing.T) {
	g, sets, leases := newLeaseTestFixture(t)

	resource := mustNewResource(t, g, sets)
	s1 := mustNewSession(t, g, leases)
	s2 := mustNewSession(t, g, leases)

	mustAcquire(t, g, leases, resource, s1)
	mustAcquire(t, g, leases, resource, s2)

	for _, tc := range []struct {
		name    string
		session NodeID
		want    bool
	}{
		{name: "not the last holder", session: s1, want: false},
		{name: "the last holder", session: s2, want: true},
		{name: "releasing a hold already released", session: s2, want: false},
	} {
		got, err := leases.Release(g, resource, tc.session)
		if err != nil {
			t.Fatalf("%s: Release(): %v", tc.name, err)
		}
		if got != tc.want {
			t.Fatalf("%s: Release() last = %v, want %v", tc.name, got, tc.want)
		}
	}

	held, err := leases.Held(g, resource)
	if err != nil || held {
		t.Fatalf("Held() = (%v,%v), want (false,nil)", held, err)
	}
}

func TestLeaseOperationsRequireOpenSessionAndSet(t *testing.T) {
	g, sets, leases := newLeaseTestFixture(t)

	resource := mustNewResource(t, g, sets)
	session := mustNewSession(t, g, leases)
	notASession := newTestNode(t, g)
	notASet := newTestNode(t, g)

	const nonexistent NodeID = 999999

	if _, err := leases.Acquire(g, resource, notASession); !errors.Is(err, ErrNotSession) {
		t.Fatalf("Acquire(notASession) error = %v, want %v", err, ErrNotSession)
	}
	if _, err := leases.Acquire(g, resource, nonexistent); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Acquire(nonexistent session) error = %v, want %v", err, ErrNodeNotFound)
	}
	if _, err := leases.Acquire(g, notASet, session); !errors.Is(err, ErrNotSet) {
		t.Fatalf("Acquire(notASet) error = %v, want %v", err, ErrNotSet)
	}
	if _, err := leases.Release(g, resource, notASession); !errors.Is(err, ErrNotSession) {
		t.Fatalf("Release(notASession) error = %v, want %v", err, ErrNotSession)
	}
	if _, err := leases.Release(g, notASet, session); !errors.Is(err, ErrNotSet) {
		t.Fatalf("Release(notASet) error = %v, want %v", err, ErrNotSet)
	}
	if _, err := leases.CloseSession(g, notASession); !errors.Is(err, ErrNotSession) {
		t.Fatalf("CloseSession(notASession) error = %v, want %v", err, ErrNotSession)
	}

	requireHolders(t, g, leases, resource, nil)
}

func TestLeaseCloseSessionReleasesEveryHoldAndReportsFreedResources(t *testing.T) {
	g, sets, leases := newLeaseTestFixture(t)

	r1 := mustNewResource(t, g, sets)
	r2 := mustNewResource(t, g, sets)
	s1 := mustNewSession(t, g, leases)
	s2 := mustNewSession(t, g, leases)

	mustAcquire(t, g, leases, r1, s1)
	mustAcquire(t, g, leases, r2, s1)
	mustAcquire(t, g, leases, r2, s2)

	freed, err := leases.CloseSession(g, s1)
	if err != nil {
		t.Fatalf("CloseSession(s1): %v", err)
	}
	if want := []NodeID{r1}; !reflect.DeepEqual(freed, want) {
		t.Fatalf("CloseSession(s1) freed = %v, want %v (r2 is still held by s2)", freed, want)
	}

	if mustNodeExists(t, g, s1) {
		t.Fatal("the closed session still exists")
	}
	requireHolders(t, g, leases, r1, nil)
	requireHolders(t, g, leases, r2, []NodeID{s2})

	freed, err = leases.CloseSession(g, s2)
	if err != nil {
		t.Fatalf("CloseSession(s2): %v", err)
	}
	if want := []NodeID{r2}; !reflect.DeepEqual(freed, want) {
		t.Fatalf("CloseSession(s2) freed = %v, want %v", freed, want)
	}

	// A closed session is gone: acting for it fails loudly.
	if _, acquireErr := leases.Acquire(g, r1, s1); !errors.Is(acquireErr, ErrNodeNotFound) {
		t.Fatalf("Acquire() for a closed session error = %v, want %v", acquireErr, ErrNodeNotFound)
	}
}

// TestLeaseCloseSessionRollsBackIfSessionIsReferencedElsewhere: the usual
// "delete only if empty" rule applies to the session node, and a refused
// close must change nothing, the holds included.
func TestLeaseCloseSessionRollsBackIfSessionIsReferencedElsewhere(t *testing.T) {
	g, sets, leases := newLeaseTestFixture(t)

	resource := mustNewResource(t, g, sets)
	session := mustNewSession(t, g, leases)

	mustAcquire(t, g, leases, resource, session)

	referrer := newTestNode(t, g)
	if _, err := g.AddRelationship(referrer, session); err != nil {
		t.Fatalf("AddRelationship(referrer, session): %v", err)
	}

	if _, err := leases.CloseSession(g, session); !errors.Is(err, ErrNodeNotEmpty) {
		t.Fatalf("CloseSession() error = %v, want %v", err, ErrNodeNotEmpty)
	}

	requireHolders(t, g, leases, resource, []NodeID{session})

	is, err := leases.IsSession(g, session)
	if err != nil || !is {
		t.Fatalf("IsSession() = (%v,%v), want (true,nil) after a refused close", is, err)
	}
}

func TestLeaseCloseAllSessionsIsTheStartupSweep(t *testing.T) {
	g, sets, leases := newLeaseTestFixture(t)

	r1 := mustNewResource(t, g, sets)
	r2 := mustNewResource(t, g, sets)
	s1 := mustNewSession(t, g, leases)
	s2 := mustNewSession(t, g, leases)
	mustNewSession(t, g, leases) // holds nothing

	mustAcquire(t, g, leases, r1, s1)
	mustAcquire(t, g, leases, r1, s2)
	mustAcquire(t, g, leases, r2, s2)

	freed, err := leases.CloseAllSessions(g)
	if err != nil {
		t.Fatalf("CloseAllSessions(): %v", err)
	}
	if want := sortedNodeIDs([]NodeID{r1, r2}); !reflect.DeepEqual(sortedNodeIDs(freed), want) {
		t.Fatalf("CloseAllSessions() freed = %v, want %v (in some order)", freed, want)
	}

	sessions, err := leases.Sessions(g)
	if err != nil {
		t.Fatalf("Sessions(): %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("Sessions() = %v, want none after the sweep", sessions)
	}

	requireHolders(t, g, leases, r1, nil)
	requireHolders(t, g, leases, r2, nil)

	// The sweep is idempotent.
	freed, err = leases.CloseAllSessions(g)
	if err != nil || len(freed) != 0 {
		t.Fatalf("second CloseAllSessions() = (%v,%v), want (none,nil)", freed, err)
	}
}

func TestLeaseAcquireComposesAndRollsBackWithEnclosingTransaction(t *testing.T) {
	g, sets, leases := newLeaseTestFixture(t)

	resource := mustNewResource(t, g, sets)
	session := mustNewSession(t, g, leases)
	errOuter := errors.New("outer failure")

	err := g.Transact(func(tx Tx) error {
		if _, acquireErr := leases.Acquire(tx, resource, session); acquireErr != nil {
			return acquireErr
		}

		return errOuter
	})
	if !errors.Is(err, errOuter) {
		t.Fatalf("Transact() error = %v, want %v", err, errOuter)
	}

	held, err := leases.Held(g, resource)
	if err != nil || held {
		t.Fatalf("Held() = (%v,%v), want (false,nil) after the enclosing transaction rolled back", held, err)
	}
}

// requireExactlyOneTrue fails unless exactly one of flags is true and no
// goroutine reported an error.
func requireExactlyOneTrue(t *testing.T, label string, flags []bool, errs []error) {
	t.Helper()

	count := 0

	for i, flag := range flags {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if flag {
			count++
		}
	}

	if count != 1 {
		t.Fatalf("%d goroutines reported %s = true, want exactly 1 (%v)", count, label, flags)
	}
}

// newActorLeaseRig builds a GraphActor with the registries a lease test
// needs, closed when the test ends.
func newActorLeaseRig(t *testing.T) (*GraphActor, *SetRegistry, *LeaseRegistry) {
	t.Helper()

	actor := NewGraphActor(&Graph{})
	t.Cleanup(actor.Close)

	names := NewNameRegistry(actor)

	ids, err := names.BootstrapNames(actor, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	sets, err := NewSetRegistry(actor, ids[NameAllSets], ids[NameAllCompositeSets], ids[NameAllCompositeSetLogs])
	if err != nil {
		t.Fatalf("NewSetRegistry(): %v", err)
	}

	leases, err := NewLeaseRegistry(actor, sets, ids[NameAllSessions])
	if err != nil {
		t.Fatalf("NewLeaseRegistry(): %v", err)
	}

	return actor, sets, leases
}

// TestGraphActorConcurrentLeasesReportExactlyOneFirstAndOneLast is the
// "ten processes want the same temporary resource" scenario: however the
// goroutines interleave, exactly one acquire sees the 0 -> 1 transition
// (so the effect is requested once) and exactly one release sees 1 -> 0.
func TestGraphActorConcurrentLeasesReportExactlyOneFirstAndOneLast(t *testing.T) {
	actor, sets, leases := newActorLeaseRig(t)

	const processes = 10

	resource := mustNewResource(t, actor, sets)

	sessions := make([]NodeID, processes)
	for i := range sessions {
		sessions[i] = mustNewSession(t, actor, leases)
	}

	firsts := make([]bool, processes)
	lasts := make([]bool, processes)
	errs := make([]error, processes)

	runConcurrently(processes, func(i int) {
		firsts[i], errs[i] = leases.Acquire(actor, resource, sessions[i])
	})
	requireExactlyOneTrue(t, "first", firsts, errs)

	runConcurrently(processes, func(i int) {
		lasts[i], errs[i] = leases.Release(actor, resource, sessions[i])
	})
	requireExactlyOneTrue(t, "last", lasts, errs)

	held, err := leases.Held(actor, resource)
	if err != nil || held {
		t.Fatalf("Held() = (%v,%v), want (false,nil) after every release", held, err)
	}
}

// TestGraphActorCrashedHolderIsFoundAndReleasedByClosingItsSession: nine
// of ten processes release, one "crashes" without releasing. A counter
// would be stuck at one forever, with nothing saying whose decrement is
// missing. Here the holder set names the culprit, and closing its session
// (what the host does when the connection drops) frees the resource.
func TestGraphActorCrashedHolderIsFoundAndReleasedByClosingItsSession(t *testing.T) {
	actor, sets, leases := newActorLeaseRig(t)

	const processes = 10

	resource := mustNewResource(t, actor, sets)

	sessions := make([]NodeID, processes)
	for i := range sessions {
		sessions[i] = mustNewSession(t, actor, leases)
	}

	crashed := sessions[processes-1]
	survivors := sessions[:processes-1]

	firsts := make([]bool, processes)
	errs := make([]error, processes)

	runConcurrently(processes, func(i int) {
		firsts[i], errs[i] = leases.Acquire(actor, resource, sessions[i])
	})
	requireExactlyOneTrue(t, "first", firsts, errs)

	lasts := make([]bool, len(survivors))
	releaseErrs := make([]error, len(survivors))

	runConcurrently(len(survivors), func(i int) {
		lasts[i], releaseErrs[i] = leases.Release(actor, resource, survivors[i])
	})

	for i, last := range lasts {
		if releaseErrs[i] != nil {
			t.Fatalf("goroutine %d: Release(): %v", i, releaseErrs[i])
		}
		if last {
			t.Fatalf("goroutine %d reported the last release while the crashed process still holds the resource", i)
		}
	}

	held, err := leases.Held(actor, resource)
	if err != nil || !held {
		t.Fatalf("Held() = (%v,%v), want (true,nil): the crashed process never released", held, err)
	}
	requireHolders(t, actor, leases, resource, []NodeID{crashed})

	freed, err := leases.CloseSession(actor, crashed)
	if err != nil {
		t.Fatalf("CloseSession(crashed): %v", err)
	}
	if want := []NodeID{resource}; !reflect.DeepEqual(freed, want) {
		t.Fatalf("CloseSession(crashed) freed = %v, want %v", freed, want)
	}

	held, err = leases.Held(actor, resource)
	if err != nil || held {
		t.Fatalf("Held() = (%v,%v), want (false,nil) after the crashed session was closed", held, err)
	}

	// The surviving sessions are still open until their own owners close
	// them; the startup sweep takes care of any that were left behind.
	if _, sweepErr := leases.CloseAllSessions(actor); sweepErr != nil {
		t.Fatalf("CloseAllSessions(): %v", sweepErr)
	}

	remaining, err := leases.Sessions(actor)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("Sessions() = (%v,%v), want none after the sweep", remaining, err)
	}
}
