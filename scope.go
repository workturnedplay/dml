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
	"strings"
)

// Scopes: who may do what, kept in the graph (theorystate.md section 116).

const (
	// NameAllScopes tags a scope record: the allow and deny sets of one path.
	NameAllScopes = "AllScopes"

	// NameAllScopeSlots tags the one slot a path's text node may have, whose
	// target is the path's scope record.
	NameAllScopeSlots = "AllScopeSlots"

	// NameAllScopeAllow and NameAllScopeDeny tag the two field slots of a
	// scope record, each pointing at a Set of principal texts.
	NameAllScopeAllow = "AllScopeAllow"
	NameAllScopeDeny  = "AllScopeDeny"

	// ScopeAnyPrincipal is the principal text that stands for every
	// principal except the empty one.
	ScopeAnyPrincipal = "*"

	scopeSeparator = "/"
)

var (
	// ErrInvalidScopePath is returned for a path that is empty, has an empty
	// segment, or is not valid text, and by ScopePath for a segment that
	// contains the separator.
	ErrInvalidScopePath = errors.New("scope path is invalid")

	// ErrInvalidScopePrincipal is returned when a grant names the empty
	// principal, which is always denied and so can never be granted.
	ErrInvalidScopePrincipal = errors.New("scope principal is invalid")

	// ErrInvalidScope is returned when scope structure is not what
	// ScopeRegistry builds.
	ErrInvalidScope = errors.New("scope structure is invalid")
)

// ScopePath joins segments into a scope path ("read/settings/3"). It rejects
// no segments, an empty segment, and a segment that contains "/": a handler
// builds paths from client-supplied keys, and an unchecked "/" would forge a
// path in another part of the tree.
func ScopePath(segments ...string) (string, error) {
	for i, segment := range segments {
		if strings.Contains(segment, scopeSeparator) {
			return "", fmt.Errorf("%w: segment %d (%q) contains %q", ErrInvalidScopePath, i, segment, scopeSeparator)
		}
	}

	path := strings.Join(segments, scopeSeparator)

	if validateErr := validateScopePath(path); validateErr != nil {
		return "", validateErr
	}

	return path, nil
}

// validateScopePath checks that path is a well-formed scope path: not empty,
// no empty segment, valid text of a length Intern accepts.
func validateScopePath(path string) error {
	if path == "" {
		return fmt.Errorf("%w: the path is empty", ErrInvalidScopePath)
	}

	if slices.Contains(strings.Split(path, scopeSeparator), "") {
		return fmt.Errorf("%w: %q has an empty segment", ErrInvalidScopePath, path)
	}

	if _, textErr := validateText(path); textErr != nil {
		return fmt.Errorf("%w: %w", ErrInvalidScopePath, textErr)
	}

	return nil
}

// scopePrefixes returns path and each of its prefixes made of whole
// segments, longest first: "a/b/c" gives "a/b/c", "a/b", "a". path must be
// valid (validateScopePath).
func scopePrefixes(path string) []string {
	prefixes := []string{path}
	rest := path

	for cut := strings.LastIndex(rest, scopeSeparator); cut >= 0; cut = strings.LastIndex(rest, scopeSeparator) {
		rest = rest[:cut]
		prefixes = append(prefixes, rest)
	}

	return prefixes
}

