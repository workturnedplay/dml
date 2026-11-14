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
	"sort"
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
