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
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// The tests in this file run plans over the wire, over net.Pipe, so they need
// no sockets.

// newPlanRig serves a host to peers that are all identified as principal,
// under authorizer (nil: none), plus opts (WithPlans, usually).
func newPlanRig(t *testing.T, principal Principal, authorizer Authorizer, opts ...WireOption) *wireRig {
	t.Helper()

	identify := func(net.Conn) (Principal, error) { return principal, nil }
	all := append([]WireOption{WithPeerIdentifier(identify), WithAuthorizer(authorizer)}, opts...)

	return newWireRig(t, nil, all...)
}

// alicePlanPolicy lets alice, and only alice, submit plans.
func alicePlanPolicy() *ResourcePolicy {
	return NewResourcePolicy(map[string][]Principal{PlanResource: {"alice"}})
}

// createPlan is a plan that creates one node per label.
func createPlan(labels ...string) Plan {
	steps := make([]PlanStep, 0, len(labels))

	for _, label := range labels {
		steps = append(steps, PlanStep{Op: PlanCreate, Bind: label})
	}

	return Plan{Steps: steps}
}

func TestWirePlanRunsAndReturnsTheBoundLabels(t *testing.T) {
	ctx := hostTestContext(t)
	rig := newPlanRig(t, "alice", alicePlanPolicy(), WithPlans(0))
	client := rig.dial(t)

	plan := createPlan("a", "b")
	plan.Steps = append(plan.Steps, PlanStep{Op: PlanAdd, From: PlanLabel("a"), To: PlanLabel("b")})

	result, err := client.RunPlan(ctx, plan)
	if err != nil {
		t.Fatalf("RunPlan(): %v", err)
	}

	if len(result.Bound) != 2 {
		t.Fatalf("Bound = %v, want the labels a and b", result.Bound)
	}

	if !mustHasRelationship(t, rig.host.Graph(), result.Bound["a"], result.Bound["b"]) {
		t.Fatal("the relationship the plan added is not in the host's graph")
	}
}

func TestWirePlanErrorsKeepTheirIdentity(t *testing.T) {
	ctx := hostTestContext(t)
	rig := newPlanRig(t, "alice", alicePlanPolicy(), WithPlans(0))
	client := rig.dial(t)

	cases := []struct {
		name string
		plan Plan
		want error
		code string
	}{
		{name: "invalid", plan: Plan{Steps: []PlanStep{{Op: "explode"}}}, want: ErrPlanInvalid, code: "plan_invalid"},
		{name: "no steps", plan: Plan{}, want: ErrPlanInvalid, code: "plan_invalid"},
		{name: "conflict", plan: Plan{Steps: []PlanStep{{Op: PlanAssertExists, Node: PlanNode(999999)}}}, want: ErrPlanConflict, code: "plan_conflict"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.RunPlan(ctx, tc.plan)

			var remote *RemoteError
			if !errors.Is(err, tc.want) || !errors.As(err, &remote) || remote.Code != tc.code {
				t.Fatalf("RunPlan() error = %#v, want a *RemoteError with code %s wrapping %v", err, tc.code, tc.want)
			}
		})
	}
}

// TestWirePlanIsRefusedUnlessEnabledAndAuthorized: plans fail closed, and a
// refused plan changes nothing.
func TestWirePlanIsRefusedUnlessEnabledAndAuthorized(t *testing.T) {
	cases := []struct {
		name       string
		principal  Principal
		authorizer Authorizer
		opts       []WireOption
	}{
		{name: "a principal the policy does not list", principal: "bob", authorizer: alicePlanPolicy(), opts: []WireOption{WithPlans(0)}},
		{name: "plans not enabled", principal: "alice", authorizer: alicePlanPolicy()},
		{name: "no authorizer", principal: "alice", opts: []WireOption{WithPlans(0)}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := hostTestContext(t)
			rig := newPlanRig(t, tc.principal, tc.authorizer, tc.opts...)
			client := rig.dial(t)
			before := len(mustFindNodes(t, rig.host.Graph()))

			_, err := client.RunPlan(ctx, createPlan("a"))

			var remote *RemoteError
			if !errors.Is(err, ErrNotAuthorized) || !errors.As(err, &remote) || remote.Code != "not_authorized" {
				t.Fatalf("RunPlan() error = %#v, want a *RemoteError with code not_authorized", err)
			}

			if got := len(mustFindNodes(t, rig.host.Graph())); got != before {
				t.Fatalf("%d nodes after a refused plan, want %d", got, before)
			}
		})
	}
}

