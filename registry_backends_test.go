// Copyright 2026 workturnedplay
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package dml

import (
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// The registry test suites are written once and run against every GraphAPI
// backend (theorystate.md section 97): the fixtures build their graph with
// newTestGraph, which builds activeTestBackend, and
// TestRegistriesOnEveryBackend re-runs the tests listed in
// registryTestsOnEveryBackend with each backend active. Run directly (outside
// that driver) every test uses a bare *Graph, as it always did.

// testGraph is what a registry test needs from a graph: the whole GraphAPI
// plus the four test-only write methods (defined near the top of
// main_test.go, and for the other backends next to their types). On *Graph the
// writes are raw and bypass Checkers; on every other backend each is a
// one-operation transaction, so Checkers run.
type testGraph interface {
	GraphAPI
	GraphStore
}

// Compile-time assertions that every backend the tests use is a testGraph.
var (
	_ testGraph = (*Graph)(nil)
	_ testGraph = (*stagedGraph)(nil)
	_ testGraph = (*BoltGraph)(nil)
	_ testGraph = (*GraphActor)(nil)
)

// activeTestBackend is the backend newTestGraph builds. It is the bare *Graph
// except while TestRegistriesOnEveryBackend runs a backend's subtests. Tests
// are not parallel, so a package variable is enough.
var activeTestBackend = testBackends()[0]

// newTestGraph returns a fresh, empty graph of the active backend. Every
// registry fixture builds its graph through it.
func newTestGraph(t *testing.T) testGraph {
	t.Helper()

	return activeTestBackend.open(t)
}

// testFuncName returns the name of the top-level function test, which is the
// name its subtest is run under.
func testFuncName(test func(*testing.T)) string {
	fn := runtime.FuncForPC(reflect.ValueOf(test).Pointer())
	if fn == nil {
		return "unnamed"
	}

	name := fn.Name()

	return name[strings.LastIndex(name, ".")+1:]
}

// TestRegistriesOnEveryBackend runs registryTestsOnEveryBackend once per
// backend. A failure under one backend only is a backend bug (or a test that
// relied on a raw write: see the list's comment).
func TestRegistriesOnEveryBackend(t *testing.T) {
	wantTypes := map[string]string{
		"Graph":       "*dml.Graph",
		"stagedGraph": "*dml.stagedGraph",
		"BoltGraph":   "*dml.BoltGraph",
	}

	for _, backend := range testBackends() {
		t.Run(backend.name, func(t *testing.T) {
			previous := activeTestBackend
			activeTestBackend = backend

			t.Cleanup(func() { activeTestBackend = previous })

			// The harness is worth something only if the fixtures really
			// build this backend, so check that before running anything.
			g, _ := newPointerTestFixture(t)
			if got := fmt.Sprintf("%T", g); got != wantTypes[backend.name] {
				t.Fatalf("the fixtures built a %s, want a %s", got, wantTypes[backend.name])
			}

			for _, test := range registryTestsOnEveryBackend {
				t.Run(testFuncName(test), test)
			}
		})
	}
}

// registryTestsOnEveryBackend lists the fixture-based tests that are run
// against every backend. A test belongs here when its setup writes nothing
// that a commit-time Checker would decline: the tests that deliberately
// corrupt a graph out-of-band (the DetectsOutOfBand* ones, most TestAdversarial*,
// and the like) stay *Graph-only, because they simulate data that never went
// through this process's Checkers, and the on-read validation they exercise
// does not depend on the backend. A test that builds its own graph instead of
// using a fixture would not change backend, so it is not listed.
var registryTestsOnEveryBackend = []func(*testing.T){
	// PointerRegistry.
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
	TestPointerRegistryTagAsPointerIsIdempotent,
	TestPointerRegistrySetTargetComposesInsideOneTransaction,
	TestPointerRegistryComposedSetTargetIsUndoneByFailedNestedTransaction,

	// PointerMetadataRegistry (Representation C).
	TestPointerMetadataRegistryHasMetadataFalseInitially,
	TestPointerMetadataRegistrySetTargetLeavesSubjectChildrenUntouched,
	TestPointerMetadataRegistrySetTargetAllowsSelfTarget,
	TestPointerMetadataRegistrySetTargetReplacesExistingTarget,
	TestPointerMetadataRegistryRemoveTargetRemovesExisting,
	TestPointerMetadataRegistrySetTargetRequiresExistingTarget,

	// PointerMetadataRegistryD (Representation D).
	TestPointerMetadataRegistryDHasMetadataFalseInitially,
	TestPointerMetadataRegistryDSetTargetLeavesSubjectChildrenUntouched,
	TestPointerMetadataRegistryDSetTargetAllowsSelfTarget,
	TestPointerMetadataRegistryDSetTargetReplacesExistingTarget,
	TestPointerMetadataRegistryDRemoveTargetRemovesExisting,
	TestPointerMetadataRegistryDSetTargetRequiresExistingTarget,
	TestPointerMetadataRegistryDAllowsUnrelatedMetadataChildren,
	TestPointerMetadataRegistryDComposedSetTargetRollsBackMetadataCreation,

	// CapsuleRegistry.
	TestNewCapsuleRequiresExistingValue,
	TestNewCapsuleTagsAndSetsValue,
	TestNewCapsuleStartsWithNoPrevOrNext,
	TestCapsuleSetPrevAndNextLinkCapsules,
	TestCapsuleRemovePrevAndNext,
	TestCapsuleOperationsRequireCapsuleTag,
	TestCapsuleRegistryDeleteCapsuleDeletesCleanCapsule,
	TestCapsuleRegistryDeleteCapsuleFailsIfStillListed,
	TestCapsuleRegistryDeleteCapsuleFailsIfPrevOrNextSet,
	TestCapsuleRegistryDeleteCapsuleFailsIfSlotHasExtraParent,
	TestCapsuleRegistryDeleteCapsuleRequiresCapsuleTag,
	TestCapsuleRegistryDeleteCapsuleRequiresExistingNode,
	TestCapsulesWithValueFindsAllOccurrences,
	TestCapsulesWithValueIgnoresUnrelatedEdges,
	TestCapsulesWithValueIgnoresUnrelatedParentsOfSlot,
	TestCapsulesWithValuePagesAcrossMultipleFetches,
	TestCapsulesWithValueFallsBackToOneFullReadPerCall,
	TestCapsuleLinkAndDeleteComposeAndRollBackWithEnclosingTransaction,

	// ListRegistry.
	TestNewListTagsListAndStartsEmpty,
	TestListAppendSingleElementIsHeadAndTail,
	TestListAppendMultipleMaintainsOrder,
	TestListPrependAddsAtFront,
	TestListInsertAfterMiddle,
	TestListInsertAfterTailUpdatesTail,
	TestListInsertAfterRequiresCapsuleInList,
	TestListRegistryCheckerCatchesInvalidStructureAtCommitTime,
	TestListOperationsRequireListTag,
	TestListContainsFindsValue,
	TestListContainsFalseForAbsentValue,
	TestListContainsScopedToOwningList,
	TestListOccurrencesOfFindsDuplicates,
	TestListContainsRequiresListTag,
	TestListContainsRequiresExistingValue,
	TestListRemoveWithoutDeletingCapsuleMiddleElement,
	TestListRemoveWithoutDeletingCapsuleHeadUpdatesHead,
	TestListRemoveWithoutDeletingCapsuleTailUpdatesTail,
	TestListRemoveWithoutDeletingCapsuleSoleElementEmptiesList,
	TestListRemoveWithoutDeletingCapsuleClearsCapsuleOwnLinks,
	TestListRemoveWithoutDeletingCapsuleRequiresCapsuleInList,
	TestListRemoveWithoutDeletingCapsuleRequiresListTag,
	TestListDeleteListRequiresListTag,
	TestListDeleteListFailsIfNotEmpty,
	TestListDeleteListSucceedsWhenEmpty,
	TestListRemoveWithoutDeletingCapsuleThenDeleteListSucceeds,
	TestListRemoveDeletesUnreferencedCapsule,
	TestListRemoveKeepsCapsuleIfStillReferencedElsewhere,
	TestListRemoveRequiresCapsuleInList,
	TestListRemoveRequiresListTag,
	TestListMutatorsComposeInsideOneTransactionAndRollBackTogether,
	TestListAppendInsideFailedNestedTransactionLeavesListValid,

	// The adversarial list/capsule tests whose setup is not itself an
	// invariant violation a Checker would decline.
	TestAdversarialListIgnoresUnrelatedListChild,
	TestAdversarialCapsuleIgnoresUnrelatedChild,
	TestAdversarialValueMayBeTheListItself,
	TestAdversarialSharedValueAcrossListsRemainsSeparateOccurrences,
	TestAdversarialDetachedCapsuleDoesNotBecomeListMember,
	TestAdversarialCapsuleValueMissingIsDetected,

	// SetRegistry.
	TestNewSetTagsSetAndStartsEmpty,
	TestSetTagAsSetTagsFreshNode,
	TestSetTagAsSetAllowsExistingChildren,
	TestSetTagAsSetIsIdempotent,
	TestSetTagAsSetRequiresExistingNode,
	TestSetAddAddsMember,
	TestSetAddIsIdempotentForExistingMember,
	TestSetAddAllowsSelfMembership,
	TestSetAddRequiresExistingMember,
	TestSetContainsRequiresExistingMember,
	TestSetRemoveRemovesExistingMember,
	TestSetRemoveNoOpWhenAbsent,
	TestSetRemoveRequiresExistingMember,
	TestSetContainsReflectsMembership,
	TestSetMembersReturnsAllDirectChildren,
	TestSetMembersDoesNotRecurseIntoNestedSet,
	TestSetSizeMatchesMemberCount,
	TestSetDeleteSetSucceedsWhenEmpty,
	TestSetDeleteSetFailsIfNotEmpty,
	TestSetDeleteSetFailsIfReferencedElsewhere,
	TestSetOperationsRequireSetTag,
	TestSetOperationsRequireExistingSetNode,
	TestSetRegistryTagAsSetRejectsCompositeSetConflict,
	TestSetAddAndRemoveAreVisibleToCommitTimeCheckers,
	TestSetMutatorsComposeInsideOneTransactionAndRollBackTogether,

	// CompositeSetRegistry.
	TestNewCompositeSetStartsEmpty,
	TestCompositeSetAddOperandScalarAdditive,
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
	TestCompositeSetRegistryCheckerCatchesMalformedDescriptorAtCommitTime,
	TestCompositeSetContainsReflectsMembership,
	TestCompositeSetContainsRequiresCompositeSetTag,
	TestCompositeSetContainsRequiresExistingValue,

	// CompositeSetLogRegistry.
	TestNewCompositeSetLogTagsBothAllListsAndAllCompositeSetLogs,
	TestCompositeSetLogAppendOperationScalarAdditive,
	TestCompositeSetLogEvaluateOrderSensitiveFold,
	TestCompositeSetLogAppendOperationRequiresKnownSetTagWhenExpand,
	TestCompositeSetLogEvaluateResolvesSetOperand,
	TestCompositeSetLogEvaluateResolvesCompositeSetOperand,
	TestCompositeSetLogEvaluateResolvesNestedCompositeSetLogOperand,
	TestCompositeSetRegistryResolvesCompositeSetLogOperand,
	TestCompositeSetLogEvaluateDetectsCycle,
	TestCompositeSetLogEvaluateDetectsCrossRepresentationCycle,
	TestCompositeSetLogContainsMatchesEvaluate,
	TestCompositeSetLogContainsRequiresExistingValue,
	TestCompositeSetLogRemoveOperationDeletesDescriptorAndCapsule,
	TestCompositeSetLogRemoveOperationRequiresOperationInLog,
	TestCompositeSetLogDeleteCompositeSetLogSucceedsWhenEmpty,
	TestCompositeSetLogDeleteCompositeSetLogFailsIfNotEmpty,
	TestCompositeSetLogOperationsRequireCompositeSetLogTag,
	TestCompositeSetLogEvaluateDetectsMalformedDescriptor,
	TestCompositeSetLogRegistrySharesListStructureChecker,
	TestSetRegistryTagAsSetRejectsCompositeSetLogConflict,
	TestCompositeSetLogRemoveOperationIsAtomicWhenCapsuleCannotBeDeleted,
	TestCompositeSetLogNestedRemoveOperationFailureLeavesLogIntactAndOuterContinues,

	// Domain pointers (Representations B and D) and cross-role scenarios.
	TestDomainPointerRegistryBNewDomainPointerAndTargetWithNoDomain,
	TestDomainPointerRegistryBNewDomainPointerIsIdempotent,
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
	TestDomainPointerRegistryDCheckerCatchesOutOfBandTargetChange,
	TestDomainPointerRegistryDCheckerCatchesOutOfBandDomainChange,
	TestDomainPointerRegistryBCheckerCatchesOutOfBandTargetChange,
	TestDomainPointerRegistryBCheckerCatchesOutOfBandDomainChange,
	TestDomainPointerRegistryDSetDomainFailureRollsBackMetadataCreation,
	TestCrossRoleNodeParticipatesInMultipleStructuresSimultaneously,
	TestCrossRoleDomainPointerDetectsCycleIntroducedThroughDomainItself,
	TestCrossRoleSetRegistryConflictCheckExercisedWhileSetIsDomainAndOperand,
	TestDomainStalenessPlainSetRemoveOfCurrentTargetIsRejected,
	TestDomainStalenessCompositeDomainMutationsThatStrandTargetAreRejected,
	TestDomainStalenessLogDomainMutationsThatStrandTargetAreRejected,
	TestDomainStalenessNestedSetShrinkIsRejectedThroughDiamond,
	TestDomainStalenessOneStrandedPointerRejectsWholeTransactionAcrossRepresentations,
	TestDomainStalenessShrinkThenRetargetInOneTransactionIsAccepted,
	TestDomainStalenessRepointingDescriptorOperandIsRejected,
	TestDomainStalenessComposedExportedCallsAreJudgedAtOutermostCommit,

	// LeaseRegistry.
	TestLeaseNewSessionIsTaggedAndListed,
	TestLeaseAcquireReportsFirstHolderOnly,
	TestLeaseReleaseReportsLastHolderOnly,
	TestLeaseOperationsRequireOpenSessionAndSet,
	TestLeaseCloseSessionReleasesEveryHoldAndReportsFreedResources,
	TestLeaseCloseSessionRollsBackIfSessionIsReferencedElsewhere,
	TestLeaseCloseAllSessionsIsTheStartupSweep,
	TestLeaseAcquireComposesAndRollsBackWithEnclosingTransaction,
}