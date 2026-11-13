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
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"slices"
	"sync"
	"time"
)

// The wire transport of the Client interface (theorystate.md section 113).
//
// Framing: a 4-byte big-endian length followed by that many bytes of JSON.
// The first frame of a connection is a hello (protocol version); the reply
// carries the host's keepalive TTL. After that every request carries a
// numeric ID and is answered by one response with the same ID, in any order,
// so a blocked WaitApplied never stalls other requests on the connection. A
// cancel frame (no response) stops one in-flight request; a close frame ends
// the session and is answered once the session's holds are released.
//
// One connection is one session (theorystate.md section 111): when the
// connection ends, however it ends, the host releases the session. There is
// no resume.
//
// Everything here works over any net.Conn except plain, unencrypted TCP, which
// the server refuses unless told otherwise (WithInsecurePlainTCP; see
// isPlainTCP). Mutual TLS is in wire_tls.go and the Windows named pipe glue is
// in pipe_windows.go.

var (
	// ErrWireProtocol is returned for a frame or request the peer should not
	// have sent: malformed JSON, a first frame that is not a hello, an
	// unsupported version, an unknown operation, a duplicate request ID.
	ErrWireProtocol = errors.New("wire protocol violation")

	// ErrWireFrameTooLarge is returned for a frame over wireMaxFrame.
	ErrWireFrameTooLarge = errors.New("wire frame is too large")

	// ErrWireBusy is returned when a connection has wireMaxInFlight requests
	// in flight already.
	ErrWireBusy = errors.New("too many requests in flight on this connection")

	// ErrWireServerClosed is returned by Serve when the server is closed.
	ErrWireServerClosed = errors.New("wire server is closed")

	// ErrWireTooManyConns is returned to a client whose connection the
	// server refused because it already serves its limit (WithMaxConns).
	ErrWireTooManyConns = errors.New("the server has too many connections")

	// ErrWirePlainTCP is returned to a client whose connection the server
	// refused because it is plain, unencrypted TCP, which has no identity and
	// can be reached by any local process (see WithInsecurePlainTCP).
	ErrWirePlainTCP = errors.New("plain TCP connections are not accepted; use TLS (ListenTLS) or a named pipe")

	// ErrRemote is the sentinel of a RemoteError whose code this build does
	// not know: a newer host may add codes without breaking older clients.
	ErrRemote = errors.New("remote error")
)

// wireHelloTimeout is how long a connection has to deliver its hello. A TLS
// connection gets the same allowance for its TLS handshake first (see
// completeTLS). A var, not a const, so tests can shrink it.
var wireHelloTimeout = 10 * time.Second

// wireFrameTimeout is how long the rest of a frame has to arrive once its
// length header has (an idle connection between frames is not limited by it),
// so that a half-sent frame cannot hold a goroutine and a buffer. A var, not a
// const, so tests can shrink it.
var wireFrameTimeout = 5 * time.Second

// Serve survives failing Accept calls (running out of file descriptors, a
// connection reset before it was accepted): it waits, starting at
// wireAcceptBackoffStart and doubling up to wireAcceptBackoffMax, and tries
// again, giving up only after wireAcceptMaxFailures failures in a row. The
// first two are vars, not consts, so tests can shrink them.
var (
	wireAcceptBackoffStart = 5 * time.Millisecond
	wireAcceptMaxFailures  = 50
)

const wireAcceptBackoffMax = time.Second

const (
	wireVersion      = 1
	wireHeaderSize   = 4
	wireMaxFrame     = 1 << 20
	wireMaxInFlight  = 256
	wireWriteTimeout = 10 * time.Second
	wireCloseTimeout = 10 * time.Second
	wireCancelGrace  = time.Second

	// wireRefuseTimeout bounds how long the server spends telling a refused
	// connection why, so refused connections cannot pile up goroutines.
	wireRefuseTimeout = 2 * time.Second

	// wireDefaultMaxConns is the connection limit of a server given no
	// WithMaxConns. Every admitted connection opens a session, which is a
	// durable commit.
	wireDefaultMaxConns = 256

	// wireHeartbeatsPerTTL is how many keepalives a client sends per host
	// TTL, so one lost or late heartbeat does not expire the session.
	wireHeartbeatsPerTTL = 3
)

const (
	wireOpHello       = "hello"
	wireOpAcquire     = "acquire"
	wireOpRelease     = "release"
	wireOpWaitApplied = "wait_applied"
	wireOpKeepalive   = "keepalive"
	wireOpCancel      = "cancel"
	wireOpClose       = "close"
	wireOpPlan        = "plan"
	wireOpRun         = "run"
)

// wireRequest is one client-to-host frame.
type wireRequest struct {
	ID        uint64        `json:"id"`
	Op        string        `json:"op"`
	Version   int           `json:"version,omitempty"`
	Resource  string        `json:"resource,omitempty"`
	Mark      uint64        `json:"mark,omitempty"`
	Target    uint64        `json:"target,omitempty"`
	Plan      *Plan         `json:"plan,omitempty"`
	Operation string        `json:"operation,omitempty"`
	Args      OperationArgs `json:"args,omitempty"`
}

// wireResponse is one host-to-client frame.
type wireResponse struct {
	ID                 uint64         `json:"id"`
	Error              *wireError     `json:"error,omitempty"`
	First              bool           `json:"first,omitempty"`
	Last               bool           `json:"last,omitempty"`
	Mark               uint64         `json:"mark,omitempty"`
	KeepaliveTTLMillis int64          `json:"keepalive_ttl_ms,omitempty"`
	PlanResult         *PlanResult    `json:"plan_result,omitempty"`
	Rows               []OperationRow `json:"rows,omitempty"`
	Cursor             string         `json:"cursor,omitempty"`
}

// wireError is an error as it crosses the wire: a stable code plus the
// host's message.
type wireError struct {
	Code    string `json:"code"`
	Message string `json:"message"`

	// OperationCode is the handler's own code, set only when Code is
	// wireCodeOperation.
	OperationCode string `json:"operation_code,omitempty"`
}

const (
	// wireCodeRemote is the code of an error that matches no table entry.
	wireCodeRemote = "remote"

	// wireCodeOperation is the code of an *OperationError: the error a
	// handler chose to show, or the host's generic failure. It is not in the
	// table because its meaning is in OperationCode.
	wireCodeOperation = "operation"
)

