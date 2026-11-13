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
	"reflect"
	"sync"
	"testing"
)

func TestFoundationalNamesIncludesAllDomainSlot(t *testing.T) {
	for _, name := range FoundationalNames {
		if name == NameAllDomainSlot {
			return
		}
	}

	t.Fatalf("FoundationalNames %v does not include %q", FoundationalNames, NameAllDomainSlot)
}

// domainPointerTestFixture bundles every registry
// newDomainPointerTestFixture constructs. Returning one struct instead of
// six separate values keeps newDomainPointerTestFixture under gocritic's
// tooManyResultsChecker limit (max 5) without losing any of the
// individual registries call sites need -- each is still just a field
// access away.
type domainPointerTestFixture struct {
	graph      testGraph
	names      *NameRegistry
	sets       *SetRegistry
	composites *CompositeSetRegistry
	logs       *CompositeSetLogRegistry
	capsules   *CapsuleRegistry
	lists      *ListRegistry
	pointers   *PointerRegistry
	domainB    *DomainPointerRegistryB
	domainD    *DomainPointerRegistryD
}

// newDomainPointerTestFixture creates a fresh Graph plus every registry
// needed to exercise both DomainPointerRegistryB and
// DomainPointerRegistryD against one shared graph: SetRegistry,
// CompositeSetRegistry, CompositeSetLogRegistry (fully wired via
// SetLogs), a plain PointerRegistry for Representation B usage
// (AllSubPointers), PointerMetadataRegistryD, and a single shared
// domainSlots PointerRegistry (AllDomainSlot) passed to both domain
// wrappers, per domainConstraint's doc comment.
func newDomainPointerTestFixture(t *testing.T) *domainPointerTestFixture {
	t.Helper()

	g := newTestGraph(t)
	names := NewNameRegistry(g)

	ids, err := names.BootstrapNames(g, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	sets, err := NewSetRegistry(g, ids[NameAllSets], ids[NameAllCompositeSets], ids[NameAllCompositeSetLogs])
	if err != nil {
		t.Fatalf("NewSetRegistry(): %v", err)
	}

	composites, err := NewCompositeSetRegistry(
		g,
		sets,
		ids[NameAllCompositeSets],
		ids[NameAllAdditiveOp],
		ids[NameAllSubtractiveOp],
		ids[NameAllScalarOperand],
		ids[NameAllSetOperand],
	)
	if err != nil {
		t.Fatalf("NewCompositeSetRegistry(): %v", err)
	}

	capsules, err := NewCapsuleRegistry(
		g,
		ids[NameAllElementCapsules],
		ids[NameAllElementCapsulePrevSlot],
		ids[NameAllElementCapsuleValueSlot],
		ids[NameAllElementCapsuleNextSlot],
	)
	if err != nil {
		t.Fatalf("NewCapsuleRegistry(): %v", err)
	}

	lists, err := NewListRegistry(g, capsules, ids[NameAllLists], ids[NameAllHeads], ids[NameAllTails])
	if err != nil {
		t.Fatalf("NewListRegistry(): %v", err)
	}

	logs, err := NewCompositeSetLogRegistry(
		g,
		lists,
		composites,
		ids[NameAllCompositeSetLogs],
		ids[NameAllAdditiveOp],
		ids[NameAllSubtractiveOp],
		ids[NameAllScalarOperand],
		ids[NameAllSetOperand],
	)
	if err != nil {
		t.Fatalf("NewCompositeSetLogRegistry(): %v", err)
	}
	composites.SetLogs(logs)

	pointers, err := NewPointerRegistry(g, ids[NameAllPointers])
	if err != nil {
		t.Fatalf("NewPointerRegistry(AllPointers): %v", err)
	}

	subPointers, err := NewPointerRegistry(g, ids[NameAllSubPointers])
	if err != nil {
		t.Fatalf("NewPointerRegistry(AllSubPointers): %v", err)
	}

	metadata, err := NewPointerMetadataRegistryD(g, ids[NameAllPointerMetadata], ids[NameAllPointerMetadataSubjectSlot], ids[NameAllPointerMetadataTargetSlot])
	if err != nil {
		t.Fatalf("NewPointerMetadataRegistryD(): %v", err)
	}

	domainSlots, err := NewPointerRegistry(g, ids[NameAllDomainSlot])
	if err != nil {
		t.Fatalf("NewPointerRegistry(AllDomainSlot): %v", err)
	}

	domainB := NewDomainPointerRegistryB(g, subPointers, domainSlots, sets, composites, logs)
	domainD := NewDomainPointerRegistryD(g, metadata, domainSlots, sets, composites, logs)

	return &domainPointerTestFixture{
		graph:      g,
		names:      names,
		sets:       sets,
		composites: composites,
		logs:       logs,
		capsules:   capsules,
		lists:      lists,
		pointers:   pointers,
		domainB:    domainB,
		domainD:    domainD,
	}
}

func TestDomainPointerRegistryBNewDomainPointerAndTargetWithNoDomain(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	p, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if err2 := fx.domainB.NewDomainPointer(fx.graph, p); err2 != nil {
		t.Fatalf("NewDomainPointer(p): %v", err2)
	}

	x, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if err3 := fx.domainB.SetTarget(fx.graph, p, x); err3 != nil {
		t.Fatalf("SetTarget(p, x) with no domain set: %v", err3)
	}

	target, hasTarget, err := fx.domainB.Target(fx.graph, p)
	if err != nil {
		t.Fatalf("Target(p): %v", err)
	}
	if !hasTarget || target != x {
		t.Fatalf("Target(p) = (%d,%v), want (%d,true)", target, hasTarget, x)
	}
}

// TestDomainPointerRegistryBNewDomainPointerIsIdempotent covers a real
// gap found on review: NewDomainPointer previously minted a fresh
// sub-pointer node unconditionally on every call, so calling it twice
// for the same anchor silently gave that anchor two children both
// tagged via the underlying PointerRegistry's own tag, making every
// subsequent subPointer-based lookup (Target/SetTarget/RemoveTarget)
// fail with ErrAmbiguousPointerMetadata. NewDomainPointer must instead
// be idempotent: a second call for the same anchor is a no-op, and the
// pointer's existing target survives untouched.
func TestDomainPointerRegistryBNewDomainPointerIsIdempotent(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	p, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if err2 := fx.domainB.NewDomainPointer(fx.graph, p); err2 != nil {
		t.Fatalf("first NewDomainPointer(p): %v", err2)
	}

	x, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if err3 := fx.domainB.SetTarget(fx.graph, p, x); err3 != nil {
		t.Fatalf("SetTarget(p, x): %v", err3)
	}

	if err4 := fx.domainB.NewDomainPointer(fx.graph, p); err4 != nil {
		t.Fatalf("second NewDomainPointer(p): %v", err4)
	}

	target, hasTarget, err := fx.domainB.Target(fx.graph, p)
	if err != nil {
		t.Fatalf("Target(p) after second NewDomainPointer(): %v", err)
	}
	if !hasTarget || target != x {
		t.Fatalf("Target(p) = (%d,%v), want (%d,true) -- unaffected by the idempotent second NewDomainPointer() call", target, hasTarget, x)
	}
}

func TestDomainPointerRegistryBSetDomainEnforcesMembership(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	p, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if err2 := fx.domainB.NewDomainPointer(fx.graph, p); err2 != nil {
		t.Fatalf("NewDomainPointer(p): %v", err2)
	}

	domainSet, err := fx.sets.NewSet(fx.graph)
	if err != nil {
		t.Fatalf("NewSet() for domain: %v", err)
	}
	allowed, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err3 := fx.sets.Add(fx.graph, domainSet, allowed); err3 != nil {
		t.Fatal(err3)
	}

	if err4 := fx.domainB.SetDomain(fx.graph, p, domainSet); err4 != nil {
		t.Fatalf("SetDomain(p, domainSet): %v", err4)
	}

	if err5 := fx.domainB.SetTarget(fx.graph, p, allowed); err5 != nil {
		t.Fatalf("SetTarget(p, allowed) within domain: %v", err5)
	}

	disallowed, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	err = fx.domainB.SetTarget(fx.graph, p, disallowed)
	if !errors.Is(err, ErrTargetOutsideDomain) {
		t.Fatalf("SetTarget(p, disallowed) error = %v, want %v", err, ErrTargetOutsideDomain)
	}

	target, hasTarget, err := fx.domainB.Target(fx.graph, p)
	if err != nil {
		t.Fatalf("Target(p): %v", err)
	}
	if !hasTarget || target != allowed {
		t.Fatalf("Target(p) = (%d,%v), want (%d,true) -- unaffected by the rejected SetTarget", target, hasTarget, allowed)
	}
}

func TestDomainPointerRegistryBSetDomainRejectsNonSetTaggedNode(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	p, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if err2 := fx.domainB.NewDomainPointer(fx.graph, p); err2 != nil {
		t.Fatalf("NewDomainPointer(p): %v", err2)
	}

	notASet, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	err = fx.domainB.SetDomain(fx.graph, p, notASet)
	if !errors.Is(err, ErrInvalidSetOperand) {
		t.Fatalf("SetDomain(p, notASet) error = %v, want %v", err, ErrInvalidSetOperand)
	}
}

func TestDomainPointerRegistryBSetDomainRejectsWhenCurrentTargetOutsideNewDomain(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	p, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if err2 := fx.domainB.NewDomainPointer(fx.graph, p); err2 != nil {
		t.Fatalf("NewDomainPointer(p): %v", err2)
	}

	x, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if err3 := fx.domainB.SetTarget(fx.graph, p, x); err3 != nil {
		t.Fatalf("SetTarget(p, x): %v", err3)
	}

	domainSet, err := fx.sets.NewSet(fx.graph)
	if err != nil {
		t.Fatal(err)
	}
	// domainSet deliberately does not contain x.

	err = fx.domainB.SetDomain(fx.graph, p, domainSet)
	if !errors.Is(err, ErrTargetOutsideDomain) {
		t.Fatalf("SetDomain(p, domainSet) error = %v, want %v", err, ErrTargetOutsideDomain)
	}

	_, hasDomain, err := fx.domainB.Domain(fx.graph, p)
	if err != nil {
		t.Fatalf("Domain(p): %v", err)
	}
	if hasDomain {
		t.Fatal("Domain(p) reports a domain despite the rejected SetDomain")
	}
}

func TestDomainPointerRegistryBRemoveDomainAllowsAnyTargetAgain(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	p, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if err2 := fx.domainB.NewDomainPointer(fx.graph, p); err2 != nil {
		t.Fatalf("NewDomainPointer(p): %v", err2)
	}

	domainSet, err := fx.sets.NewSet(fx.graph)
	if err != nil {
		t.Fatal(err)
	}
	if err3 := fx.domainB.SetDomain(fx.graph, p, domainSet); err3 != nil {
		t.Fatalf("SetDomain(p, domainSet): %v", err3)
	}

	outside, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	if err4 := fx.domainB.SetTarget(fx.graph, p, outside); !errors.Is(err4, ErrTargetOutsideDomain) {
		t.Fatalf("SetTarget(p, outside) before RemoveDomain error = %v, want %v", err4, ErrTargetOutsideDomain)
	}

	removed, err := fx.domainB.RemoveDomain(fx.graph, p)
	if err != nil {
		t.Fatalf("RemoveDomain(p): %v", err)
	}
	if !removed {
		t.Fatal("RemoveDomain() reported that nothing was removed")
	}

	if err5 := fx.domainB.SetTarget(fx.graph, p, outside); err5 != nil {
		t.Fatalf("SetTarget(p, outside) after RemoveDomain: %v", err5)
	}
}

func TestDomainPointerRegistryBSetTargetRequiresExistingSubPointer(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	p, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately not calling NewDomainPointer(p).

	x, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	err = fx.domainB.SetTarget(fx.graph, p, x)
	if !errors.Is(err, ErrNotPointer) {
		t.Fatalf("SetTarget(p, x) without a sub-pointer error = %v, want %v", err, ErrNotPointer)
	}
}

func TestDomainPointerRegistryDTargetWithNoDomainAllowsAnyTarget(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	subject, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	x, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	if err2 := fx.domainD.SetTarget(fx.graph, subject, x); err2 != nil {
		t.Fatalf("SetTarget(subject, x) with no domain: %v", err2)
	}

	target, hasTarget, err := fx.domainD.Target(fx.graph, subject)
	if err != nil {
		t.Fatalf("Target(subject): %v", err)
	}
	if !hasTarget || target != x {
		t.Fatalf("Target(subject) = (%d,%v), want (%d,true)", target, hasTarget, x)
	}
}

func TestDomainPointerRegistryDSetDomainEnforcesMembership(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	subject, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	domainSet, err := fx.sets.NewSet(fx.graph)
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err2 := fx.sets.Add(fx.graph, domainSet, allowed); err2 != nil {
		t.Fatal(err2)
	}

	if err3 := fx.domainD.SetDomain(fx.graph, subject, domainSet); err3 != nil {
		t.Fatalf("SetDomain(subject, domainSet): %v", err3)
	}

	if err4 := fx.domainD.SetTarget(fx.graph, subject, allowed); err4 != nil {
		t.Fatalf("SetTarget(subject, allowed): %v", err4)
	}

	disallowed, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	err = fx.domainD.SetTarget(fx.graph, subject, disallowed)
	if !errors.Is(err, ErrTargetOutsideDomain) {
		t.Fatalf("SetTarget(subject, disallowed) error = %v, want %v", err, ErrTargetOutsideDomain)
	}
}

func TestDomainPointerRegistryDSetDomainRejectsNonSetTaggedNode(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	subject, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	notASet, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	err = fx.domainD.SetDomain(fx.graph, subject, notASet)
	if !errors.Is(err, ErrInvalidSetOperand) {
		t.Fatalf("SetDomain(subject, notASet) error = %v, want %v", err, ErrInvalidSetOperand)
	}
}

func TestDomainPointerRegistryDSetDomainRejectsWhenCurrentTargetOutsideNewDomain(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	subject, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	x, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if err2 := fx.domainD.SetTarget(fx.graph, subject, x); err2 != nil {
		t.Fatalf("SetTarget(subject, x): %v", err2)
	}

	domainSet, err := fx.sets.NewSet(fx.graph)
	if err != nil {
		t.Fatal(err)
	}
	// domainSet deliberately does not contain x.

	err = fx.domainD.SetDomain(fx.graph, subject, domainSet)
	if !errors.Is(err, ErrTargetOutsideDomain) {
		t.Fatalf("SetDomain(subject, domainSet) error = %v, want %v", err, ErrTargetOutsideDomain)
	}

	_, hasDomain, err := fx.domainD.Domain(fx.graph, subject)
	if err != nil {
		t.Fatalf("Domain(subject): %v", err)
	}
	if hasDomain {
		t.Fatal("Domain(subject) reports a domain despite the rejected SetDomain")
	}
}

func TestDomainPointerRegistryDRemoveDomainAllowsAnyTargetAgain(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	subject, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	domainSet, err := fx.sets.NewSet(fx.graph)
	if err != nil {
		t.Fatal(err)
	}
	if err2 := fx.domainD.SetDomain(fx.graph, subject, domainSet); err2 != nil {
		t.Fatalf("SetDomain(subject, domainSet): %v", err2)
	}

	outside, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if err3 := fx.domainD.SetTarget(fx.graph, subject, outside); !errors.Is(err3, ErrTargetOutsideDomain) {
		t.Fatalf("SetTarget(subject, outside) before RemoveDomain error = %v, want %v", err3, ErrTargetOutsideDomain)
	}

	removed, err := fx.domainD.RemoveDomain(fx.graph, subject)
	if err != nil {
		t.Fatalf("RemoveDomain(subject): %v", err)
	}
	if !removed {
		t.Fatal("RemoveDomain() reported that nothing was removed")
	}

	if err4 := fx.domainD.SetTarget(fx.graph, subject, outside); err4 != nil {
		t.Fatalf("SetTarget(subject, outside) after RemoveDomain: %v", err4)
	}
}

func TestDomainPointerRegistryDDomainViaCompositeSet(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	subject, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	domainComposite, err := fx.composites.NewCompositeSet(fx.graph)
	if err != nil {
		t.Fatalf("NewCompositeSet(): %v", err)
	}
	allowed, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err2 := fx.composites.AddOperand(fx.graph, domainComposite, allowed, true, false); err2 != nil {
		t.Fatalf("AddOperand(): %v", err2)
	}

	if err3 := fx.domainD.SetDomain(fx.graph, subject, domainComposite); err3 != nil {
		t.Fatalf("SetDomain(subject, domainComposite): %v", err3)
	}

	if err4 := fx.domainD.SetTarget(fx.graph, subject, allowed); err4 != nil {
		t.Fatalf("SetTarget(subject, allowed): %v", err4)
	}

	disallowed, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	err = fx.domainD.SetTarget(fx.graph, subject, disallowed)
	if !errors.Is(err, ErrTargetOutsideDomain) {
		t.Fatalf("SetTarget(subject, disallowed) error = %v, want %v", err, ErrTargetOutsideDomain)
	}
}

func TestDomainPointerRegistryDDomainViaCompositeSetLog(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	subject, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	domainLog, err := fx.logs.NewCompositeSetLog(fx.graph)
	if err != nil {
		t.Fatalf("NewCompositeSetLog(): %v", err)
	}
	allowed, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err2 := fx.logs.AppendOperation(fx.graph, domainLog, allowed, true, false); err2 != nil {
		t.Fatalf("AppendOperation(): %v", err2)
	}

	if err3 := fx.domainD.SetDomain(fx.graph, subject, domainLog); err3 != nil {
		t.Fatalf("SetDomain(subject, domainLog): %v", err3)
	}

	if err4 := fx.domainD.SetTarget(fx.graph, subject, allowed); err4 != nil {
		t.Fatalf("SetTarget(subject, allowed): %v", err4)
	}

	disallowed, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	err = fx.domainD.SetTarget(fx.graph, subject, disallowed)
	if !errors.Is(err, ErrTargetOutsideDomain) {
		t.Fatalf("SetTarget(subject, disallowed) error = %v, want %v", err, ErrTargetOutsideDomain)
	}
}

// TestDomainPointerRegistryDCheckerCatchesOutOfBandTargetChange
// demonstrates the Checker registered by NewDomainPointerRegistryD
// catching a domain violation introduced by bypassing
// DomainPointerRegistryD entirely and mutating the underlying
// PointerMetadataRegistryD directly. This still goes through
// Graph.Transact internally (see PointerMetadataRegistryD.SetTarget), so
// the Checker still sees and rejects it -- the counterpart to
// DomainPointerRegistryB's documented lack of an equivalent Checker.
func TestDomainPointerRegistryDCheckerCatchesOutOfBandTargetChange(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	subject, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	domainSet, err := fx.sets.NewSet(fx.graph)
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err2 := fx.sets.Add(fx.graph, domainSet, allowed); err2 != nil {
		t.Fatal(err2)
	}
	if err3 := fx.domainD.SetDomain(fx.graph, subject, domainSet); err3 != nil {
		t.Fatalf("SetDomain(subject, domainSet): %v", err3)
	}

	disallowed, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	err = fx.domainD.metadata.SetTarget(fx.graph, subject, disallowed)
	if !errors.Is(err, ErrTargetOutsideDomain) {
		t.Fatalf("bypassing SetTarget() error = %v, want %v", err, ErrTargetOutsideDomain)
	}

	target, hasTarget, err := fx.domainD.Target(fx.graph, subject)
	if err != nil {
		t.Fatalf("Target(subject): %v", err)
	}
	if hasTarget {
		t.Fatalf("Target(subject) = (%d,true), want no target after the Checker declined the bypassing commit", target)
	}
}

// TestDomainPointerRegistryDCheckerCatchesOutOfBandDomainChange covers
// the other trigger direction: bypassing DomainPointerRegistryD.SetDomain
// and reassigning the domain slot's target directly through the shared
// domainSlots PointerRegistry, to a domain that no longer contains the
// pointer's current target.
func TestDomainPointerRegistryDCheckerCatchesOutOfBandDomainChange(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	subject, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	firstDomain, err := fx.sets.NewSet(fx.graph)
	if err != nil {
		t.Fatal(err)
	}
	x, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err2 := fx.sets.Add(fx.graph, firstDomain, x); err2 != nil {
		t.Fatal(err2)
	}
	if err3 := fx.domainD.SetDomain(fx.graph, subject, firstDomain); err3 != nil {
		t.Fatalf("SetDomain(subject, firstDomain): %v", err3)
	}
	if err4 := fx.domainD.SetTarget(fx.graph, subject, x); err4 != nil {
		t.Fatalf("SetTarget(subject, x): %v", err4)
	}

	secondDomain, err := fx.sets.NewSet(fx.graph)
	if err != nil {
		t.Fatal(err)
	}
	// secondDomain deliberately does not contain x.

	m, _, found, err := fx.domainD.metadata.locate(fx.graph, subject)
	if err != nil || !found {
		t.Fatalf("locate(subject): found=%v err=%v", found, err)
	}

	slot, found, err := fx.domainD.domainSlotFor(fx.graph, m)
	if err != nil || !found {
		t.Fatalf("domainSlotFor(m): found=%v err=%v", found, err)
	}

	err = fx.domainD.domainSlots.SetTarget(fx.graph, slot, secondDomain)
	if !errors.Is(err, ErrTargetOutsideDomain) {
		t.Fatalf("bypassing SetDomain() error = %v, want %v", err, ErrTargetOutsideDomain)
	}

	domain, hasDomain, err := fx.domainD.Domain(fx.graph, subject)
	if err != nil {
		t.Fatalf("Domain(subject): %v", err)
	}
	if !hasDomain || domain != firstDomain {
		t.Fatalf("Domain(subject) = (%d,%v), want (%d,true) -- unaffected by the declined bypass", domain, hasDomain, firstDomain)
	}
}

// TestCrossRoleNodeParticipatesInMultipleStructuresSimultaneously is the
// first of a planned "cross-registry adversarial" test family: instead
// of testing one registry in isolation, it builds a small cluster of
// nodes that each simultaneously participate in several different
// higher-level interpretations at once, and checks that no registry's
// own assumptions leak into another registry's behavior merely because
// they happen to share a node. This is a direct exercise of
// theorystate.md section 7a's "a fact never acquires a universal
// semantic meaning merely because one processor gives it meaning"
// principle, and of section 9a/68's claim that the same node identity
// can participate in many roles without the primitive graph needing to
// know about any of them.
//
// The scenario built here:
//   - S is a plain Set with two ordinary members (m1, m2).
//   - L is a List; S itself (not S's members) is appended as L's sole
//     element value, so S is simultaneously a Set and a List element
//     value.
//   - C is a CompositeSet with one additive, set-expansion operand
//     targeting S, so S is simultaneously a Set, a List element value,
//     and a CompositeSet operand target.
//   - P is a Representation A Pointer whose target is L, so L is
//     simultaneously a List and a Pointer target.
//   - A Representation D Domain Pointer's domain is set to C, and its
//     target is validated against C's evaluated membership (which
//     resolves recursively through S), so C is simultaneously a
//     CompositeSet and a Domain Pointer's domain.
//
// Each higher-level operation below is checked to behave exactly as it
// would in isolation, and the final step confirms that adding a new
// member to S -- multiply-interpreted as it now is -- is immediately
// visible through every layer built on top of it, with nothing needing
// to be explicitly re-synced (every Evaluate/Members in this file is
// deliberately never cached; theorystate.md sections 9a/35/81).
func TestCrossRoleNodeParticipatesInMultipleStructuresSimultaneously(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	s, err := fx.sets.NewSet(fx.graph)
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}
	m1, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	m2, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err2 := fx.sets.Add(fx.graph, s, m1); err2 != nil {
		t.Fatalf("Add(s, m1): %v", err2)
	}
	if _, err3 := fx.sets.Add(fx.graph, s, m2); err3 != nil {
		t.Fatalf("Add(s, m2): %v", err3)
	}

	list, err := fx.lists.NewList(fx.graph)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}
	if _, err4 := fx.lists.Append(fx.graph, list, s); err4 != nil {
		t.Fatalf("Append(list, s): %v", err4)
	}

	// S must still behave as an ordinary Set: being a list element
	// value must not disturb its own membership.
	members, err := fx.sets.Members(fx.graph, s)
	if err != nil {
		t.Fatalf("Members(s) after s became a list value: %v", err)
	}
	wantMembers := sortedNodeIDs([]NodeID{m1, m2})
	if got := sortedNodeIDs(members); !reflect.DeepEqual(got, wantMembers) {
		t.Fatalf("Members(s) = %v, want %v", got, wantMembers)
	}

	// The list must correctly report S as its sole element's value.
	elements, err := fx.lists.Elements(fx.graph, list)
	if err != nil {
		t.Fatalf("Elements(list): %v", err)
	}
	if want := []NodeID{s}; !reflect.DeepEqual(elements, want) {
		t.Fatalf("Elements(list) = %v, want %v", elements, want)
	}

	composite, err := fx.composites.NewCompositeSet(fx.graph)
	if err != nil {
		t.Fatalf("NewCompositeSet(): %v", err)
	}
	if _, err5 := fx.composites.AddOperand(fx.graph, composite, s, true, true); err5 != nil {
		t.Fatalf("AddOperand(composite, s, additive, expand): %v", err5)
	}

	evaluated, err := fx.composites.Evaluate(fx.graph, composite)
	if err != nil {
		t.Fatalf("Evaluate(composite): %v", err)
	}
	if got := sortedNodeIDs(evaluated); !reflect.DeepEqual(got, wantMembers) {
		t.Fatalf("Evaluate(composite) = %v, want %v (S's own members, resolved through the operand)", got, wantMembers)
	}

	p, err := fx.pointers.NewPointer(fx.graph)
	if err != nil {
		t.Fatalf("NewPointer(): %v", err)
	}
	if err6 := fx.pointers.SetTarget(fx.graph, p, list); err6 != nil {
		t.Fatalf("SetTarget(p, list): %v", err6)
	}

	target, hasTarget, err := fx.pointers.Target(fx.graph, p)
	if err != nil {
		t.Fatalf("Target(p): %v", err)
	}
	if !hasTarget || target != list {
		t.Fatalf("Target(p) = (%d,%v), want (%d,true) -- Pointer target unaffected by list's other roles", target, hasTarget, list)
	}

	// The list must still behave as an ordinary list despite also being
	// a Pointer's target.
	if _, hasHead, err11 := fx.lists.Head(fx.graph, list); err11 != nil {
		t.Fatalf("Head(list) after list became a pointer target: %v", err11)
	} else if !hasHead {
		t.Fatal("Head(list) unexpectedly empty after list became a pointer target")
	}

	subject, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if err7 := fx.domainD.SetDomain(fx.graph, subject, composite); err7 != nil {
		t.Fatalf("SetDomain(subject, composite): %v", err7)
	}

	if err8 := fx.domainD.SetTarget(fx.graph, subject, m1); err8 != nil {
		t.Fatalf("SetTarget(subject, m1) within domain: %v", err8)
	}

	outside, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	err = fx.domainD.SetTarget(fx.graph, subject, outside)
	if !errors.Is(err, ErrTargetOutsideDomain) {
		t.Fatalf("SetTarget(subject, outside) error = %v, want %v", err, ErrTargetOutsideDomain)
	}

	// Adding a new member to S -- simultaneously a Set, a list value,
	// and a CompositeSet operand target -- must be immediately visible
	// through the CompositeSet's own Evaluate, and therefore through the
	// domain pointer's own membership check, live.
	m3, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err9 := fx.sets.Add(fx.graph, s, m3); err9 != nil {
		t.Fatalf("Add(s, m3): %v", err9)
	}
	if err10 := fx.domainD.SetTarget(fx.graph, subject, m3); err10 != nil {
		t.Fatalf("SetTarget(subject, m3) after S gained a new member live: %v", err10)
	}
}

