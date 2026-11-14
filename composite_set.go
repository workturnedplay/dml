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

// CompositeSetRegistry implements the unordered composite Set
// representation of theorystate.md sections 80/81: (AllCompositeSets, C)
// tags C as CompositeSet-kind, and C's direct children are
// operand-descriptor nodes (theorystate.md section 75's occurrence/role-
// identity pattern, applied to composite Set operands) rather than
// members themselves -- unlike a plain Set (SetRegistry), whose direct
// children are its members directly.
//
// Each operand is represented by a freshly minted descriptor node U:
//
//	set -> U -> operand
//
// tagged along two independent, orthogonal axes (theorystate.md
// section 80):
//   - operation kind: (AllAdditiveOp, U) or (AllSubtractiveOp, U) --
//     whether operand contributes to set's evaluated membership via
//     union or via set-difference.
//   - operand kind: (AllScalarOperand, U) or (AllSetOperand, U) --
//     whether operand is used as a single literal member, or expanded
//     via its own current Set-kind membership.
//
// Design note -- why operand kind is always an explicit tag on U, never
// inferred from operand's own tags: an early design draft inferred
// "should this operand be expanded" from whether operand itself happened
// to already be tagged Set-kind. This repeats, one level up, the exact
// mistake theorystate.md section 10a already diagnosed and corrected for
// Pointer subject/target discovery: a node's own identity (what it
// intrinsically is) is a different fact from what a specific relationship
// means it as here. Inferring expansion from operand's own tag would make
// "add a Set object as a literal, unexpanded member of another Set" --
// explicitly permitted by theorystate.md section 9a -- inexpressible for
// composite Sets: a Set-tagged node could then only ever be used as an
// expansion operand, never as a plain scalar member, anywhere. Recording
// the intent explicitly on U, per relationship, avoids this entirely --
// the same node can freely be a literal member in one composite Set and
// an expansion operand in another, or even both within the same one.
//
// Evaluate folds set's operand descriptors per theorystate.md section 81:
//
//	Evaluate(set) = (union of resolved(u) for every additive u)
//	                minus (union of resolved(u) for every subtractive u)
//
// where `resolved(u)` of a scalar-axis u is the singleton {operand}, and of
// a set-axis u is operand's own current evaluated/derived membership,
// recursively resolved through whichever of the currently-implemented Set
// representations operand actually is -- a plain Set (delegated to the
// embedded SetRegistry), another CompositeSet (resolved recursively), or
// a CompositeSetLog (delegated to the logs field, resolved recursively,
// once wired via SetLogs) -- theorystate.md section 83's dispatcher.
//
// Like SetRegistry.Members, Evaluate is deliberately never cached: it is
// recomputed fresh from the Graph on every call, for the same reason
// given in theorystate.md sections 9a/35 -- a cached derived-membership
// view cannot be kept honestly in sync without invalidation machinery
// that does not exist and should not be built ahead of an actual need.
//
// Per theorystate.md section 85, AddOperand always mints a fresh
// descriptor node unconditionally -- no attempt is made to find and reuse
// an existing identical one.
//
// Per theorystate.md section 79, a node may carry at most one of the
// Set-representation tags (AllSets, AllCompositeSets, and
// AllCompositeSetLogs) at a time. NewCompositeSet always mints a fresh
// node, which cannot already carry any other tag, and this registry does
// not (yet) provide a TagAsCompositeSet analogous to SetRegistry.TagAsSet
// for retagging an existing node -- so no path through this registry's
// own API can violate that invariant, unlike SetRegistry.TagAsSet, which
// does check (see its doc comment), since it operates on caller-supplied
// existing nodes. If a TagAsCompositeSet is added later, it must apply
// the same ErrSetRepresentationConflict check.
type CompositeSetRegistry struct {
	sets             *SetRegistry
	logs             *CompositeSetLogRegistry
	allCompositeSets NodeID
	allAdditiveOp    NodeID
	allSubtractiveOp NodeID
	allScalarOperand NodeID
	allSetOperand    NodeID
}

