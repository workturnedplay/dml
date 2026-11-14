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
)

// Foundational names are ordinary named nodes, exactly like ROOT, whose
// special meaning comes entirely from higher-level relationships and
// processors that interpret them — never from the primitive Graph or the
// NameRegistry itself. See THEORY_NOTES_FROM_CONVERSATION.md and
// theorystate.md for the semantics each name is intended to support.
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
	// (THEORY_NOTES_FROM_CONVERSATION.md section 11 / theorystate.md
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
	// section 11 / theorystate.md section 11). See ListRegistry. Also
	// reused, dual-tagged alongside NameAllCompositeSetLogs, by
	// CompositeSetLogRegistry (theorystate.md section 82).
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

	// NameAllSets tags a node as Set-kind via the relationship
	// (AllSets, S) (theorystate.md section 9 / 9a / 79): S's direct
	// children are exactly its members, with no intermediary node needed
	// -- see SetRegistry for why Sets do not need one, unlike every
	// intermediary-node-based structure elsewhere in this file.
	NameAllSets = "AllSets"

	// NameAllCompositeSets tags a node as CompositeSet-kind via the
	// relationship (AllCompositeSets, C) (theorystate.md section 80 / 81):
	// unlike a plain Set, C's direct children are operand-descriptor
	// nodes, not members themselves. See CompositeSetRegistry.
	NameAllCompositeSets = "AllCompositeSets"

	// NameAllAdditiveOp and NameAllSubtractiveOp tag an operand-descriptor
	// node (see CompositeSetRegistry) with its operation-kind axis
	// (theorystate.md section 80): whether the descriptor's operand
	// contributes to a composite Set's evaluated membership via union
	// (additive) or set-difference (subtractive). Exactly one of these
	// two tags applies to any given descriptor node.
	NameAllAdditiveOp    = "AllAdditiveOp"
	NameAllSubtractiveOp = "AllSubtractiveOp"

	// NameAllScalarOperand and NameAllSetOperand tag an operand-descriptor
	// node with its operand-kind axis (theorystate.md section 80),
	// orthogonal to the operation-kind axis above: whether the
	// descriptor's operand is used as a single literal member (scalar) or
	// expanded via its own Set-kind membership (set). This is always
	// recorded explicitly per descriptor, never inferred from the
	// operand's own tags -- see the CompositeSetRegistry doc comment for
	// why inferring it was found to be a design mistake. Exactly one of
	// these two tags applies to any given descriptor node.
	NameAllScalarOperand = "AllScalarOperand"
	NameAllSetOperand    = "AllSetOperand"

	// NameAllCompositeSetLogs tags a node as CompositeSetLog-kind via the
	// relationship (AllCompositeSetLogs, node) (theorystate.md section
	// 82): the same node is simultaneously tagged (AllLists, node), since
	// a CompositeSetLog is an ordinary List reused and reinterpreted as
	// an append-only log of Set-mutating operations (section 10c's
	// precedent for one identity carrying more than one simultaneous
	// interpretation). See CompositeSetLogRegistry.
	NameAllCompositeSetLogs = "AllCompositeSetLogs"

	// NameAllDomainSlot tags a Domain Pointer's domain-slot node
	// (theorystate.md section 10c): a freshly-minted intermediary node,
	// attached to the pointer's anchor (P for Representation B, the
	// metadata node M for Representation D) exactly like every other
	// slot in this file, whose own single child is the domain node
	// itself. There is deliberately no separate tag marking a node as
	// "a domain" -- any node already carrying one of the three
	// Set-representation tags (AllSets, AllCompositeSets,
	// AllCompositeSetLogs) is domain-eligible (theorystate.md section
	// 9c). See DomainPointerRegistryB / DomainPointerRegistryD.
	NameAllDomainSlot = "AllDomainSlot"

	// NameAllSessions tags a node as a session (theorystate.md section
	// 111): the liveness identity of one client of the graph. Holds on
	// resources are membership of the session in the resource's holder
	// Set. Sessions are ephemeral. See LeaseRegistry.
	NameAllSessions = "AllSessions"

	// NameAllSlottedLists tags a node as a slotted list, and the next three
	// tag its members node and its head and tail slots (see
	// SlottedListRegistry). NameAllSlottedCapsules and the three role-slot
	// names after it are the capsule tag set of slotted lists: a separate
	// set from ListRegistry's, so the relevance filter keeps the two list
	// types' Checkers apart.
	NameAllSlottedLists        = "AllSlottedLists"
	NameAllSlottedListMembers  = "AllSlottedListMembers"
	NameAllSlottedListHeadSlot = "AllSlottedListHeadSlot"
	NameAllSlottedListTailSlot = "AllSlottedListTailSlot"

	NameAllSlottedCapsules         = "AllSlottedCapsules"
	NameAllSlottedCapsulePrevSlot  = "AllSlottedCapsulePrevSlot"
	NameAllSlottedCapsuleValueSlot = "AllSlottedCapsuleValueSlot"
	NameAllSlottedCapsuleNextSlot  = "AllSlottedCapsuleNextSlot"

	// NameRoot is the name of the ROOT node (theorystate.md section 12).
	// It is deliberately NOT in FoundationalNames: RootGraph needs ROOT to
	// exist before the graph stack is built, so Host ensures it directly on
	// the raw store first (theorystate.md section 112).
	NameRoot = "ROOT"
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
	NameAllSets,
	NameAllCompositeSets,
	NameAllAdditiveOp,
	NameAllSubtractiveOp,
	NameAllScalarOperand,
	NameAllSetOperand,
	NameAllCompositeSetLogs,
	NameAllDomainSlot,
	NameAllSessions,
	NameAllSlottedLists,
	NameAllSlottedListMembers,
	NameAllSlottedListHeadSlot,
	NameAllSlottedListTailSlot,
	NameAllSlottedCapsules,
	NameAllSlottedCapsulePrevSlot,
	NameAllSlottedCapsuleValueSlot,
	NameAllSlottedCapsuleNextSlot,
	NameAllFoundational,
	NameAllStrings,
	NameAllChars,
	NameAllIndexedStrings,
	NameAllIndexNodes,
	NameAllIndexEntries,
	NameAllStartsWithSlots,
	NameAllIndexStepSlots,
	NameEmptyString,
	NameIndexRoot,
	NameAllTextLists,
	NameAllTextFormSlots,
	NameAllScopes,
	NameAllScopeSlots,
	NameAllScopeAllow,
	NameAllScopeDeny,
	NameAllNotes,
	NameAllNoteSubjects,
	NameAllNoteTexts,
}

// TagFoundational tags every foundational name with (AllFoundational, id),
// plus every node in extra (the host passes ROOT), in one idempotent
// transaction. It lets a browser list the foundational nodes among the
// parents of any node with one HasRelationship each, instead of comparing
// every parent against every known foundational node. AllFoundational does
// not tag itself. ids must hold every name in FoundationalNames.
func TagFoundational(graph Transactor, ids map[string]NodeID, extra ...NodeID) error {
	tag, ok := ids[NameAllFoundational]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNameNotFound, NameAllFoundational)
	}

	nodes := make([]NodeID, 0, len(FoundationalNames)+len(extra))

	for _, name := range FoundationalNames {
		id, present := ids[name]
		if !present {
			return fmt.Errorf("%w: %q", ErrNameNotFound, name)
		}

		if id != tag {
			nodes = append(nodes, id)
		}
	}

	nodes = append(nodes, extra...)

	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		for _, node := range nodes {
			if tagErr := tagNodeTx(tx, tag, node); tagErr != nil {
				return tagErr
			}
		}

		return nil
	}))
}
