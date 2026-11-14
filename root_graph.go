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
	"sort"
)

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
	return requireAllExist(v.inner, ids...)
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

// findCommonChildren forwards to the underlying reader for any two nodes
// other than ROOT: their stored children are their children in the ROOT view
// (only ROOT's own outgoing set is virtual, section 12a). ROOT's children, or
// an underlying reader with no native lookup, are reported as
// errPagingUnsupported, and the caller scans.
func (v rootReader) findCommonChildren(a, b NodeID, limit int) ([]NodeID, error) {
	if a == v.root || b == v.root {
		return nil, errPagingUnsupported
	}

	finder, ok := v.inner.(commonChildrenFinder)
	if !ok {
		return nil, errPagingUnsupported
	}

	common, err := finder.findCommonChildren(a, b, limit)

	return common, wrapInterfaceErr(err)
}

// view runs fn against the underlying reader's view (see readView), handing it
// a reader that presents the same ROOT overlay as v does.
func (v rootReader) view(fn func(reader GraphReader) error) error {
	return readView(v.inner, func(inner GraphReader) error {
		return fn(rootReader{inner: inner, root: v.root})
	})
}

// Compile-time assertion that the ROOT overlay gives read views.
var _ readViewer = rootReader{}

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
	if existsErr := requireExists(s, id); existsErr != nil {
		return existsErr
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

	if existsErr := requireExists(graph, root); existsErr != nil {
		return nil, existsErr
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
