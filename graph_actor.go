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
	"bytes"
	"fmt"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

// graphActorReentrancyDetectionEnabled gates the debug-only reentrancy
// tripwire in GraphActor.do (theorystate.md section 90). Off by default:
// determining the calling goroutine's ID has a real per-call cost
// (parsing a freshly captured stack trace header), acceptable for tests
// but not something every production caller should pay for unasked.
//
// This tripwire is a dynamic safety net, not the correctness mechanism --
// Go closures always retain full access to their enclosing lexical
// scope, so no compile-time change makes misusing a captured variable
// (e.g. reading through a registry's stored graph reference instead of
// through tx/g) literally impossible. The actual fix is that no
// interpretation-layer registry in this file stores a graph reference at
// all anymore (theorystate.md section 90); this tripwire exists only to
// turn a future regression of that discipline into an immediate, loud
// panic instead of a silent hang, in the specific case where the
// regression would otherwise deadlock GraphActor.
var graphActorReentrancyDetectionEnabled atomic.Bool

// EnableGraphActorReentrancyDetection turns on GraphActor's debug-only
// reentrancy tripwire for every GraphActor in the process, for as long as
// it remains enabled. Intended for test binaries (see this file's
// TestMain), not for production use, given the per-call cost noted on
// graphActorReentrancyDetectionEnabled above.
func EnableGraphActorReentrancyDetection() {
	graphActorReentrancyDetectionEnabled.Store(true)
}

// DisableGraphActorReentrancyDetection turns the tripwire back off.
func DisableGraphActorReentrancyDetection() {
	graphActorReentrancyDetectionEnabled.Store(false)
}

// currentGoroutineID parses the numeric goroutine ID out of the calling
// goroutine's own stack trace header (the "goroutine NNN [running]:"
// line runtime.Stack always writes first), for GraphActor's debug-only
// reentrancy tripwire. This relies on the exact format of runtime.Stack's
// output, which the Go runtime does not guarantee as a stable API --
// acceptable here specifically because this is a debug-only backstop
// that fails safe: every caller below simply skips the tripwire check on
// any error rather than treating a parse failure as itself a violation.
func currentGoroutineID() (int64, error) {
	buf := make([]byte, 64)
	buf = buf[:runtime.Stack(buf, false)]

	fields := bytes.Fields(buf)
	if len(fields) < 2 {
		return 0, fmt.Errorf("unexpected goroutine stack header: %q", buf)
	}

	id, err := strconv.ParseInt(string(fields[1]), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parsing goroutine id from %q: %w", fields[1], err)
	}

	return id, nil
}

