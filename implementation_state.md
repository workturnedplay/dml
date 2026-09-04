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
- The Pointer processor is now implemented across all four
  representations described in THEORY_NOTES_FROM_CONVERSATION.md section
  7 / theorystate_v0.6.md section 10 (this bullet list replaces a
  previously stale version of itself that still claimed only
  Representation A existed after B and C had already been completed --
  see Resolved this session, item 8):
  - Representation A (direct child): PointerRegistry, tagged via
    AllPointers.
  - Representation B (intermediary pointer node): PointerRegistry reused
    unmodified, tagged via AllSubPointers instead (item 7; see
    TestSubPointerReusesPointerRegistryUnderDifferentTag).
  - Representation C (metadata structure, exclusion-based target lookup):
    PointerMetadataRegistry, tagged via AllPointerMetadata /
    AllPointerMetadataSubjectSlot. Deliberately kept with its known
    exclusion-based limitation -- see its doc comment and
    theorystate_v0.6.md section 10a -- rather than patched, since a
    stricter-than-necessary lower layer is useful for testing higher-layer
    reactions (section 73).
  - Representation D (corrected metadata structure, tag-based target
    lookup): PointerMetadataRegistryD, adding
    AllPointerMetadataTargetSlot. This is the construction
    THEORY_NOTES_FROM_CONVERSATION.md section 10 should have described
    from the start; see that section's correction and
    theorystate_v0.6.md section 10a for why Representation C's
    exclusion-based approach doesn't generalize safely (item 8).
- Open: whether a commit-time interception mechanism (theorystate_v0.6.md
  section 73) should eventually replace the re-check-every-call approach
  used by every registry above; not needed yet since there is exactly one
  writer and no concurrent mutation.
- Add further foundational names (AllDomainPointers, AllCapsules,
  allHEADs, allTAILs, ...) to FoundationalNames only when actually
  starting the corresponding representation's implementation, not
  preemptively.

After that:
- The generic tagging machinery this bullet used to point at as future
  work is already built and validated: FoundationalNames + BootstrapNames
  gives tags real NodeID identity (no hardcoded strings reach the
  primitive Graph), PointerRegistry is reused unmodified across
  Representations A and B by parameterizing on which tag NodeID it's
  constructed with (no branching on tag identity), and generic
  (Tag,X) membership querying is already covered by the existing
  Graph.FindOutgoing(tag) -- no further abstraction is needed for any of
  this. See theorystate_v0.6.md section 76 for the formal writeup. This
  bullet is corrected here because it had gone stale without being
  updated when that work landed.
- Ordered Lists (THEORY_NOTES_FROM_CONVERSATION.md section 11,
  theorystate section 11) has been chosen as the next higher-level
  structure over Sets (theory section 9 / theorystate section 32's open
  definitional questions are still genuinely unresolved design forks,
  whereas the List representation is already largely settled); Domains
  (theory section 6) and Sets remain for later, Domains likely wanting
  Sets first since it's framed as a constrained Set.

  The ElementCapsule primitive (CapsuleRegistry, see item 10 above) is
  now implemented: list -> ElementCapsule does not by itself make every
  list child a capsule; capsules are identified through
  AllElementCapsules (renamed from the theory docs' illustrative
  "AllCapsules"), and the capsule's previous, value, and next
  intermediary slots are each discovered through their own role tag
  rather than by child position.

  The list node itself, head/tail bookkeeping (AllHeads/AllTails tags,
  per the discussion recorded in item 10), and list-level append/
  prepend/insert-after are now implemented as ListRegistry (item 11).
  Not yet implemented: removing a capsule from a list, and deleting a
  list itself.
- Do not prematurely implement Set/List semantics in the primitive layer,
  regardless of which is chosen -- they remain higher-layer constructions,
  per the same discipline that kept Pointer semantics entirely out of
  Graph.

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

7. PointerRegistry generalized (doc-only, no code change) to cover Representation B via a second instance tagged AllSubPointers; PointerMetadataRegistry added implementing Representation C, using a subject-slot indirection node discovered during design — a naive M->subject/M->target two-edge scheme cannot represent self-targeting since the two edges would collapse into one relationship; singleChildTarget extracted from PointerRegistry.currentTarget as shared DRY logic used by both registries.


