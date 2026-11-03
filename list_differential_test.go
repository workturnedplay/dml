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
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"
)

// Randomized differential tests (implementation_state.md item 79): a long
// random sequence of operations is applied both to the real structure and to
// a trivially correct model, and after every step the two must agree. The
// seeds are fixed, so a failure reproduces, and the step label says which
// operation broke it.

var differentialSeeds = []uint64{1, 2, 3}

const (
	// listDifferentialSteps is how many operations one list run applies.
	listDifferentialSteps = 150

	// logDifferentialSteps is how many operations one log run applies. Each
	// step checks Contains of every node against Evaluate, so it is costlier.
	logDifferentialSteps = 60

	// differentialShortDivisor divides both under -short.
	differentialShortDivisor = 4

	// listDifferentialModels is how many lists share one run, so that a
	// mistake that leaks between lists shows.
	listDifferentialModels = 3

	// differentialPoolSize is how many plain nodes the runs draw from: few
	// enough that values repeat.
	differentialPoolSize = 8
)

// errDifferentialRollback fails a transaction on purpose, to check that every
// operation rolls back completely.
var errDifferentialRollback = errors.New("rollback on purpose")

// differentialSteps scales a step count down under -short.
func differentialSteps(steps int) int {
	if testing.Short() {
		return max(steps/differentialShortDivisor, 1)
	}

	return steps
}

func newDifferentialRNG(seed uint64) *rand.Rand {
	//nolint:gosec // deterministic randomness on purpose: failures must reproduce
	return rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
}

// pickFrom returns a random element of items, which must not be empty.
func pickFrom[T any](rng *rand.Rand, items []T) T {
	return items[rng.IntN(len(items))]
}

// ---------------------------------------------------------------------
// Ordered lists against a slice.

// listModelEntry is one element of a modelled list: its capsule and value.
type listModelEntry struct {
	capsule, value NodeID
}

// listModel is what a list must contain, as a plain slice.
type listModel struct {
	list    NodeID
	entries []listModelEntry
}

// values returns the modelled values in order, nil if there are none (what
// Elements returns for an empty list).
func (m *listModel) values() []NodeID {
	if len(m.entries) == 0 {
		return nil
	}

	values := make([]NodeID, 0, len(m.entries))
	for _, entry := range m.entries {
		values = append(values, entry.value)
	}

	return values
}

// capsulesOf returns the capsules of the modelled elements holding value.
func (m *listModel) capsulesOf(value NodeID) []NodeID {
	capsules := make([]NodeID, 0, len(m.entries))

	for _, entry := range m.entries {
		if entry.value == value {
			capsules = append(capsules, entry.capsule)
		}
	}

	return capsules
}

// listOp is one random operation: do performs it on api and returns what
// updates the model if it is kept.
type listOp struct {
	name string
	do   func(api Transactor) (apply func(), err error)
}

// listDifferential drives several lists of one implementation at random and
// compares them with their models.
type listDifferential struct {
	t      *testing.T
	g      testGraph
	lists  OrderedList
	rng    *rand.Rand
	models []*listModel
	values []NodeID

	// loose are capsules unlinked with RemoveWithoutDeletingCapsule, gone
	// are capsules Remove deleted.
	loose []NodeID
	gone  []NodeID
}

func newListDifferential(t *testing.T, g testGraph, lists OrderedList, seed uint64) *listDifferential {
	t.Helper()

	d := &listDifferential{t: t, g: g, lists: lists, rng: newDifferentialRNG(seed)}

	for range listDifferentialModels {
		d.models = append(d.models, &listModel{list: mustNewOrderedList(t, lists, g)})
	}

	for range differentialPoolSize {
		d.values = append(d.values, mustCreateNode(t, g))
	}

	return d
}

// pickOp chooses a random operation on m.
func (d *listDifferential) pickOp(m *listModel) listOp {
	value := d.rng.IntN(len(d.values))
	size := len(m.entries)
	kind := d.rng.IntN(6)

	// Operations on an element need one.
	if size == 0 && (kind == 2 || kind == 3 || kind == 4) {
		kind = 0
	}

	index := 0
	if size > 0 {
		index = d.rng.IntN(size)
	}

	switch kind {
	case 1:
		return d.prependOp(m, d.values[value])
	case 2:
		return d.insertAfterOp(m, index, d.values[value])
	case 3:
		return d.removeOp(m, index)
	case 4:
		return d.removeWithoutDeletingOp(m, index)
	case 5:
		return d.appendManyOp(m)
	default:
		return listOp{name: "append", do: func(api Transactor) (func(), error) {
			capsule, err := d.lists.Append(api, m.list, d.values[value])
			if err != nil {
				return nil, fmt.Errorf("appending: %w", err)
			}

			return func() {
				m.entries = append(m.entries, listModelEntry{capsule: capsule, value: d.values[value]})
			}, nil
		}}
	}
}

