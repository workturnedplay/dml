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
	"maps"
	"slices"
	"strings"
	"testing"
)

// noteRig is a NoteRegistry over one backend, with the registries it is built
// on. The closures the tests hand to Transact read only through tx and never
// call t.Fatalf, since on a GraphActor they run on the actor's goroutine.
type noteRig struct {
	graph testGraph
	ids   map[string]NodeID
	text  *TextRegistry
	notes *NoteRegistry
}

// newNoteParts builds everything a NoteRegistry needs except the registry
// itself, so a test can also exercise its constructor.
func newNoteParts(t *testing.T, graph testGraph) *noteRig {
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

	return &noteRig{graph: graph, ids: ids, text: text}
}

func newNoteRig(t *testing.T, graph testGraph) *noteRig {
	t.Helper()

	rig := newNoteParts(t, graph)

	notes, err := NewNoteRegistry(graph, rig.text, rig.ids)
	if err != nil {
		t.Fatalf("NewNoteRegistry(): %v", err)
	}

	rig.notes = notes

	return rig
}

// forEachNoteRig runs body once per backend, each on a fresh graph.
func forEachNoteRig(t *testing.T, body func(t *testing.T, rig *noteRig)) {
	t.Helper()

	for _, backend := range testBackends() {
		t.Run(backend.name, func(t *testing.T) {
			body(t, newNoteRig(t, backend.open(t)))
		})
	}
}

// add makes a note about subject with text, failing the test on an error.
func (rig *noteRig) add(t *testing.T, subject NodeID, text string) NodeID {
	t.Helper()

	note, err := rig.notes.AddNote(rig.graph, subject, text)
	if err != nil {
		t.Fatalf("AddNote(%d, %q): %v", subject, text, err)
	}

	return note
}

func (rig *noteRig) requireText(t *testing.T, note NodeID, want string, wantHas bool) {
	t.Helper()

	got, has, err := rig.notes.Text(rig.graph, note)
	if err != nil || has != wantHas || got != want {
		t.Fatalf("Text(%d) = (%q,%v,%v), want (%q,%v,nil)", note, got, has, err, want, wantHas)
	}
}

// requireNodes fails unless got holds exactly the nodes of want, in any order.
func requireNodes(t *testing.T, label string, got, want []NodeID) {
	t.Helper()

	if !slices.Equal(sortedNodeIDs(got), sortedNodeIDs(want)) {
		t.Fatalf("%s = %v, want %v (in some order)", label, got, want)
	}
}

func (rig *noteRig) requireNotes(t *testing.T, subject NodeID, want ...NodeID) {
	t.Helper()

	got, err := rig.notes.Notes(rig.graph, subject)
	if err != nil {
		t.Fatalf("Notes(%d): %v", subject, err)
	}

	requireNodes(t, "Notes", got, want)
}

func (rig *noteRig) requireInterned(t *testing.T, text string, want bool) {
	t.Helper()

	_, found, err := rig.text.Lookup(rig.graph, text)
	if err != nil || found != want {
		t.Fatalf("Lookup(%q) = (found %v, %v), want found %v", text, found, err, want)
	}
}

func TestFoundationalNamesIncludeNoteNames(t *testing.T) {
	for _, want := range []string{NameAllNotes, NameAllNoteSubjects, NameAllNoteTexts} {
		if !slices.Contains(FoundationalNames, want) {
			t.Fatalf("FoundationalNames %v does not include %q", FoundationalNames, want)
		}
	}
}