// TestCrossRoleDomainPointerDetectsCycleIntroducedThroughDomainItself is
// the second cross-role test: it builds a Domain Pointer whose domain is
// a CompositeSet that resolves through a CompositeSetLog, and confirms
// that a cycle introduced later -- purely by mutating the domain
// structure itself, never the pointer, its metadata, or either slot
// directly -- surfaces through domain validation as ErrCompositeSetCycle
// specifically, not as ErrTargetOutsideDomain or a silent false negative.
// This exercises theorystate.md section 83's cross-representation cycle
// detection through an integration point that was not previously tested
// anywhere: a Domain Pointer's write-time validateMembership call
// dispatching into that same cycle-detecting resolution path
// (domainContainsGeneric -> CompositeSetRegistry.Contains -> evaluate ->
// resolveSetOperandGeneric -> CompositeSetLogRegistry.evaluate ->
// resolveSetOperandGeneric, back into the CompositeSet that started it).
//
// Introducing the cycle (via logs.AppendOperation(log1, composite, ...))
// touches only log1 and composite -- never subject, its metadata node,
// or either slot. Before theorystate.md section 86 was closed, that
// meant no Checker could fire and the cycle was only discovered by a
// later SetTarget. The shared domain Checker now reaches subject's
// pointer through a reverse lookup from the touched log/composite to the
// domain slot that references them, evaluates the domain, hits the
// cycle, and declines the commit: the append is rolled back and the
// domain stays evaluable.
func TestCrossRoleDomainPointerDetectsCycleIntroducedThroughDomainItself(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	s, err := fx.sets.NewSet(fx.graph)
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}
	x, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err2 := fx.sets.Add(fx.graph, s, x); err2 != nil {
		t.Fatalf("Add(s, x): %v", err2)
	}

	log1, err := fx.logs.NewCompositeSetLog(fx.graph)
	if err != nil {
		t.Fatalf("NewCompositeSetLog(): %v", err)
	}
	if _, _, err3 := fx.logs.AppendOperation(fx.graph, log1, s, true, true); err3 != nil {
		t.Fatalf("AppendOperation(log1, s, additive, expand): %v", err3)
	}

	composite, err := fx.composites.NewCompositeSet(fx.graph)
	if err != nil {
		t.Fatalf("NewCompositeSet(): %v", err)
	}
	if _, err4 := fx.composites.AddOperand(fx.graph, composite, log1, true, true); err4 != nil {
		t.Fatalf("AddOperand(composite, log1, additive, expand): %v", err4)
	}

	subject, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if err5 := fx.domainD.SetDomain(fx.graph, subject, composite); err5 != nil {
		t.Fatalf("SetDomain(subject, composite): %v", err5)
	}

	// Before the cycle exists, x is legitimately reachable through
	// composite -> log1 -> s, so this must succeed.
	if err6 := fx.domainD.SetTarget(fx.graph, subject, x); err6 != nil {
		t.Fatalf("SetTarget(subject, x) before cycle introduced: %v", err6)
	}

	// Attempt to introduce the cycle purely through the domain's own
	// structure: log1 would also expand composite, which itself expands
	// log1. The Checker must decline the commit with the cycle error,
	// specifically -- not ErrTargetOutsideDomain.
	_, _, cycleErr := fx.logs.AppendOperation(fx.graph, log1, composite, true, true)
	if !errors.Is(cycleErr, ErrCompositeSetCycle) {
		t.Fatalf("AppendOperation(log1, composite, additive, expand) error = %v, want %v", cycleErr, ErrCompositeSetCycle)
	}

	// The whole append was rolled back: log1 still has its one operation
	// and the composite still evaluates, without a cycle.
	operations, err := fx.logs.Operations(fx.graph, log1)
	if err != nil {
		t.Fatalf("Operations(log1) after the declined append: %v", err)
	}
	if len(operations) != 1 {
		t.Fatalf("Operations(log1) = %v, want exactly the original operation", operations)
	}

	evaluated, err := fx.composites.Evaluate(fx.graph, composite)
	if err != nil {
		t.Fatalf("Evaluate(composite) after the declined append: %v", err)
	}
	if want := []NodeID{x}; !reflect.DeepEqual(evaluated, want) {
		t.Fatalf("Evaluate(composite) = %v, want %v", evaluated, want)
	}

	// The pointer is fully usable and its domain is untouched.
	if err2 := fx.domainD.SetTarget(fx.graph, subject, x); err2 != nil {
		t.Fatalf("SetTarget(subject, x) after the declined append: %v", err2)
	}

	domain, hasDomain, err := fx.domainD.Domain(fx.graph, subject)
	if err != nil {
		t.Fatalf("Domain(subject) after the declined append: %v", err)
	}
	if !hasDomain || domain != composite {
		t.Fatalf("Domain(subject) = (%d,%v), want (%d,true)", domain, hasDomain, composite)
	}
}

