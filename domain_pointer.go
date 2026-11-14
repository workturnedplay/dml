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

// domainConstraint holds the shared domain-slot state and operations
// used by both DomainPointerRegistryB (Representation B) and
// DomainPointerRegistryD (Representation D) -- see theorystate.md
// section 10c for why only Representations B and D can safely carry a
// domain slot, and section 9c for why a "domain" is any node already
// carrying one of the three Set-representation tags rather than a new
// tagged concept of its own.
//
// Both representations attach the domain slot identically: a single
// freshly-minted node U3, tagged AllDomainSlot, wired
// anchor -> U3 -> domainNode. They differ only in which node serves as
// "anchor" (P itself for B, the metadata node M for D) and in which
// underlying Pointer representation ultimately enforces the pointer's
// own target cardinality -- exactly the same split subjectMetadataBase
// already makes between shared subject-side logic and each
// representation's own target discovery (see that type's doc comment),
// applied here to the domain-slot concept instead.
//
// domainSlots is expected to be a single, shared *PointerRegistry
// instance -- constructed once via NewPointerRegistry(graph,
// allDomainSlot) -- passed to every domainConstraint-embedding registry
// in the same graph. Constructing a second, independent PointerRegistry
// under the same tag would register a redundant (if harmless) duplicate
// Checker for the identical cardinality invariant.
type domainConstraint struct {
	domainSlots *PointerRegistry
	sets        *SetRegistry
	composites  *CompositeSetRegistry
	logs        *CompositeSetLogRegistry
}

// domainSlotFor returns anchor's domain-slot child (U3), if any, found
// by tag rather than by position or exclusion, exactly like every other
// slot lookup in this file.
func (d *domainConstraint) domainSlotFor(graph GraphReader, anchor NodeID) (slot NodeID, found bool, err error) {
	return findUniqueTaggedChild(graph, anchor, d.domainSlots.allPointers)
}

// Domain returns anchor's current domain node, if any. hasDomain is
// false both when anchor has no domain slot at all and when it has one
// with no domain node set yet.
func (d *domainConstraint) Domain(graph GraphReader, anchor NodeID) (domain NodeID, hasDomain bool, err error) {
	slot, found, err := d.domainSlotFor(graph, anchor)
	if err != nil || !found {
		return 0, false, err
	}

	return d.domainSlots.Target(graph, slot)
}

// attachDomain sets anchor's domain to domain as one transaction (nested
// if graph is a Tx, which is how the wrapper-level SetDomain methods on
// DomainPointerRegistryB/D use it, after their own target-side
// validation), creating anchor's domain
// slot first if it does not exist yet. domain must already carry one of
// the three currently-recognized Set-representation tags
// (theorystate.md section 9c) -- checked here, at write time, via the
// same operandCarriesKnownSetTag helper composite-Set operands already
// use for the identical check (theorystate.md section 80) -- returning
// ErrInvalidSetOperand otherwise.
//
// This does NOT validate the new domain against anchor's current
// target, if any: only the representation-specific wrapper
// (DomainPointerRegistryB/D) knows how to discover anchor's current
// target for its own representation, so that check is performed there,
// before delegating to this method -- see DomainPointerRegistryB.
// SetDomain / DomainPointerRegistryD.SetDomain.
func (d *domainConstraint) attachDomain(graph Transactor, anchor, domain NodeID) error {
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		if existsErr := requireAllExist(tx, anchor, domain); existsErr != nil {
			return existsErr
		}

		known, err := operandCarriesKnownSetTag(tx, d.sets, d.composites, d.logs, domain)
		if err != nil {
			return err
		}
		if !known {
			return ErrInvalidSetOperand
		}

		slot, found, err := d.domainSlotFor(tx, anchor)
		if err != nil {
			return err
		}

		if !found {
			newSlot, createErr := createTaggedNodeTx(tx, d.domainSlots.allPointers)
			if createErr != nil {
				return createErr
			}
			if linkErr := addRelationshipTx(tx, anchor, newSlot); linkErr != nil {
				return linkErr
			}

			return addRelationshipTx(tx, newSlot, domain)
		}

		return d.domainSlots.SetTarget(tx, slot, domain)
	}))
}

