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
	"slices"
	"testing"
)

// The tests in this file run CompositeSetLogRegistry over each OrderedList
// implementation, on every backend: a log must behave the same whichever list
// type it keeps its operations in.

// bootstrapTestNames bootstraps every foundational name on graph.
func bootstrapTestNames(t *testing.T, graph testGraph) map[string]NodeID {
	t.Helper()

	ids, err := NewNameRegistry(graph).BootstrapNames(graph, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	return ids
}

// listRegistryFromIDs constructs a ListRegistry, with its capsule registry,
// from already bootstrapped names.
func listRegistryFromIDs(t *testing.T, graph GraphAPI, ids map[string]NodeID) *ListRegistry {
	t.Helper()

	capsules, err := NewCapsuleRegistry(
		graph,
		ids[NameAllElementCapsules],
		ids[NameAllElementCapsulePrevSlot],
		ids[NameAllElementCapsuleValueSlot],
		ids[NameAllElementCapsuleNextSlot],
	)
	if err != nil {
		t.Fatalf("NewCapsuleRegistry(): %v", err)
	}

	lists, err := NewListRegistry(graph, capsules, ids[NameAllLists], ids[NameAllHeads], ids[NameAllTails])
	if err != nil {
		t.Fatalf("NewListRegistry(): %v", err)
	}

	return lists
}

// slottedListFromIDs constructs a SlottedListRegistry, with a capsule registry
// of its own, from already bootstrapped names.
func slottedListFromIDs(t *testing.T, graph GraphAPI, ids map[string]NodeID) *SlottedListRegistry {
	t.Helper()

	capsules, err := NewCapsuleRegistry(
		graph,
		ids[NameAllSlottedCapsules],
		ids[NameAllSlottedCapsulePrevSlot],
		ids[NameAllSlottedCapsuleValueSlot],
		ids[NameAllSlottedCapsuleNextSlot],
	)
	if err != nil {
		t.Fatalf("NewCapsuleRegistry(): %v", err)
	}

	lists, err := NewSlottedListRegistry(
		graph,
		capsules,
		ids[NameAllSlottedLists],
		ids[NameAllSlottedListMembers],
		ids[NameAllSlottedListHeadSlot],
		ids[NameAllSlottedListTailSlot],
	)
	if err != nil {
		t.Fatalf("NewSlottedListRegistry(): %v", err)
	}

	return lists
}

// setsAndCompositesFromIDs constructs the Set and CompositeSet registries.
func setsAndCompositesFromIDs(t *testing.T, graph GraphAPI, ids map[string]NodeID) (*SetRegistry, *CompositeSetRegistry) {
	t.Helper()

	sets, err := NewSetRegistry(graph, ids[NameAllSets], ids[NameAllCompositeSets], ids[NameAllCompositeSetLogs])
	if err != nil {
		t.Fatalf("NewSetRegistry(): %v", err)
	}

	composites, err := NewCompositeSetRegistry(
		graph,
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

	return sets, composites
}

// logsOver constructs a CompositeSetLogRegistry that keeps its operations in
// lists.
func logsOver(t *testing.T, graph GraphAPI, ids map[string]NodeID, lists OrderedList, composites *CompositeSetRegistry) *CompositeSetLogRegistry {
	t.Helper()

	logs, err := NewCompositeSetLogRegistry(
		graph,
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

	return logs
}

// logListKind is one OrderedList implementation a log can be built over.
type logListKind struct {
	name  string
	build func(t *testing.T, graph testGraph, ids map[string]NodeID) OrderedList
}

var logListKinds = []logListKind{
	{name: "ListRegistry", build: func(t *testing.T, graph testGraph, ids map[string]NodeID) OrderedList {
		return listRegistryFromIDs(t, graph, ids)
	}},
	{name: "SlottedListRegistry", build: func(t *testing.T, graph testGraph, ids map[string]NodeID) OrderedList {
		return slottedListFromIDs(t, graph, ids)
	}},
}

// logRig is a CompositeSetLogRegistry over one list type, with the Set,
// CompositeSet and Representation D domain pointer registries around it.
type logRig struct {
	graph      testGraph
	sets       *SetRegistry
	composites *CompositeSetRegistry
	lists      OrderedList
	logs       *CompositeSetLogRegistry
	domainD    *DomainPointerRegistryD
}

func newLogRig(t *testing.T, graph testGraph, kind logListKind) *logRig {
	t.Helper()

	ids := bootstrapTestNames(t, graph)
	sets, composites := setsAndCompositesFromIDs(t, graph, ids)
	lists := kind.build(t, graph, ids)
	logs := logsOver(t, graph, ids, lists, composites)
	composites.SetLogs(logs)

	metadata, err := NewPointerMetadataRegistryD(graph, ids[NameAllPointerMetadata], ids[NameAllPointerMetadataSubjectSlot], ids[NameAllPointerMetadataTargetSlot])
	if err != nil {
		t.Fatalf("NewPointerMetadataRegistryD(): %v", err)
	}

	domainSlots, err := NewPointerRegistry(graph, ids[NameAllDomainSlot])
	if err != nil {
		t.Fatalf("NewPointerRegistry(AllDomainSlot): %v", err)
	}

	return &logRig{
		graph:      graph,
		sets:       sets,
		composites: composites,
		lists:      lists,
		logs:       logs,
		domainD:    NewDomainPointerRegistryD(graph, metadata, domainSlots, sets, composites, logs),
	}
}

// forEachLogRig runs body once per backend and list type, each on a fresh
// graph.
func forEachLogRig(t *testing.T, body func(t *testing.T, rig *logRig)) {
	t.Helper()

	for _, backend := range testBackends() {
		for _, kind := range logListKinds {
			t.Run(backend.name+"/"+kind.name, func(t *testing.T) {
				body(t, newLogRig(t, backend.open(t), kind))
			})
		}
	}
}

func mustNewLog(t *testing.T, rig *logRig) NodeID {
	t.Helper()

	log, err := rig.logs.NewCompositeSetLog(rig.graph)
	if err != nil {
		t.Fatalf("NewCompositeSetLog(): %v", err)
	}

	return log
}

// mustAppendOperation appends an operation and returns its descriptor and
// capsule.
func mustAppendOperation(t *testing.T, rig *logRig, log, operand NodeID, additive, expand bool) (NodeID, NodeID) {
	t.Helper()

	u, capsule, err := rig.logs.AppendOperation(rig.graph, log, operand, additive, expand)
	if err != nil {
		t.Fatalf("AppendOperation(%d): %v", operand, err)
	}

	return u, capsule
}

func TestCompositeSetLogOnEveryListTypeContainsAgreesWithEvaluate(t *testing.T) {
	forEachLogRig(t, func(t *testing.T, rig *logRig) {
		g := rig.graph
		a := mustCreateNode(t, g)
		b := mustCreateNode(t, g)
		c := mustCreateNode(t, g)
		d := mustCreateNode(t, g)
		e := mustCreateNode(t, g)

		set, err := rig.sets.NewSet(g)
		if err != nil {
			t.Fatalf("NewSet(): %v", err)
		}

		for _, member := range []NodeID{b, d} {
			if _, addErr := rig.sets.Add(g, set, member); addErr != nil {
				t.Fatalf("Add(set, %d): %v", member, addErr)
			}
		}

		// composite = {e} minus {d} = {e}
		composite, err := rig.composites.NewCompositeSet(g)
		if err != nil {
			t.Fatalf("NewCompositeSet(): %v", err)
		}

		if _, operandErr := rig.composites.AddOperand(g, composite, e, true, false); operandErr != nil {
			t.Fatalf("AddOperand(e): %v", operandErr)
		}

		if _, operandErr := rig.composites.AddOperand(g, composite, d, false, false); operandErr != nil {
			t.Fatalf("AddOperand(d, subtractive): %v", operandErr)
		}

		log := mustNewLog(t, rig)

		// {a} -> {a,b,d} -> {a,d} -> {a,d,e} -> {a,e} -> {a,b,e}
		for _, op := range []struct {
			operand          NodeID
			additive, expand bool
		}{
			{operand: a, additive: true},
			{operand: set, additive: true, expand: true},
			{operand: b},
			{operand: composite, additive: true, expand: true},
			{operand: set, expand: true},
			{operand: b, additive: true},
		} {
			mustAppendOperation(t, rig, log, op.operand, op.additive, op.expand)
		}

		evaluated, err := rig.logs.Evaluate(g, log)
		if err != nil {
			t.Fatalf("Evaluate(): %v", err)
		}

		if want := sortedNodeIDs([]NodeID{a, b, e}); !reflect.DeepEqual(sortedNodeIDs(evaluated), want) {
			t.Fatalf("Evaluate() = %v, want %v", evaluated, want)
		}

		for _, node := range []NodeID{a, b, c, d, e} {
			got, containsErr := rig.logs.Contains(g, log, node)
			if containsErr != nil {
				t.Fatalf("Contains(%d): %v", node, containsErr)
			}

			if want := slices.Contains(evaluated, node); got != want {
				t.Fatalf("Contains(%d) = %v, want %v (Evaluate() = %v)", node, got, want, evaluated)
			}
		}
	})
}

func TestCompositeSetLogOnEveryListTypeRemovesOperations(t *testing.T) {
	forEachLogRig(t, func(t *testing.T, rig *logRig) {
		g := rig.graph
		log := mustNewLog(t, rig)
		other := mustNewLog(t, rig)

		x := mustCreateNode(t, g)
		y := mustCreateNode(t, g)
		z := mustCreateNode(t, g)

		u1, capsule1 := mustAppendOperation(t, rig, log, x, true, false)
		u2, capsule2 := mustAppendOperation(t, rig, log, y, true, false)
		u3, _ := mustAppendOperation(t, rig, log, z, true, false)

		if err := rig.logs.RemoveOperation(g, log, capsule2); err != nil {
			t.Fatalf("RemoveOperation(middle): %v", err)
		}

		operations, err := rig.logs.Operations(g, log)
		if err != nil || !reflect.DeepEqual(operations, []NodeID{u1, u3}) {
			t.Fatalf("Operations() = (%v,%v), want ([%d %d],nil)", operations, err, u1, u3)
		}

		if mustNodeExists(t, g, u2) || mustNodeExists(t, g, capsule2) {
			t.Fatal("the removed operation's descriptor or capsule survived")
		}

		evaluated, err := rig.logs.Evaluate(g, log)
		if err != nil || !reflect.DeepEqual(sortedNodeIDs(evaluated), sortedNodeIDs([]NodeID{x, z})) {
			t.Fatalf("Evaluate() = (%v,%v), want {x, z}", evaluated, err)
		}

		// A capsule of one log is not an operation of another.
		if removeErr := rig.logs.RemoveOperation(g, other, capsule1); !errors.Is(removeErr, ErrCapsuleNotInList) {
			t.Fatalf("RemoveOperation(another log's capsule) error = %v, want %v", removeErr, ErrCapsuleNotInList)
		}
	})
}

func TestCompositeSetLogOnEveryListTypeDeleteOnlyWhenEmptyAndLeavesNothing(t *testing.T) {
	forEachLogRig(t, func(t *testing.T, rig *logRig) {
		g := rig.graph
		x := mustCreateNode(t, g)
		before := len(mustFindNodes(t, g))

		log := mustNewLog(t, rig)
		_, capsule := mustAppendOperation(t, rig, log, x, true, false)

		if err := rig.logs.DeleteCompositeSetLog(g, log); !errors.Is(err, ErrNodeNotEmpty) {
			t.Fatalf("DeleteCompositeSetLog() of a non-empty log error = %v, want %v", err, ErrNodeNotEmpty)
		}

		// A refused delete changes nothing, both tags included.
		if !mustIsCompositeSetLog(t, rig.logs, g, log) || !mustIsList(t, rig.lists, g, log) {
			t.Fatal("a refused DeleteCompositeSetLog() removed a tag")
		}

		if err := rig.logs.RemoveOperation(g, log, capsule); err != nil {
			t.Fatalf("RemoveOperation(): %v", err)
		}

		if err := rig.logs.DeleteCompositeSetLog(g, log); err != nil {
			t.Fatalf("DeleteCompositeSetLog() of an empty log: %v", err)
		}

		if got := len(mustFindNodes(t, g)); got != before {
			t.Fatalf("%d nodes are left after creating and deleting a log, want %d", got, before)
		}
	})
}

func TestCompositeSetLogOnEveryListTypeResolvesNestedLogsAndDetectsCycles(t *testing.T) {
	forEachLogRig(t, func(t *testing.T, rig *logRig) {
		g := rig.graph
		inner := mustNewLog(t, rig)
		outer := mustNewLog(t, rig)
		x := mustCreateNode(t, g)

		mustAppendOperation(t, rig, inner, x, true, false)
		mustAppendOperation(t, rig, outer, inner, true, true)

		got, err := rig.logs.Evaluate(g, outer)
		if err != nil || !reflect.DeepEqual(got, []NodeID{x}) {
			t.Fatalf("Evaluate(outer) = (%v,%v), want ([%d],nil)", got, err, x)
		}

		if in, containsErr := rig.logs.Contains(g, outer, x); containsErr != nil || !in {
			t.Fatalf("Contains(outer, x) = (%v,%v), want (true,nil)", in, containsErr)
		}

		mustAppendOperation(t, rig, inner, outer, true, true)

		if _, evalErr := rig.logs.Evaluate(g, outer); !errors.Is(evalErr, ErrCompositeSetCycle) {
			t.Fatalf("Evaluate(outer) in a cycle error = %v, want %v", evalErr, ErrCompositeSetCycle)
		}
	})
}

// TestCompositeSetLogOnEveryListTypeDomainStaleness: a log is a pointer's
// domain, and a change to the log that strands the pointer is declined,
// whichever list type the log keeps its operations in. A slotted list's
// mutations do not touch the log node, which is why the log registry touches it.
func TestCompositeSetLogOnEveryListTypeDomainStaleness(t *testing.T) {
	forEachLogRig(t, func(t *testing.T, rig *logRig) {
		g := rig.graph
		log := mustNewLog(t, rig)
		target := mustCreateNode(t, g)
		spare := mustCreateNode(t, g)

		_, targetCapsule := mustAppendOperation(t, rig, log, target, true, false)
		_, spareCapsule := mustAppendOperation(t, rig, log, spare, true, false)

		subject := mustCreateNode(t, g)

		if err := rig.domainD.SetDomain(g, subject, log); err != nil {
			t.Fatalf("SetDomain(): %v", err)
		}

		if err := rig.domainD.SetTarget(g, subject, target); err != nil {
			t.Fatalf("SetTarget(): %v", err)
		}

		requireOperations := func(want int) {
			t.Helper()

			operations, err := rig.logs.Operations(g, log)
			if err != nil || len(operations) != want {
				t.Fatalf("Operations() = (%v,%v), want %d operations", operations, err, want)
			}
		}

		if _, _, err := rig.logs.AppendOperation(g, log, target, false, false); !errors.Is(err, ErrTargetOutsideDomain) {
			t.Fatalf("AppendOperation(target, subtractive) error = %v, want %v", err, ErrTargetOutsideDomain)
		}

		requireOperations(2)

		if err := rig.logs.RemoveOperation(g, log, targetCapsule); !errors.Is(err, ErrTargetOutsideDomain) {
			t.Fatalf("RemoveOperation(target) error = %v, want %v", err, ErrTargetOutsideDomain)
		}

		requireOperations(2)

		if err := rig.logs.RemoveOperation(g, log, spareCapsule); err != nil {
			t.Fatalf("RemoveOperation(spare): %v", err)
		}

		requireOperations(1)

		if current, hasTarget, err := rig.domainD.Target(g, subject); err != nil || !hasTarget || current != target {
			t.Fatalf("Target() = (%d,%v,%v), want (%d,true,nil)", current, hasTarget, err, target)
		}
	})
}

// TestCompositeSetLogOfTheOtherListTypeFailsLoudly: a registry over slotted
// lists is given a log made by one over ListRegistry lists. The tag says it is
// a log, so it is accepted as an operand, and reading it fails with ErrNotList
// instead of returning something wrong.
func TestCompositeSetLogOfTheOtherListTypeFailsLoudly(t *testing.T) {
	g := newTestGraph(t)
	ids := bootstrapTestNames(t, g)
	_, composites := setsAndCompositesFromIDs(t, g, ids)

	plain := logsOver(t, g, ids, listRegistryFromIDs(t, g, ids), composites)
	slotted := logsOver(t, g, ids, slottedListFromIDs(t, g, ids), composites)
	composites.SetLogs(slotted)

	inner, err := plain.NewCompositeSetLog(g)
	if err != nil {
		t.Fatalf("NewCompositeSetLog() over ListRegistry: %v", err)
	}

	outer, err := slotted.NewCompositeSetLog(g)
	if err != nil {
		t.Fatalf("NewCompositeSetLog() over SlottedListRegistry: %v", err)
	}

	x := mustCreateNode(t, g)

	if _, _, appendErr := plain.AppendOperation(g, inner, x, true, false); appendErr != nil {
		t.Fatalf("AppendOperation(inner): %v", appendErr)
	}

	if _, _, appendErr := slotted.AppendOperation(g, outer, inner, true, true); appendErr != nil {
		t.Fatalf("AppendOperation(outer, a log of the other type): %v", appendErr)
	}

	if _, evalErr := slotted.Evaluate(g, outer); !errors.Is(evalErr, ErrNotList) {
		t.Fatalf("Evaluate(outer) error = %v, want %v", evalErr, ErrNotList)
	}
}

func TestHostLogsKeepTheirOperationsInSlottedLists(t *testing.T) {
	h := openTestHost(t, boltTestPath(t), nil)
	reg := h.Registries()

	log, err := reg.Logs.NewCompositeSetLog(h.Graph())
	if err != nil {
		t.Fatalf("NewCompositeSetLog(): %v", err)
	}

	if is, listErr := reg.SlottedLists.IsList(h.Graph(), log); listErr != nil || !is {
		t.Fatalf("SlottedLists.IsList(log) = (%v,%v), want (true,nil)", is, listErr)
	}

	if is, listErr := reg.Lists.IsList(h.Graph(), log); listErr != nil || is {
		t.Fatalf("Lists.IsList(log) = (%v,%v), want (false,nil)", is, listErr)
	}

	x := mustCreateNode(t, h.Graph())

	if _, _, appendErr := reg.Logs.AppendOperation(h.Graph(), log, x, true, false); appendErr != nil {
		t.Fatalf("AppendOperation(): %v", appendErr)
	}

	if in, containsErr := reg.Logs.Contains(h.Graph(), log, x); containsErr != nil || !in {
		t.Fatalf("Contains() = (%v,%v), want (true,nil)", in, containsErr)
	}
}