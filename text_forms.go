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
	"slices"
)

// Text lists and text forms (theorystate.md section 114): alternative
// spellings of a text, hanging off its trie node.

const (
	// NameAllTextLists tags an ordered list whose elements are non-empty
	// string nodes (a text list).
	NameAllTextLists = "AllTextLists"

	// NameAllTextFormSlots tags the one slot a string node may have, whose
	// children are its forms.
	NameAllTextFormSlots = "AllTextFormSlots"
)

var (
	// ErrNotTextList is returned for a node that is not tagged
	// (AllTextLists, node).
	ErrNotTextList = errors.New("node is not tagged as a text list")

	// ErrNotTextForm is returned for a text list that is not a form of any
	// text.
	ErrNotTextForm = errors.New("node is not a form of a text")

	// ErrInvalidTextList is returned for a text list with an element that is
	// not a non-empty string node.
	ErrInvalidTextList = errors.New("text list is invalid")

	// ErrInvalidTextForm is returned for a form whose elements do not spell
	// its text, or for form structure that is not what TextFormRegistry builds.
	ErrInvalidTextForm = errors.New("text form is invalid")

	// errFlattenLimit reports that the elements spell more than the limit
	// asked for.
	errFlattenLimit = errors.New("the text of the elements is longer than the limit")
)

// TextFormRegistry keeps text lists and the forms of texts.
//
//	T (a string node)
//	T -> R              (AllTextFormSlots, R): at most one slot per text
//	R -> form           each form is a text list: (AllTextLists, form)
//	form -> capsules    an ordered list (OrderedList) of string nodes
//
// A text list is an ordered list whose elements are non-empty string nodes
// (what the sentences and phrases of a consumer are built from). A form of T
// is a text list whose elements' texts concatenate to the text of T: the
// same text spelled with other string nodes (for example "c" and "at", or
// "c", "a" and "t", for "cat"). Everything that refers to a text links to the
// string node T; the forms only say how it can be spelled. The slot keeps the
// fan-out of T (its one-char extensions in the trie) uncrowded.
//
// The Checker declines, at commit: a text list with an element that is not a
// non-empty string node; a form that does not spell its text; a slot that
// belongs to no text, to a text that has another slot, or that has a child
// that is not a text list. It looks at the touched text lists and slots, and
// at the lists of touched capsules, and validates each in full (a form's
// length is bounded by the text length). A form emptied by hand without
// touching anything else is found by the startup sweep (VerifyAll).
type TextFormRegistry struct {
	text  *TextRegistry
	lists OrderedList

	allTextLists NodeID
	allFormSlots NodeID
}

// NewTextFormRegistry creates a TextFormRegistry over graph. text is the
// TextRegistry whose strings it spells, lists is the ordered list type its
// text lists are made of, and ids must hold the names of NameAllTextLists and
// NameAllTextFormSlots (as returned by NameRegistry.BootstrapNames). It
// registers the Checker.
func NewTextFormRegistry(graph GraphAPI, text *TextRegistry, lists OrderedList, ids map[string]NodeID) (*TextFormRegistry, error) {
	f := &TextFormRegistry{text: text, lists: lists}

	wanted := [...]struct {
		name string
		dest *NodeID
	}{
		{NameAllTextLists, &f.allTextLists},
		{NameAllTextFormSlots, &f.allFormSlots},
	}

	nodes := make([]NodeID, 0, len(wanted))

	for _, want := range wanted {
		id, ok := ids[want.name]
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrNameNotFound, want.name)
		}

		*want.dest = id
		nodes = append(nodes, id)
	}

	if existsErr := requireAllExist(graph, nodes...); existsErr != nil {
		return nil, existsErr
	}

	graph.RegisterChecker(Checker{
		Name:  fmt.Sprintf("TextFormRegistry(tag=%d)", f.allTextLists),
		Tags:  []NodeID{f.allTextLists, f.allFormSlots, lists.Capsules().allElementCapsules},
		Check: f.checkTouched,
	})

	return f, nil
}

// IsTextList reports whether id is tagged (AllTextLists, id).
func (f *TextFormRegistry) IsTextList(graph GraphReader, id NodeID) (bool, error) {
	has, err := graph.HasRelationship(f.allTextLists, id)

	return has, wrapInterfaceErr(err)
}

// requireTextList checks that list exists and is a text list.
func (f *TextFormRegistry) requireTextList(graph GraphReader, list NodeID) error {
	return requireTagged(graph, list, f.allTextLists, ErrNotTextList)
}