// RemoveDomain clears anchor's domain, if any. The domain-slot node
// itself is left in place (no cascade deletion, consistent with
// theorystate.md section 18); a domain-slot with no domain set is a
// valid, meaningful "no constraint" state, exactly like an empty
// Pointer elsewhere in this file.
func (d *domainConstraint) RemoveDomain(graph Transactor, anchor NodeID) (removed bool, err error) {
	return transactBool(graph, func(tx Tx) (bool, error) {
		if existsErr := requireExists(tx, anchor); existsErr != nil {
			return false, existsErr
		}

		slot, found, slotErr := d.domainSlotFor(tx, anchor)
		if slotErr != nil || !found {
			return false, slotErr
		}

		return d.domainSlots.RemoveTarget(tx, slot)
	})
}

// validateMembership reports whether target currently belongs to
// domain's resolved membership, dispatched generically over whichever of
// the three Set representations domain actually carries (see
// domainContainsGeneric), returning ErrTargetOutsideDomain if not.
func (d *domainConstraint) validateMembership(graph GraphReader, domain, target NodeID) error {
	contains, err := domainContainsGeneric(graph, d.sets, d.composites, d.logs, domain, target)
	if err != nil {
		return err
	}
	if !contains {
		return ErrTargetOutsideDomain
	}

	return nil
}

// checkAllowed reports whether target is a legal value to set on anchor,
// given whatever domain (if any) is currently attached via anchor's
// domain slot. An anchor with no domain slot, or a slot with no domain
// node set yet, always allows any target -- exactly like a Pointer with
// no domain constraint at all.
//
// This only validates against the domain's membership as of this call.
// A domain's membership changing afterward -- through a mutation that
// never touches anchor or its slots -- is caught by the commit-time
// Checker built by registerChecker (theorystate.md section 86), which
// calls this same function for every affected anchor.
func (d *domainConstraint) checkAllowed(graph GraphReader, anchor, target NodeID) error {
	domain, hasDomain, err := d.Domain(graph, anchor)
	if err != nil {
		return err
	}
	if !hasDomain {
		return nil
	}

	return d.validateMembership(graph, domain, target)
}

// anchorTargetFunc reports the current target of a domain-constrained
// pointer's anchor: P for Representation B (DomainPointerRegistryB.Target),
// the metadata node M for Representation D
// (PointerMetadataRegistryD.targetOfMetadata). It is the only thing that
// differs between the two representations' commit-time Checkers.
type anchorTargetFunc func(g GraphReader, anchor NodeID) (target NodeID, hasTarget bool, err error)

// registerChecker registers the commit-time Checker enforcing that no
// domain-constrained pointer is left with a target outside its domain
// (theorystate.md section 86). It is shared by DomainPointerRegistryB and
// DomainPointerRegistryD; they differ only in targetTag (the tag of the
// node holding the pointer's target: AllSubPointers or
// AllPointerMetadataTargetSlot) and targetOf.
//
// The Checker fires when a transaction touches a domain slot, a target
// holder, a node carrying any Set-representation tag, or an operand
// descriptor (an axis-tagged node). It finds every
// affected anchor via affectedAnchors, then for each anchor that has both
// a target and a domain, re-validates membership against live state. Any
// error from evaluating the domain (cycle, malformed descriptor, ...) is
// propagated and declines the commit; only an anchor whose target is
// genuinely outside its domain yields ErrTargetOutsideDomain.
//
// The Tags list includes AllCompositeSetLogs only if logs was supplied;
// as everywhere else, logs must be the same registry wired into
// composites via CompositeSetRegistry.SetLogs, and must exist before
// this registry is constructed.
func (d *domainConstraint) registerChecker(graph GraphAPI, name string, targetTag NodeID, targetOf anchorTargetFunc) {
	tags := append([]NodeID{d.domainSlots.allPointers, targetTag, d.sets.allSets, d.composites.allCompositeSets}, d.composites.operandAxisTags()...)
	if d.logs != nil {
		tags = append(tags, d.logs.allCompositeSetLogs)
	}

	graph.RegisterChecker(Checker{
		Name: name,
		Tags: tags,
		Check: func(g GraphReader, touched map[NodeID]struct{}) error {
			anchors, err := d.affectedAnchors(g, targetTag, touched)
			if err != nil {
				return err
			}

			for _, anchor := range anchors {
				target, hasTarget, targetErr := targetOf(g, anchor)
				if targetErr != nil {
					return targetErr
				}
				if !hasTarget {
					continue
				}

				if allowedErr := d.checkAllowed(g, anchor, target); allowedErr != nil {
					return allowedErr
				}
			}

			return nil
		},
	})
}

