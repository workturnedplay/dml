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
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// The tests in this file run plans in-process, through Host.RunPlan; the wire
// side is in wire_plan_test.go.

// planTestNodeCount is how many nodes the host's graph has.
func planTestNodeCount(t *testing.T, h *Host) int {
	t.Helper()

	return len(mustFindNodes(t, h.Graph()))
}

// runPlanOK runs a plan that must succeed.
func runPlanOK(t *testing.T, h *Host, steps ...PlanStep) PlanResult {
	t.Helper()

	result, err := h.RunPlan(Plan{Steps: steps}, 0)
	if err != nil {
		t.Fatalf("RunPlan(): %v", err)
	}

	return result
}

func TestPlanBindsLabelsAndBuildsStructure(t *testing.T) {
	h := openTestHost(t, boltTestPath(t), nil)

	result := runPlanOK(t, h,
		PlanStep{Op: PlanCreate, Bind: "a"},
		PlanStep{Op: PlanCreate, Bind: "b"},
		PlanStep{Op: PlanAdd, From: PlanLabel("a"), To: PlanLabel("b")},
		PlanStep{Op: PlanEnsureNamed, Bind: "n", Name: "plan-test-node"},
		PlanStep{Op: PlanIntern, Bind: "text", Text: "cat"},
		PlanStep{Op: PlanAdd, From: PlanLabel("n"), To: PlanLabel("text")},
		PlanStep{Op: PlanAssertHas, From: PlanLabel("a"), To: PlanLabel("b")},
		PlanStep{Op: PlanAssertLacks, From: PlanLabel("b"), To: PlanLabel("a")},
		PlanStep{Op: PlanAssertExists, Node: PlanLabel("n")},
	)

	if len(result.Bound) != 4 {
		t.Fatalf("Bound = %v, want the four labels a, b, n and text", result.Bound)
	}

	if !mustHasRelationship(t, h.Graph(), result.Bound["a"], result.Bound["b"]) {
		t.Fatal("the relationship (a, b) was not made")
	}

	if named, ok := h.names.Lookup("plan-test-node"); !ok || named != result.Bound["n"] {
		t.Fatalf("Lookup(plan-test-node) = (%d,%v), want (%d,true)", named, ok, result.Bound["n"])
	}

	interned, found, lookupErr := h.Registries().Text.Lookup(h.Graph(), "cat")
	if lookupErr != nil || !found || interned != result.Bound["text"] {
		t.Fatalf("Lookup(cat) = (%d,%v,%v), want (%d,true,nil)", interned, found, lookupErr, result.Bound["text"])
	}

	if !mustHasRelationship(t, h.Graph(), result.Bound["n"], result.Bound["text"]) {
		t.Fatal("the relationship (n, text) was not made")
	}
}

func TestPlanReferencesNodesByName(t *testing.T) {
	h := openTestHost(t, boltTestPath(t), nil)

	first := runPlanOK(t, h, PlanStep{Op: PlanEnsureNamed, Bind: "n", Name: "plan-test-node"})

	second := runPlanOK(t, h,
		PlanStep{Op: PlanEnsureNamed, Bind: "again", Name: "plan-test-node"},
		PlanStep{Op: PlanCreate, Bind: "x"},
		PlanStep{Op: PlanAdd, From: PlanName("plan-test-node"), To: PlanLabel("x")},
	)

	if second.Bound["again"] != first.Bound["n"] {
		t.Fatalf("ensure_named again = %d, want the same node %d", second.Bound["again"], first.Bound["n"])
	}

	if !mustHasRelationship(t, h.Graph(), first.Bound["n"], second.Bound["x"]) {
		t.Fatal("the relationship from the node named by PlanName was not made")
	}

	// A name made earlier in the same plan is already visible to later steps.
	third := runPlanOK(t, h,
		PlanStep{Op: PlanEnsureNamed, Bind: "n", Name: "plan-test-same-plan"},
		PlanStep{Op: PlanCreate, Bind: "x"},
		PlanStep{Op: PlanAdd, From: PlanName("plan-test-same-plan"), To: PlanLabel("x")},
	)

	if !mustHasRelationship(t, h.Graph(), third.Bound["n"], third.Bound["x"]) {
		t.Fatal("a name made by an earlier step of the same plan was not found by PlanName")
	}
}

func TestPlanRejectsInvalidPlansWithoutTouchingTheGraph(t *testing.T) {
	h := openTestHost(t, boltTestPath(t), nil)
	before := planTestNodeCount(t, h)

	cases := []struct {
		name  string
		steps []PlanStep
	}{
		{name: "no steps"},
		{name: "unknown operation", steps: []PlanStep{{Op: "explode"}}},
		{name: "a field the operation does not use", steps: []PlanStep{{Op: PlanCreate, Bind: "a", Name: "x"}}},
		{name: "a missing field", steps: []PlanStep{{Op: PlanAdd, From: PlanNode(1)}}},
		{name: "a label no earlier step binds", steps: []PlanStep{{Op: PlanAssertExists, Node: PlanLabel("ghost")}}},
		{name: "a label bound twice", steps: []PlanStep{{Op: PlanCreate, Bind: "a"}, {Op: PlanCreate, Bind: "a"}}},
		{name: "a reference of two kinds", steps: []PlanStep{
			{Op: PlanCreate, Bind: "a"},
			{Op: PlanAssertExists, Node: PlanRef{Label: "a", Name: "x"}},
		}},
		{name: "a reserved name", steps: []PlanStep{{Op: PlanEnsureNamed, Bind: "a", Name: resourcePrefix + "x"}}},
		{name: "text that is not UTF-8", steps: []PlanStep{{Op: PlanIntern, Bind: "a", Text: "\xff"}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := h.RunPlan(Plan{Steps: tc.steps}, 0); !errors.Is(err, ErrPlanInvalid) {
				t.Fatalf("RunPlan() error = %v, want %v", err, ErrPlanInvalid)
			}

			if got := planTestNodeCount(t, h); got != before {
				t.Fatalf("%d nodes after an invalid plan, want %d", got, before)
			}
		})
	}
}

