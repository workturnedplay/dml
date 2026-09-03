package wtw

import (
	"errors"
	"sort"
)

type NodeID uint64

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
type Graph struct {
	nextID    NodeID
	exhausted bool

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

	if g.exhausted {
		return 0, ErrNodeIDExhausted
	}

	id := g.nextID

	g.nodes[id] = struct{}{}
	g.outgoing[id] = make(map[NodeID]struct{})
	g.incoming[id] = make(map[NodeID]struct{})

	if id == ^NodeID(0) {
		g.exhausted = true
	} else {
		g.nextID++
	}

	return id, nil
}

// NodeExists reports whether id currently identifies an existing node.
func (g *Graph) NodeExists(id NodeID) bool {
	return g.nodeExists(id)
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

// FindRelationship reports whether the exact primitive relationship (from, to)
// exists.
func (g *Graph) FindRelationship(from, to NodeID) (Relationship, bool, error) {
	if !g.nodeExists(from) {
		return Relationship{}, false, ErrNodeNotFound
	}

	if !g.nodeExists(to) {
		return Relationship{}, false, ErrNodeNotFound
	}

	if _, exists := g.outgoing[from][to]; !exists {
		return Relationship{}, false, nil
	}

	return Relationship{
		From: from,
		To:   to,
	}, true, nil
}

// FindOutgoing returns all primitive relationships whose source is from.
//
// In other words, it finds every X for which (from, X) exists.
func (g *Graph) FindOutgoing(from NodeID) ([]Relationship, error) {
	if !g.nodeExists(from) {
		return nil, ErrNodeNotFound
	}

	relationships := make([]Relationship, 0, len(g.outgoing[from]))

	for to := range g.outgoing[from] {
		relationships = append(relationships, Relationship{
			From: from,
			To:   to,
		})
	}

	sort.Slice(relationships, func(i, j int) bool {
		return relationships[i].To < relationships[j].To
	})

	return relationships, nil
}

// FindIncoming returns all primitive relationships whose target is to.
//
// In other words, it finds every X for which (X, to) exists.
func (g *Graph) FindIncoming(to NodeID) ([]Relationship, error) {
	if !g.nodeExists(to) {
		return nil, ErrNodeNotFound
	}

	relationships := make([]Relationship, 0, len(g.incoming[to]))

	for from := range g.incoming[to] {
		relationships = append(relationships, Relationship{
			From: from,
			To:   to,
		})
	}

	sort.Slice(relationships, func(i, j int) bool {
		return relationships[i].From < relationships[j].From
	})

	return relationships, nil
}

// FindRelationships returns every primitive relationship in the graph.
//
// The returned relationships are sorted by From, then To. This ordering
// has no semantic meaning.
func (g *Graph) FindRelationships() []Relationship {
	total := 0
	for _, targets := range g.outgoing {
		total += len(targets)
	}

	relationships := make([]Relationship, 0, total)

	for from, targets := range g.outgoing {
		for to := range targets {
			relationships = append(relationships, Relationship{
				From: from,
				To:   to,
			})
		}
	}

	sort.Slice(relationships, func(i, j int) bool {
		if relationships[i].From != relationships[j].From {
			return relationships[i].From < relationships[j].From
		}
		return relationships[i].To < relationships[j].To
	})

	return relationships
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

// Txn groups a sequence of primitive Graph mutations so that, if the
// function passed to Graph.Transact returns a non-nil error or panics,
// every mutation performed through tx during that call is undone, in
// reverse order, before the error (or panic) propagates to the caller.
//
// Txn exists to close a real gap: several higher-level operations
// elsewhere in this file are naturally multi-step (NameRegistry.
// CreateNamedNode is CreateNode-then-Bind; PointerRegistry.SetTarget's
// replace path is RemoveRelationship-then-AddRelationship;
// PointerRegistry.NewPointer is CreateNode-then-AddRelationship). Before
// Txn existed, each step committed immediately and unconditionally, so a
// later step failing after an earlier step had already succeeded left
// permanently orphaned or inconsistent state -- for example, a node
// created but never named, or a Pointer left with no target at all
// because its old target was removed before the new one could be added.
// Txn's undo log makes each such sequence atomic with respect to failure.
//
// Txn deliberately does NOT provide isolation from concurrent access.
// The toy implementation is single-threaded/serialized
// (theorystate_v0.6.md section 19); nothing can observe a Txn's
// intermediate state mid-sequence today because nothing else runs
// between two statements in the same synchronous call. Should real
// concurrency be introduced later, Txn as written here would need real
// locking/isolation on top -- that is a separate, still-open problem
// (theorystate_v0.6.md section 19), not one Txn tries to solve.
//
// Txn also does NOT provide durability/crash-atomicity: there is no
// persistence layer yet, so a process crash mid-transaction is not a
// concern this version needs to handle.
//
// Txn is intentionally not a staged/copy-on-write view of the graph
// (theorystate_v0.6.md section 15's "transaction overlay" idea). Each Txn
// method applies its mutation directly to the real underlying Graph and
// simply records how to undo it; this is significantly simpler than a
// full overlay and is sufficient because, per the isolation point above,
// there is currently no concurrent reader that a staged view would need
// to protect from seeing uncommitted state.
//
// Txn's mutating surface deliberately covers only what current callers
// need: CreateNode, AddRelationship, and RemoveRelationship. Txn does not
// offer a DeleteNode method: undoing a delete would require resurrecting
// the exact same NodeID outside the normal monotonic counter, which is a
// real design question of its own (see the never-reuse policy discussion
// around ErrNodeIDExhausted) and is deferred until an actual caller needs
// a transactional delete. Read operations are not wrapped either, since
// every current caller already holds a reference to the underlying Graph
// (or NameRegistry/PointerRegistry wrapping one) for reads; add
// read-passthrough methods here if and when a caller actually needs them
// (theorystate_v0.6.md section 7's construct-only-what's-needed
// discipline).
//
// Nesting one Graph.Transact call inside another is not currently
// supported or used by anything in this file: an inner Txn has its own
// independent undo log and knows nothing about an enclosing one. Genuine
// nested-transaction semantics are theorystate_v0.6.md section 45, still
// OPEN; do not rely on nesting until that is deliberately designed.
type Txn struct {
	graph *Graph
	undo  []func()
}

// Transact runs fn against a fresh Txn wrapping g. If fn returns a
// non-nil error, every mutation fn performed through tx is undone, in
// reverse order, and that same error is returned. If fn panics, the same
// undo happens before the panic is re-raised, so a panicking caller does
// not leave g in a partially mutated state either. If fn returns nil,
// Transact simply returns nil; because every Txn method already applies
// its mutation directly to g as it happens, there is no separate "commit"
// step -- succeeding is simply not rolling back.
func (g *Graph) Transact(fn func(tx *Txn) error) (err error) {
	tx := &Txn{graph: g}

	defer func() {
		if r := recover(); r != nil {
			tx.rollback()
			panic(r)
		}
	}()

	err = fn(tx)
	if err != nil {
		tx.rollback()
	}

	return err
}

// rollback undoes every mutation recorded on tx so far, in reverse
// (LIFO) order. Reverse order matters: for example, if tx created a node
// and then added a relationship from it, rolling back the relationship
// first leaves the node empty, so rolling back the node's creation
// (DeleteNode) afterward is guaranteed to satisfy DeleteNode's
// no-relationships precondition (see the Graph.DeleteNode doc comment).
// Undoing in the opposite order would risk DeleteNode failing with
// ErrNodeNotEmpty.
func (tx *Txn) rollback() {
	for i := len(tx.undo) - 1; i >= 0; i-- {
		tx.undo[i]()
	}
	tx.undo = nil
}

// CreateNode behaves exactly like Graph.CreateNode, additionally
// recording an undo step that deletes the new node again if the
// enclosing transaction rolls back.
func (tx *Txn) CreateNode() (NodeID, error) {
	id, err := tx.graph.CreateNode()
	if err != nil {
		return 0, err
	}

	tx.undo = append(tx.undo, func() {
		// By the time this runs (see rollback's LIFO ordering), any
		// relationships involving id that this same transaction added
		// have already been undone, so id should be empty and this
		// delete should succeed. If something outside this transaction
		// mutated id in the meantime -- which nothing in this file
		// currently does -- this best-effort delete may fail; that
		// failure is deliberately swallowed here since Txn's rollback
		// has no error return of its own to report it through, and a
		// caller misusing Txn this way is a bug in the caller, not
		// something Txn can prevent by construction.
		_ = tx.graph.DeleteNode(id)
	})

	return id, nil
}

// AddRelationship behaves exactly like Graph.AddRelationship,
// additionally recording an undo step that removes the relationship
// again if the enclosing transaction rolls back -- but only if this call
// actually created it. If (a,b) already existed before this call
// (created == false), there is nothing for this call to undo: the
// relationship was not this transaction's to remove.
func (tx *Txn) AddRelationship(a, b NodeID) (created bool, err error) {
	created, err = tx.graph.AddRelationship(a, b)
	if err != nil {
		return false, err
	}

	if created {
		tx.undo = append(tx.undo, func() {
			_, _ = tx.graph.RemoveRelationship(a, b)
		})
	}

	return created, nil
}

// RemoveRelationship behaves exactly like Graph.RemoveRelationship,
// additionally recording an undo step that re-adds the relationship
// again if the enclosing transaction rolls back -- but only if this call
// actually removed it, symmetric with AddRelationship above.
func (tx *Txn) RemoveRelationship(a, b NodeID) (removed bool, err error) {
	removed, err = tx.graph.RemoveRelationship(a, b)
	if err != nil {
		return false, err
	}

	if removed {
		tx.undo = append(tx.undo, func() {
			_, _ = tx.graph.AddRelationship(a, b)
		})
	}

	return removed, nil
}

var (
	ErrNameAlreadyBound = errors.New("name is already bound")
	ErrNodeAlreadyNamed = errors.New("node already has a name")
	ErrNameNotFound     = errors.New("name not found")

	// ErrNameBoundToDeletedNode is returned when a name's registry
	// bookkeeping points at a NodeID that no longer exists in the
	// underlying graph. This is never expected to happen through the
	// registry's own API: it indicates that some caller deleted the node
	// via the primitive Graph.DeleteNode directly instead of going
	// through NameRegistry.DeleteNode, leaving the name -> NodeID
	// association stale. It is deliberately surfaced as a distinct,
	// loud failure rather than silently trusted (which would let further
	// structure get built on a nonexistent node) or silently repaired
	// (which would hide the upstream bug that caused it).
	ErrNameBoundToDeletedNode = errors.New("name is bound to a node that no longer exists")
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

// lookupLive returns the NodeID currently bound to name in this
// registry's bookkeeping, additionally confirming that the NodeID still
// exists in the underlying graph.
//
// bound is true only when name has an association AND that association's
// NodeID currently exists. If name has an association whose NodeID no
// longer exists, lookupLive returns ErrNameBoundToDeletedNode instead of
// a normal (id, bound) result: this is the shared fail-fast check used by
// every registry operation that is about to trust or hand out a NodeID
// (Bind, CreateNamedNode, EnsureNamedNode), so that a caller which deleted
// a named node through the primitive Graph.DeleteNode directly (bypassing
// NameRegistry.DeleteNode) gets a loud, immediate error the next time this
// registry is used, rather than silently building further structure on a
// nonexistent node.
//
// Lookup and NameForNode deliberately do NOT go through lookupLive: they
// are raw, side-effect-free bookkeeping queries, not NodeID-issuing
// operations, and keep their existing simple (value, bool) contract.
func (r *NameRegistry) lookupLive(name string) (id NodeID, bound bool, err error) {
	id, ok := r.byName[name]
	if !ok {
		return 0, false, nil
	}

	if !r.graph.NodeExists(id) {
		return 0, false, ErrNameBoundToDeletedNode
	}

	return id, true, nil
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

	existingID, bound, err := r.lookupLive(name)
	if err != nil {
		return err
	}

	if bound {
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
// If name is already bound to a live node, no new node is created and
// ErrNameAlreadyBound is returned. If name is bound to a NodeID that no
// longer exists (see lookupLive), ErrNameBoundToDeletedNode is returned
// instead, since that is a different, more serious problem than an
// ordinary already-bound name.
//
// Node creation and binding happen inside a Graph.Transact call: if Bind
// were ever to fail after CreateNode had already succeeded, the freshly
// created node would otherwise be left permanently orphaned (existing in
// the graph but never reachable through any name). Transact makes the two
// steps atomic with respect to that failure. In today's implementation
// this specific failure is not actually reachable through the public API
// -- CreateNamedNode's own lookupLive check above already guarantees
// Bind's checks will pass -- but wrapping it in Transact costs nothing
// and removes the dependency on that reasoning staying true as this code
// evolves.
func (r *NameRegistry) CreateNamedNode(name string) (NodeID, error) {
	if _, bound, err := r.lookupLive(name); err != nil {
		return 0, err
	} else if bound {
		return 0, ErrNameAlreadyBound
	}

	var id NodeID

	err := r.graph.Transact(func(tx *Txn) error {
		var err error
		id, err = tx.CreateNode()
		if err != nil {
			return err
		}

		return r.Bind(name, id)
	})
	if err != nil {
		return 0, err
	}

	return id, nil
}

// EnsureNamedNode returns the NodeID currently associated with name,
// creating and binding a fresh node exactly like CreateNamedNode if no
// such association exists yet.
//
// Unlike CreateNamedNode, EnsureNamedNode is idempotent: calling it
// repeatedly with the same name is always safe and always returns the
// same NodeID once the name has first been bound. This is the primitive
// building block for bootstrapping foundational named nodes (ROOT-like
// nodes such as AllPointers) that must exist exactly once no matter how
// many times setup code runs.
//
// If name is bound to a NodeID that no longer exists, EnsureNamedNode
// returns ErrNameBoundToDeletedNode (see lookupLive) rather than silently
// trusting the stale association or silently creating a replacement.
func (r *NameRegistry) EnsureNamedNode(name string) (NodeID, error) {
	id, bound, err := r.lookupLive(name)
	if err != nil {
		return 0, err
	}

	if bound {
		return id, nil
	}

	return r.CreateNamedNode(name)
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

// DeleteNode deletes id from the underlying graph and, only if that
// succeeds, removes any name association for id from the registry.
//
// This exists because Graph and NameRegistry are deliberately separate
// layers (Graph does not know about names). Deleting a named node directly
// through Graph.DeleteNode would leave a stale name -> NodeID / NodeID ->
// name association behind. Going through NameRegistry.DeleteNode instead
// keeps both in sync.
//
// If id currently has relationships, the underlying Graph.DeleteNode call
// fails with ErrNodeNotEmpty, and any existing name association is left
// completely untouched, exactly as if DeleteNode had never been called.
//
// It is not an error for id to have no name association; this then simply
// behaves like a plain Graph.DeleteNode.
func (r *NameRegistry) DeleteNode(id NodeID) error {
	if err := r.graph.DeleteNode(id); err != nil {
		return err
	}

	if name, ok := r.byID[id]; ok {
		delete(r.byName, name)
		delete(r.byID, id)
	}

	return nil
}

// BootstrapNames ensures that every name in names has an associated
// NodeID in this registry, creating any that do not yet exist.
//
// BootstrapNames is idempotent and resumable: because each individual
// name is established through EnsureNamedNode, calling BootstrapNames
// again — with the same list, a superset, or an overlapping list — never
// disturbs names that were already bound, and safely picks up where a
// previous partial call left off (for example, after a prior call failed
// partway through with ErrNodeIDExhausted). There is deliberately no
// transactional rollback: names successfully bound before a failure stay
// bound, which is exactly what a resumable bootstrap should do.
//
// The returned map has one entry per distinct name in names; duplicate
// entries in names collapse into a single map entry, as expected.
func (r *NameRegistry) BootstrapNames(names []string) (map[string]NodeID, error) {
	ids := make(map[string]NodeID, len(names))

	for _, name := range names {
		id, err := r.EnsureNamedNode(name)
		if err != nil {
			return nil, err
		}

		ids[name] = id
	}

	return ids, nil
}

// Foundational names are ordinary named nodes, exactly like ROOT, whose
// special meaning comes entirely from higher-level relationships and
// processors that interpret them — never from the primitive Graph or the
// NameRegistry itself. See THEORY_NOTES_FROM_CONVERSATION.md and
// theorystate_v0.6.md for the semantics each name is intended to support.
const (
	// NameAllPointers tags a node as Pointer-kind via the relationship
	// (AllPointers, P). The primitive layer enforces nothing about P;
	// invariants such as "P has at most one target" belong to a
	// higher-level processor, not yet implemented.
	NameAllPointers = "AllPointers"
)

// FoundationalNames lists every name that setup code should bootstrap via
// NameRegistry.BootstrapNames. New foundational names should be appended
// here — not bootstrapped ad hoc elsewhere — so there is a single, DRY
// source of truth for what must exist, and only once the corresponding
// representation is actually being implemented.
var FoundationalNames = []string{
	NameAllPointers,
}

// ErrCannotDeleteRoot is returned when deletion of ROOT is attempted
// through a RootGraph layer.
//
// This is deliberately distinct from ErrNodeNotEmpty. ErrNodeNotEmpty
// means "clear the relationships and try again." ErrCannotDeleteRoot means
// deletion can never succeed through this layer regardless of ROOT's
// relationship count, because ROOT's identity is structurally protected
// here, not merely blocked by leftover relationships.
var ErrCannotDeleteRoot = errors.New("cannot delete root node")

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
		// Whether to == root (the self-loop case, hidden by the ROOT
		// overlay's irreflexivity) or to != root (already represented
		// virtually by this layer), there is nothing to physically add
		// either way.
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
		// Symmetric with AddRelationship above: whether to == root or
		// not, there is no physical relationship for this layer to
		// remove.
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
// ROOT itself cannot be deleted through this layer. This is reported as
// ErrCannotDeleteRoot, not ErrNodeNotEmpty: ROOT's identity is
// structurally protected by this layer regardless of whether it currently
// has any relationships, so this failure cannot be resolved by clearing
// relationships and retrying, unlike an ordinary ErrNodeNotEmpty failure.
func (r *RootGraph) DeleteNode(id NodeID) error {
	if !r.graph.NodeExists(id) {
		return ErrNodeNotFound
	}

	if id == r.root {
		return ErrCannotDeleteRoot
	}

	return r.graph.DeleteNode(id)
}

var (
	ErrNotPointer            = errors.New("node is not tagged as a pointer")
	ErrTooManyPointerTargets = errors.New("pointer node has more than one target; the pointer invariant has already been violated")
)

// PointerRegistry enforces the Pointer invariant -- "at most one target"
// -- for nodes tagged Pointer-kind via the relationship (AllPointers, P).
//
// This implements Representation A from
// THEORY_NOTES_FROM_CONVERSATION.md section 7 / theorystate_v0.6.md
// section 10: a Pointer's target, if any, is simply P's single direct
// child in the underlying Graph. The tag itself -- (AllPointers, P) -- is
// ordinary graph structure, exactly like any other name-style tag. Like
// NameRegistry and RootGraph, PointerRegistry adds nothing to the
// primitive Graph; it is purely an interpretation/enforcement layer above
// it (theorystate_v0.6.md sections 10 and 73).
//
// PointerRegistry does not, and structurally cannot, prevent every path
// to invariant violation: a caller can always bypass this layer and call
// Graph.AddRelationship(P, Y) directly, giving a tagged node two or more
// children. PointerRegistry does not try to intercept arbitrary Graph
// mutations -- Graph must stay unaware of Pointer semantics, per the same
// layering discipline already established elsewhere in this file.
// Instead, every method here re-derives P's current target set fresh from
// the Graph on every call rather than caching it, and fails loudly with
// ErrTooManyPointerTargets if that set already has more than one member,
// rather than silently repairing or silently trusting stale expectations.
// This mirrors the fail-loud-not-silently-repair discipline already used
// for ErrNameBoundToDeletedNode in NameRegistry, and is the practical
// mitigation for the general gap recorded in theorystate_v0.6.md section
// 74: external structure built on top of the primitive Graph can go
// stale the instant a primitive mutation happens elsewhere, and nothing
// below this layer will ever notify it. A durable commit-time
// interception mechanism that could reject such a mutation before it
// lands (theorystate_v0.6.md section 73) does not exist yet; until it
// does, "always re-check, never cache" is the deliberate accepted
// boundary of this registry.
//
// Multi-step PointerRegistry operations (SetTarget's replace path,
// NewPointer's create-then-tag) run inside Graph.Transact: if a later
// step fails, an earlier step's mutation is undone rather than left
// committed, so e.g. a failed SetTarget can never leave a Pointer looking
// like it lost its old target without gaining the new one. See the Txn
// doc comment for exactly what this does and does not guarantee -- in
// particular, it is failure-atomicity, not isolation from concurrent
// access; true multi-primitive-operation transactional grouping as a
// first-class graph concept is still theorystate_v0.6.md section 14/45,
// OPEN. What exists here is the minimum needed to stop PointerRegistry's
// own multi-step operations from corrupting state on failure, not a
// general transaction feature.
type PointerRegistry struct {
	graph       *Graph
	allPointers NodeID
}

// NewPointerRegistry creates a PointerRegistry over graph, using
// allPointers as the tagging node for the (AllPointers, P) relationship.
//
// allPointers must already exist. It is the caller's responsibility to
// have bootstrapped it first, typically via
// NameRegistry.EnsureNamedNode(NameAllPointers) or
// NameRegistry.BootstrapNames(FoundationalNames). PointerRegistry itself
// has no dependency on NameRegistry or on names at all -- exactly like
// RootGraph takes its root as a plain NodeID rather than a name, keeping
// this layer decoupled from the bootstrap-naming concern.
func NewPointerRegistry(graph *Graph, allPointers NodeID) (*PointerRegistry, error) {
	if !graph.NodeExists(allPointers) {
		return nil, ErrNodeNotFound
	}

	return &PointerRegistry{
		graph:       graph,
		allPointers: allPointers,
	}, nil
}

// IsPointer reports whether id is currently tagged Pointer-kind via
// (AllPointers, id).
func (p *PointerRegistry) IsPointer(id NodeID) bool {
	return p.graph.HasRelationship(p.allPointers, id)
}

// currentTarget returns P's current single target, re-derived fresh from
// the underlying Graph on every call (see the PointerRegistry doc comment
// for why this is never cached).
//
// It requires P to exist and to be tagged Pointer-kind; otherwise it
// returns ErrNodeNotFound or ErrNotPointer respectively, without
// inspecting P's relationships at all. If P is tagged but currently has
// more than one outgoing relationship -- meaning some caller bypassed
// this registry and violated the Pointer invariant directly through the
// primitive Graph -- currentTarget returns ErrTooManyPointerTargets
// rather than silently picking one of them.
func (p *PointerRegistry) currentTarget(id NodeID) (target NodeID, hasTarget bool, err error) {
	if !p.graph.NodeExists(id) {
		return 0, false, ErrNodeNotFound
	}

	if !p.IsPointer(id) {
		return 0, false, ErrNotPointer
	}

	outgoing, err := p.graph.FindOutgoing(id)
	if err != nil {
		return 0, false, err
	}

	switch len(outgoing) {
	case 0:
		return 0, false, nil
	case 1:
		return outgoing[0].To, true, nil
	default:
		return 0, false, ErrTooManyPointerTargets
	}
}

// Target returns P's current target.
//
// hasTarget is false when P is a valid, currently-empty Pointer. See
// currentTarget for the error cases: P missing, P not tagged Pointer-kind,
// or P's invariant already violated by an out-of-band Graph mutation.
func (p *PointerRegistry) Target(id NodeID) (target NodeID, hasTarget bool, err error) {
	return p.currentTarget(id)
}

// SetTarget sets P's target to X, enforcing that P has at most one target
// both before and after the call.
//
// Both P and X must already exist, and P must already be tagged
// Pointer-kind (see NewPointer and TagAsPointer). Target's existence is
// checked before any mutation happens: if X did not exist and this check
// were skipped, a stale target could be removed before the new one failed
// to be added, losing data on a failed call. If P currently has no
// target, (P, X) is simply added. If P currently has exactly one target
// and it already equals X, this is an idempotent no-op. If P currently
// has exactly one different target, that relationship is removed and
// (P, X) is added in its place. If P currently has more than one target
// -- meaning the invariant was already violated by something outside
// this registry -- SetTarget makes no changes at all and returns
// ErrTooManyPointerTargets: it deliberately does not attempt to repair
// the violation by picking one existing target to keep or by clearing
// all of them, since either choice would be a silent, unrequested
// decision about data this registry did not create.
//
// Self-targeting, i.e. SetTarget(P, P), is allowed: self-relationships
// are permitted at the primitive layer (THEORY_NOTES_FROM_CONVERSATION.md
// section 1) and nothing about the Pointer invariant rules it out.
func (p *PointerRegistry) SetTarget(id, target NodeID) error {
	current, hasTarget, err := p.currentTarget(id)
	if err != nil {
		return err
	}

	if !p.graph.NodeExists(target) {
		return ErrNodeNotFound
	}

	if hasTarget && current == target {
		return nil
	}

	return p.graph.Transact(func(tx *Txn) error {
		if hasTarget {
			if _, err := tx.RemoveRelationship(id, current); err != nil {
				return err
			}
		}

		_, err := tx.AddRelationship(id, target)
		return err
	})
}

// RemoveTarget clears P's target, if any.
//
// The returned bool reports whether a target was actually removed. If P
// currently has no target, this is a no-op returning (false, nil). If P
// currently has more than one target -- an already-violated invariant --
// RemoveTarget makes no changes and returns ErrTooManyPointerTargets, for
// the same reason given in SetTarget: this registry does not silently
// repair violations it did not create.
func (p *PointerRegistry) RemoveTarget(id NodeID) (removed bool, err error) {
	current, hasTarget, err := p.currentTarget(id)
	if err != nil {
		return false, err
	}

	if !hasTarget {
		return false, nil
	}

	return p.graph.RemoveRelationship(id, current)
}

// NewPointer creates a fresh NodeID and immediately tags it Pointer-kind.
//
// Because the node is freshly created, it has zero relationships and
// therefore trivially satisfies the Pointer invariant -- unlike
// TagAsPointer, no invariant check is needed here.
//
// The create and tag steps run inside Graph.Transact: if tagging were
// ever to fail after the node had already been created, the node would
// otherwise be left orphaned -- it would exist but never be discoverable
// as a Pointer. See the Txn doc comment and the PointerRegistry doc
// comment above for what this atomicity does and does not cover.
func (p *PointerRegistry) NewPointer() (NodeID, error) {
	var id NodeID

	err := p.graph.Transact(func(tx *Txn) error {
		var err error
		id, err = tx.CreateNode()
		if err != nil {
			return err
		}

		_, err = tx.AddRelationship(p.allPointers, id)
		return err
	})
	if err != nil {
		return 0, err
	}

	return id, nil
}

// TagAsPointer tags an existing node id as Pointer-kind.
//
// Unlike NewPointer, id may already have relationships from before it
// became a Pointer, so TagAsPointer checks that id currently has at most
// one outgoing relationship before tagging it -- tagging a node that
// already has two or more outgoing relationships would immediately
// create an already-violated Pointer, which TagAsPointer refuses to do,
// returning ErrTooManyPointerTargets and leaving id untagged. id's
// existence is implicitly checked by the underlying FindOutgoing call,
// which returns ErrNodeNotFound if id does not exist.
//
// Tagging an id that is already tagged Pointer-kind is an idempotent
// success, exactly like the underlying Graph.AddRelationship being
// idempotent for an already-existing relationship.
func (p *PointerRegistry) TagAsPointer(id NodeID) error {
	outgoing, err := p.graph.FindOutgoing(id)
	if err != nil {
		return err
	}

	if len(outgoing) > 1 {
		return ErrTooManyPointerTargets
	}

	_, err = p.graph.AddRelationship(p.allPointers, id)
	return err
}
