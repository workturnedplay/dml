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

// Notes on nodes (theorystate.md section 117): free text linked to any node.

const (
	// NameAllNotes tags a note record.
	NameAllNotes = "AllNotes"

	// NameAllNoteSubjects and NameAllNoteTexts tag the two field slots of a
	// note: the node it is about, and its text.
	NameAllNoteSubjects = "AllNoteSubjects"
	NameAllNoteTexts    = "AllNoteTexts"
)

// ErrInvalidNote is returned when a note, or a request about notes, is not what
// NoteRegistry builds: a note without a subject, a text that is not a
// non-empty string node, a search for the empty text.
var ErrInvalidNote = errors.New("note is invalid")

// NoteRegistry keeps notes: texts linked to nodes.
//
//	(AllNotes, N)                   N is a note, a FieldRegistry record
//	N -> S1   (AllNoteSubjects, S1) S1 -> the node the note is about
//	N -> S2   (AllNoteTexts, S2)    S2 -> the text, an interned string node
//
// A node can have any number of notes, and the same text can be on any number
// of nodes. A note is its own node (the occurrence identity of theorystate.md
// section 75), which is what lets one node carry the same text twice, lets a
// note keep its identity when its text changes, and leaves room for more
// fields later (a timestamp, an author) without redesign.
//
// Nothing is added to the annotated node's own children: the only new edge it
// has is an incoming one from a note's subject slot. That is deliberate. A
// direct edge from the node to the string would be a second child of a
// Representation A pointer, and for a string node would look like a trie
// extension.
//
// A note may have no text (identity only): the text field is simply not set.
// Text is never the empty string node; SetText with "" clears the field. The
// text is interned, so it is the one canonical string node, and NotesWithText
// finds the notes with a text by reverse lookup from that node.
//
// Because both slots point at their targets, the annotated node and the text
// string cannot be deleted while a note uses them ("delete only if empty",
// theorystate.md section 18): ClearNotes first. Removing a note, or changing
// its text, never deletes a string: whether a string is still needed is a
// question for a future DeleteString, not for notes.
//
// The Checker declines, at commit: a note with no subject, and a note whose text
// is not a string node or is the empty string node. Like every registry,
// NoteRegistry stores no graph reference (theorystate.md section 90).
type NoteRegistry struct {
	texts  *TextRegistry
	fields *FieldRegistry

	allNotes     NodeID
	subjectField NodeID
	textField    NodeID
}

// NewNoteRegistry creates a NoteRegistry over graph. texts interns the texts,
// and ids must hold the three note names (as returned by
// NameRegistry.BootstrapNames). It registers the Checkers.
func NewNoteRegistry(graph GraphAPI, texts *TextRegistry, ids map[string]NodeID) (*NoteRegistry, error) {
	n := &NoteRegistry{texts: texts}

	nodes, bindErr := bindNames(ids, []nameBinding{
		{NameAllNotes, &n.allNotes},
		{NameAllNoteSubjects, &n.subjectField},
		{NameAllNoteTexts, &n.textField},
	})
	if bindErr != nil {
		return nil, bindErr
	}

	// Checked before anything is registered, so a failure leaves no Checker
	// behind.
	if existsErr := requireAllExist(graph, nodes...); existsErr != nil {
		return nil, existsErr
	}

	fields, fieldsErr := NewFieldRegistry(graph, n.allNotes, FieldSpec{Tag: n.subjectField}, FieldSpec{Tag: n.textField})
	if fieldsErr != nil {
		return nil, fieldsErr
	}

	n.fields = fields

	registerTaggedChecker(graph, fmt.Sprintf("NoteRegistry(tag=%d)", n.allNotes), []taggedCheck{
		{tag: n.allNotes, run: n.checkNote},
		{tag: n.subjectField, run: n.checkFieldSlot},
		{tag: n.textField, run: n.checkFieldSlot},
	})

	return n, nil
}

// IsNote reports whether id is tagged (AllNotes, id).
func (n *NoteRegistry) IsNote(graph GraphReader, id NodeID) (bool, error) {
	return n.fields.IsRecord(graph, id)
}

