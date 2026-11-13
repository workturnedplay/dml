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

package dml_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"dml"
)

// Tests of the host's operations (theorystate.md section 116, batch 3). They
// use only exported API and helpers of their own (prefixed ops), and need no
// socket.

const (
	opsAlice dml.Principal = "alice"
	opsBob   dml.Principal = "bob"
)

// opsReports collects what the host reports through HostConfig.OnError.
type opsReports struct {
	mu    sync.Mutex
	lines []string
}

func (r *opsReports) record(op string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.lines = append(r.lines, op+": "+err.Error())
}

func (r *opsReports) contains(part string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, line := range r.lines {
		if strings.Contains(line, part) {
			return true
		}
	}

	return false
}

func (r *opsReports) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.lines)
}

// openOperationsHost opens a host over a store in a temporary directory,
// closed when the test ends (before the directory is removed).
func openOperationsHost(t *testing.T) (*dml.Host, *opsReports) {
	t.Helper()

	reports := &opsReports{}

	host, openErr := dml.OpenHost(dml.HostConfig{
		Path:    filepath.Join(t.TempDir(), "dml.db"),
		OnError: reports.record,
	})
	if openErr != nil {
		t.Fatalf("OpenHost: %v", openErr)
	}

	t.Cleanup(func() {
		if closeErr := host.Close(); closeErr != nil {
			t.Errorf("closing the host: %v", closeErr)
		}
	})

	return host, reports
}

func opsRegisterRead(t *testing.T, host *dml.Host, name string, handler dml.ReadOperation) {
	t.Helper()

	if registerErr := host.RegisterReadOperation(name, handler); registerErr != nil {
		t.Fatalf("RegisterReadOperation(%q): %v", name, registerErr)
	}
}

func opsRegisterWrite(t *testing.T, host *dml.Host, name string, handler dml.WriteOperation) {
	t.Helper()

	if registerErr := host.RegisterWriteOperation(name, handler); registerErr != nil {
		t.Fatalf("RegisterWriteOperation(%q): %v", name, registerErr)
	}
}

func opsGrant(t *testing.T, host *dml.Host, operation string, principal dml.Principal) {
	t.Helper()

	if _, grantErr := host.GrantOperation(operation, principal); grantErr != nil {
		t.Fatalf("GrantOperation(%q, %q): %v", operation, principal, grantErr)
	}
}

func opsGrantScope(t *testing.T, host *dml.Host, path string, principal dml.Principal) {
	t.Helper()

	if _, grantErr := host.GrantScope(path, principal); grantErr != nil {
		t.Fatalf("GrantScope(%q, %q): %v", path, principal, grantErr)
	}
}

func opsDenyScope(t *testing.T, host *dml.Host, path string, principal dml.Principal) {
	t.Helper()

	if _, denyErr := host.DenyScope(path, principal); denyErr != nil {
		t.Fatalf("DenyScope(%q, %q): %v", path, principal, denyErr)
	}
}

// opsWantOperationError fails unless err is an *OperationError with code.
func opsWantOperationError(t *testing.T, err error, code string) *dml.OperationError {
	t.Helper()

	var opErr *dml.OperationError
	if !errors.As(err, &opErr) {
		t.Fatalf("error = %v, want an *OperationError with code %q", err, code)
	}

	if opErr.Code != code {
		t.Fatalf("error code = %q (%s), want %q", opErr.Code, opErr.Message, code)
	}

	return opErr
}

// opsTextInterned reports whether text was interned in host's graph.
func opsTextInterned(t *testing.T, host *dml.Host, text string) bool {
	t.Helper()

	_, found, lookupErr := host.Registries().Text.Lookup(host.Graph(), text)
	if lookupErr != nil {
		t.Fatalf("Lookup(%q): %v", text, lookupErr)
	}

	return found
}

func opsNoopRead(*dml.OperationContext, dml.GraphReader, dml.OperationArgs) (dml.OperationResult, error) {
	return dml.OperationResult{}, nil
}

