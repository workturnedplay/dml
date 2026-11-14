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

// PointerRegistry enforces the Pointer invariant -- "at most one target"
// -- for nodes tagged Pointer-kind via a caller-supplied tag relationship
// (tag, P).
//
// This same type and logic serves two of the three Pointer
// representations described in theorystate.md section 10 / 10b,
// distinguished only by which tag
// NodeID the caller passes to NewPointerRegistry -- there is no
// per-representation code path:
//   - Representation A (direct child): tag = AllPointers, applied
//     directly to the owning node P.
//   - Representation B (intermediary pointer node): tag =
//     AllSubPointers, applied to a dedicated node U. The caller
//     separately creates the ordinary relationship (P, U) themselves --
//     that edge carries no invariant and is not PointerRegistry's
//     concern -- so P's other direct children stay unconstrained by the
//     pointer representation living on U.
//
// Representation C (metadata structure) cannot reuse this type as-is,
// since the subject's own direct children must stay completely
// untouched; see PointerMetadataRegistry instead.
//
// This implements Representation A (and, via reuse, B) from
// theorystate.md section 10 / 10b: a Pointer's target, if any, is
// simply P's single direct
// child in the underlying Graph. The tag itself -- (AllPointers, P) -- is
// ordinary graph structure, exactly like any other name-style tag. Like
// NameRegistry and RootGraph, PointerRegistry adds nothing to the
// primitive Graph; it is purely an interpretation/enforcement layer above
// it (theorystate.md sections 10 and 73).
//
// PointerRegistry does not, and structurally cannot, prevent every path
// to invariant violation: a caller can still bypass this layer and give
// a tagged node two or more children with tx.AddRelationship(P, Y)
// inside a Transact (the Checker registered by NewPointerRegistry
// declines that at commit), and data that reached the graph from outside
// this process's Checkers (loaded from storage, written by an older
// build) may already violate it. PointerRegistry does not try to
// intercept arbitrary Graph mutations -- Graph must stay unaware of
// Pointer semantics, per the same layering discipline already
// established elsewhere in this file.
// Instead, every method here re-derives P's current target set fresh from
// the Graph on every call rather than caching it, and fails loudly with
// ErrTooManyPointerTargets if that set already has more than one member,
// rather than silently repairing or silently trusting stale expectations.
// This mirrors the fail-loud-not-silently-repair discipline already used
// for ErrNameBoundToDeletedNode in NameRegistry, and is the practical
// mitigation for the general gap recorded in theorystate.md section
// 74: external structure built on top of the primitive Graph can go
// stale the instant a primitive mutation happens elsewhere, and nothing
// below this layer will ever notify it. A durable commit-time
// interception mechanism that could reject such a mutation before it
// lands (theorystate.md section 73) does not exist yet; until it
// does, "always re-check, never cache" is the deliberate accepted
// boundary of this registry.
//
// Multi-step PointerRegistry operations (SetTarget's replace path,
// NewPointer's create-then-tag) run inside Graph.Transact: if a later
// step fails, an earlier step's mutation is undone rather than left
// committed, so e.g. a failed SetTarget can never leave a Pointer looking
// like it lost its old target without gaining the new one. See the Txn
// doc comment for exactly what this does and does not guarantee -- in
// particular, it is failure-atomicity, not isolation from concurrent
// access; true multi-primitive-operation transactional grouping as a
// first-class graph concept is still theorystate.md section 14/45,
// OPEN.
// What exists here is the minimum needed to stop PointerRegistry's
// own multi-step operations from corrupting state on failure, not a
// general transaction feature.
//
// PointerRegistry deliberately stores no graph reference of its own
// (theorystate.md section 90): every method below takes the graph it
// should operate against as an explicit parameter instead, typed as
// narrowly as that method actually needs (GraphReader for pure reads,
// Transactor for methods that open their own transaction, nested if the
// value passed is a Tx). This is what
// lets the exact same PointerRegistry value be safely reused across
// several separately-submitted GraphActor calls without any risk of a
// Checker or tx-composable helper accidentally reading through a stored
// reference instead of through the tx/g value it was actually handed --
// see GraphActor's own doc comment for why that specific mistake
// deadlocks rather than merely misbehaving.
type PointerRegistry struct {
	allPointers NodeID
}

