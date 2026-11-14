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

// locateBySubjectSlot finds node's metadata node and subject-slot node
// via the two-hop subject -> subject-slot -> metadata reverse lookup
// shared by both PointerMetadataRegistry (Representation C) and
// PointerMetadataRegistryD (Representation D): both representations
// identify the subject the same way, differing only in how they then
// locate the target. node must exist.
func locateBySubjectSlot(g GraphReader, node, allPointerMetadata, allSubjectSlots NodeID) (metadata, subjectSlot NodeID, found bool, err error) {
	subjectSlot, found, err = findUniqueTaggedParent(g, node, allSubjectSlots)
	if err != nil || !found {
		return 0, 0, found, err
	}

	metadata, found, err = findUniqueTaggedParent(g, subjectSlot, allPointerMetadata)
	if err != nil || !found {
		return 0, 0, found, err
	}

	return metadata, subjectSlot, true, nil
}

// ensureMetadataWithSubjectSlotTx returns subject's existing metadata/
// subject-slot pair (via locateBySubjectSlot), creating a fresh, empty
// one (M -> S -> subject, both tagged) if none exists yet, entirely
// against tx. The lookup and the creation happen in the same transaction,
// so two goroutines cannot both conclude "not found" and both create a
// pair (which would make every later lookup fail with
// ErrAmbiguousPointerMetadata). Shared by both PointerMetadataRegistry
// and PointerMetadataRegistryD, which build identical subject-side
// structure and differ only in how the target side is represented.
// Callers are responsible for checking that subject itself exists.
func ensureMetadataWithSubjectSlotTx(tx txReader, subject, allPointerMetadata, allSubjectSlots NodeID) (metadata, subjectSlot NodeID, err error) {
	var found bool
	metadata, subjectSlot, found, err = locateBySubjectSlot(tx, subject, allPointerMetadata, allSubjectSlots)
	if err != nil {
		return 0, 0, err
	}
	if found {
		return metadata, subjectSlot, nil
	}

	subjectSlot, err = createTaggedNodeTx(tx, allSubjectSlots)
	if err != nil {
		return 0, 0, err
	}
	if linkErr := addRelationshipTx(tx, subjectSlot, subject); linkErr != nil {
		return 0, 0, linkErr
	}

	metadata, err = createTaggedNodeTx(tx, allPointerMetadata)
	if err != nil {
		return 0, 0, err
	}
	if linkErr := addRelationshipTx(tx, metadata, subjectSlot); linkErr != nil {
		return 0, 0, linkErr
	}

	return metadata, subjectSlot, nil
}

// subjectMetadataBase holds the shared tag state and subject-side
// operations -- locate, ensureMetadata, EnsureMetadata, HasMetadata --
// shared identically by PointerMetadataRegistry (Representation C) and
// PointerMetadataRegistryD (Representation D). Both representations
// locate or create a subject's metadata node and subject-slot node in
// exactly the same way (via locateBySubjectSlot /
// ensureMetadataWithSubjectSlot); they differ only in how they then find
// the target, which is why only the shared subject-side logic is
// factored out here rather than merging the two types outright.
//
// PointerMetadataRegistry and PointerMetadataRegistryD each embed this
// struct anonymously, so its fields (allPointerMetadata, allSubjectSlots)
// and methods are promoted and usable exactly as if they were declared
// directly on the embedding type.
//
// Like every other registry in this file, subjectMetadataBase stores no
// graph reference of its own (theorystate.md section 90): every method
// below takes the graph it should operate against as an explicit
// parameter instead.
type subjectMetadataBase struct {
	allPointerMetadata NodeID
	allSubjectSlots    NodeID
}

// locate finds subject's metadata node and subject-slot node, if any.
// found is false if subject has no metadata yet. subject must exist.
func (b *subjectMetadataBase) locate(graph GraphReader, subject NodeID) (metadata, subjectSlot NodeID, found bool, err error) {
	return locateBySubjectSlot(graph, subject, b.allPointerMetadata, b.allSubjectSlots)
}

// ensureMetadataTx returns subject's existing metadata/subject-slot pair,
// creating a fresh, empty one (M -> S -> subject, both tagged) if none
// exists yet, against tx. The registries' SetTarget/SetDomain cores call
// this so that creating the metadata and setting the target are one
// atomic step: a later failure rolls the metadata creation back too.
func (b *subjectMetadataBase) ensureMetadataTx(tx txReader, subject NodeID) (metadata, subjectSlot NodeID, err error) {
	if existsErr := requireExists(tx, subject); existsErr != nil {
		return 0, 0, existsErr
	}

	return ensureMetadataWithSubjectSlotTx(tx, subject, b.allPointerMetadata, b.allSubjectSlots)
}