// wireErrorTable maps each sentinel a client can see to its stable code. The
// first matching entry wins on the host side; the client maps a code back to
// the same sentinel.
var wireErrorTable = []struct {
	code string
	err  error
}{
	{"not_authorized", ErrNotAuthorized},
	{"too_many_conns", ErrWireTooManyConns},
	{"plain_tcp_refused", ErrWirePlainTCP},
	{"host_closed", ErrHostClosed},
	{"conn_closed", ErrConnClosed},
	{"hold_lost", ErrHoldLost},
	{"unknown_resource", ErrUnknownResource},
	{"canceled", context.Canceled},
	{"deadline_exceeded", context.DeadlineExceeded},
	{"busy", ErrWireBusy},
	{"plan_invalid", ErrPlanInvalid},
	{"plan_conflict", ErrPlanConflict},
	{"protocol", ErrWireProtocol},
}

// wireErrorCode returns the code for err.
func wireErrorCode(err error) string {
	for _, entry := range wireErrorTable {
		if errors.Is(err, entry.err) {
			return entry.code
		}
	}

	return wireCodeRemote
}

// wireSentinel returns the sentinel for code, ErrRemote if unknown.
func wireSentinel(code string) error {
	for _, entry := range wireErrorTable {
		if entry.code == code {
			return entry.err
		}
	}

	return ErrRemote
}

// wireErrorResponse is the response carrying err.
func wireErrorResponse(err error) wireResponse {
	var opErr *OperationError
	if errors.As(err, &opErr) {
		return wireResponse{Error: &wireError{Code: wireCodeOperation, Message: opErr.Message, OperationCode: opErr.Code}}
	}

	return wireResponse{Error: &wireError{Code: wireErrorCode(err), Message: err.Error()}}
}

// RemoteError is an error the host reported over the wire. Its message is the
// host's, and errors.Is sees the sentinel its code maps to (ErrUnknownResource,
// ErrHoldLost, ...), or ErrRemote for a code this build does not know.
type RemoteError struct {
	Code    string
	Message string

	// OperationCode is the handler's own code when Code is "operation".
	// WireClient.RunOperation turns such an error into an *OperationError.
	OperationCode string

	sentinel error
}

// Error returns the host's message.
func (e *RemoteError) Error() string {
	return e.Message
}

// Unwrap returns the sentinel the code maps to.
func (e *RemoteError) Unwrap() error {
	return e.sentinel
}

// remoteError turns a wire error into a *RemoteError; nil stays nil.
func remoteError(werr *wireError) error {
	if werr == nil {
		return nil
	}

	return &RemoteError{Code: werr.Code, Message: werr.Message, OperationCode: werr.OperationCode, sentinel: wireSentinel(werr.Code)}
}

// closeQuietly closes c, ignoring the error: it is used where a close failure
// has nobody to tell and nothing to undo (a connection already dead).
func closeQuietly(c io.Closer) {
	if err := c.Close(); err != nil {
		_ = err
	}
}

// writeWireFrame writes v as one frame, in a single Write so concurrent
// writers holding a lock never interleave partial frames.
func writeWireFrame(w io.Writer, v any) error {
	payload, marshalErr := json.Marshal(v)
	if marshalErr != nil {
		return fmt.Errorf("wire: encoding a frame: %w", marshalErr)
	}

	if len(payload) > wireMaxFrame {
		return fmt.Errorf("%w: %d bytes to send, limit %d", ErrWireFrameTooLarge, len(payload), wireMaxFrame)
	}

	frame := binary.BigEndian.AppendUint32(make([]byte, 0, wireHeaderSize+len(payload)), uint32(len(payload))) //nolint:gosec // bounded by wireMaxFrame above
	frame = append(frame, payload...)

	if _, writeErr := w.Write(frame); writeErr != nil {
		return fmt.Errorf("wire: writing a frame: %w", writeErr)
	}

	return nil
}

// writeWireFrameWithin is writeWireFrame with a watchdog: if the write has
// not finished within limit the connection is closed, which unblocks it. A
// peer that stops reading therefore cannot hold a writer forever.
func writeWireFrameWithin(nc net.Conn, v any, limit time.Duration) error {
	watchdog := time.AfterFunc(limit, func() { closeQuietly(nc) })
	writeErr := writeWireFrame(nc, v)
	watchdog.Stop()

	return writeErr
}

// readWireHeader reads the length header of a frame and returns the size it
// announces, which is checked against wireMaxFrame. io.EOF (wrapped) means the
// peer closed cleanly between frames.
func readWireHeader(r io.Reader) (uint32, error) {
	var header [wireHeaderSize]byte

	if _, readErr := io.ReadFull(r, header[:]); readErr != nil {
		return 0, fmt.Errorf("wire: reading a frame header: %w", readErr)
	}

	size := binary.BigEndian.Uint32(header[:])
	if size > wireMaxFrame {
		return 0, fmt.Errorf("%w: %d bytes announced, limit %d", ErrWireFrameTooLarge, size, wireMaxFrame)
	}

	return size, nil
}

// readWireBody reads the size bytes of a frame's payload and decodes them
// into v.
func readWireBody(r io.Reader, size uint32, v any) error {
	// The payload buffer grows as bytes arrive, so a peer that announces a
	// large frame and then sends nothing costs the server almost nothing
	// (allocating the announced size up front would add up over many
	// connections).
	var payload bytes.Buffer

	if _, copyErr := io.CopyN(&payload, r, int64(size)); copyErr != nil {
		if errors.Is(copyErr, io.EOF) {
			// CopyN reports io.EOF only for a payload shorter than announced.
			copyErr = io.ErrUnexpectedEOF
		}

		return fmt.Errorf("wire: reading a frame payload: %w", copyErr)
	}

	if decodeErr := json.Unmarshal(payload.Bytes(), v); decodeErr != nil {
		return fmt.Errorf("%w: decoding a frame: %w", ErrWireProtocol, decodeErr)
	}

	return nil
}

// readWireFrame reads one frame into v. io.EOF (wrapped) means the peer
// closed cleanly between frames.
func readWireFrame(r io.Reader, v any) error {
	size, headerErr := readWireHeader(r)
	if headerErr != nil {
		return headerErr
	}

	return readWireBody(r, size, v)
}

