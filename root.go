package wtw

import "sort"

// RootView provides the single-graph ROOT overlay.
//
// ROOT is an ordinary existing NodeID selected by this higher layer.
// The relationships from ROOT to the other existing nodes are virtual:
// they are not stored in Graph.
type RootView struct {
	graph *Graph
	root  NodeID
}

// NewRootView creates a ROOT overlay for an existing node.
//
// The supplied root NodeID is not treated specially by the primitive Graph.
// RootView gives it the higher-level ROOT semantics.
func NewRootView(graph *Graph, root NodeID) (*RootView, error) {
	if !graph.NodeExists(root) {
		return nil, ErrNodeNotFound
	}

	return &RootView{
		graph: graph,
		root:  root,
	}, nil
}

// Root returns the NodeID used as this view's ROOT.
func (r *RootView) Root() NodeID {
	return r.root
}

// NodeExists reports whether id exists in the underlying graph.
//
// ROOT itself is an ordinary node and therefore is included.
func (r *RootView) NodeExists(id NodeID) bool {
	return r.graph.NodeExists(id)
}

// HasRelationship reports whether the relationship exists in the
// ROOT view.
//
// For ROOT, every existing node other than ROOT is considered a child.
// All other relationships come directly from the primitive graph.
func (r *RootView) HasRelationship(from, to NodeID) bool {
	if !r.graph.NodeExists(from) || !r.graph.NodeExists(to) {
		return false
	}

	if from == r.root {
		return to != r.root
	}

	return r.graph.HasRelationship(from, to)
}

// FindOutgoing returns all relationships whose source is from in the
// ROOT view.
//
// For ROOT, this is the virtual relationship ROOT -> every other
// existing node. For all other nodes, primitive relationships are returned.
func (r *RootView) FindOutgoing(from NodeID) ([]Relationship, error) {
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

// FindIncoming returns all relationships whose target is to in the
// ROOT view.
//
// ROOT has no parents in this overlay, so FindIncoming(ROOT) is empty.
// For all other nodes, primitive incoming relationships are returned.
func (r *RootView) FindIncoming(to NodeID) ([]Relationship, error) {
	if !r.graph.NodeExists(to) {
		return nil, ErrNodeNotFound
	}

	if to == r.root {
		return []Relationship{}, nil
	}

	return r.graph.FindIncoming(to)
}

// FindRelationship looks up an exact relationship in the ROOT view.
func (r *RootView) FindRelationship(from, to NodeID) (Relationship, bool, error) {
	if !r.graph.NodeExists(from) {
		return Relationship{}, false, ErrNodeNotFound
	}

	if !r.graph.NodeExists(to) {
		return Relationship{}, false, ErrNodeNotFound
	}

	if r.HasRelationship(from, to) {
		return Relationship{
			From: from,
			To:   to,
		}, true, nil
	}

	return Relationship{}, false, nil
}

// FindRelationships returns all relationships visible through the ROOT
// overlay.
//
// Primitive relationships are included unchanged. Virtual ROOT
// relationships are added for every existing node other than ROOT.
func (r *RootView) FindRelationships() []Relationship {
	relationships := r.graph.FindRelationships()

	relationships = append(
		relationships,
		// Virtual ROOT relationships.
	)

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