// TestCrossRoleCorruptedLoggedOperandDoesNotCorruptSiblingStructures is
// the third cross-role test, and the first deliberately built around
// mid-composition corruption rather than composition alone: it appends
// two operations to a CompositeSetLog, corrupts the second operation's
// descriptor out-of-band (giving it two operand targets, violating the
// shared operand-descriptor's own "exactly one operand" shape), and
// confirms the resulting failure is scoped exactly to Evaluate/Contains
// -- the two operations that must resolve every descriptor -- and does
// not leak into or corrupt any of: the underlying List's own structural
// validity, a completely unrelated CompositeSet sharing no structure
// with the corrupted log, or the first (uncorrupted) logged operation.
//
// This is a direct exercise of theorystate.md section 7a's claim that a
// fact's meaning belongs entirely to whichever processor interprets it:
// corrupting a node in its role as an operand descriptor must not be
// visible to ListRegistry, which interprets the exact same capsule chain
// under a completely different, narrower contract (structural list
// validity only, never descriptor shape) -- ListRegistry.Elements/Head/
// Tail have no reason to know or care that a capsule's value happens to
// also be a CompositeSetLog operand descriptor.
func TestCrossRoleCorruptedLoggedOperandDoesNotCorruptSiblingStructures(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	log, err := fx.logs.NewCompositeSetLog(fx.graph)
	if err != nil {
		t.Fatalf("NewCompositeSetLog(): %v", err)
	}

	x1, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	x2, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}

	u1, capsule1, err := fx.logs.AppendOperation(fx.graph, log, x1, true, false)
	if err != nil {
		t.Fatalf("AppendOperation(log, x1): %v", err)
	}
	u2, capsule2, err := fx.logs.AppendOperation(fx.graph, log, x2, true, false)
	if err != nil {
		t.Fatalf("AppendOperation(log, x2): %v", err)
	}

	// Corrupt only u2, entirely out-of-band: give it a second outgoing
	// relationship, violating operandTargetGeneric/resolveOperandGeneric's
	// shared "exactly one operand target" assumption for u2 specifically.
	// u1, capsule1, capsule2, and log's own head/tail/next/prev structure
	// are all left completely untouched by this.
	extra, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err2 := fx.graph.AddRelationship(u2, extra); err2 != nil {
		t.Fatalf("AddRelationship(u2, extra) [out-of-band corruption]: %v", err2)
	}

	// Evaluate/Contains must fail specifically because of u2's now-
	// invalid shape, not because anything about the list itself is
	// wrong.
	if _, err3 := fx.logs.Evaluate(fx.graph, log); !errors.Is(err3, ErrTooManyPointerTargets) {
		t.Fatalf("Evaluate(log) after corrupting u2: error = %v, want %v", err3, ErrTooManyPointerTargets)
	}
	if _, err4 := fx.logs.Contains(fx.graph, log, x1); !errors.Is(err4, ErrTooManyPointerTargets) {
		t.Fatalf("Contains(log, x1) after corrupting u2: error = %v, want %v (backward scan reaches the corrupted u2 first)", err4, ErrTooManyPointerTargets)
	}

	// Operations() must still succeed: it is built on ListRegistry.Elements,
	// which only cares about capsule/value-slot structure, never about
	// what a value's own further relationships happen to mean under some
	// other registry's interpretation.
	ops, err := fx.logs.Operations(fx.graph, log)
	if err != nil {
		t.Fatalf("Operations(log) after corrupting u2: %v", err)
	}
	if want := []NodeID{u1, u2}; !reflect.DeepEqual(ops, want) {
		t.Fatalf("Operations(log) = %v, want %v -- corrupting u2's shape must not disturb list-level ordering", ops, want)
	}

	// The underlying List's own head/tail must likewise be completely
	// unaffected: ListRegistry never inspects a value's own outgoing
	// relationships at all.
	head, hasHead, err := fx.lists.Head(fx.graph, log)
	if err != nil {
		t.Fatalf("Head(log) after corrupting u2: %v", err)
	}
	if !hasHead || head != capsule1 {
		t.Fatalf("Head(log) = (%d,%v), want (%d,true)", head, hasHead, capsule1)
	}
	tail, hasTail, err := fx.lists.Tail(fx.graph, log)
	if err != nil {
		t.Fatalf("Tail(log) after corrupting u2: %v", err)
	}
	if !hasTail || tail != capsule2 {
		t.Fatalf("Tail(log) = (%d,%v), want (%d,true)", tail, hasTail, capsule2)
	}

	// u1's own shape is untouched, so a completely independent
	// CompositeSet built from scratch -- sharing no node with log at all
	// except reusing x1 as an ordinary scalar operand -- must evaluate
	// normally, confirming the corruption did not leak into shared
	// registry-level state (e.g. some cached axis lookup) rather than
	// staying scoped to u2 itself.
	unrelated, err := fx.composites.NewCompositeSet(fx.graph)
	if err != nil {
		t.Fatalf("NewCompositeSet(): %v", err)
	}
	if _, err5 := fx.composites.AddOperand(fx.graph, unrelated, x1, true, false); err5 != nil {
		t.Fatalf("AddOperand(unrelated, x1, additive, scalar): %v", err5)
	}
	got, err := fx.composites.Evaluate(fx.graph, unrelated)
	if err != nil {
		t.Fatalf("Evaluate(unrelated) after corrupting an unrelated log's operand: %v", err)
	}
	if want := []NodeID{x1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Evaluate(unrelated) = %v, want %v", got, want)
	}
}

