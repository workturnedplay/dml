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
	"slices"
	"sort"
	"sync/atomic"
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

// findCommonChildren is the guarded form of findCommonChildrenCore (see
// commonChildrenFinder).
func (g *Graph) findCommonChildren(a, b NodeID, limit int) ([]NodeID, error) {
	release := g.guard.acquire()
	defer release()

	return g.findCommonChildrenCore(a, b, limit)
}

// findCommonChildrenCore is findCommonChildren's unguarded implementation;
// see createNodeCore's doc comment for why this split exists. It walks the
// smaller of the two children maps and probes the other.
func (g *Graph) findCommonChildrenCore(a, b NodeID, limit int) ([]NodeID, error) {
	if !g.nodeExists(a) {
		return nil, ErrNodeNotFound
	}

	if limit <= 0 {
		return nil, nil
	}

	small, large := g.outgoing[a], g.outgoing[b]
	if len(small) > len(large) {
		small, large = large, small
	}

	common := make([]NodeID, 0, min(limit, len(small)))

	for to := range small {
		if _, shared := large[to]; !shared {
			continue
		}

		common = append(common, to)

		if len(common) == limit {
			break
		}
	}

	slices.Sort(common)

	return common, nil
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
