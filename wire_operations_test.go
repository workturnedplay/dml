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
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"dml"
)

// Tests of operations over the wire (theorystate.md section 116, batch 4). They
// run over net.Pipe, so no socket is opened, and reuse the helpers of
// operations_test.go.

// opsPeer names every peer of a server as principal.
func opsPeer(principal dml.Principal) dml.WireOption {
	return dml.WithPeerIdentifier(func(net.Conn) (dml.Principal, error) { return principal, nil })
}

// opsWireClient serves host over an in-memory pipe with opts and returns a
// connected client. The client, then the server, are closed when the test ends
// (before the host, which openOperationsHost closed last).
func opsWireClient(t *testing.T, host *dml.Host, opts ...dml.WireOption) *dml.WireClient {
	t.Helper()

	server := dml.NewWireServer(host, opts...)

	t.Cleanup(func() {
		if closeErr := server.Close(); closeErr != nil {
			t.Errorf("closing the server: %v", closeErr)
		}
	})

	clientSide, serverSide := net.Pipe()

	go server.ServeConn(serverSide)

	client, dialErr := dml.NewWireClient(t.Context(), clientSide)
	if dialErr != nil {
		t.Fatalf("NewWireClient: %v", dialErr)
	}

	t.Cleanup(func() {
		if closeErr := client.Close(); closeErr != nil {
			t.Errorf("closing the client: %v", closeErr)
		}
	})

	return client
}

func TestWireOperationRunsAndReturnsRowsAndCursor(t *testing.T) {
	host, _ := openOperationsHost(t)

	opsRegisterRead(t, host, "list", func(ctx *dml.OperationContext, _ dml.GraphReader, args dml.OperationArgs) (dml.OperationResult, error) {
		return dml.OperationResult{
			Rows: []dml.OperationRow{
				{"who": string(ctx.Principal()), "echo": args["word"]},
				{"second": "row"},
			},
			Cursor: "next",
		}, nil
	})
	opsGrant(t, host, "list", opsAlice)

	client := opsWireClient(t, host, opsPeer(opsAlice), dml.WithOperations())

	result, runErr := client.RunOperation(t.Context(), "list", dml.OperationArgs{"word": "hi"})
	if runErr != nil {
		t.Fatalf("RunOperation: %v", runErr)
	}

	if len(result.Rows) != 2 || result.Rows[0]["who"] != "alice" || result.Rows[0]["echo"] != "hi" || result.Rows[1]["second"] != "row" || result.Cursor != "next" {
		t.Fatalf("result = %+v", result)
	}
}

func TestWireOperationsAreOffUnlessEnabled(t *testing.T) {
	host, _ := openOperationsHost(t)

	opsRegisterRead(t, host, "list", opsNoopRead)
	opsGrant(t, host, "list", opsAlice)

	client := opsWireClient(t, host, opsPeer(opsAlice))

	if _, runErr := client.RunOperation(t.Context(), "list", nil); !errors.Is(runErr, dml.ErrNotAuthorized) {
		t.Fatalf("a server without WithOperations: %v, want ErrNotAuthorized", runErr)
	}
}

func TestWireOperationDenialsAreIdentical(t *testing.T) {
	host, _ := openOperationsHost(t)

	opsRegisterRead(t, host, "list", opsNoopRead)
	opsGrant(t, host, "list", opsAlice)

	client := opsWireClient(t, host, opsPeer(opsBob), dml.WithOperations())

	_, forbiddenErr := client.RunOperation(t.Context(), "list", nil)
	if !errors.Is(forbiddenErr, dml.ErrNotAuthorized) {
		t.Fatalf("a forbidden operation: %v, want ErrNotAuthorized", forbiddenErr)
	}

	_, unknownErr := client.RunOperation(t.Context(), "no-such-operation", nil)
	if !errors.Is(unknownErr, dml.ErrNotAuthorized) {
		t.Fatalf("an unknown operation: %v, want ErrNotAuthorized", unknownErr)
	}

	if forbiddenErr.Error() != unknownErr.Error() {
		t.Fatalf("the two answers differ: %q and %q", forbiddenErr, unknownErr)
	}
}