// addNoteTx creates a note about subject with text ("" for none) inside tx.
func (n *NoteRegistry) addNoteTx(tx Tx, subject NodeID, text string) (NodeID, error) {
	note, newErr := n.fields.NewRecord(tx)
	if newErr != nil {
		return 0, newErr
	}

	if subjectErr := n.fields.Set(tx, note, n.subjectField, subject); subjectErr != nil {
		return 0, subjectErr
	}

	if text == "" {
		return note, nil
	}

	node, internErr := n.texts.Intern(tx, text)
	if internErr != nil {
		return 0, internErr
	}

	if textErr := n.fields.Set(tx, note, n.textField, node); textErr != nil {
		return 0, textErr
	}

	return note, nil
}

// AddNote creates a new note about subject with text, interning the text if
// needed, as one transaction (nested if graph is a Tx). The empty text makes a
// note with no text at all. It always creates a note, even if subject already
// has one with this text: use EnsureNote for "at most one". subject must exist.
func (n *NoteRegistry) AddNote(graph Transactor, subject NodeID, text string) (NodeID, error) {
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		return n.addNoteTx(tx, subject, text)
	})
}

// findNote returns a note of subject with exactly this text ("" meaning no
// text), the one with the lowest ID if there are several.
func (n *NoteRegistry) findNote(g GraphReader, subject NodeID, text string) (NodeID, bool, error) {
	wantsText := text != ""

	var want NodeID

	if wantsText {
		node, interned, lookupErr := n.texts.Lookup(g, text)
		if lookupErr != nil || !interned {
			return 0, false, lookupErr
		}

		want = node
	}

	notes, notesErr := n.Notes(g, subject)
	if notesErr != nil {
		return 0, false, notesErr
	}

	for _, note := range notes {
		value, hasValue, getErr := n.fields.Get(g, note, n.textField)
		if getErr != nil {
			return 0, false, getErr
		}

		if hasValue == wantsText && (!wantsText || value == want) {
			return note, true, nil
		}
	}

	return 0, false, nil
}

// EnsureNote returns a note about subject with exactly this text ("" meaning no
// text), creating one if there is none, as one transaction. If several match
// (AddNote can make them), the one with the lowest ID is returned.
func (n *NoteRegistry) EnsureNote(graph Transactor, subject NodeID, text string) (NodeID, error) {
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		existing, found, findErr := n.findNote(tx, subject, text)
		if findErr != nil || found {
			return existing, findErr
		}

		return n.addNoteTx(tx, subject, text)
	})
}

// Subject returns the node note is about. A node that is not a note is
// ErrNotRecord.
func (n *NoteRegistry) Subject(graph GraphReader, note NodeID) (NodeID, error) {
	subject, hasSubject, err := n.fields.Get(graph, note, n.subjectField)
	if err != nil {
		return 0, err
	}

	if !hasSubject {
		return 0, fmt.Errorf("%w: note %d has no subject", ErrInvalidNote, note)
	}

	return subject, nil
}

// Text returns the text of note; hasText is false for a note with no text.
func (n *NoteRegistry) Text(graph GraphReader, note NodeID) (text string, hasText bool, err error) {
	value, hasValue, getErr := n.fields.Get(graph, note, n.textField)
	if getErr != nil || !hasValue {
		return "", false, getErr
	}

	read, readErr := n.texts.Text(graph, value)
	if readErr != nil {
		return "", false, readErr
	}

	return read, true, nil
}

// SetText makes text the text of note, interning it if needed, as one
// transaction. The note keeps its identity, and the string it had before is
// left as it is. The empty text clears it. note must be a note (ErrNotRecord).
func (n *NoteRegistry) SetText(graph Transactor, note NodeID, text string) error {
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		// Before interning, so a refused call creates nothing.
		if requireErr := n.fields.requireRecord(tx, note); requireErr != nil {
			return requireErr
		}

		if text == "" {
			_, clearErr := n.fields.Clear(tx, note, n.textField)

			return clearErr
		}

		node, internErr := n.texts.Intern(tx, text)
		if internErr != nil {
			return internErr
		}

		return n.fields.Set(tx, note, n.textField, node)
	}))
}

