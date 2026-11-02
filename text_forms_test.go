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
	"testing"
)

// formRig is a TextFormRegistry over one backend, on slotted lists, with the
// TextRegistry (and its helpers) it spells.
type formRig struct {
	*textRig
	forms *TextFormRegistry
}

func newFormRig(t *testing.T, graph testGraph) *formRig {
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

	forms, err := NewTextFormRegistry(graph, text, slottedListFromIDs(t, graph, ids), ids)
	if err != nil {
		t.Fatalf("NewTextFormRegistry(): %v", err)
	}

	return &formRig{textRig: &textRig{graph: graph, names: names, text: text}, forms: forms}
}

// forEachFormRig runs body once per backend, each on a fresh graph.
func forEachFormRig(t *testing.T, body func(t *testing.T, rig *formRig)) {
	t.Helper()

	for _, backend := range testBackends() {
		t.Run(backend.name, func(t *testing.T) {
			body(t, newFormRig(t, backend.open(t)))
		})
	}
}

func (rig *formRig) ensureForm(t *testing.T, text NodeID, parts ...NodeID) NodeID {
	t.Helper()

	form, err := rig.forms.EnsureForm(rig.graph, text, parts)
	if err != nil {
		t.Fatalf("EnsureForm(%d, %v): %v", text, parts, err)
	}

	return form
}

func (rig *formRig) requireForms(t *testing.T, text NodeID, want []NodeID) {
	t.Helper()

	got, err := rig.forms.Forms(rig.graph, text)
	if err != nil {
		t.Fatalf("Forms(%d): %v", text, err)
	}

	if !reflect.DeepEqual(sortedNodeIDs(got), sortedNodeIDs(want)) {
		t.Fatalf("Forms(%d) = %v, want %v", text, got, want)
	}
}

// rawForm builds a form the way a hand-written transaction would, bypassing
// every check of TextFormRegistry's own methods.
func (rig *formRig) rawForm(tx Tx, text NodeID, parts ...NodeID) error {
	slot, err := createTaggedNodeTx(tx, rig.forms.allFormSlots)
	if err != nil {
		return err
	}

	if linkErr := addRelationshipTx(tx, text, slot); linkErr != nil {
		return linkErr
	}

	list, listErr := rig.forms.NewTextList(tx)
	if listErr != nil {
		return listErr
	}

	if linkErr := addRelationshipTx(tx, slot, list); linkErr != nil {
		return linkErr
	}

	_, appendErr := rig.forms.lists.AppendMany(tx, list, parts)

	return wrapInterfaceErr(appendErr)
}

func TestTextFormsAreEnsuredReadBackAndIdempotent(t *testing.T) {
	forEachFormRig(t, func(t *testing.T, rig *formRig) {
		cat := rig.intern(t, "cat")
		c := rig.intern(t, "c")
		a := rig.intern(t, "a")
		tNode := rig.intern(t, "t")
		at := rig.intern(t, "at")

		rig.requireForms(t, cat, nil)

		first := rig.ensureForm(t, cat, c, at)

		if again := rig.ensureForm(t, cat, c, at); again != first {
			t.Fatalf("EnsureForm() of the same parts = %d, want the same form %d", again, first)
		}

		chars := rig.ensureForm(t, cat, c, a, tNode)
		rig.requireForms(t, cat, []NodeID{first, chars})

		parts, partsErr := rig.forms.Parts(rig.graph, first)
		if partsErr != nil || !reflect.DeepEqual(parts, []NodeID{c, at}) {
			t.Fatalf("Parts() = (%v,%v), want ([%d %d],nil)", parts, partsErr, c, at)
		}

		for _, form := range []NodeID{first, chars} {
			owner, ownerErr := rig.forms.TextOf(rig.graph, form)
			if ownerErr != nil || owner != cat {
				t.Fatalf("TextOf(%d) = (%d,%v), want (%d,nil)", form, owner, ownerErr, cat)
			}

			spelled, textErr := rig.forms.Text(rig.graph, form)
			if textErr != nil || spelled != "cat" {
				t.Fatalf("Text(%d) = (%q,%v), want (\"cat\",nil)", form, spelled, textErr)
			}
		}

		// The string itself is unchanged by having forms.
		rig.requireText(t, cat, "cat")
	})
}

