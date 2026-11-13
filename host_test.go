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
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewRegistriesRejectsMissingNames(t *testing.T) {
	if _, err := NewRegistries(&Graph{}, NewNameRegistry(nil), map[string]NodeID{}); !errors.Is(err, ErrNameNotFound) {
		t.Fatalf("NewRegistries() error = %v, want %v", err, ErrNameNotFound)
	}
}

func TestOpenHostRequiresPath(t *testing.T) {
	if _, err := OpenHost(HostConfig{}); err == nil {
		t.Fatal("OpenHost() without a path succeeded")
	}
}

func TestOpenHostFailsFastWhenTheStoreIsAlreadyOwned(t *testing.T) {
	original := boltOpenTimeout
	boltOpenTimeout = 50 * time.Millisecond

	t.Cleanup(func() { boltOpenTimeout = original })

	path := boltTestPath(t)
	openTestHost(t, path, nil)

	if _, err := OpenHost(HostConfig{Path: path}); !errors.Is(err, ErrStoreLocked) {
		t.Fatalf("second OpenHost() error = %v, want %v", err, ErrStoreLocked)
	}
}

// TestHostManyHoldersApplyOnceAndRemoveOnce is the driving example: ten
// parallel clients want the same temporary rule; it is added once, and
// removed once after the last of them lets go.
func TestHostManyHoldersApplyOnceAndRemoveOnce(t *testing.T) {
	ctx := hostTestContext(t)
	h := openTestHost(t, boltTestPath(t), nil)
	fw := &fakeEffect{}
	registerTestResource(t, h, "example-resource", fw)

	const clients = 10

	conns := make([]*Conn, clients)
	for i := range conns {
		conns[i] = connectTest(t, h)
	}

	errs := make([]error, clients)

	runConcurrently(clients, func(i int) { errs[i] = AcquireAndWait(ctx, conns[i], "example-resource") })

	for i, acquireErr := range errs {
		if acquireErr != nil {
			t.Fatalf("client %d: AcquireAndWait(): %v", i, acquireErr)
		}
	}

	requireEffectCounts(t, fw, 1, 0, true)

	runConcurrently(clients, func(i int) { _, errs[i] = conns[i].Release(ctx, "example-resource") })

	for i, releaseErr := range errs {
		if releaseErr != nil {
			t.Fatalf("client %d: Release(): %v", i, releaseErr)
		}
	}

	eventually(t, "the effect to be removed", func() bool {
		_, _, present := fw.counts()
		return !present
	})

	requireEffectCounts(t, fw, 1, 1, false)
}

// TestHostCrashedHolderIsReleasedByConnectionDrop: nine clients release, the
// tenth "crashes" (its connection context ends). The rule must stay while it
// is held and go once the drop is noticed.
func TestHostCrashedHolderIsReleasedByConnectionDrop(t *testing.T) {
	ctx := hostTestContext(t)
	h := openTestHost(t, boltTestPath(t), nil)
	fw := &fakeEffect{}
	registerTestResource(t, h, "example-resource", fw)

	crashCtx, crash := context.WithCancel(context.Background())
	defer crash()

	crashed, err := h.Connect(crashCtx)
	if err != nil {
		t.Fatalf("Connect(): %v", err)
	}

	const survivors = 9

	conns := make([]*Conn, survivors)
	for i := range conns {
		conns[i] = connectTest(t, h)
	}

	for _, c := range append([]*Conn{crashed}, conns...) {
		if acquireErr := AcquireAndWait(ctx, c, "example-resource"); acquireErr != nil {
			t.Fatalf("AcquireAndWait(): %v", acquireErr)
		}
	}

	for i, c := range conns {
		if _, releaseErr := c.Release(ctx, "example-resource"); releaseErr != nil {
			t.Fatalf("survivor %d: Release(): %v", i, releaseErr)
		}
	}

	// Well past the grace: the crashed holder keeps the rule alive.
	time.Sleep(4 * h.cfg.TeardownGrace)
	requireEffectCounts(t, fw, 1, 0, true)

	crash()

	select {
	case <-crashed.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the connection did not end after its context was cancelled")
	}

	eventually(t, "the crashed holder's effect to be removed", func() bool {
		_, _, present := fw.counts()
		return !present
	})

	requireEffectCounts(t, fw, 1, 1, false)

	if _, releaseErr := crashed.Release(ctx, "example-resource"); !errors.Is(releaseErr, ErrConnClosed) {
		t.Fatalf("Release() on a dropped connection error = %v, want %v", releaseErr, ErrConnClosed)
	}
}

