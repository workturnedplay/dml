<!--
    Copyright 2026 workturnedplay

    Licensed under the Apache License, Version 2.0 (the "License");
    you may not use this file except in compliance with the License.
    You may obtain a copy of the License at

        http://www.apache.org/licenses/LICENSE-2.0

    Unless required by applicable law or agreed to in writing, software
    distributed under the License is distributed on an "AS IS" BASIS,
    WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
    See the License for the specific language governing permissions and
    limitations under the License.

    SPDX-License-Identifier: Apache-2.0
-->

Project Implementation State

Current implementation:
- Go 1.27 implementation. This is the foundation the project builds on,
  not a prototype (theorystate.md section 7b).
- Implementation consolidated into main.go and main_test.go.
- Primitive Graph implemented and tested.
- Graph.Transact / Txn implemented and tested, giving failure-atomicity
  (not concurrency isolation) to multi-step primitive operations.
- RootGraph implemented and tested.
- PointerRegistry implemented and tested, enforcing the Pointer invariant
  (at most one target) for nodes tagged (AllPointers, P), per
  theorystate.md section 10 / 10b. Its multi-step operations run
  inside Graph.Transact.
- Named nodes use an external name -> NodeID registry. Its
  CreateNamedNode also runs inside Graph.Transact.
- ROOT is a real NodeID with special semantics only in RootGraph.

Completed milestones:
1. Primitive NodeID/relationship graph
2. Relationship queries
3. Node deletion rules
4. ROOT virtual overlay semantics
5. Consolidation into main.go / main_test.go

Current status and remaining tasks (historical note: everything through
the four Pointer representations immediately below is completed work,
kept here as context for the genuinely open items further down, not an
active task list):
- The Pointer processor is now implemented across all four
  representations described in theorystate.md section 10 / 10b
  (this bullet list replaces a
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
    theorystate.md section 10a -- rather than patched, since a
    stricter-than-necessary lower layer is useful for testing higher-layer
    reactions (section 73).
  - Representation D (corrected metadata structure, tag-based target
    lookup): PointerMetadataRegistryD, adding
    AllPointerMetadataTargetSlot. This is the construction
    theorystate.md section 10a should have described
    from the start; see that section for why Representation C's
    exclusion-based approach doesn't generalize safely (item 8).
- Commit-time interception (theorystate.md section 73) is implemented as
  Checkers (item 20). Registries also keep their re-derive-on-every-call
  validation deliberately, because Checkers only see mutations made
  through Transact.
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
  this. See theorystate.md section 76 for the formal writeup. This
  bullet is corrected here because it had gone stale without being
  updated when that work landed.
- Ordered Lists (theorystate.md section 11 / 11a) were implemented
  ahead of Sets (theorystate.md section 9 / section 32), which were
  at the time still fully open. The minimal Set interpretation is now
  decided and implemented as SetRegistry (item 17 below;
  theorystate.md section 79); the composite Set representations
  (theorystate.md sections 80-83) are now also implemented as
  CompositeSetRegistry and CompositeSetLogRegistry (items 18-19 below).
  Domains and Domain Pointers (theorystate.md sections 9c/10c) are now
  implemented -- see item 22 -- closing out this paragraph's own
  "remain later still and unstarted" note.

  The ElementCapsule primitive (CapsuleRegistry, item 10) is implemented:
  list -> ElementCapsule does not by itself make every list child a capsule;
  capsules are identified through AllElementCapsules, and the capsule's
  previous, value, and next intermediary slots are discovered through their
  own role tags rather than by child position.

  ListRegistry is implemented through creation, append/prepend/insert-after,
  both removal forms, head/tail/traversal, value-membership queries, and
  deletion. The current implementation also validates the ordered-list
  interpretation defensively after raw Graph mutations: traversal uses a
  visited NodeID set to detect cycles deterministically, verifies list
  membership and capsule/value validity, checks reciprocal Prev/Next links,
  checks head/tail consistency and reachability, and rejects malformed
  structure with the appropriate registry error rather than timing out or
  returning a partial sequence. Capsule role-slot discovery likewise verifies
  that the discovered slot is owned by the requesting capsule through its
  AllElementCapsules-tagged parent, while unrelated non-capsule parents remain
  permitted.

  Ordered Lists are therefore feature-complete for the primitives currently
  scoped, but their final semantic contract remains OPEN in theorystate.md.
- Do not prematurely implement Set/List semantics in the primitive layer;
  they remain higher-layer constructions, per the same discipline that kept
  Pointer semantics entirely out of Graph.

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
 Pointer processor described in theorystate.md section 10 / 10b:
 (AllPointers, P) tags P as
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
 a graph concept remain theorystate.md section 14/45, both still
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
 (theorystate.md section 19), so this was not attempted. Txn also
 does not support undoing DeleteNode (would require resurrecting a
 specific NodeID outside the normal monotonic counter -- deferred until
 an actual caller needs it) and is not designed to nest
 (theorystate.md section 45, still OPEN). Covered by
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


8. Fixed a real design bug in Representation C / theorystate.md
 section 10a, found during review: identifying "the target" as
 literally "whichever child of M isn't the tagged subject-slot"
 (exclusion) silently assumes M can never have any other child, ever --
 contradicting the construction's own stated purpose of letting M grow
 structure later without disturbing what's already there.
 theorystate.md section 10a records the correction (its original
 "M -> P, M -> I" sketch had an even sharper version of the same bug:
 those two relationships collapse into one whenever target == subject,
 since primitive relationships are unique pairs). Added PointerMetadataRegistryD
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
 Ordered Lists (theorystate.md section 11 / 11a): AllElementCapsules
 tags capsule-kind
 nodes (renamed from the theory docs' illustrative "AllCapsules" to
 avoid implying a more generic capsule concept); each capsule's prev,
 value, and next roles are represented by a dedicated PointerRegistry
 instance under its own tag (AllElementCapsulePrevSlot /
 AllElementCapsuleValueSlot / AllElementCapsuleNextSlot) -- Pointer
 Representation B applied three times, reusing PointerRegistry
 unmodified rather than reimplementing "at most one target" a third
 time (theorystate.md section 76). Roles are discovered by tag via
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
 (theorystate.md section 11 / 11a) on top of CapsuleRegistry
 (item 10). A list is an ordinary
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
 theorystate.md section 18, this is deliberately "delete only if
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
 item 12 above and theorystate.md section 11 had flagged as a
 possible later
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
 parents, theorystate.md section 2.5/3), which must not be
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
 theorystate.md section 78 for the generalized principle.

 ListRegistry.RemoveAndDelete composes Remove and DeleteCapsule as two
 separate, sequential Graph.Transact calls, not one joint transaction:
 Remove's own step always fully commits on its own terms, and
 DeleteCapsule is then attempted as a best-effort second step. If
 capsule turns out not to be safely deletable, RemoveAndDelete reports
 deleted=false with no error, rather than rolling back the removal too
 -- a capsule that legitimately cannot be deleted still ends up fully,
 successfully removed from the list. This is a new, separate method, not
 a change to Remove's existing behavior: Remove's own guarantee that it
 never deletes or untags capsule (item 12, theorystate.md section
 10c) is unchanged for existing callers.

 Also clarified and pinned by a new test
 (TestCapsuleRoleSlotsAreNotTaggedWithGenericAllPointers), following a
 review finding: CapsulesWithValue's c.valueSlots.IsPointer(slot) filter
 depends on exactly one tag relationship, not two. CapsuleRegistry
 constructs its three slot PointerRegistry instances with their own
 distinct role tags (AllElementCapsulePrevSlot /
 AllElementCapsuleValueSlot / AllElementCapsuleNextSlot), never with the
 separate, generic AllPointers tag -- so IsPointer here checks precisely
 the role tag itself, inherited unmodified from PointerRegistry
 (theorystate.md section 76). Worth naming and testing directly,
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
 this simplified implementation. See theorystate.md section 78
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

16. Added adversarial structural validation for Ordered Lists and
 ElementCapsule role discovery. This is an implementation-level hardening
 of the current ListRegistry/CapsuleRegistry interpretation, not a new
 primitive-Graph invariant and not a final semantic commitment about Lists.

 ListRegistry.Elements now detects Next-chain cycles with a visited set keyed
 by ElementCapsule NodeID rather than using a timeout. It also rejects
 malformed head/tail metadata, capsules that are not members of the list,
 missing values, broken reciprocal Prev/Next links, and list members that are
 unreachable from the head. This keeps malformed raw graph mutations from
 becoming silently truncated or cross-list traversals.

 CapsuleRegistry.slotFor now verifies that a discovered role slot has the
 requesting capsule as its unique AllElementCapsules-tagged owner. Arbitrary
 unrelated parents of the role-slot node remain allowed; only competing
 capsule ownership is ambiguous and rejected.

 The adversarial tests appended to main_test.go cover cycles, broken
 Prev/Next reciprocity, cross-list contamination, disconnected members,
 malformed head/tail metadata, missing/invalid role data, duplicate role
 metadata, and related out-of-band graph mutations. The full suite, race
 detector, and go vet were run against the resulting source with an older
 local Go toolchain, while the project's go.mod remains at Go 1.27.

