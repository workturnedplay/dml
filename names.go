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
	"fmt"
	"sort"
	"sync"
)

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
	if existsErr := requireExists(tx, id); existsErr != nil {
		return false, existsErr
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
