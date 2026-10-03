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
	"unsafe"

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
	user, userErr := windows.GetCurrentProcessToken().GetTokenUser()
	if userErr != nil {
		return "", fmt.Errorf("pipe: reading the current user's SID: %w", userErr)
	}

	return fmt.Sprintf("D:P(D;;GA;;;NU)(A;;GA;;;%s)(A;;GA;;;SY)", user.User.Sid.String()), nil
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

// DialPipe connects to the named pipe path and performs the wire handshake.
// ctx bounds the dial and the handshake only.
func DialPipe(ctx context.Context, path string) (*WireClient, error) {
	nc, dialErr := winio.DialPipeContext(ctx, path)
	if dialErr != nil {
		return nil, fmt.Errorf("pipe: dialing %s: %w", path, dialErr)
	}

	return NewWireClient(ctx, nc)
}

// procGetNamedPipeClientProcessID is called through a lazy proc so that it
// does not depend on which functions the vendored x/sys/windows exposes.
var procGetNamedPipeClientProcessID = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetNamedPipeClientProcessId")

// pipeHandleSource is implemented by the connections ListenPipe accepts, if
// go-winio exposes their handle.
type pipeHandleSource interface {
	Fd() uintptr
}

// pipeClientProcessID returns the process ID of the client end of the pipe
// whose server-side handle is handle.
func pipeClientProcessID(handle uintptr) (uint32, error) {
	var pid uint32

	result, _, callErr := procGetNamedPipeClientProcessID.Call(handle, uintptr(unsafe.Pointer(&pid))) //nolint:gosec // the call needs the address of pid
	if result == 0 {
		return 0, fmt.Errorf("pipe: GetNamedPipeClientProcessId: %w", callErr)
	}

	return pid, nil
}

// processUserSID returns the SID of the user process pid runs as.
func processUserSID(pid uint32) (string, error) {
	process, openErr := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if openErr != nil {
		return "", fmt.Errorf("pipe: opening client process %d: %w", pid, openErr)
	}

	defer func() {
		if closeErr := windows.CloseHandle(process); closeErr != nil {
			_ = closeErr
		}
	}()

	var token windows.Token

	if tokenErr := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); tokenErr != nil {
		return "", fmt.Errorf("pipe: opening the token of client process %d: %w", pid, tokenErr)
	}

	defer closeQuietly(token)

	user, userErr := token.GetTokenUser()
	if userErr != nil {
		return "", fmt.Errorf("pipe: reading the user of client process %d: %w", pid, userErr)
	}

	return user.User.Sid.String(), nil
}

// PipePeerPrincipal is a PeerIdentifier for connections accepted from
// ListenPipe: the Principal is the SID of the user the client process runs
// as. A peer it cannot identify (the connection does not expose its handle,
// or the client process cannot be opened, as for a process of another
// account) is an error, which the server turns into the empty Principal.
//
// With the default pipe security only the current user and SYSTEM can
// connect, so this tells those two apart. It becomes useful when ListenPipe
// is given a wider security descriptor.
func PipePeerPrincipal(nc net.Conn) (Principal, error) {
	source, ok := nc.(pipeHandleSource)
	if !ok {
		return "", fmt.Errorf("%w: %T does not expose its pipe handle", ErrPeerUnidentified, nc)
	}

	pid, pidErr := pipeClientProcessID(source.Fd())
	if pidErr != nil {
		return "", fmt.Errorf("%w: %w", ErrPeerUnidentified, pidErr)
	}

	sid, sidErr := processUserSID(pid)
	if sidErr != nil {
		return "", fmt.Errorf("%w: %w", ErrPeerUnidentified, sidErr)
	}

	return Principal(sid), nil
}