// TestCrossRoleSetRegistryConflictCheckExercisedWhileSetIsDomainAndOperand
// is the fourth cross-role test: it deliberately attempts to corrupt a
// node that is simultaneously playing two other structural roles at
// once -- a Domain Pointer's domain (theorystate.md section 9c/10c) and
// a set-expansion operand of an unrelated CompositeSet (theorystate.md
// section 80/81) -- to confirm that theorystate.md section 79's
// mutual-exclusivity rule (a node may carry at most one of the three
// Set-representation tags) is still correctly enforced by
// SetRegistry.TagAsSet even while the node is load-bearing for two other
// registries at once, and that a declined TagAsSet call disturbs neither
// of those other two roles.
//
// The scenario: c is a CompositeSet (AllCompositeSets) holding one
// scalar additive operand, x. c is used, unmodified, in two independent
// roles simultaneously: as subject's domain (via DomainPointerRegistryD,
// so subject's target must belong to c's evaluated membership {x}), and
// as an additive, set-expansion operand of an entirely separate outer
// CompositeSet (so outer's own evaluated membership also resolves
// through c to {x}). Attempting sets.TagAsSet(c) at that point must be
// rejected with ErrSetRepresentationConflict (c already carries
// AllCompositeSets), must leave c untagged AllSets, and must leave both
// of c's other two roles working exactly as before: subject's domain
// pointer still validates against c, and outer's Evaluate still resolves
// through c, unaffected by the failed tagging attempt.
func TestCrossRoleSetRegistryConflictCheckExercisedWhileSetIsDomainAndOperand(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	c, err := fx.composites.NewCompositeSet(fx.graph)
	if err != nil {
		t.Fatalf("NewCompositeSet() for c: %v", err)
	}

	x, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err2 := fx.composites.AddOperand(fx.graph, c, x, true, false); err2 != nil {
		t.Fatalf("AddOperand(c, x, additive, scalar): %v", err2)
	}

	subject, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	if err3 := fx.domainD.SetDomain(fx.graph, subject, c); err3 != nil {
		t.Fatalf("SetDomain(subject, c): %v", err3)
	}
	if err4 := fx.domainD.SetTarget(fx.graph, subject, x); err4 != nil {
		t.Fatalf("SetTarget(subject, x) within domain c: %v", err4)
	}

	outer, err := fx.composites.NewCompositeSet(fx.graph)
	if err != nil {
		t.Fatalf("NewCompositeSet() for outer: %v", err)
	}
	if _, err5 := fx.composites.AddOperand(fx.graph, outer, c, true, true); err5 != nil {
		t.Fatalf("AddOperand(outer, c, additive, expand): %v", err5)
	}

	evaluated, err := fx.composites.Evaluate(fx.graph, outer)
	if err != nil {
		t.Fatalf("Evaluate(outer) before corruption attempt: %v", err)
	}
	if want := []NodeID{x}; !reflect.DeepEqual(evaluated, want) {
		t.Fatalf("Evaluate(outer) before corruption attempt = %v, want %v", evaluated, want)
	}

	// The actual corruption attempt: try to also tag c as a plain Set
	// while it already carries AllCompositeSets and is simultaneously
	// load-bearing as both a Domain and a CompositeSet operand.
	err = fx.sets.TagAsSet(fx.graph, c)
	if !errors.Is(err, ErrSetRepresentationConflict) {
		t.Fatalf("TagAsSet(c) error = %v, want %v", err, ErrSetRepresentationConflict)
	}

	if mustIsSet(t, fx.sets, fx.graph, c) {
		t.Fatal("c was tagged AllSets despite already being AllCompositeSets-tagged")
	}

	// c's role as outer's set-expansion operand must be completely
	// unaffected by the declined TagAsSet call.
	evaluated, err = fx.composites.Evaluate(fx.graph, outer)
	if err != nil {
		t.Fatalf("Evaluate(outer) after declined TagAsSet: %v", err)
	}
	if want := []NodeID{x}; !reflect.DeepEqual(evaluated, want) {
		t.Fatalf("Evaluate(outer) after declined TagAsSet = %v, want %v", evaluated, want)
	}

	// c's role as subject's domain must likewise be completely
	// unaffected: subject's existing target is still valid, and a target
	// outside c's membership is still correctly rejected.
	target, hasTarget, err := fx.domainD.Target(fx.graph, subject)
	if err != nil {
		t.Fatalf("Target(subject) after declined TagAsSet: %v", err)
	}
	if !hasTarget || target != x {
		t.Fatalf("Target(subject) = (%d,%v), want (%d,true) after declined TagAsSet", target, hasTarget, x)
	}

	outside, err := fx.graph.CreateNode()
	if err != nil {
		t.Fatal(err)
	}
	err = fx.domainD.SetTarget(fx.graph, subject, outside)
	if !errors.Is(err, ErrTargetOutsideDomain) {
		t.Fatalf("SetTarget(subject, outside) after declined TagAsSet: error = %v, want %v", err, ErrTargetOutsideDomain)
	}
}