// EnsureMetadata returns subject's metadata node, creating an empty one
// if none exists yet.
func (b *subjectMetadataBase) EnsureMetadata(graph Transactor, subject NodeID) (NodeID, error) {
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		metadata, _, err := b.ensureMetadataTx(tx, subject)
		return metadata, err
	})
}

// HasMetadata reports whether subject currently has an associated
// metadata node, regardless of whether a target has been set.
func (b *subjectMetadataBase) HasMetadata(graph GraphReader, subject NodeID) (bool, error) {
	if existsErr := requireExists(graph, subject); existsErr != nil {
		return false, existsErr
	}

	_, _, found, err := b.locate(graph, subject)
	return found, err
}

// PointerMetadataRegistry implements Representation C (metadata
// structure) of the Pointer processor, theorystate.md section 10's
// generalized metadata construction (see also section 10b).
//
// Unlike PointerRegistry (Representations A and B), Representation C
// keeps the subject node's own direct children completely untouched by
// the pointer representation. Instead, a dedicated metadata node M
// records the association:
//
//	(AllPointerMetadata, M)
//	M -> S                                 M's subject-slot child
//	(AllPointerMetadataSubjectSlot, S)
//	S -> subject                           S identifies the actual subject
//	M -> target                            M's target child, if any
//
// Design note -- why the subject needs its own slot node S rather than M
// pointing directly at the subject (M -> subject): a naive two-edge
// scheme (M -> subject, M -> target) cannot represent target == subject.
// Because primitive relationships are unique pairs
// (theorystate.md section 2.4/2.6), "M -> subject" and
// "M -> target" would collapse into the identical single physical
// relationship whenever target == subject, making self-targeting
// indistinguishable from an empty target. A freshly-minted S node for the
// subject-slot (the role/occurrence-identity pattern named in
// theorystate.md section 75 -- the same move as ElementCapsule nodes
// in the Ordered List
// design) means M's two children -- S and the target -- can never
// collide, since S is never equal to any subject value.
//
// Given a subject, the metadata node is discovered via a two-hop reverse
// lookup: find S among subject's parents tagged
// AllPointerMetadataSubjectSlot, then find M among S's parents tagged
// AllPointerMetadata (see findUniqueTaggedParent). This relies on
// FindIncoming being indexed; it is not a full-graph scan.
//
// As with PointerRegistry, every method re-derives current state fresh
// from the Graph on every call rather than caching it, and fails loudly
// (ErrAmbiguousPointerMetadata, ErrTooManyPointerTargets) rather than
// silently repairing when an out-of-band mutation has violated an
// invariant.
//
// Known limitation, kept deliberately rather than fixed here: M's target
// is identified *by exclusion* -- "whichever of M's children isn't S must
// be the target" -- via singleChildTarget(m.graph, metadata, slot). This
// means M is implicitly assumed to have only these two children, ever;
// any future unrelated child added to M (tagged or not) would make
// target discovery fail loudly with ErrTooManyPointerTargets even though
// nothing about the actual target changed. This is really the same
// mistake as Representation A's "at most one child, period" -- just
// shifted up one level -- and it also matches
// theorystate.md section 10a's discussion of the original "M -> P, M
// -> I" sketch, which has an even sharper version of the same bug (those
// two relationships collapse into one whenever target == subject, since
// primitive relationships are unique pairs). PointerMetadataRegistryD
// (Representation D) is the corrected construction -- see its doc
// comment. This type is kept as-is, limitation and all, rather than
// patched or deleted: it is useful as a deliberately stricter
// representation for testing how higher-level code should react when a
// lower layer refuses something Representation D would allow
// (theorystate.md section 73).
//
// It is deliberately NOT part of Registries, and not meant for use: any extra
// child of M breaks target discovery, so M can carry nothing else (no domain
// slot, section 10c). It has a Checker like every other registry, keyed on
// allPointerMetadata, and it shares that tag and the subject-slot tag with
// PointerMetadataRegistryD. Constructing both over the same tag nodes in one
// graph is therefore a mistake: this Checker would fire on D's metadata nodes
// and decline valid commits, since a D node with a target slot and a domain
// slot has two non-subject-slot children. The tests construct it only in its
// own fixtures.
type PointerMetadataRegistry struct {
	subjectMetadataBase
}

