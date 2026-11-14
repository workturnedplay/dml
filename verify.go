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
	"fmt"
)

// defaultVerifyPageSize is the page size VerifyAll uses when given a
// non-positive one.
const defaultVerifyPageSize = 1000

// VerifyAll runs every registered Checker over every node in the graph, the
// semantic integrity sweep of theorystate.md section 104. Checkers only see
// nodes a transaction touches, so data that did not come through this
// process's Checkers (an older build, a restored backup, another tool, an
// invariant added later) is otherwise unchecked until something reads it.
//
// The sweep runs one Transact per page of pageSize node IDs (a
// non-positive pageSize means defaultVerifyPageSize), each calling Tx.Touch
// on its page and changing nothing, so the existing relevance filter and
// Checkers run unchanged, it works through a GraphActor and a RootGraph,
// and it never needs the whole graph in memory on a backend that pages. The
// view can shift between pages (a node added or removed in the gap), which
// is acceptable here: later changes are checked by commit-time Checkers.
//
// It is fail-closed: the first Checker to decline is returned as an error
// wrapping ErrLoadVerification and the Checker's own attributable error, and
// nothing is repaired. It covers only the Checkers registered when it runs,
// so call it after constructing the registries.
func VerifyAll(graph Transactor, pageSize int) error {
	if pageSize <= 0 {
		pageSize = defaultVerifyPageSize
	}

	var after NodeID

	hasAfter := false

	for {
		var page []NodeID

		err := graph.Transact(func(tx Tx) error {
			var pageErr error

			page, pageErr = nodesAfter(tx, after, hasAfter, pageSize)
			if pageErr != nil {
				return pageErr
			}

			tx.Touch(page...)

			return nil
		})
		if err != nil {
			return fmt.Errorf("%w: %w", ErrLoadVerification, err)
		}

		if len(page) < pageSize {
			return nil
		}

		after = page[len(page)-1]
		hasAfter = true
	}
}

// ErrLoadVerification wraps the error VerifyAll returns when a Checker
// declines a node at startup: data already in the store (written by an
// older build, restored from a backup, or changed by another tool) violates
// an invariant this process enforces. The wrapped error is the Checker's
// own attributable "<Checker name>: <error>". Nothing is repaired
// (theorystate.md section 104).
var ErrLoadVerification = errors.New("stored data failed verification")