func TestHostKeepaliveTTLExpiresSilentConnectionsOnly(t *testing.T) {
	ctx := hostTestContext(t)
	h := openTestHost(t, boltTestPath(t), func(cfg *HostConfig) { cfg.KeepaliveTTL = 500 * time.Millisecond })

	fw := &fakeEffect{}
	registerTestResource(t, h, "example-resource", fw)

	silent := connectTest(t, h)
	alive := connectTest(t, h)

	if err := AcquireAndWait(ctx, silent, "example-resource"); err != nil {
		t.Fatalf("silent: AcquireAndWait(): %v", err)
	}

	// The alive client keeps its session fresh well past the TTL.
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if err := alive.Keepalive(ctx); err != nil {
			t.Fatalf("Keepalive(): %v", err)
		}

		time.Sleep(20 * time.Millisecond)
	}

	select {
	case <-silent.Done():
	default:
		t.Fatal("the silent connection was not expired")
	}

	select {
	case <-alive.Done():
		t.Fatal("the connection that kept calling Keepalive was expired")
	default:
	}

	eventually(t, "the expired holder's effect to be removed", func() bool {
		_, _, present := fw.counts()
		return !present
	})

	if _, err := silent.Acquire(ctx, "example-resource"); !errors.Is(err, ErrConnClosed) {
		t.Fatalf("Acquire() on an expired connection error = %v, want %v", err, ErrConnClosed)
	}
}

func TestHostReacquireWithinGraceNeverRemovesTheEffect(t *testing.T) {
	ctx := hostTestContext(t)
	h := openTestHost(t, boltTestPath(t), func(cfg *HostConfig) { cfg.TeardownGrace = 700 * time.Millisecond })

	fw := &fakeEffect{}
	registerTestResource(t, h, "example-resource", fw)

	first := connectTest(t, h)
	if err := AcquireAndWait(ctx, first, "example-resource"); err != nil {
		t.Fatalf("first: AcquireAndWait(): %v", err)
	}

	if _, err := first.Release(ctx, "example-resource"); err != nil {
		t.Fatalf("first: Release(): %v", err)
	}

	second := connectTest(t, h)
	if err := AcquireAndWait(ctx, second, "example-resource"); err != nil {
		t.Fatalf("second: AcquireAndWait(): %v", err)
	}

	requireEffectCounts(t, fw, 1, 0, true)

	if _, err := second.Release(ctx, "example-resource"); err != nil {
		t.Fatalf("second: Release(): %v", err)
	}

	eventually(t, "the effect to be removed after the grace", func() bool {
		_, _, present := fw.counts()
		return !present
	})

	requireEffectCounts(t, fw, 1, 1, false)
}

func TestHostWaitAppliedRetriesFailingEffectsAndReportsTheLastError(t *testing.T) {
	ctx := hostTestContext(t)
	h := openTestHost(t, boltTestPath(t), nil)

	flaky := &fakeEffect{applyFailures: 3}
	registerTestResource(t, h, "flaky", flaky)

	c := connectTest(t, h)
	if err := AcquireAndWait(ctx, c, "flaky"); err != nil {
		t.Fatalf("AcquireAndWait() with a flaky effect: %v", err)
	}

	if applies, _, present := flaky.counts(); applies != 4 || !present {
		t.Fatalf("applies=%d present=%v, want 4,true (three failures, then success)", applies, present)
	}

	broken := &fakeEffect{applyFailures: 1 << 30}
	registerTestResource(t, h, "broken", broken)

	shortCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()

	err := AcquireAndWait(shortCtx, c, "broken")
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, errFakeEffect) {
		t.Fatalf("AcquireAndWait() with a broken effect error = %v, want the deadline and the last effect error", err)
	}
}

