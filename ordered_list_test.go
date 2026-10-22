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
	"reflect"
	"slices"
	"testing"
)

// The tests in this file are the behaviour every OrderedList implementation
// must have. Each runs against every implementation in orderedListImpls, on
// every backend (testBackends). Tests that corrupt one implementation's own
// structure stay with that implementation (main_test.go).

// orderedListImpl is one OrderedList implementation the shared tests run
// against.
type orderedListImpl struct {
	name string

	// build constructs the implementation over graph, bootstrapping the
	// names it needs. It is called once per graph.
	build func(t *testing.T, graph testGraph) OrderedList
}

// orderedListImpls lists every implementation under test.
var orderedListImpls = []orderedListImpl{
	{name: "ListRegistry", build: buildListRegistry},
	{name: "SlottedListRegistry", build: buildSlottedListRegistry},
}

func buildSlottedListRegistry(t *testing.T, graph testGraph) OrderedList {
	t.Helper()

	return buildSlottedList(t, graph)
}

func buildListRegistry(t *testing.T, graph testGraph) OrderedList {
	t.Helper()

	return listRegistryFromIDs(t, graph, bootstrapTestNames(t, graph))
}

// forEachOrderedList runs body once per backend and implementation, each on a
// fresh graph.
func forEachOrderedList(t *testing.T, body func(t *testing.T, g testGraph, lists OrderedList)) {
	t.Helper()

	for _, backend := range testBackends() {
		for _, impl := range orderedListImpls {
			t.Run(backend.name+"/"+impl.name, func(t *testing.T) {
				graph := backend.open(t)

				body(t, graph, impl.build(t, graph))
			})
		}
	}
}

func mustNewOrderedList(t *testing.T, lists OrderedList, api Transactor) NodeID {
	t.Helper()

	list, err := lists.NewList(api)
	if err != nil {
		t.Fatalf("NewList(): %v", err)
	}

	return list
}

func mustAppendTo(t *testing.T, lists OrderedList, api Transactor, list, value NodeID) NodeID {
	t.Helper()

	capsule, err := lists.Append(api, list, value)
	if err != nil {
		t.Fatalf("Append(%d, %d): %v", list, value, err)
	}

	return capsule
}

// mustAppendValues appends count fresh nodes to list and returns them with
// their capsules, in order.
func mustAppendValues(t *testing.T, lists OrderedList, api Transactor, list NodeID, count int) (values, capsules []NodeID) {
	t.Helper()

	values = make([]NodeID, 0, count)
	capsules = make([]NodeID, 0, count)

	for range count {
		value := mustCreateNode(t, api)

		values = append(values, value)
		capsules = append(capsules, mustAppendTo(t, lists, api, list, value))
	}

	return values, capsules
}

// requireChainEnds checks list's head and tail against capsules, its capsules
// in order (none: an empty list).
func requireChainEnds(t *testing.T, lists OrderedList, graph GraphReader, list NodeID, capsules []NodeID) {
	t.Helper()

	head, hasHead, headErr := lists.Head(graph, list)
	if headErr != nil {
		t.Fatalf("Head(%d): %v", list, headErr)
	}

	tail, hasTail, tailErr := lists.Tail(graph, list)
	if tailErr != nil {
		t.Fatalf("Tail(%d): %v", list, tailErr)
	}

	if len(capsules) == 0 {
		if hasHead || hasTail {
			t.Fatalf("an empty list has head=%v tail=%v, want neither", hasHead, hasTail)
		}

		return
	}

	if !hasHead || head != capsules[0] {
		t.Fatalf("Head() = (%d,%v), want (%d,true)", head, hasHead, capsules[0])
	}

	if last := capsules[len(capsules)-1]; !hasTail || tail != last {
		t.Fatalf("Tail() = (%d,%v), want (%d,true)", tail, hasTail, last)
	}
}

// requireListState checks list's elements and its head and tail.
func requireListState(t *testing.T, lists OrderedList, graph GraphReader, list NodeID, values, capsules []NodeID) {
	t.Helper()

	requireListElements(t, lists, graph, list, values)
	requireChainEnds(t, lists, graph, list, capsules)
}