func (d *listDifferential) prependOp(m *listModel, value NodeID) listOp {
	return listOp{name: "prepend", do: func(api Transactor) (func(), error) {
		capsule, err := d.lists.Prepend(api, m.list, value)
		if err != nil {
			return nil, fmt.Errorf("prepending: %w", err)
		}

		return func() {
			m.entries = slices.Insert(m.entries, 0, listModelEntry{capsule: capsule, value: value})
		}, nil
	}}
}

func (d *listDifferential) insertAfterOp(m *listModel, index int, value NodeID) listOp {
	return listOp{name: "insert after", do: func(api Transactor) (func(), error) {
		capsule, err := d.lists.InsertAfter(api, m.list, m.entries[index].capsule, value)
		if err != nil {
			return nil, fmt.Errorf("inserting after element %d: %w", index, err)
		}

		return func() {
			m.entries = slices.Insert(m.entries, index+1, listModelEntry{capsule: capsule, value: value})
		}, nil
	}}
}

func (d *listDifferential) removeOp(m *listModel, index int) listOp {
	return listOp{name: "remove", do: func(api Transactor) (func(), error) {
		capsule := m.entries[index].capsule

		deleted, err := d.lists.Remove(api, m.list, capsule)
		if err != nil {
			return nil, fmt.Errorf("removing element %d: %w", index, err)
		}

		if !deleted {
			return nil, fmt.Errorf("the capsule %d of element %d was not deleted although nothing refers to it", capsule, index)
		}

		return func() {
			m.entries = slices.Delete(m.entries, index, index+1)
			d.gone = append(d.gone, capsule)
		}, nil
	}}
}

func (d *listDifferential) removeWithoutDeletingOp(m *listModel, index int) listOp {
	return listOp{name: "remove without deleting", do: func(api Transactor) (func(), error) {
		capsule := m.entries[index].capsule

		if err := d.lists.RemoveWithoutDeletingCapsule(api, m.list, capsule); err != nil {
			return nil, fmt.Errorf("removing element %d without deleting it: %w", index, err)
		}

		return func() {
			m.entries = slices.Delete(m.entries, index, index+1)
			d.loose = append(d.loose, capsule)
		}, nil
	}}
}

func (d *listDifferential) appendManyOp(m *listModel) listOp {
	values := make([]NodeID, 1+d.rng.IntN(4))
	for i := range values {
		values[i] = pickFrom(d.rng, d.values)
	}

	return listOp{name: "append many", do: func(api Transactor) (func(), error) {
		capsules, err := d.lists.AppendMany(api, m.list, values)
		if err != nil {
			return nil, fmt.Errorf("appending %d values: %w", len(values), err)
		}

		if len(capsules) != len(values) {
			return nil, fmt.Errorf("AppendMany returned %d capsules for %d values", len(capsules), len(values))
		}

		return func() {
			for i, capsule := range capsules {
				m.entries = append(m.entries, listModelEntry{capsule: capsule, value: values[i]})
			}
		}, nil
	}}
}

// run applies one random operation to one random list, kept or rolled back,
// and then checks everything.
func (d *listDifferential) run(step int) {
	d.t.Helper()

	m := pickFrom(d.rng, d.models)
	op := d.pickOp(m)
	rollback := d.rng.IntN(6) == 0
	label := fmt.Sprintf("step %d, %s (rolled back: %v)", step, op.name, rollback)

	if rollback {
		err := d.g.Transact(func(tx Tx) error {
			if _, doErr := op.do(tx); doErr != nil {
				return doErr
			}

			return errDifferentialRollback
		})
		if !errors.Is(err, errDifferentialRollback) {
			d.t.Fatalf("%s: Transact() error = %v, want %v", label, err, errDifferentialRollback)
		}
	} else {
		apply, err := op.do(d.g)
		if err != nil {
			d.t.Fatalf("%s: %v", label, err)
		}

		apply()
	}

	d.verify(label)
}

