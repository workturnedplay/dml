package wtw

import "sort"

// RootGraph is the graph layer exposed above the primitive Graph.
//
// ROOT is a real NodeID in the underlying Graph. Its relationship to every
// other existing node is virtual: (ROOT, X) is visible through this layer
// whenever X exists and X != ROOT, but is not physically stored in Graph.
//
// Ordinary relationships, including relationships pointing to ROOT, are
// stored normally in Graph.
type RootGraph struct {
	graph *Graph
	root  NodeID
}

// NewRootGraph creates a ROOT layer around an existing primitive node.
func NewRootGraph(graph *Graph, root NodeID) (*RootGraph, error) {
	if !graph.NodeExists(root) {
		return nil, ErrNodeNotFound
	}

	return &RootGraph{
		graph: graph,
		root:  root,
	}, nil
}

// Root returns the NodeID used as ROOT.
func (r *RootGraph) Root() NodeID {
	return r.root
}

// CreateNode creates a node in the underlying graph.
//
// The newly created node is consequently visible as a virtual child of
// ROOT through this layer.
func (r *RootGraph) CreateNode() (NodeID, error) {
	return r.graph.CreateNode()
}

// NodeExists reports whether id exists in the underlying graph.
func (r *RootGraph) NodeExists(id NodeID) bool {
	return r.graph.NodeExists(id)
}

// AddRelationship adds an ordinary relationship to the graph.
//
// A relationship from ROOT is virtual and therefore is not physically
// stored. If the target exists, (ROOT, target) already exists in this
// layer, so adding it is simply an idempotent no-op.
//
// Relationships pointing to ROOT are ordinary relationships and are stored.
func (r *RootGraph) AddRelationship(from, to NodeID) (created bool, err error) {
	if !r.graph.NodeExists(from) {
		return false, ErrNodeNotFound
	}

	if !r.graph.NodeExists(to) {
		return false, ErrNodeNotFound
	}

	if from == r.root {
		if to == r.root {
			return false, nil
		}

		// ROOT -> every other existing node is already represented
		// virtually by this layer.
		return false, nil
	}

	return r.graph.AddRelationship(from, to)
}

// RemoveRelationship removes an ordinary relationship from the graph.
//
// Virtual ROOT relationships cannot be removed because their existence is
// derived from node existence. Therefore removing (ROOT, X) is a no-op
// while X exists.
//
// Relationships pointing to ROOT are ordinary relationships and can be
// removed normally.
func (r *RootGraph) RemoveRelationship(from, to NodeID) (removed bool, err error) {
	if !r.graph.NodeExists(from) {
		return false, ErrNodeNotFound
	}

	if !r.graph.NodeExists(to) {
		return false, ErrNodeNotFound
	}

	if from == r.root {
		if to == r.root {
			return false, nil
		}

		// ROOT -> X is virtual and cannot be removed independently of X.
		return false, nil
	}

	return r.graph.RemoveRelationship(from, to)
}

// HasRelationship reports whether the relationship exists in the ROOT
// layer.
//
// ROOT has a virtual relationship to every existing node other than itself.
// All other relationships are taken from the primitive graph.
func (r *RootGraph) HasRelationship(from, to NodeID) bool {
	if !r.graph.NodeExists(from) || !r.graph.NodeExists(to) {
		return false
	}

	if from == r.root {
		return to != r.root
	}

	return r.graph.HasRelationship(from, to)
}

// FindRelationship reports whether the exact relationship exists in the
// ROOT layer.
func (r *RootGraph) FindRelationship(from, to NodeID) (Relationship, bool, error) {
	if !r.graph.NodeExists(from) {
		return Relationship{}, false, ErrNodeNotFound
	}

	if !r.graph.NodeExists(to) {
		return Relationship{}, false, ErrNodeNotFound
	}

	if !r.HasRelationship(from, to) {
		return Relationship{}, false, nil
	}

	return Relationship{
		From: from,
		To:   to,
	}, true, nil
}

// FindOutgoing returns every relationship whose source is from in the
// ROOT layer.
//
// For ROOT, this means every existing node other than ROOT.
// For every other node, it means the ordinary stored relationships.
func (r *RootGraph) FindOutgoing(from NodeID) ([]Relationship, error) {
	if !r.graph.NodeExists(from) {
		return nil, ErrNodeNotFound
	}

	if from != r.root {
		return r.graph.FindOutgoing(from)
	}

	relationships := make([]Relationship, 0, len(r.graph.nodes)-1)

	for id := range r.graph.nodes {
		if id == r.root {
			continue
		}

		relationships = append(relationships, Relationship{
			From: r.root,
			To:   id,
		})
	}

	sort.Slice(relationships, func(i, j int) bool {
		return relationships[i].To < relationships[j].To
	})

	return relationships, nil
}

// FindIncoming returns every relationship whose target is to in the ROOT
// layer.
//
// Unlike the previous implementation, ROOT is allowed to have parents.
// Those relationships are ordinary primitive relationships and are therefore
// returned normally.
//
// The only relationship excluded by the ROOT overlay is (ROOT, ROOT).
func (r *RootGraph) FindIncoming(to NodeID) ([]Relationship, error) {
	if !r.graph.NodeExists(to) {
		return nil, ErrNodeNotFound
	}

	relationships, err := r.graph.FindIncoming(to)
	if err != nil {
		return nil, err
	}

	if to != r.root {
		return relationships, nil
	}

	// (ROOT, ROOT) is not a valid relationship in the ROOT view because
	// ROOT's virtual outgoing relationship excludes itself.
	for i := range relationships {
		if relationships[i].From == r.root {
			relationships = append(relationships[:i], relationships[i+1:]...)
			break
		}
	}

	return relationships, nil
}

// FindRelationships returns every relationship visible through the ROOT
// layer.
//
// This consists of all stored primitive relationships except relationships
// whose source is ROOT, plus the virtual ROOT -> X relationship for every
// existing X != ROOT.
//
// A primitive ROOT -> X relationship, if one exists, is ignored because the
// ROOT layer represents that relationship virtually anyway.
// The primitive (ROOT, ROOT) relationship is likewise hidden.
func (r *RootGraph) FindRelationships() []Relationship {
	primitiveRelationships := r.graph.FindRelationships()
	relationships := make([]Relationship, 0, len(primitiveRelationships)+len(r.graph.nodes)-1)

	for _, relationship := range primitiveRelationships {
		if relationship.From == r.root {
			continue
		}

		relationships = append(relationships, relationship)
	}

	for id := range r.graph.nodes {
		if id == r.root {
			continue
		}

		relationships = append(relationships, Relationship{
			From: r.root,
			To:   id,
		})
	}

	sort.Slice(relationships, func(i, j int) bool {
		if relationships[i].From != relationships[j].From {
			return relationships[i].From < relationships[j].From
		}

		return relationships[i].To < relationships[j].To
	})

	return relationships
}

// DeleteNode deletes an ordinary node from the underlying graph.
//
// ROOT itself cannot be deleted through this layer.
func (r *RootGraph) DeleteNode(id NodeID) error {
	if !r.graph.NodeExists(id) {
		return ErrNodeNotFound
	}

	if id == r.root {
		return ErrNodeNotEmpty
	}

	return r.graph.DeleteNode(id)
}
