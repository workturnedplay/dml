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
	"sort"
	"testing"
)

// ---------------------------------------------------------------------
// stagedGraph and stagedOverlay: a test-only GraphAPI implementation
// deliberately built around the OPPOSITE mechanism from *Graph's
// mutate-then-check, undo-log approach (theorystate.md sections 93-97).
// Every write made during one Transact attempt is buffered in a local,
// per-attempt overlay and never touches stagedGraph's own backing store
// until that attempt both succeeds and every relevant Checker approves,
// at which point the overlay is published in one step -- the same
// buffer-then-commit shape an eventual etcd-backed GraphStore would use
// (theorystate.md section 94), simulated here entirely in memory with no
// real network or real optimistic-concurrency conflict detection.
//
// The point of this type existing at all: theorystate.md section 96
// deliberately keeps *Graph's own mechanism unchanged rather than
// reshaping it to look like a networked backend, on the grounds that a
// registry which only ever touches graph state through the Tx/GraphAPI
// interface should already be portable across mechanisms without any
// special-casing. stagedGraph exists to actually test that claim: every
// registry constructor and method in this file takes a GraphAPI/
// Transactor/Tx, never a concrete *Graph, so the existing registries can
// be, and here are, exercised against this structurally different
// backend with zero code changes -- catching, before any real networked
// backend exists, any registry code that accidentally depends on seeing
// its own writes through some path other than the tx/g value it was
// actually given, or on fn running exactly once.
//
// Like *Graph, a bare *stagedGraph supports only one goroutine at a time
// -- it reuses concurrentAccessGuard directly rather than duplicating
// its fail-fast discipline -- and can be wrapped in GraphActor for real
// concurrent access with no changes, exactly like *Graph, since it
// already satisfies GraphAPI.
type stagedGraph struct {
	guard concurrentAccessGuard

	nodes    map[NodeID]struct{}
	outgoing map[NodeID]map[NodeID]struct{}
	incoming map[NodeID]map[NodeID]struct{}
	nextID   NodeID

	checkers []Checker

	// forceConflict, if set, is called once per Transact attempt,
	// immediately after fn has returned nil and every relevant Checker
	// has approved, and before that attempt's overlay would otherwise be
	// published. attempt is 1 for the first try, 2 for the first retry,
	// and so on. Returning true discards the attempt's entire overlay --
	// as a losing real CAS commit would -- and Transact reruns fn from
	// scratch as a fresh attempt against the backing store's current
	// state: the theorystate.md section 91 "fn may be executed more than
	// once" clause, which nothing else in this codebase exercises even
	// once. This is a deliberate, fully deterministic substitute for real
	// conflict detection (this type tracks no per-attempt read-set and
	// detects no genuine conflict against one) -- see the stagedGraph
	// doc comment.
	forceConflict func(attempt int) bool
}

// newStagedGraph returns an empty stagedGraph, ready to use. Unlike
// *Graph, which supports its zero value directly, a stagedGraph must be
// constructed this way, since its maps need explicit initialization and
// no caller needs zero-value support for a test-only type.
func newStagedGraph() *stagedGraph {
	return &stagedGraph{
		nodes:    make(map[NodeID]struct{}),
		outgoing: make(map[NodeID]map[NodeID]struct{}),
		incoming: make(map[NodeID]map[NodeID]struct{}),
	}
}

// The nodeExistsCore/hasRelationshipCore/findOutgoingCore/
// findIncomingCore/findRelationshipsCore/findNodesCore methods below are
// stagedGraph's unguarded core reads, exactly mirroring *Graph's own
// core/guarded split (see createNodeCore's doc comment on *Graph for why
// this split exists): stagedGraph.Transact acquires g.guard once for an
// entire attempt, including running fn and every relevant Checker, so
// every read a stagedOverlay performs against its own base -- from
// inside fn or from inside a Checker's Check function -- must go through
// these unguarded cores rather than the guarded public methods below,
// or it would panic as if a second goroutine had raced in.

func (g *stagedGraph) nodeExistsCore(id NodeID) bool {
	_, ok := g.nodes[id]
	return ok
}

func (g *stagedGraph) hasRelationshipCore(a, b NodeID) bool {
	if !g.nodeExistsCore(a) || !g.nodeExistsCore(b) {
		return false
	}

	_, ok := g.outgoing[a][b]
	return ok
}

func (g *stagedGraph) findOutgoingCore(from NodeID) ([]Relationship, error) {
	if !g.nodeExistsCore(from) {
		return nil, ErrNodeNotFound
	}

	relationships := make([]Relationship, 0, len(g.outgoing[from]))
	for to := range g.outgoing[from] {
		relationships = append(relationships, Relationship{From: from, To: to})
	}
	sort.Slice(relationships, func(i, j int) bool { return relationships[i].To < relationships[j].To })

	return relationships, nil
}

func (g *stagedGraph) findIncomingCore(to NodeID) ([]Relationship, error) {
	if !g.nodeExistsCore(to) {
		return nil, ErrNodeNotFound
	}

	relationships := make([]Relationship, 0, len(g.incoming[to]))
	for from := range g.incoming[to] {
		relationships = append(relationships, Relationship{From: from, To: to})
	}
	sort.Slice(relationships, func(i, j int) bool { return relationships[i].From < relationships[j].From })

	return relationships, nil
}