// NewCompositeSetRegistry creates a CompositeSetRegistry over graph.
// sets is used to resolve set-expansion operands that turn out to be
// plain Sets (theorystate.md section 83's dispatcher), and must already
// be constructed over the same graph. allCompositeSets tags
// CompositeSet-kind nodes; allAdditiveOp/allSubtractiveOp tag a
// descriptor's operation-kind axis; allScalarOperand/allSetOperand tag a
// descriptor's operand-kind axis. All five tag NodeIDs must already
// exist -- typically via NameRegistry.BootstrapNames(FoundationalNames).
//
// The returned registry cannot yet resolve CompositeSetLog-kind operands
// (theorystate.md section 82) -- call SetLogs once a
// CompositeSetLogRegistry exists to enable that; see SetLogs's doc
// comment for why this is a required second step rather than a
// constructor parameter.
func NewCompositeSetRegistry(graph GraphAPI, sets *SetRegistry, allCompositeSets, allAdditiveOp, allSubtractiveOp, allScalarOperand, allSetOperand NodeID) (*CompositeSetRegistry, error) {
	if existsErr := requireAllExist(graph, allCompositeSets, allAdditiveOp, allSubtractiveOp, allScalarOperand, allSetOperand); existsErr != nil {
		return nil, existsErr
	}

	// Registers two Checkers (see Graph.RegisterChecker), covering two
	// genuinely different things that can go wrong with a composite Set:
	//
	// The first, keyed on allCompositeSets, walks every current child of
	// a touched composite-set node and validates each as a well-formed
	// operand descriptor -- mirroring exactly what Evaluate() already
	// does defensively on every read (see evaluate's own descriptor loop
	// via resolveOperand/exactlyOneTag). This specifically catches a
	// stray, non-descriptor child added directly to a composite set by
	// some out-of-band mutation, which the second Checker below would
	// not reach, since that stray child would carry none of the axis
	// tags the second Checker keys on.
	//
	// The second, keyed on the four operand-descriptor axis tags
	// themselves (theorystate.md section 80) rather than on
	// allCompositeSets, validates any individually touched node that
	// carries at least one of those tags directly, regardless of which
	// parent structure (if any) it currently belongs to. This is what
	// makes descriptor-shape enforcement genuinely shared with
	// CompositeSetLogRegistry (theorystate.md section 82): that
	// registry's own logged-operation descriptors are list-capsule
	// values, never children of an allCompositeSets-tagged node, so the
	// first Checker's parent-based walk could never reach them -- but
	// since CompositeSetLogRegistry is required to reuse these exact
	// same four axis-tag NodeIDs (see NewCompositeSetLogRegistry), this
	// second Checker fires on its descriptors too, the moment they are
	// touched, with no Checker of CompositeSetLogRegistry's own needed
	// at all.
	graph.RegisterChecker(Checker{
		Name: fmt.Sprintf("CompositeSetRegistry(tag=%d)", allCompositeSets),
		Tags: []NodeID{allCompositeSets},
		Check: func(g GraphReader, touched map[NodeID]struct{}) error {
			for node := range touched {
				tagged, taggedErr := g.HasRelationship(allCompositeSets, node)
				if taggedErr != nil {
					return wrapInterfaceErr(taggedErr)
				}
				if !tagged {
					continue
				}

				outgoing, err := g.FindOutgoing(node)
				if err != nil {
					return wrapInterfaceErr(err)
				}

				for _, rel := range outgoing {
					u := rel.To

					if _, additiveErr := exactlyOneTag(g, u, allAdditiveOp, allSubtractiveOp); additiveErr != nil {
						return additiveErr
					}
					if _, operandErr := exactlyOneTag(g, u, allScalarOperand, allSetOperand); operandErr != nil {
						return operandErr
					}
					if _, targetErr := operandTargetGeneric(g, u); targetErr != nil {
						return targetErr
					}
				}
			}

			return nil
		},
	})

	graph.RegisterChecker(Checker{
		Name: fmt.Sprintf("OperandDescriptor(tags=%d,%d,%d,%d)", allAdditiveOp, allSubtractiveOp, allScalarOperand, allSetOperand),
		Tags: []NodeID{allAdditiveOp, allSubtractiveOp, allScalarOperand, allSetOperand},
		Check: func(g GraphReader, touched map[NodeID]struct{}) error {
			for node := range touched {
				hasAdditive, err := g.HasRelationship(allAdditiveOp, node)
				if err != nil {
					return wrapInterfaceErr(err)
				}
				hasSubtractive, err := g.HasRelationship(allSubtractiveOp, node)
				if err != nil {
					return wrapInterfaceErr(err)
				}
				hasScalar, err := g.HasRelationship(allScalarOperand, node)
				if err != nil {
					return wrapInterfaceErr(err)
				}
				hasSet, err := g.HasRelationship(allSetOperand, node)
				if err != nil {
					return wrapInterfaceErr(err)
				}

				if !hasAdditive && !hasSubtractive && !hasScalar && !hasSet {
					continue
				}

				if _, tagErr := exactlyOneTag(g, node, allAdditiveOp, allSubtractiveOp); tagErr != nil {
					return tagErr
				}
				if _, tagErr := exactlyOneTag(g, node, allScalarOperand, allSetOperand); tagErr != nil {
					return tagErr
				}
				if _, targetErr := operandTargetGeneric(g, node); targetErr != nil {
					return targetErr
				}
			}

			return nil
		},
	})

	return &CompositeSetRegistry{
		sets:             sets,
		allCompositeSets: allCompositeSets,
		allAdditiveOp:    allAdditiveOp,
		allSubtractiveOp: allSubtractiveOp,
		allScalarOperand: allScalarOperand,
		allSetOperand:    allSetOperand,
	}, nil
}

