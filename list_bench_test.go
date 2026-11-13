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
	"path/filepath"
	"testing"
	"time"
)

// ---------------------------------------------------------------------
// Cost of the commit-time Checkers on a long list (theorystate.md section
// 105). ListRegistry's Checker runs validateStructure over the whole list
// on every commit that touches it, and Append reads every child of the list
// to find the tail, so both are expected to grow with the list. This
// measures how much, on the production stack (GraphActor over RootGraph
// over BoltGraph, every registry and Checker), before any local check is
// designed. Run without -race:
//
//	go test -run "^$" -bench ListCommitCost -benchtime 30x -timeout 60m
//
// Add -short to skip the largest list. Per size, the sub-benchmarks are:
//   - empty-commit: a commit that touches nothing relevant (the baseline);
//   - tail-scan: only Append's own tail lookup, read-only (no Checker runs);
//   - local-check: a commit that only touches the tail capsule, so the
//     local Checker validates that capsule and nothing else happens;
//   - checker-only: a commit that only touches the list node, which has no
//     touched member capsule, so the Checker falls back to validating the
//     whole list (the cost of a sweep, and of the old Checker on every
//     commit);
//   - append: the real operation, run last because it lengthens the list
//     (so the list is n+30 long at the end, which does not matter at 30x).
//
// append minus local-check is about the tail scan.

// listBenchSizes are the list lengths BenchmarkListCommitCost measures.
var listBenchSizes = []int{1_000, 10_000, 100_000}

const (
	// listBenchChunk is how many elements one transaction appends while a
	// benchmark list is built. Every commit validates the whole list so
	// far, so a larger chunk means fewer validations, and a smaller one a
	// smaller bolt transaction.
	listBenchChunk = 20_000

	// listBenchShortLimit is the largest list built under -short.
	listBenchShortLimit = 10_000
)

// openListBenchHost opens a Host over a fresh store and closes it when b
// ends.
func openListBenchHost(b *testing.B) *Host {
	b.Helper()

	h, err := OpenHost(HostConfig{
		Path:         filepath.Join(b.TempDir(), "list-bench.db"),
		KeepaliveTTL: -1,
		OnError:      func(string, error) {},
	})
	if err != nil {
		b.Fatalf("OpenHost(): %v", err)
	}

	b.Cleanup(func() {
		if closeErr := h.Close(); closeErr != nil {
			b.Errorf("Close(): %v", closeErr)
		}
	})

	return h
}

// buildBenchList makes a list of size elements with AppendMany, one chunk
// per transaction (appending one at a time would itself be quadratic).
func buildBenchList(b *testing.B, h *Host, lists OrderedList, size int) NodeID {
	b.Helper()

	list, err := lists.NewList(h.Graph())
	if err != nil {
		b.Fatalf("NewList(): %v", err)
	}

	for built := 0; built < size; {
		count := min(listBenchChunk, size-built)

		txErr := h.Graph().Transact(func(tx Tx) error {
			values := make([]NodeID, 0, count)

			for range count {
				value, createErr := createNodeTx(tx)
				if createErr != nil {
					return createErr
				}

				values = append(values, value)
			}

			_, appendErr := lists.AppendMany(tx, list, values)

			return wrapInterfaceErr(appendErr)
		})
		if txErr != nil {
			b.Fatalf("building the list (%d of %d built): %v", built, size, txErr)
		}

		built += count
	}

	return list
}

func BenchmarkListCommitCost(b *testing.B) {
	for _, size := range listBenchSizes {
		b.Run(fmt.Sprintf("n=%d", size), func(b *testing.B) {
			if testing.Short() && size > listBenchShortLimit {
				b.Skipf("building a list of %d elements takes a long time; skipped under -short", size)
			}

			h := openListBenchHost(b)
			graph := h.Graph()
			lists := h.Registries().Lists

			start := time.Now()
			list := buildBenchList(b, h, lists, size)
			b.Logf("built a list of %d elements in %v", size, time.Since(start))

			bystander, err := createNodeVia(graph)
			if err != nil {
				b.Fatalf("CreateNode() for the bystander: %v", err)
			}

			value, err := createNodeVia(graph)
			if err != nil {
				b.Fatalf("CreateNode() for the value: %v", err)
			}

			commit := func(b *testing.B, step func(tx Tx) error) {
				b.Helper()
				b.ReportAllocs()

				for b.Loop() {
					if txErr := graph.Transact(step); txErr != nil {
						b.Fatalf("Transact(): %v", txErr)
					}
				}
			}

			b.Run("empty-commit", func(b *testing.B) {
				commit(b, func(tx Tx) error {
					tx.Touch(bystander)

					return nil
				})
			})

			b.Run("tail-scan", func(b *testing.B) {
				commit(b, func(tx Tx) error {
					_, _, scanErr := findUniqueTaggedChild(tx, list, lists.allTails)

					return scanErr
				})
			})

			b.Run("local-check", func(b *testing.B) {
				tail, hasTail, tailErr := lists.Tail(graph, list)
				if tailErr != nil || !hasTail {
					b.Fatalf("Tail() = (%v,%v)", hasTail, tailErr)
				}

				commit(b, func(tx Tx) error {
					tx.Touch(tail)

					return nil
				})
			})

			b.Run("checker-only", func(b *testing.B) {
				commit(b, func(tx Tx) error {
					tx.Touch(list)

					return nil
				})
			})

			b.Run("append", func(b *testing.B) {
				commit(b, func(tx Tx) error {
					_, appendErr := lists.Append(tx, list, value)

					return appendErr
				})
			})
		})
	}
}