// domainPointerHandle abstracts one domain-constrained pointer so the
// same staleness scenario can run against Representation B and D.
type domainPointerHandle struct {
	setDomain func(domain NodeID) error
	setTarget func(target NodeID) error
	target    func() (NodeID, bool, error)
}

var domainPointerKinds = []struct {
	name  string
	build func(t *testing.T, fx *domainPointerTestFixture) domainPointerHandle
}{
	{"B", func(t *testing.T, fx *domainPointerTestFixture) domainPointerHandle {
		t.Helper()

		anchor := newTestNode(t, fx.graph)
		if err := fx.domainB.NewDomainPointer(fx.graph, anchor); err != nil {
			t.Fatalf("NewDomainPointer(): %v", err)
		}

		return domainPointerHandle{
			setDomain: func(domain NodeID) error { return fx.domainB.SetDomain(fx.graph, anchor, domain) },
			setTarget: func(target NodeID) error { return fx.domainB.SetTarget(fx.graph, anchor, target) },
			target:    func() (NodeID, bool, error) { return fx.domainB.Target(fx.graph, anchor) },
		}
	}},
	{"D", func(t *testing.T, fx *domainPointerTestFixture) domainPointerHandle {
		t.Helper()

		subject := newTestNode(t, fx.graph)

		return domainPointerHandle{
			setDomain: func(domain NodeID) error { return fx.domainD.SetDomain(fx.graph, subject, domain) },
			setTarget: func(target NodeID) error { return fx.domainD.SetTarget(fx.graph, subject, target) },
			target:    func() (NodeID, bool, error) { return fx.domainD.Target(fx.graph, subject) },
		}
	}},
}

// forEachDomainPointerKind runs body once per representation, each in a
// fresh fixture.
func forEachDomainPointerKind(t *testing.T, body func(t *testing.T, fx *domainPointerTestFixture, handle domainPointerHandle)) {
	t.Helper()

	for _, kind := range domainPointerKinds {
		t.Run(kind.name, func(t *testing.T) {
			fx := newDomainPointerTestFixture(t)
			body(t, fx, kind.build(t, fx))
		})
	}
}

func requireSetContains(t *testing.T, fx *domainPointerTestFixture, set, member NodeID, want bool) {
	t.Helper()

	got, err := fx.sets.Contains(fx.graph, set, member)
	if err != nil {
		t.Fatalf("Contains(%d, %d): %v", set, member, err)
	}
	if got != want {
		t.Fatalf("Contains(%d, %d) = %v, want %v", set, member, got, want)
	}
}

func requireHandleTarget(t *testing.T, handle domainPointerHandle, want NodeID) {
	t.Helper()

	target, hasTarget, err := handle.target()
	if err != nil {
		t.Fatalf("Target(): %v", err)
	}
	if !hasTarget || target != want {
		t.Fatalf("Target() = (%d,%v), want (%d,true)", target, hasTarget, want)
	}
}

func requireCompositeOperandCount(t *testing.T, fx *domainPointerTestFixture, composite NodeID, want int) {
	t.Helper()

	operands, err := fx.composites.Operands(fx.graph, composite)
	if err != nil {
		t.Fatalf("Operands(%d): %v", composite, err)
	}
	if len(operands) != want {
		t.Fatalf("Operands(%d) = %v, want %d operands", composite, operands, want)
	}
}

func requireLogOperationCount(t *testing.T, fx *domainPointerTestFixture, log NodeID, want int) {
	t.Helper()

	operations, err := fx.logs.Operations(fx.graph, log)
	if err != nil {
		t.Fatalf("Operations(%d): %v", log, err)
	}
	if len(operations) != want {
		t.Fatalf("Operations(%d) = %v, want %d operations", log, operations, want)
	}
}

func TestDomainStalenessPlainSetRemoveOfCurrentTargetIsRejected(t *testing.T) {
	forEachDomainPointerKind(t, func(t *testing.T, fx *domainPointerTestFixture, handle domainPointerHandle) {
		domain, err := fx.sets.NewSet(fx.graph)
		if err != nil {
			t.Fatalf("NewSet(): %v", err)
		}

		target := newTestNode(t, fx.graph)
		spare := newTestNode(t, fx.graph)
		for _, member := range []NodeID{target, spare} {
			if _, addErr := fx.sets.Add(fx.graph, domain, member); addErr != nil {
				t.Fatalf("Add(domain, %d): %v", member, addErr)
			}
		}

		if setErr := handle.setDomain(domain); setErr != nil {
			t.Fatalf("setDomain(): %v", setErr)
		}
		if setErr := handle.setTarget(target); setErr != nil {
			t.Fatalf("setTarget(): %v", setErr)
		}

		if _, removeErr := fx.sets.Remove(fx.graph, domain, target); !errors.Is(removeErr, ErrTargetOutsideDomain) {
			t.Fatalf("Remove(domain, target) error = %v, want %v", removeErr, ErrTargetOutsideDomain)
		}
		requireSetContains(t, fx, domain, target, true)
		requireHandleTarget(t, handle, target)

		removed, err := fx.sets.Remove(fx.graph, domain, spare)
		if err != nil {
			t.Fatalf("Remove(domain, spare): %v", err)
		}
		if !removed {
			t.Fatal("Remove(domain, spare) reported nothing removed")
		}

		extra := newTestNode(t, fx.graph)
		if _, addErr := fx.sets.Add(fx.graph, domain, extra); addErr != nil {
			t.Fatalf("Add(domain, extra): %v", addErr)
		}
	})
}