func TestOperationReadRunsForAGrantedPrincipalOnly(t *testing.T) {
	host, _ := openOperationsHost(t)

	opsRegisterRead(t, host, "list", func(ctx *dml.OperationContext, _ dml.GraphReader, args dml.OperationArgs) (dml.OperationResult, error) {
		return dml.OperationResult{
			Rows:   []dml.OperationRow{{"who": string(ctx.Principal()), "op": ctx.Operation(), "echo": args["word"]}},
			Cursor: "next",
		}, nil
	})
	opsGrant(t, host, "list", opsAlice)

	result, runErr := host.RunOperation(t.Context(), opsAlice, "list", dml.OperationArgs{"word": "hi"})
	if runErr != nil {
		t.Fatalf("alice's run: %v", runErr)
	}

	if len(result.Rows) != 1 || result.Rows[0]["who"] != "alice" || result.Rows[0]["op"] != "list" || result.Rows[0]["echo"] != "hi" || result.Cursor != "next" {
		t.Fatalf("result = %+v", result)
	}

	_, forbiddenErr := host.RunOperation(t.Context(), opsBob, "list", nil)
	if !errors.Is(forbiddenErr, dml.ErrNotAuthorized) {
		t.Fatalf("bob's run: %v, want ErrNotAuthorized", forbiddenErr)
	}

	_, unknownErr := host.RunOperation(t.Context(), opsBob, "no-such-operation", nil)
	if !errors.Is(unknownErr, dml.ErrNotAuthorized) {
		t.Fatalf("unknown operation: %v, want ErrNotAuthorized", unknownErr)
	}

	if forbiddenErr.Error() != unknownErr.Error() {
		t.Fatalf("an unknown and a forbidden operation answer differently: %q and %q", unknownErr, forbiddenErr)
	}
}

func TestOperationEmptyPrincipalIsDeniedEvenWhenEveryoneIsAllowed(t *testing.T) {
	host, _ := openOperationsHost(t)

	opsRegisterRead(t, host, "list", opsNoopRead)
	opsGrant(t, host, "list", dml.AnyPrincipal)

	if _, runErr := host.RunOperation(t.Context(), opsBob, "list", nil); runErr != nil {
		t.Fatalf("an identified principal under AnyPrincipal: %v", runErr)
	}

	if _, runErr := host.RunOperation(t.Context(), "", "list", nil); !errors.Is(runErr, dml.ErrNotAuthorized) {
		t.Fatalf("the empty principal: %v, want ErrNotAuthorized", runErr)
	}
}

func TestOperationGrantDenyAndRevokeTakeEffectOnTheNextRun(t *testing.T) {
	host, _ := openOperationsHost(t)

	opsRegisterRead(t, host, "list", opsNoopRead)

	changed, grantErr := host.GrantOperation("list", opsAlice)
	if grantErr != nil || !changed {
		t.Fatalf("first grant = (%v, %v), want (true, nil)", changed, grantErr)
	}

	changed, grantErr = host.GrantOperation("list", opsAlice)
	if grantErr != nil || changed {
		t.Fatalf("second grant = (%v, %v), want (false, nil)", changed, grantErr)
	}

	if _, runErr := host.RunOperation(t.Context(), opsAlice, "list", nil); runErr != nil {
		t.Fatalf("run after grant: %v", runErr)
	}

	if _, denyErr := host.DenyOperation("list", opsAlice); denyErr != nil {
		t.Fatalf("DenyOperation: %v", denyErr)
	}

	if _, runErr := host.RunOperation(t.Context(), opsAlice, "list", nil); !errors.Is(runErr, dml.ErrNotAuthorized) {
		t.Fatalf("deny must beat allow at the same level: %v", runErr)
	}

	revoked, revokeErr := host.RevokeOperation("list", opsAlice)
	if revokeErr != nil || !revoked {
		t.Fatalf("revoke = (%v, %v), want (true, nil)", revoked, revokeErr)
	}

	revoked, revokeErr = host.RevokeOperation("list", opsAlice)
	if revokeErr != nil || revoked {
		t.Fatalf("second revoke = (%v, %v), want (false, nil)", revoked, revokeErr)
	}

	if _, runErr := host.RunOperation(t.Context(), opsAlice, "list", nil); !errors.Is(runErr, dml.ErrNotAuthorized) {
		t.Fatalf("run after revoke: %v, want ErrNotAuthorized (default deny)", runErr)
	}
}

