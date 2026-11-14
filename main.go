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
	"fmt"
	ilog "log"
	"maps"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// ---------------------------------------------------------------------
// Host: the single owner process of one graph (theorystate.md sections 107
// and 112). It owns the store, the GraphActor, every registry and every
// Checker; clients (goroutines now, other processes and machines later)
// connect to it and call named operations, each run as one Transact.

var (
	// ErrHostClosed is returned for any operation attempted on, or still
	// running when, the Host is closed.
	ErrHostClosed = errors.New("host is closed")

	// ErrConnClosed is returned for any operation on a connection that has
	// been closed, dropped by its context, or expired by the keepalive TTL.
	ErrConnClosed = errors.New("connection is closed")

	// ErrUnknownResource is returned for a resource name that was never
	// registered with Host.RegisterResource.
	ErrUnknownResource = errors.New("resource is not registered")

	// ErrResourceRegistered is returned when a resource name is registered
	// twice.
	ErrResourceRegistered = errors.New("resource is already registered")

	// ErrResourceName is returned for an empty resource name, and for
	// PlanResource, the name a wire Authorizer is asked about for a plan,
	// which must never be a real resource.
	ErrResourceName = errors.New("resource name must not be empty or reserved")

	// ErrHoldLost is returned by WaitApplied when the session no longer
	// holds the resource it is waiting for.
	ErrHoldLost = errors.New("the session no longer holds the resource")

	// ErrEffectNotConverged is returned (inside the reconciler's error
	// report) when an effect's Apply/Remove succeeded but Present still
	// disagrees.
	ErrEffectNotConverged = errors.New("effect did not reach the wanted state")

	// ErrEffectTimeout is returned (inside the reconciler's error report)
	// when a call to an Effect did not return within
	// HostConfig.EffectTimeout and was abandoned. The call may still be
	// running; see ErrEffectBusy.
	ErrEffectTimeout = errors.New("effect call timed out")

	// ErrEffectBusy is returned (inside the reconciler's error report) when
	// a call to an Effect was not started because an earlier, abandoned
	// call to the same effect has not returned yet. It keeps the effect
	// from ever seeing two overlapping calls.
	ErrEffectBusy = errors.New("an earlier call to the effect has not returned")
)

// resourcePrefix namespaces resource names inside the NameRegistry, so they
// can never collide with a foundational name.
const resourcePrefix = "resource/"

// Effect is something outside the graph that exists exactly while a
// resource is held (a rule in some other system, say). dml defines only
// this contract; implementations, and any vocabulary specific to what they
// manage, belong to the consumer (theorystate.md section 112). The Host's
// reconciler drives it from the level (LeaseRegistry.Held), never only from
// acquire/release transitions, so every method must be idempotent and safe
// to repeat after a crash: Apply of a present effect and Remove of an absent
// one are successes. Present reports the real state of the outside world,
// not a cached belief. Implementations must not touch the graph. Each call
// runs under HostConfig.EffectTimeout: honor ctx where possible, since a call
// that ignores it is abandoned when the deadline passes (it cannot be killed,
// and no further call to the same effect starts until it returns).
type Effect interface {
	// Present reports whether the effect is currently in place.
	Present(ctx context.Context) (bool, error)
	// Apply puts the effect in place.
	Apply(ctx context.Context) error
	// Remove takes the effect away.
	Remove(ctx context.Context) error
}

// Defaults for HostConfig fields left at zero.
const (
	defaultKeepaliveTTL    = 15 * time.Second
	defaultTeardownGrace   = 2 * time.Second
	defaultReapInterval    = time.Second
	defaultResyncInterval  = 30 * time.Second
	defaultRetryInterval   = 2 * time.Second
	defaultShutdownTimeout = 10 * time.Second
	defaultEffectTimeout   = 30 * time.Second
)

