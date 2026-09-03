package dml

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
	// (AllPointers, P), for Representation A (direct child): P's own
	// single direct child, if any, is P's target. See PointerRegistry.
	NameAllPointers = "AllPointers"

	// NameAllSubPointers tags a node as Pointer-kind for Representation B
	// (intermediary pointer node, THEORY_NOTES_FROM_CONVERSATION.md
	// section 7B): identical mechanism to NameAllPointers, applied to a
	// dedicated intermediary node U rather than to the owning node P
	// directly, so that P's other direct children stay unconstrained by
	// the pointer representation. Use a second PointerRegistry instance
	// constructed with this tag; no separate type is needed.
	NameAllSubPointers = "AllSubPointers"

	// NameAllPointerMetadata tags a node as a pointer-metadata node for
	// Representation C (metadata structure,
	// THEORY_NOTES_FROM_CONVERSATION.md section 7C). See
	// PointerMetadataRegistry.
	NameAllPointerMetadata = "AllPointerMetadata"

	// NameAllPointerMetadataSubjectSlot tags a node as a subject-slot
	// node used by PointerMetadataRegistry (Representation C) and
	// PointerMetadataRegistryD (Representation D). See
	// PointerMetadataRegistry for why the subject needs its own slot
	// node rather than being pointed at directly.
	NameAllPointerMetadataSubjectSlot = "AllPointerMetadataSubjectSlot"

	// NameAllPointerMetadataTargetSlot tags a node as a target-slot node
	// used by PointerMetadataRegistryD (Representation D). See
	// PointerMetadataRegistryD for why the target, like the subject,
	// needs its own dedicated slot node rather than being identified as
	// "whichever child of M isn't tagged subject-slot" (Representation
	// C's limitation).
	NameAllPointerMetadataTargetSlot = "AllPointerMetadataTargetSlot"

	// NameAllElementCapsules tags a node as an ElementCapsule
	// (THEORY_NOTES_FROM_CONVERSATION.md section 11 / theorystate_v0.6.md
	// section 11): a freshly-minted NodeID representing one particular
	// list-element occurrence, rather than the value itself. Named
	// AllElementCapsules (not the theory docs' illustrative "AllCapsules")
	// to avoid implying a more generic capsule concept. See
	// CapsuleRegistry.
	NameAllElementCapsules = "AllElementCapsules"

	// NameAllElementCapsulePrevSlot, NameAllElementCapsuleValueSlot, and
	// NameAllElementCapsuleNextSlot each tag a capsule's respective
	// role-slot intermediary node. Each role is discovered by its own
	// tag rather than by position, so a capsule may carry additional,
	// unrelated children later without disturbing role discovery. See
	// CapsuleRegistry.
	NameAllElementCapsulePrevSlot  = "AllElementCapsulePrevSlot"
	NameAllElementCapsuleValueSlot = "AllElementCapsuleValueSlot"
	NameAllElementCapsuleNextSlot  = "AllElementCapsuleNextSlot"

	// NameAllLists tags a node as a List (THEORY_NOTES_FROM_CONVERSATION.md
	// section 11 / theorystate_v0.6.md section 11). See ListRegistry.
	NameAllLists = "AllLists"

	// NameAllHeads and NameAllTails each tag a capsule as currently being
	// the head or tail of its list, respectively. Named AllHeads/AllTails
	// (PascalCase, consistent with every other tag name in this file --
	// AllPointers, AllElementCapsules, etc.) rather than reproducing the
	// theory docs' illustrative allHEADs/allTAILs styling verbatim; same
	// tags, same semantics. See ListRegistry for why these are plain tags
	// rather than a further Pointer-style indirection: (AllHeads, X) and
	// (AllTails, X) are already two distinct relationships even when the
	// same capsule X is simultaneously both head and tail (a
	// single-element list), so there is no collision risk analogous to
	// Representation C/D's subject/target collision to guard against.
	NameAllHeads = "AllHeads"
	NameAllTails = "AllTails"
)

// FoundationalNames lists every name that setup code should bootstrap via
// NameRegistry.BootstrapNames. New foundational names should be appended
// here — not bootstrapped ad hoc elsewhere — so there is a single, DRY
// source of truth for what must exist, and only once the corresponding
// representation is actually being implemented.
var FoundationalNames = []string{
	NameAllPointers,
	NameAllSubPointers,
	NameAllPointerMetadata,
	NameAllPointerMetadataSubjectSlot,
	NameAllPointerMetadataTargetSlot,
	NameAllElementCapsules,
	NameAllElementCapsulePrevSlot,
	NameAllElementCapsuleValueSlot,
	NameAllElementCapsuleNextSlot,
	NameAllLists,
	NameAllHeads,
	NameAllTails,
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

	// ErrAmbiguousPointerMetadata is returned by PointerMetadataRegistry
	// and PointerMetadataRegistryD when a tagged-parent or tagged-child
	// lookup (subject -> subject-slot, subject-slot -> metadata node, or,
	// for Representation D, metadata -> target-slot) finds more than one
	// match. This can only happen through an out-of-band Graph mutation
	// that bypasses these registries -- e.g. two different metadata nodes
	// both tagged (AllPointerMetadata, M) ending up pointed at the same
	// subject-slot. Mirrors the fail-loud-not-silently-repair discipline
	// used elsewhere in this file (ErrNameBoundToDeletedNode,
	// ErrTooManyPointerTargets).
	ErrAmbiguousPointerMetadata = errors.New("more than one node found during pointer-metadata lookup; the uniqueness invariant has already been violated")

	// ErrNotCapsule is returned by CapsuleRegistry when asked to operate
	// on a node that is not tagged (AllElementCapsules, node).
	ErrNotCapsule = errors.New("node is not tagged as an element capsule")

	// ErrNotList is returned by ListRegistry when asked to operate on a
	// node that is not tagged (AllLists, node).
	ErrNotList = errors.New("node is not tagged as a list")

	// ErrCapsuleNotInList is returned by ListRegistry.InsertAfter when
	// the given capsule is not currently an element of the given list
	// (i.e. (list, capsule) does not exist).
	ErrCapsuleNotInList = errors.New("capsule is not an element of this list")
)

// txOps is the minimal mutating surface needed to compose primitive
// operations atomically, whether directly against a *Graph or inside an
// existing *Txn. Both *Graph and *Txn satisfy it with their existing,
// unmodified method sets.
//
// This exists so a registry's create/wire sequence can be reused both as
// a standalone top-level Graph.Transact call and as one step composed
// into a larger enclosing Transact call -- e.g. CapsuleRegistry.NewCapsule
// composing PointerRegistry's create-and-tag sequence for each of a
// capsule's three role slots -- without nesting one Graph.Transact call
// inside another. Txn deliberately does not support nesting (see the Txn
// doc comment); parameterizing over txOps instead of a concrete *Txn is
// what lets the same sequence run either standalone or composed.
type txOps interface {
	CreateNode() (NodeID, error)
	AddRelationship(a, b NodeID) (created bool, err error)
	RemoveRelationship(a, b NodeID) (removed bool, err error)
}

