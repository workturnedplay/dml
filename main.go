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
	"context"
	"errors"
	"fmt"
	ilog "log"
	"maps"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// sortedNodeSet returns the members of set as a slice sorted ascending.
// The order has no semantic meaning (theorystate.md section 5); it
// exists only so results are deterministic regardless of Go's map
// iteration order. Shared by CompositeSetRegistry.evaluate,
// CompositeSetLogRegistry.evaluate, and domainConstraint.affectedAnchors.
func sortedNodeSet(set map[NodeID]struct{}) []NodeID {
	ids := make([]NodeID, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}

	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	return ids
}

// operandDescriptorAxes reads back descriptor node u's current
// operation-kind and operand-kind tags (theorystate.md section 80), plus
// its operand target, as concrete tag NodeIDs rather than the plain
// booleans exactlyOneTag reports -- needed before removing a descriptor,
// since removal must know exactly which tag relationship to remove, not
// merely which side of each axis currently holds. Shared by
// CompositeSetRegistry.RemoveOperand and
// CompositeSetLogRegistry.RemoveOperation.
func operandDescriptorAxes(graph GraphReader, u, allAdditiveOp, allSubtractiveOp, allScalarOperand, allSetOperand NodeID) (operand NodeID, hasOperand bool, operationTag, operandTag NodeID, err error) {
	operand, hasOperand, err = singleChildTarget(graph, u)
	if err != nil {
		return 0, false, 0, 0, err
	}

	additive, err := exactlyOneTag(graph, u, allAdditiveOp, allSubtractiveOp)
	if err != nil {
		return 0, false, 0, 0, err
	}
	operationTag = allAdditiveOp
	if !additive {
		operationTag = allSubtractiveOp
	}

	expand, err := exactlyOneTag(graph, u, allSetOperand, allScalarOperand)
	if err != nil {
		return 0, false, 0, 0, err
	}
	operandTag = allScalarOperand
	if expand {
		operandTag = allSetOperand
	}

	return operand, hasOperand, operationTag, operandTag, nil
}

// buildOperandDescriptorTx creates a fresh descriptor node U, tags it
// with exactly one operation-kind tag (additiveTag xor subtractiveTag)
// and exactly one operand-kind tag (scalarTag xor setTag), and wires
// U -> operand, entirely against tx. This is the shared "mint one
// operand descriptor" sequence behind CompositeSetRegistry.AddOperand
// and CompositeSetLogRegistry.AppendOperation (theorystate.md section
// 80): both structures record operands via freshly-minted, identically
// dual-tagged descriptor nodes, differing only in *where* the descriptor
// is then attached (a direct child of the composite set vs. a list
// capsule's value).
func buildOperandDescriptorTx(tx txOps, additiveTag, subtractiveTag, scalarTag, setTag, operand NodeID, additive, expand bool) (u NodeID, err error) {
	operationTag := additiveTag
	if !additive {
		operationTag = subtractiveTag
	}
	operandTag := scalarTag
	if expand {
		operandTag = setTag
	}

	u, err = tx.CreateNode()
	if err != nil {
		return 0, wrapInterfaceErr(err)
	}
	if err2 := tagNodeTx(tx, operationTag, u); err2 != nil {
		return 0, err2
	}
	if err3 := tagNodeTx(tx, operandTag, u); err3 != nil {
		return 0, err3
	}

	_, err = tx.AddRelationship(u, operand)
	return u, wrapInterfaceErr(err)
}

// clearOperandDescriptorEdgesTx removes descriptor u's own edge to its
// operand (if any) and both of its axis tags, against tx, without
// deleting u itself. Factored out as the shared "clear one descriptor's
// edges" step used by deleteOperandDescriptorTx below.
func clearOperandDescriptorEdgesTx(tx txOps, operand NodeID, hasOperand bool, operationTag, operandTag, u NodeID) error {
	if hasOperand {
		if _, err := tx.RemoveRelationship(u, operand); err != nil {
			return wrapInterfaceErr(err)
		}
	}

	if _, err := tx.RemoveRelationship(operationTag, u); err != nil {
		return wrapInterfaceErr(err)
	}

	_, err := tx.RemoveRelationship(operandTag, u)
	return wrapInterfaceErr(err)
}

// deleteOperandDescriptorTx clears descriptor u's own edges (see
// clearOperandDescriptorEdgesTx) and then deletes u itself, against tx.
// This is the shared "tear down one descriptor node" sequence behind
// CompositeSetRegistry.RemoveOperand: a CompositeSetRegistry descriptor
// has no incoming edges beyond what clearOperandDescriptorEdgesTx itself
// clears, so u can be deleted immediately afterward in the same step.
// CompositeSetLogRegistry.RemoveOperation cannot use this directly for
// exactly that reason -- see clearOperandDescriptorEdgesTx's doc comment.
func deleteOperandDescriptorTx(tx txOps, operand NodeID, hasOperand bool, operationTag, operandTag, u NodeID) error {
	if err := clearOperandDescriptorEdgesTx(tx, operand, hasOperand, operationTag, operandTag, u); err != nil {
		return err
	}

	return wrapInterfaceErr(tx.DeleteNode(u))
}

// operandTargetGeneric returns descriptor u's operand, i.e. u's single
// outgoing relationship target. Shared by CompositeSetRegistry.OperandTarget
// and CompositeSetLogRegistry.OperandTarget, since both build identically
// shaped descriptors (theorystate.md section 80).
func operandTargetGeneric(graph GraphReader, u NodeID) (operand NodeID, err error) {
	operand, found, err := singleChildTarget(graph, u)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, ErrInvalidOperandDescriptor
	}

	return operand, nil
}

// operandCarriesKnownSetTag reports whether operand currently carries any
// currently-recognized Set-representation tag (theorystate.md section
// 79): AllSets (via sets), AllCompositeSets (via composites), or, if
// logs is non-nil (see CompositeSetRegistry.SetLogs), AllCompositeSetLogs
// (via logs). Shared by CompositeSetRegistry.AddOperand and
// CompositeSetLogRegistry.AppendOperation's identical expand-time
// validation.
func operandCarriesKnownSetTag(graph GraphReader, sets *SetRegistry, composites *CompositeSetRegistry, logs *CompositeSetLogRegistry, operand NodeID) (bool, error) {
	isSet, err := sets.IsSet(graph, operand)
	if err != nil {
		return false, err
	}
	if isSet {
		return true, nil
	}

	isComposite, err := composites.IsCompositeSet(graph, operand)
	if err != nil {
		return false, err
	}
	if isComposite {
		return true, nil
	}

	if logs != nil {
		isLog, logErr := logs.IsCompositeSetLog(graph, operand)
		if logErr != nil {
			return false, logErr
		}
		if isLog {
			return true, nil
		}
	}

	return false, nil
}

// requireOperand checks, against graph, that operand exists and -- when
// expand is true -- carries one of the currently-recognized
// Set-representation tags (see operandCarriesKnownSetTag). Shared by
// CompositeSetRegistry.addOperandTx and
// CompositeSetLogRegistry.AppendOperation, which validate an operand
// identically (theorystate.md section 80).
func requireOperand(graph GraphReader, sets *SetRegistry, composites *CompositeSetRegistry, logs *CompositeSetLogRegistry, operand NodeID, expand bool) error {
	if existsErr := requireExists(graph, operand); existsErr != nil {
		return existsErr
	}

	if expand {
		known, knownErr := operandCarriesKnownSetTag(graph, sets, composites, logs, operand)
		if knownErr != nil {
			return knownErr
		}
		if !known {
			return ErrInvalidSetOperand
		}
	}

	return nil
}

// domainContainsGeneric reports whether value currently belongs to
// domain's resolved membership, dispatched by whichever of the three
// currently-implemented Set representations domain carries
// (theorystate.md section 9c): a plain Set (via sets.Contains), a
// CompositeSet (via composites.Contains), or, if logs is non-nil (same
// nil-tolerance as operandCarriesKnownSetTag/resolveSetOperandGeneric
// above), a CompositeSetLog (via logs.Contains). Each representation's
// own Contains method already handles its own internal recursion and
// cycle detection (theorystate.md section 83), so unlike
// resolveSetOperandGeneric, no visited-set threading is needed here --
// there is nothing for this function itself to recurse into.
func domainContainsGeneric(graph GraphReader, sets *SetRegistry, composites *CompositeSetRegistry, logs *CompositeSetLogRegistry, domain, value NodeID) (bool, error) {
	isSet, err := sets.IsSet(graph, domain)
	if err != nil {
		return false, err
	}
	if isSet {
		return sets.Contains(graph, domain, value)
	}

	isComposite, err := composites.IsCompositeSet(graph, domain)
	if err != nil {
		return false, err
	}
	if isComposite {
		return composites.Contains(graph, domain, value)
	}

	if logs != nil {
		isLog, logErr := logs.IsCompositeSetLog(graph, domain)
		if logErr != nil {
			return false, logErr
		}
		if isLog {
			return logs.Contains(graph, domain, value)
		}
	}

	return false, ErrInvalidSetOperand
}

// resolveSetOperandGeneric resolves operand's own current membership,
// dispatched by whichever of the three currently-implemented Set
// representations operand actually carries (theorystate.md section 83):
// a plain Set (delegated to sets), a CompositeSet (delegated to
// composites, resolved recursively), or a CompositeSetLog (delegated to
// logs, resolved recursively) -- logs may be nil if the caller has not
// wired cross-representation dispatch to a CompositeSetLogRegistry yet
// (see CompositeSetRegistry.SetLogs), in which case a CompositeSetLog
// operand is reported via ErrInvalidSetOperand exactly like any other
// operand carrying no known Set-representation tag. Shared by
// CompositeSetRegistry and CompositeSetLogRegistry's resolveOperand
// methods, via resolveOperandGeneric below.
//
// visited tracks composite-kind (CompositeSet or CompositeSetLog)
// NodeIDs currently on the resolution path, shared across both
// representations, so a cycle crossing between them is still detected
// (theorystate.md section 83).
func resolveSetOperandGeneric(graph GraphReader, sets *SetRegistry, composites *CompositeSetRegistry, logs *CompositeSetLogRegistry, operand NodeID, visited map[NodeID]struct{}) ([]NodeID, error) {
	isSet, err := sets.IsSet(graph, operand)
	if err != nil {
		return nil, err
	}
	if isSet {
		return sets.Members(graph, operand)
	}

	isComposite, err := composites.IsCompositeSet(graph, operand)
	if err != nil {
		return nil, err
	}
	if isComposite {
		if _, seen := visited[operand]; seen {
			return nil, ErrCompositeSetCycle
		}
		visited[operand] = struct{}{}
		defer delete(visited, operand)

		return composites.evaluate(graph, operand, visited)
	}

	if logs != nil {
		isLog, logErr := logs.IsCompositeSetLog(graph, operand)
		if logErr != nil {
			return nil, logErr
		}
		if isLog {
			if _, seen := visited[operand]; seen {
				return nil, ErrCompositeSetCycle
			}
			visited[operand] = struct{}{}
			defer delete(visited, operand)

			return logs.evaluate(graph, operand, visited)
		}
	}

	return nil, ErrInvalidSetOperand
}

// resolveOperandGeneric returns the set of NodeIDs descriptor u currently
// contributes: the singleton {operand} for a scalar-axis descriptor, or
// operand's own resolved membership (via resolveSetOperandGeneric) for a
// set-axis descriptor. Shared by CompositeSetRegistry.resolveOperand and
// CompositeSetLogRegistry.resolveOperand.
func resolveOperandGeneric(graph GraphReader, allScalarOperand, allSetOperand NodeID, sets *SetRegistry, composites *CompositeSetRegistry, logs *CompositeSetLogRegistry, u NodeID, visited map[NodeID]struct{}) ([]NodeID, error) {
	operand, found, err := singleChildTarget(graph, u)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrInvalidOperandDescriptor
	}

	expand, err := exactlyOneTag(graph, u, allSetOperand, allScalarOperand)
	if err != nil {
		return nil, err
	}

	if !expand {
		return []NodeID{operand}, nil
	}

	return resolveSetOperandGeneric(graph, sets, composites, logs, operand, visited)
}

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

