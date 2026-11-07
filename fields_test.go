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

// fieldRig is a FieldRegistry over one backend with two fields: name (any
// value) and id (unique). The tag nodes are plain nodes, as a consumer would
// bootstrap them.
type fieldRig struct {
	graph   testGraph
	fields  *FieldRegistry
	records NodeID
	name    NodeID
	id      NodeID
}

func newFieldRig(t *testing.T, graph testGraph) *fieldRig {
	t.Helper()

	records := mustCreateNode(t, graph)
	name := mustCreateNode(t, graph)
	id := mustCreateNode(t, graph)

	fields, err := NewFieldRegistry(graph, records, FieldSpec{Tag: name}, FieldSpec{Tag: id, Unique: true})
	if err != nil {
		t.Fatalf("NewFieldRegistry(): %v", err)
	}

	return &fieldRig{graph: graph, fields: fields, records: records, name: name, id: id}
}

// forEachFieldRig runs body once per backend, each on a fresh graph.
func forEachFieldRig(t *testing.T, body func(t *testing.T, rig *fieldRig)) {
	t.Helper()

	for _, backend := range testBackends() {
		t.Run(backend.name, func(t *testing.T) {
			body(t, newFieldRig(t, backend.open(t)))
		})
	}
}

func (rig *fieldRig) newRecord(t *testing.T) NodeID {
	t.Helper()

	record, err := rig.fields.NewRecord(rig.graph)
	if err != nil {
		t.Fatalf("NewRecord(): %v", err)
	}

	return record
}

func (rig *fieldRig) set(t *testing.T, record, field, value NodeID) {
	t.Helper()

	if err := rig.fields.Set(rig.graph, record, field, value); err != nil {
		t.Fatalf("Set(%d, %d, %d): %v", record, field, value, err)
	}
}

func (rig *fieldRig) requireValue(t *testing.T, record, field NodeID, want NodeID, wantHas bool) {
	t.Helper()

	got, has, err := rig.fields.Get(rig.graph, record, field)
	if err != nil || has != wantHas || (wantHas && got != want) {
		t.Fatalf("Get(%d, %d) = (%d,%v,%v), want (%d,%v,nil)", record, field, got, has, err, want, wantHas)
	}
}

func (rig *fieldRig) requireOwners(t *testing.T, field, value NodeID, want []NodeID) {
	t.Helper()

	got, err := rig.fields.Owners(rig.graph, field, value)
	if err != nil {
		t.Fatalf("Owners(%d, %d): %v", field, value, err)
	}

	requireSameNodes(t, "Owners()", got, want)
}

func TestFieldSetGetReplaceAndClear(t *testing.T) {
	forEachFieldRig(t, func(t *testing.T, rig *fieldRig) {
		first := mustCreateNode(t, rig.graph)
		second := mustCreateNode(t, rig.graph)
		record := rig.newRecord(t)

		if is, err := rig.fields.IsRecord(rig.graph, record); err != nil || !is {
			t.Fatalf("IsRecord() = (%v,%v), want (true,nil)", is, err)
		}

		rig.requireValue(t, record, rig.name, 0, false)

		rig.set(t, record, rig.name, first)
		rig.requireValue(t, record, rig.name, first, true)

		rig.set(t, record, rig.name, first) // idempotent
		rig.set(t, record, rig.name, second)
		rig.requireValue(t, record, rig.name, second, true)

		// Another field of the same record is independent.
		rig.requireValue(t, record, rig.id, 0, false)

		cleared, err := rig.fields.Clear(rig.graph, record, rig.name)
		if err != nil || !cleared {
			t.Fatalf("Clear() = (%v,%v), want (true,nil)", cleared, err)
		}

		rig.requireValue(t, record, rig.name, 0, false)

		if again, againErr := rig.fields.Clear(rig.graph, record, rig.name); againErr != nil || again {
			t.Fatalf("second Clear() = (%v,%v), want (false,nil)", again, againErr)
		}

		// A record that never had the field.
		if never, neverErr := rig.fields.Clear(rig.graph, record, rig.id); neverErr != nil || never {
			t.Fatalf("Clear() of a field never set = (%v,%v), want (false,nil)", never, neverErr)
		}
	})
}

