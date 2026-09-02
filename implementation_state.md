Project Implementation State

Current implementation:
- Toy implementation in Go 1.27.
- Implementation consolidated into main.go and main_test.go.
- Primitive Graph implemented and tested.
- RootGraph implemented and tested.
- Named nodes use an external name -> NodeID registry.
- ROOT is a real NodeID with special semantics only in RootGraph.

Completed milestones:
1. Primitive NodeID/relationship graph
2. Relationship queries
3. Node deletion rules
4. ROOT virtual overlay semantics
5. Consolidation into main.go / main_test.go

Current next task:
- AllPointers now exists as a foundational name (see Resolved this
  session, item 3); the tagging relationship (AllPointers, P) is proven
  end to end.
- Decide, and only then implement, the smallest possible higher-level
  Pointer processor that enforces "at most one target" for nodes tagged
  (AllPointers, P) — per THEORY_NOTES_FROM_CONVERSATION.md section 9 and
  theorystate_v0.6.md section 10, this invariant belongs above the
  primitive Graph/NameRegistry layer, not inside it.
- Add further foundational names (AllSubPointers, AllDomainPointers,
  AllCapsules, allHEADs, allTAILs, ...) to FoundationalNames only when
  actually starting the corresponding representation's implementation,
  not preemptively.

After that:
- Continue building the generic tagging machinery.
- Use relationships such as (AllPointers, P) to express tags.
- Do not prematurely implement Set/List/Pointer semantics in the primitive layer.

Important constraints:
- Primitive Graph must remain unaware of ROOT and higher-level tags.
- NodeID width is an implementation detail, not a theory decision.
- No NoNode == 0 sentinel.
- Named node => exactly one NodeID; no multiple names for one NodeID.
- Cardinality constraints belong to higher layers.
- Keep alternative representations open where theory has not decided them.

Editing convention:
- Keep implementation consolidated in main.go and main_test.go.
- For changes, give exact:
  Find this text:
  ...
  Replace with:
  ...
  
  
Resolved this session:
1. Node deletion and the name registry now have a coordinated path.
 NameRegistry.DeleteNode(id) deletes id from the underlying Graph and,
 only if that succeeds, removes any name association for id from the
 registry. Graph.DeleteNode itself remains unaware of NameRegistry, per
 the existing layering rule — raw Graph.DeleteNode is still available and
 still does not coordinate anything; callers who need the name registry
 kept in sync must go through NameRegistry.DeleteNode instead. Covered by
 TestNameRegistryDeleteNodeRemovesNameAssociation,
 TestNameRegistryDeleteNodeFailsIfNotEmpty, and
 TestNameRegistryDeleteNodeWithoutNameWorks.

2. RootGraph.DeleteNode(ROOT) now returns a dedicated ErrCannotDeleteRoot
 instead of reusing ErrNodeNotEmpty. The two failures are semantically
 different: ErrNodeNotEmpty is resolved by clearing relationships and
 retrying; ErrCannotDeleteRoot cannot be resolved that way at all, since
 ROOT is structurally protected regardless of its relationship count.
 Covered by the updated TestRootCannotDeleteRoot.

3. Name bootstrapping is now idempotent and resumable at the registry
 level. NameRegistry.EnsureNamedNode(name) returns the existing NodeID if
 name is already bound, or creates and binds a fresh node exactly like
 CreateNamedNode if not. NameRegistry.BootstrapNames(names) applies
 EnsureNamedNode across a list and returns map[string]NodeID; it is
 naturally resumable because each step is independently idempotent, so a
 partial failure (e.g. ErrNodeIDExhausted partway through) simply leaves
 already-bound names untouched for a later retry to build on.
 FoundationalNames is the single DRY source of truth for which names must
 exist; it currently contains only NameAllPointers ("AllPointers"),
 tagging a node as Pointer-kind via the relationship (AllPointers, P).
 Covered by TestNameRegistryEnsureNamedNodeCreatesWhenMissing,
 TestNameRegistryEnsureNamedNodeIsIdempotent,
 TestNameRegistryEnsureNamedNodeFindsExistingBinding,
 TestBootstrapNamesCreatesAllNames, TestBootstrapNamesIsIdempotent,
 TestBootstrapNamesResumesAcrossOverlappingCalls,
 TestBootstrapNamesHandlesDuplicateNamesInList,
 TestFoundationalNamesIncludesAllPointers, and
 TestAllPointersTagsPointerViaRelationship.

Note for future external-metadata structures: this NameRegistry gap is one
instance of a general pattern (external bookkeeping keyed by NodeID can
outlive the NodeID once it's deleted, unless the owning layer coordinates
its own delete). Expect the same shape of problem to recur for any future
NodeID-keyed structure outside the primitive graph.

Currently unaddressed yet:
(none identified this session — flag anything new here as it comes up.)