// ScopeRegistry keeps scopes: for a path, the principals allowed and the
// principals denied.
//
//	T                                   the string node of the path's text
//	T -> S      (AllScopeSlots, S)      at most one slot per text, a Pointer
//	S -> R      (AllScopes, R)          the scope record of the path
//	R -> A      (AllScopeAllow, A)      A -> a Set of principal texts
//	R -> D      (AllScopeDeny, D)       D -> a Set of principal texts
//
// The record is a FieldRegistry record with the two fields allow and deny; the
// slot is a PointerRegistry slot, so "at most one record per path" is
// enforced as everywhere else. A principal is an interned string node (a
// certificate fingerprint, a SID) or the text ScopeAnyPrincipal. Interning a
// path or a principal costs about one trie node per character (section 114).
//
// Allowed resolves a principal and a path: from the longest prefix of the path
// to the shortest, a scope that denies the principal (or ScopeAnyPrincipal)
// denies; else one that allows it allows; else the next shorter prefix is
// tried; nothing matched is a deny. So the most specific explicit decision
// wins, deny beats allow at the same level, and the default is deny. The empty
// principal (an unidentified peer) is always denied. A principal whose text was
// never interned can still match ScopeAnyPrincipal, and nothing else.
//
// Allow, Deny and Revoke are the only writers. They take a Transactor, so they
// work standalone and compose inside a larger transaction. Like every registry,
// ScopeRegistry stores no graph reference (section 90).
//
// The Checker declines, at commit: a scope slot that belongs to no text, or to
// a text that has another one; a record that has no slot, or whose allow or
// deny value is missing or is not a Set; and a Set held more than once across
// the two fields of all records (sharing one would leak grants). It does not
// look at the members of the sets: a member that is not a string can never
// match a principal.
type ScopeRegistry struct {
	text   *TextRegistry
	sets   *SetRegistry
	fields *FieldRegistry
	slots  *PointerRegistry

	allScopes NodeID
	allSlots  NodeID
	allow     NodeID
	deny      NodeID
}

// NewScopeRegistry creates a ScopeRegistry over graph. text interns the paths
// and principals, sets holds the allow and deny sets, and ids must hold the
// four scope names (as returned by NameRegistry.BootstrapNames). It registers
// the Checkers.
func NewScopeRegistry(graph GraphAPI, text *TextRegistry, sets *SetRegistry, ids map[string]NodeID) (*ScopeRegistry, error) {
	s := &ScopeRegistry{text: text, sets: sets}

	nodes, bindErr := bindNames(ids, []nameBinding{
		{NameAllScopes, &s.allScopes},
		{NameAllScopeSlots, &s.allSlots},
		{NameAllScopeAllow, &s.allow},
		{NameAllScopeDeny, &s.deny},
	})
	if bindErr != nil {
		return nil, bindErr
	}

	// Checked before anything is registered, so a failure leaves no Checker
	// behind.
	if existsErr := requireAllExist(graph, nodes...); existsErr != nil {
		return nil, existsErr
	}

	fields, err := NewFieldRegistry(graph, s.allScopes, FieldSpec{Tag: s.allow}, FieldSpec{Tag: s.deny})
	if err != nil {
		return nil, err
	}

	slots, err := NewPointerRegistry(graph, s.allSlots)
	if err != nil {
		return nil, err
	}

	s.fields, s.slots = fields, slots

	registerTaggedChecker(graph, fmt.Sprintf("ScopeRegistry(tag=%d)", s.allScopes), []taggedCheck{
		{tag: s.allSlots, run: s.checkSlot},
		{tag: s.allScopes, run: s.checkRecord},
		{tag: s.allow, run: s.checkFieldSlot},
		{tag: s.deny, run: s.checkFieldSlot},
	})

	return s, nil
}

// fieldTags returns the two field tags of a scope record.
func (s *ScopeRegistry) fieldTags() [2]NodeID {
	return [2]NodeID{s.allow, s.deny}
}

// recordOf returns the scope record of path, if it has one. It never creates
// anything.
func (s *ScopeRegistry) recordOf(graph GraphReader, path string) (record NodeID, found bool, err error) {
	text, textFound, err := s.text.Lookup(graph, path)
	if err != nil || !textFound {
		return 0, false, err
	}

	slot, hasSlot, slotErr := findOwnedSlot(graph, text, s.text.allStrings, s.allSlots)
	if slotErr != nil || !hasSlot {
		return 0, false, slotErr
	}

	record, hasRecord, targetErr := s.slots.Target(graph, slot)
	if targetErr != nil || !hasRecord {
		return 0, false, targetErr
	}

	if tagErr := requireTagged(graph, record, s.allScopes, ErrInvalidScope); tagErr != nil {
		return 0, false, tagErr
	}

	return record, true, nil
}