func TestWireUnidentifiedPeerIsDeniedEvenWhenEveryoneIsAllowed(t *testing.T) {
	host, _ := openOperationsHost(t)

	opsRegisterRead(t, host, "list", opsNoopRead)
	opsGrant(t, host, "list", dml.AnyPrincipal)

	identified := opsWireClient(t, host, opsPeer(opsBob), dml.WithOperations())
	if _, runErr := identified.RunOperation(t.Context(), "list", nil); runErr != nil {
		t.Fatalf("an identified peer under AnyPrincipal: %v", runErr)
	}

	unidentified := opsWireClient(t, host, dml.WithOperations())
	if _, runErr := unidentified.RunOperation(t.Context(), "list", nil); !errors.Is(runErr, dml.ErrNotAuthorized) {
		t.Fatalf("a peer nobody identified: %v, want ErrNotAuthorized", runErr)
	}
}

func TestWireOperationErrorsCrossWithTheHandlersCodeAndHideInternals(t *testing.T) {
	host, reports := openOperationsHost(t)

	opsRegisterRead(t, host, "refuse", func(*dml.OperationContext, dml.GraphReader, dml.OperationArgs) (dml.OperationResult, error) {
		return dml.OperationResult{}, &dml.OperationError{Code: "refused", Message: "no thanks"}
	})
	opsRegisterRead(t, host, "leak", func(*dml.OperationContext, dml.GraphReader, dml.OperationArgs) (dml.OperationResult, error) {
		return dml.OperationResult{}, errors.New("secret detail")
	})
	opsRegisterRead(t, host, "panic", func(*dml.OperationContext, dml.GraphReader, dml.OperationArgs) (dml.OperationResult, error) {
		panic("boom")
	})
	opsRegisterRead(t, host, "list", opsNoopRead)

	for _, name := range []string{"refuse", "leak", "panic", "list"} {
		opsGrant(t, host, name, opsAlice)
	}

	client := opsWireClient(t, host, opsPeer(opsAlice), dml.WithOperations())

	_, refuseErr := client.RunOperation(t.Context(), "refuse", nil)
	if opErr := opsWantOperationError(t, refuseErr, "refused"); opErr.Message != "no thanks" {
		t.Fatalf("the handler's message = %q", opErr.Message)
	}

	_, leakErr := client.RunOperation(t.Context(), "leak", nil)
	if opErr := opsWantOperationError(t, leakErr, dml.OperationCodeInternal); strings.Contains(opErr.Message, "secret") {
		t.Fatalf("the client was shown an internal detail: %q", opErr.Message)
	}

	if !reports.contains("secret detail") {
		t.Fatal("the internal error was not reported to the host")
	}

	_, panicErr := client.RunOperation(t.Context(), "panic", nil)
	opsWantOperationError(t, panicErr, dml.OperationCodeInternal)

	if !reports.contains("panicked") {
		t.Fatal("the panic was not reported to the host")
	}

	// The connection, and the host, survived all of it.
	if _, runErr := client.RunOperation(t.Context(), "list", nil); runErr != nil {
		t.Fatalf("a run after the failures: %v", runErr)
	}
}

func TestWireOperationWriteCommitsAndAPanicRollsItBack(t *testing.T) {
	host, _ := openOperationsHost(t)

	opsRegisterWrite(t, host, "intern", func(ctx *dml.OperationContext, tx dml.Tx, args dml.OperationArgs) (dml.OperationResult, error) {
		if _, internErr := ctx.Registries().Text.Intern(tx, args["text"]); internErr != nil {
			return dml.OperationResult{}, fmt.Errorf("interning: %w", internErr)
		}

		if args["then"] == "panic" {
			panic("boom")
		}

		return dml.OperationResult{Rows: []dml.OperationRow{{"text": args["text"]}}}, nil
	})
	opsGrant(t, host, "intern", opsAlice)

	client := opsWireClient(t, host, opsPeer(opsAlice), dml.WithOperations())

	if _, runErr := client.RunOperation(t.Context(), "intern", dml.OperationArgs{"text": "kept"}); runErr != nil {
		t.Fatalf("a good run: %v", runErr)
	}

	if !opsTextInterned(t, host, "kept") {
		t.Fatal("a committed run left no trace")
	}

	_, panicErr := client.RunOperation(t.Context(), "intern", dml.OperationArgs{"text": "lost", "then": "panic"})
	opsWantOperationError(t, panicErr, dml.OperationCodeInternal)

	if opsTextInterned(t, host, "lost") {
		t.Fatal("a panicking run was not rolled back")
	}
}