// requirePart checks that part is a string node other than the empty string.
func (f *TextFormRegistry) requirePart(graph GraphReader, part NodeID) error {
	if requireErr := requireTagged(graph, part, f.text.allStrings, ErrNotString); requireErr != nil {
		return requireErr
	}

	if part == f.text.emptyString {
		return fmt.Errorf("%w: the empty string cannot be an element", ErrInvalidTextList)
	}

	return nil
}

// concat returns the text spelled by parts, one after the other. A negative
// limit means no limit; otherwise errFlattenLimit is returned as soon as the
// text is longer than limit runes.
func (f *TextFormRegistry) concat(graph GraphReader, parts []NodeID, limit int) ([]rune, error) {
	var all []rune

	for _, part := range parts {
		if partErr := f.requirePart(graph, part); partErr != nil {
			return nil, partErr
		}

		runes, runesErr := f.text.runesOf(graph, part)
		if runesErr != nil {
			return nil, runesErr
		}

		all = append(all, runes...)

		if limit >= 0 && len(all) > limit {
			return nil, errFlattenLimit
		}
	}

	return all, nil
}

// requireSpells returns ErrInvalidTextForm unless the texts of parts
// concatenate to want.
func (f *TextFormRegistry) requireSpells(graph GraphReader, parts []NodeID, want []rune) error {
	got, err := f.concat(graph, parts, len(want))

	switch {
	case err != nil && !errors.Is(err, errFlattenLimit):
		return err
	case err != nil || !slices.Equal(got, want):
		return fmt.Errorf("%w: the elements do not spell %q", ErrInvalidTextForm, string(want))
	default:
		return nil
	}
}

// NewTextList creates an empty text list.
func (f *TextFormRegistry) NewTextList(graph Transactor) (NodeID, error) {
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		list, err := f.lists.NewList(tx)
		if err != nil {
			return 0, wrapInterfaceErr(err)
		}

		if tagErr := tagNodeTx(tx, f.allTextLists, list); tagErr != nil {
			return 0, tagErr
		}

		return list, nil
	})
}

// AppendMany appends one element per part, in order, to the text list, in one
// transaction, and returns the capsules. Every part must be a non-empty string
// node (ErrNotString, ErrInvalidTextList). Appending to a form makes it stop
// spelling its text, which the Checker declines.
func (f *TextFormRegistry) AppendMany(graph Transactor, list NodeID, parts []NodeID) ([]NodeID, error) {
	return transactValue(graph, func(tx Tx) ([]NodeID, error) {
		if requireErr := f.requireTextList(tx, list); requireErr != nil {
			return nil, requireErr
		}

		for _, part := range parts {
			if partErr := f.requirePart(tx, part); partErr != nil {
				return nil, partErr
			}
		}

		// A slotted list's mutations do not touch the list node, and the
		// Checker finds the lists to validate through the touched nodes.
		tx.Touch(list)

		capsules, err := f.lists.AppendMany(tx, list, parts)

		return capsules, wrapInterfaceErr(err)
	})
}

// Parts returns the elements of the text list, in order.
func (f *TextFormRegistry) Parts(graph GraphReader, list NodeID) ([]NodeID, error) {
	if requireErr := f.requireTextList(graph, list); requireErr != nil {
		return nil, requireErr
	}

	parts, err := f.lists.Elements(graph, list)

	return parts, wrapInterfaceErr(err)
}

// Text returns the text the elements of the text list spell, one after the
// other.
func (f *TextFormRegistry) Text(graph GraphReader, list NodeID) (string, error) {
	parts, err := f.Parts(graph, list)
	if err != nil {
		return "", err
	}

	runes, concatErr := f.concat(graph, parts, -1)
	if concatErr != nil {
		return "", concatErr
	}

	return string(runes), nil
}

// ensureSlot returns text's form slot, creating and linking it if there is
// none; existed reports whether it was already there.
func (f *TextFormRegistry) ensureSlot(tx Tx, text NodeID) (slot NodeID, existed bool, err error) {
	slot, existed, err = findUniqueTaggedChild(tx, text, f.allFormSlots)
	if err != nil || existed {
		return slot, existed, err
	}

	slot, err = createTaggedNodeTx(tx, f.allFormSlots)
	if err != nil {
		return 0, false, err
	}

	return slot, false, addRelationshipTx(tx, text, slot)
}

// findForm returns the form under slot whose elements are exactly parts.
func (f *TextFormRegistry) findForm(graph GraphReader, slot NodeID, parts []NodeID) (NodeID, bool, error) {
	children, err := graph.FindOutgoing(slot)
	if err != nil {
		return 0, false, wrapInterfaceErr(err)
	}

	for _, rel := range children {
		existing, elementsErr := f.lists.Elements(graph, rel.To)
		if elementsErr != nil {
			return 0, false, wrapInterfaceErr(elementsErr)
		}

		if slices.Equal(existing, parts) {
			return rel.To, true, nil
		}
	}

	return 0, false, nil
}