// HostConfig configures a Host. Only Path is required.
//
// For KeepaliveTTL, TeardownGrace and EffectTimeout, zero means "use the
// default" and a negative value means "disabled". The other durations must
// be positive;
// zero or negative means the default.
type HostConfig struct {
	// Path is the bbolt file. Exactly one Host may own it.
	Path string

	// KeepaliveTTL is how long a connection may stay silent before the Host
	// treats it as dead. Connection drop is the primary liveness signal;
	// this is the backstop for partitions where no drop is ever seen. The
	// clock lives in host memory only. Any operation counts as a sign of
	// life, as does Conn.Keepalive. A client that waits for longer than the
	// TTL inside WaitApplied must keep calling Keepalive from elsewhere.
	KeepaliveTTL time.Duration

	// TeardownGrace is how long a resource must stay unheld before its
	// effect is removed, so that release-then-acquire races do not flap the
	// effect. Applying is never delayed.
	TeardownGrace time.Duration

	// ReapInterval is how often expired connections are collected and failed
	// session closes are retried.
	ReapInterval time.Duration

	// ResyncInterval is the longest the reconciler sleeps: every resource is
	// re-compared with the outside world at least this often, which repairs
	// drift.
	ResyncInterval time.Duration

	// RetryInterval is how soon a failed effect is retried.
	RetryInterval time.Duration

	// ShutdownTimeout bounds the final reconcile pass in Close.
	ShutdownTimeout time.Duration

	// EffectTimeout bounds each Effect.Present, Apply and Remove call. A call
	// that does not return in time is abandoned and reported as
	// ErrEffectTimeout, so one hung effect cannot stall the reconciler (which
	// serves every resource) or Close. An abandoned call cannot be killed if
	// it ignores its context, so no further call to that effect starts until
	// it returns (ErrEffectBusy). Disabled (negative), calls are made
	// directly, and Close can then wait for an effect that ignores its
	// context.
	EffectTimeout time.Duration

	// VerifyPageSize is the page size of the startup VerifyAll sweep (zero
	// means VerifyAll's own default).
	VerifyPageSize int

	// OnError receives errors from background work (effects that failed,
	// sessions that could not be closed). Nil means log.Printf.
	OnError func(op string, err error)
}

// durationOrDefault implements the "zero is default, negative is disabled"
// rule: it returns the default for zero, 0 (disabled) for a negative value,
// and v otherwise.
func durationOrDefault(v, def time.Duration) time.Duration {
	switch {
	case v == 0:
		return def
	case v < 0:
		return 0
	default:
		return v
	}
}

// positiveOrDefault returns v if it is positive and def otherwise.
func positiveOrDefault(v, def time.Duration) time.Duration {
	if v <= 0 {
		return def
	}

	return v
}

func defaultHostOnError(op string, err error) {
	ilog.Printf("dml host: %s: %v", op, err)
}

// withDefaults returns c with every unset field replaced by its default.
// After this, KeepaliveTTL and TeardownGrace of 0 mean "disabled".
func (c HostConfig) withDefaults() HostConfig {
	c.KeepaliveTTL = durationOrDefault(c.KeepaliveTTL, defaultKeepaliveTTL)
	c.TeardownGrace = durationOrDefault(c.TeardownGrace, defaultTeardownGrace)
	c.EffectTimeout = durationOrDefault(c.EffectTimeout, defaultEffectTimeout)
	c.ReapInterval = positiveOrDefault(c.ReapInterval, defaultReapInterval)
	c.ResyncInterval = positiveOrDefault(c.ResyncInterval, defaultResyncInterval)
	c.RetryInterval = positiveOrDefault(c.RetryInterval, defaultRetryInterval)
	c.ShutdownTimeout = positiveOrDefault(c.ShutdownTimeout, defaultShutdownTimeout)

	if c.OnError == nil {
		c.OnError = defaultHostOnError
	}

	return c
}

// AcquireResult is what Client.Acquire returns.
type AcquireResult struct {
	// First reports whether this call made the resource's holder set
	// non-empty. It is a transition hint: it means "wanted", never "in
	// effect".
	First bool

	// Mark is the token to pass to WaitApplied. It identifies the last
	// reconcile pass of the resource that may have started before this
	// acquire committed; the effect is applied for this hold once a later
	// pass has completed with it present.
	Mark uint64
}