// CompositeSetLogRegistry implements the append-only log-based composite
// Set representation of theorystate.md section 82: an ordered log of
// Set-mutating operations, whose current membership is the *fold* of
// that log, not an insertion-ordered collection in its own right (see
// that section for why this structure was deliberately renamed away from
// an earlier "OrderedCompositeSet" working name that wrongly implied the
// latter).
//
// A CompositeSetLog is represented as an ordinary List (ListRegistry),
// reused and reinterpreted: the tagged node carries both
// (AllLists,node) and (AllCompositeSetLogs,node) simultaneously --
// theorystate.md section 10c's precedent for one identity carrying more
// than one simultaneous interpretation, applied here to reuse
// ListRegistry's ordering machinery wholesale rather than reimplementing
// it. AllCompositeSetLogs participates in theorystate.md section 79's
// three-way Set-representation mutual exclusivity; AllLists does not,
// since it is plumbing this representation happens to be built from, not
// a Set-representation tag of its own.
//
// Each logged operation is one list element, whose *value* (via the
// ordinary, opaque ListRegistry/CapsuleRegistry value slot -- this
// registry does not reach into or specialize any List/Capsule internal)
// is a freshly minted operand-descriptor node U, exactly the same shape
// used by CompositeSetRegistry (theorystate.md section 80): U -> operand,
// tagged along the same two orthogonal axes (operation kind: additive/
// subtractive; operand kind: scalar/set, always explicit, never inferred
// from operand's own tags).
//
// Evaluate folds log's operations head to tail, in list order -- unlike
// CompositeSetRegistry.Evaluate's order-insensitive union-then-difference,
// order here is semantically load-bearing: a later operation mentioning a
// given element supersedes an earlier one.
//
//	accumulator := ∅
//	for each capsule's U, in list order:
//	    resolved := resolve(U)          // per U's operand-kind axis
//	    if U tagged additive:    accumulator := accumulator ∪ resolved
//	    if U tagged subtractive: accumulator := accumulator \ resolved
//	return accumulator
//
// Contains(log,value) exploits a proven property of this fold
// (theorystate.md section 82): value's final membership is decided
// entirely by the *last* operation in the log whose currently-resolved
// operand mentions value, so Contains scans backward and can stop at the
// first (i.e. most recent) such operation, using the cheapest membership
// check available for that operand's own kind rather than always fully
// resolving it -- see operandMentions's doc comment for exactly what is
// and is not cheaper, and Contains's own doc comment for why this
// optimization is real but not complete.
//
// Resolving a set-expansion operand dispatches, per theorystate.md
// section 83, to whichever currently-implemented Set representation the
// operand actually carries: a plain Set (delegated to sets), a
// CompositeSet (delegated to composites, resolved recursively), or
// another CompositeSetLog (resolved recursively via this same type).
// Cycle detection (theorystate.md section 83) tracks composite-kind
// (CompositeSet or CompositeSetLog) NodeIDs on the current resolution
// path, shared across both representations via one visited set, so a
// cycle crossing between them is still caught.
//
// Like every other Evaluate/Members in this file, Evaluate and Contains
// are never cached -- recomputed fresh from the Graph on every call, for
// the reasons given throughout (theorystate.md sections 9a/35).
//
// A capsule created via this registry's own AppendOperation always
// carries a well-formed operand descriptor as its value. A capsule
// created by bypassing this registry and calling the underlying
// ListRegistry.Append directly produces a capsule whose value has no
// descriptor tags at all; Evaluate/Contains fail loudly
// (ErrInvalidOperandDescriptor) on encountering such a capsule rather
// than guessing a default operation kind, matching this codebase's
// existing fail-loud-not-silently-repair discipline.
//
// CompositeSetLogRegistry depends on an existing *CompositeSetRegistry
// (to resolve CompositeSet-kind operands, and as the source of the
// shared *SetRegistry used to resolve plain-Set operands -- see
// NewCompositeSetLogRegistry); a *CompositeSetRegistry, in turn, must be
// told about a CompositeSetLogRegistry after the fact (via
// CompositeSetRegistry.SetLogs) to resolve CompositeSetLog-kind operands
// itself, since the two types mutually reference each other and Go
// cannot construct two such values in a single mutually-referential
// step. See CompositeSetRegistry.SetLogs's doc comment for the required
// construction order.
type CompositeSetLogRegistry struct {
	lists               OrderedList
	sets                *SetRegistry
	composites          *CompositeSetRegistry
	allCompositeSetLogs NodeID
	allAdditiveOp       NodeID
	allSubtractiveOp    NodeID
	allScalarOperand    NodeID
	allSetOperand       NodeID
}

// NewCompositeSetLogRegistry creates a CompositeSetLogRegistry over
// graph. This registers no Checker of its own (see Graph.RegisterChecker
// and NewCompositeSetRegistry's own two Checkers' doc comment): its
// underlying List's structure is already covered by the ListRegistry
// Checker registered when lists was itself constructed, and its logged
// operations' descriptor shape is already covered by the
// operand-descriptor Checker registered when composites was itself
// constructed, since both required arguments below must already exist.
//
// lists is used to store and traverse the log itself (each logged
// operation is one list element, in append order). It is any OrderedList
// (ListRegistry or SlottedListRegistry): the log reaches its list only
// through that interface, so which one a registry uses is the caller's
// choice, and every log the registry makes is a list of that type. A log
// made by a registry over the other type is tagged AllCompositeSetLogs
// too, so it passes the checks of an operand, and fails with ErrNotList as
// soon as it is read. composites is used
// both to resolve set-expansion operands that turn out to be
// CompositeSets (theorystate.md section 83's dispatcher) and as the
// source of the SetRegistry used to resolve plain-Set operands (via
// composites.sets, avoiding a second, independently-passed SetRegistry
// that could otherwise silently disagree with the one composites itself
// dispatches through) -- composites must already be constructed over the
// same graph. allCompositeSetLogs tags CompositeSetLog-kind nodes
// (alongside lists' own AllLists tag -- see the CompositeSetLogRegistry
// doc comment); allAdditiveOp/allSubtractiveOp and allScalarOperand/
// allSetOperand tag a logged operation's descriptor exactly as they do
// for CompositeSetRegistry (theorystate.md section 80) -- the same
// bootstrapped tag NodeIDs are expected to be passed to both
// constructors. All five tag NodeIDs must already exist -- typically via
// NameRegistry.BootstrapNames(FoundationalNames).
//
// This CompositeSetLogRegistry can immediately resolve CompositeSet and
// plain Set operands, but cannot yet resolve CompositeSetLog operands
// (including recursive self-reference) until composites is told about it
// via composites.SetLogs(this registry) -- see that method's doc comment
// for why this second wiring step is required.
func NewCompositeSetLogRegistry(graph GraphAPI, lists OrderedList, composites *CompositeSetRegistry, allCompositeSetLogs, allAdditiveOp, allSubtractiveOp, allScalarOperand, allSetOperand NodeID) (*CompositeSetLogRegistry, error) {
	if existsErr := requireAllExist(graph, allCompositeSetLogs, allAdditiveOp, allSubtractiveOp, allScalarOperand, allSetOperand); existsErr != nil {
		return nil, existsErr
	}

	return &CompositeSetLogRegistry{
		lists:               lists,
		sets:                composites.sets,
		composites:          composites,
		allCompositeSetLogs: allCompositeSetLogs,
		allAdditiveOp:       allAdditiveOp,
		allSubtractiveOp:    allSubtractiveOp,
		allScalarOperand:    allScalarOperand,
		allSetOperand:       allSetOperand,
	}, nil
}

// IsCompositeSetLog reports whether id is currently tagged
// (AllCompositeSetLogs, id).
func (c *CompositeSetLogRegistry) IsCompositeSetLog(graph GraphReader, id NodeID) (bool, error) {
	has, err := graph.HasRelationship(c.allCompositeSetLogs, id)
	return has, wrapInterfaceErr(err)
}

// requireLog checks that log exists and is tagged
// (AllCompositeSetLogs, log), returning ErrNodeNotFound or
// ErrNotCompositeSetLog otherwise. Delegates to the shared requireTagged
// helper (see its doc comment on PointerRegistry.requirePointer).
func (c *CompositeSetLogRegistry) requireLog(graph GraphReader, log NodeID) error {
	return requireTagged(graph, log, c.allCompositeSetLogs, ErrNotCompositeSetLog)
}

// NewCompositeSetLog creates a fresh NodeID and tags it both
// (AllLists, id) and (AllCompositeSetLogs, id), entirely inside one
// transaction (nested if graph is a Tx): ListRegistry.NewList mints and
// tags the list as a nested step and the second tag is applied in the
// same transaction, so there is no observable intermediate state where id
// is tagged AllLists but not yet AllCompositeSetLogs. The new log starts
// empty: no operations, no head, no tail.
func (c *CompositeSetLogRegistry) NewCompositeSetLog(graph Transactor) (NodeID, error) {
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		id, err := c.lists.NewList(tx)
		if err != nil {
			return 0, wrapInterfaceErr(err)
		}

		if tagErr := tagNodeTx(tx, c.allCompositeSetLogs, id); tagErr != nil {
			return 0, tagErr
		}

		return id, nil
	})
}

// AppendOperation appends a new operation to the tail of log, recorded as
// a freshly minted operand-descriptor node U (theorystate.md section 80)
// used as the new list element's value: log -> capsule -> U -> operand,
// with U tagged along both axes described in the CompositeSetLogRegistry
// doc comment. Entirely inside one Graph.Transact call, composing
// buildOperandDescriptorTx (minting and tagging U) with
// ListRegistry.Append (wiring U in as the new tail element's value).
//
// additive/expand mean exactly what they do for
// CompositeSetRegistry.AddOperand (see its doc comment). If expand is
// true, operand must already carry one of the currently-recognized
// Set-representation tags -- checked here, at write time, before U is
// created at all -- returning ErrInvalidSetOperand otherwise.
//
// log must already be tagged (AllCompositeSetLogs, log); operand must
// already exist. Per theorystate.md section 85, no existing identical
// descriptor is searched for or reused, exactly like AddOperand.
func (c *CompositeSetLogRegistry) AppendOperation(graph Transactor, log, operand NodeID, additive, expand bool) (u, capsule NodeID, err error) {
	err = graph.Transact(func(tx Tx) error {
		if requireErr := c.requireLog(tx, log); requireErr != nil {
			return requireErr
		}

		if requireErr := requireOperand(tx, c.sets, c.composites, c, operand, expand); requireErr != nil {
			return requireErr
		}

		var err2 error
		u, err2 = buildOperandDescriptorTx(tx, c.allAdditiveOp, c.allSubtractiveOp, c.allScalarOperand, c.allSetOperand, operand, additive, expand)
		if err2 != nil {
			return err2
		}

		// A slotted list's mutations do not touch the list node itself, and
		// the domain Checker finds the pointers a log is the domain of
		// through the log being touched, so say it changed.
		tx.Touch(log)

		capsule, err2 = c.lists.Append(tx, log, u)
		return wrapInterfaceErr(err2)
	})
	if err != nil {
		return 0, 0, wrapInterfaceErr(err)
	}

	return u, capsule, nil
}