// readWireFrameBounded is readWireFrame for a server connection: an idle
// connection between frames is not limited, but once a frame's header has
// arrived its payload must follow within wireFrameTimeout, so a half-sent frame
// cannot hold a goroutine and a buffer. A payload that is late is a protocol
// violation. The deadline is cleared once the frame is in.
func readWireFrameBounded(nc net.Conn, r io.Reader, v any) error {
	size, headerErr := readWireHeader(r)
	if headerErr != nil {
		return headerErr
	}

	if deadlineErr := nc.SetReadDeadline(time.Now().Add(wireFrameTimeout)); deadlineErr != nil {
		return fmt.Errorf("wire: setting the frame deadline: %w", deadlineErr)
	}

	bodyErr := readWireBody(r, size, v)

	switch {
	case isTimeout(bodyErr):
		return fmt.Errorf("%w: a frame of %d bytes was not complete within %v", ErrWireProtocol, size, wireFrameTimeout)
	case bodyErr != nil:
		return bodyErr
	}

	if clearErr := nc.SetReadDeadline(time.Time{}); clearErr != nil {
		return fmt.Errorf("wire: clearing the frame deadline: %w", clearErr)
	}

	return nil
}

// ---------------------------------------------------------------------
// Server

// WireServer serves a Host's Client operations over any net.Listener or
// net.Conn. Close it before closing the Host (closing the Host first also
// works: the host ends every connection, and the server then closes the
// transport under it).
type WireServer struct {
	host *Host

	// Set by NewWireServer and its options, never changed afterwards.
	maxConns      int // 0: unlimited
	authorizer    Authorizer
	identifier    PeerIdentifier
	allowPlainTCP bool
	planLimit     int // 0: plans are not accepted

	// operations is set by WithOperations: run requests are served.
	operations bool

	mu        sync.Mutex
	closed    bool
	listeners map[net.Listener]struct{}
	conns     map[net.Conn]struct{}
	admitted  int // connections counted against maxConns

	// live holds the connections that completed their handshake, so
	// Revalidate can reach them.
	live map[*wireServerConn]struct{}

	// stop is closed by Close, to wake a Serve that is waiting to retry.
	stop chan struct{}

	wg sync.WaitGroup
}

// WireOption configures a WireServer.
type WireOption func(s *WireServer)

// WithMaxConns limits how many connections the server serves at once. Zero
// keeps the default (wireDefaultMaxConns) and a negative n means unlimited.
// A connection over the limit is told why and closed without a session.
func WithMaxConns(n int) WireOption {
	return func(s *WireServer) {
		switch {
		case n > 0:
			s.maxConns = n
		case n < 0:
			s.maxConns = 0
		}
	}
}

// WithAuthorizer makes the server ask a before every acquire, release and
// wait_applied. Without one every connection may use every resource. Peers
// are named by WithPeerIdentifier; without one they are all the empty
// Principal.
//
// One exception: whatever a says, a principal may release a hold its own
// session has (refusing would only keep the effect alive until the
// connection ends). A release of anything else is asked like the rest. Holds
// a changed policy no longer allows are dropped by WireServer.Revalidate.
func WithAuthorizer(a Authorizer) WireOption {
	return func(s *WireServer) { s.authorizer = a }
}

// WithPeerIdentifier sets how the server names the peer of a connection (for
// a Windows named pipe, PipePeerPrincipal).
func WithPeerIdentifier(identify PeerIdentifier) WireOption {
	return func(s *WireServer) { s.identifier = identify }
}

// WithInsecurePlainTCP lets the server serve plain, unencrypted TCP
// connections, which it refuses by default (ErrWirePlainTCP): they carry no
// peer identity, so every peer is the empty Principal, and any local process
// can connect. It exists for a TLS-terminating proxy running next to the
// host, and for nothing else; use a named pipe or ListenTLS otherwise.
func WithInsecurePlainTCP() WireOption {
	return func(s *WireServer) { s.allowPlainTCP = true }
}

// WithPlans makes the server accept plans (see Plan): a client sends a
// complete list of steps and the host runs it as one transaction. A plan may
// have at most maxSteps steps (zero or less means DefaultPlanMaxSteps).
//
// Plans are off unless this is given, and they also need an Authorizer
// (WithAuthorizer): without one every plan is refused. The authorizer is asked
// AuthRequest{OpPlan, PlanResource}, so a policy lists the principals that may
// submit plans under the name PlanResource. Such a principal is trusted with
// the whole graph, not with one resource: a plan can add a session to any
// holder Set, which per-resource authorization would not allow, so only
// principals trusted that far should be listed.
func WithPlans(maxSteps int) WireOption {
	return func(s *WireServer) {
		if maxSteps <= 0 {
			maxSteps = DefaultPlanMaxSteps
		}

		s.planLimit = maxSteps
	}
}

// WithOperations makes the server serve run requests: a client names an
// operation the host owner registered (Host.RegisterReadOperation,
// RegisterWriteOperation) and gives named text arguments, and gets rows of named
// text back (see Host.RunOperation).
//
// Operations are off unless this is given. Unlike resources and plans they are
// not authorized by an Authorizer: what a principal may run is kept in the graph
// (Host.GrantOperation and the scope helpers), default deny, checked in the same
// view or transaction as the handler. The server still needs a PeerIdentifier
// to name the peer; an unidentified peer is the empty principal, which is
// always denied. An unknown operation and a forbidden one answer identically.
func WithOperations() WireOption {
	return func(s *WireServer) { s.operations = true }
}

// NewWireServer returns a server for host.
func NewWireServer(host *Host, opts ...WireOption) *WireServer {
	s := &WireServer{
		host:      host,
		maxConns:  wireDefaultMaxConns,
		listeners: make(map[net.Listener]struct{}),
		conns:     make(map[net.Conn]struct{}),
		live:      make(map[*wireServerConn]struct{}),
		stop:      make(chan struct{}),
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// track runs add under the server lock unless the server is closed, and
// reports whether it ran. Registering a connection (and its wg.Add) happens
// only here, so nothing is added once Close has begun waiting.
func (s *WireServer) track(add func()) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return false
	}

	add()

	return true
}

// untrack runs remove under the server lock.
func (s *WireServer) untrack(remove func()) {
	s.mu.Lock()
	defer s.mu.Unlock()

	remove()
}

func (s *WireServer) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.closed
}

// acceptFailure is the result of Serve when Accept failed: a clean nil if the
// server itself was closed, the failure otherwise.
func (s *WireServer) acceptFailure(acceptErr error) error {
	if s.isClosed() {
		return nil
	}

	return fmt.Errorf("wire: accepting a connection: %w", acceptErr)
}

