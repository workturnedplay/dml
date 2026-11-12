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

// scopeRig is a ScopeRegistry over one backend, with the registries it is
// built on. The closures the tests hand to Transact read only through tx and
// never call t.Fatalf, since on a GraphActor they run on the actor's goroutine.
type scopeRig struct {
	graph  testGraph
	names  *NameRegistry
	ids    map[string]NodeID
	text   *TextRegistry
	sets   *SetRegistry
	scopes *ScopeRegistry
}

// errScopeFixtureMissing is returned by the rig's lookups inside a hand-made
// transaction when the fixture does not have what the case expects.
var errScopeFixtureMissing = errors.New("the test fixture does not have the scope the case expects")

// newScopeParts builds everything a ScopeRegistry needs except the registry
// itself, so a test can also exercise its constructor.
func newScopeParts(t *testing.T, graph testGraph) *scopeRig {
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

	sets, err := NewSetRegistry(graph, ids[NameAllSets], ids[NameAllCompositeSets], ids[NameAllCompositeSetLogs])
	if err != nil {
		t.Fatalf("NewSetRegistry(): %v", err)
	}

	return &scopeRig{graph: graph, names: names, ids: ids, text: text, sets: sets}
}

func newScopeRig(t *testing.T, graph testGraph) *scopeRig {
	t.Helper()

	rig := newScopeParts(t, graph)

	scopes, err := NewScopeRegistry(graph, rig.text, rig.sets, rig.ids)
	if err != nil {
		t.Fatalf("NewScopeRegistry(): %v", err)
	}

	rig.scopes = scopes

	return rig
}

// forEachScopeRig runs body once per backend, each on a fresh graph.
func forEachScopeRig(t *testing.T, body func(t *testing.T, rig *scopeRig)) {
	t.Helper()

	for _, backend := range testBackends() {
		t.Run(backend.name, func(t *testing.T) {
			body(t, newScopeRig(t, backend.open(t)))
		})
	}
}

// mustGrant runs one of Allow and Deny, failing the test on an error.
func (rig *scopeRig) mustGrant(t *testing.T, grant func(Transactor, string, string) (bool, error), path, principal string) {
	t.Helper()

	if _, err := grant(rig.graph, path, principal); err != nil {
		t.Fatalf("granting %q at %q: %v", principal, path, err)
	}
}

func (rig *scopeRig) allow(t *testing.T, path, principal string) {
	t.Helper()

	rig.mustGrant(t, rig.scopes.Allow, path, principal)
}

func (rig *scopeRig) deny(t *testing.T, path, principal string) {
	t.Helper()

	rig.mustGrant(t, rig.scopes.Deny, path, principal)
}

func (rig *scopeRig) requireAllowed(t *testing.T, principal, path string, want bool) {
	t.Helper()

	got, err := rig.scopes.Allowed(rig.graph, principal, path)
	if err != nil || got != want {
		t.Fatalf("Allowed(%q, %q) = (%v,%v), want (%v,nil)", principal, path, got, err, want)
	}
}

// textTx returns the string node of text, which must have been interned.
func (rig *scopeRig) textTx(tx Tx, text string) (NodeID, error) {
	node, found, err := rig.text.Lookup(tx, text)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, errScopeFixtureMissing
	}

	return node, nil
}

// slotTx returns the scope slot of path.
func (rig *scopeRig) slotTx(tx Tx, path string) (NodeID, error) {
	text, err := rig.textTx(tx, path)
	if err != nil {
		return 0, err
	}

	slot, found, slotErr := findOwnedSlot(tx, text, rig.text.allStrings, rig.scopes.allSlots)
	if slotErr != nil {
		return 0, slotErr
	}
	if !found {
		return 0, errScopeFixtureMissing
	}

	return slot, nil
}

// recordTx returns the scope record of path, which must have one.
func (rig *scopeRig) recordTx(tx Tx, path string) (NodeID, error) {
	record, found, err := rig.scopes.recordOf(tx, path)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, errScopeFixtureMissing
	}

	return record, nil
}

// scopeCase is one question put to Allowed and its expected answer.
type scopeCase struct {
	principal, path string
	want            bool
}