// RemoveOperation removes capsule -- and its descriptor value u -- from
// log entirely.
//
// The whole removal is one Graph.Transact call: capsule is unlinked from
// log (ListRegistry.RemoveWithoutDeletingCapsule), reclaimed
// (CapsuleRegistry.DeleteCapsule, whose teardown clears capsule's
// value-slot edge into u), and then u's own edges (its operand target and
// both axis tags) are cleared and u deleted (deleteOperandDescriptorTx,
// the helper CompositeSetRegistry.RemoveOperand also uses). It is
// all-or-nothing: if any step fails -- ErrCapsuleNotEmpty because
// something unexpected still references one of capsule's role slots, or a
// commit-time Checker declines the resulting state -- every step is
// rolled back and the log is exactly as it was. An earlier version used
// three separate transactions, so a failed capsule deletion left capsule
// unlinked from log but still holding u.
//
// capsule must currently be an element of log (checked via the
// (log,capsule) containment edge, returning ErrCapsuleNotInList
// otherwise); log must already be tagged (AllCompositeSetLogs, log).
func (c *CompositeSetLogRegistry) RemoveOperation(graph Transactor, log, capsule NodeID) error {
	// Every read, including the descriptor's axes, goes through tx. If
	// graph is a Tx and this fails, only this call is undone.
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		if requireErr := c.requireLog(tx, log); requireErr != nil {
			return requireErr
		}

		// See AppendOperation: the log changes, so it is touched.
		tx.Touch(log)

		inLog, err := c.lists.IsMember(tx, log, capsule)
		if err != nil {
			return wrapInterfaceErr(err)
		}
		if !inLog {
			return ErrCapsuleNotInList
		}

		u, hasValue, err := c.lists.Capsules().Value(tx, capsule)
		if err != nil {
			return err
		}
		if !hasValue {
			return ErrInvalidOperandDescriptor
		}

		operand, hasOperand, operationTag, operandTag, err := operandDescriptorAxes(tx, u, c.allAdditiveOp, c.allSubtractiveOp, c.allScalarOperand, c.allSetOperand)
		if err != nil {
			return err
		}

		if unlinkErr := c.lists.RemoveWithoutDeletingCapsule(tx, log, capsule); unlinkErr != nil {
			return wrapInterfaceErr(unlinkErr)
		}

		if deleteErr := c.lists.Capsules().DeleteCapsule(tx, capsule); deleteErr != nil {
			return deleteErr
		}

		return deleteOperandDescriptorTx(tx, operand, hasOperand, operationTag, operandTag, u)
	}))
}

// Operations returns log's current operand-descriptor nodes (each
// logged operation's U, per theorystate.md section 80), in log order
// (head to tail) -- unlike CompositeSetRegistry.Operands, this order is
// semantically meaningful for a CompositeSetLog (theorystate.md section
// 82's fold is order-sensitive). Use OperandTarget/OperandIsAdditive/
// OperandIsSetOperand to inspect each one.
func (c *CompositeSetLogRegistry) Operations(graph GraphReader, log NodeID) ([]NodeID, error) {
	if requireErr := c.requireLog(graph, log); requireErr != nil {
		return nil, requireErr
	}

	values, err := c.lists.Elements(graph, log)

	return values, wrapInterfaceErr(err)
}

// OperandTarget returns descriptor u's operand, i.e. u's single outgoing
// relationship target. Shared logic with CompositeSetRegistry.OperandTarget
// -- see operandTargetGeneric.
func (c *CompositeSetLogRegistry) OperandTarget(graph GraphReader, u NodeID) (operand NodeID, err error) {
	return operandTargetGeneric(graph, u)
}

// OperandIsAdditive reports whether descriptor u is tagged additive
// (true, contributes via union) or subtractive (false, contributes via
// set-difference).
func (c *CompositeSetLogRegistry) OperandIsAdditive(graph GraphReader, u NodeID) (bool, error) {
	return exactlyOneTag(graph, u, c.allAdditiveOp, c.allSubtractiveOp)
}

// OperandIsSetOperand reports whether descriptor u is tagged as a
// set-expansion operand (true) or a scalar operand (false).
func (c *CompositeSetLogRegistry) OperandIsSetOperand(graph GraphReader, u NodeID) (bool, error) {
	return exactlyOneTag(graph, u, c.allSetOperand, c.allScalarOperand)
}

// Evaluate computes log's current membership by folding its logged
// operations left-to-right (head to tail) per theorystate.md section 82
// -- see the CompositeSetLogRegistry doc comment for the exact fold and
// for why order is semantically load-bearing here, unlike
// CompositeSetRegistry.Evaluate's order-insensitive union-then-difference.
//
// log must already be tagged (AllCompositeSetLogs, log). If evaluating
// log requires expanding a nested composite-kind operand (CompositeSet or
// CompositeSetLog) and that expansion would revisit a composite-kind node
// already on the current resolution path, ErrCompositeSetCycle is
// returned (theorystate.md section 83).
func (c *CompositeSetLogRegistry) Evaluate(graph GraphReader, log NodeID) ([]NodeID, error) {
	if requireErr := c.requireLog(graph, log); requireErr != nil {
		return nil, requireErr
	}

	return c.evaluate(graph, log, map[NodeID]struct{}{log: {}})
}

// evaluate is Evaluate's recursive core, assuming log has already been
// confirmed to exist, to be tagged CompositeSetLog-kind, and to already
// be recorded in visited. See CompositeSetRegistry.evaluate's doc
// comment for why visited is path-scoped, not a global ever-visited set
// -- the same reasoning applies identically here, now shared across both
// representations (theorystate.md section 83).
func (c *CompositeSetLogRegistry) evaluate(graph GraphReader, log NodeID, visited map[NodeID]struct{}) ([]NodeID, error) {
	operations, err := c.lists.Elements(graph, log)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}

	result := make(map[NodeID]struct{})

	for _, u := range operations {
		additive, err := exactlyOneTag(graph, u, c.allAdditiveOp, c.allSubtractiveOp)
		if err != nil {
			return nil, err
		}

		resolved, err := c.resolveOperand(graph, u, visited)
		if err != nil {
			return nil, err
		}

		if additive {
			for _, id := range resolved {
				result[id] = struct{}{}
			}
		} else {
			for _, id := range resolved {
				delete(result, id)
			}
		}
	}

	return sortedNodeSet(result), nil
}

// resolveOperand returns the set of NodeIDs descriptor u currently
// contributes -- shared logic with CompositeSetRegistry.resolveOperand,
// see resolveOperandGeneric.
func (c *CompositeSetLogRegistry) resolveOperand(graph GraphReader, u NodeID, visited map[NodeID]struct{}) ([]NodeID, error) {
	return resolveOperandGeneric(graph, c.allScalarOperand, c.allSetOperand, c.sets, c.composites, c, u, visited)
}

// Contains reports whether value currently belongs to log's evaluated
// membership, without necessarily folding the entire log.
//
// Per theorystate.md section 82's proven property, this scans log
// backward (tail to head): the *last* operation whose currently-resolved
// operand mentions value entirely determines value's final membership
// (additive -> present, subtractive -> absent), since every later
// operation, by definition of "last mentioning operation", does not
// itself resolve to something containing value, so no later union can
// reintroduce it and no later subtraction can remove it again. This lets
// Contains stop at the first (i.e. most recent) qualifying operation
// using the cheapest membership check available for that operand's kind
// (see operandMentions) -- a real optimization in the common case, but
// not a complete one: an operand resolving through a nested CompositeSet
// or CompositeSetLog still requires fully resolving that operand (there
// is no cheaper partial check available for those), and the worst case
// (value absent entirely, or only mentioned at the head) still walks the
// whole log. See theorystate.md section 82 for why no cache is kept to
// avoid this cost, consistent with every other Evaluate/Contains in this
// file.
func (c *CompositeSetLogRegistry) Contains(graph GraphReader, log, value NodeID) (bool, error) {
	if requireErr := c.requireLog(graph, log); requireErr != nil {
		return false, requireErr
	}

	if existsErr := requireExists(graph, value); existsErr != nil {
		return false, existsErr
	}

	return c.contains(graph, log, value, map[NodeID]struct{}{log: {}})
}

// reverseLookup returns the reverse lookups over operand descriptors. Its logs
// is c itself, so a log that expands another log is found even if composites
// was never told about logs (CompositeSetRegistry.SetLogs).
func (c *CompositeSetLogRegistry) reverseLookup() setReverseLookup {
	return setReverseLookup{composites: c.composites, logs: c}
}

// mentionCandidates returns the capsules of log whose operation might mention
// value, found by reverse lookup from value without reading the log. A
// superset of the capsules that really mention it: contains confirms each
// with operandMentions, so a composite that subtracts the value again is
// merely a candidate that fails the check.
//
// Value can be mentioned by a scalar descriptor naming it, or by a set
// descriptor whose operand contains it. So the holders are the nodes whose
// membership might contain value (the plain Sets that list it as a member,
// the composites and logs owning a scalar descriptor that names it, and every
// composite or log that expands any of those, transitively), and a candidate
// is a capsule of log whose descriptor is a scalar one naming value or a set
// one expanding a holder.
func (c *CompositeSetLogRegistry) mentionCandidates(graph GraphReader, log, value NodeID) (map[NodeID]struct{}, error) {
	walker := c.reverseLookup()
	holders := make(map[NodeID]struct{})
	scalars := make([]NodeID, 0)

	incomingErr := forEachIncoming(graph, value, func(rel Relationship) error {
		isSet, setErr := c.sets.IsSet(graph, rel.From)
		if setErr != nil {
			return setErr
		}
		if isSet {
			holders[rel.From] = struct{}{}

			return nil
		}

		isScalar, scalarErr := graph.HasRelationship(c.allScalarOperand, rel.From)
		if scalarErr != nil {
			return wrapInterfaceErr(scalarErr)
		}
		if !isScalar {
			return nil
		}

		scalars = append(scalars, rel.From)

		owners, ownersErr := walker.descriptorOwners(graph, rel.From)
		if ownersErr != nil {
			return ownersErr
		}

		for _, owner := range owners {
			// This log's own descriptors are handled through scalars.
			if owner != log {
				holders[owner] = struct{}{}
			}
		}

		return nil
	})
	if incomingErr != nil {
		return nil, incomingErr
	}

	for _, holder := range slices.Collect(maps.Keys(holders)) {
		containers, containersErr := walker.transitiveSetContainers(graph, holder)
		if containersErr != nil {
			return nil, containersErr
		}

		for _, container := range containers {
			holders[container] = struct{}{}
		}
	}

	candidates := make(map[NodeID]struct{})

	addCapsulesOf := func(descriptor NodeID) error {
		capsules, capsulesErr := c.lists.Capsules().CapsulesWithValue(graph, descriptor)
		if capsulesErr != nil {
			return capsulesErr
		}

		for _, capsule := range capsules {
			inLog, inLogErr := c.lists.IsMember(graph, log, capsule)
			if inLogErr != nil {
				return wrapInterfaceErr(inLogErr)
			}
			if inLog {
				candidates[capsule] = struct{}{}
			}
		}

		return nil
	}

	for _, descriptor := range scalars {
		if addErr := addCapsulesOf(descriptor); addErr != nil {
			return nil, addErr
		}
	}

	for holder := range holders {
		holderErr := forEachIncoming(graph, holder, func(rel Relationship) error {
			isSetOperand, tagErr := graph.HasRelationship(c.allSetOperand, rel.From)
			if tagErr != nil {
				return wrapInterfaceErr(tagErr)
			}
			if !isSetOperand {
				return nil
			}

			return addCapsulesOf(rel.From)
		})
		if holderErr != nil {
			return nil, holderErr
		}
	}

	return candidates, nil
}

// contains is Contains's recursive core, assuming log and value have
// already been confirmed to exist, log to be tagged CompositeSetLog-kind,
// and log to already be recorded in visited.
//
// It does not read the log to find where value is mentioned: the candidate
// capsules come from a reverse lookup (mentionCandidates), so a value nothing
// mentions is answered without touching the log, and otherwise the log is
// walked backward from its tail, one validated step at a time (stepBack), only
// until the most recent operation that really mentions value. Unlike Elements
// it does not validate the whole list on every call: a value the log mentions
// early still costs the distance to it, and the global properties are left to
// VerifyStructures.
func (c *CompositeSetLogRegistry) contains(graph GraphReader, log, value NodeID, visited map[NodeID]struct{}) (bool, error) {
	candidates, err := c.mentionCandidates(graph, log, value)
	if err != nil {
		return false, err
	}

	if len(candidates) == 0 {
		return false, nil
	}

	current, hasCurrent, err := c.lists.Tail(graph, log)
	if err != nil {
		return false, wrapInterfaceErr(err)
	}

	seen := make(map[NodeID]struct{})

	for hasCurrent {
		if _, repeated := seen[current]; repeated {
			return false, ErrListCycle
		}

		seen[current] = struct{}{}

		// Every operation the walk passes is checked for shape (a value, one
		// operand, one tag per axis), candidate or not, so a malformed one
		// fails loudly instead of being skipped.
		u, hasValue, valueErr := c.lists.Capsules().Value(graph, current)
		if valueErr != nil {
			return false, valueErr
		}
		if !hasValue {
			return false, ErrInvalidOperandDescriptor
		}

		operand, operandErr := operandTargetGeneric(graph, u)
		if operandErr != nil {
			return false, operandErr
		}

		expand, expandErr := exactlyOneTag(graph, u, c.allSetOperand, c.allScalarOperand)
		if expandErr != nil {
			return false, expandErr
		}

		additive, additiveErr := exactlyOneTag(graph, u, c.allAdditiveOp, c.allSubtractiveOp)
		if additiveErr != nil {
			return false, additiveErr
		}

		if _, candidate := candidates[current]; candidate {
			mentions, mentionErr := c.operandMentions(graph, operand, expand, value, visited)
			if mentionErr != nil {
				return false, mentionErr
			}
			if mentions {
				return additive, nil
			}
		}

		current, hasCurrent, err = c.lists.stepBack(graph, log, current)
		if err != nil {
			return false, wrapInterfaceErr(err)
		}
	}

	return false, nil
}