// NewPointerRegistry creates a PointerRegistry over graph, using
// allPointers as the tagging node for the (AllPointers, P) relationship.
//
// allPointers must already exist. It is the caller's responsibility to
// have bootstrapped it first, typically via
// NameRegistry.EnsureNamedNode(NameAllPointers) or
// NameRegistry.BootstrapNames(FoundationalNames). PointerRegistry itself
// has no dependency on NameRegistry or on names at all -- exactly like
// RootGraph takes its root as a plain NodeID rather than a name, keeping
// this layer decoupled from the bootstrap-naming concern.
//
// This also registers a Checker (see Graph.RegisterChecker) enforcing
// the "at most one target" invariant at commit time, for any node
// tagged (allPointers, node) touched by a future Graph.Transact call --
// the eager, commit-time counterpart to this registry's existing
// always-re-derive-on-read discipline (see the PointerRegistry doc
// comment above). Since this same type is reused unmodified across
// Representations A and B (and, via CapsuleRegistry, for each of a
// capsule's three role-slot tags), constructing any PointerRegistry
// instance -- under any tag -- wires up commit-time enforcement for
// that specific tag, with no additional per-representation code.
func NewPointerRegistry(graph GraphAPI, allPointers NodeID) (*PointerRegistry, error) {
	if existsErr := requireExists(graph, allPointers); existsErr != nil {
		return nil, existsErr
	}

	// Registers a Checker (see Graph.RegisterChecker) enforcing the "at
	// most one target" invariant at commit time, for any node tagged
	// (allPointers, node) touched by a future Graph.Transact call -- the
	// eager, commit-time counterpart to this registry's existing
	// always-re-derive-on-read discipline (see the PointerRegistry doc
	// comment above). Since this same type is reused unmodified across
	// Representations A and B (and, via CapsuleRegistry, for each of a
	// capsule's three role-slot tags), constructing any PointerRegistry
	// instance -- under any tag -- wires up commit-time enforcement for
	// that specific tag, with no additional per-representation code.
	graph.RegisterChecker(Checker{
		Name: fmt.Sprintf("PointerRegistry(tag=%d)", allPointers),
		Tags: []NodeID{allPointers},
		Check: func(g GraphReader, touched map[NodeID]struct{}) error {
			for node := range touched {
				tagged, taggedErr := g.HasRelationship(allPointers, node)
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

	return &PointerRegistry{
		allPointers: allPointers,
	}, nil
}

// IsPointer reports whether id is currently tagged Pointer-kind via
// (AllPointers, id). graph is passed explicitly, never stored (see the
// PointerRegistry doc comment) -- passing the wrong graph value here is
// a caller error at the call site, not a hidden footgun inside this
// type.
func (p *PointerRegistry) IsPointer(graph GraphReader, id NodeID) (bool, error) {
	has, err := graph.HasRelationship(p.allPointers, id)
	return has, wrapInterfaceErr(err)
}

// currentTarget returns P's current single target, re-derived fresh from
// the underlying Graph on every call (see the PointerRegistry doc comment
// for why this is never cached).
//
// It requires P to exist and to be tagged Pointer-kind; otherwise it
// returns ErrNodeNotFound or ErrNotPointer respectively, without
// inspecting P's relationships at all. If P is tagged but currently has
// more than one outgoing relationship -- meaning some caller bypassed
// this registry and violated the Pointer invariant directly through the
// primitive Graph -- currentTarget returns ErrTooManyPointerTargets
// rather than silently picking one of them.
func (p *PointerRegistry) currentTarget(graph GraphReader, id NodeID) (target NodeID, hasTarget bool, err error) {
	if requireErr := p.requirePointer(graph, id); requireErr != nil {
		return 0, false, requireErr
	}

	return singleChildTarget(graph, id)
}

// requirePointer checks that id exists and is tagged Pointer-kind,
// returning ErrNodeNotFound or ErrNotPointer otherwise, without
// inspecting id's relationships. Shared by currentTarget, SetTarget and
// RemoveTarget (corrected here from the stale "setTargetTx and
// removeTargetTx" this comment used to say -- those tx-suffixed helpers
// were inlined into the exported methods themselves back in item 33 and
// no longer exist under those names). Delegates to the shared
// requireTagged helper -- see its doc comment for why this used to be
// its own three-line check.
func (p *PointerRegistry) requirePointer(graph GraphReader, id NodeID) error {
	return requireTagged(graph, id, p.allPointers, ErrNotPointer)
}

// Target returns P's current target.
//
// hasTarget is false when P is a valid, currently-empty Pointer. See
// currentTarget for the error cases: P missing, P not tagged Pointer-kind,
// or P's invariant already violated by an out-of-band Graph mutation.
func (p *PointerRegistry) Target(graph GraphReader, id NodeID) (target NodeID, hasTarget bool, err error) {
	return p.currentTarget(graph, id)
}

// SetTarget sets P's target to X, enforcing that P has at most one target
// both before and after the call.
//
// Both P and X must already exist, and P must already be tagged
// Pointer-kind (see NewPointer and TagAsPointer). Target's existence is
// checked before any mutation happens: if X did not exist and this check
// were skipped, a stale target could be removed before the new one failed
// to be added, losing data on a failed call. If P currently has no
// target, (P, X) is simply added. If P currently has exactly one target
// and it already equals X, this is an idempotent no-op. If P currently
// has exactly one different target, that relationship is removed and
// (P, X) is added in its place. If P currently has more than one target
// -- meaning the invariant was already violated by something outside
// this registry -- SetTarget makes no changes at all and returns
// ErrTooManyPointerTargets: it deliberately does not attempt to repair
// the violation by picking one existing target to keep or by clearing
// all of them, since either choice would be a silent, unrequested
// decision about data this registry did not create.
//
// Self-targeting, i.e. SetTarget(P, P), is allowed: self-relationships
// are permitted at the primitive layer (theorystate.md section 2.8)
// and nothing about the Pointer invariant rules it out.
//
// The whole operation -- reading P's current target, validating, and
// replacing it -- runs inside one Transact call on graph, so it is atomic
// as a whole under GraphActor, not merely each of its steps, and every
// read goes through tx so the decision and the write see the same state.
// If graph is a Tx the call nests inside the caller's transaction (see
// Transactor): a failure undoes only this call, and the at-most-one-target
// Checker runs at the outermost commit.
func (p *PointerRegistry) SetTarget(graph Transactor, id, target NodeID) error {
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		if requireErr := p.requirePointer(tx, id); requireErr != nil {
			return requireErr
		}

		if existsErr := requireExists(tx, target); existsErr != nil {
			return existsErr
		}

		return singleChildTargetSetTx(tx, tx, id, target)
	}))
}

// RemoveTarget clears P's target, if any.
//
// The returned bool reports whether a target was actually removed. If P
// currently has no target, this is a no-op returning (false, nil). If P
// currently has more than one target -- an already-violated invariant --
// RemoveTarget makes no changes and returns ErrTooManyPointerTargets, for
// the same reason given in SetTarget: this registry does not silently
// repair violations it did not create.
func (p *PointerRegistry) RemoveTarget(graph Transactor, id NodeID) (removed bool, err error) {
	// Running the removal through tx (rather than as a raw graph call)
	// also makes it visible to commit-time Checkers.
	return transactBool(graph, func(tx Tx) (bool, error) {
		if requireErr := p.requirePointer(tx, id); requireErr != nil {
			return false, requireErr
		}

		return singleChildTargetRemoveTx(tx, tx, id)
	})
}

// NewPointer creates a fresh NodeID and immediately tags it Pointer-kind.
//
// Because the node is freshly created, it has zero relationships and
// therefore trivially satisfies the Pointer invariant -- unlike
// TagAsPointer, no invariant check is needed here.
//
// The create and tag steps run inside Graph.Transact: if tagging were
// ever to fail after the node had already been created, the node would
// otherwise be left orphaned -- it would exist but never be discoverable
// as a Pointer. See the Txn doc comment and the PointerRegistry doc
// comment above for what this atomicity does and does not cover.
func (p *PointerRegistry) NewPointer(graph Transactor) (NodeID, error) {
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		return newPointerTx(tx, p.allPointers)
	})
}

// TagAsPointer tags an existing node id as Pointer-kind.
//
// Unlike NewPointer, id may already have relationships from before it
// became a Pointer, so TagAsPointer checks that id currently has at most
// one outgoing relationship before tagging it -- tagging a node that
// already has two or more outgoing relationships would immediately
// create an already-violated Pointer, which TagAsPointer refuses to do,
// returning ErrTooManyPointerTargets and leaving id untagged. id's
// existence is implicitly checked by the underlying FindOutgoing call,
// which returns ErrNodeNotFound if id does not exist.
//
// Tagging an id that is already tagged Pointer-kind is an idempotent
// success, exactly like the underlying Graph.AddRelationship being
// idempotent for an already-existing relationship.
func (p *PointerRegistry) TagAsPointer(graph Transactor, id NodeID) error {
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		outgoing, txErr := tx.FindOutgoing(id)
		if txErr != nil {
			return wrapInterfaceErr(txErr)
		}

		if len(outgoing) > 1 {
			return ErrTooManyPointerTargets
		}

		return addRelationshipTx(tx, p.allPointers, id)
	}))
}