func TestFieldOwnersFindsRecordsByValueAndField(t *testing.T) {
	forEachFieldRig(t, func(t *testing.T, rig *fieldRig) {
		value := mustCreateNode(t, rig.graph)
		other := mustCreateNode(t, rig.graph)
		first := rig.newRecord(t)
		second := rig.newRecord(t)
		third := rig.newRecord(t)

		rig.set(t, first, rig.name, value)
		rig.set(t, second, rig.name, value)
		rig.set(t, third, rig.id, value)
		rig.set(t, mustNewOwnersRecord(t, rig), rig.name, other)

		// A parent that is nothing to do with fields is ignored.
		if _, err := addRelationshipVia(rig.graph, mustCreateNode(t, rig.graph), value); err != nil {
			t.Fatalf("AddRelationship(unrelated, value): %v", err)
		}

		rig.requireOwners(t, rig.name, value, []NodeID{first, second})
		rig.requireOwners(t, rig.id, value, []NodeID{third})
		rig.requireOwners(t, rig.id, other, nil)

		rig.set(t, second, rig.name, other)
		rig.requireOwners(t, rig.name, value, []NodeID{first})
	})
}

// mustNewOwnersRecord makes one more record, so Owners is checked against a
// graph with records holding other values.
func mustNewOwnersRecord(t *testing.T, rig *fieldRig) NodeID {
	t.Helper()

	return rig.newRecord(t)
}

func TestFieldUniqueRefusesASecondOwnerOfAValue(t *testing.T) {
	forEachFieldRig(t, func(t *testing.T, rig *fieldRig) {
		taken := mustCreateNode(t, rig.graph)
		free := mustCreateNode(t, rig.graph)
		first := rig.newRecord(t)
		second := rig.newRecord(t)

		rig.set(t, first, rig.id, taken)
		rig.set(t, first, rig.id, taken) // the holder may set it again

		before := len(mustFindNodes(t, rig.graph))

		if err := rig.fields.Set(rig.graph, second, rig.id, taken); !errors.Is(err, ErrFieldValueTaken) {
			t.Fatalf("Set() of a taken unique value error = %v, want %v", err, ErrFieldValueTaken)
		}

		if got := len(mustFindNodes(t, rig.graph)); got != before {
			t.Fatalf("%d nodes after a refused Set(), want %d", got, before)
		}

		rig.requireValue(t, second, rig.id, 0, false)

		rig.set(t, second, rig.id, free)
		rig.set(t, first, rig.id, free2(t, rig))
		rig.set(t, second, rig.id, taken) // free again once its holder moved on
	})
}

// free2 returns a fresh node, for tests that need one more value.
func free2(t *testing.T, rig *fieldRig) NodeID {
	t.Helper()

	return mustCreateNode(t, rig.graph)
}