// verify checks every list against its model, and the capsules that left.
func (d *listDifferential) verify(label string) {
	d.t.Helper()

	for _, m := range d.models {
		d.verifyModel(label, m)
	}

	if err := d.lists.VerifyStructures(d.g); err != nil {
		d.t.Fatalf("%s: VerifyStructures(): %v", label, err)
	}

	for _, capsule := range d.loose {
		if !mustNodeExists(d.t, d.g, capsule) {
			d.t.Fatalf("%s: capsule %d, removed without deleting it, no longer exists", label, capsule)
		}

		owners, err := d.lists.ListsOf(d.g, capsule)
		if err != nil || len(owners) != 0 {
			d.t.Fatalf("%s: ListsOf(loose capsule %d) = (%v,%v), want none", label, capsule, owners, err)
		}
	}

	for _, capsule := range d.gone {
		if mustNodeExists(d.t, d.g, capsule) {
			d.t.Fatalf("%s: capsule %d, deleted by Remove, still exists", label, capsule)
		}
	}
}

func (d *listDifferential) verifyModel(label string, m *listModel) {
	d.t.Helper()

	elements, err := d.lists.Elements(d.g, m.list)
	if err != nil {
		d.t.Fatalf("%s: Elements(%d): %v", label, m.list, err)
	}

	if !reflect.DeepEqual(elements, m.values()) {
		d.t.Fatalf("%s: Elements(%d) = %v, want %v", label, m.list, elements, m.values())
	}

	d.verifyEnds(label, m)

	for i, entry := range m.entries {
		d.verifyEntry(label, m, i, entry)
	}

	for _, value := range d.values {
		d.verifyValue(label, m, value)
	}
}

// verifyEnds checks Head and Tail.
func (d *listDifferential) verifyEnds(label string, m *listModel) {
	d.t.Helper()

	head, hasHead, headErr := d.lists.Head(d.g, m.list)
	tail, hasTail, tailErr := d.lists.Tail(d.g, m.list)

	if headErr != nil || tailErr != nil {
		d.t.Fatalf("%s: Head/Tail(%d) errors = %v, %v", label, m.list, headErr, tailErr)
	}

	if len(m.entries) == 0 {
		if hasHead || hasTail {
			d.t.Fatalf("%s: an empty list %d has head=%v tail=%v", label, m.list, hasHead, hasTail)
		}

		return
	}

	wantHead, wantTail := m.entries[0].capsule, m.entries[len(m.entries)-1].capsule

	if !hasHead || head != wantHead || !hasTail || tail != wantTail {
		d.t.Fatalf("%s: Head/Tail(%d) = (%d,%v)/(%d,%v), want %d and %d", label, m.list, head, hasHead, tail, hasTail, wantHead, wantTail)
	}
}

// verifyEntry checks what one modelled element must have: membership, its
// one list, and the step back to the element before it.
func (d *listDifferential) verifyEntry(label string, m *listModel, index int, entry listModelEntry) {
	d.t.Helper()

	member, memberErr := d.lists.IsMember(d.g, m.list, entry.capsule)
	if memberErr != nil || !member {
		d.t.Fatalf("%s: IsMember(%d, %d) = (%v,%v), want (true,nil)", label, m.list, entry.capsule, member, memberErr)
	}

	owners, ownersErr := d.lists.ListsOf(d.g, entry.capsule)
	if ownersErr != nil || !reflect.DeepEqual(owners, []NodeID{m.list}) {
		d.t.Fatalf("%s: ListsOf(%d) = (%v,%v), want ([%d],nil)", label, entry.capsule, owners, ownersErr, m.list)
	}

	prev, hasPrev, stepErr := d.lists.stepBack(d.g, m.list, entry.capsule)
	if stepErr != nil {
		d.t.Fatalf("%s: stepBack(%d, %d): %v", label, m.list, entry.capsule, stepErr)
	}

	if index == 0 {
		if hasPrev {
			d.t.Fatalf("%s: stepBack of the head %d reported a previous capsule %d", label, entry.capsule, prev)
		}

		return
	}

	if want := m.entries[index-1].capsule; !hasPrev || prev != want {
		d.t.Fatalf("%s: stepBack(%d) = (%d,%v), want (%d,true)", label, entry.capsule, prev, hasPrev, want)
	}
}

