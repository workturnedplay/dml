Project Implementation State

Current implementation:
- Toy implementation in Go 1.27.
- Implementation consolidated into main.go and main_test.go.
- Primitive Graph implemented and tested.
- Graph.Transact / Txn implemented and tested, giving failure-atomicity
  (not concurrency isolation) to multi-step primitive operations.
- RootGraph implemented and tested.
- PointerRegistry implemented and tested, enforcing the Pointer invariant
  (at most one target) for nodes tagged (AllPointers, P), per
  THEORY_NOTES_FROM_CONVERSATION.md section 7 / theorystate_v0.6.md
  section 10. Its multi-step operations run inside Graph.Transact.
- Named nodes use an external name -> NodeID registry. Its
  CreateNamedNode also runs inside Graph.Transact.
- ROOT is a real NodeID with special semantics only in RootGraph.

Completed milestones:
1. Primitive NodeID/relationship graph
2. Relationship queries
3. Node deletion rules
4. ROOT virtual overlay semantics
5. Consolidation into main.go / main_test.go

Current next task:
- The Pointer processor (PointerRegistry) is now implemented and tested
  (see Resolved this session, item 4): it enforces "at most one target"
  for nodes tagged (AllPointers, P), offers both NewPointer()
  (mint-fresh-and-tag) and TagAsPointer() (tag-existing, rejecting nodes
  that already have 2+ children), and re-derives the invariant fresh from
  the Graph on every call rather than caching it, so out-of-band
  violations via raw Graph mutation are always detected loudly
  (ErrTooManyPointerTargets) rather than silently trusted or repaired.
- Not yet implemented: Representation B (intermediary pointer node,
  THEORY_NOTES_FROM_CONVERSATION.md section 7B) and Representation C
  (metadata structure, section 7C). Only Representation A (direct child)
  exists so far. Whether/when either alternative representation is
  actually needed is still open.
- Also still open: whether a commit-time interception mechanism
  (theorystate_v0.6.md section 73) should eventually replace the
  re-check-every-call approach; not needed yet since there is exactly one
  processor (PointerRegistry) and no concurrent mutation.
- Add further foundational names (AllSubPointers, AllDomainPointers,
  AllCapsules, allHEADs, allTAILs, ...) to FoundationalNames only when
  actually starting the corresponding representation's implementation,
  not preemptively.

After that:
- Continue building the generic tagging machinery.
- Use relationships such as (AllPointers, P) to express tags.
- Do not prematurely implement Set/List semantics in the primitive layer.

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

4. PointerRegistry implements Representation A (direct child) of the
 Pointer processor described in THEORY_NOTES_FROM_CONVERSATION.md section
 7 and theorystate_v0.6.md section 10: (AllPointers, P) tags P as
 Pointer-kind, and P's target, if any, is enforced to be at most one
 direct child of P. Two ways to obtain a tagged Pointer node are
 provided: NewPointer() mints a fresh NodeID and tags it (trivially
 satisfying the invariant since it starts childless), and TagAsPointer(id)
 tags an existing node, refusing (ErrTooManyPointerTargets) if id already
 has 2+ outgoing relationships. SetTarget/RemoveTarget/Target all
 re-derive P's current target set fresh from the Graph on every call
 rather than caching it; if that set already has more than one member —
 meaning some caller bypassed PointerRegistry and mutated a tagged node
 directly through Graph.AddRelationship — every PointerRegistry method
 fails loudly with ErrTooManyPointerTargets and makes no changes, rather
 than silently repairing or silently trusting stale expectations. This
 mirrors the fail-loud-not-silently-repair discipline already used for
 ErrNameBoundToDeletedNode in NameRegistry. Multi-step operations (SetTarget's replace path, NewPointer's
 create-then-tag) now run inside Graph.Transact (see item 6 below), so a
 later step failing undoes an earlier step's mutation instead of leaving
 orphaned or half-changed state; this closes what was originally an
 accepted, pre-existing gap shared with NameRegistry.CreateNamedNode.
 Graph.Transact provides failure-atomicity only, not concurrency
 isolation -- true first-class multi-primitive-operation transactions as
 a graph concept remain theorystate_v0.6.md section 14/45, both still
 OPEN. Covered by
 TestNewPointerRegistryRequiresExistingAllPointers,
 TestPointerRegistryNewPointerStartsEmpty,
 TestPointerRegistrySetTargetAddsFirstTarget,
 TestPointerRegistrySetTargetIsIdempotentForSameTarget,
 TestPointerRegistrySetTargetReplacesExistingTarget,
 TestPointerRegistrySetTargetAllowsSelfTarget,
 TestPointerRegistrySetTargetRequiresExistingTarget,
 TestPointerRegistrySetTargetRequiresPointerTag,
 TestPointerRegistryRemoveTargetRemovesExisting,
 TestPointerRegistryRemoveTargetNoOpWhenEmpty,
 TestPointerRegistryTagAsPointerTagsFreshNode,
 TestPointerRegistryTagAsPointerAllowsExistingSingleChild,
 TestPointerRegistryTagAsPointerRejectsMultipleExistingChildren,
 TestPointerRegistryTagAsPointerIsIdempotent, and
 TestPointerRegistryDetectsOutOfBandInvariantViolation.

