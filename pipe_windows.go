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