func TestOperationGrantHelpersRejectBadInput(t *testing.T) {
	host, _ := openOperationsHost(t)

	if _, grantErr := host.GrantOperation("a/b", opsAlice); !errors.Is(grantErr, dml.ErrInvalidScopePath) {
		t.Fatalf("operation with a slash: %v, want ErrInvalidScopePath", grantErr)
	}

	if _, grantErr := host.GrantOperation("list", ""); !errors.Is(grantErr, dml.ErrInvalidScopePrincipal) {
		t.Fatalf("empty principal: %v, want ErrInvalidScopePrincipal", grantErr)
	}

	if _, grantErr := host.GrantScope("", opsAlice); !errors.Is(grantErr, dml.ErrInvalidScopePath) {
		t.Fatalf("empty path: %v, want ErrInvalidScopePath", grantErr)
	}
}

func TestOperationHandlerChecksFinerScopes(t *testing.T) {
	host, reports := openOperationsHost(t)

	opsRegisterRead(t, host, "get", func(ctx *dml.OperationContext, _ dml.GraphReader, args dml.OperationArgs) (dml.OperationResult, error) {
		allowed, allowedErr := ctx.AllowedSegments("read", "settings", args["key"])
		if allowedErr != nil {
			return dml.OperationResult{}, fmt.Errorf("checking the key: %w", allowedErr)
		}

		if !allowed {
			return dml.OperationResult{}, dml.ErrNotAuthorized
		}

		return dml.OperationResult{Rows: []dml.OperationRow{{"key": args["key"]}}}, nil
	})
	opsGrant(t, host, "get", opsAlice)
	opsGrantScope(t, host, "read/settings", opsAlice)
	opsDenyScope(t, host, "read/settings/secret", opsAlice)

	if _, runErr := host.RunOperation(t.Context(), opsAlice, "get", dml.OperationArgs{"key": "name"}); runErr != nil {
		t.Fatalf("an allowed key: %v", runErr)
	}

	_, secretErr := host.RunOperation(t.Context(), opsAlice, "get", dml.OperationArgs{"key": "secret"})
	if !errors.Is(secretErr, dml.ErrNotAuthorized) {
		t.Fatalf("a denied key: %v, want ErrNotAuthorized", secretErr)
	}

	// The handler's own denial is the same answer as for an operation that
	// does not exist.
	_, unknownErr := host.RunOperation(t.Context(), opsAlice, "no-such-operation", nil)
	if unknownErr == nil || secretErr.Error() != unknownErr.Error() {
		t.Fatalf("a handler's denial answers differently from an unknown operation: %v and %v", secretErr, unknownErr)
	}

	// A key from the client that contains the separator cannot forge a path.
	_, forgeryErr := host.RunOperation(t.Context(), opsAlice, "get", dml.OperationArgs{"key": "name/x"})
	opsWantOperationError(t, forgeryErr, dml.OperationCodeInternal)

	if !reports.contains("invalid") {
		t.Fatalf("the forged key was not reported: %d reports", reports.count())
	}
}