func (g *stagedGraph) findRelationshipsCore() []Relationship {
	total := 0
	for _, targets := range g.outgoing {
		total += len(targets)
	}

	relationships := make([]Relationship, 0, total)
	for from, targets := range g.outgoing {
		for to := range targets {
			relationships = append(relationships, Relationship{From: from, To: to})
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

func (g *stagedGraph) findNodesCore() []NodeID {
	ids := make([]NodeID, 0, len(g.nodes))
	for id := range g.nodes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	return ids
}

// NodeExists, HasRelationship, FindRelationship, FindOutgoing,
// FindIncoming, FindRelationships and FindNodes below are stagedGraph's
// guarded public reads, seeing only committed state -- exactly like
// *Graph's own public methods, and exactly like a real networked
// backend's reads see only the last committed revision, never another
// client's in-flight, not-yet-committed transaction.

func (g *stagedGraph) NodeExists(id NodeID) (bool, error) {
	release := g.guard.acquire()
	defer release()

	return g.nodeExistsCore(id), nil
}

func (g *stagedGraph) HasRelationship(a, b NodeID) (bool, error) {
	release := g.guard.acquire()
	defer release()

	return g.hasRelationshipCore(a, b), nil
}

func (g *stagedGraph) FindRelationship(from, to NodeID) (Relationship, bool, error) {
	release := g.guard.acquire()
	defer release()

	if !g.nodeExistsCore(from) {
		return Relationship{}, false, ErrNodeNotFound
	}
	if !g.nodeExistsCore(to) {
		return Relationship{}, false, ErrNodeNotFound
	}
	if !g.hasRelationshipCore(from, to) {
		return Relationship{}, false, nil
	}

	return Relationship{From: from, To: to}, true, nil
}

func (g *stagedGraph) FindOutgoing(from NodeID) ([]Relationship, error) {
	release := g.guard.acquire()
	defer release()

	return g.findOutgoingCore(from)
}

func (g *stagedGraph) FindIncoming(to NodeID) ([]Relationship, error) {
	release := g.guard.acquire()
	defer release()

	return g.findIncomingCore(to)
}

func (g *stagedGraph) FindRelationships() ([]Relationship, error) {
	release := g.guard.acquire()
	defer release()

	return g.findRelationshipsCore(), nil
}

func (g *stagedGraph) FindNodes() ([]NodeID, error) {
	release := g.guard.acquire()
	defer release()

	return g.findNodesCore(), nil
}

// RegisterChecker behaves exactly like Graph.RegisterChecker.
func (g *stagedGraph) RegisterChecker(c Checker) {
	release := g.guard.acquire()
	defer release()

	g.checkers = append(g.checkers, c)
}

// Compile-time assertion that *stagedGraph satisfies GraphAPI, mirroring
// the existing assertions for *Graph and *GraphActor.
var _ GraphAPI = (*stagedGraph)(nil)

// reserveID hands out a fresh NodeID immediately, from the shared,
// monotonic counter -- an unguarded core, only ever called from
// stagedOverlay.CreateNode while g.guard is already held for the whole
// enclosing Transact attempt. See theorystate.md section 94b: a real
// CAS backend's ID reservation is its own small atomic step, independent
// of whether the rest of the attempt's changes are ever actually
// published. If this attempt is later discarded (forceConflict, an
// error, or a declined Checker), the reserved id is simply never used by
// any node -- an abandoned reservation, exactly as a real batch-reserved
// counter would abandon an unused block; NodeIDs are never reused in
// this codebase regardless (theorystate.md section 2.2/2.3), so this can
// never collide with a later, different node.
func (g *stagedGraph) reserveID() NodeID {
	id := g.nextID
	g.nextID++

	return id
}

// runCheckers consults every registered checker against ov's merged view
// -- base as it stood when ov was created, overlaid with everything ov's
// own attempt has done so far -- exactly the theorystate.md section 95
// contract: Check observes the state fn's mutations would produce, as of
// the moment fn succeeds, never stagedGraph's real backing store
// directly, which has not been touched yet for this backend. Unguarded:
// only ever called from Transact while g.guard is already held.
func (g *stagedGraph) runCheckers(ov *stagedOverlay) error {
	return runCheckersOver(g.checkers, ov, ov.touched)
}

// publish applies every change recorded in ov to g's backing store, in
// one step -- the "commit" half of this backend's CAS-shaped Transact
// (theorystate.md section 94c). Unguarded: only ever called from
// Transact while g.guard is already held, once a whole attempt has
// succeeded, been approved by every relevant Checker, and survived
// forceConflict.
func (g *stagedGraph) publish(ov *stagedOverlay) {
	for id := range ov.createdNodes {
		g.nodes[id] = struct{}{}
		if g.outgoing[id] == nil {
			g.outgoing[id] = make(map[NodeID]struct{})
		}
		if g.incoming[id] == nil {
			g.incoming[id] = make(map[NodeID]struct{})
		}
	}

	for from, tos := range ov.addedEdges {
		for to := range tos {
			g.outgoing[from][to] = struct{}{}
			g.incoming[to][from] = struct{}{}
		}
	}

	for from, tos := range ov.removedEdges {
		for to := range tos {
			delete(g.outgoing[from], to)
			delete(g.incoming[to], from)
		}
	}

	for id := range ov.deletedNodes {
		delete(g.nodes, id)
		delete(g.outgoing, id)
		delete(g.incoming, id)
	}
}

// Transact runs the CAS-shaped attempt/retry loop this backend exists to
// exercise (theorystate.md sections 94c/97): each attempt runs fn
// against a fresh, empty stagedOverlay over the backing store's current
// committed state, and nothing fn does through that overlay is visible
// anywhere -- including to this same stagedGraph's own other reads --
// until this attempt both succeeds and is published.
//
// g.guard is acquired once for the whole call, including every retry
// forceConflict causes: from the outside, one Transact call is one
// atomic unit regardless of how many internal attempts it took, exactly
// mirroring Graph.Transact's own guard discipline. Unlike Graph.Transact,
// no data rollback is needed on failure or panic: an attempt's overlay
// never touches the backing store until publish, so a discarded overlay
// leaves no data behind to undo. Its OnRollback hooks must still run,
// though (see runAttempt).
func (g *stagedGraph) Transact(fn func(tx Tx) error) error {
	release := g.guard.acquire()
	defer release()

	for attempt := 1; ; attempt++ {
		committed, err := g.runAttempt(attempt, fn)
		if err != nil {
			return err
		}

		if committed {
			return nil
		}
	}
}

// runAttempt runs one attempt of Transact against a fresh overlay. It
// reports committed == false with a nil error when forceConflict discarded
// the attempt and Transact should rerun fn. Every attempt that is not
// published -- fn failed, a Checker declined, forceConflict discarded it, or
// anything panicked -- runs the overlay's rollback, so OnRollback hooks (for
// example NameRegistry's unstaging) run exactly as they do on the other
// backends; without this a discarded attempt's staged state would leak into
// the retry.
func (g *stagedGraph) runAttempt(attempt int, fn func(tx Tx) error) (committed bool, err error) {
	ov := newStagedOverlay(g)

	defer func() {
		if !committed {
			ov.rollback()
		}
	}()

	if fnErr := fn(ov); fnErr != nil {
		return false, fnErr
	}

	if checkErr := g.runCheckers(ov); checkErr != nil {
		return false, checkErr
	}

	if g.forceConflict != nil && g.forceConflict(attempt) {
		return false, nil
	}

	g.publish(ov)

	// Set before the hooks run: the attempt is published, so a panicking
	// hook must not run rollback hooks for it.
	committed = true

	ov.runCommitHooks()

	return true, nil
}

// stagedOverlay is the per-attempt handle stagedGraph.Transact gives fn:
// a local, in-memory buffer of every mutation this attempt has made so
// far, checked and read through exactly like a real graph would be, but
// never applied to base until the whole attempt commits (see
// stagedGraph.publish). Nested transactions (Tx.Transact) are savepoints
// over this same buffer, mirroring Txn's own mark/rollbackTo shape
// (theorystate.md section 45) -- the only difference from Txn is WHAT
// the undo log undoes: Txn's undo log reverses mutations already applied
// to the real Graph; this overlay's undo log reverses mutations already
// applied to its own local buffer, which was never visible to anything
// outside this attempt in the first place.
type stagedOverlay struct {
	base *stagedGraph

	// createdNodes/deletedNodes are this attempt's two tombstone-style
	// sets: a node created by this attempt does not yet exist in base,
	// so reads must check createdNodes; a node deleted by this attempt
	// still exists in base (until publish), so reads must check
	// deletedNodes before falling through to base at all.
	createdNodes map[NodeID]struct{}
	deletedNodes map[NodeID]struct{}

	// addedEdges/removedEdges are the relationship-level counterpart,
	// keyed the same dual-index way base.outgoing/incoming are
	// (theorystate.md section 94a): addedEdges[a][b] means this attempt
	// added (a,b); removedEdges[a][b] means this attempt removed a (a,b)
	// that was visible before this attempt touched it. The two are kept
	// mutually exclusive for any given (a,b) by AddRelationship/
	// RemoveRelationship themselves, each clearing the other's entry
	// before setting its own.
	addedEdges   map[NodeID]map[NodeID]struct{}
	removedEdges map[NodeID]map[NodeID]struct{}

	// touched mirrors Txn.touched: every NodeID this attempt's mutations
	// have involved, for Checker relevance filtering (checkerRelevant).
	touched map[NodeID]struct{}

	// txLog is this attempt's undo log -- one closure per mutation,
	// undoing that mutation's effect on this overlay's own maps (never on
	// base, which this attempt has not touched), with OnRollback hooks
	// interleaved -- and its commit-hook list, run by stagedGraph.Transact
	// only once this attempt has actually been published, never merely
	// once fn itself returns nil, since a later forceConflict-triggered
	// retry would otherwise already have run a hook for an attempt that
	// was in fact discarded.
	txLog
}

// newStagedOverlay returns an empty overlay over base, ready for one
// Transact attempt.
func newStagedOverlay(base *stagedGraph) *stagedOverlay {
	return &stagedOverlay{
		base:         base,
		createdNodes: make(map[NodeID]struct{}),
		deletedNodes: make(map[NodeID]struct{}),
		addedEdges:   make(map[NodeID]map[NodeID]struct{}),
		removedEdges: make(map[NodeID]map[NodeID]struct{}),
	}
}

// Compile-time assertions that *stagedOverlay satisfies Tx (and
// therefore GraphReader), mirroring the existing assertions for *Txn.
var _ Tx = (*stagedOverlay)(nil)
var _ GraphReader = (*stagedOverlay)(nil)

func (ov *stagedOverlay) Touch(ids ...NodeID) {
	ov.touched = touchNodes(ov.touched, ids...)
}

// nodeExists reports whether id exists in the state this attempt has
// produced so far: created-this-attempt nodes exist, deleted-this-
// attempt nodes do not, and everything else falls through to base's
// last-committed state, read through base's unguarded core (see the
// core-methods doc comment on stagedGraph above).
func (ov *stagedOverlay) nodeExists(id NodeID) bool {
	if _, deleted := ov.deletedNodes[id]; deleted {
		return false
	}
	if _, created := ov.createdNodes[id]; created {
		return true
	}

	return ov.base.nodeExistsCore(id)
}

func (ov *stagedOverlay) NodeExists(id NodeID) (bool, error) {
	return ov.nodeExists(id), nil
}

func (ov *stagedOverlay) hasRelationship(a, b NodeID) bool {
	if !ov.nodeExists(a) || !ov.nodeExists(b) {
		return false
	}
	if _, removed := ov.removedEdges[a][b]; removed {
		return false
	}
	if _, added := ov.addedEdges[a][b]; added {
		return true
	}

	return ov.base.hasRelationshipCore(a, b)
}

func (ov *stagedOverlay) HasRelationship(a, b NodeID) (bool, error) {
	return ov.hasRelationship(a, b), nil
}

func (ov *stagedOverlay) FindRelationship(from, to NodeID) (Relationship, bool, error) {
	if !ov.nodeExists(from) {
		return Relationship{}, false, ErrNodeNotFound
	}
	if !ov.nodeExists(to) {
		return Relationship{}, false, ErrNodeNotFound
	}
	if !ov.hasRelationship(from, to) {
		return Relationship{}, false, nil
	}

	return Relationship{From: from, To: to}, true, nil
}

// outgoingSet returns from's merged outgoing targets under this overlay:
// base's own targets (skipped entirely if from was created this attempt,
// since base then has nothing for it yet), plus this attempt's
// additions, minus this attempt's removals. Callers must already know
// from exists under this overlay (ov.nodeExists(from)).
func (ov *stagedOverlay) outgoingSet(from NodeID) (map[NodeID]struct{}, error) {
	result := make(map[NodeID]struct{})

	if _, created := ov.createdNodes[from]; !created {
		baseRels, err := ov.base.findOutgoingCore(from)
		if err != nil {
			return nil, err
		}
		for _, rel := range baseRels {
			result[rel.To] = struct{}{}
		}
	}

	for to := range ov.addedEdges[from] {
		result[to] = struct{}{}
	}
	for to := range ov.removedEdges[from] {
		delete(result, to)
	}

	return result, nil
}

// incomingSet is outgoingSet's reverse-direction counterpart.
func (ov *stagedOverlay) incomingSet(to NodeID) (map[NodeID]struct{}, error) {
	result := make(map[NodeID]struct{})

	if _, created := ov.createdNodes[to]; !created {
		baseRels, err := ov.base.findIncomingCore(to)
		if err != nil {
			return nil, err
		}
		for _, rel := range baseRels {
			result[rel.From] = struct{}{}
		}
	}

	for from, tos := range ov.addedEdges {
		if _, ok := tos[to]; ok {
			result[from] = struct{}{}
		}
	}
	for from, tos := range ov.removedEdges {
		if _, ok := tos[to]; ok {
			delete(result, from)
		}
	}

	return result, nil
}

func (ov *stagedOverlay) FindOutgoing(from NodeID) ([]Relationship, error) {
	if !ov.nodeExists(from) {
		return nil, ErrNodeNotFound
	}

	set, err := ov.outgoingSet(from)
	if err != nil {
		return nil, err
	}

	relationships := make([]Relationship, 0, len(set))
	for to := range set {
		relationships = append(relationships, Relationship{From: from, To: to})
	}
	sort.Slice(relationships, func(i, j int) bool { return relationships[i].To < relationships[j].To })

	return relationships, nil
}

func (ov *stagedOverlay) FindIncoming(to NodeID) ([]Relationship, error) {
	if !ov.nodeExists(to) {
		return nil, ErrNodeNotFound
	}

	set, err := ov.incomingSet(to)
	if err != nil {
		return nil, err
	}

	relationships := make([]Relationship, 0, len(set))
	for from := range set {
		relationships = append(relationships, Relationship{From: from, To: to})
	}
	sort.Slice(relationships, func(i, j int) bool { return relationships[i].From < relationships[j].From })

	return relationships, nil
}

func (ov *stagedOverlay) FindRelationships() ([]Relationship, error) {
	merged := make(map[NodeID]map[NodeID]struct{})

	for _, rel := range ov.base.findRelationshipsCore() {
		if _, deleted := ov.deletedNodes[rel.From]; deleted {
			continue
		}
		if _, deleted := ov.deletedNodes[rel.To]; deleted {
			continue
		}
		if merged[rel.From] == nil {
			merged[rel.From] = make(map[NodeID]struct{})
		}
		merged[rel.From][rel.To] = struct{}{}
	}

	for from, tos := range ov.removedEdges {
		for to := range tos {
			delete(merged[from], to)
		}
	}

	for from, tos := range ov.addedEdges {
		if merged[from] == nil {
			merged[from] = make(map[NodeID]struct{})
		}
		for to := range tos {
			merged[from][to] = struct{}{}
		}
	}

	relationships := make([]Relationship, 0)
	for from, tos := range merged {
		for to := range tos {
			relationships = append(relationships, Relationship{From: from, To: to})
		}
	}
	sort.Slice(relationships, func(i, j int) bool {
		if relationships[i].From != relationships[j].From {
			return relationships[i].From < relationships[j].From
		}
		return relationships[i].To < relationships[j].To
	})

	return relationships, nil
}

func (ov *stagedOverlay) FindNodes() ([]NodeID, error) {
	merged := make(map[NodeID]struct{})
	for _, id := range ov.base.findNodesCore() {
		merged[id] = struct{}{}
	}
	for id := range ov.deletedNodes {
		delete(merged, id)
	}
	for id := range ov.createdNodes {
		merged[id] = struct{}{}
	}

	ids := make([]NodeID, 0, len(merged))
	for id := range merged {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	return ids, nil
}

// CreateNode reserves a fresh NodeID from the shared backing counter
// immediately (see stagedGraph.reserveID) and records it as created by
// this attempt.
func (ov *stagedOverlay) CreateNode() (NodeID, error) {
	id := ov.base.reserveID()

	ov.createdNodes[id] = struct{}{}
	ov.Touch(id)

	ov.undo = append(ov.undo, func() {
		delete(ov.createdNodes, id)
	})

	return id, nil
}

// AddRelationship records (a,b) as added by this attempt, if it is not
// already visible (from base or an earlier step of this same attempt).
func (ov *stagedOverlay) AddRelationship(a, b NodeID) (created bool, err error) {
	if !ov.nodeExists(a) {
		return false, ErrNodeNotFound
	}
	if !ov.nodeExists(b) {
		return false, ErrNodeNotFound
	}
	if ov.hasRelationship(a, b) {
		return false, nil
	}

	if ov.addedEdges[a] == nil {
		ov.addedEdges[a] = make(map[NodeID]struct{})
	}
	ov.addedEdges[a][b] = struct{}{}

	wasRemoved := false
	if _, ok := ov.removedEdges[a][b]; ok {
		delete(ov.removedEdges[a], b)
		wasRemoved = true
	}

	ov.Touch(a, b)

	ov.undo = append(ov.undo, func() {
		delete(ov.addedEdges[a], b)
		if wasRemoved {
			if ov.removedEdges[a] == nil {
				ov.removedEdges[a] = make(map[NodeID]struct{})
			}
			ov.removedEdges[a][b] = struct{}{}
		}
	})

	return true, nil
}

// RemoveRelationship records (a,b) as removed by this attempt, if it is
// currently visible, symmetric with AddRelationship above.
func (ov *stagedOverlay) RemoveRelationship(a, b NodeID) (removed bool, err error) {
	if !ov.nodeExists(a) {
		return false, ErrNodeNotFound
	}
	if !ov.nodeExists(b) {
		return false, ErrNodeNotFound
	}
	if !ov.hasRelationship(a, b) {
		return false, nil
	}

	wasAdded := false
	if _, ok := ov.addedEdges[a][b]; ok {
		delete(ov.addedEdges[a], b)
		wasAdded = true
	}

	if ov.removedEdges[a] == nil {
		ov.removedEdges[a] = make(map[NodeID]struct{})
	}
	ov.removedEdges[a][b] = struct{}{}

	ov.Touch(a, b)

	ov.undo = append(ov.undo, func() {
		delete(ov.removedEdges[a], b)
		if wasAdded {
			if ov.addedEdges[a] == nil {
				ov.addedEdges[a] = make(map[NodeID]struct{})
			}
			ov.addedEdges[a][b] = struct{}{}
		}
	})

	return true, nil
}

// DeleteNode records id as deleted by this attempt, requiring id to
// currently have zero relationships in either direction under this
// overlay's own merged view -- exactly Graph.DeleteNode's own
// precondition (theorystate.md section 18), just checked against the
// state this attempt would produce rather than against a real graph
// directly.
func (ov *stagedOverlay) DeleteNode(id NodeID) error {
	if !ov.nodeExists(id) {
		return ErrNodeNotFound
	}

	outgoing, err := ov.outgoingSet(id)
	if err != nil {
		return err
	}
	if len(outgoing) != 0 {
		return ErrNodeNotEmpty
	}

	incoming, err := ov.incomingSet(id)
	if err != nil {
		return err
	}
	if len(incoming) != 0 {
		return ErrNodeNotEmpty
	}

	ov.Touch(id)

	if _, created := ov.createdNodes[id]; created {
		delete(ov.createdNodes, id)

		ov.undo = append(ov.undo, func() {
			ov.createdNodes[id] = struct{}{}
		})

		return nil
	}

	ov.deletedNodes[id] = struct{}{}

	ov.undo = append(ov.undo, func() {
		delete(ov.deletedNodes, id)
	})

	return nil
}

// The methods below exist ONLY in test builds, exactly mirroring the
// test-only CreateNode/AddRelationship/RemoveRelationship/DeleteNode
// wrappers already defined above for *GraphActor and *RootGraph: GraphAPI
// itself exposes no raw writes (theorystate.md section 92), so these run
// as one-operation Transact calls via the same createNodeVia/
// addRelationshipVia/removeRelationshipVia/deleteNodeVia helpers already
// used for those two types, meaning Checkers run for these too, exactly
// like the GraphActor/RootGraph versions (and unlike *Graph's own
// test-only versions, which deliberately bypass Checkers for the
// out-of-band adversarial tests).

func (g *stagedGraph) CreateNode() (NodeID, error) {
	return createNodeVia(g)
}

func (g *stagedGraph) AddRelationship(a, b NodeID) (bool, error) {
	return addRelationshipVia(g, a, b)
}

func (g *stagedGraph) RemoveRelationship(a, b NodeID) (bool, error) {
	return removeRelationshipVia(g, a, b)
}

func (g *stagedGraph) DeleteNode(id NodeID) error {
	return deleteNodeVia(g, id)
}

// rawWrite implements rawBackend (see testutil_test.go): step runs against a
// fresh overlay that is published at once, so the write is applied exactly as
// a committed attempt would apply it, but with no Checker pass, no
// forceConflict and no hooks. The overlay supplies the same existence and
// delete-only-if-empty rules every other write has.
func (g *stagedGraph) rawWrite(step func(w txOps) error) error {
	release := g.guard.acquire()
	defer release()

	ov := newStagedOverlay(g)

	if stepErr := step(ov); stepErr != nil {
		return wrapInterfaceErr(stepErr)
	}

	g.publish(ov)

	return nil
}

// rawStagedGraph is the stagedGraph the registry fixtures get (see
// testBackends): a *stagedGraph whose four direct write methods bypass
// Checkers. Embedding the pointer keeps every other method promoted.
type rawStagedGraph struct {
	*stagedGraph
}

func (g rawStagedGraph) CreateNode() (NodeID, error) {
	return rawCreateNode(g)
}

func (g rawStagedGraph) AddRelationship(a, b NodeID) (bool, error) {
	return rawAddRelationship(g, a, b)
}

func (g rawStagedGraph) RemoveRelationship(a, b NodeID) (bool, error) {
	return rawRemoveRelationship(g, a, b)
}

func (g rawStagedGraph) DeleteNode(id NodeID) error {
	return rawDeleteNode(g, id)
}

// Transact implements Transactor for a staged attempt already in
// progress: fn runs as a nested transaction, a savepoint over this same
// overlay, mirroring Txn.Transact exactly (theorystate.md section 45) --
// only what fn did since this call is undone on failure or panic, and
// Checkers never run here (only once, at the outermost
// stagedGraph.Transact commit).
func (ov *stagedOverlay) Transact(fn func(nested Tx) error) error {
	return ov.runNested(ov, fn)
}

// The following tests exercise stagedGraph/stagedOverlay (theorystate.md
// sections 93-97): a second, structurally different GraphAPI
// implementation used to prove that existing registries are portable
// across backends, not merely documented as if they are.

func TestStagedGraphBasicOperations(t *testing.T) {
	g := newStagedGraph()

	a, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}

	b, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for b: %v", err)
	}

	if !mustNodeExists(t, g, a) || !mustNodeExists(t, g, b) {
		t.Fatal("created nodes do not both exist")
	}

	created, err := g.AddRelationship(a, b)
	if err != nil {
		t.Fatalf("AddRelationship(a,b): %v", err)
	}
	if !created {
		t.Fatal("AddRelationship(a,b) reported that nothing was created")
	}

	if !mustHasRelationship(t, g, a, b) {
		t.Fatal("HasRelationship(a,b) = false, want true")
	}

	relationship, exists, err := g.FindRelationship(a, b)
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

	outgoing, err := g.FindOutgoing(a)
	if err != nil {
		t.Fatalf("FindOutgoing(a): %v", err)
	}
	if !reflect.DeepEqual(outgoing, []Relationship{want}) {
		t.Fatalf("FindOutgoing(a) = %v, want %v", outgoing, []Relationship{want})
	}

	incoming, err := g.FindIncoming(b)
	if err != nil {
		t.Fatalf("FindIncoming(b): %v", err)
	}
	if !reflect.DeepEqual(incoming, []Relationship{want}) {
		t.Fatalf("FindIncoming(b) = %v, want %v", incoming, []Relationship{want})
	}

	all := mustFindRelationships(t, g)
	if !reflect.DeepEqual(all, []Relationship{want}) {
		t.Fatalf("FindRelationships() = %v, want %v", all, []Relationship{want})
	}

	removed, err := g.RemoveRelationship(a, b)
	if err != nil {
		t.Fatalf("RemoveRelationship(a,b): %v", err)
	}
	if !removed {
		t.Fatal("RemoveRelationship(a,b) reported that nothing was removed")
	}

	if mustHasRelationship(t, g, a, b) {
		t.Fatal("relationship still exists after removal")
	}

	if err2 := g.DeleteNode(a); err2 != nil {
		t.Fatalf("DeleteNode(a): %v", err2)
	}
	if mustNodeExists(t, g, a) {
		t.Fatal("node a still exists after DeleteNode()")
	}
}

// TestStagedGraphFailedTransactLeavesNoTrace confirms a failed attempt's
// writes never reach the backing store -- the baseline correctness
// property this backend must have before ForceConflict-driven retries
// (TestStagedGraphForceConflictRerunsFn) can mean anything.
func TestStagedGraphFailedTransactLeavesNoTrace(t *testing.T) {
	g := newStagedGraph()
	errBoom := errors.New("boom")

	var id NodeID

	err := g.Transact(func(tx Tx) error {
		var createErr error
		id, createErr = createNodeTx(tx)
		if createErr != nil {
			return createErr
		}

		return errBoom
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("Transact() error = %v, want %v", err, errBoom)
	}

	if mustNodeExists(t, g, id) {
		t.Fatalf("node %d created by a failed attempt is visible in the backing store", id)
	}
}

// TestStagedGraphForceConflictRerunsFn exercises the theorystate.md
// section 91 "fn may be executed more than once" clause directly and
// deterministically: forceConflict discards the first attempt's overlay
// entirely, as a losing real CAS commit would, and Transact reruns fn
// from scratch. Nothing from the discarded first attempt -- including
// the NodeID it reserved -- may leak into the final, committed state.
func TestStagedGraphForceConflictRerunsFn(t *testing.T) {
	g := newStagedGraph()
	g.forceConflict = func(attempt int) bool { return attempt == 1 }

	var ids []NodeID

	err := g.Transact(func(tx Tx) error {
		id, err2 := createNodeTx(tx)
		if err2 != nil {
			return err2
		}
		ids = append(ids, id)

		return nil
	})
	if err != nil {
		t.Fatalf("Transact() error = %v", err)
	}

	if len(ids) != 2 {
		t.Fatalf("fn ran %d time(s), want exactly 2 (one forced conflict, one real commit)", len(ids))
	}

	firstAttemptID, secondAttemptID := ids[0], ids[1]

	if mustNodeExists(t, g, firstAttemptID) {
		t.Fatalf("node %d from the discarded first attempt is visible in the backing store", firstAttemptID)
	}
	if !mustNodeExists(t, g, secondAttemptID) {
		t.Fatalf("node %d from the committed second attempt does not exist", secondAttemptID)
	}

	if got := len(mustFindNodes(t, g)); got != 1 {
		t.Fatalf("FindNodes() has %d node(s), want exactly 1 (the discarded attempt's node must not linger)", got)
	}
}

// TestStagedGraphNestedTransactRollsBackOnlyInnerSteps is the
// stagedOverlay counterpart of TestNestedTransactFailureRollsBackOnlyTheInnerSteps:
// the overlay's own mark/rollbackTo mechanism must behave identically to
// Txn's, even though it operates on a local buffer that has never
// touched the real backing store, rather than on the store directly.
func TestStagedGraphNestedTransactRollsBackOnlyInnerSteps(t *testing.T) {
	g := newStagedGraph()
	errInner := errors.New("inner failure")

	var kept, dropped NodeID

	err := g.Transact(func(tx Tx) error {
		var txErr error
		kept, txErr = createNodeTx(tx)
		if txErr != nil {
			return txErr
		}

		innerErr := tx.Transact(func(inner Tx) error {
			var createErr error
			dropped, createErr = createNodeTx(inner)
			if createErr != nil {
				return createErr
			}

			if linkErr := addRelationshipTx(inner, kept, dropped); linkErr != nil {
				return linkErr
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

	if !mustNodeExists(t, g, kept) {
		t.Fatal("the outer step was lost although only the nested transaction failed")
	}
	if mustNodeExists(t, g, dropped) {
		t.Fatal("the nested step survived its own failed transaction")
	}
	if mustHasRelationship(t, g, kept, dropped) {
		t.Fatal("a relationship added inside the failed nested transaction survived")
	}
}

// TestStagedGraphCheckerDeclineLeavesNoTrace confirms a declined Checker
// discards the whole attempt, exactly like TestCheckerPanicRollsBackAndPropagates
// does for *Graph -- here there is nothing to actively roll back, since
// nothing was ever applied to the backing store in the first place.
func TestStagedGraphCheckerDeclineLeavesNoTrace(t *testing.T) {
	g := newStagedGraph()

	tag, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for tag: %v", err)
	}

	errVeto := errors.New("veto")
	g.RegisterChecker(Checker{
		Name: "veto",
		Tags: []NodeID{tag},
		Check: func(_ GraphReader, _ map[NodeID]struct{}) error {
			return errVeto
		},
	})

	var id NodeID

	err = g.Transact(func(tx Tx) error {
		var createErr error
		id, createErr = createNodeTx(tx)
		if createErr != nil {
			return createErr
		}

		return addRelationshipTx(tx, tag, id)
	})
	if !errors.Is(err, errVeto) {
		t.Fatalf("Transact() error = %v, want %v", err, errVeto)
	}

	if mustNodeExists(t, g, id) {
		t.Fatalf("node %d created by a declined attempt is visible in the backing store", id)
	}
	if mustHasRelationship(t, g, tag, id) {
		t.Fatal("relationship created by a declined attempt is visible in the backing store")
	}
}

// TestStagedGraphOnCommitRunsExactlyOnceDespiteForceConflict pins down a
// correctness property specific to this backend's commit shape
// (theorystate.md section 94c): OnCommit hooks must run exactly once,
// only when an attempt is actually published, never once per attempt.
// Each retry gets a brand-new stagedOverlay (see stagedGraph.Transact),
// so a discarded attempt's own OnCommit registrations are discarded with
// it -- without that, a forceConflict-discarded attempt's hook would
// already have run before the retry that actually commits ran its own,
// double-firing a hook meant to run exactly once per successful
// Transact call.
func TestStagedGraphOnCommitRunsExactlyOnceDespiteForceConflict(t *testing.T) {
	g := newStagedGraph()
	g.forceConflict = func(attempt int) bool { return attempt == 1 }

	runs := 0

	err := g.Transact(func(tx Tx) error {
		tx.OnCommit(func() { runs++ })
		return nil
	})
	if err != nil {
		t.Fatalf("Transact() error = %v", err)
	}

	if runs != 1 {
		t.Fatalf("OnCommit hook ran %d time(s), want exactly 1 (once, for the attempt that survives forceConflict and is actually published)", runs)
	}
}

// TestStagedGraphPointerRegistryPortability is the payoff theorystate.md
// section 97 exists for: PointerRegistry and NameRegistry are exercised
// here completely unchanged -- no special-casing, no backend-specific
// code path -- against stagedGraph instead of *Graph, confirming that
// depending only on the Tx/GraphAPI interface, as every registry in this
// file already does, is sufficient for portability across a structurally
// different backend mechanism.
func TestStagedGraphPointerRegistryPortability(t *testing.T) {
	g := newStagedGraph()
	names := NewNameRegistry(g)

	ids, err := names.BootstrapNames(g, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	pointers, err := NewPointerRegistry(g, ids[NameAllPointers])
	if err != nil {
		t.Fatalf("NewPointerRegistry(): %v", err)
	}

	p, err := pointers.NewPointer(g)
	if err != nil {
		t.Fatalf("NewPointer(): %v", err)
	}

	x, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for x: %v", err)
	}

	if err2 := pointers.SetTarget(g, p, x); err2 != nil {
		t.Fatalf("SetTarget(p, x): %v", err2)
	}

	target, hasTarget, err := pointers.Target(g, p)
	if err != nil {
		t.Fatalf("Target(p): %v", err)
	}
	if !hasTarget || target != x {
		t.Fatalf("Target(p) = (%d,%v), want (%d,true)", target, hasTarget, x)
	}

	y, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for y: %v", err)
	}

	if err3 := pointers.SetTarget(g, p, y); err3 != nil {
		t.Fatalf("SetTarget(p, y): %v", err3)
	}

	if mustHasRelationship(t, g, p, x) {
		t.Fatal("old target relationship survived a replacement SetTarget()")
	}

	target, hasTarget, err = pointers.Target(g, p)
	if err != nil {
		t.Fatalf("Target(p) after replace: %v", err)
	}
	if !hasTarget || target != y {
		t.Fatalf("Target(p) = (%d,%v), want (%d,true)", target, hasTarget, y)
	}
}

// TestGraphActorOverStagedGraphBasicOperations confirms GraphActor
// (theorystate.md section 89c) works unchanged over stagedGraph, not
// only over *Graph: NewGraphActor takes a GraphAPI, and stagedGraph is
// one, so nothing about GraphActor's own logic should need its backend
// to be *Graph specifically.
func TestGraphActorOverStagedGraphBasicOperations(t *testing.T) {
	actor := NewGraphActor(newStagedGraph())
	defer actor.Close()

	a, err := actor.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for a: %v", err)
	}

	b, err := actor.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for b: %v", err)
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

	err2 := actor.DeleteNode(b)
	if !errors.Is(err2, ErrNodeNotEmpty) {
		t.Fatalf("DeleteNode(b) error = %v, want %v (b still has an incoming relationship)", err2, ErrNodeNotEmpty)
	}
}

// TestStagedGraphRollbackHooksRunForEveryDiscardedAttempt pins down that
// stagedGraph runs OnRollback hooks for an attempt that is not published,
// whatever the reason: a forced conflict, an error from fn, or a panic.
func TestStagedGraphRollbackHooksRunForEveryDiscardedAttempt(t *testing.T) {
	g := newStagedGraph()

	var events []string

	register := func(tx Tx) {
		tx.OnRollback(func() { events = append(events, "rollback") })
		tx.OnCommit(func() { events = append(events, "commit") })
	}

	// The first attempt is discarded by a forced conflict: its rollback
	// hook runs, then the retry commits.
	g.forceConflict = func(attempt int) bool { return attempt == 1 }

	err := g.Transact(func(tx Tx) error {
		register(tx)
		return nil
	})
	if err != nil {
		t.Fatalf("Transact(conflict, then commit) error = %v", err)
	}
	if want := []string{"rollback", "commit"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("hooks ran %v, want %v", events, want)
	}

	g.forceConflict = nil
	events = nil
	errBoom := errors.New("boom")

	err = g.Transact(func(tx Tx) error {
		register(tx)
		return errBoom
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("Transact(fail) error = %v, want %v", err, errBoom)
	}
	if want := []string{"rollback"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("hooks ran %v after a failed attempt, want %v", events, want)
	}

	events = nil

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected the panic to propagate out of Transact()")
			}
		}()

		//nolint:errcheck // the closure panics before Transact can return
		_ = g.Transact(func(tx Tx) error {
			register(tx)
			panic("boom")
		})
	}()

	if want := []string{"rollback"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("hooks ran %v after a panicking attempt, want %v", events, want)
	}
}

// TestStagedGraphDiscardedAttemptsUnstageNameBindings is the NameRegistry
// consequence of the test above: a binding staged by a discarded attempt
// must not leak into the retry (it used to fail with
// ErrNameBoundToDeletedNode, since the staged node only existed in the
// discarded overlay).
func TestStagedGraphDiscardedAttemptsUnstageNameBindings(t *testing.T) {
	g := newStagedGraph()
	g.forceConflict = func(attempt int) bool { return attempt == 1 }

	names := NewNameRegistry(g)

	id, err := names.CreateNamedNode(g, "A")
	if err != nil {
		t.Fatalf("CreateNamedNode() error = %v (the discarded attempt's staged binding leaked into the retry)", err)
	}

	if found, ok := names.Lookup("A"); !ok || found != id {
		t.Fatalf("Lookup(\"A\") = (%d,%v), want (%d,true)", found, ok, id)
	}
	if !mustNodeExists(t, g, id) {
		t.Fatalf("node %d does not exist after the committed retry", id)
	}
	if got := len(mustFindNodes(t, g)); got != 1 {
		t.Fatalf("FindNodes() has %d node(s), want exactly 1", got)
	}

	requireNoStagedNames(t, names)
}