func TestDomainStalenessCompositeDomainMutationsThatStrandTargetAreRejected(t *testing.T) {
	forEachDomainPointerKind(t, func(t *testing.T, fx *domainPointerTestFixture, handle domainPointerHandle) {
		composite, err := fx.composites.NewCompositeSet(fx.graph)
		if err != nil {
			t.Fatalf("NewCompositeSet(): %v", err)
		}

		target := newTestNode(t, fx.graph)
		spare := newTestNode(t, fx.graph)

		targetOperand, err := fx.composites.AddOperand(fx.graph, composite, target, true, false)
		if err != nil {
			t.Fatalf("AddOperand(target): %v", err)
		}
		spareOperand, err := fx.composites.AddOperand(fx.graph, composite, spare, true, false)
		if err != nil {
			t.Fatalf("AddOperand(spare): %v", err)
		}

		if setErr := handle.setDomain(composite); setErr != nil {
			t.Fatalf("setDomain(): %v", setErr)
		}
		if setErr := handle.setTarget(target); setErr != nil {
			t.Fatalf("setTarget(): %v", setErr)
		}

		if removeErr := fx.composites.RemoveOperand(fx.graph, composite, targetOperand); !errors.Is(removeErr, ErrTargetOutsideDomain) {
			t.Fatalf("RemoveOperand(target) error = %v, want %v", removeErr, ErrTargetOutsideDomain)
		}
		requireCompositeOperandCount(t, fx, composite, 2)

		if _, subtractErr := fx.composites.AddOperand(fx.graph, composite, target, false, false); !errors.Is(subtractErr, ErrTargetOutsideDomain) {
			t.Fatalf("AddOperand(target, subtractive) error = %v, want %v", subtractErr, ErrTargetOutsideDomain)
		}
		requireCompositeOperandCount(t, fx, composite, 2)
		requireHandleTarget(t, handle, target)

		if removeErr := fx.composites.RemoveOperand(fx.graph, composite, spareOperand); removeErr != nil {
			t.Fatalf("RemoveOperand(spare): %v", removeErr)
		}
		requireCompositeOperandCount(t, fx, composite, 1)
	})
}

func TestDomainStalenessLogDomainMutationsThatStrandTargetAreRejected(t *testing.T) {
	forEachDomainPointerKind(t, func(t *testing.T, fx *domainPointerTestFixture, handle domainPointerHandle) {
		log, err := fx.logs.NewCompositeSetLog(fx.graph)
		if err != nil {
			t.Fatalf("NewCompositeSetLog(): %v", err)
		}

		target := newTestNode(t, fx.graph)
		spare := newTestNode(t, fx.graph)

		_, targetCapsule, err := fx.logs.AppendOperation(fx.graph, log, target, true, false)
		if err != nil {
			t.Fatalf("AppendOperation(target): %v", err)
		}
		_, spareCapsule, err := fx.logs.AppendOperation(fx.graph, log, spare, true, false)
		if err != nil {
			t.Fatalf("AppendOperation(spare): %v", err)
		}

		if setErr := handle.setDomain(log); setErr != nil {
			t.Fatalf("setDomain(): %v", setErr)
		}
		if setErr := handle.setTarget(target); setErr != nil {
			t.Fatalf("setTarget(): %v", setErr)
		}

		if _, _, subtractErr := fx.logs.AppendOperation(fx.graph, log, target, false, false); !errors.Is(subtractErr, ErrTargetOutsideDomain) {
			t.Fatalf("AppendOperation(target, subtractive) error = %v, want %v", subtractErr, ErrTargetOutsideDomain)
		}
		requireLogOperationCount(t, fx, log, 2)

		if removeErr := fx.logs.RemoveOperation(fx.graph, log, targetCapsule); !errors.Is(removeErr, ErrTargetOutsideDomain) {
			t.Fatalf("RemoveOperation(target) error = %v, want %v", removeErr, ErrTargetOutsideDomain)
		}
		requireLogOperationCount(t, fx, log, 2)
		requireHandleTarget(t, handle, target)

		if removeErr := fx.logs.RemoveOperation(fx.graph, log, spareCapsule); removeErr != nil {
			t.Fatalf("RemoveOperation(spare): %v", removeErr)
		}
		requireLogOperationCount(t, fx, log, 1)
	})
}

// TestDomainStalenessNestedSetShrinkIsRejectedThroughDiamond covers a
// three-level chain (Set -> two composites -> log, with the Set reachable
// through both composites) whose innermost Set is mutated. The reverse
// walk must reach the log's domain slot, visit the shared Set once, and
// terminate.
func TestDomainStalenessNestedSetShrinkIsRejectedThroughDiamond(t *testing.T) {
	forEachDomainPointerKind(t, func(t *testing.T, fx *domainPointerTestFixture, handle domainPointerHandle) {
		inner, err := fx.sets.NewSet(fx.graph)
		if err != nil {
			t.Fatalf("NewSet(): %v", err)
		}

		target := newTestNode(t, fx.graph)
		spare := newTestNode(t, fx.graph)
		for _, member := range []NodeID{target, spare} {
			if _, addErr := fx.sets.Add(fx.graph, inner, member); addErr != nil {
				t.Fatalf("Add(inner, %d): %v", member, addErr)
			}
		}

		log, err := fx.logs.NewCompositeSetLog(fx.graph)
		if err != nil {
			t.Fatalf("NewCompositeSetLog(): %v", err)
		}

		for i := 0; i < 2; i++ {
			composite, compositeErr := fx.composites.NewCompositeSet(fx.graph)
			if compositeErr != nil {
				t.Fatalf("NewCompositeSet(): %v", compositeErr)
			}
			if _, operandErr := fx.composites.AddOperand(fx.graph, composite, inner, true, true); operandErr != nil {
				t.Fatalf("AddOperand(composite, inner): %v", operandErr)
			}
			if _, _, appendErr := fx.logs.AppendOperation(fx.graph, log, composite, true, true); appendErr != nil {
				t.Fatalf("AppendOperation(log, composite): %v", appendErr)
			}
		}

		if setErr := handle.setDomain(log); setErr != nil {
			t.Fatalf("setDomain(): %v", setErr)
		}
		if setErr := handle.setTarget(target); setErr != nil {
			t.Fatalf("setTarget(): %v", setErr)
		}

		if _, removeErr := fx.sets.Remove(fx.graph, inner, target); !errors.Is(removeErr, ErrTargetOutsideDomain) {
			t.Fatalf("Remove(inner, target) error = %v, want %v", removeErr, ErrTargetOutsideDomain)
		}
		requireSetContains(t, fx, inner, target, true)

		if _, removeErr := fx.sets.Remove(fx.graph, inner, spare); removeErr != nil {
			t.Fatalf("Remove(inner, spare): %v", removeErr)
		}
	})
}

// TestDomainStalenessOneStrandedPointerRejectsWholeTransactionAcrossRepresentations
// shares one domain between a B pointer and a D pointer. Removing either
// pointer's target is rejected, and a single transaction removing both is
// rejected as a whole, leaving both members in place.
func TestDomainStalenessOneStrandedPointerRejectsWholeTransactionAcrossRepresentations(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	domain, err := fx.sets.NewSet(fx.graph)
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	bTarget := newTestNode(t, fx.graph)
	dTarget := newTestNode(t, fx.graph)
	free := newTestNode(t, fx.graph)
	for _, member := range []NodeID{bTarget, dTarget, free} {
		if _, addErr := fx.sets.Add(fx.graph, domain, member); addErr != nil {
			t.Fatalf("Add(domain, %d): %v", member, addErr)
		}
	}

	anchor := newTestNode(t, fx.graph)
	if newErr := fx.domainB.NewDomainPointer(fx.graph, anchor); newErr != nil {
		t.Fatalf("NewDomainPointer(): %v", newErr)
	}
	if setErr := fx.domainB.SetDomain(fx.graph, anchor, domain); setErr != nil {
		t.Fatalf("domainB.SetDomain(): %v", setErr)
	}
	if setErr := fx.domainB.SetTarget(fx.graph, anchor, bTarget); setErr != nil {
		t.Fatalf("domainB.SetTarget(): %v", setErr)
	}

	subject := newTestNode(t, fx.graph)
	if setErr := fx.domainD.SetDomain(fx.graph, subject, domain); setErr != nil {
		t.Fatalf("domainD.SetDomain(): %v", setErr)
	}
	if setErr := fx.domainD.SetTarget(fx.graph, subject, dTarget); setErr != nil {
		t.Fatalf("domainD.SetTarget(): %v", setErr)
	}

	if _, removeErr := fx.sets.Remove(fx.graph, domain, free); removeErr != nil {
		t.Fatalf("Remove(domain, free): %v", removeErr)
	}
	if _, removeErr := fx.sets.Remove(fx.graph, domain, dTarget); !errors.Is(removeErr, ErrTargetOutsideDomain) {
		t.Fatalf("Remove(domain, dTarget) error = %v, want %v", removeErr, ErrTargetOutsideDomain)
	}
	if _, removeErr := fx.sets.Remove(fx.graph, domain, bTarget); !errors.Is(removeErr, ErrTargetOutsideDomain) {
		t.Fatalf("Remove(domain, bTarget) error = %v, want %v", removeErr, ErrTargetOutsideDomain)
	}

	err = fx.graph.Transact(func(tx Tx) error {
		if removeErr := removeRelationshipTx(tx, domain, bTarget); removeErr != nil {
			return removeErr
		}

		return removeRelationshipTx(tx, domain, dTarget)
	})
	if !errors.Is(err, ErrTargetOutsideDomain) {
		t.Fatalf("Transact(remove both) error = %v, want %v", err, ErrTargetOutsideDomain)
	}

	requireSetContains(t, fx, domain, bTarget, true)
	requireSetContains(t, fx, domain, dTarget, true)
}