func TestOperationWriteCommitsAndRollsBackAsOne(t *testing.T) {
	host, reports := openOperationsHost(t)

	opsRegisterWrite(t, host, "intern", func(ctx *dml.OperationContext, tx dml.Tx, args dml.OperationArgs) (dml.OperationResult, error) {
		if _, internErr := ctx.Registries().Text.Intern(tx, args["text"]); internErr != nil {
			return dml.OperationResult{}, fmt.Errorf("interning: %w", internErr)
		}

		switch args["then"] {
		case "fail":
			return dml.OperationResult{}, &dml.OperationError{Code: "refused", Message: "refused on purpose"}
		case "panic":
			panic("boom")
		case "unexpected":
			return dml.OperationResult{}, errors.New("secret detail")
		}

		return dml.OperationResult{Rows: []dml.OperationRow{{"text": args["text"]}}}, nil
	})
	opsGrant(t, host, "intern", opsAlice)

	run := func(args dml.OperationArgs) (dml.OperationResult, error) {
		return host.RunOperation(t.Context(), opsAlice, "intern", args)
	}

	if _, runErr := run(dml.OperationArgs{"text": "kept"}); runErr != nil {
		t.Fatalf("a good run: %v", runErr)
	}

	if !opsTextInterned(t, host, "kept") {
		t.Fatal("a committed run left no trace")
	}

	_, failErr := run(dml.OperationArgs{"text": "lost-fail", "then": "fail"})
	if opErr := opsWantOperationError(t, failErr, "refused"); opErr.Message != "refused on purpose" {
		t.Fatalf("the handler's error message = %q", opErr.Message)
	}

	if opsTextInterned(t, host, "lost-fail") {
		t.Fatal("a failed run was not rolled back")
	}

	_, panicErr := run(dml.OperationArgs{"text": "lost-panic", "then": "panic"})
	opsWantOperationError(t, panicErr, dml.OperationCodeInternal)

	if opsTextInterned(t, host, "lost-panic") {
		t.Fatal("a panicking run was not rolled back")
	}

	if !reports.contains("panicked") {
		t.Fatal("a panic was not reported")
	}

	_, unexpectedErr := run(dml.OperationArgs{"text": "lost-unexpected", "then": "unexpected"})
	if opErr := opsWantOperationError(t, unexpectedErr, dml.OperationCodeInternal); strings.Contains(opErr.Message, "secret") {
		t.Fatalf("the client was shown an internal detail: %q", opErr.Message)
	}

	if !reports.contains("secret detail") {
		t.Fatal("an unexpected error was not reported")
	}

	if opsTextInterned(t, host, "lost-unexpected") {
		t.Fatal("a run that failed unexpectedly was not rolled back")
	}

	// The host survived all of it.
	if _, runErr := run(dml.OperationArgs{"text": "after"}); runErr != nil {
		t.Fatalf("a run after the failures: %v", runErr)
	}
}

func TestOperationArgumentLimits(t *testing.T) {
	host, _ := openOperationsHost(t)

	var calls atomic.Int32

	opsRegisterRead(t, host, "list", func(*dml.OperationContext, dml.GraphReader, dml.OperationArgs) (dml.OperationResult, error) {
		calls.Add(1)

		return dml.OperationResult{}, nil
	})
	opsGrant(t, host, "list", opsAlice)

	tooMany := make(dml.OperationArgs)
	for i := range dml.MaxOperationArgs + 1 {
		tooMany[fmt.Sprintf("k%d", i)] = "v"
	}

	cases := map[string]dml.OperationArgs{
		"too many":      tooMany,
		"empty name":    {"": "v"},
		"long name":     {strings.Repeat("n", dml.MaxOperationNameBytes+1): "v"},
		"long value":    {"k": strings.Repeat("x", dml.MaxOperationValueBytes+1)},
		"invalid UTF-8": {"k": "\xff"},
	}

	for label, args := range cases {
		_, runErr := host.RunOperation(t.Context(), opsAlice, "list", args)
		if runErr == nil {
			t.Fatalf("%s: accepted", label)
		}

		opsWantOperationError(t, runErr, dml.OperationCodeInvalidArguments)
	}

	if calls.Load() != 0 {
		t.Fatalf("the handler ran %d times for arguments that break the limits", calls.Load())
	}

	// Argument limits do not depend on the operation, so they reveal nothing
	// about which exist.
	_, unknownErr := host.RunOperation(t.Context(), opsBob, "no-such-operation", tooMany)
	opsWantOperationError(t, unknownErr, dml.OperationCodeInvalidArguments)

	if _, runErr := host.RunOperation(t.Context(), opsAlice, "list", dml.OperationArgs{"k": "v"}); runErr != nil {
		t.Fatalf("valid arguments: %v", runErr)
	}
}