// Client is the named-operation surface the Host offers one connection.
// Every method is one request to the host, run there as one Transact (or a
// short read). It deliberately contains no closures and no NodeIDs, so a
// network transport can implement it by sending each call to the Host and
// the in-process Conn is a client exactly like a remote one will be.
type Client interface {
	// Acquire makes the connection's session a holder of resource.
	Acquire(ctx context.Context, resource string) (AcquireResult, error)

	// Release drops the hold; last reports whether nobody else holds it.
	Release(ctx context.Context, resource string) (last bool, err error)

	// WaitApplied blocks until the effect of resource is confirmed in place
	// for a hold taken by the Acquire that returned mark, the hold is lost
	// (ErrHoldLost), the connection dies (ErrConnClosed), the host closes
	// (ErrHostClosed), or ctx ends (the error then also carries the last
	// effect failure, if any).
	WaitApplied(ctx context.Context, resource string, mark uint64) error

	// Keepalive tells the host the client is alive (any operation does).
	Keepalive(ctx context.Context) error

	// Close ends the connection and releases everything it holds.
	Close() error

	// Done is closed when the connection has ended for any reason.
	Done() <-chan struct{}
}

// AcquireAndWait acquires resource and waits until its effect is in place.
// It is built only on the Client interface, so it works over any transport.
func AcquireAndWait(ctx context.Context, client Client, resource string) error {
	result, err := client.Acquire(ctx, resource)
	if err != nil {
		return fmt.Errorf("acquiring %q: %w", resource, err)
	}

	return wrapInterfaceErr(client.WaitApplied(ctx, resource, result.Mark))
}

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

// Host owns one graph: the bbolt store, the GraphActor over RootGraph over
// BoltGraph, every registry and every Checker (theorystate.md sections 107
// and 112). Create it with OpenHost; it must be closed exactly once with
// Close.
//
// Liveness. Every client connection owns one session node. A connection is
// dropped when its context ends (the in-process stand-in for a socket
// closing), when Close is called on it, or when it stays silent past
// KeepaliveTTL. Dropping calls LeaseRegistry.CloseSession and wakes the
// reconciler. A session whose close fails is retried by the reaper.
//
// Effects. The reconciler compares, for a registered resource, the desired
// state (LeaseRegistry.Held) with the actual one (Effect.Present) and
// converges them. A wake-up looks only at the resources a change marked
// dirty (an acquire, a release, a session that freed it, its registration)
// and at those whose own deadline has come (a teardown grace running, a
// failed effect waiting for RetryInterval). Every ResyncInterval it looks at
// every resource, which repairs drift nobody touched.
type Host struct {
	cfg   HostConfig
	store *BoltGraph
	actor *GraphActor
	names *NameRegistry
	reg   *Registries
	root  NodeID

	epoch  time.Time
	stop   chan struct{}
	cancel context.CancelFunc
	kickCh chan struct{}
	bg     sync.WaitGroup
	ops    sync.WaitGroup

	mu      sync.Mutex
	closed  bool
	conns   map[NodeID]*Conn
	closing map[NodeID]struct{}

	resMu     sync.RWMutex
	resources map[string]*hostResource
	byNode    map[NodeID]*hostResource

	// dirty is the set of resources something happened to since the
	// reconciler last took it (see markDirty).
	dirtyMu sync.Mutex
	dirty   map[*hostResource]struct{}

	// operations holds the named handlers clients may run (operations.go).
	// Its zero value is ready to use.
	operations operationTable
}

