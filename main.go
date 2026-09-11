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

// Package dml - toy implementation for dml
package dml

import (
	"errors"
	"fmt"
	"sort"
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
)

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
}

// CreateNode creates a new node and returns its NodeID.
//
// IDs currently increase monotonically. Reuse of deleted IDs is
// deliberately not implemented yet.
func (g *Graph) CreateNode() (NodeID, error) {
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
func (g *Graph) NodeExists(id NodeID) bool {
	return g.nodeExists(id)
}

// AddRelationship creates the primitive relationship (a, b).
//
// Both nodes must already exist.
//
// Relationships are unique. Adding the same relationship again simply
// reports created=false.
func (g *Graph) AddRelationship(a, b NodeID) (created bool, err error) {
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

// RemoveRelationship removes the primitive relationship (a, b).
//
// The returned bool reports whether a relationship actually existed and
// was removed.
func (g *Graph) RemoveRelationship(a, b NodeID) (removed bool, err error) {
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
func (g *Graph) HasRelationship(a, b NodeID) bool {
	if !g.nodeExists(a) || !g.nodeExists(b) {
		return false
	}

	_, exists := g.outgoing[a][b]
	return exists
}

// FindRelationship reports whether the exact primitive relationship (from, to)
// exists.
func (g *Graph) FindRelationship(from, to NodeID) (Relationship, bool, error) {
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
func (g *Graph) FindOutgoing(from NodeID) ([]Relationship, error) {
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
func (g *Graph) FindIncoming(to NodeID) ([]Relationship, error) {
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
func (g *Graph) FindRelationships() []Relationship {
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

// DeleteNode deletes a node only when it has no relationships.
//
// Cascade deletion is deliberately not part of this primitive API.
func (g *Graph) DeleteNode(id NodeID) error {
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
type GraphReader interface {
	NodeExists(id NodeID) bool
	HasRelationship(a, b NodeID) bool
	FindRelationship(from, to NodeID) (Relationship, bool, error)
	FindOutgoing(from NodeID) ([]Relationship, error)
	FindIncoming(to NodeID) ([]Relationship, error)
	FindRelationships() []Relationship
}

// GraphStore is the complete primitive storage surface -- GraphReader's
// queries plus the mutating operations -- matching Graph's public
// method set exactly as it already existed before this interface was
// introduced (theorystate.md section 87/87a). This is the boundary a
// future non-in-memory backend (etcd, SpacetimeDB) would need to satisfy
// to stand in for Graph at the storage layer; today Graph is the only
// implementation.
type GraphStore interface {
	GraphReader
	CreateNode() (NodeID, error)
	AddRelationship(a, b NodeID) (created bool, err error)
	RemoveRelationship(a, b NodeID) (removed bool, err error)
	DeleteNode(id NodeID) error
}

// GraphAPI is GraphStore plus the transactional/commit-time-checking
// machinery (Transact, RegisterChecker) every registry in this file
// actually depends on. It is kept as a separate, wider interface from
// GraphStore rather than folding Transact/RegisterChecker directly into
// GraphStore, per theorystate.md section 87a/89a: those two methods'
// atomicity contract is a separate design question from raw storage,
// deliberately not yet resolved for any backend other than the
// in-memory one Graph implements, and a future backend satisfying
// GraphStore's storage contract is not thereby assumed to satisfy
// GraphAPI's transactional contract the same way.
//
// Every registry constructor in this file (NewPointerRegistry,
// NewCapsuleRegistry, NewListRegistry, and so on) takes a GraphAPI
// rather than a concrete *Graph, so any future GraphAPI implementation
// can be substituted with no change to registry logic. *Graph already
// satisfies GraphAPI exactly as defined below, with no changes to Graph
// itself -- this is a pure decoupling refactor (theorystate.md section
// 87).
//
// RootGraph is a deliberate, documented exception: its ROOT overlay
// needs to enumerate every existing node (FindOutgoing/FindRelationships
// for the ROOT case), which requires reaching into Graph's private
// nodes map directly, since no GraphAPI method exposes "every node that
// exists." RootGraph therefore still depends on the concrete *Graph
// type, not GraphAPI, until (or unless) a node-enumeration method is
// added to this interface -- a real, newly-identified gap, named here
// rather than silently worked around.
type GraphAPI interface {
	GraphStore
	Transact(fn func(tx *Txn) error) error
	RegisterChecker(c Checker)
}

// Compile-time assertion that *Graph satisfies GraphAPI, so any future
// accidental signature drift between Graph's methods and this interface
// is caught at build time rather than only at some call site far away.
var _ GraphAPI = (*Graph)(nil)

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
// Txn deliberately does NOT provide isolation from concurrent access.
// The toy implementation is single-threaded/serialized
// (theorystate.md section 19); nothing can observe a Txn's
// intermediate state mid-sequence today because nothing else runs
// between two statements in the same synchronous call. Should real
// concurrency be introduced later, Txn as written here would need real
// locking/isolation on top -- that is a separate, still-open problem
// (theorystate.md section 19), not one Txn tries to solve.
//
// Txn also does NOT provide durability/crash-atomicity: there is no
// persistence layer yet, so a process crash mid-transaction is not a
// concern this version needs to handle.
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
// Read operations are not wrapped, since every current caller already
// holds a reference to the underlying Graph (or NameRegistry/
// PointerRegistry wrapping one) for reads; add read-passthrough methods
// here if and when a caller actually needs them (theorystate.md
// section 7's construct-only-what's-needed discipline).
//
// Nesting one Graph.Transact call inside another is not currently
// supported or used by anything in this file: an inner Txn has its own
// independent undo log and knows nothing about an enclosing one. Genuine
// nested-transaction semantics are theorystate.md section 45, still
// OPEN; do not rely on nesting until that is deliberately designed.
type Txn struct {
	graph *Graph
	undo  []func()

	// touched records every NodeID this Txn's mutations have involved so
	// far -- as an endpoint of an added or removed relationship, or as a
	// created or deleted node -- so Graph.Transact can hand it to any
	// relevant Checker once fn returns successfully. See the Checker
	// type and the touch helper below. A relationship add/remove that
	// turned out to be a no-op (already existed / never existed) is
	// deliberately not recorded here, mirroring undo's own "only record
	// what actually changed" discipline.
	touched map[NodeID]struct{}
}

// touch records every one of ids as having been involved in this Txn's
// mutations so far. See the touched field doc comment above.
func (tx *Txn) touch(ids ...NodeID) {
	if tx.touched == nil {
		tx.touched = make(map[NodeID]struct{}, len(ids))
	}

	for _, id := range ids {
		tx.touched[id] = struct{}{}
	}
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
// Only once every relevant Checker has approved does Transact return
// nil.
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
func (g *Graph) Transact(fn func(tx *Txn) error) (err error) {
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

	return nil
}

// rollback undoes every mutation recorded on tx so far, in reverse
// (LIFO) order. Reverse order matters: for example, if tx created a node
// and then added a relationship from it, rolling back the relationship
// first leaves the node empty, so rolling back the node's creation
// (DeleteNode) afterward is guaranteed to satisfy DeleteNode's
// no-relationships precondition (see the Graph.DeleteNode doc comment).
// Undoing in the opposite order would risk DeleteNode failing with
// ErrNodeNotEmpty.
func (tx *Txn) rollback() {
	for i := len(tx.undo) - 1; i >= 0; i-- {
		tx.undo[i]()
	}
	tx.undo = nil
}

// CreateNode behaves exactly like Graph.CreateNode, additionally
// recording an undo step that deletes the new node again if the
// enclosing transaction rolls back.
func (tx *Txn) CreateNode() (NodeID, error) {
	id, err := tx.graph.CreateNode()
	if err != nil {
		return 0, err
	}

	tx.touch(id)

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
		if err := tx.graph.DeleteNode(id); err != nil {
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
	created, err = tx.graph.AddRelationship(a, b)
	if err != nil {
		return false, err
	}

	if created {
		tx.touch(a, b)

		tx.undo = append(tx.undo, func() {
			// Best-effort: deliberately swallowed, mirroring
			// Txn.CreateNode's undo closure above.
			if _, err := tx.graph.RemoveRelationship(a, b); err != nil {
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
	removed, err = tx.graph.RemoveRelationship(a, b)
	if err != nil {
		return false, err
	}

	if removed {
		tx.touch(a, b)

		tx.undo = append(tx.undo, func() {
			// Best-effort: deliberately swallowed, mirroring
			// Txn.CreateNode's undo closure above.
			if _, err := tx.graph.AddRelationship(a, b); err != nil {
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
	if err := tx.graph.DeleteNode(id); err != nil {
		return err
	}

	tx.touch(id)

	tx.undo = append(tx.undo, func() {
		tx.graph.resurrectNode(id)
	})

	return nil
}

// Checker validates one domain-specific invariant against the graph
// immediately after a Graph.Transact call's mutations have been fully
// applied, before Transact reports success to its own caller. This is
// the commit-time counterpart to the "always re-derive and re-check on
// read, never cache" discipline every registry in this file otherwise
// relies on (see e.g. the PointerRegistry doc comment): a Checker lets a
// violated invariant be caught and rolled back immediately, at the
// moment it is introduced, rather than only the next time some
// registry's own method happens to read the affected node
// (theorystate.md sections 73/77).
//
// A Checker's Check function runs against the real Graph, already fully
// mutated by the just-completed Transact call -- never a staged or
// partial view. This is sound, not merely convenient, under the current
// single-threaded execution model (theorystate.md section 19): nothing
// else can observe the already-mutated-but-not-yet-checked intermediate
// state, since nothing else runs between the mutation completing and
// Check running, in the same synchronous call. Building a staged/overlay
// view instead (theorystate.md section 77's original proposal) would
// only actually be required once real concurrent access exists; until
// then, "mutate for real, check for real, roll back exactly like any
// other failure if declined" is strictly simpler, and rests entirely on
// machinery that already exists and is already tested (Graph.Transact's
// existing rollback, including Txn.DeleteNode's resurrection,
// theorystate.md section 78).
//
// Checkers only run for mutations made through Graph.Transact. A raw,
// direct Graph.AddRelationship/RemoveRelationship/DeleteNode call --
// exactly the kind every existing out-of-band adversarial test in this
// file already uses -- has no commit boundary at all and therefore
// bypasses every Checker entirely, same as it already bypasses every
// registry's own enforcement. Checkers narrow, but do not close, that
// gap; they exist to catch a violation introduced by a composed,
// multi-step operation going through Transact, not to retroactively
// police arbitrary direct Graph mutations.
type Checker struct {
	// Name identifies this Checker in a declined commit's returned
	// error, so a caller can tell which specific invariant was violated
	// rather than receiving a generic failure (theorystate.md section
	// 77's "declines should be attributable, not generic" requirement).
	Name string

	// Tags lists every tag NodeID this Checker's invariant is defined in
	// terms of. Used only as a coarse, conservative relevance filter
	// (see Graph.checkerRelevant) to decide whether this Checker is
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
// per the same construct-only-what-is-actually-needed discipline used
// throughout this file (theorystate.md section 7) -- nothing in this
// codebase currently needs to remove a Checker once registered.
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
func (g *Graph) RegisterChecker(c Checker) {
	g.checkers = append(g.checkers, c)
}

// runCheckers consults every registered Checker whose Tags make it
// plausibly relevant to touched (see checkerRelevant), in registration
// order, returning the first error any relevant Checker reports, wrapped
// with that Checker's Name for attribution. touched being empty (a
// transaction that made no effective mutations at all) or no Checkers
// being registered on g are both treated as trivially passing, without
// iterating any further.
func (g *Graph) runCheckers(touched map[NodeID]struct{}) error {
	if len(touched) == 0 || len(g.checkers) == 0 {
		return nil
	}

	for _, checker := range g.checkers {
		if !g.checkerRelevant(checker, touched) {
			continue
		}

		if err := checker.Check(g, touched); err != nil {
			return fmt.Errorf("%s: %w", checker.Name, err)
		}
	}

	return nil
}

// checkerRelevant reports whether checker's invariant could plausibly
// have been affected by touched, using checker.Tags as a coarse,
// conservative filter: checker is considered relevant the moment any
// touched node currently carries any of checker's tags. This is
// deliberately conservative (it can report true when Check would in fact
// find nothing wrong) rather than precise -- precision is Check's own
// responsibility, per the Checker doc comment; this filter exists only
// to avoid invoking every registered Checker on every single commit
// regardless of relevance.
func (g *Graph) checkerRelevant(checker Checker, touched map[NodeID]struct{}) bool {
	for _, tag := range checker.Tags {
		for node := range touched {
			if g.HasRelationship(tag, node) {
				return true
			}
		}
	}

	return false
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
)

// NameRegistry maintains the one-to-one association between names and
// existing NodeIDs.
//
// Names are bootstrap metadata outside the primitive graph. The primitive
// Graph does not know about names.
type NameRegistry struct {
	graph GraphAPI

	byName map[string]NodeID
	byID   map[NodeID]string
}

// NewNameRegistry creates an empty name registry associated with graph.
//
// It does not create any nodes.
func NewNameRegistry(graph GraphAPI) *NameRegistry {
	return &NameRegistry{
		graph:  graph,
		byName: make(map[string]NodeID),
		byID:   make(map[NodeID]string),
	}
}

// Lookup returns the NodeID associated with name.
//
// The bool is false when name has no association.
func (r *NameRegistry) Lookup(name string) (NodeID, bool) {
	id, ok := r.byName[name]
	return id, ok
}

// NameForNode returns the name associated with id.
//
// The bool is false when id has no name.
func (r *NameRegistry) NameForNode(id NodeID) (string, bool) {
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
func (r *NameRegistry) lookupLive(name string) (id NodeID, bound bool, err error) {
	id, ok := r.byName[name]
	if !ok {
		return 0, false, nil
	}

	if !r.graph.NodeExists(id) {
		return 0, false, ErrNameBoundToDeletedNode
	}

	return id, true, nil
}

// Bind associates name with an existing, currently unnamed NodeID.
//
// Both directions of the association are unique:
//   - a name can identify only one NodeID
//   - a NodeID can have only one name
//
// Binding the exact same name to the exact same NodeID is an idempotent
// success.
func (r *NameRegistry) Bind(name string, id NodeID) error {
	if !r.graph.NodeExists(id) {
		return ErrNodeNotFound
	}

	existingID, bound, err := r.lookupLive(name)
	if err != nil {
		return err
	}

	if bound {
		if existingID == id {
			return nil
		}

		return ErrNameAlreadyBound
	}

	if _, ok := r.byID[id]; ok {
		return ErrNodeAlreadyNamed
	}

	r.byName[name] = id
	r.byID[id] = name

	return nil
}

// CreateNamedNode creates a new primitive node and immediately gives it name.
//
// If name is already bound to a live node, no new node is created and
// ErrNameAlreadyBound is returned. If name is bound to a NodeID that no
// longer exists (see lookupLive), ErrNameBoundToDeletedNode is returned
// instead, since that is a different, more serious problem than an
// ordinary already-bound name.
//
// Node creation and binding happen inside a Graph.Transact call: if Bind
// were ever to fail after CreateNode had already succeeded, the freshly
// created node would otherwise be left permanently orphaned (existing in
// the graph but never reachable through any name). Transact makes the two
// steps atomic with respect to that failure. In today's implementation
// this specific failure is not actually reachable through the public API
// -- CreateNamedNode's own lookupLive check above already guarantees
// Bind's checks will pass -- but wrapping it in Transact costs nothing
// and removes the dependency on that reasoning staying true as this code
// evolves.
func (r *NameRegistry) CreateNamedNode(name string) (NodeID, error) {
	if _, bound, err := r.lookupLive(name); err != nil {
		return 0, err
	} else if bound {
		return 0, ErrNameAlreadyBound
	}

	var id NodeID

	err := r.graph.Transact(func(tx *Txn) error {
		var err error
		id, err = tx.CreateNode()
		if err != nil {
			return err
		}

		return r.Bind(name, id)
	})
	if err != nil {
		return 0, wrapTxOpsErr(err)
	}

	return id, nil
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
func (r *NameRegistry) EnsureNamedNode(name string) (NodeID, error) {
	id, bound, err := r.lookupLive(name)
	if err != nil {
		return 0, err
	}

	if bound {
		return id, nil
	}

	return r.CreateNamedNode(name)
}

// Unbind removes the name association without deleting the NodeID.
//
// The bool reports whether an association was removed.
func (r *NameRegistry) Unbind(name string) (bool, error) {
	id, ok := r.byName[name]
	if !ok {
		return false, ErrNameNotFound
	}

	delete(r.byName, name)
	delete(r.byID, id)

	return true, nil
}

// DeleteNode deletes id from the underlying graph and, only if that
// succeeds, removes any name association for id from the registry.
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
func (r *NameRegistry) DeleteNode(id NodeID) error {
	if err := r.graph.DeleteNode(id); err != nil {
		return wrapTxOpsErr(err)
	}

	if name, ok := r.byID[id]; ok {
		delete(r.byName, name)
		delete(r.byID, id)
	}

	return nil
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
// bound, which is exactly what a resumable bootstrap should do.
//
// The returned map has one entry per distinct name in names; duplicate
// entries in names collapse into a single map entry, as expected.
func (r *NameRegistry) BootstrapNames(names []string) (map[string]NodeID, error) {
	ids := make(map[string]NodeID, len(names))

	for _, name := range names {
		id, err := r.EnsureNamedNode(name)
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

// RootGraph is the graph layer exposed above the primitive Graph.
//
// ROOT is a real NodeID in the underlying Graph. Its relationship to every
// other existing node is virtual: (ROOT, X) is visible through this layer
// whenever X exists and X != ROOT, but is not physically stored in Graph.
//
// Ordinary relationships, including relationships pointing to ROOT, are
// stored normally in Graph.
type RootGraph struct {
	graph *Graph
	root  NodeID
}

// NewRootGraph creates a ROOT layer around an existing primitive node.
func NewRootGraph(graph *Graph, root NodeID) (*RootGraph, error) {
	if !graph.NodeExists(root) {
		return nil, ErrNodeNotFound
	}

	return &RootGraph{
		graph: graph,
		root:  root,
	}, nil
}

// Root returns the NodeID used as ROOT.
func (r *RootGraph) Root() NodeID {
	return r.root
}

// CreateNode creates a node in the underlying graph.
//
// The newly created node is consequently visible as a virtual child of
// ROOT through this layer.
func (r *RootGraph) CreateNode() (NodeID, error) {
	return r.graph.CreateNode()
}

// NodeExists reports whether id exists in the underlying graph.
func (r *RootGraph) NodeExists(id NodeID) bool {
	return r.graph.NodeExists(id)
}

// AddRelationship adds an ordinary relationship to the graph.
//
// A relationship from ROOT is virtual and therefore is not physically
// stored. If the target exists, (ROOT, target) already exists in this
// layer, so adding it is simply an idempotent no-op.
//
// Relationships pointing to ROOT are ordinary relationships and are stored.
func (r *RootGraph) AddRelationship(from, to NodeID) (created bool, err error) {
	if !r.graph.NodeExists(from) {
		return false, ErrNodeNotFound
	}

	if !r.graph.NodeExists(to) {
		return false, ErrNodeNotFound
	}

	if from == r.root {
		// Whether to == root (the self-loop case, hidden by the ROOT
		// overlay's irreflexivity) or to != root (already represented
		// virtually by this layer), there is nothing to physically add
		// either way.
		return false, nil
	}

	return r.graph.AddRelationship(from, to)
}

// RemoveRelationship removes an ordinary relationship from the graph.
//
// Virtual ROOT relationships cannot be removed because their existence is
// derived from node existence. Therefore removing (ROOT, X) is a no-op
// while X exists.
//
// Relationships pointing to ROOT are ordinary relationships and can be
// removed normally.
func (r *RootGraph) RemoveRelationship(from, to NodeID) (removed bool, err error) {
	if !r.graph.NodeExists(from) {
		return false, ErrNodeNotFound
	}

	if !r.graph.NodeExists(to) {
		return false, ErrNodeNotFound
	}

	if from == r.root {
		// Symmetric with AddRelationship above: whether to == root or
		// not, there is no physical relationship for this layer to
		// remove.
		return false, nil
	}

	return r.graph.RemoveRelationship(from, to)
}

// HasRelationship reports whether the relationship exists in the ROOT
// layer.
//
// ROOT has a virtual relationship to every existing node other than itself.
// All other relationships are taken from the primitive graph.
func (r *RootGraph) HasRelationship(from, to NodeID) bool {
	if !r.graph.NodeExists(from) || !r.graph.NodeExists(to) {
		return false
	}

	if from == r.root {
		return to != r.root
	}

	return r.graph.HasRelationship(from, to)
}

// FindRelationship reports whether the exact relationship exists in the
// ROOT layer.
func (r *RootGraph) FindRelationship(from, to NodeID) (Relationship, bool, error) {
	if !r.graph.NodeExists(from) {
		return Relationship{}, false, ErrNodeNotFound
	}

	if !r.graph.NodeExists(to) {
		return Relationship{}, false, ErrNodeNotFound
	}

	if !r.HasRelationship(from, to) {
		return Relationship{}, false, nil
	}

	return Relationship{
		From: from,
		To:   to,
	}, true, nil
}

// FindOutgoing returns every relationship whose source is from in the
// ROOT layer.
//
// For ROOT, this means every existing node other than ROOT.
// For every other node, it means the ordinary stored relationships.
func (r *RootGraph) FindOutgoing(from NodeID) ([]Relationship, error) {
	if !r.graph.NodeExists(from) {
		return nil, ErrNodeNotFound
	}

	if from != r.root {
		return r.graph.FindOutgoing(from)
	}

	relationships := make([]Relationship, 0, len(r.graph.nodes)-1)

	for id := range r.graph.nodes {
		if id == r.root {
			continue
		}

		relationships = append(relationships, Relationship{
			From: r.root,
			To:   id,
		})
	}

	sort.Slice(relationships, func(i, j int) bool {
		return relationships[i].To < relationships[j].To
	})

	return relationships, nil
}

// FindIncoming returns every relationship whose target is to in the ROOT
// layer.
//
// Unlike the previous implementation, ROOT is allowed to have parents.
// Those relationships are ordinary primitive relationships and are therefore
// returned normally.
//
// The only relationship excluded by the ROOT overlay is (ROOT, ROOT).
func (r *RootGraph) FindIncoming(to NodeID) ([]Relationship, error) {
	if !r.graph.NodeExists(to) {
		return nil, ErrNodeNotFound
	}

	relationships, err := r.graph.FindIncoming(to)
	if err != nil {
		return nil, err
	}

	if to != r.root {
		return relationships, nil
	}

	// (ROOT, ROOT) is not a valid relationship in the ROOT view because
	// ROOT's virtual outgoing relationship excludes itself.
	for i := range relationships {
		if relationships[i].From == r.root {
			relationships = append(relationships[:i], relationships[i+1:]...)
			break
		}
	}

	return relationships, nil
}

// FindRelationships returns every relationship visible through the ROOT
// layer.
//
// This consists of all stored primitive relationships except relationships
// whose source is ROOT, plus the virtual ROOT -> X relationship for every
// existing X != ROOT.
//
// A primitive ROOT -> X relationship, if one exists, is ignored because the
// ROOT layer represents that relationship virtually anyway.
// The primitive (ROOT, ROOT) relationship is likewise hidden.
func (r *RootGraph) FindRelationships() []Relationship {
	primitiveRelationships := r.graph.FindRelationships()
	relationships := make([]Relationship, 0, len(primitiveRelationships)+len(r.graph.nodes)-1)

	for _, relationship := range primitiveRelationships {
		if relationship.From == r.root {
			continue
		}

		relationships = append(relationships, relationship)
	}

	for id := range r.graph.nodes {
		if id == r.root {
			continue
		}

		relationships = append(relationships, Relationship{
			From: r.root,
			To:   id,
		})
	}

	sort.Slice(relationships, func(i, j int) bool {
		if relationships[i].From != relationships[j].From {
			return relationships[i].From < relationships[j].From
		}

		return relationships[i].To < relationships[j].To
	})

	return relationships
}

// DeleteNode deletes an ordinary node from the underlying graph.
//
// ROOT itself cannot be deleted through this layer. This is reported as
// ErrCannotDeleteRoot, not ErrNodeNotEmpty: ROOT's identity is
// structurally protected by this layer regardless of whether it currently
// has any relationships, so this failure cannot be resolved by clearing
// relationships and retrying, unlike an ordinary ErrNodeNotEmpty failure.
func (r *RootGraph) DeleteNode(id NodeID) error {
	if !r.graph.NodeExists(id) {
		return ErrNodeNotFound
	}

	if id == r.root {
		return ErrCannotDeleteRoot
	}

	return r.graph.DeleteNode(id)
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
// operations atomically, whether directly against a *Graph or inside an
// existing *Txn. Both *Graph and *Txn satisfy it with their existing
// method sets, including DeleteNode -- Txn.DeleteNode is itself fully
// undoable (see its doc comment), so a caller composing several deletes
// into one logical teardown does not need any special pre-verification
// step of its own; an ordinary Transact rollback already covers it.
//
// This exists so a registry's create/wire sequence can be reused both as
// a standalone top-level Graph.Transact call and as one step composed
// into a larger enclosing Transact call -- e.g. CapsuleRegistry.NewCapsule
// composing PointerRegistry's create-and-tag sequence for each of a
// capsule's three role slots -- without nesting one Graph.Transact call
// inside another. Txn deliberately does not support nesting (see the Txn
// doc comment); parameterizing over txOps instead of a concrete *Txn is
// what lets the same sequence run either standalone or composed.
type txOps interface {
	CreateNode() (NodeID, error)
	AddRelationship(a, b NodeID) (created bool, err error)
	RemoveRelationship(a, b NodeID) (removed bool, err error)
	DeleteNode(id NodeID) error
}

// wrapTxOpsErr wraps an error returned directly from a call to one of
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
func wrapTxOpsErr(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w", err)
}

// tagNodeTx adds the tagging relationship (tag, id) against tx. This is
// the single-relationship-add step shared by createTaggedNodeTx below and
// by any caller that needs to apply more than one tag to a single node --
// e.g. CompositeSetRegistry.AddOperand's operand descriptors, which carry
// two independent axis tags on the same freshly created node.
func tagNodeTx(tx txOps, tag, id NodeID) error {
	_, err := tx.AddRelationship(tag, id)
	return wrapTxOpsErr(err)
}

// createTaggedNodeTx creates a fresh node and tags it via (tag, id),
// against tx. This is the shared "mint a fresh, tag-identified node"
// sequence used throughout this file: PointerRegistry.NewPointer's
// Pointer-kind tagging (via newPointerTx below), and the subject-slot /
// metadata / target-slot creation inside ensureMetadataWithSubjectSlot
// and PointerMetadataRegistryD.SetTarget.
func createTaggedNodeTx(tx txOps, tag NodeID) (NodeID, error) {
	id, err := tx.CreateNode()
	if err != nil {
		return 0, wrapTxOpsErr(err)
	}

	if err := tagNodeTx(tx, tag, id); err != nil {
		return 0, err
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
		if _, err := tx.RemoveRelationship(id, current); err != nil {
			return wrapTxOpsErr(err)
		}
	}

	_, err := tx.AddRelationship(id, target)
	return wrapTxOpsErr(err)
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
func singleChildTargetSetTx(tx txOps, graph GraphReader, node, target NodeID) error {
	current, hasCurrent, err := singleChildTarget(graph, node)
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
// It is the tx-composable counterpart of PointerRegistry.RemoveTarget's
// read-current/remove sequence: PointerRegistry.RemoveTarget itself
// removes directly against the underlying Graph, which is correct for
// standalone use (a single RemoveRelationship call needs no atomicity of
// its own) but wrong to reuse where the removal must be one step of a
// larger enclosing transaction -- it would not be recorded in that
// transaction's undo log, and so would survive a later step's rollback
// instead of being undone with it. ListRegistry.Remove is exactly such a
// caller: clearing a capsule's own prev/next slots must roll back
// together with the neighbor-relinking steps around it.
//
// removed reports whether a target actually existed and was removed.
func singleChildTargetRemoveTx(tx txOps, graph GraphReader, node NodeID) (removed bool, err error) {
	current, hasCurrent, err := singleChildTarget(graph, node)
	if err != nil {
		return false, err
	}

	if !hasCurrent {
		return false, nil
	}

	removed, err = tx.RemoveRelationship(node, current)
	return removed, wrapTxOpsErr(err)
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
		return 0, false, wrapTxOpsErr(err)
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
// to invariant violation: a caller can always bypass this layer and call
// Graph.AddRelationship(P, Y) directly, giving a tagged node two or more
// children. PointerRegistry does not try to intercept arbitrary Graph
// mutations -- Graph must stay unaware of Pointer semantics, per the same
// layering discipline already established elsewhere in this file.
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
// OPEN. What exists here is the minimum needed to stop PointerRegistry's
// own multi-step operations from corrupting state on failure, not a
// general transaction feature.
type PointerRegistry struct {
	graph       GraphAPI
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
	if !graph.NodeExists(allPointers) {
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
				if !g.HasRelationship(allPointers, node) {
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
		graph:       graph,
		allPointers: allPointers,
	}, nil
}

// IsPointer reports whether id is currently tagged Pointer-kind via
// (AllPointers, id).
func (p *PointerRegistry) IsPointer(id NodeID) bool {
	return p.graph.HasRelationship(p.allPointers, id)
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
func (p *PointerRegistry) currentTarget(id NodeID) (target NodeID, hasTarget bool, err error) {
	if !p.graph.NodeExists(id) {
		return 0, false, ErrNodeNotFound
	}

	if !p.IsPointer(id) {
		return 0, false, ErrNotPointer
	}

	return singleChildTarget(p.graph, id)
}

// Target returns P's current target.
//
// hasTarget is false when P is a valid, currently-empty Pointer. See
// currentTarget for the error cases: P missing, P not tagged Pointer-kind,
// or P's invariant already violated by an out-of-band Graph mutation.
func (p *PointerRegistry) Target(id NodeID) (target NodeID, hasTarget bool, err error) {
	return p.currentTarget(id)
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
func (p *PointerRegistry) SetTarget(id, target NodeID) error {
	current, hasTarget, err := p.currentTarget(id)
	if err != nil {
		return err
	}

	if !p.graph.NodeExists(target) {
		return ErrNodeNotFound
	}

	if hasTarget && current == target {
		return nil
	}

	return wrapTxOpsErr(p.graph.Transact(func(tx *Txn) error {
		return setPointerTargetTx(tx, id, current, hasTarget, target)
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
func (p *PointerRegistry) RemoveTarget(id NodeID) (removed bool, err error) {
	current, hasTarget, err := p.currentTarget(id)
	if err != nil {
		return false, err
	}

	if !hasTarget {
		return false, nil
	}

	removed, err = p.graph.RemoveRelationship(id, current)
	return removed, wrapTxOpsErr(err)
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
func (p *PointerRegistry) NewPointer() (NodeID, error) {
	var id NodeID

	err := p.graph.Transact(func(tx *Txn) error {
		var err error
		id, err = newPointerTx(tx, p.allPointers)
		return err
	})
	if err != nil {
		return 0, wrapTxOpsErr(err)
	}

	return id, nil
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
func (p *PointerRegistry) TagAsPointer(id NodeID) error {
	outgoing, err := p.graph.FindOutgoing(id)
	if err != nil {
		return wrapTxOpsErr(err)
	}

	if len(outgoing) > 1 {
		return ErrTooManyPointerTargets
	}

	_, err = p.graph.AddRelationship(p.allPointers, id)
	return wrapTxOpsErr(err)
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
		return 0, false, wrapTxOpsErr(err)
	}

	for _, rel := range incoming {
		if g.HasRelationship(tag, rel.From) {
			if found {
				return 0, false, ErrAmbiguousPointerMetadata
			}

			parent = rel.From
			found = true
		}
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
		return 0, false, wrapTxOpsErr(err)
	}

	for _, rel := range outgoing {
		if g.HasRelationship(tag, rel.To) {
			if found {
				return 0, false, ErrAmbiguousPointerMetadata
			}

			child = rel.To
			found = true
		}
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
	hasA := g.HasRelationship(tagA, node)
	hasB := g.HasRelationship(tagB, node)

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

// ensureMetadataWithSubjectSlot returns subject's existing metadata/
// subject-slot pair (via locateBySubjectSlot), creating a fresh, empty
// one (M -> S -> subject, both tagged) if none exists yet. Shared by both
// PointerMetadataRegistry and PointerMetadataRegistryD, which build
// identical subject-side structure and differ only in how the target
// side is represented. Callers are responsible for checking that subject
// itself exists before calling this.
func ensureMetadataWithSubjectSlot(g GraphAPI, subject, allPointerMetadata, allSubjectSlots NodeID) (metadata, subjectSlot NodeID, err error) {
	var found bool
	metadata, subjectSlot, found, err = locateBySubjectSlot(g, subject, allPointerMetadata, allSubjectSlots)
	if err != nil {
		return 0, 0, err
	}
	if found {
		return metadata, subjectSlot, nil
	}

	err = g.Transact(func(tx *Txn) error {
		var err2 error

		subjectSlot, err2 = createTaggedNodeTx(tx, allSubjectSlots)
		if err2 != nil {
			return err2
		}
		if _, err3 := tx.AddRelationship(subjectSlot, subject); err3 != nil {
			return err3
		}

		metadata, err2 = createTaggedNodeTx(tx, allPointerMetadata)
		if err2 != nil {
			return err2
		}

		_, err2 = tx.AddRelationship(metadata, subjectSlot)
		return err2
	})
	if err != nil {
		return 0, 0, wrapTxOpsErr(err)
	}

	return metadata, subjectSlot, nil
}

// subjectMetadataBase holds the graph/tag state and subject-side
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
// struct anonymously, so its fields (graph, allPointerMetadata,
// allSubjectSlots) and methods are promoted and usable exactly as if
// they were declared directly on the embedding type.
type subjectMetadataBase struct {
	graph              GraphAPI
	allPointerMetadata NodeID
	allSubjectSlots    NodeID
}

// locate finds subject's metadata node and subject-slot node, if any.
// found is false if subject has no metadata yet. subject must exist.
func (b *subjectMetadataBase) locate(subject NodeID) (metadata, subjectSlot NodeID, found bool, err error) {
	return locateBySubjectSlot(b.graph, subject, b.allPointerMetadata, b.allSubjectSlots)
}

// ensureMetadata returns subject's existing metadata/subject-slot pair,
// creating a fresh, empty one (M -> S -> subject, both tagged) if none
// exists yet.
func (b *subjectMetadataBase) ensureMetadata(subject NodeID) (metadata, subjectSlot NodeID, err error) {
	if !b.graph.NodeExists(subject) {
		return 0, 0, ErrNodeNotFound
	}

	return ensureMetadataWithSubjectSlot(b.graph, subject, b.allPointerMetadata, b.allSubjectSlots)
}

// EnsureMetadata returns subject's metadata node, creating an empty one
// if none exists yet.
func (b *subjectMetadataBase) EnsureMetadata(subject NodeID) (NodeID, error) {
	metadata, _, err := b.ensureMetadata(subject)
	return metadata, err
}

// HasMetadata reports whether subject currently has an associated
// metadata node, regardless of whether a target has been set.
func (b *subjectMetadataBase) HasMetadata(subject NodeID) (bool, error) {
	if !b.graph.NodeExists(subject) {
		return false, ErrNodeNotFound
	}

	_, _, found, err := b.locate(subject)
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
	if !graph.NodeExists(allPointerMetadata) {
		return nil, ErrNodeNotFound
	}

	if !graph.NodeExists(allSubjectSlots) {
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
				if !g.HasRelationship(allPointerMetadata, node) {
					continue
				}

				slot, found, err := findUniqueTaggedChild(g, node, allSubjectSlots)
				if err != nil {
					return err
				}
				if !found {
					continue
				}

				if _, _, err = singleChildTarget(g, node, slot); err != nil {
					return err
				}
			}

			return nil
		},
	})

	return &PointerMetadataRegistry{
		subjectMetadataBase: subjectMetadataBase{
			graph:              graph,
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
func (m *PointerMetadataRegistry) Target(subject NodeID) (target NodeID, hasTarget bool, err error) {
	if !m.graph.NodeExists(subject) {
		return 0, false, ErrNodeNotFound
	}

	metadata, slot, found, err := m.locate(subject)
	if err != nil {
		return 0, false, err
	}
	if !found {
		return 0, false, nil
	}

	return singleChildTarget(m.graph, metadata, slot)
}

// SetTarget sets subject's target to target, creating subject's metadata
// node first if it does not exist yet (see EnsureMetadata).
//
// Self-targeting (SetTarget(subject, subject)) is explicitly supported
// and correctly distinguished from an empty target -- see the
// PointerMetadataRegistry doc comment for why the subject-slot
// indirection is what makes this possible.
func (m *PointerMetadataRegistry) SetTarget(subject, target NodeID) error {
	if !m.graph.NodeExists(target) {
		return ErrNodeNotFound
	}

	metadata, slot, err := m.ensureMetadata(subject)
	if err != nil {
		return err
	}

	current, hasTarget, err := singleChildTarget(m.graph, metadata, slot)
	if err != nil {
		return err
	}

	if hasTarget && current == target {
		return nil
	}

	return wrapTxOpsErr(m.graph.Transact(func(tx *Txn) error {
		return setPointerTargetTx(tx, metadata, current, hasTarget, target)
	}))
}

// RemoveTarget clears subject's target, if any. The metadata/slot nodes
// themselves are left in place (no cascade deletion, consistent with
// theorystate.md section 18's rejection of
// deleteNodeAndRelationships); an empty metadata node is a valid,
// meaningful state, exactly like an empty Pointer in Representation A.
func (m *PointerMetadataRegistry) RemoveTarget(subject NodeID) (removed bool, err error) {
	if !m.graph.NodeExists(subject) {
		return false, ErrNodeNotFound
	}

	metadata, slot, found, err := m.locate(subject)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}

	target, hasTarget, err := singleChildTarget(m.graph, metadata, slot)
	if err != nil {
		return false, err
	}
	if !hasTarget {
		return false, nil
	}

	removed, err = m.graph.RemoveRelationship(metadata, target)
	return removed, wrapTxOpsErr(err)
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
	if !graph.NodeExists(allPointerMetadata) {
		return nil, ErrNodeNotFound
	}

	if !graph.NodeExists(allSubjectSlots) {
		return nil, ErrNodeNotFound
	}

	if !graph.NodeExists(allTargetSlots) {
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
				if !g.HasRelationship(allTargetSlots, node) {
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
			graph:              graph,
			allPointerMetadata: allPointerMetadata,
			allSubjectSlots:    allSubjectSlots,
		},
		allTargetSlots: allTargetSlots,
	}, nil
}

// targetSlot returns metadata's current target-slot child (U2), if any,
// found by tag rather than by exclusion -- see the PointerMetadataRegistryD
// doc comment for why this is the fix over Representation C.
func (m *PointerMetadataRegistryD) targetSlot(metadata NodeID) (slot NodeID, found bool, err error) {
	return findUniqueTaggedChild(m.graph, metadata, m.allTargetSlots)
}

// Target returns subject's current target via its metadata/target-slot
// nodes, if any.
//
// hasTarget is false when subject has no metadata node at all, when it
// has one with no target-slot yet, or when it has a target-slot with no
// target set yet -- callers that need to distinguish those cases should
// use HasMetadata and EnsureMetadata directly.
func (m *PointerMetadataRegistryD) Target(subject NodeID) (target NodeID, hasTarget bool, err error) {
	if !m.graph.NodeExists(subject) {
		return 0, false, ErrNodeNotFound
	}

	metadata, _, found, err := m.locate(subject)
	if err != nil {
		return 0, false, err
	}
	if !found {
		return 0, false, nil
	}

	slot, found, err := m.targetSlot(metadata)
	if err != nil {
		return 0, false, err
	}
	if !found {
		return 0, false, nil
	}

	return singleChildTarget(m.graph, slot)
}

// SetTarget sets subject's target to target, creating subject's metadata
// node and/or target-slot node first if they do not exist yet.
//
// Self-targeting (SetTarget(subject, subject)) is supported: U2 (the
// target-slot) is a freshly-minted node distinct from subject, U1, and M,
// so U2 -> target can never collide with any other relationship no
// matter what target equals.
func (m *PointerMetadataRegistryD) SetTarget(subject, target NodeID) error {
	if !m.graph.NodeExists(target) {
		return ErrNodeNotFound
	}

	metadata, _, err := m.ensureMetadata(subject)
	if err != nil {
		return err
	}

	slot, found, err := m.targetSlot(metadata)
	if err != nil {
		return err
	}

	if !found {
		return wrapTxOpsErr(m.graph.Transact(func(tx *Txn) error {
			var txErr error
			slot, txErr = createTaggedNodeTx(tx, m.allTargetSlots)
			if txErr != nil {
				return txErr
			}
			if _, txErr = tx.AddRelationship(metadata, slot); txErr != nil {
				return txErr
			}
			_, txErr = tx.AddRelationship(slot, target)
			return txErr
		}))
	}

	current, hasTarget, err := singleChildTarget(m.graph, slot)
	if err != nil {
		return err
	}

	if hasTarget && current == target {
		return nil
	}

	return wrapTxOpsErr(m.graph.Transact(func(tx *Txn) error {
		return setPointerTargetTx(tx, slot, current, hasTarget, target)
	}))
}

// RemoveTarget clears subject's target, if any. The metadata/subject-
// slot/target-slot nodes themselves are left in place (no cascade
// deletion, consistent with theorystate.md section 18's rejection of
// deleteNodeAndRelationships); an empty target-slot -- or no target-slot
// at all -- is a valid, meaningful state.
func (m *PointerMetadataRegistryD) RemoveTarget(subject NodeID) (removed bool, err error) {
	if !m.graph.NodeExists(subject) {
		return false, ErrNodeNotFound
	}

	metadata, _, found, err := m.locate(subject)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}

	slot, found, err := m.targetSlot(metadata)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}

	target, hasTarget, err := singleChildTarget(m.graph, slot)
	if err != nil {
		return false, err
	}
	if !hasTarget {
		return false, nil
	}

	removed, err = m.graph.RemoveRelationship(slot, target)
	return removed, wrapTxOpsErr(err)
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
	graph              GraphAPI
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
	if !graph.NodeExists(allElementCapsules) {
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
		graph:              graph,
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
				if !g.HasRelationship(allElementCapsules, node) {
					continue
				}

				if err2 := c.wellFormed(node); err2 != nil {
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
func (c *CapsuleRegistry) IsCapsule(id NodeID) bool {
	return c.graph.HasRelationship(c.allElementCapsules, id)
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
func (c *CapsuleRegistry) slotFor(capsule, tag NodeID) (slot NodeID, found bool, err error) {
	slot, found, err = findUniqueTaggedChild(c.graph, capsule, tag)
	if err != nil || !found {
		return slot, found, err
	}

	owner, foundOwner, err := findUniqueTaggedParent(c.graph, slot, c.allElementCapsules)
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
func (c *CapsuleRegistry) wellFormed(capsule NodeID) error {
	if !c.IsCapsule(capsule) {
		return ErrNotCapsule
	}

	for _, slots := range []*PointerRegistry{c.prevSlots, c.valueSlots, c.nextSlots} {
		slot, found, err := c.slotFor(capsule, slots.allPointers)
		if err != nil {
			return err
		}
		if !found {
			return ErrNotCapsule
		}

		if _, _, err = slots.Target(slot); err != nil {
			return err
		}
	}

	return nil
}

// buildCapsuleTx creates a fresh capsule NodeID, tags it via
// (allElementCapsules, capsule), and wires all three of its role slots
// (prev, value, next) -- each via the shared newPointerTx create-and-tag
// sequence -- against tx. This is the tx-composable core behind
// CapsuleRegistry.NewCapsule (via its newCapsuleTx method below),
// factored out as a free function, parameterized entirely over tag
// NodeIDs, so a larger composite operation (ListRegistry.Append/Prepend/
// InsertAfter) can mint a capsule as one step of its own enclosing
// Graph.Transact call instead of CapsuleRegistry opening a second, nested
// one.
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
		return 0, wrapTxOpsErr(err2)
	}

	valueSlot, err := newPointerTx(tx, allValueSlot)
	if err != nil {
		return 0, err
	}
	if _, err3 := tx.AddRelationship(capsule, valueSlot); err3 != nil {
		return 0, wrapTxOpsErr(err3)
	}
	if _, err4 := tx.AddRelationship(valueSlot, value); err4 != nil {
		return 0, wrapTxOpsErr(err4)
	}

	nextSlot, err := newPointerTx(tx, allNextSlot)
	if err != nil {
		return 0, err
	}

	_, err = tx.AddRelationship(capsule, nextSlot)
	if err != nil {
		return 0, wrapTxOpsErr(err)
	}

	return capsule, nil
}

// newCapsuleTx is CapsuleRegistry's tx-composable wrapper around
// buildCapsuleTx, supplying this registry's own tag NodeIDs. Exists so
// ListRegistry (same package) can mint a capsule as one step of its own
// enclosing Graph.Transact call.
func (c *CapsuleRegistry) newCapsuleTx(tx txOps, value NodeID) (NodeID, error) {
	return buildCapsuleTx(tx, c.allElementCapsules, c.prevSlots.allPointers, c.valueSlots.allPointers, c.nextSlots.allPointers, value)
}

// setSlotTargetTx rewires capsule's role slot -- found via slotTag --
// to target, composed into an existing tx. capsule must already have the
// given role slot (true for any capsule created via NewCapsule/
// newCapsuleTx). This is the tx-composable counterpart of the exported
// SetPrev/SetNext, for callers (ListRegistry) that need to rewire a
// capsule's slot as one step of a larger enclosing transaction rather
// than opening a new Graph.Transact per slot.
func (c *CapsuleRegistry) setSlotTargetTx(tx txOps, capsule, slotTag, target NodeID) error {
	slot, found, err := findUniqueTaggedChild(c.graph, capsule, slotTag)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotCapsule
	}

	return singleChildTargetSetTx(tx, c.graph, slot, target)
}

// setPrevTx rewires capsule's prev-slot to target, composed into an
// existing tx. See setSlotTargetTx.
func (c *CapsuleRegistry) setPrevTx(tx txOps, capsule, target NodeID) error {
	return c.setSlotTargetTx(tx, capsule, c.prevSlots.allPointers, target)
}

// setNextTx rewires capsule's next-slot to target, composed into an
// existing tx. See setSlotTargetTx.
func (c *CapsuleRegistry) setNextTx(tx txOps, capsule, target NodeID) error {
	return c.setSlotTargetTx(tx, capsule, c.nextSlots.allPointers, target)
}

// removeSlotTargetTx clears capsule's role slot -- found via slotTag --
// composed into an existing tx. capsule must already have the given role
// slot. This is the tx-composable counterpart of the exported
// RemovePrev/RemoveNext (see singleChildTargetRemoveTx for why a
// separate tx-composable path is needed rather than reusing those
// directly), used by ListRegistry.Remove so a capsule's own links can be
// cleared as part of the same transaction that relinks its neighbors.
func (c *CapsuleRegistry) removeSlotTargetTx(tx txOps, capsule, slotTag NodeID) (removed bool, err error) {
	slot, found, err := findUniqueTaggedChild(c.graph, capsule, slotTag)
	if err != nil {
		return false, err
	}
	if !found {
		return false, ErrNotCapsule
	}

	return singleChildTargetRemoveTx(tx, c.graph, slot)
}

// removePrevTx clears capsule's prev-slot, composed into an existing tx.
// See removeSlotTargetTx.
func (c *CapsuleRegistry) removePrevTx(tx txOps, capsule NodeID) (bool, error) {
	return c.removeSlotTargetTx(tx, capsule, c.prevSlots.allPointers)
}

// removeNextTx clears capsule's next-slot, composed into an existing tx.
// See removeSlotTargetTx.
func (c *CapsuleRegistry) removeNextTx(tx txOps, capsule NodeID) (bool, error) {
	return c.removeSlotTargetTx(tx, capsule, c.nextSlots.allPointers)
}

// NewCapsule creates a fresh capsule NodeID, tags it
// (AllElementCapsules, capsule), and wires all three of its role slots
// (prev, value, next), entirely inside one Graph.Transact call, via
// newCapsuleTx.
//
// value must already exist.
func (c *CapsuleRegistry) NewCapsule(value NodeID) (NodeID, error) {
	if !c.graph.NodeExists(value) {
		return 0, ErrNodeNotFound
	}

	var capsule NodeID

	err := c.graph.Transact(func(tx *Txn) error {
		var err error
		capsule, err = c.newCapsuleTx(tx, value)
		return err
	})
	if err != nil {
		return 0, wrapTxOpsErr(err)
	}

	return capsule, nil
}

// Value returns capsule's current value, i.e. its value slot's target.
//
// hasValue is false only if capsule's value slot has no target set --
// which should not occur for any capsule created via NewCapsule, since
// NewCapsule always sets the value slot's target immediately. It can
// only arise from an out-of-band mutation (e.g. RemoveTarget called
// directly through the underlying value PointerRegistry).
func (c *CapsuleRegistry) Value(capsule NodeID) (value NodeID, hasValue bool, err error) {
	slot, found, err := c.slotFor(capsule, c.valueSlots.allPointers)
	if err != nil {
		return 0, false, err
	}
	if !found {
		return 0, false, ErrNotCapsule
	}

	return c.valueSlots.Target(slot)
}

// SetValue replaces capsule's value.
func (c *CapsuleRegistry) SetValue(capsule, value NodeID) error {
	slot, found, err := c.slotFor(capsule, c.valueSlots.allPointers)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotCapsule
	}

	return c.valueSlots.SetTarget(slot, value)
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
func (c *CapsuleRegistry) CapsulesWithValue(value NodeID) ([]NodeID, error) {
	incoming, err := c.graph.FindIncoming(value)
	if err != nil {
		return nil, wrapTxOpsErr(err)
	}

	var capsules []NodeID

	for _, rel := range incoming {
		slot := rel.From

		if !c.valueSlots.IsPointer(slot) {
			continue
		}

		capsule, found, err := findUniqueTaggedParent(c.graph, slot, c.allElementCapsules)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}

		capsules = append(capsules, capsule)
	}

	return capsules, nil
}

// Prev returns capsule's previous-capsule link, if any. hasPrev is false
// for a capsule currently at the head of its list.
func (c *CapsuleRegistry) Prev(capsule NodeID) (prev NodeID, hasPrev bool, err error) {
	slot, found, err := c.slotFor(capsule, c.prevSlots.allPointers)
	if err != nil {
		return 0, false, err
	}
	if !found {
		return 0, false, ErrNotCapsule
	}

	return c.prevSlots.Target(slot)
}

// SetPrev sets capsule's previous-capsule link.
func (c *CapsuleRegistry) SetPrev(capsule, prev NodeID) error {
	slot, found, err := c.slotFor(capsule, c.prevSlots.allPointers)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotCapsule
	}

	return c.prevSlots.SetTarget(slot, prev)
}

// RemovePrev clears capsule's previous-capsule link, if any.
func (c *CapsuleRegistry) RemovePrev(capsule NodeID) (removed bool, err error) {
	slot, found, err := c.slotFor(capsule, c.prevSlots.allPointers)
	if err != nil {
		return false, err
	}
	if !found {
		return false, ErrNotCapsule
	}

	return c.prevSlots.RemoveTarget(slot)
}

// Next returns capsule's next-capsule link, if any. hasNext is false for
// a capsule currently at the tail of its list.
func (c *CapsuleRegistry) Next(capsule NodeID) (next NodeID, hasNext bool, err error) {
	slot, found, err := c.slotFor(capsule, c.nextSlots.allPointers)
	if err != nil {
		return 0, false, err
	}
	if !found {
		return 0, false, ErrNotCapsule
	}

	return c.nextSlots.Target(slot)
}

// SetNext sets capsule's next-capsule link.
func (c *CapsuleRegistry) SetNext(capsule, next NodeID) error {
	slot, found, err := c.slotFor(capsule, c.nextSlots.allPointers)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotCapsule
	}

	return c.nextSlots.SetTarget(slot, next)
}

// RemoveNext clears capsule's next-capsule link, if any.
func (c *CapsuleRegistry) RemoveNext(capsule NodeID) (removed bool, err error) {
	slot, found, err := c.slotFor(capsule, c.nextSlots.allPointers)
	if err != nil {
		return false, err
	}
	if !found {
		return false, ErrNotCapsule
	}

	return c.nextSlots.RemoveTarget(slot)
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
func (c *CapsuleRegistry) DeleteCapsule(capsule NodeID) error {
	if !c.graph.NodeExists(capsule) {
		return ErrNodeNotFound
	}

	if !c.IsCapsule(capsule) {
		return ErrNotCapsule
	}

	return wrapTxOpsErr(c.graph.Transact(func(tx *Txn) error {
		prevSlot, hasPrevSlot, err := c.slotFor(capsule, c.prevSlots.allPointers)
		if err != nil {
			return err
		}

		valueSlot, hasValueSlot, err := c.slotFor(capsule, c.valueSlots.allPointers)
		if err != nil {
			return err
		}

		nextSlot, hasNextSlot, err := c.slotFor(capsule, c.nextSlots.allPointers)
		if err != nil {
			return err
		}

		if !hasPrevSlot || !hasValueSlot || !hasNextSlot {
			return ErrCapsuleNotEmpty
		}

		value, hasValue, err := c.valueSlots.Target(valueSlot)
		if err != nil {
			return err
		}

		if _, err := tx.RemoveRelationship(capsule, prevSlot); err != nil {
			return err
		}
		if _, err := tx.RemoveRelationship(c.prevSlots.allPointers, prevSlot); err != nil {
			return err
		}

		if hasValue {
			if _, err := tx.RemoveRelationship(valueSlot, value); err != nil {
				return err
			}
		}
		if _, err := tx.RemoveRelationship(capsule, valueSlot); err != nil {
			return err
		}
		if _, err := tx.RemoveRelationship(c.valueSlots.allPointers, valueSlot); err != nil {
			return err
		}

		if _, err := tx.RemoveRelationship(capsule, nextSlot); err != nil {
			return err
		}
		if _, err := tx.RemoveRelationship(c.nextSlots.allPointers, nextSlot); err != nil {
			return err
		}

		if _, err := tx.RemoveRelationship(c.allElementCapsules, capsule); err != nil {
			return err
		}

		for _, node := range []NodeID{prevSlot, valueSlot, nextSlot, capsule} {
			if err := tx.DeleteNode(node); err != nil {
				if errors.Is(err, ErrNodeNotEmpty) {
					return ErrCapsuleNotEmpty
				}
				return err
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
// InsertAfter) each run entirely inside one Graph.Transact call,
// composing CapsuleRegistry's tx-composable newCapsuleTx/setPrevTx/
// setNextTx alongside direct tag (AddRelationship/RemoveRelationship)
// calls against the same tx -- no nested Graph.Transact calls anywhere,
// per the txOps discipline established above.
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
	graph    GraphAPI
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
	if !graph.NodeExists(allLists) {
		return nil, ErrNodeNotFound
	}

	if !graph.NodeExists(allHeads) {
		return nil, ErrNodeNotFound
	}

	if !graph.NodeExists(allTails) {
		return nil, ErrNodeNotFound
	}

	l := &ListRegistry{
		graph:    graph,
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
				if !g.HasRelationship(allLists, node) {
					continue
				}

				if err := l.validateStructure(node); err != nil {
					return err
				}
			}

			return nil
		},
	})

	return l, nil
}

// IsList reports whether id is currently tagged (AllLists, id).
func (l *ListRegistry) IsList(id NodeID) bool {
	return l.graph.HasRelationship(l.allLists, id)
}

// NewList creates a fresh NodeID and tags it (AllLists, id). The new list
// starts empty: no head, no tail, no element capsules.
func (l *ListRegistry) NewList() (NodeID, error) {
	var list NodeID

	err := l.graph.Transact(func(tx *Txn) error {
		var err error
		list, err = createTaggedNodeTx(tx, l.allLists)
		return err
	})
	if err != nil {
		return 0, wrapTxOpsErr(err)
	}

	return list, nil
}

// Head returns list's current head capsule, if any. hasHead is false for
// an empty list.
func (l *ListRegistry) Head(list NodeID) (head NodeID, hasHead bool, err error) {
	if !l.graph.NodeExists(list) {
		return 0, false, ErrNodeNotFound
	}

	if !l.IsList(list) {
		return 0, false, ErrNotList
	}

	head, found, err := findUniqueTaggedChild(l.graph, list, l.allHeads)
	if err != nil || !found {
		return head, found, err
	}
	if !l.capsules.IsCapsule(head) || !l.graph.HasRelationship(list, head) {
		return 0, false, ErrInvalidListStructure
	}
	return head, true, nil
}

// Tail returns list's current tail capsule, if any. hasTail is false for
// an empty list.
func (l *ListRegistry) Tail(list NodeID) (tail NodeID, hasTail bool, err error) {
	if !l.graph.NodeExists(list) {
		return 0, false, ErrNodeNotFound
	}

	if !l.IsList(list) {
		return 0, false, ErrNotList
	}

	tail, found, err := findUniqueTaggedChild(l.graph, list, l.allTails)
	if err != nil || !found {
		return tail, found, err
	}
	if !l.capsules.IsCapsule(tail) || !l.graph.HasRelationship(list, tail) {
		return 0, false, ErrInvalidListStructure
	}
	return tail, true, nil
}

// Append creates a fresh capsule holding value and links it as the new
// tail of list, entirely inside one Graph.Transact call.
//
// If list is currently empty, the new capsule becomes both head and
// tail. Otherwise the new capsule is wired in after the current tail
// (new capsule's prev -> old tail, old tail's next -> new capsule), the
// old tail loses its AllTails tag, and the new capsule gains it.
//
// list must already be tagged (AllLists, list); value must already
// exist.
func (l *ListRegistry) Append(list, value NodeID) (NodeID, error) {
	if !l.graph.NodeExists(list) {
		return 0, ErrNodeNotFound
	}

	if !l.IsList(list) {
		return 0, ErrNotList
	}

	if !l.graph.NodeExists(value) {
		return 0, ErrNodeNotFound
	}

	var capsule NodeID

	err := l.graph.Transact(func(tx *Txn) error {
		var err error
		capsule, err = l.appendTx(tx, list, value)
		return err
	})
	if err != nil {
		return 0, wrapTxOpsErr(err)
	}

	return capsule, nil
}

// appendTx is Append's tx-composable core: mint a fresh capsule holding
// value and link it as the new tail of list, against tx, without opening
// its own Graph.Transact call. Factored out so a larger composite
// operation (CompositeSetLogRegistry.AppendOperation) can append a value
// as one step of its own enclosing transaction, mirroring the existing
// newCapsuleTx/setPrevTx/setNextTx composability discipline
// (implementation_state.md item 11).
//
// list is assumed to already be confirmed to exist and be tagged
// (AllLists, list), and value to already exist -- exactly like every
// other *Tx helper in this file, callers are responsible for the checks
// Append itself performs before opening its transaction.
func (l *ListRegistry) appendTx(tx txOps, list, value NodeID) (NodeID, error) {
	oldTail, hasTail, err := findUniqueTaggedChild(l.graph, list, l.allTails)
	if err != nil {
		return 0, err
	}

	capsule, err := l.capsules.newCapsuleTx(tx, value)
	if err != nil {
		return 0, err
	}

	if _, err2 := tx.AddRelationship(list, capsule); err2 != nil {
		return 0, wrapTxOpsErr(err2)
	}

	if hasTail {
		if err3 := l.capsules.setPrevTx(tx, capsule, oldTail); err3 != nil {
			return 0, err3
		}
		if err4 := l.capsules.setNextTx(tx, oldTail, capsule); err4 != nil {
			return 0, err4
		}
		if _, err5 := tx.RemoveRelationship(l.allTails, oldTail); err5 != nil {
			return 0, wrapTxOpsErr(err5)
		}
	} else {
		if _, err6 := tx.AddRelationship(l.allHeads, capsule); err6 != nil {
			return 0, wrapTxOpsErr(err6)
		}
	}

	_, err = tx.AddRelationship(l.allTails, capsule)
	return capsule, wrapTxOpsErr(err)
}

// Prepend creates a fresh capsule holding value and links it as the new
// head of list, entirely inside one Graph.Transact call. Exact mirror of
// Append, swapping head/tail and prev/next roles.
//
// list must already be tagged (AllLists, list); value must already
// exist.
func (l *ListRegistry) Prepend(list, value NodeID) (NodeID, error) {
	if !l.graph.NodeExists(list) {
		return 0, ErrNodeNotFound
	}

	if !l.IsList(list) {
		return 0, ErrNotList
	}

	if !l.graph.NodeExists(value) {
		return 0, ErrNodeNotFound
	}

	var capsule NodeID

	err := l.graph.Transact(func(tx *Txn) error {
		oldHead, hasHead, err := findUniqueTaggedChild(l.graph, list, l.allHeads)
		if err != nil {
			return err
		}

		capsule, err = l.capsules.newCapsuleTx(tx, value)
		if err != nil {
			return err
		}

		if _, err2 := tx.AddRelationship(list, capsule); err2 != nil {
			return err2
		}

		if hasHead {
			if err3 := l.capsules.setNextTx(tx, capsule, oldHead); err3 != nil {
				return err3
			}
			if err4 := l.capsules.setPrevTx(tx, oldHead, capsule); err4 != nil {
				return err4
			}
			if _, err5 := tx.RemoveRelationship(l.allHeads, oldHead); err5 != nil {
				return err5
			}
		} else {
			if _, err6 := tx.AddRelationship(l.allTails, capsule); err6 != nil {
				return err6
			}
		}

		_, err = tx.AddRelationship(l.allHeads, capsule)
		return err
	})
	if err != nil {
		return 0, wrapTxOpsErr(err)
	}

	return capsule, nil
}

// InsertAfter creates a fresh capsule holding value and links it into
// list immediately after afterCapsule, entirely inside one
// Graph.Transact call.
//
// If afterCapsule was the tail, the new capsule becomes the new tail.
// Otherwise the new capsule is spliced in between afterCapsule and
// afterCapsule's old next capsule.
//
// list must already be tagged (AllLists, list); afterCapsule must
// already be an element of list (checked via the (list, afterCapsule)
// containment edge, returning ErrCapsuleNotInList otherwise); value must
// already exist.
func (l *ListRegistry) InsertAfter(list, afterCapsule, value NodeID) (NodeID, error) {
	if !l.graph.NodeExists(list) {
		return 0, ErrNodeNotFound
	}

	if !l.IsList(list) {
		return 0, ErrNotList
	}

	if !l.graph.NodeExists(value) {
		return 0, ErrNodeNotFound
	}

	if !l.graph.HasRelationship(list, afterCapsule) {
		return 0, ErrCapsuleNotInList
	}

	var capsule NodeID

	err := l.graph.Transact(func(tx *Txn) error {
		oldNext, hasNext, err := l.capsules.Next(afterCapsule)
		if err != nil {
			return err
		}

		capsule, err = l.capsules.newCapsuleTx(tx, value)
		if err != nil {
			return err
		}

		if _, err2 := tx.AddRelationship(list, capsule); err2 != nil {
			return err2
		}

		if err3 := l.capsules.setPrevTx(tx, capsule, afterCapsule); err3 != nil {
			return err3
		}
		if err4 := l.capsules.setNextTx(tx, afterCapsule, capsule); err4 != nil {
			return err4
		}

		if hasNext {
			if err5 := l.capsules.setNextTx(tx, capsule, oldNext); err5 != nil {
				return err5
			}
			if err6 := l.capsules.setPrevTx(tx, oldNext, capsule); err6 != nil {
				return err6
			}
			return nil
		}

		if _, err7 := tx.RemoveRelationship(l.allTails, afterCapsule); err7 != nil {
			return err7
		}
		_, err = tx.AddRelationship(l.allTails, capsule)
		return err
	})
	if err != nil {
		return 0, wrapTxOpsErr(err)
	}

	return capsule, nil
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
func (l *ListRegistry) validateStructure(list NodeID) error {
	head, hasHead, err := findUniqueTaggedChild(l.graph, list, l.allHeads)
	if err != nil {
		return err
	}
	tail, hasTail, err := findUniqueTaggedChild(l.graph, list, l.allTails)
	if err != nil {
		return err
	}

	outgoing, err := l.graph.FindOutgoing(list)
	if err != nil {
		return wrapTxOpsErr(err)
	}

	members := make(map[NodeID]struct{})
	for _, rel := range outgoing {
		if l.capsules.IsCapsule(rel.To) {
			members[rel.To] = struct{}{}
		}
	}

	if !hasHead || !hasTail {
		if hasHead || hasTail || len(members) != 0 {
			return ErrInvalidListStructure
		}
		return nil
	}

	if !l.capsules.IsCapsule(head) || !l.capsules.IsCapsule(tail) {
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

		if !l.capsules.IsCapsule(current) {
			return ErrInvalidListStructure
		}
		if !l.graph.HasRelationship(list, current) {
			return ErrInvalidListStructure
		}
		if _, hasValue, err := l.capsules.Value(current); err != nil {
			return err
		} else if !hasValue {
			return ErrInvalidListStructure
		}

		next, hasNext, err := l.capsules.Next(current)
		if err != nil {
			return err
		}
		if !hasNext {
			if current != tail {
				return ErrInvalidListStructure
			}
			break
		}

		if !l.capsules.IsCapsule(next) || !l.graph.HasRelationship(list, next) {
			return ErrInvalidListStructure
		}
		prev, hasPrev, err := l.capsules.Prev(next)
		if err != nil {
			return err
		}
		if !hasPrev || prev != current {
			return ErrInvalidListStructure
		}

		current = next
	}

	if _, hasPrev, err := l.capsules.Prev(head); err != nil {
		return err
	} else if hasPrev {
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
func (l *ListRegistry) Elements(list NodeID) ([]NodeID, error) {
	if !l.graph.NodeExists(list) {
		return nil, ErrNodeNotFound
	}

	if !l.IsList(list) {
		return nil, ErrNotList
	}

	if err := l.validateStructure(list); err != nil {
		return nil, err
	}

	var values []NodeID

	// The primitive Graph intentionally permits arbitrary cycles. The list
	// layer, however, interprets Next as a finite ordered chain, so traversal
	// must detect a repeated capsule rather than relying on a timeout or
	// assuming that well-formed construction is the only possible state.
	visited := make(map[NodeID]struct{})

	current, hasCurrent, err := findUniqueTaggedChild(l.graph, list, l.allHeads)
	if err != nil {
		return nil, err
	}

	for hasCurrent {
		if _, seen := visited[current]; seen {
			return nil, ErrListCycle
		}
		visited[current] = struct{}{}

		value, hasValue, err := l.capsules.Value(current)
		if err != nil {
			return nil, err
		}
		if hasValue {
			values = append(values, value)
		}

		current, hasCurrent, err = l.capsules.Next(current)
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
func (l *ListRegistry) OccurrencesOf(list, value NodeID) ([]NodeID, error) {
	if !l.graph.NodeExists(list) {
		return nil, ErrNodeNotFound
	}

	if !l.IsList(list) {
		return nil, ErrNotList
	}

	candidates, err := l.capsules.CapsulesWithValue(value)
	if err != nil {
		return nil, err
	}

	var occurrences []NodeID

	for _, capsule := range candidates {
		if l.graph.HasRelationship(list, capsule) {
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
func (l *ListRegistry) Contains(list, value NodeID) (capsule NodeID, found bool, err error) {
	occurrences, err := l.OccurrencesOf(list, value)
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
func (l *ListRegistry) RemoveWithoutDeletingCapsule(list, capsule NodeID) error {
	if !l.graph.NodeExists(list) {
		return ErrNodeNotFound
	}

	if !l.IsList(list) {
		return ErrNotList
	}

	if !l.graph.HasRelationship(list, capsule) {
		return ErrCapsuleNotInList
	}

	return wrapTxOpsErr(l.graph.Transact(func(tx *Txn) error {
		prev, hasPrev, err := l.capsules.Prev(capsule)
		if err != nil {
			return err
		}

		next, hasNext, err := l.capsules.Next(capsule)
		if err != nil {
			return err
		}

		switch {
		case hasPrev && hasNext:
			// Removing a middle element: splice prev and next together
			// directly. Head/tail are unaffected.
			if err2 := l.capsules.setNextTx(tx, prev, next); err2 != nil {
				return err2
			}
			if err3 := l.capsules.setPrevTx(tx, next, prev); err3 != nil {
				return err3
			}

		case hasPrev:
			// capsule was the tail: prev becomes the new tail.
			if _, err4 := l.capsules.removeNextTx(tx, prev); err4 != nil {
				return err4
			}
			if _, err5 := tx.RemoveRelationship(l.allTails, capsule); err5 != nil {
				return err5
			}
			if _, err6 := tx.AddRelationship(l.allTails, prev); err6 != nil {
				return err6
			}

		case hasNext:
			// capsule was the head: next becomes the new head.
			if _, err7 := l.capsules.removePrevTx(tx, next); err7 != nil {
				return err7
			}
			if _, err8 := tx.RemoveRelationship(l.allHeads, capsule); err8 != nil {
				return err8
			}
			if _, err9 := tx.AddRelationship(l.allHeads, next); err9 != nil {
				return err9
			}

		default:
			// capsule was the sole element: the list becomes empty.
			if _, err10 := tx.RemoveRelationship(l.allHeads, capsule); err10 != nil {
				return err10
			}
			if _, err11 := tx.RemoveRelationship(l.allTails, capsule); err11 != nil {
				return err11
			}
		}

		if _, err12 := l.capsules.removePrevTx(tx, capsule); err12 != nil {
			return err12
		}
		if _, err13 := l.capsules.removeNextTx(tx, capsule); err13 != nil {
			return err13
		}

		_, err = tx.RemoveRelationship(list, capsule)
		return err
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
// best-effort and deliberately not the same atomic step as removal:
// RemoveWithoutDeletingCapsule's own step always fully commits on its
// own terms, exactly as calling it directly would, and DeleteCapsule is
// then attempted separately immediately afterward. If capsule turns out
// not to be safely deletable -- some further reference to it or one of
// its role slots exists beyond what removal itself cleared, e.g. it was
// also (unusually) referenced by something outside this list -- deleted
// is false and err is nil: this is not a failure of Remove, it simply
// means capsule was left in place, standalone and still valid, exactly
// as RemoveWithoutDeletingCapsule already leaves it (see
// TestListRemoveWithoutDeletingCapsuleClearsCapsuleOwnLinks). err is
// reserved for genuine failures: list not tagged, capsule not currently
// an element of list, or an unexpected error from either underlying
// call.
//
// These are deliberately two separate Graph.Transact calls (one inside
// RemoveWithoutDeletingCapsule, one inside DeleteCapsule), not a single
// joint transaction spanning both. Under this codebase's current
// single-threaded, serialized execution model (theorystate.md
// section 19), nothing can run between them, so there is no observable
// intermediate state to protect against -- and keeping them separate is
// what lets a capsule that legitimately cannot be deleted still be
// fully, successfully removed from list, rather than the entire
// operation rolling back and leaving capsule stuck in list merely
// because it turned out to still be referenced elsewhere.
//
// list must already be tagged (AllLists, list); capsule must currently
// be an element of list, exactly like RemoveWithoutDeletingCapsule.
func (l *ListRegistry) Remove(list, capsule NodeID) (deleted bool, err error) {
	if err := l.RemoveWithoutDeletingCapsule(list, capsule); err != nil {
		return false, err
	}

	if err := l.capsules.DeleteCapsule(capsule); err != nil {
		if errors.Is(err, ErrCapsuleNotEmpty) {
			return false, nil
		}
		return false, err
	}

	return true, nil
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
func (l *ListRegistry) DeleteList(list NodeID) error {
	if !l.graph.NodeExists(list) {
		return ErrNodeNotFound
	}

	if !l.IsList(list) {
		return ErrNotList
	}

	return wrapTxOpsErr(l.graph.Transact(func(tx *Txn) error {
		if _, err := tx.RemoveRelationship(l.allLists, list); err != nil {
			return err
		}

		// tx.DeleteNode is used here (not a direct l.graph.DeleteNode
		// call) purely for consistency with every other multi-step
		// registry operation's txOps discipline in this file, now that
		// Txn.DeleteNode exists (theorystate.md section 78). This
		// is the last step in the sequence, so behavior is unchanged
		// either way: DeleteNode either succeeds outright or fails
		// without mutating anything, in which case returning its error
		// from this closure triggers Transact's normal rollback of the
		// tag-removal step above -- but going through tx means this
		// method no longer needs its own special-cased exception to a
		// pattern the rest of the file follows uniformly.
		return tx.DeleteNode(list)
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
// member is simply Graph.AddRelationship(S, X) / Graph.RemoveRelationship(S, X),
// tag-gated by requiring S to already be tagged (AllSets, S).
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
	graph   GraphAPI
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
	if !graph.NodeExists(allSets) {
		return nil, ErrNodeNotFound
	}

	for _, tag := range otherSetTags {
		if !graph.NodeExists(tag) {
			return nil, ErrNodeNotFound
		}
	}

	return &SetRegistry{
		graph:        graph,
		allSets:      allSets,
		otherSetTags: otherSetTags,
	}, nil
}

// IsSet reports whether id is currently tagged (AllSets, id).
func (s *SetRegistry) IsSet(id NodeID) bool {
	return s.graph.HasRelationship(s.allSets, id)
}

// NewSet creates a fresh NodeID and tags it (AllSets, id). The new set
// starts empty.
func (s *SetRegistry) NewSet() (NodeID, error) {
	var id NodeID

	err := s.graph.Transact(func(tx *Txn) error {
		var err error
		id, err = createTaggedNodeTx(tx, s.allSets)
		return err
	})
	if err != nil {
		return 0, wrapTxOpsErr(err)
	}

	return id, nil
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
func (s *SetRegistry) TagAsSet(id NodeID) error {
	if !s.graph.NodeExists(id) {
		return ErrNodeNotFound
	}

	for _, tag := range s.otherSetTags {
		if s.graph.HasRelationship(tag, id) {
			return ErrSetRepresentationConflict
		}
	}

	_, err := s.graph.AddRelationship(s.allSets, id)
	return wrapTxOpsErr(err)
}

// Add adds member to set. Both must already exist, and set must already
// be tagged (AllSets, set).
//
// added reports whether member was newly added. Adding an
// already-present member -- including self-membership, Add(set, set),
// which is permitted (theorystate.md section 2.8) -- is an
// idempotent no-op reporting added == false on the repeat call.
func (s *SetRegistry) Add(set, member NodeID) (added bool, err error) {
	if !s.graph.NodeExists(set) {
		return false, ErrNodeNotFound
	}

	if !s.IsSet(set) {
		return false, ErrNotSet
	}

	if !s.graph.NodeExists(member) {
		return false, ErrNodeNotFound
	}

	added, err = s.graph.AddRelationship(set, member)
	return added, wrapTxOpsErr(err)
}

// Remove removes member from set, if present.
//
// removed reports whether member was actually a member and was removed;
// removing a member that was never present is a no-op reporting
// removed == false, not an error.
func (s *SetRegistry) Remove(set, member NodeID) (removed bool, err error) {
	if !s.graph.NodeExists(set) {
		return false, ErrNodeNotFound
	}

	if !s.IsSet(set) {
		return false, ErrNotSet
	}

	if !s.graph.NodeExists(member) {
		return false, ErrNodeNotFound
	}

	removed, err = s.graph.RemoveRelationship(set, member)
	return removed, wrapTxOpsErr(err)
}

// Contains reports whether member currently belongs to set.
func (s *SetRegistry) Contains(set, member NodeID) (bool, error) {
	if !s.graph.NodeExists(set) {
		return false, ErrNodeNotFound
	}

	if !s.IsSet(set) {
		return false, ErrNotSet
	}

	if !s.graph.NodeExists(member) {
		return false, ErrNodeNotFound
	}

	return s.graph.HasRelationship(set, member), nil
}

// Members returns every current member of set, i.e. every direct child of
// set in the underlying Graph.
//
// This does NOT recurse into any member that happens to itself be tagged
// Set-kind -- see the SetRegistry doc comment.
func (s *SetRegistry) Members(set NodeID) ([]NodeID, error) {
	if !s.graph.NodeExists(set) {
		return nil, ErrNodeNotFound
	}

	if !s.IsSet(set) {
		return nil, ErrNotSet
	}

	outgoing, err := s.graph.FindOutgoing(set)
	if err != nil {
		return nil, wrapTxOpsErr(err)
	}

	members := make([]NodeID, 0, len(outgoing))
	for _, rel := range outgoing {
		members = append(members, rel.To)
	}

	return members, nil
}

// Size returns the number of current members of set.
func (s *SetRegistry) Size(set NodeID) (int, error) {
	members, err := s.Members(set)
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
func (s *SetRegistry) DeleteSet(set NodeID) error {
	if !s.graph.NodeExists(set) {
		return ErrNodeNotFound
	}

	if !s.IsSet(set) {
		return ErrNotSet
	}

	return wrapTxOpsErr(s.graph.Transact(func(tx *Txn) error {
		if _, err := tx.RemoveRelationship(s.allSets, set); err != nil {
			return err
		}

		return tx.DeleteNode(set)
	}))
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
		return 0, wrapTxOpsErr(err)
	}
	if err2 := tagNodeTx(tx, operationTag, u); err2 != nil {
		return 0, err2
	}
	if err3 := tagNodeTx(tx, operandTag, u); err3 != nil {
		return 0, err3
	}

	_, err = tx.AddRelationship(u, operand)
	return u, wrapTxOpsErr(err)
}

// clearOperandDescriptorEdgesTx removes descriptor u's own edge to its
// operand (if any) and both of its axis tags, against tx, without
// deleting u itself. Factored out as the shared "clear one descriptor's
// edges" step used by deleteOperandDescriptorTx below.
func clearOperandDescriptorEdgesTx(tx txOps, operand NodeID, hasOperand bool, operationTag, operandTag, u NodeID) error {
	if hasOperand {
		if _, err := tx.RemoveRelationship(u, operand); err != nil {
			return wrapTxOpsErr(err)
		}
	}

	if _, err := tx.RemoveRelationship(operationTag, u); err != nil {
		return wrapTxOpsErr(err)
	}

	_, err := tx.RemoveRelationship(operandTag, u)
	return wrapTxOpsErr(err)
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

	return wrapTxOpsErr(tx.DeleteNode(u))
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
func operandCarriesKnownSetTag(sets *SetRegistry, composites *CompositeSetRegistry, logs *CompositeSetLogRegistry, operand NodeID) bool {
	if sets.IsSet(operand) {
		return true
	}
	if composites.IsCompositeSet(operand) {
		return true
	}
	if logs != nil && logs.IsCompositeSetLog(operand) {
		return true
	}
	return false
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
func domainContainsGeneric(sets *SetRegistry, composites *CompositeSetRegistry, logs *CompositeSetLogRegistry, domain, value NodeID) (bool, error) {
	switch {
	case sets.IsSet(domain):
		return sets.Contains(domain, value)

	case composites.IsCompositeSet(domain):
		return composites.Contains(domain, value)

	case logs != nil && logs.IsCompositeSetLog(domain):
		return logs.Contains(domain, value)

	default:
		return false, ErrInvalidSetOperand
	}
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
func resolveSetOperandGeneric(sets *SetRegistry, composites *CompositeSetRegistry, logs *CompositeSetLogRegistry, operand NodeID, visited map[NodeID]struct{}) ([]NodeID, error) {
	switch {
	case sets.IsSet(operand):
		return sets.Members(operand)

	case composites.IsCompositeSet(operand):
		if _, seen := visited[operand]; seen {
			return nil, ErrCompositeSetCycle
		}
		visited[operand] = struct{}{}
		defer delete(visited, operand)

		return composites.evaluate(operand, visited)

	case logs != nil && logs.IsCompositeSetLog(operand):
		if _, seen := visited[operand]; seen {
			return nil, ErrCompositeSetCycle
		}
		visited[operand] = struct{}{}
		defer delete(visited, operand)

		return logs.evaluate(operand, visited)

	default:
		return nil, ErrInvalidSetOperand
	}
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

	return resolveSetOperandGeneric(sets, composites, logs, operand, visited)
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
	graph            GraphAPI
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
		if !graph.NodeExists(tag) {
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
				if !g.HasRelationship(allCompositeSets, node) {
					continue
				}

				outgoing, err := g.FindOutgoing(node)
				if err != nil {
					return wrapTxOpsErr(err)
				}

				for _, rel := range outgoing {
					u := rel.To

					if _, err = exactlyOneTag(g, u, allAdditiveOp, allSubtractiveOp); err != nil {
						return err
					}
					if _, err = exactlyOneTag(g, u, allScalarOperand, allSetOperand); err != nil {
						return err
					}
					if _, err = operandTargetGeneric(g, u); err != nil {
						return err
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
				isDescriptor := g.HasRelationship(allAdditiveOp, node) ||
					g.HasRelationship(allSubtractiveOp, node) ||
					g.HasRelationship(allScalarOperand, node) ||
					g.HasRelationship(allSetOperand, node)
				if !isDescriptor {
					continue
				}

				if _, err := exactlyOneTag(g, node, allAdditiveOp, allSubtractiveOp); err != nil {
					return err
				}
				if _, err := exactlyOneTag(g, node, allScalarOperand, allSetOperand); err != nil {
					return err
				}
				if _, err := operandTargetGeneric(g, node); err != nil {
					return err
				}
			}

			return nil
		},
	})

	return &CompositeSetRegistry{
		graph:            graph,
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
// simply replaces the previous value; passing nil un-wires it.
func (c *CompositeSetRegistry) SetLogs(logs *CompositeSetLogRegistry) {
	c.logs = logs
}

// IsCompositeSet reports whether id is currently tagged
// (AllCompositeSets, id).
func (c *CompositeSetRegistry) IsCompositeSet(id NodeID) bool {
	return c.graph.HasRelationship(c.allCompositeSets, id)
}

// NewCompositeSet creates a fresh NodeID and tags it (AllCompositeSets,
// id). The new composite set starts with no operands, evaluating to the
// empty set.
func (c *CompositeSetRegistry) NewCompositeSet() (NodeID, error) {
	var id NodeID

	err := c.graph.Transact(func(tx *Txn) error {
		var err error
		id, err = createTaggedNodeTx(tx, c.allCompositeSets)
		return err
	})
	if err != nil {
		return 0, wrapTxOpsErr(err)
	}

	return id, nil
}

// AddOperand adds an operand to set, represented by a freshly minted
// descriptor node U wired entirely inside one Graph.Transact call:
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
func (c *CompositeSetRegistry) AddOperand(set, operand NodeID, additive, expand bool) (u NodeID, err error) {
	if !c.graph.NodeExists(set) {
		return 0, ErrNodeNotFound
	}
	if !c.IsCompositeSet(set) {
		return 0, ErrNotCompositeSet
	}
	if !c.graph.NodeExists(operand) {
		return 0, ErrNodeNotFound
	}
	if expand && !operandCarriesKnownSetTag(c.sets, c, c.logs, operand) {
		return 0, ErrInvalidSetOperand
	}

	err = c.graph.Transact(func(tx *Txn) error {
		var err2 error
		u, err2 = buildOperandDescriptorTx(tx, c.allAdditiveOp, c.allSubtractiveOp, c.allScalarOperand, c.allSetOperand, operand, additive, expand)
		if err2 != nil {
			return err2
		}

		_, err2 = tx.AddRelationship(set, u)
		return err2
	})
	if err != nil {
		return 0, wrapTxOpsErr(err)
	}

	return u, nil
}

// RemoveOperand removes descriptor u from set entirely -- the
// containment edge (set, u), u's own edge to its operand, and both of u's
// axis tags -- then deletes u itself, all inside one Graph.Transact call.
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
func (c *CompositeSetRegistry) RemoveOperand(set, u NodeID) error {
	if !c.graph.NodeExists(set) {
		return ErrNodeNotFound
	}
	if !c.IsCompositeSet(set) {
		return ErrNotCompositeSet
	}
	if !c.graph.HasRelationship(set, u) {
		return ErrOperandNotInCompositeSet
	}

	operand, hasOperand, operationTag, operandTag, err := operandDescriptorAxes(c.graph, u, c.allAdditiveOp, c.allSubtractiveOp, c.allScalarOperand, c.allSetOperand)
	if err != nil {
		return err
	}

	return wrapTxOpsErr(c.graph.Transact(func(tx *Txn) error {
		if _, err := tx.RemoveRelationship(set, u); err != nil {
			return err
		}

		return deleteOperandDescriptorTx(tx, operand, hasOperand, operationTag, operandTag, u)
	}))
}

// Operands returns set's current operand-descriptor nodes -- its direct
// children in the underlying Graph -- in no particular semantic order
// beyond Graph.FindOutgoing's own deterministic NodeID sort. Use
// OperandTarget/OperandIsAdditive/OperandIsSetOperand to inspect each
// one.
func (c *CompositeSetRegistry) Operands(set NodeID) ([]NodeID, error) {
	if !c.graph.NodeExists(set) {
		return nil, ErrNodeNotFound
	}
	if !c.IsCompositeSet(set) {
		return nil, ErrNotCompositeSet
	}

	outgoing, err := c.graph.FindOutgoing(set)
	if err != nil {
		return nil, wrapTxOpsErr(err)
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
func (c *CompositeSetRegistry) OperandTarget(u NodeID) (operand NodeID, err error) {
	return operandTargetGeneric(c.graph, u)
}

// OperandIsAdditive reports whether descriptor u is tagged additive
// (true, contributes via union) or subtractive (false, contributes via
// set-difference).
func (c *CompositeSetRegistry) OperandIsAdditive(u NodeID) (bool, error) {
	return exactlyOneTag(c.graph, u, c.allAdditiveOp, c.allSubtractiveOp)
}

// OperandIsSetOperand reports whether descriptor u is tagged as a
// set-expansion operand (true) or a scalar operand (false).
func (c *CompositeSetRegistry) OperandIsSetOperand(u NodeID) (bool, error) {
	return exactlyOneTag(c.graph, u, c.allSetOperand, c.allScalarOperand)
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
func (c *CompositeSetRegistry) Evaluate(set NodeID) ([]NodeID, error) {
	if !c.graph.NodeExists(set) {
		return nil, ErrNodeNotFound
	}
	if !c.IsCompositeSet(set) {
		return nil, ErrNotCompositeSet
	}

	return c.evaluate(set, map[NodeID]struct{}{set: {}})
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
func (c *CompositeSetRegistry) Contains(set, value NodeID) (bool, error) {
	if !c.graph.NodeExists(set) {
		return false, ErrNodeNotFound
	}
	if !c.IsCompositeSet(set) {
		return false, ErrNotCompositeSet
	}
	if !c.graph.NodeExists(value) {
		return false, ErrNodeNotFound
	}

	members, err := c.evaluate(set, map[NodeID]struct{}{set: {}})
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
func (c *CompositeSetRegistry) evaluate(set NodeID, visited map[NodeID]struct{}) ([]NodeID, error) {
	operands, err := c.graph.FindOutgoing(set)
	if err != nil {
		return nil, wrapTxOpsErr(err)
	}

	type descriptor struct {
		u        NodeID
		additive bool
	}

	descriptors := make([]descriptor, 0, len(operands))
	for _, rel := range operands {
		additive, err := exactlyOneTag(c.graph, rel.To, c.allAdditiveOp, c.allSubtractiveOp)
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
		resolved, err := c.resolveOperand(d.u, visited)
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
		resolved, err := c.resolveOperand(d.u, visited)
		if err != nil {
			return nil, err
		}
		for _, id := range resolved {
			delete(result, id)
		}
	}

	out := make([]NodeID, 0, len(result))
	for id := range result {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })

	return out, nil
}

// resolveOperand returns the set of NodeIDs descriptor u currently
// contributes, dispatched via resolveOperandGeneric. Shared logic with
// CompositeSetLogRegistry.resolveOperand.
func (c *CompositeSetRegistry) resolveOperand(u NodeID, visited map[NodeID]struct{}) ([]NodeID, error) {
	return resolveOperandGeneric(c.graph, c.allScalarOperand, c.allSetOperand, c.sets, c, c.logs, u, visited)
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
func (c *CompositeSetRegistry) DeleteCompositeSet(set NodeID) error {
	if !c.graph.NodeExists(set) {
		return ErrNodeNotFound
	}
	if !c.IsCompositeSet(set) {
		return ErrNotCompositeSet
	}

	return wrapTxOpsErr(c.graph.Transact(func(tx *Txn) error {
		if _, err := tx.RemoveRelationship(c.allCompositeSets, set); err != nil {
			return err
		}

		return tx.DeleteNode(set)
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
	graph               GraphAPI
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
		if !graph.NodeExists(tag) {
			return nil, ErrNodeNotFound
		}
	}

	return &CompositeSetLogRegistry{
		graph:               graph,
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
func (c *CompositeSetLogRegistry) IsCompositeSetLog(id NodeID) bool {
	return c.graph.HasRelationship(c.allCompositeSetLogs, id)
}

// NewCompositeSetLog creates a fresh NodeID and tags it both
// (AllLists, id) and (AllCompositeSetLogs, id), entirely inside one
// Graph.Transact call. This deliberately does not call
// ListRegistry.NewList (which would tag AllLists in its own, separate
// Graph.Transact call): minting the node and applying both tags together
// here means there is no intermediate state where id is tagged AllLists
// but not yet AllCompositeSetLogs. The new log starts empty: no
// operations, no head, no tail.
func (c *CompositeSetLogRegistry) NewCompositeSetLog() (NodeID, error) {
	var id NodeID

	err := c.graph.Transact(func(tx *Txn) error {
		var err error
		id, err = createTaggedNodeTx(tx, c.lists.allLists)
		if err != nil {
			return err
		}

		return tagNodeTx(tx, c.allCompositeSetLogs, id)
	})
	if err != nil {
		return 0, wrapTxOpsErr(err)
	}

	return id, nil
}

// AppendOperation appends a new operation to the tail of log, recorded as
// a freshly minted operand-descriptor node U (theorystate.md section 80)
// used as the new list element's value: log -> capsule -> U -> operand,
// with U tagged along both axes described in the CompositeSetLogRegistry
// doc comment. Entirely inside one Graph.Transact call, composing
// buildOperandDescriptorTx (minting and tagging U) with
// ListRegistry.appendTx (wiring U in as the new tail element's value).
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
func (c *CompositeSetLogRegistry) AppendOperation(log, operand NodeID, additive, expand bool) (u, capsule NodeID, err error) {
	if !c.graph.NodeExists(log) {
		return 0, 0, ErrNodeNotFound
	}
	if !c.IsCompositeSetLog(log) {
		return 0, 0, ErrNotCompositeSetLog
	}
	if !c.graph.NodeExists(operand) {
		return 0, 0, ErrNodeNotFound
	}
	if expand && !operandCarriesKnownSetTag(c.sets, c.composites, c, operand) {
		return 0, 0, ErrInvalidSetOperand
	}

	err = c.graph.Transact(func(tx *Txn) error {
		var err2 error
		u, err2 = buildOperandDescriptorTx(tx, c.allAdditiveOp, c.allSubtractiveOp, c.allScalarOperand, c.allSetOperand, operand, additive, expand)
		if err2 != nil {
			return err2
		}

		capsule, err2 = c.lists.appendTx(tx, log, u)
		return err2
	})
	if err != nil {
		return 0, 0, wrapTxOpsErr(err)
	}

	return u, capsule, nil
}

// RemoveOperation removes capsule -- and its descriptor value u -- from
// log entirely.
//
// capsule is first unlinked from log via
// ListRegistry.RemoveWithoutDeletingCapsule (always succeeds once
// capsule is confirmed to be an element of log), then reclaimed via
// CapsuleRegistry.DeleteCapsule, whose own atomic teardown clears
// capsule's value-slot edge into u as part of removing capsule itself.
// Only once that succeeds does u have no remaining incoming edges at
// all; u's own edges (its operand target and both axis tags) are then
// cleared and u itself deleted together, inside one Graph.Transact call,
// via the same deleteOperandDescriptorTx helper
// CompositeSetRegistry.RemoveOperand already uses for the identically-
// shaped final step of its own teardown. Doing this as one Transact call
// (rather than clearing u's edges in one Transact and then deleting u
// via a separate, non-transactional Graph.DeleteNode call, as an earlier
// version of this method did) means a failure at either step -- e.g. an
// out-of-band mutation unexpectedly giving u a new relationship in the
// meantime -- rolls back cleanly instead of potentially leaving u
// half-cleared with no way to undo it.
//
// If DeleteCapsule fails (ErrCapsuleNotEmpty, e.g. because some
// out-of-band mutation gave one of capsule's role slots an unexpected
// extra reference), this method returns ErrCapsuleNotEmpty without
// touching u at all: capsule is left unlinked from log but otherwise
// fully intact, still holding u as its value, exactly as
// RemoveWithoutDeletingCapsule already leaves an ordinary capsule in the
// analogous ListRegistry case.
//
// capsule must currently be an element of log (checked via the
// (log,capsule) containment edge, returning ErrCapsuleNotInList
// otherwise); log must already be tagged (AllCompositeSetLogs, log).
func (c *CompositeSetLogRegistry) RemoveOperation(log, capsule NodeID) error {
	if !c.graph.NodeExists(log) {
		return ErrNodeNotFound
	}
	if !c.IsCompositeSetLog(log) {
		return ErrNotCompositeSetLog
	}
	if !c.graph.HasRelationship(log, capsule) {
		return ErrCapsuleNotInList
	}

	u, hasValue, err := c.lists.capsules.Value(capsule)
	if err != nil {
		return err
	}
	if !hasValue {
		return ErrInvalidOperandDescriptor
	}

	operand, hasOperand, operationTag, operandTag, err := operandDescriptorAxes(c.graph, u, c.allAdditiveOp, c.allSubtractiveOp, c.allScalarOperand, c.allSetOperand)
	if err != nil {
		return err
	}

	if err2 := c.lists.RemoveWithoutDeletingCapsule(log, capsule); err2 != nil {
		return err2
	}

	if err3 := c.lists.capsules.DeleteCapsule(capsule); err3 != nil {
		return err3
	}

	return wrapTxOpsErr(c.graph.Transact(func(tx *Txn) error {
		return deleteOperandDescriptorTx(tx, operand, hasOperand, operationTag, operandTag, u)
	}))
}

// Operations returns log's current operand-descriptor nodes (each
// logged operation's U, per theorystate.md section 80), in log order
// (head to tail) -- unlike CompositeSetRegistry.Operands, this order is
// semantically meaningful for a CompositeSetLog (theorystate.md section
// 82's fold is order-sensitive). Use OperandTarget/OperandIsAdditive/
// OperandIsSetOperand to inspect each one.
func (c *CompositeSetLogRegistry) Operations(log NodeID) ([]NodeID, error) {
	if !c.graph.NodeExists(log) {
		return nil, ErrNodeNotFound
	}
	if !c.IsCompositeSetLog(log) {
		return nil, ErrNotCompositeSetLog
	}

	return c.lists.Elements(log)
}

// OperandTarget returns descriptor u's operand, i.e. u's single outgoing
// relationship target. Shared logic with CompositeSetRegistry.OperandTarget
// -- see operandTargetGeneric.
func (c *CompositeSetLogRegistry) OperandTarget(u NodeID) (operand NodeID, err error) {
	return operandTargetGeneric(c.graph, u)
}

// OperandIsAdditive reports whether descriptor u is tagged additive
// (true, contributes via union) or subtractive (false, contributes via
// set-difference).
func (c *CompositeSetLogRegistry) OperandIsAdditive(u NodeID) (bool, error) {
	return exactlyOneTag(c.graph, u, c.allAdditiveOp, c.allSubtractiveOp)
}

// OperandIsSetOperand reports whether descriptor u is tagged as a
// set-expansion operand (true) or a scalar operand (false).
func (c *CompositeSetLogRegistry) OperandIsSetOperand(u NodeID) (bool, error) {
	return exactlyOneTag(c.graph, u, c.allSetOperand, c.allScalarOperand)
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
func (c *CompositeSetLogRegistry) Evaluate(log NodeID) ([]NodeID, error) {
	if !c.graph.NodeExists(log) {
		return nil, ErrNodeNotFound
	}
	if !c.IsCompositeSetLog(log) {
		return nil, ErrNotCompositeSetLog
	}

	return c.evaluate(log, map[NodeID]struct{}{log: {}})
}

// evaluate is Evaluate's recursive core, assuming log has already been
// confirmed to exist, to be tagged CompositeSetLog-kind, and to already
// be recorded in visited. See CompositeSetRegistry.evaluate's doc
// comment for why visited is path-scoped, not a global ever-visited set
// -- the same reasoning applies identically here, now shared across both
// representations (theorystate.md section 83).
func (c *CompositeSetLogRegistry) evaluate(log NodeID, visited map[NodeID]struct{}) ([]NodeID, error) {
	operations, err := c.lists.Elements(log)
	if err != nil {
		return nil, err
	}

	result := make(map[NodeID]struct{})

	for _, u := range operations {
		additive, err := exactlyOneTag(c.graph, u, c.allAdditiveOp, c.allSubtractiveOp)
		if err != nil {
			return nil, err
		}

		resolved, err := c.resolveOperand(u, visited)
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

	out := make([]NodeID, 0, len(result))
	for id := range result {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })

	return out, nil
}

// resolveOperand returns the set of NodeIDs descriptor u currently
// contributes -- shared logic with CompositeSetRegistry.resolveOperand,
// see resolveOperandGeneric.
func (c *CompositeSetLogRegistry) resolveOperand(u NodeID, visited map[NodeID]struct{}) ([]NodeID, error) {
	return resolveOperandGeneric(c.graph, c.allScalarOperand, c.allSetOperand, c.sets, c.composites, c, u, visited)
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
func (c *CompositeSetLogRegistry) Contains(log, value NodeID) (bool, error) {
	if !c.graph.NodeExists(log) {
		return false, ErrNodeNotFound
	}
	if !c.IsCompositeSetLog(log) {
		return false, ErrNotCompositeSetLog
	}
	if !c.graph.NodeExists(value) {
		return false, ErrNodeNotFound
	}

	return c.contains(log, value, map[NodeID]struct{}{log: {}})
}

// contains is Contains's recursive core, assuming log and value have
// already been confirmed to exist, log to be tagged CompositeSetLog-kind,
// and log to already be recorded in visited.
func (c *CompositeSetLogRegistry) contains(log, value NodeID, visited map[NodeID]struct{}) (bool, error) {
	operations, err := c.lists.Elements(log)
	if err != nil {
		return false, err
	}

	for i := len(operations) - 1; i >= 0; i-- {
		u := operations[i]

		operand, err := operandTargetGeneric(c.graph, u)
		if err != nil {
			return false, err
		}

		expand, err := exactlyOneTag(c.graph, u, c.allSetOperand, c.allScalarOperand)
		if err != nil {
			return false, err
		}

		mentions, err := c.operandMentions(operand, expand, value, visited)
		if err != nil {
			return false, err
		}
		if !mentions {
			continue
		}

		return exactlyOneTag(c.graph, u, c.allAdditiveOp, c.allSubtractiveOp)
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
func (c *CompositeSetLogRegistry) operandMentions(operand NodeID, expand bool, value NodeID, visited map[NodeID]struct{}) (bool, error) {
	if !expand {
		return operand == value, nil
	}

	switch {
	case c.sets.IsSet(operand):
		return c.sets.Contains(operand, value)

	case c.composites.IsCompositeSet(operand):
		if _, seen := visited[operand]; seen {
			return false, ErrCompositeSetCycle
		}
		visited[operand] = struct{}{}
		defer delete(visited, operand)

		resolved, err := c.composites.evaluate(operand, visited)
		if err != nil {
			return false, err
		}
		for _, id := range resolved {
			if id == value {
				return true, nil
			}
		}
		return false, nil

	case c.IsCompositeSetLog(operand):
		if _, seen := visited[operand]; seen {
			return false, ErrCompositeSetCycle
		}
		visited[operand] = struct{}{}
		defer delete(visited, operand)

		return c.contains(operand, value, visited)

	default:
		return false, ErrInvalidSetOperand
	}
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
func (c *CompositeSetLogRegistry) DeleteCompositeSetLog(log NodeID) error {
	if !c.graph.NodeExists(log) {
		return ErrNodeNotFound
	}
	if !c.IsCompositeSetLog(log) {
		return ErrNotCompositeSetLog
	}

	return wrapTxOpsErr(c.graph.Transact(func(tx *Txn) error {
		if _, err := tx.RemoveRelationship(c.allCompositeSetLogs, log); err != nil {
			return err
		}
		if _, err := tx.RemoveRelationship(c.lists.allLists, log); err != nil {
			return err
		}

		return tx.DeleteNode(log)
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
	graph       GraphAPI
	domainSlots *PointerRegistry
	sets        *SetRegistry
	composites  *CompositeSetRegistry
	logs        *CompositeSetLogRegistry
}

// domainSlotFor returns anchor's domain-slot child (U3), if any, found
// by tag rather than by position or exclusion, exactly like every other
// slot lookup in this file.
func (d *domainConstraint) domainSlotFor(anchor NodeID) (slot NodeID, found bool, err error) {
	return findUniqueTaggedChild(d.graph, anchor, d.domainSlots.allPointers)
}

// Domain returns anchor's current domain node, if any. hasDomain is
// false both when anchor has no domain slot at all and when it has one
// with no domain node set yet.
func (d *domainConstraint) Domain(anchor NodeID) (domain NodeID, hasDomain bool, err error) {
	slot, found, err := d.domainSlotFor(anchor)
	if err != nil || !found {
		return 0, false, err
	}

	return d.domainSlots.Target(slot)
}

// SetDomain sets anchor's domain to domain, creating anchor's domain
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
func (d *domainConstraint) SetDomain(anchor, domain NodeID) error {
	if !d.graph.NodeExists(anchor) {
		return ErrNodeNotFound
	}
	if !d.graph.NodeExists(domain) {
		return ErrNodeNotFound
	}
	if !operandCarriesKnownSetTag(d.sets, d.composites, d.logs, domain) {
		return ErrInvalidSetOperand
	}

	slot, found, err := d.domainSlotFor(anchor)
	if err != nil {
		return err
	}

	if !found {
		return wrapTxOpsErr(d.graph.Transact(func(tx *Txn) error {
			newSlot, err2 := createTaggedNodeTx(tx, d.domainSlots.allPointers)
			if err2 != nil {
				return err2
			}
			if _, err3 := tx.AddRelationship(anchor, newSlot); err3 != nil {
				return err3
			}
			_, err4 := tx.AddRelationship(newSlot, domain)
			return err4
		}))
	}

	return d.domainSlots.SetTarget(slot, domain)
}

// RemoveDomain clears anchor's domain, if any. The domain-slot node
// itself is left in place (no cascade deletion, consistent with
// theorystate.md section 18); a domain-slot with no domain set is a
// valid, meaningful "no constraint" state, exactly like an empty
// Pointer elsewhere in this file.
func (d *domainConstraint) RemoveDomain(anchor NodeID) (removed bool, err error) {
	if !d.graph.NodeExists(anchor) {
		return false, ErrNodeNotFound
	}

	slot, found, err := d.domainSlotFor(anchor)
	if err != nil || !found {
		return false, err
	}

	return d.domainSlots.RemoveTarget(slot)
}

// validateMembership reports whether target currently belongs to
// domain's resolved membership, dispatched generically over whichever of
// the three Set representations domain actually carries (see
// domainContainsGeneric), returning ErrTargetOutsideDomain if not.
func (d *domainConstraint) validateMembership(domain, target NodeID) error {
	contains, err := domainContainsGeneric(d.sets, d.composites, d.logs, domain, target)
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
// See theorystate.md section 86 for the known, accepted gap this check
// does not close: this only validates against the domain's membership
// as of this call. A domain's own membership changing afterward, via a
// mutation that never touches anchor or its domain slot, is not
// detected here or by any Checker -- SetDomain and SetTarget (on
// whichever of DomainPointerRegistryB/D this is embedded in) are the
// only two write paths that ever re-validate this relationship.
func (d *domainConstraint) checkAllowed(anchor, target NodeID) error {
	domain, hasDomain, err := d.Domain(anchor)
	if err != nil {
		return err
	}
	if !hasDomain {
		return nil
	}

	return d.validateMembership(domain, target)
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
// Unlike DomainPointerRegistryD, this type registers no commit-time
// Checker of its own for domain-membership enforcement. Representation
// D's anchor (M) is self-identifying via its own AllPointerMetadata tag,
// which is what lets its Checker reverse-discover M from a touched
// target- or domain-slot node. Representation B's anchor P carries no
// equivalent distinguishing tag in the general case -- P may be any
// caller-managed node, tagged however the caller's own domain requires
// or not tagged at all -- so reverse-discovering P from a touched U or
// U3 would require either an untagged "find the one parent" lookup
// (the exact anti-pattern already rejected elsewhere in this file, see
// CapsulesWithValue's doc comment) or a new bookkeeping tag applied to
// every domain-constrained P purely to support this one Checker. Given
// no current caller needs Representation B domain pointers at all yet,
// this is deferred rather than built ahead of an actual need
// (theorystate.md section 7) -- domain-membership enforcement for B is
// therefore write-time only, via SetTarget and SetDomain below; a caller
// that bypasses this type and mutates the underlying PointerRegistry or
// domain-slot PointerRegistry directly will not be caught until (or
// unless) something later reads back through this type.
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
func NewDomainPointerRegistryB(graph GraphAPI, pointers, domainSlots *PointerRegistry, sets *SetRegistry, composites *CompositeSetRegistry, logs *CompositeSetLogRegistry) *DomainPointerRegistryB {
	return &DomainPointerRegistryB{
		domainConstraint: domainConstraint{
			graph:       graph,
			domainSlots: domainSlots,
			sets:        sets,
			composites:  composites,
			logs:        logs,
		},
		pointers: pointers,
	}
}

// subPointer returns anchor's Representation B sub-pointer node U -- the
// single child of anchor tagged via the underlying PointerRegistry's own
// tag -- found by tag, not by position, exactly like every other slot
// lookup in this file.
func (b *DomainPointerRegistryB) subPointer(anchor NodeID) (u NodeID, found bool, err error) {
	return findUniqueTaggedChild(b.graph, anchor, b.pointers.allPointers)
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
func (b *DomainPointerRegistryB) NewDomainPointer(anchor NodeID) error {
	if !b.graph.NodeExists(anchor) {
		return ErrNodeNotFound
	}

	_, found, err := b.subPointer(anchor)
	if err != nil {
		return err
	}
	if found {
		return nil
	}

	return wrapTxOpsErr(b.graph.Transact(func(tx *Txn) error {
		u, err2 := newPointerTx(tx, b.pointers.allPointers)
		if err2 != nil {
			return err2
		}

		_, err2 = tx.AddRelationship(anchor, u)
		return err2
	}))
}

// Target returns anchor's current target via its sub-pointer node U, if
// any. hasTarget is false both when anchor has no discoverable U at all
// and when U exists but has no target set yet.
func (b *DomainPointerRegistryB) Target(anchor NodeID) (target NodeID, hasTarget bool, err error) {
	u, found, err := b.subPointer(anchor)
	if err != nil || !found {
		return 0, false, err
	}

	return b.pointers.Target(u)
}

// SetTarget sets anchor's target to target, first validating target
// against anchor's currently attached domain, if any (see
// domainConstraint.checkAllowed). anchor must already have a
// discoverable sub-pointer node U (see NewDomainPointer); otherwise this
// returns ErrNotPointer, mirroring the underlying PointerRegistry's own
// error for an untagged node.
func (b *DomainPointerRegistryB) SetTarget(anchor, target NodeID) error {
	if !b.graph.NodeExists(target) {
		return ErrNodeNotFound
	}

	if err := b.checkAllowed(anchor, target); err != nil {
		return err
	}

	u, found, err := b.subPointer(anchor)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotPointer
	}

	return b.pointers.SetTarget(u, target)
}

// RemoveTarget clears anchor's target, if any, via its sub-pointer node
// U.
func (b *DomainPointerRegistryB) RemoveTarget(anchor NodeID) (removed bool, err error) {
	u, found, err := b.subPointer(anchor)
	if err != nil || !found {
		return false, err
	}

	return b.pointers.RemoveTarget(u)
}

// SetDomain sets anchor's domain to domain, additionally validating that
// anchor's current target (if any) still belongs to domain before
// committing -- symmetric with SetTarget's own validation against the
// current domain. See domainConstraint.SetDomain for the shared
// creation/validation logic this delegates to.
func (b *DomainPointerRegistryB) SetDomain(anchor, domain NodeID) error {
	target, hasTarget, err := b.Target(anchor)
	if err != nil {
		return err
	}

	if hasTarget {
		if err2 := b.validateMembership(domain, target); err2 != nil {
			return err2
		}
	}

	return b.domainConstraint.SetDomain(anchor, domain)
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
// Unlike DomainPointerRegistryB, this type registers a commit-time
// Checker (see NewDomainPointerRegistryD) in addition to write-time
// enforcement in SetTarget/SetDomain below: M is self-identifying via
// its own AllPointerMetadata tag, which lets the Checker reverse-
// discover M from either a touched target-slot or a touched domain-slot
// node, using the exact same findUniqueTaggedParent lookup
// locateBySubjectSlot already uses one hop further out. See
// DomainPointerRegistryB's doc comment for why this reverse-discovery
// path is not available in the general Representation B case.
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
// This additionally registers a Checker (see Graph.RegisterChecker)
// enforcing domain-membership at commit time for any node tagged
// metadata's own target-slot tag or the shared AllDomainSlot tag: for
// each such touched node, it reverse-discovers the owning metadata node
// M (via findUniqueTaggedParent against metadata's own AllPointerMetadata
// tag -- the same lookup locateBySubjectSlot already performs one hop
// further out, applied here directly against a slot rather than via a
// subject), then re-validates M's current target against M's current
// domain, catching a caller bypassing this type and mutating the
// underlying PointerMetadataRegistryD or the shared domainSlots registry
// directly through either one.
func NewDomainPointerRegistryD(graph GraphAPI, metadata *PointerMetadataRegistryD, domainSlots *PointerRegistry, sets *SetRegistry, composites *CompositeSetRegistry, logs *CompositeSetLogRegistry) *DomainPointerRegistryD {
	d := &DomainPointerRegistryD{
		domainConstraint: domainConstraint{
			graph:       graph,
			domainSlots: domainSlots,
			sets:        sets,
			composites:  composites,
			logs:        logs,
		},
		metadata: metadata,
	}

	graph.RegisterChecker(Checker{
		Name: fmt.Sprintf("DomainPointerRegistryD(tag=%d)", domainSlots.allPointers),
		Tags: []NodeID{metadata.allTargetSlots, domainSlots.allPointers},
		Check: func(g GraphReader, touched map[NodeID]struct{}) error {
			anchors := make(map[NodeID]struct{})

			for node := range touched {
				if !g.HasRelationship(metadata.allTargetSlots, node) && !g.HasRelationship(domainSlots.allPointers, node) {
					continue
				}

				m, found, err := findUniqueTaggedParent(g, node, metadata.allPointerMetadata)
				if err != nil {
					return err
				}
				if found {
					anchors[m] = struct{}{}
				}
			}

			for m := range anchors {
				slot, found, err := metadata.targetSlot(m)
				if err != nil {
					return err
				}
				if !found {
					continue
				}

				target, hasTarget, err := singleChildTarget(g, slot)
				if err != nil {
					return err
				}
				if !hasTarget {
					continue
				}

				if err := d.checkAllowed(m, target); err != nil {
					return err
				}
			}

			return nil
		},
	})

	return d
}

// Target returns subject's current target, delegating directly to the
// underlying PointerMetadataRegistryD.
func (d *DomainPointerRegistryD) Target(subject NodeID) (target NodeID, hasTarget bool, err error) {
	return d.metadata.Target(subject)
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
func (d *DomainPointerRegistryD) SetTarget(subject, target NodeID) error {
	if !d.graph.NodeExists(target) {
		return ErrNodeNotFound
	}

	m, _, found, err := d.metadata.locate(subject)
	if err != nil {
		return err
	}
	if found {
		if err2 := d.checkAllowed(m, target); err2 != nil {
			return err2
		}
	}

	return d.metadata.SetTarget(subject, target)
}

// RemoveTarget clears subject's target, if any, delegating directly to
// the underlying PointerMetadataRegistryD.
func (d *DomainPointerRegistryD) RemoveTarget(subject NodeID) (removed bool, err error) {
	return d.metadata.RemoveTarget(subject)
}

// Domain returns subject's current domain node, if any, resolving
// subject's metadata node M first via the underlying
// PointerMetadataRegistryD's own subject-side lookup. hasDomain is false
// if subject has no metadata node at all yet, in addition to
// domainConstraint.Domain's own "no domain slot" and "no domain set"
// cases.
func (d *DomainPointerRegistryD) Domain(subject NodeID) (domain NodeID, hasDomain bool, err error) {
	m, _, found, err := d.metadata.locate(subject)
	if err != nil || !found {
		return 0, false, err
	}

	return d.domainConstraint.Domain(m)
}

// SetDomain sets subject's domain to domain, creating subject's metadata
// node first if it does not exist yet, and additionally validating that
// subject's current target (if any) still belongs to domain before
// committing -- symmetric with SetTarget's own validation against the
// current domain.
func (d *DomainPointerRegistryD) SetDomain(subject, domain NodeID) error {
	m, err := d.metadata.EnsureMetadata(subject)
	if err != nil {
		return err
	}

	target, hasTarget, err := d.metadata.Target(subject)
	if err != nil {
		return err
	}

	if hasTarget {
		if err2 := d.validateMembership(domain, target); err2 != nil {
			return err2
		}
	}

	return d.domainConstraint.SetDomain(m, domain)
}

// RemoveDomain clears subject's domain, if any, resolving subject's
// metadata node M first. removed is false if subject has no metadata
// node at all yet, in addition to domainConstraint.RemoveDomain's own
// "no domain slot" case.
func (d *DomainPointerRegistryD) RemoveDomain(subject NodeID) (removed bool, err error) {
	m, _, found, err := d.metadata.locate(subject)
	if err != nil || !found {
		return false, err
	}

	return d.domainConstraint.RemoveDomain(m)
}
