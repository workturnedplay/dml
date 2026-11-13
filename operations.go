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
	"context"
	"errors"
	"fmt"
	"sync"
	"unicode/utf8"
)

// Operations: host-registered named handlers with scoped authorization
// (theorystate.md section 116, stage 2 of plan-as-data, batch 3).
//
// The host owner registers a handler in Go under a name. A caller (in-process
// now, over the wire in batch 4) runs it with named text arguments and gets
// back rows of named text fields and an optional cursor. No NodeIDs and no
// steps cross that boundary.
//
// Authorization is the scopes of ScopeRegistry. Before a handler runs, the
// entry scope "run/<operation>" must allow the caller's principal; the check
// is made in the same read view or transaction the handler runs in, so a
// revocation cannot race a run. The handler asks for finer scopes through its
// OperationContext for the data it touches.

const (
	// OperationScopeRoot is the first segment of the entry scope of every
	// operation: the scope of operation "x" is "run/x" (see OperationScope).
	OperationScopeRoot = "run"

	// MaxOperationArgs is the most arguments one run may carry.
	MaxOperationArgs = 64

	// MaxOperationNameBytes bounds the name of an operation, of an argument
	// and of a result field.
	MaxOperationNameBytes = 128

	// MaxOperationValueBytes bounds one argument value and one error message.
	MaxOperationValueBytes = 8192

	// MaxOperationRows is the most rows a result may have.
	MaxOperationRows = 1000

	// MaxOperationFields is the most fields one result row may have.
	MaxOperationFields = 64

	// MaxOperationResultBytes bounds the size of a whole result (names,
	// values and the cursor, plus a small allowance per field and row). It is
	// small enough that the result still fits one wire frame after JSON
	// escaping.
	MaxOperationResultBytes = 128 << 10

	operationFieldOverhead = 8
	operationRowOverhead   = 4
)

// The codes of the errors the host itself makes. A handler chooses its own
// codes for the errors it wants a client to see.
const (
	// OperationCodeInternal is the code a client sees for any failure whose
	// cause the host keeps to itself.
	OperationCodeInternal = "internal"

	// OperationCodeInvalidArguments is the code of an argument list that
	// breaks the limits above.
	OperationCodeInvalidArguments = "invalid_arguments"

	// operationCodeFailed is the code given to a handler's error that has none.
	operationCodeFailed = "failed"
)

var (
	// ErrInvalidOperation is returned when an operation cannot be registered:
	// a name that is empty, too long or not usable as a scope segment (it
	// contains "/"), or no handler.
	ErrInvalidOperation = errors.New("operation is invalid")

	// ErrOperationRegistered is returned when an operation name is registered
	// twice.
	ErrOperationRegistered = errors.New("operation is already registered")

	// ErrOperationResult is returned (inside the host's error report, never to
	// the client) for a handler result that breaks the limits above or is not
	// valid text.
	ErrOperationResult = errors.New("operation result is not usable")
)

// OperationArgs are the named text arguments of one run.
type OperationArgs map[string]string

// OperationRow is one row of a result: named text fields.
type OperationRow map[string]string

// OperationResult is what a handler returns: rows of named text fields and an
// optional cursor, a string only the handler understands, for listings that
// continue in a later run.
type OperationResult struct {
	Rows   []OperationRow
	Cursor string
}

// OperationError is an error a client may see. A handler returns one for
// failures it wants to explain ("invalid pattern"). Any other error a handler
// returns is reported to HostConfig.OnError and the client gets a generic
// failure (OperationCodeInternal), so internals and secrets do not leak.
type OperationError struct {
	Code    string
	Message string
}

// Error returns the message, or the code if there is none.
func (e *OperationError) Error() string {
	if e.Message == "" {
		return e.Code
	}

	return e.Message
}