// NewPointerMetadataRegistry creates a PointerMetadataRegistry over
// graph, using allPointerMetadata to tag metadata nodes and
// allSubjectSlots to tag subject-slot nodes. Both must already exist --
// typically via NameRegistry.BootstrapNames(FoundationalNames).
//
// locate, ensureMetadata, EnsureMetadata, and HasMetadata are inherited
// unmodified from the embedded subjectMetadataBase, which is shared with
// PointerMetadataRegistryD -- see subjectMetadataBase's doc comment for
// why this subject-side logic is factored out rather than duplicated.
func NewPointerMetadataRegistry(graph GraphAPI, allPointerMetadata, allSubjectSlots NodeID) (*PointerMetadataRegistry, error) {
	if existsErr := requireAllExist(graph, allPointerMetadata, allSubjectSlots); existsErr != nil {
		return nil, existsErr
	}

	// Registers a Checker (see Graph.RegisterChecker) enforcing this
	// representation's own "at most one target, excluding the
	// subject-slot" invariant at commit time, mirroring
	// NewPointerRegistry's identical eager/lazy pairing -- see that
	// constructor's doc comment for the full reasoning. A node touched
	// by a transaction that has no discoverable subject-slot at all is
	// skipped rather than treated as a violation: that shape can only
	// arise from a separate out-of-band mutation removing the
	// subject-slot after the fact (ensureMetadataWithSubjectSlot always
	// creates M and its subject-slot together), and is not this
	// Checker's invariant to enforce -- it exists to catch a violated
	// target count, which cannot even be evaluated without first
	// knowing which child to exclude as the subject-slot.
	graph.RegisterChecker(Checker{
		Name: fmt.Sprintf("PointerMetadataRegistry(tag=%d)", allPointerMetadata),
		Tags: []NodeID{allPointerMetadata},
		Check: func(g GraphReader, touched map[NodeID]struct{}) error {
			for node := range touched {
				tagged, taggedErr := g.HasRelationship(allPointerMetadata, node)
				if taggedErr != nil {
					return wrapInterfaceErr(taggedErr)
				}
				if !tagged {
					continue
				}

				slot, found, err := findUniqueTaggedChild(g, node, allSubjectSlots)
				if err != nil {
					return err
				}
				if !found {
					continue
				}

				if _, _, targetErr := singleChildTarget(g, node, slot); targetErr != nil {
					return targetErr
				}
			}

			return nil
		},
	})

	return &PointerMetadataRegistry{
		subjectMetadataBase: subjectMetadataBase{
			allPointerMetadata: allPointerMetadata,
			allSubjectSlots:    allSubjectSlots,
		},
	}, nil
}

// Target returns subject's current target via its metadata node, if any.
//
// hasTarget is false both when subject has no metadata node at all and
// when it has one with no target set yet -- callers that need to
// distinguish those two cases should use HasMetadata first.
func (m *PointerMetadataRegistry) Target(graph GraphReader, subject NodeID) (target NodeID, hasTarget bool, err error) {
	if existsErr := requireExists(graph, subject); existsErr != nil {
		return 0, false, existsErr
	}

	metadata, slot, found, err := m.locate(graph, subject)
	if err != nil {
		return 0, false, err
	}
	if !found {
		return 0, false, nil
	}

	return singleChildTarget(graph, metadata, slot)
}

// SetTarget sets subject's target to target, creating subject's metadata
// node first if it does not exist yet (see EnsureMetadata).
//
// Self-targeting (SetTarget(subject, subject)) is explicitly supported
// and correctly distinguished from an empty target -- see the
// PointerMetadataRegistry doc comment for why the subject-slot
// indirection is what makes this possible.
func (m *PointerMetadataRegistry) SetTarget(graph Transactor, subject, target NodeID) error {
	// Creating the metadata (if needed), reading the current target and
	// replacing it are one atomic step.
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		if existsErr := requireExists(tx, target); existsErr != nil {
			return existsErr
		}

		metadata, slot, err := m.ensureMetadataTx(tx, subject)
		if err != nil {
			return err
		}

		return singleChildTargetSetTx(tx, tx, metadata, target, slot)
	}))
}

// RemoveTarget clears subject's target, if any. The metadata/slot nodes
// themselves are left in place (no cascade deletion, consistent with
// theorystate.md section 18's rejection of
// deleteNodeAndRelationships); an empty metadata node is a valid,
// meaningful state, exactly like an empty Pointer in Representation A.
func (m *PointerMetadataRegistry) RemoveTarget(graph Transactor, subject NodeID) (removed bool, err error) {
	return transactBool(graph, func(tx Tx) (bool, error) {
		if existsErr := requireExists(tx, subject); existsErr != nil {
			return false, existsErr
		}

		metadata, slot, found, locateErr := m.locate(tx, subject)
		if locateErr != nil {
			return false, locateErr
		}
		if !found {
			return false, nil
		}

		return singleChildTargetRemoveTx(tx, tx, metadata, slot)
	})
}

