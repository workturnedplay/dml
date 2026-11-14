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
	"fmt"
)

// txOps is the minimal mutating surface needed to compose primitive
// operations atomically inside a transaction. Tx (and the concrete *Txn)
// satisfies it, including DeleteNode -- Txn.DeleteNode is itself fully
// undoable (see its doc comment), so a caller composing several deletes
// into one logical teardown does not need any special pre-verification
// step of its own; an ordinary Transact rollback already covers it.
//
// This exists so a registry's create/wire sequence can be reused both as
// a standalone top-level Graph.Transact call and as one step composed
// into a larger enclosing Transact call -- e.g. CapsuleRegistry.NewCapsule
// composing PointerRegistry's create-and-tag sequence for each of a
// capsule's three role slots -- without nesting one Graph.Transact call
// inside another. Tx.Transact now supports nested transactions, so
// registry methods themselves also compose by nesting; these txOps-based
// helpers remain as shared, DRY building blocks that run against
// whatever tx they are given, standalone or composed.
type txOps interface {
	CreateNode() (NodeID, error)
	AddRelationship(a, b NodeID) (created bool, err error)
	RemoveRelationship(a, b NodeID) (removed bool, err error)
	DeleteNode(id NodeID) error
}

// txReader is the combined surface for helpers that both compose
// mutations into an already-open transaction (txOps) and need to read
// current graph state as part of deciding what to do (GraphReader) --
// e.g. reading a slot's current target before deciding whether it must
// be replaced. A single txReader-typed parameter, rather than two
// separately threaded txOps and GraphReader parameters, is what makes it
// impossible for a caller to accidentally supply the read half from a
// different -- and, under GraphActor, potentially deadlocking -- source
// than the write half (theorystate.md section 90). Tx (and the concrete
// *Txn) satisfies txReader automatically, since it already satisfies
// both txOps and GraphReader.
type txReader interface {
	txOps
	GraphReader
}

// wrapInterfaceErr wraps an error returned directly from a call to one of
// this package's interface-typed values -- txOps
// (CreateNode/AddRelationship/RemoveRelationship/DeleteNode), or
// GraphAPI/GraphStore/GraphReader (Transact/FindOutgoing/FindIncoming/
// AddRelationship/RemoveRelationship/DeleteNode/etc.) -- before that
// error is returned from one of this file's own functions or methods.
// This exists purely to satisfy static analysis (wrapcheck), which
// cannot see through any of these interfaces to know that their only
// implementations in this package (*Graph, *Txn) return nothing but this
// package's own sentinel errors (ErrNodeNotFound, ErrNodeNotEmpty,
// ErrNodeIDExhausted, and friends). Wrapping with %w preserves full
// errors.Is/errors.As compatibility -- every existing errors.Is check
// against those sentinels continues to work unchanged -- so this adds no
// behavior, only a satisfied linter. A nil err must stay nil:
// fmt.Errorf("%w", nil) would otherwise turn a successful call into a
// non-nil error.
func wrapInterfaceErr(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w", err)
}

// joinErrors is errors.Join with its result passed through
// wrapInterfaceErr, so wrapcheck does not flag the "unwrapped error from an
// external package". Nil errors are dropped, and if every error is nil the
// result is nil. errors.Is/errors.As still see every joined error, and the
// message is unchanged.
func joinErrors(errs ...error) error {
	return wrapInterfaceErr(errors.Join(errs...))
}

// transactValue runs step as one Transact call on graph and returns its
// result, or (zero value, err) if step failed or the transaction was
// declined and rolled back. If graph is a GraphAPI this is an outermost
// transaction; if it is a Tx, a nested one inside the enclosing
// transaction. step may be run more than once by a retrying backend (see
// the Transactor contract), so its result is overwritten on every run and
// only the final, committed run's value is returned.
func transactValue[T any](graph Transactor, step func(tx Tx) (T, error)) (T, error) {
	var result T

	err := graph.Transact(func(tx Tx) error {
		var stepErr error
		result, stepErr = step(tx)
		return stepErr
	})
	if err != nil {
		var zero T
		return zero, wrapInterfaceErr(err)
	}

	return result, nil
}

// transactBool is transactValue for a bool result.
func transactBool(graph Transactor, step func(tx Tx) (bool, error)) (bool, error) {
	return transactValue(graph, step)
}