func TestFieldOperationsRejectWhatIsNotTheirs(t *testing.T) {
	const nonexistent NodeID = 999999

	forEachFieldRig(t, func(t *testing.T, rig *fieldRig) {
		record := rig.newRecord(t)
		value := mustCreateNode(t, rig.graph)
		plain := mustCreateNode(t, rig.graph)

		if err := rig.fields.Set(rig.graph, plain, rig.name, value); !errors.Is(err, ErrNotRecord) {
			t.Fatalf("Set(not a record) error = %v, want %v", err, ErrNotRecord)
		}

		if err := rig.fields.Set(rig.graph, nonexistent, rig.name, value); !errors.Is(err, ErrNodeNotFound) {
			t.Fatalf("Set(nonexistent record) error = %v, want %v", err, ErrNodeNotFound)
		}

		if err := rig.fields.Set(rig.graph, record, rig.name, nonexistent); !errors.Is(err, ErrNodeNotFound) {
			t.Fatalf("Set(nonexistent value) error = %v, want %v", err, ErrNodeNotFound)
		}

		if err := rig.fields.Set(rig.graph, record, plain, value); !errors.Is(err, ErrUnknownField) {
			t.Fatalf("Set(unknown field) error = %v, want %v", err, ErrUnknownField)
		}

		if _, _, err := rig.fields.Get(rig.graph, plain, rig.name); !errors.Is(err, ErrNotRecord) {
			t.Fatalf("Get(not a record) error = %v, want %v", err, ErrNotRecord)
		}

		if _, _, err := rig.fields.Get(rig.graph, record, plain); !errors.Is(err, ErrUnknownField) {
			t.Fatalf("Get(unknown field) error = %v, want %v", err, ErrUnknownField)
		}

		if _, err := rig.fields.Owners(rig.graph, plain, value); !errors.Is(err, ErrUnknownField) {
			t.Fatalf("Owners(unknown field) error = %v, want %v", err, ErrUnknownField)
		}

		if _, err := rig.fields.Owners(rig.graph, rig.name, nonexistent); !errors.Is(err, ErrNodeNotFound) {
			t.Fatalf("Owners(nonexistent value) error = %v, want %v", err, ErrNodeNotFound)
		}

		if _, err := rig.fields.Clear(rig.graph, plain, rig.name); !errors.Is(err, ErrNotRecord) {
			t.Fatalf("Clear(not a record) error = %v, want %v", err, ErrNotRecord)
		}

		if err := rig.fields.DeleteRecord(rig.graph, plain); !errors.Is(err, ErrNotRecord) {
			t.Fatalf("DeleteRecord(not a record) error = %v, want %v", err, ErrNotRecord)
		}
	})
}