// TestDomainStalenessShrinkThenRetargetInOneTransactionIsAccepted shows
// the Checker judges the final state of a transaction, not each step: the
// intermediate state (domain shrunk, pointer not yet retargeted) is
// stranded, the final state is valid.
func TestDomainStalenessShrinkThenRetargetInOneTransactionIsAccepted(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	domain, err := fx.sets.NewSet(fx.graph)
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	oldTarget := newTestNode(t, fx.graph)
	newTarget := newTestNode(t, fx.graph)
	for _, member := range []NodeID{oldTarget, newTarget} {
		if _, addErr := fx.sets.Add(fx.graph, domain, member); addErr != nil {
			t.Fatalf("Add(domain, %d): %v", member, addErr)
		}
	}

	anchor := newTestNode(t, fx.graph)
	if newErr := fx.domainB.NewDomainPointer(fx.graph, anchor); newErr != nil {
		t.Fatalf("NewDomainPointer(): %v", newErr)
	}
	if setErr := fx.domainB.SetDomain(fx.graph, anchor, domain); setErr != nil {
		t.Fatalf("SetDomain(): %v", setErr)
	}
	if setErr := fx.domainB.SetTarget(fx.graph, anchor, oldTarget); setErr != nil {
		t.Fatalf("SetTarget(oldTarget): %v", setErr)
	}

	u, found, err := fx.domainB.subPointer(fx.graph, anchor)
	if err != nil || !found {
		t.Fatalf("subPointer(): found=%v err=%v", found, err)
	}

	err = fx.graph.Transact(func(tx Tx) error {
		if removeErr := removeRelationshipTx(tx, domain, oldTarget); removeErr != nil {
			return removeErr
		}
		if swapErr := removeRelationshipTx(tx, u, oldTarget); swapErr != nil {
			return swapErr
		}

		return addRelationshipTx(tx, u, newTarget)
	})
	if err != nil {
		t.Fatalf("Transact(shrink then retarget) error = %v, want nil", err)
	}

	target, hasTarget, err := fx.domainB.Target(fx.graph, anchor)
	if err != nil {
		t.Fatalf("Target(): %v", err)
	}
	if !hasTarget || target != newTarget {
		t.Fatalf("Target() = (%d,%v), want (%d,true)", target, hasTarget, newTarget)
	}
	requireSetContains(t, fx, domain, oldTarget, false)
}

// TestDomainPointerRegistryBCheckerCatchesOutOfBandTargetChange is the
// Representation B counterpart of
// TestDomainPointerRegistryDCheckerCatchesOutOfBandTargetChange: bypass
// the wrapper and mutate the underlying sub-pointer registry directly.
func TestDomainPointerRegistryBCheckerCatchesOutOfBandTargetChange(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	anchor := newTestNode(t, fx.graph)
	if newErr := fx.domainB.NewDomainPointer(fx.graph, anchor); newErr != nil {
		t.Fatalf("NewDomainPointer(): %v", newErr)
	}

	domain, err := fx.sets.NewSet(fx.graph)
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}
	allowed := newTestNode(t, fx.graph)
	if _, addErr := fx.sets.Add(fx.graph, domain, allowed); addErr != nil {
		t.Fatalf("Add(): %v", addErr)
	}
	if setErr := fx.domainB.SetDomain(fx.graph, anchor, domain); setErr != nil {
		t.Fatalf("SetDomain(): %v", setErr)
	}

	u, found, err := fx.domainB.subPointer(fx.graph, anchor)
	if err != nil || !found {
		t.Fatalf("subPointer(): found=%v err=%v", found, err)
	}

	disallowed := newTestNode(t, fx.graph)
	err = fx.domainB.pointers.SetTarget(fx.graph, u, disallowed)
	if !errors.Is(err, ErrTargetOutsideDomain) {
		t.Fatalf("bypassing SetTarget() error = %v, want %v", err, ErrTargetOutsideDomain)
	}

	target, hasTarget, err := fx.domainB.Target(fx.graph, anchor)
	if err != nil {
		t.Fatalf("Target(): %v", err)
	}
	if hasTarget {
		t.Fatalf("Target() = (%d,true), want no target after the Checker declined the bypassing commit", target)
	}
}

// TestDomainPointerRegistryBCheckerCatchesOutOfBandDomainChange is the
// Representation B counterpart of
// TestDomainPointerRegistryDCheckerCatchesOutOfBandDomainChange.
func TestDomainPointerRegistryBCheckerCatchesOutOfBandDomainChange(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	anchor := newTestNode(t, fx.graph)
	if newErr := fx.domainB.NewDomainPointer(fx.graph, anchor); newErr != nil {
		t.Fatalf("NewDomainPointer(): %v", newErr)
	}

	firstDomain, err := fx.sets.NewSet(fx.graph)
	if err != nil {
		t.Fatalf("NewSet(first): %v", err)
	}
	x := newTestNode(t, fx.graph)
	if _, addErr := fx.sets.Add(fx.graph, firstDomain, x); addErr != nil {
		t.Fatalf("Add(): %v", addErr)
	}
	if setErr := fx.domainB.SetDomain(fx.graph, anchor, firstDomain); setErr != nil {
		t.Fatalf("SetDomain(): %v", setErr)
	}
	if setErr := fx.domainB.SetTarget(fx.graph, anchor, x); setErr != nil {
		t.Fatalf("SetTarget(): %v", setErr)
	}

	secondDomain, err := fx.sets.NewSet(fx.graph)
	if err != nil {
		t.Fatalf("NewSet(second): %v", err)
	}
	// secondDomain deliberately does not contain x.

	slot, found, err := fx.domainB.domainSlotFor(fx.graph, anchor)
	if err != nil || !found {
		t.Fatalf("domainSlotFor(): found=%v err=%v", found, err)
	}

	err = fx.domainB.domainSlots.SetTarget(fx.graph, slot, secondDomain)
	if !errors.Is(err, ErrTargetOutsideDomain) {
		t.Fatalf("bypassing SetDomain() error = %v, want %v", err, ErrTargetOutsideDomain)
	}

	domain, hasDomain, err := fx.domainB.Domain(fx.graph, anchor)
	if err != nil {
		t.Fatalf("Domain(): %v", err)
	}
	if !hasDomain || domain != firstDomain {
		t.Fatalf("Domain() = (%d,%v), want (%d,true) -- unaffected by the declined bypass", domain, hasDomain, firstDomain)
	}
}

// domainBRig holds the registries needed to exercise
// DomainPointerRegistryB over an arbitrary GraphAPI (a GraphActor, a
// RootGraph, ...), unlike domainPointerTestFixture, which is fixed to a
// bare *Graph.
type domainBRig struct {
	sets    *SetRegistry
	domainB *DomainPointerRegistryB
}