func TestPlanStepLimit(t *testing.T) {
	h := openTestHost(t, boltTestPath(t), nil)

	three := Plan{Steps: []PlanStep{
		{Op: PlanCreate, Bind: "a"},
		{Op: PlanCreate, Bind: "b"},
		{Op: PlanCreate, Bind: "c"},
	}}

	if _, err := h.RunPlan(three, 2); !errors.Is(err, ErrPlanInvalid) {
		t.Fatalf("RunPlan(3 steps, limit 2) error = %v, want %v", err, ErrPlanInvalid)
	}

	if _, err := h.RunPlan(Plan{Steps: three.Steps[:2]}, 2); err != nil {
		t.Fatalf("RunPlan(2 steps, limit 2): %v", err)
	}

	// Zero means the default limit.
	if _, err := h.RunPlan(three, 0); err != nil {
		t.Fatalf("RunPlan(3 steps, default limit): %v", err)
	}
}

// TestPlanConflictsNameTheStepAndLeaveNothingBehind: a plan that does not fit
// the graph fails as a whole, says which step, and changes nothing, however
// much of it had already run.
func TestPlanConflictsNameTheStepAndLeaveNothingBehind(t *testing.T) {
	h := openTestHost(t, boltTestPath(t), nil)
	before := planTestNodeCount(t, h)

	cases := []struct {
		name  string
		steps []PlanStep
		step  int
	}{
		{name: "assert_lacks of a relationship that exists", step: 3, steps: []PlanStep{
			{Op: PlanCreate, Bind: "a"},
			{Op: PlanCreate, Bind: "b"},
			{Op: PlanAdd, From: PlanLabel("a"), To: PlanLabel("b")},
			{Op: PlanAssertLacks, From: PlanLabel("a"), To: PlanLabel("b")},
		}},
		{name: "assert_has of a relationship that does not exist", step: 2, steps: []PlanStep{
			{Op: PlanCreate, Bind: "a"},
			{Op: PlanCreate, Bind: "b"},
			{Op: PlanAssertHas, From: PlanLabel("a"), To: PlanLabel("b")},
		}},
		{name: "assert_exists of a node that does not exist", step: 0, steps: []PlanStep{
			{Op: PlanAssertExists, Node: PlanNode(999999)},
		}},
		{name: "a relationship to a node that does not exist", step: 1, steps: []PlanStep{
			{Op: PlanCreate, Bind: "a"},
			{Op: PlanAdd, From: PlanLabel("a"), To: PlanNode(999999)},
		}},
		{name: "deleting a node that still has a relationship", step: 3, steps: []PlanStep{
			{Op: PlanCreate, Bind: "a"},
			{Op: PlanCreate, Bind: "b"},
			{Op: PlanAdd, From: PlanLabel("a"), To: PlanLabel("b")},
			{Op: PlanDelete, Node: PlanLabel("a")},
		}},
		{name: "a label whose node an earlier step deleted", step: 2, steps: []PlanStep{
			{Op: PlanCreate, Bind: "a"},
			{Op: PlanDelete, Node: PlanLabel("a")},
			{Op: PlanAssertExists, Node: PlanLabel("a")},
		}},
		{name: "a name that is not bound", step: 0, steps: []PlanStep{
			{Op: PlanAssertExists, Node: PlanName("no-such-name")},
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := h.RunPlan(Plan{Steps: tc.steps}, 0)
			if !errors.Is(err, ErrPlanConflict) {
				t.Fatalf("RunPlan() error = %v, want %v", err, ErrPlanConflict)
			}

			if want := fmt.Sprintf("step %d", tc.step); !strings.Contains(err.Error(), want) {
				t.Fatalf("RunPlan() error = %q, want it to name %q", err, want)
			}

			if got := planTestNodeCount(t, h); got != before {
				t.Fatalf("%d nodes after a conflicting plan, want %d (nothing may be left behind)", got, before)
			}
		})
	}
}

// TestPlanSurvivesJSONWithNodeZero: node 0 is a valid NodeID, so a reference
// to it must survive the wire as "set", not be mistaken for "not set".
func TestPlanSurvivesJSONWithNodeZero(t *testing.T) {
	plan := Plan{Steps: []PlanStep{
		{Op: PlanAssertExists, Node: PlanNode(0)},
		{Op: PlanAdd, From: PlanName("n"), To: PlanLabel("l")},
		{Op: PlanIntern, Bind: "t", Text: "héllo"},
	}}

	raw, marshalErr := json.Marshal(plan)
	if marshalErr != nil {
		t.Fatalf("Marshal(): %v", marshalErr)
	}

	var got Plan

	if unmarshalErr := json.Unmarshal(raw, &got); unmarshalErr != nil {
		t.Fatalf("Unmarshal(%s): %v", raw, unmarshalErr)
	}

	if !reflect.DeepEqual(got, plan) {
		t.Fatalf("round trip = %+v, want %+v", got, plan)
	}

	if got.Steps[0].Node.ID == nil {
		t.Fatal("a reference to node 0 came back as not set")
	}
}

func TestRegisterResourceRefusesThePlanResourceName(t *testing.T) {
	h := openTestHost(t, boltTestPath(t), nil)

	if err := h.RegisterResource(PlanResource, nil); !errors.Is(err, ErrResourceName) {
		t.Fatalf("RegisterResource(%q) error = %v, want %v", PlanResource, err, ErrResourceName)
	}
}