// withoutIndex returns a copy of ids without the element at index, nil if
// nothing is left (Elements of an empty list is nil).
func withoutIndex(ids []NodeID, index int) []NodeID {
	rest := slices.Delete(slices.Clone(ids), index, index+1)
	if len(rest) == 0 {
		return nil
	}

	return rest
}

var orderedListRemovalCases = []struct {
	name        string
	size, index int
}{
	{name: "head", size: 3, index: 0},
	{name: "middle", size: 3, index: 1},
	{name: "tail", size: 3, index: 2},
	{name: "sole element", size: 1, index: 0},
}

func TestOrderedListStartsEmpty(t *testing.T) {
	forEachOrderedList(t, func(t *testing.T, g testGraph, lists OrderedList) {
		list := mustNewOrderedList(t, lists, g)

		if is, err := lists.IsList(g, list); err != nil || !is {
			t.Fatalf("IsList(list) = (%v,%v), want (true,nil)", is, err)
		}

		if is, err := lists.IsList(g, mustCreateNode(t, g)); err != nil || is {
			t.Fatalf("IsList(plain node) = (%v,%v), want (false,nil)", is, err)
		}

		requireListState(t, lists, g, list, nil, nil)

		if verifyErr := lists.VerifyStructures(g); verifyErr != nil {
			t.Fatalf("VerifyStructures(): %v", verifyErr)
		}
	})
}

func TestOrderedListAppendKeepsOrderAndEnds(t *testing.T) {
	forEachOrderedList(t, func(t *testing.T, g testGraph, lists OrderedList) {
		list := mustNewOrderedList(t, lists, g)

		for size := 1; size <= 4; size++ {
			// One more element each round, on the same list.
			value := mustCreateNode(t, g)
			capsule := mustAppendTo(t, lists, g, list, value)

			if size == 1 {
				requireListState(t, lists, g, list, []NodeID{value}, []NodeID{capsule})

				continue
			}

			elements, err := lists.Elements(g, list)
			if err != nil || len(elements) != size || elements[size-1] != value {
				t.Fatalf("after %d appends Elements() = (%v,%v), want %d elements ending in %d", size, elements, err, size, value)
			}

			if tail, hasTail, tailErr := lists.Tail(g, list); tailErr != nil || !hasTail || tail != capsule {
				t.Fatalf("Tail() = (%d,%v,%v), want (%d,true,nil)", tail, hasTail, tailErr, capsule)
			}
		}
	})
}

func TestOrderedListPrependAddsAtTheFront(t *testing.T) {
	forEachOrderedList(t, func(t *testing.T, g testGraph, lists OrderedList) {
		list := mustNewOrderedList(t, lists, g)

		// Prepend to an empty list: it is head and tail at once.
		first := mustCreateNode(t, g)

		firstCapsule, err := lists.Prepend(g, list, first)
		if err != nil {
			t.Fatalf("Prepend() on an empty list: %v", err)
		}

		requireListState(t, lists, g, list, []NodeID{first}, []NodeID{firstCapsule})

		second := mustCreateNode(t, g)

		secondCapsule, err := lists.Prepend(g, list, second)
		if err != nil {
			t.Fatalf("Prepend(): %v", err)
		}

		requireListState(t, lists, g, list, []NodeID{second, first}, []NodeID{secondCapsule, firstCapsule})
	})
}

func TestOrderedListInsertAfterMiddleAndTail(t *testing.T) {
	forEachOrderedList(t, func(t *testing.T, g testGraph, lists OrderedList) {
		list := mustNewOrderedList(t, lists, g)
		values, capsules := mustAppendValues(t, lists, g, list, 2) // a, c

		b := mustCreateNode(t, g)

		capsuleB, err := lists.InsertAfter(g, list, capsules[0], b)
		if err != nil {
			t.Fatalf("InsertAfter(a, b): %v", err)
		}

		values = slices.Insert(values, 1, b)
		capsules = slices.Insert(capsules, 1, capsuleB)
		requireListState(t, lists, g, list, values, capsules)

		d := mustCreateNode(t, g)

		capsuleD, err := lists.InsertAfter(g, list, capsules[2], d)
		if err != nil {
			t.Fatalf("InsertAfter(tail, d): %v", err)
		}

		requireListState(t, lists, g, list, append(values, d), append(capsules, capsuleD))
	})
}

