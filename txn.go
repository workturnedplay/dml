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

// findCommonChildren delegates to the real, concrete *Graph's unguarded
// core (see commonChildrenFinder and the NodeExists doc comment above).
func (tx *Txn) findCommonChildren(a, b NodeID, limit int) ([]NodeID, error) {
	return tx.graph.findCommonChildrenCore(a, b, limit)
}

// Compile-time assertion that *Txn satisfies GraphReader, exactly
// mirroring the existing assertions for *Graph and *GraphActor against
// GraphAPI.
var _ GraphReader = (*Txn)(nil)
