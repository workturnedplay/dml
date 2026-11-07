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

// Records with named fields (theorystate.md section 116). dml does not know
// what a record or a field means: the consumer supplies one tag node for the
// records and one tag node per field, and decides what each field holds.

var (
	// ErrNotRecord is returned for a node that is not tagged as a record of
	// the FieldRegistry asked about.
	ErrNotRecord = errors.New("node is not tagged as a record")

	// ErrUnknownField is returned for a field tag that was not given to
	// NewFieldRegistry.
	ErrUnknownField = errors.New("field is not one of this registry's fields")

	// ErrDuplicateField is returned by NewFieldRegistry for a field tag that
	// is listed twice or is the record tag.
	ErrDuplicateField = errors.New("a field tag is listed twice, or is the record tag")

	// ErrFieldValueTaken is returned when a second record would hold a value
	// that a unique field allows only one record to hold.
	ErrFieldValueTaken = errors.New("another record already has this value in a unique field")

	// ErrInvalidRecord is returned when a record or one of its field slots
	// does not have the shape FieldRegistry builds.
	ErrInvalidRecord = errors.New("record structure is invalid")
)

// FieldSpec declares one field of a FieldRegistry.
type FieldSpec struct {
	// Tag is the field's tag node: it tags every slot of this field.
	Tag NodeID

	// Unique means no two records may hold the same value in this field (a
	// rule's ID, say). It is enforced at write time and by the Checker.
	Unique bool
}

// fieldState is what a FieldRegistry keeps per field.
type fieldState struct {
	pointers *PointerRegistry
	unique   bool
}

// FieldRegistry keeps records with named fields.
//
//	(allRecords, R)                   R is a record
//	R -> S      (fieldTag, S)         S is R's slot for that field, at most one
//	S -> value                        the field's value, at most one
//
// Every field is a Pointer slot (PointerRegistry under the field's tag, so
// "at most one value" is enforced exactly as everywhere else), found by its
// tag and checked to belong to R alone, never by position (the same
// discipline as the capsule role slots). A slot is created by the first Set
// of its field; a record may lack any field. A value is any node: for text,
// the interned string node (theorystate.md section 114), so equal text is
// equal node and Owners finds every record holding it.
//
// Owners is the reverse lookup: from a value to the records holding it in a
// field, through the value's incoming relationships, never a scan of the
// records. The walk is shared with CapsuleRegistry.CapsulesWithValue
// (slotOwnersOf).
//
// The Checker declines, at commit: a field slot that belongs to no record, to
// two records, or a record with two slots for one field, and, for a unique
// field, a value held by two records. A unique field costs one walk over the
// value's incoming relationships per commit that touches the slot, which is
// small for the values unique fields hold (identifiers).
//
// Like every registry, FieldRegistry stores no graph reference
// (theorystate.md section 90).
type FieldRegistry struct {
	allRecords NodeID
	order      []NodeID
	fields     map[NodeID]fieldState
}

// NewFieldRegistry creates a FieldRegistry over graph. allRecords tags the
// records and each spec's tag its field slots; all must already exist (the
// consumer bootstraps them) and be distinct (ErrDuplicateField). It registers
// the Checker, and one PointerRegistry Checker per field.
func NewFieldRegistry(graph GraphAPI, allRecords NodeID, specs ...FieldSpec) (*FieldRegistry, error) {
	tags := make([]NodeID, 0, len(specs)+1)
	tags = append(tags, allRecords)

	seen := map[NodeID]struct{}{allRecords: {}}

	for _, spec := range specs {
		if _, taken := seen[spec.Tag]; taken {
			return nil, fmt.Errorf("%w: node %d", ErrDuplicateField, spec.Tag)
		}

		seen[spec.Tag] = struct{}{}
		tags = append(tags, spec.Tag)
	}

	// Checked before anything is registered, so a failure leaves no Checker
	// behind.
	if existsErr := requireAllExist(graph, tags...); existsErr != nil {
		return nil, existsErr
	}

	f := &FieldRegistry{
		allRecords: allRecords,
		order:      make([]NodeID, 0, len(specs)),
		fields:     make(map[NodeID]fieldState, len(specs)),
	}

	for _, spec := range specs {
		pointers, pointersErr := NewPointerRegistry(graph, spec.Tag)
		if pointersErr != nil {
			return nil, pointersErr
		}

		f.fields[spec.Tag] = fieldState{pointers: pointers, unique: spec.Unique}
		f.order = append(f.order, spec.Tag)
	}

	graph.RegisterChecker(Checker{
		Name:  fmt.Sprintf("FieldRegistry(tag=%d)", allRecords),
		Tags:  append([]NodeID{allRecords}, f.order...),
		Check: f.checkTouched,
	})

	return f, nil
}