// fieldSet returns the Set record holds in field (allow or deny). A record
// without one is ErrInvalidScope: records are always created with both.
func (s *ScopeRegistry) fieldSet(graph GraphReader, record, field NodeID) (NodeID, error) {
	set, hasSet, err := s.fields.Get(graph, record, field)
	if err != nil {
		return 0, err
	}
	if !hasSet {
		return 0, fmt.Errorf("%w: scope record %d has no set for field %d", ErrInvalidScope, record, field)
	}

	return set, nil
}

// mentions reports whether any of principals is a member of the set record
// holds in field.
func (s *ScopeRegistry) mentions(graph GraphReader, record, field NodeID, principals []NodeID) (bool, error) {
	set, err := s.fieldSet(graph, record, field)
	if err != nil {
		return false, err
	}

	for _, principal := range principals {
		member, containsErr := s.sets.Contains(graph, set, principal)
		if containsErr != nil {
			return false, containsErr
		}
		if member {
			return true, nil
		}
	}

	return false, nil
}

// principalNodes returns the string nodes that stand for principal in a
// grant: the node of its text and the node of ScopeAnyPrincipal, each only if
// it was ever interned. It never interns.
func (s *ScopeRegistry) principalNodes(graph GraphReader, principal string) ([]NodeID, error) {
	nodes := make([]NodeID, 0, 2)

	for _, text := range [...]string{principal, ScopeAnyPrincipal} {
		node, found, err := s.text.Lookup(graph, text)
		if err != nil {
			return nil, err
		}

		if found && !slices.Contains(nodes, node) {
			nodes = append(nodes, node)
		}
	}

	return nodes, nil
}

// decide returns the decision of the scope at path, if it has a record that
// mentions one of principals: (allowed, decided, error). A record that denies
// decides false; one that only allows decides true; otherwise nothing is
// decided and the caller tries a shorter path.
func (s *ScopeRegistry) decide(graph GraphReader, path string, principals []NodeID) (bool, bool, error) {
	record, found, err := s.recordOf(graph, path)
	if err != nil || !found {
		return false, false, err
	}

	denied, deniedErr := s.mentions(graph, record, s.deny, principals)
	if deniedErr != nil {
		return false, false, deniedErr
	}
	if denied {
		return false, true, nil
	}

	allowed, allowedErr := s.mentions(graph, record, s.allow, principals)
	if allowedErr != nil {
		return false, false, allowedErr
	}

	// Allowed decides; "not mentioned" leaves the question open.
	return allowed, allowed, nil
}

// Allowed reports whether principal may use path (see the ScopeRegistry doc
// comment for the rules). path must be a valid scope path (ScopePath builds
// one). It only reads, and never interns. Any error means the structure is not
// what it should be or could not be read: the caller must treat it as a deny.
func (s *ScopeRegistry) Allowed(graph GraphReader, principal, path string) (bool, error) {
	if pathErr := validateScopePath(path); pathErr != nil {
		return false, pathErr
	}

	if principal == "" {
		return false, nil
	}

	principals, err := s.principalNodes(graph, principal)
	if err != nil {
		return false, err
	}

	if len(principals) == 0 {
		return false, nil
	}

	for _, prefix := range scopePrefixes(path) {
		allowed, decided, decideErr := s.decide(graph, prefix, principals)
		if decideErr != nil {
			return false, decideErr
		}
		if decided {
			return allowed, nil
		}
	}

	return false, nil
}

