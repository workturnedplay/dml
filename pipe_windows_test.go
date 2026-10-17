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

//go:build windows

package dml

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	winio "github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func TestDefaultPipeSecurityNamesTheCurrentUserAndDeniesNetworkLogons(t *testing.T) {
	sddl, err := DefaultPipeSecurity()
	if err != nil {
		t.Fatalf("DefaultPipeSecurity(): %v", err)
	}

	if !strings.HasPrefix(sddl, "D:P(D;;GA;;;NU)(A;;GA;;;S-1-") || !strings.HasSuffix(sddl, ")(A;;GA;;;SY)") {
		t.Fatalf("DefaultPipeSecurity() = %q, want a protected DACL denying NU, then allowing the user's SID and SY", sddl)
	}
}

// servePipe serves server on a fresh pipe and returns its path. The server is
// closed when the test ends.
func servePipe(t *testing.T, server *WireServer) string {
	t.Helper()

	path := PipePath(fmt.Sprintf("dml-test-%d", time.Now().UnixNano()))

	listener, listenErr := ListenPipe(path, "")
	if listenErr != nil {
		t.Fatalf("ListenPipe(): %v", listenErr)
	}

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

	return path
}

// currentUserPrincipal is the SID of the user running the test.
func currentUserPrincipal(t *testing.T) Principal {
	t.Helper()

	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatalf("GetTokenUser(): %v", err)
	}

	return Principal(user.User.Sid.String())
}