// operandMentions reports whether operand's contribution -- the
// singleton {operand} for a scalar-axis descriptor (expand == false), or
// operand's own current resolved membership for a set-axis descriptor
// (expand == true) -- includes value, using the cheapest check available
// for operand's own kind (theorystate.md section 82): direct equality
// for a scalar operand, SetRegistry.Contains (an O(1) relationship
// check) for a plain Set operand, and full recursive resolution (no
// cheaper check exists for either composite representation) for a
// nested CompositeSet or CompositeSetLog operand.
func (c *CompositeSetLogRegistry) operandMentions(graph GraphReader, operand NodeID, expand bool, value NodeID, visited map[NodeID]struct{}) (bool, error) {
	if !expand {
		return operand == value, nil
	}

	isSet, err := c.sets.IsSet(graph, operand)
	if err != nil {
		return false, err
	}
	if isSet {
		return c.sets.Contains(graph, operand, value)
	}

	isComposite, err := c.composites.IsCompositeSet(graph, operand)
	if err != nil {
		return false, err
	}
	if isComposite {
		if _, seen := visited[operand]; seen {
			return false, ErrCompositeSetCycle
		}
		visited[operand] = struct{}{}
		defer delete(visited, operand)

		resolved, evalErr := c.composites.evaluate(graph, operand, visited)
		if evalErr != nil {
			return false, evalErr
		}
		for _, id := range resolved {
			if id == value {
				return true, nil
			}
		}
		return false, nil
	}

	isLog, err := c.IsCompositeSetLog(graph, operand)
	if err != nil {
		return false, err
	}
	if isLog {
		if _, seen := visited[operand]; seen {
			return false, ErrCompositeSetCycle
		}
		visited[operand] = struct{}{}
		defer delete(visited, operand)

		return c.contains(graph, operand, value, visited)
	}

	return false, ErrInvalidSetOperand
}

// DeleteCompositeSetLog deletes log from the underlying graph,
// additionally removing both of its tags -- (AllCompositeSetLogs,log)
// and (AllLists,log) -- as part of the same transaction, mirroring
// ListRegistry.DeleteList/SetRegistry.DeleteSet/
// CompositeSetRegistry.DeleteCompositeSet: each tag is itself an
// ordinary primitive relationship *into* log, and therefore itself
// counts toward log's relationship count, so both must be removed before
// Graph.DeleteNode can succeed, not after.
//
// Per theorystate.md section 18, deletion is deliberately "delete only
// if empty": DeleteCompositeSetLog refuses with ErrNodeNotEmpty if log
// still has any logged operations, or is itself referenced elsewhere.
// Callers must RemoveOperation every logged operation first.
//
// log must currently be tagged (AllCompositeSetLogs, log).
func (c *CompositeSetLogRegistry) DeleteCompositeSetLog(graph Transactor, log NodeID) error {
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		if requireErr := c.requireLog(tx, log); requireErr != nil {
			return requireErr
		}
		// The log's own tag goes first; the list type then deletes the list
		// and whatever it made for it (a slotted list has a members node and
		// two slots). If that refuses (ErrNodeNotEmpty), the whole
		// transaction rolls back and the tag is restored.
		if untagErr := removeRelationshipTx(tx, c.allCompositeSetLogs, log); untagErr != nil {
			return untagErr
		}

		return wrapInterfaceErr(c.lists.DeleteList(tx, log))
	}))
}

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

// operandAxisTags returns the four operand-descriptor axis tags
// (theorystate.md section 80).
func (c *CompositeSetRegistry) operandAxisTags() []NodeID {
	return []NodeID{c.allAdditiveOp, c.allSubtractiveOp, c.allScalarOperand, c.allSetOperand}
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

// setReverseLookup holds the reverse lookups over operand descriptors that
// need nothing but the composite and log registries: from a descriptor to the
// composite or log owning it, and from a node to every composite or log that
// expands it. domainConstraint uses them to find the pointers a mutation
// could strand, CompositeSetLogRegistry to find the operations that might
// mention a value. logs may be nil (no log registry exists yet), as for
// domainConstraint.
type setReverseLookup struct {
	composites *CompositeSetRegistry
	logs       *CompositeSetLogRegistry
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

// transitiveSetContainers returns every composite/log that expands node
// as a set operand, directly or through any chain of such composites/logs
// (excluding node itself). It is a breadth-first walk over
// setOperandContainers with a visited set, so shared sub-expressions
// (diamonds) are visited once and cycles -- legal in a corrupted graph --
// terminate.
func (r setReverseLookup) transitiveSetContainers(g GraphReader, node NodeID) ([]NodeID, error) {
	visited := map[NodeID]struct{}{node: {}}
	queue := []NodeID{node}
	var containers []NodeID

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		direct, err := r.setOperandContainers(g, current)
		if err != nil {
			return nil, err
		}

		for _, container := range direct {
			if _, seen := visited[container]; seen {
				continue
			}

			visited[container] = struct{}{}
			containers = append(containers, container)
			queue = append(queue, container)
		}
	}

	return containers, nil
}

// setOperandContainers returns every composite/log that has node as a
// set-expansion (AllSetOperand) operand, one level up. A descriptor using
// node as a scalar operand is deliberately ignored: a scalar operand
// contributes node itself, not node's membership, so a change to node's
// members cannot change the container's membership.
func (r setReverseLookup) setOperandContainers(g GraphReader, node NodeID) ([]NodeID, error) {
	incoming, err := g.FindIncoming(node)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}

	containers := make([]NodeID, 0, len(incoming))
	for _, rel := range incoming {
		isSetOperand, hasErr := g.HasRelationship(r.composites.allSetOperand, rel.From)
		if hasErr != nil {
			return nil, wrapInterfaceErr(hasErr)
		}
		if !isSetOperand {
			continue
		}

		owners, ownersErr := r.descriptorOwners(g, rel.From)
		if ownersErr != nil {
			return nil, ownersErr
		}

		containers = append(containers, owners...)
	}

	return containers, nil
}

// descriptorOwners returns the composite(s) having descriptor u as a
// direct child, plus (if a CompositeSetLogRegistry was supplied) the
// log(s) having a capsule whose value is u -- the reverse of the two ways
// an operand descriptor is attached (theorystate.md section 80/82). The
// log hop reuses CapsuleRegistry.CapsulesWithValue rather than any new
// index.
func (r setReverseLookup) descriptorOwners(g GraphReader, u NodeID) ([]NodeID, error) {
	incoming, err := g.FindIncoming(u)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}

	owners := make([]NodeID, 0, len(incoming))
	for _, rel := range incoming {
		isComposite, isCompositeErr := r.composites.IsCompositeSet(g, rel.From)
		if isCompositeErr != nil {
			return nil, isCompositeErr
		}
		if isComposite {
			owners = append(owners, rel.From)
		}
	}

	if r.logs == nil {
		return owners, nil
	}

	capsules, err := r.logs.lists.Capsules().CapsulesWithValue(g, u)
	if err != nil {
		return nil, err
	}

	for _, capsule := range capsules {
		lists, listsErr := r.logs.lists.ListsOf(g, capsule)
		if listsErr != nil {
			return nil, wrapInterfaceErr(listsErr)
		}

		for _, list := range lists {
			isLog, isLogErr := r.logs.IsCompositeSetLog(g, list)
			if isLogErr != nil {
				return nil, isLogErr
			}
			if isLog {
				owners = append(owners, list)
			}
		}
	}

	return owners, nil
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

// LeaseRegistry implements sessions and holds (theorystate.md section
// 111): reference counting that knows WHO holds a resource, so a holder
// that dies can be found and released, which a bare counter cannot do.
//
// A session is an ordinary node tagged (AllSessions, session): the
// liveness identity of one client of the graph (a goroutine, a process,
// eventually a remote machine). A resource is a plain Set (SetRegistry)
// used as a holder set: a session holds the resource exactly when it is a
// member. Nothing new is stored:
//   - membership is a unique (resource, session) pair (theorystate.md
//     section 2.6), so acquiring twice cannot double count;
//   - "what does this session hold" is FindIncoming(session) filtered to
//     Set-kind parents, a reverse lookup with no index to keep in sync;
//   - the resource is wanted exactly when its holder set is non-empty.
//
// Acquire and Release report first/last: whether this call made the
// holder set non-empty or empty. These are hints about a transition, and
// an effect outside the graph (say, adding a rule in another system) must NOT be driven
// only from them: a crash between commit and effect would lose the effect
// forever. Drive effects from the level instead (Held), with a reconciler
// that compares desired and actual state and repeats. Likewise "first ==
// false" means the resource is wanted, not that its effect is already in
// place; a client that needs the effect must wait for the reconciler.
//
// Sessions are ephemeral. A client's death is detected by whoever owns the
// connection (the host) and answered with CloseSession; the host's own
// death is answered at startup with CloseAllSessions, since every client
// connection died with it.
//
// A session holds a given resource at most once (Sets are idempotent).
// Several independent holds by one session would need the occurrence-
// descriptor pattern of theorystate.md section 75.
//
// Every mutator takes a Transactor, so it works standalone and composes
// inside a larger transaction; inside one, first/last/freed are
// provisional until the outermost commit (see Transactor). Like every
// registry here, LeaseRegistry stores no graph reference (section 90).
type LeaseRegistry struct {
	sets        *SetRegistry
	allSessions NodeID
}

// NewLeaseRegistry creates a LeaseRegistry over graph. sets holds and
// resolves the holder sets; allSessions tags session nodes and must
// already exist (NameAllSessions via NameRegistry.BootstrapNames).
func NewLeaseRegistry(graph GraphAPI, sets *SetRegistry, allSessions NodeID) (*LeaseRegistry, error) {
	if existsErr := requireExists(graph, allSessions); existsErr != nil {
		return nil, existsErr
	}

	return &LeaseRegistry{
		sets:        sets,
		allSessions: allSessions,
	}, nil
}

// IsSession reports whether id is currently tagged (AllSessions, id).
func (l *LeaseRegistry) IsSession(graph GraphReader, id NodeID) (bool, error) {
	has, err := graph.HasRelationship(l.allSessions, id)
	return has, wrapInterfaceErr(err)
}

// requireSession checks that session exists and is tagged
// (AllSessions, session), returning ErrNodeNotFound or ErrNotSession
// otherwise.
func (l *LeaseRegistry) requireSession(graph GraphReader, session NodeID) error {
	return requireTagged(graph, session, l.allSessions, ErrNotSession)
}

// NewSession creates a fresh node and tags it (AllSessions, id).
func (l *LeaseRegistry) NewSession(graph Transactor) (NodeID, error) {
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		return createTaggedNodeTx(tx, l.allSessions)
	})
}