func TestWireOperationRevocationAppliesToTheNextCall(t *testing.T) {
	host, _ := openOperationsHost(t)

	opsRegisterRead(t, host, "list", opsNoopRead)
	opsGrant(t, host, "list", opsAlice)

	client := opsWireClient(t, host, opsPeer(opsAlice), dml.WithOperations())

	if _, runErr := client.RunOperation(t.Context(), "list", nil); runErr != nil {
		t.Fatalf("run after the grant: %v", runErr)
	}

	if _, revokeErr := host.RevokeOperation("list", opsAlice); revokeErr != nil {
		t.Fatalf("RevokeOperation: %v", revokeErr)
	}

	if _, runErr := client.RunOperation(t.Context(), "list", nil); !errors.Is(runErr, dml.ErrNotAuthorized) {
		t.Fatalf("run after the revocation: %v, want ErrNotAuthorized", runErr)
	}

	opsGrant(t, host, "list", opsAlice)

	if _, runErr := client.RunOperation(t.Context(), "list", nil); runErr != nil {
		t.Fatalf("run after granting again (the connection must have survived): %v", runErr)
	}
}

func TestWireOperationArgumentsOverTheLimitCrossAsInvalidArguments(t *testing.T) {
	host, _ := openOperationsHost(t)

	opsRegisterRead(t, host, "list", opsNoopRead)
	opsGrant(t, host, "list", opsAlice)

	client := opsWireClient(t, host, opsPeer(opsAlice), dml.WithOperations())

	tooMany := make(dml.OperationArgs)
	for i := range dml.MaxOperationArgs + 1 {
		tooMany[fmt.Sprintf("k%d", i)] = "v"
	}

	_, runErr := client.RunOperation(t.Context(), "list", tooMany)
	opsWantOperationError(t, runErr, dml.OperationCodeInvalidArguments)

	if _, okErr := client.RunOperation(t.Context(), "list", dml.OperationArgs{"k": "v"}); okErr != nil {
		t.Fatalf("valid arguments after the refusal: %v", okErr)
	}
}

func TestWireOperationLargestResultStillFitsOneFrame(t *testing.T) {
	host, _ := openOperationsHost(t)

	// Characters JSON escapes to six bytes each, as many as the limit allows
	// (the 13 bytes are the row and field allowances: 4 + len("f") + 8).
	value := strings.Repeat("<", dml.MaxOperationResultBytes-13)

	opsRegisterRead(t, host, "big", func(*dml.OperationContext, dml.GraphReader, dml.OperationArgs) (dml.OperationResult, error) {
		return dml.OperationResult{Rows: []dml.OperationRow{{"f": value}}}, nil
	})
	opsRegisterRead(t, host, "list", opsNoopRead)
	opsGrant(t, host, "big", opsAlice)
	opsGrant(t, host, "list", opsAlice)

	client := opsWireClient(t, host, opsPeer(opsAlice), dml.WithOperations())

	result, runErr := client.RunOperation(t.Context(), "big", nil)
	if runErr != nil {
		t.Fatalf("the largest allowed result: %v", runErr)
	}

	if len(result.Rows) != 1 || result.Rows[0]["f"] != value {
		t.Fatalf("the result did not arrive intact (%d rows)", len(result.Rows))
	}

	// The connection survived it.
	if _, listErr := client.RunOperation(t.Context(), "list", nil); listErr != nil {
		t.Fatalf("a run after the large result: %v", listErr)
	}
}