// newRecord creates a scope record with an empty allow set and an empty deny
// set.
func (s *ScopeRegistry) newRecord(tx Tx) (NodeID, error) {
	record, err := s.fields.NewRecord(tx)
	if err != nil {
		return 0, err
	}

	for _, field := range s.fieldTags() {
		set, setErr := s.sets.NewSet(tx)
		if setErr != nil {
			return 0, setErr
		}

		if fieldErr := s.fields.Set(tx, record, field, set); fieldErr != nil {
			return 0, fieldErr
		}
	}

	return record, nil
}

// ensureRecord returns the scope record of path, creating the path's text, its
// slot and the record (with both sets) if there are none.
func (s *ScopeRegistry) ensureRecord(tx Tx, path string) (NodeID, error) {
	text, err := s.text.Intern(tx, path)
	if err != nil {
		return 0, err
	}

	slot, hasSlot, slotErr := findOwnedSlot(tx, text, s.text.allStrings, s.allSlots)
	if slotErr != nil {
		return 0, slotErr
	}

	if !hasSlot {
		created, createErr := s.slots.NewPointer(tx)
		if createErr != nil {
			return 0, createErr
		}

		if linkErr := addRelationshipTx(tx, text, created); linkErr != nil {
			return 0, linkErr
		}

		slot = created
	}

	record, hasRecord, targetErr := s.slots.Target(tx, slot)
	if targetErr != nil {
		return 0, targetErr
	}
	if hasRecord {
		return record, nil
	}

	fresh, newErr := s.newRecord(tx)
	if newErr != nil {
		return 0, newErr
	}

	return fresh, s.slots.SetTarget(tx, slot, fresh)
}

// validateScopePrincipal rejects the empty principal, which is always denied
// and so can never be granted.
func validateScopePrincipal(principal string) error {
	if principal == "" {
		return fmt.Errorf("%w: the empty principal cannot be granted anything", ErrInvalidScopePrincipal)
	}

	return nil
}

// add puts principal in the set record of path holds in field, creating the
// path's scope if needed, in one transaction (nested if graph is a Tx). added
// reports whether principal was newly a member.
func (s *ScopeRegistry) add(graph Transactor, path, principal string, field NodeID) (added bool, err error) {
	if pathErr := validateScopePath(path); pathErr != nil {
		return false, pathErr
	}

	if principalErr := validateScopePrincipal(principal); principalErr != nil {
		return false, principalErr
	}

	return transactBool(graph, func(tx Tx) (bool, error) {
		record, recordErr := s.ensureRecord(tx, path)
		if recordErr != nil {
			return false, recordErr
		}

		set, setErr := s.fieldSet(tx, record, field)
		if setErr != nil {
			return false, setErr
		}

		node, internErr := s.text.Intern(tx, principal)
		if internErr != nil {
			return false, internErr
		}

		return s.sets.Add(tx, set, node)
	})
}

// Allow adds principal to the principals allowed at path (ScopeAnyPrincipal
// for everyone), creating the scope if needed. It reports whether principal
// was newly allowed there. A principal that is also denied at the same path
// stays denied: deny beats allow at the same level.
func (s *ScopeRegistry) Allow(graph Transactor, path, principal string) (bool, error) {
	return s.add(graph, path, principal, s.allow)
}

// Deny adds principal to the principals denied at path, creating the scope if
// needed. It reports whether principal was newly denied there.
func (s *ScopeRegistry) Deny(graph Transactor, path, principal string) (bool, error) {
	return s.add(graph, path, principal, s.deny)
}