// Sessions returns every currently open session, sorted ascending. The
// number of live sessions is small by nature (it is bounded by the number
// of connected clients), so this reads the tag's children in full.
func (l *LeaseRegistry) Sessions(graph GraphReader) ([]NodeID, error) {
	outgoing, err := graph.FindOutgoing(l.allSessions)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}

	sessions := make([]NodeID, 0, len(outgoing))
	for _, rel := range outgoing {
		sessions = append(sessions, rel.To)
	}

	return sessions, nil
}

// isEmpty reports whether resource's holder set has no members. resource
// must be a Set (ErrNotSet / ErrNodeNotFound otherwise).
func (l *LeaseRegistry) isEmpty(graph GraphReader, resource NodeID) (bool, error) {
	size, err := l.sets.Size(graph, resource)
	return size == 0, err
}

// Held reports whether resource currently has at least one holder: the
// level-triggered "desired state" a reconciler should compare against the
// outside world.
func (l *LeaseRegistry) Held(graph GraphReader, resource NodeID) (bool, error) {
	empty, err := l.isEmpty(graph, resource)
	if err != nil {
		return false, err
	}

	return !empty, nil
}

// Holders returns resource's current holders (sessions), sorted
// ascending.
func (l *LeaseRegistry) Holders(graph GraphReader, resource NodeID) ([]NodeID, error) {
	return l.sets.Members(graph, resource)
}

// Acquire makes session a holder of resource. first reports whether this
// call made the holder set non-empty (resource was unheld and session was
// not already a holder); acquiring again is an idempotent no-op reporting
// false. session must be an open session and resource a Set.
//
// first is a transition hint, not "the effect is in place": see the
// LeaseRegistry doc comment.
func (l *LeaseRegistry) Acquire(graph Transactor, resource, session NodeID) (first bool, err error) {
	return transactBool(graph, func(tx Tx) (bool, error) {
		if requireErr := l.requireSession(tx, session); requireErr != nil {
			return false, requireErr
		}

		wasEmpty, emptyErr := l.isEmpty(tx, resource)
		if emptyErr != nil {
			return false, emptyErr
		}

		added, addErr := l.sets.Add(tx, resource, session)
		if addErr != nil {
			return false, addErr
		}

		return added && wasEmpty, nil
	})
}

// Release removes session from resource's holders. last reports whether
// this call made the holder set empty (session was a holder and nobody
// else is). Releasing a hold that was never taken, or was already
// released, is a no-op reporting false. session must be an open session
// and resource a Set.
func (l *LeaseRegistry) Release(graph Transactor, resource, session NodeID) (last bool, err error) {
	return transactBool(graph, func(tx Tx) (bool, error) {
		if requireErr := l.requireSession(tx, session); requireErr != nil {
			return false, requireErr
		}

		removed, removeErr := l.sets.Remove(tx, resource, session)
		if removeErr != nil {
			return false, removeErr
		}
		if !removed {
			return false, nil
		}

		return l.isEmpty(tx, resource)
	})
}

// heldBy returns every resource session currently holds: the Set-kind
// parents of session, sorted ascending. Other parents (the AllSessions tag,
// the virtual ROOT parent under a RootGraph, anything unrelated) are not
// Sets and are skipped.
func (l *LeaseRegistry) heldBy(graph GraphReader, session NodeID) ([]NodeID, error) {
	incoming, err := graph.FindIncoming(session)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}

	resources := make([]NodeID, 0, len(incoming))

	for _, rel := range incoming {
		isSet, setErr := l.sets.IsSet(graph, rel.From)
		if setErr != nil {
			return nil, setErr
		}
		if isSet {
			resources = append(resources, rel.From)
		}
	}

	return resources, nil
}

// CloseSession ends session: every hold it has is released and the
// session node is deleted, all in one transaction. freed lists the
// resources whose holder set this made empty, in ascending resource
// order, which is exactly what a host must hand to its reconciler when a
// client dies.
//
// Deletion follows the usual "delete only if empty" rule (theorystate.md
// section 18): if something other than holder sets still references the
// session, CloseSession fails with ErrNodeNotEmpty and nothing at all is
// changed, the holds included.
func (l *LeaseRegistry) CloseSession(graph Transactor, session NodeID) (freed []NodeID, err error) {
	return transactValue(graph, func(tx Tx) ([]NodeID, error) {
		if requireErr := l.requireSession(tx, session); requireErr != nil {
			return nil, requireErr
		}

		resources, heldErr := l.heldBy(tx, session)
		if heldErr != nil {
			return nil, heldErr
		}

		emptied := make([]NodeID, 0, len(resources))

		for _, resource := range resources {
			removed, removeErr := l.sets.Remove(tx, resource, session)
			if removeErr != nil {
				return nil, removeErr
			}
			if !removed {
				continue
			}

			empty, emptyErr := l.isEmpty(tx, resource)
			if emptyErr != nil {
				return nil, emptyErr
			}
			if empty {
				emptied = append(emptied, resource)
			}
		}

		if deleteErr := untagAndDeleteNodeTx(tx, session, l.allSessions); deleteErr != nil {
			return nil, deleteErr
		}

		return emptied, nil
	})
}

// CloseAllSessions closes every open session in one transaction and
// returns the resources that lost their last holder (in closing order,
// each once). A host calls it at startup: sessions are ephemeral, and
// every client connection died with the previous process. It is
// fail-closed: if any session cannot be closed, none is.
func (l *LeaseRegistry) CloseAllSessions(graph Transactor) (freed []NodeID, err error) {
	return transactValue(graph, func(tx Tx) ([]NodeID, error) {
		sessions, sessionsErr := l.Sessions(tx)
		if sessionsErr != nil {
			return nil, sessionsErr
		}

		emptied := make([]NodeID, 0)

		for _, session := range sessions {
			closed, closeErr := l.CloseSession(tx, session)
			if closeErr != nil {
				return nil, closeErr
			}

			emptied = append(emptied, closed...)
		}

		return emptied, nil
	})
}

// ---------------------------------------------------------------------
// Host: the single owner process of one graph (theorystate.md sections 107
// and 112). It owns the store, the GraphActor, every registry and every
// Checker; clients (goroutines now, other processes and machines later)
// connect to it and call named operations, each run as one Transact.

var (
	// ErrHostClosed is returned for any operation attempted on, or still
	// running when, the Host is closed.
	ErrHostClosed = errors.New("host is closed")

	// ErrConnClosed is returned for any operation on a connection that has
	// been closed, dropped by its context, or expired by the keepalive TTL.
	ErrConnClosed = errors.New("connection is closed")

	// ErrUnknownResource is returned for a resource name that was never
	// registered with Host.RegisterResource.
	ErrUnknownResource = errors.New("resource is not registered")

	// ErrResourceRegistered is returned when a resource name is registered
	// twice.
	ErrResourceRegistered = errors.New("resource is already registered")

	// ErrResourceName is returned for an empty resource name, and for
	// PlanResource, the name a wire Authorizer is asked about for a plan,
	// which must never be a real resource.
	ErrResourceName = errors.New("resource name must not be empty or reserved")

	// ErrHoldLost is returned by WaitApplied when the session no longer
	// holds the resource it is waiting for.
	ErrHoldLost = errors.New("the session no longer holds the resource")

	// ErrEffectNotConverged is returned (inside the reconciler's error
	// report) when an effect's Apply/Remove succeeded but Present still
	// disagrees.
	ErrEffectNotConverged = errors.New("effect did not reach the wanted state")

	// ErrEffectTimeout is returned (inside the reconciler's error report)
	// when a call to an Effect did not return within
	// HostConfig.EffectTimeout and was abandoned. The call may still be
	// running; see ErrEffectBusy.
	ErrEffectTimeout = errors.New("effect call timed out")

	// ErrEffectBusy is returned (inside the reconciler's error report) when
	// a call to an Effect was not started because an earlier, abandoned
	// call to the same effect has not returned yet. It keeps the effect
	// from ever seeing two overlapping calls.
	ErrEffectBusy = errors.New("an earlier call to the effect has not returned")
)

// resourcePrefix namespaces resource names inside the NameRegistry, so they
// can never collide with a foundational name.
const resourcePrefix = "resource/"

// Effect is something outside the graph that exists exactly while a
// resource is held (a rule in some other system, say). dml defines only
// this contract; implementations, and any vocabulary specific to what they
// manage, belong to the consumer (theorystate.md section 112). The Host's
// reconciler drives it from the level (LeaseRegistry.Held), never only from
// acquire/release transitions, so every method must be idempotent and safe
// to repeat after a crash: Apply of a present effect and Remove of an absent
// one are successes. Present reports the real state of the outside world,
// not a cached belief. Implementations must not touch the graph. Each call
// runs under HostConfig.EffectTimeout: honor ctx where possible, since a call
// that ignores it is abandoned when the deadline passes (it cannot be killed,
// and no further call to the same effect starts until it returns).
type Effect interface {
	// Present reports whether the effect is currently in place.
	Present(ctx context.Context) (bool, error)
	// Apply puts the effect in place.
	Apply(ctx context.Context) error
	// Remove takes the effect away.
	Remove(ctx context.Context) error
}

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

// Defaults for HostConfig fields left at zero.
const (
	defaultKeepaliveTTL    = 15 * time.Second
	defaultTeardownGrace   = 2 * time.Second
	defaultReapInterval    = time.Second
	defaultResyncInterval  = 30 * time.Second
	defaultRetryInterval   = 2 * time.Second
	defaultShutdownTimeout = 10 * time.Second
	defaultEffectTimeout   = 30 * time.Second
)

// HostConfig configures a Host. Only Path is required.
//
// For KeepaliveTTL, TeardownGrace and EffectTimeout, zero means "use the
// default" and a negative value means "disabled". The other durations must
// be positive;
// zero or negative means the default.
type HostConfig struct {
	// Path is the bbolt file. Exactly one Host may own it.
	Path string

	// KeepaliveTTL is how long a connection may stay silent before the Host
	// treats it as dead. Connection drop is the primary liveness signal;
	// this is the backstop for partitions where no drop is ever seen. The
	// clock lives in host memory only. Any operation counts as a sign of
	// life, as does Conn.Keepalive. A client that waits for longer than the
	// TTL inside WaitApplied must keep calling Keepalive from elsewhere.
	KeepaliveTTL time.Duration

	// TeardownGrace is how long a resource must stay unheld before its
	// effect is removed, so that release-then-acquire races do not flap the
	// effect. Applying is never delayed.
	TeardownGrace time.Duration

	// ReapInterval is how often expired connections are collected and failed
	// session closes are retried.
	ReapInterval time.Duration

	// ResyncInterval is the longest the reconciler sleeps: every resource is
	// re-compared with the outside world at least this often, which repairs
	// drift.
	ResyncInterval time.Duration

	// RetryInterval is how soon a failed effect is retried.
	RetryInterval time.Duration

	// ShutdownTimeout bounds the final reconcile pass in Close.
	ShutdownTimeout time.Duration

	// EffectTimeout bounds each Effect.Present, Apply and Remove call. A call
	// that does not return in time is abandoned and reported as
	// ErrEffectTimeout, so one hung effect cannot stall the reconciler (which
	// serves every resource) or Close. An abandoned call cannot be killed if
	// it ignores its context, so no further call to that effect starts until
	// it returns (ErrEffectBusy). Disabled (negative), calls are made
	// directly, and Close can then wait for an effect that ignores its
	// context.
	EffectTimeout time.Duration

	// VerifyPageSize is the page size of the startup VerifyAll sweep (zero
	// means VerifyAll's own default).
	VerifyPageSize int

	// OnError receives errors from background work (effects that failed,
	// sessions that could not be closed). Nil means log.Printf.
	OnError func(op string, err error)
}

// durationOrDefault implements the "zero is default, negative is disabled"
// rule: it returns the default for zero, 0 (disabled) for a negative value,
// and v otherwise.
func durationOrDefault(v, def time.Duration) time.Duration {
	switch {
	case v == 0:
		return def
	case v < 0:
		return 0
	default:
		return v
	}
}

// positiveOrDefault returns v if it is positive and def otherwise.
func positiveOrDefault(v, def time.Duration) time.Duration {
	if v <= 0 {
		return def
	}

	return v
}

func defaultHostOnError(op string, err error) {
	ilog.Printf("dml host: %s: %v", op, err)
}

