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
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestCreatePrivateFileGrantsOnlyTheCurrentUserAndSystem(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret-key.pem")

	file, err := CreatePrivateFile(path)
	if err != nil {
		t.Fatalf("CreatePrivateFile(): %v", err)
	}

	if closeErr := file.Close(); closeErr != nil {
		t.Fatalf("Close(): %v", closeErr)
	}

	descriptor, descriptorErr := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if descriptorErr != nil {
		t.Fatalf("GetNamedSecurityInfo(): %v", descriptorErr)
	}

	control, _, controlErr := descriptor.Control()
	if controlErr != nil {
		t.Fatalf("reading the descriptor's control flags: %v", controlErr)
	}

	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("the DACL is not protected (control flags %#x): the directory's ACL would be inherited", control)
	}

	sid, sidErr := currentUserSID()
	if sidErr != nil {
		t.Fatalf("currentUserSID(): %v", sidErr)
	}

	sddl := descriptor.String()

	for _, want := range []string{"(A;;FA;;;" + sid + ")", "(A;;FA;;;SY)"} {
		if !strings.Contains(sddl, want) {
			t.Fatalf("DACL = %q, want it to contain %q", sddl, want)
		}
	}

	if entries := strings.Count(sddl, "("); entries != 2 {
		t.Fatalf("DACL = %q has %d entries, want exactly the user and SYSTEM", sddl, entries)
	}
}