8. Fixed a real design bug in Representation C / theory section 10, found
 during review: identifying "the target" as literally "whichever child
 of M isn't the tagged subject-slot" (exclusion) silently assumes M can
 never have any other child, ever -- contradicting the construction's
 own stated purpose of letting M grow structure later without
 disturbing what's already there. THEORY_NOTES_FROM_CONVERSATION.md
 section 10 has been corrected (its original "M -> P, M -> I" sketch
 had an even sharper version of the same bug: those two relationships
 collapse into one whenever target == subject, since primitive
 relationships are unique pairs). Added PointerMetadataRegistryD
 (Representation D), which gives both the subject and the target their
 own freshly-minted, independently-tagged slot node
 (AllPointerMetadataSubjectSlot, reused from Representation C, and the
 new AllPointerMetadataTargetSlot), each discovered by its own tag
 rather than by exclusion. Representation C (PointerMetadataRegistry)
 is kept exactly as-is rather than patched or removed -- see its doc
 comment for why the limitation is now considered a deliberate, useful
 restriction rather than an oversight. Shared subject-slot discovery/
 creation logic (previously living only inside PointerMetadataRegistry)
 was factored out into locateBySubjectSlot and
 ensureMetadataWithSubjectSlot, now used by both PointerMetadataRegistry
 and PointerMetadataRegistryD; a new findUniqueTaggedChild helper (the
 forward-lookup counterpart to the existing findUniqueTaggedParent) was
 added to support D's tag-based target-slot discovery. Also corrected
 this file's own stale "Current next task" section below, which had not
 been updated when Representations B and C were completed (item 7) and
 still claimed only Representation A existed. Covered by
 TestNewPointerMetadataRegistryDRequiresExistingTags,
 TestPointerMetadataRegistryDHasMetadataFalseInitially,
 TestPointerMetadataRegistryDSetTargetLeavesSubjectChildrenUntouched,
 TestPointerMetadataRegistryDSetTargetAllowsSelfTarget,
 TestPointerMetadataRegistryDSetTargetReplacesExistingTarget,
 TestPointerMetadataRegistryDRemoveTargetRemovesExisting,
 TestPointerMetadataRegistryDSetTargetRequiresExistingTarget, and
 TestPointerMetadataRegistryDAllowsUnrelatedMetadataChildren (the last
 of which directly demonstrates the fix: adding an unrelated child to M
 does not break Representation D's target discovery, unlike
 Representation C's).

9. Removed duplication between PointerMetadataRegistry and
 PointerMetadataRegistryD: both types had byte-for-byte identical
 locate/ensureMetadata/EnsureMetadata/HasMetadata methods, since both
 representations locate or create a subject's metadata/subject-slot pair
 exactly the same way (via the already-shared locateBySubjectSlot /
 ensureMetadataWithSubjectSlot helpers) and differ only in target
 discovery. Factored the shared graph/tag fields and these four methods
 into a new subjectMetadataBase struct, which both types now embed
 anonymously; Go's field/method promotion means every existing call site
 (m.graph, m.locate(...), m.ensureMetadata(...), and the exported
 EnsureMetadata/HasMetadata called from outside the package) keeps
 working unchanged. Pure refactor, no behavior change -- all existing
 tests for both types continue to cover this without modification.

10. Added the txOps interface, satisfied unmodified by both *Graph and
 *Txn, plus three small helpers built on it: createTaggedNodeTx (create
 a node and tag it via (tag, id)), newPointerTx (thin wrapper for
 Pointer-kind tagging), and setPointerTargetTx (the shared remove-old/
 add-new replace sequence). PointerRegistry.NewPointer/SetTarget,
 ensureMetadataWithSubjectSlot, and PointerMetadataRegistryD.SetTarget
 were refactored to call these instead of repeating the same
 CreateNode-then-AddRelationship / RemoveRelationship-then-
 AddRelationship sequences inline; behavior and test coverage are
 unchanged. This exists to support composing multiple registries'
 create/wire sequences into one atomic operation without nesting
 Graph.Transact calls, which Txn does not support (see the Txn doc
 comment) -- the first consumer of this is CapsuleRegistry.NewCapsule
 below, which composes three separate Pointer-style slot creations plus
 its own tagging into a single Transact call.

 Added CapsuleRegistry, implementing the ElementCapsule primitive of
 Ordered Lists (THEORY_NOTES_FROM_CONVERSATION.md section 11 /
 theorystate_v0.6.md section 11): AllElementCapsules tags capsule-kind
 nodes (renamed from the theory docs' illustrative "AllCapsules" to
 avoid implying a more generic capsule concept); each capsule's prev,
 value, and next roles are represented by a dedicated PointerRegistry
 instance under its own tag (AllElementCapsulePrevSlot /
 AllElementCapsuleValueSlot / AllElementCapsuleNextSlot) -- Pointer
 Representation B applied three times, reusing PointerRegistry
 unmodified rather than reimplementing "at most one target" a third
 time (theorystate_v0.6.md section 76). Roles are discovered by tag via
 findUniqueTaggedChild, not by position or exclusion, matching the
 discipline established for PointerMetadataRegistryD. NewCapsule wires
 the capsule's own tag and all three slots inside one Graph.Transact
 call via the new txOps helpers. Covered by
 TestFoundationalNamesIncludesElementCapsuleNames,
 TestNewCapsuleRegistryRequiresExistingTags,
 TestNewCapsuleRequiresExistingValue, TestNewCapsuleTagsAndSetsValue,
 TestNewCapsuleStartsWithNoPrevOrNext,
 TestCapsuleSetPrevAndNextLinkCapsules, TestCapsuleRemovePrevAndNext,
 and TestCapsuleOperationsRequireCapsuleTag.

 CapsuleRegistry does not yet implement list-level concepts (head/tail
 bookkeeping, append/prepend/insert-after, or list membership itself,
 i.e. AllLists-style tagging) -- only individual capsule creation and
 prev/value/next wiring. That higher layer is deliberately deferred, per
 the discussion that head/tail should be plain tags (AllHEADs/AllTAILs)
 on capsules discovered via findUniqueTaggedChild, not a further
 Pointer-style indirection -- there is no collision risk analogous to
 Representation C/D's subject/target collision, since (AllHEADs, X) and
 (AllTAILs, X) are already two distinct relationships even when the same
 capsule X is currently both head and tail.

11. Added ListRegistry, implementing Ordered Lists
 (THEORY_NOTES_FROM_CONVERSATION.md section 11 / theorystate_v0.6.md
 section 11) on top of CapsuleRegistry (item 10). A list is an ordinary
 node tagged (AllLists, list). List membership is the ordinary
 (list, capsule) containment edge combined with the capsule's own
 (AllElementCapsules, capsule) tag -- a list's direct children are not
 assumed to all be capsules, per the existing tagging discipline.

 Head and tail are plain tags on a capsule -- (AllHeads, capsule) /
 (AllTails, capsule) -- discovered via findUniqueTaggedChild, not a
 further Pointer-style slot indirection: unlike Representation C/D's
 subject/target, which share the same source node and can therefore
 collide, AllHeads and AllTails are different sources and so can never
 collide even when the same capsule is simultaneously both head and
 tail (the normal state for a single-element list). Named AllHeads/
 AllTails (PascalCase, consistent with every other tag name in this
 file) rather than reproducing the theory docs' illustrative
 allHEADs/allTAILs styling verbatim.

 Append/Prepend/InsertAfter each run entirely inside one Graph.Transact
 call. This required extending the txOps composability work from item
 10 one level further: CapsuleRegistry gained a tx-composable
 newCapsuleTx (built on a new free function, buildCapsuleTx, since the
 previous newCapsuleTx name was already a method) and setPrevTx/
 setNextTx (built on a new singleChildTargetSetTx helper, the
 tx-composable counterpart of PointerRegistry.SetTarget's read-current/
 idempotency-check/replace sequence) -- so ListRegistry can mint a
 capsule and rewire its slots as steps of its own enclosing transaction
 without nesting Graph.Transact calls. CapsuleRegistry.NewCapsule itself
 was refactored to call the new newCapsuleTx, with no behavior change.

 Elements(list) is a read-only convenience traversing head-to-tail via
 the existing Head/Next/Value methods; it adds no new graph structure.

 Covered by TestFoundationalNamesIncludesListNames,
 TestNewListRegistryRequiresExistingTags,
 TestNewListTagsListAndStartsEmpty,
 TestListAppendSingleElementIsHeadAndTail,
 TestListAppendMultipleMaintainsOrder, TestListPrependAddsAtFront,
 TestListInsertAfterMiddle, TestListInsertAfterTailUpdatesTail,
 TestListInsertAfterRequiresCapsuleInList, and
 TestListOperationsRequireListTag.

12. Added ListRegistry.Remove and ListRegistry.DeleteList, closing the
 two gaps item 11 deliberately deferred.

 Remove(list, capsule) unlinks capsule from list: relinks capsule's
 neighbors around the gap (or updates head/tail if capsule was an
 endpoint, or clears both if capsule was the sole element), and clears
 capsule's own prev/next slots, entirely inside one Graph.Transact call.
 This needed the txOps composability chain extended one more level:
 singleChildTargetRemoveTx (the tx-composable counterpart of
 PointerRegistry.RemoveTarget -- a plain, non-transactional
 RemoveRelationship call is not safe to reuse here, since it would not
 be recorded in the enclosing transaction's undo log) and
 CapsuleRegistry.removeSlotTargetTx/removePrevTx/removeNextTx built on
 it, mirroring the existing setSlotTargetTx/setPrevTx/setNextTx. Remove
 does not delete or untag capsule -- list membership is a separate
 concern from capsule-kind/value identity, per theory section 8.

 DeleteList(list) deletes list from the underlying graph and its
 (AllLists, list) tag together, inside one Graph.Transact call. This is
 a different shape of coordinated delete than
 NameRegistry.DeleteNode's: the AllLists tag is itself an ordinary
 primitive relationship *into* list, so it must be removed *before*
 Graph.DeleteNode can succeed (not after, the way NameRegistry cleans up
 its purely-external bookkeeping) -- Transact's rollback is what makes
 this safe if DeleteNode then fails with ErrNodeNotEmpty. Per
 theorystate_v0.6.md section 18, this is deliberately "delete only if
 empty," not cascade; callers must Remove every element first.

 Covered by TestListRemoveMiddleElement, TestListRemoveHeadUpdatesHead,
 TestListRemoveTailUpdatesTail, TestListRemoveSoleElementEmptiesList,
 TestListRemoveClearsCapsuleOwnLinks, TestListRemoveRequiresCapsuleInList,
 TestListRemoveRequiresListTag, TestListDeleteListRequiresListTag,
 TestListDeleteListFailsIfNotEmpty, TestListDeleteListSucceedsWhenEmpty,
 and TestListRemoveThenDeleteListSucceeds.

 Ordered Lists are now feature-complete for the primitives currently
 scoped: creation, append/prepend/insert-after/remove, head/tail/
 traversal, and list deletion.

13. Added value-membership queries -- CapsuleRegistry.CapsulesWithValue,
 ListRegistry.OccurrencesOf, and ListRegistry.Contains -- resolving what
 item 12 above and theory section 11 had flagged as a possible later
 "Set-like index" addition. No new node, tag, or index structure turned
 out to be needed: CapsulesWithValue is a pure reverse lookup from a
 value's own Graph.FindIncoming, filtered to genuine value-slot parents
 (via the existing valueSlots PointerRegistry's IsPointer) and resolved
 to an owning capsule via the existing findUniqueTaggedParent, tagged
 AllElementCapsules.

 An earlier draft of this used a new, untagged findUniqueParent helper
 instead, requiring a slot to have exactly one parent full stop -- caught
 in review as wrong, not merely stricter than necessary: a role-slot node
 is ordinary graph structure, and nothing prevents some future unrelated
 construct from also pointing at it (a node may have any number of
 parents, THEORY_NOTES_FROM_CONVERSATION.md section 1), which must not be
 confused with a second owning capsule or make the lookup fail.
 findUniqueTaggedParent already has exactly the right semantics -- ignore
 any number of non-capsule-tagged parents, only object if two distinct
 capsule-tagged parents both claim the same slot -- so no new helper or
 error type was needed; ambiguity surfaces via the existing
 ErrAmbiguousPointerMetadata, exactly as it already does for
 findUniqueTaggedChild's other callers elsewhere in this file.

 ListRegistry.OccurrencesOf filters CapsulesWithValue's candidates down
 to the ones satisfying the existing (list,capsule) containment edge --
 the same O(1) check InsertAfter/Remove already use -- and Contains is a
 thin wrapper reporting whether any occurrence was found. Running time is
 proportional to how many places a value is referenced anywhere in the
 graph, not to the length of any particular list.

 Covered by TestCapsulesWithValueFindsAllOccurrences,
 TestCapsulesWithValueIgnoresUnrelatedEdges,
 TestCapsulesWithValueIgnoresUnrelatedParentsOfSlot,
 TestCapsulesWithValueDetectsAmbiguousCapsuleOwnership,
 TestListContainsFindsValue, TestListContainsFalseForAbsentValue,
 TestListContainsScopedToOwningList,
 TestListOccurrencesOfFindsDuplicates, TestListContainsRequiresListTag,
 and TestListContainsRequiresExistingValue.

14. Added CapsuleRegistry.DeleteCapsule and ListRegistry.RemoveAndDelete,
 resolving item 13's "not yet addressed" note about deleting a fully
 detached capsule.

 DeleteCapsule deletes a capsule and all three of its role-slot nodes
 together, inside one Graph.Transact call, but only if every one of
 those four nodes currently has *exactly* the fixed shape buildCapsuleTx
 itself establishes and nothing more -- no list membership or head/tail
 tag on the capsule, no target still set on the prev/next slots, and no
 unrelated parent added to any slot by something else. This is
 deliberately all-or-nothing: if any of the four nodes has so much as
 one relationship beyond its own fixed structure, DeleteCapsule changes
 nothing and returns the new ErrCapsuleNotEmpty (the CapsuleRegistry-
 level analogue of ErrNodeNotEmpty), rather than deleting whichever
 parts happen to be clean and leaving a broken, partially-torn-down
 capsule behind.

 Getting this right required a new shared helper, nodeIsEmpty, and a
 specific ordering discipline: Txn cannot undo a Graph.DeleteNode call
 once it succeeds, so deleting four related nodes inside one transaction
 is only safe if every one of them is first *proven* -- read-only, via
 nodeIsEmpty, after all of this operation's own relationship removals
 have gone through tx (and are therefore still fully undoable) -- to
 already have zero relationships left, before any Graph.DeleteNode call
 is made at all. Only once all four are confirmed empty are the four
 deletes performed, grouped last and in sequence -- see
 theorystate_v0.6.md section 78 for the generalized principle.

 ListRegistry.RemoveAndDelete composes Remove and DeleteCapsule as two
 separate, sequential Graph.Transact calls, not one joint transaction:
 Remove's own step always fully commits on its own terms, and
 DeleteCapsule is then attempted as a best-effort second step. If
 capsule turns out not to be safely deletable, RemoveAndDelete reports
 deleted=false with no error, rather than rolling back the removal too
 -- a capsule that legitimately cannot be deleted still ends up fully,
 successfully removed from the list. This is a new, separate method, not
 a change to Remove's existing behavior: Remove's own guarantee that it
 never deletes or untags capsule (item 12, theory section 8) is
 unchanged for existing callers.

 Also clarified and pinned by a new test
 (TestCapsuleRoleSlotsAreNotTaggedWithGenericAllPointers), following a
 review finding: CapsulesWithValue's c.valueSlots.IsPointer(slot) filter
 depends on exactly one tag relationship, not two. CapsuleRegistry
 constructs its three slot PointerRegistry instances with their own
 distinct role tags (AllElementCapsulePrevSlot /
 AllElementCapsuleValueSlot / AllElementCapsuleNextSlot), never with the
 separate, generic AllPointers tag -- so IsPointer here checks precisely
 the role tag itself, inherited unmodified from PointerRegistry
 (theorystate_v0.6.md section 76). Worth naming and testing directly,
 since PointerRegistry's generic naming (allPointers field, IsPointer
 method) makes it easy to assume a second, independent generic tag is
 also involved when it never is.

 Covered by TestCapsuleRegistryDeleteCapsuleDeletesCleanCapsule,
 TestCapsuleRegistryDeleteCapsuleFailsIfStillListed,
 TestCapsuleRegistryDeleteCapsuleFailsIfPrevOrNextSet,
 TestCapsuleRegistryDeleteCapsuleFailsIfSlotHasExtraParent,
 TestCapsuleRegistryDeleteCapsuleRequiresCapsuleTag,
 TestCapsuleRegistryDeleteCapsuleRequiresExistingNode,
 TestListRemoveAndDeleteDeletesUnreferencedCapsule,
 TestListRemoveAndDeleteKeepsCapsuleIfStillReferencedElsewhere,
 TestListRemoveAndDeleteRequiresCapsuleInList,
 TestListRemoveAndDeleteRequiresListTag, and
 TestCapsuleRoleSlotsAreNotTaggedWithGenericAllPointers.

14. Added CapsuleRegistry.DeleteCapsule and ListRegistry.RemoveAndDelete
 (see item 15 for the RemoveAndDelete rename), resolving item 13's "not
 yet addressed" note about deleting a fully detached capsule.

15. Corrected a wrong premise found during review: Txn was believed
 unable to undo a Graph.DeleteNode call once it succeeded, and item 14's
 DeleteCapsule was built around that belief (a nodeIsEmpty read-only
 pre-verification pass over all four nodes before deleting any of
 them). That belief was wrong, not merely cautious -- Graph.DeleteNode
 only ever succeeds when a node already has zero relationships in both
 directions, and NodeIDs are never reused once handed out (the counter
 only increases), so undoing a delete only ever needs to restore
 "exists, with empty relationship maps," which can never collide with
 an unrelated node. Added Graph.resurrectNode (internal) and
 Txn.DeleteNode, which records a resurrection undo step exactly like
 every other Txn method already records its own. txOps was extended to
 include DeleteNode accordingly.

 DeleteCapsule was simplified to match: the nodeIsEmpty helper and its
 separate pre-verification pass are gone entirely. Each of the four
 nodes is now deleted via tx.DeleteNode in sequence after the known
 relationships are cleared; if a later delete in the sequence fails
 (ErrNodeNotEmpty, mapped to ErrCapsuleNotEmpty), Transact's ordinary
 rollback undoes every earlier step in the same call, including any
 DeleteNode calls that had already succeeded earlier in that sequence.
 All existing DeleteCapsule tests continue to pass unmodified against
 this simplified implementation. See theorystate_v0.6.md section 78
 (corrected this session) for the full writeup, including the specific
 caveat that this safety is tied to the current toy allocator's
 never-reuse property and would not automatically transfer to a NodeID
 scheme that permits reuse.

 Separately, per discussion: ListRegistry.Remove is now the primary/
 default removal operation -- unlink capsule from its list and reclaim
 it via DeleteCapsule whenever nothing else still references it, since a
 capsule exists only to represent one list-element occurrence and has no
 reason to be left behind once unreferenced. The previous Remove
 (unlink only, capsule always survives) is renamed ListRegistry.
 RemoveWithoutDeletingCapsule and kept available for callers that need
 capsule to unconditionally survive removal. Tests previously named
 TestListRemove* for the unlink-only behavior are renamed
 TestListRemoveWithoutDeletingCapsule* accordingly; no test behavior
 changed, only names, to track the renamed method they exercise.

 Covered by TestListRemoveWithoutDeletingCapsuleMiddleElement,
 TestListRemoveWithoutDeletingCapsuleHeadUpdatesHead,
 TestListRemoveWithoutDeletingCapsuleTailUpdatesTail,
 TestListRemoveWithoutDeletingCapsuleSoleElementEmptiesList,
 TestListRemoveWithoutDeletingCapsuleClearsCapsuleOwnLinks,
 TestListRemoveWithoutDeletingCapsuleRequiresCapsuleInList,
 TestListRemoveWithoutDeletingCapsuleRequiresListTag,
 TestListRemoveWithoutDeletingCapsuleThenDeleteListSucceeds,
 TestListRemoveDeletesUnreferencedCapsule,
 TestListRemoveKeepsCapsuleIfStillReferencedElsewhere,
 TestListRemoveRequiresCapsuleInList, and TestListRemoveRequiresListTag.

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
- Txn does not support nesting one Graph.Transact call inside another
  (Txn.DeleteNode is supported -- see item 15). Nesting is not needed by
  any current caller; add support if and when one actually needs it.
