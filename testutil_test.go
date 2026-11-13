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
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
)

// TestMain enables GraphActor's debug-only reentrancy tripwire
// (theorystate.md section 90) for this entire test binary, so any test
// anywhere in this file that reintroduces the misplaced-storage bug
// class the tripwire exists to catch -- a registry reading through a
// stored graph reference instead of through the tx/g value it was
// actually handed -- panics loudly and immediately instead of hanging
// forever.
func TestMain(m *testing.M) {
	EnableGraphActorReentrancyDetection()
	os.Exit(m.Run())
}

// The methods below exist ONLY in test builds. Production code has no
// way to mutate a graph outside Transact (theorystate.md section 92), but
// these tests need direct writes for setup and to simulate foreign or
// loaded data that never passed through this process's Checkers.
// Defining them here, rather than rewriting every test call site, keeps
// the production API strict and the escape hatch visibly test-only.
//
// On *Graph they call the unexported cores directly, so they bypass
// Checkers exactly as the old exported methods did (the out-of-band
// adversarial tests depend on that). *GraphActor and *RootGraph have no
// core to call, so their versions run as one-operation transactions
// (Checkers run); on *RootGraph the transaction handle presents the ROOT
// overlay, so the overlay's write rules are still what these tests
// exercise.

func (g *Graph) CreateNode() (NodeID, error) {
	release := g.guard.acquire()
	defer release()

	return g.createNodeCore()
}

func (g *Graph) AddRelationship(a, b NodeID) (created bool, err error) {
	release := g.guard.acquire()
	defer release()

	return g.addRelationshipCore(a, b)
}

func (g *Graph) RemoveRelationship(a, b NodeID) (removed bool, err error) {
	release := g.guard.acquire()
	defer release()

	return g.removeRelationshipCore(a, b)
}

func (g *Graph) DeleteNode(id NodeID) error {
	release := g.guard.acquire()
	defer release()

	return g.deleteNodeCore(id)
}

func createNodeVia(graph Transactor) (NodeID, error) {
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		return createNodeTx(tx)
	})
}

func addRelationshipVia(graph Transactor, a, b NodeID) (bool, error) {
	return transactBool(graph, func(tx Tx) (bool, error) {
		created, err := tx.AddRelationship(a, b)
		return created, wrapInterfaceErr(err)
	})
}

func removeRelationshipVia(graph Transactor, a, b NodeID) (bool, error) {
	return transactBool(graph, func(tx Tx) (bool, error) {
		removed, err := tx.RemoveRelationship(a, b)
		return removed, wrapInterfaceErr(err)
	})
}

func deleteNodeVia(graph Transactor, id NodeID) error {
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		return deleteNodeTx(tx, id)
	}))
}

func (ga *GraphActor) CreateNode() (NodeID, error) {
	return createNodeVia(ga)
}

func (ga *GraphActor) AddRelationship(a, b NodeID) (bool, error) {
	return addRelationshipVia(ga, a, b)
}

func (ga *GraphActor) RemoveRelationship(a, b NodeID) (bool, error) {
	return removeRelationshipVia(ga, a, b)
}

func (ga *GraphActor) DeleteNode(id NodeID) error {
	return deleteNodeVia(ga, id)
}

func (r *RootGraph) CreateNode() (NodeID, error) {
	return createNodeVia(r)
}

func (r *RootGraph) AddRelationship(a, b NodeID) (bool, error) {
	return addRelationshipVia(r, a, b)
}

func (r *RootGraph) RemoveRelationship(a, b NodeID) (bool, error) {
	return removeRelationshipVia(r, a, b)
}

func (r *RootGraph) DeleteNode(id NodeID) error {
	return deleteNodeVia(r, id)
}

// sortedNodeIDs returns a sorted copy of ids, for comparing test results
// against results whose order is documented as unspecified (e.g.
// CapsuleRegistry.CapsulesWithValue, ListRegistry.OccurrencesOf).
func sortedNodeIDs(ids []NodeID) []NodeID {
	sorted := make([]NodeID, len(ids))
	copy(sorted, ids)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted
}

// The must* helpers below wrap the GraphReader/registry methods that
// gained an error return (NodeExists, HasRelationship, FindRelationships,
// FindNodes, and the IsX family), failing the test immediately if the
// call itself errors, and otherwise returning exactly what the wrapped
// method reported. Every purely in-memory backend in this file never
// actually returns a non-nil error here; these exist so the hundreds of
// top-level test assertions calling these methods don't each need their
// own "if err != nil { t.Fatalf(...) }" line. They must NEVER be called
// from inside a Transact or Checker.Check closure: such a closure may run
// on a GraphActor's own dedicated worker goroutine rather than the test's
// own, and calling t.Fatalf from any goroutine other than the one running
// the test is unsupported by the testing package. Call sites inside such
// a closure instead propagate the error by returning it, exactly like
// every other error already flowing out of that closure.

func mustNodeExists(t *testing.T, g GraphReader, id NodeID) bool {
	t.Helper()

	exists, err := g.NodeExists(id)
	if err != nil {
		t.Fatalf("NodeExists(%d): %v", id, err)
	}

	return exists
}

func mustHasRelationship(t *testing.T, g GraphReader, a, b NodeID) bool {
	t.Helper()

	has, err := g.HasRelationship(a, b)
	if err != nil {
		t.Fatalf("HasRelationship(%d,%d): %v", a, b, err)
	}

	return has
}

func mustFindRelationships(t *testing.T, g GraphReader) []Relationship {
	t.Helper()

	relationships, err := g.FindRelationships()
	if err != nil {
		t.Fatalf("FindRelationships(): %v", err)
	}

	return relationships
}

