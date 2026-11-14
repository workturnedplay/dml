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

// OrderedList is what an ordered list implementation offers: the operations
// the composite Set log, the shared behaviour tests and, later, other
// consumers use, so a list can be swapped for another implementation of the
// same behaviour. ListRegistry is one; every implementation keeps the same
// contract:
//   - every method checks that list is a list first (ErrNodeNotFound, then
//     ErrNotList), before anything else;
//   - a capsule that is not an element of list is ErrCapsuleNotInList for
//     InsertAfter, Remove and RemoveWithoutDeletingCapsule;
//   - Elements of an empty list is nil;
//   - a member capsule belongs to exactly one list (ListsOf);
//   - IsMember(list, capsule) reports whether capsule is an element of list,
//     after the same list check (ErrNodeNotFound, then ErrNotList): code
//     that needs membership uses it, never the raw edge, which differs
//     between implementations;
//   - every mutator takes a Transactor, so it composes and nests.
//
// stepBack is unexported on purpose: it is only for code in this package, so
// the interface cannot be implemented elsewhere.
type OrderedList interface {
	IsList(graph GraphReader, id NodeID) (bool, error)
	NewList(graph Transactor) (NodeID, error)
	Head(graph GraphReader, list NodeID) (head NodeID, hasHead bool, err error)
	Tail(graph GraphReader, list NodeID) (tail NodeID, hasTail bool, err error)
	Append(graph Transactor, list, value NodeID) (NodeID, error)
	AppendMany(graph Transactor, list NodeID, values []NodeID) ([]NodeID, error)
	Prepend(graph Transactor, list, value NodeID) (NodeID, error)
	InsertAfter(graph Transactor, list, afterCapsule, value NodeID) (NodeID, error)
	Remove(graph Transactor, list, capsule NodeID) (deleted bool, err error)
	RemoveWithoutDeletingCapsule(graph Transactor, list, capsule NodeID) error
	DeleteList(graph Transactor, list NodeID) error
	Elements(graph GraphReader, list NodeID) ([]NodeID, error)
	OccurrencesOf(graph GraphReader, list, value NodeID) ([]NodeID, error)
	Contains(graph GraphReader, list, value NodeID) (capsule NodeID, found bool, err error)
	ListsOf(graph GraphReader, capsule NodeID) ([]NodeID, error)
	IsMember(graph GraphReader, list, capsule NodeID) (bool, error)
	VerifyStructures(graph GraphAPI) error

	// Capsules is the CapsuleRegistry the list's capsules are made by.
	Capsules() *CapsuleRegistry

	// stepBack returns the capsule before capsule in list, checking only
	// what one step needs (see ListRegistry.stepBack).
	stepBack(graph GraphReader, list, capsule NodeID) (NodeID, bool, error)
}

// Compile-time assertion that *ListRegistry satisfies OrderedList.
var _ OrderedList = (*ListRegistry)(nil)