func TestHostRemovesStaleEffectLeftByAPreviousRun(t *testing.T) {
	h := openTestHost(t, boltTestPath(t), nil)
	fw := &fakeEffect{present: true}
	registerTestResource(t, h, "stale", fw)

	eventually(t, "the stale effect to be removed", func() bool {
		_, _, present := fw.counts()
		return !present
	})
}

func TestHostOperationsFailLoudly(t *testing.T) {
	ctx := hostTestContext(t)
	h := openTestHost(t, boltTestPath(t), nil)
	registerTestResource(t, h, "example-resource", nil)

	if err := h.RegisterResource("example-resource", nil); !errors.Is(err, ErrResourceRegistered) {
		t.Fatalf("second RegisterResource() error = %v, want %v", err, ErrResourceRegistered)
	}
	if err := h.RegisterResource("", nil); !errors.Is(err, ErrResourceName) {
		t.Fatalf("RegisterResource(\"\") error = %v, want %v", err, ErrResourceName)
	}

	c := connectTest(t, h)

	if _, err := c.Acquire(ctx, "nope"); !errors.Is(err, ErrUnknownResource) {
		t.Fatalf("Acquire(unknown) error = %v, want %v", err, ErrUnknownResource)
	}

	result, err := c.Acquire(ctx, "example-resource")
	if err != nil || !result.First {
		t.Fatalf("Acquire() = (%+v,%v), want First=true", result, err)
	}

	if last, releaseErr := c.Release(ctx, "example-resource"); releaseErr != nil || !last {
		t.Fatalf("Release() = (%v,%v), want last=true", last, releaseErr)
	}

	if waitErr := c.WaitApplied(ctx, "example-resource", result.Mark); !errors.Is(waitErr, ErrHoldLost) {
		t.Fatalf("WaitApplied() after the release error = %v, want %v", waitErr, ErrHoldLost)
	}

	if closeErr := c.Close(); closeErr != nil {
		t.Fatalf("Close(): %v", closeErr)
	}
	if closeErr := c.Close(); closeErr != nil {
		t.Fatalf("second Close(): %v", closeErr)
	}

	if _, acquireErr := c.Acquire(ctx, "example-resource"); !errors.Is(acquireErr, ErrConnClosed) {
		t.Fatalf("Acquire() after Close error = %v, want %v", acquireErr, ErrConnClosed)
	}
	if keepErr := c.Keepalive(ctx); !errors.Is(keepErr, ErrConnClosed) {
		t.Fatalf("Keepalive() after Close error = %v, want %v", keepErr, ErrConnClosed)
	}
}

func TestHostCloseRemovesEffectsAndRejectsFurtherWork(t *testing.T) {
	ctx := hostTestContext(t)
	h := openTestHost(t, boltTestPath(t), func(cfg *HostConfig) { cfg.TeardownGrace = time.Hour })

	fw := &fakeEffect{}
	registerTestResource(t, h, "example-resource", fw)

	c := connectTest(t, h)
	if err := AcquireAndWait(ctx, c, "example-resource"); err != nil {
		t.Fatalf("AcquireAndWait(): %v", err)
	}

	if err := h.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}

	// The grace is an hour, so only the final shutdown pass can have
	// removed the rule.
	requireEffectCounts(t, fw, 1, 1, false)

	if _, err := h.Connect(ctx); !errors.Is(err, ErrHostClosed) {
		t.Fatalf("Connect() after Close error = %v, want %v", err, ErrHostClosed)
	}
	if err := h.RegisterResource("late", nil); !errors.Is(err, ErrHostClosed) {
		t.Fatalf("RegisterResource() after Close error = %v, want %v", err, ErrHostClosed)
	}
	if _, err := c.Acquire(ctx, "example-resource"); !errors.Is(err, ErrConnClosed) {
		t.Fatalf("Acquire() on a connection of a closed host error = %v, want %v", err, ErrConnClosed)
	}

	select {
	case <-c.Done():
	default:
		t.Fatal("the connection of a closed host is not done")
	}
}

