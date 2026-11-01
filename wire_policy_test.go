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
	"net"
	"testing"
)

func TestMutablePolicyReplaceChangesDecisionsAndZeroValueDeniesAll(t *testing.T) {
	request := AuthRequest{Operation: OpAcquire, Resource: "example-resource"}

	var empty MutablePolicy

	if err := empty.Authorize("alice", request); !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("zero-value Authorize() error = %v, want %v", err, ErrNotAuthorized)
	}

	policy := NewMutablePolicy(map[string][]Principal{"example-resource": {"alice"}})

	if err := policy.Authorize("alice", request); err != nil {
		t.Fatalf("Authorize(alice) = %v, want nil", err)
	}

	if err := policy.Authorize("bob", request); !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("Authorize(bob) error = %v, want %v", err, ErrNotAuthorized)
	}

	policy.Replace(map[string][]Principal{"example-resource": {"bob"}})

	if err := policy.Authorize("alice", request); !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("Authorize(alice) after Replace error = %v, want %v", err, ErrNotAuthorized)
	}

	if err := policy.Authorize("bob", request); err != nil {
		t.Fatalf("Authorize(bob) after Replace = %v, want nil", err)
	}

	policy.Replace(nil)

	if err := policy.Authorize("bob", request); !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("Authorize(bob) after Replace(nil) error = %v, want %v", err, ErrNotAuthorized)
	}
}

// TestWireHolderMayAlwaysReleaseWhatItHolds: after its access is revoked a
// principal cannot acquire, but it can still let go of what it holds. Anything
// else it asks to release is refused exactly as before, so nothing is
// revealed about what exists.
func TestWireHolderMayAlwaysReleaseWhatItHolds(t *testing.T) {
	ctx := hostTestContext(t)
	policy := NewMutablePolicy(map[string][]Principal{"allowed": {"alice"}})
	rig := newAuthRig(t, "alice", policy)
	client := rig.dial(t)

	if acquireErr := AcquireAndWait(ctx, client, "allowed"); acquireErr != nil {
		t.Fatalf("AcquireAndWait(allowed): %v", acquireErr)
	}

	policy.Replace(nil)

	if _, acquireErr := client.Acquire(ctx, "allowed"); !errors.Is(acquireErr, ErrNotAuthorized) {
		t.Fatalf("Acquire(allowed) after the revocation error = %v, want %v", acquireErr, ErrNotAuthorized)
	}

	if last, releaseErr := client.Release(ctx, "allowed"); releaseErr != nil || !last {
		t.Fatalf("Release(allowed) of an own hold after the revocation = (%v,%v), want (true,nil)", last, releaseErr)
	}

	for _, resource := range []string{"allowed", "denied", "no-such-resource"} {
		if _, releaseErr := client.Release(ctx, resource); !errors.Is(releaseErr, ErrNotAuthorized) {
			t.Fatalf("Release(%q) of something not held error = %v, want %v", resource, releaseErr, ErrNotAuthorized)
		}
	}
}

// TestWireRevalidateReleasesOnlyHoldsThePolicyNoLongerAllows: the policy
// is replaced while a client holds two resources; Revalidate drops the one
// that is no longer allowed (its effect goes), keeps the other, and leaves the
// connection alive. Allowing the resource again lets the client take it.
func TestWireRevalidateReleasesOnlyHoldsThePolicyNoLongerAllows(t *testing.T) {
	ctx := hostTestContext(t)

	both := map[string][]Principal{"keep": {"alice"}, "revoke": {"alice"}}
	policy := NewMutablePolicy(both)
	identify := func(net.Conn) (Principal, error) { return "alice", nil }
	rig := newWireRig(t, nil, WithPeerIdentifier(identify), WithAuthorizer(policy))

	keepEffect := &fakeEffect{}
	revokeEffect := &fakeEffect{}
	registerTestResource(t, rig.host, "keep", keepEffect)
	registerTestResource(t, rig.host, "revoke", revokeEffect)

	client := rig.dial(t)

	for _, resource := range []string{"keep", "revoke"} {
		if acquireErr := AcquireAndWait(ctx, client, resource); acquireErr != nil {
			t.Fatalf("AcquireAndWait(%q): %v", resource, acquireErr)
		}
	}

	requireEffectCounts(t, revokeEffect, 1, 0, true)

	policy.Replace(map[string][]Principal{"keep": {"alice"}})

	released, err := rig.server.Revalidate(ctx)
	if err != nil || released != 1 {
		t.Fatalf("Revalidate() = (%d,%v), want (1,nil)", released, err)
	}

	eventually(t, "the revoked hold's effect to be removed", func() bool {
		_, _, present := revokeEffect.counts()
		return !present
	})

	requireEffectCounts(t, keepEffect, 1, 0, true)

	if keepErr := client.Keepalive(ctx); keepErr != nil {
		t.Fatalf("Keepalive() after Revalidate: %v", keepErr)
	}

	if _, acquireErr := client.Acquire(ctx, "revoke"); !errors.Is(acquireErr, ErrNotAuthorized) {
		t.Fatalf("Acquire(revoke) after the revocation error = %v, want %v", acquireErr, ErrNotAuthorized)
	}

	if again, againErr := rig.server.Revalidate(ctx); againErr != nil || again != 0 {
		t.Fatalf("second Revalidate() = (%d,%v), want (0,nil)", again, againErr)
	}

	policy.Replace(both)

	if acquireErr := AcquireAndWait(ctx, client, "revoke"); acquireErr != nil {
		t.Fatalf("AcquireAndWait(revoke) after allowing it again: %v", acquireErr)
	}

	requireEffectCounts(t, revokeEffect, 2, 1, true)
}

func TestWireRevalidateWithoutAnAuthorizerChangesNothing(t *testing.T) {
	ctx := hostTestContext(t)
	rig := newWireRig(t, nil)
	registerTestResource(t, rig.host, "example-resource", nil)

	client := rig.dial(t)

	if acquireErr := AcquireAndWait(ctx, client, "example-resource"); acquireErr != nil {
		t.Fatalf("AcquireAndWait(): %v", acquireErr)
	}

	if released, err := rig.server.Revalidate(ctx); err != nil || released != 0 {
		t.Fatalf("Revalidate() = (%d,%v), want (0,nil)", released, err)
	}

	if last, releaseErr := client.Release(ctx, "example-resource"); releaseErr != nil || !last {
		t.Fatalf("Release() = (%v,%v), want last=true: the hold must have survived", last, releaseErr)
	}
}