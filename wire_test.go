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
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// wireRig is a Host served over in-memory pipes (net.Pipe), so these tests
// need no network and therefore no firewall exception, on any OS. The tests
// that use real TCP sockets are the TestFWNeeded* ones, behind the
// portmasterFirewalled build tag (wire_firewalled_test.go and
// wire_tls_firewalled_test.go).
type wireRig struct {
	host   *Host
	server *WireServer
}

func newWireRig(t *testing.T, mutate func(cfg *HostConfig), opts ...WireOption) *wireRig {
	t.Helper()

	h := openTestHost(t, boltTestPath(t), mutate)
	server := NewWireServer(h, opts...)

	// Registered after the host's own cleanup, so it runs first.
	t.Cleanup(func() {
		if closeErr := server.Close(); closeErr != nil {
			t.Errorf("server Close(): %v", closeErr)
		}
	})

	return &wireRig{host: h, server: server}
}

func (r *wireRig) dial(t *testing.T) *WireClient {
	t.Helper()

	return startWireClient(t, serveInMemory(t, r.server))
}

// serveInMemory serves one in-memory connection on server and returns the
// client side. It is closed, and the handler awaited, when the test ends.
func serveInMemory(t *testing.T, server *WireServer) net.Conn {
	t.Helper()

	clientSide, serverSide := net.Pipe()
	done := make(chan struct{})

	go func() {
		defer close(done)
		server.ServeConn(serverSide)
	}()

	t.Cleanup(func() {
		closeQuietly(clientSide)
		<-done
	})

	return clientSide
}

// startWireClient performs the wire handshake over nc and closes the client
// (gracefully) when the test ends. It works over any net.Conn.
func startWireClient(t *testing.T, nc net.Conn) *WireClient {
	t.Helper()

	client, err := NewWireClient(hostTestContext(t), nc)
	if err != nil {
		t.Fatalf("NewWireClient(): %v", err)
	}

	t.Cleanup(func() {
		if closeErr := client.Close(); closeErr != nil {
			t.Errorf("client Close(): %v", closeErr)
		}
	})

	return client
}

// newRawWirePeer connects a hand-driven peer, speaking raw frames, to server.
func newRawWirePeer(t *testing.T, server *WireServer) (net.Conn, *bufio.Reader) {
	t.Helper()

	clientSide := serveInMemory(t, server)

	return clientSide, bufio.NewReader(clientSide)
}