// OpenHost performs the startup order of theorystate.md sections 110 and
// 112 and returns a running Host:
//
//  1. OpenBoltGraph (single-open guard, format version check);
//  2. CheckStore (physical and layout check);
//  3. NameRegistry.LoadNames and the ROOT node, on the raw store: RootGraph
//     needs ROOT to exist before the stack can be built;
//  4. GraphActor over RootGraph over BoltGraph (actor outermost);
//  5. BootstrapNames(FoundationalNames) and every registry (this registers
//     the Checkers);
//  6. CloseAllSessions: sessions are ephemeral, every client died with the
//     previous process;
//  7. VerifyAll, ListRegistry.VerifyStructures and NameRegistry.VerifyBindings,
//     all fail-closed.
//
// Any failure closes whatever was opened and returns the error.
func OpenHost(cfg HostConfig) (*Host, error) {
	if cfg.Path == "" {
		return nil, errors.New("host: HostConfig.Path is required")
	}

	cfg = cfg.withDefaults()

	store, err := OpenBoltGraph(cfg.Path)
	if err != nil {
		return nil, fmt.Errorf("host startup: %w", err)
	}

	h, startErr := startHost(cfg, store)
	if startErr != nil {
		// store.Close() returns nil on success, and joinErrors drops nils,
		// so this is just startErr unless closing failed too.
		return nil, joinErrors(startErr, store.Close())
	}

	return h, nil
}

// startHost runs startup steps 2 to 4 and hands over to finishHostStart.
func startHost(cfg HostConfig, store *BoltGraph) (*Host, error) {
	if checkErr := store.CheckStore(); checkErr != nil {
		return nil, fmt.Errorf("host startup: checking the store: %w", checkErr)
	}

	names := NewNameRegistry(store)

	if loadErr := names.LoadNames(store); loadErr != nil {
		return nil, fmt.Errorf("host startup: loading names: %w", loadErr)
	}

	root, rootErr := names.EnsureNamedNode(store, NameRoot)
	if rootErr != nil {
		return nil, fmt.Errorf("host startup: ensuring ROOT: %w", rootErr)
	}

	rootGraph, layerErr := NewRootGraph(store, root)
	if layerErr != nil {
		return nil, fmt.Errorf("host startup: building the ROOT layer: %w", layerErr)
	}

	actor := NewGraphActor(rootGraph)

	h, finishErr := finishHostStart(cfg, store, actor, names, root)
	if finishErr != nil {
		actor.Close()
		return nil, finishErr
	}

	return h, nil
}

// finishHostStart runs startup steps 5 to 7 and starts the background
// goroutines. Nothing is started before every fallible step has passed.
func finishHostStart(cfg HostConfig, store *BoltGraph, actor *GraphActor, names *NameRegistry, root NodeID) (*Host, error) {
	ids, idsErr := names.BootstrapNames(actor, FoundationalNames)
	if idsErr != nil {
		return nil, fmt.Errorf("host startup: bootstrapping names: %w", idsErr)
	}

	if tagErr := TagFoundational(actor, ids, root); tagErr != nil {
		return nil, fmt.Errorf("host startup: tagging foundational names: %w", tagErr)
	}

	reg, regErr := NewRegistries(actor, names, ids)
	if regErr != nil {
		return nil, fmt.Errorf("host startup: constructing registries: %w", regErr)
	}

	if _, sweepErr := reg.Leases.CloseAllSessions(actor); sweepErr != nil {
		return nil, fmt.Errorf("host startup: closing stale sessions: %w", sweepErr)
	}

	if verifyErr := VerifyAll(actor, cfg.VerifyPageSize); verifyErr != nil {
		return nil, fmt.Errorf("host startup: %w", verifyErr)
	}

	for _, lists := range []OrderedList{reg.Lists, reg.SlottedLists} {
		if listsErr := lists.VerifyStructures(actor); listsErr != nil {
			return nil, fmt.Errorf("host startup: %w", listsErr)
		}
	}

	if bindErr := names.VerifyBindings(actor); bindErr != nil {
		return nil, fmt.Errorf("host startup: verifying names: %w", bindErr)
	}

	h := &Host{
		cfg:       cfg,
		store:     store,
		actor:     actor,
		names:     names,
		reg:       reg,
		root:      root,
		epoch:     time.Now(),
		stop:      make(chan struct{}),
		kickCh:    make(chan struct{}, 1),
		conns:     make(map[NodeID]*Conn),
		closing:   make(map[NodeID]struct{}),
		resources: make(map[string]*hostResource),
		byNode:    make(map[NodeID]*hostResource),
		dirty:     make(map[*hostResource]struct{}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel

	h.bg.Go(func() { h.reconcileLoop(ctx) })
	h.bg.Go(h.reapLoop)

	return h, nil
}

// Registries returns the registries, for the owner process's own use.
func (h *Host) Registries() *Registries {
	return h.reg
}

// Graph returns the graph handle for the owner process's own use. It is
// guarded (see hostGraph): after Close every call returns ErrHostClosed
// instead of panicking, Close waits for calls still running, and a write that
// changes a resource's holder set wakes the reconciler. Pass it to registry
// methods as usual; never use it from inside a Transact closure (use the tx
// the closure was given), exactly as with a GraphActor.
func (h *Host) Graph() GraphAPI {
	return hostGraph{host: h}
}

// monoNow is the host's monotonic clock: time since the host started.
func (h *Host) monoNow() time.Duration {
	return time.Since(h.epoch)
}

// report hands a background error to cfg.OnError.
func (h *Host) report(op string, err error) {
	if err != nil {
		h.cfg.OnError(op, err)
	}
}

// enter registers an operation that uses the actor. It fails once Close has
// begun, and Close waits for every entered operation, so no operation can
// reach the actor after it has been closed. Every successful enter must be
// paired with leave.
func (h *Host) enter() bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return false
	}

	h.ops.Add(1)

	return true
}