// createTaggedNodeTx creates a fresh node and tags it via (tag, id),
// against tx. This is the shared "mint a fresh, tag-identified node"
// sequence used throughout this file: PointerRegistry.NewPointer's
// Pointer-kind tagging (via newPointerTx below), and the subject-slot /
// metadata / target-slot creation inside ensureMetadataWithSubjectSlot
// and PointerMetadataRegistryD.SetTarget.
func createTaggedNodeTx(tx txOps, tag NodeID) (NodeID, error) {
	id, err := tx.CreateNode()
	if err != nil {
		return 0, err
	}

	if _, err := tx.AddRelationship(tag, id); err != nil {
		return 0, err
	}

	return id, nil
}

// newPointerTx creates a fresh NodeID and tags it Pointer-kind via
// (allPointers, id), against whatever txOps tx is given. This is the
// create-and-tag sequence behind PointerRegistry.NewPointer, factored out
// so a larger composite operation (e.g. CapsuleRegistry.NewCapsule) can
// compose it into its own enclosing Graph.Transact call instead of
// PointerRegistry opening a second, nested one.
func newPointerTx(tx txOps, allPointers NodeID) (NodeID, error) {
	return createTaggedNodeTx(tx, allPointers)
}

// setPointerTargetTx sets id's target to target within tx, given id's
// current target state (current, hasCurrent) as already determined by the
// caller. It performs the remove-old/add-new sequence directly against
// tx, so it can be composed into a larger enclosing transaction (e.g. a
// future list operation rewiring an existing capsule slot's target)
// without nesting Graph.Transact calls. Callers are responsible for the
// "already has this target -> no-op" idempotency check before calling
// this, exactly as PointerRegistry.SetTarget already does.
func setPointerTargetTx(tx txOps, id, current NodeID, hasCurrent bool, target NodeID) error {
	if hasCurrent {
		if _, err := tx.RemoveRelationship(id, current); err != nil {
			return err
		}
	}

	_, err := tx.AddRelationship(id, target)
	return err
}

// singleChildTargetSetTx sets node's single "target" child -- under the
// same at-most-one-child invariant as singleChildTarget/PointerRegistry
// -- composed into an existing tx rather than opening a new
// Graph.Transact. It is the tx-composable counterpart of
// PointerRegistry.SetTarget's read-current/idempotency-check/replace
// sequence, for callers (CapsuleRegistry, and through it ListRegistry)
// that need to rewire an already-tagged Pointer-style slot node as one
// step of a larger enclosing transaction.
//
// node is assumed to already be a valid, at-most-one-child node (e.g. a
// capsule role slot); callers are responsible for target's existence,
// exactly as PointerRegistry.SetTarget's caller-facing checks already
// are. This deliberately skips the IsPointer-style tag check that
// PointerRegistry.currentTarget performs, since callers here have
// already located node via a tag-based lookup (e.g.
// findUniqueTaggedChild) immediately beforehand.
func singleChildTargetSetTx(tx txOps, graph *Graph, node, target NodeID) error {
	current, hasCurrent, err := singleChildTarget(graph, node)
	if err != nil {
		return err
	}

	if hasCurrent && current == target {
		return nil
	}

	return setPointerTargetTx(tx, node, current, hasCurrent, target)
}

// singleChildTarget returns the single relevant child of node in the
// underlying Graph, after excluding any NodeIDs listed in exclude.
//
// This is the shared "at most one relevant child" invariant check behind
// every Pointer representation implemented so far:
//   - Representation A (PointerRegistry, tag AllPointers): node is P
//     itself, no exclusions -- P's own direct child is the target.
//   - Representation B (PointerRegistry, tag AllSubPointers): node is U,
//     no exclusions -- identical mechanism, different tag, see the
//     PointerRegistry doc comment.
//   - Representation C (PointerMetadataRegistry): node is the metadata
//     node M, excluding the subject-slot node S so that S's own
//     structural presence as a child of M is never mistaken for the
//     Pointer's actual target.
//
// hasTarget is false when node's only children, if any, are exactly the
// excluded set. If more than one non-excluded child remains,
// ErrTooManyPointerTargets is returned rather than arbitrarily picking
// one -- see the PointerRegistry doc comment for why (theorystate_v0.6.md
// section 74: out-of-band mutation can violate this at any time, and
// every caller re-derives fresh rather than caching).
func singleChildTarget(g *Graph, node NodeID, exclude ...NodeID) (target NodeID, hasTarget bool, err error) {
	outgoing, err := g.FindOutgoing(node)
	if err != nil {
		return 0, false, err
	}

outer:
	for _, rel := range outgoing {
		for _, ex := range exclude {
			if rel.To == ex {
				continue outer
			}
		}

		if hasTarget {
			return 0, false, ErrTooManyPointerTargets
		}

		target = rel.To
		hasTarget = true
	}

	return target, hasTarget, nil
}

// PointerRegistry enforces the Pointer invariant -- "at most one target"
// -- for nodes tagged Pointer-kind via a caller-supplied tag relationship
// (tag, P).
//
// This same type and logic serves two of the three Pointer
// representations described in THEORY_NOTES_FROM_CONVERSATION.md section
// 7 / theorystate_v0.6.md section 10, distinguished only by which tag
// NodeID the caller passes to NewPointerRegistry -- there is no
// per-representation code path:
//   - Representation A (direct child): tag = AllPointers, applied
//     directly to the owning node P.
//   - Representation B (intermediary pointer node): tag =
//     AllSubPointers, applied to a dedicated node U. The caller
//     separately creates the ordinary relationship (P, U) themselves --
//     that edge carries no invariant and is not PointerRegistry's
//     concern -- so P's other direct children stay unconstrained by the
//     pointer representation living on U.
//
// Representation C (metadata structure) cannot reuse this type as-is,
// since the subject's own direct children must stay completely
// untouched; see PointerMetadataRegistry instead.
//
// This implements Representation A (and, via reuse, B) from
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

	return singleChildTarget(p.graph, id)
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
		return setPointerTargetTx(tx, id, current, hasTarget, target)
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
		id, err = newPointerTx(tx, p.allPointers)
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

// findUniqueTaggedParent returns the single parent of node that is tagged
// via (tag, parent), i.e. for which g.HasRelationship(tag, parent) holds.
//
// This is the shared reverse-lookup primitive behind
// PointerMetadataRegistry's two-hop subject -> slot -> metadata
// discovery. It requires node to exist. found is false if no tagged
// parent exists. If more than one tagged parent exists -- only reachable
// through an out-of-band Graph mutation -- ErrAmbiguousPointerMetadata is
// returned instead of arbitrarily picking one.
func findUniqueTaggedParent(g *Graph, node, tag NodeID) (parent NodeID, found bool, err error) {
	incoming, err := g.FindIncoming(node)
	if err != nil {
		return 0, false, err
	}

	for _, rel := range incoming {
		if g.HasRelationship(tag, rel.From) {
			if found {
				return 0, false, ErrAmbiguousPointerMetadata
			}

			parent = rel.From
			found = true
		}
	}

	return parent, found, nil
}