// SetLogs wires this CompositeSetRegistry to logs, letting Evaluate/
// AddOperand recognize and resolve operands that are themselves
// CompositeSetLog-kind (theorystate.md section 82/83). This is separate
// from NewCompositeSetRegistry because CompositeSetLogRegistry itself
// depends on an existing *CompositeSetRegistry (to resolve its own
// CompositeSet-kind operands, and to obtain the shared *SetRegistry it
// dispatches plain-Set operands through -- see
// NewCompositeSetLogRegistry) -- the two representations mutually
// reference each other, and Go cannot construct two such values in a
// single mutually-referential step. Construct in the order
// SetRegistry -> CompositeSetRegistry -> CompositeSetLogRegistry(composites)
// -> composites.SetLogs(that log registry).
//
// Calling this is optional: a CompositeSetRegistry with logs left unset
// (nil) still works for every other operand kind --
// resolveSetOperandGeneric treats a nil logs exactly like "this operand
// doesn't carry a recognized Set-representation tag," surfacing
// ErrInvalidSetOperand rather than panicking. Calling SetLogs again
// simply replaces the previous value; passing nil un-wires it. SetLogs
// writes an unsynchronized field that every operation reads, so it must
// be called during setup, before the registry is shared with other
// goroutines.
func (c *CompositeSetRegistry) SetLogs(logs *CompositeSetLogRegistry) {
	c.logs = logs
}

// IsCompositeSet reports whether id is currently tagged
// (AllCompositeSets, id).
func (c *CompositeSetRegistry) IsCompositeSet(graph GraphReader, id NodeID) (bool, error) {
	has, err := graph.HasRelationship(c.allCompositeSets, id)
	return has, wrapInterfaceErr(err)
}

// requireCompositeSet checks that set exists and is tagged
// (AllCompositeSets, set), returning ErrNodeNotFound or
// ErrNotCompositeSet otherwise. Delegates to the shared requireTagged
// helper (see its doc comment on PointerRegistry.requirePointer).
func (c *CompositeSetRegistry) requireCompositeSet(graph GraphReader, set NodeID) error {
	return requireTagged(graph, set, c.allCompositeSets, ErrNotCompositeSet)
}