// verifyValue checks Contains and OccurrencesOf for one value.
func (d *listDifferential) verifyValue(label string, m *listModel, value NodeID) {
	d.t.Helper()

	want := m.capsulesOf(value)

	found, ok, err := d.lists.Contains(d.g, m.list, value)
	if err != nil {
		d.t.Fatalf("%s: Contains(%d, %d): %v", label, m.list, value, err)
	}

	if ok != (len(want) > 0) || (ok && !slices.Contains(want, found)) {
		d.t.Fatalf("%s: Contains(%d, %d) = (%d,%v), want one of %v", label, m.list, value, found, ok, want)
	}

	occurrences, occurrencesErr := d.lists.OccurrencesOf(d.g, m.list, value)
	if occurrencesErr != nil {
		d.t.Fatalf("%s: OccurrencesOf(%d, %d): %v", label, m.list, value, occurrencesErr)
	}

	if !reflect.DeepEqual(sortedNodeIDs(occurrences), sortedNodeIDs(want)) {
		d.t.Fatalf("%s: OccurrencesOf(%d, %d) = %v, want %v", label, m.list, value, occurrences, want)
	}
}

func TestOrderedListsMatchASliceModelUnderRandomOperations(t *testing.T) {
	forEachOrderedList(t, func(t *testing.T, g testGraph, lists OrderedList) {
		for _, seed := range differentialSeeds {
			d := newListDifferential(t, g, lists, seed)

			for step := range differentialSteps(listDifferentialSteps) {
				d.run(step)
			}
		}
	})
}

// ---------------------------------------------------------------------
// CompositeSetLog.Contains against Evaluate.

// logDifferential builds sets, composites and logs that expand each other
// without cycles (a composite expands sets and lower composites, a log sets,
// composites and lower logs) and changes them at random.
type logDifferential struct {
	t   *testing.T
	rig *logRig
	rng *rand.Rand

	plain      []NodeID
	sets       []NodeID
	composites []NodeID
	logs       []NodeID

	// compositeOperands and logCapsules hold, per composite and per log, what
	// RemoveOperand and RemoveOperation need.
	compositeOperands [][]NodeID
	logCapsules       [][]NodeID
}

const (
	logDifferentialSets       = 2
	logDifferentialComposites = 2
	logDifferentialLogs       = 3
)

func newLogDifferential(t *testing.T, rig *logRig, seed uint64) *logDifferential {
	t.Helper()

	d := &logDifferential{t: t, rig: rig, rng: newDifferentialRNG(seed)}

	for range differentialPoolSize {
		d.plain = append(d.plain, mustCreateNode(t, rig.graph))
	}

	for range logDifferentialSets {
		set, err := rig.sets.NewSet(rig.graph)
		if err != nil {
			t.Fatalf("NewSet(): %v", err)
		}

		d.sets = append(d.sets, set)
	}

	for range logDifferentialComposites {
		composite, err := rig.composites.NewCompositeSet(rig.graph)
		if err != nil {
			t.Fatalf("NewCompositeSet(): %v", err)
		}

		d.composites = append(d.composites, composite)
	}

	for range logDifferentialLogs {
		d.logs = append(d.logs, mustNewLog(t, rig))
	}

	d.compositeOperands = make([][]NodeID, len(d.composites))
	d.logCapsules = make([][]NodeID, len(d.logs))

	return d
}

// everyNode returns every node the run knows, which any operand may be (as a
// scalar) and which Contains is asked about.
func (d *logDifferential) everyNode() []NodeID {
	return slices.Concat(d.plain, d.sets, d.composites, d.logs)
}

// operand picks an operand: half the time one of expandable (expanded into
// its members), otherwise any node as a plain element.
func (d *logDifferential) operand(expandable []NodeID) (operand NodeID, expand bool) {
	if len(expandable) > 0 && d.rng.IntN(2) == 0 {
		return pickFrom(d.rng, expandable), true
	}

	return pickFrom(d.rng, d.everyNode()), false
}

// run applies one random operation and then checks everything.
func (d *logDifferential) run(step int) {
	d.t.Helper()

	g := d.rig.graph
	op := ""

	switch d.rng.IntN(8) {
	case 0:
		op = "set add"

		if _, err := d.rig.sets.Add(g, pickFrom(d.rng, d.sets), pickFrom(d.rng, d.everyNode())); err != nil {
			d.t.Fatalf("step %d, %s: %v", step, op, err)
		}
	case 1:
		op = "set remove"

		if _, err := d.rig.sets.Remove(g, pickFrom(d.rng, d.sets), pickFrom(d.rng, d.everyNode())); err != nil {
			d.t.Fatalf("step %d, %s: %v", step, op, err)
		}
	case 2:
		op = "composite add operand"
		d.addCompositeOperand(step, op)
	case 3:
		op = "composite remove operand"
		d.removeCompositeOperand(step, op)
	case 6:
		op = "log remove operation"
		d.removeLogOperation(step, op)
	default:
		op = "log append operation"
		d.appendLogOperation()
	}

	d.verify(fmt.Sprintf("step %d, %s", step, op))
}