// affectedAnchors returns, sorted, every anchor whose domain-pointer
// validity a transaction touching touched could have changed. An anchor
// is affected when:
//   - a touched node is a domain slot or a target holder (targetTag):
//     its owners are affected; or
//   - a touched node is Set-kind (any of the three representations):
//     that node, and every composite/log that transitively expands it,
//     may be some pointer's domain, so the owners of every domain slot
//     referencing any of them are affected; or
//   - a touched node is an operand descriptor (carries an axis tag): its
//     composite(s)/log(s) changed definition (for example its operand was
//     re-pointed), so they are treated exactly like a touched Set-kind
//     node. The walk up uses the descriptor's intact incoming edge, not
//     the edge that may just have been removed, so no diff is needed.
//
// Candidates are collected via reverse lookups only (Graph.incoming is
// the reverse index -- nothing is stored or kept in sync; see
// theorystate.md section 86). They are candidates, not verified anchors:
// the caller's targetOf and Domain lookups reject non-anchors by finding
// no target or no domain slot. Deleted touched nodes are skipped.
func (d *domainConstraint) affectedAnchors(g GraphReader, targetTag NodeID, touched map[NodeID]struct{}) ([]NodeID, error) {
	anchors := make(map[NodeID]struct{})

	for node := range touched {
		exists, err := g.NodeExists(node)
		if err != nil {
			return nil, wrapInterfaceErr(err)
		}
		if !exists {
			continue
		}

		isDomainSlot, err := d.domainSlots.IsPointer(g, node)
		if err != nil {
			return nil, err
		}

		isTargetHolder, err := g.HasRelationship(targetTag, node)
		if err != nil {
			return nil, wrapInterfaceErr(err)
		}

		if isDomainSlot || isTargetHolder {
			if slotErr := d.addSlotOwners(g, node, targetTag, anchors); slotErr != nil {
				return nil, slotErr
			}
		}

		known, err := operandCarriesKnownSetTag(g, d.sets, d.composites, d.logs, node)
		if err != nil {
			return nil, err
		}
		if known {
			if expandErr := d.addAnchorsExpanding(g, targetTag, node, anchors); expandErr != nil {
				return nil, expandErr
			}
		}

		isDescriptor, err := d.isOperandDescriptor(g, node)
		if err != nil {
			return nil, err
		}
		if !isDescriptor {
			continue
		}

		owners, ownersErr := d.reverseLookup().descriptorOwners(g, node)
		if ownersErr != nil {
			return nil, ownersErr
		}

		for _, owner := range owners {
			if expandErr := d.addAnchorsExpanding(g, targetTag, owner, anchors); expandErr != nil {
				return nil, expandErr
			}
		}
	}

	return sortedNodeSet(anchors), nil
}

// addAnchorsExpanding adds to anchors the owners of every domain slot
// referencing node or any composite/log that transitively expands it:
// every pointer whose domain could have changed membership because node's
// own definition or membership changed.
func (d *domainConstraint) addAnchorsExpanding(g GraphReader, targetTag, node NodeID, anchors map[NodeID]struct{}) error {
	containers, err := d.reverseLookup().transitiveSetContainers(g, node)
	if err != nil {
		return err
	}

	for _, candidate := range append([]NodeID{node}, containers...) {
		slots, slotsErr := d.domainSlotsOf(g, candidate)
		if slotsErr != nil {
			return slotsErr
		}

		for _, slot := range slots {
			if ownersErr := d.addSlotOwners(g, slot, targetTag, anchors); ownersErr != nil {
				return ownersErr
			}
		}
	}

	return nil
}

// isOperandDescriptor reports whether node carries any operand-descriptor
// axis tag (theorystate.md section 80).
func (d *domainConstraint) isOperandDescriptor(g GraphReader, node NodeID) (bool, error) {
	for _, tag := range d.composites.operandAxisTags() {
		has, err := g.HasRelationship(tag, node)
		if err != nil {
			return false, wrapInterfaceErr(err)
		}
		if has {
			return true, nil
		}
	}

	return false, nil
}