// Serve accepts connections from l, one goroutine each, until l is closed or
// the server is closed (then it returns nil), or Accept keeps failing. A
// failing Accept (out of file descriptors, say) is reported to the host's
// error hook and retried after a growing pause; only wireAcceptMaxFailures
// failures in a row end Serve with an error, so a passing resource shortage
// does not leave a host that is up but accepting nobody. It takes ownership of
// l: Close closes it.
func (s *WireServer) Serve(l net.Listener) error {
	if !s.track(func() { s.listeners[l] = struct{}{} }) {
		closeQuietly(l)

		return ErrWireServerClosed
	}

	defer s.untrack(func() { delete(s.listeners, l) })

	failures := 0
	backoff := wireAcceptBackoffStart

	for {
		nc, acceptErr := l.Accept()
		if acceptErr == nil {
			failures = 0
			backoff = wireAcceptBackoffStart

			go s.ServeConn(nc)

			continue
		}

		// A closed listener is not coming back, whoever closed it.
		if errors.Is(acceptErr, net.ErrClosed) || s.isClosed() {
			return s.acceptFailure(acceptErr)
		}

		failures++

		if failures >= wireAcceptMaxFailures {
			return fmt.Errorf("wire: accepting a connection: giving up after %d failures in a row: %w", failures, acceptErr)
		}

		s.host.report("wire: accepting a connection", acceptErr)

		// The server was closed while waiting: a clean end, like above.
		if !s.pause(backoff) {
			return s.acceptFailure(acceptErr)
		}

		backoff = min(backoff*2, wireAcceptBackoffMax)
	}
}

// pause waits for d and reports false if the server was closed meanwhile.
func (s *WireServer) pause(d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-s.stop:
		return false
	case <-timer.C:
		return true
	}
}

// Close stops accepting, closes every connection and waits for the handlers.
// Every session that was open is released by then.
func (s *WireServer) Close() error {
	s.mu.Lock()

	if !s.closed {
		s.closed = true
		close(s.stop)
	}
	listeners := slices.Collect(maps.Keys(s.listeners))
	conns := slices.Collect(maps.Keys(s.conns))
	s.mu.Unlock()

	errs := make([]error, 0, len(listeners))

	for _, l := range listeners {
		errs = append(errs, l.Close())
	}

	for _, nc := range conns {
		closeQuietly(nc)
	}

	s.wg.Wait()

	return joinErrors(errs...)
}

// reportMisbehavior reports a connection dropped for breaking the protocol.
// An ordinary disconnect is not reported.
func (s *WireServer) reportMisbehavior(err error) {
	if errors.Is(err, ErrWireProtocol) || errors.Is(err, ErrWireFrameTooLarge) {
		s.host.report("wire: dropping a misbehaving connection", err)
	}
}

// register tracks nc unless the server is closed (registered), and reports
// whether it is within the connection limit (admitted). Every registered
// connection must be passed to unregister.
func (s *WireServer) register(nc net.Conn) (registered, admitted bool) {
	registered = s.track(func() {
		s.conns[nc] = struct{}{}
		s.wg.Add(1)

		if s.maxConns <= 0 || s.admitted < s.maxConns {
			s.admitted++
			admitted = true
		}
	})

	return registered, admitted
}

// unregister undoes register.
func (s *WireServer) unregister(nc net.Conn, admitted bool) {
	s.untrack(func() {
		delete(s.conns, nc)

		if admitted {
			s.admitted--
		}
	})
}

// addLive records sc as past its handshake, so Revalidate can reach it.
func (s *WireServer) addLive(sc *wireServerConn) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.live[sc] = struct{}{}
}

// removeLive undoes addLive.
func (s *WireServer) removeLive(sc *wireServerConn) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.live, sc)
}

// Revalidate releases every hold of every live connection whose principal the
// authorizer no longer allows to acquire that resource (an authorizer that
// fails counts as saying no), and returns how many it released. Call it after
// replacing the policy (MutablePolicy.Replace). The client is not told: its
// next call for that resource is refused (ErrNotAuthorized), and a
// WaitApplied already in flight ends with ErrHoldLost. A request that began
// before the policy changed has finished by the time a connection is
// scanned, so no hold it took is missed. Without an authorizer there is
// nothing to revalidate.
func (s *WireServer) Revalidate(ctx context.Context) (int, error) {
	s.mu.Lock()
	live := slices.Collect(maps.Keys(s.live))
	s.mu.Unlock()

	total := 0
	errs := make([]error, 0, len(live))

	for _, sc := range live {
		released, revalidateErr := sc.revalidate(ctx)
		total += released

		// A connection that ended meanwhile released its holds by ending.
		if revalidateErr != nil && !errors.Is(revalidateErr, ErrConnClosed) {
			errs = append(errs, revalidateErr)
		}
	}

	return total, joinErrors(errs...)
}

// refuse answers the first request of a connection the server will not serve
// with cause, so the client's handshake fails with the reason. It never
// opens a session, and a peer that is slow to talk or to listen is cut off
// after wireRefuseTimeout.
func (s *WireServer) refuse(nc net.Conn, cause error) {
	watchdog := time.AfterFunc(wireRefuseTimeout, func() { closeQuietly(nc) })
	defer watchdog.Stop()

	var req wireRequest

	if readErr := readWireFrame(bufio.NewReader(nc), &req); readErr != nil {
		return
	}

	resp := wireErrorResponse(cause)
	resp.ID = req.ID

	if writeErr := writeWireFrame(nc, &resp); writeErr != nil {
		_ = writeErr // the peer is gone; there is nobody to tell
	}
}

// identify names the peer of nc. A peer that cannot be identified is the
// empty Principal, which an Authorizer must not treat as trusted.
func (s *WireServer) identify(nc net.Conn) Principal {
	if s.identifier == nil {
		return ""
	}

	principal, err := s.identifier(nc)
	if err != nil {
		s.host.report("wire: identifying a peer", err)

		return ""
	}

	return principal
}

// refusal returns why the server will not serve nc, or nil if it will:
// plain TCP unless WithInsecurePlainTCP was given, or a connection over the
// limit (admitted is false).
func (s *WireServer) refusal(nc net.Conn, admitted bool) error {
	switch {
	case !s.allowPlainTCP && isPlainTCP(nc):
		return ErrWirePlainTCP
	case !admitted:
		return fmt.Errorf("%w: the server already serves its limit of %d connections", ErrWireTooManyConns, s.maxConns)
	default:
		return nil
	}
}

