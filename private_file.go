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
	"fmt"
	"os"
)

// CreatePrivateFile creates the file path for writing and returns it. The
// file must not exist (an existing one is an error satisfying
// errors.Is(err, os.ErrExist)), so nothing is ever overwritten, and it is
// readable only by its owner from the moment it exists: mode 0600 on Unix
// systems and, on Windows, where mode bits mean nothing, a protected ACL
// granting access only to the current user and SYSTEM. It is meant for
// private keys. The caller closes the file.
func CreatePrivateFile(path string) (*os.File, error) {
	file, err := createPrivateFile(path)
	if err != nil {
		return nil, fmt.Errorf("private file: %w", err)
	}

	return file, nil
}