func TestOrderedListAppendManyAppendsInOrderAndIsAtomic(t *testing.T) {
	forEachOrderedList(t, func(t *testing.T, g testGraph, lists OrderedList) {
		list := mustNewOrderedList(t, lists, g)

		if none, err := lists.AppendMany(g, list, nil); err != nil || len(none) != 0 {
			t.Fatalf("AppendMany(nothing) = (%v,%v), want (none,nil)", none, err)
		}

		values, capsules := mustAppendValues(t, lists, g, list, 1)
		more := []NodeID{mustCreateNode(t, g), mustCreateNode(t, g), mustCreateNode(t, g)}

		added, err := lists.AppendMany(g, list, more)
		if err != nil || len(added) != len(more) {
			t.Fatalf("AppendMany() = (%v,%v), want %d capsules", added, err, len(more))
		}

		values = append(values, more...)
		capsules = append(capsules, added...)
		requireListState(t, lists, g, list, values, capsules)

		const nonexistent NodeID = 999999

		if _, manyErr := lists.AppendMany(g, list, []NodeID{mustCreateNode(t, g), nonexistent}); !errors.Is(manyErr, ErrNodeNotFound) {
			t.Fatalf("AppendMany() with a missing value error = %v, want %v", manyErr, ErrNodeNotFound)
		}

		requireListState(t, lists, g, list, values, capsules)
	})
}

func TestOrderedListRemoveReclaimsTheCapsule(t *testing.T) {
	forEachOrderedList(t, func(t *testing.T, g testGraph, lists OrderedList) {
		for _, tc := range orderedListRemovalCases {
			t.Run(tc.name, func(t *testing.T) {
				list := mustNewOrderedList(t, lists, g)
				values, capsules := mustAppendValues(t, lists, g, list, tc.size)
				removed := capsules[tc.index]

				deleted, err := lists.Remove(g, list, removed)
				if err != nil || !deleted {
					t.Fatalf("Remove() = (%v,%v), want (true,nil)", deleted, err)
				}

				if mustNodeExists(t, g, removed) {
					t.Fatalf("capsule %d survived Remove()", removed)
				}

				requireListState(t, lists, g, list, withoutIndex(values, tc.index), withoutIndex(capsules, tc.index))
			})
		}
	})
}

func TestOrderedListRemoveWithoutDeletingKeepsTheCapsule(t *testing.T) {
	forEachOrderedList(t, func(t *testing.T, g testGraph, lists OrderedList) {
		for _, tc := range orderedListRemovalCases {
			t.Run(tc.name, func(t *testing.T) {
				list := mustNewOrderedList(t, lists, g)
				values, capsules := mustAppendValues(t, lists, g, list, tc.size)
				removed := capsules[tc.index]

				if err := lists.RemoveWithoutDeletingCapsule(g, list, removed); err != nil {
					t.Fatalf("RemoveWithoutDeletingCapsule(): %v", err)
				}

				requireListState(t, lists, g, list, withoutIndex(values, tc.index), withoutIndex(capsules, tc.index))

				// The capsule stands alone: still a capsule, in no list, with
				// no links into the list it left.
				if !mustNodeExists(t, g, removed) || !mustIsCapsule(t, lists.Capsules(), g, removed) {
					t.Fatalf("capsule %d did not survive RemoveWithoutDeletingCapsule()", removed)
				}

				owners, ownersErr := lists.ListsOf(g, removed)
				if ownersErr != nil || len(owners) != 0 {
					t.Fatalf("ListsOf(removed capsule) = (%v,%v), want none", owners, ownersErr)
				}

				if _, hasPrev, prevErr := lists.Capsules().Prev(g, removed); prevErr != nil || hasPrev {
					t.Fatalf("Prev(removed capsule) = (%v,%v), want no link", hasPrev, prevErr)
				}

				if _, hasNext, nextErr := lists.Capsules().Next(g, removed); nextErr != nil || hasNext {
					t.Fatalf("Next(removed capsule) = (%v,%v), want no link", hasNext, nextErr)
				}
			})
		}
	})
}