// findUniqueTaggedChild returns the single child of node that is tagged
// via (tag, child), i.e. for which g.HasRelationship(tag, child) holds.
//
// This is the forward-lookup counterpart to findUniqueTaggedParent: where
// findUniqueTaggedParent scans node's *parents* for one tagged tag,
// findUniqueTaggedChild scans node's *children* for one tagged tag. Used
// by PointerMetadataRegistryD to find a specific slot child of M by tag,
// rather than by exclusion/assumption about M's other children -- see the
// PointerMetadataRegistryD doc comment for why this is the fix over
// Representation C's exclusion-based approach.
//
// It requires node to exist. found is false if no tagged child exists. If
// more than one tagged child exists -- only reachable through an
// out-of-band Graph mutation -- ErrAmbiguousPointerMetadata is returned
// instead of arbitrarily picking one.
func findUniqueTaggedChild(g *Graph, node, tag NodeID) (child NodeID, found bool, err error) {
	outgoing, err := g.FindOutgoing(node)
	if err != nil {
		return 0, false, err
	}

	for _, rel := range outgoing {
		if g.HasRelationship(tag, rel.To) {
			if found {
				return 0, false, ErrAmbiguousPointerMetadata
			}

			child = rel.To
			found = true
		}
	}

	return child, found, nil
}

// locateBySubjectSlot finds node's metadata node and subject-slot node
// via the two-hop subject -> subject-slot -> metadata reverse lookup
// shared by both PointerMetadataRegistry (Representation C) and
// PointerMetadataRegistryD (Representation D): both representations
// identify the subject the same way, differing only in how they then
// locate the target. node must exist.
func locateBySubjectSlot(g *Graph, node, allPointerMetadata, allSubjectSlots NodeID) (metadata, subjectSlot NodeID, found bool, err error) {
	subjectSlot, found, err = findUniqueTaggedParent(g, node, allSubjectSlots)
	if err != nil || !found {
		return 0, 0, found, err
	}

	metadata, found, err = findUniqueTaggedParent(g, subjectSlot, allPointerMetadata)
	if err != nil || !found {
		return 0, 0, found, err
	}

	return metadata, subjectSlot, true, nil
}

// ensureMetadataWithSubjectSlot returns subject's existing metadata/
// subject-slot pair (via locateBySubjectSlot), creating a fresh, empty
// one (M -> S -> subject, both tagged) if none exists yet. Shared by both
// PointerMetadataRegistry and PointerMetadataRegistryD, which build
// identical subject-side structure and differ only in how the target
// side is represented. Callers are responsible for checking that subject
// itself exists before calling this.
func ensureMetadataWithSubjectSlot(g *Graph, subject, allPointerMetadata, allSubjectSlots NodeID) (metadata, subjectSlot NodeID, err error) {
	var found bool
	metadata, subjectSlot, found, err = locateBySubjectSlot(g, subject, allPointerMetadata, allSubjectSlots)
	if err != nil {
		return 0, 0, err
	}
	if found {
		return metadata, subjectSlot, nil
	}

	err = g.Transact(func(tx *Txn) error {
		var err error

		subjectSlot, err = createTaggedNodeTx(tx, allSubjectSlots)
		if err != nil {
			return err
		}
		if _, err := tx.AddRelationship(subjectSlot, subject); err != nil {
			return err
		}

		metadata, err = createTaggedNodeTx(tx, allPointerMetadata)
		if err != nil {
			return err
		}

		_, err = tx.AddRelationship(metadata, subjectSlot)
		return err
	})
	if err != nil {
		return 0, 0, err
	}

	return metadata, subjectSlot, nil
}

// subjectMetadataBase holds the graph/tag state and subject-side
// operations -- locate, ensureMetadata, EnsureMetadata, HasMetadata --
// shared identically by PointerMetadataRegistry (Representation C) and
// PointerMetadataRegistryD (Representation D). Both representations
// locate or create a subject's metadata node and subject-slot node in
// exactly the same way (via locateBySubjectSlot /
// ensureMetadataWithSubjectSlot); they differ only in how they then find
// the target, which is why only the shared subject-side logic is
// factored out here rather than merging the two types outright.
//
// PointerMetadataRegistry and PointerMetadataRegistryD each embed this
// struct anonymously, so its fields (graph, allPointerMetadata,
// allSubjectSlots) and methods are promoted and usable exactly as if
// they were declared directly on the embedding type.
type subjectMetadataBase struct {
	graph              *Graph
	allPointerMetadata NodeID
	allSubjectSlots    NodeID
}

// locate finds subject's metadata node and subject-slot node, if any.
// found is false if subject has no metadata yet. subject must exist.
func (b *subjectMetadataBase) locate(subject NodeID) (metadata, subjectSlot NodeID, found bool, err error) {
	return locateBySubjectSlot(b.graph, subject, b.allPointerMetadata, b.allSubjectSlots)
}

// ensureMetadata returns subject's existing metadata/subject-slot pair,
// creating a fresh, empty one (M -> S -> subject, both tagged) if none
// exists yet.
func (b *subjectMetadataBase) ensureMetadata(subject NodeID) (metadata, subjectSlot NodeID, err error) {
	if !b.graph.NodeExists(subject) {
		return 0, 0, ErrNodeNotFound
	}

	return ensureMetadataWithSubjectSlot(b.graph, subject, b.allPointerMetadata, b.allSubjectSlots)
}

// EnsureMetadata returns subject's metadata node, creating an empty one
// if none exists yet.
func (b *subjectMetadataBase) EnsureMetadata(subject NodeID) (NodeID, error) {
	metadata, _, err := b.ensureMetadata(subject)
	return metadata, err
}

// HasMetadata reports whether subject currently has an associated
// metadata node, regardless of whether a target has been set.
func (b *subjectMetadataBase) HasMetadata(subject NodeID) (bool, error) {
	if !b.graph.NodeExists(subject) {
		return false, ErrNodeNotFound
	}

	_, _, found, err := b.locate(subject)
	return found, err
}

// PointerMetadataRegistry implements Representation C (metadata
// structure) of the Pointer processor,
// THEORY_NOTES_FROM_CONVERSATION.md section 7C / theorystate_v0.6.md
// section 10's generalized metadata construction.
//
// Unlike PointerRegistry (Representations A and B), Representation C
// keeps the subject node's own direct children completely untouched by
// the pointer representation. Instead, a dedicated metadata node M
// records the association:
//
//	(AllPointerMetadata, M)
//	M -> S                                 M's subject-slot child
//	(AllPointerMetadataSubjectSlot, S)
//	S -> subject                           S identifies the actual subject
//	M -> target                            M's target child, if any
//
// Design note -- why the subject needs its own slot node S rather than M
// pointing directly at the subject (M -> subject): a naive two-edge
// scheme (M -> subject, M -> target) cannot represent target == subject.
// Because primitive relationships are unique pairs
// (THEORY_NOTES_FROM_CONVERSATION.md section 1), "M -> subject" and
// "M -> target" would collapse into the identical single physical
// relationship whenever target == subject, making self-targeting
// indistinguishable from an empty target. A freshly-minted S node for the
// subject-slot (the role/occurrence-identity pattern noted in
// THEORY_NOTES_FROM_CONVERSATION.md section 12 and theorystate_v0.6.md
// section 75 -- the same move as ElementCapsule nodes in the Ordered List
// design) means M's two children -- S and the target -- can never
// collide, since S is never equal to any subject value.
//
// Given a subject, the metadata node is discovered via a two-hop reverse
// lookup: find S among subject's parents tagged
// AllPointerMetadataSubjectSlot, then find M among S's parents tagged
// AllPointerMetadata (see findUniqueTaggedParent). This relies on
// FindIncoming being indexed; it is not a full-graph scan.
//
// As with PointerRegistry, every method re-derives current state fresh
// from the Graph on every call rather than caching it, and fails loudly
// (ErrAmbiguousPointerMetadata, ErrTooManyPointerTargets) rather than
// silently repairing when an out-of-band mutation has violated an
// invariant.
//
// Known limitation, kept deliberately rather than fixed here: M's target
// is identified *by exclusion* -- "whichever of M's children isn't S must
// be the target" -- via singleChildTarget(m.graph, metadata, slot). This
// means M is implicitly assumed to have only these two children, ever;
// any future unrelated child added to M (tagged or not) would make
// target discovery fail loudly with ErrTooManyPointerTargets even though
// nothing about the actual target changed. This is really the same
// mistake as Representation A's "at most one child, period" -- just
// shifted up one level -- and it also matches
// THEORY_NOTES_FROM_CONVERSATION.md section 10's original "M -> P, M ->
// I" sketch, which has an even sharper version of the same bug (those two
// relationships collapse into one whenever target == subject, since
// primitive relationships are unique pairs). PointerMetadataRegistryD
// (Representation D) is the corrected construction -- see its doc
// comment. This type is kept as-is, limitation and all, rather than
// patched or deleted: it is useful as a deliberately stricter
// representation for testing how higher-level code should react when a
// lower layer refuses something Representation D would allow
// (theorystate_v0.6.md section 73).
type PointerMetadataRegistry struct {
	subjectMetadataBase
}

