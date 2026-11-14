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
	"fmt"
	"maps"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// passOutcome is a snapshot of one resource's reconcile progress.
type passOutcome struct {
	completed uint64
	applied   bool
	err       error
	changed   <-chan struct{}
}

// hostResource is one registered resource: its holder Set, its optional
// effect, and the reconciler's progress on it.
type hostResource struct {
	name   string
	node   NodeID
	effect Effect

	// timeout is HostConfig.EffectTimeout (0: calls are made directly), and
	// busy is set while a call to effect is running, including one that was
	// abandoned after the timeout (see callEffect).
	timeout time.Duration
	busy    atomic.Bool

	mu        sync.Mutex
	started   uint64
	completed uint64
	applied   bool
	lastErr   error
	changed   chan struct{}

	// Touched only by the reconciler goroutine (and by Close after that
	// goroutine has exited): when the resource first became unheld while its
	// effect was still present.
	unheld      bool
	unheldSince time.Duration
}

func newHostResource(name string, node NodeID, effect Effect, timeout time.Duration) *hostResource {
	return &hostResource{name: name, node: node, effect: effect, timeout: timeout, changed: make(chan struct{})}
}

// beginPass numbers a new reconcile pass. It must be called before the
// pass reads the holder set.
func (r *hostResource) beginPass() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.started++

	return r.started
}

// mark returns the number of the latest pass that has begun. A client calls
// it after its hold has committed: every pass numbered above it begins after
// the commit and therefore sees the hold.
func (r *hostResource) mark() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.started
}

// finishPass records the outcome of pass and wakes every waiter. applied
// means: the resource was held, and its effect is confirmed present.
func (r *hostResource) finishPass(pass uint64, applied bool, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.completed = pass
	r.applied = applied
	r.lastErr = err

	close(r.changed)
	r.changed = make(chan struct{})
}

func (r *hostResource) outcome() passOutcome {
	r.mu.Lock()
	defer r.mu.Unlock()

	return passOutcome{completed: r.completed, applied: r.applied, err: r.lastErr, changed: r.changed}
}

// callEffect runs fn, one call to r.effect, under r.timeout (op names the
// call in errors). With no timeout fn is called directly.
//
// Otherwise fn runs on its own goroutine, so an effect that ignores its
// context cannot hold the caller past the deadline: the call is then
// abandoned and ErrEffectTimeout returned. The goroutine cannot be killed,
// so busy stays set until fn returns, and a new call meanwhile fails at once
// with ErrEffectBusy instead of overlapping the stuck one. If ctx (the
// host's) ends first, the call is abandoned the same way. Whatever an
// abandoned call eventually does is seen by a later pass, since Present
// reports real state.
func (r *hostResource) callEffect(ctx context.Context, op string, fn func(ctx context.Context) error) error {
	if r.timeout <= 0 {
		return fn(ctx)
	}

	if !r.busy.CompareAndSwap(false, true) {
		return fmt.Errorf("%w: %s of %q", ErrEffectBusy, op, r.name)
	}

	callCtx, cancel := context.WithTimeout(ctx, r.timeout)
	done := make(chan error, 1)

	go func() {
		callErr := fn(callCtx)

		// The result goes out before cancel: cancelling callCtx wakes the
		// select below through callCtx.Done(), and if that could happen
		// before the result is in done, a call that returned in time would
		// be reported as timed out. done is buffered, so this never blocks,
		// even when the caller has already abandoned the call.
		r.busy.Store(false)

		done <- callErr

		cancel()
	}()

	select {
	case callErr := <-done:
		return callErr
	case <-callCtx.Done():
	}

	// The call may have returned at the same moment the deadline passed.
	select {
	case callErr := <-done:
		return callErr
	default:
	}

	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%s of %q abandoned: %w", op, r.name, ctxErr)
	}

	return fmt.Errorf("%w: %s of %q did not return within %v", ErrEffectTimeout, op, r.name, r.timeout)
}