func TestOrderedListContainsAndOccurrencesAreScopedToTheList(t *testing.T) {
	forEachOrderedList(t, func(t *testing.T, g testGraph, lists OrderedList) {
		list := mustNewOrderedList(t, lists, g)
		other := mustNewOrderedList(t, lists, g)
		value := mustCreateNode(t, g)
		absent := mustCreateNode(t, g)

		first := mustAppendTo(t, lists, g, list, value)
		second := mustAppendTo(t, lists, g, list, value)
		mustAppendTo(t, lists, g, other, mustCreateNode(t, g))

		occurrences, err := lists.OccurrencesOf(g, list, value)
		if err != nil || !reflect.DeepEqual(sortedNodeIDs(occurrences), sortedNodeIDs([]NodeID{first, second})) {
			t.Fatalf("OccurrencesOf() = (%v,%v), want both capsules of the value", occurrences, err)
		}

		found, ok, err := lists.Contains(g, list, value)
		if err != nil || !ok || (found != first && found != second) {
			t.Fatalf("Contains() = (%d,%v,%v), want one of the value's capsules", found, ok, err)
		}

		if _, inOther, containsErr := lists.Contains(g, other, value); containsErr != nil || inOther {
			t.Fatalf("Contains(other, value) = (%v,%v), want (false,nil)", inOther, containsErr)
		}

		if _, inList, containsErr := lists.Contains(g, list, absent); containsErr != nil || inList {
			t.Fatalf("Contains(list, absent) = (%v,%v), want (false,nil)", inList, containsErr)
		}

		const nonexistent NodeID = 999999

		if _, _, containsErr := lists.Contains(g, list, nonexistent); !errors.Is(containsErr, ErrNodeNotFound) {
			t.Fatalf("Contains(nonexistent) error = %v, want %v", containsErr, ErrNodeNotFound)
		}
	})
}

func TestOrderedListEveryCapsuleBelongsToExactlyOneList(t *testing.T) {
	forEachOrderedList(t, func(t *testing.T, g testGraph, lists OrderedList) {
		listA := mustNewOrderedList(t, lists, g)
		listB := mustNewOrderedList(t, lists, g)
		value := mustCreateNode(t, g)

		capsuleA := mustAppendTo(t, lists, g, listA, value)
		capsuleB := mustAppendTo(t, lists, g, listB, value)

		if capsuleA == capsuleB {
			t.Fatal("one value in two lists shares a capsule, want one capsule per occurrence")
		}

		for _, tc := range []struct{ capsule, list NodeID }{{capsuleA, listA}, {capsuleB, listB}} {
			owners, err := lists.ListsOf(g, tc.capsule)
			if err != nil || !reflect.DeepEqual(owners, []NodeID{tc.list}) {
				t.Fatalf("ListsOf(%d) = (%v,%v), want ([%d],nil)", tc.capsule, owners, err, tc.list)
			}
		}
	})
}