// OperationErrorf returns an *OperationError with the given code and a
// formatted message.
func OperationErrorf(code, format string, args ...any) *OperationError {
	return &OperationError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// internalOperationError is the error a client sees when the cause stays with
// the host.
func internalOperationError() *OperationError {
	return &OperationError{Code: OperationCodeInternal, Message: "the operation failed"}
}

// operationDenied is the one answer for an operation that does not exist and
// one the caller may not run, so the answer reveals nothing about which.
func operationDenied() error {
	return fmt.Errorf("%w: the operation is unknown or not allowed to you", ErrNotAuthorized)
}

// OperationScope returns the entry scope of operation: "run/<operation>". It
// rejects a name that is empty or contains "/" (ErrInvalidScopePath).
func OperationScope(operation string) (string, error) {
	return ScopePath(OperationScopeRoot, operation)
}

// OperationContext is what a handler knows about the run it is part of. It is
// valid only while the handler runs. Everything it reads goes through the read
// view or the transaction the handler was given.
type OperationContext struct {
	host      *Host
	reader    GraphReader
	principal Principal
	operation string
}

// Principal returns who is running the operation.
func (c *OperationContext) Principal() Principal {
	return c.principal
}

// Operation returns the name of the operation being run.
func (c *OperationContext) Operation() string {
	return c.operation
}

// Registries returns the host's registries. A handler must read and write
// them only through the graph value it was handed, never through
// Host.Graph().
func (c *OperationContext) Registries() *Registries {
	return c.host.reg
}

// Allowed reports whether the principal may use the scope path (see
// ScopeRegistry for the rules). It only reads. Any error means the answer
// could not be found: treat it as a deny.
func (c *OperationContext) Allowed(path string) (bool, error) {
	return c.host.reg.Scopes.Allowed(c.reader, string(c.principal), path)
}

// AllowedSegments is Allowed for a path built from segments with ScopePath, so
// a segment that came from the client and contains "/" is an error and cannot
// forge a scope elsewhere in the tree.
func (c *OperationContext) AllowedSegments(segments ...string) (bool, error) {
	path, pathErr := ScopePath(segments...)
	if pathErr != nil {
		return false, pathErr
	}

	return c.Allowed(path)
}

// Require returns nil if the principal may use the scope path, an error
// wrapping ErrNotAuthorized if not (a handler returns it as it is, and the
// client sees the same answer as for an operation it may not run), and any
// other error if the answer could not be found.
func (c *OperationContext) Require(path string) error {
	allowed, allowedErr := c.Allowed(path)
	if allowedErr != nil {
		return allowedErr
	}

	if !allowed {
		return fmt.Errorf("%w: scope %q", ErrNotAuthorized, path)
	}

	return nil
}

// entryFault marks a failure to read the entry scope of an operation. The
// client is answered as for a denial (fail closed), and the cause is
// reported.
type entryFault struct {
	cause error
}

func (f *entryFault) Error() string {
	return "checking the entry scope: " + f.cause.Error()
}

func (f *entryFault) Unwrap() error {
	return f.cause
}

// requireEntry checks the entry scope of the operation being run.
func (c *OperationContext) requireEntry() error {
	path, pathErr := OperationScope(c.operation)
	if pathErr != nil {
		return &entryFault{cause: pathErr}
	}

	if requireErr := c.Require(path); requireErr != nil {
		if errors.Is(requireErr, ErrNotAuthorized) {
			return requireErr
		}

		return &entryFault{cause: requireErr}
	}

	return nil
}

// ReadOperation is the handler of an operation that only reads. It runs in one
// read view (one consistent snapshot, no commit), and must read only through
// graph. It may be called more than once for one run by a retrying backend, so
// it has no side effects outside the graph.
type ReadOperation func(ctx *OperationContext, graph GraphReader, args OperationArgs) (OperationResult, error)

// WriteOperation is the handler of an operation that writes. It runs inside
// one Transact on the host's guarded graph together with the entry-scope
// check, so everything it does commits or rolls back as one and a write that
// changes a resource's holder set wakes the reconciler. It follows the
// Transact contract (theorystate.md section 91): it may run more than once, it
// reads and writes only through tx, and anything it keeps outside the graph is
// updated only from tx.OnCommit/tx.OnRollback.
type WriteOperation func(ctx *OperationContext, tx Tx, args OperationArgs) (OperationResult, error)

// hostOperation is one registered operation: exactly one handler is set.
type hostOperation struct {
	read  ReadOperation
	write WriteOperation
}

// operationTable holds the registered operations. Its zero value is ready to
// use.
type operationTable struct {
	mu  sync.RWMutex
	ops map[string]hostOperation
}

// add records op under name and reports false if the name is taken.
func (t *operationTable) add(name string, op hostOperation) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, taken := t.ops[name]; taken {
		return false
	}

	if t.ops == nil {
		t.ops = make(map[string]hostOperation)
	}

	t.ops[name] = op

	return true
}