func TestWirePlanFollowsTheCurrentPolicy(t *testing.T) {
	ctx := hostTestContext(t)
	policy := NewMutablePolicy(map[string][]Principal{PlanResource: {"alice"}})
	rig := newPlanRig(t, "alice", policy, WithPlans(0))
	client := rig.dial(t)

	if _, err := client.RunPlan(ctx, createPlan("a")); err != nil {
		t.Fatalf("RunPlan() while allowed: %v", err)
	}

	policy.Replace(nil)

	if _, err := client.RunPlan(ctx, createPlan("a")); !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("RunPlan() after the revocation error = %v, want %v", err, ErrNotAuthorized)
	}
}

func TestWirePlanStepLimitComesFromWithPlans(t *testing.T) {
	ctx := hostTestContext(t)
	rig := newPlanRig(t, "alice", alicePlanPolicy(), WithPlans(2))
	client := rig.dial(t)

	if _, err := client.RunPlan(ctx, createPlan("a", "b", "c")); !errors.Is(err, ErrPlanInvalid) {
		t.Fatalf("RunPlan(3 steps) error = %v, want %v", err, ErrPlanInvalid)
	}

	if _, err := client.RunPlan(ctx, createPlan("a", "b")); err != nil {
		t.Fatalf("RunPlan(2 steps): %v", err)
	}
}

// oversizedSteps is enough maximum-length texts to make a plan that cannot
// fit in one frame.
const oversizedSteps = 2 * wireMaxFrame / MaxTextRunes

// TestWireOversizedPlanIsRefusedAndTheConnectionSurvives: a plan too large for
// one frame fails on the client without a byte being written, and the
// connection and the session are unharmed (it used to be torn down).
func TestWireOversizedPlanIsRefusedAndTheConnectionSurvives(t *testing.T) {
	ctx := hostTestContext(t)
	rig := newPlanRig(t, "alice", alicePlanPolicy(), WithPlans(0))
	client := rig.dial(t)

	text := strings.Repeat("a", MaxTextRunes)
	steps := make([]PlanStep, 0, oversizedSteps)

	for i := range oversizedSteps {
		steps = append(steps, PlanStep{Op: PlanIntern, Bind: fmt.Sprintf("t%d", i), Text: text})
	}

	if _, err := client.RunPlan(ctx, Plan{Steps: steps}); !errors.Is(err, ErrWireFrameTooLarge) {
		t.Fatalf("RunPlan(oversized) error = %v, want %v", err, ErrWireFrameTooLarge)
	}

	select {
	case <-client.Done():
		t.Fatal("the connection ended because one request was too large")
	default:
	}

	if keepErr := client.Keepalive(ctx); keepErr != nil {
		t.Fatalf("Keepalive() after the refused plan: %v", keepErr)
	}

	if _, err := client.RunPlan(ctx, createPlan("a")); err != nil {
		t.Fatalf("RunPlan() after the refused plan: %v", err)
	}
}

// TestWireSlowFrameIsDroppedAfterTheFrameDeadline: a peer that announces a
// frame and sends only part of it is cut off and reported, instead of holding
// a goroutine and a buffer forever.
func TestWireSlowFrameIsDroppedAfterTheFrameDeadline(t *testing.T) {
	original := wireFrameTimeout
	wireFrameTimeout = 100 * time.Millisecond

	// Registered first, so it runs after the rig's own cleanups.
	t.Cleanup(func() { wireFrameTimeout = original })

	reported := &errorLog{}
	rig := newWireRig(t, func(cfg *HostConfig) { cfg.OnError = reported.add })

	peer, reader := newRawWirePeer(t, rig.server)

	if writeErr := writeWireFrame(peer, &wireRequest{ID: 1, Op: wireOpHello, Version: wireVersion}); writeErr != nil {
		t.Fatalf("writing the hello: %v", writeErr)
	}

	var hello wireResponse
	if readErr := readWireFrame(reader, &hello); readErr != nil || hello.Error != nil {
		t.Fatalf("reading the hello response = (%v,%+v), want a clean answer", readErr, hello)
	}

	// A header announcing ten bytes, and only three of them.
	partial := binary.BigEndian.AppendUint32(nil, 10)
	partial = append(partial, "abc"...)

	if _, writeErr := peer.Write(partial); writeErr != nil {
		t.Fatalf("writing the partial frame: %v", writeErr)
	}

	if deadlineErr := peer.SetReadDeadline(time.Now().Add(10 * time.Second)); deadlineErr != nil {
		t.Fatalf("SetReadDeadline(): %v", deadlineErr)
	}

	var next wireResponse
	if readErr := readWireFrame(reader, &next); !errors.Is(readErr, io.EOF) {
		t.Fatalf("read after the frame deadline error = %v, want %v", readErr, io.EOF)
	}

	eventually(t, "the slow frame to be reported", func() bool { return reported.has(ErrWireProtocol) })
}