// withDefaults returns c with every unset field replaced by its default.
// After this, KeepaliveTTL and TeardownGrace of 0 mean "disabled".
func (c HostConfig) withDefaults() HostConfig {
	c.KeepaliveTTL = durationOrDefault(c.KeepaliveTTL, defaultKeepaliveTTL)
	c.TeardownGrace = durationOrDefault(c.TeardownGrace, defaultTeardownGrace)
	c.EffectTimeout = durationOrDefault(c.EffectTimeout, defaultEffectTimeout)
	c.ReapInterval = positiveOrDefault(c.ReapInterval, defaultReapInterval)
	c.ResyncInterval = positiveOrDefault(c.ResyncInterval, defaultResyncInterval)
	c.RetryInterval = positiveOrDefault(c.RetryInterval, defaultRetryInterval)
	c.ShutdownTimeout = positiveOrDefault(c.ShutdownTimeout, defaultShutdownTimeout)

	if c.OnError == nil {
		c.OnError = defaultHostOnError
	}

	return c
}

// AcquireResult is what Client.Acquire returns.
type AcquireResult struct {
	// First reports whether this call made the resource's holder set
	// non-empty. It is a transition hint: it means "wanted", never "in
	// effect".
	First bool

	// Mark is the token to pass to WaitApplied. It identifies the last
	// reconcile pass of the resource that may have started before this
	// acquire committed; the effect is applied for this hold once a later
	// pass has completed with it present.
	Mark uint64
}

// Client is the named-operation surface the Host offers one connection.
// Every method is one request to the host, run there as one Transact (or a
// short read). It deliberately contains no closures and no NodeIDs, so a
// network transport can implement it by sending each call to the Host and
// the in-process Conn is a client exactly like a remote one will be.
type Client interface {
	// Acquire makes the connection's session a holder of resource.
	Acquire(ctx context.Context, resource string) (AcquireResult, error)

	// Release drops the hold; last reports whether nobody else holds it.
	Release(ctx context.Context, resource string) (last bool, err error)

	// WaitApplied blocks until the effect of resource is confirmed in place
	// for a hold taken by the Acquire that returned mark, the hold is lost
	// (ErrHoldLost), the connection dies (ErrConnClosed), the host closes
	// (ErrHostClosed), or ctx ends (the error then also carries the last
	// effect failure, if any).
	WaitApplied(ctx context.Context, resource string, mark uint64) error

	// Keepalive tells the host the client is alive (any operation does).
	Keepalive(ctx context.Context) error

	// Close ends the connection and releases everything it holds.
	Close() error

	// Done is closed when the connection has ended for any reason.
	Done() <-chan struct{}
}

// AcquireAndWait acquires resource and waits until its effect is in place.
// It is built only on the Client interface, so it works over any transport.
func AcquireAndWait(ctx context.Context, client Client, resource string) error {
	result, err := client.Acquire(ctx, resource)
	if err != nil {
		return fmt.Errorf("acquiring %q: %w", resource, err)
	}

	return wrapInterfaceErr(client.WaitApplied(ctx, resource, result.Mark))
}

// passOutcome is a snapshot of one resource's reconcile progress.
type passOutcome struct {
	completed uint64
	applied   bool
	err       error
	changed   <-chan struct{}
}

// hostResource is one registered resource: its holder Set, its optional
// effect, and the reconciler's progress on it.
type hostResource struct {
	name   string
	node   NodeID
	effect Effect

	// timeout is HostConfig.EffectTimeout (0: calls are made directly), and
	// busy is set while a call to effect is running, including one that was
	// abandoned after the timeout (see callEffect).
	timeout time.Duration
	busy    atomic.Bool

	mu        sync.Mutex
	started   uint64
	completed uint64
	applied   bool
	lastErr   error
	changed   chan struct{}

	// Touched only by the reconciler goroutine (and by Close after that
	// goroutine has exited): when the resource first became unheld while its
	// effect was still present.
	unheld      bool
	unheldSince time.Duration
}

func newHostResource(name string, node NodeID, effect Effect, timeout time.Duration) *hostResource {
	return &hostResource{name: name, node: node, effect: effect, timeout: timeout, changed: make(chan struct{})}
}

// beginPass numbers a new reconcile pass. It must be called before the
// pass reads the holder set.
func (r *hostResource) beginPass() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.started++

	return r.started
}

// mark returns the number of the latest pass that has begun. A client calls
// it after its hold has committed: every pass numbered above it begins after
// the commit and therefore sees the hold.
func (r *hostResource) mark() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.started
}

// finishPass records the outcome of pass and wakes every waiter. applied
// means: the resource was held, and its effect is confirmed present.
func (r *hostResource) finishPass(pass uint64, applied bool, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.completed = pass
	r.applied = applied
	r.lastErr = err

	close(r.changed)
	r.changed = make(chan struct{})
}

func (r *hostResource) outcome() passOutcome {
	r.mu.Lock()
	defer r.mu.Unlock()

	return passOutcome{completed: r.completed, applied: r.applied, err: r.lastErr, changed: r.changed}
}

// callEffect runs fn, one call to r.effect, under r.timeout (op names the
// call in errors). With no timeout fn is called directly.
//
// Otherwise fn runs on its own goroutine, so an effect that ignores its
// context cannot hold the caller past the deadline: the call is then
// abandoned and ErrEffectTimeout returned. The goroutine cannot be killed,
// so busy stays set until fn returns, and a new call meanwhile fails at once
// with ErrEffectBusy instead of overlapping the stuck one. If ctx (the
// host's) ends first, the call is abandoned the same way. Whatever an
// abandoned call eventually does is seen by a later pass, since Present
// reports real state.
func (r *hostResource) callEffect(ctx context.Context, op string, fn func(ctx context.Context) error) error {
	if r.timeout <= 0 {
		return fn(ctx)
	}

	if !r.busy.CompareAndSwap(false, true) {
		return fmt.Errorf("%w: %s of %q", ErrEffectBusy, op, r.name)
	}

	callCtx, cancel := context.WithTimeout(ctx, r.timeout)
	done := make(chan error, 1)

	go func() {
		callErr := fn(callCtx)

		// The result goes out before cancel: cancelling callCtx wakes the
		// select below through callCtx.Done(), and if that could happen
		// before the result is in done, a call that returned in time would
		// be reported as timed out. done is buffered, so this never blocks,
		// even when the caller has already abandoned the call.
		r.busy.Store(false)

		done <- callErr

		cancel()
	}()

	select {
	case callErr := <-done:
		return callErr
	case <-callCtx.Done():
	}

	// The call may have returned at the same moment the deadline passed.
	select {
	case callErr := <-done:
		return callErr
	default:
	}

	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%s of %q abandoned: %w", op, r.name, ctxErr)
	}

	return fmt.Errorf("%w: %s of %q did not return within %v", ErrEffectTimeout, op, r.name, r.timeout)
}

// checkPresent asks the effect whether it is in place, under the deadline.
func (r *hostResource) checkPresent(ctx context.Context) (bool, error) {
	var present bool

	err := r.callEffect(ctx, "present", func(callCtx context.Context) error {
		var presentErr error

		present, presentErr = r.effect.Present(callCtx)

		return wrapInterfaceErr(presentErr)
	})
	if err != nil {
		return false, err
	}

	return present, nil
}

// converge applies or removes the effect and verifies the result against
// the real state.
func (r *hostResource) converge(ctx context.Context, want bool) error {
	op, run := "remove", r.effect.Remove
	if want {
		op, run = "apply", r.effect.Apply
	}

	if opErr := r.callEffect(ctx, op, run); opErr != nil {
		return fmt.Errorf("changing effect of %q to present=%v: %w", r.name, want, opErr)
	}

	present, err := r.checkPresent(ctx)
	if err != nil {
		return fmt.Errorf("verifying effect of %q: %w", r.name, err)
	}

	if present != want {
		return fmt.Errorf("%w: %q, wanted present=%v", ErrEffectNotConverged, r.name, want)
	}

	return nil
}

// Host owns one graph: the bbolt store, the GraphActor over RootGraph over
// BoltGraph, every registry and every Checker (theorystate.md sections 107
// and 112). Create it with OpenHost; it must be closed exactly once with
// Close.
//
// Liveness. Every client connection owns one session node. A connection is
// dropped when its context ends (the in-process stand-in for a socket
// closing), when Close is called on it, or when it stays silent past
// KeepaliveTTL. Dropping calls LeaseRegistry.CloseSession and wakes the
// reconciler. A session whose close fails is retried by the reaper.
//
// Effects. The reconciler compares, for a registered resource, the desired
// state (LeaseRegistry.Held) with the actual one (Effect.Present) and
// converges them. A wake-up looks only at the resources a change marked
// dirty (an acquire, a release, a session that freed it, its registration)
// and at those whose own deadline has come (a teardown grace running, a
// failed effect waiting for RetryInterval). Every ResyncInterval it looks at
// every resource, which repairs drift nobody touched.
type Host struct {
	cfg   HostConfig
	store *BoltGraph
	actor *GraphActor
	names *NameRegistry
	reg   *Registries
	root  NodeID

	epoch  time.Time
	stop   chan struct{}
	cancel context.CancelFunc
	kickCh chan struct{}
	bg     sync.WaitGroup
	ops    sync.WaitGroup

	mu      sync.Mutex
	closed  bool
	conns   map[NodeID]*Conn
	closing map[NodeID]struct{}

	resMu     sync.RWMutex
	resources map[string]*hostResource
	byNode    map[NodeID]*hostResource

	// dirty is the set of resources something happened to since the
	// reconciler last took it (see markDirty).
	dirtyMu sync.Mutex
	dirty   map[*hostResource]struct{}

	// operations holds the named handlers clients may run (operations.go).
	// Its zero value is ready to use.
	operations operationTable
}

// OpenHost performs the startup order of theorystate.md sections 110 and
// 112 and returns a running Host:
//
//  1. OpenBoltGraph (single-open guard, format version check);
//  2. CheckStore (physical and layout check);
//  3. NameRegistry.LoadNames and the ROOT node, on the raw store: RootGraph
//     needs ROOT to exist before the stack can be built;
//  4. GraphActor over RootGraph over BoltGraph (actor outermost);
//  5. BootstrapNames(FoundationalNames) and every registry (this registers
//     the Checkers);
//  6. CloseAllSessions: sessions are ephemeral, every client died with the
//     previous process;
//  7. VerifyAll, ListRegistry.VerifyStructures and NameRegistry.VerifyBindings,
//     all fail-closed.
//
// Any failure closes whatever was opened and returns the error.
func OpenHost(cfg HostConfig) (*Host, error) {
	if cfg.Path == "" {
		return nil, errors.New("host: HostConfig.Path is required")
	}

	cfg = cfg.withDefaults()

	store, err := OpenBoltGraph(cfg.Path)
	if err != nil {
		return nil, fmt.Errorf("host startup: %w", err)
	}

	h, startErr := startHost(cfg, store)
	if startErr != nil {
		// store.Close() returns nil on success, and joinErrors drops nils,
		// so this is just startErr unless closing failed too.
		return nil, joinErrors(startErr, store.Close())
	}

	return h, nil
}

// startHost runs startup steps 2 to 4 and hands over to finishHostStart.
func startHost(cfg HostConfig, store *BoltGraph) (*Host, error) {
	if checkErr := store.CheckStore(); checkErr != nil {
		return nil, fmt.Errorf("host startup: checking the store: %w", checkErr)
	}

	names := NewNameRegistry(store)

	if loadErr := names.LoadNames(store); loadErr != nil {
		return nil, fmt.Errorf("host startup: loading names: %w", loadErr)
	}

	root, rootErr := names.EnsureNamedNode(store, NameRoot)
	if rootErr != nil {
		return nil, fmt.Errorf("host startup: ensuring ROOT: %w", rootErr)
	}

	rootGraph, layerErr := NewRootGraph(store, root)
	if layerErr != nil {
		return nil, fmt.Errorf("host startup: building the ROOT layer: %w", layerErr)
	}

	actor := NewGraphActor(rootGraph)

	h, finishErr := finishHostStart(cfg, store, actor, names, root)
	if finishErr != nil {
		actor.Close()
		return nil, finishErr
	}

	return h, nil
}