// ServeConn serves one connection until it ends, then releases its session.
// It blocks, and takes ownership of nc. A connection the server will not
// serve is refused (see refusal): one over the limit (WithMaxConns) or plain
// TCP (WithInsecurePlainTCP). A TLS connection first completes its TLS
// handshake (see completeTLS); a failed one is reported (ErrTLSHandshake).
func (s *WireServer) ServeConn(nc net.Conn) {
	registered, admitted := s.register(nc)
	if !registered {
		closeQuietly(nc)

		return
	}

	defer s.wg.Done()
	defer s.unregister(nc, admitted)
	defer closeQuietly(nc)

	if cause := s.refusal(nc, admitted); cause != nil {
		s.refuse(nc, cause)

		return
	}

	// Done here, and not implicitly inside the hello read, so that a failure
	// is known to be a TLS failure and can be reported with its cause.
	if tlsErr := completeTLS(nc); tlsErr != nil {
		s.reportTLSFailure(tlsErr)

		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sc := newWireServerConn(s.host, nc)

	conn, handshakeErr := sc.handshake(ctx)
	if handshakeErr != nil {
		s.reportMisbehavior(handshakeErr)

		return
	}

	sc.conn = conn
	sc.authorizer = s.authorizer
	sc.principal = s.identify(nc)
	sc.planLimit = s.planLimit
	sc.operations = s.operations

	s.addLive(sc)
	defer s.removeLive(sc)

	// A connection the host ends (keepalive expiry, shutdown) must also end
	// the transport, so the client finds out.
	sc.wg.Go(func() {
		select {
		case <-conn.Done():
			closeQuietly(nc)
		case <-ctx.Done():
		}
	})

	loopErr := sc.serve(ctx)

	cancel()
	s.host.report("wire: ending a connection", conn.Close())
	sc.wg.Wait()
	s.reportMisbehavior(loopErr)
}

// wireServerConn is the server side of one connection.
type wireServerConn struct {
	host   *Host
	nc     net.Conn
	reader *bufio.Reader
	conn   *Conn // set after the handshake, before anything else reads it

	// authorizer and principal decide who may use which resource. Both are
	// set after the handshake, before the read loop starts. A nil authorizer
	// allows everything.
	authorizer Authorizer
	principal  Principal

	// planLimit is the most steps a plan may have, set from the server after
	// the handshake like authorizer; 0 means plans are not accepted.
	planLimit int

	// operations says whether run requests are served (WithOperations), set
	// like planLimit.
	operations bool

	// grantMu makes "authorize an acquire and take the hold" one step with
	// respect to Revalidate: acquires hold it for reading, Revalidate for
	// writing. A policy replaced before Revalidate starts is therefore seen
	// by every acquire that begins after it, and every acquire that began
	// before has finished (its hold is listed) by the time Revalidate scans.
	grantMu sync.RWMutex

	writeMu sync.Mutex

	mu       sync.Mutex
	inflight map[uint64]context.CancelFunc
	slots    chan struct{}

	wg sync.WaitGroup
}

func newWireServerConn(host *Host, nc net.Conn) *wireServerConn {
	return &wireServerConn{
		host:     host,
		nc:       nc,
		reader:   bufio.NewReader(nc),
		inflight: make(map[uint64]context.CancelFunc),
		slots:    make(chan struct{}, wireMaxInFlight),
	}
}

// respond writes the response to request id. A failed write means the
// connection is dead, so it is closed, which ends the read loop.
func (sc *wireServerConn) respond(id uint64, resp wireResponse) {
	resp.ID = id

	sc.writeMu.Lock()
	defer sc.writeMu.Unlock()

	if writeErr := writeWireFrameWithin(sc.nc, &resp, wireWriteTimeout); writeErr != nil {
		closeQuietly(sc.nc)
	}
}

// isTimeout reports whether err is, or wraps, a timeout reported by a
// connection (an expired deadline), whatever the transport.
func isTimeout(err error) bool {
	var timeoutErr net.Error

	return errors.As(err, &timeoutErr) && timeoutErr.Timeout()
}

// readHello reads the first frame, which must arrive within wireHelloTimeout.
// The limit is a read deadline, not a timer that closes the connection, so a
// peer that sends nothing gets a recognizable timeout: it is told (a protocol
// error frame) and the error is reported like any other protocol violation.
// The deadline is cleared once the frame is in.
func (sc *wireServerConn) readHello() (wireRequest, error) {
	var hello wireRequest

	if deadlineErr := sc.nc.SetReadDeadline(time.Now().Add(wireHelloTimeout)); deadlineErr != nil {
		return hello, fmt.Errorf("wire: setting the hello deadline: %w", deadlineErr)
	}

	readErr := readWireFrame(sc.reader, &hello)

	switch {
	case isTimeout(readErr):
		timeoutErr := fmt.Errorf("%w: no hello within %v", ErrWireProtocol, wireHelloTimeout)
		sc.respond(0, wireErrorResponse(timeoutErr))

		return hello, timeoutErr
	case readErr != nil:
		return hello, readErr
	}

	if clearErr := sc.nc.SetReadDeadline(time.Time{}); clearErr != nil {
		return hello, fmt.Errorf("wire: clearing the hello deadline: %w", clearErr)
	}

	return hello, nil
}

// handshake reads the hello within wireHelloTimeout, opens the session and
// answers with the host's keepalive TTL.
func (sc *wireServerConn) handshake(ctx context.Context) (*Conn, error) {
	hello, readErr := sc.readHello()
	if readErr != nil {
		return nil, readErr
	}

	if hello.Op != wireOpHello || hello.Version != wireVersion {
		protocolErr := fmt.Errorf("%w: expected a hello for protocol version %d, got operation %q version %d", ErrWireProtocol, wireVersion, hello.Op, hello.Version)
		sc.respond(hello.ID, wireErrorResponse(protocolErr))

		return nil, protocolErr
	}

	conn, connectErr := sc.host.Connect(ctx)
	if connectErr != nil {
		// A host that is closing is just shutting down; any other failure
		// (the store could not commit the session) is the operator's to see,
		// and the client only learns a generic code.
		if !errors.Is(connectErr, ErrHostClosed) {
			sc.host.report("wire: opening a session", connectErr)
		}

		sc.respond(hello.ID, wireErrorResponse(connectErr))

		return nil, connectErr
	}

	sc.respond(hello.ID, wireResponse{KeepaliveTTLMillis: sc.host.cfg.KeepaliveTTL.Milliseconds()})

	return conn, nil
}

// serve is the read loop: it returns when the connection ends (the error says
// why) or a close request has been answered (nil).
func (sc *wireServerConn) serve(ctx context.Context) error {
	for {
		var req wireRequest

		if readErr := readWireFrameBounded(sc.nc, sc.reader, &req); readErr != nil {
			return readErr
		}

		if !sc.dispatch(ctx, req) {
			return nil
		}
	}
}

// dispatch handles one request and reports whether the read loop goes on.
func (sc *wireServerConn) dispatch(ctx context.Context, req wireRequest) bool {
	switch req.Op {
	case wireOpCancel:
		sc.cancelInflight(req.Target)

		return true
	case wireOpClose:
		resp := wireResponse{}
		if closeErr := sc.conn.Close(); closeErr != nil {
			resp = wireErrorResponse(closeErr)
		}

		sc.respond(req.ID, resp)

		return false
	}

	reqCtx, startErr := sc.startRequest(ctx, req.ID)
	if startErr != nil {
		sc.respond(req.ID, wireErrorResponse(startErr))

		return true
	}

	sc.wg.Go(func() {
		defer sc.finishRequest(req.ID)
		sc.respond(req.ID, sc.execute(reqCtx, req))
	})

	return true
}

// startRequest reserves an in-flight slot for request id and returns the
// context that a cancel frame or the end of the connection will cancel.
func (sc *wireServerConn) startRequest(ctx context.Context, id uint64) (context.Context, error) {
	select {
	case sc.slots <- struct{}{}:
	default:
		return nil, fmt.Errorf("%w: more than %d requests in flight", ErrWireBusy, wireMaxInFlight)
	}

	sc.mu.Lock()
	defer sc.mu.Unlock()

	if _, duplicate := sc.inflight[id]; duplicate {
		<-sc.slots

		return nil, fmt.Errorf("%w: request %d is already in flight", ErrWireProtocol, id)
	}

	reqCtx, reqCancel := context.WithCancel(ctx)
	sc.inflight[id] = reqCancel

	return reqCtx, nil
}

// finishRequest releases request id's slot and context.
func (sc *wireServerConn) finishRequest(id uint64) {
	sc.mu.Lock()
	cancel := sc.inflight[id]
	delete(sc.inflight, id)
	sc.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	<-sc.slots
}

// cancelInflight cancels request id if it is still in flight.
func (sc *wireServerConn) cancelInflight(id uint64) {
	sc.mu.Lock()
	cancel := sc.inflight[id]
	sc.mu.Unlock()

	if cancel != nil {
		cancel()
	}
}

// wireResourceOps are the operations that name a resource, and so need
// authorizing.
var wireResourceOps = []string{wireOpAcquire, wireOpRelease, wireOpWaitApplied}

// authorize reports whether the connection's principal may do request. It
// fails closed: an authorizer that fails for any reason other than saying no
// denies too.
func (sc *wireServerConn) authorize(request AuthRequest) error {
	if sc.authorizer == nil {
		return nil
	}

	authErr := sc.authorizer.Authorize(sc.principal, request)

	switch {
	case authErr == nil:
		return nil
	case errors.Is(authErr, ErrNotAuthorized):
		return wrapInterfaceErr(authErr)
	default:
		return fmt.Errorf("%w: authorizing %s of resource %q failed: %w", ErrNotAuthorized, request.Operation, request.Resource, authErr)
	}
}

// authorizeRequest is authorize for one wire request, with one exception: a
// principal may always let go of a hold its own session has, whatever the
// policy now says. Whether the session holds the resource is only asked after
// the policy said no, and any failure to find out leaves the refusal, so a
// resource that does not exist or is not held reveals nothing.
func (sc *wireServerConn) authorizeRequest(ctx context.Context, req wireRequest) error {
	authErr := sc.authorize(AuthRequest{Operation: WireOperation(req.Op), Resource: req.Resource})
	if authErr == nil {
		return nil
	}

	if req.Op == wireOpRelease {
		if holder, holdErr := sc.conn.holds(ctx, req.Resource); holdErr == nil && holder {
			return nil
		}
	}

	return authErr
}

// revalidate releases the holds of this connection's session that the
// authorizer no longer allows (see WireServer.Revalidate) and returns how
// many it released.
func (sc *wireServerConn) revalidate(ctx context.Context) (int, error) {
	if sc.authorizer == nil {
		return 0, nil
	}

	sc.grantMu.Lock()
	defer sc.grantMu.Unlock()

	held, heldErr := sc.conn.heldResources()
	if heldErr != nil {
		return 0, heldErr
	}

	released := 0
	errs := make([]error, 0, len(held))

	for _, resource := range held {
		if sc.authorize(AuthRequest{Operation: OpAcquire, Resource: resource}) == nil {
			continue
		}

		if revokeErr := sc.conn.revoke(ctx, resource); revokeErr != nil {
			errs = append(errs, revokeErr)

			continue
		}

		released++
	}

	return released, joinErrors(errs...)
}

// executePlan runs a submitted plan as one transaction on the host. It fails
// closed: without WithPlans, or without an authorizer, every plan is refused,
// and with one the principal must be allowed OpPlan on PlanResource. The plan
// is validated and run by Host.RunPlan, which reports ErrPlanInvalid or
// ErrPlanConflict. The authorization comes before anything about the plan is
// looked at, so a refused peer learns nothing from the answer.
func (sc *wireServerConn) executePlan(req wireRequest) wireResponse {
	if sc.planLimit <= 0 || sc.authorizer == nil {
		return wireErrorResponse(fmt.Errorf("%w: this server does not accept plans", ErrNotAuthorized))
	}

	if authErr := sc.authorize(AuthRequest{Operation: OpPlan, Resource: PlanResource}); authErr != nil {
		return wireErrorResponse(authErr)
	}

	if req.Plan == nil {
		return wireErrorResponse(fmt.Errorf("%w: a plan request carries no plan", ErrWireProtocol))
	}

	if sc.conn.isDone() {
		return wireErrorResponse(ErrConnClosed)
	}

	sc.conn.touch()

	result, runErr := sc.host.RunPlan(*req.Plan, sc.planLimit)
	if runErr != nil {
		return wireErrorResponse(runErr)
	}

	return wireResponse{PlanResult: &result}
}

// runOperation runs a submitted operation for the connection's principal. It
// fails closed: without WithOperations every run is refused. Everything else
// (the limits, the entry scope, the handler, what the client may see of an
// error) is Host.RunOperation's. A result is bounded by MaxOperationResultBytes
// so that, with JSON escaping at its worst (six bytes for one), it still fits one
// frame: a response that did not would end the connection.
func (sc *wireServerConn) runOperation(ctx context.Context, req wireRequest) wireResponse {
	if !sc.operations {
		return wireErrorResponse(fmt.Errorf("%w: this server does not accept operations", ErrNotAuthorized))
	}

	if sc.conn.isDone() {
		return wireErrorResponse(ErrConnClosed)
	}

	sc.conn.touch()

	result, runErr := sc.host.RunOperation(ctx, sc.principal, req.Operation, req.Args)
	if runErr != nil {
		return wireErrorResponse(runErr)
	}

	return wireResponse{Rows: result.Rows, Cursor: result.Cursor}
}

// execute runs one operation on the session and builds its response.
func (sc *wireServerConn) execute(ctx context.Context, req wireRequest) wireResponse {
	if req.Op == wireOpAcquire {
		// Authorizing and taking the hold are one step with respect to
		// Revalidate (see grantMu).
		sc.grantMu.RLock()
		defer sc.grantMu.RUnlock()
	}

	// Before the host looks the resource up, so an unregistered name is
	// refused exactly like a forbidden one and nothing is revealed.
	if slices.Contains(wireResourceOps, req.Op) {
		if authErr := sc.authorizeRequest(ctx, req); authErr != nil {
			return wireErrorResponse(authErr)
		}
	}

	switch req.Op {
	case wireOpAcquire:
		result, err := sc.conn.Acquire(ctx, req.Resource)
		if err != nil {
			return wireErrorResponse(err)
		}

		return wireResponse{First: result.First, Mark: result.Mark}
	case wireOpRelease:
		last, err := sc.conn.Release(ctx, req.Resource)
		if err != nil {
			return wireErrorResponse(err)
		}

		return wireResponse{Last: last}
	case wireOpWaitApplied:
		if err := sc.conn.WaitApplied(ctx, req.Resource, req.Mark); err != nil {
			return wireErrorResponse(err)
		}

		return wireResponse{}
	case wireOpKeepalive:
		if err := sc.conn.Keepalive(ctx); err != nil {
			return wireErrorResponse(err)
		}

		return wireResponse{}
	case wireOpPlan:
		return sc.executePlan(req)
	case wireOpRun:
		return sc.runOperation(ctx, req)
	default:
		return wireErrorResponse(fmt.Errorf("%w: unknown operation %q", ErrWireProtocol, req.Op))
	}
}

// ---------------------------------------------------------------------
// Client

// WireClient is the remote implementation of Client over one net.Conn. It is
// safe for concurrent use; requests are multiplexed.
type WireClient struct {
	nc     net.Conn
	reader *bufio.Reader

	writeMu sync.Mutex

	mu         sync.Mutex
	nextID     uint64
	pending    map[uint64]chan wireResponse
	closed     bool
	closeCause error

	done      chan struct{}
	closeOnce sync.Once
	bg        sync.WaitGroup
}

// Compile-time assertion that *WireClient satisfies Client.
var _ Client = (*WireClient)(nil)

// NewWireClient performs the handshake over nc and starts the read loop and
// the automatic heartbeat. It takes ownership of nc: on failure nc is closed.
// ctx bounds only the handshake.
func NewWireClient(ctx context.Context, nc net.Conn) (*WireClient, error) {
	c := &WireClient{
		nc:      nc,
		reader:  bufio.NewReader(nc),
		pending: make(map[uint64]chan wireResponse),
		done:    make(chan struct{}),
	}

	c.bg.Go(c.readLoop)

	resp, helloErr := c.call(ctx, wireRequest{Op: wireOpHello, Version: wireVersion})
	if helloErr != nil {
		c.shutdown(nil)
		c.bg.Wait()

		return nil, fmt.Errorf("wire: handshake: %w", helloErr)
	}

	// The host says how long it lets a connection stay silent; a TTL of 0
	// means it does not expire silent connections, so no heartbeat is needed.
	if ttlMillis := resp.KeepaliveTTLMillis; ttlMillis > 0 {
		interval := max(time.Duration(ttlMillis)*time.Millisecond/wireHeartbeatsPerTTL, time.Millisecond)
		c.bg.Go(func() { c.heartbeat(interval) })
	}

	return c, nil
}

// closedErrorLocked is the error for an operation on a closed connection.
// The caller holds c.mu.
func (c *WireClient) closedErrorLocked() error {
	if c.closeCause == nil {
		return ErrConnClosed
	}

	return fmt.Errorf("%w: %w", ErrConnClosed, c.closeCause)
}

func (c *WireClient) closedError() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.closedErrorLocked()
}

