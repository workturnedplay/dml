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

//go:build portmasterFirewalled

package dml

import (
	"context"
	"errors"
	"net"
	"testing"
)

// Tests in this file open real TCP sockets on 127.0.0.1, which a
// block-by-default firewall (Portmaster) refuses unless the test binary has an
// exception. They are compiled only with -tags portmasterFirewalled (see
// test.bat), into one stable binary, so a single permanent rule covers them.
// Everything else in the wire tests runs over net.Pipe and needs nothing.

// listenLoopback listens on a free loopback TCP port.
func listenLoopback(t *testing.T) net.Listener {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen(): %v", err)
	}

	return listener
}

// serveListener serves server on listener in the background. When the test
// ends it closes the server and checks that Serve ended cleanly. Call it after
// the host is opened, so its cleanup runs before the host's.
func serveListener(t *testing.T, server *WireServer, listener net.Listener) {
	t.Helper()

	served := make(chan error, 1)

	go func() { served <- server.Serve(listener) }()

	t.Cleanup(func() {
		if closeErr := server.Close(); closeErr != nil {
			t.Errorf("server Close(): %v", closeErr)
		}

		if serveErr := <-served; serveErr != nil && !errors.Is(serveErr, ErrWireServerClosed) {
			t.Errorf("Serve(): %v", serveErr)
		}
	})
}

// dialLoopback opens a plain TCP connection to address. The caller hands it
// to a WireClient, which owns and closes it.
func dialLoopback(ctx context.Context, t *testing.T, address string) net.Conn {
	t.Helper()

	var dialer net.Dialer

	nc, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		t.Fatalf("DialContext(): %v", err)
	}

	return nc
}

// TestFWNeededWireRefusesPlainTCPByDefault: a plain TCP client gets the
// refusal and no session is opened.
func TestFWNeededWireRefusesPlainTCPByDefault(t *testing.T) {
	ctx := hostTestContext(t)
	h := openTestHost(t, boltTestPath(t), nil)
	registerTestResource(t, h, "example-resource", nil)

	listener := listenLoopback(t)
	serveListener(t, NewWireServer(h), listener)

	_, err := NewWireClient(ctx, dialLoopback(ctx, t, listener.Addr().String()))
	if !errors.Is(err, ErrWirePlainTCP) {
		t.Fatalf("NewWireClient() over plain TCP error = %v, want %v", err, ErrWirePlainTCP)
	}

	var remote *RemoteError
	if !errors.As(err, &remote) || remote.Code != "plain_tcp_refused" {
		t.Fatalf("NewWireClient() over plain TCP error = %#v, want a *RemoteError with code plain_tcp_refused", err)
	}

	sessions, sessionsErr := h.Registries().Leases.Sessions(h.Graph())
	if sessionsErr != nil || len(sessions) != 0 {
		t.Fatalf("Sessions() = (%v,%v), want none for a refused connection", sessions, sessionsErr)
	}
}

// TestFWNeededWireOverLoopbackTCP runs the whole client/host flow over a real
// loopback TCP connection, with the opt-out that allows plain TCP.
func TestFWNeededWireOverLoopbackTCP(t *testing.T) {
	ctx := hostTestContext(t)
	h := openTestHost(t, boltTestPath(t), nil)
	fw := &fakeEffect{}
	registerTestResource(t, h, "example-resource", fw)

	listener := listenLoopback(t)
	serveListener(t, NewWireServer(h, WithInsecurePlainTCP()), listener)

	client := startWireClient(t, dialLoopback(ctx, t, listener.Addr().String()))

	if acquireErr := AcquireAndWait(ctx, client, "example-resource"); acquireErr != nil {
		t.Fatalf("AcquireAndWait(): %v", acquireErr)
	}

	requireEffectCounts(t, fw, 1, 0, true)

	if last, releaseErr := client.Release(ctx, "example-resource"); releaseErr != nil || !last {
		t.Fatalf("Release() = (%v,%v), want last=true", last, releaseErr)
	}

	eventually(t, "the effect to be removed", func() bool {
		_, _, present := fw.counts()
		return !present
	})
}