17. Added SetRegistry, implementing the minimal Set interpretation of
 theorystate.md section 9 / 9a (formalized as section 79):
 (AllSets, S) tags S as Set-kind, and S's direct children are exactly its
 members -- no intermediary node is needed, unlike every other structure
 in this file, because primitive relationships are already unique pairs
 (theorystate.md section 2.6, ruling out duplicate membership by
 construction) and Sets carry no order (section 5) needing an
 occurrence-identity node (section 75) the way List elements do. IsSet /
 NewSet / TagAsSet / Add / Remove / Contains / Members / Size / DeleteSet
 are provided; DeleteSet mirrors ListRegistry.DeleteList's "remove the tag
 as part of the same transaction, then delete only if empty" shape.
 Self-membership is permitted (theorystate.md section 2.8).
 Members() deliberately does not recurse into a member that happens to
 itself be tagged Set-kind (theorystate.md section 9a) -- recursive
 expansion is reserved for the separate, still-deferred
 CompositeSetRegistry / CompositeSetLogRegistry designs (theorystate.md
 sections 80-83), which additionally required resolving a real design flaw
 found during review before being written up: an operand's intent to be
 expanded-as-a-set versus kept-as-a-scalar-member must be recorded
 explicitly per operand (a fresh, per-relationship descriptor node, the
 same theorystate.md section 75 occurrence-identity pattern used
 elsewhere), never inferred from the operand node's own tags -- inferring
 it was found to silently make "add a Set as a literal member of another
 Set" (explicitly permitted by section 9a) inexpressible. See
 theorystate.md sections 79-85 for the full write-up, including the
 two composite representations' designs, why a cached derived-membership
 view is not being built, and an explored-and-declined concurrent-lookup
 optimization.

 Because a Set imposes no cardinality or structural invariant on its
 children beyond the tag itself, SetRegistry has no adversarial
 out-of-band-mutation test suite analogous to CapsuleRegistry/
 ListRegistry's -- there is no invariant for an out-of-band mutation to
 violate. NameAllSets was added to FoundationalNames now that this
 representation is actually being implemented, per this file's own
 "add foundational names only when starting the corresponding
 representation" discipline.

 Covered by TestFoundationalNamesIncludesAllSets,
 TestNewSetRegistryRequiresExistingAllSets,
 TestNewSetTagsSetAndStartsEmpty, TestSetTagAsSetTagsFreshNode,
 TestSetTagAsSetAllowsExistingChildren, TestSetTagAsSetIsIdempotent,
 TestSetTagAsSetRequiresExistingNode, TestSetAddAddsMember,
 TestSetAddIsIdempotentForExistingMember, TestSetAddAllowsSelfMembership,
 TestSetAddRequiresExistingMember, TestSetContainsRequiresExistingMember,
 TestSetRemoveRemovesExistingMember, TestSetRemoveNoOpWhenAbsent,
 TestSetRemoveRequiresExistingMember, TestSetContainsReflectsMembership,
 TestSetMembersReturnsAllDirectChildren,
 TestSetMembersDoesNotRecurseIntoNestedSet,
 TestSetSizeMatchesMemberCount, TestSetDeleteSetSucceedsWhenEmpty,
 TestSetDeleteSetFailsIfNotEmpty, TestSetDeleteSetFailsIfReferencedElsewhere,
 TestSetOperationsRequireSetTag, and TestSetOperationsRequireExistingSetNode.

18. Added CompositeSetRegistry, implementing the unordered composite Set
 representation of theorystate.md sections 80/81: (AllCompositeSets, C)
 tags C as CompositeSet-kind, and C's direct children are freshly-minted
 operand-descriptor nodes U (theorystate.md section 75's occurrence/role-
 identity pattern) rather than members directly -- C -> U -> operand,
 with U tagged along two independent axes (operation kind: AllAdditiveOp/
 AllSubtractiveOp; operand kind: AllScalarOperand/AllSetOperand, recorded
 explicitly per theorystate.md section 80's correction rather than
 inferred from operand's own tags). NewCompositeSet, AddOperand,
 RemoveOperand, Operands, OperandTarget, OperandIsAdditive,
 OperandIsSetOperand, Evaluate, and DeleteCompositeSet are provided.
 AddOperand always mints a fresh descriptor unconditionally, per
 theorystate.md section 85 -- no existing identical descriptor is
 searched for or reused.

 Evaluate implements theorystate.md section 81's fold (union of every
 additive operand's resolved membership, minus the union of every
 subtractive operand's resolved membership) and is never cached, for the
 same reason SetRegistry.Members is never cached. Resolving a
 set-expansion operand dispatches, per theorystate.md section 83, to
 whichever currently-implemented Set representation the operand actually
 carries -- a plain Set (via the embedded SetRegistry) or another
 CompositeSet (resolved recursively); CompositeSetLogRegistry
 (theorystate.md section 82) remains unimplemented, so the dispatcher
 does not yet need a third case.

 Cycle detection (theorystate.md section 83) tracks composite-kind
 NodeIDs on the *current resolution path*, not every composite-kind node
 ever seen during one Evaluate call -- a real design point caught during
 review, not a detail: a naive "ever visited" set would incorrectly
 reject a DAG where the same composite Set is legitimately reached via
 two different, non-cyclic branches (a shared sub-expression).
 resolveSetOperand adds a composite operand to the visited set
 immediately before recursing into it and removes it again immediately
 afterward (via defer), matching a standard depth-first on-stack cycle
 check; a genuine cycle is reported via the new ErrCompositeSetCycle.
 TestCompositeSetEvaluateAllowsDiamondSharedOperand pins this down
 directly.

 A small shared helper, exactlyOneTag, checks that a node carries exactly
 one of two mutually exclusive tags (used for both of a descriptor's
 independent axes), returning the new ErrInvalidOperandDescriptor if a
 descriptor has neither or both -- reachable only through an out-of-band
 mutation, since AddOperand always wires exactly one tag per axis.
 createTaggedNodeTx was refactored to share its single-relationship-add
 step with a new tagNodeTx helper, used directly by AddOperand to apply a
 descriptor's two tags without duplicating the same tx.AddRelationship
 call.

 theorystate.md section 79's Set-representation mutual-exclusivity rule
 (a node may carry at most one of AllSets / AllCompositeSets /
 AllCompositeSetLogs) is now enforced, resolving the gap flagged below at
 the time SetRegistry was added: NewSetRegistry now accepts the tag
 NodeIDs of every other currently-implemented Set representation
 (currently just AllCompositeSets), and SetRegistry.TagAsSet refuses
 (ErrSetRepresentationConflict) to tag a node already carrying one of
 them. NewSet/NewCompositeSet do not need an equivalent check: both
 always tag a freshly created node, which cannot already carry any other
 representation's tag. CompositeSetRegistry does not yet provide a
 TagAsCompositeSet analogous to TagAsSet (retagging an arbitrary existing
 node as a composite set is not yet a need any caller has); if one is
 added later it must apply the same check.

 NameAllCompositeSets, NameAllAdditiveOp, NameAllSubtractiveOp,
 NameAllScalarOperand, and NameAllSetOperand were added to
 FoundationalNames now that this representation is actually being
 implemented, per this file's own "add foundational names only when
 starting the corresponding representation" discipline.

 Covered by TestFoundationalNamesIncludesCompositeSetNames,
 TestNewCompositeSetRegistryRequiresExistingTags,
 TestNewCompositeSetStartsEmpty, TestCompositeSetAddOperandScalarAdditive,
 TestCompositeSetEvaluateUnionThenDifference,
 TestCompositeSetAddOperandRequiresKnownSetTagWhenSetOperand,
 TestCompositeSetEvaluateResolvesNestedCompositeSetOperand,
 TestCompositeSetEvaluateDetectsCycle,
 TestCompositeSetEvaluateAllowsDiamondSharedOperand,
 TestCompositeSetRemoveOperandDeletesDescriptor,
 TestCompositeSetRemoveOperandRequiresOperandInSet,
 TestCompositeSetDeleteCompositeSetSucceedsWhenEmpty,
 TestCompositeSetDeleteCompositeSetFailsIfNotEmpty,
 TestCompositeSetOperationsRequireCompositeSetTag,
 TestCompositeSetEvaluateDetectsMalformedDescriptor, and
 TestSetRegistryTagAsSetRejectsCompositeSetConflict.

19. Added CompositeSetLogRegistry, implementing the append-only,
 order-sensitive composite Set representation of theorystate.md section
 82: the tagged node is dual-tagged (AllLists, node) and
 (AllCompositeSetLogs, node) -- an ordinary List, reused and
 reinterpreted rather than reimplemented, per theorystate.md section
 10c's precedent for one identity carrying more than one simultaneous
 interpretation. NewCompositeSetLog mints the node and applies both tags
 inside one Graph.Transact call, so there is never an observable
 intermediate state carrying only one of the two. Each logged operation
 is a list element whose value is an operand-descriptor node U, exactly
 the same shape CompositeSetRegistry already uses (theorystate.md
 section 80). AppendOperation, RemoveOperation, Operations,
 OperandTarget, OperandIsAdditive, OperandIsSetOperand, Evaluate,
 Contains, and DeleteCompositeSetLog are provided.

 Evaluate implements theorystate.md section 82's order-sensitive
 left-to-right fold (unlike CompositeSetRegistry.Evaluate's
 order-insensitive union-then-difference): a later operation mentioning
 a given element supersedes an earlier one, regardless of which axis
 either operation is tagged with. Contains implements the backward-scan
 optimization theorystate.md section 82 proves is sound: it walks the
 log tail-to-head and stops at the first (i.e. most recent) operation
 whose operand currently mentions the queried value, using the cheapest
 membership check available for that operand's own kind (direct equality
 for a scalar operand, SetRegistry.Contains for a plain Set operand, and
 full recursive resolution -- no cheaper check exists -- for a nested
 CompositeSet or CompositeSetLog operand). Neither Evaluate nor Contains
 is ever cached, for the same reason given throughout this file.

 Cross-representation dispatch and cycle detection (theorystate.md
 section 83) are now fully bidirectional across all three Set
 representations. This requires CompositeSetRegistry and
 CompositeSetLogRegistry to each be able to resolve the other's kind,
 which is a genuine mutual dependency Go cannot construct in one step;
 resolved by having CompositeSetLogRegistry take an existing
 *CompositeSetRegistry as a constructor argument (immediately gaining
 the ability to resolve CompositeSet-kind and its own kind, recursively),
 and adding a new CompositeSetRegistry.SetLogs(logs) method, called
 after both registries exist, to complete the wiring in the other
 direction. A CompositeSetRegistry that never has SetLogs called
 continues to work for every other operand kind; a CompositeSetLog-kind
 operand it encounters is simply reported via the existing
 ErrInvalidSetOperand, exactly as any other unrecognized tag would be.
 The shared visited-set cycle tracking (theorystate.md section 83) now
 spans both composite representations, so a cycle alternating between a
 CompositeSet and a CompositeSetLog is still caught by either one's
 Evaluate.

 Per this file's own "add foundational names only when starting the
 corresponding representation" discipline, NameAllCompositeSetLogs was
 added to FoundationalNames now that this representation is actually
 being implemented. theorystate.md section 79's Set-representation
 mutual-exclusivity rule now covers all three tags (AllSets,
 AllCompositeSets, AllCompositeSetLogs): NewSetRegistry's otherSetTags
 and SetRegistry.TagAsSet's check needed no code change to support this
 (they were already generic over however many tags are passed), only an
 additional tag NodeID supplied by callers wiring up a
 CompositeSetLogRegistry alongside SetRegistry.

 DRY: per theorystate.md section 80's own anticipation ("expected to
 share one internal build/validate/resolve implementation... once both
 are implemented"), the operand-descriptor build/inspect/teardown/resolve
 logic previously living only inside CompositeSetRegistry was factored
 out into shared free functions -- buildOperandDescriptorTx,
 operandDescriptorAxes, clearOperandDescriptorEdgesTx,
 deleteOperandDescriptorTx, operandTargetGeneric, resolveOperandGeneric,
 resolveSetOperandGeneric, and operandCarriesKnownSetTag -- now used by
 both CompositeSetRegistry (AddOperand, RemoveOperand, OperandTarget,
 resolveOperand) and CompositeSetLogRegistry. This is a pure refactor of
 CompositeSetRegistry's existing methods; no behavior change, and all of
 item 18's existing tests continue to pass against it unmodified.
 Separately, ListRegistry.Append was split into a new tx-composable
 appendTx (mirroring the existing newCapsuleTx/setPrevTx/setNextTx
 pattern), so CompositeSetLogRegistry.AppendOperation can mint its
 descriptor and append it to the log inside one atomic transaction
 instead of two separate ones.

 RemoveOperation's teardown order is more constrained than
 CompositeSetRegistry.RemoveOperand's: a CompositeSetLog's descriptor U
 has an incoming edge from its owning capsule's value slot (unlike a
 CompositeSetRegistry descriptor, whose only incoming edge is the direct
 containment edge from its composite set, removed in the same step as
 everything else). RemoveOperation therefore first unlinks and reclaims
 the capsule via ListRegistry.RemoveWithoutDeletingCapsule +
 CapsuleRegistry.DeleteCapsule (whose own atomic teardown clears the
 value-slot-to-U edge as part of deleting the capsule), and only once
 that succeeds -- leaving U with no remaining incoming edges -- clears
 U's own outgoing/tag edges and deletes U itself in a second transaction.
 If DeleteCapsule fails (ErrCapsuleNotEmpty), RemoveOperation stops there
 without touching U at all, leaving the capsule unlinked but otherwise
 intact, exactly mirroring ListRegistry.Remove's own best-effort second
 step.

 Covered by TestFoundationalNamesIncludesAllCompositeSetLogs,
 TestNewCompositeSetLogRegistryRequiresExistingTags,
 TestNewCompositeSetLogTagsBothAllListsAndAllCompositeSetLogs,
 TestCompositeSetLogAppendOperationScalarAdditive,
 TestCompositeSetLogEvaluateOrderSensitiveFold,
 TestCompositeSetLogAppendOperationRequiresKnownSetTagWhenExpand,
 TestCompositeSetLogEvaluateResolvesSetOperand,
 TestCompositeSetLogEvaluateResolvesCompositeSetOperand,
 TestCompositeSetLogEvaluateResolvesNestedCompositeSetLogOperand,
 TestCompositeSetRegistryResolvesCompositeSetLogOperand,
 TestCompositeSetRegistryWithoutSetLogsRejectsCompositeSetLogOperand,
 TestCompositeSetLogEvaluateDetectsCycle,
 TestCompositeSetLogEvaluateDetectsCrossRepresentationCycle,
 TestCompositeSetLogContainsMatchesEvaluate,
 TestCompositeSetLogContainsRequiresExistingValue,
 TestCompositeSetLogRemoveOperationDeletesDescriptorAndCapsule,
 TestCompositeSetLogRemoveOperationRequiresOperationInLog,
 TestCompositeSetLogDeleteCompositeSetLogSucceedsWhenEmpty,
 TestCompositeSetLogDeleteCompositeSetLogFailsIfNotEmpty,
 TestCompositeSetLogOperationsRequireCompositeSetLogTag,
 TestCompositeSetLogEvaluateDetectsMalformedDescriptor, and
 TestSetRegistryTagAsSetRejectsCompositeSetLogConflict.