// NewCompositeSet creates a fresh NodeID and tags it (AllCompositeSets,
// id). The new composite set starts with no operands, evaluating to the
// empty set.
func (c *CompositeSetRegistry) NewCompositeSet(graph Transactor) (NodeID, error) {
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		return createTaggedNodeTx(tx, c.allCompositeSets)
	})
}

// AddOperand adds an operand to set, represented by a freshly minted
// descriptor node U wired entirely inside one transaction (nested if
// graph is a Tx):
// set -> U -> operand, with U tagged along both axes described in the
// CompositeSetRegistry doc comment.
//
// additive selects the operation-kind axis (true: union / additive,
// false: set-difference / subtractive). expand selects the operand-kind
// axis (true: operand is expanded via its own Set-kind membership,
// false: operand is used as a single literal member).
//
// If expand is true, operand must already carry one of the
// currently-recognized Set-representation tags (AllSets,
// AllCompositeSets, or, if this registry has been wired via SetLogs,
// AllCompositeSetLogs) -- checked here, at write time, before U is
// created at all, via operandCarriesKnownSetTag -- returning
// ErrInvalidSetOperand otherwise. If expand is false, operand may be any
// existing node of any kind.
//
// set must already be tagged (AllCompositeSets, set); operand must
// already exist. Per theorystate.md section 85, no existing identical
// descriptor is searched for or reused -- see the CompositeSetRegistry
// doc comment.
func (c *CompositeSetRegistry) AddOperand(graph Transactor, set, operand NodeID, additive, expand bool) (NodeID, error) {
	// Every check and the descriptor's creation see the same state.
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		if requireErr := c.requireCompositeSet(tx, set); requireErr != nil {
			return 0, requireErr
		}

		if requireErr := requireOperand(tx, c.sets, c, c.logs, operand, expand); requireErr != nil {
			return 0, requireErr
		}

		u, err := buildOperandDescriptorTx(tx, c.allAdditiveOp, c.allSubtractiveOp, c.allScalarOperand, c.allSetOperand, operand, additive, expand)
		if err != nil {
			return 0, err
		}

		if linkErr := addRelationshipTx(tx, set, u); linkErr != nil {
			return 0, linkErr
		}

		return u, nil
	})
}

// RemoveOperand removes descriptor u from set entirely -- the
// containment edge (set, u), u's own edge to its operand, and both of u's
// axis tags -- then deletes u itself, all inside one transaction (nested
// if graph is a Tx).
//
// This is deliberately simpler than CapsuleRegistry.DeleteCapsule: a
// descriptor node u has no sub-structure of its own (unlike a capsule's
// three role slots), so there is nothing else that could be left
// dangling by removing it. If u has picked up some unrelated extra
// relationship through an out-of-band mutation, the final tx.DeleteNode
// call simply fails with the underlying ErrNodeNotEmpty, and the whole
// removal rolls back via ordinary Transact rollback -- no separate
// ErrCapsuleNotEmpty-style check is needed here.
//
// u must currently be a descriptor of set, i.e. (set, u) must exist;
// otherwise ErrOperandNotInCompositeSet is returned. operand itself is
// never deleted -- only u's own edge to it is removed -- since operand is
// caller-owned data that may still be referenced elsewhere.
func (c *CompositeSetRegistry) RemoveOperand(graph Transactor, set, u NodeID) error {
	// The descriptor's axes are read through tx too, so the tags removed
	// are the ones it actually has when the removal happens.
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		if requireErr := c.requireCompositeSet(tx, set); requireErr != nil {
			return requireErr
		}

		inSet, err := tx.HasRelationship(set, u)
		if err != nil {
			return wrapInterfaceErr(err)
		}
		if !inSet {
			return ErrOperandNotInCompositeSet
		}

		operand, hasOperand, operationTag, operandTag, err := operandDescriptorAxes(tx, u, c.allAdditiveOp, c.allSubtractiveOp, c.allScalarOperand, c.allSetOperand)
		if err != nil {
			return err
		}

		if removeErr := removeRelationshipTx(tx, set, u); removeErr != nil {
			return removeErr
		}

		return deleteOperandDescriptorTx(tx, operand, hasOperand, operationTag, operandTag, u)
	}))
}