// checkPresent asks the effect whether it is in place, under the deadline.
func (r *hostResource) checkPresent(ctx context.Context) (bool, error) {
	var present bool

	err := r.callEffect(ctx, "present", func(callCtx context.Context) error {
		var presentErr error

		present, presentErr = r.effect.Present(callCtx)

		return wrapInterfaceErr(presentErr)
	})
	if err != nil {
		return false, err
	}

	return present, nil
}

// converge applies or removes the effect and verifies the result against
// the real state.
func (r *hostResource) converge(ctx context.Context, want bool) error {
	op, run := "remove", r.effect.Remove
	if want {
		op, run = "apply", r.effect.Apply
	}

	if opErr := r.callEffect(ctx, op, run); opErr != nil {
		return fmt.Errorf("changing effect of %q to present=%v: %w", r.name, want, opErr)
	}

	present, err := r.checkPresent(ctx)
	if err != nil {
		return fmt.Errorf("verifying effect of %q: %w", r.name, err)
	}

	if present != want {
		return fmt.Errorf("%w: %q, wanted present=%v", ErrEffectNotConverged, r.name, want)
	}

	return nil
}

// kickReconciler wakes the reconciler. The channel is buffered with one
// token, so a kick that arrives during a pass triggers another pass after
// it: a pass that began before a commit can never be the last one.
func (h *Host) kickReconciler() {
	select {
	case h.kickCh <- struct{}{}:
	default:
	}
}

// markDirty tells the reconciler that something happened to resources that
// may change what they should be, and wakes it. Call it AFTER the change has
// committed (and, for Acquire, after the mark was read): the reconciler clears
// a mark before it numbers its next pass, so any pass numbered after the
// clear sees the change, and WaitApplied waits for such a pass.
func (h *Host) markDirty(resources ...*hostResource) {
	h.dirtyMu.Lock()

	for _, r := range resources {
		h.dirty[r] = struct{}{}
	}

	h.dirtyMu.Unlock()

	h.kickReconciler()
}

// markDirtyNodes is markDirty for the registered resources whose holder Set
// is one of nodes. Nodes that are not registered resources are ignored.
func (h *Host) markDirtyNodes(nodes []NodeID) {
	h.resMu.RLock()

	found := make([]*hostResource, 0, len(nodes))

	for _, node := range nodes {
		if r, ok := h.byNode[node]; ok {
			found = append(found, r)
		}
	}

	h.resMu.RUnlock()

	if len(found) > 0 {
		h.markDirty(found...)
	}
}

// takeDirty returns the marked resources and empties the set.
func (h *Host) takeDirty() map[*hostResource]struct{} {
	h.dirtyMu.Lock()
	defer h.dirtyMu.Unlock()

	taken := h.dirty
	h.dirty = make(map[*hostResource]struct{})

	return taken
}

// clearDirty drops r's mark. The reconciler calls it just before it numbers
// a pass for r, so a mark set after that call (a change committed after the
// pass began) survives and causes another pass.
func (h *Host) clearDirty(r *hostResource) {
	h.dirtyMu.Lock()
	defer h.dirtyMu.Unlock()

	delete(h.dirty, r)
}

// reconcileLoop is the reconciler goroutine. due holds, for the resources
// that want another look without anything touching them (a teardown grace
// running, a failed effect waiting to retry), when that is; only this
// goroutine uses it.
func (h *Host) reconcileLoop(ctx context.Context) {
	due := make(map[*hostResource]time.Duration)
	lastFull := h.monoNow()

	for {
		timer := time.NewTimer(h.nextWake(lastFull, due))

		select {
		case <-h.stop:
			timer.Stop()
			return
		case <-h.kickCh:
			timer.Stop()
		case <-timer.C:
		}

		if h.monoNow()-lastFull >= h.cfg.ResyncInterval {
			h.reconcileSet(ctx, h.resourceSnapshot(), false, due)
			lastFull = h.monoNow()

			continue
		}

		h.reconcileSet(ctx, h.pendingResources(due), false, due)
	}
}

