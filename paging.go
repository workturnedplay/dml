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

// readViewer is implemented by a GraphReader that can run several reads
// against one consistent view of the graph without a write transaction
// (BoltGraph: one bolt read transaction; GraphActor: one job on the actor's
// goroutine; the ROOT overlay and the host's guarded graph forward to what
// they wrap). It lets code that needs more than one read to agree with itself
// (a handler of a remote read operation, theorystate.md section 116) avoid a
// Transact, which on a durable backend costs a commit. Use readView, not this
// interface directly. fn must read only through the reader it is given: it
// must not call Transact, or any method of the graph view was called on.
type readViewer interface {
	view(fn func(reader GraphReader) error) error
}

// readView runs fn against one consistent view of graph. If graph provides
// one (see readViewer) fn runs inside it; otherwise graph itself is that view
// (a Tx, which sees its own writes, and the memory backends, where one
// goroutine at a time makes consecutive reads agree). fn's error is returned.
func readView(graph GraphReader, fn func(reader GraphReader) error) error {
	if viewer, ok := graph.(readViewer); ok {
		return wrapInterfaceErr(viewer.view(fn))
	}

	return fn(graph)
}

// commonChildrenFinder is implemented by a GraphReader that can find the
// children two nodes have in common without reading either node's whole
// child set (*Graph, BoltGraph's boltView and, through forwarding, the ROOT
// overlay and GraphActor). It makes "the child of this node that carries
// this tag" cost about the smaller of the two child sets instead of the
// node's own: a list node has one child per element and a tag hub one child
// per tagged node, so the hub is usually the smaller. a must exist
// (ErrNodeNotFound); a missing b has no children, so nothing is in common.
// At most limit nodes are returned, sorted ascending; if more than limit are
// common, which of them is unspecified. A non-positive limit returns
// nothing. An implementation that cannot answer a request natively returns
// errPagingUnsupported (a signal, not a failure) and the caller scans one
// child set instead (scanTaggedChild).
type commonChildrenFinder interface {
	findCommonChildren(a, b NodeID, limit int) ([]NodeID, error)
}

// Compile-time assertions that every reader with a native common-children
// lookup provides it.
var (
	_ commonChildrenFinder = (*Graph)(nil)
	_ commonChildrenFinder = (*Txn)(nil)
	_ commonChildrenFinder = graphCoreReader{}
	_ commonChildrenFinder = boltView{}
	_ commonChildrenFinder = (*boltTxn)(nil)
	_ commonChildrenFinder = (*BoltGraph)(nil)
	_ commonChildrenFinder = rootReader{}
	_ commonChildrenFinder = (*GraphActor)(nil)
)

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

// hasOutgoing reports whether node has at least one outgoing relationship. It
// reads a page of one, so with a native pager it does not read every child
// whatever the node's fan-out.
func hasOutgoing(reader GraphReader, node NodeID) (bool, error) {
	page, err := outgoingPages(reader, node, 1).next()

	return len(page) > 0, err
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