// Notes returns the notes about subject, ascending. The order has no meaning.
// subject must exist.
func (n *NoteRegistry) Notes(graph GraphReader, subject NodeID) ([]NodeID, error) {
	return n.fields.Owners(graph, n.subjectField, subject)
}

// NotesWithText returns the notes whose text is text, ascending, by reverse
// lookup from the text's string node. A text that was never interned has none.
// The empty text is ErrInvalidNote: a note with no text is not found this way.
func (n *NoteRegistry) NotesWithText(graph GraphReader, text string) ([]NodeID, error) {
	if text == "" {
		return nil, fmt.Errorf("%w: the empty text is no text, and cannot be searched for", ErrInvalidNote)
	}

	node, interned, lookupErr := n.texts.Lookup(graph, text)
	if lookupErr != nil || !interned {
		return nil, lookupErr
	}

	return n.fields.Owners(graph, n.textField, node)
}

// NodesWithNote returns the nodes that have a note with this text, ascending
// and each once.
func (n *NoteRegistry) NodesWithNote(graph GraphReader, text string) ([]NodeID, error) {
	notes, notesErr := n.NotesWithText(graph, text)
	if notesErr != nil {
		return nil, notesErr
	}

	subjects := make(map[NodeID]struct{}, len(notes))

	for _, note := range notes {
		subject, subjectErr := n.Subject(graph, note)
		if subjectErr != nil {
			return nil, subjectErr
		}

		subjects[subject] = struct{}{}
	}

	return sortedNodeSet(subjects), nil
}

// RemoveNote deletes note, as one transaction. The node it was about and the
// text are never touched. If something else still refers to the note (another
// note is about it, say) the delete fails with ErrNodeNotEmpty and nothing
// changes.
func (n *NoteRegistry) RemoveNote(graph Transactor, note NodeID) error {
	return n.fields.DeleteRecord(graph, note)
}

// ClearNotes deletes every note about subject, as one transaction, and returns
// how many there were. It is what to do before deleting subject.
func (n *NoteRegistry) ClearNotes(graph Transactor, subject NodeID) (int, error) {
	return transactValue(graph, func(tx Tx) (int, error) {
		notes, notesErr := n.Notes(tx, subject)
		if notesErr != nil {
			return 0, notesErr
		}

		for _, note := range notes {
			if removeErr := n.fields.DeleteRecord(tx, note); removeErr != nil {
				return 0, removeErr
			}
		}

		return len(notes), nil
	})
}

// ---------------------------------------------------------------------
// Commit-time Checker. Local: it costs time proportional to what the
// transaction touched. FieldRegistry's own Checker covers the record and slot
// shape; this one covers what a note must hold.

// checkNote: a note has a subject, and a text, if it has one, is a string node
// other than the empty string.
func (n *NoteRegistry) checkNote(g GraphReader, note NodeID) error {
	_, hasSubject, subjectErr := n.fields.Get(g, note, n.subjectField)
	if subjectErr != nil {
		return subjectErr
	}

	if !hasSubject {
		return fmt.Errorf("%w: note %d has no subject", ErrInvalidNote, note)
	}

	text, hasText, textErr := n.fields.Get(g, note, n.textField)
	if textErr != nil || !hasText {
		return textErr
	}

	if text == n.texts.EmptyString() {
		return fmt.Errorf("%w: note %d has the empty string as its text; a note with no text has no text field", ErrInvalidNote, note)
	}

	isString, stringErr := n.texts.IsString(g, text)
	if stringErr != nil {
		return stringErr
	}

	if !isString {
		return fmt.Errorf("%w: the text of note %d is not a string node", ErrInvalidNote, note)
	}

	return nil
}

// checkFieldSlot: a subject or text slot belongs to a note, which is then
// checked.
func (n *NoteRegistry) checkFieldSlot(g GraphReader, slot NodeID) error {
	note, hasNote, parentErr := findUniqueTaggedParent(g, slot, n.allNotes)
	if parentErr != nil {
		return parentErr
	}

	if !hasNote {
		return fmt.Errorf("%w: a note field slot belongs to no note", ErrInvalidNote)
	}

	return n.checkNote(g, note)
}