// TestHostRestartSweepsSessionsAndKeepsNames covers startup: a session left
// behind (as after a crash, when nobody could close it) is swept, its holds
// vanish, and the resource name keeps its node.
func TestHostRestartSweepsSessionsAndKeepsNames(t *testing.T) {
	path := boltTestPath(t)
	first := openTestHost(t, path, nil)
	registerTestResource(t, first, "example-resource", nil)

	node, ok := first.names.Lookup(resourcePrefix + "example-resource")
	if !ok {
		t.Fatal("the resource name is not bound")
	}

	// A session the host does not track, like one a crash left behind.
	leases := first.Registries().Leases

	orphan, err := leases.NewSession(first.Graph())
	if err != nil {
		t.Fatalf("NewSession(): %v", err)
	}
	if _, acquireErr := leases.Acquire(first.Graph(), node, orphan); acquireErr != nil {
		t.Fatalf("Acquire(): %v", acquireErr)
	}

	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("Close(): %v", closeErr)
	}

	second := openTestHost(t, path, nil)

	sessions, err := second.Registries().Leases.Sessions(second.Graph())
	if err != nil || len(sessions) != 0 {
		t.Fatalf("Sessions() after restart = (%v,%v), want none", sessions, err)
	}

	held, err := second.Registries().Leases.Held(second.Graph(), node)
	if err != nil || held {
		t.Fatalf("Held() after restart = (%v,%v), want (false,nil)", held, err)
	}

	if again, found := second.names.Lookup(resourcePrefix + "example-resource"); !found || again != node {
		t.Fatalf("Lookup() after restart = (%d,%v), want (%d,true)", again, found, node)
	}

	// Registering the resource again finds the same node and works.
	registerTestResource(t, second, "example-resource", nil)
}

// TestHostReconcilerLooksOnlyAtResourcesThatChanged: with the resync an hour
// away, activity on one resource must not make the reconciler look at another.
func TestHostReconcilerLooksOnlyAtResourcesThatChanged(t *testing.T) {
	ctx := hostTestContext(t)
	h := openTestHost(t, boltTestPath(t), func(cfg *HostConfig) { cfg.ResyncInterval = time.Hour })

	touched := &fakeEffect{}
	untouched := &fakeEffect{}
	registerTestResource(t, h, "touched", touched)
	registerTestResource(t, h, "untouched", untouched)

	// Registration marks each resource, so each is looked at once.
	eventually(t, "both resources to be looked at after registration", func() bool {
		return touched.presentCalls() >= 1 && untouched.presentCalls() >= 1
	})

	untouchedBefore := untouched.presentCalls()
	touchedBefore := touched.presentCalls()

	c := connectTest(t, h)

	for range 3 {
		if err := AcquireAndWait(ctx, c, "touched"); err != nil {
			t.Fatalf("AcquireAndWait(): %v", err)
		}

		if _, err := c.Release(ctx, "touched"); err != nil {
			t.Fatalf("Release(): %v", err)
		}

		eventually(t, "the touched effect to be removed", func() bool {
			_, _, present := touched.counts()
			return !present
		})
	}

	if got := touched.presentCalls(); got <= touchedBefore {
		t.Fatalf("the touched resource was looked at %d times, want more than %d", got, touchedBefore)
	}

	if got := untouched.presentCalls(); got != untouchedBefore {
		t.Fatalf("the untouched resource was looked at %d times, want still %d", got, untouchedBefore)
	}
}