// NewPointerMetadataRegistry creates a PointerMetadataRegistry over
// graph, using allPointerMetadata to tag metadata nodes and
// allSubjectSlots to tag subject-slot nodes. Both must already exist --
// typically via NameRegistry.BootstrapNames(FoundationalNames).
//
// locate, ensureMetadata, EnsureMetadata, and HasMetadata are inherited
// unmodified from the embedded subjectMetadataBase, which is shared with
// PointerMetadataRegistryD -- see subjectMetadataBase's doc comment for
// why this subject-side logic is factored out rather than duplicated.
func NewPointerMetadataRegistry(graph *Graph, allPointerMetadata, allSubjectSlots NodeID) (*PointerMetadataRegistry, error) {
	if !graph.NodeExists(allPointerMetadata) {
		return nil, ErrNodeNotFound
	}

	if !graph.NodeExists(allSubjectSlots) {
		return nil, ErrNodeNotFound
	}

	return &PointerMetadataRegistry{
		subjectMetadataBase: subjectMetadataBase{
			graph:              graph,
			allPointerMetadata: allPointerMetadata,
			allSubjectSlots:    allSubjectSlots,
		},
	}, nil
}

// Target returns subject's current target via its metadata node, if any.
//
// hasTarget is false both when subject has no metadata node at all and
// when it has one with no target set yet -- callers that need to
// distinguish those two cases should use HasMetadata first.
func (m *PointerMetadataRegistry) Target(subject NodeID) (target NodeID, hasTarget bool, err error) {
	if !m.graph.NodeExists(subject) {
		return 0, false, ErrNodeNotFound
	}

	metadata, slot, found, err := m.locate(subject)
	if err != nil {
		return 0, false, err
	}
	if !found {
		return 0, false, nil
	}

	return singleChildTarget(m.graph, metadata, slot)
}

// SetTarget sets subject's target to target, creating subject's metadata
// node first if it does not exist yet (see EnsureMetadata).
//
// Self-targeting (SetTarget(subject, subject)) is explicitly supported
// and correctly distinguished from an empty target -- see the
// PointerMetadataRegistry doc comment for why the subject-slot
// indirection is what makes this possible.
func (m *PointerMetadataRegistry) SetTarget(subject, target NodeID) error {
	if !m.graph.NodeExists(target) {
		return ErrNodeNotFound
	}

	metadata, slot, err := m.ensureMetadata(subject)
	if err != nil {
		return err
	}

	current, hasTarget, err := singleChildTarget(m.graph, metadata, slot)
	if err != nil {
		return err
	}

	if hasTarget && current == target {
		return nil
	}

	return m.graph.Transact(func(tx *Txn) error {
		return setPointerTargetTx(tx, metadata, current, hasTarget, target)
	})
}

// RemoveTarget clears subject's target, if any. The metadata/slot nodes
// themselves are left in place (no cascade deletion, consistent with
// theorystate_v0.6.md section 18's rejection of
// deleteNodeAndRelationships); an empty metadata node is a valid,
// meaningful state, exactly like an empty Pointer in Representation A.
func (m *PointerMetadataRegistry) RemoveTarget(subject NodeID) (removed bool, err error) {
	if !m.graph.NodeExists(subject) {
		return false, ErrNodeNotFound
	}

	metadata, slot, found, err := m.locate(subject)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}

	target, hasTarget, err := singleChildTarget(m.graph, metadata, slot)
	if err != nil {
		return false, err
	}
	if !hasTarget {
		return false, nil
	}

	return m.graph.RemoveRelationship(metadata, target)
}

// PointerMetadataRegistryD implements Representation D, a corrected
// generalization of Representation C (PointerMetadataRegistry) /
// THEORY_NOTES_FROM_CONVERSATION.md section 10's original "M -> P, M ->
// I" sketch.
//
// Representation C and section 10's original sketch share a real bug:
// they each identify one of M's two children *by exclusion* -- "whichever
// child isn't the subject/subject-slot must be the target/information
// node" (or, in section 10's even earlier sketch, "M -> P" and "M -> I"
// collapse into the same relationship whenever target == subject, since
// primitive relationships are unique pairs -- THEORY_NOTES section 1).
// Both are really the same underlying mistake as Representation A's "at
// most one child, no room for anything else": M is implicitly assumed to
// have *exactly* the relevant children and nothing more, which directly
// contradicts section 10's own stated goal ("This is a general
// construction, not merely a pointer trick" -- i.e. M should be free to
// grow additional, unrelated children later without breaking discovery).
//
// Representation D fixes this by giving *both* roles their own dedicated,
// freshly-minted, explicitly tagged slot node, exactly symmetric with
// each other:
//
//	(AllPointerMetadata, M)
//	M -> U1                                  M's subject-slot child
//	(AllPointerMetadataSubjectSlot, U1)
//	U1 -> subject                            U1 identifies the subject
//	M -> U2                                  M's target-slot child (once set)
//	(AllPointerMetadataTargetSlot, U2)
//	U2 -> target                             U2 identifies the target
//
// Both U1 and U2 are discovered by their own tag, not by exclusion, so M
// can carry any number of additional, unrelated children -- tagged or
// not, now or added later -- without ever disturbing subject or target
// discovery. This is the same occurrence/role-identity pattern already
// named in theorystate_v0.6.md section 75, applied twice over instead of
// once.
//
// Representation C (PointerMetadataRegistry) is kept, not deleted, even
// though Representation D supersedes it as the *correct* general
// construction: C's exclusion-based limitation is now understood and
// named rather than accidental, and deliberately keeping the more
// restrictive representation available is useful for testing how
// higher-level code should react when a lower layer is stricter than
// necessary (theorystate_v0.6.md section 73's commit-time interception
// question -- should such a restriction be enforced, ignored, or merely
// reported?). Do not add further logic to C to "fix" it; add it here to
// D instead.
//
// As with every other registry in this file, every method here
// re-derives current state fresh from the Graph on every call, and fails
// loudly (ErrAmbiguousPointerMetadata, ErrTooManyPointerTargets) rather
// than silently repairing an out-of-band invariant violation.
type PointerMetadataRegistryD struct {
	subjectMetadataBase
	allTargetSlots NodeID
}

