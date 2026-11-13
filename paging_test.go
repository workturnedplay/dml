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
	"slices"
	"testing"
)

func TestPageNodeIDs(t *testing.T) {
	sorted := []NodeID{0, 2, 4, 6, 8}
	maxLimit := int(^uint(0) >> 1)

	tests := []struct {
		name     string
		after    NodeID
		hasAfter bool
		limit    int
		want     []NodeID
	}{
		{name: "first page", limit: 2, want: []NodeID{0, 2}},
		{name: "zero is a valid cursor", after: 0, hasAfter: true, limit: 2, want: []NodeID{2, 4}},
		{name: "cursor between IDs", after: 3, hasAfter: true, limit: 2, want: []NodeID{4, 6}},
		{name: "cursor at the last ID", after: 8, hasAfter: true, limit: 2, want: []NodeID{}},
		{name: "cursor past the end", after: 100, hasAfter: true, limit: 2, want: []NodeID{}},
		{name: "limit larger than what remains", after: 4, hasAfter: true, limit: 100, want: []NodeID{6, 8}},
		{name: "huge limit does not overflow", after: 0, hasAfter: true, limit: maxLimit, want: []NodeID{2, 4, 6, 8}},
		{name: "zero limit", limit: 0, want: []NodeID{}},
		{name: "negative limit", limit: -5, want: []NodeID{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := pageNodeIDs(sorted, tc.after, tc.hasAfter, tc.limit)
			if len(got) != len(tc.want) || (len(got) > 0 && !reflect.DeepEqual(got, tc.want)) {
				t.Fatalf("pageNodeIDs() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBoltGraphFindNodesAfterMatchesFindNodes(t *testing.T) {
	g := newBoltTestGraph(t)

	for range 7 {
		mustCreateNode(t, g)
	}

	all := mustFindNodes(t, g)

	// Every page size and every cursor must agree with slicing FindNodes.
	for limit := 1; limit <= len(all)+1; limit++ {
		var paged []NodeID

		var after NodeID

		hasAfter := false

		for {
			page, pageErr := boltRead(g, func(v boltView) ([]NodeID, error) { return v.findNodesAfter(after, hasAfter, limit) })
			if pageErr != nil {
				t.Fatalf("findNodesAfter(): %v", pageErr)
			}
			paged = append(paged, page...)

			if len(page) < limit {
				break
			}

			after = page[len(page)-1]
			hasAfter = true
		}

		if !reflect.DeepEqual(paged, all) {
			t.Fatalf("paging with limit %d gave %v, want %v", limit, paged, all)
		}
	}
}

func TestBoltGraphFindOutgoingAfterMatchesFindOutgoing(t *testing.T) {
	g := newBoltTestGraph(t)

	from := mustCreateNode(t, g)

	for range 7 {
		to := mustCreateNode(t, g)
		if _, err := g.AddRelationship(from, to); err != nil {
			t.Fatalf("AddRelationship(from, to): %v", err)
		}
	}

	all, err := g.FindOutgoing(from)
	if err != nil {
		t.Fatalf("FindOutgoing(from): %v", err)
	}

	for limit := 1; limit <= len(all)+1; limit++ {
		var paged []Relationship

		var after NodeID

		hasAfter := false

		for {
			page, pageErr := boltRead(g, func(v boltView) ([]Relationship, error) {
				return v.findOutgoingAfter(from, after, hasAfter, limit)
			})
			if pageErr != nil {
				t.Fatalf("findOutgoingAfter() with limit %d: %v", limit, pageErr)
			}
			paged = append(paged, page...)

			if len(page) < limit {
				break
			}

			after = page[len(page)-1].To
			hasAfter = true
		}

		if !reflect.DeepEqual(paged, all) {
			t.Fatalf("paging with limit %d gave %v, want %v", limit, paged, all)
		}
	}
}

func TestBoltGraphFindIncomingAfterMatchesFindIncoming(t *testing.T) {
	g := newBoltTestGraph(t)

	to := mustCreateNode(t, g)

	for range 7 {
		from := mustCreateNode(t, g)
		if _, err := g.AddRelationship(from, to); err != nil {
			t.Fatalf("AddRelationship(from, to): %v", err)
		}
	}

	all, err := g.FindIncoming(to)
	if err != nil {
		t.Fatalf("FindIncoming(to): %v", err)
	}

	for limit := 1; limit <= len(all)+1; limit++ {
		var paged []Relationship

		var after NodeID

		hasAfter := false

		for {
			page, pageErr := boltRead(g, func(v boltView) ([]Relationship, error) {
				return v.findIncomingAfter(to, after, hasAfter, limit)
			})
			if pageErr != nil {
				t.Fatalf("findIncomingAfter() with limit %d: %v", limit, pageErr)
			}
			paged = append(paged, page...)

			if len(page) < limit {
				break
			}

			after = page[len(page)-1].From
			hasAfter = true
		}

		if !reflect.DeepEqual(paged, all) {
			t.Fatalf("paging with limit %d gave %v, want %v", limit, paged, all)
		}
	}
}

func collectPages(t *testing.T, it *pageIterator[Relationship]) []Relationship {
	t.Helper()

	var all []Relationship

	for {
		page, err := it.next()
		if err != nil {
			t.Fatalf("next(): %v", err)
		}

		if len(page) == 0 {
			return all
		}

		all = append(all, page...)
	}
}

// TestPageIteratorsMatchFullReadsOnEveryBackendAndThroughGraphActor checks
// both iterators against the unpaged reads on every backend, directly and
// behind a GraphActor: native paging where there is one, the snapshot
// fallback where there is not.
func TestPageIteratorsMatchFullReadsOnEveryBackendAndThroughGraphActor(t *testing.T) {
	for _, backend := range testBackends() {
		for _, viaActor := range []bool{false, true} {
			name := backend.name
			if viaActor {
				name += "/GraphActor"
			}

			t.Run(name, func(t *testing.T) {
				graph := backend.open(t)

				if viaActor {
					actor := NewGraphActor(graph)
					t.Cleanup(actor.Close)

					graph = actor
				}

				hub := mustCreateNode(t, graph)

				for range 7 {
					spoke := mustCreateNode(t, graph)

					if _, err := addRelationshipVia(graph, hub, spoke); err != nil {
						t.Fatalf("AddRelationship(hub, spoke): %v", err)
					}
					if _, err := addRelationshipVia(graph, spoke, hub); err != nil {
						t.Fatalf("AddRelationship(spoke, hub): %v", err)
					}
				}

				wantOut, err := graph.FindOutgoing(hub)
				if err != nil {
					t.Fatalf("FindOutgoing(hub): %v", err)
				}

				wantIn, err2 := graph.FindIncoming(hub)
				if err2 != nil {
					t.Fatalf("FindIncoming(hub): %v", err2)
				}

				for limit := 1; limit <= len(wantOut)+1; limit++ {
					if got := collectPages(t, outgoingPages(graph, hub, limit)); !reflect.DeepEqual(got, wantOut) {
						t.Fatalf("outgoingPages(limit %d) = %v, want %v", limit, got, wantOut)
					}

					if got := collectPages(t, incomingPages(graph, hub, limit)); !reflect.DeepEqual(got, wantIn) {
						t.Fatalf("incomingPages(limit %d) = %v, want %v", limit, got, wantIn)
					}
				}
			})
		}
	}
}

func TestFindCommonChildrenMatchesTheIntersectionOnEveryBackend(t *testing.T) {
	const nonexistent NodeID = 999999

	forEachBackendAndActor(t, func(t *testing.T, graph testGraph) {
		finder, ok := graph.(commonChildrenFinder)
		if !ok {
			t.Skip("no native common-children lookup")
		}

		left := mustCreateNode(t, graph)
		right := mustCreateNode(t, graph)
		loner := mustCreateNode(t, graph)

		kids := make([]NodeID, 0, 20)
		for range 20 {
			kids = append(kids, mustCreateNode(t, graph))
		}

		for _, kid := range kids {
			if _, linkErr := addRelationshipVia(graph, left, kid); linkErr != nil {
				t.Fatalf("AddRelationship(left, %d): %v", kid, linkErr)
			}
		}

		// Spread out, so a leapfrog has to skip.
		shared := []NodeID{kids[3], kids[10], kids[19]}

		for _, kid := range shared {
			if _, linkErr := addRelationshipVia(graph, right, kid); linkErr != nil {
				t.Fatalf("AddRelationship(right, %d): %v", kid, linkErr)
			}
		}

		if _, probeErr := finder.findCommonChildren(left, right, 1); errors.Is(probeErr, errPagingUnsupported) {
			t.Skip("the backend underneath has no native common-children lookup")
		}

		cases := []struct {
			name  string
			a, b  NodeID
			limit int
			want  []NodeID
			err   error
		}{
			{name: "left and right", a: left, b: right, limit: 100, want: shared},
			{name: "right and left", a: right, b: left, limit: 100, want: shared},
			{name: "a node with itself", a: left, b: left, limit: 100, want: kids},
			{name: "disjoint", a: left, b: loner, limit: 100},
			{name: "zero limit", a: left, b: right, limit: 0},
			{name: "negative limit", a: left, b: right, limit: -1},
			{name: "missing first node", a: nonexistent, b: right, limit: 100, err: ErrNodeNotFound},
			{name: "missing second node", a: left, b: nonexistent, limit: 100},
		}

		for _, tc := range cases {
			got, err := finder.findCommonChildren(tc.a, tc.b, tc.limit)
			if !errors.Is(err, tc.err) {
				t.Fatalf("%s: error = %v, want %v", tc.name, err, tc.err)
			}

			if len(got) != len(tc.want) || (len(got) > 0 && !reflect.DeepEqual(got, tc.want)) {
				t.Fatalf("%s: findCommonChildren() = %v, want %v", tc.name, got, tc.want)
			}
		}

		capped, cappedErr := finder.findCommonChildren(left, right, 2)
		if cappedErr != nil || len(capped) != 2 {
			t.Fatalf("findCommonChildren(limit 2) = (%v,%v), want two nodes", capped, cappedErr)
		}

		for _, kid := range capped {
			if !slices.Contains(shared, kid) {
				t.Fatalf("findCommonChildren(limit 2) returned %d, which is not a common child", kid)
			}
		}
	})
}

func TestFindUniqueTaggedChildAgreesWithAScanOnEveryBackend(t *testing.T) {
	const nonexistent NodeID = 999999

	finders := []struct {
		name string
		find func(GraphReader, NodeID, NodeID) (NodeID, bool, error)
	}{
		{name: "findUniqueTaggedChild", find: findUniqueTaggedChild},
		{name: "scanTaggedChild", find: scanTaggedChild},
	}

	forEachBackendAndActor(t, func(t *testing.T, graph testGraph) {
		node := mustCreateNode(t, graph)
		tag := mustCreateNode(t, graph)
		outsider := mustCreateNode(t, graph)

		// The tag has a child that is not a child of node: an intersection,
		// not a union.
		if _, linkErr := addRelationshipVia(graph, tag, outsider); linkErr != nil {
			t.Fatalf("AddRelationship(tag, outsider): %v", linkErr)
		}

		kids := make([]NodeID, 0, 6)

		for range 6 {
			kid := mustCreateNode(t, graph)

			if _, linkErr := addRelationshipVia(graph, node, kid); linkErr != nil {
				t.Fatalf("AddRelationship(node, %d): %v", kid, linkErr)
			}

			kids = append(kids, kid)
		}

		check := func(label string, from, wantChild NodeID, wantFound bool, wantErr error) {
			t.Helper()

			for _, impl := range finders {
				child, found, err := impl.find(graph, from, tag)
				if !errors.Is(err, wantErr) || found != wantFound || child != wantChild {
					t.Fatalf("%s: %s = (%d,%v,%v), want (%d,%v,%v)", label, impl.name, child, found, err, wantChild, wantFound, wantErr)
				}
			}
		}

		tagKid := func(kid NodeID) {
			t.Helper()

			if _, linkErr := addRelationshipVia(graph, tag, kid); linkErr != nil {
				t.Fatalf("AddRelationship(tag, %d): %v", kid, linkErr)
			}
		}

		check("nothing tagged", node, 0, false, nil)

		tagKid(kids[2])
		check("one tagged", node, kids[2], true, nil)

		tagKid(kids[4])
		check("two tagged", node, 0, false, ErrAmbiguousPointerMetadata)

		check("missing node", nonexistent, 0, false, ErrNodeNotFound)
	})
}

// countingReader counts FindIncoming calls for one watched node. Embedding
// the GraphReader interface deliberately hides any optional pager the
// wrapped value has, so this reader looks like a backend with no paging.
type countingReader struct {
	GraphReader
	watched           NodeID
	findIncomingCalls int
}

func (r *countingReader) FindIncoming(to NodeID) ([]Relationship, error) {
	if to == r.watched {
		r.findIncomingCalls++
	}

	relationships, err := r.GraphReader.FindIncoming(to)

	return relationships, wrapInterfaceErr(err)
}

// unsupportedPagerReader additionally claims incomingPager but always
// reports errPagingUnsupported, like a forwarding layer over a backend
// that cannot page.
type unsupportedPagerReader struct {
	countingReader
	nativeCalls int
}

func (r *unsupportedPagerReader) findIncomingAfter(_, _ NodeID, _ bool, _ int) ([]Relationship, error) {
	r.nativeCalls++

	return nil, errPagingUnsupported
}

// TestCapsulesWithValueFallsBackToOneFullReadPerCall is the regression
// test for the quadratic fallback: over a reader with no paging, and over
// one whose pager reports errPagingUnsupported, CapsulesWithValue must
// read the watched node's incoming relationships exactly once, however
// many internal pages it walks.
func TestCapsulesWithValueFallsBackToOneFullReadPerCall(t *testing.T) {
	original := capsulesWithValuePageSize
	capsulesWithValuePageSize = 3
	t.Cleanup(func() { capsulesWithValuePageSize = original })

	g, capsules := newCapsuleTestFixture(t)

	value, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode() for value: %v", err)
	}

	const occurrences = 7

	for range occurrences {
		if _, err2 := capsules.NewCapsule(g, value); err2 != nil {
			t.Fatalf("NewCapsule(): %v", err2)
		}
	}

	plain := &countingReader{GraphReader: g, watched: value}

	got, err := capsules.CapsulesWithValue(plain, value)
	if err != nil {
		t.Fatalf("CapsulesWithValue(plain): %v", err)
	}
	if len(got) != occurrences {
		t.Fatalf("CapsulesWithValue(plain) returned %d capsules, want %d", len(got), occurrences)
	}
	if plain.findIncomingCalls != 1 {
		t.Fatalf("FindIncoming(value) ran %d times, want exactly 1 (a full read per page is quadratic)", plain.findIncomingCalls)
	}

	unsupported := &unsupportedPagerReader{countingReader: countingReader{GraphReader: g, watched: value}}

	got, err = capsules.CapsulesWithValue(unsupported, value)
	if err != nil {
		t.Fatalf("CapsulesWithValue(unsupported): %v", err)
	}
	if len(got) != occurrences {
		t.Fatalf("CapsulesWithValue(unsupported) returned %d capsules, want %d", len(got), occurrences)
	}
	if unsupported.nativeCalls != 1 || unsupported.findIncomingCalls != 1 {
		t.Fatalf("native pager called %d times and FindIncoming %d times, want 1 and 1", unsupported.nativeCalls, unsupported.findIncomingCalls)
	}
}

// TestRootReaderFindOutgoingAfterMatchesFindOutgoing exercises
// rootReader.findOutgoingAfter (theorystate.md section 105): ROOT's own
// virtual outgoing set pages over existing node IDs rather than over any
// stored relationship, so this deliberately creates ROOT after a couple of
// other nodes and creates a few more afterwards, putting ROOT's own NodeID
// in the middle of the ID space paged over -- exactly the case that would
// silently shorten a page if filtering ROOT back out (the overlay's own
// irreflexivity) were not compensated for.
func TestRootReaderFindOutgoingAfterMatchesFindOutgoing(t *testing.T) {
	g := newBoltTestGraph(t)

	mustCreateNode(t, g)
	mustCreateNode(t, g)
	root := mustCreateNode(t, g)

	for range 4 {
		mustCreateNode(t, g)
	}

	r, err := NewRootGraph(g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	all, err := r.FindOutgoing(root)
	if err != nil {
		t.Fatalf("FindOutgoing(root): %v", err)
	}

	for limit := 1; limit <= len(all)+1; limit++ {
		var paged []Relationship

		var after NodeID

		hasAfter := false

		for {
			page, pageErr := r.findOutgoingAfter(root, after, hasAfter, limit)
			if pageErr != nil {
				t.Fatalf("findOutgoingAfter() with limit %d: %v", limit, pageErr)
			}
			paged = append(paged, page...)

			if len(page) < limit {
				break
			}

			after = page[len(page)-1].To
			hasAfter = true
		}

		if !reflect.DeepEqual(paged, all) {
			t.Fatalf("paging with limit %d gave %v, want %v", limit, paged, all)
		}
	}
}

// TestRootReaderFindOutgoingAfterReportsUnsupportedOverNonPagingBackend
// pins down the fix for the ROOT overlay silently re-fetching on every
// page: over a backend that cannot page, it must say so, and the iterator
// must then fall back to one full read and still return the right answer.
func TestRootReaderFindOutgoingAfterReportsUnsupportedOverNonPagingBackend(t *testing.T) {
	var g Graph

	root := newTestNode(t, &g)
	other := newTestNode(t, &g)

	for range 4 {
		newTestNode(t, &g)
	}

	r, err := NewRootGraph(&g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	for _, from := range []NodeID{root, other} {
		if _, pageErr := r.findOutgoingAfter(from, 0, false, 2); !errors.Is(pageErr, errPagingUnsupported) {
			t.Fatalf("findOutgoingAfter(%d) error = %v, want %v", from, pageErr, errPagingUnsupported)
		}
	}

	want, err := r.FindOutgoing(root)
	if err != nil {
		t.Fatalf("FindOutgoing(root): %v", err)
	}

	if got := collectPages(t, outgoingPages(r, root, 2)); !reflect.DeepEqual(got, want) {
		t.Fatalf("outgoingPages(root) = %v, want %v", got, want)
	}
}

// TestRootReaderFindOutgoingAfterOfMissingRootIsNotFound: a paged read of
// ROOT's outgoing set must answer like FindOutgoing(ROOT) when ROOT no
// longer exists, not with an empty page.
func TestRootReaderFindOutgoingAfterOfMissingRootIsNotFound(t *testing.T) {
	g := newBoltTestGraph(t)

	root := mustCreateNode(t, g)
	mustCreateNode(t, g)

	r, err := NewRootGraph(g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	// Deleted through the underlying graph, bypassing RootGraph's
	// ErrCannotDeleteRoot protection.
	if deleteErr := g.DeleteNode(root); deleteErr != nil {
		t.Fatalf("raw DeleteNode(ROOT): %v", deleteErr)
	}

	if _, pageErr := r.findOutgoingAfter(root, 0, false, 2); !errors.Is(pageErr, ErrNodeNotFound) {
		t.Fatalf("findOutgoingAfter(deleted ROOT) error = %v, want %v", pageErr, ErrNodeNotFound)
	}
}

// TestRootReaderFindOutgoingAfterToleratesHugeLimit: the page limit is
// only an upper bound and must never be used to size an allocation.
func TestRootReaderFindOutgoingAfterToleratesHugeLimit(t *testing.T) {
	g := newBoltTestGraph(t)

	root := mustCreateNode(t, g)
	a := mustCreateNode(t, g)

	r, err := NewRootGraph(g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	got, err := r.findOutgoingAfter(root, 0, false, int(^uint(0)>>1))
	if err != nil {
		t.Fatalf("findOutgoingAfter() with the largest possible limit: %v", err)
	}

	if want := []Relationship{{From: root, To: a}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("findOutgoingAfter() = %v, want %v", got, want)
	}
}

// TestRootReaderFindIncomingAfterMatchesFindIncoming exercises
// rootReader.findIncomingAfter (theorystate.md section 105). ROOT's NodeID
// sits in the middle of the sources paged over, so the virtual (ROOT, to)
// parent has to be spliced into the middle of a page, and a physically
// stored (ROOT, hub) and (ROOT, ROOT) must be hidden rather than duplicated
// or leaked. Every page size must agree with the unpaged FindIncoming.
func TestRootReaderFindIncomingAfterMatchesFindIncoming(t *testing.T) {
	g := newBoltTestGraph(t)

	low1 := mustCreateNode(t, g)
	low2 := mustCreateNode(t, g)
	root := mustCreateNode(t, g)
	high1 := mustCreateNode(t, g)
	high2 := mustCreateNode(t, g)
	hub := mustCreateNode(t, g)

	for _, from := range []NodeID{low1, low2, root, high1, high2} {
		if _, addErr := g.AddRelationship(from, hub); addErr != nil {
			t.Fatalf("AddRelationship(%d, hub): %v", from, addErr)
		}
	}

	for _, from := range []NodeID{low1, high1, root} {
		if _, addErr := g.AddRelationship(from, root); addErr != nil {
			t.Fatalf("AddRelationship(%d, root): %v", from, addErr)
		}
	}

	r, newErr := NewRootGraph(g, root)
	if newErr != nil {
		t.Fatalf("NewRootGraph(): %v", newErr)
	}

	maxLimit := int(^uint(0) >> 1)

	for _, tc := range []struct {
		name string
		to   NodeID
	}{
		{name: "hub", to: hub},
		{name: "root", to: root},
	} {
		t.Run(tc.name, func(t *testing.T) {
			all, allErr := r.FindIncoming(tc.to)
			if allErr != nil {
				t.Fatalf("FindIncoming(%d): %v", tc.to, allErr)
			}

			for limit := 1; limit <= len(all)+1; limit++ {
				var paged []Relationship

				var after NodeID

				hasAfter := false

				for {
					page, pageErr := r.findIncomingAfter(tc.to, after, hasAfter, limit)
					if pageErr != nil {
						t.Fatalf("findIncomingAfter() with limit %d: %v", limit, pageErr)
					}
					paged = append(paged, page...)

					if len(page) < limit {
						break
					}

					after = page[len(page)-1].From
					hasAfter = true
				}

				if !reflect.DeepEqual(paged, all) {
					t.Fatalf("paging with limit %d gave %v, want %v", limit, paged, all)
				}
			}

			huge, hugeErr := r.findIncomingAfter(tc.to, 0, false, maxLimit)
			if hugeErr != nil {
				t.Fatalf("findIncomingAfter() with the largest possible limit: %v", hugeErr)
			}
			if !reflect.DeepEqual(huge, all) {
				t.Fatalf("findIncomingAfter() with the largest possible limit = %v, want %v", huge, all)
			}
		})
	}
}

// TestRootReaderFindIncomingAfterReportsUnsupportedOverNonPagingBackend:
// over a backend that cannot page, the overlay must say so, and the
// iterator must then fall back to one full read and still be right.
func TestRootReaderFindIncomingAfterReportsUnsupportedOverNonPagingBackend(t *testing.T) {
	var g Graph

	root := newTestNode(t, &g)
	a := newTestNode(t, &g)
	hub := newTestNode(t, &g)

	if _, addErr := g.AddRelationship(a, hub); addErr != nil {
		t.Fatalf("AddRelationship(a, hub): %v", addErr)
	}

	r, err := NewRootGraph(&g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	if _, pageErr := r.findIncomingAfter(hub, 0, false, 2); !errors.Is(pageErr, errPagingUnsupported) {
		t.Fatalf("findIncomingAfter() error = %v, want %v", pageErr, errPagingUnsupported)
	}

	want, err := r.FindIncoming(hub)
	if err != nil {
		t.Fatalf("FindIncoming(hub): %v", err)
	}

	if got := collectPages(t, incomingPages(r, hub, 1)); !reflect.DeepEqual(got, want) {
		t.Fatalf("incomingPages(hub) = %v, want %v", got, want)
	}
}

// TestRootReaderFindIncomingAfterWithoutRootNodeHasNoVirtualParent: with
// ROOT deleted through the raw graph the overlay adds nothing virtual (the
// same answer as FindIncoming), and a missing target is still
// ErrNodeNotFound.
func TestRootReaderFindIncomingAfterWithoutRootNodeHasNoVirtualParent(t *testing.T) {
	g := newBoltTestGraph(t)

	root := mustCreateNode(t, g)
	a := mustCreateNode(t, g)
	hub := mustCreateNode(t, g)

	if _, addErr := g.AddRelationship(a, hub); addErr != nil {
		t.Fatalf("AddRelationship(a, hub): %v", addErr)
	}

	r, err := NewRootGraph(g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	// Deleted through the underlying graph, bypassing RootGraph's
	// ErrCannotDeleteRoot protection.
	if deleteErr := g.DeleteNode(root); deleteErr != nil {
		t.Fatalf("raw DeleteNode(ROOT): %v", deleteErr)
	}

	got, err := r.findIncomingAfter(hub, 0, false, 5)
	if err != nil {
		t.Fatalf("findIncomingAfter(): %v", err)
	}
	if want := []Relationship{{From: a, To: hub}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("findIncomingAfter() = %v, want %v", got, want)
	}

	const missing NodeID = 999999

	if _, pageErr := r.findIncomingAfter(missing, 0, false, 5); !errors.Is(pageErr, ErrNodeNotFound) {
		t.Fatalf("findIncomingAfter(missing) error = %v, want %v", pageErr, ErrNodeNotFound)
	}
}

// TestRootGraphInsideGraphActorPagesIncomingNatively checks the production
// stack: the actor forwards to the RootGraph, whose promoted
// findIncomingAfter now pages natively over the BoltGraph underneath, so
// the first page must not report errPagingUnsupported.
func TestRootGraphInsideGraphActorPagesIncomingNatively(t *testing.T) {
	g := newBoltTestGraph(t)

	root := mustCreateNode(t, g)
	a := mustCreateNode(t, g)
	b := mustCreateNode(t, g)
	hub := mustCreateNode(t, g)

	rootGraph, err := NewRootGraph(g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	actor := NewGraphActor(rootGraph)
	defer actor.Close()

	for _, from := range []NodeID{a, b} {
		if _, addErr := addRelationshipVia(actor, from, hub); addErr != nil {
			t.Fatalf("AddRelationship(%d, hub): %v", from, addErr)
		}
	}

	if _, pageErr := actor.findIncomingAfter(hub, 0, false, 1); pageErr != nil {
		t.Fatalf("findIncomingAfter() through the actor: %v (want native paging)", pageErr)
	}

	want, err := actor.FindIncoming(hub)
	if err != nil {
		t.Fatalf("FindIncoming(hub): %v", err)
	}

	for limit := 1; limit <= len(want)+1; limit++ {
		if got := collectPages(t, incomingPages(actor, hub, limit)); !reflect.DeepEqual(got, want) {
			t.Fatalf("incomingPages(limit %d) = %v, want %v", limit, got, want)
		}
	}
}
