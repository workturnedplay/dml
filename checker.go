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
	"fmt"
)

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

// findCommonChildren delegates to graph's unguarded core. See the
// graphCoreReader doc comment.
func (r graphCoreReader) findCommonChildren(a, b NodeID, limit int) ([]NodeID, error) {
	return r.graph.findCommonChildrenCore(a, b, limit)
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
