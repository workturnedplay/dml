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
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// privateFileSDDLFormat is the security descriptor of a private file: a
// protected DACL (no inherited entries) that gives FILE_ALL_ACCESS to the
// user given as a SID string, and to SYSTEM, and to nobody else. The
// verbs are the same ones DefaultPipeSecurity uses for the pipe.
const privateFileSDDLFormat = "D:P(A;;FA;;;%s)(A;;FA;;;SY)"

// currentUserSID returns the SID string of the user this process runs as.
// It is shared by DefaultPipeSecurity and createPrivateFile.
func currentUserSID() (string, error) {
	user, userErr := windows.GetCurrentProcessToken().GetTokenUser()
	if userErr != nil {
		return "", fmt.Errorf("reading the current user's SID: %w", userErr)
	}

	return user.User.Sid.String(), nil
}

// createPrivateFile is CreatePrivateFile for Windows. The file is created
// by CreateFile(CREATE_NEW) with the protected DACL already attached, so it
// is never readable by anyone else, not even for an instant (setting the
// ACL after creating the file would leave a window).
func createPrivateFile(path string) (*os.File, error) {
	sid, sidErr := currentUserSID()
	if sidErr != nil {
		return nil, sidErr
	}

	descriptor, descriptorErr := windows.SecurityDescriptorFromString(fmt.Sprintf(privateFileSDDLFormat, sid))
	if descriptorErr != nil {
		return nil, fmt.Errorf("building the security descriptor: %w", descriptorErr)
	}

	name, nameErr := windows.UTF16PtrFromString(path)
	if nameErr != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: nameErr}
	}

	attributes := &windows.SecurityAttributes{SecurityDescriptor: descriptor}
	attributes.Length = uint32(unsafe.Sizeof(*attributes)) //nolint:gosec // the size of a small fixed struct

	handle, createErr := windows.CreateFile(
		name,
		windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ,
		attributes,
		windows.CREATE_NEW,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if createErr != nil {
		// A PathError, like os.OpenFile's, so errors.Is(err, os.ErrExist)
		// and the message behave the same on every platform.
		return nil, &os.PathError{Op: "open", Path: path, Err: createErr}
	}

	return os.NewFile(uintptr(handle), path), nil
}