func newDomainBRig(t *testing.T, api GraphAPI) domainBRig {
	t.Helper()

	names := NewNameRegistry(api)
	ids, err := names.BootstrapNames(api, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	sets, err := NewSetRegistry(api, ids[NameAllSets], ids[NameAllCompositeSets], ids[NameAllCompositeSetLogs])
	if err != nil {
		t.Fatalf("NewSetRegistry(): %v", err)
	}

	composites, err := NewCompositeSetRegistry(
		api,
		sets,
		ids[NameAllCompositeSets],
		ids[NameAllAdditiveOp],
		ids[NameAllSubtractiveOp],
		ids[NameAllScalarOperand],
		ids[NameAllSetOperand],
	)
	if err != nil {
		t.Fatalf("NewCompositeSetRegistry(): %v", err)
	}

	subPointers, err := NewPointerRegistry(api, ids[NameAllSubPointers])
	if err != nil {
		t.Fatalf("NewPointerRegistry(AllSubPointers): %v", err)
	}

	domainSlots, err := NewPointerRegistry(api, ids[NameAllDomainSlot])
	if err != nil {
		t.Fatalf("NewPointerRegistry(AllDomainSlot): %v", err)
	}

	return domainBRig{
		sets:    sets,
		domainB: NewDomainPointerRegistryB(api, subPointers, domainSlots, sets, composites, nil),
	}
}

// TestDomainPointerRegistryBCheckerToleratesUniversalRootParent runs two
// domain-constrained B pointers under a RootGraph, where ROOT is a virtual
// parent of every slot. Without addSlotOwners' universal-parent skip, ROOT
// would be treated as an anchor and its forward lookups would see both
// slots and report ambiguity, rejecting unrelated transactions. Staleness
// must still be detected.
func TestDomainPointerRegistryBCheckerToleratesUniversalRootParent(t *testing.T) {
	var g Graph

	root := newTestNode(t, &g)
	rootGraph, err := NewRootGraph(&g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	rig := newDomainBRig(t, rootGraph)

	type pointerCase struct {
		domain, target, spare NodeID
	}

	cases := make([]pointerCase, 2)
	for i := range cases {
		anchor, createErr := rootGraph.CreateNode()
		if createErr != nil {
			t.Fatalf("CreateNode(anchor): %v", createErr)
		}
		if newErr := rig.domainB.NewDomainPointer(rootGraph, anchor); newErr != nil {
			t.Fatalf("NewDomainPointer(): %v", newErr)
		}

		domain, setErr := rig.sets.NewSet(rootGraph)
		if setErr != nil {
			t.Fatalf("NewSet(): %v", setErr)
		}

		target, targetErr := rootGraph.CreateNode()
		if targetErr != nil {
			t.Fatalf("CreateNode(target): %v", targetErr)
		}
		spare, spareErr := rootGraph.CreateNode()
		if spareErr != nil {
			t.Fatalf("CreateNode(spare): %v", spareErr)
		}

		for _, member := range []NodeID{target, spare} {
			if _, addErr := rig.sets.Add(rootGraph, domain, member); addErr != nil {
				t.Fatalf("Add(domain, %d): %v", member, addErr)
			}
		}
		if domainErr := rig.domainB.SetDomain(rootGraph, anchor, domain); domainErr != nil {
			t.Fatalf("SetDomain(): %v", domainErr)
		}
		if pointErr := rig.domainB.SetTarget(rootGraph, anchor, target); pointErr != nil {
			t.Fatalf("SetTarget(): %v", pointErr)
		}

		cases[i] = pointerCase{domain: domain, target: target, spare: spare}
	}

	for _, c := range cases {
		if _, removeErr := rig.sets.Remove(rootGraph, c.domain, c.spare); removeErr != nil {
			t.Fatalf("Remove(domain, spare) error = %v, want nil", removeErr)
		}
		if _, removeErr := rig.sets.Remove(rootGraph, c.domain, c.target); !errors.Is(removeErr, ErrTargetOutsideDomain) {
			t.Fatalf("Remove(domain, target) error = %v, want %v", removeErr, ErrTargetOutsideDomain)
		}
	}
}

// TestDomainPointerRegistryBSetTargetRacingDomainShrinkNeverStrandsPointerUnderGraphActor
// races SetTarget against removal of the same member from the domain,
// entirely through one GraphActor. Each is read-then-commit across
// separate round trips, so they can interleave; the property under test
// is that the pointer never ends up targeting a non-member -- the losing
// side is declined with ErrTargetOutsideDomain (write-time or by the
// Checker), never committed stale.
func TestDomainPointerRegistryBSetTargetRacingDomainShrinkNeverStrandsPointerUnderGraphActor(t *testing.T) {
	actor := NewGraphActor(&Graph{})
	defer actor.Close()

	rig := newDomainBRig(t, actor)

	anchor, err := actor.CreateNode()
	if err != nil {
		t.Fatalf("CreateNode(anchor): %v", err)
	}
	if newErr := rig.domainB.NewDomainPointer(actor, anchor); newErr != nil {
		t.Fatalf("NewDomainPointer(): %v", newErr)
	}

	domain, err := rig.sets.NewSet(actor)
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}
	if setErr := rig.domainB.SetDomain(actor, anchor, domain); setErr != nil {
		t.Fatalf("SetDomain(): %v", setErr)
	}

	const rounds = 100

	for round := 0; round < rounds; round++ {
		candidate, createErr := actor.CreateNode()
		if createErr != nil {
			t.Fatalf("round %d: CreateNode(): %v", round, createErr)
		}
		if _, addErr := rig.sets.Add(actor, domain, candidate); addErr != nil {
			t.Fatalf("round %d: Add(): %v", round, addErr)
		}

		var wg sync.WaitGroup
		var setErr, removeErr error

		wg.Add(2)
		go func() {
			defer wg.Done()
			setErr = rig.domainB.SetTarget(actor, anchor, candidate)
		}()
		go func() {
			defer wg.Done()
			_, removeErr = rig.sets.Remove(actor, domain, candidate)
		}()
		wg.Wait()

		for _, opErr := range []error{setErr, removeErr} {
			if opErr != nil && !errors.Is(opErr, ErrTargetOutsideDomain) {
				t.Fatalf("round %d: error = %v, want nil or %v", round, opErr, ErrTargetOutsideDomain)
			}
		}

		target, hasTarget, targetErr := rig.domainB.Target(actor, anchor)
		if targetErr != nil {
			t.Fatalf("round %d: Target(): %v", round, targetErr)
		}
		if !hasTarget {
			continue
		}

		member, memberErr := rig.sets.Contains(actor, domain, target)
		if memberErr != nil {
			t.Fatalf("round %d: Contains(): %v", round, memberErr)
		}
		if !member {
			t.Fatalf("round %d: pointer targets %d, which is no longer in its domain", round, target)
		}
	}
}

// TestDomainStalenessRepointingDescriptorOperandIsRejected covers
// re-pointing a descriptor's operand inside one transaction: the
// descriptor stays well-formed, so only the domain Checker can notice that
// the composite no longer contains the pointer's target.
func TestDomainStalenessRepointingDescriptorOperandIsRejected(t *testing.T) {
	forEachDomainPointerKind(t, func(t *testing.T, fx *domainPointerTestFixture, handle domainPointerHandle) {
		composite, err := fx.composites.NewCompositeSet(fx.graph)
		if err != nil {
			t.Fatalf("NewCompositeSet(): %v", err)
		}

		target := newTestNode(t, fx.graph)
		other := newTestNode(t, fx.graph)

		u, err := fx.composites.AddOperand(fx.graph, composite, target, true, false)
		if err != nil {
			t.Fatalf("AddOperand(target): %v", err)
		}

		if setErr := handle.setDomain(composite); setErr != nil {
			t.Fatalf("setDomain(): %v", setErr)
		}
		if setErr := handle.setTarget(target); setErr != nil {
			t.Fatalf("setTarget(): %v", setErr)
		}

		err = fx.graph.Transact(func(tx Tx) error {
			if removeErr := removeRelationshipTx(tx, u, target); removeErr != nil {
				return removeErr
			}

			return addRelationshipTx(tx, u, other)
		})
		if !errors.Is(err, ErrTargetOutsideDomain) {
			t.Fatalf("Transact(repoint) error = %v, want %v", err, ErrTargetOutsideDomain)
		}

		requireHandleTarget(t, handle, target)

		if !mustHasRelationship(t, fx.graph, u, target) {
			t.Fatal("the declined re-point was not rolled back")
		}

		// The same re-point is accepted when the target stays in the domain.
		err = fx.graph.Transact(func(tx Tx) error {
			if removeErr := removeRelationshipTx(tx, u, target); removeErr != nil {
				return removeErr
			}
			if addErr := addRelationshipTx(tx, u, other); addErr != nil {
				return addErr
			}

			_, operandErr := fx.composites.AddOperand(tx, composite, target, true, false)

			return operandErr
		})
		if err != nil {
			t.Fatalf("Transact(repoint and keep target) error = %v, want nil", err)
		}
	})
}

func TestDomainPointerRegistryDSetDomainFailureRollsBackMetadataCreation(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	subject := newTestNode(t, fx.graph)
	notASet := newTestNode(t, fx.graph)

	err := fx.domainD.SetDomain(fx.graph, subject, notASet)
	if !errors.Is(err, ErrInvalidSetOperand) {
		t.Fatalf("SetDomain(subject, notASet) error = %v, want %v", err, ErrInvalidSetOperand)
	}

	has, hasErr := fx.domainD.metadata.HasMetadata(fx.graph, subject)
	if hasErr != nil {
		t.Fatalf("HasMetadata(): %v", hasErr)
	}
	if has {
		t.Fatal("a rejected SetDomain left a metadata node behind; it should have rolled back with the rest")
	}
}

// TestDomainStalenessComposedExportedCallsAreJudgedAtOutermostCommit is
// the composition the nested-transaction design exists for: exported
// registry calls chained in one transaction, where an intermediate state
// strands the pointer and a later call repairs it. The domain Checker
// judges only the final state, at the outermost commit.
func TestDomainStalenessComposedExportedCallsAreJudgedAtOutermostCommit(t *testing.T) {
	fx := newDomainPointerTestFixture(t)

	domain, err := fx.sets.NewSet(fx.graph)
	if err != nil {
		t.Fatalf("NewSet(): %v", err)
	}

	oldTarget := newTestNode(t, fx.graph)
	newTarget := newTestNode(t, fx.graph)
	for _, member := range []NodeID{oldTarget, newTarget} {
		if _, addErr := fx.sets.Add(fx.graph, domain, member); addErr != nil {
			t.Fatalf("Add(domain, %d): %v", member, addErr)
		}
	}

	anchor := newTestNode(t, fx.graph)
	if newErr := fx.domainB.NewDomainPointer(fx.graph, anchor); newErr != nil {
		t.Fatalf("NewDomainPointer(): %v", newErr)
	}
	if setErr := fx.domainB.SetDomain(fx.graph, anchor, domain); setErr != nil {
		t.Fatalf("SetDomain(): %v", setErr)
	}
	if setErr := fx.domainB.SetTarget(fx.graph, anchor, oldTarget); setErr != nil {
		t.Fatalf("SetTarget(oldTarget): %v", setErr)
	}

	// Shrink the domain, then retarget: the state between the two calls
	// is stranded, the final state is valid, so the commit is accepted.
	err = fx.graph.Transact(func(tx Tx) error {
		if _, removeErr := fx.sets.Remove(tx, domain, oldTarget); removeErr != nil {
			return removeErr
		}

		return fx.domainB.SetTarget(tx, anchor, newTarget)
	})
	if err != nil {
		t.Fatalf("Transact(shrink then retarget) error = %v, want nil", err)
	}

	requireSetContains(t, fx, domain, oldTarget, false)

	target, hasTarget, targetErr := fx.domainB.Target(fx.graph, anchor)
	if targetErr != nil {
		t.Fatalf("Target(): %v", targetErr)
	}
	if !hasTarget || target != newTarget {
		t.Fatalf("Target() = (%d,%v), want (%d,true)", target, hasTarget, newTarget)
	}

	// Shrinking alone leaves the pointer stranded: the inner call reports
	// success provisionally and the outermost commit declines everything.
	err = fx.graph.Transact(func(tx Tx) error {
		removed, removeErr := fx.sets.Remove(tx, domain, newTarget)
		if removeErr != nil {
			return removeErr
		}
		if !removed {
			t.Error("inner Remove() = false, want a provisional true")
		}

		return nil
	})
	if !errors.Is(err, ErrTargetOutsideDomain) {
		t.Fatalf("Transact(shrink only) error = %v, want %v", err, ErrTargetOutsideDomain)
	}

	requireSetContains(t, fx, domain, newTarget, true)
}