func TestNewFieldRegistryRejectsBadDeclarations(t *testing.T) {
	const nonexistent NodeID = 999999

	g := newTestGraph(t)
	records := mustCreateNode(t, g)
	field := mustCreateNode(t, g)

	cases := []struct {
		name    string
		records NodeID
		specs   []FieldSpec
		want    error
	}{
		{name: "the same field twice", records: records, specs: []FieldSpec{{Tag: field}, {Tag: field}}, want: ErrDuplicateField},
		{name: "the record tag as a field", records: records, specs: []FieldSpec{{Tag: records}}, want: ErrDuplicateField},
		{name: "a missing field tag", records: records, specs: []FieldSpec{{Tag: nonexistent}}, want: ErrNodeNotFound},
		{name: "a missing record tag", records: nonexistent, want: ErrNodeNotFound},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewFieldRegistry(g, tc.records, tc.specs...); !errors.Is(err, tc.want) {
				t.Fatalf("NewFieldRegistry() error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestFieldDeleteRecordLeavesNoSlotsAndKeepsValues(t *testing.T) {
	forEachFieldRig(t, func(t *testing.T, rig *fieldRig) {
		name := mustCreateNode(t, rig.graph)
		id := mustCreateNode(t, rig.graph)
		before := len(mustFindNodes(t, rig.graph))

		record := rig.newRecord(t)
		rig.set(t, record, rig.name, name)
		rig.set(t, record, rig.id, id)

		if err := rig.fields.DeleteRecord(rig.graph, record); err != nil {
			t.Fatalf("DeleteRecord(): %v", err)
		}

		if got := len(mustFindNodes(t, rig.graph)); got != before {
			t.Fatalf("%d nodes after creating and deleting a record, want %d", got, before)
		}

		if !mustNodeExists(t, rig.graph, name) || !mustNodeExists(t, rig.graph, id) {
			t.Fatal("DeleteRecord() deleted a value")
		}

		rig.requireOwners(t, rig.id, id, nil)
	})
}

func TestFieldDeleteRecordRefusedWhileReferencedChangesNothing(t *testing.T) {
	forEachFieldRig(t, func(t *testing.T, rig *fieldRig) {
		value := mustCreateNode(t, rig.graph)
		record := rig.newRecord(t)
		rig.set(t, record, rig.name, value)

		referrer := mustCreateNode(t, rig.graph)
		if _, err := addRelationshipVia(rig.graph, referrer, record); err != nil {
			t.Fatalf("AddRelationship(referrer, record): %v", err)
		}

		if err := rig.fields.DeleteRecord(rig.graph, record); !errors.Is(err, ErrNodeNotEmpty) {
			t.Fatalf("DeleteRecord() of a referenced record error = %v, want %v", err, ErrNodeNotEmpty)
		}

		if is, err := rig.fields.IsRecord(rig.graph, record); err != nil || !is {
			t.Fatalf("IsRecord() = (%v,%v) after a refused delete, want (true,nil)", is, err)
		}

		rig.requireValue(t, record, rig.name, value, true)

		if _, err := removeRelationshipVia(rig.graph, referrer, record); err != nil {
			t.Fatalf("RemoveRelationship(referrer, record): %v", err)
		}

		if err := rig.fields.DeleteRecord(rig.graph, record); err != nil {
			t.Fatalf("DeleteRecord() once unreferenced: %v", err)
		}
	})
}

// fieldFixture is two records, each with a unique id, and their id slots.
type fieldFixture struct {
	first, second             NodeID
	firstIDSlot, secondIDSlot NodeID
	firstID, secondID         NodeID
}

func newFieldFixture(t *testing.T, rig *fieldRig) fieldFixture {
	t.Helper()

	fx := fieldFixture{
		first:    rig.newRecord(t),
		second:   rig.newRecord(t),
		firstID:  mustCreateNode(t, rig.graph),
		secondID: mustCreateNode(t, rig.graph),
	}

	rig.set(t, fx.first, rig.id, fx.firstID)
	rig.set(t, fx.second, rig.id, fx.secondID)

	slot := func(record NodeID) NodeID {
		found, has, err := findOwnedSlot(rig.graph, record, rig.records, rig.id)
		if err != nil || !has {
			t.Fatalf("findOwnedSlot(%d) = (%v,%v), want a slot", record, has, err)
		}

		return found
	}

	fx.firstIDSlot = slot(fx.first)
	fx.secondIDSlot = slot(fx.second)

	return fx
}

func TestFieldCheckerDeclinesEachHandMadeViolation(t *testing.T) {
	cases := []struct {
		name    string
		want    error
		corrupt func(tx Tx, rig *fieldRig, fx fieldFixture) error
	}{
		{
			name: "a slot that belongs to no record",
			want: ErrInvalidRecord,
			corrupt: func(tx Tx, rig *fieldRig, _ fieldFixture) error {
				_, err := createTaggedNodeTx(tx, rig.id)

				return err
			},
		},
		{
			name: "a record with two slots for one field",
			want: ErrAmbiguousPointerMetadata,
			corrupt: func(tx Tx, rig *fieldRig, fx fieldFixture) error {
				extra, err := createTaggedNodeTx(tx, rig.id)
				if err != nil {
					return err
				}

				return addRelationshipTx(tx, fx.first, extra)
			},
		},
		{
			name: "a slot shared by two records",
			want: ErrAmbiguousPointerMetadata,
			corrupt: func(tx Tx, _ *fieldRig, fx fieldFixture) error {
				return addRelationshipTx(tx, fx.second, fx.firstIDSlot)
			},
		},
		{
			name: "a unique value held by two records",
			want: ErrFieldValueTaken,
			corrupt: func(tx Tx, _ *fieldRig, fx fieldFixture) error {
				if removeErr := removeRelationshipTx(tx, fx.secondIDSlot, fx.secondID); removeErr != nil {
					return removeErr
				}

				return addRelationshipTx(tx, fx.secondIDSlot, fx.firstID)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			forEachFieldRig(t, func(t *testing.T, rig *fieldRig) {
				fx := newFieldFixture(t, rig)
				before := len(mustFindNodes(t, rig.graph))

				err := rig.graph.Transact(func(tx Tx) error {
					return tc.corrupt(tx, rig, fx)
				})
				if !errors.Is(err, tc.want) {
					t.Fatalf("Transact() error = %v, want %v", err, tc.want)
				}

				if got := len(mustFindNodes(t, rig.graph)); got != before {
					t.Fatalf("%d nodes after the declined commit, want %d", got, before)
				}

				rig.requireValue(t, fx.first, rig.id, fx.firstID, true)
				rig.requireValue(t, fx.second, rig.id, fx.secondID, true)
			})
		})
	}
}