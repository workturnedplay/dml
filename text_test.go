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
	"strings"
	"testing"
)

// textRig is a TextRegistry over one backend, with the NameRegistry its
// chars are named in.
type textRig struct {
	graph testGraph
	names *NameRegistry
	text  *TextRegistry
}

func newTextRig(t *testing.T, graph testGraph) *textRig {
	t.Helper()

	names := NewNameRegistry(graph)

	ids, err := names.BootstrapNames(graph, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	text, err := NewTextRegistry(graph, names, ids)
	if err != nil {
		t.Fatalf("NewTextRegistry(): %v", err)
	}

	return &textRig{graph: graph, names: names, text: text}
}

// forEachTextRig runs body once per backend, each on a fresh graph.
func forEachTextRig(t *testing.T, body func(t *testing.T, rig *textRig)) {
	t.Helper()

	for _, backend := range testBackends() {
		t.Run(backend.name, func(t *testing.T) {
			body(t, newTextRig(t, backend.open(t)))
		})
	}
}

func (rig *textRig) intern(t *testing.T, text string) NodeID {
	t.Helper()

	node, err := rig.text.Intern(rig.graph, text)
	if err != nil {
		t.Fatalf("Intern(%q): %v", text, err)
	}

	return node
}

func (rig *textRig) internIndexed(t *testing.T, text string) NodeID {
	t.Helper()

	node, err := rig.text.InternIndexed(rig.graph, text)
	if err != nil {
		t.Fatalf("InternIndexed(%q): %v", text, err)
	}

	return node
}

func (rig *textRig) requireText(t *testing.T, node NodeID, want string) {
	t.Helper()

	got, err := rig.text.Text(rig.graph, node)
	if err != nil || got != want {
		t.Fatalf("Text(%d) = (%q,%v), want (%q,nil)", node, got, err, want)
	}
}

// requireCount checks how many children the tag node has.
func (rig *textRig) requireCount(t *testing.T, label string, tag NodeID, want int) {
	t.Helper()

	children, err := rig.graph.FindOutgoing(tag)
	if err != nil {
		t.Fatalf("FindOutgoing(%s): %v", label, err)
	}

	if len(children) != want {
		t.Fatalf("%s has %d children, want %d", label, len(children), want)
	}
}

func (rig *textRig) requireStrings(t *testing.T, want int) {
	t.Helper()

	rig.requireCount(t, "AllStrings", rig.text.allStrings, want)
}

func (rig *textRig) requireIndexNodes(t *testing.T, want int) {
	t.Helper()

	rig.requireCount(t, "AllIndexNodes", rig.text.allIndexNodes, want)
}

func (rig *textRig) charSlots(t *testing.T, r rune) charSlots {
	t.Helper()

	slots, found, err := rig.text.existingChar(rig.graph, make(map[rune]charSlots), r)
	if err != nil || !found {
		t.Fatalf("existingChar(%q) = (found %v, %v), want found", r, found, err)
	}

	return slots
}

func requireSameNodes(t *testing.T, label string, got, want []NodeID) {
	t.Helper()

	if !reflect.DeepEqual(sortedNodeIDs(got), sortedNodeIDs(want)) {
		t.Fatalf("%s = %v, want %v (in some order)", label, got, want)
	}
}

func TestTextInternIsCanonicalAndSharesTails(t *testing.T) {
	forEachTextRig(t, func(t *testing.T, rig *textRig) {
		rig.requireStrings(t, 1) // the empty string

		cat := rig.intern(t, "cat")
		rig.requireStrings(t, 4) // "", "t", "at", "cat"

		if again := rig.intern(t, "cat"); again != cat {
			t.Fatalf("Intern(cat) again = %d, want the same node %d", again, cat)
		}

		rig.requireStrings(t, 4)

		at := rig.intern(t, "at")
		rig.requireStrings(t, 4) // "at" already existed as a tail of "cat"

		hat := rig.intern(t, "hat")
		rig.requireStrings(t, 5) // only "hat" is new

		for _, tail := range []NodeID{cat, hat} {
			if !mustHasRelationship(t, rig.graph, at, tail) {
				t.Fatalf("%d is not a child of \"at\", so the tail is not shared", tail)
			}
		}

		if empty := rig.intern(t, ""); empty != rig.text.EmptyString() {
			t.Fatalf("Intern(\"\") = %d, want the root %d", empty, rig.text.EmptyString())
		}

		for text, node := range map[string]NodeID{"cat": cat, "at": at, "hat": hat, "": rig.text.EmptyString()} {
			rig.requireText(t, node, text)

			found, ok, err := rig.text.Lookup(rig.graph, text)
			if err != nil || !ok || found != node {
				t.Fatalf("Lookup(%q) = (%d,%v,%v), want (%d,true,nil)", text, found, ok, err, node)
			}
		}

		multi := rig.intern(t, "héllo 日本!")
		rig.requireText(t, multi, "héllo 日本!")
	})
}

func TestTextLookupCreatesNothing(t *testing.T) {
	forEachTextRig(t, func(t *testing.T, rig *textRig) {
		rig.intern(t, "cat")

		if _, ok, err := rig.text.Lookup(rig.graph, "dog"); err != nil || ok {
			t.Fatalf("Lookup(dog) = (%v,%v), want (false,nil)", ok, err)
		}

		if _, ok := rig.names.Lookup(charName('d')); ok {
			t.Fatal("Lookup() created the char node of 'd'")
		}

		rig.requireStrings(t, 4)
	})
}

func TestTextRejectsInvalidInput(t *testing.T) {
	forEachTextRig(t, func(t *testing.T, rig *textRig) {
		if _, err := rig.text.Intern(rig.graph, "\xff\xfe"); !errors.Is(err, ErrInvalidText) {
			t.Fatalf("Intern(invalid UTF-8) error = %v, want %v", err, ErrInvalidText)
		}

		if _, err := rig.text.Intern(rig.graph, strings.Repeat("a", MaxTextRunes+1)); !errors.Is(err, ErrTextTooLong) {
			t.Fatalf("Intern(too long) error = %v, want %v", err, ErrTextTooLong)
		}

		if _, _, err := rig.text.Lookup(rig.graph, "\xff"); !errors.Is(err, ErrInvalidText) {
			t.Fatalf("Lookup(invalid UTF-8) error = %v, want %v", err, ErrInvalidText)
		}

		rig.requireStrings(t, 1)
	})
}

func TestTextOperationsRequireAString(t *testing.T) {
	const nonexistent NodeID = 999999

	forEachTextRig(t, func(t *testing.T, rig *textRig) {
		rig.intern(t, "a")

		plain := mustCreateNode(t, rig.graph)
		char := rig.charSlots(t, 'a').node

		for _, node := range []NodeID{plain, char} {
			if _, err := rig.text.Text(rig.graph, node); !errors.Is(err, ErrNotString) {
				t.Fatalf("Text(%d) error = %v, want %v", node, err, ErrNotString)
			}

			if _, err := rig.text.MarkIndexed(rig.graph, node); !errors.Is(err, ErrNotString) {
				t.Fatalf("MarkIndexed(%d) error = %v, want %v", node, err, ErrNotString)
			}
		}

		if _, err := rig.text.Text(rig.graph, nonexistent); !errors.Is(err, ErrNodeNotFound) {
			t.Fatalf("Text(nonexistent) error = %v, want %v", err, ErrNodeNotFound)
		}
	})
}

// TestTextInternRollsBackWithTheEnclosingTransaction: strings and the
// staged char names both disappear with a failed enclosing transaction.
func TestTextInternRollsBackWithTheEnclosingTransaction(t *testing.T) {
	forEachTextRig(t, func(t *testing.T, rig *textRig) {
		errOuter := errors.New("outer failure")

		err := rig.graph.Transact(func(tx Tx) error {
			if _, internErr := rig.text.Intern(tx, "cat"); internErr != nil {
				return internErr
			}

			return errOuter
		})
		if !errors.Is(err, errOuter) {
			t.Fatalf("Transact() error = %v, want %v", err, errOuter)
		}

		rig.requireStrings(t, 1)

		if _, ok := rig.names.Lookup(charName('c')); ok {
			t.Fatal("the char name of 'c' survived the rollback")
		}

		if _, ok, lookupErr := rig.text.Lookup(rig.graph, "cat"); lookupErr != nil || ok {
			t.Fatalf("Lookup(cat) = (%v,%v), want (false,nil)", ok, lookupErr)
		}

		// And the same text can be interned afterwards.
		rig.requireText(t, rig.intern(t, "cat"), "cat")
	})
}

func TestTextEndingWithFindsEveryStringThatEndsWithTheSuffix(t *testing.T) {
	forEachTextRig(t, func(t *testing.T, rig *textRig) {
		cat := rig.intern(t, "cat")
		hat := rig.intern(t, "hat")
		car := rig.intern(t, "car")
		at := rig.intern(t, "at")

		got, err := rig.text.EndingWith(rig.graph, "at", 10, false)
		if err != nil {
			t.Fatalf("EndingWith(at): %v", err)
		}

		requireSameNodes(t, "EndingWith(at)", got, []NodeID{at, cat, hat})

		if _, markErr := rig.text.MarkIndexed(rig.graph, cat); markErr != nil {
			t.Fatalf("MarkIndexed(cat): %v", markErr)
		}

		got, err = rig.text.EndingWith(rig.graph, "at", 10, true)
		if err != nil {
			t.Fatalf("EndingWith(at, indexed only): %v", err)
		}

		requireSameNodes(t, "EndingWith(at, indexed only)", got, []NodeID{cat})

		limited, err := rig.text.EndingWith(rig.graph, "at", 2, false)
		if err != nil || len(limited) != 2 {
			t.Fatalf("EndingWith(at, limit 2) = (%v,%v), want two results", limited, err)
		}

		if limited[0] != at {
			t.Fatalf("EndingWith(at) starts with %d, want the shortest string %d", limited[0], at)
		}

		for _, suffix := range []string{"zzz", "ar"} {
			none, noneErr := rig.text.EndingWith(rig.graph, suffix, 10, false)
			if noneErr != nil {
				t.Fatalf("EndingWith(%q): %v", suffix, noneErr)
			}

			if suffix == "zzz" && len(none) != 0 {
				t.Fatalf("EndingWith(zzz) = %v, want none", none)
			}

			if suffix == "ar" {
				requireSameNodes(t, "EndingWith(ar)", none, []NodeID{rig.intern(t, "ar"), car})
			}
		}
	})
}

func TestTextIndexedWithPrefix(t *testing.T) {
	forEachTextRig(t, func(t *testing.T, rig *textRig) {
		cat := rig.internIndexed(t, "cat")
		car := rig.internIndexed(t, "car")
		at := rig.internIndexed(t, "at")

		// Strings that exist only as stepping stones are not indexed.
		rig.intern(t, "cart")

		cases := []struct {
			prefix string
			want   []NodeID
		}{
			{prefix: "ca", want: []NodeID{cat, car}},
			{prefix: "a", want: []NodeID{at}},
			{prefix: "cat", want: []NodeID{cat}},
			{prefix: "", want: []NodeID{cat, car, at}},
			{prefix: "x"},
			{prefix: "cart"},
		}

		for _, tc := range cases {
			got, err := rig.text.IndexedWithPrefix(rig.graph, tc.prefix, 10)
			if err != nil {
				t.Fatalf("IndexedWithPrefix(%q): %v", tc.prefix, err)
			}

			requireSameNodes(t, "IndexedWithPrefix("+tc.prefix+")", got, tc.want)
		}

		if limited, err := rig.text.IndexedWithPrefix(rig.graph, "", 2); err != nil || len(limited) != 2 {
			t.Fatalf("IndexedWithPrefix(limit 2) = (%v,%v), want two results", limited, err)
		}

		if none, err := rig.text.IndexedWithPrefix(rig.graph, "", 0); err != nil || len(none) != 0 {
			t.Fatalf("IndexedWithPrefix(limit 0) = (%v,%v), want none", none, err)
		}

		again, err := rig.text.MarkIndexed(rig.graph, cat)
		if err != nil || again {
			t.Fatalf("MarkIndexed() of an indexed string = (%v,%v), want (false,nil)", again, err)
		}

		if indexed, indexedErr := rig.text.IsIndexed(rig.graph, cat); indexedErr != nil || !indexed {
			t.Fatalf("IsIndexed(cat) = (%v,%v), want (true,nil)", indexed, indexedErr)
		}
	})
}

func TestTextUnmarkIndexedPrunesWhatNothingElseNeeds(t *testing.T) {
	forEachTextRig(t, func(t *testing.T, rig *textRig) {
		cat := rig.internIndexed(t, "cat")
		car := rig.internIndexed(t, "car")

		rig.requireIndexNodes(t, 5) // root, c, ca, cat, car
		rig.requireCount(t, "AllIndexEntries", rig.text.allIndexEntries, 2)

		removed, err := rig.text.UnmarkIndexed(rig.graph, cat)
		if err != nil || !removed {
			t.Fatalf("UnmarkIndexed(cat) = (%v,%v), want (true,nil)", removed, err)
		}

		rig.requireIndexNodes(t, 4) // "cat" went, "ca" stays for "car"
		rig.requireCount(t, "AllIndexEntries", rig.text.allIndexEntries, 1)
		rig.requireText(t, cat, "cat") // the string itself stays

		got, err := rig.text.IndexedWithPrefix(rig.graph, "ca", 10)
		if err != nil {
			t.Fatalf("IndexedWithPrefix(ca): %v", err)
		}

		requireSameNodes(t, "IndexedWithPrefix(ca)", got, []NodeID{car})

		if again, againErr := rig.text.UnmarkIndexed(rig.graph, cat); againErr != nil || again {
			t.Fatalf("second UnmarkIndexed(cat) = (%v,%v), want (false,nil)", again, againErr)
		}

		if _, unmarkErr := rig.text.UnmarkIndexed(rig.graph, car); unmarkErr != nil {
			t.Fatalf("UnmarkIndexed(car): %v", unmarkErr)
		}

		rig.requireIndexNodes(t, 1) // only the root is left

		// It can be indexed again.
		if marked, markErr := rig.text.MarkIndexed(rig.graph, cat); markErr != nil || !marked {
			t.Fatalf("MarkIndexed(cat) after unmarking = (%v,%v), want (true,nil)", marked, markErr)
		}

		rig.requireIndexNodes(t, 4) // root, c, ca, cat

		// An indexed string with a longer one beneath it keeps its path.
		rig.internIndexed(t, "ca")

		if _, unmarkErr := rig.text.UnmarkIndexed(rig.graph, rig.intern(t, "ca")); unmarkErr != nil {
			t.Fatalf("UnmarkIndexed(ca): %v", unmarkErr)
		}

		rig.requireIndexNodes(t, 4)

		got, err = rig.text.IndexedWithPrefix(rig.graph, "ca", 10)
		if err != nil {
			t.Fatalf("IndexedWithPrefix(ca): %v", err)
		}

		requireSameNodes(t, "IndexedWithPrefix(ca) with ca unmarked", got, []NodeID{cat})
	})
}

// TestTextCheckerDeclinesEachViolationAtCommitTime corrupts the structure in
// one transaction per case and checks the commit is declined and rolled back.
// The closures read only through tx: the char slots are looked up before the
// transaction, since a bare *Graph panics on a guarded read from inside one.
func TestTextCheckerDeclinesEachViolationAtCommitTime(t *testing.T) {
	cases := []struct {
		name    string
		want    error
		corrupt func(tx Tx, rig *textRig, cat NodeID, c charSlots) error
	}{
		{
			name: "two strings for the same tail and char",
			want: ErrAmbiguousPointerMetadata,
			corrupt: func(tx Tx, rig *textRig, _ NodeID, c charSlots) error {
				at, found, err := rig.text.Lookup(tx, "at")
				if err != nil || !found {
					return ErrInvalidTextStructure
				}

				duplicate, createErr := createTaggedNodeTx(tx, rig.text.allStrings)
				if createErr != nil {
					return createErr
				}

				if linkErr := addRelationshipTx(tx, at, duplicate); linkErr != nil {
					return linkErr
				}

				return addRelationshipTx(tx, c.startsWith, duplicate)
			},
		},
		{
			name: "a string with no first char",
			want: ErrInvalidTextStructure,
			corrupt: func(tx Tx, rig *textRig, _ NodeID, _ charSlots) error {
				orphan, createErr := createTaggedNodeTx(tx, rig.text.allStrings)
				if createErr != nil {
					return createErr
				}

				return addRelationshipTx(tx, rig.text.emptyString, orphan)
			},
		},
		{
			name: "a char that is also a string",
			want: ErrInvalidTextStructure,
			corrupt: func(tx Tx, rig *textRig, _ NodeID, c charSlots) error {
				return addRelationshipTx(tx, rig.text.allStrings, c.node)
			},
		},
		{
			name: "the empty string gets a tail",
			want: ErrInvalidTextStructure,
			corrupt: func(tx Tx, rig *textRig, cat NodeID, _ charSlots) error {
				return addRelationshipTx(tx, cat, rig.text.emptyString)
			},
		},
		{
			name: "indexed with no entry",
			want: ErrInvalidTextStructure,
			corrupt: func(tx Tx, rig *textRig, cat NodeID, _ charSlots) error {
				return addRelationshipTx(tx, rig.text.allIndexedStrings, cat)
			},
		},
		{
			name: "an index node with no step slot",
			want: ErrInvalidTextStructure,
			corrupt: func(tx Tx, rig *textRig, _ NodeID, _ charSlots) error {
				stray, createErr := createTaggedNodeTx(tx, rig.text.allIndexNodes)
				if createErr != nil {
					return createErr
				}

				return addRelationshipTx(tx, rig.text.indexRoot, stray)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			forEachTextRig(t, func(t *testing.T, rig *textRig) {
				cat := rig.intern(t, "cat")
				c := rig.charSlots(t, 'c')

				err := rig.graph.Transact(func(tx Tx) error {
					return tc.corrupt(tx, rig, cat, c)
				})
				if !errors.Is(err, tc.want) {
					t.Fatalf("Transact() error = %v, want %v", err, tc.want)
				}

				rig.requireStrings(t, 4)
				rig.requireIndexNodes(t, 1)
				rig.requireText(t, cat, "cat")
			})
		})
	}
}

func (rig *textRig) deleteString(t *testing.T, node NodeID) {
	t.Helper()

	if err := rig.text.DeleteString(rig.graph, node); err != nil {
		t.Fatalf("DeleteString(%d): %v", node, err)
	}
}

func (rig *textRig) requireInterned(t *testing.T, text string, want bool) {
	t.Helper()

	_, found, err := rig.text.Lookup(rig.graph, text)
	if err != nil || found != want {
		t.Fatalf("Lookup(%q) = (found %v, %v), want found %v", text, found, err, want)
	}
}

// requireInUse checks that err is DeleteString's refusal of a string that is
// still needed, and that it is also an ErrNodeNotEmpty.
func requireInUse(t *testing.T, label string, err error) {
	t.Helper()

	if !errors.Is(err, ErrStringInUse) || !errors.Is(err, ErrNodeNotEmpty) {
		t.Fatalf("%s error = %v, want %v and %v", label, err, ErrStringInUse, ErrNodeNotEmpty)
	}
}

func TestTextDeleteStringRemovesOnlyThatString(t *testing.T) {
	forEachTextRig(t, func(t *testing.T, rig *textRig) {
		cat := rig.intern(t, "cat")
		at := rig.intern(t, "at")

		rig.requireStrings(t, 4) // "", "t", "at", "cat"

		rig.deleteString(t, cat)

		rig.requireStrings(t, 3)
		rig.requireInterned(t, "cat", false)
		rig.requireInterned(t, "at", true)
		rig.requireText(t, at, "at")

		if _, err := rig.text.Text(rig.graph, cat); !errors.Is(err, ErrNodeNotFound) {
			t.Fatalf("Text(deleted) error = %v, want %v", err, ErrNodeNotFound)
		}

		if err := rig.text.DeleteString(rig.graph, cat); !errors.Is(err, ErrNodeNotFound) {
			t.Fatalf("second DeleteString(cat) error = %v, want %v", err, ErrNodeNotFound)
		}

		// The suffixes stay until deleted themselves.
		rig.deleteString(t, at)
		rig.deleteString(t, rig.intern(t, "t"))
		rig.requireStrings(t, 1)

		// The same text can be interned again.
		rig.requireText(t, rig.intern(t, "cat"), "cat")
		rig.requireStrings(t, 4)
	})
}

func TestTextDeleteStringRefusesAStringThatIsStillExtended(t *testing.T) {
	forEachTextRig(t, func(t *testing.T, rig *textRig) {
		cat := rig.intern(t, "cat")
		at := rig.intern(t, "at")

		requireInUse(t, "DeleteString(at)", rig.text.DeleteString(rig.graph, at))

		rig.requireStrings(t, 4)
		rig.requireText(t, cat, "cat")
		rig.requireText(t, at, "at")

		// Once nothing extends it, it goes.
		rig.deleteString(t, cat)
		rig.deleteString(t, at)
		rig.requireStrings(t, 2) // "" and "t"
	})
}

func TestTextDeleteStringLeavesThePrefixIndexToo(t *testing.T) {
	forEachTextRig(t, func(t *testing.T, rig *textRig) {
		cat := rig.internIndexed(t, "cat")
		car := rig.internIndexed(t, "car")

		rig.deleteString(t, cat)

		rig.requireIndexNodes(t, 4) // root, c, ca, car
		rig.requireCount(t, "AllIndexEntries", rig.text.allIndexEntries, 1)
		rig.requireCount(t, "AllIndexedStrings", rig.text.allIndexedStrings, 1)
		rig.requireInterned(t, "cat", false)

		got, err := rig.text.IndexedWithPrefix(rig.graph, "ca", 10)
		if err != nil {
			t.Fatalf("IndexedWithPrefix(ca): %v", err)
		}

		requireSameNodes(t, "IndexedWithPrefix(ca)", got, []NodeID{car})

		rig.deleteString(t, car)
		rig.requireIndexNodes(t, 1) // only the root is left

		if verifyErr := VerifyAll(rig.graph, 5); verifyErr != nil {
			t.Fatalf("VerifyAll(): %v", verifyErr)
		}
	})
}

// TestTextDeleteStringRefusedKeepsTheIndexEntry: the unmarking DeleteString
// does first is rolled back with the refusal.
func TestTextDeleteStringRefusedKeepsTheIndexEntry(t *testing.T) {
	forEachTextRig(t, func(t *testing.T, rig *textRig) {
		at := rig.internIndexed(t, "at")
		rig.internIndexed(t, "cat")

		requireInUse(t, "DeleteString(at)", rig.text.DeleteString(rig.graph, at))

		if indexed, err := rig.text.IsIndexed(rig.graph, at); err != nil || !indexed {
			t.Fatalf("IsIndexed(at) after the refusal = (%v,%v), want (true,nil)", indexed, err)
		}

		rig.requireIndexNodes(t, 6) // root, a, at, c, ca, cat
		rig.requireCount(t, "AllIndexEntries", rig.text.allIndexEntries, 2)

		got, err := rig.text.IndexedWithPrefix(rig.graph, "a", 10)
		if err != nil {
			t.Fatalf("IndexedWithPrefix(a): %v", err)
		}

		requireSameNodes(t, "IndexedWithPrefix(a)", got, []NodeID{at})
	})
}

func TestTextDeleteStringRejectsWhatIsNotADeletableString(t *testing.T) {
	const nonexistent NodeID = 999999

	forEachTextRig(t, func(t *testing.T, rig *textRig) {
		rig.intern(t, "a")

		plain := mustCreateNode(t, rig.graph)
		char := rig.charSlots(t, 'a').node
		nodesBefore := len(mustFindNodes(t, rig.graph))

		cases := []struct {
			name string
			node NodeID
			want error
		}{
			{name: "the empty string", node: rig.text.EmptyString(), want: ErrCannotDeleteEmptyString},
			{name: "a node that is not a string", node: plain, want: ErrNotString},
			{name: "a char", node: char, want: ErrNotString},
			{name: "a node that does not exist", node: nonexistent, want: ErrNodeNotFound},
		}

		for _, tc := range cases {
			if err := rig.text.DeleteString(rig.graph, tc.node); !errors.Is(err, tc.want) {
				t.Fatalf("%s: DeleteString() error = %v, want %v", tc.name, err, tc.want)
			}
		}

		if got := len(mustFindNodes(t, rig.graph)); got != nodesBefore {
			t.Fatalf("%d nodes after the refused calls, want %d", got, nodesBefore)
		}

		rig.requireStrings(t, 2) // "" and "a"
	})
}

func TestTextDeleteStringRollsBackWithTheEnclosingTransaction(t *testing.T) {
	forEachTextRig(t, func(t *testing.T, rig *textRig) {
		cat := rig.internIndexed(t, "cat")
		errOuter := errors.New("outer failure")

		err := rig.graph.Transact(func(tx Tx) error {
			if deleteErr := rig.text.DeleteString(tx, cat); deleteErr != nil {
				return deleteErr
			}

			return errOuter
		})
		if !errors.Is(err, errOuter) {
			t.Fatalf("Transact() error = %v, want %v", err, errOuter)
		}

		rig.requireStrings(t, 4)
		rig.requireText(t, cat, "cat")
		rig.requireInterned(t, "cat", true)

		if indexed, indexedErr := rig.text.IsIndexed(rig.graph, cat); indexedErr != nil || !indexed {
			t.Fatalf("IsIndexed(cat) after the rollback = (%v,%v), want (true,nil)", indexed, indexedErr)
		}
	})
}

// reclaimAll runs ReclaimStrings page by page, limit per call, until Done. It
// returns how many strings were deleted in all and the number of calls, and
// checks that every call made progress.
func (rig *textRig) reclaimAll(t *testing.T, limit int) (int, int) {
	t.Helper()

	var (
		total  int
		cursor TextCursor
	)

	for calls := 1; calls <= 1000; calls++ {
		page, err := rig.text.ReclaimStrings(rig.graph, cursor, limit)
		if err != nil {
			t.Fatalf("ReclaimStrings(limit %d): %v", limit, err)
		}

		total += page.Deleted

		if page.Done {
			return total, calls
		}

		if page.Cursor == cursor {
			t.Fatalf("ReclaimStrings(limit %d) made no progress from %v", limit, cursor)
		}

		cursor = page.Cursor
	}

	t.Fatalf("ReclaimStrings(limit %d) was not Done after 1000 calls", limit)

	return 0, 0
}

// reference makes a plain node that points at str, the way a list element, a
// note or a set member refers to text, and returns it.
func (rig *textRig) reference(t *testing.T, str NodeID) NodeID {
	t.Helper()

	ref := mustCreateNode(t, rig.graph)

	if _, err := rig.graph.AddRelationship(ref, str); err != nil {
		t.Fatalf("AddRelationship(%d,%d): %v", ref, str, err)
	}

	return ref
}

func (rig *textRig) requireVerified(t *testing.T) {
	t.Helper()

	if err := VerifyAll(rig.graph, 5); err != nil {
		t.Fatalf("VerifyAll(): %v", err)
	}
}

func TestTextReclaimStringsDeletesTheSteppingStonesDeleteStringLeft(t *testing.T) {
	forEachTextRig(t, func(t *testing.T, rig *textRig) {
		cat := rig.intern(t, "cat")
		rig.deleteString(t, cat)

		rig.requireStrings(t, 3) // "", "t", "at"

		if deleted, _ := rig.reclaimAll(t, 100); deleted != 2 {
			t.Fatalf("ReclaimStrings() deleted %d, want 2", deleted)
		}

		rig.requireStrings(t, 1)
		rig.requireInterned(t, "at", false)
		rig.requireInterned(t, "t", false)

		// The empty string and the char nodes are never reclaimed.
		rig.requireText(t, rig.text.EmptyString(), "")
		rig.charSlots(t, 'c')

		if deleted, _ := rig.reclaimAll(t, 100); deleted != 0 {
			t.Fatalf("second ReclaimStrings() deleted %d, want 0", deleted)
		}

		rig.requireVerified(t)
	})
}

func TestTextReclaimStringsKeepsWhatIsStillNeeded(t *testing.T) {
	forEachTextRig(t, func(t *testing.T, rig *textRig) {
		hat := rig.intern(t, "hat")
		rig.intern(t, "cat")
		car := rig.internIndexed(t, "car")
		ref := rig.reference(t, hat)

		rig.requireStrings(t, 8) // "", t, at, hat, cat, r, ar, car

		// Only "cat" goes: "hat" is referred to, "car" is marked indexed, and
		// "at", "r" and "ar" each still have a string that extends them.
		if deleted, _ := rig.reclaimAll(t, 100); deleted != 1 {
			t.Fatalf("ReclaimStrings() deleted %d, want 1", deleted)
		}

		rig.requireStrings(t, 7)
		rig.requireInterned(t, "cat", false)
		rig.requireInterned(t, "hat", true)
		rig.requireInterned(t, "car", true)
		rig.requireVerified(t)

		got, err := rig.text.IndexedWithPrefix(rig.graph, "ca", 10)
		if err != nil {
			t.Fatalf("IndexedWithPrefix(ca): %v", err)
		}

		requireSameNodes(t, "IndexedWithPrefix(ca)", got, []NodeID{car})

		// Once nothing refers to "hat", it goes, and the tails only it kept.
		if _, removeErr := rig.graph.RemoveRelationship(ref, hat); removeErr != nil {
			t.Fatalf("RemoveRelationship(%d,%d): %v", ref, hat, removeErr)
		}

		if deleted, _ := rig.reclaimAll(t, 100); deleted != 3 {
			t.Fatalf("ReclaimStrings() after dropping the reference deleted %d, want 3", deleted)
		}

		rig.requireStrings(t, 4) // "", r, ar, car
		rig.requireInterned(t, "hat", false)
		rig.requireInterned(t, "at", false)
		rig.requireInterned(t, "car", true)

		// Unmarked, "car" and its chain go too.
		if _, unmarkErr := rig.text.UnmarkIndexed(rig.graph, car); unmarkErr != nil {
			t.Fatalf("UnmarkIndexed(car): %v", unmarkErr)
		}

		if deleted, _ := rig.reclaimAll(t, 100); deleted != 3 {
			t.Fatalf("ReclaimStrings() after unmarking deleted %d, want 3", deleted)
		}

		rig.requireStrings(t, 1)
		rig.requireIndexNodes(t, 1)
		rig.requireVerified(t)
	})
}

// TestTextReclaimStringsReclaimsAWholeChainInOnePass: the six strings that
// "abcdef" needed go in one call, not one per pass.
func TestTextReclaimStringsReclaimsAWholeChainInOnePass(t *testing.T) {
	forEachTextRig(t, func(t *testing.T, rig *textRig) {
		rig.intern(t, "abcdef")
		rig.requireStrings(t, 7)

		deleted, calls := rig.reclaimAll(t, 100)
		if deleted != 6 || calls != 1 {
			t.Fatalf("ReclaimStrings() = (%d deleted, %d calls), want (6, 1)", deleted, calls)
		}

		rig.requireStrings(t, 1)
		rig.requireVerified(t)
	})
}

// TestTextReclaimStringsFinishesTheChainItIsIn: a call that reaches its limit
// still finishes the chain it is deleting, since nothing would revisit the
// tails it skipped.
func TestTextReclaimStringsFinishesTheChainItIsIn(t *testing.T) {
	forEachTextRig(t, func(t *testing.T, rig *textRig) {
		rig.intern(t, "abcdef")

		// "", f, ef, def: four strings examined, none of them a leaf.
		first, err := rig.text.ReclaimStrings(rig.graph, TextCursor{}, 4)
		if err != nil || first.Deleted != 0 || first.Done || !first.Cursor.HasAfter {
			t.Fatalf("first call = (%+v,%v), want nothing deleted, not Done, a cursor", first, err)
		}

		rig.requireStrings(t, 7)

		// cdef, bcdef, abcdef: the leaf is reached, and its whole chain goes
		// although that is more than the limit.
		second, err := rig.text.ReclaimStrings(rig.graph, first.Cursor, 4)
		if err != nil || second.Deleted != 6 || !second.Done {
			t.Fatalf("second call = (%+v,%v), want 6 deleted and Done", second, err)
		}

		rig.requireStrings(t, 1)
		rig.requireVerified(t)
	})
}

func TestTextReclaimStringsGivesTheSameResultAtEveryLimit(t *testing.T) {
	forEachTextRig(t, func(t *testing.T, rig *textRig) {
		hat := rig.internIndexed(t, "hat")

		for _, limit := range []int{1, 2, 3, 100} {
			rig.intern(t, "cat")
			rig.intern(t, "car")
			rig.intern(t, "dog")

			rig.requireStrings(t, 11) // "", t, at, hat, cat, r, ar, car, g, og, dog

			deleted, calls := rig.reclaimAll(t, limit)
			if deleted != 7 {
				t.Fatalf("limit %d: ReclaimStrings() deleted %d, want 7", limit, deleted)
			}

			if limit == 100 && calls != 1 {
				t.Fatalf("limit %d: ReclaimStrings() took %d calls, want 1", limit, calls)
			}

			rig.requireStrings(t, 4) // "", t, at, hat: "hat" is marked, so its tails stay
			rig.requireText(t, hat, "hat")
			rig.requireVerified(t)
		}
	})
}

func TestTextReclaimStringsHonoursANonPositiveLimit(t *testing.T) {
	forEachTextRig(t, func(t *testing.T, rig *textRig) {
		rig.intern(t, "cat")

		from := TextCursor{After: 5, HasAfter: true}

		for _, limit := range []int{0, -1} {
			page, err := rig.text.ReclaimStrings(rig.graph, from, limit)
			if err != nil || page.Deleted != 0 || page.Done || page.Cursor != from {
				t.Fatalf("limit %d = (%+v,%v), want nothing, not Done, the cursor unchanged", limit, page, err)
			}
		}

		rig.requireStrings(t, 4)
	})
}

func TestTextReclaimStringsRollsBackWithTheEnclosingTransaction(t *testing.T) {
	forEachTextRig(t, func(t *testing.T, rig *textRig) {
		cat := rig.intern(t, "cat")
		errOuter := errors.New("outer failure")

		var inside ReclaimPage

		err := rig.graph.Transact(func(tx Tx) error {
			page, reclaimErr := rig.text.ReclaimStrings(tx, TextCursor{}, 100)
			if reclaimErr != nil {
				return reclaimErr
			}

			inside = page

			return errOuter
		})
		if !errors.Is(err, errOuter) {
			t.Fatalf("Transact() error = %v, want %v", err, errOuter)
		}

		if inside.Deleted != 3 {
			t.Fatalf("ReclaimStrings() inside the transaction deleted %d, want 3", inside.Deleted)
		}

		rig.requireStrings(t, 4)
		rig.requireText(t, cat, "cat")
		rig.requireInterned(t, "at", true)
		rig.requireVerified(t)
	})
}

// collectContaining runs StringsContaining page by page, asking for limit
// matches per call, until Done. It returns every match and the number of
// calls, and checks that the matches are ascending and that every call made
// progress.
func (rig *textRig) collectContaining(t *testing.T, substr string, limit int, indexedOnly bool) ([]NodeID, int) {
	t.Helper()

	var (
		matches []NodeID
		cursor  TextCursor
	)

	for calls := 1; calls <= 1000; calls++ {
		page, err := rig.text.StringsContaining(rig.graph, substr, cursor, limit, indexedOnly)
		if err != nil {
			t.Fatalf("StringsContaining(%q): %v", substr, err)
		}

		matches = append(matches, page.Matches...)

		if page.Done {
			if !slices.IsSorted(matches) {
				t.Fatalf("StringsContaining(%q) matches %v are not ascending", substr, matches)
			}

			return matches, calls
		}

		if page.Cursor == cursor {
			t.Fatalf("StringsContaining(%q) made no progress from %v", substr, cursor)
		}

		cursor = page.Cursor
	}

	t.Fatalf("StringsContaining(%q) was not Done after 1000 calls", substr)

	return nil, 0
}

func TestTextStringsContainingFindsEveryStringWithTheSubstring(t *testing.T) {
	forEachTextRig(t, func(t *testing.T, rig *textRig) {
		rig.intern(t, "cat")
		rig.intern(t, "hat")
		rig.intern(t, "car")

		// "", "t", "at", "cat", "hat", "r", "ar", "car": the stepping stones
		// exist already, so interning them again creates nothing.
		rig.requireStrings(t, 8)

		node := func(text string) NodeID { return rig.intern(t, text) }

		cases := []struct {
			substr string
			want   []NodeID
		}{
			{substr: "at", want: []NodeID{node("at"), node("cat"), node("hat")}},
			{substr: "a", want: []NodeID{node("at"), node("cat"), node("hat"), node("ar"), node("car")}},
			{substr: "ca", want: []NodeID{node("cat"), node("car")}},
			{substr: "ar", want: []NodeID{node("ar"), node("car")}},
			{substr: "t", want: []NodeID{node("t"), node("at"), node("cat"), node("hat")}},
			{substr: "cat", want: []NodeID{node("cat")}},
			{substr: "catx"},
			{substr: "zzz"},
			{substr: "", want: []NodeID{
				rig.text.EmptyString(), node("t"), node("at"), node("cat"), node("hat"),
				node("r"), node("ar"), node("car"),
			}},
		}

		for _, tc := range cases {
			got, _ := rig.collectContaining(t, tc.substr, 100, false)

			requireSameNodes(t, "StringsContaining("+tc.substr+")", got, tc.want)
		}

		// Searching creates nothing, not even the char of a text never used.
		rig.requireStrings(t, 8)

		if _, ok := rig.names.Lookup(charName('z')); ok {
			t.Fatal("StringsContaining() created the char node of 'z'")
		}
	})
}

func TestTextStringsContainingFindsSubstringsInTheMiddleAndOverlapping(t *testing.T) {
	forEachTextRig(t, func(t *testing.T, rig *textRig) {
		rig.intern(t, "banana")

		// "", "a", "na", "ana", "nana", "anana", "banana".
		node := func(text string) NodeID { return rig.intern(t, text) }

		cases := []struct {
			substr string
			want   []NodeID
		}{
			{substr: "nan", want: []NodeID{node("nana"), node("anana"), node("banana")}},
			{substr: "ana", want: []NodeID{node("ana"), node("nana"), node("anana"), node("banana")}},
			// Two occurrences of "ana" overlap in "anana"; "anan" is only in the
			// two strings long enough to hold it.
			{substr: "anan", want: []NodeID{node("anana"), node("banana")}},
			{substr: "banana", want: []NodeID{node("banana")}},
			{substr: "bananas"},
		}

		for _, tc := range cases {
			got, _ := rig.collectContaining(t, tc.substr, 100, false)

			requireSameNodes(t, "StringsContaining("+tc.substr+")", got, tc.want)
		}
	})
}

// TestTextStringsContainingMatchesWholeRunesNotBytes checks multi-byte text
// against a model: every suffix of the text, tested with strings.Contains.
func TestTextStringsContainingMatchesWholeRunesNotBytes(t *testing.T) {
	forEachTextRig(t, func(t *testing.T, rig *textRig) {
		const text = "héllo 日本!"

		rig.intern(t, text)

		runes := []rune(text)

		for _, substr := range []string{"日本", "é", "o 日", "本!"} {
			var want []NodeID

			for i := range runes {
				suffix := string(runes[i:])
				if strings.Contains(suffix, substr) {
					want = append(want, rig.intern(t, suffix))
				}
			}

			got, _ := rig.collectContaining(t, substr, 100, false)

			requireSameNodes(t, "StringsContaining("+substr+")", got, want)
		}
	})
}

func TestTextStringsContainingPagesWithAResumeCursor(t *testing.T) {
	forEachTextRig(t, func(t *testing.T, rig *textRig) {
		rig.intern(t, "cat")
		rig.intern(t, "hat")
		rig.intern(t, "car")

		all, allCalls := rig.collectContaining(t, "a", 100, false)
		if len(all) != 5 || allCalls != 1 {
			t.Fatalf("one big page = (%v, %d calls), want 5 matches in 1 call", all, allCalls)
		}

		for _, limit := range []int{1, 2, 3} {
			got, calls := rig.collectContaining(t, "a", limit, false)

			if !reflect.DeepEqual(got, all) {
				t.Fatalf("limit %d found %v, want %v in the same order", limit, got, all)
			}

			if minCalls := (len(all) + limit - 1) / limit; calls < minCalls {
				t.Fatalf("limit %d took %d calls, want at least %d", limit, calls, minCalls)
			}
		}

		// A page never holds more than limit matches, and a cursor past the
		// last string finds nothing and is Done.
		page, err := rig.text.StringsContaining(rig.graph, "a", TextCursor{}, 2, false)
		if err != nil || len(page.Matches) != 2 || page.Done {
			t.Fatalf("first page = (%+v,%v), want two matches and not Done", page, err)
		}

		last := all[len(all)-1]

		end, err := rig.text.StringsContaining(rig.graph, "a", TextCursor{After: last, HasAfter: true}, 2, false)
		if err != nil || len(end.Matches) != 0 || !end.Done {
			t.Fatalf("page after the last string = (%+v,%v), want no matches and Done", end, err)
		}
	})
}

func TestTextStringsContainingBoundsTheWorkOfOneCall(t *testing.T) {
	forEachTextRig(t, func(t *testing.T, rig *textRig) {
		rig.intern(t, "cat")
		rig.intern(t, "hat")
		rig.intern(t, "car")
		rig.requireStrings(t, 8)

		previous := containingScanLimit
		containingScanLimit = 3

		t.Cleanup(func() { containingScanLimit = previous })

		// Nothing matches, yet one call stops after 3 of the 8 strings and says
		// the scan is not over.
		page, err := rig.text.StringsContaining(rig.graph, "zzz", TextCursor{}, 10, false)
		if err != nil || len(page.Matches) != 0 || page.Done || !page.Cursor.HasAfter {
			t.Fatalf("first call = (%+v,%v), want no matches, not Done, a cursor", page, err)
		}

		none, calls := rig.collectContaining(t, "zzz", 10, false)
		if len(none) != 0 || calls != 3 {
			t.Fatalf("scan for a missing substring = (%v, %d calls), want no matches in 3 calls", none, calls)
		}

		// The bound loses no match.
		got, _ := rig.collectContaining(t, "a", 10, false)
		if len(got) != 5 {
			t.Fatalf("StringsContaining(a) under a scan limit of 3 found %d, want 5", len(got))
		}
	})
}

func TestTextStringsContainingIndexedOnlyScansTheMarkedStrings(t *testing.T) {
	forEachTextRig(t, func(t *testing.T, rig *textRig) {
		cat := rig.internIndexed(t, "cat")
		hat := rig.intern(t, "hat")
		at := rig.intern(t, "at")

		got, _ := rig.collectContaining(t, "at", 10, true)
		requireSameNodes(t, "indexed only", got, []NodeID{cat})

		got, _ = rig.collectContaining(t, "at", 10, false)
		requireSameNodes(t, "every string", got, []NodeID{at, cat, hat})

		if _, err := rig.text.MarkIndexed(rig.graph, at); err != nil {
			t.Fatalf("MarkIndexed(at): %v", err)
		}

		got, _ = rig.collectContaining(t, "at", 10, true)
		requireSameNodes(t, "indexed only after marking at", got, []NodeID{at, cat})

		if _, err := rig.text.UnmarkIndexed(rig.graph, cat); err != nil {
			t.Fatalf("UnmarkIndexed(cat): %v", err)
		}

		got, _ = rig.collectContaining(t, "at", 10, true)
		requireSameNodes(t, "indexed only after unmarking cat", got, []NodeID{at})
	})
}

func TestTextStringsContainingRejectsBadInputAndHonoursANonPositiveLimit(t *testing.T) {
	forEachTextRig(t, func(t *testing.T, rig *textRig) {
		rig.intern(t, "cat")

		if _, err := rig.text.StringsContaining(rig.graph, "\xff", TextCursor{}, 10, false); !errors.Is(err, ErrInvalidText) {
			t.Fatalf("StringsContaining(invalid UTF-8) error = %v, want %v", err, ErrInvalidText)
		}

		tooLong := strings.Repeat("a", MaxTextRunes+1)
		if _, err := rig.text.StringsContaining(rig.graph, tooLong, TextCursor{}, 10, false); !errors.Is(err, ErrTextTooLong) {
			t.Fatalf("StringsContaining(too long) error = %v, want %v", err, ErrTextTooLong)
		}

		from := TextCursor{After: 5, HasAfter: true}

		for _, limit := range []int{0, -1} {
			page, err := rig.text.StringsContaining(rig.graph, "a", from, limit, false)
			if err != nil || len(page.Matches) != 0 || page.Done || page.Cursor != from {
				t.Fatalf("limit %d = (%+v,%v), want nothing, not Done, the cursor unchanged", limit, page, err)
			}
		}
	})
}

// TestTextStringsContainingSeesTheEnclosingTransaction: inside a transaction
// the scan sees the transaction's own writes, and they are gone after it
// rolls back.
func TestTextStringsContainingSeesTheEnclosingTransaction(t *testing.T) {
	forEachTextRig(t, func(t *testing.T, rig *textRig) {
		rig.intern(t, "cat")

		errOuter := errors.New("outer failure")

		var (
			dog    NodeID
			inside []NodeID
		)

		err := rig.graph.Transact(func(tx Tx) error {
			node, internErr := rig.text.Intern(tx, "dog")
			if internErr != nil {
				return internErr
			}

			dog = node

			page, searchErr := rig.text.StringsContaining(tx, "do", TextCursor{}, 10, false)
			if searchErr != nil {
				return searchErr
			}

			inside = page.Matches

			return errOuter
		})
		if !errors.Is(err, errOuter) {
			t.Fatalf("Transact() error = %v, want %v", err, errOuter)
		}

		requireSameNodes(t, "matches inside the transaction", inside, []NodeID{dog})

		after, _ := rig.collectContaining(t, "do", 10, false)
		if len(after) != 0 {
			t.Fatalf("StringsContaining(do) after the rollback = %v, want none", after)
		}
	})
}

func TestNewTextRegistryRequiresEveryName(t *testing.T) {
	var g Graph

	names := NewNameRegistry(&g)

	ids, err := names.BootstrapNames(&g, FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	delete(ids, NameIndexRoot)

	if _, newErr := NewTextRegistry(&g, names, ids); !errors.Is(newErr, ErrNameNotFound) {
		t.Fatalf("NewTextRegistry() error = %v, want %v", newErr, ErrNameNotFound)
	}
}

func TestTagFoundationalRequiresTheNames(t *testing.T) {
	if err := TagFoundational(&Graph{}, map[string]NodeID{}); !errors.Is(err, ErrNameNotFound) {
		t.Fatalf("TagFoundational() error = %v, want %v", err, ErrNameNotFound)
	}
}

// TestHostTagsFoundationalNamesAndKeepsTextAcrossARestart: the host tags
// every foundational name and ROOT with AllFoundational, serves the text
// layer, and a restart (whose startup sweep runs the text Checker over every
// node) finds the same nodes again.
func TestHostTagsFoundationalNamesAndKeepsTextAcrossARestart(t *testing.T) {
	path := boltTestPath(t)
	first := openTestHost(t, path, nil)

	tag, ok := first.names.Lookup(NameAllFoundational)
	if !ok {
		t.Fatal("AllFoundational is not bound")
	}

	for _, name := range FoundationalNames {
		if name == NameAllFoundational {
			continue
		}

		id, bound := first.names.Lookup(name)
		if !bound {
			t.Fatalf("foundational name %q is not bound", name)
		}

		if !mustHasRelationship(t, first.Graph(), tag, id) {
			t.Fatalf("%q is not tagged AllFoundational", name)
		}
	}

	if !mustHasRelationship(t, first.Graph(), tag, first.root) {
		t.Fatal("ROOT is not tagged AllFoundational")
	}

	ids, err := first.names.BootstrapNames(first.Graph(), FoundationalNames)
	if err != nil {
		t.Fatalf("BootstrapNames(): %v", err)
	}

	if tagErr := TagFoundational(first.Graph(), ids, first.root); tagErr != nil {
		t.Fatalf("second TagFoundational(): %v", tagErr)
	}

	text := first.Registries().Text

	hello, err := text.InternIndexed(first.Graph(), "héllo")
	if err != nil {
		t.Fatalf("InternIndexed(): %v", err)
	}

	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("Close(): %v", closeErr)
	}

	second := openTestHost(t, path, nil)

	found, ok, err := second.Registries().Text.Lookup(second.Graph(), "héllo")
	if err != nil || !ok || found != hello {
		t.Fatalf("Lookup() after restart = (%d,%v,%v), want (%d,true,nil)", found, ok, err, hello)
	}

	got, err := second.Registries().Text.Text(second.Graph(), hello)
	if err != nil || got != "héllo" {
		t.Fatalf("Text() after restart = (%q,%v), want (\"héllo\",nil)", got, err)
	}

	matches, err := second.Registries().Text.IndexedWithPrefix(second.Graph(), "hé", 5)
	if err != nil || len(matches) != 1 || matches[0] != hello {
		t.Fatalf("IndexedWithPrefix() after restart = (%v,%v), want just %d", matches, err, hello)
	}
}