// grantExample installs the example of theorystate.md section 116 (everyone
// reads the settings except the upstream URLs, except the third one) plus one
// grant to a named principal, and returns questions with their answers.
func (rig *scopeRig) grantExample(t *testing.T) []scopeCase {
	t.Helper()

	rig.allow(t, "read/settings", ScopeAnyPrincipal)
	rig.deny(t, "read/settings/upstream_urls", ScopeAnyPrincipal)
	rig.allow(t, "read/settings/upstream_urls/3", ScopeAnyPrincipal)
	rig.allow(t, "write/rules", "alice")

	return []scopeCase{
		{principal: "alice", path: "read/settings", want: true},
		{principal: "alice", path: "read/settings/foo", want: true},
		{principal: "alice", path: "read/settings/upstream_urls", want: false},
		{principal: "alice", path: "read/settings/upstream_urls/2", want: false},
		{principal: "alice", path: "read/settings/upstream_urls/3", want: true},
		{principal: "alice", path: "read/other", want: false},
		{principal: "alice", path: "read", want: false},
		{principal: "alice", path: "write/rules", want: true},
		{principal: "alice", path: "write/rules/x", want: true},
		{principal: "alice", path: "write", want: false},
		{principal: "bob", path: "write/rules", want: false},
		{principal: "bob", path: "read/settings", want: true},
		{principal: ScopeAnyPrincipal, path: "read/settings", want: true},
		{principal: "", path: "read/settings", want: false},
	}
}

func TestFoundationalNamesIncludeScopeNames(t *testing.T) {
	for _, want := range []string{NameAllScopes, NameAllScopeSlots, NameAllScopeAllow, NameAllScopeDeny} {
		if !slices.Contains(FoundationalNames, want) {
			t.Fatalf("FoundationalNames %v does not include %q", FoundationalNames, want)
		}
	}
}

func TestScopePathJoinsSegmentsAndRejectsForgeries(t *testing.T) {
	good := []struct {
		segments []string
		want     string
	}{
		{segments: []string{"read", "settings", "3"}, want: "read/settings/3"},
		{segments: []string{"run"}, want: "run"},
		{segments: []string{"héllo", "wörld"}, want: "héllo/wörld"},
	}

	for _, tc := range good {
		got, err := ScopePath(tc.segments...)
		if err != nil || got != tc.want {
			t.Fatalf("ScopePath(%q) = (%q,%v), want (%q,nil)", tc.segments, got, err, tc.want)
		}
	}

	bad := [][]string{
		nil,
		{""},
		{"a", ""},
		{"", "a"},
		{"a/b"},
		{"a", "b/c"},
		{"\xff"},
		{strings.Repeat("a", MaxTextRunes+1)},
	}

	for _, segments := range bad {
		if got, err := ScopePath(segments...); !errors.Is(err, ErrInvalidScopePath) {
			t.Fatalf("ScopePath(%q) = (%q,%v), want an error wrapping %v", segments, got, err, ErrInvalidScopePath)
		}
	}
}