// tagNodeTx adds the tagging relationship (tag, id) against tx. This is
// the single-relationship-add step shared by createTaggedNodeTx below and
// by any caller that needs to apply more than one tag to a single node --
// e.g. CompositeSetRegistry.AddOperand's operand descriptors, which carry
// two independent axis tags on the same freshly created node.
func tagNodeTx(tx txOps, tag, id NodeID) error {
	return addRelationshipTx(tx, tag, id)
}

// createNodeTx is tx.CreateNode with its error wrapped via
// wrapInterfaceErr. Every helper and Transact closure in this file
// should create nodes through this rather than returning an error
// straight from the interface method, which wrapcheck flags.
func createNodeTx(tx txOps) (NodeID, error) {
	id, err := tx.CreateNode()
	return id, wrapInterfaceErr(err)
}

// addRelationshipTx is tx.AddRelationship with its error wrapped and its
// created bool discarded. Use it wherever the caller does not care
// whether the relationship already existed.
func addRelationshipTx(tx txOps, from, to NodeID) error {
	_, err := tx.AddRelationship(from, to)
	return wrapInterfaceErr(err)
}

// removeRelationshipTx is tx.RemoveRelationship with its error wrapped
// and its removed bool discarded.
func removeRelationshipTx(tx txOps, from, to NodeID) error {
	_, err := tx.RemoveRelationship(from, to)
	return wrapInterfaceErr(err)
}

// deleteNodeTx is tx.DeleteNode with its error wrapped. The wrapping
// preserves errors.Is, so callers can still test for ErrNodeNotEmpty.
func deleteNodeTx(tx txOps, id NodeID) error {
	return wrapInterfaceErr(tx.DeleteNode(id))
}

// untagAndDeleteNodeTx removes each (tag, node) relationship and then
// deletes node, all against tx. The tags are ordinary relationships
// *into* node, so they must go before the delete can succeed; if the
// delete then fails (e.g. ErrNodeNotEmpty), the enclosing Transact's
// rollback restores every tag. Shared by DeleteList, DeleteSet,
// DeleteCompositeSet and DeleteCompositeSetLog.
func untagAndDeleteNodeTx(tx txOps, node NodeID, tags ...NodeID) error {
	for _, tag := range tags {
		if err := removeRelationshipTx(tx, tag, node); err != nil {
			return err
		}
	}

	return deleteNodeTx(tx, node)
}

// createTaggedNodeTx creates a fresh node and tags it via (tag, id),
// against tx. This is the shared "mint a fresh, tag-identified node"
// sequence used throughout this file: PointerRegistry.NewPointer's
// Pointer-kind tagging (via newPointerTx below), and the subject-slot /
// metadata / target-slot creation inside ensureMetadataWithSubjectSlot
// and PointerMetadataRegistryD.SetTarget.
func createTaggedNodeTx(tx txOps, tag NodeID) (NodeID, error) {
	id, err := createNodeTx(tx)
	if err != nil {
		return 0, err
	}

	if err2 := tagNodeTx(tx, tag, id); err2 != nil {
		return 0, err2
	}

	return id, nil
}

// newPointerTx creates a fresh NodeID and tags it Pointer-kind via
// (allPointers, id), against whatever txOps tx is given. This is the
// create-and-tag sequence behind PointerRegistry.NewPointer, factored out
// so a larger composite operation (e.g. CapsuleRegistry.NewCapsule) can
// compose it into its own enclosing Graph.Transact call instead of
// PointerRegistry opening a second, nested one.
func newPointerTx(tx txOps, allPointers NodeID) (NodeID, error) {
	return createTaggedNodeTx(tx, allPointers)
}

// setPointerTargetTx sets id's target to target within tx, given id's
// current target state (current, hasCurrent) as already determined by the
// caller. It performs the remove-old/add-new sequence directly against
// tx, so it can be composed into a larger enclosing transaction (e.g. a
// future list operation rewiring an existing capsule slot's target)
// without nesting Graph.Transact calls. Callers are responsible for the
// "already has this target -> no-op" idempotency check before calling
// this, exactly as PointerRegistry.SetTarget already does.
func setPointerTargetTx(tx txOps, id, current NodeID, hasCurrent bool, target NodeID) error {
	if hasCurrent {
		if err := removeRelationshipTx(tx, id, current); err != nil {
			return err
		}
	}

	return addRelationshipTx(tx, id, target)
}