func TestPipeEndToEnd(t *testing.T) {
	ctx := hostTestContext(t)
	h := openTestHost(t, boltTestPath(t), nil)
	fw := &fakeEffect{}
	registerTestResource(t, h, "example-resource", fw)

	path := servePipe(t, NewWireServer(h))

	client, err := DialPipe(ctx, path)
	if err != nil {
		t.Fatalf("DialPipe(): %v", err)
	}

	t.Cleanup(func() {
		if closeErr := client.Close(); closeErr != nil {
			t.Errorf("client Close(): %v", closeErr)
		}
	})

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

// pipeDialer connects a client to the pipe at path.
type pipeDialer func(ctx context.Context, path string) (net.Conn, error)

// dialPipeAnonymously connects like winio.DialPipeContext, which lets the
// server impersonate the client only at the Anonymous level: the server
// cannot learn who it is.
func dialPipeAnonymously(ctx context.Context, path string) (net.Conn, error) {
	nc, err := winio.DialPipeContext(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("dialing %s anonymously: %w", path, err)
	}

	return nc, nil
}

// acceptPipePeer connects a client made by dial to a fresh pipe, has the
// client send one byte and the server read it (Windows impersonates the last
// message read, so identifying a peer needs one), and returns the server's
// side of the connection.
func acceptPipePeer(t *testing.T, dial pipeDialer) net.Conn {
	t.Helper()

	ctx := hostTestContext(t)
	path := PipePath(fmt.Sprintf("dml-test-peer-%d", time.Now().UnixNano()))

	listener, listenErr := ListenPipe(path, "")
	if listenErr != nil {
		t.Fatalf("ListenPipe(): %v", listenErr)
	}

	t.Cleanup(func() { closeQuietly(listener) })

	type acceptResult struct {
		conn net.Conn
		err  error
	}

	accepted := make(chan acceptResult, 1)

	go func() {
		nc, acceptErr := listener.Accept()
		accepted <- acceptResult{conn: nc, err: acceptErr}
	}()

	clientConn, dialErr := dial(ctx, path)
	if dialErr != nil {
		t.Fatalf("dialing the pipe: %v", dialErr)
	}

	t.Cleanup(func() { closeQuietly(clientConn) })

	var got acceptResult

	select {
	case got = <-accepted:
	case <-time.After(10 * time.Second):
		t.Fatal("the pipe did not accept the connection")
	}

	if got.err != nil {
		t.Fatalf("Accept(): %v", got.err)
	}

	t.Cleanup(func() { closeQuietly(got.conn) })

	if _, writeErr := clientConn.Write([]byte{1}); writeErr != nil {
		t.Fatalf("the client writing a byte: %v", writeErr)
	}

	if deadlineErr := got.conn.SetReadDeadline(time.Now().Add(10 * time.Second)); deadlineErr != nil {
		t.Fatalf("SetReadDeadline(): %v", deadlineErr)
	}

	var first [1]byte

	if _, readErr := io.ReadFull(got.conn, first[:]); readErr != nil {
		t.Fatalf("the server reading the client's byte: %v", readErr)
	}

	return got.conn
}

func TestPipePeerPrincipalIsTheClientUser(t *testing.T) {
	peer := acceptPipePeer(t, dialPipeConn)
	want := currentUserPrincipal(t)

	// Every identification impersonates on a thread of its own and reverts,
	// so any number of them must keep working.
	for i := range 20 {
		principal, err := PipePeerPrincipal(peer)
		if err != nil {
			t.Fatalf("PipePeerPrincipal() call %d: %v", i, err)
		}

		if principal != want {
			t.Fatalf("PipePeerPrincipal() call %d = %q, want the current user %q", i, principal, want)
		}
	}
}

// TestPipePeerPrincipalRefusesAnAnonymousClient: a client that lets the server
// see no identity is unidentified (the safe outcome: the empty Principal),
// and failing to identify it leaves nothing behind that breaks the next one.
func TestPipePeerPrincipalRefusesAnAnonymousClient(t *testing.T) {
	anonymous := acceptPipePeer(t, dialPipeAnonymously)

	for i := range 3 {
		if _, err := PipePeerPrincipal(anonymous); !errors.Is(err, ErrPeerUnidentified) {
			t.Fatalf("PipePeerPrincipal(anonymous) call %d error = %v, want %v", i, err, ErrPeerUnidentified)
		}
	}

	identified := acceptPipePeer(t, dialPipeConn)

	principal, err := PipePeerPrincipal(identified)
	if err != nil {
		t.Fatalf("PipePeerPrincipal() after refusing an anonymous client: %v", err)
	}

	if want := currentUserPrincipal(t); principal != want {
		t.Fatalf("PipePeerPrincipal() = %q, want the current user %q", principal, want)
	}
}

// TestPipeAuthorizationDeniesAnAnonymousClient: through a server, a client
// that dialed anonymously is the empty Principal and is denied, even though
// the policy lists the very user running it.
func TestPipeAuthorizationDeniesAnAnonymousClient(t *testing.T) {
	ctx := hostTestContext(t)
	h := openTestHost(t, boltTestPath(t), nil)
	registerTestResource(t, h, "mine", nil)

	policy := NewResourcePolicy(map[string][]Principal{
		"mine": {currentUserPrincipal(t)},
	})

	path := servePipe(t, NewWireServer(h, WithPeerIdentifier(PipePeerPrincipal), WithAuthorizer(policy)))

	nc, dialErr := dialPipeAnonymously(ctx, path)
	if dialErr != nil {
		t.Fatalf("dialPipeAnonymously(): %v", dialErr)
	}

	client, clientErr := NewWireClient(ctx, nc)
	if clientErr != nil {
		t.Fatalf("NewWireClient(): %v", clientErr)
	}

	t.Cleanup(func() {
		if closeErr := client.Close(); closeErr != nil {
			t.Errorf("client Close(): %v", closeErr)
		}
	})

	if _, acquireErr := client.Acquire(ctx, "mine"); !errors.Is(acquireErr, ErrNotAuthorized) {
		t.Fatalf("Acquire(mine) by an anonymous client error = %v, want %v", acquireErr, ErrNotAuthorized)
	}
}

func TestPipePeerPrincipalRefusesAConnectionThatHidesItsHandle(t *testing.T) {
	clientSide, serverSide := net.Pipe()

	t.Cleanup(func() {
		closeQuietly(clientSide)
		closeQuietly(serverSide)
	})

	if _, err := PipePeerPrincipal(serverSide); !errors.Is(err, ErrPeerUnidentified) {
		t.Fatalf("PipePeerPrincipal(net.Pipe) error = %v, want %v", err, ErrPeerUnidentified)
	}
}

func TestPipeAuthorizationUsesTheClientSID(t *testing.T) {
	ctx := hostTestContext(t)
	h := openTestHost(t, boltTestPath(t), nil)
	registerTestResource(t, h, "mine", nil)
	registerTestResource(t, h, "theirs", nil)

	policy := NewResourcePolicy(map[string][]Principal{
		"mine":   {currentUserPrincipal(t)},
		"theirs": {"S-1-5-32-544"}, // BUILTIN\Administrators, not this user
	})

	path := servePipe(t, NewWireServer(h, WithPeerIdentifier(PipePeerPrincipal), WithAuthorizer(policy)))

	client, err := DialPipe(ctx, path)
	if err != nil {
		t.Fatalf("DialPipe(): %v", err)
	}

	t.Cleanup(func() {
		if closeErr := client.Close(); closeErr != nil {
			t.Errorf("client Close(): %v", closeErr)
		}
	})

	if acquireErr := AcquireAndWait(ctx, client, "mine"); acquireErr != nil {
		t.Fatalf("AcquireAndWait(mine): %v", acquireErr)
	}

	if _, acquireErr := client.Acquire(ctx, "theirs"); !errors.Is(acquireErr, ErrNotAuthorized) {
		t.Fatalf("Acquire(theirs) error = %v, want %v", acquireErr, ErrNotAuthorized)
	}
}