func TestTextFormsRejectWhatDoesNotSpellTheText(t *testing.T) {
	forEachFormRig(t, func(t *testing.T, rig *formRig) {
		cat := rig.intern(t, "cat")
		c := rig.intern(t, "c")
		at := rig.intern(t, "at")
		dog := rig.intern(t, "dog")
		plain := mustCreateNode(t, rig.graph)

		before := len(mustFindNodes(t, rig.graph))

		cases := []struct {
			name  string
			text  NodeID
			parts []NodeID
			want  error
		}{
			{name: "another text", text: cat, parts: []NodeID{c, dog}, want: ErrInvalidTextForm},
			{name: "too short", text: cat, parts: []NodeID{c}, want: ErrInvalidTextForm},
			{name: "too long", text: cat, parts: []NodeID{c, at, c}, want: ErrInvalidTextForm},
			{name: "no elements", text: cat, want: ErrInvalidTextForm},
			{name: "the empty string as an element", text: cat, parts: []NodeID{c, at, rig.text.EmptyString()}, want: ErrInvalidTextList},
			{name: "a node that is not a string", text: cat, parts: []NodeID{c, plain}, want: ErrNotString},
			{name: "the empty string as the text", text: rig.text.EmptyString(), parts: []NodeID{c}, want: ErrInvalidTextForm},
			{name: "a text that is not a string", text: plain, parts: []NodeID{c}, want: ErrNotString},
		}

		for _, tc := range cases {
			if _, err := rig.forms.EnsureForm(rig.graph, tc.text, tc.parts); !errors.Is(err, tc.want) {
				t.Fatalf("%s: EnsureForm() error = %v, want %v", tc.name, err, tc.want)
			}
		}

		// Every refusal rolled back, the form slot included.
		if got := len(mustFindNodes(t, rig.graph)); got != before {
			t.Fatalf("%d nodes after the refused forms, want %d", got, before)
		}

		rig.requireForms(t, cat, nil)
	})
}

func TestTextFormCheckerDeclinesEachHandMadeViolation(t *testing.T) {
	type formNodes struct {
		cat, c, plain NodeID
	}

	cases := []struct {
		name    string
		want    error
		corrupt func(tx Tx, rig *formRig, n formNodes) error
	}{
		{
			name: "a text list with an element that is not a string",
			want: ErrNotString,
			corrupt: func(tx Tx, rig *formRig, n formNodes) error {
				list, err := rig.forms.NewTextList(tx)
				if err != nil {
					return err
				}

				_, appendErr := rig.forms.lists.Append(tx, list, n.plain)

				return wrapInterfaceErr(appendErr)
			},
		},
		{
			name: "a text list with the empty string as an element",
			want: ErrInvalidTextList,
			corrupt: func(tx Tx, rig *formRig, _ formNodes) error {
				list, err := rig.forms.NewTextList(tx)
				if err != nil {
					return err
				}

				_, appendErr := rig.forms.lists.Append(tx, list, rig.text.EmptyString())

				return wrapInterfaceErr(appendErr)
			},
		},
		{
			name: "a form that does not spell its text",
			want: ErrInvalidTextForm,
			corrupt: func(tx Tx, rig *formRig, n formNodes) error {
				return rig.rawForm(tx, n.cat, n.c)
			},
		},
		{
			name: "a text with two form slots",
			want: ErrAmbiguousPointerMetadata,
			corrupt: func(tx Tx, rig *formRig, n formNodes) error {
				for range 2 {
					slot, err := createTaggedNodeTx(tx, rig.forms.allFormSlots)
					if err != nil {
						return err
					}

					if linkErr := addRelationshipTx(tx, n.cat, slot); linkErr != nil {
						return linkErr
					}
				}

				return nil
			},
		},
		{
			name: "a form slot that belongs to no text",
			want: ErrInvalidTextForm,
			corrupt: func(tx Tx, rig *formRig, _ formNodes) error {
				_, err := createTaggedNodeTx(tx, rig.forms.allFormSlots)

				return err
			},
		},
		{
			name: "a child of a form slot that is not a text list",
			want: ErrInvalidTextForm,
			corrupt: func(tx Tx, rig *formRig, n formNodes) error {
				slot, err := createTaggedNodeTx(tx, rig.forms.allFormSlots)
				if err != nil {
					return err
				}

				if linkErr := addRelationshipTx(tx, n.cat, slot); linkErr != nil {
					return linkErr
				}

				return addRelationshipTx(tx, slot, n.plain)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			forEachFormRig(t, func(t *testing.T, rig *formRig) {
				nodes := formNodes{cat: rig.intern(t, "cat"), c: rig.intern(t, "c"), plain: mustCreateNode(t, rig.graph)}
				before := len(mustFindNodes(t, rig.graph))

				err := rig.graph.Transact(func(tx Tx) error {
					return tc.corrupt(tx, rig, nodes)
				})
				if !errors.Is(err, tc.want) {
					t.Fatalf("Transact() error = %v, want %v", err, tc.want)
				}

				if got := len(mustFindNodes(t, rig.graph)); got != before {
					t.Fatalf("%d nodes after the declined commit, want %d", got, before)
				}

				rig.requireForms(t, nodes.cat, nil)
			})
		})
	}
}