// addSlotOwners adds every candidate owner (parent) of slot to anchors.
//
// Two kinds of parent are skipped. The tag hubs (AllDomainSlot,
// targetTag) would make the later forward lookup O(every slot). And any
// parent that itself has the AllDomainSlot hub as a child is a universal
// parent -- ROOT under a RootGraph, which is a virtual parent of every
// node (theorystate.md section 12a) -- never a real anchor; treating it
// as one would make its forward lookups see every slot in the graph as
// its own and misreport ambiguity or pair unrelated pointers' targets and
// domains.
func (d *domainConstraint) addSlotOwners(g GraphReader, slot, targetTag NodeID, anchors map[NodeID]struct{}) error {
	incoming, err := g.FindIncoming(slot)
	if err != nil {
		return wrapInterfaceErr(err)
	}

	for _, rel := range incoming {
		owner := rel.From
		if owner == d.domainSlots.allPointers || owner == targetTag {
			continue
		}

		isUniversalParent, hasErr := g.HasRelationship(owner, d.domainSlots.allPointers)
		if hasErr != nil {
			return wrapInterfaceErr(hasErr)
		}
		if isUniversalParent {
			continue
		}

		anchors[owner] = struct{}{}
	}

	return nil
}

// reverseLookup returns d's reverse lookups over composites and logs.
func (d *domainConstraint) reverseLookup() setReverseLookup {
	return setReverseLookup{composites: d.composites, logs: d.logs}
}

// domainSlotsOf returns every domain slot whose target is domain: a
// reverse lookup over FindIncoming(domain), filtered by the AllDomainSlot
// tag.
func (d *domainConstraint) domainSlotsOf(g GraphReader, domain NodeID) ([]NodeID, error) {
	incoming, err := g.FindIncoming(domain)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}

	slots := make([]NodeID, 0, len(incoming))
	for _, rel := range incoming {
		isSlot, isSlotErr := d.domainSlots.IsPointer(g, rel.From)
		if isSlotErr != nil {
			return nil, isSlotErr
		}
		if isSlot {
			slots = append(slots, rel.From)
		}
	}

	return slots, nil
}

// DomainPointerRegistryB adds domain-constrained target enforcement on
// top of an existing Representation B PointerRegistry instance
// (theorystate.md section 10b), attaching the domain slot directly to
// the anchor node P:
//
//	P -> U             (allPointers' own tag, U)   -- existing target slot
//	P -> U3            (AllDomainSlot, U3)         -- new domain slot
//	U3 -> domainNode
//
// pointers must be a genuine Representation B instance -- i.e.
// constructed with an intermediary-node tag such as AllSubPointers,
// never AllPointers itself (Representation A). This type cannot detect
// that distinction from the tag alone, since PointerRegistry is
// deliberately representation-agnostic (theorystate.md section 76);
// wrapping a Representation A instance here is a caller error this type
// has no way to reject, and would silently corrupt that pointer's own
// "at most one target" invariant the first time a domain slot was
// attached, per theorystate.md section 10c's explanation of why
// Representation A cannot safely carry one.
//
// U itself, not P, is where PointerRegistry's own target-cardinality
// invariant is enforced, so P is free to carry the additional domain
// slot without disturbing it -- U is discovered generically from P via
// the underlying PointerRegistry's own tag (see the subPointer method),
// the same tag-based child lookup used throughout this file, rather than
// requiring callers to separately track and pass U alongside P.
//
// Domain-membership is enforced both at write time (SetTarget/SetDomain
// below) and at commit time, by the same shared Checker Representation D
// uses (domainConstraint.registerChecker). B's anchor P carries no
// self-identifying tag -- P may be any caller-managed node -- so the
// Checker does not try to reverse-discover P by an untagged single-parent
// lookup. It enumerates the parents of a touched sub-pointer or
// domain-slot node as candidates and lets the forward, tag-based
// lookups (subPointer, domainSlotFor) accept or reject each: a parent
// that is not an anchor simply has no such child and is skipped, exactly
// as findUniqueTaggedParent tolerates unrelated parents elsewhere in this
// file. No new tag is needed. The Checker also catches a domain node's
// own membership changing later (theorystate.md section 86). Raw,
// non-Transact Graph mutations still bypass every Checker, as documented
// on the Checker type.
type DomainPointerRegistryB struct {
	domainConstraint
	pointers *PointerRegistry
}