// TestHostReleaseAndDropRemoveEffectsWithoutWaitingForTheResync: with the
// resync an hour away, only the dirty marks and the teardown grace can bring
// the reconciler back, for an explicit release and for a dropped connection.
func TestHostReleaseAndDropRemoveEffectsWithoutWaitingForTheResync(t *testing.T) {
	ctx := hostTestContext(t)
	h := openTestHost(t, boltTestPath(t), func(cfg *HostConfig) { cfg.ResyncInterval = time.Hour })

	released := &fakeEffect{}
	dropped := &fakeEffect{}
	registerTestResource(t, h, "released", released)
	registerTestResource(t, h, "dropped", dropped)

	releasing := connectTest(t, h)
	dropping := connectTest(t, h)

	if err := AcquireAndWait(ctx, releasing, "released"); err != nil {
		t.Fatalf("releasing: AcquireAndWait(): %v", err)
	}

	if err := AcquireAndWait(ctx, dropping, "dropped"); err != nil {
		t.Fatalf("dropping: AcquireAndWait(): %v", err)
	}

	if _, err := releasing.Release(ctx, "released"); err != nil {
		t.Fatalf("releasing: Release(): %v", err)
	}

	if err := dropping.Close(); err != nil {
		t.Fatalf("dropping: Close(): %v", err)
	}

	eventually(t, "both effects to be removed", func() bool {
		_, _, releasedPresent := released.counts()
		_, _, droppedPresent := dropped.counts()

		return !releasedPresent && !droppedPresent
	})
}

// TestHostResyncRepairsDriftNobodyTouched: the outside world changes behind
// the host's back, no acquire or release happens, and the periodic full resync
// still puts the effect back.
func TestHostResyncRepairsDriftNobodyTouched(t *testing.T) {
	ctx := hostTestContext(t)
	h := openTestHost(t, boltTestPath(t), nil)
	fw := &fakeEffect{}
	registerTestResource(t, h, "example-resource", fw)

	c := connectTest(t, h)
	if err := AcquireAndWait(ctx, c, "example-resource"); err != nil {
		t.Fatalf("AcquireAndWait(): %v", err)
	}

	fw.mu.Lock()
	fw.present = false
	fw.mu.Unlock()

	eventually(t, "the resync to apply the effect again", func() bool {
		applies, _, present := fw.counts()
		return applies >= 2 && present
	})
}

// hungEffect is a fakeEffect whose Apply blocks until unblock is called and
// ignores its context: the shape of an effect stuck in some outside call.
type hungEffect struct {
	fakeEffect
	release chan struct{}
	once    sync.Once
	entered atomic.Int32
}

func newHungEffect() *hungEffect {
	return &hungEffect{release: make(chan struct{})}
}

func (h *hungEffect) Apply(ctx context.Context) error {
	h.entered.Add(1)

	<-h.release

	return h.fakeEffect.Apply(ctx)
}

func (h *hungEffect) unblock() {
	h.once.Do(func() { close(h.release) })
}