// IsRecord reports whether id is currently tagged (allRecords, id).
func (f *FieldRegistry) IsRecord(graph GraphReader, id NodeID) (bool, error) {
	has, err := graph.HasRelationship(f.allRecords, id)

	return has, wrapInterfaceErr(err)
}

// requireRecord checks that record exists and is a record.
func (f *FieldRegistry) requireRecord(graph GraphReader, record NodeID) error {
	return requireTagged(graph, record, f.allRecords, ErrNotRecord)
}

// lookupField returns the state of the field with the given tag, or
// ErrUnknownField.
func (f *FieldRegistry) lookupField(field NodeID) (fieldState, error) {
	state, ok := f.fields[field]
	if !ok {
		return fieldState{}, fmt.Errorf("%w: node %d", ErrUnknownField, field)
	}

	return state, nil
}

// NewRecord creates a fresh node and tags it as a record, with no fields.
func (f *FieldRegistry) NewRecord(graph Transactor) (NodeID, error) {
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		return createTaggedNodeTx(tx, f.allRecords)
	})
}

// Get returns record's value for field. hasValue is false when the record has
// no slot for the field or its slot holds nothing.
func (f *FieldRegistry) Get(graph GraphReader, record, field NodeID) (value NodeID, hasValue bool, err error) {
	state, fieldErr := f.lookupField(field)
	if fieldErr != nil {
		return 0, false, fieldErr
	}

	if requireErr := f.requireRecord(graph, record); requireErr != nil {
		return 0, false, requireErr
	}

	slot, found, slotErr := findOwnedSlot(graph, record, f.allRecords, field)
	if slotErr != nil || !found {
		return 0, false, slotErr
	}

	return state.pointers.Target(graph, slot)
}

// Set makes value record's value for field, replacing any earlier one, as one
// transaction (nested if graph is a Tx). The field's slot is created if the
// record has none. For a unique field, another record already holding value is
// ErrFieldValueTaken and nothing changes. value must exist.
func (f *FieldRegistry) Set(graph Transactor, record, field, value NodeID) error {
	state, fieldErr := f.lookupField(field)
	if fieldErr != nil {
		return fieldErr
	}

	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		if requireErr := f.requireRecord(tx, record); requireErr != nil {
			return requireErr
		}

		if existsErr := requireExists(tx, value); existsErr != nil {
			return existsErr
		}

		if state.unique {
			if freeErr := f.requireValueFree(tx, field, record, value); freeErr != nil {
				return freeErr
			}
		}

		slot, found, slotErr := findOwnedSlot(tx, record, f.allRecords, field)
		if slotErr != nil {
			return slotErr
		}

		if found {
			return state.pointers.SetTarget(tx, slot, value)
		}

		newSlot, createErr := createTaggedNodeTx(tx, field)
		if createErr != nil {
			return createErr
		}

		if linkErr := addRelationshipTx(tx, record, newSlot); linkErr != nil {
			return linkErr
		}

		return addRelationshipTx(tx, newSlot, value)
	}))
}

// Clear removes record's value for field, if any, and reports whether it
// removed one. The slot itself stays (an empty slot is a valid state).
func (f *FieldRegistry) Clear(graph Transactor, record, field NodeID) (bool, error) {
	state, fieldErr := f.lookupField(field)
	if fieldErr != nil {
		return false, fieldErr
	}

	return transactBool(graph, func(tx Tx) (bool, error) {
		if requireErr := f.requireRecord(tx, record); requireErr != nil {
			return false, requireErr
		}

		slot, found, slotErr := findOwnedSlot(tx, record, f.allRecords, field)
		if slotErr != nil || !found {
			return false, slotErr
		}

		return state.pointers.RemoveTarget(tx, slot)
	})
}