// NewPointerMetadataRegistryD creates a PointerMetadataRegistryD over
// graph, using allPointerMetadata to tag metadata nodes, allSubjectSlots
// to tag subject-slot nodes, and allTargetSlots to tag target-slot nodes.
// All three must already exist -- typically via
// NameRegistry.BootstrapNames(FoundationalNames).
//
// locate, ensureMetadata, EnsureMetadata, and HasMetadata are inherited
// unmodified from the embedded subjectMetadataBase, which is shared with
// PointerMetadataRegistry -- see subjectMetadataBase's doc comment for
// why this subject-side logic is factored out rather than duplicated.
func NewPointerMetadataRegistryD(graph *Graph, allPointerMetadata, allSubjectSlots, allTargetSlots NodeID) (*PointerMetadataRegistryD, error) {
	if !graph.NodeExists(allPointerMetadata) {
		return nil, ErrNodeNotFound
	}

	if !graph.NodeExists(allSubjectSlots) {
		return nil, ErrNodeNotFound
	}

	if !graph.NodeExists(allTargetSlots) {
		return nil, ErrNodeNotFound
	}

	return &PointerMetadataRegistryD{
		subjectMetadataBase: subjectMetadataBase{
			graph:              graph,
			allPointerMetadata: allPointerMetadata,
			allSubjectSlots:    allSubjectSlots,
		},
		allTargetSlots: allTargetSlots,
	}, nil
}

// targetSlot returns metadata's current target-slot child (U2), if any,
// found by tag rather than by exclusion -- see the PointerMetadataRegistryD
// doc comment for why this is the fix over Representation C.
func (m *PointerMetadataRegistryD) targetSlot(metadata NodeID) (slot NodeID, found bool, err error) {
	return findUniqueTaggedChild(m.graph, metadata, m.allTargetSlots)
}

// Target returns subject's current target via its metadata/target-slot
// nodes, if any.
//
// hasTarget is false when subject has no metadata node at all, when it
// has one with no target-slot yet, or when it has a target-slot with no
// target set yet -- callers that need to distinguish those cases should
// use HasMetadata and EnsureMetadata directly.
func (m *PointerMetadataRegistryD) Target(subject NodeID) (target NodeID, hasTarget bool, err error) {
	if !m.graph.NodeExists(subject) {
		return 0, false, ErrNodeNotFound
	}

	metadata, _, found, err := m.locate(subject)
	if err != nil {
		return 0, false, err
	}
	if !found {
		return 0, false, nil
	}

	slot, found, err := m.targetSlot(metadata)
	if err != nil {
		return 0, false, err
	}
	if !found {
		return 0, false, nil
	}

	return singleChildTarget(m.graph, slot)
}

// SetTarget sets subject's target to target, creating subject's metadata
// node and/or target-slot node first if they do not exist yet.
//
// Self-targeting (SetTarget(subject, subject)) is supported: U2 (the
// target-slot) is a freshly-minted node distinct from subject, U1, and M,
// so U2 -> target can never collide with any other relationship no
// matter what target equals.
func (m *PointerMetadataRegistryD) SetTarget(subject, target NodeID) error {
	if !m.graph.NodeExists(target) {
		return ErrNodeNotFound
	}

	metadata, _, err := m.ensureMetadata(subject)
	if err != nil {
		return err
	}

	slot, found, err := m.targetSlot(metadata)
	if err != nil {
		return err
	}

	if !found {
		return m.graph.Transact(func(tx *Txn) error {
			var err error
			slot, err = createTaggedNodeTx(tx, m.allTargetSlots)
			if err != nil {
				return err
			}
			if _, err := tx.AddRelationship(metadata, slot); err != nil {
				return err
			}
			_, err = tx.AddRelationship(slot, target)
			return err
		})
	}

	current, hasTarget, err := singleChildTarget(m.graph, slot)
	if err != nil {
		return err
	}

	if hasTarget && current == target {
		return nil
	}

	return m.graph.Transact(func(tx *Txn) error {
		return setPointerTargetTx(tx, slot, current, hasTarget, target)
	})
}

// RemoveTarget clears subject's target, if any. The metadata/subject-
// slot/target-slot nodes themselves are left in place (no cascade
// deletion, consistent with theorystate_v0.6.md section 18's rejection of
// deleteNodeAndRelationships); an empty target-slot -- or no target-slot
// at all -- is a valid, meaningful state.
func (m *PointerMetadataRegistryD) RemoveTarget(subject NodeID) (removed bool, err error) {
	if !m.graph.NodeExists(subject) {
		return false, ErrNodeNotFound
	}

	metadata, _, found, err := m.locate(subject)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}

	slot, found, err := m.targetSlot(metadata)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}

	target, hasTarget, err := singleChildTarget(m.graph, slot)
	if err != nil {
		return false, err
	}
	if !hasTarget {
		return false, nil
	}

	return m.graph.RemoveRelationship(slot, target)
}

// CapsuleRegistry implements the ElementCapsule primitive of Ordered
// Lists (THEORY_NOTES_FROM_CONVERSATION.md section 11 / theorystate_v0.6.md
// section 11): each list-element *occurrence* gets its own freshly-minted
// NodeID (the capsule) rather than reusing the value's own NodeID, so the
// same value can occur multiple times in a list through different
// capsules (theorystate_v0.6.md section 75's occurrence/role-identity
// pattern).
//
// A capsule's previous, value, and next roles are each represented by a
// dedicated intermediary slot node -- exactly Pointer Representation B
// (THEORY_NOTES_FROM_CONVERSATION.md section 7B / theorystate_v0.6.md
// section 10), applied three times under three different tags:
//
//	(AllElementCapsules, capsule)
//	capsule -> Uprev    (AllElementCapsulePrevSlot, Uprev)   Uprev -> prevCapsule
//	capsule -> Uvalue   (AllElementCapsuleValueSlot, Uvalue) Uvalue -> value
//	capsule -> Unext    (AllElementCapsuleNextSlot, Unext)   Unext -> nextCapsule
//
// Each slot's own target is enforced to be at most one using the exact
// same PointerRegistry type already used for Representations A and B;
// CapsuleRegistry embeds three PointerRegistry instances -- one per role,
// distinguished only by tag, per theorystate_v0.6.md section 76's
// tag-parameterization discipline -- rather than reimplementing "at most
// one target" a third time.
//
// The three slot roles are discovered by their own tag
// (findUniqueTaggedChild), never by position or by exclusion, so a
// capsule remains free to carry additional, unrelated children later
// without disturbing role discovery -- the same discipline established
// for PointerMetadataRegistryD.
//
// CapsuleRegistry does not itself know about lists, heads, or tails, and
// does not itself decide when a capsule is linked into or unlinked from a
// list -- it only mints and wires individual capsules and their three
// roles. List-level operations (append/prepend/insert, head/tail
// bookkeeping) are a separate, higher layer to be built on top of this
// one; not implemented yet. Per discussion, head/tail is expected to be a
// plain (AllHEADs, capsule) / (AllTAILs, capsule) tag pair discovered via
// findUniqueTaggedChild, not a further Pointer-style indirection: unlike
// Representation C/D's subject/target collision risk, (AllHEADs, X) and
// (AllTAILs, X) are already two distinct relationships even when the same
// capsule X is simultaneously both head and tail (a single-element list).
type CapsuleRegistry struct {
	graph              *Graph
	allElementCapsules NodeID
	prevSlots          *PointerRegistry
	valueSlots         *PointerRegistry
	nextSlots          *PointerRegistry
}