// NewDomainPointerRegistryB creates a DomainPointerRegistryB over graph,
// domain-constraining targets set through pointers (a pre-constructed
// Representation B PointerRegistry -- see the type's doc comment for why
// Representation A must never be passed here). domainSlots is a
// pre-constructed PointerRegistry for the shared AllDomainSlot tag (see
// domainConstraint's doc comment for why it should be constructed once
// and shared with any DomainPointerRegistryD in the same graph). logs
// may be nil if no CompositeSetLogRegistry exists yet in the calling
// program (see operandCarriesKnownSetTag's identical nil-tolerance) --
// a domain pointed at a CompositeSetLog-kind node is then rejected via
// ErrInvalidSetOperand exactly like any other unrecognized domain kind,
// until logs is available.
//
// This also registers the shared commit-time domain Checker (see
// domainConstraint.registerChecker), keyed on pointers' own tag.
func NewDomainPointerRegistryB(graph GraphAPI, pointers, domainSlots *PointerRegistry, sets *SetRegistry, composites *CompositeSetRegistry, logs *CompositeSetLogRegistry) *DomainPointerRegistryB {
	b := &DomainPointerRegistryB{
		domainConstraint: domainConstraint{
			domainSlots: domainSlots,
			sets:        sets,
			composites:  composites,
			logs:        logs,
		},
		pointers: pointers,
	}

	b.registerChecker(
		graph,
		fmt.Sprintf("DomainPointerRegistryB(tag=%d)", domainSlots.allPointers),
		pointers.allPointers,
		b.Target,
	)

	return b
}

// subPointer returns anchor's Representation B sub-pointer node U -- the
// single child of anchor tagged via the underlying PointerRegistry's own
// tag -- found by tag, not by position, exactly like every other slot
// lookup in this file.
func (b *DomainPointerRegistryB) subPointer(graph GraphReader, anchor NodeID) (u NodeID, found bool, err error) {
	return findUniqueTaggedChild(graph, anchor, b.pointers.allPointers)
}

// NewDomainPointer mints a fresh sub-pointer node U, tags it via the
// underlying PointerRegistry's own tag, and wires (anchor, U) --
// completing Representation B's structural pattern for
// DomainPointerRegistryB's own use, so callers do not need to separately
// call the underlying PointerRegistry.NewPointer and wire the edge
// themselves. anchor must already exist; it need not be otherwise
// tagged in any particular way, consistent with Representation B leaving
// P's other direct children unconstrained (theorystate.md section 10b).
//
// Calling this more than once for the same anchor is an idempotent
// no-op: if anchor already has a discoverable sub-pointer node (see
// subPointer), NewDomainPointer leaves it untouched and returns nil
// rather than minting a second one. This closes a real gap found on
// review, not merely a hypothetical one -- without this check, a second
// call gave anchor two children both tagged via the underlying
// PointerRegistry's own tag, which made every subsequent subPointer
// lookup (and therefore Target/SetTarget/RemoveTarget) fail with
// ErrAmbiguousPointerMetadata from then on. This matches the
// idempotency discipline already followed by
// PointerRegistry.TagAsPointer and NameRegistry.EnsureNamedNode
// elsewhere in this file.
//
// The existence check and the creation run inside one Graph.Transact
// call, so concurrent callers under GraphActor cannot both conclude "no
// sub-pointer yet" and each mint one.
func (b *DomainPointerRegistryB) NewDomainPointer(graph Transactor, anchor NodeID) error {
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		if existsErr := requireExists(tx, anchor); existsErr != nil {
			return existsErr
		}

		_, found, err := b.subPointer(tx, anchor)
		if err != nil {
			return err
		}
		if found {
			return nil
		}

		u, err := newPointerTx(tx, b.pointers.allPointers)
		if err != nil {
			return err
		}

		return addRelationshipTx(tx, anchor, u)
	}))
}

// Target returns anchor's current target via its sub-pointer node U, if
// any. hasTarget is false both when anchor has no discoverable U at all
// and when U exists but has no target set yet.
func (b *DomainPointerRegistryB) Target(graph GraphReader, anchor NodeID) (target NodeID, hasTarget bool, err error) {
	u, found, err := b.subPointer(graph, anchor)
	if err != nil || !found {
		return 0, false, err
	}

	return b.pointers.Target(graph, u)
}

