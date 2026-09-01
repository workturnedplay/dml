package wtw

import (
	"errors"
	"sort"
)

type NodeID uint64

const NoNode NodeID = 0

type Relationship struct {
	From NodeID
	To   NodeID
}

var (
	ErrNodeNotFound    = errors.New("node not found")
	ErrNodeNotEmpty    = errors.New("node has relationships")
	ErrNodeIDExhausted = errors.New("node ID space exhausted")
)

// Graph is the primitive graph.
//
// Semantically, it consists of:
//   - existing NodeIDs
//   - unique directed relationships (A, B)
//
// The separate outgoing/incoming maps are implementation indexes.
// They are not additional semantic primitives.
//
// NodeID 0 is reserved as NoNode. In relationship queries, NoNode is
// also used as a wildcard.
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
// IDs currently increase monotonically. Reuse of deleted IDs is
// deliberately not implemented yet.
func (g *Graph) CreateNode() (NodeID, error) {
	g.ensureInitialized()

	if g.nextID == ^NodeID(0) {
		return NoNode, ErrNodeIDExhausted
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
	return id != NoNode && g.nodeExists(id)
}

// AddRelationship creates the primitive relationship (a, b).
//
// Both nodes must already exist.
//
// Relationships are unique. Adding the same relationship again simply
// reports created=false.
func (g *Graph) AddRelationship(a, b NodeID) (created bool, err error) {
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
func (g *Graph) RemoveRelationship(a, b NodeID) (removed bool, err error) {
	if !g.nodeExists(a) {
		return false, ErrNodeNotFound
	}
	if !g.nodeExists(b) {
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

// FindRelationships finds primitive relationships matching the supplied
// endpoints.
//
// NoNode (0) means "wildcard":
//
//	FindRelationships(0, B) -> all (X, B)
//	FindRelationships(A, 0) -> all (A, X)
//	FindRelationships(A, B) -> the exact fact (A, B), if it exists
//	FindRelationships(0, 0) -> all relationships
//
// Returned relationships are sorted by From, then To. This ordering is
// only deterministic presentation order; it has no semantic meaning.
func (g *Graph) FindRelationships(from, to NodeID) ([]Relationship, error) {
	if from != NoNode && !g.nodeExists(from) {
		return nil, ErrNodeNotFound
	}

	if to != NoNode && !g.nodeExists(to) {
		return nil, ErrNodeNotFound
	}

	var relationships []Relationship

	switch {
	case from != NoNode && to != NoNode:
		if _, exists := g.outgoing[from][to]; exists {
			relationships = []Relationship{
				{From: from, To: to},
			}
		}

	case from != NoNode:
		relationships = make([]Relationship, 0, len(g.outgoing[from]))

		for target := range g.outgoing[from] {
			relationships = append(relationships, Relationship{
				From: from,
				To:   target,
			})
		}

	case to != NoNode:
		relationships = make([]Relationship, 0, len(g.incoming[to]))

		for source := range g.incoming[to] {
			relationships = append(relationships, Relationship{
				From: source,
				To:   to,
			})
		}

	default:
		total := 0
		for _, targets := range g.outgoing {
			total += len(targets)
		}

		relationships = make([]Relationship, 0, total)

		for source, targets := range g.outgoing {
			for target := range targets {
				relationships = append(relationships, Relationship{
					From: source,
					To:   target,
				})
			}
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

// DeleteNode deletes a node only when it has no relationships.
//
// Cascade deletion is deliberately not part of this primitive API.
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
