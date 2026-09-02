package wtw

import "errors"

var (
	ErrNameAlreadyBound = errors.New("name is already bound")
	ErrNodeAlreadyNamed = errors.New("node already has a name")
	ErrNameNotFound     = errors.New("name not found")
)

// NameRegistry maintains the one-to-one association between names and
// existing NodeIDs.
//
// Names are bootstrap metadata outside the primitive graph. The primitive
// Graph does not know about names.
type NameRegistry struct {
	graph *Graph

	byName map[string]NodeID
	byID   map[NodeID]string
}

// NewNameRegistry creates an empty name registry associated with graph.
//
// It does not create any nodes.
func NewNameRegistry(graph *Graph) *NameRegistry {
	return &NameRegistry{
		graph:  graph,
		byName: make(map[string]NodeID),
		byID:   make(map[NodeID]string),
	}
}

// Lookup returns the NodeID associated with name.
//
// The bool is false when name has no association.
func (r *NameRegistry) Lookup(name string) (NodeID, bool) {
	id, ok := r.byName[name]
	return id, ok
}

// NameForNode returns the name associated with id.
//
// The bool is false when id has no name.
func (r *NameRegistry) NameForNode(id NodeID) (string, bool) {
	name, ok := r.byID[id]
	return name, ok
}

// Bind associates name with an existing, currently unnamed NodeID.
//
// Both directions of the association are unique:
//   - a name can identify only one NodeID
//   - a NodeID can have only one name
//
// Binding the exact same name to the exact same NodeID is an idempotent
// success.
func (r *NameRegistry) Bind(name string, id NodeID) error {
	if !r.graph.NodeExists(id) {
		return ErrNodeNotFound
	}

	if existingID, ok := r.byName[name]; ok {
		if existingID == id {
			return nil
		}

		return ErrNameAlreadyBound
	}

	if _, ok := r.byID[id]; ok {
		return ErrNodeAlreadyNamed
	}

	r.byName[name] = id
	r.byID[id] = name

	return nil
}

// CreateNamedNode creates a new primitive node and immediately gives it name.
//
// If name is already bound, no new node is created.
func (r *NameRegistry) CreateNamedNode(name string) (NodeID, error) {
	if _, ok := r.byName[name]; ok {
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

// Unbind removes the name association without deleting the NodeID.
//
// The bool reports whether an association was removed.
func (r *NameRegistry) Unbind(name string) (bool, error) {
	id, ok := r.byName[name]
	if !ok {
		return false, ErrNameNotFound
	}

	delete(r.byName, name)
	delete(r.byID, id)

	return true, nil
}