// Operands returns set's current operand-descriptor nodes -- its direct
// children in the underlying Graph -- in no particular semantic order
// beyond Graph.FindOutgoing's own deterministic NodeID sort. Use
// OperandTarget/OperandIsAdditive/OperandIsSetOperand to inspect each
// one.
func (c *CompositeSetRegistry) Operands(graph GraphReader, set NodeID) ([]NodeID, error) {
	if requireErr := c.requireCompositeSet(graph, set); requireErr != nil {
		return nil, requireErr
	}

	outgoing, err := graph.FindOutgoing(set)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}

	operands := make([]NodeID, 0, len(outgoing))
	for _, rel := range outgoing {
		operands = append(operands, rel.To)
	}

	return operands, nil
}

// OperandTarget returns descriptor u's operand, i.e. u's single outgoing
// relationship target. Shared logic with CompositeSetLogRegistry.OperandTarget
// -- see operandTargetGeneric.
func (c *CompositeSetRegistry) OperandTarget(graph GraphReader, u NodeID) (operand NodeID, err error) {
	return operandTargetGeneric(graph, u)
}

// OperandIsAdditive reports whether descriptor u is tagged additive
// (true, contributes via union) or subtractive (false, contributes via
// set-difference).
func (c *CompositeSetRegistry) OperandIsAdditive(graph GraphReader, u NodeID) (bool, error) {
	return exactlyOneTag(graph, u, c.allAdditiveOp, c.allSubtractiveOp)
}

// OperandIsSetOperand reports whether descriptor u is tagged as a
// set-expansion operand (true) or a scalar operand (false).
func (c *CompositeSetRegistry) OperandIsSetOperand(graph GraphReader, u NodeID) (bool, error) {
	return exactlyOneTag(graph, u, c.allSetOperand, c.allScalarOperand)
}

// Evaluate computes set's current membership by folding its operand
// descriptors per theorystate.md section 81 -- see the
// CompositeSetRegistry doc comment for the exact fold and for why this is
// never cached.
//
// set must already be tagged (AllCompositeSets, set). If evaluating set
// requires expanding a nested composite Set operand and that expansion
// would revisit a composite-kind node already on the current resolution
// path, ErrCompositeSetCycle is returned (theorystate.md section 83).
func (c *CompositeSetRegistry) Evaluate(graph GraphReader, set NodeID) ([]NodeID, error) {
	if requireErr := c.requireCompositeSet(graph, set); requireErr != nil {
		return nil, requireErr
	}

	return c.evaluate(graph, set, map[NodeID]struct{}{set: {}})
}

// Contains reports whether value currently belongs to set's evaluated
// membership -- a thin wrapper around Evaluate, added for symmetry with
// SetRegistry.Contains and CompositeSetLogRegistry.Contains. Unlike
// CompositeSetLogRegistry.Contains, this does not implement a
// backward-scan optimization: CompositeSetRegistry's union-then-
// difference fold (theorystate.md section 81) is not order-sensitive,
// so there is no "most recent mention" to scan backward toward -- the
// full Evaluate must run regardless.
//
// set must already be tagged (AllCompositeSets, set); value must already
// exist. Like Evaluate, this is never cached.
func (c *CompositeSetRegistry) Contains(graph GraphReader, set, value NodeID) (bool, error) {
	if requireErr := c.requireCompositeSet(graph, set); requireErr != nil {
		return false, requireErr
	}

	if existsErr := requireExists(graph, value); existsErr != nil {
		return false, existsErr
	}

	members, err := c.evaluate(graph, set, map[NodeID]struct{}{set: {}})
	if err != nil {
		return false, err
	}

	for _, id := range members {
		if id == value {
			return true, nil
		}
	}

	return false, nil
}