func TestOperationResultLimitsFailTheRunAndRollItBack(t *testing.T) {
	host, reports := openOperationsHost(t)

	opsRegisterWrite(t, host, "big", func(ctx *dml.OperationContext, tx dml.Tx, args dml.OperationArgs) (dml.OperationResult, error) {
		if _, internErr := ctx.Registries().Text.Intern(tx, "written-by-big"); internErr != nil {
			return dml.OperationResult{}, fmt.Errorf("interning: %w", internErr)
		}

		switch args["kind"] {
		case "rows":
			return dml.OperationResult{Rows: make([]dml.OperationRow, dml.MaxOperationRows+1)}, nil
		case "bytes":
			row := dml.OperationRow{"f": strings.Repeat("x", dml.MaxOperationResultBytes+1)}

			return dml.OperationResult{Rows: []dml.OperationRow{row}}, nil
		case "utf8":
			return dml.OperationResult{Rows: []dml.OperationRow{{"f": "\xff"}}}, nil
		default:
			return dml.OperationResult{Rows: []dml.OperationRow{{"f": "fine"}}}, nil
		}
	})
	opsGrant(t, host, "big", opsAlice)

	for _, kind := range []string{"rows", "bytes", "utf8"} {
		before := reports.count()

		_, runErr := host.RunOperation(t.Context(), opsAlice, "big", dml.OperationArgs{"kind": kind})
		opsWantOperationError(t, runErr, dml.OperationCodeInternal)

		if reports.count() == before {
			t.Fatalf("%s: an unusable result was not reported", kind)
		}

		if opsTextInterned(t, host, "written-by-big") {
			t.Fatalf("%s: the write of a run with an unusable result was not rolled back", kind)
		}
	}

	if _, runErr := host.RunOperation(t.Context(), opsAlice, "big", dml.OperationArgs{"kind": "ok"}); runErr != nil {
		t.Fatalf("a result within the limits: %v", runErr)
	}
}

func TestOperationRegistrationRules(t *testing.T) {
	host, _ := openOperationsHost(t)

	for _, name := range []string{"", "a/b", strings.Repeat("n", dml.MaxOperationNameBytes+1)} {
		if registerErr := host.RegisterReadOperation(name, opsNoopRead); !errors.Is(registerErr, dml.ErrInvalidOperation) {
			t.Fatalf("name %q: %v, want ErrInvalidOperation", name, registerErr)
		}
	}

	if registerErr := host.RegisterReadOperation("nil-read", nil); !errors.Is(registerErr, dml.ErrInvalidOperation) {
		t.Fatalf("nil read handler: %v, want ErrInvalidOperation", registerErr)
	}

	if registerErr := host.RegisterWriteOperation("nil-write", nil); !errors.Is(registerErr, dml.ErrInvalidOperation) {
		t.Fatalf("nil write handler: %v, want ErrInvalidOperation", registerErr)
	}

	opsRegisterRead(t, host, "once", opsNoopRead)

	if registerErr := host.RegisterReadOperation("once", opsNoopRead); !errors.Is(registerErr, dml.ErrOperationRegistered) {
		t.Fatalf("a second read registration: %v, want ErrOperationRegistered", registerErr)
	}

	noopWrite := func(*dml.OperationContext, dml.Tx, dml.OperationArgs) (dml.OperationResult, error) {
		return dml.OperationResult{}, nil
	}

	if registerErr := host.RegisterWriteOperation("once", noopWrite); !errors.Is(registerErr, dml.ErrOperationRegistered) {
		t.Fatalf("a write registration of a taken name: %v, want ErrOperationRegistered", registerErr)
	}
}

func TestOperationRunAndRegisterFailOnceTheHostIsClosedOrTheContextEnded(t *testing.T) {
	host, _ := openOperationsHost(t)

	opsRegisterRead(t, host, "list", opsNoopRead)
	opsGrant(t, host, "list", opsAlice)

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()

	if _, runErr := host.RunOperation(cancelled, opsAlice, "list", nil); !errors.Is(runErr, cancelled.Err()) {
		t.Fatalf("a run with an ended context: %v, want %v", runErr, cancelled.Err())
	}

	if closeErr := host.Close(); closeErr != nil {
		t.Fatalf("Close: %v", closeErr)
	}

	if _, runErr := host.RunOperation(t.Context(), opsAlice, "list", nil); !errors.Is(runErr, dml.ErrHostClosed) {
		t.Fatalf("a run on a closed host: %v, want ErrHostClosed", runErr)
	}

	if registerErr := host.RegisterReadOperation("late", opsNoopRead); !errors.Is(registerErr, dml.ErrHostClosed) {
		t.Fatalf("a registration on a closed host: %v, want ErrHostClosed", registerErr)
	}
}