// NewCapsuleRegistry creates a CapsuleRegistry over graph.
// allElementCapsules tags capsule-kind nodes; allPrevSlot, allValueSlot,
// and allNextSlot each tag a capsule's respective role-slot node. All
// four must already exist -- typically via
// NameRegistry.BootstrapNames(FoundationalNames). allPrevSlot,
// allValueSlot, and allNextSlot's existence is checked by the embedded
// NewPointerRegistry calls; allElementCapsules is checked here.
func NewCapsuleRegistry(graph *Graph, allElementCapsules, allPrevSlot, allValueSlot, allNextSlot NodeID) (*CapsuleRegistry, error) {
	if !graph.NodeExists(allElementCapsules) {
		return nil, ErrNodeNotFound
	}

	prevSlots, err := NewPointerRegistry(graph, allPrevSlot)
	if err != nil {
		return nil, err
	}

	valueSlots, err := NewPointerRegistry(graph, allValueSlot)
	if err != nil {
		return nil, err
	}

	nextSlots, err := NewPointerRegistry(graph, allNextSlot)
	if err != nil {
		return nil, err
	}

	return &CapsuleRegistry{
		graph:              graph,
		allElementCapsules: allElementCapsules,
		prevSlots:          prevSlots,
		valueSlots:         valueSlots,
		nextSlots:          nextSlots,
	}, nil
}

// IsCapsule reports whether id is currently tagged
// (AllElementCapsules, id).
func (c *CapsuleRegistry) IsCapsule(id NodeID) bool {
	return c.graph.HasRelationship(c.allElementCapsules, id)
}

// slotFor returns capsule's role-slot child tagged via (tag, slot) --
// e.g. its prev, value, or next slot -- found by tag rather than by
// position, so a capsule may carry additional, unrelated children later
// without disturbing role discovery. found is false if capsule has no
// such slot yet, which for a capsule created via NewCapsule should only
// happen due to an out-of-band mutation, since NewCapsule always creates
// all three slots up front. capsule's existence is checked implicitly by
// the underlying FindOutgoing call (via findUniqueTaggedChild), which
// returns ErrNodeNotFound if capsule does not exist.
func (c *CapsuleRegistry) slotFor(capsule, tag NodeID) (slot NodeID, found bool, err error) {
	return findUniqueTaggedChild(c.graph, capsule, tag)
}

// buildCapsuleTx creates a fresh capsule NodeID, tags it via
// (allElementCapsules, capsule), and wires all three of its role slots
// (prev, value, next) -- each via the shared newPointerTx create-and-tag
// sequence -- against tx. This is the tx-composable core behind
// CapsuleRegistry.NewCapsule (via its newCapsuleTx method below),
// factored out as a free function, parameterized entirely over tag
// NodeIDs, so a larger composite operation (ListRegistry.Append/Prepend/
// InsertAfter) can mint a capsule as one step of its own enclosing
// Graph.Transact call instead of CapsuleRegistry opening a second, nested
// one.
//
// The value slot's target is set to value immediately, since a freshly
// created slot trivially satisfies the Pointer invariant (it starts
// childless), exactly like PointerRegistry.NewPointer. The prev and next
// slots are left empty: a capsule with no preceding or following
// neighbor is a normal, valid state -- e.g. a single-element list's sole
// capsule is simultaneously head and tail, with both slots empty.
func buildCapsuleTx(tx txOps, allElementCapsules, allPrevSlot, allValueSlot, allNextSlot, value NodeID) (NodeID, error) {
	capsule, err := createTaggedNodeTx(tx, allElementCapsules)
	if err != nil {
		return 0, err
	}

	prevSlot, err := newPointerTx(tx, allPrevSlot)
	if err != nil {
		return 0, err
	}
	if _, err := tx.AddRelationship(capsule, prevSlot); err != nil {
		return 0, err
	}

	valueSlot, err := newPointerTx(tx, allValueSlot)
	if err != nil {
		return 0, err
	}
	if _, err := tx.AddRelationship(capsule, valueSlot); err != nil {
		return 0, err
	}
	if _, err := tx.AddRelationship(valueSlot, value); err != nil {
		return 0, err
	}

	nextSlot, err := newPointerTx(tx, allNextSlot)
	if err != nil {
		return 0, err
	}

	_, err = tx.AddRelationship(capsule, nextSlot)
	if err != nil {
		return 0, err
	}

	return capsule, nil
}

// newCapsuleTx is CapsuleRegistry's tx-composable wrapper around
// buildCapsuleTx, supplying this registry's own tag NodeIDs. Exists so
// ListRegistry (same package) can mint a capsule as one step of its own
// enclosing Graph.Transact call.
func (c *CapsuleRegistry) newCapsuleTx(tx txOps, value NodeID) (NodeID, error) {
	return buildCapsuleTx(tx, c.allElementCapsules, c.prevSlots.allPointers, c.valueSlots.allPointers, c.nextSlots.allPointers, value)
}

// setSlotTargetTx rewires capsule's role slot -- found via slotTag --
// to target, composed into an existing tx. capsule must already have the
// given role slot (true for any capsule created via NewCapsule/
// newCapsuleTx). This is the tx-composable counterpart of the exported
// SetPrev/SetNext, for callers (ListRegistry) that need to rewire a
// capsule's slot as one step of a larger enclosing transaction rather
// than opening a new Graph.Transact per slot.
func (c *CapsuleRegistry) setSlotTargetTx(tx txOps, capsule, slotTag, target NodeID) error {
	slot, found, err := findUniqueTaggedChild(c.graph, capsule, slotTag)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotCapsule
	}

	return singleChildTargetSetTx(tx, c.graph, slot, target)
}

// setPrevTx rewires capsule's prev-slot to target, composed into an
// existing tx. See setSlotTargetTx.
func (c *CapsuleRegistry) setPrevTx(tx txOps, capsule, target NodeID) error {
	return c.setSlotTargetTx(tx, capsule, c.prevSlots.allPointers, target)
}

// setNextTx rewires capsule's next-slot to target, composed into an
// existing tx. See setSlotTargetTx.
func (c *CapsuleRegistry) setNextTx(tx txOps, capsule, target NodeID) error {
	return c.setSlotTargetTx(tx, capsule, c.nextSlots.allPointers, target)
}

// NewCapsule creates a fresh capsule NodeID, tags it
// (AllElementCapsules, capsule), and wires all three of its role slots
// (prev, value, next), entirely inside one Graph.Transact call, via
// newCapsuleTx.
//
// value must already exist.
func (c *CapsuleRegistry) NewCapsule(value NodeID) (NodeID, error) {
	if !c.graph.NodeExists(value) {
		return 0, ErrNodeNotFound
	}

	var capsule NodeID

	err := c.graph.Transact(func(tx *Txn) error {
		var err error
		capsule, err = c.newCapsuleTx(tx, value)
		return err
	})
	if err != nil {
		return 0, err
	}

	return capsule, nil
}