func TestScopePrefixesAreLongestFirst(t *testing.T) {
	cases := []struct {
		path string
		want []string
	}{
		{path: "a/b/c", want: []string{"a/b/c", "a/b", "a"}},
		{path: "a/b", want: []string{"a/b", "a"}},
		{path: "a", want: []string{"a"}},
	}

	for _, tc := range cases {
		if got := scopePrefixes(tc.path); !slices.Equal(got, tc.want) {
			t.Fatalf("scopePrefixes(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestNewScopeRegistryRequiresItsNamesAndNodes(t *testing.T) {
	const nonexistent NodeID = 999999

	var g Graph

	parts := newScopeParts(t, &g)

	cases := []struct {
		name   string
		mutate func(ids map[string]NodeID)
		want   error
	}{
		{name: "a missing name", mutate: func(ids map[string]NodeID) { delete(ids, NameAllScopeDeny) }, want: ErrNameNotFound},
		{name: "a node that does not exist", mutate: func(ids map[string]NodeID) { ids[NameAllScopeAllow] = nonexistent }, want: ErrNodeNotFound},
	}

	for _, tc := range cases {
		ids := maps.Clone(parts.ids)
		tc.mutate(ids)

		if _, err := NewScopeRegistry(&g, parts.text, parts.sets, ids); !errors.Is(err, tc.want) {
			t.Fatalf("%s: NewScopeRegistry() error = %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestScopeAllowedResolvesTheMostSpecificDecision(t *testing.T) {
	forEachScopeRig(t, func(t *testing.T, rig *scopeRig) {
		for _, tc := range rig.grantExample(t) {
			rig.requireAllowed(t, tc.principal, tc.path, tc.want)
		}
	})
}

func TestScopeDenyBeatsAllowAtOneLevelAndSpecificBeatsBroad(t *testing.T) {
	forEachScopeRig(t, func(t *testing.T, rig *scopeRig) {
		// A narrower allow overrides a broader deny, for the same principal.
		rig.deny(t, "a", "alice")
		rig.allow(t, "a/b", "alice")
		rig.requireAllowed(t, "alice", "a/b", true)
		rig.requireAllowed(t, "alice", "a/c", false)
		rig.requireAllowed(t, "alice", "a", false)

		// A deny of one principal does not touch another allowed by "*".
		rig.allow(t, "x", ScopeAnyPrincipal)
		rig.deny(t, "x", "alice")
		rig.requireAllowed(t, "alice", "x", false)
		rig.requireAllowed(t, "bob", "x", true)

		// At one level, deny wins whatever the order of the grants.
		rig.allow(t, "p", "alice")
		rig.deny(t, "p", "alice")
		rig.requireAllowed(t, "alice", "p", false)
	})
}

func TestScopeEmptyAndUnknownPrincipals(t *testing.T) {
	forEachScopeRig(t, func(t *testing.T, rig *scopeRig) {
		rig.allow(t, "p", "alice")

		// Neither "stranger" nor "*" was ever interned: nothing can match.
		rig.requireAllowed(t, "stranger", "p", false)
		rig.requireAllowed(t, "", "p", false)

		rig.allow(t, "p", ScopeAnyPrincipal)

		// A principal never interned still matches "*", and only that; the
		// empty principal (an unidentified peer) matches nothing, "*" included.
		rig.requireAllowed(t, "stranger", "p", true)
		rig.requireAllowed(t, "", "p", false)
	})
}

func TestScopeAllowedNeverCreatesAnything(t *testing.T) {
	forEachScopeRig(t, func(t *testing.T, rig *scopeRig) {
		rig.grantExample(t)

		nodesBefore := len(mustFindNodes(t, rig.graph))

		for _, principal := range []string{"alice", "zed", ScopeAnyPrincipal, ""} {
			for _, path := range []string{"read/settings", "zzz/never/interned", "Z"} {
				if _, err := rig.scopes.Allowed(rig.graph, principal, path); err != nil {
					t.Fatalf("Allowed(%q, %q): %v", principal, path, err)
				}
			}
		}

		if got := len(mustFindNodes(t, rig.graph)); got != nodesBefore {
			t.Fatalf("%d nodes after Allowed(), want %d", got, nodesBefore)
		}

		if _, bound := rig.names.Lookup(charName('Z')); bound {
			t.Fatal("Allowed() created the char node of 'Z'")
		}
	})
}

func TestScopeAllowAndDenyReportWhetherTheyChangedAnything(t *testing.T) {
	forEachScopeRig(t, func(t *testing.T, rig *scopeRig) {
		steps := []struct {
			name      string
			grant     func(Transactor, string, string) (bool, error)
			principal string
			want      bool
		}{
			{name: "first Allow", grant: rig.scopes.Allow, principal: "alice", want: true},
			{name: "second Allow", grant: rig.scopes.Allow, principal: "alice", want: false},
			{name: "Allow of another principal", grant: rig.scopes.Allow, principal: ScopeAnyPrincipal, want: true},
			{name: "first Deny", grant: rig.scopes.Deny, principal: "alice", want: true},
			{name: "second Deny", grant: rig.scopes.Deny, principal: "alice", want: false},
		}

		for _, step := range steps {
			got, err := step.grant(rig.graph, "p", step.principal)
			if err != nil || got != step.want {
				t.Fatalf("%s = (%v,%v), want (%v,nil)", step.name, got, err, step.want)
			}
		}
	})
}

func TestScopeRevokeRemovesFromBothSetsAndCreatesNothing(t *testing.T) {
	forEachScopeRig(t, func(t *testing.T, rig *scopeRig) {
		rig.allow(t, "a", "alice")
		rig.deny(t, "a/b", "alice")
		rig.requireAllowed(t, "alice", "a/b", false)

		revoked, err := rig.scopes.Revoke(rig.graph, "a/b", "alice")
		if err != nil || !revoked {
			t.Fatalf("Revoke(a/b, alice) = (%v,%v), want (true,nil)", revoked, err)
		}

		// The deny is gone, so the broader allow decides again.
		rig.requireAllowed(t, "alice", "a/b", true)

		if again, againErr := rig.scopes.Revoke(rig.graph, "a/b", "alice"); againErr != nil || again {
			t.Fatalf("second Revoke() = (%v,%v), want (false,nil)", again, againErr)
		}

		// The scope record stays, empty.
		if _, found, recordErr := rig.scopes.recordOf(rig.graph, "a/b"); recordErr != nil || !found {
			t.Fatalf("recordOf(a/b) = (found %v, %v), want the empty record kept", found, recordErr)
		}

		// A principal in both sets is removed from both.
		rig.allow(t, "p", "alice")
		rig.deny(t, "p", "alice")

		if both, bothErr := rig.scopes.Revoke(rig.graph, "p", "alice"); bothErr != nil || !both {
			t.Fatalf("Revoke(p, alice) = (%v,%v), want (true,nil)", both, bothErr)
		}

		for name, grant := range map[string]func(Transactor, string, string) (bool, error){"Allow": rig.scopes.Allow, "Deny": rig.scopes.Deny} {
			if added, grantErr := grant(rig.graph, "p", "alice"); grantErr != nil || !added {
				t.Fatalf("%s after Revoke() = (%v,%v), want it newly granted", name, added, grantErr)
			}
		}

		// Nothing to revoke, and nothing created doing so.
		nodesBefore := len(mustFindNodes(t, rig.graph))

		for _, tc := range []struct{ path, principal string }{
			{path: "a/b", principal: "stranger"},
			{path: "zzz/none", principal: "alice"},
			{path: "zzz/none", principal: "stranger"},
		} {
			if got, revokeErr := rig.scopes.Revoke(rig.graph, tc.path, tc.principal); revokeErr != nil || got {
				t.Fatalf("Revoke(%q, %q) = (%v,%v), want (false,nil)", tc.path, tc.principal, got, revokeErr)
			}
		}

		if got := len(mustFindNodes(t, rig.graph)); got != nodesBefore {
			t.Fatalf("%d nodes after revoking nothing, want %d", got, nodesBefore)
		}
	})
}

func TestScopeRejectsInvalidArguments(t *testing.T) {
	forEachScopeRig(t, func(t *testing.T, rig *scopeRig) {
		rig.allow(t, "a", "alice")

		nodesBefore := len(mustFindNodes(t, rig.graph))

		pathOps := []struct {
			name string
			run  func(path string) error
		}{
			{name: "Allow", run: func(path string) error { _, e := rig.scopes.Allow(rig.graph, path, "alice"); return e }},
			{name: "Deny", run: func(path string) error { _, e := rig.scopes.Deny(rig.graph, path, "alice"); return e }},
			{name: "Revoke", run: func(path string) error { _, e := rig.scopes.Revoke(rig.graph, path, "alice"); return e }},
			{name: "Allowed", run: func(path string) error { _, e := rig.scopes.Allowed(rig.graph, "alice", path); return e }},
		}

		badPaths := []string{"", "a//b", "/a", "a/", "\xff", strings.Repeat("a", MaxTextRunes+1)}

		for _, op := range pathOps {
			for _, path := range badPaths {
				if err := op.run(path); !errors.Is(err, ErrInvalidScopePath) {
					t.Fatalf("%s(%.20q) error = %v, want %v", op.name, path, err, ErrInvalidScopePath)
				}
			}
		}

		principalOps := []struct {
			name string
			run  func() error
		}{
			{name: "Allow", run: func() error { _, e := rig.scopes.Allow(rig.graph, "a", ""); return e }},
			{name: "Deny", run: func() error { _, e := rig.scopes.Deny(rig.graph, "a", ""); return e }},
			{name: "Revoke", run: func() error { _, e := rig.scopes.Revoke(rig.graph, "a", ""); return e }},
		}

		for _, op := range principalOps {
			if err := op.run(); !errors.Is(err, ErrInvalidScopePrincipal) {
				t.Fatalf("%s() of the empty principal error = %v, want %v", op.name, err, ErrInvalidScopePrincipal)
			}
		}

		// Asking about the empty principal is not an error: it is a deny.
		rig.requireAllowed(t, "", "a", false)

		if got := len(mustFindNodes(t, rig.graph)); got != nodesBefore {
			t.Fatalf("%d nodes after the refused calls, want %d", got, nodesBefore)
		}
	})
}

func TestScopeWritesComposeAndRollBackWithEnclosingTransaction(t *testing.T) {
	forEachScopeRig(t, func(t *testing.T, rig *scopeRig) {
		nodesBefore := len(mustFindNodes(t, rig.graph))
		errOuter := errors.New("outer failure")

		var inside bool

		err := rig.graph.Transact(func(tx Tx) error {
			if _, allowErr := rig.scopes.Allow(tx, "a/b", "alice"); allowErr != nil {
				return allowErr
			}

			var allowedErr error

			// The transaction reads its own uncommitted grant.
			inside, allowedErr = rig.scopes.Allowed(tx, "alice", "a/b/c")
			if allowedErr != nil {
				return allowedErr
			}

			return errOuter
		})
		if !errors.Is(err, errOuter) {
			t.Fatalf("Transact(fail) error = %v, want %v", err, errOuter)
		}

		if !inside {
			t.Fatal("the grant was not visible inside its own transaction")
		}

		rig.requireAllowed(t, "alice", "a/b/c", false)

		if got := len(mustFindNodes(t, rig.graph)); got != nodesBefore {
			t.Fatalf("%d nodes after the rollback, want %d", got, nodesBefore)
		}

		commitErr := rig.graph.Transact(func(tx Tx) error {
			_, allowErr := rig.scopes.Allow(tx, "a/b", "alice")

			return allowErr
		})
		if commitErr != nil {
			t.Fatalf("Transact(commit) error = %v", commitErr)
		}

		rig.requireAllowed(t, "alice", "a/b/c", true)
	})
}

// TestScopeCheckerDeclinesEachHandMadeViolation corrupts the scope structure in
// one transaction per case and checks that the commit is declined and leaves
// the valid scopes as they were.
func TestScopeCheckerDeclinesEachHandMadeViolation(t *testing.T) {
	cases := []struct {
		name    string
		want    error
		corrupt func(tx Tx, rig *scopeRig) error
	}{
		{
			name: "a scope slot that belongs to no text",
			want: ErrInvalidScope,
			corrupt: func(tx Tx, rig *scopeRig) error {
				_, err := createTaggedNodeTx(tx, rig.scopes.allSlots)

				return err
			},
		},
		{
			name: "a text with two scope slots",
			want: ErrAmbiguousPointerMetadata,
			corrupt: func(tx Tx, rig *scopeRig) error {
				text, err := rig.textTx(tx, "a/b")
				if err != nil {
					return err
				}

				extra, createErr := createTaggedNodeTx(tx, rig.scopes.allSlots)
				if createErr != nil {
					return createErr
				}

				return addRelationshipTx(tx, text, extra)
			},
		},
		{
			name: "a scope record with no path slot",
			want: ErrInvalidScope,
			corrupt: func(tx Tx, rig *scopeRig) error {
				_, err := rig.scopes.fields.NewRecord(tx)

				return err
			},
		},
		{
			name: "a scope record with no allow set",
			want: ErrInvalidScope,
			corrupt: func(tx Tx, rig *scopeRig) error {
				record, err := rig.recordTx(tx, "a/b")
				if err != nil {
					return err
				}

				_, clearErr := rig.scopes.fields.Clear(tx, record, rig.scopes.allow)

				return clearErr
			},
		},
		{
			name: "an allow value that is not a Set",
			want: ErrNotSet,
			corrupt: func(tx Tx, rig *scopeRig) error {
				record, err := rig.recordTx(tx, "a/b")
				if err != nil {
					return err
				}

				plain, createErr := createNodeTx(tx)
				if createErr != nil {
					return createErr
				}

				return rig.scopes.fields.Set(tx, record, rig.scopes.allow, plain)
			},
		},
		{
			name: "one set held by both fields of a record",
			want: ErrInvalidScope,
			corrupt: func(tx Tx, rig *scopeRig) error {
				record, err := rig.recordTx(tx, "a/b")
				if err != nil {
					return err
				}

				allowSet, setErr := rig.scopes.fieldSet(tx, record, rig.scopes.allow)
				if setErr != nil {
					return setErr
				}

				return rig.scopes.fields.Set(tx, record, rig.scopes.deny, allowSet)
			},
		},
		{
			name: "one set shared by two records",
			want: ErrInvalidScope,
			corrupt: func(tx Tx, rig *scopeRig) error {
				first, err := rig.recordTx(tx, "a/b")
				if err != nil {
					return err
				}

				second, secondErr := rig.recordTx(tx, "c")
				if secondErr != nil {
					return secondErr
				}

				shared, setErr := rig.scopes.fieldSet(tx, second, rig.scopes.allow)
				if setErr != nil {
					return setErr
				}

				return rig.scopes.fields.Set(tx, first, rig.scopes.allow, shared)
			},
		},
		{
			name: "a scope slot pointing at a node that is no scope record",
			want: ErrInvalidScope,
			corrupt: func(tx Tx, rig *scopeRig) error {
				slot, err := rig.slotTx(tx, "a/b")
				if err != nil {
					return err
				}

				plain, createErr := createNodeTx(tx)
				if createErr != nil {
					return createErr
				}

				return rig.scopes.slots.SetTarget(tx, slot, plain)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			forEachScopeRig(t, func(t *testing.T, rig *scopeRig) {
				rig.allow(t, "a/b", "alice")
				rig.allow(t, "c", "alice")

				nodesBefore := len(mustFindNodes(t, rig.graph))

				err := rig.graph.Transact(func(tx Tx) error {
					return tc.corrupt(tx, rig)
				})
				if !errors.Is(err, tc.want) {
					t.Fatalf("Transact() error = %v, want %v", err, tc.want)
				}

				if got := len(mustFindNodes(t, rig.graph)); got != nodesBefore {
					t.Fatalf("%d nodes after the declined commit, want %d", got, nodesBefore)
				}

				rig.requireAllowed(t, "alice", "a/b", true)
				rig.requireAllowed(t, "alice", "c", true)
			})
		})
	}
}

func TestScopeVerifyAllAcceptsValidScopes(t *testing.T) {
	forEachScopeRig(t, func(t *testing.T, rig *scopeRig) {
		rig.grantExample(t)

		if err := VerifyAll(rig.graph, 5); err != nil {
			t.Fatalf("VerifyAll() on valid scopes: %v", err)
		}
	})
}

// TestScopeAllowedReadsThroughAReadView: the answers inside one read view (one
// bolt read transaction on BoltGraph, one job on a GraphActor) are the answers
// of direct calls, on every backend, directly and behind a GraphActor.
func TestScopeAllowedReadsThroughAReadView(t *testing.T) {
	forEachBackendAndActor(t, func(t *testing.T, graph testGraph) {
		rig := newScopeRig(t, graph)
		cases := rig.grantExample(t)
		got := make([]bool, len(cases))

		err := readView(rig.graph, func(reader GraphReader) error {
			for i, tc := range cases {
				allowed, allowedErr := rig.scopes.Allowed(reader, tc.principal, tc.path)
				if allowedErr != nil {
					return allowedErr
				}

				got[i] = allowed
			}

			return nil
		})
		if err != nil {
			t.Fatalf("readView(): %v", err)
		}

		for i, tc := range cases {
			if got[i] != tc.want {
				t.Fatalf("Allowed(%q, %q) in a read view = %v, want %v", tc.principal, tc.path, got[i], tc.want)
			}
		}
	})
}

// TestHostRegistriesCarryScopesAcrossARestart: the host's registries serve
// scopes, and a restart (whose startup sweep runs the scope Checker over every
// node) finds the same grants again.
func TestHostRegistriesCarryScopesAcrossARestart(t *testing.T) {
	path := boltTestPath(t)
	first := openTestHost(t, path, nil)
	scopes := first.Registries().Scopes

	if _, err := scopes.Allow(first.Graph(), "run/example", "alice"); err != nil {
		t.Fatalf("Allow(): %v", err)
	}

	if _, err := scopes.Deny(first.Graph(), "run/example/secret", "alice"); err != nil {
		t.Fatalf("Deny(): %v", err)
	}

	check := func(h *Host) {
		t.Helper()

		for _, tc := range []scopeCase{
			{principal: "alice", path: "run/example/list", want: true},
			{principal: "alice", path: "run/example/secret", want: false},
			{principal: "bob", path: "run/example", want: false},
			{principal: "alice", path: "run", want: false},
		} {
			got, err := h.Registries().Scopes.Allowed(h.Graph(), tc.principal, tc.path)
			if err != nil || got != tc.want {
				t.Fatalf("Allowed(%q, %q) = (%v,%v), want (%v,nil)", tc.principal, tc.path, got, err, tc.want)
			}
		}
	}

	check(first)

	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("Close(): %v", closeErr)
	}

	check(openTestHost(t, path, nil))
}