// PointerMetadataRegistryD implements Representation D, a corrected
// generalization of Representation C (PointerMetadataRegistry) /
// theorystate.md section 10a's original "M -> P, M -> I" sketch.
//
// Representation C and that original sketch share a real bug: they each
// identify one of M's two children *by exclusion* -- "whichever
// child isn't the subject/subject-slot must be the target/information
// node" (or, in the even earlier sketch, "M -> P" and "M -> I"
// collapse into the same relationship whenever target == subject, since
// primitive relationships are unique pairs -- theorystate.md section
// 2.4/2.6). Both are really the same underlying mistake as Representation
// A's "at most one child, no room for anything else": M is implicitly
// assumed to have *exactly* the relevant children and nothing more, which
// directly contradicts section 10a's own stated goal ("This is a general
// construction, not merely a pointer trick" -- i.e. M should be free to
// grow additional, unrelated children later without breaking discovery).
//
// Representation D fixes this by giving *both* roles their own dedicated,
// freshly-minted, explicitly tagged slot node, exactly symmetric with
// each other:
//
//	(AllPointerMetadata, M)
//	M -> U1                                  M's subject-slot child
//	(AllPointerMetadataSubjectSlot, U1)
//	U1 -> subject                            U1 identifies the subject
//	M -> U2                                  M's target-slot child (once set)
//	(AllPointerMetadataTargetSlot, U2)
//	U2 -> target                             U2 identifies the target
//
// Both U1 and U2 are discovered by their own tag, not by exclusion, so M
// can carry any number of additional, unrelated children -- tagged or
// not, now or added later -- without ever disturbing subject or target
// discovery. This is the same occurrence/role-identity pattern already
// named in theorystate.md section 75, applied twice over instead of
// once.
//
// Representation C (PointerMetadataRegistry) is kept, not deleted, even
// though Representation D supersedes it as the *correct* general
// construction: C's exclusion-based limitation is now understood and
// named rather than accidental, and deliberately keeping the more
// restrictive representation available is useful for testing how
// higher-level code should react when a lower layer is stricter than
// necessary (theorystate.md section 73's commit-time interception
// question -- should such a restriction be enforced, ignored, or merely
// reported?). Do not add further logic to C to "fix" it; add it here to
// D instead.
//
// As with every other registry in this file, every method here
// re-derives current state fresh from the Graph on every call, and fails
// loudly (ErrAmbiguousPointerMetadata, ErrTooManyPointerTargets) rather
// than silently repairing an out-of-band invariant violation.
type PointerMetadataRegistryD struct {
	subjectMetadataBase
	allTargetSlots NodeID
}

// NewPointerMetadataRegistryD creates a PointerMetadataRegistryD over
// graph, using allPointerMetadata to tag metadata nodes, allSubjectSlots
// to tag subject-slot nodes, and allTargetSlots to tag target-slot nodes.
// All three must already exist -- typically via
// NameRegistry.BootstrapNames(FoundationalNames).
//
// locate, ensureMetadata, EnsureMetadata, and HasMetadata are inherited
// unmodified from the embedded subjectMetadataBase, which is shared with
// PointerMetadataRegistry -- see subjectMetadataBase's doc comment for
// why this subject-side logic is factored out rather than duplicated.
func NewPointerMetadataRegistryD(graph GraphAPI, allPointerMetadata, allSubjectSlots, allTargetSlots NodeID) (*PointerMetadataRegistryD, error) {
	if existsErr := requireAllExist(graph, allPointerMetadata, allSubjectSlots, allTargetSlots); existsErr != nil {
		return nil, existsErr
	}

	// Registers a Checker (see Graph.RegisterChecker) enforcing this
	// representation's own "at most one target" invariant at commit
	// time. Unlike Representation C, D's target lives on its own
	// independently-tagged target-slot node (U2), discovered entirely by
	// tag rather than by exclusion, so the invariant to check here is
	// simply "does a node tagged allTargetSlots have at most one child"
	// -- no exclusion set needed, mirroring the reasoning in
	// NewPointerRegistry's identical Checker.
	graph.RegisterChecker(Checker{
		Name: fmt.Sprintf("PointerMetadataRegistryD(tag=%d)", allPointerMetadata),
		Tags: []NodeID{allTargetSlots},
		Check: func(g GraphReader, touched map[NodeID]struct{}) error {
			for node := range touched {
				tagged, taggedErr := g.HasRelationship(allTargetSlots, node)
				if taggedErr != nil {
					return wrapInterfaceErr(taggedErr)
				}
				if !tagged {
					continue
				}

				if _, _, err := singleChildTarget(g, node); err != nil {
					return err
				}
			}

			return nil
		},
	})

	return &PointerMetadataRegistryD{
		subjectMetadataBase: subjectMetadataBase{
			allPointerMetadata: allPointerMetadata,
			allSubjectSlots:    allSubjectSlots,
		},
		allTargetSlots: allTargetSlots,
	}, nil
}

