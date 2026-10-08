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
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"dml"
)

// The examples in this file are compiled and run by go test, and use only
// exported API, so they show what a consumer of the package can build. None
// of them opens a socket (the wire examples run over net.Pipe), so none needs a
// firewall exception.

// toyEffect is the Effect these examples use: something outside the graph that
// exists exactly while a resource is held, here only a flag. A real Effect
// would add a rule to some other system and must, like this one, be idempotent
// (see dml.Effect).
type toyEffect struct {
	inPlace atomic.Bool
	applies atomic.Int32
	removes atomic.Int32
}

// Compile-time assertion that *toyEffect satisfies dml.Effect.
var _ dml.Effect = (*toyEffect)(nil)

// Present reports whether the effect is in place.
func (e *toyEffect) Present(context.Context) (bool, error) {
	return e.inPlace.Load(), nil
}

// Apply puts the effect in place.
func (e *toyEffect) Apply(context.Context) error {
	e.applies.Add(1)
	e.inPlace.Store(true)

	return nil
}

// Remove takes the effect away.
func (e *toyEffect) Remove(context.Context) error {
	e.removes.Add(1)
	e.inPlace.Store(false)

	return nil
}

// check panics on a failed step of an example, which fails the example.
func check(err error) {
	if err != nil {
		panic(err)
	}
}

// openExampleHost opens a Host over a store in a temporary directory. The
// returned function closes the host (it is safe to call after the example has
// closed it already) and removes the directory.
func openExampleHost() (*dml.Host, func()) {
	dir, mkdirErr := os.MkdirTemp("", "dml-example-")
	check(mkdirErr)

	host, openErr := dml.OpenHost(dml.HostConfig{Path: filepath.Join(dir, "dml.db")})
	if openErr != nil {
		panic(errors.Join(openErr, os.RemoveAll(dir)))
	}

	return host, func() {
		check(host.Close())
		check(os.RemoveAll(dir))
	}
}

// Several clients want the same outside effect. It is applied once, however
// many of them hold the resource, and removed once the last holder is gone.
func ExampleHost() {
	ctx := context.Background()

	host, cleanup := openExampleHost()
	defer cleanup()

	rule := &toyEffect{}
	check(host.RegisterResource("example-rule", rule))

	first, err := host.Connect(ctx)
	check(err)

	second, err := host.Connect(ctx)
	check(err)

	check(dml.AcquireAndWait(ctx, first, "example-rule"))
	check(dml.AcquireAndWait(ctx, second, "example-rule"))
	fmt.Println("in effect:", rule.inPlace.Load(), "after", rule.applies.Load(), "apply")

	_, err = first.Release(ctx, "example-rule")
	check(err)
	fmt.Println("in effect after the first release:", rule.inPlace.Load())

	// A client that goes away releases everything it holds; closing the host
	// then removes what nobody holds without waiting out the teardown grace.
	check(second.Close())
	check(host.Close())
	fmt.Println("in effect after the last holder is gone:", rule.inPlace.Load(), "after", rule.removes.Load(), "remove")

	// Output:
	// in effect: true after 1 apply
	// in effect after the first release: true
	// in effect after the last holder is gone: false after 1 remove
}

// A server serves a Host's operations to clients over any connection, and
// decides per resource who may use it. Here a connection is an in-memory pipe
// and every peer is identified as "example-user"; in real use the peer's
// identity comes from the transport (PipePeerPrincipal for a Windows named
// pipe, TLSPeerPrincipal for mutual TLS).
func ExampleWireServer() {
	ctx := context.Background()

	host, cleanup := openExampleHost()
	defer cleanup()

	rule := &toyEffect{}
	check(host.RegisterResource("allowed-resource", rule))
	check(host.RegisterResource("other-resource", nil))

	// A resource with no entry in the policy is denied to everyone.
	policy := dml.NewResourcePolicy(map[string][]dml.Principal{
		"allowed-resource": {"example-user"},
	})

	server := dml.NewWireServer(host,
		dml.WithPeerIdentifier(func(net.Conn) (dml.Principal, error) { return "example-user", nil }),
		dml.WithAuthorizer(policy),
	)

	// Deferred in this order so they run client, then server, then host.
	defer func() { check(server.Close()) }()

	clientSide, serverSide := net.Pipe()

	go server.ServeConn(serverSide)

	client, err := dml.NewWireClient(ctx, clientSide)
	check(err)

	defer func() { check(client.Close()) }()

	check(dml.AcquireAndWait(ctx, client, "allowed-resource"))
	fmt.Println("allowed-resource in effect:", rule.inPlace.Load())

	_, err = client.Acquire(ctx, "other-resource")
	fmt.Println("other-resource denied:", errors.Is(err, dml.ErrNotAuthorized))

	// Output:
	// allowed-resource in effect: true
	// other-resource denied: true
}

// A client over mutual TLS is identified by the fingerprint of its
// certificate, and a ResourcePolicy lists those fingerprints. (Serving it needs
// a socket: see the README for ListenTLS and DialTLS.)
func ExampleCertificateAuthority_Issue() {
	authority, err := dml.NewCertificateAuthority("example CA", 2*time.Hour)
	check(err)

	// A leaf may not outlive its CA, so its validity is shorter.
	alice, err := authority.Issue(dml.CertificateSpec{
		CommonName: "alice",
		Client:     true,
		Validity:   time.Hour,
	})
	check(err)

	policy := dml.NewResourcePolicy(map[string][]dml.Principal{
		"example-resource": {alice.Principal()},
	})

	fmt.Println(strings.HasPrefix(string(alice.Principal()), "tls-sha256:"))
	fmt.Println(policy.Authorize(alice.Principal(), "example-resource"))
	fmt.Println(errors.Is(policy.Authorize("tls-sha256:someone-else", "example-resource"), dml.ErrNotAuthorized))
	fmt.Println(errors.Is(policy.Authorize(alice.Principal(), "unlisted-resource"), dml.ErrNotAuthorized))

	// Output:
	// true
	// <nil>
	// true
	// true
}