// shutdown ends the connection once; the first cause wins.
func (c *WireClient) shutdown(cause error) {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.closeCause = cause
		c.mu.Unlock()

		close(c.done)
		closeQuietly(c.nc)
	})
}

// readLoop delivers responses to their callers until the connection ends.
func (c *WireClient) readLoop() {
	for {
		var resp wireResponse

		if readErr := readWireFrame(c.reader, &resp); readErr != nil {
			c.shutdown(readErr)

			return
		}

		c.deliver(resp)
	}
}

// deliver hands resp to the caller waiting for its ID, if any (a caller that
// gave up has forgotten its ID).
func (c *WireClient) deliver(resp wireResponse) {
	c.mu.Lock()
	replies, found := c.pending[resp.ID]
	delete(c.pending, resp.ID)
	c.mu.Unlock()

	if found {
		replies <- resp // buffered with room for exactly this one response
	}
}

// register allocates a request ID and the channel its response arrives on.
func (c *WireClient) register() (id uint64, replies chan wireResponse, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return 0, nil, c.closedErrorLocked()
	}

	c.nextID++
	id = c.nextID
	replies = make(chan wireResponse, 1)
	c.pending[id] = replies

	return id, replies, nil
}

func (c *WireClient) forget(id uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.pending, id)
}