// GraphActor is a concurrency-safe wrapper around a private GraphAPI
// backend -- a plain *Graph, or a decorator stack over one such as
// RootGraph (theorystate.md section 87b) -- implementing GraphAPI so it
// can be substituted for a concrete *Graph
// anywhere a registry in this file expects one -- with zero change to
// any registry's own logic, exactly per section 87's decoupling
// refactor. GraphActor is the CSP/actor-style mechanism explored in
// theorystate.md section 89c: rather than protecting the underlying
// *Graph with a lock, GraphActor gives it exactly one owner -- a single
// dedicated goroutine, started by NewGraphActor -- and every other
// goroutine communicates with that owner by submitting whole closures
// over a channel, never by touching the *Graph directly. This makes
// Graph's own bare, unsynchronized maps (theorystate.md section 89b)
// safe to share across goroutines for the first time, without changing
// Graph itself at all.
//
// Txn and Checker need no change to work correctly underneath
// GraphActor. Both already state, as their own load-bearing soundness
// argument, that nothing can observe an in-progress mutation because
// nothing else runs between two statements in the same synchronous call
// (theorystate.md section 19). GraphActor does not weaken that
// argument -- it makes it true again by construction, merely relocated
// from "whichever goroutine happens to call Transact" to "the one
// goroutine GraphActor dedicates to the real Graph."
//
// What is, and is not, atomic under GraphActor. Any single call routed
// through GraphActor -- including an entire Graph.Transact call, however
// many CreateNode/AddRelationship/RemoveRelationship/DeleteNode steps its
// own fn performs internally -- runs to completion on the actor's
// goroutine before the next queued call is even looked at, so it can
// never be interleaved with anything else. What is NOT free is grouping
// more than one separately-submitted call into one larger atomic unit:
// a caller's own read-then-decide-then-write sequence spanning several
// calls, for example, reads a pointer's Target via one call and only
// later, separately, commits a replacement via Graph.Transact -- two
// distinct round trips through GraphActor, not one -- so a second
// goroutine's own such sequence on the very same pointer can
// legitimately land in between them. (A single registry method is not
// exposed to this: since implementation_state.md item 30, every
// Pointer-family method performs its reads inside the same Transact as
// its writes.) This is a real,
// lost-update/write-skew-shaped hazard, not a bug in GraphActor itself
// (theorystate.md section 89c); it is exactly the same shape of gap
// DomainPointerRegistryD's own commit-time Checker already exists to
// close for that structure specifically, by re-validating against live
// current state at commit time rather than trusting what an earlier
// caller read. TestGraphActorConcurrentSetTargetNeverProducesTooManyTargets
// demonstrates this concretely: PointerRegistry's own existing Checker
// (registered once, exactly as it already is for single-threaded use) is
// what catches and rolls back a losing goroutine's stale commit, with no
// GraphActor-specific machinery required. Solving this in general -- for
// a plan whose steps are not fixed in Go source, e.g. one assembled
// dynamically by some future in-graph processor -- needs new,
// not-yet-designed, in-graph transaction-descriptor machinery
// (theorystate.md section 89c); GraphActor deliberately does not attempt
// that here.
//
// Deadlock is not a risk GraphActor introduces: a job running on its one
// goroutine never blocks waiting for an external reply mid-flight (the
// same non-blocking request/response discipline theorystate.md section
// 47a already requires cross-graph), so there is no circular wait for
// deadlock to arise from.
//
// Panics are recovered inside the actor's own goroutine and re-raised in
// the original calling goroutine once that call returns, so that a
// panicking Transact closure -- see Graph.Transact's own documented
// panic-then-rollback behavior -- looks, from its caller's perspective,
// exactly like a direct, non-actor call to Graph.Transact would: the
// panic still propagates to the caller, but the actor's single dedicated
// goroutine survives to keep serving every other, unrelated caller
// afterward, rather than the panic silently killing the one goroutine
// every future request depends on (which, left unrecovered, would in
// fact crash the entire process, not merely this actor -- an unrecovered
// panic in any goroutine terminates the whole program).
//
// Close stops the actor's goroutine. GraphActor must not be used
// concurrently with, or after, a call to Close -- an accepted,
// documented caller responsibility (theorystate.md section 89b's
// "document the assumption explicitly" resolution), not something
// GraphActor attempts to guard against itself.
type GraphActor struct {
	graph     GraphAPI
	requests  chan func(GraphAPI)
	stopped   chan struct{}
	closeOnce sync.Once

	// workerGoroutineID holds the goroutine ID of this actor's own
	// dedicated worker goroutine (the one running run(), below), once it
	// has started. Zero means "not yet known" -- real goroutine IDs
	// assigned by the runtime never reach zero, so zero is a safe
	// "unset" sentinel. Used only by the debug-only reentrancy tripwire
	// in do(); see graphActorReentrancyDetectionEnabled above. Stored as
	// atomic.Int64, not a plain int64 field, because it is written once
	// from run()'s own goroutine and read from every other goroutine
	// calling do() -- an ordinary unsynchronized field here would itself
	// be a data race.
	workerGoroutineID atomic.Int64
}

// Compile-time assertion that *GraphActor satisfies GraphAPI, exactly
// mirroring the existing assertion for *Graph above.
var _ GraphAPI = (*GraphActor)(nil)

// NewGraphActor starts a GraphActor's dedicated goroutine and returns
// immediately. backend becomes owned by that goroutine from this point
// on: per the GraphActor doc comment, nothing outside the returned
// *GraphActor may touch backend (or anything it wraps) directly again --
// every future access must go through the returned value's own methods.
//
// backend is normally a plain *Graph, but may be a decorator stack over
// one, most importantly a RootGraph. In that case the actor must be the
// OUTERMOST layer: NewGraphActor(NewRootGraph(&g, root)). Inside the
// actor every overlay method runs as one atomic job, and the overlay's
// stored reference is the raw backend rather than the actor (see the
// RootGraph doc comment and theorystate.md section 87b/90).
func NewGraphActor(backend GraphAPI) *GraphActor {
	ga := &GraphActor{
		graph:    backend,
		requests: make(chan func(GraphAPI)),
		stopped:  make(chan struct{}),
	}

	go ga.run()

	return ga
}