// Revoke removes principal from both the allow and the deny set of path, and
// reports whether it was in either. It creates nothing: a path with no scope,
// or a principal never interned, changes nothing. The scope record stays, empty.
func (s *ScopeRegistry) Revoke(graph Transactor, path, principal string) (bool, error) {
	if pathErr := validateScopePath(path); pathErr != nil {
		return false, pathErr
	}

	if principalErr := validateScopePrincipal(principal); principalErr != nil {
		return false, principalErr
	}

	return transactBool(graph, func(tx Tx) (bool, error) {
		node, found, lookupErr := s.text.Lookup(tx, principal)
		if lookupErr != nil || !found {
			return false, lookupErr
		}

		record, hasRecord, recordErr := s.recordOf(tx, path)
		if recordErr != nil || !hasRecord {
			return false, recordErr
		}

		revoked := false

		for _, field := range s.fieldTags() {
			set, setErr := s.fieldSet(tx, record, field)
			if setErr != nil {
				return false, setErr
			}

			removed, removeErr := s.sets.Remove(tx, set, node)
			if removeErr != nil {
				return false, removeErr
			}

			revoked = revoked || removed
		}

		return revoked, nil
	})
}

// ---------------------------------------------------------------------
// Commit-time Checker. Local: it costs time proportional to what the
// transaction touched.

// checkSlot: a scope slot belongs to one text, which has no other scope slot,
// and its target, if any, is a scope record.
func (s *ScopeRegistry) checkSlot(g GraphReader, slot NodeID) error {
	text, hasText, err := findUniqueTaggedParent(g, slot, s.text.allStrings)
	if err != nil {
		return err
	}
	if !hasText {
		return fmt.Errorf("%w: a scope slot belongs to no text", ErrInvalidScope)
	}

	owned, hasOwned, ownedErr := findOwnedSlot(g, text, s.text.allStrings, s.allSlots)
	if ownedErr != nil {
		return ownedErr
	}
	if !hasOwned || owned != slot {
		return fmt.Errorf("%w: a text has another scope slot", ErrInvalidScope)
	}

	record, hasRecord, targetErr := s.slots.Target(g, slot)
	if targetErr != nil || !hasRecord {
		return targetErr
	}

	return requireTagged(g, record, s.allScopes, ErrInvalidScope)
}

// requireExclusive returns ErrInvalidScope unless set is held exactly once
// across the allow and deny fields of every record, which is by record (the
// caller has checked that record holds it).
func (s *ScopeRegistry) requireExclusive(g GraphReader, record, set NodeID) error {
	holders := 0

	for _, field := range s.fieldTags() {
		owners, err := s.fields.Owners(g, field, set)
		if err != nil {
			return err
		}

		holders += len(owners)
	}

	if holders != 1 {
		return fmt.Errorf("%w: set %d of scope record %d is held %d times, want once", ErrInvalidScope, set, record, holders)
	}

	return nil
}

// checkRecord: a scope record has a slot that is valid and points at it, an
// allow and a deny set that are Sets, and no set is shared.
func (s *ScopeRegistry) checkRecord(g GraphReader, record NodeID) error {
	slot, hasSlot, err := findUniqueTaggedParent(g, record, s.allSlots)
	if err != nil {
		return err
	}
	if !hasSlot {
		return fmt.Errorf("%w: scope record %d has no path slot", ErrInvalidScope, record)
	}

	if slotErr := s.checkSlot(g, slot); slotErr != nil {
		return slotErr
	}

	for _, field := range s.fieldTags() {
		set, setErr := s.fieldSet(g, record, field)
		if setErr != nil {
			return setErr
		}

		if requireErr := s.sets.requireSet(g, set); requireErr != nil {
			return requireErr
		}

		if exclusiveErr := s.requireExclusive(g, record, set); exclusiveErr != nil {
			return exclusiveErr
		}
	}

	return nil
}

// checkFieldSlot: an allow or deny slot belongs to a scope record, which is
// then checked in full.
func (s *ScopeRegistry) checkFieldSlot(g GraphReader, slot NodeID) error {
	record, hasRecord, err := findUniqueTaggedParent(g, slot, s.allScopes)
	if err != nil {
		return err
	}
	if !hasRecord {
		return fmt.Errorf("%w: a scope field slot belongs to no record", ErrInvalidScope)
	}

	return s.checkRecord(g, record)
}