// send writes one frame. A failed write kills the connection.
func (c *WireClient) send(req *wireRequest) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if writeErr := writeWireFrameWithin(c.nc, req, wireWriteTimeout); writeErr != nil {
		// A frame that is too large is refused before a byte is written, so
		// the request fails and the connection is unharmed.
		if errors.Is(writeErr, ErrWireFrameTooLarge) {
			return writeErr
		}

		c.shutdown(writeErr)

		return c.closedError()
	}

	return nil
}

// settle returns resp together with the error it carries, if any.
func settle(resp wireResponse) (wireResponse, error) {
	return resp, remoteError(resp.Error)
}

// call sends req and waits for its response. If ctx ends first the host is
// told to stop (a cancel frame; the connection and the session stay), and its
// answer is awaited briefly, because for WaitApplied it carries the last
// effect error.
func (c *WireClient) call(ctx context.Context, req wireRequest) (wireResponse, error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return wireResponse{}, fmt.Errorf("%s: %w", req.Op, ctxErr)
	}

	id, replies, registerErr := c.register()
	if registerErr != nil {
		return wireResponse{}, registerErr
	}

	req.ID = id

	if sendErr := c.send(&req); sendErr != nil {
		c.forget(id)

		return wireResponse{}, sendErr
	}

	select {
	case resp := <-replies:
		return settle(resp)
	case <-c.done:
		// A response that arrived just before the connection ended still counts.
		select {
		case resp := <-replies:
			return settle(resp)
		default:
		}

		c.forget(id)

		return wireResponse{}, c.closedError()
	case <-ctx.Done():
		return c.cancelCall(ctx, id, replies, req.Op)
	}
}