// run is the body of GraphActor's one dedicated goroutine: it services
// requests until the channel is closed (by Close), then closes stopped
// so Close can report that the goroutine has actually exited.
func (ga *GraphActor) run() {
	defer close(ga.stopped)

	// Record this goroutine's own ID once, before servicing any request,
	// for the reentrancy tripwire in do() below. If the ID cannot be
	// determined for some reason, workerGoroutineID is simply left at
	// its zero-value "unknown" sentinel and the tripwire silently does
	// not fire for this actor -- a missed debug assertion, not a
	// correctness problem, since the tripwire is a backstop on top of
	// the real fix (theorystate.md section 90), not the fix itself.
	if id, err := currentGoroutineID(); err == nil {
		ga.workerGoroutineID.Store(id)
	}

	for req := range ga.requests {
		req(ga.graph)
	}
}

// do submits fn to run against ga's private backend, on ga's own
// dedicated goroutine, and blocks until fn has returned. Every exported
// GraphActor method is built on this one primitive; see the GraphActor
// doc comment for exactly what this does and does not make atomic. A
// panic inside fn is recovered here, on the actor's own goroutine (so
// the actor survives to service future callers), and re-raised here
// again, back on the calling goroutine, once fn has finished -- see the
// GraphActor doc comment's panic-handling paragraph.
func (ga *GraphActor) do(fn func(g GraphAPI)) {
	if graphActorReentrancyDetectionEnabled.Load() {
		if callerID, err := currentGoroutineID(); err == nil {
			if workerID := ga.workerGoroutineID.Load(); workerID != 0 && callerID == workerID {
				panic(fmt.Sprintf(
					"GraphActor reentrancy detected: goroutine %d, this actor's own dedicated worker, called back into GraphActor.do while already executing an earlier request on this same actor -- this is the reentrancy-deadlock class documented on GraphActor and theorystate.md section 90 (e.g. a Checker or tx-composable helper reading through a registry's stored graph reference instead of through tx/g), which would otherwise hang forever instead of panicking. Enabled via EnableGraphActorReentrancyDetection; see that function's doc comment.",
					callerID,
				))
			}
		}
	}

	done := make(chan struct{})

	var recovered any
	var panicked bool

	ga.requests <- func(g GraphAPI) {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
				recovered = r
			}
			close(done)
		}()

		fn(g)
	}

	<-done

	if panicked {
		panic(recovered)
	}
}

// Close stops ga's dedicated goroutine and blocks until it has actually
// exited. Close is idempotent: only the first call has any effect,
// later calls simply return once the goroutine has stopped. See the
// GraphActor doc comment for what Close does not attempt to guard
// against.
func (ga *GraphActor) Close() {
	ga.closeOnce.Do(func() {
		close(ga.requests)
	})

	<-ga.stopped
}

// NodeExists behaves exactly like the backend's NodeExists, routed
// through ga's dedicated goroutine.
func (ga *GraphActor) NodeExists(id NodeID) (bool, error) {
	var exists bool
	var err error

	ga.do(func(g GraphAPI) {
		exists, err = g.NodeExists(id)
	})

	return exists, wrapInterfaceErr(err)
}

// HasRelationship behaves exactly like the backend's HasRelationship,
// routed through ga's dedicated goroutine.
func (ga *GraphActor) HasRelationship(a, b NodeID) (bool, error) {
	var has bool
	var err error

	ga.do(func(g GraphAPI) {
		has, err = g.HasRelationship(a, b)
	})

	return has, wrapInterfaceErr(err)
}

// FindRelationship behaves exactly like the backend's FindRelationship,
// routed through ga's dedicated goroutine.
func (ga *GraphActor) FindRelationship(from, to NodeID) (relationship Relationship, exists bool, err error) {
	ga.do(func(g GraphAPI) {
		relationship, exists, err = g.FindRelationship(from, to)
	})

	return relationship, exists, wrapInterfaceErr(err)
}

// FindOutgoing behaves exactly like the backend's FindOutgoing, routed
// through ga's dedicated goroutine.
func (ga *GraphActor) FindOutgoing(from NodeID) (relationships []Relationship, err error) {
	ga.do(func(g GraphAPI) {
		relationships, err = g.FindOutgoing(from)
	})

	return relationships, wrapInterfaceErr(err)
}

// FindIncoming behaves exactly like the backend's FindIncoming, routed
// through ga's dedicated goroutine.
func (ga *GraphActor) FindIncoming(to NodeID) (relationships []Relationship, err error) {
	ga.do(func(g GraphAPI) {
		relationships, err = g.FindIncoming(to)
	})

	return relationships, wrapInterfaceErr(err)
}