func TestOrderedListIsMemberIsScopedToTheList(t *testing.T) {
	const nonexistent NodeID = 999999

	forEachOrderedList(t, func(t *testing.T, g testGraph, lists OrderedList) {
		list := mustNewOrderedList(t, lists, g)
		other := mustNewOrderedList(t, lists, g)
		_, capsules := mustAppendValues(t, lists, g, list, 2)
		_, otherCapsules := mustAppendValues(t, lists, g, other, 1)

		cases := []struct {
			name    string
			list    NodeID
			capsule NodeID
			want    bool
		}{
			{name: "head", list: list, capsule: capsules[0], want: true},
			{name: "tail", list: list, capsule: capsules[1], want: true},
			{name: "capsule of another list", list: list, capsule: otherCapsules[0]},
			{name: "plain node", list: list, capsule: mustCreateNode(t, g)},
			{name: "nonexistent node", list: list, capsule: nonexistent},
		}

		for _, tc := range cases {
			got, err := lists.IsMember(g, tc.list, tc.capsule)
			if err != nil || got != tc.want {
				t.Fatalf("%s: IsMember() = (%v,%v), want (%v,nil)", tc.name, got, err, tc.want)
			}
		}

		if removeErr := lists.RemoveWithoutDeletingCapsule(g, list, capsules[0]); removeErr != nil {
			t.Fatalf("RemoveWithoutDeletingCapsule(): %v", removeErr)
		}

		if got, err := lists.IsMember(g, list, capsules[0]); err != nil || got {
			t.Fatalf("IsMember() after removal = (%v,%v), want (false,nil)", got, err)
		}

		if _, err := lists.IsMember(g, mustCreateNode(t, g), capsules[1]); !errors.Is(err, ErrNotList) {
			t.Fatalf("IsMember(not a list) error = %v, want %v", err, ErrNotList)
		}

		if _, err := lists.IsMember(g, nonexistent, capsules[1]); !errors.Is(err, ErrNodeNotFound) {
			t.Fatalf("IsMember(nonexistent list) error = %v, want %v", err, ErrNodeNotFound)
		}
	})
}

func TestOrderedListDeleteListOnlyWhenEmpty(t *testing.T) {
	forEachOrderedList(t, func(t *testing.T, g testGraph, lists OrderedList) {
		list := mustNewOrderedList(t, lists, g)
		values, capsules := mustAppendValues(t, lists, g, list, 1)

		if err := lists.DeleteList(g, list); !errors.Is(err, ErrNodeNotEmpty) {
			t.Fatalf("DeleteList() of a non-empty list error = %v, want %v", err, ErrNodeNotEmpty)
		}

		// A refused delete changes nothing, the tag included.
		if is, err := lists.IsList(g, list); err != nil || !is {
			t.Fatalf("IsList() after a refused DeleteList() = (%v,%v), want (true,nil)", is, err)
		}

		requireListState(t, lists, g, list, values, capsules)

		if deleted, err := lists.Remove(g, list, capsules[0]); err != nil || !deleted {
			t.Fatalf("Remove() = (%v,%v), want (true,nil)", deleted, err)
		}

		if err := lists.DeleteList(g, list); err != nil {
			t.Fatalf("DeleteList() of an empty list: %v", err)
		}

		if mustNodeExists(t, g, list) {
			t.Fatalf("list %d still exists after DeleteList()", list)
		}
	})
}

func TestOrderedListOperationsRequireAList(t *testing.T) {
	forEachOrderedList(t, func(t *testing.T, g testGraph, lists OrderedList) {
		notAList := mustCreateNode(t, g)
		value := mustCreateNode(t, g)

		capsule, capsuleErr := lists.Capsules().NewCapsule(g, value)
		if capsuleErr != nil {
			t.Fatalf("NewCapsule(): %v", capsuleErr)
		}

		if _, _, err := lists.Head(g, notAList); !errors.Is(err, ErrNotList) {
			t.Fatalf("Head() error = %v, want %v", err, ErrNotList)
		}

		if _, _, err := lists.Tail(g, notAList); !errors.Is(err, ErrNotList) {
			t.Fatalf("Tail() error = %v, want %v", err, ErrNotList)
		}

		if _, err := lists.Append(g, notAList, value); !errors.Is(err, ErrNotList) {
			t.Fatalf("Append() error = %v, want %v", err, ErrNotList)
		}

		if _, err := lists.AppendMany(g, notAList, nil); !errors.Is(err, ErrNotList) {
			t.Fatalf("AppendMany() error = %v, want %v", err, ErrNotList)
		}

		if _, err := lists.Prepend(g, notAList, value); !errors.Is(err, ErrNotList) {
			t.Fatalf("Prepend() error = %v, want %v", err, ErrNotList)
		}

		if _, err := lists.InsertAfter(g, notAList, capsule, value); !errors.Is(err, ErrNotList) {
			t.Fatalf("InsertAfter() error = %v, want %v", err, ErrNotList)
		}

		if _, err := lists.Elements(g, notAList); !errors.Is(err, ErrNotList) {
			t.Fatalf("Elements() error = %v, want %v", err, ErrNotList)
		}

		if _, _, err := lists.Contains(g, notAList, value); !errors.Is(err, ErrNotList) {
			t.Fatalf("Contains() error = %v, want %v", err, ErrNotList)
		}

		if _, err := lists.OccurrencesOf(g, notAList, value); !errors.Is(err, ErrNotList) {
			t.Fatalf("OccurrencesOf() error = %v, want %v", err, ErrNotList)
		}

		if _, err := lists.Remove(g, notAList, capsule); !errors.Is(err, ErrNotList) {
			t.Fatalf("Remove() error = %v, want %v", err, ErrNotList)
		}

		if err := lists.RemoveWithoutDeletingCapsule(g, notAList, capsule); !errors.Is(err, ErrNotList) {
			t.Fatalf("RemoveWithoutDeletingCapsule() error = %v, want %v", err, ErrNotList)
		}

		if err := lists.DeleteList(g, notAList); !errors.Is(err, ErrNotList) {
			t.Fatalf("DeleteList() error = %v, want %v", err, ErrNotList)
		}
	})
}

