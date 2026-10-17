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
	"fmt"
	"net"
	"runtime"

	winio "github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// Windows named pipe transport for WireServer/WireClient (theorystate.md
// section 113). Access control is the pipe's DACL: it decides who may connect
// at all.

const (
	pipeNamespace = `\\.\pipe\`

	// pipeBufferSize is the in/out buffer size of the pipe (bytes).
	pipeBufferSize int32 = 64 * 1024
)

// PipePath returns the full path of the named pipe called name.
func PipePath(name string) string {
	return pipeNamespace + name
}

// DefaultPipeSecurity returns the SDDL ListenPipe uses when given none: a
// protected DACL that first denies network logons (so the pipe is not reachable
// over SMB, even as the same account), then allows only the current user and
// SYSTEM.
func DefaultPipeSecurity() (string, error) {
	sid, sidErr := currentUserSID()
	if sidErr != nil {
		return "", fmt.Errorf("pipe: %w", sidErr)
	}

	return fmt.Sprintf("D:P(D;;GA;;;NU)(A;;GA;;;%s)(A;;GA;;;SY)", sid), nil
}

// ListenPipe listens on the named pipe path (see PipePath) with the given SDDL
// as its security descriptor; an empty one means DefaultPipeSecurity. The pipe
// is in byte mode: the wire protocol does its own framing.
func ListenPipe(path, securityDescriptor string) (net.Listener, error) {
	if securityDescriptor == "" {
		defaultSD, sdErr := DefaultPipeSecurity()
		if sdErr != nil {
			return nil, sdErr
		}

		securityDescriptor = defaultSD
	}

	listener, listenErr := winio.ListenPipe(path, &winio.PipeConfig{
		SecurityDescriptor: securityDescriptor,
		MessageMode:        false,
		InputBufferSize:    pipeBufferSize,
		OutputBufferSize:   pipeBufferSize,
	})
	if listenErr != nil {
		return nil, fmt.Errorf("pipe: listening on %s: %w", path, listenErr)
	}

	return listener, nil
}

const (
	// pipeDialAccess is the access a client asks for on the pipe: the one
	// winio's own default dial asks for.
	pipeDialAccess = windows.GENERIC_READ | windows.GENERIC_WRITE

	// pipeImpersonationLevel is the level at which a client lets the server
	// impersonate it. Identification is the least that lets the server read
	// the client's token (PipePeerPrincipal); it does not let the server act
	// as the client. A client dialing anonymously (winio.DialPipeContext does)
	// cannot be identified at all.
	pipeImpersonationLevel = winio.PipeImpLevelIdentification
)

// dialPipeConn connects to the named pipe path at pipeImpersonationLevel,
// without any wire handshake.
func dialPipeConn(ctx context.Context, path string) (net.Conn, error) {
	nc, dialErr := winio.DialPipeAccessImpLevel(ctx, path, pipeDialAccess, pipeImpersonationLevel)
	if dialErr != nil {
		return nil, fmt.Errorf("pipe: dialing %s: %w", path, dialErr)
	}

	return nc, nil
}

// DialPipe connects to the named pipe path and performs the wire handshake.
// It lets the server read the identity of this process (impersonation level
// Identification, never more), which is what a server using
// PipePeerPrincipal needs to authorize the client. ctx bounds the dial and
// the handshake only.
func DialPipe(ctx context.Context, path string) (*WireClient, error) {
	nc, dialErr := dialPipeConn(ctx, path)
	if dialErr != nil {
		return nil, dialErr
	}

	return NewWireClient(ctx, nc)
}

// procImpersonateNamedPipeClient is called through a lazy proc: x/sys/windows
// has no wrapper for ImpersonateNamedPipeClient.
var procImpersonateNamedPipeClient = windows.NewLazySystemDLL("advapi32.dll").NewProc("ImpersonateNamedPipeClient")

// pipeHandleSource is implemented by the connections ListenPipe accepts, if
// go-winio exposes their handle.
type pipeHandleSource interface {
	Fd() uintptr
}

// impersonateNamedPipeClient makes the calling thread impersonate the client
// of the pipe whose server-side handle is handle (the security context of the
// last message read from it). On failure the thread is not impersonating.
func impersonateNamedPipeClient(handle uintptr) error {
	result, _, callErr := procImpersonateNamedPipeClient.Call(handle)
	if result == 0 {
		return fmt.Errorf("pipe: ImpersonateNamedPipeClient: %w", callErr)
	}

	return nil
}

// threadTokenUserSID returns the SID of the user of the calling thread's
// impersonation token. It is opened as the process (openAsSelf): at the
// Identification level the token cannot be used for access checks.
func threadTokenUserSID() (string, error) {
	var token windows.Token

	if tokenErr := windows.OpenThreadToken(windows.CurrentThread(), windows.TOKEN_QUERY, true, &token); tokenErr != nil {
		return "", fmt.Errorf("pipe: opening the impersonation token of the client: %w", tokenErr)
	}

	defer closeQuietly(token)

	user, userErr := token.GetTokenUser()
	if userErr != nil {
		return "", fmt.Errorf("pipe: reading the user of the impersonation token: %w", userErr)
	}

	return user.User.Sid.String(), nil
}

// impersonatedUserSID impersonates the client of the pipe on the calling
// thread, reads its user's SID and reverts. reverted reports whether the
// thread is back to the process's own identity; when it is false the thread
// must never be reused (see clientUserSID).
func impersonatedUserSID(handle uintptr) (sid string, reverted bool, err error) {
	if impersonateErr := impersonateNamedPipeClient(handle); impersonateErr != nil {
		return "", true, impersonateErr
	}

	userSID, readErr := threadTokenUserSID()

	if revertErr := windows.RevertToSelf(); revertErr != nil {
		return "", false, fmt.Errorf("pipe: reverting to self after impersonating a client: %w", revertErr)
	}

	return userSID, true, readErr
}

// clientUserSID returns the SID of the user at the client end of the pipe
// whose server-side handle is handle. Impersonation changes the identity of
// an OS thread, so it runs on a goroutine of its own, locked to its thread:
// the caller's goroutine never carries the client's identity. The thread is
// unlocked again only if the revert succeeded. A goroutine that ends while
// locked makes the Go runtime terminate its thread, so a thread that could not
// be reverted is never reused.
func clientUserSID(handle uintptr) (string, error) {
	type outcome struct {
		sid string
		err error
	}

	outcomes := make(chan outcome, 1)

	go func() {
		runtime.LockOSThread()

		sid, reverted, err := impersonatedUserSID(handle)
		if reverted {
			runtime.UnlockOSThread()
		}

		outcomes <- outcome{sid: sid, err: err}
	}()

	result := <-outcomes

	return result.sid, result.err
}

// PipePeerPrincipal is a PeerIdentifier for connections accepted from
// ListenPipe: the Principal is the SID of the user at the client end of the
// pipe, read by impersonating the client (ImpersonateNamedPipeClient) and
// reading the token of the impersonating thread. Unlike looking up the
// client's process ID, this cannot be fooled by the ID being reused, and it
// works for a client of any account, SYSTEM included, even for a host that
// is not elevated: impersonation at the Identification level is always
// allowed.
//
// It needs two things. The server must already have read at least one
// message from the connection (Windows impersonates "the last message
// read"; WireServer identifies a peer after its hello, so this holds there),
// and the client must have connected at the Identification level or higher
// (DialPipe does; a plain winio.DialPipeContext connects anonymously). A peer
// it cannot identify for any reason (the connection does not expose its
// handle, nothing was read yet, an anonymous client) is an error wrapping
// ErrPeerUnidentified, which the server turns into the empty Principal.
//
// With the default pipe security only the current user and SYSTEM can
// connect, so this tells those two apart. It becomes useful when ListenPipe
// is given a wider security descriptor.
func PipePeerPrincipal(nc net.Conn) (Principal, error) {
	source, ok := nc.(pipeHandleSource)
	if !ok {
		return "", fmt.Errorf("%w: %T does not expose its pipe handle", ErrPeerUnidentified, nc)
	}

	sid, sidErr := clientUserSID(source.Fd())
	if sidErr != nil {
		return "", fmt.Errorf("%w: %w", ErrPeerUnidentified, sidErr)
	}

	return Principal(sid), nil
}