// targetSlot returns metadata's current target-slot child (U2), if any,
// found by tag rather than by exclusion -- see the PointerMetadataRegistryD
// doc comment for why this is the fix over Representation C.
func (m *PointerMetadataRegistryD) targetSlot(graph GraphReader, metadata NodeID) (slot NodeID, found bool, err error) {
	return findUniqueTaggedChild(graph, metadata, m.allTargetSlots)
}

// targetOfMetadata returns the current target recorded on metadata node
// metadata, if any: its target-slot's single child. hasTarget is false
// when metadata has no target-slot yet, or has one with no target set.
// This is Target's tail once the subject has already been resolved to
// its metadata node, and is also what the shared domain Checker uses
// (metadata nodes are its anchors -- see domainConstraint.registerChecker).
func (m *PointerMetadataRegistryD) targetOfMetadata(graph GraphReader, metadata NodeID) (target NodeID, hasTarget bool, err error) {
	slot, found, err := m.targetSlot(graph, metadata)
	if err != nil {
		return 0, false, err
	}
	if !found {
		return 0, false, nil
	}

	return singleChildTarget(graph, slot)
}

// Target returns subject's current target via its metadata/target-slot
// nodes, if any.
//
// hasTarget is false when subject has no metadata node at all, when it
// has one with no target-slot yet, or when it has a target-slot with no
// target set yet -- callers that need to distinguish those cases should
// use HasMetadata and EnsureMetadata directly.
func (m *PointerMetadataRegistryD) Target(graph GraphReader, subject NodeID) (target NodeID, hasTarget bool, err error) {
	if existsErr := requireExists(graph, subject); existsErr != nil {
		return 0, false, existsErr
	}

	metadata, _, found, err := m.locate(graph, subject)
	if err != nil {
		return 0, false, err
	}
	if !found {
		return 0, false, nil
	}

	return m.targetOfMetadata(graph, metadata)
}

// SetTarget sets subject's target to target, creating subject's metadata
// node and/or target-slot node first if they do not exist yet.
//
// Self-targeting (SetTarget(subject, subject)) is supported: U2 (the
// target-slot) is a freshly-minted node distinct from subject, U1, and M,
// so U2 -> target can never collide with any other relationship no
// matter what target equals.
func (m *PointerMetadataRegistryD) SetTarget(graph Transactor, subject, target NodeID) error {
	// Creating the metadata and/or target-slot (if needed), reading the
	// current target and replacing it are one atomic step.
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		if existsErr := requireExists(tx, target); existsErr != nil {
			return existsErr
		}

		metadata, _, err := m.ensureMetadataTx(tx, subject)
		if err != nil {
			return err
		}

		slot, found, err := m.targetSlot(tx, metadata)
		if err != nil {
			return err
		}

		if found {
			return singleChildTargetSetTx(tx, tx, slot, target)
		}

		newSlot, err := createTaggedNodeTx(tx, m.allTargetSlots)
		if err != nil {
			return err
		}
		if linkErr := addRelationshipTx(tx, metadata, newSlot); linkErr != nil {
			return linkErr
		}

		return addRelationshipTx(tx, newSlot, target)
	}))
}

// RemoveTarget clears subject's target, if any. The metadata/subject-
// slot/target-slot nodes themselves are left in place (no cascade
// deletion, consistent with theorystate.md section 18's rejection of
// deleteNodeAndRelationships); an empty target-slot -- or no target-slot
// at all -- is a valid, meaningful state.
func (m *PointerMetadataRegistryD) RemoveTarget(graph Transactor, subject NodeID) (removed bool, err error) {
	return transactBool(graph, func(tx Tx) (bool, error) {
		if existsErr := requireExists(tx, subject); existsErr != nil {
			return false, existsErr
		}

		metadata, _, found, locateErr := m.locate(tx, subject)
		if locateErr != nil {
			return false, locateErr
		}
		if !found {
			return false, nil
		}

		slot, found, slotErr := m.targetSlot(tx, metadata)
		if slotErr != nil {
			return false, slotErr
		}
		if !found {
			return false, nil
		}

		return singleChildTargetRemoveTx(tx, tx, slot)
	})
}
