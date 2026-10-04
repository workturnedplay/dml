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
	"crypto/tls"
	"errors"
	"net"
	"testing"
)

// addrConn is a net.Conn that reports a chosen remote address, so the checks
// that look at the address can be tested over net.Pipe without a socket.
type addrConn struct {
	net.Conn
	remote net.Addr
}

func (c addrConn) RemoteAddr() net.Addr { return c.remote }

var (
	testTCPAddr  = &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 49152}
	testUnixAddr = &net.UnixAddr{Name: "dml.sock", Net: "unix"}
)

func TestIsPlainTCPSeesOnlyUnencryptedTCP(t *testing.T) {
	clientPipe, serverPipe := net.Pipe()

	t.Cleanup(func() {
		closeQuietly(clientPipe)
		closeQuietly(serverPipe)
	})

	// No handshake happens on construction, so no certificates are needed.
	overTCP := tls.Server(addrConn{Conn: serverPipe, remote: testTCPAddr}, &tls.Config{MinVersion: tls.VersionTLS13})

	cases := []struct {
		name string
		conn net.Conn
		want bool
	}{
		{name: "in-memory pipe", conn: serverPipe, want: false},
		{name: "TCP address", conn: addrConn{Conn: serverPipe, remote: testTCPAddr}, want: true},
		{name: "unix socket address", conn: addrConn{Conn: serverPipe, remote: testUnixAddr}, want: false},
		{name: "TLS over a TCP address", conn: overTCP, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPlainTCP(tc.conn); got != tc.want {
				t.Fatalf("isPlainTCP() = %v, want %v", got, tc.want)
			}
		})
	}
}

// servePretendingTCP serves one in-memory connection on server whose server
// side reports a TCP remote address, and returns the client side. It is
// closed, and the handler awaited, when the test ends.
func servePretendingTCP(t *testing.T, server *WireServer) net.Conn {
	t.Helper()

	clientSide, serverSide := net.Pipe()
	done := make(chan struct{})

	go func() {
		defer close(done)
		server.ServeConn(addrConn{Conn: serverSide, remote: testTCPAddr})
	}()

	t.Cleanup(func() {
		closeQuietly(clientSide)
		<-done
	})

	return clientSide
}

func TestWireServerRefusesPlainTCPByDefault(t *testing.T) {
	ctx := hostTestContext(t)
	rig := newWireRig(t, nil)

	_, err := NewWireClient(ctx, servePretendingTCP(t, rig.server))
	if !errors.Is(err, ErrWirePlainTCP) {
		t.Fatalf("NewWireClient() over a TCP connection error = %v, want %v", err, ErrWirePlainTCP)
	}

	var remote *RemoteError
	if !errors.As(err, &remote) || remote.Code != "plain_tcp_refused" {
		t.Fatalf("NewWireClient() over a TCP connection error = %#v, want a *RemoteError with code plain_tcp_refused", err)
	}

	sessions, sessionsErr := rig.host.Registries().Leases.Sessions(rig.host.Graph())
	if sessionsErr != nil || len(sessions) != 0 {
		t.Fatalf("Sessions() = (%v,%v), want none for a refused connection", sessions, sessionsErr)
	}
}

func TestWireServerAcceptsPlainTCPWhenToldTo(t *testing.T) {
	ctx := hostTestContext(t)
	rig := newWireRig(t, nil, WithInsecurePlainTCP())
	registerTestResource(t, rig.host, "example-resource", nil)

	client := startWireClient(t, servePretendingTCP(t, rig.server))

	if err := AcquireAndWait(ctx, client, "example-resource"); err != nil {
		t.Fatalf("AcquireAndWait() with WithInsecurePlainTCP: %v", err)
	}
}