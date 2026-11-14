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
