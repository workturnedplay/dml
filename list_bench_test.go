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

// benchCommit times step as one transaction per iteration. Shared by every
// list benchmark.
func benchCommit(b *testing.B, graph GraphAPI, step func(tx Tx) error) {
	b.Helper()
	b.ReportAllocs()

	for b.Loop() {
		if txErr := graph.Transact(step); txErr != nil {
			b.Fatalf("Transact(): %v", txErr)
		}
	}
}

// buildInChunks makes total items in transactions of at most chunk items each
// (one transaction for everything would make bolt's transaction, and every
// commit-time Checker pass over it, needlessly large). step makes count items
// inside tx; it may be re-run, so it must not keep state outside tx. what names
// the items in the failure message.
func buildInChunks(b *testing.B, graph GraphAPI, what string, total, chunk int, step func(tx Tx, count int) error) {
	b.Helper()

	for built := 0; built < total; {
		count := min(chunk, total-built)

		txErr := graph.Transact(func(tx Tx) error {
			return step(tx, count)
		})
		if txErr != nil {
			b.Fatalf("building %s (%d of %d built): %v", what, built, total, txErr)
		}

		built += count
	}
}

// buildBenchList makes a list of size elements with AppendMany, one chunk
// per transaction (appending one at a time would itself be quadratic).
func buildBenchList(b *testing.B, h *Host, lists OrderedList, size int) NodeID {
	b.Helper()

	list, err := lists.NewList(h.Graph())
	if err != nil {
		b.Fatalf("NewList(): %v", err)
	}

	what := fmt.Sprintf("the list (%d elements)", size)

	buildInChunks(b, h.Graph(), what, size, listBenchChunk, func(tx Tx, count int) error {
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

	return list
}

// buildCrowd makes count lists of one element each (the "other lists" of
// BenchmarkManyListsCommitCost), manyListsCrowdChunk lists per transaction.
func buildCrowd(b *testing.B, h *Host, lists OrderedList, count int) {
	b.Helper()

	what := fmt.Sprintf("the crowd (%d lists)", count)

	buildInChunks(b, h.Graph(), what, count, manyListsCrowdChunk, func(tx Tx, n int) error {
		for range n {
			list, newErr := lists.NewList(tx)
			if newErr != nil {
				return wrapInterfaceErr(newErr)
			}

			value, createErr := createNodeTx(tx)
			if createErr != nil {
				return createErr
			}

			if _, appendErr := lists.AppendMany(tx, list, []NodeID{value}); appendErr != nil {
				return wrapInterfaceErr(appendErr)
			}
		}

		return nil
	})
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

			b.Run("empty-commit", func(b *testing.B) {
				benchCommit(b, graph, func(tx Tx) error {
					tx.Touch(bystander)

					return nil
				})
			})

			b.Run("tail-scan", func(b *testing.B) {
				benchCommit(b, graph, func(tx Tx) error {
					_, _, scanErr := findUniqueTaggedChild(tx, list, lists.allTails)

					return scanErr
				})
			})

			b.Run("local-check", func(b *testing.B) {
				tail, hasTail, tailErr := lists.Tail(graph, list)
				if tailErr != nil || !hasTail {
					b.Fatalf("Tail() = (%v,%v)", hasTail, tailErr)
				}

				benchCommit(b, graph, func(tx Tx) error {
					tx.Touch(tail)

					return nil
				})
			})

			b.Run("checker-only", func(b *testing.B) {
				benchCommit(b, graph, func(tx Tx) error {
					tx.Touch(list)

					return nil
				})
			})

			b.Run("append", func(b *testing.B) {
				benchCommit(b, graph, func(tx Tx) error {
					_, appendErr := lists.Append(tx, list, value)

					return appendErr
				})
			})
		})
	}
}