func TestOrderedListRefusesACapsuleThatIsNotAnElement(t *testing.T) {
	forEachOrderedList(t, func(t *testing.T, g testGraph, lists OrderedList) {
		list := mustNewOrderedList(t, lists, g)
		values, capsules := mustAppendValues(t, lists, g, list, 2)
		value := mustCreateNode(t, g)

		outsider, capsuleErr := lists.Capsules().NewCapsule(g, value)
		if capsuleErr != nil {
			t.Fatalf("NewCapsule(): %v", capsuleErr)
		}

		plain := mustCreateNode(t, g)

		if _, err := lists.Remove(g, list, outsider); !errors.Is(err, ErrCapsuleNotInList) {
			t.Fatalf("Remove(outsider) error = %v, want %v", err, ErrCapsuleNotInList)
		}

		if err := lists.RemoveWithoutDeletingCapsule(g, list, outsider); !errors.Is(err, ErrCapsuleNotInList) {
			t.Fatalf("RemoveWithoutDeletingCapsule(outsider) error = %v, want %v", err, ErrCapsuleNotInList)
		}

		if _, err := lists.InsertAfter(g, list, outsider, value); !errors.Is(err, ErrCapsuleNotInList) {
			t.Fatalf("InsertAfter(outsider) error = %v, want %v", err, ErrCapsuleNotInList)
		}

		if _, err := lists.InsertAfter(g, list, plain, value); !errors.Is(err, ErrCapsuleNotInList) {
			t.Fatalf("InsertAfter(plain node) error = %v, want %v", err, ErrCapsuleNotInList)
		}

		requireListState(t, lists, g, list, values, capsules)
	})
}

func TestOrderedListMutatorsComposeAndRollBackTogether(t *testing.T) {
	forEachOrderedList(t, func(t *testing.T, g testGraph, lists OrderedList) {
		list := mustNewOrderedList(t, lists, g)
		a := mustCreateNode(t, g)
		b := mustCreateNode(t, g)
		c := mustCreateNode(t, g)
		capsuleA := mustAppendTo(t, lists, g, list, a)

		var capsuleB, capsuleC NodeID

		// Append, Prepend and Remove in one transaction: [a] -> [c, a, b] -> [c, b].
		err := g.Transact(func(tx Tx) error {
			var stepErr error

			capsuleB, stepErr = lists.Append(tx, list, b)
			if stepErr != nil {
				return wrapInterfaceErr(stepErr)
			}

			capsuleC, stepErr = lists.Prepend(tx, list, c)
			if stepErr != nil {
				return wrapInterfaceErr(stepErr)
			}

			deleted, stepErr := lists.Remove(tx, list, capsuleA)
			if stepErr != nil {
				return wrapInterfaceErr(stepErr)
			}

			if !deleted {
				t.Error("Remove() reported the capsule was not deleted")
			}

			return nil
		})
		if err != nil {
			t.Fatalf("Transact(compose) error = %v", err)
		}

		requireListState(t, lists, g, list, []NodeID{c, b}, []NodeID{capsuleC, capsuleB})

		errOuter := errors.New("outer failure")

		err = g.Transact(func(tx Tx) error {
			if _, appendErr := lists.Append(tx, list, a); appendErr != nil {
				return wrapInterfaceErr(appendErr)
			}

			return errOuter
		})
		if !errors.Is(err, errOuter) {
			t.Fatalf("Transact(fail) error = %v, want %v", err, errOuter)
		}

		requireListState(t, lists, g, list, []NodeID{c, b}, []NodeID{capsuleC, capsuleB})
	})
}