// Value returns capsule's current value, i.e. its value slot's target.
//
// hasValue is false only if capsule's value slot has no target set --
// which should not occur for any capsule created via NewCapsule, since
// NewCapsule always sets the value slot's target immediately. It can
// only arise from an out-of-band mutation (e.g. RemoveTarget called
// directly through the underlying value PointerRegistry).
func (c *CapsuleRegistry) Value(capsule NodeID) (value NodeID, hasValue bool, err error) {
	slot, found, err := c.slotFor(capsule, c.valueSlots.allPointers)
	if err != nil {
		return 0, false, err
	}
	if !found {
		return 0, false, ErrNotCapsule
	}

	return c.valueSlots.Target(slot)
}

// SetValue replaces capsule's value.
func (c *CapsuleRegistry) SetValue(capsule, value NodeID) error {
	slot, found, err := c.slotFor(capsule, c.valueSlots.allPointers)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotCapsule
	}

	return c.valueSlots.SetTarget(slot, value)
}

// Prev returns capsule's previous-capsule link, if any. hasPrev is false
// for a capsule currently at the head of its list.
func (c *CapsuleRegistry) Prev(capsule NodeID) (prev NodeID, hasPrev bool, err error) {
	slot, found, err := c.slotFor(capsule, c.prevSlots.allPointers)
	if err != nil {
		return 0, false, err
	}
	if !found {
		return 0, false, ErrNotCapsule
	}

	return c.prevSlots.Target(slot)
}

// SetPrev sets capsule's previous-capsule link.
func (c *CapsuleRegistry) SetPrev(capsule, prev NodeID) error {
	slot, found, err := c.slotFor(capsule, c.prevSlots.allPointers)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotCapsule
	}

	return c.prevSlots.SetTarget(slot, prev)
}

// RemovePrev clears capsule's previous-capsule link, if any.
func (c *CapsuleRegistry) RemovePrev(capsule NodeID) (removed bool, err error) {
	slot, found, err := c.slotFor(capsule, c.prevSlots.allPointers)
	if err != nil {
		return false, err
	}
	if !found {
		return false, ErrNotCapsule
	}

	return c.prevSlots.RemoveTarget(slot)
}

// Next returns capsule's next-capsule link, if any. hasNext is false for
// a capsule currently at the tail of its list.
func (c *CapsuleRegistry) Next(capsule NodeID) (next NodeID, hasNext bool, err error) {
	slot, found, err := c.slotFor(capsule, c.nextSlots.allPointers)
	if err != nil {
		return 0, false, err
	}
	if !found {
		return 0, false, ErrNotCapsule
	}

	return c.nextSlots.Target(slot)
}

// SetNext sets capsule's next-capsule link.
func (c *CapsuleRegistry) SetNext(capsule, next NodeID) error {
	slot, found, err := c.slotFor(capsule, c.nextSlots.allPointers)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotCapsule
	}

	return c.nextSlots.SetTarget(slot, next)
}

// RemoveNext clears capsule's next-capsule link, if any.
func (c *CapsuleRegistry) RemoveNext(capsule NodeID) (removed bool, err error) {
	slot, found, err := c.slotFor(capsule, c.nextSlots.allPointers)
	if err != nil {
		return false, err
	}
	if !found {
		return false, ErrNotCapsule
	}

	return c.nextSlots.RemoveTarget(slot)
}

// ListRegistry implements Ordered Lists (THEORY_NOTES_FROM_CONVERSATION.md
// section 11 / theorystate_v0.6.md section 11) on top of CapsuleRegistry.
//
// A list is an ordinary node tagged (AllLists, list). List membership --
// which of a list's direct children are actual ElementCapsules, as
// opposed to unrelated metadata or comments a caller might attach later
// -- is identified through the ordinary (list, capsule) containment edge
// combined with the capsule's own (AllElementCapsules, capsule) tag,
// exactly per the discipline already established for CapsuleRegistry: a
// list's direct children are not assumed to all be capsules.
//
// Head and tail are each a plain tag on a capsule -- (AllHeads, capsule)
// and (AllTails, capsule) -- discovered as the single child of list
// tagged accordingly, via findUniqueTaggedChild. This deliberately does
// NOT use a further Pointer-style indirection (a dedicated head/tail
// slot node, the way Representation C/D uses slot nodes for subject/
// target): that indirection exists specifically to prevent two roles
// sharing the same *source* node from colliding into a single primitive
// relationship when their targets happen to be equal (section 1: (A,B)
// is a unique pair). Here the two roles have different sources --
// (AllHeads, X) and (AllTails, X) -- so they can never collide even when
// the same capsule X is simultaneously both head and tail, which is
// exactly the normal, expected state for a single-element list. Adding
// slot indirection here would be pure unneeded overhead.
//
// ListRegistry's mutating operations (NewList, Append, Prepend,
// InsertAfter) each run entirely inside one Graph.Transact call,
// composing CapsuleRegistry's tx-composable newCapsuleTx/setPrevTx/
// setNextTx alongside direct tag (AddRelationship/RemoveRelationship)
// calls against the same tx -- no nested Graph.Transact calls anywhere,
// per the txOps discipline established above.
//
// As with every other registry in this file, list structure is
// re-derived fresh from the Graph on every call rather than cached.
//
// Not yet implemented: removing a capsule from a list (unlinking and
// re-relinking neighbors, adjusting head/tail, without cascading into
// node deletion -- see theorystate_v0.6.md section 18's rejection of
// automatic cascade delete) and deleting a list itself. Both are
// deferred to a future change, not overlooked.
type ListRegistry struct {
	graph    *Graph
	capsules *CapsuleRegistry
	allLists NodeID
	allHeads NodeID
	allTails NodeID
}

// NewListRegistry creates a ListRegistry over graph, using capsules for
// all per-capsule slot operations, allLists to tag list-kind nodes, and
// allHeads/allTails to tag a list's current head/tail capsule. All three
// tag NodeIDs must already exist -- typically via
// NameRegistry.BootstrapNames(FoundationalNames). capsules must already
// be constructed over the same graph.
func NewListRegistry(graph *Graph, capsules *CapsuleRegistry, allLists, allHeads, allTails NodeID) (*ListRegistry, error) {
	if !graph.NodeExists(allLists) {
		return nil, ErrNodeNotFound
	}

	if !graph.NodeExists(allHeads) {
		return nil, ErrNodeNotFound
	}

	if !graph.NodeExists(allTails) {
		return nil, ErrNodeNotFound
	}

	return &ListRegistry{
		graph:    graph,
		capsules: capsules,
		allLists: allLists,
		allHeads: allHeads,
		allTails: allTails,
	}, nil
}

// IsList reports whether id is currently tagged (AllLists, id).
func (l *ListRegistry) IsList(id NodeID) bool {
	return l.graph.HasRelationship(l.allLists, id)
}

// NewList creates a fresh NodeID and tags it (AllLists, id). The new list
// starts empty: no head, no tail, no element capsules.
func (l *ListRegistry) NewList() (NodeID, error) {
	var list NodeID

	err := l.graph.Transact(func(tx *Txn) error {
		var err error
		list, err = createTaggedNodeTx(tx, l.allLists)
		return err
	})
	if err != nil {
		return 0, err
	}

	return list, nil
}