func mustFindNodes(t *testing.T, g GraphReader) []NodeID {
	t.Helper()

	ids, err := g.FindNodes()
	if err != nil {
		t.Fatalf("FindNodes(): %v", err)
	}

	return ids
}

func mustIsPointer(t *testing.T, p *PointerRegistry, g GraphReader, id NodeID) bool {
	t.Helper()

	is, err := p.IsPointer(g, id)
	if err != nil {
		t.Fatalf("IsPointer(%d): %v", id, err)
	}

	return is
}

func mustIsCapsule(t *testing.T, c *CapsuleRegistry, g GraphReader, id NodeID) bool {
	t.Helper()

	is, err := c.IsCapsule(g, id)
	if err != nil {
		t.Fatalf("IsCapsule(%d): %v", id, err)
	}

	return is
}

func mustIsList(t *testing.T, l OrderedList, g GraphReader, id NodeID) bool {
	t.Helper()

	is, err := l.IsList(g, id)
	if err != nil {
		t.Fatalf("IsList(%d): %v", id, err)
	}

	return is
}

func mustIsSet(t *testing.T, s *SetRegistry, g GraphReader, id NodeID) bool {
	t.Helper()

	is, err := s.IsSet(g, id)
	if err != nil {
		t.Fatalf("IsSet(%d): %v", id, err)
	}

	return is
}

func mustIsCompositeSet(t *testing.T, c *CompositeSetRegistry, g GraphReader, id NodeID) bool {
	t.Helper()

	is, err := c.IsCompositeSet(g, id)
	if err != nil {
		t.Fatalf("IsCompositeSet(%d): %v", id, err)
	}

	return is
}

func mustIsCompositeSetLog(t *testing.T, c *CompositeSetLogRegistry, g GraphReader, id NodeID) bool {
	t.Helper()

	is, err := c.IsCompositeSetLog(g, id)
	if err != nil {
		t.Fatalf("IsCompositeSetLog(%d): %v", id, err)
	}

	return is
}

// mustCreateNode creates a node through any Transactor, failing t on error.
func mustCreateNode(t *testing.T, api Transactor) NodeID {
	t.Helper()

	id, err := createNodeVia(api)
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	return id
}

// The following tests cover theorystate.md section 86: a domain
// pointer's validity depends on its domain's membership, so the shared
// commit-time domain Checker must decline any transaction that would
// strand a pointer outside its domain, for both Representation B and D.

// newTestNode creates a fresh node in g, failing t on error.
func newTestNode(t *testing.T, g testGraph) NodeID {
	t.Helper()

	id, err := g.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(): %v", err)
	}

	return id
}

// testBackend is one GraphAPI implementation to run a shared test against.
// open returns a testGraph (the GraphAPI plus the test-only write methods),
// which is what the registry fixtures need (see newTestGraph).
type testBackend struct {
	name string
	open func(tb *testing.T) testGraph
}

// testBackends lists every GraphAPI implementation tests run against. The
// first entry, the bare *Graph, is the default backend (activeTestBackend).
func testBackends() []testBackend {
	return []testBackend{
		{name: "Graph", open: func(_ *testing.T) testGraph { return &Graph{} }},
		{name: "stagedGraph", open: func(_ *testing.T) testGraph { return newStagedGraph() }},
		{name: "BoltGraph", open: func(tb *testing.T) testGraph { return newBoltTestGraph(tb) }},
	}
}

// forEachBackendAndActor runs body once per GraphAPI implementation, directly
// and behind a GraphActor.
func forEachBackendAndActor(t *testing.T, body func(t *testing.T, graph testGraph)) {
	t.Helper()

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

				body(t, graph)
			})
		}
	}
}

// runConcurrently runs fn(i) for every i in [0, n) on its own goroutine
// and waits for all of them.
func runConcurrently(n int, fn func(i int)) {
	var wg sync.WaitGroup

	for i := range n {
		wg.Go(func() { fn(i) })
	}

	wg.Wait()
}

// The methods below exist ONLY in test builds, exactly like the ones for
// stagedGraph: GraphAPI has no raw writes, so these run as one-operation
// transactions and Checkers run for them.

func (g *BoltGraph) CreateNode() (NodeID, error) {
	return createNodeVia(g)
}

func (g *BoltGraph) AddRelationship(a, b NodeID) (bool, error) {
	return addRelationshipVia(g, a, b)
}

func (g *BoltGraph) RemoveRelationship(a, b NodeID) (bool, error) {
	return removeRelationshipVia(g, a, b)
}

func (g *BoltGraph) DeleteNode(id NodeID) error {
	return deleteNodeVia(g, id)
}

func boltTestPath(t *testing.T) string {
	t.Helper()

	return filepath.Join(t.TempDir(), "dml.db")
}

// openBoltTestGraph opens the store at path and closes it when the test
// ends (Close is idempotent, so tests may also close it themselves).
func openBoltTestGraph(t *testing.T, path string) *BoltGraph {
	t.Helper()

	g, err := OpenBoltGraph(path)
	if err != nil {
		t.Fatalf("OpenBoltGraph(%q): %v", path, err)
	}

	t.Cleanup(func() {
		if closeErr := g.Close(); closeErr != nil {
			t.Errorf("Close(): %v", closeErr)
		}
	})

	return g
}

func newBoltTestGraph(t *testing.T) *BoltGraph {
	t.Helper()

	return openBoltTestGraph(t, boltTestPath(t))
}
