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
	"strings"
)

// Plans: a remote transaction as data (theorystate.md section 115). A plan is
// complete when it is sent: straight-line steps, no loops, no waiting for the
// client in the middle. The host runs it as one Transact.

const (
	// PlanResource is the resource name a wire Authorizer is asked about for a
	// plan (AuthRequest{OpPlan, PlanResource}); a policy lists the principals
	// that may submit plans under this name. It is not a real resource.
	PlanResource = "dml:plan"

	// DefaultPlanMaxSteps is the step limit when none is given.
	DefaultPlanMaxSteps = 1000

	maxPlanLabelBytes = 128
	maxPlanNameBytes  = 256
)

var (
	// ErrPlanInvalid is returned for a plan that is wrong whatever the graph
	// holds: an unknown operation, missing or extra fields, a label that is
	// not bound by an earlier step or is bound twice, too many steps.
	ErrPlanInvalid = errors.New("invalid plan")

	// ErrPlanConflict is returned for a plan that does not fit the graph as it
	// is now: a failed assert, a node or name that does not exist, a node
	// that cannot be deleted. Nothing was changed; the client re-reads and
	// may try again.
	ErrPlanConflict = errors.New("plan conflict")
)

// PlanOp is the operation of one plan step.
type PlanOp string

// The operations of a plan step and the fields each uses:
//
//	create         Bind                 a new node
//	ensure_named   Bind, Name           the node named Name, created if there is none
//	intern         Bind, Text           the string node of Text (TextRegistry.Intern)
//	add            From, To             the relationship (From, To)
//	remove         From, To             remove it
//	delete         Node                 delete the node (NameRegistry.DeleteNode)
//	assert_has     From, To             conflict unless the relationship exists
//	assert_lacks   From, To             conflict if the relationship exists
//	assert_exists  Node                 conflict unless the node exists
const (
	PlanCreate       PlanOp = "create"
	PlanEnsureNamed  PlanOp = "ensure_named"
	PlanIntern       PlanOp = "intern"
	PlanAdd          PlanOp = "add"
	PlanRemove       PlanOp = "remove"
	PlanDelete       PlanOp = "delete"
	PlanAssertHas    PlanOp = "assert_has"
	PlanAssertLacks  PlanOp = "assert_lacks"
	PlanAssertExists PlanOp = "assert_exists"
)

// PlanRef names a node in a plan: exactly one of a label (a node bound by an
// earlier step of the same plan), a NodeID, or a registered name. A pointer
// holds the ID so that node 0 can be told from "not set".
type PlanRef struct {
	Label string  `json:"label,omitempty"`
	ID    *NodeID `json:"id,omitempty"`
	Name  string  `json:"name,omitempty"`
}

// PlanLabel refers to the node an earlier step bound to label.
func PlanLabel(label string) PlanRef { return PlanRef{Label: label} }

// PlanNode refers to the node with this ID.
func PlanNode(id NodeID) PlanRef { return PlanRef{ID: &id} }

// PlanName refers to the node bound to this registered name.
func PlanName(name string) PlanRef { return PlanRef{Name: name} }

func (r PlanRef) isZero() bool {
	return r.Label == "" && r.ID == nil && r.Name == ""
}

// PlanStep is one step of a plan. Which fields it must and may have depends
// on Op (see the PlanOp constants); any other field must be empty.
type PlanStep struct {
	Op   PlanOp  `json:"op"`
	Bind string  `json:"bind,omitempty"`
	Node PlanRef `json:"node,omitzero"`
	From PlanRef `json:"from,omitzero"`
	To   PlanRef `json:"to,omitzero"`
	Name string  `json:"name,omitempty"`
	Text string  `json:"text,omitempty"`
}

// Plan is a list of steps the host runs, in order, as one transaction: all of
// them take effect or none does.
type Plan struct {
	Steps []PlanStep `json:"steps"`
}

// PlanResult is what a plan returned: the node each label was bound to.
type PlanResult struct {
	Bound map[string]NodeID `json:"bound,omitempty"`
}

// planShape says which fields a step of one operation has.
type planShape struct {
	binds, node, from, to, name, text bool
}

var planShapes = map[PlanOp]planShape{
	PlanCreate:       {binds: true},
	PlanEnsureNamed:  {binds: true, name: true},
	PlanIntern:       {binds: true, text: true},
	PlanAdd:          {from: true, to: true},
	PlanRemove:       {from: true, to: true},
	PlanDelete:       {node: true},
	PlanAssertHas:    {from: true, to: true},
	PlanAssertLacks:  {from: true, to: true},
	PlanAssertExists: {node: true},
}

// reservedNamePrefixes start names the host makes itself; a plan cannot
// create them.
var reservedNamePrefixes = []string{resourcePrefix, charNamePrefix}