// FindRelationships behaves exactly like the backend's
// FindRelationships, routed through ga's dedicated goroutine. If the
// backend is a RootGraph, its whole overlay computation runs as this one
// job, so the result is a single consistent snapshot.
func (ga *GraphActor) FindRelationships() ([]Relationship, error) {
	var relationships []Relationship
	var err error

	ga.do(func(g GraphAPI) {
		relationships, err = g.FindRelationships()
	})

	return relationships, wrapInterfaceErr(err)
}

// FindNodes behaves exactly like the backend's FindNodes, routed through
// ga's dedicated goroutine.
func (ga *GraphActor) FindNodes() ([]NodeID, error) {
	var ids []NodeID
	var err error

	ga.do(func(g GraphAPI) {
		ids, err = g.FindNodes()
	})

	return ids, wrapInterfaceErr(err)
}

// Transact behaves exactly like the backend's Transact, with fn's entire
// body -- however many steps it performs against tx -- run as one single
// job on ga's dedicated goroutine, so nothing else can ever be
// interleaved with it. See the GraphActor doc comment for what this does
// and does not make atomic relative to some other, separately-submitted
// call.
func (ga *GraphActor) Transact(fn func(tx Tx) error) (err error) {
	ga.do(func(g GraphAPI) {
		err = g.Transact(fn)
	})

	return wrapInterfaceErr(err)
}

// actorPage runs one bounded page read as a single job on ga's goroutine.
func actorPage[T any](ga *GraphActor, fetch func(g GraphAPI) ([]T, error)) ([]T, error) {
	var page []T
	var err error

	ga.do(func(g GraphAPI) {
		page, err = fetch(g)
	})

	return page, wrapInterfaceErr(err)
}

// findOutgoingAfter forwards one page read to the backend's own pager, as
// one short job, so a long walk never holds the actor for longer than one
// page. If the backend cannot page, errPagingUnsupported tells
// pageIterator to fall back to one full read.
func (ga *GraphActor) findOutgoingAfter(from, after NodeID, hasAfter bool, limit int) ([]Relationship, error) {
	return actorPage(ga, func(g GraphAPI) ([]Relationship, error) {
		pager, ok := g.(outgoingPager)
		if !ok {
			return nil, errPagingUnsupported
		}

		page, err := pager.findOutgoingAfter(from, after, hasAfter, limit)

		return page, wrapInterfaceErr(err)
	})
}

// findIncomingAfter is findOutgoingAfter's mirror.
func (ga *GraphActor) findIncomingAfter(to, after NodeID, hasAfter bool, limit int) ([]Relationship, error) {
	return actorPage(ga, func(g GraphAPI) ([]Relationship, error) {
		pager, ok := g.(incomingPager)
		if !ok {
			return nil, errPagingUnsupported
		}

		page, err := pager.findIncomingAfter(to, after, hasAfter, limit)

		return page, wrapInterfaceErr(err)
	})
}

// findCommonChildren forwards the lookup to the backend as one short job. If
// the backend has none, errPagingUnsupported tells the caller to scan.
func (ga *GraphActor) findCommonChildren(a, b NodeID, limit int) ([]NodeID, error) {
	return actorPage(ga, func(g GraphAPI) ([]NodeID, error) {
		finder, ok := g.(commonChildrenFinder)
		if !ok {
			return nil, errPagingUnsupported
		}

		common, err := finder.findCommonChildren(a, b, limit)

		return common, wrapInterfaceErr(err)
	})
}

// view runs fn as one job on ga's goroutine, so all its reads agree (see
// readViewer). If the backend has a view of its own (BoltGraph) fn runs inside
// it; otherwise fn reads the backend directly, which is consistent because
// nothing else runs on this goroutine meanwhile. fn must not call ga.
func (ga *GraphActor) view(fn func(reader GraphReader) error) error {
	var err error

	ga.do(func(g GraphAPI) {
		err = readView(g, fn)
	})

	return wrapInterfaceErr(err)
}

// Compile-time assertion that GraphActor forwards read views.
var _ readViewer = (*GraphActor)(nil)

// Compile-time assertions that GraphActor forwards paging.
var (
	_ outgoingPager = (*GraphActor)(nil)
	_ incomingPager = (*GraphActor)(nil)
)

// RegisterChecker behaves exactly like the backend's RegisterChecker,
// routed through ga's dedicated goroutine.
func (ga *GraphActor) RegisterChecker(c Checker) {
	ga.do(func(g GraphAPI) {
		g.RegisterChecker(c)
	})
}