// TestTextFormsAreNotAppendedTo: a form is spelled once; appending to it would
// make it spell something else, which the Checker declines.
func TestTextFormsAreNotAppendedTo(t *testing.T) {
	forEachFormRig(t, func(t *testing.T, rig *formRig) {
		cat := rig.intern(t, "cat")
		c := rig.intern(t, "c")
		at := rig.intern(t, "at")

		form := rig.ensureForm(t, cat, c, at)

		if _, err := rig.forms.AppendMany(rig.graph, form, []NodeID{c}); !errors.Is(err, ErrInvalidTextForm) {
			t.Fatalf("AppendMany() to a form error = %v, want %v", err, ErrInvalidTextForm)
		}

		parts, partsErr := rig.forms.Parts(rig.graph, form)
		if partsErr != nil || !reflect.DeepEqual(parts, []NodeID{c, at}) {
			t.Fatalf("Parts() = (%v,%v), want the form unchanged ([%d %d],nil)", parts, partsErr, c, at)
		}
	})
}

func TestTextFormsDeleteFormLeavesNothingAndKeepsStrings(t *testing.T) {
	forEachFormRig(t, func(t *testing.T, rig *formRig) {
		cat := rig.intern(t, "cat")
		c := rig.intern(t, "c")
		a := rig.intern(t, "a")
		tNode := rig.intern(t, "t")
		at := rig.intern(t, "at")

		before := len(mustFindNodes(t, rig.graph))

		first := rig.ensureForm(t, cat, c, at)
		second := rig.ensureForm(t, cat, c, a, tNode)

		if err := rig.forms.DeleteForm(rig.graph, first); err != nil {
			t.Fatalf("DeleteForm(first): %v", err)
		}

		rig.requireForms(t, cat, []NodeID{second})

		if err := rig.forms.DeleteForm(rig.graph, second); err != nil {
			t.Fatalf("DeleteForm(second): %v", err)
		}

		rig.requireForms(t, cat, nil)

		if got := len(mustFindNodes(t, rig.graph)); got != before {
			t.Fatalf("%d nodes after deleting every form, want %d (the slot must go with the last form)", got, before)
		}

		if err := rig.forms.DeleteForm(rig.graph, first); !errors.Is(err, ErrNodeNotFound) {
			t.Fatalf("DeleteForm() of a deleted form error = %v, want %v", err, ErrNodeNotFound)
		}

		for text, node := range map[string]NodeID{"cat": cat, "c": c, "at": at} {
			rig.requireText(t, node, text)
		}

		// A text list that is not a form is not deleted by DeleteForm.
		list, err := rig.forms.NewTextList(rig.graph)
		if err != nil {
			t.Fatalf("NewTextList(): %v", err)
		}

		if deleteErr := rig.forms.DeleteForm(rig.graph, list); !errors.Is(deleteErr, ErrNotTextForm) {
			t.Fatalf("DeleteForm(not a form) error = %v, want %v", deleteErr, ErrNotTextForm)
		}
	})
}

