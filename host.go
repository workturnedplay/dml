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
	"maps"
	"slices"
	"sync"
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