// lookup returns the operation registered under name.
func (t *operationTable) lookup(name string) (hostOperation, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	op, found := t.ops[name]

	return op, found
}

// validateOperationName rejects a name that cannot be an operation name.
func validateOperationName(name string) error {
	if len(name) > MaxOperationNameBytes {
		return fmt.Errorf("%w: the name is longer than %d bytes", ErrInvalidOperation, MaxOperationNameBytes)
	}

	if _, pathErr := OperationScope(name); pathErr != nil {
		return fmt.Errorf("%w: %w", ErrInvalidOperation, pathErr)
	}

	return nil
}

// registerOperation records op under name.
func (h *Host) registerOperation(name string, op hostOperation) error {
	if nameErr := validateOperationName(name); nameErr != nil {
		return nameErr
	}

	if !h.enter() {
		return ErrHostClosed
	}
	defer h.leave()

	if !h.operations.add(name, op) {
		return fmt.Errorf("%w: %q", ErrOperationRegistered, name)
	}

	return nil
}

// RegisterReadOperation registers handler as the operation called name. Like
// resources, operations are only held in memory, so they are registered again
// after every restart; the scopes that authorize them are in the graph and
// persist. Nobody may run it until its entry scope ("run/<name>", see
// OperationScope, GrantOperation) allows them.
func (h *Host) RegisterReadOperation(name string, handler ReadOperation) error {
	if handler == nil {
		return fmt.Errorf("%w: %q has no handler", ErrInvalidOperation, name)
	}

	return h.registerOperation(name, hostOperation{read: handler})
}

// RegisterWriteOperation is RegisterReadOperation for a handler that writes.
func (h *Host) RegisterWriteOperation(name string, handler WriteOperation) error {
	if handler == nil {
		return fmt.Errorf("%w: %q has no handler", ErrInvalidOperation, name)
	}

	return h.registerOperation(name, hostOperation{write: handler})
}

// validateOperationArgs checks the arguments of a run against the limits. It
// does not depend on which operation is asked for, so it reveals nothing about
// which exist, and its messages never echo what the client sent.
func validateOperationArgs(args OperationArgs) error {
	if len(args) > MaxOperationArgs {
		return OperationErrorf(OperationCodeInvalidArguments, "at most %d arguments are accepted", MaxOperationArgs)
	}

	for name, value := range args {
		switch {
		case name == "":
			return OperationErrorf(OperationCodeInvalidArguments, "an argument has an empty name")
		case len(name) > MaxOperationNameBytes:
			return OperationErrorf(OperationCodeInvalidArguments, "an argument name is longer than %d bytes", MaxOperationNameBytes)
		case len(value) > MaxOperationValueBytes:
			return OperationErrorf(OperationCodeInvalidArguments, "an argument value is longer than %d bytes", MaxOperationValueBytes)
		case !utf8.ValidString(name) || !utf8.ValidString(value):
			return OperationErrorf(OperationCodeInvalidArguments, "arguments must be valid UTF-8")
		}
	}

	return nil
}

// validateOperationResult checks what a handler returned against the limits.
func validateOperationResult(result OperationResult) error {
	if len(result.Rows) > MaxOperationRows {
		return fmt.Errorf("%w: %d rows, limit %d", ErrOperationResult, len(result.Rows), MaxOperationRows)
	}

	if !utf8.ValidString(result.Cursor) {
		return fmt.Errorf("%w: the cursor is not valid UTF-8", ErrOperationResult)
	}

	size := len(result.Cursor)

	for _, row := range result.Rows {
		if len(row) > MaxOperationFields {
			return fmt.Errorf("%w: a row has %d fields, limit %d", ErrOperationResult, len(row), MaxOperationFields)
		}

		size += operationRowOverhead

		for name, value := range row {
			if name == "" || len(name) > MaxOperationNameBytes || !utf8.ValidString(name) || !utf8.ValidString(value) {
				return fmt.Errorf("%w: a field name is empty or too long, or a name or value is not valid UTF-8", ErrOperationResult)
			}

			size += len(name) + len(value) + operationFieldOverhead
		}
	}

	if size > MaxOperationResultBytes {
		return fmt.Errorf("%w: about %d bytes, limit %d", ErrOperationResult, size, MaxOperationResultBytes)
	}

	return nil
}