5. RootGraph.AddRelationship and RootGraph.RemoveRelationship each had a
 dead inner `if to == r.root {...} else {...}` branch whose two arms
 always returned the identical result (false, nil either way) —
 simplified to a single `if from == r.root { return false, nil }` in
 both methods. No behavior change; existing tests
 (TestRootAddRelationshipDoesNotPhysicallyStoreVirtualRelationship,
 TestRootRemoveRelationshipCannotRemoveVirtualRelationship,
 TestRootDoesNotPointToItself, and others) still cover both the
 to == root and to != root cases and continue to pass.

6. Added Graph.Transact(func(tx *Txn) error) error and the accompanying
 Txn type, giving failure-atomicity to multi-step primitive operations:
 if the function passed to Transact returns an error or panics, every
 mutation performed through tx is undone, in reverse (LIFO) order,
 before the error/panic propagates. This closes a real, previously
 unaddressed gap: NameRegistry.CreateNamedNode's CreateNode-then-Bind
 and PointerRegistry's CreateNode-then-tag / remove-then-add sequences
 all used to commit each step immediately and unconditionally, so a
 later step failing after an earlier one had already succeeded could
 leave a node created-but-unnamed, or a Pointer left targetless because
 its old target was removed before the new one could be added. Both
 NameRegistry.CreateNamedNode and PointerRegistry.NewPointer/SetTarget
 now run their multi-step sequences through Graph.Transact.
 Txn deliberately provides failure-atomicity only, not isolation from
 concurrent access -- nothing currently runs concurrently
 (theorystate_v0.6.md section 19), so this was not attempted. Txn also
 does not support undoing DeleteNode (would require resurrecting a
 specific NodeID outside the normal monotonic counter -- deferred until
 an actual caller needs it) and is not designed to nest
 (theorystate_v0.6.md section 45, still OPEN). Covered by
 TestTransactCommitsMutationsOnSuccess,
 TestTransactRollsBackCreateNodeOnLaterFailure,
 TestTransactRollsBackRelationshipsInLIFOOrder,
 TestTransactRollsBackRemoveRelationshipOnLaterFailure,
 TestTransactDoesNotUndoPreexistingRelationship, and
 TestTransactRollsBackOnPanic. Note: through today's public API, the
 specific failure points these tests protect against (Bind failing after
 CreateNode; AddRelationship failing after RemoveRelationship or after
 CreateNode) are not actually reachable by NameRegistry/PointerRegistry
 callers, since each has its own pre-check that already guarantees the
 later step will succeed -- so the direct proof of Txn's rollback
 behavior lives in the Txn-level tests above, not in a forced-failure
 integration test through NameRegistry or PointerRegistry. The
 protection is still real and intentional: it removes the dependency on
 that pre-check reasoning staying true as this code evolves.

Currently unaddressed yet:
- No commit-time interception exists to prevent a raw
  Graph.AddRelationship from creating a second child on an
  already-tagged Pointer node in the first place; PointerRegistry can
  only detect the violation after the fact, on its next call for that
  node (see item 4 above and theorystate_v0.6.md section 73). This is a
  different concern from item 6's Txn: Txn makes a registry's own
  multi-step sequence atomic against its own later failure; it does
  nothing to stop an unrelated caller from bypassing the registry
  entirely via the raw Graph. Whether/when a real interception mechanism
  (theorystate_v0.6.md section 73) is worth building is open.
- Txn does not support transactional DeleteNode, and does not support
  nesting one Graph.Transact call inside another. Neither is needed by
  any current caller; add support if and when one actually needs it.