// singleChildTargetSetTx sets node's single "target" child -- under the
// same at-most-one-child invariant as singleChildTarget/PointerRegistry
// -- composed into an existing tx rather than opening a new
// Graph.Transact. It is the tx-composable counterpart of
// PointerRegistry.SetTarget's read-current/idempotency-check/replace
// sequence, for callers (CapsuleRegistry, and through it ListRegistry)
// that need to rewire an already-tagged Pointer-style slot node as one
// step of a larger enclosing transaction.
//
// node is assumed to already be a valid, at-most-one-child node (e.g. a
// capsule role slot); callers are responsible for target's existence,
// exactly as PointerRegistry.SetTarget's caller-facing checks already
// are. This deliberately skips the IsPointer-style tag check that
// PointerRegistry.currentTarget performs, since callers here have
// already located node via a tag-based lookup (e.g.
// findUniqueTaggedChild) immediately beforehand.
//
// exclude has the same meaning as for singleChildTarget: children of
// node that are structural rather than target candidates
// (PointerMetadataRegistry's subject-slot).
func singleChildTargetSetTx(tx txOps, graph GraphReader, node, target NodeID, exclude ...NodeID) error {
	current, hasCurrent, err := singleChildTarget(graph, node, exclude...)
	if err != nil {
		return err
	}

	if hasCurrent && current == target {
		return nil
	}

	return setPointerTargetTx(tx, node, current, hasCurrent, target)
}

// singleChildTargetRemoveTx clears node's single "target" child, if any,
// composed into an existing tx rather than opening a new Graph.Transact.
// It is the shared read-current/remove step behind
// PointerRegistry.RemoveTarget and PointerMetadataRegistry(D).RemoveTarget.
// Because it runs against tx, the removal is recorded in that
// transaction's undo log and is undone with it. ListRegistry.Remove is
// the kind of caller that needs this: clearing a capsule's own prev/next
// slots must roll back together with the neighbor-relinking steps around
// it.
//
// removed reports whether a target actually existed and was removed.
// exclude has the same meaning as for singleChildTarget.
func singleChildTargetRemoveTx(tx txOps, graph GraphReader, node NodeID, exclude ...NodeID) (removed bool, err error) {
	current, hasCurrent, err := singleChildTarget(graph, node, exclude...)
	if err != nil {
		return false, err
	}

	if !hasCurrent {
		return false, nil
	}

	removed, err = tx.RemoveRelationship(node, current)
	return removed, wrapInterfaceErr(err)
}

// requireTagged checks that id exists and is tagged via (tag, id),
// returning ErrNodeNotFound or notTaggedErr otherwise, without
// inspecting id's other relationships. This is the shared "exists and
// carries this registry's own tag" precondition check factored out of
// what used to be six byte-for-byte identical three-line checks --
// PointerRegistry.requirePointer, CapsuleRegistry.requireCapsule,
// ListRegistry.requireList, SetRegistry.requireSet,
// CompositeSetRegistry.requireCompositeSet, and
// CompositeSetLogRegistry.requireLog -- each differing only in which tag
// NodeID and which dedicated ErrNotX sentinel it returned.
func requireTagged(graph GraphReader, id, tag NodeID, notTaggedErr error) error {
	exists, err := graph.NodeExists(id)
	if err != nil {
		return wrapInterfaceErr(err)
	}
	if !exists {
		return ErrNodeNotFound
	}

	has, err := graph.HasRelationship(tag, id)
	if err != nil {
		return wrapInterfaceErr(err)
	}
	if !has {
		return notTaggedErr
	}

	return nil
}

// requireExists checks that id exists, returning ErrNodeNotFound
// otherwise. It is the shared "and the node exists" step of every
// registry: constructors (the tag nodes), requireListValue,
// requireSetMember, AppendMany and the mutators that take a target.
func requireExists(graph GraphReader, id NodeID) error {
	exists, err := graph.NodeExists(id)
	if err != nil {
		return wrapInterfaceErr(err)
	}
	if !exists {
		return ErrNodeNotFound
	}

	return nil
}

// requireAllExist is requireExists over a list: it returns ErrNodeNotFound
// for the first of ids that does not exist. It is the constructors' "every
// tag node must already exist" check.
func requireAllExist(graph GraphReader, ids ...NodeID) error {
	for _, id := range ids {
		if err := requireExists(graph, id); err != nil {
			return err
		}
	}

	return nil
}
