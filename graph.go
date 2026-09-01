package wtw

import (
	"errors"
	"sort"
)

type NodeID uint64

var (
	ErrNodeNotFound         = errors.New("node not found")
	ErrNodeNotEmpty         = errors.New("node has relationships")
	ErrRelationshipExist    = errors.New("relationship already exists")
	ErrRelationshipNotFound = errors.New("relationship not found")
	ErrNodeIDExhausted      = errors.New("node ID space exhausted")
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
	nextID NodeID

	nodes map[NodeID]struct{}

	// outgoing[A][B] means the primitive relationship (A, B) exists.
	outgoing map[NodeID]map[NodeID]struct{}

	// incoming[B][A] means the primitive relationship (A, B) exists.
	incoming map[NodeID]map[NodeID]struct{}
}

// CreateNode creates a new node and returns its NodeID.
//
// NodeID 0 is reserved as "no NodeID". IDs currently increase
// monotonically. Reuse of deleted IDs is deliberately not implemented
// yet; the theory leaves the exact representation open.
func (g *Graph) CreateNode() (NodeID, error) {
	g.ensureInitialized()

	if g.nextID == ^NodeID(0) {
		return 0, ErrNodeIDExhausted
	}

	id := g.nextID
	g.nextID++

	g.nodes[id] = struct{}{}
	g.outgoing[id] = make(map[NodeID]struct{})
	g.incoming[id] = make(map[NodeID]struct{})

	return id, nil
}

// NodeExists reports whether id currently identifies an existing node.
func (g *Graph) NodeExists(id NodeID) bool {
	return id != 0 && g.nodeExists(id)
}

// AddRelationship creates the primitive relationship (a, b).
//
// Both nodes must already exist.
//
// Relationships are unique. Adding the same relationship twice does not
// create a second relationship; the returned bool is false on the second
// attempt.
//
// The bool reports whether a new relationship was actually created.
func (g *Graph) AddRelationship(a, b NodeID) (bool, error) {
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
func (g *Graph) RemoveRelationship(a, b NodeID) (bool, error) {
	if !g.nodeExists(a) || !g.nodeExists(b) {
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

// Children returns all X for which (a, X) exists.
//
// The returned slice has no semantic ordering. It is sorted only so that
// callers and tests receive deterministic output.
func (g *Graph) Children(a NodeID) ([]NodeID, error) {
	if !g.nodeExists(a) {
		return nil, ErrNodeNotFound
	}

	children := make([]NodeID, 0, len(g.outgoing[a]))
	for child := range g.outgoing[a] {
		children = append(children, child)
	}

	sort.Slice(children, func(i, j int) bool {
		return children[i] < children[j]
	})

	return children, nil
}

// Parents returns all X for which (X, a) exists.
//
// The returned slice has no semantic ordering. It is sorted only so that
// callers and tests receive deterministic output.
func (g *Graph) Parents(a NodeID) ([]NodeID, error) {
	if !g.nodeExists(a) {
		return nil, ErrNodeNotFound
	}

	parents := make([]NodeID, 0, len(g.incoming[a]))
	for parent := range g.incoming[a] {
		parents = append(parents, parent)
	}

	sort.Slice(parents, func(i, j int) bool {
		return parents[i] < parents[j]
	})

	return parents, nil
}

// DeleteNode deletes a node only when it has no relationships.
//
// Atomic cascade deletion is deliberately not part of this primitive API.
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

func (g *Graph) ensureInitialized() {
	if g.nextID == 0 {
		g.nextID = 1
	}

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