// ---------------------------------------------------------------------
// Many lists (implementation_state.md item 86). ListRegistry finds a tail as
// the child its list shares with the AllTails hub (item 68), which costs about
// the smaller of the two child sets, and the hub has one member per non-empty
// list. So its worst case is a long list in a graph with as many other lists,
// and a short list in the same graph is the control. A commit that touches
// only a list node makes ListRegistry validate the whole list, while the
// slotted type checks head and tail. BenchmarkManyListsCommitCost runs the same
// steps for both list types over the production stack. Run without -race:
//
//	go test -run "^$" -bench ManyListsCommitCost -benchtime 30x -timeout 120m
//
// -short limits it to the 1,000 and 10,000 sizes. To try one size and one
// type first:
//
//	go test -run "^$" -bench "ManyListsCommitCost/^ListRegistry$/^lists=1000$" -benchtime 30x
//
// The ^ and $ matter: -bench splits its pattern at each / and matches every
// part unanchored against the name at that level, so a bare ListRegistry
// also selects SlottedListRegistry and a bare lists=1000 also selects
// lists=10000 and lists=100000.
//
// Per size, the sub-benchmarks are empty-commit (the baseline), and for each
// of short-list and long-list: tail-lookup (read-only, no Checker work),
// touch-tail (a commit touching the tail capsule: the local check),
// touch-list (a commit touching only the list node) and append (last, since it
// lengthens the list by about 30, which does not matter at 30x).

const (
	// manyListsCrowdChunk is how many one-element lists one transaction
	// makes while the crowd is built.
	manyListsCrowdChunk = 5_000

	// manyListsShortLen is the length of the control list.
	manyListsShortLen = 10
)

// manyListsKinds are the list types BenchmarkManyListsCommitCost compares.
var manyListsKinds = []struct {
	name string
	pick func(h *Host) OrderedList
}{
	{name: "ListRegistry", pick: func(h *Host) OrderedList { return h.Registries().Lists }},
	{name: "SlottedListRegistry", pick: func(h *Host) OrderedList { return h.Registries().SlottedLists }},
}

func BenchmarkManyListsCommitCost(b *testing.B) {
	for _, kind := range manyListsKinds {
		b.Run(kind.name, func(b *testing.B) {
			for _, size := range listBenchSizes {
				b.Run(fmt.Sprintf("lists=%d", size), func(b *testing.B) {
					benchManyLists(b, kind.pick, size)
				})
			}
		})
	}
}

// benchManyLists builds size one-element lists, then a short list and a long
// list of size elements, over a fresh host, and times the steps on each.
func benchManyLists(b *testing.B, pick func(h *Host) OrderedList, size int) {
	b.Helper()

	if testing.Short() && size > listBenchShortLimit {
		b.Skipf("building %d lists takes a long time; skipped under -short", size)
	}

	h := openListBenchHost(b)
	graph := h.Graph()
	lists := pick(h)

	start := time.Now()
	buildCrowd(b, h, lists, size)
	shortList := buildBenchList(b, h, lists, manyListsShortLen)
	longList := buildBenchList(b, h, lists, size)
	b.Logf("built %d one-element lists, a list of %d and a list of %d in %v",
		size, manyListsShortLen, size, time.Since(start))

	bystander, err := createNodeVia(graph)
	if err != nil {
		b.Fatalf("CreateNode() for the bystander: %v", err)
	}

	value, err := createNodeVia(graph)
	if err != nil {
		b.Fatalf("CreateNode() for the value: %v", err)
	}

	b.Run("empty-commit", func(b *testing.B) {
		benchCommit(b, graph, func(tx Tx) error {
			tx.Touch(bystander)

			return nil
		})
	})

	targets := [...]struct {
		name string
		list NodeID
	}{
		{name: "short-list", list: shortList},
		{name: "long-list", list: longList},
	}

	for _, target := range targets {
		b.Run(target.name, func(b *testing.B) {
			b.Run("tail-lookup", func(b *testing.B) {
				benchCommit(b, graph, func(tx Tx) error {
					_, _, tailErr := lists.Tail(tx, target.list)

					return wrapInterfaceErr(tailErr)
				})
			})

			b.Run("touch-tail", func(b *testing.B) {
				tail, hasTail, tailErr := lists.Tail(graph, target.list)
				if tailErr != nil || !hasTail {
					b.Fatalf("Tail() = (%v,%v)", hasTail, tailErr)
				}

				benchCommit(b, graph, func(tx Tx) error {
					tx.Touch(tail)

					return nil
				})
			})

			b.Run("touch-list", func(b *testing.B) {
				benchCommit(b, graph, func(tx Tx) error {
					tx.Touch(target.list)

					return nil
				})
			})

			b.Run("append", func(b *testing.B) {
				benchCommit(b, graph, func(tx Tx) error {
					_, appendErr := lists.AppendMany(tx, target.list, []NodeID{value})

					return wrapInterfaceErr(appendErr)
				})
			})
		})
	}
}
