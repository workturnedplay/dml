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
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCreatePrivateFileCreatesAWritableFileAndNeverOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret-key.pem")

	file, err := CreatePrivateFile(path)
	if err != nil {
		t.Fatalf("CreatePrivateFile(): %v", err)
	}

	if _, writeErr := file.WriteString("first"); writeErr != nil {
		t.Fatalf("writing: %v", writeErr)
	}

	if closeErr := file.Close(); closeErr != nil {
		t.Fatalf("Close(): %v", closeErr)
	}

	if again, againErr := CreatePrivateFile(path); !errors.Is(againErr, os.ErrExist) {
		if againErr == nil {
			closeQuietly(again)
		}

		t.Fatalf("second CreatePrivateFile() error = %v, want %v", againErr, os.ErrExist)
	}

	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("reading the file back: %v", readErr)
	}

	if string(got) != "first" {
		t.Fatalf("the file holds %q, want %q (a refused create must not touch it)", got, "first")
	}
}