// executeOperation runs one handler call for opCtx: it checks the entry scope,
// calls the handler, checks its result, and turns a panic into an error (so the
// transaction or view it runs in is left cleanly and rolled back, and the
// wire server is never taken down by a handler).
func executeOperation(opCtx *OperationContext, call func() (OperationResult, error)) (result OperationResult, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = OperationResult{}
			err = fmt.Errorf("operation %q panicked: %v", opCtx.operation, recovered)
		}
	}()

	if entryErr := opCtx.requireEntry(); entryErr != nil {
		return OperationResult{}, entryErr
	}

	handlerResult, handlerErr := call()
	if handlerErr != nil {
		return OperationResult{}, wrapInterfaceErr(handlerErr)
	}

	if resultErr := validateOperationResult(handlerResult); resultErr != nil {
		return OperationResult{}, resultErr
	}

	return handlerResult, nil
}

// runRead runs a read handler in one read view.
func (h *Host) runRead(principal Principal, name string, handler ReadOperation, args OperationArgs) (OperationResult, error) {
	var result OperationResult

	viewErr := readView(h.Graph(), func(reader GraphReader) error {
		opCtx := &OperationContext{host: h, reader: reader, principal: principal, operation: name}

		var execErr error

		result, execErr = executeOperation(opCtx, func() (OperationResult, error) {
			return handler(opCtx, reader, args)
		})

		return execErr
	})
	if viewErr != nil {
		return OperationResult{}, viewErr
	}

	return result, nil
}

// runWrite runs a write handler in one transaction on the guarded graph.
func (h *Host) runWrite(principal Principal, name string, handler WriteOperation, args OperationArgs) (OperationResult, error) {
	return transactValue(h.Graph(), func(tx Tx) (OperationResult, error) {
		opCtx := &OperationContext{host: h, reader: tx, principal: principal, operation: name}

		return executeOperation(opCtx, func() (OperationResult, error) {
			return handler(opCtx, tx, args)
		})
	})
}

// cleanOperationError returns a copy of a handler's error that is safe to send
// to a client, and false if it is too large or not valid text.
func cleanOperationError(opErr *OperationError) (*OperationError, bool) {
	code := opErr.Code
	if code == "" {
		code = operationCodeFailed
	}

	if len(code)+len(opErr.Message) > MaxOperationValueBytes || !utf8.ValidString(code) || !utf8.ValidString(opErr.Message) {
		return nil, false
	}

	return &OperationError{Code: code, Message: opErr.Message}, true
}

// operationClientError decides what the client sees of err, which ended a run
// of the operation called name: a closing host, a denial (the same answer
// whatever the reason), an error the handler chose to show, or a generic
// failure with the cause reported to the host's error hook.
func (h *Host) operationClientError(name string, err error) error {
	var (
		fault *entryFault
		opErr *OperationError
	)

	switch {
	case errors.Is(err, ErrHostClosed):
		return ErrHostClosed
	case errors.As(err, &fault):
		h.report("operation "+name+": checking its scope", fault.cause)

		return operationDenied()
	case errors.Is(err, ErrNotAuthorized):
		return operationDenied()
	case errors.As(err, &opErr):
		if clean, ok := cleanOperationError(opErr); ok {
			return clean
		}

		h.report("operation "+name, fmt.Errorf("%w: the error it returned is too large or not valid text", ErrOperationResult))

		return internalOperationError()
	default:
		h.report("operation "+name, err)

		return internalOperationError()
	}
}

