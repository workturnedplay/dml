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
	"net"
	"sync/atomic"
)

// Per-resource authorization for the wire transport (theorystate.md section
// 113). Who may connect at all is the transport's own business (on Windows,
// the named pipe's DACL); this file decides which resource a connection that
// got in may use.

var (
	// ErrNotAuthorized is returned to a wire client that may not use the
	// resource it asked for.
	ErrNotAuthorized = errors.New("not authorized for this resource")

	// ErrPeerUnidentified is returned by a PeerIdentifier that cannot say who
	// is on the other end of a connection. The server then treats the peer as
	// the empty Principal.
	ErrPeerUnidentified = errors.New("the peer of this connection cannot be identified")
)

// Principal is who the transport says is on the other end of a connection: a
// Windows SID string for a named pipe. The empty Principal means unknown.
type Principal string

// AnyPrincipal in a ResourcePolicy entry allows everyone, unidentified peers
// included.
const AnyPrincipal Principal = "*"

// PeerIdentifier names the peer of a connection the server accepted. It is
// called once per connection, after the handshake. An error makes the peer
// the empty Principal (and is reported), never an allowed one.
type PeerIdentifier func(nc net.Conn) (Principal, error)

// WireOperation names what a wire client asks to do with a resource.
type WireOperation string

// The operations a wire client can ask to do with a named resource.
const (
	OpAcquire     WireOperation = wireOpAcquire
	OpRelease     WireOperation = wireOpRelease
	OpWaitApplied WireOperation = wireOpWaitApplied
)

// AuthRequest is what an Authorizer is asked: may the principal do Operation
// to Resource. WireServer.Revalidate asks about OpAcquire (may it hold the
// resource at all).
type AuthRequest struct {
	Operation WireOperation
	Resource  string
}

// Authorizer decides which principals may use which resources over the wire.
type Authorizer interface {
	// Authorize returns nil if principal may do request.Operation to
	// request.Resource, and an error wrapping ErrNotAuthorized otherwise. Any
	// other error also denies. It is called for names that are not registered
	// resources too, and must not touch the graph.
	Authorize(principal Principal, request AuthRequest) error
}

// AuthorizerFunc adapts a function to Authorizer.
type AuthorizerFunc func(principal Principal, request AuthRequest) error

// Authorize calls f.
func (f AuthorizerFunc) Authorize(principal Principal, request AuthRequest) error {
	return wrapInterfaceErr(f(principal, request))
}

// ResourcePolicy is an Authorizer from a fixed table: for each resource
// name, the principals allowed to use it. A resource with no entry, or an
// empty one, is denied to everyone (default deny). An entry containing
// AnyPrincipal allows everyone. It is immutable and safe for concurrent use.
type ResourcePolicy struct {
	allowed map[string]map[Principal]struct{}
}

// Compile-time assertion that *ResourcePolicy satisfies Authorizer.
var _ Authorizer = (*ResourcePolicy)(nil)

// NewResourcePolicy builds a policy from rules. rules is copied.
func NewResourcePolicy(rules map[string][]Principal) *ResourcePolicy {
	allowed := make(map[string]map[Principal]struct{}, len(rules))

	for resource, principals := range rules {
		set := make(map[Principal]struct{}, len(principals))

		for _, principal := range principals {
			set[principal] = struct{}{}
		}

		allowed[resource] = set
	}

	return &ResourcePolicy{allowed: allowed}
}

// Authorize implements Authorizer. It decides by resource alone: the
// operation is ignored.
func (p *ResourcePolicy) Authorize(principal Principal, request AuthRequest) error {
	set := p.allowed[request.Resource] // a missing entry is a nil map: nothing listed

	if _, listed := set[principal]; listed {
		return nil
	}

	if _, everyone := set[AnyPrincipal]; everyone {
		return nil
	}

	return fmt.Errorf("%w: resource %q", ErrNotAuthorized, request.Resource)
}

// MutablePolicy is an Authorizer whose table can be replaced while the host
// runs, which ResourcePolicy (immutable) cannot. Replace takes effect for
// every request that starts after it returns. Holds a principal already has
// are not touched by it: WireServer.Revalidate releases the ones the new
// table no longer allows. The zero value has no table and denies everyone.
// It is safe for concurrent use.
type MutablePolicy struct {
	current atomic.Pointer[ResourcePolicy]
}

// Compile-time assertion that *MutablePolicy satisfies Authorizer.
var _ Authorizer = (*MutablePolicy)(nil)

// NewMutablePolicy returns a MutablePolicy holding NewResourcePolicy(rules).
func NewMutablePolicy(rules map[string][]Principal) *MutablePolicy {
	p := &MutablePolicy{}
	p.Replace(rules)

	return p
}

// Replace makes NewResourcePolicy(rules) the table in force. rules is copied;
// nil means no resource is allowed to anyone.
func (p *MutablePolicy) Replace(rules map[string][]Principal) {
	p.current.Store(NewResourcePolicy(rules))
}

// Authorize implements Authorizer against the table in force.
func (p *MutablePolicy) Authorize(principal Principal, request AuthRequest) error {
	policy := p.current.Load()
	if policy == nil {
		return fmt.Errorf("%w: resource %q (no policy set)", ErrNotAuthorized, request.Resource)
	}

	return policy.Authorize(principal, request)
}