func (h *Host) leave() {
	h.ops.Done()
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

// Close shuts the host down: operations stop, every connection is dropped
// and its session closed, a final reconcile pass removes every effect that
// is no longer held (without the teardown grace), and the actor and store
// are closed. It is safe to call more than once; later calls return nil.
func (h *Host) Close() error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}

	h.closed = true
	close(h.stop)
	h.mu.Unlock()

	h.cancel()
	h.bg.Wait()
	h.ops.Wait()

	// Nothing else uses the actor from here on.
	errs := h.shutdownConns()

	finalCtx, finalCancel := context.WithTimeout(context.Background(), h.cfg.ShutdownTimeout)
	h.reconcileAll(finalCtx, true)
	finalCancel()

	h.actor.Close()
	errs = append(errs, h.store.Close())

	return joinErrors(errs...)
}

// shutdownConns drops every remaining connection and retries every session
// close that had failed. Only Close calls it, after all other users of the
// actor have finished.
func (h *Host) shutdownConns() []error {
	h.mu.Lock()
	conns := slices.Collect(maps.Values(h.conns))
	pending := slices.Collect(maps.Keys(h.closing))
	h.mu.Unlock()

	errs := make([]error, 0, len(conns)+len(pending))

	for _, c := range conns {
		c.markDone()
		errs = append(errs, h.releaseConn(c))
	}

	for _, session := range pending {
		errs = append(errs, h.closeSession(session))
	}

	return errs
}

// RegisterResource declares a resource and its effect (nil for a pure
// lease with no outside effect). It creates the resource's holder Set,
// named resource/<name>, once and idempotently across restarts. Clients can
// acquire only registered resources. The effect is only held in memory, so
// it must be registered again after every restart; the reconciler then
// compares it with reality at once and removes a rule left behind by a
// previous run.
func (h *Host) RegisterResource(name string, effect Effect) error {
	if name == "" || name == PlanResource {
		return ErrResourceName
	}

	if !h.enter() {
		return ErrHostClosed
	}
	defer h.leave()

	node, err := transactValue(h.actor, func(tx Tx) (NodeID, error) {
		id, nameErr := h.names.EnsureNamedNode(tx, resourcePrefix+name)
		if nameErr != nil {
			return 0, nameErr
		}

		if tagErr := h.reg.Sets.TagAsSet(tx, id); tagErr != nil {
			return 0, tagErr
		}

		return id, nil
	})
	if err != nil {
		return fmt.Errorf("registering resource %q: %w", name, err)
	}

	res := newHostResource(name, node, effect, h.cfg.EffectTimeout)
	if !h.addResource(res) {
		return fmt.Errorf("%w: %q", ErrResourceRegistered, name)
	}

	h.markDirty(res)

	return nil
}

