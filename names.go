package wtw

import (
	"errors"
	"sort"
)

var (
	ErrNameAlreadyBound = errors.New("name is already bound to a different node")
	ErrNameNotFound     = errors.New("name not found")
	ErrNodeAlreadyNamed = errors.New("node already has this name")
)

// NameRegistry maintains bootstrap associations between human-readable names
// and NodeIDs.
//
// Names are not part of the primitive graph. They are external bootstrap
// knowledge that allows the orchestrator to refer to particular NodeIDs by
// stable names such as "ROOT" or "AllPointers".
//
// Multiple names may refer to the same NodeID.
type NameRegistry struct {
	graph *Graph

	byName map[string]NodeID
	byID   map[NodeID]map[string]struct{}
}

// NewNameRegistry creates an empty name registry associated with graph.
//
// The registry does not create any nodes.
func NewNameRegistry(graph *Graph) *NameRegistry {
	return &NameRegistry{
		graph:  graph,
		byName: make(map[string]NodeID),
		byID:   make(map[NodeID]map[string]struct{}),
	}
}

// Lookup returns the NodeID currently associated with name.
//
// The bool is false when the name has no association.
func (r *NameRegistry) Lookup(name string) (NodeID, bool) {
	id, ok := r.byName[name]
	return id, ok
}

// Bind associates name with an existing NodeID.
//
// A name may only have one NodeID at a time. Binding an already-bound name
// to the same NodeID is treated as a successful no-op.
//
// Multiple different names may refer to the same NodeID.
func (r *NameRegistry) Bind(name string, id NodeID) error {
	if !r.graph.NodeExists(id) {
		return ErrNodeNotFound
	}

	if existing, ok := r.byName[name]; ok {
		if existing == id {
			return nil
		}

		return ErrNameAlreadyBound
	}

	r.byName[name] = id

	names := r.byID[id]
	if names == nil {
		names = make(map[string]struct{})
		r.byID[id] = names
	}

	names[name] = struct{}{}

	return nil
}

// CreateNamedNode creates a new primitive node and associates name with it.
//
// If name is already bound, no node is created and ErrNameAlreadyBound is
// returned.
func (r *NameRegistry) CreateNamedNode(name string) (NodeID, error) {
	if _, exists := r.byName[name]; exists {
		return 0, ErrNameAlreadyBound
	}

	id, err := r.graph.CreateNode()
	if err != nil {
		return 0, err
	}

	if err := r.Bind(name, id); err != nil {
		return 0, err
	}

	return id, nil
}

// Unbind removes the association for name.
//
// The NodeID itself is not deleted from the graph.
//
// The bool reports whether an association was actually removed.
func (r *NameRegistry) Unbind(name string) (bool, error) {
	id, ok := r.byName[name]
	if !ok {
		return false, nil
	}

	delete(r.byName, name)

	names := r.byID[id]
	delete(names, name)

	if len(names) == 0 {
		delete(r.byID, id)
	}

	return true, nil
}

// NamesForNode returns all names currently associated with id.
//
// The returned slice is sorted for deterministic results.
//
// An existing node with no names returns an empty slice.
// A nonexistent node returns ErrNodeNotFound.
func (r *NameRegistry) NamesForNode(id NodeID) ([]string, error) {
	if !r.graph.NodeExists(id) {
		return nil, ErrNodeNotFound
	}

	names := r.byID[id]

	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}

	sort.Strings(result)

	return result, nil
}