// SetTarget sets anchor's target to target, first validating target
// against anchor's currently attached domain, if any (see
// domainConstraint.checkAllowed). anchor must already have a
// discoverable sub-pointer node U (see NewDomainPointer); otherwise this
// returns ErrNotPointer, mirroring the underlying PointerRegistry's own
// error for an untagged node.
func (b *DomainPointerRegistryB) SetTarget(graph Transactor, anchor, target NodeID) error {
	// The domain check and the write see the same state, so a domain
	// change cannot slip in between.
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		if existsErr := requireExists(tx, target); existsErr != nil {
			return existsErr
		}

		if allowedErr := b.checkAllowed(tx, anchor, target); allowedErr != nil {
			return allowedErr
		}

		u, found, err := b.subPointer(tx, anchor)
		if err != nil {
			return err
		}
		if !found {
			return ErrNotPointer
		}

		return b.pointers.SetTarget(tx, u, target)
	}))
}

// RemoveTarget clears anchor's target, if any, via its sub-pointer node
// U.
func (b *DomainPointerRegistryB) RemoveTarget(graph Transactor, anchor NodeID) (removed bool, err error) {
	return transactBool(graph, func(tx Tx) (bool, error) {
		u, found, subErr := b.subPointer(tx, anchor)
		if subErr != nil || !found {
			return false, subErr
		}

		return b.pointers.RemoveTarget(tx, u)
	})
}

// SetDomain sets anchor's domain to domain, additionally validating that
// anchor's current target (if any) still belongs to domain before
// committing -- symmetric with SetTarget's own validation against the
// current domain. See domainConstraint.SetDomain for the shared
// creation/validation logic this delegates to.
func (b *DomainPointerRegistryB) SetDomain(graph Transactor, anchor, domain NodeID) error {
	// Reading the current target, validating it against the new domain
	// and writing the domain are one atomic step.
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		target, hasTarget, err := b.Target(tx, anchor)
		if err != nil {
			return err
		}

		if hasTarget {
			if memberErr := b.validateMembership(tx, domain, target); memberErr != nil {
				return memberErr
			}
		}

		return b.attachDomain(tx, anchor, domain)
	}))
}

// DomainPointerRegistryD adds domain-constrained target enforcement on
// top of an existing PointerMetadataRegistryD instance (Representation
// D, theorystate.md section 10a), attaching the domain slot to the
// metadata node M as a third, independently-tagged sibling of the
// subject-slot and target-slot:
//
//	M -> U1            (AllPointerMetadataSubjectSlot, U1) -> subject
//	M -> U2            (AllPointerMetadataTargetSlot, U2)  -> target
//	M -> U3            (AllDomainSlot, U3)                 -> domainNode
//
// This is safe for exactly the reason theorystate.md section 10c gives
// for Representation D generally: M's subject and target are both
// discovered entirely by tag, with no exclusion list at all, so M
// remains free to carry any number of additional tagged children --
// including U3 -- without disturbing either discovery.
//
// Like DomainPointerRegistryB, this type registers the shared
// commit-time domain Checker (see domainConstraint.registerChecker) in
// addition to write-time enforcement in SetTarget/SetDomain below. Here
// the anchors are metadata nodes M, and a target is read via
// PointerMetadataRegistryD.targetOfMetadata. The Checker also catches a
// domain node's own membership changing later (theorystate.md
// section 86).
type DomainPointerRegistryD struct {
	domainConstraint
	metadata *PointerMetadataRegistryD
}

// NewDomainPointerRegistryD creates a DomainPointerRegistryD over graph,
// domain-constraining targets set through metadata (a pre-constructed
// PointerMetadataRegistryD). domainSlots, sets, composites, and logs are
// exactly as for NewDomainPointerRegistryB -- domainSlots in particular
// should be the same shared instance passed there, if both exist in the
// same graph.
//
// This additionally registers the shared commit-time domain Checker (see
// domainConstraint.registerChecker), keyed on metadata's own target-slot
// tag. It catches a caller bypassing this type and mutating the
// underlying PointerMetadataRegistryD or the shared domainSlots registry
// directly, and a domain node's own membership changing later
// (theorystate.md section 86).
func NewDomainPointerRegistryD(graph GraphAPI, metadata *PointerMetadataRegistryD, domainSlots *PointerRegistry, sets *SetRegistry, composites *CompositeSetRegistry, logs *CompositeSetLogRegistry) *DomainPointerRegistryD {
	d := &DomainPointerRegistryD{
		domainConstraint: domainConstraint{
			domainSlots: domainSlots,
			sets:        sets,
			composites:  composites,
			logs:        logs,
		},
		metadata: metadata,
	}

	d.registerChecker(
		graph,
		fmt.Sprintf("DomainPointerRegistryD(tag=%d)", domainSlots.allPointers),
		metadata.allTargetSlots,
		metadata.targetOfMetadata,
	)

	return d
}