// addResource records r unless its name is taken, and reports whether it did.
func (h *Host) addResource(r *hostResource) bool {
	h.resMu.Lock()
	defer h.resMu.Unlock()

	if _, taken := h.resources[r.name]; taken {
		return false
	}

	h.resources[r.name] = r
	h.byNode[r.node] = r

	return true
}

func (h *Host) lookupResource(name string) (*hostResource, error) {
	h.resMu.RLock()
	defer h.resMu.RUnlock()

	r, ok := h.resources[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownResource, name)
	}

	return r, nil
}

// resourceSnapshot returns the registered resources sorted by name.
func (h *Host) resourceSnapshot() []*hostResource {
	h.resMu.RLock()
	defer h.resMu.RUnlock()

	names := slices.Sorted(maps.Keys(h.resources))
	snapshot := make([]*hostResource, 0, len(names))

	for _, name := range names {
		snapshot = append(snapshot, h.resources[name])
	}

	return snapshot
}

// Conn is one client's connection to the Host, in-process. It implements
// Client. A remote transport would implement Client over the network and
// call the same Host methods on the far side.
type Conn struct {
	host     *Host
	session  NodeID
	done     chan struct{}
	doneFlag atomic.Bool
	lastSeen atomic.Int64
}

// Compile-time assertion that *Conn satisfies Client.
var _ Client = (*Conn)(nil)

// Connect opens a connection and its session. The connection lives until
// Close is called on it, ctx ends (the in-process equivalent of a dropped
// socket), or it stays silent past KeepaliveTTL.
func (h *Host) Connect(ctx context.Context) (*Conn, error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("connecting: %w", ctxErr)
	}

	if !h.enter() {
		return nil, ErrHostClosed
	}
	defer h.leave()

	session, err := h.reg.Leases.NewSession(h.actor)
	if err != nil {
		return nil, fmt.Errorf("opening a session: %w", err)
	}

	c := &Conn{host: h, session: session, done: make(chan struct{})}
	c.touch()

	h.mu.Lock()
	h.conns[session] = c
	h.mu.Unlock()

	go h.watchConn(ctx, c)

	return c, nil
}

// watchConn drops c when its context ends, and ends when c ends first.
func (h *Host) watchConn(ctx context.Context, c *Conn) {
	select {
	case <-ctx.Done():
		h.report("dropping a connection whose context ended", h.dropConn(c))
	case <-c.done:
	}
}

// dropConn ends c and closes its session. Only the first caller does the
// work. If the host is already closing, Close closes the session instead.
func (h *Host) dropConn(c *Conn) error {
	if !c.markDone() {
		return nil
	}

	if !h.enter() {
		return nil
	}
	defer h.leave()

	return h.releaseConn(c)
}

// releaseConn forgets c and closes its session; a failed close stays
// pending for the reaper to retry.
func (h *Host) releaseConn(c *Conn) error {
	h.mu.Lock()
	delete(h.conns, c.session)
	h.closing[c.session] = struct{}{}
	h.mu.Unlock()

	return h.closeSession(c.session)
}

// closeSession releases every hold of session and deletes it, then wakes
// the reconciler. A session that is already gone counts as closed. On any
// other failure the session stays in h.closing and is retried.
func (h *Host) closeSession(session NodeID) error {
	freed, err := h.reg.Leases.CloseSession(h.actor, session)
	if err != nil && !errors.Is(err, ErrNodeNotFound) {
		return fmt.Errorf("closing session %d: %w", session, err)
	}

	h.mu.Lock()
	delete(h.closing, session)
	h.mu.Unlock()

	// The reconciler is level-driven, so freed is only a wake-up hint: mark
	// the resources that lost their last holder so it looks at them.
	h.markDirtyNodes(freed)

	return nil
}