// cancelCall is call's path for a ctx that ended while waiting.
func (c *WireClient) cancelCall(ctx context.Context, id uint64, replies <-chan wireResponse, op string) (wireResponse, error) {
	if sendErr := c.send(&wireRequest{Op: wireOpCancel, Target: id}); sendErr != nil {
		c.forget(id)

		return wireResponse{}, fmt.Errorf("%s: %w (%w)", op, ctx.Err(), sendErr)
	}

	grace := time.NewTimer(wireCancelGrace)
	defer grace.Stop()

	select {
	case resp := <-replies:
		if resp.Error == nil {
			return resp, nil // it finished before the cancel arrived
		}

		remote := remoteError(resp.Error)
		if errors.Is(remote, context.Canceled) || errors.Is(remote, context.DeadlineExceeded) {
			return wireResponse{}, fmt.Errorf("%w (host: %s)", ctx.Err(), resp.Error.Message)
		}

		return wireResponse{}, remote
	case <-c.done:
	case <-grace.C:
	}

	c.forget(id)

	return wireResponse{}, fmt.Errorf("%s: %w", op, ctx.Err())
}

// heartbeat sends a keepalive every interval until the connection ends. It
// proves the process is alive, not that its logic is making progress.
func (c *WireClient) heartbeat(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
		}

		ctx, cancel := context.WithTimeout(context.Background(), interval)
		_, callErr := c.call(ctx, wireRequest{Op: wireOpKeepalive})
		cancel()

		if errors.Is(callErr, ErrConnClosed) {
			return
		}
	}
}

// Acquire implements Client.
func (c *WireClient) Acquire(ctx context.Context, resource string) (AcquireResult, error) {
	resp, err := c.call(ctx, wireRequest{Op: wireOpAcquire, Resource: resource})
	if err != nil {
		return AcquireResult{}, err
	}

	return AcquireResult{First: resp.First, Mark: resp.Mark}, nil
}

// Release implements Client.
func (c *WireClient) Release(ctx context.Context, resource string) (bool, error) {
	resp, err := c.call(ctx, wireRequest{Op: wireOpRelease, Resource: resource})
	if err != nil {
		return false, err
	}

	return resp.Last, nil
}

// WaitApplied implements Client. If ctx ends the host is told to stop
// waiting; the connection and the hold stay.
func (c *WireClient) WaitApplied(ctx context.Context, resource string, mark uint64) error {
	_, err := c.call(ctx, wireRequest{Op: wireOpWaitApplied, Resource: resource, Mark: mark})

	return err
}

// Keepalive implements Client. The heartbeat already sends these.
func (c *WireClient) Keepalive(ctx context.Context) error {
	_, err := c.call(ctx, wireRequest{Op: wireOpKeepalive})

	return err
}

// RunPlan sends plan to the host, which runs it as one transaction (see Plan),
// and returns the node each label was bound to. It needs a server that was
// given WithPlans and an Authorizer that lets this client submit plans; the
// errors are ErrNotAuthorized, ErrPlanInvalid (wrong whatever the graph
// holds) and ErrPlanConflict (does not fit the graph as it is now; nothing was
// changed). A plan too large for one frame is ErrWireFrameTooLarge and leaves
// the connection alone. It is not part of Client: the in-process Conn has
// closures and does not need it.
func (c *WireClient) RunPlan(ctx context.Context, plan Plan) (PlanResult, error) {
	resp, err := c.call(ctx, wireRequest{Op: wireOpPlan, Plan: &plan})
	if err != nil {
		return PlanResult{}, err
	}

	if resp.PlanResult == nil {
		return PlanResult{}, fmt.Errorf("%w: the host answered a plan without a result", ErrWireProtocol)
	}

	return *resp.PlanResult, nil
}

// RunOperation asks the host to run the operation called name with args and
// returns its rows and cursor (see Host.RunOperation). It needs a server that
// was given WithOperations. The errors are ErrNotAuthorized (the operation is
// unknown or this client may not run it, indistinguishably), an *OperationError
// (the code and message the handler chose to show, OperationCodeInvalidArguments
// for arguments over the limits, or OperationCodeInternal for a failure the host
// keeps to itself) and the usual connection errors. It is not part of Client:
// the in-process caller uses Host.RunOperation.
func (c *WireClient) RunOperation(ctx context.Context, name string, args OperationArgs) (OperationResult, error) {
	resp, err := c.call(ctx, wireRequest{Op: wireOpRun, Operation: name, Args: args})
	if err != nil {
		var remote *RemoteError
		if errors.As(err, &remote) && remote.Code == wireCodeOperation {
			return OperationResult{}, &OperationError{Code: remote.OperationCode, Message: remote.Message}
		}

		return OperationResult{}, err
	}

	return OperationResult{Rows: resp.Rows, Cursor: resp.Cursor}, nil
}

// Done implements Client.
func (c *WireClient) Done() <-chan struct{} {
	return c.done
}

// Close implements Client: the host is asked to end the session and answers
// once its holds are released, so they are gone when Close returns. Closing
// twice, or a connection that already ended, returns nil.
func (c *WireClient) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), wireCloseTimeout)
	defer cancel()

	_, callErr := c.call(ctx, wireRequest{Op: wireOpClose})

	c.shutdown(nil)
	c.bg.Wait()

	if callErr != nil && !errors.Is(callErr, ErrConnClosed) {
		return callErr
	}

	return nil
}