// Head returns list's current head capsule, if any. hasHead is false for
// an empty list.
func (l *ListRegistry) Head(list NodeID) (head NodeID, hasHead bool, err error) {
	if !l.graph.NodeExists(list) {
		return 0, false, ErrNodeNotFound
	}

	if !l.IsList(list) {
		return 0, false, ErrNotList
	}

	return findUniqueTaggedChild(l.graph, list, l.allHeads)
}

// Tail returns list's current tail capsule, if any. hasTail is false for
// an empty list.
func (l *ListRegistry) Tail(list NodeID) (tail NodeID, hasTail bool, err error) {
	if !l.graph.NodeExists(list) {
		return 0, false, ErrNodeNotFound
	}

	if !l.IsList(list) {
		return 0, false, ErrNotList
	}

	return findUniqueTaggedChild(l.graph, list, l.allTails)
}

// Append creates a fresh capsule holding value and links it as the new
// tail of list, entirely inside one Graph.Transact call.
//
// If list is currently empty, the new capsule becomes both head and
// tail. Otherwise the new capsule is wired in after the current tail
// (new capsule's prev -> old tail, old tail's next -> new capsule), the
// old tail loses its AllTails tag, and the new capsule gains it.
//
// list must already be tagged (AllLists, list); value must already
// exist.
func (l *ListRegistry) Append(list, value NodeID) (NodeID, error) {
	if !l.graph.NodeExists(list) {
		return 0, ErrNodeNotFound
	}

	if !l.IsList(list) {
		return 0, ErrNotList
	}

	if !l.graph.NodeExists(value) {
		return 0, ErrNodeNotFound
	}

	var capsule NodeID

	err := l.graph.Transact(func(tx *Txn) error {
		oldTail, hasTail, err := findUniqueTaggedChild(l.graph, list, l.allTails)
		if err != nil {
			return err
		}

		capsule, err = l.capsules.newCapsuleTx(tx, value)
		if err != nil {
			return err
		}

		if _, err := tx.AddRelationship(list, capsule); err != nil {
			return err
		}

		if hasTail {
			if err := l.capsules.setPrevTx(tx, capsule, oldTail); err != nil {
				return err
			}
			if err := l.capsules.setNextTx(tx, oldTail, capsule); err != nil {
				return err
			}
			if _, err := tx.RemoveRelationship(l.allTails, oldTail); err != nil {
				return err
			}
		} else {
			if _, err := tx.AddRelationship(l.allHeads, capsule); err != nil {
				return err
			}
		}

		_, err = tx.AddRelationship(l.allTails, capsule)
		return err
	})
	if err != nil {
		return 0, err
	}

	return capsule, nil
}

// Prepend creates a fresh capsule holding value and links it as the new
// head of list, entirely inside one Graph.Transact call. Exact mirror of
// Append, swapping head/tail and prev/next roles.
//
// list must already be tagged (AllLists, list); value must already
// exist.
func (l *ListRegistry) Prepend(list, value NodeID) (NodeID, error) {
	if !l.graph.NodeExists(list) {
		return 0, ErrNodeNotFound
	}

	if !l.IsList(list) {
		return 0, ErrNotList
	}

	if !l.graph.NodeExists(value) {
		return 0, ErrNodeNotFound
	}

	var capsule NodeID

	err := l.graph.Transact(func(tx *Txn) error {
		oldHead, hasHead, err := findUniqueTaggedChild(l.graph, list, l.allHeads)
		if err != nil {
			return err
		}

		capsule, err = l.capsules.newCapsuleTx(tx, value)
		if err != nil {
			return err
		}

		if _, err := tx.AddRelationship(list, capsule); err != nil {
			return err
		}

		if hasHead {
			if err := l.capsules.setNextTx(tx, capsule, oldHead); err != nil {
				return err
			}
			if err := l.capsules.setPrevTx(tx, oldHead, capsule); err != nil {
				return err
			}
			if _, err := tx.RemoveRelationship(l.allHeads, oldHead); err != nil {
				return err
			}
		} else {
			if _, err := tx.AddRelationship(l.allTails, capsule); err != nil {
				return err
			}
		}

		_, err = tx.AddRelationship(l.allHeads, capsule)
		return err
	})
	if err != nil {
		return 0, err
	}

	return capsule, nil
}

// InsertAfter creates a fresh capsule holding value and links it into
// list immediately after afterCapsule, entirely inside one
// Graph.Transact call.
//
// If afterCapsule was the tail, the new capsule becomes the new tail.
// Otherwise the new capsule is spliced in between afterCapsule and
// afterCapsule's old next capsule.
//
// list must already be tagged (AllLists, list); afterCapsule must
// already be an element of list (checked via the (list, afterCapsule)
// containment edge, returning ErrCapsuleNotInList otherwise); value must
// already exist.
func (l *ListRegistry) InsertAfter(list, afterCapsule, value NodeID) (NodeID, error) {
	if !l.graph.NodeExists(list) {
		return 0, ErrNodeNotFound
	}

	if !l.IsList(list) {
		return 0, ErrNotList
	}

	if !l.graph.NodeExists(value) {
		return 0, ErrNodeNotFound
	}

	if !l.graph.HasRelationship(list, afterCapsule) {
		return 0, ErrCapsuleNotInList
	}

	var capsule NodeID

	err := l.graph.Transact(func(tx *Txn) error {
		oldNext, hasNext, err := l.capsules.Next(afterCapsule)
		if err != nil {
			return err
		}

		capsule, err = l.capsules.newCapsuleTx(tx, value)
		if err != nil {
			return err
		}

		if _, err := tx.AddRelationship(list, capsule); err != nil {
			return err
		}

		if err := l.capsules.setPrevTx(tx, capsule, afterCapsule); err != nil {
			return err
		}
		if err := l.capsules.setNextTx(tx, afterCapsule, capsule); err != nil {
			return err
		}

		if hasNext {
			if err := l.capsules.setNextTx(tx, capsule, oldNext); err != nil {
				return err
			}
			if err := l.capsules.setPrevTx(tx, oldNext, capsule); err != nil {
				return err
			}
			return nil
		}

		if _, err := tx.RemoveRelationship(l.allTails, afterCapsule); err != nil {
			return err
		}
		_, err = tx.AddRelationship(l.allTails, capsule)
		return err
	})
	if err != nil {
		return 0, err
	}

	return capsule, nil
}

// Elements returns list's current values, in head-to-tail order, by
// traversing the capsule chain via CapsuleRegistry.Next.
//
// This is a plain read-only convenience built entirely on existing
// registry methods (Head, then repeated Next/Value) -- it adds no new
// graph structure or discovery mechanism of its own.
func (l *ListRegistry) Elements(list NodeID) ([]NodeID, error) {
	if !l.graph.NodeExists(list) {
		return nil, ErrNodeNotFound
	}

	if !l.IsList(list) {
		return nil, ErrNotList
	}

	var values []NodeID

	current, hasCurrent, err := findUniqueTaggedChild(l.graph, list, l.allHeads)
	if err != nil {
		return nil, err
	}

	for hasCurrent {
		value, hasValue, err := l.capsules.Value(current)
		if err != nil {
			return nil, err
		}
		if hasValue {
			values = append(values, value)
		}

		current, hasCurrent, err = l.capsules.Next(current)
		if err != nil {
			return nil, err
		}
	}

	return values, nil
}