// reapLoop expires silent connections and retries failed session closes.
func (h *Host) reapLoop() {
	ticker := time.NewTicker(h.cfg.ReapInterval)
	defer ticker.Stop()

	for {
		select {
		case <-h.stop:
			return
		case <-ticker.C:
			h.reapOnce()
		}
	}
}

func (h *Host) reapOnce() {
	for _, c := range h.expiredConns() {
		if c.markDone() {
			h.report("expiring a silent connection", h.releaseConn(c))
		}
	}

	h.mu.Lock()
	pending := slices.Collect(maps.Keys(h.closing))
	h.mu.Unlock()

	for _, session := range pending {
		h.report("retrying a session close", h.closeSession(session))
	}
}

// expiredConns returns the connections silent for longer than KeepaliveTTL.
func (h *Host) expiredConns() []*Conn {
	ttl := h.cfg.KeepaliveTTL
	if ttl <= 0 {
		return nil
	}

	now := h.monoNow()

	h.mu.Lock()
	defer h.mu.Unlock()

	var expired []*Conn

	for _, c := range h.conns {
		if now-time.Duration(c.lastSeen.Load()) > ttl {
			expired = append(expired, c)
		}
	}

	return expired
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

// touch records a sign of life.
func (c *Conn) touch() {
	c.lastSeen.Store(int64(c.host.monoNow()))
}

func (c *Conn) isDone() bool {
	return c.doneFlag.Load()
}

// markDone ends the connection and reports whether this call was the one
// that did.
func (c *Conn) markDone() bool {
	if !c.doneFlag.CompareAndSwap(false, true) {
		return false
	}

	close(c.done)

	return true
}

// Done is closed when the connection has ended for any reason.
func (c *Conn) Done() <-chan struct{} {
	return c.done
}

// Close ends the connection and releases every hold of its session.
func (c *Conn) Close() error {
	return c.host.dropConn(c)
}

// Keepalive records a sign of life. It never touches the graph.
func (c *Conn) Keepalive(ctx context.Context) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("keepalive: %w", ctxErr)
	}

	if c.isDone() {
		return ErrConnClosed
	}

	c.touch()

	return nil
}

// enterResourceQuietly validates the request and enters the host, without
// counting it as a sign of life of the client (for work the host itself
// starts); on success the caller must call host.leave.
func (c *Conn) enterResourceQuietly(ctx context.Context, name string) (*hostResource, error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("resource %q: %w", name, ctxErr)
	}

	if c.isDone() {
		return nil, ErrConnClosed
	}

	res, err := c.host.lookupResource(name)
	if err != nil {
		return nil, err
	}

	if !c.host.enter() {
		return nil, ErrHostClosed
	}

	return res, nil
}

// enterResource is enterResourceQuietly for a request of the client, which is
// a sign of life; on success the caller must call host.leave.
func (c *Conn) enterResource(ctx context.Context, name string) (*hostResource, error) {
	res, err := c.enterResourceQuietly(ctx, name)
	if err != nil {
		return nil, err
	}

	c.touch()

	return res, nil
}

// opErr turns a graph error into the connection's own: if the connection
// ended in the meantime (so its session may be gone) the real cause is
// ErrConnClosed.
func (c *Conn) opErr(op, resource string, err error) error {
	if c.isDone() {
		return fmt.Errorf("%w (%s %q: %w)", ErrConnClosed, op, resource, err)
	}

	return fmt.Errorf("%s %q: %w", op, resource, err)
}

// Acquire implements Client: one Transact adding the session to the
// resource's holder Set.
func (c *Conn) Acquire(ctx context.Context, resource string) (AcquireResult, error) {
	res, err := c.enterResource(ctx, resource)
	if err != nil {
		return AcquireResult{}, err
	}
	defer c.host.leave()

	first, acquireErr := c.host.reg.Leases.Acquire(c.host.actor, res.node, c.session)
	if acquireErr != nil {
		return AcquireResult{}, c.opErr("acquire", resource, acquireErr)
	}

	mark := res.mark()
	c.host.markDirty(res)

	return AcquireResult{First: first, Mark: mark}, nil
}

