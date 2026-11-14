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
	"sync/atomic"
	"time"
)

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