// EnsureForm returns the form of text whose elements are exactly parts,
// creating it (and the text's form slot) in one transaction if there is none.
// The texts of parts must concatenate to the text of text, else
// ErrInvalidTextForm; the empty string has no forms.
func (f *TextFormRegistry) EnsureForm(graph Transactor, text NodeID, parts []NodeID) (NodeID, error) {
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		want, err := f.text.runesOf(tx, text)
		if err != nil {
			return 0, err
		}

		if len(want) == 0 {
			return 0, fmt.Errorf("%w: the empty string has no forms", ErrInvalidTextForm)
		}

		if spellsErr := f.requireSpells(tx, parts, want); spellsErr != nil {
			return 0, spellsErr
		}

		slot, existed, slotErr := f.ensureSlot(tx, text)
		if slotErr != nil {
			return 0, slotErr
		}

		if existed {
			form, found, findErr := f.findForm(tx, slot, parts)
			if findErr != nil || found {
				return form, findErr
			}
		}

		created, newErr := f.NewTextList(tx)
		if newErr != nil {
			return 0, newErr
		}

		if linkErr := addRelationshipTx(tx, slot, created); linkErr != nil {
			return 0, linkErr
		}

		if _, appendErr := f.AppendMany(tx, created, parts); appendErr != nil {
			return 0, appendErr
		}

		return created, nil
	})
}

// Forms returns the forms of text, ascending. A text with no form slot has
// none.
func (f *TextFormRegistry) Forms(graph GraphReader, text NodeID) ([]NodeID, error) {
	if requireErr := requireTagged(graph, text, f.text.allStrings, ErrNotString); requireErr != nil {
		return nil, requireErr
	}

	slot, found, err := findUniqueTaggedChild(graph, text, f.allFormSlots)
	if err != nil || !found {
		return nil, err
	}

	children, childrenErr := graph.FindOutgoing(slot)
	if childrenErr != nil {
		return nil, wrapInterfaceErr(childrenErr)
	}

	forms := make([]NodeID, 0, len(children))
	for _, rel := range children {
		forms = append(forms, rel.To)
	}

	return forms, nil
}

// TextOf returns the text the form is a form of. A text list that is not under
// a form slot is ErrNotTextForm.
func (f *TextFormRegistry) TextOf(graph GraphReader, form NodeID) (NodeID, error) {
	if requireErr := f.requireTextList(graph, form); requireErr != nil {
		return 0, requireErr
	}

	slot, hasSlot, err := findUniqueTaggedParent(graph, form, f.allFormSlots)
	if err != nil {
		return 0, err
	}
	if !hasSlot {
		return 0, ErrNotTextForm
	}

	text, hasText, textErr := findUniqueTaggedParent(graph, slot, f.text.allStrings)
	if textErr != nil {
		return 0, textErr
	}
	if !hasText {
		return 0, fmt.Errorf("%w: its slot belongs to no text", ErrInvalidTextForm)
	}

	return text, nil
}

// emptyList removes every element of list, deleting the capsules.
func (f *TextFormRegistry) emptyList(tx Tx, list NodeID) error {
	for {
		head, hasHead, headErr := f.lists.Head(tx, list)
		if headErr != nil {
			return wrapInterfaceErr(headErr)
		}
		if !hasHead {
			return nil
		}

		if _, removeErr := f.lists.Remove(tx, list, head); removeErr != nil {
			return wrapInterfaceErr(removeErr)
		}
	}
}

// dropEmptySlot deletes slot, and its link from its text, if it has no form
// left.
func (f *TextFormRegistry) dropEmptySlot(tx Tx, slot NodeID) error {
	children, childrenErr := tx.FindOutgoing(slot)
	if childrenErr != nil {
		return wrapInterfaceErr(childrenErr)
	}
	if len(children) != 0 {
		return nil
	}

	text, hasText, textErr := findUniqueTaggedParent(tx, slot, f.text.allStrings)
	if textErr != nil {
		return textErr
	}

	if hasText {
		if unlinkErr := removeRelationshipTx(tx, text, slot); unlinkErr != nil {
			return unlinkErr
		}
	}

	return untagAndDeleteNodeTx(tx, slot, f.allFormSlots)
}