// release removes the session from res's holder Set and wakes the reconciler.
// The caller has entered the host.
func (c *Conn) release(res *hostResource, resource string) (bool, error) {
	last, err := c.host.reg.Leases.Release(c.host.actor, res.node, c.session)
	if err != nil {
		return false, c.opErr("release", resource, err)
	}

	c.host.markDirty(res)

	return last, nil
}

// Release implements Client: one Transact removing the session from the
// resource's holder Set. The effect itself is removed by the reconciler,
// after the teardown grace.
func (c *Conn) Release(ctx context.Context, resource string) (bool, error) {
	res, err := c.enterResource(ctx, resource)
	if err != nil {
		return false, err
	}
	defer c.host.leave()

	return c.release(res, resource)
}

// isHolder reports whether this connection's session holds res.
func (c *Conn) isHolder(res *hostResource) (bool, error) {
	return c.host.reg.Sets.Contains(c.host.actor, res.node, c.session)
}

// holds reports whether this connection's session holds the named resource.
// It is for the wire server's authorization, not a request of the client.
func (c *Conn) holds(ctx context.Context, resource string) (bool, error) {
	res, err := c.enterResourceQuietly(ctx, resource)
	if err != nil {
		return false, err
	}
	defer c.host.leave()

	holder, holdErr := c.isHolder(res)
	if holdErr != nil {
		return false, c.opErr("check the hold of", resource, holdErr)
	}

	return holder, nil
}

// heldResources returns the names of the registered resources this
// connection's session holds, for WireServer.Revalidate.
func (c *Conn) heldResources() ([]string, error) {
	if c.isDone() {
		return nil, ErrConnClosed
	}

	if !c.host.enter() {
		return nil, ErrHostClosed
	}
	defer c.host.leave()

	var held []string

	for _, res := range c.host.resourceSnapshot() {
		holder, err := c.isHolder(res)
		if err != nil {
			return nil, c.opErr("list the holds of", res.name, err)
		}

		if holder {
			held = append(held, res.name)
		}
	}

	return held, nil
}

// revoke releases the session's hold of the named resource on the host's own
// initiative (WireServer.Revalidate): unlike Release it is not a sign of life
// of the client.
func (c *Conn) revoke(ctx context.Context, resource string) error {
	res, err := c.enterResourceQuietly(ctx, resource)
	if err != nil {
		return err
	}
	defer c.host.leave()

	_, releaseErr := c.release(res, resource)

	return releaseErr
}

// waitErr builds WaitApplied's context-ended error, carrying the last
// effect failure if there was one.
func waitErr(cause error, resource string, last error) error {
	if last == nil {
		return fmt.Errorf("waiting for %q to be applied: %w", resource, cause)
	}

	return fmt.Errorf("waiting for %q to be applied: %w (last effect error: %w)", resource, cause, last)
}

// WaitApplied implements Client. Each time it wakes it first checks that
// the session still holds the resource, then whether a pass numbered above
// mark has completed with the effect present. See AcquireResult.Mark.
func (c *Conn) WaitApplied(ctx context.Context, resource string, mark uint64) error {
	res, err := c.enterResource(ctx, resource)
	if err != nil {
		return err
	}
	defer c.host.leave()

	for {
		holder, holdErr := c.isHolder(res)
		if holdErr != nil {
			return c.opErr("wait for", resource, holdErr)
		}

		if !holder {
			return fmt.Errorf("%w: %q", ErrHoldLost, resource)
		}

		out := res.outcome()
		if out.completed > mark && out.applied {
			return nil
		}

		select {
		case <-ctx.Done():
			return waitErr(ctx.Err(), resource, out.err)
		case <-c.done:
			return ErrConnClosed
		case <-c.host.stop:
			return ErrHostClosed
		case <-out.changed:
		}
	}
}