// finishHostStart runs startup steps 5 to 7 and starts the background
// goroutines. Nothing is started before every fallible step has passed.
func finishHostStart(cfg HostConfig, store *BoltGraph, actor *GraphActor, names *NameRegistry, root NodeID) (*Host, error) {
	ids, idsErr := names.BootstrapNames(actor, FoundationalNames)
	if idsErr != nil {
		return nil, fmt.Errorf("host startup: bootstrapping names: %w", idsErr)
	}

	if tagErr := TagFoundational(actor, ids, root); tagErr != nil {
		return nil, fmt.Errorf("host startup: tagging foundational names: %w", tagErr)
	}

	reg, regErr := NewRegistries(actor, names, ids)
	if regErr != nil {
		return nil, fmt.Errorf("host startup: constructing registries: %w", regErr)
	}

	if _, sweepErr := reg.Leases.CloseAllSessions(actor); sweepErr != nil {
		return nil, fmt.Errorf("host startup: closing stale sessions: %w", sweepErr)
	}

	if verifyErr := VerifyAll(actor, cfg.VerifyPageSize); verifyErr != nil {
		return nil, fmt.Errorf("host startup: %w", verifyErr)
	}

	for _, lists := range []OrderedList{reg.Lists, reg.SlottedLists} {
		if listsErr := lists.VerifyStructures(actor); listsErr != nil {
			return nil, fmt.Errorf("host startup: %w", listsErr)
		}
	}

	if bindErr := names.VerifyBindings(actor); bindErr != nil {
		return nil, fmt.Errorf("host startup: verifying names: %w", bindErr)
	}

	h := &Host{
		cfg:       cfg,
		store:     store,
		actor:     actor,
		names:     names,
		reg:       reg,
		root:      root,
		epoch:     time.Now(),
		stop:      make(chan struct{}),
		kickCh:    make(chan struct{}, 1),
		conns:     make(map[NodeID]*Conn),
		closing:   make(map[NodeID]struct{}),
		resources: make(map[string]*hostResource),
		byNode:    make(map[NodeID]*hostResource),
		dirty:     make(map[*hostResource]struct{}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel

	h.bg.Go(func() { h.reconcileLoop(ctx) })
	h.bg.Go(h.reapLoop)

	return h, nil
}

// Registries returns the registries, for the owner process's own use.
func (h *Host) Registries() *Registries {
	return h.reg
}

// Graph returns the graph handle for the owner process's own use. It is
// guarded (see hostGraph): after Close every call returns ErrHostClosed
// instead of panicking, Close waits for calls still running, and a write that
// changes a resource's holder set wakes the reconciler. Pass it to registry
// methods as usual; never use it from inside a Transact closure (use the tx
// the closure was given), exactly as with a GraphActor.
func (h *Host) Graph() GraphAPI {
	return hostGraph{host: h}
}

// monoNow is the host's monotonic clock: time since the host started.
func (h *Host) monoNow() time.Duration {
	return time.Since(h.epoch)
}

// report hands a background error to cfg.OnError.
func (h *Host) report(op string, err error) {
	if err != nil {
		h.cfg.OnError(op, err)
	}
}

// enter registers an operation that uses the actor. It fails once Close has
// begun, and Close waits for every entered operation, so no operation can
// reach the actor after it has been closed. Every successful enter must be
// paired with leave.
func (h *Host) enter() bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return false
	}

	h.ops.Add(1)

	return true
}

func (h *Host) leave() {
	h.ops.Done()
}

// kickReconciler wakes the reconciler. The channel is buffered with one
// token, so a kick that arrives during a pass triggers another pass after
// it: a pass that began before a commit can never be the last one.
func (h *Host) kickReconciler() {
	select {
	case h.kickCh <- struct{}{}:
	default:
	}
}

// markDirty tells the reconciler that something happened to resources that
// may change what they should be, and wakes it. Call it AFTER the change has
// committed (and, for Acquire, after the mark was read): the reconciler clears
// a mark before it numbers its next pass, so any pass numbered after the
// clear sees the change, and WaitApplied waits for such a pass.
func (h *Host) markDirty(resources ...*hostResource) {
	h.dirtyMu.Lock()

	for _, r := range resources {
		h.dirty[r] = struct{}{}
	}

	h.dirtyMu.Unlock()

	h.kickReconciler()
}

// markDirtyNodes is markDirty for the registered resources whose holder Set
// is one of nodes. Nodes that are not registered resources are ignored.
func (h *Host) markDirtyNodes(nodes []NodeID) {
	h.resMu.RLock()

	found := make([]*hostResource, 0, len(nodes))

	for _, node := range nodes {
		if r, ok := h.byNode[node]; ok {
			found = append(found, r)
		}
	}

	h.resMu.RUnlock()

	if len(found) > 0 {
		h.markDirty(found...)
	}
}

// takeDirty returns the marked resources and empties the set.
func (h *Host) takeDirty() map[*hostResource]struct{} {
	h.dirtyMu.Lock()
	defer h.dirtyMu.Unlock()

	taken := h.dirty
	h.dirty = make(map[*hostResource]struct{})

	return taken
}

// clearDirty drops r's mark. The reconciler calls it just before it numbers
// a pass for r, so a mark set after that call (a change committed after the
// pass began) survives and causes another pass.
func (h *Host) clearDirty(r *hostResource) {
	h.dirtyMu.Lock()
	defer h.dirtyMu.Unlock()

	delete(h.dirty, r)
}

// Close shuts the host down: operations stop, every connection is dropped
// and its session closed, a final reconcile pass removes every effect that
// is no longer held (without the teardown grace), and the actor and store
// are closed. It is safe to call more than once; later calls return nil.
func (h *Host) Close() error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}

	h.closed = true
	close(h.stop)
	h.mu.Unlock()

	h.cancel()
	h.bg.Wait()
	h.ops.Wait()

	// Nothing else uses the actor from here on.
	errs := h.shutdownConns()

	finalCtx, finalCancel := context.WithTimeout(context.Background(), h.cfg.ShutdownTimeout)
	h.reconcileAll(finalCtx, true)
	finalCancel()

	h.actor.Close()
	errs = append(errs, h.store.Close())

	return joinErrors(errs...)
}

// shutdownConns drops every remaining connection and retries every session
// close that had failed. Only Close calls it, after all other users of the
// actor have finished.
func (h *Host) shutdownConns() []error {
	h.mu.Lock()
	conns := slices.Collect(maps.Values(h.conns))
	pending := slices.Collect(maps.Keys(h.closing))
	h.mu.Unlock()

	errs := make([]error, 0, len(conns)+len(pending))

	for _, c := range conns {
		c.markDone()
		errs = append(errs, h.releaseConn(c))
	}

	for _, session := range pending {
		errs = append(errs, h.closeSession(session))
	}

	return errs
}

// RegisterResource declares a resource and its effect (nil for a pure
// lease with no outside effect). It creates the resource's holder Set,
// named resource/<name>, once and idempotently across restarts. Clients can
// acquire only registered resources. The effect is only held in memory, so
// it must be registered again after every restart; the reconciler then
// compares it with reality at once and removes a rule left behind by a
// previous run.
func (h *Host) RegisterResource(name string, effect Effect) error {
	if name == "" || name == PlanResource {
		return ErrResourceName
	}

	if !h.enter() {
		return ErrHostClosed
	}
	defer h.leave()

	node, err := transactValue(h.actor, func(tx Tx) (NodeID, error) {
		id, nameErr := h.names.EnsureNamedNode(tx, resourcePrefix+name)
		if nameErr != nil {
			return 0, nameErr
		}

		if tagErr := h.reg.Sets.TagAsSet(tx, id); tagErr != nil {
			return 0, tagErr
		}

		return id, nil
	})
	if err != nil {
		return fmt.Errorf("registering resource %q: %w", name, err)
	}

	res := newHostResource(name, node, effect, h.cfg.EffectTimeout)
	if !h.addResource(res) {
		return fmt.Errorf("%w: %q", ErrResourceRegistered, name)
	}

	h.markDirty(res)

	return nil
}

// addResource records r unless its name is taken, and reports whether it did.
func (h *Host) addResource(r *hostResource) bool {
	h.resMu.Lock()
	defer h.resMu.Unlock()

	if _, taken := h.resources[r.name]; taken {
		return false
	}

	h.resources[r.name] = r
	h.byNode[r.node] = r

	return true
}

func (h *Host) lookupResource(name string) (*hostResource, error) {
	h.resMu.RLock()
	defer h.resMu.RUnlock()

	r, ok := h.resources[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownResource, name)
	}

	return r, nil
}

// resourceSnapshot returns the registered resources sorted by name.
func (h *Host) resourceSnapshot() []*hostResource {
	h.resMu.RLock()
	defer h.resMu.RUnlock()

	names := slices.Sorted(maps.Keys(h.resources))
	snapshot := make([]*hostResource, 0, len(names))

	for _, name := range names {
		snapshot = append(snapshot, h.resources[name])
	}

	return snapshot
}

// Conn is one client's connection to the Host, in-process. It implements
// Client. A remote transport would implement Client over the network and
// call the same Host methods on the far side.
type Conn struct {
	host     *Host
	session  NodeID
	done     chan struct{}
	doneFlag atomic.Bool
	lastSeen atomic.Int64
}

// Compile-time assertion that *Conn satisfies Client.
var _ Client = (*Conn)(nil)

// Connect opens a connection and its session. The connection lives until
// Close is called on it, ctx ends (the in-process equivalent of a dropped
// socket), or it stays silent past KeepaliveTTL.
func (h *Host) Connect(ctx context.Context) (*Conn, error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("connecting: %w", ctxErr)
	}

	if !h.enter() {
		return nil, ErrHostClosed
	}
	defer h.leave()

	session, err := h.reg.Leases.NewSession(h.actor)
	if err != nil {
		return nil, fmt.Errorf("opening a session: %w", err)
	}

	c := &Conn{host: h, session: session, done: make(chan struct{})}
	c.touch()

	h.mu.Lock()
	h.conns[session] = c
	h.mu.Unlock()

	go h.watchConn(ctx, c)

	return c, nil
}

// watchConn drops c when its context ends, and ends when c ends first.
func (h *Host) watchConn(ctx context.Context, c *Conn) {
	select {
	case <-ctx.Done():
		h.report("dropping a connection whose context ended", h.dropConn(c))
	case <-c.done:
	}
}

// dropConn ends c and closes its session. Only the first caller does the
// work. If the host is already closing, Close closes the session instead.
func (h *Host) dropConn(c *Conn) error {
	if !c.markDone() {
		return nil
	}

	if !h.enter() {
		return nil
	}
	defer h.leave()

	return h.releaseConn(c)
}

// releaseConn forgets c and closes its session; a failed close stays
// pending for the reaper to retry.
func (h *Host) releaseConn(c *Conn) error {
	h.mu.Lock()
	delete(h.conns, c.session)
	h.closing[c.session] = struct{}{}
	h.mu.Unlock()

	return h.closeSession(c.session)
}

// closeSession releases every hold of session and deletes it, then wakes
// the reconciler. A session that is already gone counts as closed. On any
// other failure the session stays in h.closing and is retried.
func (h *Host) closeSession(session NodeID) error {
	freed, err := h.reg.Leases.CloseSession(h.actor, session)
	if err != nil && !errors.Is(err, ErrNodeNotFound) {
		return fmt.Errorf("closing session %d: %w", session, err)
	}

	h.mu.Lock()
	delete(h.closing, session)
	h.mu.Unlock()

	// The reconciler is level-driven, so freed is only a wake-up hint: mark
	// the resources that lost their last holder so it looks at them.
	h.markDirtyNodes(freed)

	return nil
}

// reapLoop expires silent connections and retries failed session closes.
func (h *Host) reapLoop() {
	ticker := time.NewTicker(h.cfg.ReapInterval)
	defer ticker.Stop()

	for {
		select {
		case <-h.stop:
			return
		case <-ticker.C:
			h.reapOnce()
		}
	}
}