// Target returns subject's current target, delegating directly to the
// underlying PointerMetadataRegistryD.
func (d *DomainPointerRegistryD) Target(graph GraphReader, subject NodeID) (target NodeID, hasTarget bool, err error) {
	return d.metadata.Target(graph, subject)
}

// SetTarget sets subject's target to target, first validating target
// against subject's currently attached domain, if any (see
// domainConstraint.checkAllowed), before delegating to the underlying
// PointerMetadataRegistryD.SetTarget.
//
// A subject with no metadata node at all yet cannot possibly have a
// domain attached, so this skips the domain check entirely in that case
// rather than forcing metadata into existence merely to discover there
// is nothing to check -- exactly the same "read-only, don't create"
// discipline PointerMetadataRegistryD.Target itself already follows.
func (d *DomainPointerRegistryD) SetTarget(graph Transactor, subject, target NodeID) error {
	// The domain check and the write see the same state.
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		if existsErr := requireExists(tx, target); existsErr != nil {
			return existsErr
		}

		m, _, found, err := d.metadata.locate(tx, subject)
		if err != nil {
			return err
		}
		if found {
			if allowedErr := d.checkAllowed(tx, m, target); allowedErr != nil {
				return allowedErr
			}
		}

		return d.metadata.SetTarget(tx, subject, target)
	}))
}

// RemoveTarget clears subject's target, if any, delegating directly to
// the underlying PointerMetadataRegistryD.
func (d *DomainPointerRegistryD) RemoveTarget(graph Transactor, subject NodeID) (removed bool, err error) {
	return d.metadata.RemoveTarget(graph, subject)
}

// Domain returns subject's current domain node, if any, resolving
// subject's metadata node M first via the underlying
// PointerMetadataRegistryD's own subject-side lookup. hasDomain is false
// if subject has no metadata node at all yet, in addition to
// domainConstraint.Domain's own "no domain slot" and "no domain set"
// cases.
func (d *DomainPointerRegistryD) Domain(graph GraphReader, subject NodeID) (domain NodeID, hasDomain bool, err error) {
	m, _, found, err := d.metadata.locate(graph, subject)
	if err != nil || !found {
		return 0, false, err
	}

	return d.domainConstraint.Domain(graph, m)
}

// SetDomain sets subject's domain to domain, creating subject's metadata
// node first if it does not exist yet, and additionally validating that
// subject's current target (if any) still belongs to domain before
// committing -- symmetric with SetTarget's own validation against the
// current domain.
func (d *DomainPointerRegistryD) SetDomain(graph Transactor, subject, domain NodeID) error {
	// Creating subject's metadata, validating its current target and
	// writing the domain are one atomic step, so a rejected SetDomain
	// leaves no metadata behind.
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		m, _, err := d.metadata.ensureMetadataTx(tx, subject)
		if err != nil {
			return err
		}

		target, hasTarget, err := d.metadata.targetOfMetadata(tx, m)
		if err != nil {
			return err
		}

		if hasTarget {
			if memberErr := d.validateMembership(tx, domain, target); memberErr != nil {
				return memberErr
			}
		}

		return d.attachDomain(tx, m, domain)
	}))
}

// RemoveDomain clears subject's domain, if any, resolving subject's
// metadata node M first. removed is false if subject has no metadata
// node at all yet, in addition to domainConstraint.RemoveDomain's own
// "no domain slot" case.
func (d *DomainPointerRegistryD) RemoveDomain(graph Transactor, subject NodeID) (removed bool, err error) {
	return transactBool(graph, func(tx Tx) (bool, error) {
		m, _, found, locateErr := d.metadata.locate(tx, subject)
		if locateErr != nil || !found {
			return false, locateErr
		}

		return d.domainConstraint.RemoveDomain(tx, m)
	})
}