func TestNewNoteRegistryRequiresItsNamesAndNodes(t *testing.T) {
	const nonexistent NodeID = 999999

	var g Graph

	parts := newNoteParts(t, &g)

	cases := []struct {
		name   string
		mutate func(ids map[string]NodeID)
		want   error
	}{
		{name: "a missing name", mutate: func(ids map[string]NodeID) { delete(ids, NameAllNoteTexts) }, want: ErrNameNotFound},
		{name: "a node that does not exist", mutate: func(ids map[string]NodeID) { ids[NameAllNotes] = nonexistent }, want: ErrNodeNotFound},
	}

	for _, tc := range cases {
		ids := maps.Clone(parts.ids)
		tc.mutate(ids)

		if _, err := NewNoteRegistry(&g, parts.text, ids); !errors.Is(err, tc.want) {
			t.Fatalf("%s: NewNoteRegistry() error = %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestNoteAddAndReadBack(t *testing.T) {
	forEachNoteRig(t, func(t *testing.T, rig *noteRig) {
		subject := mustCreateNode(t, rig.graph)
		note := rig.add(t, subject, "hello")

		if is, err := rig.notes.IsNote(rig.graph, note); err != nil || !is {
			t.Fatalf("IsNote(note) = (%v,%v), want (true,nil)", is, err)
		}

		if is, err := rig.notes.IsNote(rig.graph, subject); err != nil || is {
			t.Fatalf("IsNote(subject) = (%v,%v), want (false,nil)", is, err)
		}

		if got, err := rig.notes.Subject(rig.graph, note); err != nil || got != subject {
			t.Fatalf("Subject() = (%d,%v), want (%d,nil)", got, err, subject)
		}

		rig.requireText(t, note, "hello", true)
		rig.requireNotes(t, subject, note)

		with, err := rig.notes.NotesWithText(rig.graph, "hello")
		if err != nil {
			t.Fatalf("NotesWithText(): %v", err)
		}

		requireNodes(t, "NotesWithText", with, []NodeID{note})

		nodes, err := rig.notes.NodesWithNote(rig.graph, "hello")
		if err != nil {
			t.Fatalf("NodesWithNote(): %v", err)
		}

		requireNodes(t, "NodesWithNote", nodes, []NodeID{subject})

		// A text nobody used has no notes, and interns nothing.
		none, err := rig.notes.NotesWithText(rig.graph, "never used")
		if err != nil || len(none) != 0 {
			t.Fatalf("NotesWithText(never used) = (%v,%v), want (none,nil)", none, err)
		}

		rig.requireInterned(t, "never used", false)
	})
}

func TestNoteSeveralNotesPerNodeAndOneTextOnSeveralNodes(t *testing.T) {
	forEachNoteRig(t, func(t *testing.T, rig *noteRig) {
		first := mustCreateNode(t, rig.graph)
		second := mustCreateNode(t, rig.graph)

		a := rig.add(t, first, "shared")
		b := rig.add(t, first, "shared") // the same text twice is two notes
		c := rig.add(t, first, "other")
		d := rig.add(t, second, "shared")

		rig.requireNotes(t, first, a, b, c)
		rig.requireNotes(t, second, d)

		with, err := rig.notes.NotesWithText(rig.graph, "shared")
		if err != nil {
			t.Fatalf("NotesWithText(): %v", err)
		}

		requireNodes(t, "NotesWithText(shared)", with, []NodeID{a, b, d})

		nodes, err := rig.notes.NodesWithNote(rig.graph, "shared")
		if err != nil {
			t.Fatalf("NodesWithNote(): %v", err)
		}

		requireNodes(t, "NodesWithNote(shared)", nodes, []NodeID{first, second})

		// One interned string serves every note that uses it.
		node, found, err := rig.text.Lookup(rig.graph, "shared")
		if err != nil || !found {
			t.Fatalf("Lookup(shared) = (%d,%v,%v)", node, found, err)
		}
	})
}

func TestNoteEnsureIsIdempotentAndAddAlwaysCreates(t *testing.T) {
	forEachNoteRig(t, func(t *testing.T, rig *noteRig) {
		subject := mustCreateNode(t, rig.graph)
		other := mustCreateNode(t, rig.graph)

		ensure := func(node NodeID, text string) NodeID {
			t.Helper()

			note, err := rig.notes.EnsureNote(rig.graph, node, text)
			if err != nil {
				t.Fatalf("EnsureNote(%d, %q): %v", node, text, err)
			}

			return note
		}

		x := ensure(subject, "x")
		if again := ensure(subject, "x"); again != x {
			t.Fatalf("second EnsureNote() = %d, want the same note %d", again, x)
		}

		y := ensure(subject, "y")
		if y == x {
			t.Fatal("EnsureNote() of another text returned the same note")
		}

		if onOther := ensure(other, "x"); onOther == x {
			t.Fatal("EnsureNote() on another node returned the first node's note")
		}

		// AddNote does not look: it makes a second note with the same text.
		dup := rig.add(t, subject, "x")
		if dup == x {
			t.Fatal("AddNote() returned an existing note")
		}

		if got := ensure(subject, "x"); got != x && got != dup {
			t.Fatalf("EnsureNote() with two matches = %d, want one of %d, %d", got, x, dup)
		}

		// "No text" is a text like any other for EnsureNote.
		bare := ensure(subject, "")
		if again := ensure(subject, ""); again != bare || bare == x || bare == y {
			t.Fatalf("EnsureNote(\"\") = %d then %d, want one new note both times", bare, again)
		}

		rig.requireNotes(t, subject, x, y, dup, bare)
	})
}

func TestNoteWithoutTextKeepsItsIdentity(t *testing.T) {
	forEachNoteRig(t, func(t *testing.T, rig *noteRig) {
		subject := mustCreateNode(t, rig.graph)
		note := rig.add(t, subject, "")

		rig.requireText(t, note, "", false)
		rig.requireNotes(t, subject, note)

		if setErr := rig.notes.SetText(rig.graph, note, "now it has one"); setErr != nil {
			t.Fatalf("SetText(): %v", setErr)
		}

		rig.requireText(t, note, "now it has one", true)

		if clearErr := rig.notes.SetText(rig.graph, note, ""); clearErr != nil {
			t.Fatalf("SetText(\"\"): %v", clearErr)
		}

		rig.requireText(t, note, "", false)
		rig.requireNotes(t, subject, note)

		// The text it had is still a string, and nothing finds the note by it.
		rig.requireInterned(t, "now it has one", true)

		with, err := rig.notes.NotesWithText(rig.graph, "now it has one")
		if err != nil || len(with) != 0 {
			t.Fatalf("NotesWithText() = (%v,%v), want (none,nil)", with, err)
		}

		if _, searchErr := rig.notes.NotesWithText(rig.graph, ""); !errors.Is(searchErr, ErrInvalidNote) {
			t.Fatalf("NotesWithText(\"\") error = %v, want %v", searchErr, ErrInvalidNote)
		}

		if _, searchErr := rig.notes.NodesWithNote(rig.graph, ""); !errors.Is(searchErr, ErrInvalidNote) {
			t.Fatalf("NodesWithNote(\"\") error = %v, want %v", searchErr, ErrInvalidNote)
		}
	})
}

func TestNoteSetTextMovesTheNoteToAnotherStringAndKeepsTheOldOne(t *testing.T) {
	forEachNoteRig(t, func(t *testing.T, rig *noteRig) {
		subject := mustCreateNode(t, rig.graph)
		note := rig.add(t, subject, "old")

		for range 2 { // the second call changes nothing
			if setErr := rig.notes.SetText(rig.graph, note, "new"); setErr != nil {
				t.Fatalf("SetText(): %v", setErr)
			}
		}

		rig.requireText(t, note, "new", true)
		rig.requireNotes(t, subject, note)
		rig.requireInterned(t, "old", true)

		oldNodes, err := rig.notes.NodesWithNote(rig.graph, "old")
		if err != nil || len(oldNodes) != 0 {
			t.Fatalf("NodesWithNote(old) = (%v,%v), want (none,nil)", oldNodes, err)
		}

		newNodes, err := rig.notes.NodesWithNote(rig.graph, "new")
		if err != nil {
			t.Fatalf("NodesWithNote(new): %v", err)
		}

		requireNodes(t, "NodesWithNote(new)", newNodes, []NodeID{subject})
	})
}

func TestNoteRemoveAndClearLeaveTheNodeAndTheStrings(t *testing.T) {
	forEachNoteRig(t, func(t *testing.T, rig *noteRig) {
		first := mustCreateNode(t, rig.graph)
		second := mustCreateNode(t, rig.graph)

		a := rig.add(t, first, "a")
		b := rig.add(t, first, "b")
		c := rig.add(t, second, "a")

		if removeErr := rig.notes.RemoveNote(rig.graph, a); removeErr != nil {
			t.Fatalf("RemoveNote(): %v", removeErr)
		}

		if mustNodeExists(t, rig.graph, a) {
			t.Fatal("the removed note still exists")
		}

		rig.requireNotes(t, first, b)
		rig.requireNotes(t, second, c)
		rig.requireInterned(t, "a", true)

		cleared, err := rig.notes.ClearNotes(rig.graph, first)
		if err != nil || cleared != 1 {
			t.Fatalf("ClearNotes() = (%d,%v), want (1,nil)", cleared, err)
		}

		rig.requireNotes(t, first)
		rig.requireNotes(t, second, c)

		if again, againErr := rig.notes.ClearNotes(rig.graph, first); againErr != nil || again != 0 {
			t.Fatalf("second ClearNotes() = (%d,%v), want (0,nil)", again, againErr)
		}

		if !mustNodeExists(t, rig.graph, first) {
			t.Fatal("ClearNotes() deleted the annotated node")
		}
	})
}

func TestNoteBlocksDeletingTheAnnotatedNodeUntilCleared(t *testing.T) {
	forEachNoteRig(t, func(t *testing.T, rig *noteRig) {
		subject := mustCreateNode(t, rig.graph)
		note := rig.add(t, subject, "keep me")

		if deleteErr := deleteNodeVia(rig.graph, subject); !errors.Is(deleteErr, ErrNodeNotEmpty) {
			t.Fatalf("deleting an annotated node error = %v, want %v", deleteErr, ErrNodeNotEmpty)
		}

		text, found, err := rig.text.Lookup(rig.graph, "keep me")
		if err != nil || !found {
			t.Fatalf("Lookup() = (%d,%v,%v)", text, found, err)
		}

		if deleteErr := deleteNodeVia(rig.graph, text); !errors.Is(deleteErr, ErrNodeNotEmpty) {
			t.Fatalf("deleting a string a note uses error = %v, want %v", deleteErr, ErrNodeNotEmpty)
		}

		// A note that is itself annotated cannot be removed either.
		meta := rig.add(t, note, "about the note")

		if removeErr := rig.notes.RemoveNote(rig.graph, note); !errors.Is(removeErr, ErrNodeNotEmpty) {
			t.Fatalf("RemoveNote() of an annotated note error = %v, want %v", removeErr, ErrNodeNotEmpty)
		}

		if removeErr := rig.notes.RemoveNote(rig.graph, meta); removeErr != nil {
			t.Fatalf("RemoveNote(meta): %v", removeErr)
		}

		if _, clearErr := rig.notes.ClearNotes(rig.graph, subject); clearErr != nil {
			t.Fatalf("ClearNotes(): %v", clearErr)
		}

		if deleteErr := deleteNodeVia(rig.graph, subject); deleteErr != nil {
			t.Fatalf("deleting the node after ClearNotes(): %v", deleteErr)
		}

		if mustNodeExists(t, rig.graph, subject) {
			t.Fatal("the node still exists after its deletion")
		}
	})
}

func TestNoteRejectsInvalidArgumentsAndCreatesNothing(t *testing.T) {
	const nonexistent NodeID = 999999

	forEachNoteRig(t, func(t *testing.T, rig *noteRig) {
		subject := mustCreateNode(t, rig.graph)
		plain := mustCreateNode(t, rig.graph)

		nodesBefore := len(mustFindNodes(t, rig.graph))

		if _, err := rig.notes.AddNote(rig.graph, nonexistent, "x"); !errors.Is(err, ErrNodeNotFound) {
			t.Fatalf("AddNote(nonexistent) error = %v, want %v", err, ErrNodeNotFound)
		}

		if _, err := rig.notes.AddNote(rig.graph, subject, "\xff"); !errors.Is(err, ErrInvalidText) {
			t.Fatalf("AddNote(invalid UTF-8) error = %v, want %v", err, ErrInvalidText)
		}

		if _, err := rig.notes.AddNote(rig.graph, subject, strings.Repeat("a", MaxTextRunes+1)); !errors.Is(err, ErrTextTooLong) {
			t.Fatalf("AddNote(too long) error = %v, want %v", err, ErrTextTooLong)
		}

		if _, err := rig.notes.Subject(rig.graph, plain); !errors.Is(err, ErrNotRecord) {
			t.Fatalf("Subject(not a note) error = %v, want %v", err, ErrNotRecord)
		}

		if _, _, err := rig.notes.Text(rig.graph, plain); !errors.Is(err, ErrNotRecord) {
			t.Fatalf("Text(not a note) error = %v, want %v", err, ErrNotRecord)
		}

		if err := rig.notes.SetText(rig.graph, plain, "x"); !errors.Is(err, ErrNotRecord) {
			t.Fatalf("SetText(not a note) error = %v, want %v", err, ErrNotRecord)
		}

		if err := rig.notes.RemoveNote(rig.graph, plain); !errors.Is(err, ErrNotRecord) {
			t.Fatalf("RemoveNote(not a note) error = %v, want %v", err, ErrNotRecord)
		}

		if _, err := rig.notes.Notes(rig.graph, nonexistent); !errors.Is(err, ErrNodeNotFound) {
			t.Fatalf("Notes(nonexistent) error = %v, want %v", err, ErrNodeNotFound)
		}

		if got := len(mustFindNodes(t, rig.graph)); got != nodesBefore {
			t.Fatalf("%d nodes after the refused calls, want %d", got, nodesBefore)
		}
	})
}

// TestNoteOnPointerAndStringLeavesTheirOwnStructureAlone is the reason a note
// adds no edge out of the annotated node: a Representation A pointer allows one
// child, and a string's children are its trie extensions.
func TestNoteOnPointerAndStringLeavesTheirOwnStructureAlone(t *testing.T) {
	forEachNoteRig(t, func(t *testing.T, rig *noteRig) {
		pointers, err := NewPointerRegistry(rig.graph, rig.ids[NameAllPointers])
		if err != nil {
			t.Fatalf("NewPointerRegistry(): %v", err)
		}

		pointer, err := pointers.NewPointer(rig.graph)
		if err != nil {
			t.Fatalf("NewPointer(): %v", err)
		}

		first := mustCreateNode(t, rig.graph)
		second := mustCreateNode(t, rig.graph)

		if setErr := pointers.SetTarget(rig.graph, pointer, first); setErr != nil {
			t.Fatalf("SetTarget(first): %v", setErr)
		}

		rig.add(t, pointer, "about the pointer")

		if setErr := pointers.SetTarget(rig.graph, pointer, second); setErr != nil {
			t.Fatalf("SetTarget(second) after annotating the pointer: %v", setErr)
		}

		if target, hasTarget, targetErr := pointers.Target(rig.graph, pointer); targetErr != nil || !hasTarget || target != second {
			t.Fatalf("Target() = (%d,%v,%v), want (%d,true,nil)", target, hasTarget, targetErr, second)
		}

		word, err := rig.text.Intern(rig.graph, "word")
		if err != nil {
			t.Fatalf("Intern(): %v", err)
		}

		rig.add(t, word, "a word")

		if got, textErr := rig.text.Text(rig.graph, word); textErr != nil || got != "word" {
			t.Fatalf("Text(word) = (%q,%v), want (word,nil)", got, textErr)
		}

		if verifyErr := VerifyAll(rig.graph, 5); verifyErr != nil {
			t.Fatalf("VerifyAll(): %v", verifyErr)
		}
	})
}

// TestNoteCheckerDeclinesEachHandMadeViolation corrupts a note in one
// transaction per case and checks that the commit is declined and the valid
// note is left as it was.
func TestNoteCheckerDeclinesEachHandMadeViolation(t *testing.T) {
	cases := []struct {
		name    string
		want    error
		corrupt func(tx Tx, rig *noteRig, note NodeID) error
	}{
		{
			name: "a note with no subject",
			want: ErrInvalidNote,
			corrupt: func(tx Tx, rig *noteRig, _ NodeID) error {
				_, err := rig.notes.fields.NewRecord(tx)

				return err
			},
		},
		{
			name: "a note whose subject is cleared",
			want: ErrInvalidNote,
			corrupt: func(tx Tx, rig *noteRig, note NodeID) error {
				_, err := rig.notes.fields.Clear(tx, note, rig.notes.subjectField)

				return err
			},
		},
		{
			name: "a text that is not a string node",
			want: ErrInvalidNote,
			corrupt: func(tx Tx, rig *noteRig, note NodeID) error {
				plain, createErr := createNodeTx(tx)
				if createErr != nil {
					return createErr
				}

				return rig.notes.fields.Set(tx, note, rig.notes.textField, plain)
			},
		},
		{
			name: "the empty string node as the text",
			want: ErrInvalidNote,
			corrupt: func(tx Tx, rig *noteRig, note NodeID) error {
				return rig.notes.fields.Set(tx, note, rig.notes.textField, rig.text.EmptyString())
			},
		},
		{
			name: "a subject slot that belongs to no note",
			want: ErrInvalidRecord,
			corrupt: func(tx Tx, rig *noteRig, _ NodeID) error {
				_, err := createTaggedNodeTx(tx, rig.notes.subjectField)

				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			forEachNoteRig(t, func(t *testing.T, rig *noteRig) {
				subject := mustCreateNode(t, rig.graph)
				note := rig.add(t, subject, "hello")

				nodesBefore := len(mustFindNodes(t, rig.graph))

				err := rig.graph.Transact(func(tx Tx) error {
					return tc.corrupt(tx, rig, note)
				})
				if !errors.Is(err, tc.want) {
					t.Fatalf("Transact() error = %v, want %v", err, tc.want)
				}

				if got := len(mustFindNodes(t, rig.graph)); got != nodesBefore {
					t.Fatalf("%d nodes after the declined commit, want %d", got, nodesBefore)
				}

				rig.requireText(t, note, "hello", true)
				rig.requireNotes(t, subject, note)
			})
		})
	}
}

func TestNoteVerifyAllAcceptsValidNotes(t *testing.T) {
	forEachNoteRig(t, func(t *testing.T, rig *noteRig) {
		subject := mustCreateNode(t, rig.graph)

		rig.add(t, subject, "one")
		rig.add(t, subject, "")
		rig.add(t, subject, "one")

		if err := VerifyAll(rig.graph, 5); err != nil {
			t.Fatalf("VerifyAll() on valid notes: %v", err)
		}
	})
}

// TestHostRegistriesCarryNotesAcrossARestart: the host's registries serve
// notes, and a restart (whose startup sweep runs the note Checker over every
// node) finds the same notes again.
func TestHostRegistriesCarryNotesAcrossARestart(t *testing.T) {
	path := boltTestPath(t)
	first := openTestHost(t, path, nil)

	subject := mustCreateNode(t, first.Graph())

	notes := first.Registries().Notes

	withText, err := notes.AddNote(first.Graph(), subject, "remember this")
	if err != nil {
		t.Fatalf("AddNote(): %v", err)
	}

	bare, err := notes.AddNote(first.Graph(), subject, "")
	if err != nil {
		t.Fatalf("AddNote(\"\"): %v", err)
	}

	verify := func(h *Host) {
		t.Helper()

		reopened := h.Registries().Notes

		got, notesErr := reopened.Notes(h.Graph(), subject)
		if notesErr != nil {
			t.Fatalf("Notes(): %v", notesErr)
		}

		requireNodes(t, "Notes after the restart", got, []NodeID{withText, bare})

		text, hasText, textErr := reopened.Text(h.Graph(), withText)
		if textErr != nil || !hasText || text != "remember this" {
			t.Fatalf("Text() = (%q,%v,%v), want (remember this,true,nil)", text, hasText, textErr)
		}

		if _, hasBare, bareErr := reopened.Text(h.Graph(), bare); bareErr != nil || hasBare {
			t.Fatalf("Text(bare) = (has %v, %v), want (false, nil)", hasBare, bareErr)
		}
	}

	verify(first)

	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("Close(): %v", closeErr)
	}

	verify(openTestHost(t, path, nil))
}