func (d *logDifferential) addCompositeOperand(step int, op string) {
	d.t.Helper()

	i := d.rng.IntN(len(d.composites))
	operand, expand := d.operand(slices.Concat(d.sets, d.composites[:i]))
	additive := d.rng.IntN(2) == 0

	descriptor, err := d.rig.composites.AddOperand(d.rig.graph, d.composites[i], operand, additive, expand)
	if err != nil {
		d.t.Fatalf("step %d, %s: %v", step, op, err)
	}

	d.compositeOperands[i] = append(d.compositeOperands[i], descriptor)
}

func (d *logDifferential) removeCompositeOperand(step int, op string) {
	d.t.Helper()

	i := d.rng.IntN(len(d.composites))
	if len(d.compositeOperands[i]) == 0 {
		return
	}

	j := d.rng.IntN(len(d.compositeOperands[i]))

	if err := d.rig.composites.RemoveOperand(d.rig.graph, d.composites[i], d.compositeOperands[i][j]); err != nil {
		d.t.Fatalf("step %d, %s: %v", step, op, err)
	}

	d.compositeOperands[i] = slices.Delete(d.compositeOperands[i], j, j+1)
}

func (d *logDifferential) appendLogOperation() {
	d.t.Helper()

	i := d.rng.IntN(len(d.logs))
	operand, expand := d.operand(slices.Concat(d.sets, d.composites, d.logs[:i]))
	additive := d.rng.IntN(2) == 0

	_, capsule := mustAppendOperation(d.t, d.rig, d.logs[i], operand, additive, expand)

	d.logCapsules[i] = append(d.logCapsules[i], capsule)
}

func (d *logDifferential) removeLogOperation(step int, op string) {
	d.t.Helper()

	i := d.rng.IntN(len(d.logs))
	if len(d.logCapsules[i]) == 0 {
		return
	}

	j := d.rng.IntN(len(d.logCapsules[i]))

	if err := d.rig.logs.RemoveOperation(d.rig.graph, d.logs[i], d.logCapsules[i][j]); err != nil {
		d.t.Fatalf("step %d, %s: %v", step, op, err)
	}

	d.logCapsules[i] = slices.Delete(d.logCapsules[i], j, j+1)
}

// verify checks that Contains agrees with Evaluate for every log and every
// composite, and that the lists of the logs are still valid.
func (d *logDifferential) verify(label string) {
	d.t.Helper()

	g := d.rig.graph
	every := d.everyNode()

	for _, log := range d.logs {
		evaluated, err := d.rig.logs.Evaluate(g, log)
		if err != nil {
			d.t.Fatalf("%s: Evaluate(log %d): %v", label, log, err)
		}

		for _, node := range every {
			got, containsErr := d.rig.logs.Contains(g, log, node)
			if containsErr != nil {
				d.t.Fatalf("%s: Contains(log %d, %d): %v", label, log, node, containsErr)
			}

			if want := slices.Contains(evaluated, node); got != want {
				d.t.Fatalf("%s: Contains(log %d, %d) = %v, want %v (Evaluate() = %v)", label, log, node, got, want, evaluated)
			}
		}
	}

	for _, composite := range d.composites {
		evaluated, err := d.rig.composites.Evaluate(g, composite)
		if err != nil {
			d.t.Fatalf("%s: Evaluate(composite %d): %v", label, composite, err)
		}

		for _, node := range every {
			got, containsErr := d.rig.composites.Contains(g, composite, node)
			if containsErr != nil {
				d.t.Fatalf("%s: Contains(composite %d, %d): %v", label, composite, node, containsErr)
			}

			if want := slices.Contains(evaluated, node); got != want {
				d.t.Fatalf("%s: Contains(composite %d, %d) = %v, want %v (Evaluate() = %v)", label, composite, node, got, want, evaluated)
			}
		}
	}

	if err := d.rig.lists.VerifyStructures(g); err != nil {
		d.t.Fatalf("%s: VerifyStructures(): %v", label, err)
	}
}

func TestCompositeSetLogContainsMatchesEvaluateUnderRandomOperations(t *testing.T) {
	for _, backend := range testBackends() {
		for _, kind := range logListKinds {
			t.Run(backend.name+"/"+kind.name, func(t *testing.T) {
				for _, seed := range differentialSeeds[:2] {
					d := newLogDifferential(t, newLogRig(t, backend.open(t), kind), seed)

					for step := range differentialSteps(logDifferentialSteps) {
						d.run(step)
					}
				}
			})
		}
	}
}