func TestOrderedListStepBackWalksFromTheTailToTheHead(t *testing.T) {
	forEachOrderedList(t, func(t *testing.T, g testGraph, lists OrderedList) {
		list := mustNewOrderedList(t, lists, g)
		_, capsules := mustAppendValues(t, lists, g, list, 3)

		current := capsules[2]

		for i := 2; i > 0; i-- {
			prev, hasPrev, err := lists.stepBack(g, list, current)
			if err != nil || !hasPrev || prev != capsules[i-1] {
				t.Fatalf("stepBack(%d) = (%d,%v,%v), want (%d,true,nil)", current, prev, hasPrev, err, capsules[i-1])
			}

			current = prev
		}

		if _, hasPrev, err := lists.stepBack(g, list, current); err != nil || hasPrev {
			t.Fatalf("stepBack(head) = (%v,%v), want (false,nil)", hasPrev, err)
		}

		outsider, capsuleErr := lists.Capsules().NewCapsule(g, mustCreateNode(t, g))
		if capsuleErr != nil {
			t.Fatalf("NewCapsule(): %v", capsuleErr)
		}

		if _, _, err := lists.stepBack(g, list, outsider); !errors.Is(err, ErrInvalidListStructure) {
			t.Fatalf("stepBack(outsider) error = %v, want %v", err, ErrInvalidListStructure)
		}
	})
}

// TestOrderedListMixedWorkloadStaysValid mixes every mutator and checks the
// result, in full, with Elements and VerifyStructures.
func TestOrderedListMixedWorkloadStaysValid(t *testing.T) {
	forEachOrderedList(t, func(t *testing.T, g testGraph, lists OrderedList) {
		list := mustNewOrderedList(t, lists, g)
		values, capsules := mustAppendValues(t, lists, g, list, 3) // a b c

		d := mustCreateNode(t, g)

		capsuleD, err := lists.Prepend(g, list, d) // d a b c
		if err != nil {
			t.Fatalf("Prepend(): %v", err)
		}

		values = append([]NodeID{d}, values...)
		capsules = append([]NodeID{capsuleD}, capsules...)

		if deleted, removeErr := lists.Remove(g, list, capsules[2]); removeErr != nil || !deleted { // d a c
			t.Fatalf("Remove(b) = (%v,%v), want (true,nil)", deleted, removeErr)
		}

		values, capsules = withoutIndex(values, 2), withoutIndex(capsules, 2)

		e := mustCreateNode(t, g)

		capsuleE, err := lists.InsertAfter(g, list, capsules[1], e) // d a e c
		if err != nil {
			t.Fatalf("InsertAfter(): %v", err)
		}

		values = []NodeID{values[0], values[1], e, values[2]}
		capsules = []NodeID{capsules[0], capsules[1], capsuleE, capsules[2]}

		f := mustCreateNode(t, g)
		capsuleF := mustAppendTo(t, lists, g, list, f) // d a e c f

		values, capsules = append(values, f), append(capsules, capsuleF)

		if removeErr := lists.RemoveWithoutDeletingCapsule(g, list, capsules[0]); removeErr != nil { // a e c f
			t.Fatalf("RemoveWithoutDeletingCapsule(head): %v", removeErr)
		}

		values, capsules = withoutIndex(values, 0), withoutIndex(capsules, 0)

		requireListState(t, lists, g, list, values, capsules)

		if verifyErr := lists.VerifyStructures(g); verifyErr != nil {
			t.Fatalf("VerifyStructures(): %v", verifyErr)
		}
	})
}