func (h *Host) reapOnce() {
	for _, c := range h.expiredConns() {
		if c.markDone() {
			h.report("expiring a silent connection", h.releaseConn(c))
		}
	}

	h.mu.Lock()
	pending := slices.Collect(maps.Keys(h.closing))
	h.mu.Unlock()

	for _, session := range pending {
		h.report("retrying a session close", h.closeSession(session))
	}
}

// expiredConns returns the connections silent for longer than KeepaliveTTL.
func (h *Host) expiredConns() []*Conn {
	ttl := h.cfg.KeepaliveTTL
	if ttl <= 0 {
		return nil
	}

	now := h.monoNow()

	h.mu.Lock()
	defer h.mu.Unlock()

	var expired []*Conn

	for _, c := range h.conns {
		if now-time.Duration(c.lastSeen.Load()) > ttl {
			expired = append(expired, c)
		}
	}

	return expired
}

// reconcileLoop is the reconciler goroutine. due holds, for the resources
// that want another look without anything touching them (a teardown grace
// running, a failed effect waiting to retry), when that is; only this
// goroutine uses it.
func (h *Host) reconcileLoop(ctx context.Context) {
	due := make(map[*hostResource]time.Duration)
	lastFull := h.monoNow()

	for {
		timer := time.NewTimer(h.nextWake(lastFull, due))

		select {
		case <-h.stop:
			timer.Stop()
			return
		case <-h.kickCh:
			timer.Stop()
		case <-timer.C:
		}

		if h.monoNow()-lastFull >= h.cfg.ResyncInterval {
			h.reconcileSet(ctx, h.resourceSnapshot(), false, due)
			lastFull = h.monoNow()

			continue
		}

		h.reconcileSet(ctx, h.pendingResources(due), false, due)
	}
}

// nextWake is how long the reconciler may sleep: until the next full resync
// or the earliest resource deadline, whichever comes first.
func (h *Host) nextWake(lastFull time.Duration, due map[*hostResource]time.Duration) time.Duration {
	now := h.monoNow()
	wait := h.cfg.ResyncInterval - (now - lastFull)

	for _, at := range due {
		wait = min(wait, at-now)
	}

	return max(wait, 0)
}

// pendingResources returns, sorted by name, the resources marked dirty plus
// those whose deadline in due has come.
func (h *Host) pendingResources(due map[*hostResource]time.Duration) []*hostResource {
	pending := h.takeDirty()
	now := h.monoNow()

	for r, at := range due {
		if at <= now {
			pending[r] = struct{}{}
		}
	}

	list := slices.Collect(maps.Keys(pending))
	sort.Slice(list, func(i, j int) bool { return list[i].name < list[j].name })

	return list
}

// reconcileSet reconciles each of resources, and records in due when each
// wants another look (removing it from due if it does not). final is set by
// Close and skips the teardown grace.
func (h *Host) reconcileSet(ctx context.Context, resources []*hostResource, final bool, due map[*hostResource]time.Duration) {
	for _, r := range resources {
		h.clearDirty(r)

		if wait := h.reconcile(ctx, r, final); wait > 0 {
			due[r] = h.monoNow() + wait
		} else {
			delete(due, r)
		}
	}
}

// reconcileAll runs one pass over every registered resource. Close uses it
// for its final pass, so nothing is left to remember afterwards.
func (h *Host) reconcileAll(ctx context.Context, final bool) {
	h.reconcileSet(ctx, h.resourceSnapshot(), final, make(map[*hostResource]time.Duration))
}

// graceRemaining starts (or continues) the teardown grace of an unheld,
// still present resource and returns how much of it is left.
func (h *Host) graceRemaining(r *hostResource, final bool) time.Duration {
	if final || h.cfg.TeardownGrace <= 0 {
		return 0
	}

	now := h.monoNow()

	if !r.unheld {
		r.unheld = true
		r.unheldSince = now
	}

	return max(r.unheldSince+h.cfg.TeardownGrace-now, 0)
}

// failPass records a failed pass, reports it, and returns the retry delay.
func (h *Host) failPass(r *hostResource, pass uint64, err error) time.Duration {
	r.finishPass(pass, false, err)
	h.report("reconciling "+r.name, err)

	return h.cfg.RetryInterval
}

// reconcile brings one resource's effect in line with its holders and
// returns how soon it wants another look (0 for none).
//
// The pass is numbered before it reads the holder set. A client whose
// acquire committed before that read is covered by this pass; WaitApplied
// waits for a pass numbered above the client's mark, which cannot have begun
// before the commit.
func (h *Host) reconcile(ctx context.Context, r *hostResource, final bool) time.Duration {
	pass := r.beginPass()

	held, err := h.reg.Leases.Held(h.actor, r.node)
	if err != nil {
		return h.failPass(r, pass, fmt.Errorf("reading the holders of %q: %w", r.name, err))
	}

	if held {
		r.unheld = false
	}

	if r.effect == nil {
		r.finishPass(pass, held, nil)
		return 0
	}

	present, err := r.checkPresent(ctx)
	if err != nil {
		return h.failPass(r, pass, fmt.Errorf("checking the effect of %q: %w", r.name, err))
	}

	if held == present {
		r.unheld = false
		r.finishPass(pass, held, nil)

		return 0
	}

	if !held {
		if wait := h.graceRemaining(r, final); wait > 0 {
			r.finishPass(pass, false, nil)
			return wait
		}
	}

	if convergeErr := r.converge(ctx, held); convergeErr != nil {
		return h.failPass(r, pass, convergeErr)
	}

	r.unheld = false
	r.finishPass(pass, held, nil)

	return 0
}

// touch records a sign of life.
func (c *Conn) touch() {
	c.lastSeen.Store(int64(c.host.monoNow()))
}

func (c *Conn) isDone() bool {
	return c.doneFlag.Load()
}

// markDone ends the connection and reports whether this call was the one
// that did.
func (c *Conn) markDone() bool {
	if !c.doneFlag.CompareAndSwap(false, true) {
		return false
	}

	close(c.done)

	return true
}

// Done is closed when the connection has ended for any reason.
func (c *Conn) Done() <-chan struct{} {
	return c.done
}

// Close ends the connection and releases every hold of its session.
func (c *Conn) Close() error {
	return c.host.dropConn(c)
}

// Keepalive records a sign of life. It never touches the graph.
func (c *Conn) Keepalive(ctx context.Context) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("keepalive: %w", ctxErr)
	}

	if c.isDone() {
		return ErrConnClosed
	}

	c.touch()

	return nil
}

// enterResourceQuietly validates the request and enters the host, without
// counting it as a sign of life of the client (for work the host itself
// starts); on success the caller must call host.leave.
func (c *Conn) enterResourceQuietly(ctx context.Context, name string) (*hostResource, error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("resource %q: %w", name, ctxErr)
	}

	if c.isDone() {
		return nil, ErrConnClosed
	}

	res, err := c.host.lookupResource(name)
	if err != nil {
		return nil, err
	}

	if !c.host.enter() {
		return nil, ErrHostClosed
	}

	return res, nil
}

// enterResource is enterResourceQuietly for a request of the client, which is
// a sign of life; on success the caller must call host.leave.
func (c *Conn) enterResource(ctx context.Context, name string) (*hostResource, error) {
	res, err := c.enterResourceQuietly(ctx, name)
	if err != nil {
		return nil, err
	}

	c.touch()

	return res, nil
}

// opErr turns a graph error into the connection's own: if the connection
// ended in the meantime (so its session may be gone) the real cause is
// ErrConnClosed.
func (c *Conn) opErr(op, resource string, err error) error {
	if c.isDone() {
		return fmt.Errorf("%w (%s %q: %w)", ErrConnClosed, op, resource, err)
	}

	return fmt.Errorf("%s %q: %w", op, resource, err)
}

// Acquire implements Client: one Transact adding the session to the
// resource's holder Set.
func (c *Conn) Acquire(ctx context.Context, resource string) (AcquireResult, error) {
	res, err := c.enterResource(ctx, resource)
	if err != nil {
		return AcquireResult{}, err
	}
	defer c.host.leave()

	first, acquireErr := c.host.reg.Leases.Acquire(c.host.actor, res.node, c.session)
	if acquireErr != nil {
		return AcquireResult{}, c.opErr("acquire", resource, acquireErr)
	}

	mark := res.mark()
	c.host.markDirty(res)

	return AcquireResult{First: first, Mark: mark}, nil
}

// release removes the session from res's holder Set and wakes the reconciler.
// The caller has entered the host.
func (c *Conn) release(res *hostResource, resource string) (bool, error) {
	last, err := c.host.reg.Leases.Release(c.host.actor, res.node, c.session)
	if err != nil {
		return false, c.opErr("release", resource, err)
	}

	c.host.markDirty(res)

	return last, nil
}

// Release implements Client: one Transact removing the session from the
// resource's holder Set. The effect itself is removed by the reconciler,
// after the teardown grace.
func (c *Conn) Release(ctx context.Context, resource string) (bool, error) {
	res, err := c.enterResource(ctx, resource)
	if err != nil {
		return false, err
	}
	defer c.host.leave()

	return c.release(res, resource)
}

// isHolder reports whether this connection's session holds res.
func (c *Conn) isHolder(res *hostResource) (bool, error) {
	return c.host.reg.Sets.Contains(c.host.actor, res.node, c.session)
}

// holds reports whether this connection's session holds the named resource.
// It is for the wire server's authorization, not a request of the client.
func (c *Conn) holds(ctx context.Context, resource string) (bool, error) {
	res, err := c.enterResourceQuietly(ctx, resource)
	if err != nil {
		return false, err
	}
	defer c.host.leave()

	holder, holdErr := c.isHolder(res)
	if holdErr != nil {
		return false, c.opErr("check the hold of", resource, holdErr)
	}

	return holder, nil
}

// heldResources returns the names of the registered resources this
// connection's session holds, for WireServer.Revalidate.
func (c *Conn) heldResources() ([]string, error) {
	if c.isDone() {
		return nil, ErrConnClosed
	}

	if !c.host.enter() {
		return nil, ErrHostClosed
	}
	defer c.host.leave()

	var held []string

	for _, res := range c.host.resourceSnapshot() {
		holder, err := c.isHolder(res)
		if err != nil {
			return nil, c.opErr("list the holds of", res.name, err)
		}

		if holder {
			held = append(held, res.name)
		}
	}

	return held, nil
}

// revoke releases the session's hold of the named resource on the host's own
// initiative (WireServer.Revalidate): unlike Release it is not a sign of life
// of the client.
func (c *Conn) revoke(ctx context.Context, resource string) error {
	res, err := c.enterResourceQuietly(ctx, resource)
	if err != nil {
		return err
	}
	defer c.host.leave()

	_, releaseErr := c.release(res, resource)

	return releaseErr
}

// waitErr builds WaitApplied's context-ended error, carrying the last
// effect failure if there was one.
func waitErr(cause error, resource string, last error) error {
	if last == nil {
		return fmt.Errorf("waiting for %q to be applied: %w", resource, cause)
	}

	return fmt.Errorf("waiting for %q to be applied: %w (last effect error: %w)", resource, cause, last)
}

// WaitApplied implements Client. Each time it wakes it first checks that
// the session still holds the resource, then whether a pass numbered above
// mark has completed with the effect present. See AcquireResult.Mark.
func (c *Conn) WaitApplied(ctx context.Context, resource string, mark uint64) error {
	res, err := c.enterResource(ctx, resource)
	if err != nil {
		return err
	}
	defer c.host.leave()

	for {
		holder, holdErr := c.isHolder(res)
		if holdErr != nil {
			return c.opErr("wait for", resource, holdErr)
		}

		if !holder {
			return fmt.Errorf("%w: %q", ErrHoldLost, resource)
		}

		out := res.outcome()
		if out.completed > mark && out.applied {
			return nil
		}

		select {
		case <-ctx.Done():
			return waitErr(ctx.Err(), resource, out.err)
		case <-c.done:
			return ErrConnClosed
		case <-c.host.stop:
			return ErrHostClosed
		case <-out.changed:
		}
	}
}
