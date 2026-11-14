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
// one -- see the PointerRegistry doc comment for why (theorystate.md
// section 74: out-of-band mutation can violate this at any time, and
// every caller re-derives fresh rather than caching).
func singleChildTarget(g GraphReader, node NodeID, exclude ...NodeID) (target NodeID, hasTarget bool, err error) {
	outgoing, err := g.FindOutgoing(node)
	if err != nil {
		return 0, false, wrapInterfaceErr(err)
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

// findUniqueTaggedParent returns the single parent of node that is tagged
// via (tag, parent), i.e. for which g.HasRelationship(tag, parent) holds.
//
// This is the shared reverse-lookup primitive behind
// PointerMetadataRegistry's two-hop subject -> slot -> metadata
// discovery. It requires node to exist. found is false if no tagged
// parent exists. If more than one tagged parent exists -- only reachable
// through an out-of-band Graph mutation -- ErrAmbiguousPointerMetadata is
// returned instead of arbitrarily picking one.
func findUniqueTaggedParent(g GraphReader, node, tag NodeID) (parent NodeID, found bool, err error) {
	incoming, err := g.FindIncoming(node)
	if err != nil {
		return 0, false, wrapInterfaceErr(err)
	}

	for _, rel := range incoming {
		has, hasErr := g.HasRelationship(tag, rel.From)
		if hasErr != nil {
			return 0, false, wrapInterfaceErr(hasErr)
		}
		if !has {
			continue
		}

		if found {
			return 0, false, ErrAmbiguousPointerMetadata
		}

		parent = rel.From
		found = true
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
func findUniqueTaggedChild(g GraphReader, node, tag NodeID) (child NodeID, found bool, err error) {
	// A reader that can intersect two child sets natively answers in time
	// bounded by the smaller of them (see commonChildrenFinder). Asking for
	// two is enough to tell none, one and more than one apart, so the
	// ambiguity check is kept.
	if finder, native := g.(commonChildrenFinder); native {
		common, findErr := finder.findCommonChildren(node, tag, 2)

		switch {
		case findErr == nil:
			return uniqueNode(common)
		case !errors.Is(findErr, errPagingUnsupported):
			return 0, false, wrapInterfaceErr(findErr)
		}
	}

	return scanTaggedChild(g, node, tag)
}

// uniqueNode turns the (at most two) nodes of a common-children lookup into
// findUniqueTaggedChild's answer.
func uniqueNode(nodes []NodeID) (NodeID, bool, error) {
	switch len(nodes) {
	case 0:
		return 0, false, nil
	case 1:
		return nodes[0], true, nil
	default:
		return 0, false, ErrAmbiguousPointerMetadata
	}
}

// scanTaggedChild is findUniqueTaggedChild for a reader with no native
// common-children lookup: it reads all of node's children and probes each for
// the tag, so it costs the fan-out of node.
func scanTaggedChild(g GraphReader, node, tag NodeID) (child NodeID, found bool, err error) {
	outgoing, err := g.FindOutgoing(node)
	if err != nil {
		return 0, false, wrapInterfaceErr(err)
	}

	for _, rel := range outgoing {
		has, hasErr := g.HasRelationship(tag, rel.To)
		if hasErr != nil {
			return 0, false, wrapInterfaceErr(hasErr)
		}
		if !has {
			continue
		}

		if found {
			return 0, false, ErrAmbiguousPointerMetadata
		}

		child = rel.To
		found = true
	}

	return child, found, nil
}

// exactlyOneTag reports which of tagA or tagB currently tags node via
// (tag, node), requiring exactly one of the two to hold. This is the
// shared axis-check behind CompositeSetRegistry operand descriptors' two
// orthogonal tag axes (theorystate.md section 80): operation kind
// (AllAdditiveOp/AllSubtractiveOp) and operand kind
// (AllScalarOperand/AllSetOperand).
//
// isA reports whether tagA (rather than tagB) is the one that holds. If
// neither or both hold -- only reachable through an out-of-band mutation,
// since CompositeSetRegistry.AddOperand always wires a fresh descriptor
// with exactly one tag per axis -- ErrInvalidOperandDescriptor is
// returned instead of guessing.
func exactlyOneTag(g GraphReader, node, tagA, tagB NodeID) (isA bool, err error) {
	hasA, err := g.HasRelationship(tagA, node)
	if err != nil {
		return false, wrapInterfaceErr(err)
	}

	hasB, err := g.HasRelationship(tagB, node)
	if err != nil {
		return false, wrapInterfaceErr(err)
	}

	switch {
	case hasA && !hasB:
		return true, nil
	case hasB && !hasA:
		return false, nil
	default:
		return false, ErrInvalidOperandDescriptor
	}
}