// checkFields returns an error unless s has exactly the fields its operation
// uses. An empty Text is a valid text (the empty string), so a missing Text is
// accepted.
func (sh planShape) checkFields(s PlanStep) error {
	fields := [...]struct {
		name     string
		present  bool
		wanted   bool
		optional bool
	}{
		{name: "bind", present: s.Bind != "", wanted: sh.binds},
		{name: "node", present: !s.Node.isZero(), wanted: sh.node},
		{name: "from", present: !s.From.isZero(), wanted: sh.from},
		{name: "to", present: !s.To.isZero(), wanted: sh.to},
		{name: "name", present: s.Name != "", wanted: sh.name},
		{name: "text", present: s.Text != "", wanted: sh.text, optional: true},
	}

	for _, field := range fields {
		switch {
		case field.present && !field.wanted:
			return fmt.Errorf("field %q is not used by this operation", field.name)
		case !field.present && field.wanted && !field.optional:
			return fmt.Errorf("field %q is missing", field.name)
		}
	}

	return nil
}

// validate checks r: exactly one kind, and a label must already be bound.
func (r PlanRef) validate(bound map[string]struct{}) error {
	kinds := 0

	if r.Label != "" {
		kinds++
	}

	if r.ID != nil {
		kinds++
	}

	if r.Name != "" {
		kinds++
	}

	if kinds != 1 {
		return errors.New("a reference must be exactly one of label, id and name")
	}

	if len(r.Label) > maxPlanLabelBytes || len(r.Name) > maxPlanNameBytes {
		return errors.New("a reference is too long")
	}

	if _, ok := bound[r.Label]; r.Label != "" && !ok {
		return fmt.Errorf("label %q is not bound by an earlier step", r.Label)
	}

	return nil
}

// validatePlanName checks a name an ensure_named step would create.
func validatePlanName(name string) error {
	if len(name) > maxPlanNameBytes {
		return fmt.Errorf("the name is longer than %d bytes", maxPlanNameBytes)
	}

	for _, prefix := range reservedNamePrefixes {
		if strings.HasPrefix(name, prefix) {
			return fmt.Errorf("names starting with %q are reserved", prefix)
		}
	}

	return nil
}

// validate checks one step against the labels bound so far and, if it binds
// one, adds it to bound.
func (s PlanStep) validate(bound map[string]struct{}) error {
	shape, known := planShapes[s.Op]
	if !known {
		return errors.New("unknown operation")
	}

	if fieldsErr := shape.checkFields(s); fieldsErr != nil {
		return fieldsErr
	}

	for _, ref := range []PlanRef{s.Node, s.From, s.To} {
		if ref.isZero() {
			continue
		}

		if refErr := ref.validate(bound); refErr != nil {
			return refErr
		}
	}

	if shape.name {
		if nameErr := validatePlanName(s.Name); nameErr != nil {
			return nameErr
		}
	}

	if shape.text {
		if _, textErr := validateText(s.Text); textErr != nil {
			return textErr
		}
	}

	if shape.binds {
		if len(s.Bind) > maxPlanLabelBytes {
			return fmt.Errorf("the label is longer than %d bytes", maxPlanLabelBytes)
		}

		if _, taken := bound[s.Bind]; taken {
			return fmt.Errorf("label %q is bound twice", s.Bind)
		}

		bound[s.Bind] = struct{}{}
	}

	return nil
}

// validate checks the whole plan without touching the graph. Every failure is
// ErrPlanInvalid.
func (p Plan) validate(maxSteps int) error {
	if len(p.Steps) == 0 {
		return fmt.Errorf("%w: the plan has no steps", ErrPlanInvalid)
	}

	if len(p.Steps) > maxSteps {
		return fmt.Errorf("%w: %d steps, the limit is %d", ErrPlanInvalid, len(p.Steps), maxSteps)
	}

	bound := make(map[string]struct{}, len(p.Steps))

	for i, step := range p.Steps {
		if stepErr := step.validate(bound); stepErr != nil {
			return fmt.Errorf("%w: step %d (%s): %w", ErrPlanInvalid, i, step.Op, stepErr)
		}
	}

	return nil
}

// RunPlan validates plan (at most maxSteps steps, zero meaning
// DefaultPlanMaxSteps) and runs it as one transaction through the host's
// guarded graph, so Checkers run and a change to a resource's holders wakes the
// reconciler. It is what the wire server calls for a submitted plan; an
// in-process caller usually has closures and does not need it. A plan runs to
// completion once started.
func (h *Host) RunPlan(plan Plan, maxSteps int) (PlanResult, error) {
	limit := maxSteps
	if limit <= 0 {
		limit = DefaultPlanMaxSteps
	}

	if validateErr := plan.validate(limit); validateErr != nil {
		return PlanResult{}, validateErr
	}

	return planRunner{names: h.names, text: h.reg.Text}.run(h.Graph(), plan)
}

// planRunner executes plans against a graph.
type planRunner struct {
	names *NameRegistry
	text  *TextRegistry
}

// run executes plan as one transaction (nested if graph is a Tx). The label
// map is built inside the closure, since the closure may be re-run.
func (r planRunner) run(graph Transactor, plan Plan) (PlanResult, error) {
	return transactValue(graph, func(tx Tx) (PlanResult, error) {
		bound := make(map[string]NodeID, len(plan.Steps))

		for i, step := range plan.Steps {
			if stepErr := r.exec(tx, bound, step); stepErr != nil {
				return PlanResult{}, planStepError(i, step.Op, stepErr)
			}
		}

		return PlanResult{Bound: bound}, nil
	})
}

