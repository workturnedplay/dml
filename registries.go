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

// Registries bundles every registry the Host constructs over its graph.
// Constructing them registers their commit-time Checkers, which is why they
// are built before the startup sweep (VerifyAll). Representation C
// (PointerMetadataRegistry) is deliberately absent: it exists as a
// deliberately stricter test representation (theorystate.md section 10a).
type Registries struct {
	Pointers    *PointerRegistry
	SubPointers *PointerRegistry
	Metadata    *PointerMetadataRegistryD
	Capsules    *CapsuleRegistry
	Lists       *ListRegistry
	Sets        *SetRegistry
	Composites  *CompositeSetRegistry
	Logs        *CompositeSetLogRegistry
	DomainSlots *PointerRegistry
	DomainB     *DomainPointerRegistryB
	DomainD     *DomainPointerRegistryD
	Leases      *LeaseRegistry

	// SlottedCapsules and SlottedLists are the second ordered list type
	// (head, tail and members in slots), with a capsule tag set of its own.
	SlottedCapsules *CapsuleRegistry
	SlottedLists    *SlottedListRegistry

	// Text is the interned-string layer (theorystate.md section 114).
	Text *TextRegistry

	// TextForms keeps text lists and the forms (alternative spellings) of
	// texts, over the slotted lists.
	TextForms *TextFormRegistry

	// Scopes keeps who may do what, by path (theorystate.md section 116).
	Scopes *ScopeRegistry

	// Notes keeps texts linked to nodes (theorystate.md section 117).
	Notes *NoteRegistry
}

// NewRegistries constructs every registry over graph. ids must hold an
// entry for every name in FoundationalNames (as returned by
// NameRegistry.BootstrapNames); a missing one is ErrNameNotFound, never a
// silent NodeID 0. names is the registry those names were bootstrapped
// through: the text layer names its chars in it.
func NewRegistries(graph GraphAPI, names *NameRegistry, ids map[string]NodeID) (*Registries, error) {
	for _, name := range FoundationalNames {
		if _, ok := ids[name]; !ok {
			return nil, fmt.Errorf("%w: %q", ErrNameNotFound, name)
		}
	}

	pointers, err := NewPointerRegistry(graph, ids[NameAllPointers])
	if err != nil {
		return nil, err
	}

	subPointers, err := NewPointerRegistry(graph, ids[NameAllSubPointers])
	if err != nil {
		return nil, err
	}

	metadata, err := NewPointerMetadataRegistryD(graph, ids[NameAllPointerMetadata], ids[NameAllPointerMetadataSubjectSlot], ids[NameAllPointerMetadataTargetSlot])
	if err != nil {
		return nil, err
	}

	capsules, err := NewCapsuleRegistry(graph, ids[NameAllElementCapsules], ids[NameAllElementCapsulePrevSlot], ids[NameAllElementCapsuleValueSlot], ids[NameAllElementCapsuleNextSlot])
	if err != nil {
		return nil, err
	}

	lists, err := NewListRegistry(graph, capsules, ids[NameAllLists], ids[NameAllHeads], ids[NameAllTails])
	if err != nil {
		return nil, err
	}

	slottedCapsules, err := NewCapsuleRegistry(
		graph,
		ids[NameAllSlottedCapsules],
		ids[NameAllSlottedCapsulePrevSlot],
		ids[NameAllSlottedCapsuleValueSlot],
		ids[NameAllSlottedCapsuleNextSlot],
	)
	if err != nil {
		return nil, err
	}

	slottedLists, err := NewSlottedListRegistry(
		graph,
		slottedCapsules,
		ids[NameAllSlottedLists],
		ids[NameAllSlottedListMembers],
		ids[NameAllSlottedListHeadSlot],
		ids[NameAllSlottedListTailSlot],
	)
	if err != nil {
		return nil, err
	}

	sets, err := NewSetRegistry(graph, ids[NameAllSets], ids[NameAllCompositeSets], ids[NameAllCompositeSetLogs])
	if err != nil {
		return nil, err
	}

	composites, err := NewCompositeSetRegistry(graph, sets, ids[NameAllCompositeSets], ids[NameAllAdditiveOp], ids[NameAllSubtractiveOp], ids[NameAllScalarOperand], ids[NameAllSetOperand])
	if err != nil {
		return nil, err
	}

	// The log's operations are kept in slotted lists; passing lists instead
	// is the only change needed to keep them in ListRegistry lists.
	logs, err := NewCompositeSetLogRegistry(graph, slottedLists, composites, ids[NameAllCompositeSetLogs], ids[NameAllAdditiveOp], ids[NameAllSubtractiveOp], ids[NameAllScalarOperand], ids[NameAllSetOperand])
	if err != nil {
		return nil, err
	}

	composites.SetLogs(logs)

	domainSlots, err := NewPointerRegistry(graph, ids[NameAllDomainSlot])
	if err != nil {
		return nil, err
	}

	leases, err := NewLeaseRegistry(graph, sets, ids[NameAllSessions])
	if err != nil {
		return nil, err
	}

	text, err := NewTextRegistry(graph, names, ids)
	if err != nil {
		return nil, err
	}

	forms, err := NewTextFormRegistry(graph, text, slottedLists, ids)
	if err != nil {
		return nil, err
	}

	scopes, err := NewScopeRegistry(graph, text, sets, ids)
	if err != nil {
		return nil, err
	}

	notes, err := NewNoteRegistry(graph, text, ids)
	if err != nil {
		return nil, err
	}

	return &Registries{
		Pointers:    pointers,
		SubPointers: subPointers,
		Metadata:    metadata,
		Capsules:    capsules,
		Lists:       lists,
		Sets:        sets,
		Composites:  composites,
		Logs:        logs,
		DomainSlots: domainSlots,
		DomainB:     NewDomainPointerRegistryB(graph, subPointers, domainSlots, sets, composites, logs),
		DomainD:     NewDomainPointerRegistryD(graph, metadata, domainSlots, sets, composites, logs),
		Leases:      leases,

		SlottedCapsules: slottedCapsules,
		SlottedLists:    slottedLists,
		Text:            text,
		TextForms:       forms,
		Scopes:          scopes,
		Notes:           notes,
	}, nil
}