// DeleteForm deletes the form with its elements, in one transaction, and the
// text's form slot if it was the last form. The strings it was made of stay.
func (f *TextFormRegistry) DeleteForm(graph Transactor, form NodeID) error {
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		if requireErr := f.requireTextList(tx, form); requireErr != nil {
			return requireErr
		}

		slot, hasSlot, slotErr := findUniqueTaggedParent(tx, form, f.allFormSlots)
		if slotErr != nil {
			return slotErr
		}
		if !hasSlot {
			return ErrNotTextForm
		}

		if emptyErr := f.emptyList(tx, form); emptyErr != nil {
			return emptyErr
		}

		if untagErr := removeRelationshipTx(tx, f.allTextLists, form); untagErr != nil {
			return untagErr
		}

		if unlinkErr := removeRelationshipTx(tx, slot, form); unlinkErr != nil {
			return unlinkErr
		}

		if deleteErr := f.lists.DeleteList(tx, form); deleteErr != nil {
			return wrapInterfaceErr(deleteErr)
		}

		return f.dropEmptySlot(tx, slot)
	}))
}

// ---------------------------------------------------------------------
// Commit-time Checker.

// collect adds to lists and slots what a touched node says needs checking: the
// node itself if it is a text list or a form slot, and the text lists a
// touched capsule is a member of.
func (f *TextFormRegistry) collect(graph GraphReader, node NodeID, lists, slots map[NodeID]struct{}) error {
	isList, listErr := f.IsTextList(graph, node)
	if listErr != nil {
		return listErr
	}
	if isList {
		lists[node] = struct{}{}
	}

	isSlot, slotErr := graph.HasRelationship(f.allFormSlots, node)
	if slotErr != nil {
		return wrapInterfaceErr(slotErr)
	}
	if isSlot {
		slots[node] = struct{}{}
	}

	isCapsule, capsuleErr := f.lists.Capsules().IsCapsule(graph, node)
	if capsuleErr != nil {
		return capsuleErr
	}
	if !isCapsule {
		return nil
	}

	owners, ownersErr := f.lists.ListsOf(graph, node)
	if ownersErr != nil {
		return wrapInterfaceErr(ownersErr)
	}

	for _, owner := range owners {
		ownerIsText, ownerErr := f.IsTextList(graph, owner)
		if ownerErr != nil {
			return ownerErr
		}
		if ownerIsText {
			lists[owner] = struct{}{}
		}
	}

	return nil
}

// checkTouched is the commit-time Checker.
func (f *TextFormRegistry) checkTouched(graph GraphReader, touched map[NodeID]struct{}) error {
	lists := make(map[NodeID]struct{})
	slots := make(map[NodeID]struct{})

	for node := range touched {
		if collectErr := f.collect(graph, node, lists, slots); collectErr != nil {
			return collectErr
		}
	}

	for _, slot := range sortedNodeSet(slots) {
		if slotErr := f.checkSlot(graph, slot); slotErr != nil {
			return slotErr
		}
	}

	for _, list := range sortedNodeSet(lists) {
		if listErr := f.checkList(graph, list); listErr != nil {
			return listErr
		}
	}

	return nil
}

// checkSlot: a form slot belongs to one text, which has no other slot, and
// every child of it is a text list.
func (f *TextFormRegistry) checkSlot(graph GraphReader, slot NodeID) error {
	text, hasText, err := findUniqueTaggedParent(graph, slot, f.text.allStrings)
	if err != nil {
		return err
	}
	if !hasText {
		return fmt.Errorf("%w: a form slot belongs to no text", ErrInvalidTextForm)
	}

	owned, hasOwned, ownedErr := findUniqueTaggedChild(graph, text, f.allFormSlots)
	if ownedErr != nil {
		return ownedErr
	}
	if !hasOwned || owned != slot {
		return fmt.Errorf("%w: a text has another form slot", ErrInvalidTextForm)
	}

	children, childrenErr := graph.FindOutgoing(slot)
	if childrenErr != nil {
		return wrapInterfaceErr(childrenErr)
	}

	for _, rel := range children {
		isList, listErr := f.IsTextList(graph, rel.To)
		if listErr != nil {
			return listErr
		}
		if !isList {
			return fmt.Errorf("%w: a child of a form slot is not a text list", ErrInvalidTextForm)
		}
	}

	return nil
}

// checkList: every element of the text list is a non-empty string node, and,
// if it is a form, the elements spell its text.
func (f *TextFormRegistry) checkList(graph GraphReader, list NodeID) error {
	parts, err := f.lists.Elements(graph, list)
	if err != nil {
		return wrapInterfaceErr(err)
	}

	for _, part := range parts {
		if partErr := f.requirePart(graph, part); partErr != nil {
			return partErr
		}
	}

	text, textErr := f.TextOf(graph, list)
	if errors.Is(textErr, ErrNotTextForm) {
		return nil
	}
	if textErr != nil {
		return textErr
	}

	want, wantErr := f.text.runesOf(graph, text)
	if wantErr != nil {
		return wantErr
	}

	return f.requireSpells(graph, parts, want)
}