// planStepError says which step failed. A failure that depends on what the
// graph holds is a conflict; anything else is reported as it is.
func planStepError(index int, op PlanOp, err error) error {
	switch {
	case errors.Is(err, ErrPlanConflict):
		return fmt.Errorf("%w (step %d, %s)", err, index, op)
	case errors.Is(err, ErrNodeNotFound), errors.Is(err, ErrNodeNotEmpty),
		errors.Is(err, ErrNameNotFound), errors.Is(err, ErrNameRetired):
		return fmt.Errorf("%w: step %d (%s): %w", ErrPlanConflict, index, op, err)
	default:
		return fmt.Errorf("plan step %d (%s): %w", index, op, err)
	}
}

// lookup returns the node ref names, without checking that it exists.
func (r planRunner) lookup(tx Tx, bound map[string]NodeID, ref PlanRef) (NodeID, error) {
	switch {
	case ref.Label != "":
		id, ok := bound[ref.Label]
		if !ok {
			return 0, fmt.Errorf("%w: label %q is not bound", ErrPlanInvalid, ref.Label)
		}

		return id, nil
	case ref.ID != nil:
		return *ref.ID, nil
	default:
		id, found, err := r.names.lookupLive(tx, ref.Name)
		if err != nil {
			return 0, err
		}
		if !found {
			return 0, fmt.Errorf("%w: %q", ErrNameNotFound, ref.Name)
		}

		return id, nil
	}
}

// resolve returns the node ref names, which must exist now (a label whose
// node an earlier step deleted does not).
func (r planRunner) resolve(tx Tx, bound map[string]NodeID, ref PlanRef) (NodeID, error) {
	id, err := r.lookup(tx, bound, ref)
	if err != nil {
		return 0, err
	}

	if existsErr := requireExists(tx, id); existsErr != nil {
		return 0, existsErr
	}

	return id, nil
}

// resolveEnds resolves both ends of a relationship step.
func (r planRunner) resolveEnds(tx Tx, bound map[string]NodeID, step PlanStep) (from, to NodeID, err error) {
	from, err = r.resolve(tx, bound, step.From)
	if err != nil {
		return 0, 0, err
	}

	to, err = r.resolve(tx, bound, step.To)
	if err != nil {
		return 0, 0, err
	}

	return from, to, nil
}

// produce runs a step that yields a node.
func (r planRunner) produce(tx Tx, step PlanStep) (NodeID, error) {
	switch step.Op {
	case PlanEnsureNamed:
		return r.names.EnsureNamedNode(tx, step.Name)
	case PlanIntern:
		return r.text.Intern(tx, step.Text)
	default:
		return createNodeTx(tx)
	}
}

// execEdge runs a step on a relationship.
func (r planRunner) execEdge(tx Tx, bound map[string]NodeID, step PlanStep) error {
	from, to, endsErr := r.resolveEnds(tx, bound, step)
	if endsErr != nil {
		return endsErr
	}

	switch step.Op {
	case PlanAdd:
		return addRelationshipTx(tx, from, to)
	case PlanRemove:
		return removeRelationshipTx(tx, from, to)
	default:
		has, hasErr := tx.HasRelationship(from, to)
		if hasErr != nil {
			return wrapInterfaceErr(hasErr)
		}

		switch {
		case has == (step.Op == PlanAssertHas):
			return nil
		case has:
			return fmt.Errorf("%w: relationship (%d,%d) exists", ErrPlanConflict, from, to)
		default:
			return fmt.Errorf("%w: relationship (%d,%d) does not exist", ErrPlanConflict, from, to)
		}
	}
}

// execNode runs a step on one node.
func (r planRunner) execNode(tx Tx, bound map[string]NodeID, step PlanStep) error {
	if step.Op == PlanDelete {
		id, resolveErr := r.resolve(tx, bound, step.Node)
		if resolveErr != nil {
			return resolveErr
		}

		return r.names.DeleteNode(tx, id)
	}

	id, lookupErr := r.lookup(tx, bound, step.Node)
	if lookupErr != nil {
		return lookupErr
	}

	exists, existsErr := tx.NodeExists(id)
	if existsErr != nil {
		return wrapInterfaceErr(existsErr)
	}

	if !exists {
		return fmt.Errorf("%w: node %d does not exist", ErrPlanConflict, id)
	}

	return nil
}

// exec runs one step.
func (r planRunner) exec(tx Tx, bound map[string]NodeID, step PlanStep) error {
	switch step.Op {
	case PlanCreate, PlanEnsureNamed, PlanIntern:
		id, err := r.produce(tx, step)
		if err != nil {
			return err
		}

		bound[step.Bind] = id

		return nil
	case PlanAdd, PlanRemove, PlanAssertHas, PlanAssertLacks:
		return r.execEdge(tx, bound, step)
	case PlanDelete, PlanAssertExists:
		return r.execNode(tx, bound, step)
	default:
		return fmt.Errorf("%w: unknown operation %q", ErrPlanInvalid, step.Op)
	}
}