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
	"errors"
	"fmt"
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

func TestPipePeerPrincipalIsTheClientUser(t *testing.T) {
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

	clientConn, dialErr := winio.DialPipeContext(ctx, path)
	if dialErr != nil {
		t.Fatalf("DialPipeContext(): %v", dialErr)
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

	principal, principalErr := PipePeerPrincipal(got.conn)
	if principalErr != nil {
		t.Fatalf("PipePeerPrincipal(): %v", principalErr)
	}

	if want := currentUserPrincipal(t); principal != want {
		t.Fatalf("PipePeerPrincipal() = %q, want the current user %q", principal, want)
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