// nextWake is how long the reconciler may sleep: until the next full resync
// or the earliest resource deadline, whichever comes first.
func (h *Host) nextWake(lastFull time.Duration, due map[*hostResource]time.Duration) time.Duration {
	now := h.monoNow()
	wait := h.cfg.ResyncInterval - (now - lastFull)

	for _, at := range due {
		wait = min(wait, at-now)
	}

	return max(wait, 0)
}

// pendingResources returns, sorted by name, the resources marked dirty plus
// those whose deadline in due has come.
func (h *Host) pendingResources(due map[*hostResource]time.Duration) []*hostResource {
	pending := h.takeDirty()
	now := h.monoNow()

	for r, at := range due {
		if at <= now {
			pending[r] = struct{}{}
		}
	}

	list := slices.Collect(maps.Keys(pending))
	sort.Slice(list, func(i, j int) bool { return list[i].name < list[j].name })

	return list
}

// reconcileSet reconciles each of resources, and records in due when each
// wants another look (removing it from due if it does not). final is set by
// Close and skips the teardown grace.
func (h *Host) reconcileSet(ctx context.Context, resources []*hostResource, final bool, due map[*hostResource]time.Duration) {
	for _, r := range resources {
		h.clearDirty(r)

		if wait := h.reconcile(ctx, r, final); wait > 0 {
			due[r] = h.monoNow() + wait
		} else {
			delete(due, r)
		}
	}
}

// reconcileAll runs one pass over every registered resource. Close uses it
// for its final pass, so nothing is left to remember afterwards.
func (h *Host) reconcileAll(ctx context.Context, final bool) {
	h.reconcileSet(ctx, h.resourceSnapshot(), final, make(map[*hostResource]time.Duration))
}

// graceRemaining starts (or continues) the teardown grace of an unheld,
// still present resource and returns how much of it is left.
func (h *Host) graceRemaining(r *hostResource, final bool) time.Duration {
	if final || h.cfg.TeardownGrace <= 0 {
		return 0
	}

	now := h.monoNow()

	if !r.unheld {
		r.unheld = true
		r.unheldSince = now
	}

	return max(r.unheldSince+h.cfg.TeardownGrace-now, 0)
}

// failPass records a failed pass, reports it, and returns the retry delay.
func (h *Host) failPass(r *hostResource, pass uint64, err error) time.Duration {
	r.finishPass(pass, false, err)
	h.report("reconciling "+r.name, err)

	return h.cfg.RetryInterval
}

// reconcile brings one resource's effect in line with its holders and
// returns how soon it wants another look (0 for none).
//
// The pass is numbered before it reads the holder set. A client whose
// acquire committed before that read is covered by this pass; WaitApplied
// waits for a pass numbered above the client's mark, which cannot have begun
// before the commit.
func (h *Host) reconcile(ctx context.Context, r *hostResource, final bool) time.Duration {
	pass := r.beginPass()

	held, err := h.reg.Leases.Held(h.actor, r.node)
	if err != nil {
		return h.failPass(r, pass, fmt.Errorf("reading the holders of %q: %w", r.name, err))
	}

	if held {
		r.unheld = false
	}

	if r.effect == nil {
		r.finishPass(pass, held, nil)
		return 0
	}

	present, err := r.checkPresent(ctx)
	if err != nil {
		return h.failPass(r, pass, fmt.Errorf("checking the effect of %q: %w", r.name, err))
	}

	if held == present {
		r.unheld = false
		r.finishPass(pass, held, nil)

		return 0
	}

	if !held {
		if wait := h.graceRemaining(r, final); wait > 0 {
			r.finishPass(pass, false, nil)
			return wait
		}
	}

	if convergeErr := r.converge(ctx, held); convergeErr != nil {
		return h.failPass(r, pass, convergeErr)
	}

	r.unheld = false
	r.finishPass(pass, held, nil)

	return 0
}
