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
	"maps"
	"slices"
)

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