20. Added the commit-time invariant checking mechanism theorystate.md
 sections 73/77 had left open, resolving the first "Currently unaddressed
 yet" bullet below (now moved out of that section). Checker is a new
 type ({Name string; Tags []NodeID; Check func(g *Graph, touched
 map[NodeID]struct{}) error}), registered via the new
 Graph.RegisterChecker and consulted by Graph.Transact via the new
 runCheckers/checkerRelevant, immediately after a transaction's fn
 succeeds and before Transact reports success to its own caller: if a
 relevant Checker declines, its error is treated exactly like an error
 fn itself returned, and the whole transaction is rolled back via the
 same existing mechanism. Txn gained a touched map[NodeID]struct{} field
 (populated by CreateNode/AddRelationship/RemoveRelationship/DeleteNode,
 mirroring undo's own "only record what actually changed" discipline),
 handed to runCheckers as the relevance-filtering input.

 Per design discussion, this deliberately does NOT give Txn a staged/
 overlay view: a Checker's Check function runs against the real,
 already-mutated Graph, exactly as every other read in this file already
 does, since nothing else can observe the intermediate state under the
 current single-threaded execution model (theorystate.md section 19) --
 an overlay would only be required once real concurrent access exists.
 This also means every Checker's own validation logic is, without
 exception, a thin adapter around a check that already existed and was
 already independently tested: singleChildTarget for PointerRegistry and
 PointerMetadataRegistryD, singleChildTarget-with-exclusion for
 PointerMetadataRegistry, validateStructure for ListRegistry, and
 exactlyOneTag/operandTargetGeneric for CompositeSetRegistry's two
 Checkers. No new invariant-checking logic was written from scratch
 except CapsuleRegistry.wellFormed (below).

 PointerRegistry, PointerMetadataRegistry, PointerMetadataRegistryD,
 CapsuleRegistry, ListRegistry, and CompositeSetRegistry's constructors
 each now register their own Checker(s) as part of construction --
 simply constructing a registry wires up its commit-time enforcement, no
 separate opt-in step needed. SetRegistry registers none (a Set has no
 invariant beyond its own tag). CompositeSetRegistry registers two: one
 keyed on AllCompositeSets that walks a touched composite set's current
 children (mirroring Evaluate()'s own defensive walk, catching a stray
 non-descriptor child added out of band), and one keyed on the four
 shared operand-descriptor axis tags themselves (theorystate.md section
 80) rather than on AllCompositeSets, which is what lets it also cover
 CompositeSetLogRegistry's own descriptors -- those are list-capsule
 values, never children of a composite-set node, so the first Checker's
 parent-based walk could never reach them, but the axis-tag-keyed one
 fires on them directly since CompositeSetLogRegistry is required to
 reuse the same tag NodeIDs. CompositeSetLogRegistry therefore registers
 no Checker of its own at all -- its List structure and its descriptor
 shape are both already covered by Checkers registered when its required
 *ListRegistry/*CompositeSetRegistry constructor arguments were
 themselves constructed. Covered by
 TestCompositeSetLogRegistrySharesListStructureChecker and
 TestCompositeSetLogRegistrySharesOperandDescriptorChecker.

 Added CapsuleRegistry.wellFormed, the one genuinely new piece of
 validation logic this feature needed: previously there was no single
 function answering "is this capsule well-formed, full stop" -- only
 three separate slotFor/Value/Prev/Next-style calls, each surfacing a
 problem with its own specific role independently (see
 TestAdversarialCapsuleMultipleRoleViolationsEachDetectedIndependently,
 added the prior session specifically to document this gap). wellFormed
 bundles checking all three role slots' presence, ownership, and
 cardinality into one call, backing CapsuleRegistry's own Checker.
 DeleteCapsule does not use it: DeleteCapsule's all-or-nothing teardown
 already gets an equivalent guarantee for free by attempting the real
 deletes and relying on Transact's existing rollback if one fails, so a
 pre-check there would be redundant, not a missed reuse opportunity.

 Covered by TestListRegistryCheckerCatchesInvalidStructureAtCommitTime,
 TestCompositeSetRegistryCheckerCatchesMalformedDescriptorAtCommitTime,
 TestCompositeSetLogRegistrySharesListStructureChecker, and
 TestCompositeSetLogRegistrySharesOperandDescriptorChecker -- each
 simulates its violation through a raw Graph.Transact call rather than a
 direct, non-transactional Graph mutation, since Checkers only ever run
 for mutations made through Transact; the many existing out-of-band
 adversarial tests elsewhere in this file, which do mutate directly, are
 unaffected by this feature and continue to be caught only lazily, on
 next read, exactly as before.

21. Corrected a real, if narrow, atomicity gap found during review in
 item 19's CompositeSetLogRegistry.RemoveOperation: after unlinking and
 reclaiming the capsule (ListRegistry.RemoveWithoutDeletingCapsule +
 CapsuleRegistry.DeleteCapsule), the final step -- clearing descriptor
 U's own remaining edges and deleting U itself -- was split across a
 Graph.Transact call (clearing U's edges only) followed by a separate,
 non-transactional Graph.DeleteNode(U) call. This meant a failure in
 that final raw DeleteNode call (e.g. some future code path having
 given U an unexpected new relationship in the meantime) could leave U
 with its edges already cleared but not yet deleted, with no rollback
 available for that half state. Fixed by combining both steps into one
 Graph.Transact call using the existing deleteOperandDescriptorTx helper
 -- the same helper CompositeSetRegistry.RemoveOperand already uses for
 its own, identically-shaped final teardown step -- rather than manually
 inlining clearOperandDescriptorEdgesTx followed by a raw delete. This
 also removes a small amount of duplicated logic: by the time this final
 step runs, DeleteCapsule has already cleared U's one previously-
 problematic incoming edge (from its owning capsule's value slot), so
 nothing about CompositeSetLogRegistry's teardown actually requires
 splitting the clear and delete steps apart anymore. No test behavior
 changed -- TestCompositeSetLogRemoveOperationDeletesDescriptorAndCapsule
 and TestCompositeSetLogRemoveOperationRequiresOperationInLog continue to
 pass unmodified against the corrected implementation.

22. Added Domain Pointers (theorystate.md sections 9c/10c, design decided
 in a prior session; this session implements it), plus a small
 independently-useful gap noticed while designing it.

 CompositeSetRegistry gained a Contains(set, value) method, symmetric
 with SetRegistry.Contains and CompositeSetLogRegistry.Contains -- a
 thin wrapper around the existing Evaluate/evaluate, with no
 backward-scan optimization (unlike CompositeSetLogRegistry.Contains),
 since CompositeSetRegistry's union-then-difference fold is not
 order-sensitive.

 A Domain (theorystate.md section 9c) is not a new tagged concept: any
 node already carrying one of the three Set-representation tags (AllSets,
 AllCompositeSets, AllCompositeSetLogs) is domain-eligible, dispatched
 generically via the new domainContainsGeneric function -- the
 Contains-flavored counterpart to the existing resolveSetOperandGeneric,
 needing no visited-set threading of its own since each representation's
 own Contains already handles its own recursion/cycle detection.

 Domain Pointers (theorystate.md section 10c) attach a domain slot,
 tagged via the new NameAllDomainSlot foundational name, to a pointer's
 anchor node -- P for Representation B, the metadata node M for
 Representation D -- reusing PointerRegistry under this new tag a fourth
 time for the slot's own "at most one domain" cardinality, exactly like
 CapsuleRegistry's three role slots. Representations A and C cannot
 safely carry a domain slot (both discover their own target via
 zero-exclusion or single-exclusion child scans that an extra tagged
 child would immediately violate); only B and D are supported, sharing a
 new domainConstraint struct for the domain-slot lookup/create/remove/
 validate logic common to both, split from each representation's own
 target-discovery exactly the way subjectMetadataBase already splits
 PointerMetadataRegistry(D)'s shared subject-side logic from their
 differing target-side logic.

 DomainPointerRegistryD wraps an existing PointerMetadataRegistryD and
 registers its own commit-time Checker: M is self-identifying via its
 own AllPointerMetadata tag, so the Checker can reverse-discover M from
 either a touched target-slot or domain-slot node (via
 findUniqueTaggedParent, the same lookup locateBySubjectSlot already uses
 one hop further out) and re-validate M's current target against M's
 current domain, catching a caller bypassing DomainPointerRegistryD and
 mutating the underlying PointerMetadataRegistryD or the shared
 domain-slot PointerRegistry directly.

 DomainPointerRegistryB wraps an existing Representation B
 PointerRegistry and deliberately registers no Checker of its own,
 discovered as a real, non-obvious gap while implementing rather than
 anticipated when this feature was designed: its anchor P carries no
 self-identifying tag in the general case (P may be any caller-managed
 node), so reverse-discovering P from a touched sub-pointer or
 domain-slot node would require either an untagged "find the one
 parent" lookup -- the exact anti-pattern item 13 already rejected for
 CapsulesWithValue -- or a new bookkeeping tag added purely to support
 this one Checker. Since no current caller needs Representation B
 domain pointers yet, this is deferred rather than built ahead of an
 actual need (theorystate.md section 7); domain enforcement for B is
 write-time only (SetTarget/SetDomain), documented as a known, narrower
 gap than D's in DomainPointerRegistryB's own doc comment and in
 theorystate.md section 10c. DomainPointerRegistryB additionally
 provides NewDomainPointer, discovering/minting its sub-pointer node U
 via the underlying PointerRegistry's own tag rather than requiring
 callers to separately track U alongside P.

 Both wrapper types validate a proposed SetDomain against the pointer's
 current target, and a proposed SetTarget against the pointer's current
 domain, symmetrically -- a domain that would immediately strand the
 existing target is rejected via the same ErrTargetOutsideDomain used
 for an out-of-domain SetTarget, and the domain slot is left unchanged.

 See theorystate.md section 86 for the one gap intentionally not closed
 by either representation's Checker: a domain node's own membership
 changing later, via a mutation that never touches the pointer or its
 domain/target slots at all, is not detected by anything in this
 session's implementation -- option 1 (accept the gap) from that
 section's three recorded options, not option 2 (a reverse index from
 domain to referencing pointers), which remains the recorded likely
 future direction.

 Covered by TestCompositeSetContainsReflectsMembership,
 TestCompositeSetContainsRequiresCompositeSetTag,
 TestCompositeSetContainsRequiresExistingValue,
 TestFoundationalNamesIncludesAllDomainSlot,
 TestDomainPointerRegistryBNewDomainPointerAndTargetWithNoDomain,
 TestDomainPointerRegistryBSetDomainEnforcesMembership,
 TestDomainPointerRegistryBSetDomainRejectsNonSetTaggedNode,
 TestDomainPointerRegistryBSetDomainRejectsWhenCurrentTargetOutsideNewDomain,
 TestDomainPointerRegistryBRemoveDomainAllowsAnyTargetAgain,
 TestDomainPointerRegistryBSetTargetRequiresExistingSubPointer,
 TestDomainPointerRegistryDTargetWithNoDomainAllowsAnyTarget,
 TestDomainPointerRegistryDSetDomainEnforcesMembership,
 TestDomainPointerRegistryDSetDomainRejectsNonSetTaggedNode,
 TestDomainPointerRegistryDSetDomainRejectsWhenCurrentTargetOutsideNewDomain,
 TestDomainPointerRegistryDRemoveDomainAllowsAnyTargetAgain,
 TestDomainPointerRegistryDDomainViaCompositeSet,
 TestDomainPointerRegistryDDomainViaCompositeSetLog,
 TestDomainPointerRegistryDCheckerCatchesOutOfBandTargetChange, and
 TestDomainPointerRegistryDCheckerCatchesOutOfBandDomainChange.

23. Fixed a real correctness bug found on review in
 DomainPointerRegistryB.NewDomainPointer: it previously minted a fresh
 sub-pointer node U and wired (anchor, U) unconditionally on every call,
 with no check for whether anchor already had one. Calling it twice for
 the same anchor therefore silently gave anchor two children both tagged
 via the underlying PointerRegistry's own tag, which made every
 subsequent subPointer-based lookup (Target/SetTarget/RemoveTarget) fail
 with ErrAmbiguousPointerMetadata from then on -- a real, reachable
 out-of-band-looking invariant violation caused entirely through this
 registry's own public API, not merely a hypothetical one. NewDomainPointer
 now checks for an existing sub-pointer first (via the existing subPointer
 lookup) and is a no-op if one is already present, matching the
 idempotency discipline already followed by PointerRegistry.TagAsPointer
 and NameRegistry.EnsureNamedNode elsewhere in this file. Covered by
 TestDomainPointerRegistryBNewDomainPointerIsIdempotent.

24. Extracted GraphReader/GraphStore/GraphAPI interfaces (theorystate.md
 section 87) from Graph's already-existing public method set, and
 changed every registry's stored graph field, and every free helper
 function that previously took graph *Graph, to take the narrowest of
 these three interfaces its own logic actually needs: GraphReader for
 read-only helpers -- singleChildTarget, findUniqueTaggedParent,
 findUniqueTaggedChild, exactlyOneTag, locateBySubjectSlot,
 operandDescriptorAxes, operandTargetGeneric, resolveOperandGeneric,
 singleChildTargetSetTx/singleChildTargetRemoveTx's own graph parameter,
 and every registered Checker's Check function -- and GraphAPI, adding
 Transact/RegisterChecker, for every registry constructor and stored
 field: NameRegistry, PointerRegistry, subjectMetadataBase (shared by
 PointerMetadataRegistry/PointerMetadataRegistryD),
 ensureMetadataWithSubjectSlot, CapsuleRegistry, ListRegistry,
 SetRegistry, CompositeSetRegistry, CompositeSetLogRegistry, and
 domainConstraint (shared by DomainPointerRegistryB/
 DomainPointerRegistryD).

 *Graph already satisfies GraphAPI without any change to Graph itself,
 so this is a pure decoupling refactor: no registry's own logic changed,
 no new foundational name was added, and every existing test continues
 to pass unmodified, since every test fixture already constructed
 registries by passing &g (a *Graph) where an interface value is now
 expected, which Go accepts automatically without any test-side change.

 RootGraph was originally a deliberate, documented exception,
 discovered while doing this refactor: its ROOT-overlay node enumeration
 needed to reach Graph's private nodes map, since no GraphAPI method
 exposed "every node that exists", so it depended on the concrete *Graph
 rather than GraphAPI. That gap is closed -- see item 27.
 Txn similarly still depends on the concrete *Graph type (via its
 unexported resurrectNode method), which is correct and deliberate, not
 an oversight -- theorystate.md section 89a records that Txn's undo-log
 rollback approach is specific to the in-memory backend's own mechanism
 for satisfying the Transact contract, not a mechanism every future
 backend is expected to reuse.

25. Added GraphActor (theorystate.md section 89c), a CSP/actor-style
 wrapper making the in-memory Graph safe to share across multiple
 goroutines for the first time: GraphActor owns a private *Graph behind
 one dedicated goroutine, and every other goroutine communicates with it
 only by submitting whole closures over a channel (the new do method),
 never by touching the wrapped *Graph directly. GraphActor implements
 GraphAPI exactly like *Graph already does (see the new
 var _ GraphAPI = (*GraphActor)(nil) assertion), so it can be substituted
 for a concrete *Graph in every existing registry constructor with zero
 change to any registry's own logic -- a direct, concrete use of the
 section 87 storage-interface extraction, which was built purely for
 testability/decoupling reasons at the time and had no concurrency
 motivation.

 Txn and Checker needed no change at all to work correctly underneath
 GraphActor: both already state, as their own load-bearing soundness
 argument, that nothing can observe an in-progress mutation because
 nothing else runs between two statements in the same synchronous call
 (theorystate.md section 19) -- GraphActor does not weaken that premise,
 it makes it literally true again by construction, merely relocated from
 "whichever goroutine happens to call Transact" to "the one goroutine
 GraphActor dedicates to the real Graph."

 Any single call routed through GraphActor -- including an entire
 Graph.Transact call, however many steps its own fn performs internally
 -- is atomic, since it runs to completion on the actor's one goroutine
 before the next queued call is even looked at. What is NOT free, and is
 documented as such on GraphActor itself rather than silently glossed
 over, is grouping more than one separately-submitted call into one
 larger atomic unit: PointerRegistry.SetTarget's own read-then-Transact
 shape (reading the current target via one round trip, committing a
 replacement via a separate, later round trip) means two goroutines'
 SetTarget calls on the same pointer can legitimately interleave between
 those two round trips. This is a real lost-update/write-skew hazard,
 not a bug in GraphActor -- and, exactly as theorystate.md section 89c
 predicted, it is already caught by machinery built for an entirely
 different reason: PointerRegistry's own commit-time Checker (registered
 once, at construction, unchanged from its existing single-threaded
 form) re-validates the "at most one target" invariant against live
 current state immediately after any relevant Transact call, so a
 losing goroutine's stale commit is rejected and rolled back with
 ErrTooManyPointerTargets rather than silently corrupting the pointer.
 TestGraphActorConcurrentSetTargetNeverProducesTooManyTargets
 demonstrates this concretely, under real concurrent goroutines racing
 SetTarget on one shared pointer via one GraphActor. Solving this in
 general -- for a plan whose steps are not fixed in Go source, e.g. one
 assembled dynamically by a future in-graph processor -- remains open,
 needing the not-yet-designed in-graph transaction-descriptor machinery
 theorystate.md section 89c names but does not build.

 Panics are handled explicitly: a panic inside any closure submitted via
 do is recovered on the actor's own goroutine and re-raised on the
 original calling goroutine once that call returns, so a panicking
 Transact closure looks, from its caller's perspective, exactly like a
 direct, non-actor Graph.Transact call already does (see Graph.Transact's
 own documented panic-then-rollback behavior), while the actor's single
 dedicated goroutine survives to keep serving every other, unrelated
 caller afterward -- left unrecovered, that panic would otherwise crash
 the entire process, not merely this one actor, since an unrecovered
 panic in any goroutine terminates the whole program.
 TestGraphActorTransactPanicPropagatesAndActorSurvives covers both
 halves of this: the panic reaching the original caller, and the actor
 remaining usable immediately afterward.

 Close stops the actor's dedicated goroutine and blocks until it has
 actually exited; it is idempotent (TestGraphActorCloseIsIdempotent) via
 sync.Once. GraphActor must not be used concurrently with, or after, a
 call to Close -- an accepted, explicitly documented caller
 responsibility, matching theorystate.md section 89b's "document the
 assumption explicitly" resolution rather than building unrequested
 synchronization no current caller needs.

 Covered by TestGraphActorBasicOperations (exercising every GraphAPI
 method at least once through the actor), TestGraphActorTransactRollsBackOnFailure,
 TestGraphActorTransactPanicPropagatesAndActorSurvives,
 TestGraphActorCloseIsIdempotent,
 TestGraphActorConcurrentCreateNodeProducesUniqueIDs (proving
 Graph.CreateNode's own non-atomic nextID counter increment is safe
 under real concurrent goroutines only because GraphActor serializes
 access to it), and
 TestGraphActorConcurrentSetTargetNeverProducesTooManyTargets.

 Not addressed by this item, and not claimed to be: MVCC/persistent-
 structure-based intra-graph parallelism (theorystate.md section 89c
 options (a)/(b), still OPEN); in-graph transaction-descriptor
 vocabulary for dynamically-assembled multi-step operations;
 goroutines-as-Part-C-participants as an alternative to one shared
 GraphActor. (RootGraph's former dependency on the concrete *Graph,
 which this item originally listed as unresolved, is closed by item 27,
 which also defines how RootGraph and GraphActor are stacked.)

26. Closed the last two exceptions to the "no stored graph reference"
 discipline (theorystate.md section 90, newly written up this session):
 subjectMetadataBase (embedded by PointerMetadataRegistry and
 PointerMetadataRegistryD) and domainConstraint (embedded by
 DomainPointerRegistryB and DomainPointerRegistryD) each used to store a
 GraphAPI field at construction and read it back via b.graph/d.graph in
 their own methods -- the exact stored-reference pattern every other
 registry in this file (PointerRegistry, CapsuleRegistry, ListRegistry,
 SetRegistry, CompositeSetRegistry, CompositeSetLogRegistry) had already
 been refactored away from. Both fields are now removed, and every
 affected method gained an explicit graph parameter instead, typed as
 narrowly as it actually needs (GraphReader for pure reads, GraphAPI for
 methods opening their own Transact): subjectMetadataBase's locate/
 ensureMetadata/EnsureMetadata/HasMetadata; PointerMetadataRegistry's
 Target/SetTarget/RemoveTarget; PointerMetadataRegistryD's targetSlot/
 Target/SetTarget/RemoveTarget; domainConstraint's domainSlotFor/Domain/
 SetDomain/RemoveDomain/validateMembership/checkAllowed;
 DomainPointerRegistryB's subPointer/NewDomainPointer/Target/SetTarget/
 RemoveTarget/SetDomain; and DomainPointerRegistryD's Target/SetTarget/
 RemoveTarget/Domain/SetDomain/RemoveDomain, including the commit-time
 Checker closure registered in NewDomainPointerRegistryD (which now
 threads its own g parameter into metadata.targetSlot and
 d.checkAllowed rather than reading a stored reference).
 NewDomainPointerRegistryB's graph parameter is now unused for storage
 and renamed to _, matching NewNameRegistry's existing convention for an
 accepted-but-unused constructor parameter.

 This is a pure mechanical signature refactor with no behavior change --
 every existing error, idempotency guarantee, and test outcome is
 unchanged, only how the graph value reaches each method. Motivation
 (theorystate.md section 90): under GraphActor, a stored reference could
 itself be the GraphActor, and any code already running on its one
 dedicated worker goroutine (a Checker, or a tx-composable helper) that
 read through the stored reference instead of the g/tx value it was
 actually given would call back into GraphActor.do from within that same
 goroutine's own execution -- a genuine reentrancy deadlock, exactly the
 class of bug GraphActor's debug-only reentrancy tripwire
 (EnableGraphActorReentrancyDetection) exists to catch. No registry in
 this file now stores a graph reference at all, closing this hazard by
 construction rather than only detecting it after the fact.

27. Closed the GraphAPI node-enumeration gap and redesigned RootGraph as
 a decorator layer of the graph stack (theorystate.md section 87b).

 GraphReader gained FindNodes() []NodeID (every existing NodeID, sorted
 ascending), implemented on Graph (guarded public method plus unguarded
 findNodesCore, following the existing split), Txn, graphCoreReader,
 GraphActor, and the ROOT overlay.

 RootGraph is no longer a parallel object holding a *Graph. It now
 implements GraphAPI over any GraphAPI, applying the overlay at every
 seam: direct calls (embedded rootStore over rootReader), Transact (the
 closure receives a rootStore wrapped around the underlying Tx), and
 Checkers (RegisterChecker wraps Check so it receives a rootReader). To
 make Transact's handle decoratable, Transact now takes the new Tx
 interface (a GraphStore scoped to one transaction; *Txn implements it)
 instead of the concrete *Txn; this is a mechanical
 func(tx *Txn) error -> func(tx Tx) error change, since every helper
 already took txOps/txReader.

 GraphActor now owns a GraphAPI backend rather than a *Graph, and is
 always the OUTERMOST layer: NewGraphActor(NewRootGraph(&g, root)). This
 makes multi-step overlay methods atomic (one job on the actor
 goroutine) and guarantees the overlay's stored reference is the raw
 backend, never the actor (section 90's hazard). NewRootGraph returns
 ErrRootGraphOverActor for a *GraphActor. GraphActor's delegating methods
 now wrap errors from the interface-typed backend (wrapInterfaceErr).

 Bugs fixed along the way: RootGraph.FindIncoming did not report the
 virtual (ROOT, X) parent for X != ROOT, contradicting HasRelationship
 and FindOutgoing(ROOT); it now does, without duplicating a physically
 stored (ROOT, X), and still hides (ROOT, ROOT). RootGraph.
 FindRelationships sized a slice with len(nodes)-1, which panics on an
 empty graph; the overlay now reports nothing when ROOT does not exist.
 DRY: the repeated existence preamble is rootReader.requireExist and the
 duplicated ROOT-children enumeration is rootReader.
 virtualRootRelationships.

 RootGraph is now covered by concurrentAccessGuard like any other
 caller. Known limit: Checker.Tags relevance filtering still consults
 stored facts only (theorystate.md section 87b).

 Covered by TestFindNodesReturnsSortedExistingNodes,
 TestTxnFindNodesReflectsUncommittedCreatesAndRollback,
 TestGraphActorFindNodes, TestRootGraphInsideGraphActor,
 TestGraphActorRootGraphFindRelationshipsIsAtomic,
 TestRootFindIncomingIncludesVirtualRootParent,
 TestRootGraphTransactAndCheckerSeeOverlay,
 TestNewRootGraphRejectsGraphActor, and
 TestRootFindRelationshipsWithoutRootNodeEmitsNoVirtualRelationships.

28. Fixed a lint regression from item 27: once Transact's closure took
 the Tx interface instead of the concrete *Txn, wrapcheck began flagging
 every error returned unwrapped from a tx method (in main.go and in
 tests). Added wrap helpers createNodeTx / addRelationshipTx /
 removeRelationshipTx / deleteNodeTx (all built on wrapInterfaceErr,
 which preserves errors.Is), and untagAndDeleteNodeTx, which DRYs the
 identical "remove tag(s), then delete" tail of DeleteList / DeleteSet /
 DeleteCompositeSet / DeleteCompositeSetLog. tagNodeTx,
 createTaggedNodeTx and setPointerTargetTx now delegate to them.
 DeleteCapsule's eight repeated RemoveRelationship blocks became one
 table-driven loop. No behavior change.

29. Closed theorystate.md section 86 (Domain Pointer staleness) and
 DomainPointerRegistryB's missing commit-time Checker with one shared
 mechanism. domainConstraint.registerChecker builds the Checker for both
 representations, parameterized by the tag of the target holder
 (AllSubPointers for B, AllPointerMetadataTargetSlot for D) and an
 anchorTargetFunc (DomainPointerRegistryB.Target,
 PointerMetadataRegistryD.targetOfMetadata). It fires when a transaction
 touches a domain slot, a target holder, or a node carrying any
 Set-representation tag, finds affected anchors by reverse lookups
 (affectedAnchors, addSlotOwners, domainSlotsOf,
 transitiveSetContainers, setOperandContainers, descriptorOwners --
 FindIncoming plus CapsulesWithValue, no stored index), and re-runs
 checkAllowed for each. B needs no new tag: parents of a touched node are
 candidates, and forward tagged lookups reject non-anchors. Candidates
 that have the AllDomainSlot hub as a child are skipped (ROOT under a
 RootGraph is a virtual universal parent).

 Bug found and fixed on the way: SetRegistry.Add/Remove called the raw
 graph, so no Checker ever saw plain-Set membership changes; they now
 run as one-edge Transact calls (and report false when a Checker declines
 the commit). DRY: sortedNodeSet replaces two identical copies in the
 composite/log evaluate methods; PointerMetadataRegistryD.targetOfMetadata
 is now shared by Target and the Checker. NewDomainPointerRegistryB's
 graph parameter is now used (was `_`).

 TestCrossRoleDomainPointerDetectsCycleIntroducedThroughDomainItself
 previously pinned the section 86 gap (the cycle-introducing append
 succeeded); it now asserts the append is declined with
 ErrCompositeSetCycle and rolled back.

 Covered by TestDomainStalenessPlainSetRemoveOfCurrentTargetIsRejected,
 TestDomainStalenessCompositeDomainMutationsThatStrandTargetAreRejected,
 TestDomainStalenessLogDomainMutationsThatStrandTargetAreRejected,
 TestDomainStalenessNestedSetShrinkIsRejectedThroughDiamond,
 TestDomainStalenessOneStrandedPointerRejectsWholeTransactionAcrossRepresentations,
 TestDomainStalenessShrinkThenRetargetInOneTransactionIsAccepted,
 TestDomainPointerRegistryBCheckerCatchesOutOfBandTargetChange,
 TestDomainPointerRegistryBCheckerCatchesOutOfBandDomainChange,
 TestDomainPointerRegistryBCheckerToleratesUniversalRootParent,
 TestDomainPointerRegistryBSetTargetRacingDomainShrinkNeverStrandsPointerUnderGraphActor,
 and TestSetAddAndRemoveAreVisibleToCommitTimeCheckers.

30. Made registry operations atomic under Transact and added Tx.OnCommit
 (theorystate.md section 91). Four parts.

 (a) The Transact contract is now written down on GraphAPI.Transact: fn
 may be re-run against a different state (a future retrying backend
 will), so it must read everything its decisions depend on through tx,
 must have no side effects outside tx, and must not call Transact.

 (b) Read-decide-write sequences moved inside their Transact. Two were
 real bugs under GraphActor, both check-then-create with no Checker
 protection: ensureMetadataWithSubjectSlot (two goroutines could each
 create a metadata/subject-slot pair for one subject, after which every
 lookup failed with ErrAmbiguousPointerMetadata) and
 DomainPointerRegistryB.NewDomainPointer (two sub-pointers for one
 anchor). The rest were protected only by Checkers, which turned a lost
 update into a spurious ErrTooManyPointerTargets or
 ErrTargetOutsideDomain. Converted: PointerRegistry.SetTarget/
 RemoveTarget/TagAsPointer, PointerMetadataRegistry(D).SetTarget/
 RemoveTarget/EnsureMetadata, SetRegistry.TagAsSet, and the
 DomainPointerRegistryB/D SetTarget/SetDomain/RemoveTarget/RemoveDomain
 (with domainConstraint.setDomainTx/removeDomainTx). Each now has an
 unexported *Tx core taking a txReader and the exported method is a thin
 Transact wrapper. RemoveTarget was previously a raw graph call that no
 Checker saw; it now runs through Transact. A failed SetTarget/
 SetDomain on a subject with no metadata now rolls the metadata creation
 back too. DRY: transactBool, requirePointer, and an exclude variadic on
 singleChildTargetSetTx/RemoveTx (so Representation C reuses them); the
 free function ensureMetadataWithSubjectSlot became
 ensureMetadataWithSubjectSlotTx and subjectMetadataBase.ensureMetadata
 became ensureMetadataTx.

 (c) Tx gained OnCommit(fn): hooks run once, in order, after every
 Checker approves and before Transact returns, and are discarded on
 rollback. Txn implements it; RootGraph.Transact hands its closure a new
 rootTx (rootStore plus OnCommit forwarding), and rootStore is no longer
 a Tx itself. This exists because "mutate outside state only after
 commit" cannot be done by the caller after Transact returns: under
 GraphActor two goroutines could both pass the unbound check before
 either recorded its binding.

 (d) NameRegistry now updates byName/byID only from commit hooks
 (bindTx/recordBinding/forgetNode/dropBinding). CreateNamedNode and
 EnsureNamedNode share namedNodeTx/transactNamedNode and are one atomic
 step; Bind and DeleteNode became Transact calls; bindCore is gone.
 (The maps' own synchronization for readers on other goroutines is
 item 31(c).)

 The existence/tag pre-checks in ListRegistry, CapsuleRegistry,
 SetRegistry, CompositeSetRegistry and CompositeSetLogRegistry were
 initially left outside their Transact on the grounds that a stale
 pre-check only fails or is caught by a Checker. That was the wrong
 call: a retrying backend would not re-run them. Item 31 moved them.

 The GraphActor race test now uses staleReadThenSetTarget (the old
 SetTarget shape) to keep exercising the caller-composed hazard and the
 Checker that catches it; the new TestGraphActorConcurrentSetTargetIsAtomic
 pins the fixed behaviour.

 Covered by TestTxOnCommitRunsOnlyAfterSuccessfulCommit,
 TestRootGraphTransactForwardsOnCommit,
 TestNameRegistryBindTxIsDiscardedOnRollbackAndAppliedOnCommit,
 TestGraphActorConcurrentSetTargetIsAtomic,
 TestGraphActorConcurrentEnsureMetadataCreatesExactlyOneMetadataNode,
 TestGraphActorConcurrentNewDomainPointerCreatesExactlyOneSubPointer,
 TestGraphActorConcurrentCreateNamedNodeSameNameBindsExactlyOnce, and
 TestDomainPointerRegistryDSetDomainFailureRollsBackMetadataCreation.

31. Finished item 30 and withdrew the "no current caller" deferrals
 behind its leftovers (theorystate.md section 7b).

 (a) Every remaining check-then-write in ListRegistry, CapsuleRegistry,
 SetRegistry, CompositeSetRegistry and CompositeSetLogRegistry now runs
 inside its Transact against tx: Append/Prepend/InsertAfter (via
 requireList/requireListValue), RemoveWithoutDeletingCapsule
 (removeWithoutDeletingCapsuleTx), DeleteList, NewCapsule, DeleteCapsule
 (deleteCapsuleTx), SetValue/SetPrev/SetNext/RemovePrev/RemoveNext,
 SetRegistry.Add/Remove/DeleteSet, CompositeSetRegistry.AddOperand/
 RemoveOperand/DeleteCompositeSet (addOperandTx/removeOperandTx) and
 CompositeSetLogRegistry.AppendOperation/DeleteCompositeSetLog.
 CapsuleRegistry's slot rewiring uses slotFor (ownership-checked) for
 writes as well as reads; previously the tx helpers used an unchecked
 findUniqueTaggedChild, so ListRegistry's own writes skipped the check
 the reads applied. DRY: transactValue (generic; transactBool wraps it),
 requireList/requireSet/requireCompositeSet/requireLog/requireCapsule/
 requireOperand.

 (b) CompositeSetLogRegistry.RemoveOperation is one transaction
 (removeOperationTx). It used three, and a failed DeleteCapsule left the
 capsule unlinked from the log but still holding its descriptor; it is
 now all-or-nothing. ListRegistry.Remove deliberately remains two
 transactions (removal always commits, deletion is best-effort; Transact
 has no savepoints) and its comment now says why the intermediate state
 is safe under GraphActor -- the old "nothing can run between them"
 claim was false.

 (c) NameRegistry guards byName/byID with a sync.RWMutex, so Lookup and
 NameForNode are safe from any goroutine while a GraphActor binds and
 deletes names. Unbind takes the write lock. Readers see only committed
 bindings.

 (d) Comments and docs that justified gaps with "no current caller" or
 called the code a toy were corrected, along with stale statements
 (Txn "read operations are not wrapped", Txn isolation, the open
 "commit-time interception" bullet, the "no protection against
 concurrent goroutine access" bullet).

 Covered by TestNameRegistryLookupIsSafeWhileGraphActorBindsAndUnbinds,
 TestGraphActorConcurrentListAppendKeepsListValid and
 TestCompositeSetLogRemoveOperationIsAtomicWhenCapsuleCannotBeDeleted.

32. Added nested transactions (theorystate.md section 45), step 1 of 4
 (the mechanism; registries adopt it in the following steps).
 Tx gained Transact through a new Transactor interface (also embedded by
 GraphAPI): on a GraphAPI it opens an outermost transaction, on a Tx a
 nested one. A nested transaction is a savepoint (Txn.mark/rollbackTo over
 the shared undo log and commit-hook list), not a transaction of its own:
 if it fails or panics, only what it did is undone and its OnCommit hooks
 are discarded, and the enclosing closure may handle the error and
 continue. Checkers still run only at the outermost commit, since they
 judge the final state and a step may legitimately be invalid until a
 later one repairs it; an inner success is provisional until then.
 Txn.touched is not rewound by a nested rollback (conservative superset).
 rootTx forwards Transact and re-applies the ROOT overlay to the nested
 handle. transactValue/transactBool take a Transactor.
 Still to do: registry mutators take Transactor and the *Tx cores collapse
 into the exported methods; raw writes leave GraphAPI.

 Covered by TestNestedTransactCommitsWithOuter,
 TestNestedTransactFailureRollsBackOnlyTheInnerSteps,
 TestNestedTransactSuccessIsRolledBackWithOuterFailure,
 TestNestedTransactRunsCheckersOnlyAtOutermostCommit,
 TestNestedTransactPanicRollsBackToSavepointAndPropagates,
 TestRootGraphNestedTransactPresentsOverlayAndForwardsOnCommit and
 TestGraphActorNestedTransactRollsBackOnlyInnerSteps.

33. Nested transactions, step 2 of 4: NameRegistry and the Pointer family
 (theorystate.md section 45). Exported mutators now take a Transactor
 (GraphAPI or Tx) and the tx-composable cores that were only the bodies of
 thin wrappers are inlined into them: NameRegistry.Bind (bindTx),
 CreateNamedNode/EnsureNamedNode (namedNode replaces namedNodeTx and
 transactNamedNode), DeleteNode, BootstrapNames;
 PointerRegistry.SetTarget/RemoveTarget/NewPointer/TagAsPointer;
 subjectMetadataBase.EnsureMetadata; PointerMetadataRegistry(D)
 .SetTarget/RemoveTarget. ensureMetadataTx stays: it returns both the
 metadata and subject-slot and is a shared helper, not a wrapper's core.
 Call sites in CapsuleRegistry, ListRegistry and the domain-pointer
 registries that used the removed cores now call the exported methods, so
 their helpers' tx parameter widened from txReader to Tx (a Transactor);
 batches 3 and 4 collapse those helpers themselves. Fixed a stale
 singleChildTargetRemoveTx doc.
 Known limitation, documented on NameRegistry.Bind: name bindings are
 applied on outermost commit, so inside one enclosing transaction a
 binding is not yet visible to later checks in that transaction; binding
 the same name or node twice in one transaction is not detected.

 Covered by TestPointerRegistrySetTargetComposesInsideOneTransaction,
 TestPointerRegistryComposedSetTargetIsUndoneByFailedNestedTransaction,
 TestNameRegistryCreateNamedNodeComposesAndRollsBackWithEnclosingTransaction
 and TestPointerMetadataRegistryDComposedSetTargetRollsBackMetadataCreation.

34. Nested transactions, step 3 of 4: CapsuleRegistry and ListRegistry
 (theorystate.md section 45). NewCapsule/SetValue/SetPrev/SetNext/
 RemovePrev/RemoveNext/DeleteCapsule and NewList/Append/Prepend/
 InsertAfter/RemoveWithoutDeletingCapsule/Remove/DeleteList now take a
 Transactor. Removed: newCapsuleTx, setPrevTx, setNextTx, removePrevTx,
 removeNextTx, deleteCapsuleTx, appendTx, removeWithoutDeletingCapsuleTx
 (their bodies live in the exported methods; setSlotTarget and
 removeSlotTarget are the shared bodies behind the role-specific
 setters/removers). ListRegistry now mints capsules with NewCapsule and
 rewires with SetPrev/SetNext, which nest.
 ListRegistry.Remove is now ONE transaction: the removal is its own work
 and DeleteCapsule runs as a nested savepoint, so a refused deletion
 undoes only itself and the removal still commits (this supersedes the
 "two transactions on purpose" caveat of item 31(b)); no other goroutine
 can observe the intermediate unlinked-but-not-deleted state.
 CompositeSetLogRegistry's AppendOperation/removeOperationTx call the
 exported methods; batch 4 collapses those and the rest.

 Covered by TestListMutatorsComposeInsideOneTransactionAndRollBackTogether,
 TestListAppendInsideFailedNestedTransactionLeavesListValid and
 TestCapsuleLinkAndDeleteComposeAndRollBackWithEnclosingTransaction.

35. Nested transactions, step 4 of 4: sets, composites, logs, domain
 pointers, and the NameRegistry staging fix (theorystate.md section 45).
 SetRegistry (NewSet/TagAsSet/Add/Remove/DeleteSet),
 CompositeSetRegistry (NewCompositeSet/AddOperand/RemoveOperand/
 DeleteCompositeSet), CompositeSetLogRegistry (NewCompositeSetLog/
 AppendOperation/RemoveOperation/DeleteCompositeSetLog) and the domain
 pointers (DomainPointerRegistryB/D NewDomainPointer/SetTarget/
 RemoveTarget/SetDomain/RemoveDomain, domainConstraint.RemoveDomain) take
 a Transactor. Removed: addOperandTx, removeOperandTx, removeOperationTx,
 newDomainPointerTx, DomainPointerRegistryB/D setTargetTx, removeTargetTx,
 setDomainTx, removeDomainTx, domainConstraint.setDomainTx/removeDomainTx.
 domainConstraint.setDomainTx became attachDomain (a shared body, opening
 its own nested transaction). NewCompositeSetLog now nests
 ListRegistry.NewList instead of duplicating its tagging. The last
 exceptions to "exported methods compose" are the constructors, which call
 RegisterChecker and so keep taking a GraphAPI.

 Tx gained OnRollback(fn): hooks are appended to the undo log, so nested
 rollbacks run them LIFO with the graph undo steps, and commit drops them.
 NameRegistry uses it to close the limitation recorded in item 33: a
 binding is now STAGED in a pending overlay (pendingByName/pendingByID,
 plus pendingGone for committed bindings whose node the transaction
 deleted). Only the transaction's own checks (checkBind, lookupLive)
 consult the overlay, so a second bind of the same name or node inside one
 transaction is rejected and a deleted node's name can be rebound, while
 Lookup and NameForNode still report committed state only. Each staged
 change registers an OnCommit hook that publishes it and an OnRollback hook
 that reverses it (recordBinding was replaced by stageBinding/
 commitBinding/stageForget).

 Covered by TestTxOnRollbackRunsInReverseOrderAndOnlyOnRollback,
 TestTxOnRollbackRunsWhenCheckerDeclinesCommit,
 TestRootGraphTransactForwardsOnRollback,
 TestNameRegistryStagedBindingsAreVisibleInsideTheTransactionOnly,
 TestNameRegistryNestedFailureUnstagesItsBinding,
 TestNameRegistryDeleteThenRebindSameNameInOneTransaction,
 TestNameRegistryCreateThenDeleteInOneTransactionLeavesNoBinding,
 TestSetMutatorsComposeInsideOneTransactionAndRollBackTogether,
 TestCompositeSetLogNestedRemoveOperationFailureLeavesLogIntactAndOuterContinues
 and TestDomainStalenessComposedExportedCallsAreJudgedAtOutermostCommit.

36. Mutation only through Transact (theorystate.md section 92). GraphAPI
 is now GraphReader + Transactor + RegisterChecker; GraphStore (the
 writes) is reachable only through Tx. *Graph no longer exports
 CreateNode/AddRelationship/RemoveRelationship/DeleteNode (only the
 unexported *Core methods Txn calls remain; the concurrentAccessGuard now
 wraps queries, RegisterChecker and Transact). RootGraph embeds
 rootReader instead of rootStore (rootStore remains the write half of
 rootTx). GraphActor's four delegating write methods are gone. The
 Checker doc no longer claims a raw write can bypass Checkers; what still
 bypasses them is data that did not come through this process's Checkers
 (loaded, foreign, older build, pre-registration).
 main_test.go defines the four write methods on *Graph, *GraphActor and
*RootGraph in test builds only: on *Graph they call the cores directly
(raw, bypassing Checkers, as the out-of-band adversarial tests need); on
*GraphActor and *RootGraph they run as one-operation transactions, so
Checkers now run for those tests' setup writes. No test call site
changed. Fixed a stale PointerRegistry doc claiming Graph.AddRelationship
could be called directly.

37. Two small, purely mechanical follow-ups found while checking the
 codebase against an external review's suggested checklist (GPT-5.6
 Luna: audit graph-passing/ownership, re-examine RootGraph/GraphActor
 stacking, close DomainPointerRegistryD's cascade into stored-graph
 fields). Nothing on that checklist needed fixing: items 24/26/87/90
 already cover the graph-passing audit, items 27/87b already cover the
 RootGraph/GraphActor stacking, and the DomainPointerRegistryD-cascade
 concern no longer applies -- item 26 already closed subjectMetadataBase's
 and domainConstraint's stored-graph-reference fields, and SetRegistry/
 CompositeSetRegistry/CompositeSetLogRegistry never had one to begin
 with. The same pass did turn up the two unrelated, purely mechanical
 items below; neither closes a correctness gap, since everything they
 touch was already correct.

 (a) Factored out requireTagged(graph, id, tag, notTaggedErr), the
 shared "exists and carries this registry's own tag" precondition check
 that PointerRegistry.requirePointer, CapsuleRegistry.requireCapsule,
 ListRegistry.requireList, SetRegistry.requireSet, CompositeSetRegistry.
 requireCompositeSet, and CompositeSetLogRegistry.requireLog each
 implemented as an identical three-line existence-then-tag check,
 differing only in which tag NodeID and which dedicated ErrNotX
 sentinel to return -- the same class of duplication subjectMetadataBase
 (item 9) and singleChildTarget were factored out to close previously.
 Each requireX is now a one-line delegation to requireTagged; no
 behavior changed, since every IsX method these previously called
 (IsPointer, IsCapsule, IsList, IsSet, IsCompositeSet, IsCompositeSetLog)
 is itself already nothing but a single graph.HasRelationship(tag, id)
 call, confirmed identical to requireTagged's own check before
 extracting it. No test changed: every existing ErrNotX/ErrNodeNotFound
 assertion continues to observe the same outcome. Along the way, fixed a
 second stale doc comment of the same shape as item 28's: requirePointer
 claimed to be "Shared by currentTarget, setTargetTx and removeTargetTx"
 -- setTargetTx and removeTargetTx were inlined into SetTarget and
 RemoveTarget themselves back in item 33 and no longer exist under those
 names; the comment was never updated to say so.

 (b) Added TestCheckerPanicRollsBackAndPropagates, closing a real gap in
 test coverage (not a bug -- the behavior it covers was already
 correct). Graph.Transact's defer/recover is registered once, before fn
 runs, and therefore already covers a panic from inside the commit-time
 Checker pass (runCheckers) exactly as it covers one from fn itself
 (TestTransactRollsBackOnPanic) -- but nothing previously exercised that
 second path. The new test registers a Checker whose Check function
 panics, confirms the panic still propagates out of Transact(), and
 confirms the node created and the relationship added by fn were both
 rolled back first, exactly like a panic originating in fn would be.
 GraphActor and RootGraph need no analogous test of their own: both
 funnel every panic through this same Graph.Transact mechanism (see
 GraphActor.do's existing recover-and-re-panic, which wraps the entire
 g.Transact(fn) call regardless of where inside it a panic originates,
 already proven by TestGraphActorTransactPanicPropagatesAndActorSurvives),
 so one test at the source is sufficient.

38. Added stagedGraph/stagedOverlay (theorystate.md sections 93-97),
 a second, test-only GraphAPI implementation deliberately built around
 the opposite mechanism from *Graph's mutate-then-check, undo-log
 approach: every write made during one Transact attempt is buffered in a
 local stagedOverlay and only applied to stagedGraph's own backing maps
 once fn succeeds and every relevant Checker approves, simulating the
 buffer-then-CAS-commit shape an eventual networked backend (etcd) would
 use. A forceConflict hook, consulted once per attempt right before
 publish, can discard the attempt's entire overlay and force Transact to
 rerun fn from scratch -- exercising the "fn may run more than once"
 clause of the Transact contract (item 30/theorystate.md section 91)
 that nothing previously exercised even once. Nested transactions
 (stagedOverlay.Transact) are savepoints over the same overlay, mirroring
 Txn's own mark/rollbackTo shape (item 32/theorystate.md section 45).

 checkerRelevant was extracted from a *Graph method into a free function
 taking a GraphReader, so stagedGraph's own runCheckers shares the exact
 same relevance-filtering logic as Graph.runCheckers instead of
 duplicating it; Checker's doc comment was reworded to state its
 contract backend-neutrally (Check observes the state fn's mutations
 would produce, as of the moment fn succeeds), with the stronger
 single-threaded "this is the real, already-mutated Graph" claim moved
 to live on Graph.Transact's own doc comment as that backend's specific
 soundness argument rather than restated as if universal. No behavior
 change to *Graph itself.

 The payoff this exists for: PointerRegistry, NameRegistry, GraphActor,
 and RootGraph are each exercised against stagedGraph with zero change
 to their own code, confirming that depending only on the Tx/GraphAPI
 interface -- already true of every registry in this file -- is
 sufficient for portability across a structurally different backend
 mechanism, rather than an untested claim. Covered by
 TestStagedGraphBasicOperations,
 TestStagedGraphFailedTransactLeavesNoTrace,
 TestStagedGraphForceConflictRerunsFn,
 TestStagedGraphNestedTransactRollsBackOnlyInnerSteps,
 TestStagedGraphCheckerDeclineLeavesNoTrace,
 TestStagedGraphOnCommitRunsExactlyOnceDespiteForceConflict,
 TestStagedGraphPointerRegistryPortability,
 TestGraphActorOverStagedGraphBasicOperations, and
 TestRootGraphOverStagedGraphBasicOperations.

 Not addressed by this item: stagedGraph tracks no real per-attempt
 read-set and detects no genuine conflict of its own (forceConflict is
 an externally-driven stand-in, not a conflict-detection mechanism); and
 there is no harness re-running the full existing registry test suite
 against both backends automatically, only hand-written portability
 tests against stagedGraph specifically (theorystate.md section 97).

Currently unaddressed yet:
- A "run every Checker over everything at load" pass belongs with any
  persistence work (item 36).
- Nested transactions as a production-backend feature are realized for
  the in-memory backend only (savepoints over the undo log); a
  structurally different, test-only realization also exists for
  stagedGraph (item 38). A real non-memory production backend's own
  savepoint mechanism remains theorystate.md section 45 / 89a. Txn.
  DeleteNode is supported -- see item 15.
- Domain-pointer staleness residuals (theorystate.md section 86): raw
  non-Transact mutations, out-of-band tag removal or descriptor
  re-pointing inside a Transact, and O(pointers-per-domain) validation
  cost per commit (unmemoized) remain accepted.
- A bare *Graph is not safe for concurrent use. concurrentAccessGuard
  panics on detected overlap instead of corrupting state (theorystate.md
  section 89b); it cannot catch a non-overlapping handoff with no
  happens-before edge, which `go test -race` covers. GraphActor (item 25,
  section 89c) is the supported way to share a graph between goroutines.

Explored and declined (implementation-level; the theory-level
counterpart of this list is theorystate.md's own DECIDED/TENTATIVE/OPEN/
REJECTED discipline -- this section exists so an approach that was tried
and abandoned during implementation has a home distinct from both the
numbered changelog above and the "currently unaddressed" list, rather
than being buried inside whichever changelog entry happened to also fix
it):
- Inferring a composite-Set operand's expand-vs-scalar intent from the
  operand node's own tags, instead of recording it explicitly per
  descriptor -- rejected before any code was written, since it makes
  "add a Set as a literal, unexpanded member of another Set"
  (theorystate.md section 9a) inexpressible. See theorystate.md section
  80's "Rejected first attempt" and items 17/18 above.
- An untagged, "the slot has exactly one parent, full stop" helper for
  CapsulesWithValue's reverse ownership lookup -- caught in review as
  unsafe, not merely stricter than necessary, since a role-slot node may
  legitimately acquire unrelated parents later (theorystate.md section
  2.8). findUniqueTaggedParent's existing tag-filtered semantics were
  reused instead of adding a new helper or error type. See item 13.
- A read-only nodeIsEmpty pre-verification pass ahead of
  CapsuleRegistry.DeleteCapsule's multi-node teardown, built on the
  since-corrected belief that Txn could not undo a successful
  Graph.DeleteNode call. Removed once Txn.DeleteNode was added; see item
  15 and theorystate.md section 78 for the corrected reasoning.
- Concurrent/bidirectional forward+backward search for composite-Set
  Contains queries, considered as an analogue of
  CapsuleRegistry.CapsulesWithValue's reverse lookup -- found not to
  structurally transfer (the reverse edge set is not narrowly scoped the
  way a value-slot's incoming edges are) and would reintroduce
  concurrency-control machinery this project has otherwise deferred. See
  theorystate.md section 84.
- Naming this project's log-based composite Set representation
  "OrderedCompositeSet" -- renamed to CompositeSetLogRegistry once it was
  recognized that the original name wrongly implied first-class
  insertion-order preservation rather than a replayable operation history
  whose *fold* determines membership. See theorystate.md section 82.
- Splitting CompositeSetLogRegistry.RemoveOperation's final descriptor
  teardown across two separate Graph.Transact calls (clear edges, then a
  raw, non-transactional Graph.DeleteNode) -- corrected to one Transact
  call via the shared deleteOperandDescriptorTx helper once the
  atomicity gap this left was found in review. See item 21.
- An "Immediate" Checker tier (Checkers run at the end of every nested
  transaction, over the nodes it touched) and a Tx.Verify() to run
  Checkers early. Not built: inner boundaries are registry-method
  boundaries and a composed operation may be legitimately invalid halfway
  through (the domain Checker is the example, see
  TestDomainStalenessComposedExportedCallsAreJudgedAtOutermostCommit), so
  every Checker runs once, at the outermost commit. If wanted later, a
  Checker.Immediate field whose zero value means "deferred" adds the tier
  without changing any existing Checker. See item 32 and theorystate.md
  section 45.
