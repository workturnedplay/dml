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