func TestTextListIsGenericAndChecksItsElements(t *testing.T) {
	forEachFormRig(t, func(t *testing.T, rig *formRig) {
		the := rig.intern(t, "The")
		space := rig.intern(t, " ")
		cat := rig.intern(t, "cat")
		plain := mustCreateNode(t, rig.graph)

		list, err := rig.forms.NewTextList(rig.graph)
		if err != nil {
			t.Fatalf("NewTextList(): %v", err)
		}

		if _, appendErr := rig.forms.AppendMany(rig.graph, list, []NodeID{the, space, cat}); appendErr != nil {
			t.Fatalf("AppendMany(): %v", appendErr)
		}

		spelled, textErr := rig.forms.Text(rig.graph, list)
		if textErr != nil || spelled != "The cat" {
			t.Fatalf("Text() = (%q,%v), want (\"The cat\",nil)", spelled, textErr)
		}

		if _, appendErr := rig.forms.AppendMany(rig.graph, list, []NodeID{cat, plain}); !errors.Is(appendErr, ErrNotString) {
			t.Fatalf("AppendMany(plain node) error = %v, want %v", appendErr, ErrNotString)
		}

		if spelled, textErr = rig.forms.Text(rig.graph, list); textErr != nil || spelled != "The cat" {
			t.Fatalf("Text() after the refused append = (%q,%v), want it unchanged", spelled, textErr)
		}

		// A list that is not a text list is not one for these methods.
		other, otherErr := rig.forms.lists.NewList(rig.graph)
		if otherErr != nil {
			t.Fatalf("NewList(): %v", otherErr)
		}

		if _, appendErr := rig.forms.AppendMany(rig.graph, other, []NodeID{cat}); !errors.Is(appendErr, ErrNotTextList) {
			t.Fatalf("AppendMany(not a text list) error = %v, want %v", appendErr, ErrNotTextList)
		}

		if _, partsErr := rig.forms.Parts(rig.graph, other); !errors.Is(partsErr, ErrNotTextList) {
			t.Fatalf("Parts(not a text list) error = %v, want %v", partsErr, ErrNotTextList)
		}
	})
}

// TestTextEndingWithIgnoresFormSlots: a trie node's form slot is a child of it
// but not a string, so suffix search must not return it.
func TestTextEndingWithIgnoresFormSlots(t *testing.T) {
	forEachFormRig(t, func(t *testing.T, rig *formRig) {
		cat := rig.intern(t, "cat")
		c := rig.intern(t, "c")
		at := rig.intern(t, "at")

		before, beforeErr := rig.text.EndingWith(rig.graph, "at", 10, false)
		if beforeErr != nil {
			t.Fatalf("EndingWith(): %v", beforeErr)
		}

		rig.ensureForm(t, cat, c, at)

		after, afterErr := rig.text.EndingWith(rig.graph, "at", 10, false)
		if afterErr != nil {
			t.Fatalf("EndingWith() with a form present: %v", afterErr)
		}

		if !reflect.DeepEqual(after, before) {
			t.Fatalf("EndingWith() = %v with a form present, want %v", after, before)
		}
	})
}

func TestHostRegistriesCarryTextForms(t *testing.T) {
	h := openTestHost(t, boltTestPath(t), nil)
	reg := h.Registries()

	cat, err := reg.Text.Intern(h.Graph(), "cat")
	if err != nil {
		t.Fatalf("Intern(cat): %v", err)
	}

	c, err := reg.Text.Intern(h.Graph(), "c")
	if err != nil {
		t.Fatalf("Intern(c): %v", err)
	}

	at, err := reg.Text.Intern(h.Graph(), "at")
	if err != nil {
		t.Fatalf("Intern(at): %v", err)
	}

	form, err := reg.TextForms.EnsureForm(h.Graph(), cat, []NodeID{c, at})
	if err != nil {
		t.Fatalf("EnsureForm(): %v", err)
	}

	forms, err := reg.TextForms.Forms(h.Graph(), cat)
	if err != nil || !reflect.DeepEqual(forms, []NodeID{form}) {
		t.Fatalf("Forms() = (%v,%v), want ([%d],nil)", forms, err, form)
	}
}