// Owners returns the records whose value for field is value, ascending. It
// reads value's incoming relationships (in pages), not the records.
func (f *FieldRegistry) Owners(graph GraphReader, field, value NodeID) ([]NodeID, error) {
	if _, fieldErr := f.lookupField(field); fieldErr != nil {
		return nil, fieldErr
	}

	owners, err := slotOwnersOf(graph, value, field, f.allRecords)
	if err != nil {
		return nil, err
	}

	slices.Sort(owners)

	return owners, nil
}

// DeleteRecord deletes record with all its field slots, as one transaction. The
// values are never touched. If something other than its own slots still
// references the record (a list capsule holding it, say), the delete fails with
// ErrNodeNotEmpty and nothing changes.
func (f *FieldRegistry) DeleteRecord(graph Transactor, record NodeID) error {
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		if requireErr := f.requireRecord(tx, record); requireErr != nil {
			return requireErr
		}

		for _, field := range f.order {
			slot, found, slotErr := findOwnedSlot(tx, record, f.allRecords, field)
			if slotErr != nil {
				return slotErr
			}
			if !found {
				continue
			}

			if _, clearErr := f.fields[field].pointers.RemoveTarget(tx, slot); clearErr != nil {
				return clearErr
			}

			if unlinkErr := removeRelationshipTx(tx, record, slot); unlinkErr != nil {
				return unlinkErr
			}

			if deleteErr := untagAndDeleteNodeTx(tx, slot, field); deleteErr != nil {
				return deleteErr
			}
		}

		return untagAndDeleteNodeTx(tx, record, f.allRecords)
	}))
}

// requireValueFree returns ErrFieldValueTaken if a record other than record
// holds value in field.
func (f *FieldRegistry) requireValueFree(graph GraphReader, field, record, value NodeID) error {
	owners, err := slotOwnersOf(graph, value, field, f.allRecords)
	if err != nil {
		return err
	}

	for _, owner := range owners {
		if owner != record {
			return fmt.Errorf("%w: node %d holds %d in field %d", ErrFieldValueTaken, owner, value, field)
		}
	}

	return nil
}

// checkTouched is the commit-time Checker: every touched record and every
// touched field slot is validated.
func (f *FieldRegistry) checkTouched(g GraphReader, touched map[NodeID]struct{}) error {
	for node := range touched {
		isRecord, recordErr := f.IsRecord(g, node)
		if recordErr != nil {
			return recordErr
		}

		if isRecord {
			if checkErr := f.checkRecord(g, node); checkErr != nil {
				return checkErr
			}
		}

		for _, field := range f.order {
			isSlot, slotErr := g.HasRelationship(field, node)
			if slotErr != nil {
				return wrapInterfaceErr(slotErr)
			}
			if !isSlot {
				continue
			}

			if checkErr := f.checkSlot(g, field, node); checkErr != nil {
				return checkErr
			}
		}
	}

	return nil
}

// checkRecord: a record has at most one slot per field, and each is its own.
func (f *FieldRegistry) checkRecord(g GraphReader, record NodeID) error {
	for _, field := range f.order {
		if _, _, slotErr := findOwnedSlot(g, record, f.allRecords, field); slotErr != nil {
			return slotErr
		}
	}

	return nil
}

// checkSlot: a field slot belongs to exactly one record, which has no other
// slot for the field, and, for a unique field, its value is held by no other
// record.
func (f *FieldRegistry) checkSlot(g GraphReader, field, slot NodeID) error {
	record, hasRecord, recordErr := findUniqueTaggedParent(g, slot, f.allRecords)
	if recordErr != nil {
		return recordErr
	}
	if !hasRecord {
		return fmt.Errorf("%w: a slot of field %d belongs to no record", ErrInvalidRecord, field)
	}

	owned, hasOwned, ownedErr := findOwnedSlot(g, record, f.allRecords, field)
	if ownedErr != nil {
		return ownedErr
	}
	if !hasOwned || owned != slot {
		return fmt.Errorf("%w: record %d has another slot for field %d", ErrInvalidRecord, record, field)
	}

	state := f.fields[field]
	if !state.unique {
		return nil
	}

	value, hasValue, valueErr := state.pointers.Target(g, slot)
	if valueErr != nil || !hasValue {
		return valueErr
	}

	return f.requireValueFree(g, field, record, value)
}