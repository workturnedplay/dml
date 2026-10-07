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

// Package dml implements the dml graph engine: a primitive directed graph
// plus the higher-level registries (names, pointers, lists, sets, domain
// pointers) built on it. It is the foundation the rest of the project is
// built on; see theorystate.md section 7b for the correctness discipline
// this code follows.
package dml

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	ilog "log"
	"maps"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	bolt "go.etcd.io/bbolt"
	berrors "go.etcd.io/bbolt/errors"
)

type NodeID uint64

type Relationship struct {
	From NodeID
	To   NodeID
}

var (
	ErrNodeNotFound    = errors.New("node not found")
	ErrNodeNotEmpty    = errors.New("node has relationships")
	ErrNodeIDExhausted = errors.New("node ID space exhausted")

	// ErrGraphStoreUnavailable is returned by a GraphReader method when
	// the backend's own read channel failed -- an I/O error, a closed
	// database handle, or similar -- as opposed to the request itself
	// being invalid (ErrNodeNotFound) or the store answering with content
	// that is present but violates this package's own on-disk layout
	// (ErrStoreCorrupt). Every in-memory-backed GraphReader in this file
	// (*Graph, *Txn, graphCoreReader, *GraphActor, and the test-only
	// stagedGraph/stagedOverlay) has no failure mode of this kind and
	// never returns it; BoltGraph is, at present, the only backend that
	// can. Callers should treat it the way any other backend-availability
	// failure is treated: as a reason not to trust this specific read,
	// not as a claim about whether the underlying data is well-formed.
	ErrGraphStoreUnavailable = errors.New("graph store did not answer a read")
)

// concurrentAccessGuard is a fail-fast (not blocking) protection against
// two goroutines calling into the same *Graph at the same time. Unlike a
// sync.Mutex, which would make a second, concurrent caller simply wait
// its turn -- silently turning a caller bug into merely slow, serialized
// behavior -- this panics the instant overlap is detected, on the theory
// that concurrent access to a bare *Graph is always a caller bug (the
// safe, supported path for real concurrency is GraphActor,
// theorystate.md section 89c) and should fail loud rather than either
// silently corrupt state or silently paper over the mistake. This
// mirrors the same fail-loud-not-silently-repair discipline used
// throughout this file (ErrTooManyPointerTargets,
// ErrNameBoundToDeletedNode) and, more specifically, GraphActor's own
// reentrancy tripwire (theorystate.md section 90) -- this is that same
// idea, applied to genuine cross-goroutine racing on Graph itself rather
// than to GraphActor's specific reentrancy-deadlock shape.
//
// This deliberately does NOT track which goroutine holds the guard,
// unlike GraphActor's reentrancy tripwire (which does, via
// currentGoroutineID): no *Graph method ever calls back into another
// *Graph method through its own public, guarded API while already
// executing one -- every guarded method's own logic instead runs through
// an unexported, unguarded "core" counterpart (findOutgoingCore,
// hasRelationshipCore, and so on; the write operations exist only as
// cores, reachable through Txn), and Txn's methods, runCheckers,
// checkerRelevant, and every Checker's Check function (via the
// graphCoreReader adapter passed to it) all read and write through those
// same cores directly, never through the guarded public methods. There
// is therefore no legitimate same-goroutine nesting for a goroutine-ID
// check to need to distinguish from genuine cross-goroutine overlap:
// Graph.Transact acquires this guard exactly once for its own entire
// duration (including running fn and every relevant Checker), and
// nothing internal to this file ever re-enters it.
//
// A caller wanting real concurrent access to one Graph should use
// GraphActor instead, which serializes every access onto one dedicated
// goroutine and therefore never trips this guard at all.
//
// Known limitation, the same honest caveat GraphActor's own tripwire
// names on itself: this catches genuine *temporal overlap* between two
// calls -- the overwhelmingly common real-world shape of this bug -- but
// does not by itself give the full Go memory-model guarantee `go test
// -race` checks (a handoff between goroutines with no overlap but also
// no happens-before synchronization is still technically racy under the
// memory model, even though this guard would never observe any overlap
// to panic on). This is a dynamic safety net for the common case, not a
// substitute for -race or for GraphActor's actual serialization.
//
// RootGraph used to be a known exception to this guard's coverage: its
// ROOT overlay read Graph.nodes directly, because no interface method
// enumerated existing nodes. GraphReader.FindNodes closed that gap
// (theorystate.md section 87b), so RootGraph now reaches the graph only
// through the public, guarded API and is covered like any other caller.
type concurrentAccessGuard struct {
	held atomic.Bool
}

// acquire panics immediately if the guard is already held -- by any
// goroutine, including this one -- and otherwise marks it held and
// returns a function that releases it. See the concurrentAccessGuard
// doc comment for why this fails loud rather than blocking, and why no
// goroutine-identity tracking is needed.
func (c *concurrentAccessGuard) acquire() (release func()) {
	if !c.held.CompareAndSwap(false, true) {
		panic("dml: concurrent access to *Graph detected from more than one goroutine -- a bare *Graph supports only one goroutine at a time; use GraphActor for safe multi-goroutine access (theorystate.md section 89c)")
	}

	return func() {
		c.held.Store(false)
	}
}

// Graph is the primitive graph.
//
// Semantically, it consists of:
//   - existing NodeIDs
//   - unique directed relationships (A, B)
//
// The separate outgoing/incoming maps are implementation indexes.
// They are not additional semantic primitives.
type Graph struct {
	nextID    NodeID
	exhausted bool

	nodes map[NodeID]struct{}

	// outgoing[A][B] means the primitive relationship (A, B) exists.
	outgoing map[NodeID]map[NodeID]struct{}

	// incoming[B][A] means the primitive relationship (A, B) exists.
	incoming map[NodeID]map[NodeID]struct{}

	// checkers holds every Checker registered via RegisterChecker, run by
	// Transact immediately after a transaction's mutations succeed, and
	// before Transact reports that success to its own caller. See the
	// Checker type below (theorystate.md sections 73/77).
	checkers []Checker

	// guard is the fail-fast concurrent-access protection described on
	// concurrentAccessGuard above (theorystate.md section 89b). Every
	// public, top-level entry point into a *Graph -- each query method,
	// RegisterChecker, and Transact for its entire duration -- acquires
	// and releases it; nothing internal to this file ever re-enters it.
	guard concurrentAccessGuard
}

// createNodeCore creates a new node and returns its NodeID. It is the
// only implementation of node creation: *Graph deliberately exports no
// write methods, so a node can be created only through Txn.CreateNode,
// i.e. from inside Graph.Transact, where commit-time Checkers and commit
// hooks apply (theorystate.md section 92). The same holds for
// addRelationshipCore, removeRelationshipCore and deleteNodeCore below.
//
// IDs currently increase monotonically. Reuse of deleted IDs is
// deliberately not implemented yet.
//
// Unlike the exported query methods, the *Core methods do not acquire
// g's concurrentAccessGuard: they are called by Txn and graphCoreReader,
// which only ever run inside a single already-guarded Graph.Transact
// call, so that this same-goroutine nesting is never mistaken for
// genuine cross-goroutine overlap. Every query method has an exported,
// guarded form and a *Core counterpart following this same split for the
// same reason (see the concurrentAccessGuard doc comment).
func (g *Graph) createNodeCore() (NodeID, error) {
	g.ensureInitialized()

	if g.exhausted {
		return 0, ErrNodeIDExhausted
	}

	id := g.nextID

	g.nodes[id] = struct{}{}
	g.outgoing[id] = make(map[NodeID]struct{})
	g.incoming[id] = make(map[NodeID]struct{})

	if id == ^NodeID(0) {
		g.exhausted = true
	} else {
		g.nextID++
	}

	return id, nil
}

// NodeExists reports whether id currently identifies an existing node.
//
// This acquires g's concurrentAccessGuard for the duration of the call;
// see that type's doc comment. The unexported nodeExists already serves
// as this method's unguarded core, called directly by every other
// *Graph method and by graphCoreReader/Txn. The error return exists to
// satisfy GraphReader; a bare *Graph has no failure mode here and always
// returns nil (see the GraphReader doc comment).
func (g *Graph) NodeExists(id NodeID) (bool, error) {
	release := g.guard.acquire()
	defer release()

	return g.nodeExists(id), nil
}

// addRelationshipCore creates the primitive relationship (a, b). Both
// nodes must already exist. Relationships are unique: adding the same
// relationship again simply reports created=false. See createNodeCore
// for why this is unexported and unguarded, and reachable only through
// Txn.
func (g *Graph) addRelationshipCore(a, b NodeID) (created bool, err error) {
	g.ensureInitialized()

	if !g.nodeExists(a) {
		return false, ErrNodeNotFound
	}
	if !g.nodeExists(b) {
		return false, ErrNodeNotFound
	}

	if _, exists := g.outgoing[a][b]; exists {
		return false, nil
	}

	g.outgoing[a][b] = struct{}{}
	g.incoming[b][a] = struct{}{}

	return true, nil
}

// removeRelationshipCore removes the primitive relationship (a, b). The
// returned bool reports whether a relationship actually existed and was
// removed. See createNodeCore for why this is unexported and unguarded,
// and reachable only through Txn.
func (g *Graph) removeRelationshipCore(a, b NodeID) (removed bool, err error) {
	if !g.nodeExists(a) {
		return false, ErrNodeNotFound
	}
	if !g.nodeExists(b) {
		return false, ErrNodeNotFound
	}

	if _, exists := g.outgoing[a][b]; !exists {
		return false, nil
	}

	delete(g.outgoing[a], b)
	delete(g.incoming[b], a)

	return true, nil
}

// HasRelationship reports whether the primitive relationship (a, b) exists.
//
// This acquires g's concurrentAccessGuard for the duration of the call;
// see hasRelationshipCore for the actual, unguarded implementation. The
// error return exists to satisfy GraphReader; a bare *Graph has no
// failure mode here and always returns nil (see the GraphReader doc
// comment).
func (g *Graph) HasRelationship(a, b NodeID) (bool, error) {
	release := g.guard.acquire()
	defer release()

	return g.hasRelationshipCore(a, b), nil
}

// hasRelationshipCore is HasRelationship's unguarded implementation; see
// createNodeCore's doc comment for why this split exists and who calls
// it directly.
func (g *Graph) hasRelationshipCore(a, b NodeID) bool {
	if !g.nodeExists(a) || !g.nodeExists(b) {
		return false
	}

	_, exists := g.outgoing[a][b]
	return exists
}

// FindRelationship reports whether the exact primitive relationship (from, to)
// exists.
//
// This acquires g's concurrentAccessGuard for the duration of the call;
// see findRelationshipCore for the actual, unguarded implementation.
func (g *Graph) FindRelationship(from, to NodeID) (Relationship, bool, error) {
	release := g.guard.acquire()
	defer release()

	return g.findRelationshipCore(from, to)
}

// findRelationshipCore is FindRelationship's unguarded implementation;
// see createNodeCore's doc comment for why this split exists and who
// calls it directly.
func (g *Graph) findRelationshipCore(from, to NodeID) (Relationship, bool, error) {
	if !g.nodeExists(from) {
		return Relationship{}, false, ErrNodeNotFound
	}

	if !g.nodeExists(to) {
		return Relationship{}, false, ErrNodeNotFound
	}

	if _, exists := g.outgoing[from][to]; !exists {
		return Relationship{}, false, nil
	}

	return Relationship{
		From: from,
		To:   to,
	}, true, nil
}

// FindOutgoing returns all primitive relationships whose source is from.
//
// In other words, it finds every X for which (from, X) exists.
//
// This acquires g's concurrentAccessGuard for the duration of the call;
// see findOutgoingCore for the actual, unguarded implementation.
func (g *Graph) FindOutgoing(from NodeID) ([]Relationship, error) {
	release := g.guard.acquire()
	defer release()

	return g.findOutgoingCore(from)
}

// findOutgoingCore is FindOutgoing's unguarded implementation; see
// createNodeCore's doc comment for why this split exists and who calls
// it directly.
func (g *Graph) findOutgoingCore(from NodeID) ([]Relationship, error) {
	if !g.nodeExists(from) {
		return nil, ErrNodeNotFound
	}

	relationships := make([]Relationship, 0, len(g.outgoing[from]))

	for to := range g.outgoing[from] {
		relationships = append(relationships, Relationship{
			From: from,
			To:   to,
		})
	}

	sort.Slice(relationships, func(i, j int) bool {
		return relationships[i].To < relationships[j].To
	})

	return relationships, nil
}

// FindIncoming returns all primitive relationships whose target is to.
//
// In other words, it finds every X for which (X, to) exists.
//
// This acquires g's concurrentAccessGuard for the duration of the call;
// see findIncomingCore for the actual, unguarded implementation.
func (g *Graph) FindIncoming(to NodeID) ([]Relationship, error) {
	release := g.guard.acquire()
	defer release()

	return g.findIncomingCore(to)
}

// findIncomingCore is FindIncoming's unguarded implementation; see
// createNodeCore's doc comment for why this split exists and who calls
// it directly.
func (g *Graph) findIncomingCore(to NodeID) ([]Relationship, error) {
	if !g.nodeExists(to) {
		return nil, ErrNodeNotFound
	}

	relationships := make([]Relationship, 0, len(g.incoming[to]))

	for from := range g.incoming[to] {
		relationships = append(relationships, Relationship{
			From: from,
			To:   to,
		})
	}

	sort.Slice(relationships, func(i, j int) bool {
		return relationships[i].From < relationships[j].From
	})

	return relationships, nil
}

// FindRelationships returns every primitive relationship in the graph.
//
// The returned relationships are sorted by From, then To. This ordering
// has no semantic meaning.
//
// This acquires g's concurrentAccessGuard for the duration of the call;
// see findRelationshipsCore for the actual, unguarded implementation. The
// error return exists to satisfy GraphReader; a bare *Graph has no
// failure mode here and always returns nil (see the GraphReader doc
// comment).
func (g *Graph) FindRelationships() ([]Relationship, error) {
	release := g.guard.acquire()
	defer release()

	return g.findRelationshipsCore(), nil
}

// findRelationshipsCore is FindRelationships's unguarded implementation;
// see createNodeCore's doc comment for why this split exists and who
// calls it directly.
func (g *Graph) findRelationshipsCore() []Relationship {
	total := 0
	for _, targets := range g.outgoing {
		total += len(targets)
	}

	relationships := make([]Relationship, 0, total)

	for from, targets := range g.outgoing {
		for to := range targets {
			relationships = append(relationships, Relationship{
				From: from,
				To:   to,
			})
		}
	}

	sort.Slice(relationships, func(i, j int) bool {
		if relationships[i].From != relationships[j].From {
			return relationships[i].From < relationships[j].From
		}
		return relationships[i].To < relationships[j].To
	})

	return relationships
}

// FindNodes returns the NodeID of every existing node, sorted ascending.
// The ordering has no semantic meaning.
//
// This acquires g's concurrentAccessGuard for the duration of the call;
// see findNodesCore for the actual, unguarded implementation. The error
// return exists to satisfy GraphReader; a bare *Graph has no failure mode
// here and always returns nil (see the GraphReader doc comment).
func (g *Graph) FindNodes() ([]NodeID, error) {
	release := g.guard.acquire()
	defer release()

	return g.findNodesCore(), nil
}

// findNodesCore is FindNodes's unguarded implementation; see
// createNodeCore's doc comment for why this split exists and who calls
// it directly.
func (g *Graph) findNodesCore() []NodeID {
	ids := make([]NodeID, 0, len(g.nodes))

	for id := range g.nodes {
		ids = append(ids, id)
	}

	sort.Slice(ids, func(i, j int) bool {
		return ids[i] < ids[j]
	})

	return ids
}

// deleteNodeCore deletes a node only when it has no relationships.
// Cascade deletion is deliberately not part of this primitive API. See
// createNodeCore for why this is unexported and unguarded, and reachable
// only through Txn.
func (g *Graph) deleteNodeCore(id NodeID) error {
	if !g.nodeExists(id) {
		return ErrNodeNotFound
	}

	if len(g.outgoing[id]) != 0 || len(g.incoming[id]) != 0 {
		return ErrNodeNotEmpty
	}

	delete(g.nodes, id)
	delete(g.outgoing, id)
	delete(g.incoming, id)

	return nil
}

// resurrectNode re-inserts id into the graph with no relationships,
// without consuming a new ID from the monotonic counter.
//
// This is an internal, low-level helper used only by Txn.DeleteNode's
// rollback path. It is safe -- not merely convenient -- because of two
// facts holding together: (1) Graph.DeleteNode only ever succeeds when
// id already has zero relationships in both directions, so "existing,
// with empty outgoing/incoming maps" is a *complete* restoration of id's
// prior state, not merely a partial one; and (2) NodeIDs are never
// reused once handed out by CreateNode (the counter only increases, and
// a deleted id is never returned to any pool of ids available for
// reuse), so resurrecting id can never collide with some unrelated node
// that might have taken over the same id in the meantime -- no such
// takeover is possible. Do not call this for any purpose other than
// undoing a Txn-recorded DeleteNode.
func (g *Graph) resurrectNode(id NodeID) {
	g.ensureInitialized()
	g.nodes[id] = struct{}{}
	g.outgoing[id] = make(map[NodeID]struct{})
	g.incoming[id] = make(map[NodeID]struct{})
}

func (g *Graph) ensureInitialized() {
	if g.nodes == nil {
		g.nodes = make(map[NodeID]struct{})
	}
	if g.outgoing == nil {
		g.outgoing = make(map[NodeID]map[NodeID]struct{})
	}
	if g.incoming == nil {
		g.incoming = make(map[NodeID]map[NodeID]struct{})
	}
}

func (g *Graph) nodeExists(id NodeID) bool {
	_, exists := g.nodes[id]
	return exists
}

// GraphReader is the read-only query surface shared by every storage
// backend. It exists so that helper functions and Checkers which only
// ever need to read graph state -- never create, tag, or delete
// anything -- can depend on exactly that capability rather than a
// concrete storage type, or the wider GraphStore/GraphAPI surfaces below
// that also grant write access (theorystate.md section 87).
//
// NodeExists, HasRelationship, FindRelationships, and FindNodes each
// return an error alongside their result. An earlier version of this
// interface gave them no way to fail at all, which was modeled on the
// in-memory *Graph -- for which none of these four queries can genuinely
// fail -- and broke the moment BoltGraph (a disk-backed backend,
// theorystate.md section 108) needed to report a real read failure
// instead of panicking. Every backend's own implementation of these four
// still returns a nil error in the overwhelming majority of cases -- in
// particular, always, for every purely in-memory-backed reader in this
// file (*Graph, *Txn, graphCoreReader, *GraphActor). ErrGraphStoreUnavailable
// is reserved for a backend whose read channel itself failed, as opposed
// to a request that is merely invalid (ErrNodeNotFound) or content that
// is present but violates this package's own layout (ErrStoreCorrupt) --
// see ErrGraphStoreUnavailable's own doc comment for that distinction.
type GraphReader interface {
	NodeExists(id NodeID) (bool, error)
	HasRelationship(a, b NodeID) (bool, error)
	FindRelationship(from, to NodeID) (Relationship, bool, error)
	FindOutgoing(from NodeID) ([]Relationship, error)
	FindIncoming(to NodeID) ([]Relationship, error)
	FindRelationships() ([]Relationship, error)

	// FindNodes returns the NodeID of every node that currently exists,
	// sorted ascending. The order has no semantic meaning (theorystate.md
	// section 5); it exists only so callers get deterministic output.
	// This is the node-enumeration counterpart of FindRelationships, and
	// is what lets RootGraph's ROOT overlay run over any GraphStore
	// rather than only the concrete *Graph (theorystate.md section 87b).
	FindNodes() ([]NodeID, error)
}

// GraphStore is the complete primitive storage surface -- GraphReader's
// queries plus the mutating operations. It is reachable only through Tx,
// the handle a Transact closure receives: GraphAPI deliberately does not
// include it, so a graph can be mutated only inside a transaction, where
// commit-time Checkers and commit hooks apply (theorystate.md section
// 92). This refines sections 87/87a, which extracted this surface from
// Graph's then-public method set; the concrete *Graph no longer exports
// the mutating methods at all. It is also the surface a future
// non-in-memory backend would provide to its transaction handle.
type GraphStore interface {
	GraphReader
	CreateNode() (NodeID, error)
	AddRelationship(a, b NodeID) (created bool, err error)
	RemoveRelationship(a, b NodeID) (removed bool, err error)
	DeleteNode(id NodeID) error
}

// GraphAPI is the root handle of a graph: GraphReader's queries, plus
// Transactor (the only way to mutate anything) and RegisterChecker every
// registry constructor needs. It deliberately does NOT include
// GraphStore's mutating operations: those are reachable only through the
// Tx a Transact closure receives, so no mutation can bypass commit-time
// Checkers or commit hooks (theorystate.md section 92). Reads are
// allowed outside a transaction (each is a snapshot; a decision based on
// one must still re-read inside the transaction, see the Transactor
// contract). The transactional contract is deliberately not assumed to
// be satisfied the same way by every backend (theorystate.md section
// 87a/89a).
//
// Every registry constructor in this file (NewPointerRegistry,
// NewCapsuleRegistry, NewListRegistry, and so on) takes a GraphAPI
// rather than a concrete *Graph, so any future GraphAPI implementation
// can be substituted with no change to registry logic. *Graph already
// satisfies GraphAPI exactly as defined below, with no changes to Graph
// itself -- this is a pure decoupling refactor (theorystate.md section
// 87).
//
// RootGraph is not an exception either: it implements GraphAPI itself,
// as a decorator over any other GraphAPI (using GraphReader.FindNodes to
// enumerate nodes), so it can stand in for a Graph anywhere a registry or
// GraphActor takes one (theorystate.md section 87b).
type GraphAPI interface {
	GraphReader
	Transactor

	// RegisterChecker registers c to be consulted after every future
	// outermost transaction whose changeset could be relevant to it (see
	// Checker).
	RegisterChecker(c Checker)
}

// Transactor is the ability to run a function as one atomic unit. Both
// GraphAPI and Tx provide it: on a GraphAPI, Transact opens an outermost
// transaction; on a Tx, it opens a nested transaction inside the one
// already in progress (theorystate.md section 45). Registry mutators take
// a Transactor, so one exported method works standalone and also composes
// inside a larger transaction, with no separate tx-composable core.
type Transactor interface {
	// Transact runs fn as one atomic unit: if fn returns an error or
	// panics, every mutation made through tx is undone. For an outermost
	// transaction the same happens if a relevant Checker declines the
	// result.
	//
	// Contract every fn must honour (theorystate.md section 91):
	//   - fn may be executed more than once, and against a state that
	//     differs from the one the caller saw earlier (a retrying backend
	//     re-runs it after a conflict). Nothing fn does may depend on
	//     having run exactly once.
	//   - fn must read every piece of graph state its decisions depend on
	//     through tx, never through a graph value captured from outside;
	//     a read made before Transact can be stale by the time fn runs.
	//   - fn must have no side effects outside tx. State kept outside the
	//     graph (NameRegistry's maps) is updated with tx.OnCommit, never
	//     directly from fn; if later steps of the same transaction must
	//     already see the change, stage it and undo the staging with
	//     tx.OnRollback (see NameRegistry.Bind).
	//   - fn may open a nested transaction only through the tx it was
	//     given, never through another graph handle (on a GraphActor that
	//     would deadlock).
	//
	// Nested transactions (Transact called on a Tx):
	//   - They are savepoints inside the enclosing transaction. If the
	//     nested fn fails or panics, only what it did is undone, including
	//     the OnCommit hooks it registered; the enclosing fn may handle the
	//     returned error and carry on.
	//   - Checkers run once, at the outermost commit, over the whole
	//     changeset, because they judge the final state and an
	//     intermediate one may legitimately violate an invariant that a
	//     later step repairs. A nested call that returns nil is therefore
	//     provisional until the outermost Transact returns nil.
	Transact(fn func(tx Tx) error) error
}

// Compile-time assertion that *Graph satisfies GraphAPI, so any future
// accidental signature drift between Graph's methods and this interface
// is caught at build time rather than only at some call site far away.
var _ GraphAPI = (*Graph)(nil)

// Tx is the handle a Transact closure works through: GraphStore's full
// read/write surface, scoped to one transaction, plus OnCommit and nested
// Transact (via Transactor). *Txn is the concrete implementation for the
// in-memory Graph. Transact takes this interface, not the concrete *Txn,
// so that a layer decorating a GraphAPI -- most importantly RootGraph --
// can hand the closure a handle presenting the same overlaid view inside
// the transaction as outside it (theorystate.md section 87b). Every
// helper in this file already takes the narrower txOps/txReader, which Tx
// satisfies.
type Tx interface {
	GraphStore
	Transactor

	// OnCommit registers fn to run exactly once, after this
	// transaction's mutations have been applied and every relevant
	// Checker has approved, and before Transact returns. If the
	// transaction is rolled back for any reason, fn is discarded and
	// never runs. Hooks run in registration order, on the goroutine (and
	// under the exclusive access) that runs Transact, so a hook may
	// safely update caller-owned state that must stay in step with the
	// graph, such as NameRegistry's maps. A hook must not call back into
	// the graph and must not panic.
	OnCommit(fn func())

	// OnRollback registers fn to run if this transaction, or the nested
	// transaction in progress when it is registered, is rolled back (an
	// error, a panic, or a Checker declining the commit). Hooks run in
	// reverse registration order, interleaved with the undoing of the
	// graph mutations made around them, so the graph and the state the
	// hooks maintain are rolled back in step. If the outermost
	// transaction commits, the hook is discarded and never runs.
	// Together with OnCommit this lets a caller keep state outside the
	// graph that later steps of the same transaction can already see,
	// without publishing it to other readers before commit (see
	// NameRegistry). A hook must not call back into the graph and must
	// not panic.
	OnRollback(fn func())

	// Touch marks ids as touched by this transaction without changing
	// anything, so commit-time Checkers whose relevance filter matches them
	// run at the outermost commit exactly as if the transaction had modified
	// them. It is what lets a startup sweep (VerifyAll, theorystate.md
	// section 104) make Checkers re-validate nodes nothing has changed.
	Touch(ids ...NodeID)
}

// Compile-time assertion that *Txn satisfies Tx.
var _ Tx = (*Txn)(nil)

// Txn groups a sequence of primitive Graph mutations so that, if the
// function passed to Graph.Transact returns a non-nil error or panics,
// every mutation performed through tx during that call is undone, in
// reverse order, before the error (or panic) propagates to the caller.
//
// Txn exists to close a real gap: several higher-level operations
// elsewhere in this file are naturally multi-step (NameRegistry.
// CreateNamedNode is CreateNode-then-Bind; PointerRegistry.SetTarget's
// replace path is RemoveRelationship-then-AddRelationship;
// PointerRegistry.NewPointer is CreateNode-then-AddRelationship). Before
// Txn existed, each step committed immediately and unconditionally, so a
// later step failing after an earlier step had already succeeded left
// permanently orphaned or inconsistent state -- for example, a node
// created but never named, or a Pointer left with no target at all
// because its old target was removed before the new one could be added.
// Txn's undo log makes each such sequence atomic with respect to failure.
//
// Txn does not itself provide isolation; isolation comes from whatever
// owns the *Graph. A bare *Graph admits one goroutine at a time
// (concurrentAccessGuard panics on detected overlap, theorystate.md
// section 89b), and GraphActor runs every transaction on one dedicated
// goroutine (theorystate.md section 89c), so nothing can observe a Txn's
// intermediate state mid-sequence. A backend with genuinely concurrent
// writers needs its own mechanism for the Transact contract
// (theorystate.md section 89a) instead of this undo log.
//
// Txn also does NOT provide durability/crash-atomicity: it undoes
// failures inside a running process only, and a crash mid-transaction
// loses the whole in-memory graph anyway. Durability belongs to a
// persistent backend, which gets it from its own commit (BoltGraph: one
// bbolt update transaction per Transact).
//
// Txn is intentionally not a staged/copy-on-write view of the graph
// (theorystate.md section 15's "transaction overlay" idea). Each Txn
// method applies its mutation directly to the real underlying Graph and
// simply records how to undo it; this is significantly simpler than a
// full overlay and is sufficient because, per the isolation point above,
// there is currently no concurrent reader that a staged view would need
// to protect from seeing uncommitted state.
//
// Txn's mutating surface covers CreateNode, AddRelationship,
// RemoveRelationship, and DeleteNode. An earlier version of this comment
// claimed DeleteNode could not be supported transactionally because
// undoing it would require "resurrecting the exact same NodeID outside
// the normal monotonic counter" -- that claim was wrong, not merely
// cautious: NodeIDs in this implementation are never reused once handed
// out by CreateNode (the counter only ever increases; a deleted id is
// never returned to a pool of ids available for reuse), and
// Graph.DeleteNode only ever succeeds when the node already has zero
// relationships in both directions. Put together, undoing a DeleteNode
// never needs to reconstruct any relationship state at all -- it only
// ever needs to restore "id exists, with empty relationship maps",
// which is a complete and exact restoration of id's state immediately
// before the delete, and can never collide with some other node having
// taken over id in the meantime, since that can't happen. See
// Graph.resurrectNode and Txn.DeleteNode below.
//
// Txn also implements GraphReader (see the read methods below), so
// helpers read current state through the very same tx they write
// through; that is what keeps a decision and its write on one state.
//
// Nested transactions are supported through Txn.Transact (part of the Tx
// interface, so tx.Transact(fn) inside a closure). A nested transaction
// is a savepoint in the enclosing one, not a transaction of its own: it
// shares tx's undo log and commit-hook list and merely remembers where
// they stood on entry (txMark). If the nested fn fails or panics,
// everything done since that point is undone and every OnCommit hook
// registered since then is discarded; the enclosing closure may handle the
// error and carry on. If it succeeds, nothing more happens and its
// effects stand or fall with the outermost transaction. Checkers run
// only once, at the outermost commit, over the whole changeset, because
// they judge the final state (theorystate.md section 45). An inner
// success is therefore provisional until the outermost Graph.Transact
// returns nil.
type Txn struct {
	graph *Graph

	// txLog is this transaction's undo log and commit-hook list (see
	// txLog). Graph.Transact runs the commit hooks only once the
	// transaction has fully succeeded.
	txLog

	// touched records every NodeID this Txn's mutations have involved so
	// far -- as an endpoint of an added or removed relationship, or as a
	// created or deleted node, or explicitly via Touch -- so
	// Graph.Transact can hand it to any relevant Checker once fn returns
	// successfully. A relationship add/remove that turned out to be a
	// no-op (already existed / never existed) is deliberately not recorded
	// by the mutation itself, mirroring undo's own "only record what
	// actually changed" discipline.
	touched map[NodeID]struct{}
}

// Touch marks every one of ids as involved in this transaction, exactly as
// if a mutation had changed it, without changing anything. Every effective
// mutation calls it for the nodes it changed; VerifyAll (theorystate.md
// section 104) calls it to make Checkers re-validate untouched nodes. See
// the touched field doc comment above.
func (tx *Txn) Touch(ids ...NodeID) {
	tx.touched = touchNodes(tx.touched, ids...)
}

// touchNodes adds ids to set and returns it, allocating the set on first
// use. It is the one implementation of Tx.Touch's bookkeeping, shared by
// every Tx implementation that records a touched set (Txn, the test-only
// stagedOverlay, and BoltGraph's boltTxn).
func touchNodes(set map[NodeID]struct{}, ids ...NodeID) map[NodeID]struct{} {
	if set == nil {
		set = make(map[NodeID]struct{}, len(ids))
	}

	for _, id := range ids {
		set[id] = struct{}{}
	}

	return set
}

// Transact runs fn against a fresh Txn wrapping g. If fn returns a
// non-nil error, every mutation fn performed through tx is undone, in
// reverse order, and that same error is returned. If fn panics, the same
// undo happens before the panic is re-raised, so a panicking caller does
// not leave g in a partially mutated state either.
//
// If fn returns nil, Transact does not yet report success: it first runs
// every registered Checker that could plausibly be relevant to what fn
// touched (see the Checker type and runCheckers below). If a relevant
// Checker declines, its error is treated exactly like an error returned
// by fn itself -- every mutation fn performed is undone, in reverse
// order, and the Checker's (wrapped) error is returned instead of nil.
// Only once every relevant Checker has approved does Transact run any
// commit hooks registered via tx.OnCommit (see Tx) and return nil; on any
// failure the hooks are discarded unrun.
//
// Because every Txn method already applies its mutation directly to g as
// it happens, there is still no separate "staged" commit step -- a
// Checker runs against the real, already-mutated Graph, never a partial
// or overlay view. This is sound, not merely convenient, under the
// current single-threaded execution model (theorystate.md section 19):
// nothing else can observe the already-mutated-but-not-yet-checked
// intermediate state, since nothing else runs between the mutation
// completing and Check running, in the same synchronous call. See the
// Checker type's own doc comment for the fuller reasoning, including why
// a staged/overlay view (theorystate.md section 77's original proposal)
// is deferred rather than needed here.
//
// Transact also acquires g's concurrentAccessGuard (theorystate.md
// section 89b) for its entire duration -- including running fn and every
// relevant Checker -- rather than per sub-step, so the whole call is
// treated as one atomic unit from the guard's perspective. tx's own
// methods, and every Checker's Check function, read and write through
// Graph's unguarded core methods directly rather than through the
// guarded public API, so this single acquisition is never re-entered by
// anything Transact itself calls.
func (g *Graph) Transact(fn func(tx Tx) error) (err error) {
	release := g.guard.acquire()
	defer release()

	tx := &Txn{graph: g}

	defer func() {
		if r := recover(); r != nil {
			tx.rollback()
			panic(r)
		}
	}()

	err = fn(tx)
	if err != nil {
		tx.rollback()
		return err
	}

	if err2 := g.runCheckers(tx.touched); err2 != nil {
		tx.rollback()
		return err2
	}

	tx.runCommitHooks()

	return nil
}

// txMark records how long a transaction's undo log and commit-hook list
// were at some point, so a nested transaction can later be rolled back to
// exactly that point.
type txMark struct {
	undo  int
	hooks int
}

// txLog is the undo log and commit-hook list shared by every Tx
// implementation that applies its mutations as it goes and undoes them on
// failure: Txn, the test-only stagedOverlay, and BoltGraph's boltTxn. The
// embedding type appends one closure to undo per effective mutation, and
// gets OnCommit, OnRollback, nested savepoints (runNested) and rollback
// from here, so those exist once (theorystate.md sections 45, 91).
type txLog struct {
	undo        []func()
	commitHooks []func()
}

// mark returns the log's current position, for rollbackTo.
func (l *txLog) mark() txMark {
	return txMark{undo: len(l.undo), hooks: len(l.commitHooks)}
}

// rollback undoes everything recorded so far. See rollbackTo.
func (l *txLog) rollback() {
	l.rollbackTo(txMark{})
}

// rollbackTo undoes, in reverse (LIFO) order, every step recorded since m
// was taken, and discards every commit hook registered since then. The
// zero txMark rolls back the whole transaction. Reverse order matters: if a
// transaction created a node and then added a relationship from it,
// undoing the relationship first leaves the node empty, so undoing the
// node's creation afterward satisfies DeleteNode's no-relationships
// precondition (see the Graph.DeleteNode doc comment).
//
// The embedding type's touched set is deliberately not rewound: it stays a
// conservative superset of what the transaction changed. A Checker given a
// node whose change was rolled back simply re-validates a node that is
// already valid, and every Checker in this file ignores nodes that no
// longer exist.
func (l *txLog) rollbackTo(m txMark) {
	for i := len(l.undo) - 1; i >= m.undo; i-- {
		l.undo[i]()
	}

	clear(l.undo[m.undo:])
	l.undo = l.undo[:m.undo]

	clear(l.commitHooks[m.hooks:])
	l.commitHooks = l.commitHooks[:m.hooks]
}

// OnCommit implements Tx.OnCommit: fn runs once, after every Checker has
// approved (and, for a durable backend, after the commit), and is
// discarded if the transaction rolls back.
func (l *txLog) OnCommit(fn func()) {
	l.commitHooks = append(l.commitHooks, fn)
}

// OnRollback implements Tx.OnRollback. The hook is recorded in the undo
// log, so it runs in LIFO order with the graph mutations around it when
// the transaction -- or the nested transaction it was registered in -- is
// rolled back, and is simply dropped when the outermost transaction
// commits.
func (l *txLog) OnRollback(fn func()) {
	l.undo = append(l.undo, fn)
}

// runCommitHooks runs and clears every registered commit hook, in
// registration order. The backend calls it only after fn, every relevant
// Checker and (where there is one) the durable commit have succeeded.
func (l *txLog) runCommitHooks() {
	hooks := l.commitHooks
	l.commitHooks = nil

	for _, hook := range hooks {
		hook()
	}
}

// runNested runs fn as a nested transaction, a savepoint inside the
// enclosing one, handing it tx (the embedding type, which shares this
// log). If fn returns an error or panics, everything done since the call
// and every commit hook registered since then is undone; the enclosing
// closure may handle the error and carry on. If fn succeeds nothing more
// happens: Checkers run once, at the outermost commit, and the effects are
// provisional until then (theorystate.md section 45).
func (l *txLog) runNested(tx Tx, fn func(nested Tx) error) error {
	mark := l.mark()

	defer func() {
		if r := recover(); r != nil {
			l.rollbackTo(mark)
			panic(r)
		}
	}()

	if err := fn(tx); err != nil {
		l.rollbackTo(mark)
		return err
	}

	return nil
}

// Transact implements Transactor for a transaction already in progress:
// it runs fn as a nested transaction, a savepoint inside tx. fn receives
// tx itself, since the nested transaction shares tx's state.
//
// If fn returns an error, everything fn did through tx and every commit
// hook it registered is undone or discarded, and the error is returned;
// the enclosing closure may handle it and continue. If fn panics, the
// same rollback happens before the panic is re-raised, so an enclosing
// closure that recovers is left with state as it was before this call. If
// fn succeeds, nothing more happens: no Checker runs here (they run once,
// at the outermost commit) and the effects are provisional until then.
func (tx *Txn) Transact(fn func(nested Tx) error) error {
	return tx.runNested(tx, fn)
}

// CreateNode behaves exactly like Graph.CreateNode, additionally
// recording an undo step that deletes the new node again if the
// enclosing transaction rolls back.
func (tx *Txn) CreateNode() (NodeID, error) {
	id, err := tx.graph.createNodeCore()
	if err != nil {
		return 0, err
	}

	tx.Touch(id)

	tx.undo = append(tx.undo, func() {
		// By the time this runs (see rollback's LIFO ordering), any
		// relationships involving id that this same transaction added
		// have already been undone, so id should be empty and this
		// delete should succeed. If something outside this transaction
		// mutated id in the meantime -- which nothing in this file
		// currently does -- this best-effort delete may fail; that
		// failure is deliberately swallowed here since Txn's rollback
		// has no error return of its own to report it through, and a
		// caller misusing Txn this way is a bug in the caller, not
		// something Txn can prevent by construction.
		//
		// This calls the unguarded core, not the public CreateNode/
		// DeleteNode methods, since this closure only ever runs from
		// inside rollback, itself only ever called from within a single
		// already-guarded Graph.Transact call -- see the
		// concurrentAccessGuard doc comment.
		if err := tx.graph.deleteNodeCore(id); err != nil {
			_ = err
		}
	})

	return id, nil
}

// AddRelationship behaves exactly like Graph.AddRelationship,
// additionally recording an undo step that removes the relationship
// again if the enclosing transaction rolls back -- but only if this call
// actually created it. If (a,b) already existed before this call
// (created == false), there is nothing for this call to undo: the
// relationship was not this transaction's to remove.
func (tx *Txn) AddRelationship(a, b NodeID) (created bool, err error) {
	created, err = tx.graph.addRelationshipCore(a, b)
	if err != nil {
		return false, err
	}

	if created {
		tx.Touch(a, b)

		tx.undo = append(tx.undo, func() {
			// Best-effort: deliberately swallowed, mirroring
			// Txn.CreateNode's undo closure above. Calls the unguarded
			// core for the same reason given there.
			if _, err := tx.graph.removeRelationshipCore(a, b); err != nil {
				_ = err
			}
		})
	}

	return created, nil
}

// RemoveRelationship behaves exactly like Graph.RemoveRelationship,
// additionally recording an undo step that re-adds the relationship
// again if the enclosing transaction rolls back -- but only if this call
// actually removed it, symmetric with AddRelationship above.
func (tx *Txn) RemoveRelationship(a, b NodeID) (removed bool, err error) {
	removed, err = tx.graph.removeRelationshipCore(a, b)
	if err != nil {
		return false, err
	}

	if removed {
		tx.Touch(a, b)

		tx.undo = append(tx.undo, func() {
			// Best-effort: deliberately swallowed, mirroring
			// Txn.CreateNode's undo closure above. Calls the unguarded
			// core for the same reason given there.
			if _, err := tx.graph.addRelationshipCore(a, b); err != nil {
				_ = err
			}
		})
	}

	return removed, nil
}

// DeleteNode behaves exactly like Graph.DeleteNode, additionally
// recording an undo step that resurrects id -- exactly as it was
// immediately before this call -- if the enclosing transaction rolls
// back. See Graph.resurrectNode and the Txn doc comment above for why
// this is a safe, complete restoration and not a workaround: id had
// zero relationships in both directions immediately before this call
// (that is Graph.DeleteNode's own precondition for succeeding), and
// NodeIDs are never reused, so there is nothing more to restore and no
// possibility of id having been claimed by an unrelated node in the
// meantime.
//
// This is what lets a caller compose several DeleteNode calls into one
// logical multi-node teardown inside a single Graph.Transact call (see
// CapsuleRegistry.DeleteCapsule) without first having to prove every
// node's emptiness ahead of time: if a later DeleteNode call in the
// sequence fails, Transact's normal LIFO rollback undoes every earlier
// step, including any DeleteNode calls that had already succeeded
// earlier in that same sequence, exactly like it already does for
// AddRelationship/RemoveRelationship/CreateNode.
func (tx *Txn) DeleteNode(id NodeID) error {
	if err := tx.graph.deleteNodeCore(id); err != nil {
		return err
	}

	tx.Touch(id)

	tx.undo = append(tx.undo, func() {
		tx.graph.resurrectNode(id)
	})

	return nil
}

// NodeExists , HasRelationship, FindRelationship, FindOutgoing,
// FindIncoming, FindRelationships, and FindNodes below make *Txn satisfy
// GraphReader, delegating directly to the real, concrete *Graph this Txn
// is running against -- specifically, to its unexported, unguarded core
// methods, never to its guarded public ones. Every Txn method exists
// only while a single Graph.Transact call already holds that Graph's
// concurrentAccessGuard for the call's entire duration (theorystate.md
// section 89b); calling back into the guarded public API from here would
// incorrectly panic as if a second goroutine had raced in, even though
// it is the same goroutine legitimately still inside its one enclosing
// Transact call.
//
// These exist so tx-composable helpers (the *Tx-suffixed functions
// throughout this file) can read current state through the same tx value
// they already use for writes, instead of needing a second, separately
// threaded GraphReader parameter that a caller could accidentally supply
// from some other source -- in particular, from a registry's own stored
// graph reference. Reading through a stored reference instead of tx is
// exactly the bug class theorystate.md section 90 records: under a plain
// *Graph it is harmless, since tx.graph and the stored reference are the
// same value, but under GraphActor a stored reference may itself be the
// GraphActor, and calling back into it from inside a closure already
// running on the actor's one dedicated goroutine deadlocks. tx.NodeExists
// and friends give every helper a value that is always correct for both
// cases: the real *Graph, directly, with no channel involved at all.
func (tx *Txn) NodeExists(id NodeID) (bool, error) {
	return tx.graph.nodeExists(id), nil
}

// HasRelationship delegates to the real, concrete *Graph's unguarded
// core. See the NodeExists doc comment above.
func (tx *Txn) HasRelationship(a, b NodeID) (bool, error) {
	return tx.graph.hasRelationshipCore(a, b), nil
}

// FindRelationship delegates to the real, concrete *Graph's unguarded
// core. See the NodeExists doc comment above.
func (tx *Txn) FindRelationship(from, to NodeID) (Relationship, bool, error) {
	return tx.graph.findRelationshipCore(from, to)
}

// FindOutgoing delegates to the real, concrete *Graph's unguarded core.
// See the NodeExists doc comment above.
func (tx *Txn) FindOutgoing(from NodeID) ([]Relationship, error) {
	return tx.graph.findOutgoingCore(from)
}

// FindIncoming delegates to the real, concrete *Graph's unguarded core.
// See the NodeExists doc comment above.
func (tx *Txn) FindIncoming(to NodeID) ([]Relationship, error) {
	return tx.graph.findIncomingCore(to)
}

// FindRelationships delegates to the real, concrete *Graph's unguarded
// core. See the NodeExists doc comment above.
func (tx *Txn) FindRelationships() ([]Relationship, error) {
	return tx.graph.findRelationshipsCore(), nil
}

// FindNodes delegates to the real, concrete *Graph's unguarded core.
// See the NodeExists doc comment above.
func (tx *Txn) FindNodes() ([]NodeID, error) {
	return tx.graph.findNodesCore(), nil
}

// Compile-time assertion that *Txn satisfies GraphReader, exactly
// mirroring the existing assertions for *Graph and *GraphActor against
// GraphAPI.
var _ GraphReader = (*Txn)(nil)

// Checker validates one domain-specific invariant against the graph
// immediately after a Transact call's mutations have been fully
// applied, before Transact reports success to its own caller. This is
// the commit-time counterpart to the "always re-derive and re-check on
// read, never cache" discipline every registry in this file otherwise
// relies on (see e.g. the PointerRegistry doc comment): a Checker lets a
// violated invariant be caught and rolled back immediately, at the
// moment it is introduced, rather than only the next time some
// registry's own method happens to read the affected node
// (theorystate.md sections 73/77).
//
// Check's contract is stated backend-neutrally, since more than one
// GraphAPI implementation exists in this file (theorystate.md section
// 95): Check observes the state fn's mutations would produce, as of the
// moment fn reports success -- never a state older than that, and never
// one that depends on anything that happens after this call returns.
// For *Graph specifically, this is the real, already-mutated Graph
// itself, because nothing else can observe the intermediate state under
// the current single-threaded execution model (theorystate.md section
// 19); see Graph.Transact's own doc comment for that backend-specific
// soundness argument, including why a staged/overlay view is not needed
// for *Graph (theorystate.md section 77's resolution note). A backend
// whose writes are not visible anywhere until a final commit step
// (theorystate.md section 94, an etcd-backed GraphStore) instead hands
// Check a view of its own not-yet-published changes merged over
// last-committed state -- still exactly "the state fn's mutations would
// produce," just realized by a different mechanism; theorystate.md
// section 97 sketches a test-only GraphAPI implementation built around
// exactly this second shape, specifically to catch registry code that
// accidentally depends on the first shape's specifics.
//
// Checkers only run for mutations made through Graph.Transact, and there
// is no public way to mutate a graph outside one (theorystate.md section
// 92). What still bypasses them is state that did not come through this
// process's Checkers: data loaded from storage, written by another
// client or an older build, or already present when a registry was
// constructed. The out-of-band adversarial tests in this file simulate
// exactly that, using the unexported core write methods through
// test-only helpers. Checkers catch a violation introduced by a
// composed, multi-step operation; the registries' on-read fail-loud
// validation remains the second line of defence.
type Checker struct {
	// Name identifies this Checker in a declined commit's returned
	// error, so a caller can tell which specific invariant was violated
	// rather than receiving a generic failure (theorystate.md section
	// 77's "declines should be attributable, not generic" requirement).
	Name string

	// Tags lists every tag NodeID this Checker's invariant is defined in
	// terms of. Used only as a coarse, conservative relevance filter
	// (see checkerRelevant) to decide whether this Checker is
	// worth invoking at all for a given transaction's changeset -- never
	// consulted by Check itself, which remains free to interpret its own
	// tags however its own invariant actually requires.
	Tags []NodeID

	// Check reports whether this Checker's invariant currently holds.
	// touched lists every NodeID the just-completed transaction's
	// mutations involved (as an endpoint of an added or removed
	// relationship, or as a created or deleted node); Check is expected
	// to use touched to narrow down which of its own tagged nodes, if
	// any, actually need re-validating, rather than re-scanning the
	// whole graph on every single commit. g is typed as GraphReader,
	// not the concrete *Graph, since no Checker in this file ever needs
	// to mutate anything -- only ever to validate (theorystate.md
	// section 87).
	Check func(g GraphReader, touched map[NodeID]struct{}) error
}

// RegisterChecker adds c to the set of Checkers Transact consults after
// every future transaction whose changeset could plausibly be relevant
// to it (see Checker.Tags and checkerRelevant). Checkers are consulted
// in registration order; there is currently no way to unregister one,
// so a Checker stays registered for the lifetime of its graph.
//
// Every registry constructor in this file that has a real invariant to
// enforce (PointerRegistry, PointerMetadataRegistry,
// PointerMetadataRegistryD, CapsuleRegistry, ListRegistry,
// CompositeSetRegistry) registers its own Checker here as part of
// construction, so simply constructing a registry is what wires its
// invariant into commit-time enforcement -- no separate opt-in step is
// needed. SetRegistry registers none, since a Set has no invariant
// beyond its own tag (see the SetRegistry doc comment). Note also that
// operand-descriptor shape (theorystate.md section 80) is checked by a
// Checker registered inside NewCompositeSetRegistry that is keyed on the
// shared axis tags themselves, not on AllCompositeSets -- this is what
// lets it also cover CompositeSetLogRegistry's own descriptors (see that
// Checker's own comment for why), so CompositeSetLogRegistry registers
// no Checker of its own at all: its underlying List's structure and its
// logged operations' descriptor shape are both already covered by
// Checkers registered when its required *ListRegistry and
// *CompositeSetRegistry constructor arguments were themselves
// constructed.
// RegisterChecker also acquires g's concurrentAccessGuard for the
// duration of the call (theorystate.md section 89b): appending to
// g.checkers is itself an unsynchronized mutation, exactly like the node
// and relationship maps, and every call site in this codebase happens at
// registry-construction time, before any Transact call is in flight, so
// this can never be re-entered from within an already-guarded scope.
func (g *Graph) RegisterChecker(c Checker) {
	release := g.guard.acquire()
	defer release()

	g.checkers = append(g.checkers, c)
}

// graphCoreReader is a thin GraphReader adapter that reads directly
// through Graph's unexported, unguarded core methods, rather than
// through Graph's own public, guarded ones. runCheckers hands one of
// these to every Checker's Check function -- instead of the concrete
// *Graph directly -- because Check always runs from inside a
// Graph.Transact call that is already holding that Graph's
// concurrentAccessGuard for the call's entire duration (theorystate.md
// section 89b); calling back into the guarded public API from there
// would incorrectly panic as if a second goroutine had raced in, even
// though it is the same goroutine legitimately still inside its one
// enclosing Transact call.
type graphCoreReader struct {
	graph *Graph
}

// NodeExists delegates to graph's unguarded core. See the
// graphCoreReader doc comment.
func (r graphCoreReader) NodeExists(id NodeID) (bool, error) {
	return r.graph.nodeExists(id), nil
}

// HasRelationship delegates to graph's unguarded core. See the
// graphCoreReader doc comment.
func (r graphCoreReader) HasRelationship(a, b NodeID) (bool, error) {
	return r.graph.hasRelationshipCore(a, b), nil
}

// FindRelationship delegates to graph's unguarded core. See the
// graphCoreReader doc comment.
func (r graphCoreReader) FindRelationship(from, to NodeID) (Relationship, bool, error) {
	return r.graph.findRelationshipCore(from, to)
}

// FindOutgoing delegates to graph's unguarded core. See the
// graphCoreReader doc comment.
func (r graphCoreReader) FindOutgoing(from NodeID) ([]Relationship, error) {
	return r.graph.findOutgoingCore(from)
}

// FindIncoming delegates to graph's unguarded core. See the
// graphCoreReader doc comment.
func (r graphCoreReader) FindIncoming(to NodeID) ([]Relationship, error) {
	return r.graph.findIncomingCore(to)
}

// FindRelationships delegates to graph's unguarded core. See the
// graphCoreReader doc comment.
func (r graphCoreReader) FindRelationships() ([]Relationship, error) {
	return r.graph.findRelationshipsCore(), nil
}

// FindNodes delegates to graph's unguarded core. See the
// graphCoreReader doc comment.
func (r graphCoreReader) FindNodes() ([]NodeID, error) {
	return r.graph.findNodesCore(), nil
}

// Compile-time assertion that graphCoreReader satisfies GraphReader,
// mirroring the existing assertions for *Graph, *Txn, and *GraphActor.
var _ GraphReader = graphCoreReader{}

// runCheckers consults every registered Checker whose Tags make it
// plausibly relevant to touched (see checkerRelevant), in registration
// order, returning the first error any relevant Checker reports, wrapped
// with that Checker's Name for attribution. touched being empty (a
// transaction that made no effective mutations at all) or no Checkers
// being registered on g are both treated as trivially passing, without
// iterating any further.
//
// Every Checker's Check function is handed a graphCoreReader wrapping g,
// not g itself, since Check always runs from inside a call to Transact
// that is already holding g's concurrentAccessGuard -- see the
// graphCoreReader doc comment.
func (g *Graph) runCheckers(touched map[NodeID]struct{}) error {
	return runCheckersOver(g.checkers, graphCoreReader{graph: g}, touched)
}

// runCheckersOver consults every Checker in checkers whose Tags make it
// plausibly relevant to touched (see checkerRelevant), in order, returning
// the first error any relevant Checker reports, wrapped with that
// Checker's Name for attribution. An empty touched set or an empty
// checkers list trivially passes. view is the state the transaction's
// mutations produce, in whatever form the backend provides it (Graph's
// graphCoreReader, stagedGraph's overlay, BoltGraph's update-transaction
// view). It is shared by every GraphAPI implementation's Transact.
func runCheckersOver(checkers []Checker, view GraphReader, touched map[NodeID]struct{}) error {
	if len(touched) == 0 || len(checkers) == 0 {
		return nil
	}

	for _, checker := range checkers {
		relevant, err := checkerRelevant(view, checker, touched)
		if err != nil {
			return err
		}
		if !relevant {
			continue
		}

		if checkErr := checker.Check(view, touched); checkErr != nil {
			return fmt.Errorf("%s: %w", checker.Name, checkErr)
		}
	}

	return nil
}

// checkerRelevant reports whether checker's invariant could plausibly
// have been affected by touched, using checker.Tags as a coarse,
// conservative filter: checker is considered relevant the moment any
// touched node currently carries any of checker's tags, as read through
// view. This is deliberately conservative (it can report true when
// Check would in fact find nothing wrong) rather than precise --
// precision is Check's own responsibility, per the Checker doc comment;
// this filter exists only to avoid invoking every registered Checker on
// every single commit regardless of relevance.
//
// view is passed explicitly rather than this being a *Graph method
// reading Graph's own fields directly, so the identical filtering logic
// is shared by every GraphAPI implementation's own Checker-consulting
// code -- not just *Graph's own runCheckers (theorystate.md section 95:
// Checker's contract, and therefore this relevance filter alongside it,
// is stated backend-neutrally).
func checkerRelevant(view GraphReader, checker Checker, touched map[NodeID]struct{}) (bool, error) {
	for _, tag := range checker.Tags {
		for node := range touched {
			has, err := view.HasRelationship(tag, node)
			if err != nil {
				return false, wrapInterfaceErr(err)
			}
			if has {
				return true, nil
			}
		}
	}

	return false, nil
}

// nodePager is implemented by a GraphReader that can list a bounded page of
// node IDs without materializing every node (BoltGraph's boltView and,
// through forwarding, the ROOT overlay). Any other reader falls back to
// FindNodes, which is fine for the memory-bound backends. This stays an
// unexported optional interface until the paged-read rework of
// theorystate.md section 105 decides the exported shape.
type nodePager interface {
	findNodesAfter(after NodeID, hasAfter bool, limit int) ([]NodeID, error)
}

// pageNodeIDs returns up to limit IDs from sorted (ascending) that are
// greater than after, or from the start if hasAfter is false. hasAfter is a
// separate flag because NodeID 0 is a valid cursor. A non-positive limit
// returns nothing.
func pageNodeIDs(sorted []NodeID, after NodeID, hasAfter bool, limit int) []NodeID {
	return pageSorted(sorted, after, hasAfter, limit, func(id NodeID) NodeID { return id })
}

// nodesAfter returns a page of node IDs from reader, using its paging if it
// has any (see nodePager) and FindNodes otherwise. The fallback path can
// genuinely fail on a backend whose FindNodes can fail (BoltGraph, e.g.
// reached via a RootGraph wrapping one directly, which does not itself
// implement nodePager), so the error is propagated rather than assumed
// away.
func nodesAfter(reader GraphReader, after NodeID, hasAfter bool, limit int) ([]NodeID, error) {
	if pager, ok := reader.(nodePager); ok {
		ids, err := pager.findNodesAfter(after, hasAfter, limit)
		return ids, wrapInterfaceErr(err)
	}

	ids, err := reader.FindNodes()
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}

	return pageNodeIDs(ids, after, hasAfter, limit), nil
}

// outgoingPager is implemented by a GraphReader that can list a bounded
// page of one node's own outgoing relationships without materializing
// every one of them (BoltGraph's boltView, and the ROOT overlay for any
// anchor -- ROOT's own virtual outgoing set pages over node IDs; see
// rootReader.findOutgoingAfter). An implementation that cannot page a
// particular request returns errPagingUnsupported, and outgoingPages then
// reads the full FindOutgoing once and slices it; a reader that does not
// implement this at all gets the same treatment. Like nodePager, this stays an unexported
// optional interface until the paged-read rework of theorystate.md
// section 105 decides the exported shape.
type outgoingPager interface {
	findOutgoingAfter(from, after NodeID, hasAfter bool, limit int) ([]Relationship, error)
}

// incomingPager is outgoingPager's mirror for incoming relationships.
// Implemented by BoltGraph (its boltView, and BoltGraph and GraphActor
// forwarding to it) and by the ROOT overlay, which splices its virtual
// (ROOT, to) parent into each page and hides any physically-stored
// ROOT-sourced relationship (theorystate.md sections 12a and 105; see
// rootReader.findIncomingAfter).
type incomingPager interface {
	findIncomingAfter(to, after NodeID, hasAfter bool, limit int) ([]Relationship, error)
}

// pageSorted returns up to limit elements from sorted (already ascending by
// the NodeID that key extracts) whose key is greater than after, or from
// the start if hasAfter is false. hasAfter is a separate flag because
// NodeID 0 is a valid cursor. A non-positive limit returns nothing. It is
// the one implementation behind pageNodeIDs (key: the ID itself) and the
// relationship pages (key: To for an outgoing page, From for an incoming
// page).
func pageSorted[T any](sorted []T, after NodeID, hasAfter bool, limit int, key func(T) NodeID) []T {
	start := 0
	if hasAfter {
		start = sort.Search(len(sorted), func(i int) bool { return key(sorted[i]) > after })
	}

	count := min(max(limit, 0), len(sorted)-start)

	return sorted[start : start+count]
}

// errPagingUnsupported is returned by an outgoingPager/incomingPager that
// cannot page natively for this particular request -- most importantly a
// forwarding layer (the ROOT overlay, GraphActor) whose inner reader does
// not page. It is a signal, not a failure: pageIterator reacts by
// switching, permanently, to fetching the full result once and slicing it,
// so a backend with no native paging costs one full read per walk instead
// of one per page (theorystate.md section 105).
var errPagingUnsupported = errors.New("this reader cannot page natively for this request")

func relationshipTarget(r Relationship) NodeID { return r.To }

func relationshipSource(r Relationship) NodeID { return r.From }

// pageIterator walks one sorted sequence in bounded pages. With a native
// pager each page is a bounded read; without one (or once the native
// pager reports errPagingUnsupported) the full sequence is read exactly
// once and sliced, so the total work is linear either way. Callers loop
// next until it returns an empty page.
type pageIterator[T any] struct {
	limit    int
	key      func(T) NodeID
	native   func(after NodeID, hasAfter bool, pageLimit int) ([]T, error)
	loadAll  func() ([]T, error)
	after    NodeID
	hasAfter bool
	snapshot []T
	loaded   bool
	done     bool
}

// next returns the next page, or an empty page once the sequence is
// exhausted. A non-positive limit yields nothing.
func (it *pageIterator[T]) next() ([]T, error) {
	if it.done || it.limit <= 0 {
		return nil, nil
	}

	page, err := it.fetch()
	if err != nil {
		return nil, err
	}

	if len(page) < it.limit {
		it.done = true
	}

	if len(page) > 0 {
		it.after = it.key(page[len(page)-1])
		it.hasAfter = true
	}

	return page, nil
}

// fetch reads the next page natively if it still can, otherwise from the
// once-loaded snapshot.
func (it *pageIterator[T]) fetch() ([]T, error) {
	if it.native != nil {
		page, err := it.native(it.after, it.hasAfter, it.limit)
		if !errors.Is(err, errPagingUnsupported) {
			return page, wrapInterfaceErr(err)
		}

		it.native = nil
	}

	if !it.loaded {
		all, err := it.loadAll()
		if err != nil {
			return nil, wrapInterfaceErr(err)
		}

		it.snapshot, it.loaded = all, true
	}

	return pageSorted(it.snapshot, it.after, it.hasAfter, it.limit, it.key), nil
}

// outgoingPages returns an iterator over from's own outgoing relationships,
// limit at a time, sorted by target.
func outgoingPages(reader GraphReader, from NodeID, limit int) *pageIterator[Relationship] {
	it := &pageIterator[Relationship]{
		limit: limit,
		key:   relationshipTarget,
		loadAll: func() ([]Relationship, error) {
			relationships, err := reader.FindOutgoing(from)
			return relationships, wrapInterfaceErr(err)
		},
	}

	if pager, ok := reader.(outgoingPager); ok {
		it.native = func(after NodeID, hasAfter bool, pageLimit int) ([]Relationship, error) {
			relationships, err := pager.findOutgoingAfter(from, after, hasAfter, pageLimit)
			return relationships, wrapInterfaceErr(err)
		}
	}

	return it
}

// incomingPages is outgoingPages' mirror for to's own incoming
// relationships, sorted by source.
func incomingPages(reader GraphReader, to NodeID, limit int) *pageIterator[Relationship] {
	it := &pageIterator[Relationship]{
		limit: limit,
		key:   relationshipSource,
		loadAll: func() ([]Relationship, error) {
			relationships, err := reader.FindIncoming(to)
			return relationships, wrapInterfaceErr(err)
		},
	}

	if pager, ok := reader.(incomingPager); ok {
		it.native = func(after NodeID, hasAfter bool, pageLimit int) ([]Relationship, error) {
			relationships, err := pager.findIncomingAfter(to, after, hasAfter, pageLimit)
			return relationships, wrapInterfaceErr(err)
		}
	}

	return it
}

// defaultVerifyPageSize is the page size VerifyAll uses when given a
// non-positive one.
const defaultVerifyPageSize = 1000

// VerifyAll runs every registered Checker over every node in the graph, the
// semantic integrity sweep of theorystate.md section 104. Checkers only see
// nodes a transaction touches, so data that did not come through this
// process's Checkers (an older build, a restored backup, another tool, an
// invariant added later) is otherwise unchecked until something reads it.
//
// The sweep runs one Transact per page of pageSize node IDs (a
// non-positive pageSize means defaultVerifyPageSize), each calling Tx.Touch
// on its page and changing nothing, so the existing relevance filter and
// Checkers run unchanged, it works through a GraphActor and a RootGraph,
// and it never needs the whole graph in memory on a backend that pages. The
// view can shift between pages (a node added or removed in the gap), which
// is acceptable here: later changes are checked by commit-time Checkers.
//
// It is fail-closed: the first Checker to decline is returned as an error
// wrapping ErrLoadVerification and the Checker's own attributable error, and
// nothing is repaired. It covers only the Checkers registered when it runs,
// so call it after constructing the registries.
func VerifyAll(graph Transactor, pageSize int) error {
	if pageSize <= 0 {
		pageSize = defaultVerifyPageSize
	}

	var after NodeID

	hasAfter := false

	for {
		var page []NodeID

		err := graph.Transact(func(tx Tx) error {
			var pageErr error

			page, pageErr = nodesAfter(tx, after, hasAfter, pageSize)
			if pageErr != nil {
				return pageErr
			}

			tx.Touch(page...)

			return nil
		})
		if err != nil {
			return fmt.Errorf("%w: %w", ErrLoadVerification, err)
		}

		if len(page) < pageSize {
			return nil
		}

		after = page[len(page)-1]
		hasAfter = true
	}
}

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

// ErrStoreCorrupt is returned when the persistent store does not have the
// structure BoltGraph writes: a missing bucket or a malformed counter
// record. It indicates a damaged or foreign file, never a caller mistake.
var ErrStoreCorrupt = errors.New("persistent graph store is corrupt")

// ErrStoreFormat is returned when the store's format version record is
// malformed or names a version this build does not read. The version is
// stored because on-disk keys are 8-byte big-endian NodeIDs; a future layout
// change must be detected, never guessed at (theorystate.md section 112).
var ErrStoreFormat = errors.New("persistent graph store has an unsupported format version")

// ErrStoreLocked is returned when the store file is already open in another
// process (or elsewhere in this one), see openBoltDB.
var ErrStoreLocked = errors.New("persistent graph store is already open")

// ErrLoadVerification wraps the error VerifyAll returns when a Checker
// declines a node at startup: data already in the store (written by an
// older build, restored from a backup, or changed by another tool) violates
// an invariant this process enforces. The wrapped error is the Checker's
// own attributable "<Checker name>: <error>". Nothing is repaired
// (theorystate.md section 104).
var ErrLoadVerification = errors.New("stored data failed verification")

// BoltGraph's on-disk layout (theorystate.md section 102). NodeIDs are
// 8-byte big-endian, so byte order equals numeric order and a prefix scan
// returns relationships sorted exactly as Graph does. nodes: id ->
// present. out: from||to -> present. in: to||from -> present (the reverse
// index). meta: one counter record, next ID (8 bytes) plus an exhausted
// flag (1 byte). names: name (the key's raw bytes) -> one name record, a
// state byte (bound or retired) plus a NodeID (theorystate.md section 103).
// The values of nodes, out and in are the one-byte boltPresent, never
// empty, so "key exists" is always Get(key) != nil.
var (
	boltBucketNodes = []byte("nodes")
	boltBucketOut   = []byte("out")
	boltBucketIn    = []byte("in")
	boltBucketMeta  = []byte("meta")
	boltBucketNames = []byte("names")
	boltCounterKey  = []byte("counter")
	boltPresent     = []byte{1}
	boltFormatKey   = []byte("format")
)

const boltIDSize = 8

// boltFormatVersion is the store layout version this build reads and
// writes: the layout described at boltBucketNodes. It is stored in the meta
// bucket under boltFormatKey as 8 bytes, big-endian.
const boltFormatVersion uint64 = 1

// boltKey8 encodes id as an 8-byte big-endian key.
func boltKey8(id NodeID) []byte {
	key := make([]byte, boltIDSize)
	binary.BigEndian.PutUint64(key, uint64(id))

	return key
}

// boltKey16 encodes the ordered pair (first, second) as a 16-byte key.
func boltKey16(first, second NodeID) []byte {
	key := make([]byte, 2*boltIDSize)
	binary.BigEndian.PutUint64(key[:boltIDSize], uint64(first))
	binary.BigEndian.PutUint64(key[boltIDSize:], uint64(second))

	return key
}

// boltID decodes the NodeID held in the first eight bytes of key.
func boltID(key []byte) NodeID {
	return NodeID(binary.BigEndian.Uint64(key))
}

// wrapBoltErr wraps an error returned by the bolt package with the
// operation that failed, preserving errors.Is/As. A nil err stays nil.
func wrapBoltErr(op string, err error) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("bolt %s: %w", op, err)
}

// boltScanPrefix calls visit for every key in bucket that starts with
// prefix, in key order, until visit returns false. The key slice is valid
// only during the call.
func boltScanPrefix(bucket *bolt.Bucket, prefix []byte, visit func(key []byte) bool) {
	cursor := bucket.Cursor()

	for key, _ := cursor.Seek(prefix); key != nil && bytes.HasPrefix(key, prefix); key, _ = cursor.Next() {
		if !visit(key) {
			return
		}
	}
}

// boltHasPrefix reports whether bucket holds any key starting with prefix.
func boltHasPrefix(bucket *bolt.Bucket, prefix []byte) bool {
	found := false

	boltScanPrefix(bucket, prefix, func(_ []byte) bool {
		found = true
		return false
	})

	return found
}

// boltView is the read side of BoltGraph over one bolt transaction: a
// read-only one for reads made outside a Transact, or the update
// transaction itself for reads made inside one, where it sees that
// transaction's own uncommitted writes (theorystate.md section 101). It is
// also what a Checker's Check function receives.
type boltView struct {
	nodes *bolt.Bucket
	out   *bolt.Bucket
	in    *bolt.Bucket
}

// Compile-time assertion that boltView satisfies GraphReader.
var _ GraphReader = boltView{}

// newBoltView returns the view over btx, or ErrStoreCorrupt if a bucket is
// missing.
func newBoltView(btx *bolt.Tx) (boltView, error) {
	view := boltView{
		nodes: btx.Bucket(boltBucketNodes),
		out:   btx.Bucket(boltBucketOut),
		in:    btx.Bucket(boltBucketIn),
	}

	if view.nodes == nil || view.out == nil || view.in == nil {
		return boltView{}, ErrStoreCorrupt
	}

	return view, nil
}

// NodeExists reports whether id exists. bolt reads from an already-open
// transaction cannot themselves fail, so this always returns a nil
// error; the return exists to satisfy GraphReader (see its doc comment).
func (v boltView) NodeExists(id NodeID) (bool, error) {
	return v.nodes.Get(boltKey8(id)) != nil, nil
}

// HasRelationship reports whether the relationship (a, b) exists.
func (v boltView) HasRelationship(a, b NodeID) (bool, error) {
	existsA, err := v.NodeExists(a)
	if err != nil {
		return false, err
	}
	if !existsA {
		return false, nil
	}

	existsB, err := v.NodeExists(b)
	if err != nil {
		return false, err
	}
	if !existsB {
		return false, nil
	}

	return v.out.Get(boltKey16(a, b)) != nil, nil
}

// FindRelationship reports whether the exact relationship (from, to)
// exists.
func (v boltView) FindRelationship(from, to NodeID) (Relationship, bool, error) {
	existsFrom, err := v.NodeExists(from)
	if err != nil {
		return Relationship{}, false, err
	}

	existsTo, err := v.NodeExists(to)
	if err != nil {
		return Relationship{}, false, err
	}

	if !existsFrom || !existsTo {
		return Relationship{}, false, ErrNodeNotFound
	}

	if v.out.Get(boltKey16(from, to)) == nil {
		return Relationship{}, false, nil
	}

	return Relationship{From: from, To: to}, true, nil
}

// FindOutgoing returns every relationship whose source is from, sorted by
// To.
func (v boltView) FindOutgoing(from NodeID) ([]Relationship, error) {
	exists, err := v.NodeExists(from)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNodeNotFound
	}

	relationships := []Relationship{}

	boltScanPrefix(v.out, boltKey8(from), func(key []byte) bool {
		relationships = append(relationships, Relationship{From: from, To: boltID(key[boltIDSize:])})
		return true
	})

	return relationships, nil
}

// FindIncoming returns every relationship whose target is to, sorted by
// From.
func (v boltView) FindIncoming(to NodeID) ([]Relationship, error) {
	exists, err := v.NodeExists(to)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNodeNotFound
	}

	relationships := []Relationship{}

	boltScanPrefix(v.in, boltKey8(to), func(key []byte) bool {
		relationships = append(relationships, Relationship{From: boltID(key[boltIDSize:]), To: to})
		return true
	})

	return relationships, nil
}

// FindRelationships returns every relationship, sorted by From then To.
// This is O(graph); see theorystate.md section 105. bolt cursor reads
// from an already-open transaction cannot themselves fail, so this
// always returns a nil error.
func (v boltView) FindRelationships() ([]Relationship, error) {
	relationships := []Relationship{}
	cursor := v.out.Cursor()

	for key, _ := cursor.First(); key != nil; key, _ = cursor.Next() {
		relationships = append(relationships, Relationship{From: boltID(key), To: boltID(key[boltIDSize:])})
	}

	return relationships, nil
}

// FindNodes returns every existing NodeID, sorted ascending. This is
// O(graph); see theorystate.md section 105. bolt cursor reads from an
// already-open transaction cannot themselves fail, so this always
// returns a nil error.
func (v boltView) FindNodes() ([]NodeID, error) {
	ids := []NodeID{}
	cursor := v.nodes.Cursor()

	for key, _ := cursor.First(); key != nil; key, _ = cursor.Next() {
		ids = append(ids, boltID(key))
	}

	return ids, nil
}

// findNodesAfter returns up to limit node IDs greater than after (from the
// first node if hasAfter is false), in ascending order, reading only that
// range with a cursor (see nodePager). bolt cursor reads from an
// already-open transaction cannot themselves fail, so this always
// returns a nil error; the return exists to satisfy nodePager, whose
// signature must accommodate a backend-generic caller (nodesAfter) that
// can genuinely fail elsewhere (e.g. its own fallback path calling
// GraphReader.FindNodes on a backend that has no paging of its own).
func (v boltView) findNodesAfter(after NodeID, hasAfter bool, limit int) ([]NodeID, error) {
	ids := []NodeID{}
	cursor := v.nodes.Cursor()

	var key []byte

	if hasAfter {
		key, _ = cursor.Seek(boltKey8(after))
		if key != nil && boltID(key) == after {
			key, _ = cursor.Next()
		}
	} else {
		key, _ = cursor.First()
	}

	for ; key != nil && len(ids) < limit; key, _ = cursor.Next() {
		ids = append(ids, boltID(key))
	}

	return ids, nil
}

// findOutgoingAfter returns up to limit relationships whose source is from
// and whose target is greater than after (from the first if hasAfter is
// false), in ascending target order, reading only that range with a cursor
// (see outgoingPager). bolt cursor reads from an already-open transaction
// cannot themselves fail, so this always returns a nil error once from's
// existence is confirmed.
func (v boltView) findOutgoingAfter(from, after NodeID, hasAfter bool, limit int) ([]Relationship, error) {
	exists, err := v.NodeExists(from)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNodeNotFound
	}

	relationships := []Relationship{}
	prefix := boltKey8(from)
	cursor := v.out.Cursor()

	var key []byte

	if hasAfter {
		key, _ = cursor.Seek(boltKey16(from, after))
		if key != nil && boltID(key[boltIDSize:]) == after {
			key, _ = cursor.Next()
		}
	} else {
		key, _ = cursor.Seek(prefix)
	}

	for ; key != nil && bytes.HasPrefix(key, prefix) && len(relationships) < limit; key, _ = cursor.Next() {
		relationships = append(relationships, Relationship{From: from, To: boltID(key[boltIDSize:])})
	}

	return relationships, nil
}

// findIncomingAfter is findOutgoingAfter's mirror over the incoming index
// (see incomingPager).
func (v boltView) findIncomingAfter(to, after NodeID, hasAfter bool, limit int) ([]Relationship, error) {
	exists, err := v.NodeExists(to)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNodeNotFound
	}

	relationships := []Relationship{}
	prefix := boltKey8(to)
	cursor := v.in.Cursor()

	var key []byte

	if hasAfter {
		key, _ = cursor.Seek(boltKey16(to, after))
		if key != nil && boltID(key[boltIDSize:]) == after {
			key, _ = cursor.Next()
		}
	} else {
		key, _ = cursor.Seek(prefix)
	}

	for ; key != nil && bytes.HasPrefix(key, prefix) && len(relationships) < limit; key, _ = cursor.Next() {
		relationships = append(relationships, Relationship{From: boltID(key[boltIDSize:]), To: to})
	}

	return relationships, nil
}

// Compile-time assertions that boltView pages nodes and relationships.
// boltTxn picks these up for free through embedding boltView.
var (
	_ nodePager     = boltView{}
	_ outgoingPager = boltView{}
	_ incomingPager = boltView{}
)

// checkEdges verifies every key of the edge index primary against its
// mirror index: the key has the right length, the mirrored key exists (the
// two indexes describe the same relationships, theorystate.md section 4),
// and both endpoints are existing nodes. primaryName and mirrorName are
// only for the message.
func (v boltView) checkEdges(primary, mirror *bolt.Bucket, primaryName, mirrorName string) error {
	cursor := primary.Cursor()

	for key, _ := cursor.First(); key != nil; key, _ = cursor.Next() {
		if len(key) != 2*boltIDSize {
			return fmt.Errorf("%w: %s index key of %d bytes, want %d", ErrStoreCorrupt, primaryName, len(key), 2*boltIDSize)
		}

		first, second := boltID(key), boltID(key[boltIDSize:])

		if mirror.Get(boltKey16(second, first)) == nil {
			return fmt.Errorf("%w: key (%d,%d) is in the %s index but its mirror is missing from the %s index", ErrStoreCorrupt, first, second, primaryName, mirrorName)
		}

		existsFirst, err := v.NodeExists(first)
		if err != nil {
			return err
		}

		existsSecond, err := v.NodeExists(second)
		if err != nil {
			return err
		}

		if !existsFirst || !existsSecond {
			return fmt.Errorf("%w: key (%d,%d) in the %s index refers to a node that does not exist", ErrStoreCorrupt, first, second, primaryName)
		}
	}

	return nil
}

// checkLayout verifies BoltGraph's own layout, which bolt's page-level check
// knows nothing about: node keys have the right length, the persisted ID
// counter is ahead of every node (so no existing ID can be issued again,
// theorystate.md section 40) unless the ID space is exhausted, and the
// outgoing and incoming indexes mirror each other. next and exhausted are
// the persisted counter.
func (v boltView) checkLayout(next NodeID, exhausted bool) error {
	var highest NodeID

	hasNodes := false
	cursor := v.nodes.Cursor()

	for key, _ := cursor.First(); key != nil; key, _ = cursor.Next() {
		if len(key) != boltIDSize {
			return fmt.Errorf("%w: node key of %d bytes, want %d", ErrStoreCorrupt, len(key), boltIDSize)
		}

		highest = boltID(key)
		hasNodes = true
	}

	if hasNodes && !exhausted && highest >= next {
		return fmt.Errorf("%w: node %d exists but the ID counter is only at %d, so it could be issued again", ErrStoreCorrupt, highest, next)
	}

	if err := v.checkEdges(v.out, v.in, "outgoing", "incoming"); err != nil {
		return err
	}

	return v.checkEdges(v.in, v.out, "incoming", "outgoing")
}

// boltTxn is the Tx BoltGraph.Transact hands its closure: reads come from
// the embedded boltView (over the update transaction, so they see this
// transaction's own writes), writes go to the same bolt transaction, and
// the embedded txLog records how to undo each write so a nested
// transaction can be rolled back to a savepoint. The outermost rollback
// needs no data undo at all: bolt discards the whole update transaction,
// and only the rollback hooks must run (see abort).
type boltTxn struct {
	boltView
	txLog

	meta    *bolt.Bucket
	names   *bolt.Bucket
	touched map[NodeID]struct{}

	// broken is the first failure of a data undo step. A transaction whose
	// savepoint could not be restored is no longer trustworthy, so
	// Transact aborts it instead of committing.
	broken error

	// dead is set once the outermost transaction is being abandoned or has
	// ended: data undo steps must no longer touch the (discarded or
	// closed) bolt transaction, only rollback hooks still run.
	dead bool
}

// Compile-time assertion that *boltTxn satisfies Tx.
var _ Tx = (*boltTxn)(nil)

// newBoltTxn returns the Tx over the update transaction btx, or
// ErrStoreCorrupt if a bucket is missing.
func newBoltTxn(btx *bolt.Tx) (*boltTxn, error) {
	view, err := newBoltView(btx)
	if err != nil {
		return nil, err
	}

	meta := btx.Bucket(boltBucketMeta)
	names := btx.Bucket(boltBucketNames)

	if meta == nil || names == nil {
		return nil, ErrStoreCorrupt
	}

	return &boltTxn{boltView: view, meta: meta, names: names}, nil
}

// pushUndo records step as the undo of a data mutation. A failing step
// poisons the transaction (see broken) rather than being ignored, and a
// step is skipped once the transaction is dead (see dead).
func (t *boltTxn) pushUndo(step func() error) {
	t.undo = append(t.undo, func() {
		if t.dead {
			return
		}

		if err := step(); err != nil && t.broken == nil {
			t.broken = err
		}
	})
}

// abort abandons the outermost transaction: bolt itself discards every
// write, so only the OnRollback hooks run (in LIFO order), and no data
// undo step touches the store. Calling it again does nothing.
func (t *boltTxn) abort() {
	t.dead = true
	t.rollback()
}

// Touch marks ids as touched without changing anything (see Tx.Touch).
func (t *boltTxn) Touch(ids ...NodeID) {
	t.touched = touchNodes(t.touched, ids...)
}

// Transact opens a nested transaction, a savepoint (see txLog.runNested).
func (t *boltTxn) Transact(fn func(nested Tx) error) error {
	return t.runNested(t, fn)
}

// counter reads the persisted ID counter: the next NodeID CreateNode will
// hand out and whether the ID space is exhausted. A store that has never
// created a node has no counter record, which reads as (0, false).
func (t *boltTxn) counter() (next NodeID, exhausted bool, err error) {
	raw := t.meta.Get(boltCounterKey)
	if raw == nil {
		return 0, false, nil
	}

	if len(raw) != boltIDSize+1 {
		return 0, false, ErrStoreCorrupt
	}

	return boltID(raw), raw[boltIDSize] == 1, nil
}

// setCounter persists the ID counter, in the same bolt transaction as the
// node creation it belongs to, so a committed node and the counter that
// covers it are never separated by a crash -- the never-reuse guarantee
// (theorystate.md sections 40, 78) survives restarts.
func (t *boltTxn) setCounter(next NodeID, exhausted bool) error {
	raw := make([]byte, boltIDSize+1)
	binary.BigEndian.PutUint64(raw, uint64(next))

	if exhausted {
		raw[boltIDSize] = 1
	}

	return wrapBoltErr("write counter", t.meta.Put(boltCounterKey, raw))
}

// CreateNode creates a node, exactly like Graph's node creation. Undoing
// it (nested rollback) deletes the node but, like Graph, does not give the
// ID back: the counter only increases within a process.
func (t *boltTxn) CreateNode() (NodeID, error) {
	next, exhausted, err := t.counter()
	if err != nil {
		return 0, err
	}

	if exhausted {
		return 0, ErrNodeIDExhausted
	}

	id := next
	key := boltKey8(id)

	t.pushUndo(func() error {
		return wrapBoltErr("undo create node", t.nodes.Delete(key))
	})

	if putErr := t.nodes.Put(key, boltPresent); putErr != nil {
		return 0, wrapBoltErr("create node", putErr)
	}

	if id == ^NodeID(0) {
		exhausted = true
	} else {
		next++
	}

	if counterErr := t.setCounter(next, exhausted); counterErr != nil {
		return 0, counterErr
	}

	t.Touch(id)

	return id, nil
}

// putEdgeKeys writes both index entries of one relationship.
func (t *boltTxn) putEdgeKeys(outKey, inKey []byte) error {
	if err := t.out.Put(outKey, boltPresent); err != nil {
		return wrapBoltErr("put outgoing edge", err)
	}

	return wrapBoltErr("put incoming edge", t.in.Put(inKey, boltPresent))
}

// deleteEdgeKeys removes both index entries of one relationship.
func (t *boltTxn) deleteEdgeKeys(outKey, inKey []byte) error {
	if err := t.out.Delete(outKey); err != nil {
		return wrapBoltErr("delete outgoing edge", err)
	}

	return wrapBoltErr("delete incoming edge", t.in.Delete(inKey))
}

// AddRelationship adds (a, b); both nodes must exist. Adding an existing
// relationship reports created == false and records nothing to undo.
func (t *boltTxn) AddRelationship(a, b NodeID) (created bool, err error) {
	existsA, err := t.NodeExists(a)
	if err != nil {
		return false, err
	}
	existsB, err := t.NodeExists(b)
	if err != nil {
		return false, err
	}
	if !existsA || !existsB {
		return false, ErrNodeNotFound
	}

	outKey, inKey := boltKey16(a, b), boltKey16(b, a)
	if t.out.Get(outKey) != nil {
		return false, nil
	}

	// The undo is recorded first: deleting keys that were never written is
	// a no-op, so a failure halfway through the two writes is still undone.
	t.pushUndo(func() error { return t.deleteEdgeKeys(outKey, inKey) })

	if putErr := t.putEdgeKeys(outKey, inKey); putErr != nil {
		return false, putErr
	}

	t.Touch(a, b)

	return true, nil
}

// RemoveRelationship removes (a, b); both nodes must exist. Removing a
// relationship that is not there reports removed == false.
func (t *boltTxn) RemoveRelationship(a, b NodeID) (removed bool, err error) {
	existsA, err := t.NodeExists(a)
	if err != nil {
		return false, err
	}
	existsB, err := t.NodeExists(b)
	if err != nil {
		return false, err
	}
	if !existsA || !existsB {
		return false, ErrNodeNotFound
	}

	outKey, inKey := boltKey16(a, b), boltKey16(b, a)
	if t.out.Get(outKey) == nil {
		return false, nil
	}

	t.pushUndo(func() error { return t.putEdgeKeys(outKey, inKey) })

	if deleteErr := t.deleteEdgeKeys(outKey, inKey); deleteErr != nil {
		return false, deleteErr
	}

	t.Touch(a, b)

	return true, nil
}

// DeleteNode deletes id only if it has no relationships in either
// direction (theorystate.md section 18). The undo restores the bare node,
// which is a complete restoration for the same reason as Txn.DeleteNode
// (theorystate.md section 78).
func (t *boltTxn) DeleteNode(id NodeID) error {
	exists, err := t.NodeExists(id)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNodeNotFound
	}

	key := boltKey8(id)
	if boltHasPrefix(t.out, key) || boltHasPrefix(t.in, key) {
		return ErrNodeNotEmpty
	}

	t.pushUndo(func() error {
		return wrapBoltErr("undo delete node", t.nodes.Put(key, boltPresent))
	})

	if err := t.nodes.Delete(key); err != nil {
		return wrapBoltErr("delete node", err)
	}

	t.Touch(id)

	return nil
}

// boltNameRecordSize is the size of a stored name record: one state byte
// followed by an 8-byte NodeID.
const boltNameRecordSize = 1 + boltIDSize

// encodeNameRecord encodes rec as a state byte followed by its NodeID.
func encodeNameRecord(rec nameRecord) []byte {
	raw := make([]byte, boltNameRecordSize)
	raw[0] = byte(rec.state)
	binary.BigEndian.PutUint64(raw[1:], uint64(rec.id))

	return raw
}

// decodeNameRecord is encodeNameRecord's inverse. A record that is not
// exactly the expected size, or whose state is neither bound nor retired
// (an absent record is never stored), is ErrStoreCorrupt.
func decodeNameRecord(raw []byte) (nameRecord, error) {
	if len(raw) != boltNameRecordSize {
		return nameRecord{}, ErrStoreCorrupt
	}

	state := nameState(raw[0])
	if state != nameBound && state != nameRetired {
		return nameRecord{}, ErrStoreCorrupt
	}

	return nameRecord{state: state, id: boltID(raw[1:])}, nil
}

// Compile-time assertions that boltTxn provides the name store.
var (
	_ nameRecordProvider = (*boltTxn)(nil)
	_ nameRecordStore    = (*boltTxn)(nil)
)

// nameRecords implements nameRecordProvider: a bolt transaction stores name
// records in its own store, inside the same bolt transaction as the nodes
// (theorystate.md section 103).
func (t *boltTxn) nameRecords() nameRecordStore {
	return t
}

// getNameRecord returns name's record, or a record in the nameAbsent state
// if there is none.
func (t *boltTxn) getNameRecord(name string) (nameRecord, error) {
	raw := t.names.Get([]byte(name))
	if raw == nil {
		return nameRecord{}, nil
	}

	return decodeNameRecord(raw)
}

// putNameRecord stores rec as name's record.
func (t *boltTxn) putNameRecord(name string, rec nameRecord) error {
	return t.setNameRaw(name, encodeNameRecord(rec))
}

// deleteNameRecord removes name's record, if any.
func (t *boltTxn) deleteNameRecord(name string) error {
	return t.setNameRaw(name, nil)
}

// setNameRaw replaces name's stored bytes with raw (nil deletes the
// record), recording how to restore the previous bytes so that a nested
// rollback undoes the write like every other mutation.
func (t *boltTxn) setNameRaw(name string, raw []byte) error {
	key := []byte(name)

	// Copied: bolt's returned slice is only valid until the next write.
	// A nil previous value means "no record", since stored values are
	// never empty.
	previous := append([]byte(nil), t.names.Get(key)...)

	t.pushUndo(func() error { return t.writeNameRaw(key, previous) })

	return t.writeNameRaw(key, raw)
}

// writeNameRaw puts raw under key, or deletes key if raw is nil.
func (t *boltTxn) writeNameRaw(key, raw []byte) error {
	if raw == nil {
		return wrapBoltErr("delete name record", t.names.Delete(key))
	}

	return wrapBoltErr("put name record", t.names.Put(key, raw))
}

// forEachNameRecord calls fn for every stored name record, in name order.
func (t *boltTxn) forEachNameRecord(fn func(name string, rec nameRecord)) error {
	cursor := t.names.Cursor()

	for key, raw := cursor.First(); key != nil; key, raw = cursor.Next() {
		rec, err := decodeNameRecord(raw)
		if err != nil {
			return err
		}

		fn(string(key), rec)
	}

	return nil
}

// BoltGraph is the disk-backed GraphAPI (theorystate.md sections 100-108):
// nodes, relationships and the ID counter live in one bbolt file, laid out
// as described at boltBucketNodes. Only one process may open the file
// (bbolt takes a file lock), which is the graph-host model of section 107.
//
// A bare BoltGraph supports one goroutine at a time (concurrentAccessGuard
// panics on overlap, exactly like *Graph); wrap it in a GraphActor for
// concurrent callers, which is also what serializes reads and writes so a
// read transaction is never opened while a write transaction is open on
// the same goroutine (bbolt's documented deadlock). Reads outside a
// Transact each run in their own short read transaction; inside a Transact
// every read goes through the update transaction.
//
// Durability: Transact is one bbolt update transaction, so each committed
// Transact is durable and an aborted or crashed one leaves no trace.
// OnCommit hooks run only after that commit has succeeded, and OnRollback
// hooks run on any abort, including a failed commit.
//
// NodeExists, HasRelationship, FindRelationships, and FindNodes return
// ErrGraphStoreUnavailable (wrapping the underlying failure) if the store
// itself fails, rather than panicking, per GraphReader's own doc comment.
// Paged reads remain the separate, still-open rework of theorystate.md
// section 105.
type BoltGraph struct {
	guard    concurrentAccessGuard
	db       *bolt.DB
	checkers []Checker
}

// Compile-time assertion that *BoltGraph satisfies GraphAPI.
var _ GraphAPI = (*BoltGraph)(nil)

// boltOpenTimeout is how long bolt waits for the file lock. A var, not a
// const, so tests can shrink it.
var boltOpenTimeout = time.Second

// boltOpenGuardFactor times boltOpenTimeout is how long openBoltDB waits for
// bolt.Open before giving up on it.
const boltOpenGuardFactor = 5

// boltOpenResult is what the goroutine running bolt.Open reports.
type boltOpenResult struct {
	db  *bolt.DB
	err error
}

// finishBoltOpen turns bolt.Open's outcome into this package's errors: a
// lock timeout becomes ErrStoreLocked.
func finishBoltOpen(path string, res boltOpenResult) (*bolt.DB, error) {
	switch {
	case res.err == nil:
		return res.db, nil
	case errors.Is(res.err, berrors.ErrTimeout):
		return nil, fmt.Errorf("%w: %s: %w", ErrStoreLocked, path, res.err)
	default:
		return nil, fmt.Errorf("bolt: opening %s: %w", path, res.err)
	}
}

// openBoltDB opens the bolt file at path, failing fast with ErrStoreLocked
// if it is already open. bbolt documents its Timeout option for Darwin and
// Linux only, so on Windows a second open of a locked file might block
// forever. bolt.Open therefore runs in its own goroutine and openBoltDB
// gives up after boltOpenGuardFactor times the timeout; if that abandoned
// open ever succeeds later, the late handle is closed at once so it cannot
// keep the file locked. An OS-held lock needs no cleanup after a crash,
// unlike a lock file (theorystate.md section 112).
func openBoltDB(path string) (*bolt.DB, error) {
	timeout := boltOpenTimeout
	if timeout <= 0 {
		timeout = time.Second
	}

	results := make(chan boltOpenResult, 1)

	go func() {
		db, openErr := bolt.Open(path, 0o600, &bolt.Options{Timeout: timeout})
		results <- boltOpenResult{db: db, err: openErr}
	}()

	guard := time.NewTimer(boltOpenGuardFactor * timeout)
	defer guard.Stop()

	select {
	case res := <-results:
		return finishBoltOpen(path, res)
	case <-guard.C:
		go func() {
			if late := <-results; late.db != nil {
				if closeErr := late.db.Close(); closeErr != nil {
					_ = closeErr
				}
			}
		}()

		return nil, fmt.Errorf("%w: %s did not open within %v", ErrStoreLocked, path, boltOpenGuardFactor*timeout)
	}
}

// encodeBoltFormat encodes a format version as the 8-byte record.
func encodeBoltFormat(version uint64) []byte {
	raw := make([]byte, boltIDSize)
	binary.BigEndian.PutUint64(raw, version)

	return raw
}

// readBoltFormat returns the stored format version; present is false if the
// meta bucket has no record. A record of the wrong size is ErrStoreFormat.
func readBoltFormat(meta *bolt.Bucket) (version uint64, present bool, err error) {
	raw := meta.Get(boltFormatKey)
	if raw == nil {
		return 0, false, nil
	}

	if len(raw) != boltIDSize {
		return 0, false, fmt.Errorf("%w: format record of %d bytes, want %d", ErrStoreFormat, len(raw), boltIDSize)
	}

	return binary.BigEndian.Uint64(raw), true, nil
}

// requireBoltFormat accepts only the version this build reads.
func requireBoltFormat(version uint64) error {
	if version != boltFormatVersion {
		return fmt.Errorf("%w: the store is version %d, this build reads version %d", ErrStoreFormat, version, boltFormatVersion)
	}

	return nil
}

// ensureBoltFormat checks the store's format record, writing it if there is
// none (a new store, or a store from before the record existed, which has
// exactly this layout), and fails loudly on any other version.
func ensureBoltFormat(meta *bolt.Bucket) error {
	version, present, err := readBoltFormat(meta)
	if err != nil {
		return err
	}

	if present {
		return requireBoltFormat(version)
	}

	return wrapBoltErr("write format version", meta.Put(boltFormatKey, encodeBoltFormat(boltFormatVersion)))
}

// checkFormat is ensureBoltFormat's read-only counterpart for CheckStore: a
// missing record is corruption here, since opening a store always writes one.
func (t *boltTxn) checkFormat() error {
	version, present, err := readBoltFormat(t.meta)
	if err != nil {
		return err
	}

	if !present {
		return fmt.Errorf("%w: the store has no format version record", ErrStoreCorrupt)
	}

	return requireBoltFormat(version)
}

// OpenBoltGraph opens, creating if necessary, the store at path. It fails
// with ErrStoreLocked if the file is already open (see openBoltDB; the graph
// host must not be started twice, theorystate.md section 107) and with
// ErrStoreFormat if the store's format version is not the one this build
// reads (see ensureBoltFormat).
func OpenBoltGraph(path string) (*BoltGraph, error) {
	db, err := openBoltDB(path)
	if err != nil {
		return nil, err
	}

	initErr := db.Update(func(btx *bolt.Tx) error {
		for _, name := range [][]byte{boltBucketNodes, boltBucketOut, boltBucketIn, boltBucketMeta, boltBucketNames} {
			if _, bucketErr := btx.CreateBucketIfNotExists(name); bucketErr != nil {
				return wrapBoltErr("create bucket "+string(name), bucketErr)
			}
		}

		return ensureBoltFormat(btx.Bucket(boltBucketMeta))
	})
	if initErr != nil {
		if closeErr := db.Close(); closeErr != nil {
			return nil, fmt.Errorf("bolt: initializing %s: %w (closing also failed: %w)", path, initErr, closeErr)
		}

		return nil, fmt.Errorf("bolt: initializing %s: %w", path, initErr)
	}

	return &BoltGraph{db: db}, nil
}

// Close closes the store. It is idempotent.
func (g *BoltGraph) Close() error {
	release := g.guard.acquire()
	defer release()

	return wrapBoltErr("close", g.db.Close())
}

// CheckStore is the physical half of the startup integrity check
// (theorystate.md section 104): bolt's own page-level consistency check,
// followed by BoltGraph's layout checks (see boltView.checkLayout). It
// returns ErrStoreCorrupt, wrapping the first problem found and counting the
// rest, and fixes nothing. It runs directly against the store, so call it
// right after OpenBoltGraph, before the graph is handed to a GraphActor;
// bolt documents that its checker must not run alongside other writers.
func (g *BoltGraph) CheckStore() error {
	release := g.guard.acquire()
	defer release()

	err := g.db.View(func(btx *bolt.Tx) error {
		var first error

		extra := 0

		// The channel must be drained completely: bolt's checker runs in
		// its own goroutine.
		for checkErr := range btx.Check() {
			if first == nil {
				first = checkErr
				continue
			}

			extra++
		}

		if first != nil {
			return fmt.Errorf("%w: %w (and %d more problems)", ErrStoreCorrupt, first, extra)
		}

		txn, openErr := newBoltTxn(btx)
		if openErr != nil {
			return openErr
		}

		if formatErr := txn.checkFormat(); formatErr != nil {
			return formatErr
		}

		next, exhausted, counterErr := txn.counter()
		if counterErr != nil {
			return counterErr
		}

		return txn.checkLayout(next, exhausted)
	})

	return wrapBoltErr("check store", err)
}

// boltRead runs fn against a read-only view of g's store, in its own
// short bolt read transaction, and returns its result. The caller must
// hold g's guard.
func boltRead[T any](g *BoltGraph, fn func(v boltView) (T, error)) (T, error) {
	var (
		result T
		fnErr  error
	)

	viewErr := g.db.View(func(btx *bolt.Tx) error {
		view, openErr := newBoltView(btx)
		if openErr != nil {
			fnErr = openErr
			return openErr
		}

		result, fnErr = fn(view)

		return fnErr
	})

	var zero T

	switch {
	case fnErr != nil:
		// The request itself failed (ErrNodeNotFound, ErrStoreCorrupt, ...).
		return zero, wrapInterfaceErr(fnErr)
	case viewErr != nil:
		// fn never failed, so bolt's own read channel did: a closed
		// database, an I/O error beginning or ending the transaction.
		return zero, fmt.Errorf("%w: %w", ErrGraphStoreUnavailable, viewErr)
	default:
		return result, nil
	}
}

// NodeExists reports whether id exists.
func (g *BoltGraph) NodeExists(id NodeID) (bool, error) {
	release := g.guard.acquire()
	defer release()

	return boltRead(g, func(v boltView) (bool, error) { return v.NodeExists(id) })
}

// HasRelationship reports whether (a, b) exists.
func (g *BoltGraph) HasRelationship(a, b NodeID) (bool, error) {
	release := g.guard.acquire()
	defer release()

	return boltRead(g, func(v boltView) (bool, error) { return v.HasRelationship(a, b) })
}

// FindRelationship reports whether the exact relationship exists.
func (g *BoltGraph) FindRelationship(from, to NodeID) (Relationship, bool, error) {
	release := g.guard.acquire()
	defer release()

	type found struct {
		relationship Relationship
		exists       bool
	}

	result, err := boltRead(g, func(v boltView) (found, error) {
		relationship, exists, findErr := v.FindRelationship(from, to)
		return found{relationship: relationship, exists: exists}, findErr
	})

	return result.relationship, result.exists, err
}

// FindOutgoing returns every relationship whose source is from.
func (g *BoltGraph) FindOutgoing(from NodeID) ([]Relationship, error) {
	release := g.guard.acquire()
	defer release()

	return boltRead(g, func(v boltView) ([]Relationship, error) { return v.FindOutgoing(from) })
}

// FindIncoming returns every relationship whose target is to.
func (g *BoltGraph) FindIncoming(to NodeID) ([]Relationship, error) {
	release := g.guard.acquire()
	defer release()

	return boltRead(g, func(v boltView) ([]Relationship, error) { return v.FindIncoming(to) })
}

// FindRelationships returns every relationship. O(graph).
func (g *BoltGraph) FindRelationships() ([]Relationship, error) {
	release := g.guard.acquire()
	defer release()

	return boltRead(g, func(v boltView) ([]Relationship, error) { return v.FindRelationships() })
}

// FindNodes returns every existing NodeID. O(graph).
func (g *BoltGraph) FindNodes() ([]NodeID, error) {
	release := g.guard.acquire()
	defer release()

	return boltRead(g, func(v boltView) ([]NodeID, error) { return v.FindNodes() })
}

// findNodesAfter, findOutgoingAfter and findIncomingAfter let BoltGraph
// itself, not only a transaction over it, page natively (see nodePager,
// outgoingPager, incomingPager): each is one short read transaction
// returning one bounded page. This is what makes paged reads effective
// for a caller that holds the graph outside a Transact, including through
// a RootGraph or GraphActor forwarding to it.
func (g *BoltGraph) findNodesAfter(after NodeID, hasAfter bool, limit int) ([]NodeID, error) {
	release := g.guard.acquire()
	defer release()

	return boltRead(g, func(v boltView) ([]NodeID, error) { return v.findNodesAfter(after, hasAfter, limit) })
}

func (g *BoltGraph) findOutgoingAfter(from, after NodeID, hasAfter bool, limit int) ([]Relationship, error) {
	release := g.guard.acquire()
	defer release()

	return boltRead(g, func(v boltView) ([]Relationship, error) {
		return v.findOutgoingAfter(from, after, hasAfter, limit)
	})
}

func (g *BoltGraph) findIncomingAfter(to, after NodeID, hasAfter bool, limit int) ([]Relationship, error) {
	release := g.guard.acquire()
	defer release()

	return boltRead(g, func(v boltView) ([]Relationship, error) {
		return v.findIncomingAfter(to, after, hasAfter, limit)
	})
}

// Compile-time assertions that BoltGraph pages.
var (
	_ nodePager     = (*BoltGraph)(nil)
	_ outgoingPager = (*BoltGraph)(nil)
	_ incomingPager = (*BoltGraph)(nil)
)

// RegisterChecker registers c, exactly like Graph.RegisterChecker.
func (g *BoltGraph) RegisterChecker(c Checker) {
	release := g.guard.acquire()
	defer release()

	g.checkers = append(g.checkers, c)
}

// Transact runs fn as one bbolt update transaction. Every read fn makes
// goes through tx, which sees fn's own writes. If fn returns an error or
// panics, or a relevant Checker declines the resulting state, or a nested
// rollback could not be restored, the update is abandoned: bolt discards
// every write and only the OnRollback hooks run. Otherwise the update
// commits durably and only then do the OnCommit hooks run, still inside
// this call and under the guard, so they are serialized with every other
// closure. If the commit itself fails, the OnRollback hooks run instead.
func (g *BoltGraph) Transact(fn func(tx Tx) error) error {
	release := g.guard.acquire()
	defer release()

	var txn *boltTxn

	err := g.db.Update(func(btx *bolt.Tx) error {
		var openErr error

		txn, openErr = newBoltTxn(btx)
		if openErr != nil {
			return openErr
		}

		defer func() {
			if r := recover(); r != nil {
				txn.abort()
				panic(r)
			}
		}()

		if fnErr := fn(txn); fnErr != nil {
			txn.abort()
			return fnErr
		}

		if checkErr := runCheckersOver(g.checkers, txn.boltView, txn.touched); checkErr != nil {
			txn.abort()
			return checkErr
		}

		if txn.broken != nil {
			txn.abort()
			return txn.broken
		}

		return nil
	})
	if err != nil {
		if txn != nil {
			// Only reached with work still to do when the commit itself
			// failed; abort is idempotent otherwise.
			txn.abort()
		}

		return wrapInterfaceErr(err)
	}

	txn.runCommitHooks()

	return nil
}

var (
	ErrNameAlreadyBound = errors.New("name is already bound")
	ErrNodeAlreadyNamed = errors.New("node already has a name")
	ErrNameNotFound     = errors.New("name not found")

	// ErrNameBoundToDeletedNode is returned when a name's registry
	// bookkeeping points at a NodeID that no longer exists in the
	// underlying graph. This is never expected to happen through the
	// registry's own API: it indicates that some caller deleted the node
	// via the primitive Graph.DeleteNode directly instead of going
	// through NameRegistry.DeleteNode, leaving the name -> NodeID
	// association stale. It is deliberately surfaced as a distinct,
	// loud failure rather than silently trusted (which would let further
	// structure get built on a nonexistent node) or silently repaired
	// (which would hide the upstream bug that caused it).
	ErrNameBoundToDeletedNode = errors.New("name is bound to a node that no longer exists")

	// ErrNameRetired is returned when a name whose node was deleted, or
	// which was unbound, on purpose is bound or ensured again
	// (theorystate.md section 103). Recreating it silently would hide a
	// possible bug in whatever deleted it, so the caller must either stop
	// asking for the name or run NameRegistry.Purge on it deliberately.
	ErrNameRetired = errors.New("name is retired")

	// ErrNameNotRetired is returned by NameRegistry.Purge for a name that
	// is bound: retire it first (delete its node or unbind it).
	ErrNameNotRetired = errors.New("name is not retired")

	// ErrNamesNotLoaded is returned when the persistent store already has
	// a record for a name that this NameRegistry does not know about,
	// which means LoadNames was not called after opening the store.
	// Continuing would mint a second node for an existing name.
	ErrNamesNotLoaded = errors.New("the store has a name record this registry has not loaded; call LoadNames after opening the store")
)

// NameRegistry maintains the one-to-one association between names and
// existing NodeIDs.
//
// Names are bootstrap metadata outside the primitive graph. The primitive
// Graph does not know about names.
//
// A name has a record, and a record is either bound (the name identifies a
// live node) or retired (the name's node was deleted, or the name was
// unbound, on purpose; theorystate.md section 103). Ensuring or binding a
// retired name fails with ErrNameRetired instead of silently making a new
// node, because that would hide a possible bug in whatever deleted it;
// Purge deletes a retired record so the name can be created again. When the
// backend can store records (BoltGraph), each change is written to the
// store in the same transaction as the node it concerns, and LoadNames
// reads the committed records back after a restart; records and byID are
// then a cache of the store. Without such a backend they are the only copy.
//
// Like every other registry in this file, NameRegistry stores no graph
// reference of its own (theorystate.md section 90): every method that
// needs graph access takes it as an explicit parameter instead. records/
// byID, in contrast, are genuine registry-owned bookkeeping -- not graph
// storage -- and stay as ordinary receiver fields; only a stored graph
// reference is the thing being eliminated here.
//
// records/byID are guarded by mu, so Lookup and NameForNode may be called
// from any goroutine at any time, including while a GraphActor is
// running transactions that bind and delete names. Writers are the
// commit hooks registered by Bind/DeleteNode (which run on the
// goroutine that runs the transaction) and LoadNames. Readers see only
// committed bindings. A returned NodeID is a snapshot: the node may be
// deleted immediately afterwards, which is what lookupLive's
// ErrNameBoundToDeletedNode check is for.
//
// Records changed by a transaction that has not committed yet are staged
// in the pending overlay. Only that transaction's own checks (checkBind,
// lookupLive) consult the overlay, so inside one transaction a second
// CreateNamedNode/Bind for the same name or node is rejected, and a name
// whose node the transaction deleted is already retired, while Lookup and
// NameForNode keep reporting committed bindings only. Every staged change registers
// an OnCommit hook that publishes it and an OnRollback hook that
// reverses it, so the overlay is empty whenever no transaction is in
// flight. A transaction is exclusive (a bare Graph admits one goroutine
// at a time, a GraphActor runs one closure at a time), so the overlay
// only ever describes one transaction.
type NameRegistry struct {
	mu      sync.RWMutex
	records map[string]nameRecord
	byID    map[NodeID]string

	// pending is the staging overlay: the records the transaction in
	// flight has written and not yet committed. An entry in the nameAbsent
	// state hides a committed record that the transaction has removed.
	pending map[string]nameRecord
}

// nameState says what a name's record is. The zero value means "no
// record".
type nameState uint8

const (
	// nameAbsent: the name has no record. In the staging overlay it means
	// the transaction in flight has removed the committed record.
	nameAbsent nameState = iota

	// nameBound: the name identifies the live node in nameRecord.id.
	nameBound

	// nameRetired: the name's node was deleted, or the name unbound, on
	// purpose; nameRecord.id is the last node it identified.
	nameRetired
)

// nameRecord is one name's record.
type nameRecord struct {
	state nameState
	id    NodeID
}

// nameRecordStore is what a backend that keeps name records durably
// provides to a transaction (theorystate.md section 103). Its writes belong
// to the same transaction as the node changes around them, so a record and
// the node it names commit or roll back together. Only BoltGraph's
// transaction implements it; the in-memory backends keep records in the
// registry alone.
type nameRecordStore interface {
	// getNameRecord returns name's record, or one in the nameAbsent state.
	getNameRecord(name string) (nameRecord, error)
	putNameRecord(name string, rec nameRecord) error
	deleteNameRecord(name string) error
	forEachNameRecord(fn func(name string, rec nameRecord)) error
}

// nameRecordProvider is implemented by a Tx that can provide a
// nameRecordStore. Wrappers such as rootTx forward it.
type nameRecordProvider interface {
	nameRecords() nameRecordStore
}

// nameStoreOf returns tx's durable name store, or nil if its backend keeps
// none.
func nameStoreOf(tx Tx) nameRecordStore {
	if provider, ok := tx.(nameRecordProvider); ok {
		return provider.nameRecords()
	}

	return nil
}

// NewNameRegistry creates an empty name registry.
//
// It does not create any nodes. Unlike every other registry constructor
// in this file, NewNameRegistry accepts but ignores a graph parameter:
// NameRegistry has no tag nodes to check for existence and registers no
// Checker, so it has no actual use for one at construction time (contrast
// PointerRegistry and friends, which use their graph parameter for
// exactly those two things -- theorystate.md section 90). The parameter
// is named `_` deliberately, both so every registry constructor in this
// file keeps the same recognizable shape and so this unused parameter
// never trips revive's unused-parameter check. Every method below that
// actually needs graph access takes it explicitly, per call.
func NewNameRegistry(_ GraphAPI) *NameRegistry {
	return &NameRegistry{
		records: make(map[string]nameRecord),
		byID:    make(map[NodeID]string),
		pending: make(map[string]nameRecord),
	}
}

// Lookup returns the NodeID associated with name.
//
// The bool is false when name has no association.
func (r *NameRegistry) Lookup(name string) (NodeID, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	rec := r.records[name]
	if rec.state != nameBound {
		return 0, false
	}

	return rec.id, true
}

// NameForNode returns the name associated with id.
//
// The bool is false when id has no name.
func (r *NameRegistry) NameForNode(id NodeID) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	name, ok := r.byID[id]
	return name, ok
}

// lookupLive returns the NodeID currently bound to name in this
// registry's bookkeeping, additionally confirming that the NodeID still
// exists in the underlying graph.
//
// bound is true only when name has an association AND that association's
// NodeID currently exists. If name has an association whose NodeID no
// longer exists, lookupLive returns ErrNameBoundToDeletedNode instead of
// a normal (id, bound) result: this is the shared fail-fast check used by
// every registry operation that is about to trust or hand out a NodeID
// (Bind, CreateNamedNode, EnsureNamedNode), so that a caller which deleted
// a named node through the primitive Graph.DeleteNode directly (bypassing
// NameRegistry.DeleteNode) gets a loud, immediate error the next time this
// registry is used, rather than silently building further structure on a
// nonexistent node.
//
// Lookup and NameForNode deliberately do NOT go through lookupLive: they
// are raw, side-effect-free bookkeeping queries, not NodeID-issuing
// operations, and keep their existing simple (value, bool) contract.
func (r *NameRegistry) lookupLive(graph GraphReader, name string) (id NodeID, bound bool, err error) {
	// A retired name is neither free nor bound: asking for it fails loudly
	// (theorystate.md section 103).
	rec := r.effectiveRecord(name)

	switch rec.state {
	case nameAbsent:
		return 0, false, nil
	case nameRetired:
		return 0, false, fmt.Errorf("%w: %q", ErrNameRetired, name)
	case nameBound:
		exists, existsErr := graph.NodeExists(rec.id)
		if existsErr != nil {
			return 0, false, wrapInterfaceErr(existsErr)
		}
		if !exists {
			return 0, false, ErrNameBoundToDeletedNode
		}

		return rec.id, true, nil
	default:
		return 0, false, fmt.Errorf("name %q has an unknown record state %d", name, rec.state)
	}
}

// Bind associates name with an existing, currently unnamed NodeID.
//
// Both directions of the association are unique:
//   - a name can identify only one NodeID
//   - a NodeID can have only one name
//
// Binding the exact same name to the exact same NodeID is an idempotent
// success.
//
// The binding is staged inside the transaction and published only when
// the outermost transaction commits (see NameRegistry): later steps of
// the same transaction already see it, so binding the same name, or the
// same node, a second time inside one transaction is rejected exactly as
// it would be after a commit; Lookup and NameForNode see it only after
// the commit; and a rollback, nested or outermost, unstages it.
func (r *NameRegistry) Bind(graph Transactor, name string, id NodeID) error {
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		alreadyBound, err := r.checkBind(tx, name, id)
		if err != nil {
			return err
		}

		if alreadyBound {
			return nil
		}

		// The record is written to the store (if the backend has one) and
		// staged, not published: see setRecord.
		return r.setRecord(tx, name, nameRecord{state: nameBound, id: id})
	}))
}

// checkBind validates that name may be bound to id, reading only from
// graph and never mutating anything -- neither the graph nor this
// registry's maps. alreadyBound reports that name is already bound to
// exactly id, so binding it again is an idempotent no-op.
func (r *NameRegistry) checkBind(tx Tx, name string, id NodeID) (alreadyBound bool, err error) {
	exists, err := tx.NodeExists(id)
	if err != nil {
		return false, wrapInterfaceErr(err)
	}
	if !exists {
		return false, ErrNodeNotFound
	}

	existingID, bound, err := r.lookupLive(tx, name)
	if err != nil {
		return false, err
	}

	if bound {
		if existingID == id {
			return true, nil
		}

		return false, ErrNameAlreadyBound
	}

	if storeErr := r.requireStoreAbsent(tx, name); storeErr != nil {
		return false, storeErr
	}

	if _, ok := r.effectiveNameFor(id); ok {
		return false, ErrNodeAlreadyNamed
	}

	return false, nil
}

// effectiveRecord returns name's record as seen from inside the
// transaction in flight: the committed records overlaid with that
// transaction's staged changes. A name with no record is returned in the
// nameAbsent state. Lookup, by contrast, reports committed bindings only.
func (r *NameRegistry) effectiveRecord(name string) nameRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if staged, ok := r.pending[name]; ok {
		return staged
	}

	return r.records[name]
}

// effectiveNameFor is effectiveRecord's counterpart for the node -> name
// direction: the name currently bound to id, if any. A retired name is not
// bound to anything.
func (r *NameRegistry) effectiveNameFor(id NodeID) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for stagedName, stagedRec := range r.pending {
		if stagedRec.state == nameBound && stagedRec.id == id {
			return stagedName, true
		}
	}

	name, ok := r.byID[id]
	if !ok {
		return "", false
	}

	if stagedRec, staged := r.pending[name]; staged && (stagedRec.state != nameBound || stagedRec.id != id) {
		return "", false
	}

	return name, true
}

// requireStoreAbsent checks, when tx's backend keeps name records durably,
// that the store has no record for name. It is called on the path that is
// about to create a new record because the registry's own view says the
// name is free: a record in the store means the registry never loaded the
// store (ErrNamesNotLoaded), and continuing would mint a second node for an
// existing name.
func (r *NameRegistry) requireStoreAbsent(tx Tx, name string) error {
	store := nameStoreOf(tx)
	if store == nil {
		return nil
	}

	rec, err := store.getNameRecord(name)
	if err != nil {
		return wrapInterfaceErr(err)
	}

	if rec.state != nameAbsent {
		return ErrNamesNotLoaded
	}

	return nil
}

// setRecord makes rec name's record for the transaction tx: it is written
// to the backend's store (if it has one) inside tx, so it commits or rolls
// back with the node changes around it, and staged in the overlay so later
// steps of the same transaction see it. The staged record is published by
// an OnCommit hook and unstaged by an OnRollback hook, so a rolled-back or
// Checker-declined transaction leaves no trace and, under GraphActor, both
// happen on the actor's own goroutine, serialized with every other closure
// that reads them. A rec in the nameAbsent state removes the record.
func (r *NameRegistry) setRecord(tx Tx, name string, rec nameRecord) error {
	if store := nameStoreOf(tx); store != nil {
		var storeErr error

		if rec.state == nameAbsent {
			storeErr = store.deleteNameRecord(name)
		} else {
			storeErr = store.putNameRecord(name, rec)
		}

		if storeErr != nil {
			return wrapInterfaceErr(storeErr)
		}
	}

	r.stageRecord(tx, name, rec)

	return nil
}

// stageRecord stages rec as name's record for tx and registers the hooks
// that publish it on commit and restore the previous staged state on
// rollback. Staging the same name twice in one transaction is fine: the
// commit hooks run in order, so the last one wins.
func (r *NameRegistry) stageRecord(tx Tx, name string, rec nameRecord) {
	r.mu.Lock()
	previous, hadPrevious := r.pending[name]
	r.pending[name] = rec
	r.mu.Unlock()

	tx.OnRollback(func() { r.unstageRecord(name, previous, hadPrevious) })
	tx.OnCommit(func() { r.publishRecord(name, rec) })
}

// unstageRecord restores name's staged state to what it was before a
// stageRecord call: the previous staged record, or none.
func (r *NameRegistry) unstageRecord(name string, previous nameRecord, hadPrevious bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if hadPrevious {
		r.pending[name] = previous
		return
	}

	delete(r.pending, name)
}

// publishRecord makes rec name's committed record. It runs as an OnCommit
// hook.
func (r *NameRegistry) publishRecord(name string, rec nameRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.pending, name)

	if old, ok := r.records[name]; ok && old.state == nameBound {
		delete(r.byID, old.id)
	}

	if rec.state == nameAbsent {
		delete(r.records, name)
		return
	}

	r.records[name] = rec

	if rec.state == nameBound {
		r.byID[rec.id] = name
	}
}

// replaceCommitted replaces every committed record with loaded. It runs as
// an OnCommit hook of LoadNames.
func (r *NameRegistry) replaceCommitted(loaded map[string]nameRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.records = make(map[string]nameRecord, len(loaded))
	r.byID = make(map[NodeID]string, len(loaded))

	for name, rec := range loaded {
		r.records[name] = rec

		if rec.state == nameBound {
			r.byID[rec.id] = name
		}
	}
}

// namedNode returns the live node bound to name, or creates and binds a
// fresh one. If name is already bound to a live node, that node is
// returned when allowExisting is true and ErrNameAlreadyBound otherwise;
// a binding to a deleted node is always ErrNameBoundToDeletedNode (see
// lookupLive). The lookup, the node creation and the binding are one
// transaction (nested if graph is a Tx), which is what makes concurrent
// calls for the same name safe under GraphActor.
func (r *NameRegistry) namedNode(graph Transactor, name string, allowExisting bool) (NodeID, error) {
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		existing, bound, err := r.lookupLive(tx, name)
		if err != nil {
			return 0, err
		}

		if bound {
			if allowExisting {
				return existing, nil
			}

			return 0, ErrNameAlreadyBound
		}

		id, err := createNodeTx(tx)
		if err != nil {
			return 0, err
		}

		if bindErr := r.Bind(tx, name, id); bindErr != nil {
			return 0, bindErr
		}

		return id, nil
	})
}

// CreateNamedNode creates a new primitive node and immediately gives it name.
//
// If name is already bound to a live node, no new node is created and
// ErrNameAlreadyBound is returned. If name is bound to a NodeID that no
// longer exists (see lookupLive), ErrNameBoundToDeletedNode is returned
// instead, since that is a different, more serious problem than an
// ordinary already-bound name.
//
// The name lookup, the node creation and the binding all happen inside
// one transaction (see namedNode), and the registry's maps are updated
// only once the outermost transaction has committed (see Bind). A
// failure at any step therefore leaves neither an orphaned node nor a
// stale name association, and two goroutines racing to create the same
// name under GraphActor cannot both succeed. If graph is a Tx the call
// nests inside the caller's transaction and shares its fate.
func (r *NameRegistry) CreateNamedNode(graph Transactor, name string) (NodeID, error) {
	return r.namedNode(graph, name, false)
}

// EnsureNamedNode returns the NodeID currently associated with name,
// creating and binding a fresh node exactly like CreateNamedNode if no
// such association exists yet.
//
// Unlike CreateNamedNode, EnsureNamedNode is idempotent: calling it
// repeatedly with the same name is always safe and always returns the
// same NodeID once the name has first been bound. This is the primitive
// building block for bootstrapping foundational named nodes (ROOT-like
// nodes such as AllPointers) that must exist exactly once no matter how
// many times setup code runs.
//
// If name is bound to a NodeID that no longer exists, EnsureNamedNode
// returns ErrNameBoundToDeletedNode (see lookupLive) rather than silently
// trusting the stale association or silently creating a replacement.
func (r *NameRegistry) EnsureNamedNode(graph Transactor, name string) (NodeID, error) {
	return r.namedNode(graph, name, true)
}

// Unbind retires name without deleting its NodeID: the name no longer
// resolves, and ensuring or binding it fails with ErrNameRetired until
// Purge removes the record. The record change is written in the same
// transaction as everything else the caller does, if graph is a Tx.
//
// The bool reports whether a binding was retired. A name that is not bound
// (missing or already retired) is ErrNameNotFound.
func (r *NameRegistry) Unbind(graph Transactor, name string) (bool, error) {
	return transactBool(graph, func(tx Tx) (bool, error) {
		rec := r.effectiveRecord(name)
		if rec.state != nameBound {
			return false, ErrNameNotFound
		}

		return true, r.setRecord(tx, name, nameRecord{state: nameRetired, id: rec.id})
	})
}

// Purge deletes name's retired record, so the name can be created again
// with a new NodeID (NodeIDs are never reused). It is a deliberate,
// separate operation and is never done implicitly (theorystate.md section
// 103): a name that is bound is ErrNameNotRetired (delete its node or
// Unbind it first), and a name with no record is ErrNameNotFound.
func (r *NameRegistry) Purge(graph Transactor, name string) error {
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		switch rec := r.effectiveRecord(name); rec.state {
		case nameAbsent:
			return ErrNameNotFound
		case nameBound:
			return ErrNameNotRetired
		case nameRetired:
			return r.setRecord(tx, name, nameRecord{})
		default:
			return fmt.Errorf("name %q has an unknown record state %d", name, rec.state)
		}
	}))
}

// LoadNames replaces this registry's committed records with the ones the
// backend's store holds. Call it once after opening a persistent store and
// before anything else uses the registry (a registry that skipped it fails
// with ErrNamesNotLoaded instead of duplicating a name). On a backend with
// no durable name store it does nothing. The records are published by a
// commit hook, so it also works inside a larger transaction.
func (r *NameRegistry) LoadNames(graph Transactor) error {
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		store := nameStoreOf(tx)
		if store == nil {
			return nil
		}

		// Fresh on every run of fn (it may be re-run).
		loaded := make(map[string]nameRecord)

		if err := store.forEachNameRecord(func(name string, rec nameRecord) { loaded[name] = rec }); err != nil {
			return wrapInterfaceErr(err)
		}

		tx.OnCommit(func() { r.replaceCommitted(loaded) })

		return nil
	}))
}

// VerifyBindings checks that every bound name's node still exists, the
// load-time counterpart of the ErrNameBoundToDeletedNode check a later use
// would make (theorystate.md sections 6a, 104). It returns the first
// problem, in name order, and repairs nothing. Retired names are not
// checked: their node is meant to be gone. Call it after LoadNames.
//
// The records are copied first and the graph is only consulted after the
// lock is released: under a GraphActor, that call runs on the actor
// goroutine, whose commit hooks take this registry's lock for writing.
func (r *NameRegistry) VerifyBindings(graph GraphReader) error {
	r.mu.RLock()
	bound := make(map[string]NodeID, len(r.records))

	for name, rec := range r.records {
		if rec.state == nameBound {
			bound[name] = rec.id
		}
	}

	r.mu.RUnlock()

	names := make([]string, 0, len(bound))
	for name := range bound {
		names = append(names, name)
	}

	sort.Strings(names)

	for _, name := range names {
		exists, err := graph.NodeExists(bound[name])
		if err != nil {
			return wrapInterfaceErr(err)
		}
		if !exists {
			return fmt.Errorf("%w: %q is bound to node %d", ErrNameBoundToDeletedNode, name, bound[name])
		}
	}

	return nil
}

// DeleteNode deletes id from the underlying graph and, only if that
// succeeds, retires any name bound to id (its record becomes retired, so
// the name cannot silently be recreated; see Purge).
//
// This exists because Graph and NameRegistry are deliberately separate
// layers (Graph does not know about names). Deleting a named node directly
// through Graph.DeleteNode would leave a stale name -> NodeID / NodeID ->
// name association behind. Going through NameRegistry.DeleteNode instead
// keeps both in sync.
//
// If id currently has relationships, the underlying Graph.DeleteNode call
// fails with ErrNodeNotEmpty, and any existing name association is left
// completely untouched, exactly as if DeleteNode had never been called.
//
// It is not an error for id to have no name association; this then simply
// behaves like a plain Graph.DeleteNode.
func (r *NameRegistry) DeleteNode(graph Transactor, id NodeID) error {
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		name, named := r.effectiveNameFor(id)

		if delErr := deleteNodeTx(tx, id); delErr != nil {
			return delErr
		}

		if !named {
			return nil
		}

		// The name is retired in the same transaction as the delete: it
		// stays unusable for the rest of this transaction, and the record
		// commits or rolls back together with the deletion.
		return r.setRecord(tx, name, nameRecord{state: nameRetired, id: id})
	}))
}

// BootstrapNames ensures that every name in names has an associated
// NodeID in this registry, creating any that do not yet exist.
//
// BootstrapNames is idempotent and resumable: because each individual
// name is established through EnsureNamedNode, calling BootstrapNames
// again — with the same list, a superset, or an overlapping list — never
// disturbs names that were already bound, and safely picks up where a
// previous partial call left off (for example, after a prior call failed
// partway through with ErrNodeIDExhausted). There is deliberately no
// transactional rollback: names successfully bound before a failure stay
// bound, which is exactly what a resumable bootstrap should do. (If graph
// is a Tx, every name is bound inside the caller's transaction and shares
// its fate.)
//
// The returned map has one entry per distinct name in names; duplicate
// entries in names collapse into a single map entry, as expected.
func (r *NameRegistry) BootstrapNames(graph Transactor, names []string) (map[string]NodeID, error) {
	ids := make(map[string]NodeID, len(names))

	for _, name := range names {
		id, err := r.EnsureNamedNode(graph, name)
		if err != nil {
			return nil, err
		}

		ids[name] = id
	}

	return ids, nil
}

// Foundational names are ordinary named nodes, exactly like ROOT, whose
// special meaning comes entirely from higher-level relationships and
// processors that interpret them — never from the primitive Graph or the
// NameRegistry itself. See THEORY_NOTES_FROM_CONVERSATION.md and
// theorystate.md for the semantics each name is intended to support.
const (
	// NameAllPointers tags a node as Pointer-kind via the relationship
	// (AllPointers, P), for Representation A (direct child): P's own
	// single direct child, if any, is P's target. See PointerRegistry.
	NameAllPointers = "AllPointers"

	// NameAllSubPointers tags a node as Pointer-kind for Representation B
	// (intermediary pointer node, THEORY_NOTES_FROM_CONVERSATION.md
	// section 7B): identical mechanism to NameAllPointers, applied to a
	// dedicated intermediary node U rather than to the owning node P
	// directly, so that P's other direct children stay unconstrained by
	// the pointer representation. Use a second PointerRegistry instance
	// constructed with this tag; no separate type is needed.
	NameAllSubPointers = "AllSubPointers"

	// NameAllPointerMetadata tags a node as a pointer-metadata node for
	// Representation C (metadata structure,
	// THEORY_NOTES_FROM_CONVERSATION.md section 7C). See
	// PointerMetadataRegistry.
	NameAllPointerMetadata = "AllPointerMetadata"

	// NameAllPointerMetadataSubjectSlot tags a node as a subject-slot
	// node used by PointerMetadataRegistry (Representation C) and
	// PointerMetadataRegistryD (Representation D). See
	// PointerMetadataRegistry for why the subject needs its own slot
	// node rather than being pointed at directly.
	NameAllPointerMetadataSubjectSlot = "AllPointerMetadataSubjectSlot"

	// NameAllPointerMetadataTargetSlot tags a node as a target-slot node
	// used by PointerMetadataRegistryD (Representation D). See
	// PointerMetadataRegistryD for why the target, like the subject,
	// needs its own dedicated slot node rather than being identified as
	// "whichever child of M isn't tagged subject-slot" (Representation
	// C's limitation).
	NameAllPointerMetadataTargetSlot = "AllPointerMetadataTargetSlot"

	// NameAllElementCapsules tags a node as an ElementCapsule
	// (THEORY_NOTES_FROM_CONVERSATION.md section 11 / theorystate.md
	// section 11): a freshly-minted NodeID representing one particular
	// list-element occurrence, rather than the value itself. Named
	// AllElementCapsules (not the theory docs' illustrative "AllCapsules")
	// to avoid implying a more generic capsule concept. See
	// CapsuleRegistry.
	NameAllElementCapsules = "AllElementCapsules"

	// NameAllElementCapsulePrevSlot, NameAllElementCapsuleValueSlot, and
	// NameAllElementCapsuleNextSlot each tag a capsule's respective
	// role-slot intermediary node. Each role is discovered by its own
	// tag rather than by position, so a capsule may carry additional,
	// unrelated children later without disturbing role discovery. See
	// CapsuleRegistry.
	NameAllElementCapsulePrevSlot  = "AllElementCapsulePrevSlot"
	NameAllElementCapsuleValueSlot = "AllElementCapsuleValueSlot"
	NameAllElementCapsuleNextSlot  = "AllElementCapsuleNextSlot"

	// NameAllLists tags a node as a List (THEORY_NOTES_FROM_CONVERSATION.md
	// section 11 / theorystate.md section 11). See ListRegistry. Also
	// reused, dual-tagged alongside NameAllCompositeSetLogs, by
	// CompositeSetLogRegistry (theorystate.md section 82).
	NameAllLists = "AllLists"

	// NameAllHeads and NameAllTails each tag a capsule as currently being
	// the head or tail of its list, respectively. Named AllHeads/AllTails
	// (PascalCase, consistent with every other tag name in this file --
	// AllPointers, AllElementCapsules, etc.) rather than reproducing the
	// theory docs' illustrative allHEADs/allTAILs styling verbatim; same
	// tags, same semantics. See ListRegistry for why these are plain tags
	// rather than a further Pointer-style indirection: (AllHeads, X) and
	// (AllTails, X) are already two distinct relationships even when the
	// same capsule X is simultaneously both head and tail (a
	// single-element list), so there is no collision risk analogous to
	// Representation C/D's subject/target collision to guard against.
	NameAllHeads = "AllHeads"
	NameAllTails = "AllTails"

	// NameAllSets tags a node as Set-kind via the relationship
	// (AllSets, S) (theorystate.md section 9 / 9a / 79): S's direct
	// children are exactly its members, with no intermediary node needed
	// -- see SetRegistry for why Sets do not need one, unlike every
	// intermediary-node-based structure elsewhere in this file.
	NameAllSets = "AllSets"

	// NameAllCompositeSets tags a node as CompositeSet-kind via the
	// relationship (AllCompositeSets, C) (theorystate.md section 80 / 81):
	// unlike a plain Set, C's direct children are operand-descriptor
	// nodes, not members themselves. See CompositeSetRegistry.
	NameAllCompositeSets = "AllCompositeSets"

	// NameAllAdditiveOp and NameAllSubtractiveOp tag an operand-descriptor
	// node (see CompositeSetRegistry) with its operation-kind axis
	// (theorystate.md section 80): whether the descriptor's operand
	// contributes to a composite Set's evaluated membership via union
	// (additive) or set-difference (subtractive). Exactly one of these
	// two tags applies to any given descriptor node.
	NameAllAdditiveOp    = "AllAdditiveOp"
	NameAllSubtractiveOp = "AllSubtractiveOp"

	// NameAllScalarOperand and NameAllSetOperand tag an operand-descriptor
	// node with its operand-kind axis (theorystate.md section 80),
	// orthogonal to the operation-kind axis above: whether the
	// descriptor's operand is used as a single literal member (scalar) or
	// expanded via its own Set-kind membership (set). This is always
	// recorded explicitly per descriptor, never inferred from the
	// operand's own tags -- see the CompositeSetRegistry doc comment for
	// why inferring it was found to be a design mistake. Exactly one of
	// these two tags applies to any given descriptor node.
	NameAllScalarOperand = "AllScalarOperand"
	NameAllSetOperand    = "AllSetOperand"

	// NameAllCompositeSetLogs tags a node as CompositeSetLog-kind via the
	// relationship (AllCompositeSetLogs, node) (theorystate.md section
	// 82): the same node is simultaneously tagged (AllLists, node), since
	// a CompositeSetLog is an ordinary List reused and reinterpreted as
	// an append-only log of Set-mutating operations (section 10c's
	// precedent for one identity carrying more than one simultaneous
	// interpretation). See CompositeSetLogRegistry.
	NameAllCompositeSetLogs = "AllCompositeSetLogs"

	// NameAllDomainSlot tags a Domain Pointer's domain-slot node
	// (theorystate.md section 10c): a freshly-minted intermediary node,
	// attached to the pointer's anchor (P for Representation B, the
	// metadata node M for Representation D) exactly like every other
	// slot in this file, whose own single child is the domain node
	// itself. There is deliberately no separate tag marking a node as
	// "a domain" -- any node already carrying one of the three
	// Set-representation tags (AllSets, AllCompositeSets,
	// AllCompositeSetLogs) is domain-eligible (theorystate.md section
	// 9c). See DomainPointerRegistryB / DomainPointerRegistryD.
	NameAllDomainSlot = "AllDomainSlot"

	// NameAllSessions tags a node as a session (theorystate.md section
	// 111): the liveness identity of one client of the graph. Holds on
	// resources are membership of the session in the resource's holder
	// Set. Sessions are ephemeral. See LeaseRegistry.
	NameAllSessions = "AllSessions"

	// NameRoot is the name of the ROOT node (theorystate.md section 12).
	// It is deliberately NOT in FoundationalNames: RootGraph needs ROOT to
	// exist before the graph stack is built, so Host ensures it directly on
	// the raw store first (theorystate.md section 112).
	NameRoot = "ROOT"
)

// FoundationalNames lists every name that setup code should bootstrap via
// NameRegistry.BootstrapNames. New foundational names should be appended
// here — not bootstrapped ad hoc elsewhere — so there is a single, DRY
// source of truth for what must exist, and only once the corresponding
// representation is actually being implemented.
var FoundationalNames = []string{
	NameAllPointers,
	NameAllSubPointers,
	NameAllPointerMetadata,
	NameAllPointerMetadataSubjectSlot,
	NameAllPointerMetadataTargetSlot,
	NameAllElementCapsules,
	NameAllElementCapsulePrevSlot,
	NameAllElementCapsuleValueSlot,
	NameAllElementCapsuleNextSlot,
	NameAllLists,
	NameAllHeads,
	NameAllTails,
	NameAllSets,
	NameAllCompositeSets,
	NameAllAdditiveOp,
	NameAllSubtractiveOp,
	NameAllScalarOperand,
	NameAllSetOperand,
	NameAllCompositeSetLogs,
	NameAllDomainSlot,
	NameAllSessions,
}

// ErrCannotDeleteRoot is returned when deletion of ROOT is attempted
// through a RootGraph layer.
//
// This is deliberately distinct from ErrNodeNotEmpty. ErrNodeNotEmpty
// means "clear the relationships and try again." ErrCannotDeleteRoot means
// deletion can never succeed through this layer regardless of ROOT's
// relationship count, because ROOT's identity is structurally protected
// here, not merely blocked by leftover relationships.
var ErrCannotDeleteRoot = errors.New("cannot delete root node")

// ErrRootGraphOverActor is returned by NewRootGraph when asked to wrap a
// *GraphActor. A RootGraph belongs inside a GraphActor, not around one;
// see the RootGraph doc comment and theorystate.md section 87b.
var ErrRootGraphOverActor = errors.New("root graph cannot wrap a graph actor; place the root graph inside the actor instead")

// rootReader is the read half of the ROOT overlay, defined over any
// GraphReader.
//
// ROOT is a real NodeID in the underlying graph. Its outgoing
// relationships are entirely virtual: (ROOT, X) is visible whenever both
// exist and X != ROOT, and any *stored* relationship whose source is ROOT
// is ignored (including a stored (ROOT, ROOT), which the overlay hides).
// The overlay is bidirectional (theorystate.md section 4): ROOT is also
// reported as a parent of every existing X != ROOT. Relationships pointing
// *to* ROOT from other nodes are ordinary and stored normally.
//
// If ROOT itself no longer exists -- only reachable by deleting it through
// the raw graph, bypassing DeleteNode's ErrCannotDeleteRoot -- the
// overlay reports no virtual relationships at all, consistent with
// HasRelationship reporting false for any relationship whose source does
// not exist.
//
// rootReader is used three ways: as RootGraph's own (read-only)
// non-transactional surface, as the read half of rootStore, and directly
// to wrap the GraphReader a Checker's Check function receives (see
// RootGraph.RegisterChecker).
type rootReader struct {
	inner GraphReader
	root  NodeID
}

// Compile-time assertion that rootReader satisfies GraphReader.
var _ GraphReader = rootReader{}

// requireExist returns ErrNodeNotFound if any of ids does not currently
// exist.
func (v rootReader) requireExist(ids ...NodeID) error {
	for _, id := range ids {
		exists, err := v.inner.NodeExists(id)
		if err != nil {
			return wrapInterfaceErr(err)
		}
		if !exists {
			return ErrNodeNotFound
		}
	}

	return nil
}

// virtualRootRelationships returns the virtual (ROOT, X) relationship for
// every existing X != ROOT, sorted by To (FindNodes is already sorted
// ascending). It returns nothing if ROOT does not exist.
func (v rootReader) virtualRootRelationships() ([]Relationship, error) {
	exists, err := v.inner.NodeExists(v.root)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}
	if !exists {
		return nil, nil
	}

	ids, err := v.inner.FindNodes()
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}

	relationships := make([]Relationship, 0, len(ids))

	for _, id := range ids {
		if id == v.root {
			continue
		}

		relationships = append(relationships, Relationship{
			From: v.root,
			To:   id,
		})
	}

	return relationships, nil
}

// NodeExists reports whether id exists in the underlying graph.
func (v rootReader) NodeExists(id NodeID) (bool, error) {
	exists, err := v.inner.NodeExists(id)
	return exists, wrapInterfaceErr(err)
}

// FindNodes returns every existing node, ROOT included.
func (v rootReader) FindNodes() ([]NodeID, error) {
	ids, err := v.inner.FindNodes()
	return ids, wrapInterfaceErr(err)
}

// findNodesAfter pages through the same set as FindNodes (ROOT included),
// forwarding to the underlying reader's paging where it has any (see
// nodePager).
func (v rootReader) findNodesAfter(after NodeID, hasAfter bool, limit int) ([]NodeID, error) {
	return nodesAfter(v.inner, after, hasAfter, limit)
}

// Compile-time assertion that the ROOT overlay pages.
var _ nodePager = rootReader{}

// findOutgoingAfter pages through from's own outgoing relationships in the
// ROOT view. For any node other than ROOT this is a pure, complete
// delegation to the underlying reader's own paging (see outgoingPager):
// nothing about a non-ROOT node's outgoing relationships is virtual, so
// there is nothing here for the overlay to add or hide. For ROOT itself,
// whose entire outgoing set is virtual (theorystate.md section 12a), this
// pages over existing node IDs instead (see nodesAfter) and translates
// each into a virtual (ROOT, X) relationship, fetching one extra candidate
// so that filtering ROOT itself back out -- excluded by the overlay's own
// irreflexivity -- still leaves a full page whenever one is available,
// rather than under-reporting a page that only looks short because ROOT
// happened to fall inside it.
//
// findIncomingAfter is the incoming counterpart (see incomingPager).
func (v rootReader) findOutgoingAfter(from, after NodeID, hasAfter bool, limit int) ([]Relationship, error) {
	if from != v.root {
		pager, ok := v.inner.(outgoingPager)
		if !ok {
			return nil, errPagingUnsupported
		}

		relationships, err := pager.findOutgoingAfter(from, after, hasAfter, limit)

		return relationships, wrapInterfaceErr(err)
	}

	// ROOT's outgoing set pages over node IDs, which needs a pager for
	// them underneath; otherwise let the caller fall back to one full read.
	if _, ok := v.inner.(nodePager); !ok {
		return nil, errPagingUnsupported
	}

	rootExists, err := v.inner.NodeExists(v.root)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}
	if !rootExists {
		// Same answer as FindOutgoing(ROOT), not an empty page.
		return nil, ErrNodeNotFound
	}

	want := max(limit, 0)

	// Fetch one extra candidate so that filtering ROOT itself back out
	// still leaves a full page whenever one is available. Guarded against
	// overflow for a limit already at the edge of int's range, which no
	// realistic caller passes.
	fetchWant := want + 1
	if fetchWant <= want {
		fetchWant = want
	}

	ids, err := nodesAfter(v.inner, after, hasAfter, fetchWant)
	if err != nil {
		return nil, err
	}

	// Capacity is bounded by what was actually fetched: want may be as
	// large as an int, and make would panic for it.
	relationships := make([]Relationship, 0, min(want, len(ids)))

	for _, id := range ids {
		if id == v.root {
			continue
		}
		if len(relationships) == want {
			break
		}

		relationships = append(relationships, Relationship{From: v.root, To: id})
	}

	return relationships, nil
}

// Compile-time assertions that the ROOT overlay pages outgoing and
// incoming relationships.
var (
	_ outgoingPager = rootReader{}
	_ incomingPager = rootReader{}
)

// hideRootSourced returns stored without any relationship whose source is
// ROOT: the overlay represents ROOT's outgoing side entirely virtually, so
// a physically stored (ROOT, X) must not be reported a second time and a
// stored (ROOT, ROOT) must stay hidden (theorystate.md section 12a). The
// result has room for one more element, the virtual ROOT parent a caller
// may append.
func (v rootReader) hideRootSourced(stored []Relationship) []Relationship {
	visible := make([]Relationship, 0, len(stored)+1)

	for _, relationship := range stored {
		if relationship.From == v.root {
			continue
		}

		visible = append(visible, relationship)
	}

	return visible
}

// virtualRootParent returns the virtual (ROOT, to) relationship when the
// overlay presents one: to must not be ROOT itself (irreflexivity) and ROOT
// must exist. ok is false otherwise.
func (v rootReader) virtualRootParent(to NodeID) (relationship Relationship, ok bool, err error) {
	if to == v.root {
		return Relationship{}, false, nil
	}

	rootExists, err := v.inner.NodeExists(v.root)
	if err != nil {
		return Relationship{}, false, wrapInterfaceErr(err)
	}
	if !rootExists {
		return Relationship{}, false, nil
	}

	return Relationship{From: v.root, To: to}, true, nil
}

// sortBySource sorts relationships by From, the order every incoming read
// uses. The order has no semantic meaning (theorystate.md section 5).
func sortBySource(relationships []Relationship) {
	sort.Slice(relationships, func(i, j int) bool {
		return relationships[i].From < relationships[j].From
	})
}

// findIncomingAfter pages through to's own incoming relationships in the
// ROOT view (see incomingPager), sorted by source. Stored relationships
// come from the underlying reader's own paging; the overlay then hides any
// ROOT-sourced one and adds the virtual (ROOT, to) parent when it belongs
// after the cursor (see virtualRootParent).
//
// One extra stored candidate is fetched: at most one stored relationship is
// sourced at ROOT (pairs are unique) and it is hidden, so want+1 stored
// candidates still leave want visible ones whenever that many exist. The
// merged page is then cut to want. A virtual parent that falls outside the
// cut is not lost: the cursor is the last returned source, ROOT is past it,
// and the next page adds it again.
//
// If the underlying reader cannot page, errPagingUnsupported tells
// pageIterator to fall back to one full FindIncoming.
func (v rootReader) findIncomingAfter(to, after NodeID, hasAfter bool, limit int) ([]Relationship, error) {
	pager, ok := v.inner.(incomingPager)
	if !ok {
		return nil, errPagingUnsupported
	}

	want := max(limit, 0)

	// Guarded against overflow for a limit already at the edge of int's
	// range, which no realistic caller passes.
	fetchWant := want + 1
	if fetchWant <= want {
		fetchWant = want
	}

	stored, err := pager.findIncomingAfter(to, after, hasAfter, fetchWant)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}

	relationships := v.hideRootSourced(stored)

	if !hasAfter || v.root > after {
		virtual, hasVirtual, virtualErr := v.virtualRootParent(to)
		if virtualErr != nil {
			return nil, virtualErr
		}
		if hasVirtual {
			relationships = append(relationships, virtual)
		}
	}

	sortBySource(relationships)

	return relationships[:min(want, len(relationships))], nil
}

// HasRelationship reports whether the relationship exists in the ROOT
// view: ROOT has a virtual relationship to every existing node other than
// itself; every other relationship comes from the underlying graph.
func (v rootReader) HasRelationship(from, to NodeID) (bool, error) {
	existsFrom, err := v.inner.NodeExists(from)
	if err != nil {
		return false, wrapInterfaceErr(err)
	}
	if !existsFrom {
		return false, nil
	}

	existsTo, err := v.inner.NodeExists(to)
	if err != nil {
		return false, wrapInterfaceErr(err)
	}
	if !existsTo {
		return false, nil
	}

	if from == v.root {
		return to != v.root, nil
	}

	has, err := v.inner.HasRelationship(from, to)
	return has, wrapInterfaceErr(err)
}

// FindRelationship reports whether the exact relationship exists in the
// ROOT view.
func (v rootReader) FindRelationship(from, to NodeID) (Relationship, bool, error) {
	if err := v.requireExist(from, to); err != nil {
		return Relationship{}, false, err
	}

	has, err := v.HasRelationship(from, to)
	if err != nil {
		return Relationship{}, false, err
	}
	if !has {
		return Relationship{}, false, nil
	}

	return Relationship{
		From: from,
		To:   to,
	}, true, nil
}

// FindOutgoing returns every relationship whose source is from in the
// ROOT view: for ROOT, every existing node other than ROOT; for every
// other node, its ordinary stored relationships.
func (v rootReader) FindOutgoing(from NodeID) ([]Relationship, error) {
	exists, err := v.inner.NodeExists(from)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}
	if !exists {
		return nil, ErrNodeNotFound
	}

	if from != v.root {
		relationships, findErr := v.inner.FindOutgoing(from)
		return relationships, wrapInterfaceErr(findErr)
	}

	return v.virtualRootRelationships()
}

// FindIncoming returns every relationship whose target is to in the ROOT
// view: the stored relationships from nodes other than ROOT, plus the
// virtual (ROOT, to) relationship when to != ROOT (and ROOT exists).
// Stored relationships whose source is ROOT are dropped, so a physically
// stored (ROOT, X) is never reported twice and a stored (ROOT, ROOT) is
// hidden.
func (v rootReader) FindIncoming(to NodeID) ([]Relationship, error) {
	exists, err := v.inner.NodeExists(to)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}
	if !exists {
		return nil, ErrNodeNotFound
	}

	stored, err := v.inner.FindIncoming(to)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}

	relationships := v.hideRootSourced(stored)

	virtual, hasVirtual, err := v.virtualRootParent(to)
	if err != nil {
		return nil, err
	}
	if hasVirtual {
		relationships = append(relationships, virtual)
	}

	sortBySource(relationships)

	return relationships, nil
}

// FindRelationships returns every relationship visible in the ROOT view:
// all stored relationships except those whose source is ROOT, plus the
// virtual ROOT -> X relationship for every existing X != ROOT. A stored
// ROOT -> X is ignored because the overlay represents it virtually anyway.
func (v rootReader) FindRelationships() ([]Relationship, error) {
	stored, err := v.inner.FindRelationships()
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}

	virtual, err := v.virtualRootRelationships()
	if err != nil {
		return nil, err
	}

	relationships := make([]Relationship, 0, len(stored)+len(virtual))

	for _, relationship := range stored {
		if relationship.From == v.root {
			continue
		}

		relationships = append(relationships, relationship)
	}

	relationships = append(relationships, virtual...)

	sort.Slice(relationships, func(i, j int) bool {
		if relationships[i].From != relationships[j].From {
			return relationships[i].From < relationships[j].From
		}

		return relationships[i].To < relationships[j].To
	})

	return relationships, nil
}

// rootStore is the full read/write ROOT overlay over any GraphStore. It
// implements GraphStore and, wrapped in rootTx, is the handle
// RootGraph.Transact gives its closure. RootGraph itself has no writes
// outside a transaction, so its non-transactional methods use only
// rootReader.
//
// Writes follow the overlay's rules: a relationship whose source is ROOT
// is virtual, so adding or removing one is a no-op reporting false (after
// the usual existence checks); ROOT itself can never be deleted.
// Everything else passes through unchanged.
type rootStore struct {
	rootReader
	store GraphStore
}

// Compile-time assertion that rootStore satisfies GraphStore.
var _ GraphStore = rootStore{}

// rootTx is the handle RootGraph.Transact gives its closure: the ROOT
// overlay over the underlying transaction (rootStore) plus that
// transaction's own OnCommit, OnRollback and nested Transact, so it
// satisfies Tx.
type rootTx struct {
	rootStore
	tx Tx
}

// Compile-time assertion that rootTx satisfies Tx.
var _ Tx = rootTx{}

// newRootTx returns the ROOT overlay over the transaction tx.
func newRootTx(tx Tx, root NodeID) rootTx {
	return rootTx{
		rootStore: newRootStore(tx, root),
		tx:        tx,
	}
}

// OnCommit forwards to the underlying transaction, so commit hooks
// registered through the overlay run when that transaction commits.
func (t rootTx) OnCommit(fn func()) {
	t.tx.OnCommit(fn)
}

// OnRollback forwards to the underlying transaction, so rollback hooks
// registered through the overlay run when that transaction rolls back.
func (t rootTx) OnRollback(fn func()) {
	t.tx.OnRollback(fn)
}

// Touch forwards to the underlying transaction: touched sets hold NodeIDs
// only, which the ROOT overlay does not change.
func (t rootTx) Touch(ids ...NodeID) {
	t.tx.Touch(ids...)
}

// nameRecords forwards to the underlying transaction, so name records are
// stored durably (or not) exactly as they would be without the ROOT layer.
func (t rootTx) nameRecords() nameRecordStore {
	return nameStoreOf(t.tx)
}

// Compile-time assertion that rootTx can provide a name store.
var _ nameRecordProvider = rootTx{}

// Transact forwards to the underlying transaction's nested Transact and
// hands fn a handle presenting the same ROOT overlay, so the overlay is
// in force inside nested transactions exactly as it is in the outermost
// one.
func (t rootTx) Transact(fn func(nested Tx) error) error {
	return wrapInterfaceErr(t.tx.Transact(func(inner Tx) error {
		return fn(newRootTx(inner, t.root))
	}))
}

// newRootStore returns the ROOT overlay over store.
func newRootStore(store GraphStore, root NodeID) rootStore {
	return rootStore{
		rootReader: rootReader{inner: store, root: root},
		store:      store,
	}
}

// CreateNode creates a node in the underlying graph. The new node is
// consequently visible as a virtual child of ROOT.
func (s rootStore) CreateNode() (NodeID, error) {
	id, err := s.store.CreateNode()
	return id, wrapInterfaceErr(err)
}

// AddRelationship adds an ordinary relationship. A relationship from ROOT
// is virtual and not physically stored, so adding one is an idempotent
// no-op. Relationships pointing to ROOT are ordinary and are stored.
func (s rootStore) AddRelationship(from, to NodeID) (created bool, err error) {
	if existErr := s.requireExist(from, to); existErr != nil {
		return false, existErr
	}

	if from == s.root {
		// Whether to == root (the self-loop case, hidden by the overlay's
		// irreflexivity) or to != root (already represented virtually),
		// there is nothing to physically add either way.
		return false, nil
	}

	created, err = s.store.AddRelationship(from, to)
	return created, wrapInterfaceErr(err)
}

// RemoveRelationship removes an ordinary relationship. Virtual ROOT
// relationships cannot be removed, so removing (ROOT, X) is a no-op.
func (s rootStore) RemoveRelationship(from, to NodeID) (removed bool, err error) {
	if existErr := s.requireExist(from, to); existErr != nil {
		return false, existErr
	}

	if from == s.root {
		// Symmetric with AddRelationship above.
		return false, nil
	}

	removed, err = s.store.RemoveRelationship(from, to)
	return removed, wrapInterfaceErr(err)
}

// DeleteNode deletes an ordinary node. ROOT itself cannot be deleted
// through this layer. This is reported as ErrCannotDeleteRoot, not
// ErrNodeNotEmpty: ROOT's identity is structurally protected regardless of
// its relationship count, so this failure cannot be resolved by clearing
// relationships and retrying (theorystate.md section 18a).
func (s rootStore) DeleteNode(id NodeID) error {
	exists, err := s.NodeExists(id)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNodeNotFound
	}

	if id == s.root {
		return ErrCannotDeleteRoot
	}

	return wrapInterfaceErr(s.store.DeleteNode(id))
}

// RootGraph is the ROOT overlay as a layer of the graph stack: it
// implements GraphAPI over any other GraphAPI, so it can be used
// anywhere a Graph is -- as the graph a registry is constructed over, or
// as the backend a GraphActor owns.
//
// The overlay is applied at every seam the graph is seen through, so
// there is no place where the virtual (ROOT, X) relationships are visible
// and another where they are not:
//   - non-transactional reads: via the embedded rootReader (RootGraph has
//     no write methods; writes exist only inside Transact);
//   - Transact: the closure receives a rootTx wrapped around the
//     underlying transaction's Tx (which is why Transact takes the Tx
//     interface rather than the concrete *Txn);
//   - Checkers: RegisterChecker wraps each Check so it receives a
//     rootReader over the reader it was given.
//
// Go embedding is not inheritance -- a promoted method's receiver is the
// embedded value -- so this cannot be done by embedding *Graph; it has to
// be decoration at the interface seams (theorystate.md section 87b).
//
// STACKING RULE: put the GraphActor outermost, i.e.
// NewGraphActor(NewRootGraph(&g, root)), never the reverse. Inside the
// actor, every RootGraph method (including multi-step ones such as
// FindRelationships) runs as one job on the actor's goroutine, so it is
// atomic, and the reference RootGraph stores is the raw backend, never
// the actor, so the reentrancy hazard of theorystate.md section 90
// cannot arise. NewRootGraph rejects a *GraphActor to enforce this.
type RootGraph struct {
	rootReader
	api GraphAPI
}

// Compile-time assertion that *RootGraph satisfies GraphAPI.
var _ GraphAPI = (*RootGraph)(nil)

// NewRootGraph creates a ROOT layer around an existing primitive node.
// It returns ErrRootGraphOverActor if graph is a *GraphActor (see the
// RootGraph stacking rule) and ErrNodeNotFound if root does not exist.
func NewRootGraph(graph GraphAPI, root NodeID) (*RootGraph, error) {
	if _, isActor := graph.(*GraphActor); isActor {
		return nil, ErrRootGraphOverActor
	}

	exists, err := graph.NodeExists(root)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}
	if !exists {
		return nil, ErrNodeNotFound
	}

	return &RootGraph{
		rootReader: rootReader{inner: graph, root: root},
		api:        graph,
	}, nil
}

// Root returns the NodeID used as ROOT.
func (r *RootGraph) Root() NodeID {
	return r.root
}

// Transact behaves like the underlying graph's Transact, except that fn
// receives a handle presenting the same ROOT overlay inside the
// transaction as outside it. Rollback and commit-time Checkers are the
// underlying graph's own.
func (r *RootGraph) Transact(fn func(tx Tx) error) error {
	return wrapInterfaceErr(r.api.Transact(func(tx Tx) error {
		return fn(newRootTx(tx, r.root))
	}))
}

// RegisterChecker registers c on the underlying graph, wrapped so that
// c.Check receives a reader presenting the ROOT overlay -- the same view
// the registry that registered it uses everywhere else. Name and Tags are
// unchanged. Note Tags-based relevance filtering still looks at stored
// facts only (see theorystate.md section 87b).
func (r *RootGraph) RegisterChecker(c Checker) {
	if c.Check != nil {
		check := c.Check
		root := r.root

		c.Check = func(g GraphReader, touched map[NodeID]struct{}) error {
			return check(rootReader{inner: g, root: root}, touched)
		}
	}

	r.api.RegisterChecker(c)
}

var (
	ErrNotPointer            = errors.New("node is not tagged as a pointer")
	ErrTooManyPointerTargets = errors.New("pointer node has more than one target; the pointer invariant has already been violated")

	// ErrAmbiguousPointerMetadata is returned by PointerMetadataRegistry
	// and PointerMetadataRegistryD when a tagged-parent or tagged-child
	// lookup (subject -> subject-slot, subject-slot -> metadata node, or,
	// for Representation D, metadata -> target-slot) finds more than one
	// match. This can only happen through an out-of-band Graph mutation
	// that bypasses these registries -- e.g. two different metadata nodes
	// both tagged (AllPointerMetadata, M) ending up pointed at the same
	// subject-slot. Mirrors the fail-loud-not-silently-repair discipline
	// used elsewhere in this file (ErrNameBoundToDeletedNode,
	// ErrTooManyPointerTargets).
	ErrAmbiguousPointerMetadata = errors.New("more than one node found during pointer-metadata lookup; the uniqueness invariant has already been violated")

	// ErrNotCapsule is returned by CapsuleRegistry when asked to operate
	// on a node that is not tagged (AllElementCapsules, node).
	ErrNotCapsule = errors.New("node is not tagged as an element capsule")

	// ErrNotList is returned by ListRegistry when asked to operate on a
	// node that is not tagged (AllLists, node).
	ErrNotList = errors.New("node is not tagged as a list")

	// ErrNotSet is returned by SetRegistry when asked to operate on a
	// node that is not tagged (AllSets, node).
	ErrNotSet = errors.New("node is not tagged as a set")

	// ErrNotSession is returned by LeaseRegistry when asked to act as, or
	// for, a node that is not tagged (AllSessions, node) -- including a
	// session that has already been closed and deleted, which fails with
	// ErrNodeNotFound instead.
	ErrNotSession = errors.New("node is not tagged as a session")

	// ErrSetRepresentationConflict is returned when an operation would
	// give a node more than one of the mutually exclusive
	// Set-representation tags (AllSets, AllCompositeSets, and eventually
	// AllCompositeSetLogs -- theorystate.md section 79) at the same time.
	ErrSetRepresentationConflict = errors.New("node already carries a different set-representation tag")

	// ErrNotCompositeSet is returned by CompositeSetRegistry when asked
	// to operate on a node that is not tagged (AllCompositeSets, node).
	ErrNotCompositeSet = errors.New("node is not tagged as a composite set")

	// ErrNotCompositeSetLog is returned by CompositeSetLogRegistry when
	// asked to operate on a node that is not tagged
	// (AllCompositeSetLogs, node).
	ErrNotCompositeSetLog = errors.New("node is not tagged as a composite set log")

	// ErrOperandNotInCompositeSet is returned by
	// CompositeSetRegistry.RemoveOperand when the given descriptor node
	// is not currently a direct child of the given composite set, and
	// reused by CompositeSetLogRegistry.RemoveOperation for the
	// identically-shaped problem (the descriptor is not currently an
	// operation of the given log).
	ErrOperandNotInCompositeSet = errors.New("descriptor is not an operand of this composite set")
	// ErrInvalidOperandDescriptor is returned when an operand-descriptor
	// node (theorystate.md section 80) does not have exactly the shape
	// CompositeSetRegistry.AddOperand / CompositeSetLogRegistry.AppendOperation
	// always create: exactly one operation-kind tag (additive xor
	// subtractive), exactly one operand-kind tag (scalar xor set), and
	// exactly one outgoing relationship identifying its operand. This can
	// happen through an out-of-band Graph mutation, or (for
	// CompositeSetLogRegistry specifically) by appending a value directly
	// via the underlying ListRegistry.Append instead of going through
	// AppendOperation.
	ErrInvalidOperandDescriptor = errors.New("operand descriptor does not have the expected shape")
	// ErrInvalidSetOperand is returned when an operand-descriptor tagged
	// as a set-expansion operand (AllSetOperand) points at a node that
	// does not currently carry any known Set-representation tag. This is
	// checked both when the descriptor is created (AddOperand /
	// AppendOperation) and freshly re-checked every time it is resolved
	// (Evaluate never caches), so it can also surface if the operand's
	// Set-representation tag was removed after the descriptor was
	// created. Also returned by CompositeSetRegistry.resolveSetOperand /
	// CompositeSetLogRegistry.resolveSetOperand if an operand somehow
	// carries none of the three currently-recognized representations at
	// resolve time.
	ErrInvalidSetOperand = errors.New("set operand does not carry a known set-representation tag")
	// ErrCompositeSetCycle is returned by CompositeSetRegistry.Evaluate
	// and CompositeSetLogRegistry.Evaluate/Contains when resolving a
	// set-expansion operand would revisit a composite-kind node already
	// on the current resolution path (theorystate.md section 83). A
	// plain Set operand can never participate in a cycle, since it is
	// always a leaf; only chains of composite-kind nodes (CompositeSet
	// and/or CompositeSetLog, in any combination) referencing each other
	// can cycle.
	ErrCompositeSetCycle = errors.New("composite set operand graph contains a cycle")
	// ErrTargetOutsideDomain is returned by DomainPointerRegistryB/D's
	// SetTarget when the given target does not currently belong to the
	// pointer's attached domain's resolved membership (theorystate.md
	// section 9c/10c/86), and by their SetDomain when the pointer's
	// current target does not belong to the proposed new domain. A
	// domain is any node carrying one of the three Set-representation
	// tags (AllSets, AllCompositeSets, AllCompositeSetLogs); see
	// domainContainsGeneric.
	ErrTargetOutsideDomain = errors.New("target does not belong to the pointer's domain")
	// ErrCapsuleNotInList is returned by ListRegistry.InsertAfter when
	// the given capsule is not currently an element of the given list
	// (i.e. (list, capsule) does not exist).
	ErrCapsuleNotInList = errors.New("capsule is not an element of this list")

	// ErrCapsuleNotEmpty is returned by CapsuleRegistry.DeleteCapsule
	// when capsule, or one of its three role-slot nodes, currently
	// carries any relationship beyond the fixed shape buildCapsuleTx
	// itself establishes -- e.g. capsule is still an element of some
	// list, still tagged head/tail, a slot still has its target set, or
	// a slot has picked up some unrelated parent of its own. This is the
	// CapsuleRegistry-level analogue of ErrNodeNotEmpty, one layer up:
	// DeleteCapsule makes no changes at all when this is returned, and a
	// caller must first undo whatever is holding the capsule or one of
	// its slots open (e.g. via ListRegistry.Remove) before deletion can
	// succeed.
	ErrCapsuleNotEmpty = errors.New("capsule or one of its role slots has relationships beyond its own fixed structure; the capsule cannot be safely deleted")

	// ErrInvalidListStructure is returned when ListRegistry discovers that
	// the graph no longer satisfies the structural invariants of an ordered
	// list. This is intended for out-of-band Graph mutations: normal list
	// operations maintain these relationships transactionally.
	ErrInvalidListStructure = errors.New("list structure is invalid")

	// ErrListCycle is returned by ListRegistry.Elements when the head-to-tail
	// traversal encounters the same ElementCapsule more than once. A cycle
	// can only arise through an out-of-band graph mutation because the
	// normal list operations maintain an acyclic chain.
	ErrListCycle = errors.New("list next-chain contains a cycle")
)

// txOps is the minimal mutating surface needed to compose primitive
// operations atomically inside a transaction. Tx (and the concrete *Txn)
// satisfies it, including DeleteNode -- Txn.DeleteNode is itself fully
// undoable (see its doc comment), so a caller composing several deletes
// into one logical teardown does not need any special pre-verification
// step of its own; an ordinary Transact rollback already covers it.
//
// This exists so a registry's create/wire sequence can be reused both as
// a standalone top-level Graph.Transact call and as one step composed
// into a larger enclosing Transact call -- e.g. CapsuleRegistry.NewCapsule
// composing PointerRegistry's create-and-tag sequence for each of a
// capsule's three role slots -- without nesting one Graph.Transact call
// inside another. Tx.Transact now supports nested transactions, so
// registry methods themselves also compose by nesting; these txOps-based
// helpers remain as shared, DRY building blocks that run against
// whatever tx they are given, standalone or composed.
type txOps interface {
	CreateNode() (NodeID, error)
	AddRelationship(a, b NodeID) (created bool, err error)
	RemoveRelationship(a, b NodeID) (removed bool, err error)
	DeleteNode(id NodeID) error
}

// txReader is the combined surface for helpers that both compose
// mutations into an already-open transaction (txOps) and need to read
// current graph state as part of deciding what to do (GraphReader) --
// e.g. reading a slot's current target before deciding whether it must
// be replaced. A single txReader-typed parameter, rather than two
// separately threaded txOps and GraphReader parameters, is what makes it
// impossible for a caller to accidentally supply the read half from a
// different -- and, under GraphActor, potentially deadlocking -- source
// than the write half (theorystate.md section 90). Tx (and the concrete
// *Txn) satisfies txReader automatically, since it already satisfies
// both txOps and GraphReader.
type txReader interface {
	txOps
	GraphReader
}

// wrapInterfaceErr wraps an error returned directly from a call to one of
// this package's interface-typed values -- txOps
// (CreateNode/AddRelationship/RemoveRelationship/DeleteNode), or
// GraphAPI/GraphStore/GraphReader (Transact/FindOutgoing/FindIncoming/
// AddRelationship/RemoveRelationship/DeleteNode/etc.) -- before that
// error is returned from one of this file's own functions or methods.
// This exists purely to satisfy static analysis (wrapcheck), which
// cannot see through any of these interfaces to know that their only
// implementations in this package (*Graph, *Txn) return nothing but this
// package's own sentinel errors (ErrNodeNotFound, ErrNodeNotEmpty,
// ErrNodeIDExhausted, and friends). Wrapping with %w preserves full
// errors.Is/errors.As compatibility -- every existing errors.Is check
// against those sentinels continues to work unchanged -- so this adds no
// behavior, only a satisfied linter. A nil err must stay nil:
// fmt.Errorf("%w", nil) would otherwise turn a successful call into a
// non-nil error.
func wrapInterfaceErr(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w", err)
}

// joinErrors is errors.Join with its result passed through
// wrapInterfaceErr, so wrapcheck does not flag the "unwrapped error from an
// external package". Nil errors are dropped, and if every error is nil the
// result is nil. errors.Is/errors.As still see every joined error, and the
// message is unchanged.
func joinErrors(errs ...error) error {
	return wrapInterfaceErr(errors.Join(errs...))
}

// transactValue runs step as one Transact call on graph and returns its
// result, or (zero value, err) if step failed or the transaction was
// declined and rolled back. If graph is a GraphAPI this is an outermost
// transaction; if it is a Tx, a nested one inside the enclosing
// transaction. step may be run more than once by a retrying backend (see
// the Transactor contract), so its result is overwritten on every run and
// only the final, committed run's value is returned.
func transactValue[T any](graph Transactor, step func(tx Tx) (T, error)) (T, error) {
	var result T

	err := graph.Transact(func(tx Tx) error {
		var stepErr error
		result, stepErr = step(tx)
		return stepErr
	})
	if err != nil {
		var zero T
		return zero, wrapInterfaceErr(err)
	}

	return result, nil
}

// transactBool is transactValue for a bool result.
func transactBool(graph Transactor, step func(tx Tx) (bool, error)) (bool, error) {
	return transactValue(graph, step)
}

// tagNodeTx adds the tagging relationship (tag, id) against tx. This is
// the single-relationship-add step shared by createTaggedNodeTx below and
// by any caller that needs to apply more than one tag to a single node --
// e.g. CompositeSetRegistry.AddOperand's operand descriptors, which carry
// two independent axis tags on the same freshly created node.
func tagNodeTx(tx txOps, tag, id NodeID) error {
	return addRelationshipTx(tx, tag, id)
}

// createNodeTx is tx.CreateNode with its error wrapped via
// wrapInterfaceErr. Every helper and Transact closure in this file
// should create nodes through this rather than returning an error
// straight from the interface method, which wrapcheck flags.
func createNodeTx(tx txOps) (NodeID, error) {
	id, err := tx.CreateNode()
	return id, wrapInterfaceErr(err)
}

// addRelationshipTx is tx.AddRelationship with its error wrapped and its
// created bool discarded. Use it wherever the caller does not care
// whether the relationship already existed.
func addRelationshipTx(tx txOps, from, to NodeID) error {
	_, err := tx.AddRelationship(from, to)
	return wrapInterfaceErr(err)
}

// removeRelationshipTx is tx.RemoveRelationship with its error wrapped
// and its removed bool discarded.
func removeRelationshipTx(tx txOps, from, to NodeID) error {
	_, err := tx.RemoveRelationship(from, to)
	return wrapInterfaceErr(err)
}

// deleteNodeTx is tx.DeleteNode with its error wrapped. The wrapping
// preserves errors.Is, so callers can still test for ErrNodeNotEmpty.
func deleteNodeTx(tx txOps, id NodeID) error {
	return wrapInterfaceErr(tx.DeleteNode(id))
}

// untagAndDeleteNodeTx removes each (tag, node) relationship and then
// deletes node, all against tx. The tags are ordinary relationships
// *into* node, so they must go before the delete can succeed; if the
// delete then fails (e.g. ErrNodeNotEmpty), the enclosing Transact's
// rollback restores every tag. Shared by DeleteList, DeleteSet,
// DeleteCompositeSet and DeleteCompositeSetLog.
func untagAndDeleteNodeTx(tx txOps, node NodeID, tags ...NodeID) error {
	for _, tag := range tags {
		if err := removeRelationshipTx(tx, tag, node); err != nil {
			return err
		}
	}

	return deleteNodeTx(tx, node)
}

// createTaggedNodeTx creates a fresh node and tags it via (tag, id),
// against tx. This is the shared "mint a fresh, tag-identified node"
// sequence used throughout this file: PointerRegistry.NewPointer's
// Pointer-kind tagging (via newPointerTx below), and the subject-slot /
// metadata / target-slot creation inside ensureMetadataWithSubjectSlot
// and PointerMetadataRegistryD.SetTarget.
func createTaggedNodeTx(tx txOps, tag NodeID) (NodeID, error) {
	id, err := createNodeTx(tx)
	if err != nil {
		return 0, err
	}

	if err2 := tagNodeTx(tx, tag, id); err2 != nil {
		return 0, err2
	}

	return id, nil
}

// newPointerTx creates a fresh NodeID and tags it Pointer-kind via
// (allPointers, id), against whatever txOps tx is given. This is the
// create-and-tag sequence behind PointerRegistry.NewPointer, factored out
// so a larger composite operation (e.g. CapsuleRegistry.NewCapsule) can
// compose it into its own enclosing Graph.Transact call instead of
// PointerRegistry opening a second, nested one.
func newPointerTx(tx txOps, allPointers NodeID) (NodeID, error) {
	return createTaggedNodeTx(tx, allPointers)
}

// setPointerTargetTx sets id's target to target within tx, given id's
// current target state (current, hasCurrent) as already determined by the
// caller. It performs the remove-old/add-new sequence directly against
// tx, so it can be composed into a larger enclosing transaction (e.g. a
// future list operation rewiring an existing capsule slot's target)
// without nesting Graph.Transact calls. Callers are responsible for the
// "already has this target -> no-op" idempotency check before calling
// this, exactly as PointerRegistry.SetTarget already does.
func setPointerTargetTx(tx txOps, id, current NodeID, hasCurrent bool, target NodeID) error {
	if hasCurrent {
		if err := removeRelationshipTx(tx, id, current); err != nil {
			return err
		}
	}

	return addRelationshipTx(tx, id, target)
}

// singleChildTargetSetTx sets node's single "target" child -- under the
// same at-most-one-child invariant as singleChildTarget/PointerRegistry
// -- composed into an existing tx rather than opening a new
// Graph.Transact. It is the tx-composable counterpart of
// PointerRegistry.SetTarget's read-current/idempotency-check/replace
// sequence, for callers (CapsuleRegistry, and through it ListRegistry)
// that need to rewire an already-tagged Pointer-style slot node as one
// step of a larger enclosing transaction.
//
// node is assumed to already be a valid, at-most-one-child node (e.g. a
// capsule role slot); callers are responsible for target's existence,
// exactly as PointerRegistry.SetTarget's caller-facing checks already
// are. This deliberately skips the IsPointer-style tag check that
// PointerRegistry.currentTarget performs, since callers here have
// already located node via a tag-based lookup (e.g.
// findUniqueTaggedChild) immediately beforehand.
//
// exclude has the same meaning as for singleChildTarget: children of
// node that are structural rather than target candidates
// (PointerMetadataRegistry's subject-slot).
func singleChildTargetSetTx(tx txOps, graph GraphReader, node, target NodeID, exclude ...NodeID) error {
	current, hasCurrent, err := singleChildTarget(graph, node, exclude...)
	if err != nil {
		return err
	}

	if hasCurrent && current == target {
		return nil
	}

	return setPointerTargetTx(tx, node, current, hasCurrent, target)
}

// singleChildTargetRemoveTx clears node's single "target" child, if any,
// composed into an existing tx rather than opening a new Graph.Transact.
// It is the shared read-current/remove step behind
// PointerRegistry.RemoveTarget and PointerMetadataRegistry(D).RemoveTarget.
// Because it runs against tx, the removal is recorded in that
// transaction's undo log and is undone with it. ListRegistry.Remove is
// the kind of caller that needs this: clearing a capsule's own prev/next
// slots must roll back together with the neighbor-relinking steps around
// it.
//
// removed reports whether a target actually existed and was removed.
// exclude has the same meaning as for singleChildTarget.
func singleChildTargetRemoveTx(tx txOps, graph GraphReader, node NodeID, exclude ...NodeID) (removed bool, err error) {
	current, hasCurrent, err := singleChildTarget(graph, node, exclude...)
	if err != nil {
		return false, err
	}

	if !hasCurrent {
		return false, nil
	}

	removed, err = tx.RemoveRelationship(node, current)
	return removed, wrapInterfaceErr(err)
}

// singleChildTarget returns the single relevant child of node in the
// underlying Graph, after excluding any NodeIDs listed in exclude.
//
// This is the shared "at most one relevant child" invariant check behind
// every Pointer representation implemented so far:
//   - Representation A (PointerRegistry, tag AllPointers): node is P
//     itself, no exclusions -- P's own direct child is the target.
//   - Representation B (PointerRegistry, tag AllSubPointers): node is U,
//     no exclusions -- identical mechanism, different tag, see the
//     PointerRegistry doc comment.
//   - Representation C (PointerMetadataRegistry): node is the metadata
//     node M, excluding the subject-slot node S so that S's own
//     structural presence as a child of M is never mistaken for the
//     Pointer's actual target.
//
// hasTarget is false when node's only children, if any, are exactly the
// excluded set. If more than one non-excluded child remains,
// ErrTooManyPointerTargets is returned rather than arbitrarily picking
// one -- see the PointerRegistry doc comment for why (theorystate.md
// section 74: out-of-band mutation can violate this at any time, and
// every caller re-derives fresh rather than caching).
func singleChildTarget(g GraphReader, node NodeID, exclude ...NodeID) (target NodeID, hasTarget bool, err error) {
	outgoing, err := g.FindOutgoing(node)
	if err != nil {
		return 0, false, wrapInterfaceErr(err)
	}

outer:
	for _, rel := range outgoing {
		for _, ex := range exclude {
			if rel.To == ex {
				continue outer
			}
		}

		if hasTarget {
			return 0, false, ErrTooManyPointerTargets
		}

		target = rel.To
		hasTarget = true
	}

	return target, hasTarget, nil
}

// requireTagged checks that id exists and is tagged via (tag, id),
// returning ErrNodeNotFound or notTaggedErr otherwise, without
// inspecting id's other relationships. This is the shared "exists and
// carries this registry's own tag" precondition check factored out of
// what used to be six byte-for-byte identical three-line checks --
// PointerRegistry.requirePointer, CapsuleRegistry.requireCapsule,
// ListRegistry.requireList, SetRegistry.requireSet,
// CompositeSetRegistry.requireCompositeSet, and
// CompositeSetLogRegistry.requireLog -- each differing only in which tag
// NodeID and which dedicated ErrNotX sentinel it returned.
func requireTagged(graph GraphReader, id, tag NodeID, notTaggedErr error) error {
	exists, err := graph.NodeExists(id)
	if err != nil {
		return wrapInterfaceErr(err)
	}
	if !exists {
		return ErrNodeNotFound
	}

	has, err := graph.HasRelationship(tag, id)
	if err != nil {
		return wrapInterfaceErr(err)
	}
	if !has {
		return notTaggedErr
	}

	return nil
}

// PointerRegistry enforces the Pointer invariant -- "at most one target"
// -- for nodes tagged Pointer-kind via a caller-supplied tag relationship
// (tag, P).
//
// This same type and logic serves two of the three Pointer
// representations described in theorystate.md section 10 / 10b,
// distinguished only by which tag
// NodeID the caller passes to NewPointerRegistry -- there is no
// per-representation code path:
//   - Representation A (direct child): tag = AllPointers, applied
//     directly to the owning node P.
//   - Representation B (intermediary pointer node): tag =
//     AllSubPointers, applied to a dedicated node U. The caller
//     separately creates the ordinary relationship (P, U) themselves --
//     that edge carries no invariant and is not PointerRegistry's
//     concern -- so P's other direct children stay unconstrained by the
//     pointer representation living on U.
//
// Representation C (metadata structure) cannot reuse this type as-is,
// since the subject's own direct children must stay completely
// untouched; see PointerMetadataRegistry instead.
//
// This implements Representation A (and, via reuse, B) from
// theorystate.md section 10 / 10b: a Pointer's target, if any, is
// simply P's single direct
// child in the underlying Graph. The tag itself -- (AllPointers, P) -- is
// ordinary graph structure, exactly like any other name-style tag. Like
// NameRegistry and RootGraph, PointerRegistry adds nothing to the
// primitive Graph; it is purely an interpretation/enforcement layer above
// it (theorystate.md sections 10 and 73).
//
// PointerRegistry does not, and structurally cannot, prevent every path
// to invariant violation: a caller can still bypass this layer and give
// a tagged node two or more children with tx.AddRelationship(P, Y)
// inside a Transact (the Checker registered by NewPointerRegistry
// declines that at commit), and data that reached the graph from outside
// this process's Checkers (loaded from storage, written by an older
// build) may already violate it. PointerRegistry does not try to
// intercept arbitrary Graph mutations -- Graph must stay unaware of
// Pointer semantics, per the same layering discipline already
// established elsewhere in this file.
// Instead, every method here re-derives P's current target set fresh from
// the Graph on every call rather than caching it, and fails loudly with
// ErrTooManyPointerTargets if that set already has more than one member,
// rather than silently repairing or silently trusting stale expectations.
// This mirrors the fail-loud-not-silently-repair discipline already used
// for ErrNameBoundToDeletedNode in NameRegistry, and is the practical
// mitigation for the general gap recorded in theorystate.md section
// 74: external structure built on top of the primitive Graph can go
// stale the instant a primitive mutation happens elsewhere, and nothing
// below this layer will ever notify it. A durable commit-time
// interception mechanism that could reject such a mutation before it
// lands (theorystate.md section 73) does not exist yet; until it
// does, "always re-check, never cache" is the deliberate accepted
// boundary of this registry.
//
// Multi-step PointerRegistry operations (SetTarget's replace path,
// NewPointer's create-then-tag) run inside Graph.Transact: if a later
// step fails, an earlier step's mutation is undone rather than left
// committed, so e.g. a failed SetTarget can never leave a Pointer looking
// like it lost its old target without gaining the new one. See the Txn
// doc comment for exactly what this does and does not guarantee -- in
// particular, it is failure-atomicity, not isolation from concurrent
// access; true multi-primitive-operation transactional grouping as a
// first-class graph concept is still theorystate.md section 14/45,
// OPEN.
// What exists here is the minimum needed to stop PointerRegistry's
// own multi-step operations from corrupting state on failure, not a
// general transaction feature.
//
// PointerRegistry deliberately stores no graph reference of its own
// (theorystate.md section 90): every method below takes the graph it
// should operate against as an explicit parameter instead, typed as
// narrowly as that method actually needs (GraphReader for pure reads,
// Transactor for methods that open their own transaction, nested if the
// value passed is a Tx). This is what
// lets the exact same PointerRegistry value be safely reused across
// several separately-submitted GraphActor calls without any risk of a
// Checker or tx-composable helper accidentally reading through a stored
// reference instead of through the tx/g value it was actually handed --
// see GraphActor's own doc comment for why that specific mistake
// deadlocks rather than merely misbehaving.
type PointerRegistry struct {
	allPointers NodeID
}

// NewPointerRegistry creates a PointerRegistry over graph, using
// allPointers as the tagging node for the (AllPointers, P) relationship.
//
// allPointers must already exist. It is the caller's responsibility to
// have bootstrapped it first, typically via
// NameRegistry.EnsureNamedNode(NameAllPointers) or
// NameRegistry.BootstrapNames(FoundationalNames). PointerRegistry itself
// has no dependency on NameRegistry or on names at all -- exactly like
// RootGraph takes its root as a plain NodeID rather than a name, keeping
// this layer decoupled from the bootstrap-naming concern.
//
// This also registers a Checker (see Graph.RegisterChecker) enforcing
// the "at most one target" invariant at commit time, for any node
// tagged (allPointers, node) touched by a future Graph.Transact call --
// the eager, commit-time counterpart to this registry's existing
// always-re-derive-on-read discipline (see the PointerRegistry doc
// comment above). Since this same type is reused unmodified across
// Representations A and B (and, via CapsuleRegistry, for each of a
// capsule's three role-slot tags), constructing any PointerRegistry
// instance -- under any tag -- wires up commit-time enforcement for
// that specific tag, with no additional per-representation code.
func NewPointerRegistry(graph GraphAPI, allPointers NodeID) (*PointerRegistry, error) {
	exists, err := graph.NodeExists(allPointers)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}
	if !exists {
		return nil, ErrNodeNotFound
	}

	// Registers a Checker (see Graph.RegisterChecker) enforcing the "at
	// most one target" invariant at commit time, for any node tagged
	// (allPointers, node) touched by a future Graph.Transact call -- the
	// eager, commit-time counterpart to this registry's existing
	// always-re-derive-on-read discipline (see the PointerRegistry doc
	// comment above). Since this same type is reused unmodified across
	// Representations A and B (and, via CapsuleRegistry, for each of a
	// capsule's three role-slot tags), constructing any PointerRegistry
	// instance -- under any tag -- wires up commit-time enforcement for
	// that specific tag, with no additional per-representation code.
	graph.RegisterChecker(Checker{
		Name: fmt.Sprintf("PointerRegistry(tag=%d)", allPointers),
		Tags: []NodeID{allPointers},
		Check: func(g GraphReader, touched map[NodeID]struct{}) error {
			for node := range touched {
				tagged, taggedErr := g.HasRelationship(allPointers, node)
				if taggedErr != nil {
					return wrapInterfaceErr(taggedErr)
				}
				if !tagged {
					continue
				}

				if _, _, err := singleChildTarget(g, node); err != nil {
					return err
				}
			}

			return nil
		},
	})

	return &PointerRegistry{
		allPointers: allPointers,
	}, nil
}

// IsPointer reports whether id is currently tagged Pointer-kind via
// (AllPointers, id). graph is passed explicitly, never stored (see the
// PointerRegistry doc comment) -- passing the wrong graph value here is
// a caller error at the call site, not a hidden footgun inside this
// type.
func (p *PointerRegistry) IsPointer(graph GraphReader, id NodeID) (bool, error) {
	has, err := graph.HasRelationship(p.allPointers, id)
	return has, wrapInterfaceErr(err)
}

// currentTarget returns P's current single target, re-derived fresh from
// the underlying Graph on every call (see the PointerRegistry doc comment
// for why this is never cached).
//
// It requires P to exist and to be tagged Pointer-kind; otherwise it
// returns ErrNodeNotFound or ErrNotPointer respectively, without
// inspecting P's relationships at all. If P is tagged but currently has
// more than one outgoing relationship -- meaning some caller bypassed
// this registry and violated the Pointer invariant directly through the
// primitive Graph -- currentTarget returns ErrTooManyPointerTargets
// rather than silently picking one of them.
func (p *PointerRegistry) currentTarget(graph GraphReader, id NodeID) (target NodeID, hasTarget bool, err error) {
	if requireErr := p.requirePointer(graph, id); requireErr != nil {
		return 0, false, requireErr
	}

	return singleChildTarget(graph, id)
}

// requirePointer checks that id exists and is tagged Pointer-kind,
// returning ErrNodeNotFound or ErrNotPointer otherwise, without
// inspecting id's relationships. Shared by currentTarget, SetTarget and
// RemoveTarget (corrected here from the stale "setTargetTx and
// removeTargetTx" this comment used to say -- those tx-suffixed helpers
// were inlined into the exported methods themselves back in item 33 and
// no longer exist under those names). Delegates to the shared
// requireTagged helper -- see its doc comment for why this used to be
// its own three-line check.
func (p *PointerRegistry) requirePointer(graph GraphReader, id NodeID) error {
	return requireTagged(graph, id, p.allPointers, ErrNotPointer)
}

// Target returns P's current target.
//
// hasTarget is false when P is a valid, currently-empty Pointer. See
// currentTarget for the error cases: P missing, P not tagged Pointer-kind,
// or P's invariant already violated by an out-of-band Graph mutation.
func (p *PointerRegistry) Target(graph GraphReader, id NodeID) (target NodeID, hasTarget bool, err error) {
	return p.currentTarget(graph, id)
}

// SetTarget sets P's target to X, enforcing that P has at most one target
// both before and after the call.
//
// Both P and X must already exist, and P must already be tagged
// Pointer-kind (see NewPointer and TagAsPointer). Target's existence is
// checked before any mutation happens: if X did not exist and this check
// were skipped, a stale target could be removed before the new one failed
// to be added, losing data on a failed call. If P currently has no
// target, (P, X) is simply added. If P currently has exactly one target
// and it already equals X, this is an idempotent no-op. If P currently
// has exactly one different target, that relationship is removed and
// (P, X) is added in its place. If P currently has more than one target
// -- meaning the invariant was already violated by something outside
// this registry -- SetTarget makes no changes at all and returns
// ErrTooManyPointerTargets: it deliberately does not attempt to repair
// the violation by picking one existing target to keep or by clearing
// all of them, since either choice would be a silent, unrequested
// decision about data this registry did not create.
//
// Self-targeting, i.e. SetTarget(P, P), is allowed: self-relationships
// are permitted at the primitive layer (theorystate.md section 2.8)
// and nothing about the Pointer invariant rules it out.
//
// The whole operation -- reading P's current target, validating, and
// replacing it -- runs inside one Transact call on graph, so it is atomic
// as a whole under GraphActor, not merely each of its steps, and every
// read goes through tx so the decision and the write see the same state.
// If graph is a Tx the call nests inside the caller's transaction (see
// Transactor): a failure undoes only this call, and the at-most-one-target
// Checker runs at the outermost commit.
func (p *PointerRegistry) SetTarget(graph Transactor, id, target NodeID) error {
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		if requireErr := p.requirePointer(tx, id); requireErr != nil {
			return requireErr
		}

		exists, err := tx.NodeExists(target)
		if err != nil {
			return wrapInterfaceErr(err)
		}
		if !exists {
			return ErrNodeNotFound
		}

		return singleChildTargetSetTx(tx, tx, id, target)
	}))
}

// RemoveTarget clears P's target, if any.
//
// The returned bool reports whether a target was actually removed. If P
// currently has no target, this is a no-op returning (false, nil). If P
// currently has more than one target -- an already-violated invariant --
// RemoveTarget makes no changes and returns ErrTooManyPointerTargets, for
// the same reason given in SetTarget: this registry does not silently
// repair violations it did not create.
func (p *PointerRegistry) RemoveTarget(graph Transactor, id NodeID) (removed bool, err error) {
	// Running the removal through tx (rather than as a raw graph call)
	// also makes it visible to commit-time Checkers.
	return transactBool(graph, func(tx Tx) (bool, error) {
		if requireErr := p.requirePointer(tx, id); requireErr != nil {
			return false, requireErr
		}

		return singleChildTargetRemoveTx(tx, tx, id)
	})
}

// NewPointer creates a fresh NodeID and immediately tags it Pointer-kind.
//
// Because the node is freshly created, it has zero relationships and
// therefore trivially satisfies the Pointer invariant -- unlike
// TagAsPointer, no invariant check is needed here.
//
// The create and tag steps run inside Graph.Transact: if tagging were
// ever to fail after the node had already been created, the node would
// otherwise be left orphaned -- it would exist but never be discoverable
// as a Pointer. See the Txn doc comment and the PointerRegistry doc
// comment above for what this atomicity does and does not cover.
func (p *PointerRegistry) NewPointer(graph Transactor) (NodeID, error) {
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		return newPointerTx(tx, p.allPointers)
	})
}

// TagAsPointer tags an existing node id as Pointer-kind.
//
// Unlike NewPointer, id may already have relationships from before it
// became a Pointer, so TagAsPointer checks that id currently has at most
// one outgoing relationship before tagging it -- tagging a node that
// already has two or more outgoing relationships would immediately
// create an already-violated Pointer, which TagAsPointer refuses to do,
// returning ErrTooManyPointerTargets and leaving id untagged. id's
// existence is implicitly checked by the underlying FindOutgoing call,
// which returns ErrNodeNotFound if id does not exist.
//
// Tagging an id that is already tagged Pointer-kind is an idempotent
// success, exactly like the underlying Graph.AddRelationship being
// idempotent for an already-existing relationship.
func (p *PointerRegistry) TagAsPointer(graph Transactor, id NodeID) error {
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		outgoing, txErr := tx.FindOutgoing(id)
		if txErr != nil {
			return wrapInterfaceErr(txErr)
		}

		if len(outgoing) > 1 {
			return ErrTooManyPointerTargets
		}

		return addRelationshipTx(tx, p.allPointers, id)
	}))
}

// findUniqueTaggedParent returns the single parent of node that is tagged
// via (tag, parent), i.e. for which g.HasRelationship(tag, parent) holds.
//
// This is the shared reverse-lookup primitive behind
// PointerMetadataRegistry's two-hop subject -> slot -> metadata
// discovery. It requires node to exist. found is false if no tagged
// parent exists. If more than one tagged parent exists -- only reachable
// through an out-of-band Graph mutation -- ErrAmbiguousPointerMetadata is
// returned instead of arbitrarily picking one.
func findUniqueTaggedParent(g GraphReader, node, tag NodeID) (parent NodeID, found bool, err error) {
	incoming, err := g.FindIncoming(node)
	if err != nil {
		return 0, false, wrapInterfaceErr(err)
	}

	for _, rel := range incoming {
		has, hasErr := g.HasRelationship(tag, rel.From)
		if hasErr != nil {
			return 0, false, wrapInterfaceErr(hasErr)
		}
		if !has {
			continue
		}

		if found {
			return 0, false, ErrAmbiguousPointerMetadata
		}

		parent = rel.From
		found = true
	}

	return parent, found, nil
}

// findUniqueTaggedChild returns the single child of node that is tagged
// via (tag, child), i.e. for which g.HasRelationship(tag, child) holds.
//
// This is the forward-lookup counterpart to findUniqueTaggedParent: where
// findUniqueTaggedParent scans node's *parents* for one tagged tag,
// findUniqueTaggedChild scans node's *children* for one tagged tag. Used
// by PointerMetadataRegistryD to find a specific slot child of M by tag,
// rather than by exclusion/assumption about M's other children -- see the
// PointerMetadataRegistryD doc comment for why this is the fix over
// Representation C's exclusion-based approach.
//
// It requires node to exist. found is false if no tagged child exists. If
// more than one tagged child exists -- only reachable through an
// out-of-band Graph mutation -- ErrAmbiguousPointerMetadata is returned
// instead of arbitrarily picking one.
func findUniqueTaggedChild(g GraphReader, node, tag NodeID) (child NodeID, found bool, err error) {
	outgoing, err := g.FindOutgoing(node)
	if err != nil {
		return 0, false, wrapInterfaceErr(err)
	}

	for _, rel := range outgoing {
		has, hasErr := g.HasRelationship(tag, rel.To)
		if hasErr != nil {
			return 0, false, wrapInterfaceErr(hasErr)
		}
		if !has {
			continue
		}

		if found {
			return 0, false, ErrAmbiguousPointerMetadata
		}

		child = rel.To
		found = true
	}

	return child, found, nil
}

// exactlyOneTag reports which of tagA or tagB currently tags node via
// (tag, node), requiring exactly one of the two to hold. This is the
// shared axis-check behind CompositeSetRegistry operand descriptors' two
// orthogonal tag axes (theorystate.md section 80): operation kind
// (AllAdditiveOp/AllSubtractiveOp) and operand kind
// (AllScalarOperand/AllSetOperand).
//
// isA reports whether tagA (rather than tagB) is the one that holds. If
// neither or both hold -- only reachable through an out-of-band mutation,
// since CompositeSetRegistry.AddOperand always wires a fresh descriptor
// with exactly one tag per axis -- ErrInvalidOperandDescriptor is
// returned instead of guessing.
func exactlyOneTag(g GraphReader, node, tagA, tagB NodeID) (isA bool, err error) {
	hasA, err := g.HasRelationship(tagA, node)
	if err != nil {
		return false, wrapInterfaceErr(err)
	}

	hasB, err := g.HasRelationship(tagB, node)
	if err != nil {
		return false, wrapInterfaceErr(err)
	}

	switch {
	case hasA && !hasB:
		return true, nil
	case hasB && !hasA:
		return false, nil
	default:
		return false, ErrInvalidOperandDescriptor
	}
}

// locateBySubjectSlot finds node's metadata node and subject-slot node
// via the two-hop subject -> subject-slot -> metadata reverse lookup
// shared by both PointerMetadataRegistry (Representation C) and
// PointerMetadataRegistryD (Representation D): both representations
// identify the subject the same way, differing only in how they then
// locate the target. node must exist.
func locateBySubjectSlot(g GraphReader, node, allPointerMetadata, allSubjectSlots NodeID) (metadata, subjectSlot NodeID, found bool, err error) {
	subjectSlot, found, err = findUniqueTaggedParent(g, node, allSubjectSlots)
	if err != nil || !found {
		return 0, 0, found, err
	}

	metadata, found, err = findUniqueTaggedParent(g, subjectSlot, allPointerMetadata)
	if err != nil || !found {
		return 0, 0, found, err
	}

	return metadata, subjectSlot, true, nil
}

// ensureMetadataWithSubjectSlotTx returns subject's existing metadata/
// subject-slot pair (via locateBySubjectSlot), creating a fresh, empty
// one (M -> S -> subject, both tagged) if none exists yet, entirely
// against tx. The lookup and the creation happen in the same transaction,
// so two goroutines cannot both conclude "not found" and both create a
// pair (which would make every later lookup fail with
// ErrAmbiguousPointerMetadata). Shared by both PointerMetadataRegistry
// and PointerMetadataRegistryD, which build identical subject-side
// structure and differ only in how the target side is represented.
// Callers are responsible for checking that subject itself exists.
func ensureMetadataWithSubjectSlotTx(tx txReader, subject, allPointerMetadata, allSubjectSlots NodeID) (metadata, subjectSlot NodeID, err error) {
	var found bool
	metadata, subjectSlot, found, err = locateBySubjectSlot(tx, subject, allPointerMetadata, allSubjectSlots)
	if err != nil {
		return 0, 0, err
	}
	if found {
		return metadata, subjectSlot, nil
	}

	subjectSlot, err = createTaggedNodeTx(tx, allSubjectSlots)
	if err != nil {
		return 0, 0, err
	}
	if linkErr := addRelationshipTx(tx, subjectSlot, subject); linkErr != nil {
		return 0, 0, linkErr
	}

	metadata, err = createTaggedNodeTx(tx, allPointerMetadata)
	if err != nil {
		return 0, 0, err
	}
	if linkErr := addRelationshipTx(tx, metadata, subjectSlot); linkErr != nil {
		return 0, 0, linkErr
	}

	return metadata, subjectSlot, nil
}

// subjectMetadataBase holds the shared tag state and subject-side
// operations -- locate, ensureMetadata, EnsureMetadata, HasMetadata --
// shared identically by PointerMetadataRegistry (Representation C) and
// PointerMetadataRegistryD (Representation D). Both representations
// locate or create a subject's metadata node and subject-slot node in
// exactly the same way (via locateBySubjectSlot /
// ensureMetadataWithSubjectSlot); they differ only in how they then find
// the target, which is why only the shared subject-side logic is
// factored out here rather than merging the two types outright.
//
// PointerMetadataRegistry and PointerMetadataRegistryD each embed this
// struct anonymously, so its fields (allPointerMetadata, allSubjectSlots)
// and methods are promoted and usable exactly as if they were declared
// directly on the embedding type.
//
// Like every other registry in this file, subjectMetadataBase stores no
// graph reference of its own (theorystate.md section 90): every method
// below takes the graph it should operate against as an explicit
// parameter instead.
type subjectMetadataBase struct {
	allPointerMetadata NodeID
	allSubjectSlots    NodeID
}

// locate finds subject's metadata node and subject-slot node, if any.
// found is false if subject has no metadata yet. subject must exist.
func (b *subjectMetadataBase) locate(graph GraphReader, subject NodeID) (metadata, subjectSlot NodeID, found bool, err error) {
	return locateBySubjectSlot(graph, subject, b.allPointerMetadata, b.allSubjectSlots)
}

// ensureMetadataTx returns subject's existing metadata/subject-slot pair,
// creating a fresh, empty one (M -> S -> subject, both tagged) if none
// exists yet, against tx. The registries' SetTarget/SetDomain cores call
// this so that creating the metadata and setting the target are one
// atomic step: a later failure rolls the metadata creation back too.
func (b *subjectMetadataBase) ensureMetadataTx(tx txReader, subject NodeID) (metadata, subjectSlot NodeID, err error) {
	exists, err := tx.NodeExists(subject)
	if err != nil {
		return 0, 0, wrapInterfaceErr(err)
	}
	if !exists {
		return 0, 0, ErrNodeNotFound
	}

	return ensureMetadataWithSubjectSlotTx(tx, subject, b.allPointerMetadata, b.allSubjectSlots)
}

// EnsureMetadata returns subject's metadata node, creating an empty one
// if none exists yet.
func (b *subjectMetadataBase) EnsureMetadata(graph Transactor, subject NodeID) (NodeID, error) {
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		metadata, _, err := b.ensureMetadataTx(tx, subject)
		return metadata, err
	})
}

// HasMetadata reports whether subject currently has an associated
// metadata node, regardless of whether a target has been set.
func (b *subjectMetadataBase) HasMetadata(graph GraphReader, subject NodeID) (bool, error) {
	exists, err := graph.NodeExists(subject)
	if err != nil {
		return false, wrapInterfaceErr(err)
	}
	if !exists {
		return false, ErrNodeNotFound
	}

	_, _, found, err := b.locate(graph, subject)
	return found, err
}

// PointerMetadataRegistry implements Representation C (metadata
// structure) of the Pointer processor, theorystate.md section 10's
// generalized metadata construction (see also section 10b).
//
// Unlike PointerRegistry (Representations A and B), Representation C
// keeps the subject node's own direct children completely untouched by
// the pointer representation. Instead, a dedicated metadata node M
// records the association:
//
//	(AllPointerMetadata, M)
//	M -> S                                 M's subject-slot child
//	(AllPointerMetadataSubjectSlot, S)
//	S -> subject                           S identifies the actual subject
//	M -> target                            M's target child, if any
//
// Design note -- why the subject needs its own slot node S rather than M
// pointing directly at the subject (M -> subject): a naive two-edge
// scheme (M -> subject, M -> target) cannot represent target == subject.
// Because primitive relationships are unique pairs
// (theorystate.md section 2.4/2.6), "M -> subject" and
// "M -> target" would collapse into the identical single physical
// relationship whenever target == subject, making self-targeting
// indistinguishable from an empty target. A freshly-minted S node for the
// subject-slot (the role/occurrence-identity pattern named in
// theorystate.md section 75 -- the same move as ElementCapsule nodes
// in the Ordered List
// design) means M's two children -- S and the target -- can never
// collide, since S is never equal to any subject value.
//
// Given a subject, the metadata node is discovered via a two-hop reverse
// lookup: find S among subject's parents tagged
// AllPointerMetadataSubjectSlot, then find M among S's parents tagged
// AllPointerMetadata (see findUniqueTaggedParent). This relies on
// FindIncoming being indexed; it is not a full-graph scan.
//
// As with PointerRegistry, every method re-derives current state fresh
// from the Graph on every call rather than caching it, and fails loudly
// (ErrAmbiguousPointerMetadata, ErrTooManyPointerTargets) rather than
// silently repairing when an out-of-band mutation has violated an
// invariant.
//
// Known limitation, kept deliberately rather than fixed here: M's target
// is identified *by exclusion* -- "whichever of M's children isn't S must
// be the target" -- via singleChildTarget(m.graph, metadata, slot). This
// means M is implicitly assumed to have only these two children, ever;
// any future unrelated child added to M (tagged or not) would make
// target discovery fail loudly with ErrTooManyPointerTargets even though
// nothing about the actual target changed. This is really the same
// mistake as Representation A's "at most one child, period" -- just
// shifted up one level -- and it also matches
// theorystate.md section 10a's discussion of the original "M -> P, M
// -> I" sketch, which has an even sharper version of the same bug (those
// two relationships collapse into one whenever target == subject, since
// primitive relationships are unique pairs). PointerMetadataRegistryD
// (Representation D) is the corrected construction -- see its doc
// comment. This type is kept as-is, limitation and all, rather than
// patched or deleted: it is useful as a deliberately stricter
// representation for testing how higher-level code should react when a
// lower layer refuses something Representation D would allow
// (theorystate.md section 73).
type PointerMetadataRegistry struct {
	subjectMetadataBase
}

// NewPointerMetadataRegistry creates a PointerMetadataRegistry over
// graph, using allPointerMetadata to tag metadata nodes and
// allSubjectSlots to tag subject-slot nodes. Both must already exist --
// typically via NameRegistry.BootstrapNames(FoundationalNames).
//
// locate, ensureMetadata, EnsureMetadata, and HasMetadata are inherited
// unmodified from the embedded subjectMetadataBase, which is shared with
// PointerMetadataRegistryD -- see subjectMetadataBase's doc comment for
// why this subject-side logic is factored out rather than duplicated.
func NewPointerMetadataRegistry(graph GraphAPI, allPointerMetadata, allSubjectSlots NodeID) (*PointerMetadataRegistry, error) {
	existsMetadata, err := graph.NodeExists(allPointerMetadata)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}
	if !existsMetadata {
		return nil, ErrNodeNotFound
	}

	existsSlots, err := graph.NodeExists(allSubjectSlots)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}
	if !existsSlots {
		return nil, ErrNodeNotFound
	}

	// Registers a Checker (see Graph.RegisterChecker) enforcing this
	// representation's own "at most one target, excluding the
	// subject-slot" invariant at commit time, mirroring
	// NewPointerRegistry's identical eager/lazy pairing -- see that
	// constructor's doc comment for the full reasoning. A node touched
	// by a transaction that has no discoverable subject-slot at all is
	// skipped rather than treated as a violation: that shape can only
	// arise from a separate out-of-band mutation removing the
	// subject-slot after the fact (ensureMetadataWithSubjectSlot always
	// creates M and its subject-slot together), and is not this
	// Checker's invariant to enforce -- it exists to catch a violated
	// target count, which cannot even be evaluated without first
	// knowing which child to exclude as the subject-slot.
	graph.RegisterChecker(Checker{
		Name: fmt.Sprintf("PointerMetadataRegistry(tag=%d)", allPointerMetadata),
		Tags: []NodeID{allPointerMetadata},
		Check: func(g GraphReader, touched map[NodeID]struct{}) error {
			for node := range touched {
				tagged, taggedErr := g.HasRelationship(allPointerMetadata, node)
				if taggedErr != nil {
					return wrapInterfaceErr(taggedErr)
				}
				if !tagged {
					continue
				}

				slot, found, err := findUniqueTaggedChild(g, node, allSubjectSlots)
				if err != nil {
					return err
				}
				if !found {
					continue
				}

				if _, _, targetErr := singleChildTarget(g, node, slot); targetErr != nil {
					return targetErr
				}
			}

			return nil
		},
	})

	return &PointerMetadataRegistry{
		subjectMetadataBase: subjectMetadataBase{
			allPointerMetadata: allPointerMetadata,
			allSubjectSlots:    allSubjectSlots,
		},
	}, nil
}

// Target returns subject's current target via its metadata node, if any.
//
// hasTarget is false both when subject has no metadata node at all and
// when it has one with no target set yet -- callers that need to
// distinguish those two cases should use HasMetadata first.
func (m *PointerMetadataRegistry) Target(graph GraphReader, subject NodeID) (target NodeID, hasTarget bool, err error) {
	exists, err := graph.NodeExists(subject)
	if err != nil {
		return 0, false, wrapInterfaceErr(err)
	}
	if !exists {
		return 0, false, ErrNodeNotFound
	}

	metadata, slot, found, err := m.locate(graph, subject)
	if err != nil {
		return 0, false, err
	}
	if !found {
		return 0, false, nil
	}

	return singleChildTarget(graph, metadata, slot)
}

// SetTarget sets subject's target to target, creating subject's metadata
// node first if it does not exist yet (see EnsureMetadata).
//
// Self-targeting (SetTarget(subject, subject)) is explicitly supported
// and correctly distinguished from an empty target -- see the
// PointerMetadataRegistry doc comment for why the subject-slot
// indirection is what makes this possible.
func (m *PointerMetadataRegistry) SetTarget(graph Transactor, subject, target NodeID) error {
	// Creating the metadata (if needed), reading the current target and
	// replacing it are one atomic step.
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		exists, err := tx.NodeExists(target)
		if err != nil {
			return wrapInterfaceErr(err)
		}
		if !exists {
			return ErrNodeNotFound
		}

		metadata, slot, err := m.ensureMetadataTx(tx, subject)
		if err != nil {
			return err
		}

		return singleChildTargetSetTx(tx, tx, metadata, target, slot)
	}))
}

// RemoveTarget clears subject's target, if any. The metadata/slot nodes
// themselves are left in place (no cascade deletion, consistent with
// theorystate.md section 18's rejection of
// deleteNodeAndRelationships); an empty metadata node is a valid,
// meaningful state, exactly like an empty Pointer in Representation A.
func (m *PointerMetadataRegistry) RemoveTarget(graph Transactor, subject NodeID) (removed bool, err error) {
	return transactBool(graph, func(tx Tx) (bool, error) {
		exists, existsErr := tx.NodeExists(subject)
		if existsErr != nil {
			return false, wrapInterfaceErr(existsErr)
		}
		if !exists {
			return false, ErrNodeNotFound
		}

		metadata, slot, found, locateErr := m.locate(tx, subject)
		if locateErr != nil {
			return false, locateErr
		}
		if !found {
			return false, nil
		}

		return singleChildTargetRemoveTx(tx, tx, metadata, slot)
	})
}

// PointerMetadataRegistryD implements Representation D, a corrected
// generalization of Representation C (PointerMetadataRegistry) /
// theorystate.md section 10a's original "M -> P, M -> I" sketch.
//
// Representation C and that original sketch share a real bug: they each
// identify one of M's two children *by exclusion* -- "whichever
// child isn't the subject/subject-slot must be the target/information
// node" (or, in the even earlier sketch, "M -> P" and "M -> I"
// collapse into the same relationship whenever target == subject, since
// primitive relationships are unique pairs -- theorystate.md section
// 2.4/2.6). Both are really the same underlying mistake as Representation
// A's "at most one child, no room for anything else": M is implicitly
// assumed to have *exactly* the relevant children and nothing more, which
// directly contradicts section 10a's own stated goal ("This is a general
// construction, not merely a pointer trick" -- i.e. M should be free to
// grow additional, unrelated children later without breaking discovery).
//
// Representation D fixes this by giving *both* roles their own dedicated,
// freshly-minted, explicitly tagged slot node, exactly symmetric with
// each other:
//
//	(AllPointerMetadata, M)
//	M -> U1                                  M's subject-slot child
//	(AllPointerMetadataSubjectSlot, U1)
//	U1 -> subject                            U1 identifies the subject
//	M -> U2                                  M's target-slot child (once set)
//	(AllPointerMetadataTargetSlot, U2)
//	U2 -> target                             U2 identifies the target
//
// Both U1 and U2 are discovered by their own tag, not by exclusion, so M
// can carry any number of additional, unrelated children -- tagged or
// not, now or added later -- without ever disturbing subject or target
// discovery. This is the same occurrence/role-identity pattern already
// named in theorystate.md section 75, applied twice over instead of
// once.
//
// Representation C (PointerMetadataRegistry) is kept, not deleted, even
// though Representation D supersedes it as the *correct* general
// construction: C's exclusion-based limitation is now understood and
// named rather than accidental, and deliberately keeping the more
// restrictive representation available is useful for testing how
// higher-level code should react when a lower layer is stricter than
// necessary (theorystate.md section 73's commit-time interception
// question -- should such a restriction be enforced, ignored, or merely
// reported?). Do not add further logic to C to "fix" it; add it here to
// D instead.
//
// As with every other registry in this file, every method here
// re-derives current state fresh from the Graph on every call, and fails
// loudly (ErrAmbiguousPointerMetadata, ErrTooManyPointerTargets) rather
// than silently repairing an out-of-band invariant violation.
type PointerMetadataRegistryD struct {
	subjectMetadataBase
	allTargetSlots NodeID
}

// NewPointerMetadataRegistryD creates a PointerMetadataRegistryD over
// graph, using allPointerMetadata to tag metadata nodes, allSubjectSlots
// to tag subject-slot nodes, and allTargetSlots to tag target-slot nodes.
// All three must already exist -- typically via
// NameRegistry.BootstrapNames(FoundationalNames).
//
// locate, ensureMetadata, EnsureMetadata, and HasMetadata are inherited
// unmodified from the embedded subjectMetadataBase, which is shared with
// PointerMetadataRegistry -- see subjectMetadataBase's doc comment for
// why this subject-side logic is factored out rather than duplicated.
func NewPointerMetadataRegistryD(graph GraphAPI, allPointerMetadata, allSubjectSlots, allTargetSlots NodeID) (*PointerMetadataRegistryD, error) {
	existsMetadata, err := graph.NodeExists(allPointerMetadata)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}
	if !existsMetadata {
		return nil, ErrNodeNotFound
	}

	existsSubjectSlots, err := graph.NodeExists(allSubjectSlots)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}
	if !existsSubjectSlots {
		return nil, ErrNodeNotFound
	}

	existsTargetSlots, err := graph.NodeExists(allTargetSlots)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}
	if !existsTargetSlots {
		return nil, ErrNodeNotFound
	}

	// Registers a Checker (see Graph.RegisterChecker) enforcing this
	// representation's own "at most one target" invariant at commit
	// time. Unlike Representation C, D's target lives on its own
	// independently-tagged target-slot node (U2), discovered entirely by
	// tag rather than by exclusion, so the invariant to check here is
	// simply "does a node tagged allTargetSlots have at most one child"
	// -- no exclusion set needed, mirroring the reasoning in
	// NewPointerRegistry's identical Checker.
	graph.RegisterChecker(Checker{
		Name: fmt.Sprintf("PointerMetadataRegistryD(tag=%d)", allPointerMetadata),
		Tags: []NodeID{allTargetSlots},
		Check: func(g GraphReader, touched map[NodeID]struct{}) error {
			for node := range touched {
				tagged, taggedErr := g.HasRelationship(allTargetSlots, node)
				if taggedErr != nil {
					return wrapInterfaceErr(taggedErr)
				}
				if !tagged {
					continue
				}

				if _, _, err := singleChildTarget(g, node); err != nil {
					return err
				}
			}

			return nil
		},
	})

	return &PointerMetadataRegistryD{
		subjectMetadataBase: subjectMetadataBase{
			allPointerMetadata: allPointerMetadata,
			allSubjectSlots:    allSubjectSlots,
		},
		allTargetSlots: allTargetSlots,
	}, nil
}

// targetSlot returns metadata's current target-slot child (U2), if any,
// found by tag rather than by exclusion -- see the PointerMetadataRegistryD
// doc comment for why this is the fix over Representation C.
func (m *PointerMetadataRegistryD) targetSlot(graph GraphReader, metadata NodeID) (slot NodeID, found bool, err error) {
	return findUniqueTaggedChild(graph, metadata, m.allTargetSlots)
}

// targetOfMetadata returns the current target recorded on metadata node
// metadata, if any: its target-slot's single child. hasTarget is false
// when metadata has no target-slot yet, or has one with no target set.
// This is Target's tail once the subject has already been resolved to
// its metadata node, and is also what the shared domain Checker uses
// (metadata nodes are its anchors -- see domainConstraint.registerChecker).
func (m *PointerMetadataRegistryD) targetOfMetadata(graph GraphReader, metadata NodeID) (target NodeID, hasTarget bool, err error) {
	slot, found, err := m.targetSlot(graph, metadata)
	if err != nil {
		return 0, false, err
	}
	if !found {
		return 0, false, nil
	}

	return singleChildTarget(graph, slot)
}

// Target returns subject's current target via its metadata/target-slot
// nodes, if any.
//
// hasTarget is false when subject has no metadata node at all, when it
// has one with no target-slot yet, or when it has a target-slot with no
// target set yet -- callers that need to distinguish those cases should
// use HasMetadata and EnsureMetadata directly.
func (m *PointerMetadataRegistryD) Target(graph GraphReader, subject NodeID) (target NodeID, hasTarget bool, err error) {
	exists, err := graph.NodeExists(subject)
	if err != nil {
		return 0, false, wrapInterfaceErr(err)
	}
	if !exists {
		return 0, false, ErrNodeNotFound
	}

	metadata, _, found, err := m.locate(graph, subject)
	if err != nil {
		return 0, false, err
	}
	if !found {
		return 0, false, nil
	}

	return m.targetOfMetadata(graph, metadata)
}

// SetTarget sets subject's target to target, creating subject's metadata
// node and/or target-slot node first if they do not exist yet.
//
// Self-targeting (SetTarget(subject, subject)) is supported: U2 (the
// target-slot) is a freshly-minted node distinct from subject, U1, and M,
// so U2 -> target can never collide with any other relationship no
// matter what target equals.
func (m *PointerMetadataRegistryD) SetTarget(graph Transactor, subject, target NodeID) error {
	// Creating the metadata and/or target-slot (if needed), reading the
	// current target and replacing it are one atomic step.
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		exists, err := tx.NodeExists(target)
		if err != nil {
			return wrapInterfaceErr(err)
		}
		if !exists {
			return ErrNodeNotFound
		}

		metadata, _, err := m.ensureMetadataTx(tx, subject)
		if err != nil {
			return err
		}

		slot, found, err := m.targetSlot(tx, metadata)
		if err != nil {
			return err
		}

		if found {
			return singleChildTargetSetTx(tx, tx, slot, target)
		}

		newSlot, err := createTaggedNodeTx(tx, m.allTargetSlots)
		if err != nil {
			return err
		}
		if linkErr := addRelationshipTx(tx, metadata, newSlot); linkErr != nil {
			return linkErr
		}

		return addRelationshipTx(tx, newSlot, target)
	}))
}

// RemoveTarget clears subject's target, if any. The metadata/subject-
// slot/target-slot nodes themselves are left in place (no cascade
// deletion, consistent with theorystate.md section 18's rejection of
// deleteNodeAndRelationships); an empty target-slot -- or no target-slot
// at all -- is a valid, meaningful state.
func (m *PointerMetadataRegistryD) RemoveTarget(graph Transactor, subject NodeID) (removed bool, err error) {
	return transactBool(graph, func(tx Tx) (bool, error) {
		exists, existsErr := tx.NodeExists(subject)
		if existsErr != nil {
			return false, wrapInterfaceErr(existsErr)
		}
		if !exists {
			return false, ErrNodeNotFound
		}

		metadata, _, found, locateErr := m.locate(tx, subject)
		if locateErr != nil {
			return false, locateErr
		}
		if !found {
			return false, nil
		}

		slot, found, slotErr := m.targetSlot(tx, metadata)
		if slotErr != nil {
			return false, slotErr
		}
		if !found {
			return false, nil
		}

		return singleChildTargetRemoveTx(tx, tx, slot)
	})
}

// CapsuleRegistry implements the ElementCapsule primitive of Ordered
// Lists (theorystate.md section 11 / 11a): each list-element
// *occurrence* gets its own freshly-minted
// NodeID (the capsule) rather than reusing the value's own NodeID, so the
// same value can occur multiple times in a list through different
// capsules (theorystate.md section 75's occurrence/role-identity
// pattern).
//
// A capsule's previous, value, and next roles are each represented by a
// dedicated intermediary slot node -- exactly Pointer Representation B
// (theorystate.md section 10 / 10b), applied three times under
// three different tags:
//
//	(AllElementCapsules, capsule)
//	capsule -> Uprev    (AllElementCapsulePrevSlot, Uprev)   Uprev -> prevCapsule
//	capsule -> Uvalue   (AllElementCapsuleValueSlot, Uvalue) Uvalue -> value
//	capsule -> Unext    (AllElementCapsuleNextSlot, Unext)   Unext -> nextCapsule
//
// Each slot's own target is enforced to be at most one using the exact
// same PointerRegistry type already used for Representations A and B;
// CapsuleRegistry embeds three PointerRegistry instances -- one per role,
// distinguished only by tag, per theorystate.md section 76's
// tag-parameterization discipline -- rather than reimplementing "at most
// one target" a third time.
//
// The three slot roles are discovered by their own tag
// (findUniqueTaggedChild), never by position or by exclusion, so a
// capsule remains free to carry additional, unrelated children later
// without disturbing role discovery -- the same discipline established
// for PointerMetadataRegistryD.
//
// CapsuleRegistry does not itself know about lists, heads, or tails, and
// does not itself decide when a capsule is linked into or unlinked from a
// list -- it only mints and wires individual capsules and their three
// roles. List-level operations (append/prepend/insert, head/tail
// bookkeeping) are a separate, higher layer to be built on top of this
// one; not implemented yet. Per discussion, head/tail is expected to be a
// plain (AllHEADs, capsule) / (AllTAILs, capsule) tag pair discovered via
// findUniqueTaggedChild, not a further Pointer-style indirection: unlike
// Representation C/D's subject/target collision risk, (AllHEADs, X) and
// (AllTAILs, X) are already two distinct relationships even when the same
// capsule X is simultaneously both head and tail (a single-element list).
type CapsuleRegistry struct {
	allElementCapsules NodeID
	prevSlots          *PointerRegistry
	valueSlots         *PointerRegistry
	nextSlots          *PointerRegistry
}

// NewCapsuleRegistry creates a CapsuleRegistry over graph.
// allElementCapsules tags capsule-kind nodes; allPrevSlot, allValueSlot,
// and allNextSlot each tag a capsule's respective role-slot node. All
// four must already exist -- typically via
// NameRegistry.BootstrapNames(FoundationalNames). allPrevSlot,
// allValueSlot, and allNextSlot's existence is checked by the embedded
// NewPointerRegistry calls; allElementCapsules is checked here.
func NewCapsuleRegistry(graph GraphAPI, allElementCapsules, allPrevSlot, allValueSlot, allNextSlot NodeID) (*CapsuleRegistry, error) {
	exists, err := graph.NodeExists(allElementCapsules)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}
	if !exists {
		return nil, ErrNodeNotFound
	}

	prevSlots, err := NewPointerRegistry(graph, allPrevSlot)
	if err != nil {
		return nil, err
	}

	valueSlots, err := NewPointerRegistry(graph, allValueSlot)
	if err != nil {
		return nil, err
	}

	nextSlots, err := NewPointerRegistry(graph, allNextSlot)
	if err != nil {
		return nil, err
	}

	c := &CapsuleRegistry{
		allElementCapsules: allElementCapsules,
		prevSlots:          prevSlots,
		valueSlots:         valueSlots,
		nextSlots:          nextSlots,
	}

	// Registered against c itself (rather than against the individual
	// tag NodeIDs, the way the simpler registries above do), since this
	// Checker's Check closure needs c.wellFormed -- which needs the
	// fully assembled CapsuleRegistry, not just the three underlying
	// PointerRegistry instances, to also check per-slot ownership, not
	// merely per-slot cardinality. c is fully initialized above before
	// this closure is ever invoked; capturing it by reference here is
	// safe because Check only ever runs later, during some future
	// Graph.Transact call, never during this constructor itself.
	graph.RegisterChecker(Checker{
		Name: fmt.Sprintf("CapsuleRegistry(tag=%d)", allElementCapsules),
		Tags: []NodeID{allElementCapsules},
		Check: func(g GraphReader, touched map[NodeID]struct{}) error {
			for node := range touched {
				tagged, taggedErr := g.HasRelationship(allElementCapsules, node)
				if taggedErr != nil {
					return wrapInterfaceErr(taggedErr)
				}
				if !tagged {
					continue
				}

				if err2 := c.wellFormed(g, node); err2 != nil {
					return err2
				}
			}

			return nil
		},
	})

	return c, nil
}

// IsCapsule reports whether id is currently tagged
// (AllElementCapsules, id).
func (c *CapsuleRegistry) IsCapsule(graph GraphReader, id NodeID) (bool, error) {
	has, err := graph.HasRelationship(c.allElementCapsules, id)
	return has, wrapInterfaceErr(err)
}

// requireCapsule checks that id exists and is tagged
// (AllElementCapsules, id), returning ErrNodeNotFound or ErrNotCapsule
// otherwise. Delegates to the shared requireTagged helper (see its doc
// comment on PointerRegistry.requirePointer).
func (c *CapsuleRegistry) requireCapsule(graph GraphReader, id NodeID) error {
	return requireTagged(graph, id, c.allElementCapsules, ErrNotCapsule)
}

// slotFor returns capsule's role-slot child tagged via (tag, slot) --
// e.g. its prev, value, or next slot -- found by tag rather than by
// position, so a capsule may carry additional, unrelated children later
// without disturbing role discovery. found is false if capsule has no
// such slot yet, which for a capsule created via NewCapsule should only
// happen due to an out-of-band mutation, since NewCapsule always creates
// all three slots up front.
//
// The discovered slot must also have capsule as its unique
// AllElementCapsules-tagged parent. This second check is important because
// the primitive Graph permits arbitrary additional parents: a raw mutation
// could otherwise make one capsule point at another capsule's role slot and
// silently alias that role. Unrelated non-capsule parents remain permitted;
// two distinct capsule-tagged parents produce ErrAmbiguousPointerMetadata.
// capsule's existence is checked implicitly by the underlying lookups.
func (c *CapsuleRegistry) slotFor(graph GraphReader, capsule, tag NodeID) (slot NodeID, found bool, err error) {
	slot, found, err = findUniqueTaggedChild(graph, capsule, tag)
	if err != nil || !found {
		return slot, found, err
	}

	owner, foundOwner, err := findUniqueTaggedParent(graph, slot, c.allElementCapsules)
	if err != nil {
		return 0, false, err
	}
	if !foundOwner || owner != capsule {
		return 0, false, ErrAmbiguousPointerMetadata
	}

	return slot, true, nil
}

// wellFormed reports whether capsule currently has exactly the fixed
// shape buildCapsuleTx itself establishes: all three role slots (prev,
// value, next) present and uniquely owned by capsule (via slotFor), and
// each slot's own "at most one target" Pointer invariant intact (via the
// underlying PointerRegistry.Target for that role).
//
// This bundles into one comprehensive verdict what was previously only
// answerable by making three separate slotFor/Value/Prev/Next-style
// calls and noticing if any of them failed -- there was no single
// function to ask "is this capsule well-formed, full stop" before this.
// It exists primarily to back the Checker registered by
// NewCapsuleRegistry (see Graph.RegisterChecker) for eager, commit-time
// enforcement.
//
// DeleteCapsule deliberately does not call this: DeleteCapsule's own
// all-or-nothing teardown already gets an equivalent guarantee for free
// by attempting the real deletes directly and relying on Transact's
// existing rollback if any of them turns out to fail (see DeleteCapsule's
// own doc comment) -- a pre-check here would only be redundant work for
// that specific caller, not a missed reuse opportunity.
func (c *CapsuleRegistry) wellFormed(graph GraphReader, capsule NodeID) error {
	isCapsule, err := c.IsCapsule(graph, capsule)
	if err != nil {
		return err
	}
	if !isCapsule {
		return ErrNotCapsule
	}

	for _, slots := range []*PointerRegistry{c.prevSlots, c.valueSlots, c.nextSlots} {
		slot, found, slotErr := c.slotFor(graph, capsule, slots.allPointers)
		if slotErr != nil {
			return slotErr
		}
		if !found {
			return ErrNotCapsule
		}

		if _, _, targetErr := slots.Target(graph, slot); targetErr != nil {
			return targetErr
		}
	}

	return nil
}

// buildCapsuleTx creates a fresh capsule NodeID, tags it via
// (allElementCapsules, capsule), and wires all three of its role slots
// (prev, value, next) -- each via the shared newPointerTx create-and-tag
// sequence -- against tx. This is the body behind
// CapsuleRegistry.NewCapsule, factored out as a free function
// parameterized entirely over tag NodeIDs. Callers that mint a capsule as
// one step of a larger operation (ListRegistry.Append/Prepend/
// InsertAfter) call NewCapsule with their own tx, which nests.
//
// The value slot's target is set to value immediately, since a freshly
// created slot trivially satisfies the Pointer invariant (it starts
// childless), exactly like PointerRegistry.NewPointer. The prev and next
// slots are left empty: a capsule with no preceding or following
// neighbor is a normal, valid state -- e.g. a single-element list's sole
// capsule is simultaneously head and tail, with both slots empty.
func buildCapsuleTx(tx txOps, allElementCapsules, allPrevSlot, allValueSlot, allNextSlot, value NodeID) (NodeID, error) {
	capsule, err := createTaggedNodeTx(tx, allElementCapsules)
	if err != nil {
		return 0, err
	}

	prevSlot, err := newPointerTx(tx, allPrevSlot)
	if err != nil {
		return 0, err
	}
	if _, err2 := tx.AddRelationship(capsule, prevSlot); err2 != nil {
		return 0, wrapInterfaceErr(err2)
	}

	valueSlot, err := newPointerTx(tx, allValueSlot)
	if err != nil {
		return 0, err
	}
	if _, err3 := tx.AddRelationship(capsule, valueSlot); err3 != nil {
		return 0, wrapInterfaceErr(err3)
	}
	if _, err4 := tx.AddRelationship(valueSlot, value); err4 != nil {
		return 0, wrapInterfaceErr(err4)
	}

	nextSlot, err := newPointerTx(tx, allNextSlot)
	if err != nil {
		return 0, err
	}

	_, err = tx.AddRelationship(capsule, nextSlot)
	if err != nil {
		return 0, wrapInterfaceErr(err)
	}

	return capsule, nil
}

// setSlotTarget rewires capsule's role slot -- the one slots is
// parameterized on (prev, value or next) -- to target, as one
// transaction (nested if graph is a Tx). The slot is discovered through
// slotFor, so the ownership check the read accessors apply also guards
// every write; ErrNotCapsule is returned if capsule has no such slot.
// This is the shared body behind the exported SetPrev/SetNext/SetValue,
// so each of them reads the slot and writes it in one transaction.
func (c *CapsuleRegistry) setSlotTarget(graph Transactor, capsule NodeID, slots *PointerRegistry, target NodeID) error {
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		slot, found, err := c.slotFor(tx, capsule, slots.allPointers)
		if err != nil {
			return err
		}
		if !found {
			return ErrNotCapsule
		}

		return slots.SetTarget(tx, slot, target)
	}))
}

// removeSlotTarget clears capsule's role slot -- the one slots is
// parameterized on -- as one transaction (nested if graph is a Tx). The
// slot is discovered through slotFor (ownership-checked); ErrNotCapsule
// is returned if capsule has no such slot. This is the shared body behind
// the exported RemovePrev/RemoveNext, so a capsule's own links can be
// cleared as part of the same transaction that relinks its neighbors.
func (c *CapsuleRegistry) removeSlotTarget(graph Transactor, capsule NodeID, slots *PointerRegistry) (removed bool, err error) {
	return transactBool(graph, func(tx Tx) (bool, error) {
		slot, found, slotErr := c.slotFor(tx, capsule, slots.allPointers)
		if slotErr != nil {
			return false, slotErr
		}
		if !found {
			return false, ErrNotCapsule
		}

		return slots.RemoveTarget(tx, slot)
	})
}

// NewCapsule creates a fresh capsule NodeID, tags it
// (AllElementCapsules, capsule), and wires all three of its role slots
// (prev, value, next), entirely inside one transaction (nested if graph
// is a Tx), via buildCapsuleTx.
//
// value must already exist.
func (c *CapsuleRegistry) NewCapsule(graph Transactor, value NodeID) (NodeID, error) {
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		exists, err := tx.NodeExists(value)
		if err != nil {
			return 0, wrapInterfaceErr(err)
		}
		if !exists {
			return 0, ErrNodeNotFound
		}

		return buildCapsuleTx(tx, c.allElementCapsules, c.prevSlots.allPointers, c.valueSlots.allPointers, c.nextSlots.allPointers, value)
	})
}

// Value returns capsule's current value, i.e. its value slot's target.
//
// hasValue is false only if capsule's value slot has no target set --
// which should not occur for any capsule created via NewCapsule, since
// NewCapsule always sets the value slot's target immediately. It can
// only arise from an out-of-band mutation (e.g. RemoveTarget called
// directly through the underlying value PointerRegistry).
func (c *CapsuleRegistry) Value(graph GraphReader, capsule NodeID) (value NodeID, hasValue bool, err error) {
	slot, found, err := c.slotFor(graph, capsule, c.valueSlots.allPointers)
	if err != nil {
		return 0, false, err
	}
	if !found {
		return 0, false, ErrNotCapsule
	}

	return c.valueSlots.Target(graph, slot)
}

// SetValue replaces capsule's value.
func (c *CapsuleRegistry) SetValue(graph Transactor, capsule, value NodeID) error {
	return c.setSlotTarget(graph, capsule, c.valueSlots, value)
}

// CapsulesWithValue returns every capsule, anywhere in the graph, whose
// value slot currently targets value -- not scoped to any particular
// list.
//
// This is the reverse-lookup counterpart to Value(capsule): Value walks
// capsule -> valueSlot -> value forward; CapsulesWithValue walks
// value -> valueSlot -> capsule backward, starting from
// Graph.FindIncoming(value) (an indexed map lookup, not a scan of any
// list). This is the realization behind ListRegistry.Contains/
// OccurrencesOf below: theorystate.md section 11's open question
// about adding "a Set-like index atop a List... for doesElementExist(X)"
// turns out not to need any new node, tag, or index structure at all --
// the ElementCapsule/value-slot wiring already built for ordinary list
// traversal already has everything a reverse lookup needs, exactly
// because each list-element occurrence already has its own
// freshly-minted, uniquely-tagged identity (theorystate.md section
// 75). The only thing missing was asking the question from the value's
// side instead of the list's side -- the same realization already
// implicit in how InsertAfter/Remove check list membership via a direct
// (list, capsule) relationship lookup instead of walking the list to
// find afterCapsule/capsule.
//
// Candidates are found via value's incoming relationships, filtered to
// those actually tagged as value-slots (IsPointer under this registry's
// value-slot tag) -- value may well have other, unrelated incoming
// relationships elsewhere in the graph (a Pointer target, a
// PointerMetadata target, etc.), which are silently skipped rather than
// mistaken for capsule occurrences.
//
// Naming note worth being explicit about, raised in review: valueSlots
// is a *PointerRegistry constructed with allValueSlot (i.e.
// AllElementCapsuleValueSlot) as its own tag -- not with the separate,
// generic AllPointers tag; see NewCapsuleRegistry. So
// c.valueSlots.IsPointer(slot) here checks exactly one relationship,
// (AllElementCapsuleValueSlot, slot), never a second, independent
// (AllPointers, slot) fact -- a value slot is never tagged both ways.
// IsPointer is still the method's name, inherited unmodified from
// PointerRegistry per theorystate.md section 76's
// tag-parameterization discipline (one type, no branching on which tag
// it holds), which makes it easy to misread this call as depending on
// two independent tags when only one ever exists.
// TestCapsuleRoleSlotsAreNotTaggedWithGenericAllPointers pins this down
// directly, so that constructing CapsuleRegistry's three slot registries
// against the shared generic AllPointers tag instead of their own
// distinct role tags -- which would silently break slotFor's per-role
// discovery -- would be caught immediately.
//
// Each qualifying slot's owning capsule is then found via
// findUniqueTaggedParent(slot, allElementCapsules) -- deliberately a
// *tagged* parent lookup, not a "the slot has exactly one parent, full
// stop" lookup. A role-slot node is free to acquire any number of
// additional, unrelated parents over time (some future metadata
// structure referencing the slot node itself, for its own reasons --
// nothing in this file prevents that, since a node may have any number
// of parents, theorystate.md section 2.8) without that
// being confused for a second owning capsule. ErrAmbiguousPointerMetadata
// (the same error findUniqueTaggedParent/findUniqueTaggedChild already
// return elsewhere in this file for an analogous ambiguity) is returned
// only if two distinct *capsule-tagged* nodes both claim the same slot --
// a genuine invariant violation, since buildCapsuleTx wires each slot to
// exactly one owning capsule at creation and nothing legitimate ever
// adds a second one. A slot with no capsule-tagged parent at all (found
// == false, e.g. after some out-of-band edit removed the owning edge) is
// silently skipped rather than fabricated. Like slotFor's other callers
// (Value, Prev, Next, ...), this does not separately re-verify that the
// discovered capsule's own value slot (via slotFor) is this exact slot --
// a well-formed graph only ever wires that edge to match, the same level
// of defensiveness already used elsewhere in this file.
//
// Running time is proportional to the number of incoming relationships
// value happens to have across the whole graph -- typically small and
// unrelated to the length of any list value occurs in -- not to the
// length of any particular list.
//
// value need not currently be tagged or used as a capsule value at all.
// If value does not exist, this fails with ErrNodeNotFound, via the
// underlying Graph.FindIncoming(value) call.
//
// The returned capsules are in no particular semantic order (they follow
// Graph.FindIncoming's own deterministic sort by slot NodeID, which does
// not necessarily correspond to capsule creation order).
//
// Reading value's incoming relationships is now paged internally
// (theorystate.md section 105), capsulesWithValuePageSize at a time, so a
// value referenced from a very large number of places -- the "very popular
// value node" case that section names directly -- does not require
// materializing its entire incoming edge list in memory just to filter it
// down to genuine value-slot owners. A backend that pages natively
// (BoltGraph, a transaction over it, and BoltGraph or GraphActor
// forwarding to it) reads one bounded page at a time. Any other reader is
// read in full exactly once and sliced (see pageIterator), so this never
// costs more than the single FindIncoming it replaced.
func (c *CapsuleRegistry) CapsulesWithValue(graph GraphReader, value NodeID) ([]NodeID, error) {
	var capsules []NodeID

	pages := incomingPages(graph, value, capsulesWithValuePageSize)

	for {
		page, err := pages.next()
		if err != nil {
			return nil, err
		}

		if len(page) == 0 {
			return capsules, nil
		}

		for _, rel := range page {
			slot := rel.From

			isValueSlot, isSlotErr := c.valueSlots.IsPointer(graph, slot)
			if isSlotErr != nil {
				return nil, isSlotErr
			}
			if !isValueSlot {
				continue
			}

			capsule, found, findErr := findUniqueTaggedParent(graph, slot, c.allElementCapsules)
			if findErr != nil {
				return nil, findErr
			}
			if !found {
				continue
			}

			capsules = append(capsules, capsule)
		}
	}
}

// capsulesWithValuePageSize bounds each internal page CapsulesWithValue
// reads while walking a value's incoming relationships (theorystate.md
// section 105). A var, not a const, specifically so tests can shrink it to
// exercise the multi-page loop without needing thousands of capsules.
var capsulesWithValuePageSize = 1000

// Prev returns capsule's previous-capsule link, if any. hasPrev is false
// for a capsule currently at the head of its list.
func (c *CapsuleRegistry) Prev(graph GraphReader, capsule NodeID) (prev NodeID, hasPrev bool, err error) {
	slot, found, err := c.slotFor(graph, capsule, c.prevSlots.allPointers)
	if err != nil {
		return 0, false, err
	}
	if !found {
		return 0, false, ErrNotCapsule
	}

	return c.prevSlots.Target(graph, slot)
}

// SetPrev sets capsule's previous-capsule link.
func (c *CapsuleRegistry) SetPrev(graph Transactor, capsule, prev NodeID) error {
	return c.setSlotTarget(graph, capsule, c.prevSlots, prev)
}

// RemovePrev clears capsule's previous-capsule link, if any.
func (c *CapsuleRegistry) RemovePrev(graph Transactor, capsule NodeID) (removed bool, err error) {
	return c.removeSlotTarget(graph, capsule, c.prevSlots)
}

// Next returns capsule's next-capsule link, if any. hasNext is false for
// a capsule currently at the tail of its list.
func (c *CapsuleRegistry) Next(graph GraphReader, capsule NodeID) (next NodeID, hasNext bool, err error) {
	slot, found, err := c.slotFor(graph, capsule, c.nextSlots.allPointers)
	if err != nil {
		return 0, false, err
	}
	if !found {
		return 0, false, ErrNotCapsule
	}

	return c.nextSlots.Target(graph, slot)
}

// SetNext sets capsule's next-capsule link.
func (c *CapsuleRegistry) SetNext(graph Transactor, capsule, next NodeID) error {
	return c.setSlotTarget(graph, capsule, c.nextSlots, next)
}

// RemoveNext clears capsule's next-capsule link, if any.
func (c *CapsuleRegistry) RemoveNext(graph Transactor, capsule NodeID) (removed bool, err error) {
	return c.removeSlotTarget(graph, capsule, c.nextSlots)
}

// DeleteCapsule deletes capsule and all three of its role-slot nodes
// (prev, value, next) from the underlying graph, entirely inside one
// Graph.Transact call, but only if doing so cannot leave anything
// orphaned or partially torn down.
//
// A capsule minted via NewCapsule/newCapsuleTx has a fixed, known shape:
// its own AllElementCapsules tag, exactly three outgoing edges to its
// role slots, each slot's own role tag, and (usually) a target already
// set on the value slot. DeleteCapsule removes every relationship
// CapsuleRegistry itself is aware of through tx first (capsule's own
// three slot-edges and its AllElementCapsules tag, the value slot's
// target edge if set, and each slot's own role-tag edge), then deletes
// each of the four nodes via tx.DeleteNode. If capsule or any of its
// three slots still carries something beyond that fixed shape --
// ListRegistry still linking capsule into a list, a caller having
// called SetPrev/SetNext without a matching removal, or some future
// structure referencing a slot node for its own reasons -- the
// corresponding tx.DeleteNode call fails with the underlying Graph's own
// ErrNodeNotEmpty, which this method maps to the more specific
// ErrCapsuleNotEmpty.
//
// This is deliberately all-or-nothing, and getting that right requires
// no special care: tx.DeleteNode is itself fully undoable (see the Txn
// doc comment), so if a later delete in the sequence below fails,
// Graph.Transact's ordinary LIFO rollback automatically undoes every
// earlier step in this same call -- including any DeleteNode calls that
// had already succeeded earlier in the sequence -- exactly like it
// already does for every other multi-step operation in this file. No
// separate pre-verification pass is needed before deleting anything;
// see theorystate.md section 78 for why an earlier version of this
// method needed one; and why it no longer does.
//
// Every relationship this removes is one CapsuleRegistry itself is
// certain it created (via buildCapsuleTx). value itself is never
// deleted -- only the valueSlot -> value edge is removed -- since value
// is caller-owned data that may still be referenced elsewhere (e.g. by
// another capsule's own value slot). prevSlot and nextSlot's own target
// edges, if set, are deliberately never force-cleared here: doing so
// would risk leaving a *neighboring* capsule (whatever prevSlot/nextSlot
// currently points at) with a dangling reference into a node this call
// is about to delete. Refusing to delete when a prev/next target is
// still set (surfaced as ErrCapsuleNotEmpty via the corresponding slot's
// failed tx.DeleteNode) is the correct behavior, not a missing feature;
// see TestCapsuleRegistryDeleteCapsuleFailsIfPrevOrNextSet.
//
// capsule must currently be tagged (AllElementCapsules, capsule); a
// capsule somehow missing one of its three role slots -- only reachable
// through an out-of-band Graph mutation -- is treated the same as
// ErrCapsuleNotEmpty rather than guessed about.
//
// Every existence, tag and slot check runs against tx. If graph is a Tx
// the call is a nested transaction: if it fails, only what it did is
// undone (including any DeleteNode calls that had already succeeded),
// and the enclosing closure may carry on. That is what lets
// ListRegistry.Remove treat this as a best-effort step inside its own
// single transaction, and lets CompositeSetLogRegistry.RemoveOperation
// treat it as one all-or-nothing step of a larger one.
func (c *CapsuleRegistry) DeleteCapsule(graph Transactor, capsule NodeID) error {
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		if requireErr := c.requireCapsule(tx, capsule); requireErr != nil {
			return requireErr
		}

		prevSlot, hasPrevSlot, err := c.slotFor(tx, capsule, c.prevSlots.allPointers)
		if err != nil {
			return err
		}

		valueSlot, hasValueSlot, err := c.slotFor(tx, capsule, c.valueSlots.allPointers)
		if err != nil {
			return err
		}

		nextSlot, hasNextSlot, err := c.slotFor(tx, capsule, c.nextSlots.allPointers)
		if err != nil {
			return err
		}

		if !hasPrevSlot || !hasValueSlot || !hasNextSlot {
			return ErrCapsuleNotEmpty
		}

		value, hasValue, err := c.valueSlots.Target(tx, valueSlot)
		if err != nil {
			return err
		}

		// Every relationship here is one buildCapsuleTx itself created.
		// prevSlot/nextSlot's own targets are deliberately absent: see
		// the DeleteCapsule doc comment.
		edges := []Relationship{
			{From: capsule, To: prevSlot},
			{From: c.prevSlots.allPointers, To: prevSlot},
			{From: capsule, To: valueSlot},
			{From: c.valueSlots.allPointers, To: valueSlot},
			{From: capsule, To: nextSlot},
			{From: c.nextSlots.allPointers, To: nextSlot},
			{From: c.allElementCapsules, To: capsule},
		}
		if hasValue {
			edges = append(edges, Relationship{From: valueSlot, To: value})
		}

		for _, edge := range edges {
			if removeErr := removeRelationshipTx(tx, edge.From, edge.To); removeErr != nil {
				return removeErr
			}
		}

		for _, node := range []NodeID{prevSlot, valueSlot, nextSlot, capsule} {
			if deleteErr := deleteNodeTx(tx, node); deleteErr != nil {
				if errors.Is(deleteErr, ErrNodeNotEmpty) {
					return ErrCapsuleNotEmpty
				}
				return deleteErr
			}
		}

		return nil
	}))
}

// ListRegistry implements Ordered Lists (theorystate.md section 11
// / 11a) on top of CapsuleRegistry.
//
// A list is an ordinary node tagged (AllLists, list). List membership --
// which of a list's direct children are actual ElementCapsules, as
// opposed to unrelated metadata or comments a caller might attach later
// -- is identified through the ordinary (list, capsule) containment edge
// combined with the capsule's own (AllElementCapsules, capsule) tag,
// exactly per the discipline already established for CapsuleRegistry: a
// list's direct children are not assumed to all be capsules.
//
// Head and tail are each a plain tag on a capsule -- (AllHeads, capsule)
// and (AllTails, capsule) -- discovered as the single child of list
// tagged accordingly, via findUniqueTaggedChild. This deliberately does
// NOT use a further Pointer-style indirection (a dedicated head/tail
// slot node, the way Representation C/D uses slot nodes for subject/
// target): that indirection exists specifically to prevent two roles
// sharing the same *source* node from colliding into a single primitive
// relationship when their targets happen to be equal (section 1: (A,B)
// is a unique pair). Here the two roles have different sources --
// (AllHeads, X) and (AllTails, X) -- so they can never collide even when
// the same capsule X is simultaneously both head and tail, which is
// exactly the normal, expected state for a single-element list. Adding
// slot indirection here would be pure unneeded overhead.
//
// ListRegistry's mutating operations (NewList, Append, Prepend,
// InsertAfter, Remove, RemoveWithoutDeletingCapsule, DeleteList) each run
// as one transaction on the Transactor they are given (nested if it is a
// Tx), composing CapsuleRegistry's exported NewCapsule/SetPrev/SetNext/
// RemovePrev/RemoveNext/DeleteCapsule alongside direct tag
// (AddRelationship/RemoveRelationship) calls against the same tx. Any of
// them can therefore be called standalone or inside a larger
// transaction, where it shares that transaction's fate.
//
// As with every other registry in this file, list structure is
// re-derived fresh from the Graph on every call rather than cached.
//
// Two removal paths are available: RemoveWithoutDeletingCapsule unlinks
// capsule from list only, leaving it standalone and intact (no cascading
// node deletion, consistent with theorystate.md section 18's
// rejection of automatic cascade delete); Remove does the same unlinking
// and then additionally reclaims capsule via CapsuleRegistry.DeleteCapsule
// whenever nothing else still references it. DeleteList removes a list
// itself once empty.
type ListRegistry struct {
	capsules *CapsuleRegistry
	allLists NodeID
	allHeads NodeID
	allTails NodeID
}

// NewListRegistry creates a ListRegistry over graph, using capsules for
// all per-capsule slot operations, allLists to tag list-kind nodes, and
// allHeads/allTails to tag a list's current head/tail capsule. All three
// tag NodeIDs must already exist -- typically via
// NameRegistry.BootstrapNames(FoundationalNames). capsules must already
// be constructed over the same graph.
func NewListRegistry(graph GraphAPI, capsules *CapsuleRegistry, allLists, allHeads, allTails NodeID) (*ListRegistry, error) {
	existsLists, err := graph.NodeExists(allLists)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}
	if !existsLists {
		return nil, ErrNodeNotFound
	}

	existsHeads, err := graph.NodeExists(allHeads)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}
	if !existsHeads {
		return nil, ErrNodeNotFound
	}

	existsTails, err := graph.NodeExists(allTails)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}
	if !existsTails {
		return nil, ErrNodeNotFound
	}

	l := &ListRegistry{
		capsules: capsules,
		allLists: allLists,
		allHeads: allHeads,
		allTails: allTails,
	}

	// Registered against l itself so this Checker's Check closure can
	// call l.validateStructure -- the exact same structural check
	// Elements() already runs lazily on read (see that method and
	// validateStructure's own doc comment), now additionally run eagerly
	// at commit time for any touched node currently tagged (allLists,
	// node).
	//
	// This Checker only ever runs for mutations made through
	// Graph.Transact (see the Checker doc comment). Every legitimate
	// ListRegistry mutation already touches the list node itself within
	// that same transaction (Append/Prepend/InsertAfter/
	// RemoveWithoutDeletingCapsule each add or remove a (list,capsule)
	// relationship), so Tags: []NodeID{allLists} is sufficient for every
	// call path this registry itself exposes -- it is not a narrower
	// version of some broader relevance rule this Checker is missing.
	// A raw, direct Graph.AddRelationship/RemoveRelationship call
	// bypassing Transact entirely -- exactly what every existing
	// out-of-band adversarial test in this file already does -- still
	// bypasses this Checker the same way it already bypasses every other
	// one; validateStructure's existing lazy, on-read enforcement (via
	// Elements) remains the backstop for that case, unaffected by this
	// addition.
	graph.RegisterChecker(Checker{
		Name: fmt.Sprintf("ListRegistry(tag=%d)", allLists),
		Tags: []NodeID{allLists},
		Check: func(g GraphReader, touched map[NodeID]struct{}) error {
			for node := range touched {
				tagged, taggedErr := g.HasRelationship(allLists, node)
				if taggedErr != nil {
					return wrapInterfaceErr(taggedErr)
				}
				if !tagged {
					continue
				}

				if err := l.validateStructure(g, node); err != nil {
					return err
				}
			}

			return nil
		},
	})

	return l, nil
}

// IsList reports whether id is currently tagged (AllLists, id).
func (l *ListRegistry) IsList(graph GraphReader, id NodeID) (bool, error) {
	has, err := graph.HasRelationship(l.allLists, id)
	return has, wrapInterfaceErr(err)
}

// requireList checks that list exists and is tagged (AllLists, list),
// returning ErrNodeNotFound or ErrNotList otherwise. The mutating
// methods call it with their tx, so the check and the write see the same
// state. Delegates to the shared requireTagged helper (see its doc
// comment on PointerRegistry.requirePointer).
func (l *ListRegistry) requireList(graph GraphReader, list NodeID) error {
	return requireTagged(graph, list, l.allLists, ErrNotList)
}

// requireListValue is requireList plus a check that value exists.
func (l *ListRegistry) requireListValue(graph GraphReader, list, value NodeID) error {
	if requireErr := l.requireList(graph, list); requireErr != nil {
		return requireErr
	}

	exists, err := graph.NodeExists(value)
	if err != nil {
		return wrapInterfaceErr(err)
	}
	if !exists {
		return ErrNodeNotFound
	}

	return nil
}

// NewList creates a fresh NodeID and tags it (AllLists, id). The new list
// starts empty: no head, no tail, no element capsules.
func (l *ListRegistry) NewList(graph Transactor) (NodeID, error) {
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		return createTaggedNodeTx(tx, l.allLists)
	})
}

// Head returns list's current head capsule, if any. hasHead is false for
// an empty list.
func (l *ListRegistry) Head(graph GraphReader, list NodeID) (head NodeID, hasHead bool, err error) {
	return l.boundary(graph, list, l.allHeads)
}

// boundary returns the capsule of list tagged via (tag, capsule), if any:
// the shared body of Head (tag AllHeads) and Tail (tag AllTails). A tagged
// node that is not a capsule, or not linked into list, is
// ErrInvalidListStructure.
func (l *ListRegistry) boundary(graph GraphReader, list, tag NodeID) (capsule NodeID, found bool, err error) {
	if requireErr := l.requireList(graph, list); requireErr != nil {
		return 0, false, requireErr
	}

	capsule, found, err = findUniqueTaggedChild(graph, list, tag)
	if err != nil || !found {
		return capsule, found, err
	}

	isCapsule, capsuleErr := l.capsules.IsCapsule(graph, capsule)
	if capsuleErr != nil {
		return 0, false, capsuleErr
	}

	hasEdge, edgeErr := graph.HasRelationship(list, capsule)
	if edgeErr != nil {
		return 0, false, wrapInterfaceErr(edgeErr)
	}

	if !isCapsule || !hasEdge {
		return 0, false, ErrInvalidListStructure
	}

	return capsule, true, nil
}

// Tail returns list's current tail capsule, if any. hasTail is false for
// an empty list.
func (l *ListRegistry) Tail(graph GraphReader, list NodeID) (tail NodeID, hasTail bool, err error) {
	return l.boundary(graph, list, l.allTails)
}

// Append creates a fresh capsule holding value and links it as the new
// tail of list, entirely inside one transaction (nested if graph is a
// Tx, so a larger operation such as CompositeSetLogRegistry.
// AppendOperation can append as one step of its own transaction).
//
// If list is currently empty, the new capsule becomes both head and
// tail. Otherwise the new capsule is wired in after the current tail
// (new capsule's prev -> old tail, old tail's next -> new capsule), the
// old tail loses its AllTails tag, and the new capsule gains it.
//
// list must already be tagged (AllLists, list); value must already
// exist. Both are checked against tx itself, so a caller composing this
// into a larger transaction cannot act on a stale pre-check.
func (l *ListRegistry) Append(graph Transactor, list, value NodeID) (NodeID, error) {
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		if requireErr := l.requireListValue(tx, list, value); requireErr != nil {
			return 0, requireErr
		}

		oldTail, hasTail, err := findUniqueTaggedChild(tx, list, l.allTails)
		if err != nil {
			return 0, err
		}

		capsule, err := l.capsules.NewCapsule(tx, value)
		if err != nil {
			return 0, err
		}

		if linkErr := addRelationshipTx(tx, list, capsule); linkErr != nil {
			return 0, linkErr
		}

		if hasTail {
			if prevErr := l.capsules.SetPrev(tx, capsule, oldTail); prevErr != nil {
				return 0, prevErr
			}
			if nextErr := l.capsules.SetNext(tx, oldTail, capsule); nextErr != nil {
				return 0, nextErr
			}
			if untagErr := removeRelationshipTx(tx, l.allTails, oldTail); untagErr != nil {
				return 0, untagErr
			}
		} else {
			if headErr := addRelationshipTx(tx, l.allHeads, capsule); headErr != nil {
				return 0, headErr
			}
		}

		if tailErr := addRelationshipTx(tx, l.allTails, capsule); tailErr != nil {
			return 0, tailErr
		}

		return capsule, nil
	})
}

// Prepend creates a fresh capsule holding value and links it as the new
// head of list, entirely inside one transaction (nested if graph is a
// Tx). Exact mirror of Append, swapping head/tail and prev/next roles.
//
// list must already be tagged (AllLists, list); value must already
// exist.
func (l *ListRegistry) Prepend(graph Transactor, list, value NodeID) (NodeID, error) {
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		if requireErr := l.requireListValue(tx, list, value); requireErr != nil {
			return 0, requireErr
		}

		oldHead, hasHead, err := findUniqueTaggedChild(tx, list, l.allHeads)
		if err != nil {
			return 0, err
		}

		capsule, err := l.capsules.NewCapsule(tx, value)
		if err != nil {
			return 0, err
		}

		if linkErr := addRelationshipTx(tx, list, capsule); linkErr != nil {
			return 0, linkErr
		}

		if hasHead {
			if nextErr := l.capsules.SetNext(tx, capsule, oldHead); nextErr != nil {
				return 0, nextErr
			}
			if prevErr := l.capsules.SetPrev(tx, oldHead, capsule); prevErr != nil {
				return 0, prevErr
			}
			if untagErr := removeRelationshipTx(tx, l.allHeads, oldHead); untagErr != nil {
				return 0, untagErr
			}
		} else {
			if tailErr := addRelationshipTx(tx, l.allTails, capsule); tailErr != nil {
				return 0, tailErr
			}
		}

		if headErr := addRelationshipTx(tx, l.allHeads, capsule); headErr != nil {
			return 0, headErr
		}

		return capsule, nil
	})
}

// InsertAfter creates a fresh capsule holding value and links it into
// list immediately after afterCapsule, entirely inside one transaction
// (nested if graph is a Tx).
//
// If afterCapsule was the tail, the new capsule becomes the new tail.
// Otherwise the new capsule is spliced in between afterCapsule and
// afterCapsule's old next capsule.
//
// list must already be tagged (AllLists, list); afterCapsule must
// already be an element of list (checked via the (list, afterCapsule)
// containment edge, returning ErrCapsuleNotInList otherwise); value must
// already exist.
func (l *ListRegistry) InsertAfter(graph Transactor, list, afterCapsule, value NodeID) (NodeID, error) {
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		if requireErr := l.requireListValue(tx, list, value); requireErr != nil {
			return 0, requireErr
		}

		inList, err := tx.HasRelationship(list, afterCapsule)
		if err != nil {
			return 0, wrapInterfaceErr(err)
		}
		if !inList {
			return 0, ErrCapsuleNotInList
		}

		oldNext, hasNext, err := l.capsules.Next(tx, afterCapsule)
		if err != nil {
			return 0, err
		}

		capsule, err := l.capsules.NewCapsule(tx, value)
		if err != nil {
			return 0, err
		}

		if linkErr := addRelationshipTx(tx, list, capsule); linkErr != nil {
			return 0, linkErr
		}

		if prevErr := l.capsules.SetPrev(tx, capsule, afterCapsule); prevErr != nil {
			return 0, prevErr
		}
		if nextErr := l.capsules.SetNext(tx, afterCapsule, capsule); nextErr != nil {
			return 0, nextErr
		}

		if hasNext {
			if oldNextErr := l.capsules.SetNext(tx, capsule, oldNext); oldNextErr != nil {
				return 0, oldNextErr
			}
			if oldPrevErr := l.capsules.SetPrev(tx, oldNext, capsule); oldPrevErr != nil {
				return 0, oldPrevErr
			}

			return capsule, nil
		}

		if untagErr := removeRelationshipTx(tx, l.allTails, afterCapsule); untagErr != nil {
			return 0, untagErr
		}
		if tailErr := addRelationshipTx(tx, l.allTails, capsule); tailErr != nil {
			return 0, tailErr
		}

		return capsule, nil
	})
}

// validateStructure checks the ordered-list invariants that are meaningful
// at this layer without imposing any restriction on unrelated primitive
// graph relationships. In particular, direct list children only count as
// elements when they are tagged AllElementCapsules; arbitrary non-capsule
// children remain permitted.
//
// The check deliberately walks the Next chain with a visited set. The
// primitive Graph permits cycles, but an ordered list is interpreted as a
// finite head-to-tail sequence. The same pass also checks list membership,
// reciprocal Prev/Next links, and that every capsule-tagged list member is
// actually reachable from the head. Thus a corrupted graph is rejected
// rather than silently producing a plausible partial sequence.
func (l *ListRegistry) validateStructure(graph GraphReader, list NodeID) error {
	head, hasHead, err := findUniqueTaggedChild(graph, list, l.allHeads)
	if err != nil {
		return err
	}
	tail, hasTail, err := findUniqueTaggedChild(graph, list, l.allTails)
	if err != nil {
		return err
	}

	outgoing, err := graph.FindOutgoing(list)
	if err != nil {
		return wrapInterfaceErr(err)
	}

	members := make(map[NodeID]struct{})
	for _, rel := range outgoing {
		isMember, memberErr := l.capsules.IsCapsule(graph, rel.To)
		if memberErr != nil {
			return memberErr
		}
		if isMember {
			members[rel.To] = struct{}{}
		}
	}

	if !hasHead || !hasTail {
		if hasHead || hasTail || len(members) != 0 {
			return ErrInvalidListStructure
		}
		return nil
	}

	headIsCapsule, err := l.capsules.IsCapsule(graph, head)
	if err != nil {
		return err
	}
	tailIsCapsule, err := l.capsules.IsCapsule(graph, tail)
	if err != nil {
		return err
	}
	if !headIsCapsule || !tailIsCapsule {
		return ErrInvalidListStructure
	}
	if _, ok := members[head]; !ok {
		return ErrInvalidListStructure
	}
	if _, ok := members[tail]; !ok {
		return ErrInvalidListStructure
	}

	visited := make(map[NodeID]struct{}, len(members))
	current := head
	for {
		if _, seen := visited[current]; seen {
			return ErrListCycle
		}
		visited[current] = struct{}{}

		currentIsCapsule, currentIsCapsuleErr := l.capsules.IsCapsule(graph, current)
		if currentIsCapsuleErr != nil {
			return currentIsCapsuleErr
		}
		if !currentIsCapsule {
			return ErrInvalidListStructure
		}

		currentInList, currentInListErr := graph.HasRelationship(list, current)
		if currentInListErr != nil {
			return wrapInterfaceErr(currentInListErr)
		}
		if !currentInList {
			return ErrInvalidListStructure
		}

		_, hasValue, valueErr := l.capsules.Value(graph, current)
		if valueErr != nil {
			return valueErr
		}
		if !hasValue {
			return ErrInvalidListStructure
		}

		next, hasNext, nextErr := l.capsules.Next(graph, current)
		if nextErr != nil {
			return nextErr
		}
		if !hasNext {
			if current != tail {
				return ErrInvalidListStructure
			}
			break
		}

		nextIsCapsule, nextIsCapsuleErr := l.capsules.IsCapsule(graph, next)
		if nextIsCapsuleErr != nil {
			return nextIsCapsuleErr
		}
		nextInList, nextInListErr := graph.HasRelationship(list, next)
		if nextInListErr != nil {
			return wrapInterfaceErr(nextInListErr)
		}
		if !nextIsCapsule || !nextInList {
			return ErrInvalidListStructure
		}

		prev, hasPrev, prevErr := l.capsules.Prev(graph, next)
		if prevErr != nil {
			return prevErr
		}
		if !hasPrev || prev != current {
			return ErrInvalidListStructure
		}

		current = next
	}

	_, headHasPrev, err := l.capsules.Prev(graph, head)
	if err != nil {
		return err
	}
	if headHasPrev {
		return ErrInvalidListStructure
	}

	if len(visited) != len(members) {
		return ErrInvalidListStructure
	}
	return nil
}

// Elements returns list's current values, in head-to-tail order, by
// traversing the capsule chain via CapsuleRegistry.Next. It first validates
// the list structure so out-of-band mutations cannot turn a corrupted
// chain into a silently accepted partial or cross-list traversal.
func (l *ListRegistry) Elements(graph GraphReader, list NodeID) ([]NodeID, error) {
	if requireErr := l.requireList(graph, list); requireErr != nil {
		return nil, requireErr
	}

	err := l.validateStructure(graph, list)
	if err != nil {
		return nil, err
	}

	var values []NodeID

	// The primitive Graph intentionally permits arbitrary cycles. The list
	// layer, however, interprets Next as a finite ordered chain, so traversal
	// must detect a repeated capsule rather than relying on a timeout or
	// assuming that well-formed construction is the only possible state.
	visited := make(map[NodeID]struct{})

	current, hasCurrent, err := findUniqueTaggedChild(graph, list, l.allHeads)
	if err != nil {
		return nil, err
	}

	for hasCurrent {
		if _, seen := visited[current]; seen {
			return nil, ErrListCycle
		}
		visited[current] = struct{}{}

		value, hasValue, err := l.capsules.Value(graph, current)
		if err != nil {
			return nil, err
		}
		if hasValue {
			values = append(values, value)
		}

		current, hasCurrent, err = l.capsules.Next(graph, current)
		if err != nil {
			return nil, err
		}
	}

	return values, nil
}

// OccurrencesOf returns every capsule within list whose value equals
// value, in no particular semantic order (see the ordering note on
// CapsuleRegistry.CapsulesWithValue, which this is built directly on
// top of).
//
// This exists because a value may legitimately occur more than once in
// the same list, each occurrence via its own capsule
// (theorystate.md section 75's occurrence-identity distinction) --
// Contains below only
// needs to know whether at least one occurrence exists, but some callers
// legitimately need all of them (e.g. removing every occurrence of a
// value, or counting duplicates).
//
// list must already be tagged (AllLists, list). If value does not
// exist, this fails with ErrNodeNotFound, via
// CapsuleRegistry.CapsulesWithValue.
func (l *ListRegistry) OccurrencesOf(graph GraphReader, list, value NodeID) ([]NodeID, error) {
	if requireErr := l.requireList(graph, list); requireErr != nil {
		return nil, requireErr
	}

	candidates, err := l.capsules.CapsulesWithValue(graph, value)
	if err != nil {
		return nil, err
	}

	var occurrences []NodeID

	for _, capsule := range candidates {
		inList, inListErr := graph.HasRelationship(list, capsule)
		if inListErr != nil {
			return nil, wrapInterfaceErr(inListErr)
		}
		if inList {
			occurrences = append(occurrences, capsule)
		}
	}

	return occurrences, nil
}

// Contains reports whether value currently occurs at least once in
// list, returning one such capsule if so. If value occurs more than
// once, which capsule is returned is unspecified -- see OccurrencesOf
// to find every occurrence.
//
// Built directly on OccurrencesOf: per theorystate.md section 11's
// note that a Set-like membership index "may be added later" for this
// exact query, no new node, tag, or index structure turned out to be
// necessary -- see the CapsuleRegistry.CapsulesWithValue doc comment for
// why the existing list/capsule/value-slot structure already supports
// this query in time proportional to how many places value is
// referenced, not to list's length.
//
// list must already be tagged (AllLists, list). If value does not
// exist, this fails with ErrNodeNotFound.
func (l *ListRegistry) Contains(graph GraphReader, list, value NodeID) (capsule NodeID, found bool, err error) {
	occurrences, err := l.OccurrencesOf(graph, list, value)
	if err != nil {
		return 0, false, err
	}

	if len(occurrences) == 0 {
		return 0, false, nil
	}

	return occurrences[0], true, nil
}

// RemoveWithoutDeletingCapsule unlinks capsule from list, relinking
// capsule's neighbors (if any) around the gap and updating head/tail
// tagging as needed, entirely inside one Graph.Transact call. capsule's
// own prev/next slots are cleared as part of the same transaction, since
// they described its position within the list it is now leaving -- this
// leaves capsule as a standalone, valid, empty-linked capsule rather
// than one carrying stale links into a list it is no longer part of.
//
// This does not delete capsule itself, or its role-slot nodes -- no
// cascade deletion, consistent with theorystate.md section 18's
// rejection of deleteNodeAndRelationships. capsule keeps its
// AllElementCapsules tag and its value: list membership is a separate
// concern from capsule-kind or value identity (theorystate.md
// section 10c -- the same node identity can participate in multiple
// interpretations without changing its primitive facts).
//
// This is the lower-level primitive Remove (below) builds on: Remove
// calls this method first and then attempts CapsuleRegistry.DeleteCapsule
// as a second, separate step. Call this method directly instead of
// Remove when capsule must unconditionally survive removal regardless of
// whether it happens to be otherwise unreferenced -- e.g. a caller
// planning to immediately re-link capsule into a different list or
// position.
//
// list must already be tagged (AllLists, list); capsule must currently
// be an element of list (checked via the (list, capsule) containment
// edge, returning ErrCapsuleNotInList otherwise).
func (l *ListRegistry) RemoveWithoutDeletingCapsule(graph Transactor, list, capsule NodeID) error {
	// Membership is checked against tx, so a caller composing this into
	// a larger transaction (CompositeSetLogRegistry.RemoveOperation)
	// cannot act on a stale check.
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		if requireErr := l.requireList(tx, list); requireErr != nil {
			return requireErr
		}

		inList, err := tx.HasRelationship(list, capsule)
		if err != nil {
			return wrapInterfaceErr(err)
		}
		if !inList {
			return ErrCapsuleNotInList
		}

		prev, hasPrev, err := l.capsules.Prev(tx, capsule)
		if err != nil {
			return err
		}

		next, hasNext, err := l.capsules.Next(tx, capsule)
		if err != nil {
			return err
		}

		switch {
		case hasPrev && hasNext:
			// Removing a middle element: splice prev and next together
			// directly. Head/tail are unaffected.
			if err2 := l.capsules.SetNext(tx, prev, next); err2 != nil {
				return err2
			}
			if err3 := l.capsules.SetPrev(tx, next, prev); err3 != nil {
				return err3
			}

		case hasPrev:
			// capsule was the tail: prev becomes the new tail.
			if _, err4 := l.capsules.RemoveNext(tx, prev); err4 != nil {
				return err4
			}
			if err5 := removeRelationshipTx(tx, l.allTails, capsule); err5 != nil {
				return err5
			}
			if err6 := addRelationshipTx(tx, l.allTails, prev); err6 != nil {
				return err6
			}

		case hasNext:
			// capsule was the head: next becomes the new head.
			if _, err7 := l.capsules.RemovePrev(tx, next); err7 != nil {
				return err7
			}
			if err8 := removeRelationshipTx(tx, l.allHeads, capsule); err8 != nil {
				return err8
			}
			if err9 := addRelationshipTx(tx, l.allHeads, next); err9 != nil {
				return err9
			}

		default:
			// capsule was the sole element: the list becomes empty.
			if err10 := removeRelationshipTx(tx, l.allHeads, capsule); err10 != nil {
				return err10
			}
			if err11 := removeRelationshipTx(tx, l.allTails, capsule); err11 != nil {
				return err11
			}
		}

		if _, err12 := l.capsules.RemovePrev(tx, capsule); err12 != nil {
			return err12
		}
		if _, err13 := l.capsules.RemoveNext(tx, capsule); err13 != nil {
			return err13
		}

		return removeRelationshipTx(tx, list, capsule)
	}))
}

// Remove unlinks capsule from list via RemoveWithoutDeletingCapsule, and
// then additionally attempts to delete capsule and its three role-slot
// nodes via CapsuleRegistry.DeleteCapsule -- this is the list structure
// cleaning up after itself: a capsule exists only to represent one
// occurrence of a value within a list (theorystate.md section 75),
// so once it is removed from its (only) list and nothing else has taken
// an interest in it, there is no reason to leave it behind as an orphan.
//
// deleted reports whether the capsule was actually deleted. Deletion is
// best-effort: the removal is this call's own work, and DeleteCapsule
// runs as a nested transaction (a savepoint). If capsule turns out not
// to be safely deletable -- some further reference to it or one of its
// role slots exists beyond what removal itself cleared, e.g. it was also
// (unusually) referenced by something outside this list -- the nested
// transaction undoes only itself, the removal still commits, and deleted
// is false with err nil: this is not a failure of Remove, it simply
// means capsule was left in place, standalone and still valid, exactly
// as RemoveWithoutDeletingCapsule already leaves it (see
// TestListRemoveWithoutDeletingCapsuleClearsCapsuleOwnLinks). err is
// reserved for genuine failures: list not tagged, capsule not currently
// an element of list, or an unexpected error from either underlying
// call.
//
// The whole call is one transaction, so no other goroutine can observe
// the intermediate "unlinked but not yet deleted" state, and if graph is
// a Tx the removal (and the deletion, if it happens) commit or roll back
// with the enclosing transaction.
//
// list must already be tagged (AllLists, list); capsule must currently
// be an element of list, exactly like RemoveWithoutDeletingCapsule.
func (l *ListRegistry) Remove(graph Transactor, list, capsule NodeID) (deleted bool, err error) {
	return transactBool(graph, func(tx Tx) (bool, error) {
		if removeErr := l.RemoveWithoutDeletingCapsule(tx, list, capsule); removeErr != nil {
			return false, removeErr
		}

		deleteErr := l.capsules.DeleteCapsule(tx, capsule)
		if deleteErr != nil {
			if errors.Is(deleteErr, ErrCapsuleNotEmpty) {
				return false, nil
			}

			return false, deleteErr
		}

		return true, nil
	})
}

// DeleteList deletes list from the underlying graph, additionally
// removing its (AllLists, list) tag as part of the same transaction.
//
// Unlike NameRegistry.DeleteNode's coordinated delete -- which deletes
// the primitive node first and only then cleans up name bookkeeping that
// lives entirely outside the graph -- the (AllLists, list) tag is itself
// an ordinary primitive relationship *into* list, and therefore itself
// counts toward list's relationship count. It must be removed *before*
// Graph.DeleteNode can succeed, not after. This method's own
// Graph.Transact call is what makes that safe: if list turns out not to
// be empty and the underlying DeleteNode call fails with
// ErrNodeNotEmpty, the tag removal that already happened is rolled back
// automatically, leaving list exactly as tagged and populated as before
// this call -- rather than leaving a list untagged but still present
// with orphaned element capsules.
//
// Per theorystate.md section 18, deletion is deliberately "delete
// only if empty," not cascade: DeleteList refuses (ErrNodeNotEmpty,
// resolvable by clearing and retrying -- unlike RootGraph's
// ErrCannotDeleteRoot, this is not structurally permanent) if list
// currently has any element capsules or any other relationship at all.
// Callers wanting to delete a non-empty list must first Remove every
// element capsule; DeleteList does not touch those now-detached capsule
// nodes at all.
//
// list must currently be tagged (AllLists, list).
func (l *ListRegistry) DeleteList(graph Transactor, list NodeID) error {
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		if requireErr := l.requireList(tx, list); requireErr != nil {
			return requireErr
		}
		// Tag removal and delete are steps of this one transaction: if
		// the delete fails (e.g. ErrNodeNotEmpty), Transact's rollback
		// restores the (AllLists, list) tag. See untagAndDeleteNodeTx and
		// theorystate.md section 78.
		return untagAndDeleteNodeTx(tx, list, l.allLists)
	}))
}

// SetRegistry implements the minimal Set interpretation of
// theorystate.md section 9 / 9a (formalized further in section 79):
// (AllSets, S) tags S as Set-kind, and S's direct children in the
// underlying Graph are exactly its members.
//
// Unlike every intermediary-node-based structure elsewhere in this file
// (PointerRegistry's Representation B, CapsuleRegistry's role slots,
// PointerMetadataRegistry(D)'s subject/target slots), a Set needs no
// intermediary node at all. Two properties specific to Sets, neither of
// which holds for those other structures, make this safe:
//   - Sets carry no order (theorystate.md section 5), so there is no
//     positional/sequencing information any intermediary node would need
//     to carry.
//   - Primitive relationships are already unique pairs
//     (theorystate.md section 2.6): (S, X) cannot exist more than
//     once, so duplicate membership is structurally impossible without any
//     registry-level enforcement at all.
//
// A member is therefore simply a direct child of S; adding/removing a
// member is simply one (S, X) relationship add/remove, tag-gated by
// requiring S to already be tagged (AllSets, S). Each is performed as a
// one-step Graph.Transact call rather than a raw Graph call, so that
// commit-time Checkers observe every membership change: a domain
// pointer's validity depends on its domain's membership
// (theorystate.md section 86), and a raw call would bypass every Checker.
//
// Because a Set imposes no cardinality or structural invariant on its
// children beyond the tag itself, there is no analogue here of the
// adversarial out-of-band-mutation test suites written for
// CapsuleRegistry/ListRegistry: any child of a tagged node is, by
// definition, a valid member. There is nothing an out-of-band mutation
// could do to a tagged Set's children that this registry would need to
// detect or reject.
//
// Self-membership (Add(S, S)) is permitted, matching
// theorystate.md sections 2.8 and 9a.
//
// A Set containing another Set as a member does NOT, by itself, imply
// recursive membership expansion (theorystate.md section 9a):
// Members(S) returns S's own direct children only, and never expands into
// a member that happens to itself be tagged Set-kind. Recursive,
// operand-based expansion is a separate, higher-level structure -- see
// theorystate.md sections 80-83 for the deferred (not yet
// implemented) CompositeSetRegistry / CompositeSetLogRegistry designs that
// provide it, and for why expansion intent must be recorded explicitly per
// operand rather than inferred from an operand's own tags.
//
// theorystate.md section 79 additionally decides that a node may
// carry at most one of the three Set-representation tags (AllSets,
// AllCompositeSets, AllCompositeSetLogs) -- never more than one at a
// time. This is now enforced for all three: NewSetRegistry accepts the
// tag NodeIDs of every other currently-implemented Set representation
// (AllCompositeSets, as of CompositeSetRegistry's addition, and
// AllCompositeSetLogs, as of CompositeSetLogRegistry's), and TagAsSet
// refuses (ErrSetRepresentationConflict) to tag a node already carrying
// any of them.
type SetRegistry struct {
	allSets NodeID

	// otherSetTags holds the tag NodeIDs of every other
	// currently-implemented Set representation, checked by TagAsSet to
	// enforce theorystate.md section 79's mutual exclusivity. NewSet does
	// not need this check: it always tags a freshly created node, which
	// cannot already carry any other representation's tag.
	otherSetTags []NodeID
}

// NewSetRegistry creates a SetRegistry over graph, using allSets as the
// tagging node for the (AllSets, S) relationship. allSets must already
// exist -- typically via NameRegistry.EnsureNamedNode(NameAllSets) or
// NameRegistry.BootstrapNames(FoundationalNames).
//
// otherSetTags should list the tag NodeID of every other
// currently-implemented Set representation (e.g. AllCompositeSets,
// AllCompositeSetLogs), so that TagAsSet can enforce theorystate.md
// section 79's mutual exclusivity. Each, if given, must already exist.
// Passing none is valid (no cross-representation check is performed),
// which is only appropriate if no other Set representation exists in the
// calling program yet.
func NewSetRegistry(graph GraphAPI, allSets NodeID, otherSetTags ...NodeID) (*SetRegistry, error) {
	existsSets, err := graph.NodeExists(allSets)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}
	if !existsSets {
		return nil, ErrNodeNotFound
	}

	for _, tag := range otherSetTags {
		exists, tagErr := graph.NodeExists(tag)
		if tagErr != nil {
			return nil, wrapInterfaceErr(tagErr)
		}
		if !exists {
			return nil, ErrNodeNotFound
		}
	}

	return &SetRegistry{
		allSets:      allSets,
		otherSetTags: otherSetTags,
	}, nil
}

// IsSet reports whether id is currently tagged (AllSets, id).
func (s *SetRegistry) IsSet(graph GraphReader, id NodeID) (bool, error) {
	has, err := graph.HasRelationship(s.allSets, id)
	return has, wrapInterfaceErr(err)
}

// requireSet checks that set exists and is tagged (AllSets, set),
// returning ErrNodeNotFound or ErrNotSet otherwise. Delegates to the
// shared requireTagged helper (see its doc comment on
// PointerRegistry.requirePointer).
func (s *SetRegistry) requireSet(graph GraphReader, set NodeID) error {
	return requireTagged(graph, set, s.allSets, ErrNotSet)
}

// requireSetMember is requireSet plus a check that member exists.
func (s *SetRegistry) requireSetMember(graph GraphReader, set, member NodeID) error {
	if requireErr := s.requireSet(graph, set); requireErr != nil {
		return requireErr
	}

	exists, err := graph.NodeExists(member)
	if err != nil {
		return wrapInterfaceErr(err)
	}
	if !exists {
		return ErrNodeNotFound
	}

	return nil
}

// NewSet creates a fresh NodeID and tags it (AllSets, id). The new set
// starts empty.
func (s *SetRegistry) NewSet(graph Transactor) (NodeID, error) {
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		return createTaggedNodeTx(tx, s.allSets)
	})
}

// TagAsSet tags an existing node id as Set-kind.
//
// Unlike PointerRegistry.TagAsPointer, no cardinality invariant needs to
// be checked before tagging: a Set imposes no cardinality constraint on
// its children, so id's existing children, however many, simply become
// its members once tagged. Tagging an id that is already tagged Set-kind
// is an idempotent success, exactly like the underlying
// Graph.AddRelationship being idempotent for an already-existing
// relationship.
//
// theorystate.md section 79's mutual-exclusivity rule is enforced here
// via otherSetTags, supplied at construction (see NewSetRegistry):
// ErrSetRepresentationConflict is returned if id already carries any of
// them.
func (s *SetRegistry) TagAsSet(graph Transactor, id NodeID) error {
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		exists, err := tx.NodeExists(id)
		if err != nil {
			return wrapInterfaceErr(err)
		}
		if !exists {
			return ErrNodeNotFound
		}

		for _, tag := range s.otherSetTags {
			conflict, conflictErr := tx.HasRelationship(tag, id)
			if conflictErr != nil {
				return wrapInterfaceErr(conflictErr)
			}
			if conflict {
				return ErrSetRepresentationConflict
			}
		}

		return addRelationshipTx(tx, s.allSets, id)
	}))
}

// Add adds member to set. Both must already exist, and set must already
// be tagged (AllSets, set).
//
// added reports whether member was newly added. Adding an
// already-present member -- including self-membership, Add(set, set),
// which is permitted (theorystate.md section 2.8) -- is an
// idempotent no-op reporting added == false on the repeat call.
func (s *SetRegistry) Add(graph Transactor, set, member NodeID) (added bool, err error) {
	// The checks run inside the transaction, against the same state the
	// write sees. At top level a declined commit is rolled back, so
	// transactBool reports false (nothing was added) in that case. If
	// graph is a Tx, Checkers only judge the result at the outermost
	// commit, so added is provisional until then (see Transactor).
	return transactBool(graph, func(tx Tx) (bool, error) {
		if requireErr := s.requireSetMember(tx, set, member); requireErr != nil {
			return false, requireErr
		}

		created, txErr := tx.AddRelationship(set, member)
		return created, wrapInterfaceErr(txErr)
	})
}

// Remove removes member from set, if present.
//
// removed reports whether member was actually a member and was removed;
// removing a member that was never present is a no-op reporting
// removed == false, not an error.
func (s *SetRegistry) Remove(graph Transactor, set, member NodeID) (removed bool, err error) {
	// The checks run inside the transaction, against the same state the
	// write sees. At top level a declined commit is rolled back, so
	// transactBool reports false (nothing was removed) in that case. If
	// graph is a Tx, Checkers only judge the result at the outermost
	// commit, so removed is provisional until then (see Transactor).
	return transactBool(graph, func(tx Tx) (bool, error) {
		if requireErr := s.requireSetMember(tx, set, member); requireErr != nil {
			return false, requireErr
		}

		dropped, txErr := tx.RemoveRelationship(set, member)
		return dropped, wrapInterfaceErr(txErr)
	})
}

// Contains reports whether member currently belongs to set.
func (s *SetRegistry) Contains(graph GraphReader, set, member NodeID) (bool, error) {
	if requireErr := s.requireSetMember(graph, set, member); requireErr != nil {
		return false, requireErr
	}

	has, err := graph.HasRelationship(set, member)
	return has, wrapInterfaceErr(err)
}

// Members returns every current member of set, i.e. every direct child of
// set in the underlying Graph.
//
// This does NOT recurse into any member that happens to itself be tagged
// Set-kind -- see the SetRegistry doc comment.
func (s *SetRegistry) Members(graph GraphReader, set NodeID) ([]NodeID, error) {
	if requireErr := s.requireSet(graph, set); requireErr != nil {
		return nil, requireErr
	}

	outgoing, err := graph.FindOutgoing(set)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}

	members := make([]NodeID, 0, len(outgoing))
	for _, rel := range outgoing {
		members = append(members, rel.To)
	}

	return members, nil
}

// Size returns the number of current members of set.
func (s *SetRegistry) Size(graph GraphReader, set NodeID) (int, error) {
	members, err := s.Members(graph, set)
	if err != nil {
		return 0, err
	}

	return len(members), nil
}

// DeleteSet deletes set from the underlying graph, additionally removing
// its (AllSets, set) tag as part of the same transaction -- mirroring
// ListRegistry.DeleteList: the AllSets tag is itself an ordinary
// primitive relationship *into* set, and therefore itself counts toward
// set's relationship count, so it must be removed before Graph.DeleteNode
// can succeed, not after.
//
// Per theorystate.md section 18, deletion is deliberately "delete
// only if empty," not cascade: DeleteSet refuses with ErrNodeNotEmpty
// (resolvable by removing every member and retrying) if set currently has
// any members, or is itself currently a member of some other Set or
// otherwise referenced elsewhere -- Graph.DeleteNode requires both
// outgoing and incoming relationships to be empty.
//
// set must currently be tagged (AllSets, set).
func (s *SetRegistry) DeleteSet(graph Transactor, set NodeID) error {
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		if requireErr := s.requireSet(tx, set); requireErr != nil {
			return requireErr
		}
		return untagAndDeleteNodeTx(tx, set, s.allSets)
	}))
}

// sortedNodeSet returns the members of set as a slice sorted ascending.
// The order has no semantic meaning (theorystate.md section 5); it
// exists only so results are deterministic regardless of Go's map
// iteration order. Shared by CompositeSetRegistry.evaluate,
// CompositeSetLogRegistry.evaluate, and domainConstraint.affectedAnchors.
func sortedNodeSet(set map[NodeID]struct{}) []NodeID {
	ids := make([]NodeID, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}

	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	return ids
}

// operandDescriptorAxes reads back descriptor node u's current
// operation-kind and operand-kind tags (theorystate.md section 80), plus
// its operand target, as concrete tag NodeIDs rather than the plain
// booleans exactlyOneTag reports -- needed before removing a descriptor,
// since removal must know exactly which tag relationship to remove, not
// merely which side of each axis currently holds. Shared by
// CompositeSetRegistry.RemoveOperand and
// CompositeSetLogRegistry.RemoveOperation.
func operandDescriptorAxes(graph GraphReader, u, allAdditiveOp, allSubtractiveOp, allScalarOperand, allSetOperand NodeID) (operand NodeID, hasOperand bool, operationTag, operandTag NodeID, err error) {
	operand, hasOperand, err = singleChildTarget(graph, u)
	if err != nil {
		return 0, false, 0, 0, err
	}

	additive, err := exactlyOneTag(graph, u, allAdditiveOp, allSubtractiveOp)
	if err != nil {
		return 0, false, 0, 0, err
	}
	operationTag = allAdditiveOp
	if !additive {
		operationTag = allSubtractiveOp
	}

	expand, err := exactlyOneTag(graph, u, allSetOperand, allScalarOperand)
	if err != nil {
		return 0, false, 0, 0, err
	}
	operandTag = allScalarOperand
	if expand {
		operandTag = allSetOperand
	}

	return operand, hasOperand, operationTag, operandTag, nil
}

// buildOperandDescriptorTx creates a fresh descriptor node U, tags it
// with exactly one operation-kind tag (additiveTag xor subtractiveTag)
// and exactly one operand-kind tag (scalarTag xor setTag), and wires
// U -> operand, entirely against tx. This is the shared "mint one
// operand descriptor" sequence behind CompositeSetRegistry.AddOperand
// and CompositeSetLogRegistry.AppendOperation (theorystate.md section
// 80): both structures record operands via freshly-minted, identically
// dual-tagged descriptor nodes, differing only in *where* the descriptor
// is then attached (a direct child of the composite set vs. a list
// capsule's value).
func buildOperandDescriptorTx(tx txOps, additiveTag, subtractiveTag, scalarTag, setTag, operand NodeID, additive, expand bool) (u NodeID, err error) {
	operationTag := additiveTag
	if !additive {
		operationTag = subtractiveTag
	}
	operandTag := scalarTag
	if expand {
		operandTag = setTag
	}

	u, err = tx.CreateNode()
	if err != nil {
		return 0, wrapInterfaceErr(err)
	}
	if err2 := tagNodeTx(tx, operationTag, u); err2 != nil {
		return 0, err2
	}
	if err3 := tagNodeTx(tx, operandTag, u); err3 != nil {
		return 0, err3
	}

	_, err = tx.AddRelationship(u, operand)
	return u, wrapInterfaceErr(err)
}

// clearOperandDescriptorEdgesTx removes descriptor u's own edge to its
// operand (if any) and both of its axis tags, against tx, without
// deleting u itself. Factored out as the shared "clear one descriptor's
// edges" step used by deleteOperandDescriptorTx below.
func clearOperandDescriptorEdgesTx(tx txOps, operand NodeID, hasOperand bool, operationTag, operandTag, u NodeID) error {
	if hasOperand {
		if _, err := tx.RemoveRelationship(u, operand); err != nil {
			return wrapInterfaceErr(err)
		}
	}

	if _, err := tx.RemoveRelationship(operationTag, u); err != nil {
		return wrapInterfaceErr(err)
	}

	_, err := tx.RemoveRelationship(operandTag, u)
	return wrapInterfaceErr(err)
}

// deleteOperandDescriptorTx clears descriptor u's own edges (see
// clearOperandDescriptorEdgesTx) and then deletes u itself, against tx.
// This is the shared "tear down one descriptor node" sequence behind
// CompositeSetRegistry.RemoveOperand: a CompositeSetRegistry descriptor
// has no incoming edges beyond what clearOperandDescriptorEdgesTx itself
// clears, so u can be deleted immediately afterward in the same step.
// CompositeSetLogRegistry.RemoveOperation cannot use this directly for
// exactly that reason -- see clearOperandDescriptorEdgesTx's doc comment.
func deleteOperandDescriptorTx(tx txOps, operand NodeID, hasOperand bool, operationTag, operandTag, u NodeID) error {
	if err := clearOperandDescriptorEdgesTx(tx, operand, hasOperand, operationTag, operandTag, u); err != nil {
		return err
	}

	return wrapInterfaceErr(tx.DeleteNode(u))
}

// operandTargetGeneric returns descriptor u's operand, i.e. u's single
// outgoing relationship target. Shared by CompositeSetRegistry.OperandTarget
// and CompositeSetLogRegistry.OperandTarget, since both build identically
// shaped descriptors (theorystate.md section 80).
func operandTargetGeneric(graph GraphReader, u NodeID) (operand NodeID, err error) {
	operand, found, err := singleChildTarget(graph, u)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, ErrInvalidOperandDescriptor
	}

	return operand, nil
}

// operandCarriesKnownSetTag reports whether operand currently carries any
// currently-recognized Set-representation tag (theorystate.md section
// 79): AllSets (via sets), AllCompositeSets (via composites), or, if
// logs is non-nil (see CompositeSetRegistry.SetLogs), AllCompositeSetLogs
// (via logs). Shared by CompositeSetRegistry.AddOperand and
// CompositeSetLogRegistry.AppendOperation's identical expand-time
// validation.
func operandCarriesKnownSetTag(graph GraphReader, sets *SetRegistry, composites *CompositeSetRegistry, logs *CompositeSetLogRegistry, operand NodeID) (bool, error) {
	isSet, err := sets.IsSet(graph, operand)
	if err != nil {
		return false, err
	}
	if isSet {
		return true, nil
	}

	isComposite, err := composites.IsCompositeSet(graph, operand)
	if err != nil {
		return false, err
	}
	if isComposite {
		return true, nil
	}

	if logs != nil {
		isLog, logErr := logs.IsCompositeSetLog(graph, operand)
		if logErr != nil {
			return false, logErr
		}
		if isLog {
			return true, nil
		}
	}

	return false, nil
}

// requireOperand checks, against graph, that operand exists and -- when
// expand is true -- carries one of the currently-recognized
// Set-representation tags (see operandCarriesKnownSetTag). Shared by
// CompositeSetRegistry.addOperandTx and
// CompositeSetLogRegistry.AppendOperation, which validate an operand
// identically (theorystate.md section 80).
func requireOperand(graph GraphReader, sets *SetRegistry, composites *CompositeSetRegistry, logs *CompositeSetLogRegistry, operand NodeID, expand bool) error {
	exists, err := graph.NodeExists(operand)
	if err != nil {
		return wrapInterfaceErr(err)
	}
	if !exists {
		return ErrNodeNotFound
	}

	if expand {
		known, knownErr := operandCarriesKnownSetTag(graph, sets, composites, logs, operand)
		if knownErr != nil {
			return knownErr
		}
		if !known {
			return ErrInvalidSetOperand
		}
	}

	return nil
}

// domainContainsGeneric reports whether value currently belongs to
// domain's resolved membership, dispatched by whichever of the three
// currently-implemented Set representations domain carries
// (theorystate.md section 9c): a plain Set (via sets.Contains), a
// CompositeSet (via composites.Contains), or, if logs is non-nil (same
// nil-tolerance as operandCarriesKnownSetTag/resolveSetOperandGeneric
// above), a CompositeSetLog (via logs.Contains). Each representation's
// own Contains method already handles its own internal recursion and
// cycle detection (theorystate.md section 83), so unlike
// resolveSetOperandGeneric, no visited-set threading is needed here --
// there is nothing for this function itself to recurse into.
func domainContainsGeneric(graph GraphReader, sets *SetRegistry, composites *CompositeSetRegistry, logs *CompositeSetLogRegistry, domain, value NodeID) (bool, error) {
	isSet, err := sets.IsSet(graph, domain)
	if err != nil {
		return false, err
	}
	if isSet {
		return sets.Contains(graph, domain, value)
	}

	isComposite, err := composites.IsCompositeSet(graph, domain)
	if err != nil {
		return false, err
	}
	if isComposite {
		return composites.Contains(graph, domain, value)
	}

	if logs != nil {
		isLog, logErr := logs.IsCompositeSetLog(graph, domain)
		if logErr != nil {
			return false, logErr
		}
		if isLog {
			return logs.Contains(graph, domain, value)
		}
	}

	return false, ErrInvalidSetOperand
}

// resolveSetOperandGeneric resolves operand's own current membership,
// dispatched by whichever of the three currently-implemented Set
// representations operand actually carries (theorystate.md section 83):
// a plain Set (delegated to sets), a CompositeSet (delegated to
// composites, resolved recursively), or a CompositeSetLog (delegated to
// logs, resolved recursively) -- logs may be nil if the caller has not
// wired cross-representation dispatch to a CompositeSetLogRegistry yet
// (see CompositeSetRegistry.SetLogs), in which case a CompositeSetLog
// operand is reported via ErrInvalidSetOperand exactly like any other
// operand carrying no known Set-representation tag. Shared by
// CompositeSetRegistry and CompositeSetLogRegistry's resolveOperand
// methods, via resolveOperandGeneric below.
//
// visited tracks composite-kind (CompositeSet or CompositeSetLog)
// NodeIDs currently on the resolution path, shared across both
// representations, so a cycle crossing between them is still detected
// (theorystate.md section 83).
func resolveSetOperandGeneric(graph GraphReader, sets *SetRegistry, composites *CompositeSetRegistry, logs *CompositeSetLogRegistry, operand NodeID, visited map[NodeID]struct{}) ([]NodeID, error) {
	isSet, err := sets.IsSet(graph, operand)
	if err != nil {
		return nil, err
	}
	if isSet {
		return sets.Members(graph, operand)
	}

	isComposite, err := composites.IsCompositeSet(graph, operand)
	if err != nil {
		return nil, err
	}
	if isComposite {
		if _, seen := visited[operand]; seen {
			return nil, ErrCompositeSetCycle
		}
		visited[operand] = struct{}{}
		defer delete(visited, operand)

		return composites.evaluate(graph, operand, visited)
	}

	if logs != nil {
		isLog, logErr := logs.IsCompositeSetLog(graph, operand)
		if logErr != nil {
			return nil, logErr
		}
		if isLog {
			if _, seen := visited[operand]; seen {
				return nil, ErrCompositeSetCycle
			}
			visited[operand] = struct{}{}
			defer delete(visited, operand)

			return logs.evaluate(graph, operand, visited)
		}
	}

	return nil, ErrInvalidSetOperand
}

// resolveOperandGeneric returns the set of NodeIDs descriptor u currently
// contributes: the singleton {operand} for a scalar-axis descriptor, or
// operand's own resolved membership (via resolveSetOperandGeneric) for a
// set-axis descriptor. Shared by CompositeSetRegistry.resolveOperand and
// CompositeSetLogRegistry.resolveOperand.
func resolveOperandGeneric(graph GraphReader, allScalarOperand, allSetOperand NodeID, sets *SetRegistry, composites *CompositeSetRegistry, logs *CompositeSetLogRegistry, u NodeID, visited map[NodeID]struct{}) ([]NodeID, error) {
	operand, found, err := singleChildTarget(graph, u)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrInvalidOperandDescriptor
	}

	expand, err := exactlyOneTag(graph, u, allSetOperand, allScalarOperand)
	if err != nil {
		return nil, err
	}

	if !expand {
		return []NodeID{operand}, nil
	}

	return resolveSetOperandGeneric(graph, sets, composites, logs, operand, visited)
}

// CompositeSetRegistry implements the unordered composite Set
// representation of theorystate.md sections 80/81: (AllCompositeSets, C)
// tags C as CompositeSet-kind, and C's direct children are
// operand-descriptor nodes (theorystate.md section 75's occurrence/role-
// identity pattern, applied to composite Set operands) rather than
// members themselves -- unlike a plain Set (SetRegistry), whose direct
// children are its members directly.
//
// Each operand is represented by a freshly minted descriptor node U:
//
//	set -> U -> operand
//
// tagged along two independent, orthogonal axes (theorystate.md
// section 80):
//   - operation kind: (AllAdditiveOp, U) or (AllSubtractiveOp, U) --
//     whether operand contributes to set's evaluated membership via
//     union or via set-difference.
//   - operand kind: (AllScalarOperand, U) or (AllSetOperand, U) --
//     whether operand is used as a single literal member, or expanded
//     via its own current Set-kind membership.
//
// Design note -- why operand kind is always an explicit tag on U, never
// inferred from operand's own tags: an early design draft inferred
// "should this operand be expanded" from whether operand itself happened
// to already be tagged Set-kind. This repeats, one level up, the exact
// mistake theorystate.md section 10a already diagnosed and corrected for
// Pointer subject/target discovery: a node's own identity (what it
// intrinsically is) is a different fact from what a specific relationship
// means it as here. Inferring expansion from operand's own tag would make
// "add a Set object as a literal, unexpanded member of another Set" --
// explicitly permitted by theorystate.md section 9a -- inexpressible for
// composite Sets: a Set-tagged node could then only ever be used as an
// expansion operand, never as a plain scalar member, anywhere. Recording
// the intent explicitly on U, per relationship, avoids this entirely --
// the same node can freely be a literal member in one composite Set and
// an expansion operand in another, or even both within the same one.
//
// Evaluate folds set's operand descriptors per theorystate.md section 81:
//
//	Evaluate(set) = (union of resolved(u) for every additive u)
//	                minus (union of resolved(u) for every subtractive u)
//
// where `resolved(u)` of a scalar-axis u is the singleton {operand}, and of
// a set-axis u is operand's own current evaluated/derived membership,
// recursively resolved through whichever of the currently-implemented Set
// representations operand actually is -- a plain Set (delegated to the
// embedded SetRegistry), another CompositeSet (resolved recursively), or
// a CompositeSetLog (delegated to the logs field, resolved recursively,
// once wired via SetLogs) -- theorystate.md section 83's dispatcher.
//
// Like SetRegistry.Members, Evaluate is deliberately never cached: it is
// recomputed fresh from the Graph on every call, for the same reason
// given in theorystate.md sections 9a/35 -- a cached derived-membership
// view cannot be kept honestly in sync without invalidation machinery
// that does not exist and should not be built ahead of an actual need.
//
// Per theorystate.md section 85, AddOperand always mints a fresh
// descriptor node unconditionally -- no attempt is made to find and reuse
// an existing identical one.
//
// Per theorystate.md section 79, a node may carry at most one of the
// Set-representation tags (AllSets, AllCompositeSets, and
// AllCompositeSetLogs) at a time. NewCompositeSet always mints a fresh
// node, which cannot already carry any other tag, and this registry does
// not (yet) provide a TagAsCompositeSet analogous to SetRegistry.TagAsSet
// for retagging an existing node -- so no path through this registry's
// own API can violate that invariant, unlike SetRegistry.TagAsSet, which
// does check (see its doc comment), since it operates on caller-supplied
// existing nodes. If a TagAsCompositeSet is added later, it must apply
// the same ErrSetRepresentationConflict check.
type CompositeSetRegistry struct {
	sets             *SetRegistry
	logs             *CompositeSetLogRegistry
	allCompositeSets NodeID
	allAdditiveOp    NodeID
	allSubtractiveOp NodeID
	allScalarOperand NodeID
	allSetOperand    NodeID
}

// NewCompositeSetRegistry creates a CompositeSetRegistry over graph.
// sets is used to resolve set-expansion operands that turn out to be
// plain Sets (theorystate.md section 83's dispatcher), and must already
// be constructed over the same graph. allCompositeSets tags
// CompositeSet-kind nodes; allAdditiveOp/allSubtractiveOp tag a
// descriptor's operation-kind axis; allScalarOperand/allSetOperand tag a
// descriptor's operand-kind axis. All five tag NodeIDs must already
// exist -- typically via NameRegistry.BootstrapNames(FoundationalNames).
//
// The returned registry cannot yet resolve CompositeSetLog-kind operands
// (theorystate.md section 82) -- call SetLogs once a
// CompositeSetLogRegistry exists to enable that; see SetLogs's doc
// comment for why this is a required second step rather than a
// constructor parameter.
func NewCompositeSetRegistry(graph GraphAPI, sets *SetRegistry, allCompositeSets, allAdditiveOp, allSubtractiveOp, allScalarOperand, allSetOperand NodeID) (*CompositeSetRegistry, error) {
	for _, tag := range []NodeID{allCompositeSets, allAdditiveOp, allSubtractiveOp, allScalarOperand, allSetOperand} {
		exists, err := graph.NodeExists(tag)
		if err != nil {
			return nil, wrapInterfaceErr(err)
		}
		if !exists {
			return nil, ErrNodeNotFound
		}
	}

	// Registers two Checkers (see Graph.RegisterChecker), covering two
	// genuinely different things that can go wrong with a composite Set:
	//
	// The first, keyed on allCompositeSets, walks every current child of
	// a touched composite-set node and validates each as a well-formed
	// operand descriptor -- mirroring exactly what Evaluate() already
	// does defensively on every read (see evaluate's own descriptor loop
	// via resolveOperand/exactlyOneTag). This specifically catches a
	// stray, non-descriptor child added directly to a composite set by
	// some out-of-band mutation, which the second Checker below would
	// not reach, since that stray child would carry none of the axis
	// tags the second Checker keys on.
	//
	// The second, keyed on the four operand-descriptor axis tags
	// themselves (theorystate.md section 80) rather than on
	// allCompositeSets, validates any individually touched node that
	// carries at least one of those tags directly, regardless of which
	// parent structure (if any) it currently belongs to. This is what
	// makes descriptor-shape enforcement genuinely shared with
	// CompositeSetLogRegistry (theorystate.md section 82): that
	// registry's own logged-operation descriptors are list-capsule
	// values, never children of an allCompositeSets-tagged node, so the
	// first Checker's parent-based walk could never reach them -- but
	// since CompositeSetLogRegistry is required to reuse these exact
	// same four axis-tag NodeIDs (see NewCompositeSetLogRegistry), this
	// second Checker fires on its descriptors too, the moment they are
	// touched, with no Checker of CompositeSetLogRegistry's own needed
	// at all.
	graph.RegisterChecker(Checker{
		Name: fmt.Sprintf("CompositeSetRegistry(tag=%d)", allCompositeSets),
		Tags: []NodeID{allCompositeSets},
		Check: func(g GraphReader, touched map[NodeID]struct{}) error {
			for node := range touched {
				tagged, taggedErr := g.HasRelationship(allCompositeSets, node)
				if taggedErr != nil {
					return wrapInterfaceErr(taggedErr)
				}
				if !tagged {
					continue
				}

				outgoing, err := g.FindOutgoing(node)
				if err != nil {
					return wrapInterfaceErr(err)
				}

				for _, rel := range outgoing {
					u := rel.To

					if _, additiveErr := exactlyOneTag(g, u, allAdditiveOp, allSubtractiveOp); additiveErr != nil {
						return additiveErr
					}
					if _, operandErr := exactlyOneTag(g, u, allScalarOperand, allSetOperand); operandErr != nil {
						return operandErr
					}
					if _, targetErr := operandTargetGeneric(g, u); targetErr != nil {
						return targetErr
					}
				}
			}

			return nil
		},
	})

	graph.RegisterChecker(Checker{
		Name: fmt.Sprintf("OperandDescriptor(tags=%d,%d,%d,%d)", allAdditiveOp, allSubtractiveOp, allScalarOperand, allSetOperand),
		Tags: []NodeID{allAdditiveOp, allSubtractiveOp, allScalarOperand, allSetOperand},
		Check: func(g GraphReader, touched map[NodeID]struct{}) error {
			for node := range touched {
				hasAdditive, err := g.HasRelationship(allAdditiveOp, node)
				if err != nil {
					return wrapInterfaceErr(err)
				}
				hasSubtractive, err := g.HasRelationship(allSubtractiveOp, node)
				if err != nil {
					return wrapInterfaceErr(err)
				}
				hasScalar, err := g.HasRelationship(allScalarOperand, node)
				if err != nil {
					return wrapInterfaceErr(err)
				}
				hasSet, err := g.HasRelationship(allSetOperand, node)
				if err != nil {
					return wrapInterfaceErr(err)
				}

				if !hasAdditive && !hasSubtractive && !hasScalar && !hasSet {
					continue
				}

				if _, tagErr := exactlyOneTag(g, node, allAdditiveOp, allSubtractiveOp); tagErr != nil {
					return tagErr
				}
				if _, tagErr := exactlyOneTag(g, node, allScalarOperand, allSetOperand); tagErr != nil {
					return tagErr
				}
				if _, targetErr := operandTargetGeneric(g, node); targetErr != nil {
					return targetErr
				}
			}

			return nil
		},
	})

	return &CompositeSetRegistry{
		sets:             sets,
		allCompositeSets: allCompositeSets,
		allAdditiveOp:    allAdditiveOp,
		allSubtractiveOp: allSubtractiveOp,
		allScalarOperand: allScalarOperand,
		allSetOperand:    allSetOperand,
	}, nil
}

// SetLogs wires this CompositeSetRegistry to logs, letting Evaluate/
// AddOperand recognize and resolve operands that are themselves
// CompositeSetLog-kind (theorystate.md section 82/83). This is separate
// from NewCompositeSetRegistry because CompositeSetLogRegistry itself
// depends on an existing *CompositeSetRegistry (to resolve its own
// CompositeSet-kind operands, and to obtain the shared *SetRegistry it
// dispatches plain-Set operands through -- see
// NewCompositeSetLogRegistry) -- the two representations mutually
// reference each other, and Go cannot construct two such values in a
// single mutually-referential step. Construct in the order
// SetRegistry -> CompositeSetRegistry -> CompositeSetLogRegistry(composites)
// -> composites.SetLogs(that log registry).
//
// Calling this is optional: a CompositeSetRegistry with logs left unset
// (nil) still works for every other operand kind --
// resolveSetOperandGeneric treats a nil logs exactly like "this operand
// doesn't carry a recognized Set-representation tag," surfacing
// ErrInvalidSetOperand rather than panicking. Calling SetLogs again
// simply replaces the previous value; passing nil un-wires it. SetLogs
// writes an unsynchronized field that every operation reads, so it must
// be called during setup, before the registry is shared with other
// goroutines.
func (c *CompositeSetRegistry) SetLogs(logs *CompositeSetLogRegistry) {
	c.logs = logs
}

// IsCompositeSet reports whether id is currently tagged
// (AllCompositeSets, id).
func (c *CompositeSetRegistry) IsCompositeSet(graph GraphReader, id NodeID) (bool, error) {
	has, err := graph.HasRelationship(c.allCompositeSets, id)
	return has, wrapInterfaceErr(err)
}

// requireCompositeSet checks that set exists and is tagged
// (AllCompositeSets, set), returning ErrNodeNotFound or
// ErrNotCompositeSet otherwise. Delegates to the shared requireTagged
// helper (see its doc comment on PointerRegistry.requirePointer).
func (c *CompositeSetRegistry) requireCompositeSet(graph GraphReader, set NodeID) error {
	return requireTagged(graph, set, c.allCompositeSets, ErrNotCompositeSet)
}

// NewCompositeSet creates a fresh NodeID and tags it (AllCompositeSets,
// id). The new composite set starts with no operands, evaluating to the
// empty set.
func (c *CompositeSetRegistry) NewCompositeSet(graph Transactor) (NodeID, error) {
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		return createTaggedNodeTx(tx, c.allCompositeSets)
	})
}

// AddOperand adds an operand to set, represented by a freshly minted
// descriptor node U wired entirely inside one transaction (nested if
// graph is a Tx):
// set -> U -> operand, with U tagged along both axes described in the
// CompositeSetRegistry doc comment.
//
// additive selects the operation-kind axis (true: union / additive,
// false: set-difference / subtractive). expand selects the operand-kind
// axis (true: operand is expanded via its own Set-kind membership,
// false: operand is used as a single literal member).
//
// If expand is true, operand must already carry one of the
// currently-recognized Set-representation tags (AllSets,
// AllCompositeSets, or, if this registry has been wired via SetLogs,
// AllCompositeSetLogs) -- checked here, at write time, before U is
// created at all, via operandCarriesKnownSetTag -- returning
// ErrInvalidSetOperand otherwise. If expand is false, operand may be any
// existing node of any kind.
//
// set must already be tagged (AllCompositeSets, set); operand must
// already exist. Per theorystate.md section 85, no existing identical
// descriptor is searched for or reused -- see the CompositeSetRegistry
// doc comment.
func (c *CompositeSetRegistry) AddOperand(graph Transactor, set, operand NodeID, additive, expand bool) (NodeID, error) {
	// Every check and the descriptor's creation see the same state.
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		if requireErr := c.requireCompositeSet(tx, set); requireErr != nil {
			return 0, requireErr
		}

		if requireErr := requireOperand(tx, c.sets, c, c.logs, operand, expand); requireErr != nil {
			return 0, requireErr
		}

		u, err := buildOperandDescriptorTx(tx, c.allAdditiveOp, c.allSubtractiveOp, c.allScalarOperand, c.allSetOperand, operand, additive, expand)
		if err != nil {
			return 0, err
		}

		if linkErr := addRelationshipTx(tx, set, u); linkErr != nil {
			return 0, linkErr
		}

		return u, nil
	})
}

// RemoveOperand removes descriptor u from set entirely -- the
// containment edge (set, u), u's own edge to its operand, and both of u's
// axis tags -- then deletes u itself, all inside one transaction (nested
// if graph is a Tx).
//
// This is deliberately simpler than CapsuleRegistry.DeleteCapsule: a
// descriptor node u has no sub-structure of its own (unlike a capsule's
// three role slots), so there is nothing else that could be left
// dangling by removing it. If u has picked up some unrelated extra
// relationship through an out-of-band mutation, the final tx.DeleteNode
// call simply fails with the underlying ErrNodeNotEmpty, and the whole
// removal rolls back via ordinary Transact rollback -- no separate
// ErrCapsuleNotEmpty-style check is needed here.
//
// u must currently be a descriptor of set, i.e. (set, u) must exist;
// otherwise ErrOperandNotInCompositeSet is returned. operand itself is
// never deleted -- only u's own edge to it is removed -- since operand is
// caller-owned data that may still be referenced elsewhere.
func (c *CompositeSetRegistry) RemoveOperand(graph Transactor, set, u NodeID) error {
	// The descriptor's axes are read through tx too, so the tags removed
	// are the ones it actually has when the removal happens.
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		if requireErr := c.requireCompositeSet(tx, set); requireErr != nil {
			return requireErr
		}

		inSet, err := tx.HasRelationship(set, u)
		if err != nil {
			return wrapInterfaceErr(err)
		}
		if !inSet {
			return ErrOperandNotInCompositeSet
		}

		operand, hasOperand, operationTag, operandTag, err := operandDescriptorAxes(tx, u, c.allAdditiveOp, c.allSubtractiveOp, c.allScalarOperand, c.allSetOperand)
		if err != nil {
			return err
		}

		if removeErr := removeRelationshipTx(tx, set, u); removeErr != nil {
			return removeErr
		}

		return deleteOperandDescriptorTx(tx, operand, hasOperand, operationTag, operandTag, u)
	}))
}

// Operands returns set's current operand-descriptor nodes -- its direct
// children in the underlying Graph -- in no particular semantic order
// beyond Graph.FindOutgoing's own deterministic NodeID sort. Use
// OperandTarget/OperandIsAdditive/OperandIsSetOperand to inspect each
// one.
func (c *CompositeSetRegistry) Operands(graph GraphReader, set NodeID) ([]NodeID, error) {
	if requireErr := c.requireCompositeSet(graph, set); requireErr != nil {
		return nil, requireErr
	}

	outgoing, err := graph.FindOutgoing(set)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}

	operands := make([]NodeID, 0, len(outgoing))
	for _, rel := range outgoing {
		operands = append(operands, rel.To)
	}

	return operands, nil
}

// OperandTarget returns descriptor u's operand, i.e. u's single outgoing
// relationship target. Shared logic with CompositeSetLogRegistry.OperandTarget
// -- see operandTargetGeneric.
func (c *CompositeSetRegistry) OperandTarget(graph GraphReader, u NodeID) (operand NodeID, err error) {
	return operandTargetGeneric(graph, u)
}

// OperandIsAdditive reports whether descriptor u is tagged additive
// (true, contributes via union) or subtractive (false, contributes via
// set-difference).
func (c *CompositeSetRegistry) OperandIsAdditive(graph GraphReader, u NodeID) (bool, error) {
	return exactlyOneTag(graph, u, c.allAdditiveOp, c.allSubtractiveOp)
}

// OperandIsSetOperand reports whether descriptor u is tagged as a
// set-expansion operand (true) or a scalar operand (false).
func (c *CompositeSetRegistry) OperandIsSetOperand(graph GraphReader, u NodeID) (bool, error) {
	return exactlyOneTag(graph, u, c.allSetOperand, c.allScalarOperand)
}

// Evaluate computes set's current membership by folding its operand
// descriptors per theorystate.md section 81 -- see the
// CompositeSetRegistry doc comment for the exact fold and for why this is
// never cached.
//
// set must already be tagged (AllCompositeSets, set). If evaluating set
// requires expanding a nested composite Set operand and that expansion
// would revisit a composite-kind node already on the current resolution
// path, ErrCompositeSetCycle is returned (theorystate.md section 83).
func (c *CompositeSetRegistry) Evaluate(graph GraphReader, set NodeID) ([]NodeID, error) {
	if requireErr := c.requireCompositeSet(graph, set); requireErr != nil {
		return nil, requireErr
	}

	return c.evaluate(graph, set, map[NodeID]struct{}{set: {}})
}

// Contains reports whether value currently belongs to set's evaluated
// membership -- a thin wrapper around Evaluate, added for symmetry with
// SetRegistry.Contains and CompositeSetLogRegistry.Contains. Unlike
// CompositeSetLogRegistry.Contains, this does not implement a
// backward-scan optimization: CompositeSetRegistry's union-then-
// difference fold (theorystate.md section 81) is not order-sensitive,
// so there is no "most recent mention" to scan backward toward -- the
// full Evaluate must run regardless.
//
// set must already be tagged (AllCompositeSets, set); value must already
// exist. Like Evaluate, this is never cached.
func (c *CompositeSetRegistry) Contains(graph GraphReader, set, value NodeID) (bool, error) {
	existsSet, err := graph.NodeExists(set)
	if err != nil {
		return false, wrapInterfaceErr(err)
	}
	if !existsSet {
		return false, ErrNodeNotFound
	}

	isComposite, err := c.IsCompositeSet(graph, set)
	if err != nil {
		return false, err
	}
	if !isComposite {
		return false, ErrNotCompositeSet
	}

	existsValue, err := graph.NodeExists(value)
	if err != nil {
		return false, wrapInterfaceErr(err)
	}
	if !existsValue {
		return false, ErrNodeNotFound
	}

	members, err := c.evaluate(graph, set, map[NodeID]struct{}{set: {}})
	if err != nil {
		return false, err
	}

	for _, id := range members {
		if id == value {
			return true, nil
		}
	}

	return false, nil
}

// evaluate is Evaluate's recursive core, assuming set has already been
// confirmed to exist, to be tagged CompositeSet-kind, and to already be
// recorded in visited.
//
// visited tracks composite-kind NodeIDs currently on the resolution
// path -- not every composite-kind node ever seen during this Evaluate
// call -- so that a DAG where the same composite Set is legitimately
// reached via two different, non-cyclic branches is not mistaken for a
// cycle. resolveSetOperand adds to visited immediately before, and
// removes from visited immediately after, each recursive call into a
// nested composite Set (a standard depth-first on-stack cycle check);
// evaluate itself never mutates visited directly.
func (c *CompositeSetRegistry) evaluate(graph GraphReader, set NodeID, visited map[NodeID]struct{}) ([]NodeID, error) {
	operands, err := graph.FindOutgoing(set)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}

	type descriptor struct {
		u        NodeID
		additive bool
	}

	descriptors := make([]descriptor, 0, len(operands))
	for _, rel := range operands {
		additive, err := exactlyOneTag(graph, rel.To, c.allAdditiveOp, c.allSubtractiveOp)
		if err != nil {
			return nil, err
		}
		descriptors = append(descriptors, descriptor{u: rel.To, additive: additive})
	}

	result := make(map[NodeID]struct{})

	// Additive operands are folded first (union), then subtractive
	// operands (set-difference). This grouping -- not the relative order
	// within each group, which carries no meaning for union/difference --
	// is what keeps the result independent of FindOutgoing's arbitrary
	// NodeID-sorted order.
	for _, d := range descriptors {
		if !d.additive {
			continue
		}
		resolved, err := c.resolveOperand(graph, d.u, visited)
		if err != nil {
			return nil, err
		}
		for _, id := range resolved {
			result[id] = struct{}{}
		}
	}
	for _, d := range descriptors {
		if d.additive {
			continue
		}
		resolved, err := c.resolveOperand(graph, d.u, visited)
		if err != nil {
			return nil, err
		}
		for _, id := range resolved {
			delete(result, id)
		}
	}

	return sortedNodeSet(result), nil
}

// resolveOperand returns the set of NodeIDs descriptor u currently
// contributes, dispatched via resolveOperandGeneric. Shared logic with
// CompositeSetLogRegistry.resolveOperand.
func (c *CompositeSetRegistry) resolveOperand(graph GraphReader, u NodeID, visited map[NodeID]struct{}) ([]NodeID, error) {
	return resolveOperandGeneric(graph, c.allScalarOperand, c.allSetOperand, c.sets, c, c.logs, u, visited)
}

// DeleteCompositeSet deletes set from the underlying graph, additionally
// removing its (AllCompositeSets, set) tag as part of the same
// transaction -- mirroring SetRegistry.DeleteSet/ListRegistry.DeleteList:
// the tag is itself an ordinary primitive relationship into set, so it
// must be removed before Graph.DeleteNode can succeed, not after.
//
// Per theorystate.md section 18, deletion is deliberately "delete only if
// empty": DeleteCompositeSet refuses with ErrNodeNotEmpty if set still
// has any operand descriptors, or is itself referenced elsewhere.
// Callers must RemoveOperand every descriptor first.
//
// set must currently be tagged (AllCompositeSets, set).
func (c *CompositeSetRegistry) DeleteCompositeSet(graph Transactor, set NodeID) error {
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		if requireErr := c.requireCompositeSet(tx, set); requireErr != nil {
			return requireErr
		}
		return untagAndDeleteNodeTx(tx, set, c.allCompositeSets)
	}))
}

// CompositeSetLogRegistry implements the append-only log-based composite
// Set representation of theorystate.md section 82: an ordered log of
// Set-mutating operations, whose current membership is the *fold* of
// that log, not an insertion-ordered collection in its own right (see
// that section for why this structure was deliberately renamed away from
// an earlier "OrderedCompositeSet" working name that wrongly implied the
// latter).
//
// A CompositeSetLog is represented as an ordinary List (ListRegistry),
// reused and reinterpreted: the tagged node carries both
// (AllLists,node) and (AllCompositeSetLogs,node) simultaneously --
// theorystate.md section 10c's precedent for one identity carrying more
// than one simultaneous interpretation, applied here to reuse
// ListRegistry's ordering machinery wholesale rather than reimplementing
// it. AllCompositeSetLogs participates in theorystate.md section 79's
// three-way Set-representation mutual exclusivity; AllLists does not,
// since it is plumbing this representation happens to be built from, not
// a Set-representation tag of its own.
//
// Each logged operation is one list element, whose *value* (via the
// ordinary, opaque ListRegistry/CapsuleRegistry value slot -- this
// registry does not reach into or specialize any List/Capsule internal)
// is a freshly minted operand-descriptor node U, exactly the same shape
// used by CompositeSetRegistry (theorystate.md section 80): U -> operand,
// tagged along the same two orthogonal axes (operation kind: additive/
// subtractive; operand kind: scalar/set, always explicit, never inferred
// from operand's own tags).
//
// Evaluate folds log's operations head to tail, in list order -- unlike
// CompositeSetRegistry.Evaluate's order-insensitive union-then-difference,
// order here is semantically load-bearing: a later operation mentioning a
// given element supersedes an earlier one.
//
//	accumulator := ∅
//	for each capsule's U, in list order:
//	    resolved := resolve(U)          // per U's operand-kind axis
//	    if U tagged additive:    accumulator := accumulator ∪ resolved
//	    if U tagged subtractive: accumulator := accumulator \ resolved
//	return accumulator
//
// Contains(log,value) exploits a proven property of this fold
// (theorystate.md section 82): value's final membership is decided
// entirely by the *last* operation in the log whose currently-resolved
// operand mentions value, so Contains scans backward and can stop at the
// first (i.e. most recent) such operation, using the cheapest membership
// check available for that operand's own kind rather than always fully
// resolving it -- see operandMentions's doc comment for exactly what is
// and is not cheaper, and Contains's own doc comment for why this
// optimization is real but not complete.
//
// Resolving a set-expansion operand dispatches, per theorystate.md
// section 83, to whichever currently-implemented Set representation the
// operand actually carries: a plain Set (delegated to sets), a
// CompositeSet (delegated to composites, resolved recursively), or
// another CompositeSetLog (resolved recursively via this same type).
// Cycle detection (theorystate.md section 83) tracks composite-kind
// (CompositeSet or CompositeSetLog) NodeIDs on the current resolution
// path, shared across both representations via one visited set, so a
// cycle crossing between them is still caught.
//
// Like every other Evaluate/Members in this file, Evaluate and Contains
// are never cached -- recomputed fresh from the Graph on every call, for
// the reasons given throughout (theorystate.md sections 9a/35).
//
// A capsule created via this registry's own AppendOperation always
// carries a well-formed operand descriptor as its value. A capsule
// created by bypassing this registry and calling the underlying
// ListRegistry.Append directly produces a capsule whose value has no
// descriptor tags at all; Evaluate/Contains fail loudly
// (ErrInvalidOperandDescriptor) on encountering such a capsule rather
// than guessing a default operation kind, matching this codebase's
// existing fail-loud-not-silently-repair discipline.
//
// CompositeSetLogRegistry depends on an existing *CompositeSetRegistry
// (to resolve CompositeSet-kind operands, and as the source of the
// shared *SetRegistry used to resolve plain-Set operands -- see
// NewCompositeSetLogRegistry); a *CompositeSetRegistry, in turn, must be
// told about a CompositeSetLogRegistry after the fact (via
// CompositeSetRegistry.SetLogs) to resolve CompositeSetLog-kind operands
// itself, since the two types mutually reference each other and Go
// cannot construct two such values in a single mutually-referential
// step. See CompositeSetRegistry.SetLogs's doc comment for the required
// construction order.
type CompositeSetLogRegistry struct {
	lists               *ListRegistry
	sets                *SetRegistry
	composites          *CompositeSetRegistry
	allCompositeSetLogs NodeID
	allAdditiveOp       NodeID
	allSubtractiveOp    NodeID
	allScalarOperand    NodeID
	allSetOperand       NodeID
}

// NewCompositeSetLogRegistry creates a CompositeSetLogRegistry over
// graph. This registers no Checker of its own (see Graph.RegisterChecker
// and NewCompositeSetRegistry's own two Checkers' doc comment): its
// underlying List's structure is already covered by the ListRegistry
// Checker registered when lists was itself constructed, and its logged
// operations' descriptor shape is already covered by the
// operand-descriptor Checker registered when composites was itself
// constructed, since both required arguments below must already exist.
//
// lists is used to store and traverse the log itself (each logged
// operation is one list element, in append order); composites is used
// both to resolve set-expansion operands that turn out to be
// CompositeSets (theorystate.md section 83's dispatcher) and as the
// source of the SetRegistry used to resolve plain-Set operands (via
// composites.sets, avoiding a second, independently-passed SetRegistry
// that could otherwise silently disagree with the one composites itself
// dispatches through) -- composites must already be constructed over the
// same graph. allCompositeSetLogs tags CompositeSetLog-kind nodes
// (alongside lists' own AllLists tag -- see the CompositeSetLogRegistry
// doc comment); allAdditiveOp/allSubtractiveOp and allScalarOperand/
// allSetOperand tag a logged operation's descriptor exactly as they do
// for CompositeSetRegistry (theorystate.md section 80) -- the same
// bootstrapped tag NodeIDs are expected to be passed to both
// constructors. All five tag NodeIDs must already exist -- typically via
// NameRegistry.BootstrapNames(FoundationalNames).
//
// This CompositeSetLogRegistry can immediately resolve CompositeSet and
// plain Set operands, but cannot yet resolve CompositeSetLog operands
// (including recursive self-reference) until composites is told about it
// via composites.SetLogs(this registry) -- see that method's doc comment
// for why this second wiring step is required.
func NewCompositeSetLogRegistry(graph GraphAPI, lists *ListRegistry, composites *CompositeSetRegistry, allCompositeSetLogs, allAdditiveOp, allSubtractiveOp, allScalarOperand, allSetOperand NodeID) (*CompositeSetLogRegistry, error) {
	for _, tag := range []NodeID{allCompositeSetLogs, allAdditiveOp, allSubtractiveOp, allScalarOperand, allSetOperand} {
		exists, err := graph.NodeExists(tag)
		if err != nil {
			return nil, wrapInterfaceErr(err)
		}
		if !exists {
			return nil, ErrNodeNotFound
		}
	}

	return &CompositeSetLogRegistry{
		lists:               lists,
		sets:                composites.sets,
		composites:          composites,
		allCompositeSetLogs: allCompositeSetLogs,
		allAdditiveOp:       allAdditiveOp,
		allSubtractiveOp:    allSubtractiveOp,
		allScalarOperand:    allScalarOperand,
		allSetOperand:       allSetOperand,
	}, nil
}

// IsCompositeSetLog reports whether id is currently tagged
// (AllCompositeSetLogs, id).
func (c *CompositeSetLogRegistry) IsCompositeSetLog(graph GraphReader, id NodeID) (bool, error) {
	has, err := graph.HasRelationship(c.allCompositeSetLogs, id)
	return has, wrapInterfaceErr(err)
}

// requireLog checks that log exists and is tagged
// (AllCompositeSetLogs, log), returning ErrNodeNotFound or
// ErrNotCompositeSetLog otherwise. Delegates to the shared requireTagged
// helper (see its doc comment on PointerRegistry.requirePointer).
func (c *CompositeSetLogRegistry) requireLog(graph GraphReader, log NodeID) error {
	return requireTagged(graph, log, c.allCompositeSetLogs, ErrNotCompositeSetLog)
}

// NewCompositeSetLog creates a fresh NodeID and tags it both
// (AllLists, id) and (AllCompositeSetLogs, id), entirely inside one
// transaction (nested if graph is a Tx): ListRegistry.NewList mints and
// tags the list as a nested step and the second tag is applied in the
// same transaction, so there is no observable intermediate state where id
// is tagged AllLists but not yet AllCompositeSetLogs. The new log starts
// empty: no operations, no head, no tail.
func (c *CompositeSetLogRegistry) NewCompositeSetLog(graph Transactor) (NodeID, error) {
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		id, err := c.lists.NewList(tx)
		if err != nil {
			return 0, err
		}

		if tagErr := tagNodeTx(tx, c.allCompositeSetLogs, id); tagErr != nil {
			return 0, tagErr
		}

		return id, nil
	})
}

// AppendOperation appends a new operation to the tail of log, recorded as
// a freshly minted operand-descriptor node U (theorystate.md section 80)
// used as the new list element's value: log -> capsule -> U -> operand,
// with U tagged along both axes described in the CompositeSetLogRegistry
// doc comment. Entirely inside one Graph.Transact call, composing
// buildOperandDescriptorTx (minting and tagging U) with
// ListRegistry.Append (wiring U in as the new tail element's value).
//
// additive/expand mean exactly what they do for
// CompositeSetRegistry.AddOperand (see its doc comment). If expand is
// true, operand must already carry one of the currently-recognized
// Set-representation tags -- checked here, at write time, before U is
// created at all -- returning ErrInvalidSetOperand otherwise.
//
// log must already be tagged (AllCompositeSetLogs, log); operand must
// already exist. Per theorystate.md section 85, no existing identical
// descriptor is searched for or reused, exactly like AddOperand.
func (c *CompositeSetLogRegistry) AppendOperation(graph Transactor, log, operand NodeID, additive, expand bool) (u, capsule NodeID, err error) {
	err = graph.Transact(func(tx Tx) error {
		if requireErr := c.requireLog(tx, log); requireErr != nil {
			return requireErr
		}

		if requireErr := requireOperand(tx, c.sets, c.composites, c, operand, expand); requireErr != nil {
			return requireErr
		}

		var err2 error
		u, err2 = buildOperandDescriptorTx(tx, c.allAdditiveOp, c.allSubtractiveOp, c.allScalarOperand, c.allSetOperand, operand, additive, expand)
		if err2 != nil {
			return err2
		}

		capsule, err2 = c.lists.Append(tx, log, u)
		return err2
	})
	if err != nil {
		return 0, 0, wrapInterfaceErr(err)
	}

	return u, capsule, nil
}

// RemoveOperation removes capsule -- and its descriptor value u -- from
// log entirely.
//
// The whole removal is one Graph.Transact call: capsule is unlinked from
// log (ListRegistry.RemoveWithoutDeletingCapsule), reclaimed
// (CapsuleRegistry.DeleteCapsule, whose teardown clears capsule's
// value-slot edge into u), and then u's own edges (its operand target and
// both axis tags) are cleared and u deleted (deleteOperandDescriptorTx,
// the helper CompositeSetRegistry.RemoveOperand also uses). It is
// all-or-nothing: if any step fails -- ErrCapsuleNotEmpty because
// something unexpected still references one of capsule's role slots, or a
// commit-time Checker declines the resulting state -- every step is
// rolled back and the log is exactly as it was. An earlier version used
// three separate transactions, so a failed capsule deletion left capsule
// unlinked from log but still holding u.
//
// capsule must currently be an element of log (checked via the
// (log,capsule) containment edge, returning ErrCapsuleNotInList
// otherwise); log must already be tagged (AllCompositeSetLogs, log).
func (c *CompositeSetLogRegistry) RemoveOperation(graph Transactor, log, capsule NodeID) error {
	// Every read, including the descriptor's axes, goes through tx. If
	// graph is a Tx and this fails, only this call is undone.
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		if requireErr := c.requireLog(tx, log); requireErr != nil {
			return requireErr
		}

		inLog, err := tx.HasRelationship(log, capsule)
		if err != nil {
			return wrapInterfaceErr(err)
		}
		if !inLog {
			return ErrCapsuleNotInList
		}

		u, hasValue, err := c.lists.capsules.Value(tx, capsule)
		if err != nil {
			return err
		}
		if !hasValue {
			return ErrInvalidOperandDescriptor
		}

		operand, hasOperand, operationTag, operandTag, err := operandDescriptorAxes(tx, u, c.allAdditiveOp, c.allSubtractiveOp, c.allScalarOperand, c.allSetOperand)
		if err != nil {
			return err
		}

		if unlinkErr := c.lists.RemoveWithoutDeletingCapsule(tx, log, capsule); unlinkErr != nil {
			return unlinkErr
		}

		if deleteErr := c.lists.capsules.DeleteCapsule(tx, capsule); deleteErr != nil {
			return deleteErr
		}

		return deleteOperandDescriptorTx(tx, operand, hasOperand, operationTag, operandTag, u)
	}))
}

// Operations returns log's current operand-descriptor nodes (each
// logged operation's U, per theorystate.md section 80), in log order
// (head to tail) -- unlike CompositeSetRegistry.Operands, this order is
// semantically meaningful for a CompositeSetLog (theorystate.md section
// 82's fold is order-sensitive). Use OperandTarget/OperandIsAdditive/
// OperandIsSetOperand to inspect each one.
func (c *CompositeSetLogRegistry) Operations(graph GraphReader, log NodeID) ([]NodeID, error) {
	if requireErr := c.requireLog(graph, log); requireErr != nil {
		return nil, requireErr
	}

	return c.lists.Elements(graph, log)
}

// OperandTarget returns descriptor u's operand, i.e. u's single outgoing
// relationship target. Shared logic with CompositeSetRegistry.OperandTarget
// -- see operandTargetGeneric.
func (c *CompositeSetLogRegistry) OperandTarget(graph GraphReader, u NodeID) (operand NodeID, err error) {
	return operandTargetGeneric(graph, u)
}

// OperandIsAdditive reports whether descriptor u is tagged additive
// (true, contributes via union) or subtractive (false, contributes via
// set-difference).
func (c *CompositeSetLogRegistry) OperandIsAdditive(graph GraphReader, u NodeID) (bool, error) {
	return exactlyOneTag(graph, u, c.allAdditiveOp, c.allSubtractiveOp)
}

// OperandIsSetOperand reports whether descriptor u is tagged as a
// set-expansion operand (true) or a scalar operand (false).
func (c *CompositeSetLogRegistry) OperandIsSetOperand(graph GraphReader, u NodeID) (bool, error) {
	return exactlyOneTag(graph, u, c.allSetOperand, c.allScalarOperand)
}

// Evaluate computes log's current membership by folding its logged
// operations left-to-right (head to tail) per theorystate.md section 82
// -- see the CompositeSetLogRegistry doc comment for the exact fold and
// for why order is semantically load-bearing here, unlike
// CompositeSetRegistry.Evaluate's order-insensitive union-then-difference.
//
// log must already be tagged (AllCompositeSetLogs, log). If evaluating
// log requires expanding a nested composite-kind operand (CompositeSet or
// CompositeSetLog) and that expansion would revisit a composite-kind node
// already on the current resolution path, ErrCompositeSetCycle is
// returned (theorystate.md section 83).
func (c *CompositeSetLogRegistry) Evaluate(graph GraphReader, log NodeID) ([]NodeID, error) {
	if requireErr := c.requireLog(graph, log); requireErr != nil {
		return nil, requireErr
	}

	return c.evaluate(graph, log, map[NodeID]struct{}{log: {}})
}

// evaluate is Evaluate's recursive core, assuming log has already been
// confirmed to exist, to be tagged CompositeSetLog-kind, and to already
// be recorded in visited. See CompositeSetRegistry.evaluate's doc
// comment for why visited is path-scoped, not a global ever-visited set
// -- the same reasoning applies identically here, now shared across both
// representations (theorystate.md section 83).
func (c *CompositeSetLogRegistry) evaluate(graph GraphReader, log NodeID, visited map[NodeID]struct{}) ([]NodeID, error) {
	operations, err := c.lists.Elements(graph, log)
	if err != nil {
		return nil, err
	}

	result := make(map[NodeID]struct{})

	for _, u := range operations {
		additive, err := exactlyOneTag(graph, u, c.allAdditiveOp, c.allSubtractiveOp)
		if err != nil {
			return nil, err
		}

		resolved, err := c.resolveOperand(graph, u, visited)
		if err != nil {
			return nil, err
		}

		if additive {
			for _, id := range resolved {
				result[id] = struct{}{}
			}
		} else {
			for _, id := range resolved {
				delete(result, id)
			}
		}
	}

	return sortedNodeSet(result), nil
}

// resolveOperand returns the set of NodeIDs descriptor u currently
// contributes -- shared logic with CompositeSetRegistry.resolveOperand,
// see resolveOperandGeneric.
func (c *CompositeSetLogRegistry) resolveOperand(graph GraphReader, u NodeID, visited map[NodeID]struct{}) ([]NodeID, error) {
	return resolveOperandGeneric(graph, c.allScalarOperand, c.allSetOperand, c.sets, c.composites, c, u, visited)
}

// Contains reports whether value currently belongs to log's evaluated
// membership, without necessarily folding the entire log.
//
// Per theorystate.md section 82's proven property, this scans log
// backward (tail to head): the *last* operation whose currently-resolved
// operand mentions value entirely determines value's final membership
// (additive -> present, subtractive -> absent), since every later
// operation, by definition of "last mentioning operation", does not
// itself resolve to something containing value, so no later union can
// reintroduce it and no later subtraction can remove it again. This lets
// Contains stop at the first (i.e. most recent) qualifying operation
// using the cheapest membership check available for that operand's kind
// (see operandMentions) -- a real optimization in the common case, but
// not a complete one: an operand resolving through a nested CompositeSet
// or CompositeSetLog still requires fully resolving that operand (there
// is no cheaper partial check available for those), and the worst case
// (value absent entirely, or only mentioned at the head) still walks the
// whole log. See theorystate.md section 82 for why no cache is kept to
// avoid this cost, consistent with every other Evaluate/Contains in this
// file.
func (c *CompositeSetLogRegistry) Contains(graph GraphReader, log, value NodeID) (bool, error) {
	existsLog, err := graph.NodeExists(log)
	if err != nil {
		return false, wrapInterfaceErr(err)
	}
	if !existsLog {
		return false, ErrNodeNotFound
	}

	isLog, err := c.IsCompositeSetLog(graph, log)
	if err != nil {
		return false, err
	}
	if !isLog {
		return false, ErrNotCompositeSetLog
	}

	existsValue, err := graph.NodeExists(value)
	if err != nil {
		return false, wrapInterfaceErr(err)
	}
	if !existsValue {
		return false, ErrNodeNotFound
	}

	return c.contains(graph, log, value, map[NodeID]struct{}{log: {}})
}

// contains is Contains's recursive core, assuming log and value have
// already been confirmed to exist, log to be tagged CompositeSetLog-kind,
// and log to already be recorded in visited.
func (c *CompositeSetLogRegistry) contains(graph GraphReader, log, value NodeID, visited map[NodeID]struct{}) (bool, error) {
	operations, err := c.lists.Elements(graph, log)
	if err != nil {
		return false, err
	}

	for i := len(operations) - 1; i >= 0; i-- {
		u := operations[i]

		operand, err := operandTargetGeneric(graph, u)
		if err != nil {
			return false, err
		}

		expand, err := exactlyOneTag(graph, u, c.allSetOperand, c.allScalarOperand)
		if err != nil {
			return false, err
		}

		mentions, err := c.operandMentions(graph, operand, expand, value, visited)
		if err != nil {
			return false, err
		}
		if !mentions {
			continue
		}

		return exactlyOneTag(graph, u, c.allAdditiveOp, c.allSubtractiveOp)
	}

	return false, nil
}

// operandMentions reports whether operand's contribution -- the
// singleton {operand} for a scalar-axis descriptor (expand == false), or
// operand's own current resolved membership for a set-axis descriptor
// (expand == true) -- includes value, using the cheapest check available
// for operand's own kind (theorystate.md section 82): direct equality
// for a scalar operand, SetRegistry.Contains (an O(1) relationship
// check) for a plain Set operand, and full recursive resolution (no
// cheaper check exists for either composite representation) for a
// nested CompositeSet or CompositeSetLog operand.
func (c *CompositeSetLogRegistry) operandMentions(graph GraphReader, operand NodeID, expand bool, value NodeID, visited map[NodeID]struct{}) (bool, error) {
	if !expand {
		return operand == value, nil
	}

	isSet, err := c.sets.IsSet(graph, operand)
	if err != nil {
		return false, err
	}
	if isSet {
		return c.sets.Contains(graph, operand, value)
	}

	isComposite, err := c.composites.IsCompositeSet(graph, operand)
	if err != nil {
		return false, err
	}
	if isComposite {
		if _, seen := visited[operand]; seen {
			return false, ErrCompositeSetCycle
		}
		visited[operand] = struct{}{}
		defer delete(visited, operand)

		resolved, evalErr := c.composites.evaluate(graph, operand, visited)
		if evalErr != nil {
			return false, evalErr
		}
		for _, id := range resolved {
			if id == value {
				return true, nil
			}
		}
		return false, nil
	}

	isLog, err := c.IsCompositeSetLog(graph, operand)
	if err != nil {
		return false, err
	}
	if isLog {
		if _, seen := visited[operand]; seen {
			return false, ErrCompositeSetCycle
		}
		visited[operand] = struct{}{}
		defer delete(visited, operand)

		return c.contains(graph, operand, value, visited)
	}

	return false, ErrInvalidSetOperand
}

// DeleteCompositeSetLog deletes log from the underlying graph,
// additionally removing both of its tags -- (AllCompositeSetLogs,log)
// and (AllLists,log) -- as part of the same transaction, mirroring
// ListRegistry.DeleteList/SetRegistry.DeleteSet/
// CompositeSetRegistry.DeleteCompositeSet: each tag is itself an
// ordinary primitive relationship *into* log, and therefore itself
// counts toward log's relationship count, so both must be removed before
// Graph.DeleteNode can succeed, not after.
//
// Per theorystate.md section 18, deletion is deliberately "delete only
// if empty": DeleteCompositeSetLog refuses with ErrNodeNotEmpty if log
// still has any logged operations, or is itself referenced elsewhere.
// Callers must RemoveOperation every logged operation first.
//
// log must currently be tagged (AllCompositeSetLogs, log).
func (c *CompositeSetLogRegistry) DeleteCompositeSetLog(graph Transactor, log NodeID) error {
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		if requireErr := c.requireLog(tx, log); requireErr != nil {
			return requireErr
		}
		return untagAndDeleteNodeTx(tx, log, c.allCompositeSetLogs, c.lists.allLists)
	}))
}

// domainConstraint holds the shared domain-slot state and operations
// used by both DomainPointerRegistryB (Representation B) and
// DomainPointerRegistryD (Representation D) -- see theorystate.md
// section 10c for why only Representations B and D can safely carry a
// domain slot, and section 9c for why a "domain" is any node already
// carrying one of the three Set-representation tags rather than a new
// tagged concept of its own.
//
// Both representations attach the domain slot identically: a single
// freshly-minted node U3, tagged AllDomainSlot, wired
// anchor -> U3 -> domainNode. They differ only in which node serves as
// "anchor" (P itself for B, the metadata node M for D) and in which
// underlying Pointer representation ultimately enforces the pointer's
// own target cardinality -- exactly the same split subjectMetadataBase
// already makes between shared subject-side logic and each
// representation's own target discovery (see that type's doc comment),
// applied here to the domain-slot concept instead.
//
// domainSlots is expected to be a single, shared *PointerRegistry
// instance -- constructed once via NewPointerRegistry(graph,
// allDomainSlot) -- passed to every domainConstraint-embedding registry
// in the same graph. Constructing a second, independent PointerRegistry
// under the same tag would register a redundant (if harmless) duplicate
// Checker for the identical cardinality invariant.
type domainConstraint struct {
	domainSlots *PointerRegistry
	sets        *SetRegistry
	composites  *CompositeSetRegistry
	logs        *CompositeSetLogRegistry
}

// domainSlotFor returns anchor's domain-slot child (U3), if any, found
// by tag rather than by position or exclusion, exactly like every other
// slot lookup in this file.
func (d *domainConstraint) domainSlotFor(graph GraphReader, anchor NodeID) (slot NodeID, found bool, err error) {
	return findUniqueTaggedChild(graph, anchor, d.domainSlots.allPointers)
}

// Domain returns anchor's current domain node, if any. hasDomain is
// false both when anchor has no domain slot at all and when it has one
// with no domain node set yet.
func (d *domainConstraint) Domain(graph GraphReader, anchor NodeID) (domain NodeID, hasDomain bool, err error) {
	slot, found, err := d.domainSlotFor(graph, anchor)
	if err != nil || !found {
		return 0, false, err
	}

	return d.domainSlots.Target(graph, slot)
}

// attachDomain sets anchor's domain to domain as one transaction (nested
// if graph is a Tx, which is how the wrapper-level SetDomain methods on
// DomainPointerRegistryB/D use it, after their own target-side
// validation), creating anchor's domain
// slot first if it does not exist yet. domain must already carry one of
// the three currently-recognized Set-representation tags
// (theorystate.md section 9c) -- checked here, at write time, via the
// same operandCarriesKnownSetTag helper composite-Set operands already
// use for the identical check (theorystate.md section 80) -- returning
// ErrInvalidSetOperand otherwise.
//
// This does NOT validate the new domain against anchor's current
// target, if any: only the representation-specific wrapper
// (DomainPointerRegistryB/D) knows how to discover anchor's current
// target for its own representation, so that check is performed there,
// before delegating to this method -- see DomainPointerRegistryB.
// SetDomain / DomainPointerRegistryD.SetDomain.
func (d *domainConstraint) attachDomain(graph Transactor, anchor, domain NodeID) error {
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		existsAnchor, err := tx.NodeExists(anchor)
		if err != nil {
			return wrapInterfaceErr(err)
		}
		if !existsAnchor {
			return ErrNodeNotFound
		}

		existsDomain, err := tx.NodeExists(domain)
		if err != nil {
			return wrapInterfaceErr(err)
		}
		if !existsDomain {
			return ErrNodeNotFound
		}

		known, err := operandCarriesKnownSetTag(tx, d.sets, d.composites, d.logs, domain)
		if err != nil {
			return err
		}
		if !known {
			return ErrInvalidSetOperand
		}

		slot, found, err := d.domainSlotFor(tx, anchor)
		if err != nil {
			return err
		}

		if !found {
			newSlot, createErr := createTaggedNodeTx(tx, d.domainSlots.allPointers)
			if createErr != nil {
				return createErr
			}
			if linkErr := addRelationshipTx(tx, anchor, newSlot); linkErr != nil {
				return linkErr
			}

			return addRelationshipTx(tx, newSlot, domain)
		}

		return d.domainSlots.SetTarget(tx, slot, domain)
	}))
}

// RemoveDomain clears anchor's domain, if any. The domain-slot node
// itself is left in place (no cascade deletion, consistent with
// theorystate.md section 18); a domain-slot with no domain set is a
// valid, meaningful "no constraint" state, exactly like an empty
// Pointer elsewhere in this file.
func (d *domainConstraint) RemoveDomain(graph Transactor, anchor NodeID) (removed bool, err error) {
	return transactBool(graph, func(tx Tx) (bool, error) {
		exists, existsErr := tx.NodeExists(anchor)
		if existsErr != nil {
			return false, wrapInterfaceErr(existsErr)
		}
		if !exists {
			return false, ErrNodeNotFound
		}

		slot, found, slotErr := d.domainSlotFor(tx, anchor)
		if slotErr != nil || !found {
			return false, slotErr
		}

		return d.domainSlots.RemoveTarget(tx, slot)
	})
}

// validateMembership reports whether target currently belongs to
// domain's resolved membership, dispatched generically over whichever of
// the three Set representations domain actually carries (see
// domainContainsGeneric), returning ErrTargetOutsideDomain if not.
func (d *domainConstraint) validateMembership(graph GraphReader, domain, target NodeID) error {
	contains, err := domainContainsGeneric(graph, d.sets, d.composites, d.logs, domain, target)
	if err != nil {
		return err
	}
	if !contains {
		return ErrTargetOutsideDomain
	}

	return nil
}

// checkAllowed reports whether target is a legal value to set on anchor,
// given whatever domain (if any) is currently attached via anchor's
// domain slot. An anchor with no domain slot, or a slot with no domain
// node set yet, always allows any target -- exactly like a Pointer with
// no domain constraint at all.
//
// This only validates against the domain's membership as of this call.
// A domain's membership changing afterward -- through a mutation that
// never touches anchor or its slots -- is caught by the commit-time
// Checker built by registerChecker (theorystate.md section 86), which
// calls this same function for every affected anchor.
func (d *domainConstraint) checkAllowed(graph GraphReader, anchor, target NodeID) error {
	domain, hasDomain, err := d.Domain(graph, anchor)
	if err != nil {
		return err
	}
	if !hasDomain {
		return nil
	}

	return d.validateMembership(graph, domain, target)
}

// anchorTargetFunc reports the current target of a domain-constrained
// pointer's anchor: P for Representation B (DomainPointerRegistryB.Target),
// the metadata node M for Representation D
// (PointerMetadataRegistryD.targetOfMetadata). It is the only thing that
// differs between the two representations' commit-time Checkers.
type anchorTargetFunc func(g GraphReader, anchor NodeID) (target NodeID, hasTarget bool, err error)

// registerChecker registers the commit-time Checker enforcing that no
// domain-constrained pointer is left with a target outside its domain
// (theorystate.md section 86). It is shared by DomainPointerRegistryB and
// DomainPointerRegistryD; they differ only in targetTag (the tag of the
// node holding the pointer's target: AllSubPointers or
// AllPointerMetadataTargetSlot) and targetOf.
//
// The Checker fires when a transaction touches a domain slot, a target
// holder, a node carrying any Set-representation tag, or an operand
// descriptor (an axis-tagged node). It finds every
// affected anchor via affectedAnchors, then for each anchor that has both
// a target and a domain, re-validates membership against live state. Any
// error from evaluating the domain (cycle, malformed descriptor, ...) is
// propagated and declines the commit; only an anchor whose target is
// genuinely outside its domain yields ErrTargetOutsideDomain.
//
// The Tags list includes AllCompositeSetLogs only if logs was supplied;
// as everywhere else, logs must be the same registry wired into
// composites via CompositeSetRegistry.SetLogs, and must exist before
// this registry is constructed.
func (d *domainConstraint) registerChecker(graph GraphAPI, name string, targetTag NodeID, targetOf anchorTargetFunc) {
	tags := append([]NodeID{d.domainSlots.allPointers, targetTag, d.sets.allSets, d.composites.allCompositeSets}, d.composites.operandAxisTags()...)
	if d.logs != nil {
		tags = append(tags, d.logs.allCompositeSetLogs)
	}

	graph.RegisterChecker(Checker{
		Name: name,
		Tags: tags,
		Check: func(g GraphReader, touched map[NodeID]struct{}) error {
			anchors, err := d.affectedAnchors(g, targetTag, touched)
			if err != nil {
				return err
			}

			for _, anchor := range anchors {
				target, hasTarget, targetErr := targetOf(g, anchor)
				if targetErr != nil {
					return targetErr
				}
				if !hasTarget {
					continue
				}

				if allowedErr := d.checkAllowed(g, anchor, target); allowedErr != nil {
					return allowedErr
				}
			}

			return nil
		},
	})
}

// affectedAnchors returns, sorted, every anchor whose domain-pointer
// validity a transaction touching touched could have changed. An anchor
// is affected when:
//   - a touched node is a domain slot or a target holder (targetTag):
//     its owners are affected; or
//   - a touched node is Set-kind (any of the three representations):
//     that node, and every composite/log that transitively expands it,
//     may be some pointer's domain, so the owners of every domain slot
//     referencing any of them are affected; or
//   - a touched node is an operand descriptor (carries an axis tag): its
//     composite(s)/log(s) changed definition (for example its operand was
//     re-pointed), so they are treated exactly like a touched Set-kind
//     node. The walk up uses the descriptor's intact incoming edge, not
//     the edge that may just have been removed, so no diff is needed.
//
// Candidates are collected via reverse lookups only (Graph.incoming is
// the reverse index -- nothing is stored or kept in sync; see
// theorystate.md section 86). They are candidates, not verified anchors:
// the caller's targetOf and Domain lookups reject non-anchors by finding
// no target or no domain slot. Deleted touched nodes are skipped.
func (d *domainConstraint) affectedAnchors(g GraphReader, targetTag NodeID, touched map[NodeID]struct{}) ([]NodeID, error) {
	anchors := make(map[NodeID]struct{})

	for node := range touched {
		exists, err := g.NodeExists(node)
		if err != nil {
			return nil, wrapInterfaceErr(err)
		}
		if !exists {
			continue
		}

		isDomainSlot, err := d.domainSlots.IsPointer(g, node)
		if err != nil {
			return nil, err
		}

		isTargetHolder, err := g.HasRelationship(targetTag, node)
		if err != nil {
			return nil, wrapInterfaceErr(err)
		}

		if isDomainSlot || isTargetHolder {
			if slotErr := d.addSlotOwners(g, node, targetTag, anchors); slotErr != nil {
				return nil, slotErr
			}
		}

		known, err := operandCarriesKnownSetTag(g, d.sets, d.composites, d.logs, node)
		if err != nil {
			return nil, err
		}
		if known {
			if expandErr := d.addAnchorsExpanding(g, targetTag, node, anchors); expandErr != nil {
				return nil, expandErr
			}
		}

		isDescriptor, err := d.isOperandDescriptor(g, node)
		if err != nil {
			return nil, err
		}
		if !isDescriptor {
			continue
		}

		owners, ownersErr := d.descriptorOwners(g, node)
		if ownersErr != nil {
			return nil, ownersErr
		}

		for _, owner := range owners {
			if expandErr := d.addAnchorsExpanding(g, targetTag, owner, anchors); expandErr != nil {
				return nil, expandErr
			}
		}
	}

	return sortedNodeSet(anchors), nil
}

// addAnchorsExpanding adds to anchors the owners of every domain slot
// referencing node or any composite/log that transitively expands it:
// every pointer whose domain could have changed membership because node's
// own definition or membership changed.
func (d *domainConstraint) addAnchorsExpanding(g GraphReader, targetTag, node NodeID, anchors map[NodeID]struct{}) error {
	containers, err := d.transitiveSetContainers(g, node)
	if err != nil {
		return err
	}

	for _, candidate := range append([]NodeID{node}, containers...) {
		slots, slotsErr := d.domainSlotsOf(g, candidate)
		if slotsErr != nil {
			return slotsErr
		}

		for _, slot := range slots {
			if ownersErr := d.addSlotOwners(g, slot, targetTag, anchors); ownersErr != nil {
				return ownersErr
			}
		}
	}

	return nil
}

// isOperandDescriptor reports whether node carries any operand-descriptor
// axis tag (theorystate.md section 80).
func (d *domainConstraint) isOperandDescriptor(g GraphReader, node NodeID) (bool, error) {
	for _, tag := range d.composites.operandAxisTags() {
		has, err := g.HasRelationship(tag, node)
		if err != nil {
			return false, wrapInterfaceErr(err)
		}
		if has {
			return true, nil
		}
	}

	return false, nil
}

// operandAxisTags returns the four operand-descriptor axis tags
// (theorystate.md section 80).
func (c *CompositeSetRegistry) operandAxisTags() []NodeID {
	return []NodeID{c.allAdditiveOp, c.allSubtractiveOp, c.allScalarOperand, c.allSetOperand}
}

// addSlotOwners adds every candidate owner (parent) of slot to anchors.
//
// Two kinds of parent are skipped. The tag hubs (AllDomainSlot,
// targetTag) would make the later forward lookup O(every slot). And any
// parent that itself has the AllDomainSlot hub as a child is a universal
// parent -- ROOT under a RootGraph, which is a virtual parent of every
// node (theorystate.md section 12a) -- never a real anchor; treating it
// as one would make its forward lookups see every slot in the graph as
// its own and misreport ambiguity or pair unrelated pointers' targets and
// domains.
func (d *domainConstraint) addSlotOwners(g GraphReader, slot, targetTag NodeID, anchors map[NodeID]struct{}) error {
	incoming, err := g.FindIncoming(slot)
	if err != nil {
		return wrapInterfaceErr(err)
	}

	for _, rel := range incoming {
		owner := rel.From
		if owner == d.domainSlots.allPointers || owner == targetTag {
			continue
		}

		isUniversalParent, hasErr := g.HasRelationship(owner, d.domainSlots.allPointers)
		if hasErr != nil {
			return wrapInterfaceErr(hasErr)
		}
		if isUniversalParent {
			continue
		}

		anchors[owner] = struct{}{}
	}

	return nil
}

// domainSlotsOf returns every domain slot whose target is domain: a
// reverse lookup over FindIncoming(domain), filtered by the AllDomainSlot
// tag.
func (d *domainConstraint) domainSlotsOf(g GraphReader, domain NodeID) ([]NodeID, error) {
	incoming, err := g.FindIncoming(domain)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}

	slots := make([]NodeID, 0, len(incoming))
	for _, rel := range incoming {
		isSlot, isSlotErr := d.domainSlots.IsPointer(g, rel.From)
		if isSlotErr != nil {
			return nil, isSlotErr
		}
		if isSlot {
			slots = append(slots, rel.From)
		}
	}

	return slots, nil
}

// transitiveSetContainers returns every composite/log that expands node
// as a set operand, directly or through any chain of such composites/logs
// (excluding node itself). It is a breadth-first walk over
// setOperandContainers with a visited set, so shared sub-expressions
// (diamonds) are visited once and cycles -- legal in a corrupted graph --
// terminate.
func (d *domainConstraint) transitiveSetContainers(g GraphReader, node NodeID) ([]NodeID, error) {
	visited := map[NodeID]struct{}{node: {}}
	queue := []NodeID{node}
	var containers []NodeID

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		direct, err := d.setOperandContainers(g, current)
		if err != nil {
			return nil, err
		}

		for _, container := range direct {
			if _, seen := visited[container]; seen {
				continue
			}

			visited[container] = struct{}{}
			containers = append(containers, container)
			queue = append(queue, container)
		}
	}

	return containers, nil
}

// setOperandContainers returns every composite/log that has node as a
// set-expansion (AllSetOperand) operand, one level up. A descriptor using
// node as a scalar operand is deliberately ignored: a scalar operand
// contributes node itself, not node's membership, so a change to node's
// members cannot change the container's membership.
func (d *domainConstraint) setOperandContainers(g GraphReader, node NodeID) ([]NodeID, error) {
	incoming, err := g.FindIncoming(node)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}

	containers := make([]NodeID, 0, len(incoming))
	for _, rel := range incoming {
		isSetOperand, hasErr := g.HasRelationship(d.composites.allSetOperand, rel.From)
		if hasErr != nil {
			return nil, wrapInterfaceErr(hasErr)
		}
		if !isSetOperand {
			continue
		}

		owners, ownersErr := d.descriptorOwners(g, rel.From)
		if ownersErr != nil {
			return nil, ownersErr
		}

		containers = append(containers, owners...)
	}

	return containers, nil
}

// descriptorOwners returns the composite(s) having descriptor u as a
// direct child, plus (if a CompositeSetLogRegistry was supplied) the
// log(s) having a capsule whose value is u -- the reverse of the two ways
// an operand descriptor is attached (theorystate.md section 80/82). The
// log hop reuses CapsuleRegistry.CapsulesWithValue rather than any new
// index.
func (d *domainConstraint) descriptorOwners(g GraphReader, u NodeID) ([]NodeID, error) {
	incoming, err := g.FindIncoming(u)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}

	owners := make([]NodeID, 0, len(incoming))
	for _, rel := range incoming {
		isComposite, isCompositeErr := d.composites.IsCompositeSet(g, rel.From)
		if isCompositeErr != nil {
			return nil, isCompositeErr
		}
		if isComposite {
			owners = append(owners, rel.From)
		}
	}

	if d.logs == nil {
		return owners, nil
	}

	capsules, err := d.logs.lists.capsules.CapsulesWithValue(g, u)
	if err != nil {
		return nil, err
	}

	for _, capsule := range capsules {
		capsuleIncoming, capsuleErr := g.FindIncoming(capsule)
		if capsuleErr != nil {
			return nil, wrapInterfaceErr(capsuleErr)
		}

		for _, rel := range capsuleIncoming {
			isLog, isLogErr := d.logs.IsCompositeSetLog(g, rel.From)
			if isLogErr != nil {
				return nil, isLogErr
			}
			if isLog {
				owners = append(owners, rel.From)
			}
		}
	}

	return owners, nil
}

// DomainPointerRegistryB adds domain-constrained target enforcement on
// top of an existing Representation B PointerRegistry instance
// (theorystate.md section 10b), attaching the domain slot directly to
// the anchor node P:
//
//	P -> U             (allPointers' own tag, U)   -- existing target slot
//	P -> U3            (AllDomainSlot, U3)         -- new domain slot
//	U3 -> domainNode
//
// pointers must be a genuine Representation B instance -- i.e.
// constructed with an intermediary-node tag such as AllSubPointers,
// never AllPointers itself (Representation A). This type cannot detect
// that distinction from the tag alone, since PointerRegistry is
// deliberately representation-agnostic (theorystate.md section 76);
// wrapping a Representation A instance here is a caller error this type
// has no way to reject, and would silently corrupt that pointer's own
// "at most one target" invariant the first time a domain slot was
// attached, per theorystate.md section 10c's explanation of why
// Representation A cannot safely carry one.
//
// U itself, not P, is where PointerRegistry's own target-cardinality
// invariant is enforced, so P is free to carry the additional domain
// slot without disturbing it -- U is discovered generically from P via
// the underlying PointerRegistry's own tag (see the subPointer method),
// the same tag-based child lookup used throughout this file, rather than
// requiring callers to separately track and pass U alongside P.
//
// Domain-membership is enforced both at write time (SetTarget/SetDomain
// below) and at commit time, by the same shared Checker Representation D
// uses (domainConstraint.registerChecker). B's anchor P carries no
// self-identifying tag -- P may be any caller-managed node -- so the
// Checker does not try to reverse-discover P by an untagged single-parent
// lookup. It enumerates the parents of a touched sub-pointer or
// domain-slot node as candidates and lets the forward, tag-based
// lookups (subPointer, domainSlotFor) accept or reject each: a parent
// that is not an anchor simply has no such child and is skipped, exactly
// as findUniqueTaggedParent tolerates unrelated parents elsewhere in this
// file. No new tag is needed. The Checker also catches a domain node's
// own membership changing later (theorystate.md section 86). Raw,
// non-Transact Graph mutations still bypass every Checker, as documented
// on the Checker type.
type DomainPointerRegistryB struct {
	domainConstraint
	pointers *PointerRegistry
}

// NewDomainPointerRegistryB creates a DomainPointerRegistryB over graph,
// domain-constraining targets set through pointers (a pre-constructed
// Representation B PointerRegistry -- see the type's doc comment for why
// Representation A must never be passed here). domainSlots is a
// pre-constructed PointerRegistry for the shared AllDomainSlot tag (see
// domainConstraint's doc comment for why it should be constructed once
// and shared with any DomainPointerRegistryD in the same graph). logs
// may be nil if no CompositeSetLogRegistry exists yet in the calling
// program (see operandCarriesKnownSetTag's identical nil-tolerance) --
// a domain pointed at a CompositeSetLog-kind node is then rejected via
// ErrInvalidSetOperand exactly like any other unrecognized domain kind,
// until logs is available.
//
// This also registers the shared commit-time domain Checker (see
// domainConstraint.registerChecker), keyed on pointers' own tag.
func NewDomainPointerRegistryB(graph GraphAPI, pointers, domainSlots *PointerRegistry, sets *SetRegistry, composites *CompositeSetRegistry, logs *CompositeSetLogRegistry) *DomainPointerRegistryB {
	b := &DomainPointerRegistryB{
		domainConstraint: domainConstraint{
			domainSlots: domainSlots,
			sets:        sets,
			composites:  composites,
			logs:        logs,
		},
		pointers: pointers,
	}

	b.registerChecker(
		graph,
		fmt.Sprintf("DomainPointerRegistryB(tag=%d)", domainSlots.allPointers),
		pointers.allPointers,
		b.Target,
	)

	return b
}

// subPointer returns anchor's Representation B sub-pointer node U -- the
// single child of anchor tagged via the underlying PointerRegistry's own
// tag -- found by tag, not by position, exactly like every other slot
// lookup in this file.
func (b *DomainPointerRegistryB) subPointer(graph GraphReader, anchor NodeID) (u NodeID, found bool, err error) {
	return findUniqueTaggedChild(graph, anchor, b.pointers.allPointers)
}

// NewDomainPointer mints a fresh sub-pointer node U, tags it via the
// underlying PointerRegistry's own tag, and wires (anchor, U) --
// completing Representation B's structural pattern for
// DomainPointerRegistryB's own use, so callers do not need to separately
// call the underlying PointerRegistry.NewPointer and wire the edge
// themselves. anchor must already exist; it need not be otherwise
// tagged in any particular way, consistent with Representation B leaving
// P's other direct children unconstrained (theorystate.md section 10b).
//
// Calling this more than once for the same anchor is an idempotent
// no-op: if anchor already has a discoverable sub-pointer node (see
// subPointer), NewDomainPointer leaves it untouched and returns nil
// rather than minting a second one. This closes a real gap found on
// review, not merely a hypothetical one -- without this check, a second
// call gave anchor two children both tagged via the underlying
// PointerRegistry's own tag, which made every subsequent subPointer
// lookup (and therefore Target/SetTarget/RemoveTarget) fail with
// ErrAmbiguousPointerMetadata from then on. This matches the
// idempotency discipline already followed by
// PointerRegistry.TagAsPointer and NameRegistry.EnsureNamedNode
// elsewhere in this file.
//
// The existence check and the creation run inside one Graph.Transact
// call, so concurrent callers under GraphActor cannot both conclude "no
// sub-pointer yet" and each mint one.
func (b *DomainPointerRegistryB) NewDomainPointer(graph Transactor, anchor NodeID) error {
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		exists, err := tx.NodeExists(anchor)
		if err != nil {
			return wrapInterfaceErr(err)
		}
		if !exists {
			return ErrNodeNotFound
		}

		_, found, err := b.subPointer(tx, anchor)
		if err != nil {
			return err
		}
		if found {
			return nil
		}

		u, err := newPointerTx(tx, b.pointers.allPointers)
		if err != nil {
			return err
		}

		return addRelationshipTx(tx, anchor, u)
	}))
}

// Target returns anchor's current target via its sub-pointer node U, if
// any. hasTarget is false both when anchor has no discoverable U at all
// and when U exists but has no target set yet.
func (b *DomainPointerRegistryB) Target(graph GraphReader, anchor NodeID) (target NodeID, hasTarget bool, err error) {
	u, found, err := b.subPointer(graph, anchor)
	if err != nil || !found {
		return 0, false, err
	}

	return b.pointers.Target(graph, u)
}

// SetTarget sets anchor's target to target, first validating target
// against anchor's currently attached domain, if any (see
// domainConstraint.checkAllowed). anchor must already have a
// discoverable sub-pointer node U (see NewDomainPointer); otherwise this
// returns ErrNotPointer, mirroring the underlying PointerRegistry's own
// error for an untagged node.
func (b *DomainPointerRegistryB) SetTarget(graph Transactor, anchor, target NodeID) error {
	// The domain check and the write see the same state, so a domain
	// change cannot slip in between.
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		exists, err := tx.NodeExists(target)
		if err != nil {
			return wrapInterfaceErr(err)
		}
		if !exists {
			return ErrNodeNotFound
		}

		if allowedErr := b.checkAllowed(tx, anchor, target); allowedErr != nil {
			return allowedErr
		}

		u, found, err := b.subPointer(tx, anchor)
		if err != nil {
			return err
		}
		if !found {
			return ErrNotPointer
		}

		return b.pointers.SetTarget(tx, u, target)
	}))
}

// RemoveTarget clears anchor's target, if any, via its sub-pointer node
// U.
func (b *DomainPointerRegistryB) RemoveTarget(graph Transactor, anchor NodeID) (removed bool, err error) {
	return transactBool(graph, func(tx Tx) (bool, error) {
		u, found, subErr := b.subPointer(tx, anchor)
		if subErr != nil || !found {
			return false, subErr
		}

		return b.pointers.RemoveTarget(tx, u)
	})
}

// SetDomain sets anchor's domain to domain, additionally validating that
// anchor's current target (if any) still belongs to domain before
// committing -- symmetric with SetTarget's own validation against the
// current domain. See domainConstraint.SetDomain for the shared
// creation/validation logic this delegates to.
func (b *DomainPointerRegistryB) SetDomain(graph Transactor, anchor, domain NodeID) error {
	// Reading the current target, validating it against the new domain
	// and writing the domain are one atomic step.
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		target, hasTarget, err := b.Target(tx, anchor)
		if err != nil {
			return err
		}

		if hasTarget {
			if memberErr := b.validateMembership(tx, domain, target); memberErr != nil {
				return memberErr
			}
		}

		return b.attachDomain(tx, anchor, domain)
	}))
}

// DomainPointerRegistryD adds domain-constrained target enforcement on
// top of an existing PointerMetadataRegistryD instance (Representation
// D, theorystate.md section 10a), attaching the domain slot to the
// metadata node M as a third, independently-tagged sibling of the
// subject-slot and target-slot:
//
//	M -> U1            (AllPointerMetadataSubjectSlot, U1) -> subject
//	M -> U2            (AllPointerMetadataTargetSlot, U2)  -> target
//	M -> U3            (AllDomainSlot, U3)                 -> domainNode
//
// This is safe for exactly the reason theorystate.md section 10c gives
// for Representation D generally: M's subject and target are both
// discovered entirely by tag, with no exclusion list at all, so M
// remains free to carry any number of additional tagged children --
// including U3 -- without disturbing either discovery.
//
// Like DomainPointerRegistryB, this type registers the shared
// commit-time domain Checker (see domainConstraint.registerChecker) in
// addition to write-time enforcement in SetTarget/SetDomain below. Here
// the anchors are metadata nodes M, and a target is read via
// PointerMetadataRegistryD.targetOfMetadata. The Checker also catches a
// domain node's own membership changing later (theorystate.md
// section 86).
type DomainPointerRegistryD struct {
	domainConstraint
	metadata *PointerMetadataRegistryD
}

// NewDomainPointerRegistryD creates a DomainPointerRegistryD over graph,
// domain-constraining targets set through metadata (a pre-constructed
// PointerMetadataRegistryD). domainSlots, sets, composites, and logs are
// exactly as for NewDomainPointerRegistryB -- domainSlots in particular
// should be the same shared instance passed there, if both exist in the
// same graph.
//
// This additionally registers the shared commit-time domain Checker (see
// domainConstraint.registerChecker), keyed on metadata's own target-slot
// tag. It catches a caller bypassing this type and mutating the
// underlying PointerMetadataRegistryD or the shared domainSlots registry
// directly, and a domain node's own membership changing later
// (theorystate.md section 86).
func NewDomainPointerRegistryD(graph GraphAPI, metadata *PointerMetadataRegistryD, domainSlots *PointerRegistry, sets *SetRegistry, composites *CompositeSetRegistry, logs *CompositeSetLogRegistry) *DomainPointerRegistryD {
	d := &DomainPointerRegistryD{
		domainConstraint: domainConstraint{
			domainSlots: domainSlots,
			sets:        sets,
			composites:  composites,
			logs:        logs,
		},
		metadata: metadata,
	}

	d.registerChecker(
		graph,
		fmt.Sprintf("DomainPointerRegistryD(tag=%d)", domainSlots.allPointers),
		metadata.allTargetSlots,
		metadata.targetOfMetadata,
	)

	return d
}

// Target returns subject's current target, delegating directly to the
// underlying PointerMetadataRegistryD.
func (d *DomainPointerRegistryD) Target(graph GraphReader, subject NodeID) (target NodeID, hasTarget bool, err error) {
	return d.metadata.Target(graph, subject)
}

// SetTarget sets subject's target to target, first validating target
// against subject's currently attached domain, if any (see
// domainConstraint.checkAllowed), before delegating to the underlying
// PointerMetadataRegistryD.SetTarget.
//
// A subject with no metadata node at all yet cannot possibly have a
// domain attached, so this skips the domain check entirely in that case
// rather than forcing metadata into existence merely to discover there
// is nothing to check -- exactly the same "read-only, don't create"
// discipline PointerMetadataRegistryD.Target itself already follows.
func (d *DomainPointerRegistryD) SetTarget(graph Transactor, subject, target NodeID) error {
	// The domain check and the write see the same state.
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		exists, err := tx.NodeExists(target)
		if err != nil {
			return wrapInterfaceErr(err)
		}
		if !exists {
			return ErrNodeNotFound
		}

		m, _, found, err := d.metadata.locate(tx, subject)
		if err != nil {
			return err
		}
		if found {
			if allowedErr := d.checkAllowed(tx, m, target); allowedErr != nil {
				return allowedErr
			}
		}

		return d.metadata.SetTarget(tx, subject, target)
	}))
}

// RemoveTarget clears subject's target, if any, delegating directly to
// the underlying PointerMetadataRegistryD.
func (d *DomainPointerRegistryD) RemoveTarget(graph Transactor, subject NodeID) (removed bool, err error) {
	return d.metadata.RemoveTarget(graph, subject)
}

// Domain returns subject's current domain node, if any, resolving
// subject's metadata node M first via the underlying
// PointerMetadataRegistryD's own subject-side lookup. hasDomain is false
// if subject has no metadata node at all yet, in addition to
// domainConstraint.Domain's own "no domain slot" and "no domain set"
// cases.
func (d *DomainPointerRegistryD) Domain(graph GraphReader, subject NodeID) (domain NodeID, hasDomain bool, err error) {
	m, _, found, err := d.metadata.locate(graph, subject)
	if err != nil || !found {
		return 0, false, err
	}

	return d.domainConstraint.Domain(graph, m)
}

// SetDomain sets subject's domain to domain, creating subject's metadata
// node first if it does not exist yet, and additionally validating that
// subject's current target (if any) still belongs to domain before
// committing -- symmetric with SetTarget's own validation against the
// current domain.
func (d *DomainPointerRegistryD) SetDomain(graph Transactor, subject, domain NodeID) error {
	// Creating subject's metadata, validating its current target and
	// writing the domain are one atomic step, so a rejected SetDomain
	// leaves no metadata behind.
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		m, _, err := d.metadata.ensureMetadataTx(tx, subject)
		if err != nil {
			return err
		}

		target, hasTarget, err := d.metadata.targetOfMetadata(tx, m)
		if err != nil {
			return err
		}

		if hasTarget {
			if memberErr := d.validateMembership(tx, domain, target); memberErr != nil {
				return memberErr
			}
		}

		return d.attachDomain(tx, m, domain)
	}))
}

// RemoveDomain clears subject's domain, if any, resolving subject's
// metadata node M first. removed is false if subject has no metadata
// node at all yet, in addition to domainConstraint.RemoveDomain's own
// "no domain slot" case.
func (d *DomainPointerRegistryD) RemoveDomain(graph Transactor, subject NodeID) (removed bool, err error) {
	return transactBool(graph, func(tx Tx) (bool, error) {
		m, _, found, locateErr := d.metadata.locate(tx, subject)
		if locateErr != nil || !found {
			return false, locateErr
		}

		return d.domainConstraint.RemoveDomain(tx, m)
	})
}

// LeaseRegistry implements sessions and holds (theorystate.md section
// 111): reference counting that knows WHO holds a resource, so a holder
// that dies can be found and released, which a bare counter cannot do.
//
// A session is an ordinary node tagged (AllSessions, session): the
// liveness identity of one client of the graph (a goroutine, a process,
// eventually a remote machine). A resource is a plain Set (SetRegistry)
// used as a holder set: a session holds the resource exactly when it is a
// member. Nothing new is stored:
//   - membership is a unique (resource, session) pair (theorystate.md
//     section 2.6), so acquiring twice cannot double count;
//   - "what does this session hold" is FindIncoming(session) filtered to
//     Set-kind parents, a reverse lookup with no index to keep in sync;
//   - the resource is wanted exactly when its holder set is non-empty.
//
// Acquire and Release report first/last: whether this call made the
// holder set non-empty or empty. These are hints about a transition, and
// an effect outside the graph (say, adding a rule in another system) must NOT be driven
// only from them: a crash between commit and effect would lose the effect
// forever. Drive effects from the level instead (Held), with a reconciler
// that compares desired and actual state and repeats. Likewise "first ==
// false" means the resource is wanted, not that its effect is already in
// place; a client that needs the effect must wait for the reconciler.
//
// Sessions are ephemeral. A client's death is detected by whoever owns the
// connection (the host) and answered with CloseSession; the host's own
// death is answered at startup with CloseAllSessions, since every client
// connection died with it.
//
// A session holds a given resource at most once (Sets are idempotent).
// Several independent holds by one session would need the occurrence-
// descriptor pattern of theorystate.md section 75.
//
// Every mutator takes a Transactor, so it works standalone and composes
// inside a larger transaction; inside one, first/last/freed are
// provisional until the outermost commit (see Transactor). Like every
// registry here, LeaseRegistry stores no graph reference (section 90).
type LeaseRegistry struct {
	sets        *SetRegistry
	allSessions NodeID
}

// NewLeaseRegistry creates a LeaseRegistry over graph. sets holds and
// resolves the holder sets; allSessions tags session nodes and must
// already exist (NameAllSessions via NameRegistry.BootstrapNames).
func NewLeaseRegistry(graph GraphAPI, sets *SetRegistry, allSessions NodeID) (*LeaseRegistry, error) {
	exists, err := graph.NodeExists(allSessions)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}
	if !exists {
		return nil, ErrNodeNotFound
	}

	return &LeaseRegistry{
		sets:        sets,
		allSessions: allSessions,
	}, nil
}

// IsSession reports whether id is currently tagged (AllSessions, id).
func (l *LeaseRegistry) IsSession(graph GraphReader, id NodeID) (bool, error) {
	has, err := graph.HasRelationship(l.allSessions, id)
	return has, wrapInterfaceErr(err)
}

// requireSession checks that session exists and is tagged
// (AllSessions, session), returning ErrNodeNotFound or ErrNotSession
// otherwise.
func (l *LeaseRegistry) requireSession(graph GraphReader, session NodeID) error {
	return requireTagged(graph, session, l.allSessions, ErrNotSession)
}

// NewSession creates a fresh node and tags it (AllSessions, id).
func (l *LeaseRegistry) NewSession(graph Transactor) (NodeID, error) {
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		return createTaggedNodeTx(tx, l.allSessions)
	})
}

// Sessions returns every currently open session, sorted ascending. The
// number of live sessions is small by nature (it is bounded by the number
// of connected clients), so this reads the tag's children in full.
func (l *LeaseRegistry) Sessions(graph GraphReader) ([]NodeID, error) {
	outgoing, err := graph.FindOutgoing(l.allSessions)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}

	sessions := make([]NodeID, 0, len(outgoing))
	for _, rel := range outgoing {
		sessions = append(sessions, rel.To)
	}

	return sessions, nil
}

// isEmpty reports whether resource's holder set has no members. resource
// must be a Set (ErrNotSet / ErrNodeNotFound otherwise).
func (l *LeaseRegistry) isEmpty(graph GraphReader, resource NodeID) (bool, error) {
	size, err := l.sets.Size(graph, resource)
	return size == 0, err
}

// Held reports whether resource currently has at least one holder: the
// level-triggered "desired state" a reconciler should compare against the
// outside world.
func (l *LeaseRegistry) Held(graph GraphReader, resource NodeID) (bool, error) {
	empty, err := l.isEmpty(graph, resource)
	if err != nil {
		return false, err
	}

	return !empty, nil
}

// Holders returns resource's current holders (sessions), sorted
// ascending.
func (l *LeaseRegistry) Holders(graph GraphReader, resource NodeID) ([]NodeID, error) {
	return l.sets.Members(graph, resource)
}

// Acquire makes session a holder of resource. first reports whether this
// call made the holder set non-empty (resource was unheld and session was
// not already a holder); acquiring again is an idempotent no-op reporting
// false. session must be an open session and resource a Set.
//
// first is a transition hint, not "the effect is in place": see the
// LeaseRegistry doc comment.
func (l *LeaseRegistry) Acquire(graph Transactor, resource, session NodeID) (first bool, err error) {
	return transactBool(graph, func(tx Tx) (bool, error) {
		if requireErr := l.requireSession(tx, session); requireErr != nil {
			return false, requireErr
		}

		wasEmpty, emptyErr := l.isEmpty(tx, resource)
		if emptyErr != nil {
			return false, emptyErr
		}

		added, addErr := l.sets.Add(tx, resource, session)
		if addErr != nil {
			return false, addErr
		}

		return added && wasEmpty, nil
	})
}

// Release removes session from resource's holders. last reports whether
// this call made the holder set empty (session was a holder and nobody
// else is). Releasing a hold that was never taken, or was already
// released, is a no-op reporting false. session must be an open session
// and resource a Set.
func (l *LeaseRegistry) Release(graph Transactor, resource, session NodeID) (last bool, err error) {
	return transactBool(graph, func(tx Tx) (bool, error) {
		if requireErr := l.requireSession(tx, session); requireErr != nil {
			return false, requireErr
		}

		removed, removeErr := l.sets.Remove(tx, resource, session)
		if removeErr != nil {
			return false, removeErr
		}
		if !removed {
			return false, nil
		}

		return l.isEmpty(tx, resource)
	})
}

// heldBy returns every resource session currently holds: the Set-kind
// parents of session, sorted ascending. Other parents (the AllSessions tag,
// the virtual ROOT parent under a RootGraph, anything unrelated) are not
// Sets and are skipped.
func (l *LeaseRegistry) heldBy(graph GraphReader, session NodeID) ([]NodeID, error) {
	incoming, err := graph.FindIncoming(session)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}

	resources := make([]NodeID, 0, len(incoming))

	for _, rel := range incoming {
		isSet, setErr := l.sets.IsSet(graph, rel.From)
		if setErr != nil {
			return nil, setErr
		}
		if isSet {
			resources = append(resources, rel.From)
		}
	}

	return resources, nil
}

// CloseSession ends session: every hold it has is released and the
// session node is deleted, all in one transaction. freed lists the
// resources whose holder set this made empty, in ascending resource
// order, which is exactly what a host must hand to its reconciler when a
// client dies.
//
// Deletion follows the usual "delete only if empty" rule (theorystate.md
// section 18): if something other than holder sets still references the
// session, CloseSession fails with ErrNodeNotEmpty and nothing at all is
// changed, the holds included.
func (l *LeaseRegistry) CloseSession(graph Transactor, session NodeID) (freed []NodeID, err error) {
	return transactValue(graph, func(tx Tx) ([]NodeID, error) {
		if requireErr := l.requireSession(tx, session); requireErr != nil {
			return nil, requireErr
		}

		resources, heldErr := l.heldBy(tx, session)
		if heldErr != nil {
			return nil, heldErr
		}

		emptied := make([]NodeID, 0, len(resources))

		for _, resource := range resources {
			removed, removeErr := l.sets.Remove(tx, resource, session)
			if removeErr != nil {
				return nil, removeErr
			}
			if !removed {
				continue
			}

			empty, emptyErr := l.isEmpty(tx, resource)
			if emptyErr != nil {
				return nil, emptyErr
			}
			if empty {
				emptied = append(emptied, resource)
			}
		}

		if deleteErr := untagAndDeleteNodeTx(tx, session, l.allSessions); deleteErr != nil {
			return nil, deleteErr
		}

		return emptied, nil
	})
}

// CloseAllSessions closes every open session in one transaction and
// returns the resources that lost their last holder (in closing order,
// each once). A host calls it at startup: sessions are ephemeral, and
// every client connection died with the previous process. It is
// fail-closed: if any session cannot be closed, none is.
func (l *LeaseRegistry) CloseAllSessions(graph Transactor) (freed []NodeID, err error) {
	return transactValue(graph, func(tx Tx) ([]NodeID, error) {
		sessions, sessionsErr := l.Sessions(tx)
		if sessionsErr != nil {
			return nil, sessionsErr
		}

		emptied := make([]NodeID, 0)

		for _, session := range sessions {
			closed, closeErr := l.CloseSession(tx, session)
			if closeErr != nil {
				return nil, closeErr
			}

			emptied = append(emptied, closed...)
		}

		return emptied, nil
	})
}

// ---------------------------------------------------------------------
// Host: the single owner process of one graph (theorystate.md sections 107
// and 112). It owns the store, the GraphActor, every registry and every
// Checker; clients (goroutines now, other processes and machines later)
// connect to it and call named operations, each run as one Transact.

var (
	// ErrHostClosed is returned for any operation attempted on, or still
	// running when, the Host is closed.
	ErrHostClosed = errors.New("host is closed")

	// ErrConnClosed is returned for any operation on a connection that has
	// been closed, dropped by its context, or expired by the keepalive TTL.
	ErrConnClosed = errors.New("connection is closed")

	// ErrUnknownResource is returned for a resource name that was never
	// registered with Host.RegisterResource.
	ErrUnknownResource = errors.New("resource is not registered")

	// ErrResourceRegistered is returned when a resource name is registered
	// twice.
	ErrResourceRegistered = errors.New("resource is already registered")

	// ErrResourceName is returned for an empty resource name.
	ErrResourceName = errors.New("resource name must not be empty")

	// ErrHoldLost is returned by WaitApplied when the session no longer
	// holds the resource it is waiting for.
	ErrHoldLost = errors.New("the session no longer holds the resource")

	// ErrEffectNotConverged is returned (inside the reconciler's error
	// report) when an effect's Apply/Remove succeeded but Present still
	// disagrees.
	ErrEffectNotConverged = errors.New("effect did not reach the wanted state")

	// ErrEffectTimeout is returned (inside the reconciler's error report)
	// when a call to an Effect did not return within
	// HostConfig.EffectTimeout and was abandoned. The call may still be
	// running; see ErrEffectBusy.
	ErrEffectTimeout = errors.New("effect call timed out")

	// ErrEffectBusy is returned (inside the reconciler's error report) when
	// a call to an Effect was not started because an earlier, abandoned
	// call to the same effect has not returned yet. It keeps the effect
	// from ever seeing two overlapping calls.
	ErrEffectBusy = errors.New("an earlier call to the effect has not returned")
)

// resourcePrefix namespaces resource names inside the NameRegistry, so they
// can never collide with a foundational name.
const resourcePrefix = "resource/"

// Effect is something outside the graph that exists exactly while a
// resource is held (a rule in some other system, say). dml defines only
// this contract; implementations, and any vocabulary specific to what they
// manage, belong to the consumer (theorystate.md section 112). The Host's
// reconciler drives it from the level (LeaseRegistry.Held), never only from
// acquire/release transitions, so every method must be idempotent and safe
// to repeat after a crash: Apply of a present effect and Remove of an absent
// one are successes. Present reports the real state of the outside world,
// not a cached belief. Implementations must not touch the graph. Each call
// runs under HostConfig.EffectTimeout: honor ctx where possible, since a call
// that ignores it is abandoned when the deadline passes (it cannot be killed,
// and no further call to the same effect starts until it returns).
type Effect interface {
	// Present reports whether the effect is currently in place.
	Present(ctx context.Context) (bool, error)
	// Apply puts the effect in place.
	Apply(ctx context.Context) error
	// Remove takes the effect away.
	Remove(ctx context.Context) error
}

// Registries bundles every registry the Host constructs over its graph.
// Constructing them registers their commit-time Checkers, which is why they
// are built before the startup sweep (VerifyAll). Representation C
// (PointerMetadataRegistry) is deliberately absent: it exists as a
// deliberately stricter test representation (theorystate.md section 10a).
type Registries struct {
	Pointers    *PointerRegistry
	SubPointers *PointerRegistry
	Metadata    *PointerMetadataRegistryD
	Capsules    *CapsuleRegistry
	Lists       *ListRegistry
	Sets        *SetRegistry
	Composites  *CompositeSetRegistry
	Logs        *CompositeSetLogRegistry
	DomainSlots *PointerRegistry
	DomainB     *DomainPointerRegistryB
	DomainD     *DomainPointerRegistryD
	Leases      *LeaseRegistry
}

// NewRegistries constructs every registry over graph. ids must hold an
// entry for every name in FoundationalNames (as returned by
// NameRegistry.BootstrapNames); a missing one is ErrNameNotFound, never a
// silent NodeID 0.
func NewRegistries(graph GraphAPI, ids map[string]NodeID) (*Registries, error) {
	for _, name := range FoundationalNames {
		if _, ok := ids[name]; !ok {
			return nil, fmt.Errorf("%w: %q", ErrNameNotFound, name)
		}
	}

	pointers, err := NewPointerRegistry(graph, ids[NameAllPointers])
	if err != nil {
		return nil, err
	}

	subPointers, err := NewPointerRegistry(graph, ids[NameAllSubPointers])
	if err != nil {
		return nil, err
	}

	metadata, err := NewPointerMetadataRegistryD(graph, ids[NameAllPointerMetadata], ids[NameAllPointerMetadataSubjectSlot], ids[NameAllPointerMetadataTargetSlot])
	if err != nil {
		return nil, err
	}

	capsules, err := NewCapsuleRegistry(graph, ids[NameAllElementCapsules], ids[NameAllElementCapsulePrevSlot], ids[NameAllElementCapsuleValueSlot], ids[NameAllElementCapsuleNextSlot])
	if err != nil {
		return nil, err
	}

	lists, err := NewListRegistry(graph, capsules, ids[NameAllLists], ids[NameAllHeads], ids[NameAllTails])
	if err != nil {
		return nil, err
	}

	sets, err := NewSetRegistry(graph, ids[NameAllSets], ids[NameAllCompositeSets], ids[NameAllCompositeSetLogs])
	if err != nil {
		return nil, err
	}

	composites, err := NewCompositeSetRegistry(graph, sets, ids[NameAllCompositeSets], ids[NameAllAdditiveOp], ids[NameAllSubtractiveOp], ids[NameAllScalarOperand], ids[NameAllSetOperand])
	if err != nil {
		return nil, err
	}

	logs, err := NewCompositeSetLogRegistry(graph, lists, composites, ids[NameAllCompositeSetLogs], ids[NameAllAdditiveOp], ids[NameAllSubtractiveOp], ids[NameAllScalarOperand], ids[NameAllSetOperand])
	if err != nil {
		return nil, err
	}

	composites.SetLogs(logs)

	domainSlots, err := NewPointerRegistry(graph, ids[NameAllDomainSlot])
	if err != nil {
		return nil, err
	}

	leases, err := NewLeaseRegistry(graph, sets, ids[NameAllSessions])
	if err != nil {
		return nil, err
	}

	return &Registries{
		Pointers:    pointers,
		SubPointers: subPointers,
		Metadata:    metadata,
		Capsules:    capsules,
		Lists:       lists,
		Sets:        sets,
		Composites:  composites,
		Logs:        logs,
		DomainSlots: domainSlots,
		DomainB:     NewDomainPointerRegistryB(graph, subPointers, domainSlots, sets, composites, logs),
		DomainD:     NewDomainPointerRegistryD(graph, metadata, domainSlots, sets, composites, logs),
		Leases:      leases,
	}, nil
}

// Defaults for HostConfig fields left at zero.
const (
	defaultKeepaliveTTL    = 15 * time.Second
	defaultTeardownGrace   = 2 * time.Second
	defaultReapInterval    = time.Second
	defaultResyncInterval  = 30 * time.Second
	defaultRetryInterval   = 2 * time.Second
	defaultShutdownTimeout = 10 * time.Second
	defaultEffectTimeout   = 30 * time.Second
)

// HostConfig configures a Host. Only Path is required.
//
// For KeepaliveTTL, TeardownGrace and EffectTimeout, zero means "use the
// default" and a negative value means "disabled". The other durations must
// be positive;
// zero or negative means the default.
type HostConfig struct {
	// Path is the bbolt file. Exactly one Host may own it.
	Path string

	// KeepaliveTTL is how long a connection may stay silent before the Host
	// treats it as dead. Connection drop is the primary liveness signal;
	// this is the backstop for partitions where no drop is ever seen. The
	// clock lives in host memory only. Any operation counts as a sign of
	// life, as does Conn.Keepalive. A client that waits for longer than the
	// TTL inside WaitApplied must keep calling Keepalive from elsewhere.
	KeepaliveTTL time.Duration

	// TeardownGrace is how long a resource must stay unheld before its
	// effect is removed, so that release-then-acquire races do not flap the
	// effect. Applying is never delayed.
	TeardownGrace time.Duration

	// ReapInterval is how often expired connections are collected and failed
	// session closes are retried.
	ReapInterval time.Duration

	// ResyncInterval is the longest the reconciler sleeps: every resource is
	// re-compared with the outside world at least this often, which repairs
	// drift.
	ResyncInterval time.Duration

	// RetryInterval is how soon a failed effect is retried.
	RetryInterval time.Duration

	// ShutdownTimeout bounds the final reconcile pass in Close.
	ShutdownTimeout time.Duration

	// EffectTimeout bounds each Effect.Present, Apply and Remove call. A call
	// that does not return in time is abandoned and reported as
	// ErrEffectTimeout, so one hung effect cannot stall the reconciler (which
	// serves every resource) or Close. An abandoned call cannot be killed if
	// it ignores its context, so no further call to that effect starts until
	// it returns (ErrEffectBusy). Disabled (negative), calls are made
	// directly, and Close can then wait for an effect that ignores its
	// context.
	EffectTimeout time.Duration

	// VerifyPageSize is the page size of the startup VerifyAll sweep (zero
	// means VerifyAll's own default).
	VerifyPageSize int

	// OnError receives errors from background work (effects that failed,
	// sessions that could not be closed). Nil means log.Printf.
	OnError func(op string, err error)
}

// durationOrDefault implements the "zero is default, negative is disabled"
// rule: it returns the default for zero, 0 (disabled) for a negative value,
// and v otherwise.
func durationOrDefault(v, def time.Duration) time.Duration {
	switch {
	case v == 0:
		return def
	case v < 0:
		return 0
	default:
		return v
	}
}

// positiveOrDefault returns v if it is positive and def otherwise.
func positiveOrDefault(v, def time.Duration) time.Duration {
	if v <= 0 {
		return def
	}

	return v
}

func defaultHostOnError(op string, err error) {
	ilog.Printf("dml host: %s: %v", op, err)
}

// withDefaults returns c with every unset field replaced by its default.
// After this, KeepaliveTTL and TeardownGrace of 0 mean "disabled".
func (c HostConfig) withDefaults() HostConfig {
	c.KeepaliveTTL = durationOrDefault(c.KeepaliveTTL, defaultKeepaliveTTL)
	c.TeardownGrace = durationOrDefault(c.TeardownGrace, defaultTeardownGrace)
	c.EffectTimeout = durationOrDefault(c.EffectTimeout, defaultEffectTimeout)
	c.ReapInterval = positiveOrDefault(c.ReapInterval, defaultReapInterval)
	c.ResyncInterval = positiveOrDefault(c.ResyncInterval, defaultResyncInterval)
	c.RetryInterval = positiveOrDefault(c.RetryInterval, defaultRetryInterval)
	c.ShutdownTimeout = positiveOrDefault(c.ShutdownTimeout, defaultShutdownTimeout)

	if c.OnError == nil {
		c.OnError = defaultHostOnError
	}

	return c
}

// AcquireResult is what Client.Acquire returns.
type AcquireResult struct {
	// First reports whether this call made the resource's holder set
	// non-empty. It is a transition hint: it means "wanted", never "in
	// effect".
	First bool

	// Mark is the token to pass to WaitApplied. It identifies the last
	// reconcile pass of the resource that may have started before this
	// acquire committed; the effect is applied for this hold once a later
	// pass has completed with it present.
	Mark uint64
}

// Client is the named-operation surface the Host offers one connection.
// Every method is one request to the host, run there as one Transact (or a
// short read). It deliberately contains no closures and no NodeIDs, so a
// network transport can implement it by sending each call to the Host and
// the in-process Conn is a client exactly like a remote one will be.
type Client interface {
	// Acquire makes the connection's session a holder of resource.
	Acquire(ctx context.Context, resource string) (AcquireResult, error)

	// Release drops the hold; last reports whether nobody else holds it.
	Release(ctx context.Context, resource string) (last bool, err error)

	// WaitApplied blocks until the effect of resource is confirmed in place
	// for a hold taken by the Acquire that returned mark, the hold is lost
	// (ErrHoldLost), the connection dies (ErrConnClosed), the host closes
	// (ErrHostClosed), or ctx ends (the error then also carries the last
	// effect failure, if any).
	WaitApplied(ctx context.Context, resource string, mark uint64) error

	// Keepalive tells the host the client is alive (any operation does).
	Keepalive(ctx context.Context) error

	// Close ends the connection and releases everything it holds.
	Close() error

	// Done is closed when the connection has ended for any reason.
	Done() <-chan struct{}
}

// AcquireAndWait acquires resource and waits until its effect is in place.
// It is built only on the Client interface, so it works over any transport.
func AcquireAndWait(ctx context.Context, client Client, resource string) error {
	result, err := client.Acquire(ctx, resource)
	if err != nil {
		return fmt.Errorf("acquiring %q: %w", resource, err)
	}

	return wrapInterfaceErr(client.WaitApplied(ctx, resource, result.Mark))
}

// passOutcome is a snapshot of one resource's reconcile progress.
type passOutcome struct {
	completed uint64
	applied   bool
	err       error
	changed   <-chan struct{}
}

// hostResource is one registered resource: its holder Set, its optional
// effect, and the reconciler's progress on it.
type hostResource struct {
	name   string
	node   NodeID
	effect Effect

	// timeout is HostConfig.EffectTimeout (0: calls are made directly), and
	// busy is set while a call to effect is running, including one that was
	// abandoned after the timeout (see callEffect).
	timeout time.Duration
	busy    atomic.Bool

	mu        sync.Mutex
	started   uint64
	completed uint64
	applied   bool
	lastErr   error
	changed   chan struct{}

	// Touched only by the reconciler goroutine (and by Close after that
	// goroutine has exited): when the resource first became unheld while its
	// effect was still present.
	unheld      bool
	unheldSince time.Duration
}

func newHostResource(name string, node NodeID, effect Effect, timeout time.Duration) *hostResource {
	return &hostResource{name: name, node: node, effect: effect, timeout: timeout, changed: make(chan struct{})}
}

// beginPass numbers a new reconcile pass. It must be called before the
// pass reads the holder set.
func (r *hostResource) beginPass() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.started++

	return r.started
}

// mark returns the number of the latest pass that has begun. A client calls
// it after its hold has committed: every pass numbered above it begins after
// the commit and therefore sees the hold.
func (r *hostResource) mark() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.started
}

// finishPass records the outcome of pass and wakes every waiter. applied
// means: the resource was held, and its effect is confirmed present.
func (r *hostResource) finishPass(pass uint64, applied bool, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.completed = pass
	r.applied = applied
	r.lastErr = err

	close(r.changed)
	r.changed = make(chan struct{})
}

func (r *hostResource) outcome() passOutcome {
	r.mu.Lock()
	defer r.mu.Unlock()

	return passOutcome{completed: r.completed, applied: r.applied, err: r.lastErr, changed: r.changed}
}

// callEffect runs fn, one call to r.effect, under r.timeout (op names the
// call in errors). With no timeout fn is called directly.
//
// Otherwise fn runs on its own goroutine, so an effect that ignores its
// context cannot hold the caller past the deadline: the call is then
// abandoned and ErrEffectTimeout returned. The goroutine cannot be killed,
// so busy stays set until fn returns, and a new call meanwhile fails at once
// with ErrEffectBusy instead of overlapping the stuck one. If ctx (the
// host's) ends first, the call is abandoned the same way. Whatever an
// abandoned call eventually does is seen by a later pass, since Present
// reports real state.
func (r *hostResource) callEffect(ctx context.Context, op string, fn func(ctx context.Context) error) error {
	if r.timeout <= 0 {
		return fn(ctx)
	}

	if !r.busy.CompareAndSwap(false, true) {
		return fmt.Errorf("%w: %s of %q", ErrEffectBusy, op, r.name)
	}

	callCtx, cancel := context.WithTimeout(ctx, r.timeout)
	done := make(chan error, 1)

	go func() {
		callErr := fn(callCtx)

		// The result goes out before cancel: cancelling callCtx wakes the
		// select below through callCtx.Done(), and if that could happen
		// before the result is in done, a call that returned in time would
		// be reported as timed out. done is buffered, so this never blocks,
		// even when the caller has already abandoned the call.
		r.busy.Store(false)

		done <- callErr

		cancel()
	}()

	select {
	case callErr := <-done:
		return callErr
	case <-callCtx.Done():
	}

	// The call may have returned at the same moment the deadline passed.
	select {
	case callErr := <-done:
		return callErr
	default:
	}

	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%s of %q abandoned: %w", op, r.name, ctxErr)
	}

	return fmt.Errorf("%w: %s of %q did not return within %v", ErrEffectTimeout, op, r.name, r.timeout)
}

// checkPresent asks the effect whether it is in place, under the deadline.
func (r *hostResource) checkPresent(ctx context.Context) (bool, error) {
	var present bool

	err := r.callEffect(ctx, "present", func(callCtx context.Context) error {
		var presentErr error

		present, presentErr = r.effect.Present(callCtx)

		return wrapInterfaceErr(presentErr)
	})
	if err != nil {
		return false, err
	}

	return present, nil
}

// converge applies or removes the effect and verifies the result against
// the real state.
func (r *hostResource) converge(ctx context.Context, want bool) error {
	op, run := "remove", r.effect.Remove
	if want {
		op, run = "apply", r.effect.Apply
	}

	if opErr := r.callEffect(ctx, op, run); opErr != nil {
		return fmt.Errorf("changing effect of %q to present=%v: %w", r.name, want, opErr)
	}

	present, err := r.checkPresent(ctx)
	if err != nil {
		return fmt.Errorf("verifying effect of %q: %w", r.name, err)
	}

	if present != want {
		return fmt.Errorf("%w: %q, wanted present=%v", ErrEffectNotConverged, r.name, want)
	}

	return nil
}

// Host owns one graph: the bbolt store, the GraphActor over RootGraph over
// BoltGraph, every registry and every Checker (theorystate.md sections 107
// and 112). Create it with OpenHost; it must be closed exactly once with
// Close.
//
// Liveness. Every client connection owns one session node. A connection is
// dropped when its context ends (the in-process stand-in for a socket
// closing), when Close is called on it, or when it stays silent past
// KeepaliveTTL. Dropping calls LeaseRegistry.CloseSession and wakes the
// reconciler. A session whose close fails is retried by the reaper.
//
// Effects. The reconciler compares, for a registered resource, the desired
// state (LeaseRegistry.Held) with the actual one (Effect.Present) and
// converges them. A wake-up looks only at the resources a change marked
// dirty (an acquire, a release, a session that freed it, its registration)
// and at those whose own deadline has come (a teardown grace running, a
// failed effect waiting for RetryInterval). Every ResyncInterval it looks at
// every resource, which repairs drift nobody touched.
type Host struct {
	cfg   HostConfig
	store *BoltGraph
	actor *GraphActor
	names *NameRegistry
	reg   *Registries
	root  NodeID

	epoch  time.Time
	stop   chan struct{}
	cancel context.CancelFunc
	kickCh chan struct{}
	bg     sync.WaitGroup
	ops    sync.WaitGroup

	mu      sync.Mutex
	closed  bool
	conns   map[NodeID]*Conn
	closing map[NodeID]struct{}

	resMu     sync.RWMutex
	resources map[string]*hostResource
	byNode    map[NodeID]*hostResource

	// dirty is the set of resources something happened to since the
	// reconciler last took it (see markDirty).
	dirtyMu sync.Mutex
	dirty   map[*hostResource]struct{}
}

// OpenHost performs the startup order of theorystate.md sections 110 and
// 112 and returns a running Host:
//
//  1. OpenBoltGraph (single-open guard, format version check);
//  2. CheckStore (physical and layout check);
//  3. NameRegistry.LoadNames and the ROOT node, on the raw store: RootGraph
//     needs ROOT to exist before the stack can be built;
//  4. GraphActor over RootGraph over BoltGraph (actor outermost);
//  5. BootstrapNames(FoundationalNames) and every registry (this registers
//     the Checkers);
//  6. CloseAllSessions: sessions are ephemeral, every client died with the
//     previous process;
//  7. VerifyAll and NameRegistry.VerifyBindings, both fail-closed.
//
// Any failure closes whatever was opened and returns the error.
func OpenHost(cfg HostConfig) (*Host, error) {
	if cfg.Path == "" {
		return nil, errors.New("host: HostConfig.Path is required")
	}

	cfg = cfg.withDefaults()

	store, err := OpenBoltGraph(cfg.Path)
	if err != nil {
		return nil, fmt.Errorf("host startup: %w", err)
	}

	h, startErr := startHost(cfg, store)
	if startErr != nil {
		// store.Close() returns nil on success, and joinErrors drops nils,
		// so this is just startErr unless closing failed too.
		return nil, joinErrors(startErr, store.Close())
	}

	return h, nil
}

// startHost runs startup steps 2 to 4 and hands over to finishHostStart.
func startHost(cfg HostConfig, store *BoltGraph) (*Host, error) {
	if checkErr := store.CheckStore(); checkErr != nil {
		return nil, fmt.Errorf("host startup: checking the store: %w", checkErr)
	}

	names := NewNameRegistry(store)

	if loadErr := names.LoadNames(store); loadErr != nil {
		return nil, fmt.Errorf("host startup: loading names: %w", loadErr)
	}

	root, rootErr := names.EnsureNamedNode(store, NameRoot)
	if rootErr != nil {
		return nil, fmt.Errorf("host startup: ensuring ROOT: %w", rootErr)
	}

	rootGraph, layerErr := NewRootGraph(store, root)
	if layerErr != nil {
		return nil, fmt.Errorf("host startup: building the ROOT layer: %w", layerErr)
	}

	actor := NewGraphActor(rootGraph)

	h, finishErr := finishHostStart(cfg, store, actor, names, root)
	if finishErr != nil {
		actor.Close()
		return nil, finishErr
	}

	return h, nil
}

// finishHostStart runs startup steps 5 to 7 and starts the background
// goroutines. Nothing is started before every fallible step has passed.
func finishHostStart(cfg HostConfig, store *BoltGraph, actor *GraphActor, names *NameRegistry, root NodeID) (*Host, error) {
	ids, idsErr := names.BootstrapNames(actor, FoundationalNames)
	if idsErr != nil {
		return nil, fmt.Errorf("host startup: bootstrapping names: %w", idsErr)
	}

	reg, regErr := NewRegistries(actor, ids)
	if regErr != nil {
		return nil, fmt.Errorf("host startup: constructing registries: %w", regErr)
	}

	if _, sweepErr := reg.Leases.CloseAllSessions(actor); sweepErr != nil {
		return nil, fmt.Errorf("host startup: closing stale sessions: %w", sweepErr)
	}

	if verifyErr := VerifyAll(actor, cfg.VerifyPageSize); verifyErr != nil {
		return nil, fmt.Errorf("host startup: %w", verifyErr)
	}

	if bindErr := names.VerifyBindings(actor); bindErr != nil {
		return nil, fmt.Errorf("host startup: verifying names: %w", bindErr)
	}

	h := &Host{
		cfg:       cfg,
		store:     store,
		actor:     actor,
		names:     names,
		reg:       reg,
		root:      root,
		epoch:     time.Now(),
		stop:      make(chan struct{}),
		kickCh:    make(chan struct{}, 1),
		conns:     make(map[NodeID]*Conn),
		closing:   make(map[NodeID]struct{}),
		resources: make(map[string]*hostResource),
		byNode:    make(map[NodeID]*hostResource),
		dirty:     make(map[*hostResource]struct{}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel

	h.bg.Go(func() { h.reconcileLoop(ctx) })
	h.bg.Go(h.reapLoop)

	return h, nil
}

// Registries returns the registries, for the owner process's own use.
func (h *Host) Registries() *Registries {
	return h.reg
}

// Graph returns the actor-fronted graph, for the owner process's own use.
func (h *Host) Graph() *GraphActor {
	return h.actor
}

// monoNow is the host's monotonic clock: time since the host started.
func (h *Host) monoNow() time.Duration {
	return time.Since(h.epoch)
}

// report hands a background error to cfg.OnError.
func (h *Host) report(op string, err error) {
	if err != nil {
		h.cfg.OnError(op, err)
	}
}

// enter registers an operation that uses the actor. It fails once Close has
// begun, and Close waits for every entered operation, so no operation can
// reach the actor after it has been closed. Every successful enter must be
// paired with leave.
func (h *Host) enter() bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return false
	}

	h.ops.Add(1)

	return true
}

func (h *Host) leave() {
	h.ops.Done()
}

// kickReconciler wakes the reconciler. The channel is buffered with one
// token, so a kick that arrives during a pass triggers another pass after
// it: a pass that began before a commit can never be the last one.
func (h *Host) kickReconciler() {
	select {
	case h.kickCh <- struct{}{}:
	default:
	}
}

// markDirty tells the reconciler that something happened to resources that
// may change what they should be, and wakes it. Call it AFTER the change has
// committed (and, for Acquire, after the mark was read): the reconciler clears
// a mark before it numbers its next pass, so any pass numbered after the
// clear sees the change, and WaitApplied waits for such a pass.
func (h *Host) markDirty(resources ...*hostResource) {
	h.dirtyMu.Lock()

	for _, r := range resources {
		h.dirty[r] = struct{}{}
	}

	h.dirtyMu.Unlock()

	h.kickReconciler()
}

// markDirtyNodes is markDirty for the registered resources whose holder Set
// is one of nodes. Nodes that are not registered resources are ignored.
func (h *Host) markDirtyNodes(nodes []NodeID) {
	h.resMu.RLock()

	found := make([]*hostResource, 0, len(nodes))

	for _, node := range nodes {
		if r, ok := h.byNode[node]; ok {
			found = append(found, r)
		}
	}

	h.resMu.RUnlock()

	if len(found) > 0 {
		h.markDirty(found...)
	}
}

// takeDirty returns the marked resources and empties the set.
func (h *Host) takeDirty() map[*hostResource]struct{} {
	h.dirtyMu.Lock()
	defer h.dirtyMu.Unlock()

	taken := h.dirty
	h.dirty = make(map[*hostResource]struct{})

	return taken
}

// clearDirty drops r's mark. The reconciler calls it just before it numbers
// a pass for r, so a mark set after that call (a change committed after the
// pass began) survives and causes another pass.
func (h *Host) clearDirty(r *hostResource) {
	h.dirtyMu.Lock()
	defer h.dirtyMu.Unlock()

	delete(h.dirty, r)
}

// Close shuts the host down: operations stop, every connection is dropped
// and its session closed, a final reconcile pass removes every effect that
// is no longer held (without the teardown grace), and the actor and store
// are closed. It is safe to call more than once; later calls return nil.
func (h *Host) Close() error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}

	h.closed = true
	close(h.stop)
	h.mu.Unlock()

	h.cancel()
	h.bg.Wait()
	h.ops.Wait()

	// Nothing else uses the actor from here on.
	errs := h.shutdownConns()

	finalCtx, finalCancel := context.WithTimeout(context.Background(), h.cfg.ShutdownTimeout)
	h.reconcileAll(finalCtx, true)
	finalCancel()

	h.actor.Close()
	errs = append(errs, h.store.Close())

	return joinErrors(errs...)
}

// shutdownConns drops every remaining connection and retries every session
// close that had failed. Only Close calls it, after all other users of the
// actor have finished.
func (h *Host) shutdownConns() []error {
	h.mu.Lock()
	conns := slices.Collect(maps.Values(h.conns))
	pending := slices.Collect(maps.Keys(h.closing))
	h.mu.Unlock()

	errs := make([]error, 0, len(conns)+len(pending))

	for _, c := range conns {
		c.markDone()
		errs = append(errs, h.releaseConn(c))
	}

	for _, session := range pending {
		errs = append(errs, h.closeSession(session))
	}

	return errs
}

// RegisterResource declares a resource and its effect (nil for a pure
// lease with no outside effect). It creates the resource's holder Set,
// named resource/<name>, once and idempotently across restarts. Clients can
// acquire only registered resources. The effect is only held in memory, so
// it must be registered again after every restart; the reconciler then
// compares it with reality at once and removes a rule left behind by a
// previous run.
func (h *Host) RegisterResource(name string, effect Effect) error {
	if name == "" {
		return ErrResourceName
	}

	if !h.enter() {
		return ErrHostClosed
	}
	defer h.leave()

	node, err := transactValue(h.actor, func(tx Tx) (NodeID, error) {
		id, nameErr := h.names.EnsureNamedNode(tx, resourcePrefix+name)
		if nameErr != nil {
			return 0, nameErr
		}

		if tagErr := h.reg.Sets.TagAsSet(tx, id); tagErr != nil {
			return 0, tagErr
		}

		return id, nil
	})
	if err != nil {
		return fmt.Errorf("registering resource %q: %w", name, err)
	}

	res := newHostResource(name, node, effect, h.cfg.EffectTimeout)
	if !h.addResource(res) {
		return fmt.Errorf("%w: %q", ErrResourceRegistered, name)
	}

	h.markDirty(res)

	return nil
}

// addResource records r unless its name is taken, and reports whether it did.
func (h *Host) addResource(r *hostResource) bool {
	h.resMu.Lock()
	defer h.resMu.Unlock()

	if _, taken := h.resources[r.name]; taken {
		return false
	}

	h.resources[r.name] = r
	h.byNode[r.node] = r

	return true
}

func (h *Host) lookupResource(name string) (*hostResource, error) {
	h.resMu.RLock()
	defer h.resMu.RUnlock()

	r, ok := h.resources[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownResource, name)
	}

	return r, nil
}

// resourceSnapshot returns the registered resources sorted by name.
func (h *Host) resourceSnapshot() []*hostResource {
	h.resMu.RLock()
	defer h.resMu.RUnlock()

	names := slices.Sorted(maps.Keys(h.resources))
	snapshot := make([]*hostResource, 0, len(names))

	for _, name := range names {
		snapshot = append(snapshot, h.resources[name])
	}

	return snapshot
}

// Conn is one client's connection to the Host, in-process. It implements
// Client. A remote transport would implement Client over the network and
// call the same Host methods on the far side.
type Conn struct {
	host     *Host
	session  NodeID
	done     chan struct{}
	doneFlag atomic.Bool
	lastSeen atomic.Int64
}

// Compile-time assertion that *Conn satisfies Client.
var _ Client = (*Conn)(nil)

// Connect opens a connection and its session. The connection lives until
// Close is called on it, ctx ends (the in-process equivalent of a dropped
// socket), or it stays silent past KeepaliveTTL.
func (h *Host) Connect(ctx context.Context) (*Conn, error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("connecting: %w", ctxErr)
	}

	if !h.enter() {
		return nil, ErrHostClosed
	}
	defer h.leave()

	session, err := h.reg.Leases.NewSession(h.actor)
	if err != nil {
		return nil, fmt.Errorf("opening a session: %w", err)
	}

	c := &Conn{host: h, session: session, done: make(chan struct{})}
	c.touch()

	h.mu.Lock()
	h.conns[session] = c
	h.mu.Unlock()

	go h.watchConn(ctx, c)

	return c, nil
}

// watchConn drops c when its context ends, and ends when c ends first.
func (h *Host) watchConn(ctx context.Context, c *Conn) {
	select {
	case <-ctx.Done():
		h.report("dropping a connection whose context ended", h.dropConn(c))
	case <-c.done:
	}
}

// dropConn ends c and closes its session. Only the first caller does the
// work. If the host is already closing, Close closes the session instead.
func (h *Host) dropConn(c *Conn) error {
	if !c.markDone() {
		return nil
	}

	if !h.enter() {
		return nil
	}
	defer h.leave()

	return h.releaseConn(c)
}

// releaseConn forgets c and closes its session; a failed close stays
// pending for the reaper to retry.
func (h *Host) releaseConn(c *Conn) error {
	h.mu.Lock()
	delete(h.conns, c.session)
	h.closing[c.session] = struct{}{}
	h.mu.Unlock()

	return h.closeSession(c.session)
}

// closeSession releases every hold of session and deletes it, then wakes
// the reconciler. A session that is already gone counts as closed. On any
// other failure the session stays in h.closing and is retried.
func (h *Host) closeSession(session NodeID) error {
	freed, err := h.reg.Leases.CloseSession(h.actor, session)
	if err != nil && !errors.Is(err, ErrNodeNotFound) {
		return fmt.Errorf("closing session %d: %w", session, err)
	}

	h.mu.Lock()
	delete(h.closing, session)
	h.mu.Unlock()

	// The reconciler is level-driven, so freed is only a wake-up hint: mark
	// the resources that lost their last holder so it looks at them.
	h.markDirtyNodes(freed)

	return nil
}

// reapLoop expires silent connections and retries failed session closes.
func (h *Host) reapLoop() {
	ticker := time.NewTicker(h.cfg.ReapInterval)
	defer ticker.Stop()

	for {
		select {
		case <-h.stop:
			return
		case <-ticker.C:
			h.reapOnce()
		}
	}
}

func (h *Host) reapOnce() {
	for _, c := range h.expiredConns() {
		if c.markDone() {
			h.report("expiring a silent connection", h.releaseConn(c))
		}
	}

	h.mu.Lock()
	pending := slices.Collect(maps.Keys(h.closing))
	h.mu.Unlock()

	for _, session := range pending {
		h.report("retrying a session close", h.closeSession(session))
	}
}

// expiredConns returns the connections silent for longer than KeepaliveTTL.
func (h *Host) expiredConns() []*Conn {
	ttl := h.cfg.KeepaliveTTL
	if ttl <= 0 {
		return nil
	}

	now := h.monoNow()

	h.mu.Lock()
	defer h.mu.Unlock()

	var expired []*Conn

	for _, c := range h.conns {
		if now-time.Duration(c.lastSeen.Load()) > ttl {
			expired = append(expired, c)
		}
	}

	return expired
}

// reconcileLoop is the reconciler goroutine. due holds, for the resources
// that want another look without anything touching them (a teardown grace
// running, a failed effect waiting to retry), when that is; only this
// goroutine uses it.
func (h *Host) reconcileLoop(ctx context.Context) {
	due := make(map[*hostResource]time.Duration)
	lastFull := h.monoNow()

	for {
		timer := time.NewTimer(h.nextWake(lastFull, due))

		select {
		case <-h.stop:
			timer.Stop()
			return
		case <-h.kickCh:
			timer.Stop()
		case <-timer.C:
		}

		if h.monoNow()-lastFull >= h.cfg.ResyncInterval {
			h.reconcileSet(ctx, h.resourceSnapshot(), false, due)
			lastFull = h.monoNow()

			continue
		}

		h.reconcileSet(ctx, h.pendingResources(due), false, due)
	}
}

// nextWake is how long the reconciler may sleep: until the next full resync
// or the earliest resource deadline, whichever comes first.
func (h *Host) nextWake(lastFull time.Duration, due map[*hostResource]time.Duration) time.Duration {
	now := h.monoNow()
	wait := h.cfg.ResyncInterval - (now - lastFull)

	for _, at := range due {
		wait = min(wait, at-now)
	}

	return max(wait, 0)
}

// pendingResources returns, sorted by name, the resources marked dirty plus
// those whose deadline in due has come.
func (h *Host) pendingResources(due map[*hostResource]time.Duration) []*hostResource {
	pending := h.takeDirty()
	now := h.monoNow()

	for r, at := range due {
		if at <= now {
			pending[r] = struct{}{}
		}
	}

	list := slices.Collect(maps.Keys(pending))
	sort.Slice(list, func(i, j int) bool { return list[i].name < list[j].name })

	return list
}

// reconcileSet reconciles each of resources, and records in due when each
// wants another look (removing it from due if it does not). final is set by
// Close and skips the teardown grace.
func (h *Host) reconcileSet(ctx context.Context, resources []*hostResource, final bool, due map[*hostResource]time.Duration) {
	for _, r := range resources {
		h.clearDirty(r)

		if wait := h.reconcile(ctx, r, final); wait > 0 {
			due[r] = h.monoNow() + wait
		} else {
			delete(due, r)
		}
	}
}

// reconcileAll runs one pass over every registered resource. Close uses it
// for its final pass, so nothing is left to remember afterwards.
func (h *Host) reconcileAll(ctx context.Context, final bool) {
	h.reconcileSet(ctx, h.resourceSnapshot(), final, make(map[*hostResource]time.Duration))
}

// graceRemaining starts (or continues) the teardown grace of an unheld,
// still present resource and returns how much of it is left.
func (h *Host) graceRemaining(r *hostResource, final bool) time.Duration {
	if final || h.cfg.TeardownGrace <= 0 {
		return 0
	}

	now := h.monoNow()

	if !r.unheld {
		r.unheld = true
		r.unheldSince = now
	}

	return max(r.unheldSince+h.cfg.TeardownGrace-now, 0)
}

// failPass records a failed pass, reports it, and returns the retry delay.
func (h *Host) failPass(r *hostResource, pass uint64, err error) time.Duration {
	r.finishPass(pass, false, err)
	h.report("reconciling "+r.name, err)

	return h.cfg.RetryInterval
}

// reconcile brings one resource's effect in line with its holders and
// returns how soon it wants another look (0 for none).
//
// The pass is numbered before it reads the holder set. A client whose
// acquire committed before that read is covered by this pass; WaitApplied
// waits for a pass numbered above the client's mark, which cannot have begun
// before the commit.
func (h *Host) reconcile(ctx context.Context, r *hostResource, final bool) time.Duration {
	pass := r.beginPass()

	held, err := h.reg.Leases.Held(h.actor, r.node)
	if err != nil {
		return h.failPass(r, pass, fmt.Errorf("reading the holders of %q: %w", r.name, err))
	}

	if held {
		r.unheld = false
	}

	if r.effect == nil {
		r.finishPass(pass, held, nil)
		return 0
	}

	present, err := r.checkPresent(ctx)
	if err != nil {
		return h.failPass(r, pass, fmt.Errorf("checking the effect of %q: %w", r.name, err))
	}

	if held == present {
		r.unheld = false
		r.finishPass(pass, held, nil)

		return 0
	}

	if !held {
		if wait := h.graceRemaining(r, final); wait > 0 {
			r.finishPass(pass, false, nil)
			return wait
		}
	}

	if convergeErr := r.converge(ctx, held); convergeErr != nil {
		return h.failPass(r, pass, convergeErr)
	}

	r.unheld = false
	r.finishPass(pass, held, nil)

	return 0
}

// touch records a sign of life.
func (c *Conn) touch() {
	c.lastSeen.Store(int64(c.host.monoNow()))
}

func (c *Conn) isDone() bool {
	return c.doneFlag.Load()
}

// markDone ends the connection and reports whether this call was the one
// that did.
func (c *Conn) markDone() bool {
	if !c.doneFlag.CompareAndSwap(false, true) {
		return false
	}

	close(c.done)

	return true
}

// Done is closed when the connection has ended for any reason.
func (c *Conn) Done() <-chan struct{} {
	return c.done
}

// Close ends the connection and releases every hold of its session.
func (c *Conn) Close() error {
	return c.host.dropConn(c)
}

// Keepalive records a sign of life. It never touches the graph.
func (c *Conn) Keepalive(ctx context.Context) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("keepalive: %w", ctxErr)
	}

	if c.isDone() {
		return ErrConnClosed
	}

	c.touch()

	return nil
}

// enterResource validates the request and enters the host; on success the
// caller must call host.leave.
func (c *Conn) enterResource(ctx context.Context, name string) (*hostResource, error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("resource %q: %w", name, ctxErr)
	}

	if c.isDone() {
		return nil, ErrConnClosed
	}

	res, err := c.host.lookupResource(name)
	if err != nil {
		return nil, err
	}

	if !c.host.enter() {
		return nil, ErrHostClosed
	}

	c.touch()

	return res, nil
}

// opErr turns a graph error into the connection's own: if the connection
// ended in the meantime (so its session may be gone) the real cause is
// ErrConnClosed.
func (c *Conn) opErr(op, resource string, err error) error {
	if c.isDone() {
		return fmt.Errorf("%w (%s %q: %w)", ErrConnClosed, op, resource, err)
	}

	return fmt.Errorf("%s %q: %w", op, resource, err)
}

// Acquire implements Client: one Transact adding the session to the
// resource's holder Set.
func (c *Conn) Acquire(ctx context.Context, resource string) (AcquireResult, error) {
	res, err := c.enterResource(ctx, resource)
	if err != nil {
		return AcquireResult{}, err
	}
	defer c.host.leave()

	first, acquireErr := c.host.reg.Leases.Acquire(c.host.actor, res.node, c.session)
	if acquireErr != nil {
		return AcquireResult{}, c.opErr("acquire", resource, acquireErr)
	}

	mark := res.mark()
	c.host.markDirty(res)

	return AcquireResult{First: first, Mark: mark}, nil
}

// Release implements Client: one Transact removing the session from the
// resource's holder Set. The effect itself is removed by the reconciler,
// after the teardown grace.
func (c *Conn) Release(ctx context.Context, resource string) (bool, error) {
	res, err := c.enterResource(ctx, resource)
	if err != nil {
		return false, err
	}
	defer c.host.leave()

	last, releaseErr := c.host.reg.Leases.Release(c.host.actor, res.node, c.session)
	if releaseErr != nil {
		return false, c.opErr("release", resource, releaseErr)
	}

	c.host.markDirty(res)

	return last, nil
}

// waitErr builds WaitApplied's context-ended error, carrying the last
// effect failure if there was one.
func waitErr(cause error, resource string, last error) error {
	if last == nil {
		return fmt.Errorf("waiting for %q to be applied: %w", resource, cause)
	}

	return fmt.Errorf("waiting for %q to be applied: %w (last effect error: %w)", resource, cause, last)
}

// WaitApplied implements Client. Each time it wakes it first checks that
// the session still holds the resource, then whether a pass numbered above
// mark has completed with the effect present. See AcquireResult.Mark.
func (c *Conn) WaitApplied(ctx context.Context, resource string, mark uint64) error {
	res, err := c.enterResource(ctx, resource)
	if err != nil {
		return err
	}
	defer c.host.leave()

	for {
		holder, holdErr := c.host.reg.Sets.Contains(c.host.actor, res.node, c.session)
		if holdErr != nil {
			return c.opErr("wait for", resource, holdErr)
		}

		if !holder {
			return fmt.Errorf("%w: %q", ErrHoldLost, resource)
		}

		out := res.outcome()
		if out.completed > mark && out.applied {
			return nil
		}

		select {
		case <-ctx.Done():
			return waitErr(ctx.Err(), resource, out.err)
		case <-c.done:
			return ErrConnClosed
		case <-c.host.stop:
			return ErrHostClosed
		case <-out.changed:
		}
	}
}