// TestHostHungEffectTimesOutIsNotCalledAgainWhileStuckAndDoesNotBlockOthers:
// an Apply that never returns is abandoned at EffectTimeout and reported; no
// second Apply starts while it is stuck (ErrEffectBusy instead); another
// resource is still reconciled; and once the stuck call finally returns, its
// result is picked up without a second Apply.
func TestHostHungEffectTimesOutIsNotCalledAgainWhileStuckAndDoesNotBlockOthers(t *testing.T) {
	ctx := hostTestContext(t)
	reported := &errorLog{}
	h := openTestHost(t, boltTestPath(t), func(cfg *HostConfig) {
		cfg.EffectTimeout = 50 * time.Millisecond
		cfg.OnError = reported.add
	})

	hung := newHungEffect()
	t.Cleanup(hung.unblock)

	plain := &fakeEffect{}
	registerTestResource(t, h, "hung", hung)
	registerTestResource(t, h, "plain", plain)

	c := connectTest(t, h)

	result, acquireErr := c.Acquire(ctx, "hung")
	if acquireErr != nil {
		t.Fatalf("Acquire(hung): %v", acquireErr)
	}

	if plainErr := AcquireAndWait(ctx, c, "plain"); plainErr != nil {
		t.Fatalf("AcquireAndWait(plain) while another effect is hung: %v", plainErr)
	}

	eventually(t, "the timeout to be reported", func() bool { return reported.has(ErrEffectTimeout) })

	// Several retries pass while the first Apply is still stuck.
	waitCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()

	if waitErr := c.WaitApplied(waitCtx, "hung", result.Mark); !errors.Is(waitErr, context.DeadlineExceeded) {
		t.Fatalf("WaitApplied() on a hung effect error = %v, want %v", waitErr, context.DeadlineExceeded)
	}

	if got := hung.entered.Load(); got != 1 {
		t.Fatalf("Apply was entered %d times while stuck, want exactly 1", got)
	}

	if !reported.has(ErrEffectBusy) {
		t.Fatal("no ErrEffectBusy was reported for the retries made while the call was stuck")
	}

	hung.unblock()

	if appliedErr := c.WaitApplied(ctx, "hung", result.Mark); appliedErr != nil {
		t.Fatalf("WaitApplied() after the stuck call returned: %v", appliedErr)
	}

	requireEffectCounts(t, &hung.fakeEffect, 1, 0, true)
}

// TestHostCloseDoesNotWaitForAnEffectThatIgnoresItsContext: with the default
// (long) EffectTimeout, a call stuck inside Apply must not keep Close from
// returning.
func TestHostCloseDoesNotWaitForAnEffectThatIgnoresItsContext(t *testing.T) {
	ctx := hostTestContext(t)
	h := openTestHost(t, boltTestPath(t), nil)

	hung := newHungEffect()
	t.Cleanup(hung.unblock)

	registerTestResource(t, h, "hung", hung)

	c := connectTest(t, h)

	if _, acquireErr := c.Acquire(ctx, "hung"); acquireErr != nil {
		t.Fatalf("Acquire(): %v", acquireErr)
	}

	eventually(t, "the effect to be stuck inside Apply", func() bool { return hung.entered.Load() == 1 })

	closed := make(chan error, 1)

	go func() { closed <- h.Close() }()

	select {
	case closeErr := <-closed:
		if closeErr != nil {
			t.Fatalf("Close(): %v", closeErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close() did not return while an effect call ignored its context")
	}
}

func TestHostConfigEffectTimeoutDefaultsAndDisable(t *testing.T) {
	if got := (HostConfig{}).withDefaults().EffectTimeout; got != defaultEffectTimeout {
		t.Fatalf("default EffectTimeout = %v, want %v", got, defaultEffectTimeout)
	}

	if got := (HostConfig{EffectTimeout: -1}).withDefaults().EffectTimeout; got != 0 {
		t.Fatalf("a negative EffectTimeout became %v, want 0 (disabled)", got)
	}

	if got := (HostConfig{EffectTimeout: time.Second}).withDefaults().EffectTimeout; got != time.Second {
		t.Fatalf("EffectTimeout = %v, want it kept at %v", got, time.Second)
	}
}

// TestHostResourceCallEffectNeverReportsATimeoutForACallThatReturned makes
// many fast calls under a generous deadline: none may be reported as timed
// out. A regression guard for the order in which callEffect's goroutine
// sends its result and cancels the call's context; the failure it guards
// against is a race, so it is probabilistic (most likely to show under -race).
func TestHostResourceCallEffectNeverReportsATimeoutForACallThatReturned(t *testing.T) {
	r := newHostResource("example-resource", 0, &fakeEffect{}, time.Minute)

	for i := range 20000 {
		if _, err := r.checkPresent(context.Background()); err != nil {
			t.Fatalf("call %d: checkPresent() error = %v, want nil", i, err)
		}
	}
}
