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
	"testing"
)

func TestTextFormsContainingFindsTheFormsAndTextsThatUseAToken(t *testing.T) {
	forEachFormRig(t, func(t *testing.T, rig *formRig) {
		cat := rig.intern(t, "cat")
		hat := rig.intern(t, "hat")
		c := rig.intern(t, "c")
		a := rig.intern(t, "a")
		tNode := rig.intern(t, "t")
		h := rig.intern(t, "h")
		at := rig.intern(t, "at")
		dog := rig.intern(t, "dog")
		plain := mustCreateNode(t, rig.graph)

		catSplit := rig.ensureForm(t, cat, c, at)
		catChars := rig.ensureForm(t, cat, c, a, tNode)
		hatSplit := rig.ensureForm(t, hat, h, at)

		// Neither of these is a form, so neither may show up.
		loose, err := rig.forms.NewTextList(rig.graph)
		if err != nil {
			t.Fatalf("NewTextList(): %v", err)
		}

		if _, appendErr := rig.forms.AppendMany(rig.graph, loose, []NodeID{at}); appendErr != nil {
			t.Fatalf("AppendMany(): %v", appendErr)
		}

		other, err := rig.forms.lists.NewList(rig.graph)
		if err != nil {
			t.Fatalf("NewList(): %v", err)
		}

		if _, appendErr := rig.forms.lists.Append(rig.graph, other, at); appendErr != nil {
			t.Fatalf("Append(): %v", appendErr)
		}

		cases := []struct {
			name      string
			part      NodeID
			wantForms []NodeID
			wantTexts []NodeID
		}{
			{name: "a token two texts use", part: at, wantForms: []NodeID{catSplit, hatSplit}, wantTexts: []NodeID{cat, hat}},
			{name: "a token used by two forms of one text", part: c, wantForms: []NodeID{catSplit, catChars}, wantTexts: []NodeID{cat}},
			{name: "a token one form uses", part: h, wantForms: []NodeID{hatSplit}, wantTexts: []NodeID{hat}},
			{name: "a string no form uses", part: dog},
		}

		for _, tc := range cases {
			forms, formsErr := rig.forms.FormsContaining(rig.graph, tc.part)
			if formsErr != nil {
				t.Fatalf("%s: FormsContaining(): %v", tc.name, formsErr)
			}

			requireSameNodes(t, tc.name+": FormsContaining()", forms, tc.wantForms)

			texts, textsErr := rig.forms.TextsContaining(rig.graph, tc.part)
			if textsErr != nil {
				t.Fatalf("%s: TextsContaining(): %v", tc.name, textsErr)
			}

			requireSameNodes(t, tc.name+": TextsContaining()", texts, tc.wantTexts)
		}

		if _, formsErr := rig.forms.FormsContaining(rig.graph, plain); !errors.Is(formsErr, ErrNotString) {
			t.Fatalf("FormsContaining(not a string) error = %v, want %v", formsErr, ErrNotString)
		}

		if _, textsErr := rig.forms.TextsContaining(rig.graph, plain); !errors.Is(textsErr, ErrNotString) {
			t.Fatalf("TextsContaining(not a string) error = %v, want %v", textsErr, ErrNotString)
		}

		if deleteErr := rig.forms.DeleteForm(rig.graph, catSplit); deleteErr != nil {
			t.Fatalf("DeleteForm(): %v", deleteErr)
		}

		forms, formsErr := rig.forms.FormsContaining(rig.graph, at)
		if formsErr != nil {
			t.Fatalf("FormsContaining() after DeleteForm(): %v", formsErr)
		}

		requireSameNodes(t, "FormsContaining() after DeleteForm()", forms, []NodeID{hatSplit})
	})
}