// RunOperation runs the operation called name for principal with args and
// returns its result. It is what the wire server calls, and an in-process
// caller can use it directly (so tests need no sockets).
//
// The arguments are checked against the limits first. Then the operation is
// looked up; an unknown one and one the principal may not run answer
// identically (an error wrapping ErrNotAuthorized). The entry scope and the
// handler share one read view or one transaction. The errors are:
//   - ErrNotAuthorized: unknown or forbidden (including a handler's own
//     denial);
//   - *OperationError: an error the handler chose to show, the invalid
//     arguments, or OperationCodeInternal for any failure the host keeps to
//     itself (it reaches HostConfig.OnError);
//   - ErrHostClosed, or the context's error if ctx had ended before the run.
//
// ctx is checked only before the run starts: a handler is one short read view
// or transaction, and is not interrupted.
func (h *Host) RunOperation(ctx context.Context, principal Principal, name string, args OperationArgs) (OperationResult, error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return OperationResult{}, fmt.Errorf("operation: %w", ctxErr)
	}

	if argsErr := validateOperationArgs(args); argsErr != nil {
		return OperationResult{}, argsErr
	}

	op, found := h.operations.lookup(name)
	if !found {
		return OperationResult{}, operationDenied()
	}

	var (
		result  OperationResult
		runErr  error
		isWrite = op.write != nil
	)

	if isWrite {
		result, runErr = h.runWrite(principal, name, op.write, args)
	} else {
		result, runErr = h.runRead(principal, name, op.read, args)
	}

	if runErr != nil {
		return OperationResult{}, h.operationClientError(name, runErr)
	}

	return result, nil
}

// scopeEdit is one of ScopeRegistry's writers (Allow, Deny, Revoke).
type scopeEdit func(s *ScopeRegistry, graph Transactor, path, principal string) (bool, error)

// editScope applies edit to the scope at path for principal, in one
// transaction on the host's graph, and reports whether anything changed.
func (h *Host) editScope(action, path string, principal Principal, edit scopeEdit) (bool, error) {
	changed, editErr := edit(h.reg.Scopes, h.Graph(), path, string(principal))
	if editErr != nil {
		return false, fmt.Errorf("%s scope %q for %q: %w", action, path, principal, editErr)
	}

	return changed, nil
}

// editOperationScope is editScope for the entry scope of operation.
func (h *Host) editOperationScope(action, operation string, principal Principal, edit scopeEdit) (bool, error) {
	path, pathErr := OperationScope(operation)
	if pathErr != nil {
		return false, pathErr
	}

	return h.editScope(action, path, principal, edit)
}

// GrantOperation allows principal (AnyPrincipal for everyone) to run
// operation, and reports whether that is new. A principal that is also denied
// stays denied: deny beats allow at the same level. The owner's own helper:
// nothing a client can do reaches it, except a plan from a principal trusted
// with the whole graph (see WithPlans).
func (h *Host) GrantOperation(operation string, principal Principal) (bool, error) {
	return h.editOperationScope("granting", operation, principal, (*ScopeRegistry).Allow)
}

// DenyOperation denies principal (AnyPrincipal for everyone) the operation,
// and reports whether that is new.
func (h *Host) DenyOperation(operation string, principal Principal) (bool, error) {
	return h.editOperationScope("denying", operation, principal, (*ScopeRegistry).Deny)
}

// RevokeOperation removes principal from both the allow and the deny set of
// the operation's entry scope, and reports whether it was in either.
func (h *Host) RevokeOperation(operation string, principal Principal) (bool, error) {
	return h.editOperationScope("revoking", operation, principal, (*ScopeRegistry).Revoke)
}

// GrantScope allows principal to use the scope path (for the data a handler
// guards, such as "read/settings"), and reports whether that is new.
func (h *Host) GrantScope(path string, principal Principal) (bool, error) {
	return h.editScope("granting", path, principal, (*ScopeRegistry).Allow)
}

// DenyScope denies principal the scope path, and reports whether that is new.
func (h *Host) DenyScope(path string, principal Principal) (bool, error) {
	return h.editScope("denying", path, principal, (*ScopeRegistry).Deny)
}

// RevokeScope removes principal from both the allow and the deny set of the
// scope path, and reports whether it was in either.
func (h *Host) RevokeScope(path string, principal Principal) (bool, error) {
	return h.editScope("revoking", path, principal, (*ScopeRegistry).Revoke)
}