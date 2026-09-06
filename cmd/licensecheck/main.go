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

package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		return // No files passed
	}

	target := []byte("SPDX-License-Identifier:"+" "+ "Apache-2.0") // so it doesn't match this in this file!
	
	// Pre-allocate a single buffer for all reads to eliminate loop heap allocations.
	// 1024 bytes safely covers maximum expected copyright header lengths.
	buf := make([]byte, 1024)
	failed := false

	for _, path := range os.Args[1:] {
		if !checkFile(path, target, buf) {
			fmt.Fprintf(os.Stderr, "FATAL: Missing or incorrect license header in %s\n", path)
			failed = true
		}
	}

	// Fail-loud to abort the git commit hook
	if failed {
		os.Exit(1)
	}
}

func checkFile(path string, target, buf []byte) bool {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: Syscall failed to open %s: %v\n", path, err)
		return false
	}
	// Explicit cleanup via defer guarantees fd release regardless of read success
	defer f.Close() 

	n, err := io.ReadFull(f, buf)
	// io.ErrUnexpectedEOF and io.EOF are acceptable here if the file size < 1024 bytes.
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		fmt.Fprintf(os.Stderr, "ERROR: I/O read failure on %s: %v\n", path, err)
		return false
	}

	// Slice the buffer strictly to bytes read to avoid bounds panic or false 
	// positives from residual data in the reused buffer.
	return bytes.Contains(buf[:n], target)
}
