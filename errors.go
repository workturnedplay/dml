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
	"errors"
)

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

	// ErrNotSet is returned by SetRegistry when asked to operate on a
	// node that is not tagged (AllSets, node).
	ErrNotSet = errors.New("node is not tagged as a set")

	// ErrNotSession is returned by LeaseRegistry when asked to act as, or
	// for, a node that is not tagged (AllSessions, node) -- including a
	// session that has already been closed and deleted, which fails with
	// ErrNodeNotFound instead.
	ErrNotSession = errors.New("node is not tagged as a session")

	// ErrSetRepresentationConflict is returned when an operation would
	// give a node more than one of the mutually exclusive
	// Set-representation tags (AllSets, AllCompositeSets, and eventually
	// AllCompositeSetLogs -- theorystate.md section 79) at the same time.
	ErrSetRepresentationConflict = errors.New("node already carries a different set-representation tag")

	// ErrNotCompositeSet is returned by CompositeSetRegistry when asked
	// to operate on a node that is not tagged (AllCompositeSets, node).
	ErrNotCompositeSet = errors.New("node is not tagged as a composite set")

	// ErrNotCompositeSetLog is returned by CompositeSetLogRegistry when
	// asked to operate on a node that is not tagged
	// (AllCompositeSetLogs, node).
	ErrNotCompositeSetLog = errors.New("node is not tagged as a composite set log")

	// ErrOperandNotInCompositeSet is returned by
	// CompositeSetRegistry.RemoveOperand when the given descriptor node
	// is not currently a direct child of the given composite set, and
	// reused by CompositeSetLogRegistry.RemoveOperation for the
	// identically-shaped problem (the descriptor is not currently an
	// operation of the given log).
	ErrOperandNotInCompositeSet = errors.New("descriptor is not an operand of this composite set")
	// ErrInvalidOperandDescriptor is returned when an operand-descriptor
	// node (theorystate.md section 80) does not have exactly the shape
	// CompositeSetRegistry.AddOperand / CompositeSetLogRegistry.AppendOperation
	// always create: exactly one operation-kind tag (additive xor
	// subtractive), exactly one operand-kind tag (scalar xor set), and
	// exactly one outgoing relationship identifying its operand. This can
	// happen through an out-of-band Graph mutation, or (for
	// CompositeSetLogRegistry specifically) by appending a value directly
	// via the underlying ListRegistry.Append instead of going through
	// AppendOperation.
	ErrInvalidOperandDescriptor = errors.New("operand descriptor does not have the expected shape")
	// ErrInvalidSetOperand is returned when an operand-descriptor tagged
	// as a set-expansion operand (AllSetOperand) points at a node that
	// does not currently carry any known Set-representation tag. This is
	// checked both when the descriptor is created (AddOperand /
	// AppendOperation) and freshly re-checked every time it is resolved
	// (Evaluate never caches), so it can also surface if the operand's
	// Set-representation tag was removed after the descriptor was
	// created. Also returned by CompositeSetRegistry.resolveSetOperand /
	// CompositeSetLogRegistry.resolveSetOperand if an operand somehow
	// carries none of the three currently-recognized representations at
	// resolve time.
	ErrInvalidSetOperand = errors.New("set operand does not carry a known set-representation tag")
	// ErrCompositeSetCycle is returned by CompositeSetRegistry.Evaluate
	// and CompositeSetLogRegistry.Evaluate/Contains when resolving a
	// set-expansion operand would revisit a composite-kind node already
	// on the current resolution path (theorystate.md section 83). A
	// plain Set operand can never participate in a cycle, since it is
	// always a leaf; only chains of composite-kind nodes (CompositeSet
	// and/or CompositeSetLog, in any combination) referencing each other
	// can cycle.
	ErrCompositeSetCycle = errors.New("composite set operand graph contains a cycle")
	// ErrTargetOutsideDomain is returned by DomainPointerRegistryB/D's
	// SetTarget when the given target does not currently belong to the
	// pointer's attached domain's resolved membership (theorystate.md
	// section 9c/10c/86), and by their SetDomain when the pointer's
	// current target does not belong to the proposed new domain. A
	// domain is any node carrying one of the three Set-representation
	// tags (AllSets, AllCompositeSets, AllCompositeSetLogs); see
	// domainContainsGeneric.
	ErrTargetOutsideDomain = errors.New("target does not belong to the pointer's domain")
	// ErrCapsuleNotInList is returned by ListRegistry.InsertAfter when
	// the given capsule is not currently an element of the given list
	// (i.e. (list, capsule) does not exist).
	ErrCapsuleNotInList = errors.New("capsule is not an element of this list")

	// ErrCapsuleNotEmpty is returned by CapsuleRegistry.DeleteCapsule
	// when capsule, or one of its three role-slot nodes, currently
	// carries any relationship beyond the fixed shape buildCapsuleTx
	// itself establishes -- e.g. capsule is still an element of some
	// list, still tagged head/tail, a slot still has its target set, or
	// a slot has picked up some unrelated parent of its own. This is the
	// CapsuleRegistry-level analogue of ErrNodeNotEmpty, one layer up:
	// DeleteCapsule makes no changes at all when this is returned, and a
	// caller must first undo whatever is holding the capsule or one of
	// its slots open (e.g. via ListRegistry.Remove) before deletion can
	// succeed.
	ErrCapsuleNotEmpty = errors.New("capsule or one of its role slots has relationships beyond its own fixed structure; the capsule cannot be safely deleted")

	// ErrInvalidListStructure is returned when ListRegistry discovers that
	// the graph no longer satisfies the structural invariants of an ordered
	// list. This is intended for out-of-band Graph mutations: normal list
	// operations maintain these relationships transactionally.
	ErrInvalidListStructure = errors.New("list structure is invalid")

	// ErrListCycle is returned by ListRegistry.Elements when the head-to-tail
	// traversal encounters the same ElementCapsule more than once. A cycle
	// can only arise through an out-of-band graph mutation because the
	// normal list operations maintain an acyclic chain.
	ErrListCycle = errors.New("list next-chain contains a cycle")
)