// evaluate is Evaluate's recursive core, assuming set has already been
// confirmed to exist, to be tagged CompositeSet-kind, and to already be
// recorded in visited.
//
// visited tracks composite-kind NodeIDs currently on the resolution
// path -- not every composite-kind node ever seen during this Evaluate
// call -- so that a DAG where the same composite Set is legitimately
// reached via two different, non-cyclic branches is not mistaken for a
// cycle. resolveSetOperand adds to visited immediately before, and
// removes from visited immediately after, each recursive call into a
// nested composite Set (a standard depth-first on-stack cycle check);
// evaluate itself never mutates visited directly.
func (c *CompositeSetRegistry) evaluate(graph GraphReader, set NodeID, visited map[NodeID]struct{}) ([]NodeID, error) {
	operands, err := graph.FindOutgoing(set)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}

	type descriptor struct {
		u        NodeID
		additive bool
	}

	descriptors := make([]descriptor, 0, len(operands))
	for _, rel := range operands {
		additive, err := exactlyOneTag(graph, rel.To, c.allAdditiveOp, c.allSubtractiveOp)
		if err != nil {
			return nil, err
		}
		descriptors = append(descriptors, descriptor{u: rel.To, additive: additive})
	}

	result := make(map[NodeID]struct{})

	// Additive operands are folded first (union), then subtractive
	// operands (set-difference). This grouping -- not the relative order
	// within each group, which carries no meaning for union/difference --
	// is what keeps the result independent of FindOutgoing's arbitrary
	// NodeID-sorted order.
	for _, d := range descriptors {
		if !d.additive {
			continue
		}
		resolved, err := c.resolveOperand(graph, d.u, visited)
		if err != nil {
			return nil, err
		}
		for _, id := range resolved {
			result[id] = struct{}{}
		}
	}
	for _, d := range descriptors {
		if d.additive {
			continue
		}
		resolved, err := c.resolveOperand(graph, d.u, visited)
		if err != nil {
			return nil, err
		}
		for _, id := range resolved {
			delete(result, id)
		}
	}

	return sortedNodeSet(result), nil
}

// resolveOperand returns the set of NodeIDs descriptor u currently
// contributes, dispatched via resolveOperandGeneric. Shared logic with
// CompositeSetLogRegistry.resolveOperand.
func (c *CompositeSetRegistry) resolveOperand(graph GraphReader, u NodeID, visited map[NodeID]struct{}) ([]NodeID, error) {
	return resolveOperandGeneric(graph, c.allScalarOperand, c.allSetOperand, c.sets, c, c.logs, u, visited)
}

// DeleteCompositeSet deletes set from the underlying graph, additionally
// removing its (AllCompositeSets, set) tag as part of the same
// transaction -- mirroring SetRegistry.DeleteSet/ListRegistry.DeleteList:
// the tag is itself an ordinary primitive relationship into set, so it
// must be removed before Graph.DeleteNode can succeed, not after.
//
// Per theorystate.md section 18, deletion is deliberately "delete only if
// empty": DeleteCompositeSet refuses with ErrNodeNotEmpty if set still
// has any operand descriptors, or is itself referenced elsewhere.
// Callers must RemoveOperand every descriptor first.
//
// set must currently be tagged (AllCompositeSets, set).
func (c *CompositeSetRegistry) DeleteCompositeSet(graph Transactor, set NodeID) error {
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		if requireErr := c.requireCompositeSet(tx, set); requireErr != nil {
			return requireErr
		}
		return untagAndDeleteNodeTx(tx, set, c.allCompositeSets)
	}))
}

// operandAxisTags returns the four operand-descriptor axis tags
// (theorystate.md section 80).
func (c *CompositeSetRegistry) operandAxisTags() []NodeID {
	return []NodeID{c.allAdditiveOp, c.allSubtractiveOp, c.allScalarOperand, c.allSetOperand}
}