func TestWireManyHoldersApplyOnceAndRemoveOnce(t *testing.T) {
	ctx := hostTestContext(t)
	rig := newWireRig(t, nil)
	fw := &fakeEffect{}
	registerTestResource(t, rig.host, "example-resource", fw)

	const clients = 10

	remote := make([]*WireClient, clients)
	for i := range remote {
		remote[i] = rig.dial(t)
	}

	errs := make([]error, clients)

	runConcurrently(clients, func(i int) { errs[i] = AcquireAndWait(ctx, remote[i], "example-resource") })

	for i, acquireErr := range errs {
		if acquireErr != nil {
			t.Fatalf("client %d: AcquireAndWait(): %v", i, acquireErr)
		}
	}

	requireEffectCounts(t, fw, 1, 0, true)

	runConcurrently(clients, func(i int) { _, errs[i] = remote[i].Release(ctx, "example-resource") })

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

func TestWireErrorsKeepTheirIdentity(t *testing.T) {
	ctx := hostTestContext(t)
	rig := newWireRig(t, nil)
	registerTestResource(t, rig.host, "example-resource", nil)
	client := rig.dial(t)

	_, err := client.Acquire(ctx, "nope")
	if !errors.Is(err, ErrUnknownResource) {
		t.Fatalf("Acquire(unknown) error = %v, want %v", err, ErrUnknownResource)
	}

	var remote *RemoteError
	if !errors.As(err, &remote) || remote.Code != "unknown_resource" {
		t.Fatalf("Acquire(unknown) error = %#v, want a *RemoteError with code unknown_resource", err)
	}

	result, err := client.Acquire(ctx, "example-resource")
	if err != nil || !result.First {
		t.Fatalf("Acquire() = (%+v,%v), want First=true", result, err)
	}

	if last, releaseErr := client.Release(ctx, "example-resource"); releaseErr != nil || !last {
		t.Fatalf("Release() = (%v,%v), want last=true", last, releaseErr)
	}

	if waitErr := client.WaitApplied(ctx, "example-resource", result.Mark); !errors.Is(waitErr, ErrHoldLost) {
		t.Fatalf("WaitApplied() after the release error = %v, want %v", waitErr, ErrHoldLost)
	}

	// A code this build does not know still arrives, as ErrRemote.
	unknown := remoteError(&wireError{Code: "made_up", Message: "boom"})
	if !errors.Is(unknown, ErrRemote) || unknown.Error() != "boom" {
		t.Fatalf("remoteError(unknown code) = %v, want ErrRemote with the host's message", unknown)
	}
}

// TestWireWaitAppliedCancelKeepsTheConnectionAndReportsTheLastEffectError
// also shows requests are multiplexed: while one WaitApplied is pending,
// another resource is acquired and applied on the same connection.
func TestWireWaitAppliedCancelKeepsTheConnectionAndReportsTheLastEffectError(t *testing.T) {
	ctx := hostTestContext(t)
	rig := newWireRig(t, nil)

	registerTestResource(t, rig.host, "broken", &fakeEffect{applyFailures: 1 << 30})
	registerTestResource(t, rig.host, "plain", nil)

	client := rig.dial(t)

	waitCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	waited := make(chan error, 1)

	go func() { waited <- AcquireAndWait(waitCtx, client, "broken") }()

	if plainErr := AcquireAndWait(ctx, client, "plain"); plainErr != nil {
		t.Fatalf("AcquireAndWait(plain) while another wait is pending: %v", plainErr)
	}

	select {
	case early := <-waited:
		t.Fatalf("the broken resource's wait ended early with %v", early)
	default:
	}

	waitErr := <-waited
	if !errors.Is(waitErr, context.DeadlineExceeded) {
		t.Fatalf("AcquireAndWait(broken) error = %v, want %v", waitErr, context.DeadlineExceeded)
	}

	if !strings.Contains(waitErr.Error(), errFakeEffect.Error()) {
		t.Fatalf("AcquireAndWait(broken) error = %q, want the host's last effect error in it", waitErr)
	}

	// The cancel ended one request, not the connection or the hold.
	if keepErr := client.Keepalive(ctx); keepErr != nil {
		t.Fatalf("Keepalive() after a cancelled wait: %v", keepErr)
	}

	select {
	case <-client.Done():
		t.Fatal("the connection ended because one request was cancelled")
	default:
	}
}

func TestWireCloseReleasesHoldsBeforeReturning(t *testing.T) {
	ctx := hostTestContext(t)
	rig := newWireRig(t, nil)
	fw := &fakeEffect{}
	registerTestResource(t, rig.host, "example-resource", fw)

	client := rig.dial(t)
	if acquireErr := AcquireAndWait(ctx, client, "example-resource"); acquireErr != nil {
		t.Fatalf("AcquireAndWait(): %v", acquireErr)
	}

	node, ok := rig.host.names.Lookup(resourcePrefix + "example-resource")
	if !ok {
		t.Fatal("the resource name is not bound")
	}

	if closeErr := client.Close(); closeErr != nil {
		t.Fatalf("Close(): %v", closeErr)
	}

	held, err := rig.host.Registries().Leases.Held(rig.host.Graph(), node)
	if err != nil || held {
		t.Fatalf("Held() right after Close() = (%v,%v), want (false,nil)", held, err)
	}

	eventually(t, "the effect to be removed", func() bool {
		_, _, present := fw.counts()
		return !present
	})

	if _, acquireErr := client.Acquire(ctx, "example-resource"); !errors.Is(acquireErr, ErrConnClosed) {
		t.Fatalf("Acquire() after Close error = %v, want %v", acquireErr, ErrConnClosed)
	}

	if closeErr := client.Close(); closeErr != nil {
		t.Fatalf("second Close(): %v", closeErr)
	}
}

func TestWireDroppedConnectionReleasesTheSession(t *testing.T) {
	ctx := hostTestContext(t)
	rig := newWireRig(t, nil)
	fw := &fakeEffect{}
	registerTestResource(t, rig.host, "example-resource", fw)

	client := rig.dial(t)
	if acquireErr := AcquireAndWait(ctx, client, "example-resource"); acquireErr != nil {
		t.Fatalf("AcquireAndWait(): %v", acquireErr)
	}

	// The process "crashes": the transport vanishes with no close frame.
	closeQuietly(client.nc)

	select {
	case <-client.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the client did not notice its connection died")
	}

	eventually(t, "the dropped session's effect to be removed", func() bool {
		_, _, present := fw.counts()
		return !present
	})

	if _, releaseErr := client.Release(ctx, "example-resource"); !errors.Is(releaseErr, ErrConnClosed) {
		t.Fatalf("Release() on a dropped connection error = %v, want %v", releaseErr, ErrConnClosed)
	}
}

func TestWireClientHeartbeatKeepsAnIdleConnectionAliveAcrossTheTTL(t *testing.T) {
	ctx := hostTestContext(t)

	const ttl = 200 * time.Millisecond

	rig := newWireRig(t, func(cfg *HostConfig) { cfg.KeepaliveTTL = ttl })
	registerTestResource(t, rig.host, "example-resource", nil)

	client := rig.dial(t)
	if acquireErr := AcquireAndWait(ctx, client, "example-resource"); acquireErr != nil {
		t.Fatalf("AcquireAndWait(): %v", acquireErr)
	}

	time.Sleep(4 * ttl)

	select {
	case <-client.Done():
		t.Fatal("the idle connection was expired although the client heartbeats")
	default:
	}

	if keepErr := client.Keepalive(ctx); keepErr != nil {
		t.Fatalf("Keepalive() after idling past the TTL: %v", keepErr)
	}

	node, ok := rig.host.names.Lookup(resourcePrefix + "example-resource")
	if !ok {
		t.Fatal("the resource name is not bound")
	}

	held, err := rig.host.Registries().Leases.Held(rig.host.Graph(), node)
	if err != nil || !held {
		t.Fatalf("Held() = (%v,%v), want (true,nil): the session must have survived", held, err)
	}
}

// TestWireSilentRawPeerIsExpiredAndDisconnected drives the protocol by hand
// with a peer that says hello and then goes silent: the host expires it and
// the server closes the transport, so the peer sees EOF.
func TestWireSilentRawPeerIsExpiredAndDisconnected(t *testing.T) {
	h := openTestHost(t, boltTestPath(t), func(cfg *HostConfig) { cfg.KeepaliveTTL = 150 * time.Millisecond })
	server := NewWireServer(h)

	t.Cleanup(func() {
		if closeErr := server.Close(); closeErr != nil {
			t.Errorf("server Close(): %v", closeErr)
		}
	})

	peer, reader := newRawWirePeer(t, server)

	if writeErr := writeWireFrame(peer, &wireRequest{ID: 1, Op: wireOpHello, Version: wireVersion}); writeErr != nil {
		t.Fatalf("writing the hello: %v", writeErr)
	}

	var hello wireResponse
	if readErr := readWireFrame(reader, &hello); readErr != nil {
		t.Fatalf("reading the hello response: %v", readErr)
	}

	if hello.Error != nil || hello.KeepaliveTTLMillis != 150 {
		t.Fatalf("hello response = %+v, want no error and a 150ms keepalive TTL", hello)
	}

	if deadlineErr := peer.SetReadDeadline(time.Now().Add(10 * time.Second)); deadlineErr != nil {
		t.Fatalf("SetReadDeadline(): %v", deadlineErr)
	}

	var next wireResponse

	if readErr := readWireFrame(reader, &next); !errors.Is(readErr, io.EOF) {
		t.Fatalf("read from an expired connection error = %v, want %v", readErr, io.EOF)
	}
}

func TestWireHandshakeRejectsWrongVersionAndMissingHello(t *testing.T) {
	h := openTestHost(t, boltTestPath(t), nil)
	server := NewWireServer(h)

	t.Cleanup(func() {
		if closeErr := server.Close(); closeErr != nil {
			t.Errorf("server Close(): %v", closeErr)
		}
	})

	cases := []struct {
		name  string
		first wireRequest
	}{
		{name: "wrong version", first: wireRequest{ID: 1, Op: wireOpHello, Version: wireVersion + 1}},
		{name: "no hello", first: wireRequest{ID: 1, Op: wireOpAcquire, Resource: "x"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			peer, reader := newRawWirePeer(t, server)

			if writeErr := writeWireFrame(peer, &tc.first); writeErr != nil {
				t.Fatalf("writing the first frame: %v", writeErr)
			}

			var resp wireResponse
			if readErr := readWireFrame(reader, &resp); readErr != nil {
				t.Fatalf("reading the response: %v", readErr)
			}

			if resp.Error == nil || resp.Error.Code != "protocol" {
				t.Fatalf("response = %+v, want a protocol error", resp)
			}

			var after wireResponse
			if readErr := readWireFrame(reader, &after); !errors.Is(readErr, io.EOF) {
				t.Fatalf("read after the rejection error = %v, want %v", readErr, io.EOF)
			}
		})
	}
}

func TestWireFrameRoundTripAndLimits(t *testing.T) {
	var buf bytes.Buffer

	want := wireRequest{ID: 7, Op: wireOpAcquire, Resource: "example-resource", Mark: ^uint64(0)}
	if writeErr := writeWireFrame(&buf, &want); writeErr != nil {
		t.Fatalf("writeWireFrame(): %v", writeErr)
	}

	var got wireRequest
	if readErr := readWireFrame(&buf, &got); readErr != nil {
		t.Fatalf("readWireFrame(): %v", readErr)
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip = %+v, want %+v (uint64 values must survive exactly)", got, want)
	}

	var sink wireRequest

	oversize := binary.BigEndian.AppendUint32(nil, wireMaxFrame+1)
	if readErr := readWireFrame(bytes.NewReader(oversize), &sink); !errors.Is(readErr, ErrWireFrameTooLarge) {
		t.Fatalf("oversized announcement error = %v, want %v", readErr, ErrWireFrameTooLarge)
	}

	truncated := binary.BigEndian.AppendUint32(nil, 10)
	truncated = append(truncated, "abc"...)

	if readErr := readWireFrame(bytes.NewReader(truncated), &sink); !errors.Is(readErr, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated payload error = %v, want %v", readErr, io.ErrUnexpectedEOF)
	}

	garbage := binary.BigEndian.AppendUint32(nil, 3)
	garbage = append(garbage, "abc"...)

	if readErr := readWireFrame(bytes.NewReader(garbage), &sink); !errors.Is(readErr, ErrWireProtocol) {
		t.Fatalf("non-JSON payload error = %v, want %v", readErr, ErrWireProtocol)
	}

	if readErr := readWireFrame(bytes.NewReader(nil), &sink); !errors.Is(readErr, io.EOF) {
		t.Fatalf("empty stream error = %v, want %v", readErr, io.EOF)
	}

	huge := wireRequest{Resource: strings.Repeat("x", wireMaxFrame)}
	if writeErr := writeWireFrame(&buf, &huge); !errors.Is(writeErr, ErrWireFrameTooLarge) {
		t.Fatalf("oversized write error = %v, want %v", writeErr, ErrWireFrameTooLarge)
	}
}

// TestWireRequestsBeyondTheInFlightLimitAreRefusedAndTheConnectionSurvives
// fills one connection with blocked WaitApplied requests: exactly the ones
// over wireMaxInFlight are refused with ErrWireBusy, the admitted ones go on
// waiting, and cancelling them leaves the connection, the hold and the freed
// slots intact.
func TestWireRequestsBeyondTheInFlightLimitAreRefusedAndTheConnectionSurvives(t *testing.T) {
	ctx := hostTestContext(t)
	rig := newWireRig(t, func(cfg *HostConfig) {
		// Few reconcile passes, so the blocked waiters are not woken constantly.
		cfg.RetryInterval = time.Hour
		cfg.ResyncInterval = time.Hour
	})

	// An effect that never applies keeps every WaitApplied blocked.
	registerTestResource(t, rig.host, "broken", &fakeEffect{applyFailures: 1 << 30})

	client := rig.dial(t)

	acquired, err := client.Acquire(ctx, "broken")
	if err != nil {
		t.Fatalf("Acquire(): %v", err)
	}

	const refused = 44

	const total = wireMaxInFlight + refused

	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make(chan error, total)

	for range total {
		go func() { results <- client.WaitApplied(waitCtx, "broken", acquired.Mark) }()
	}

	// Exactly the requests over the limit come back at once, all refused.
	for range refused {
		select {
		case waitErr := <-results:
			if !errors.Is(waitErr, ErrWireBusy) {
				t.Fatalf("an early result was %v, want %v", waitErr, ErrWireBusy)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for the over-limit requests to be refused")
		}
	}

	// The admitted ones are still waiting.
	select {
	case early := <-results:
		t.Fatalf("an admitted request ended early with %v", early)
	case <-time.After(100 * time.Millisecond):
	}

	cancel()

	for range wireMaxInFlight {
		select {
		case waitErr := <-results:
			if !errors.Is(waitErr, context.Canceled) {
				t.Fatalf("a cancelled request returned %v, want %v", waitErr, context.Canceled)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for the cancelled requests to return")
		}
	}

	// The host frees a slot only after answering, so allow for a moment of
	// ErrWireBusy before the connection is fully usable again.
	eventually(t, "the in-flight slots to be freed", func() bool {
		keepErr := client.Keepalive(ctx)
		if keepErr != nil && !errors.Is(keepErr, ErrWireBusy) {
			t.Fatalf("Keepalive() after the cancellations: %v", keepErr)
		}

		return keepErr == nil
	})

	// The cancellations ended requests, not the hold.
	if last, releaseErr := client.Release(ctx, "broken"); releaseErr != nil || !last {
		t.Fatalf("Release() = (%v,%v), want last=true: the hold must have survived", last, releaseErr)
	}
}

func TestWireMaxConnsOptionDefaultsAndUnlimited(t *testing.T) {
	cases := []struct {
		name string
		opts []WireOption
		want int
	}{
		{name: "default", want: wireDefaultMaxConns},
		{name: "zero keeps the default", opts: []WireOption{WithMaxConns(0)}, want: wireDefaultMaxConns},
		{name: "explicit", opts: []WireOption{WithMaxConns(3)}, want: 3},
		{name: "negative is unlimited", opts: []WireOption{WithMaxConns(-1)}, want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NewWireServer(nil, tc.opts...).maxConns; got != tc.want {
				t.Fatalf("maxConns = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestWireMaxConnsRefusesTheConnectionOverTheLimitWithoutOpeningASession(t *testing.T) {
	ctx := hostTestContext(t)
	rig := newWireRig(t, nil, WithMaxConns(2))
	registerTestResource(t, rig.host, "example-resource", nil)

	first := rig.dial(t)
	second := rig.dial(t)

	_, refusedErr := NewWireClient(ctx, serveInMemory(t, rig.server))
	if !errors.Is(refusedErr, ErrWireTooManyConns) {
		t.Fatalf("NewWireClient() over the limit error = %v, want %v", refusedErr, ErrWireTooManyConns)
	}

	var remote *RemoteError
	if !errors.As(refusedErr, &remote) || remote.Code != "too_many_conns" {
		t.Fatalf("NewWireClient() over the limit error = %#v, want a *RemoteError with code too_many_conns", refusedErr)
	}

	sessions, sessionsErr := rig.host.Registries().Leases.Sessions(rig.host.Graph())
	if sessionsErr != nil || len(sessions) != 2 {
		t.Fatalf("Sessions() = (%v,%v), want exactly the 2 admitted connections' sessions", sessions, sessionsErr)
	}

	// The admitted clients are unaffected.
	for i, client := range []*WireClient{first, second} {
		if acquireErr := AcquireAndWait(ctx, client, "example-resource"); acquireErr != nil {
			t.Fatalf("admitted client %d: AcquireAndWait(): %v", i, acquireErr)
		}
	}
}

func TestWireMaxConnsFreesTheSlotWhenAConnectionEnds(t *testing.T) {
	ctx := hostTestContext(t)
	rig := newWireRig(t, nil, WithMaxConns(1))

	first := rig.dial(t)

	if _, refusedErr := NewWireClient(ctx, serveInMemory(t, rig.server)); !errors.Is(refusedErr, ErrWireTooManyConns) {
		t.Fatalf("NewWireClient() over the limit error = %v, want %v", refusedErr, ErrWireTooManyConns)
	}

	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("Close(): %v", closeErr)
	}

	// The slot is freed just after the host has released the session.
	var second *WireClient

	eventually(t, "the slot to be freed", func() bool {
		client, dialErr := NewWireClient(ctx, serveInMemory(t, rig.server))
		if dialErr != nil {
			if !errors.Is(dialErr, ErrWireTooManyConns) {
				t.Fatalf("NewWireClient() error = %v, want nil or %v", dialErr, ErrWireTooManyConns)
			}

			return false
		}

		second = client

		return true
	})

	t.Cleanup(func() {
		if closeErr := second.Close(); closeErr != nil {
			t.Errorf("second Close(): %v", closeErr)
		}
	})
}

// newAuthRig serves a host with two registered resources to peers that are
// all identified as principal, under authorizer.
func newAuthRig(t *testing.T, principal Principal, authorizer Authorizer) *wireRig {
	t.Helper()

	identify := func(net.Conn) (Principal, error) { return principal, nil }
	rig := newWireRig(t, nil, WithPeerIdentifier(identify), WithAuthorizer(authorizer))

	registerTestResource(t, rig.host, "allowed", nil)
	registerTestResource(t, rig.host, "denied", nil)

	return rig
}

func TestWireAuthorizerDecidesPerResource(t *testing.T) {
	ctx := hostTestContext(t)
	policy := NewResourcePolicy(map[string][]Principal{
		"allowed": {"S-1-5-test-alice"},
		"denied":  {"S-1-5-test-bob"},
	})
	rig := newAuthRig(t, "S-1-5-test-alice", policy)
	client := rig.dial(t)

	if allowedErr := AcquireAndWait(ctx, client, "allowed"); allowedErr != nil {
		t.Fatalf("AcquireAndWait(allowed): %v", allowedErr)
	}

	_, acquireErr := client.Acquire(ctx, "denied")
	if !errors.Is(acquireErr, ErrNotAuthorized) {
		t.Fatalf("Acquire(denied) error = %v, want %v", acquireErr, ErrNotAuthorized)
	}

	var remote *RemoteError
	if !errors.As(acquireErr, &remote) || remote.Code != "not_authorized" {
		t.Fatalf("Acquire(denied) error = %#v, want a *RemoteError with code not_authorized", acquireErr)
	}

	if _, releaseErr := client.Release(ctx, "denied"); !errors.Is(releaseErr, ErrNotAuthorized) {
		t.Fatalf("Release(denied) error = %v, want %v", releaseErr, ErrNotAuthorized)
	}

	if waitErr := client.WaitApplied(ctx, "denied", 0); !errors.Is(waitErr, ErrNotAuthorized) {
		t.Fatalf("WaitApplied(denied) error = %v, want %v", waitErr, ErrNotAuthorized)
	}

	// A name that is not a registered resource is refused the same way, not
	// reported as unknown, so nothing about what exists is revealed.
	if _, unknownErr := client.Acquire(ctx, "no-such-resource"); !errors.Is(unknownErr, ErrNotAuthorized) {
		t.Fatalf("Acquire(no-such-resource) error = %v, want %v", unknownErr, ErrNotAuthorized)
	}

	node, found := rig.host.names.Lookup(resourcePrefix + "denied")
	if !found {
		t.Fatal("the resource name is not bound")
	}

	held, heldErr := rig.host.Registries().Leases.Held(rig.host.Graph(), node)
	if heldErr != nil || held {
		t.Fatalf("Held(denied) = (%v,%v), want (false,nil): a refused request must change nothing", held, heldErr)
	}

	// Operations that name no resource are not subject to it.
	if keepErr := client.Keepalive(ctx); keepErr != nil {
		t.Fatalf("Keepalive(): %v", keepErr)
	}
}

// recordingAuthorizer denies everything and remembers what it was asked.
type recordingAuthorizer struct {
	mu    sync.Mutex
	calls []recordedAuthorization
}

type recordedAuthorization struct {
	principal Principal
	resource  string
}

func (r *recordingAuthorizer) Authorize(principal Principal, resource string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.calls = append(r.calls, recordedAuthorization{principal: principal, resource: resource})

	return ErrNotAuthorized
}

func (r *recordingAuthorizer) recorded() []recordedAuthorization {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]recordedAuthorization(nil), r.calls...)
}

func TestWireAuthorizerSeesAnUnknownPrincipalWhenThePeerCannotBeIdentified(t *testing.T) {
	errIdentify := errors.New("no identity available")

	cases := []struct {
		name string
		opts []WireOption
	}{
		{name: "no identifier"},
		{
			name: "identifier fails",
			opts: []WireOption{WithPeerIdentifier(func(net.Conn) (Principal, error) { return "S-1-5-ignored", errIdentify })},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := hostTestContext(t)
			recorder := &recordingAuthorizer{}
			opts := append([]WireOption{WithAuthorizer(recorder)}, tc.opts...)

			rig := newWireRig(t, nil, opts...)
			registerTestResource(t, rig.host, "example-resource", nil)

			client := rig.dial(t)

			if _, acquireErr := client.Acquire(ctx, "example-resource"); !errors.Is(acquireErr, ErrNotAuthorized) {
				t.Fatalf("Acquire() error = %v, want %v", acquireErr, ErrNotAuthorized)
			}

			want := []recordedAuthorization{{principal: "", resource: "example-resource"}}
			if got := recorder.recorded(); !reflect.DeepEqual(got, want) {
				t.Fatalf("the authorizer was asked %+v, want %+v (an unidentified peer is the empty principal)", got, want)
			}
		})
	}
}

func TestWireAuthorizerThatFailsDenies(t *testing.T) {
	ctx := hostTestContext(t)
	authorizer := AuthorizerFunc(func(Principal, string) error { return errors.New("policy store unavailable") })
	rig := newAuthRig(t, "S-1-5-test-alice", authorizer)
	client := rig.dial(t)

	_, err := client.Acquire(ctx, "allowed")
	if !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("Acquire() with a failing authorizer error = %v, want %v", err, ErrNotAuthorized)
	}

	if !strings.Contains(err.Error(), "policy store unavailable") {
		t.Fatalf("Acquire() error = %q, want the authorizer's own failure in it", err)
	}
}

func TestWireWithoutAnAuthorizerAllowsEveryResource(t *testing.T) {
	ctx := hostTestContext(t)
	rig := newWireRig(t, nil)
	registerTestResource(t, rig.host, "example-resource", nil)

	if acquireErr := AcquireAndWait(ctx, rig.dial(t), "example-resource"); acquireErr != nil {
		t.Fatalf("AcquireAndWait() without an authorizer: %v", acquireErr)
	}
}

func TestResourcePolicyAuthorize(t *testing.T) {
	rules := map[string][]Principal{
		"listed": {"alice", "bob"},
		"open":   {AnyPrincipal},
		"empty":  {},
	}
	policy := NewResourcePolicy(rules)

	// The policy keeps its own copy of the rules.
	rules["listed"] = []Principal{"mallory"}

	cases := []struct {
		name      string
		principal Principal
		resource  string
		allowed   bool
	}{
		{name: "listed principal", principal: "alice", resource: "listed", allowed: true},
		{name: "second listed principal", principal: "bob", resource: "listed", allowed: true},
		{name: "other principal", principal: "carol", resource: "listed"},
		{name: "unknown principal", principal: "", resource: "listed"},
		{name: "open to everyone", principal: "carol", resource: "open", allowed: true},
		{name: "open to unknown", principal: "", resource: "open", allowed: true},
		{name: "empty list", principal: "alice", resource: "empty"},
		{name: "no entry", principal: "alice", resource: "missing"},
		{name: "rule changed after construction", principal: "mallory", resource: "listed"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := policy.Authorize(tc.principal, tc.resource)

			switch {
			case tc.allowed && err != nil:
				t.Fatalf("Authorize(%q, %q) = %v, want nil", tc.principal, tc.resource, err)
			case !tc.allowed && !errors.Is(err, ErrNotAuthorized):
				t.Fatalf("Authorize(%q, %q) = %v, want %v", tc.principal, tc.resource, err, ErrNotAuthorized)
			}
		})
	}
}
