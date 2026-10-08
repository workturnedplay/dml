<!--
    Copyright 2026 workturnedplay

    Licensed under the Apache License, Version 2.0 (the "License");
    you may not use this file except in compliance with the License.
    You may obtain a copy of the License at

        http://www.apache.org/licenses/LICENSE-2.0

    Unless required by applicable law or agreed to in writing, software
    distributed under the License is distributed on an "AS IS" BASIS,
    WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
    See the License for the specific language governing permissions and
    limitations under the License.

    SPDX-License-Identifier: Apache-2.0
-->

## dml

work in progress

## Development Setup

After cloning the repository, initialize the local Git hooks:

```cmd
dothisafterclone.bat
```

OR

```bash
git config core.hooksPath .githooks
```

This reduces the post-clone setup to a single, explicit command while keeping the hook version-controlled inside `.githooks/pre-commit`.

## Embedding the host

dml provides the generic mechanism, and your program supplies what is specific
to it. One process, the *host*, owns the graph store. Clients (goroutines in
that process, other processes, other machines) connect to it and call named
operations. A *resource* is something clients may *hold*; an *effect* is
something outside the graph (a rule in another system, say) that must exist
exactly while the resource is held. The host applies the effect when the first
client acquires the resource and removes it after the last one lets go, and a
client that dies releases everything it held. You implement `dml.Effect`
(`Present`, `Apply`, `Remove`, all idempotent); dml ships no production effects.

The host side, here over a Windows named pipe:

```go
host, err := dml.OpenHost(dml.HostConfig{Path: "dml.db"})
// check err
defer host.Close()

err = host.RegisterResource("my-rule", myEffect{}) // check err

// Who may use which resource. A resource with no entry is denied to everyone.
policy := dml.NewResourcePolicy(map[string][]dml.Principal{
	"my-rule": {"S-1-5-21-1111111111-2222222222-3333333333-1001"},
})

server := dml.NewWireServer(host,
	dml.WithPeerIdentifier(dml.PipePeerPrincipal),
	dml.WithAuthorizer(policy))
// Deferred after host.Close, so the server closes first.
defer server.Close()

listener, err := dml.ListenPipe(dml.PipePath("my-host"), "") // check err
go server.Serve(listener) // returns nil once server.Close is called
```

Over TCP the transport is mutual TLS only (plain TCP is refused), with the
certificates from `dmlcert` (see below):

```go
cfg, err := dml.LoadServerTLS("certs/host1.pem", "certs/host1-key.pem", "certs/ca.pem")
listener, err := dml.ListenTLS("127.0.0.1:7600", cfg)

server := dml.NewWireServer(host,
	dml.WithPeerIdentifier(dml.TLSPeerPrincipal),
	dml.WithAuthorizer(policy)) // policy lists "tls-sha256:..." principals
go server.Serve(listener)
```

A client:

```go
client, err := dml.DialPipe(ctx, dml.PipePath("my-host")) // or:
//   cfg, err := dml.LoadClientTLS("certs/alice.pem", "certs/alice-key.pem", "certs/ca.pem", "localhost")
//   client, err := dml.DialTLS(ctx, "127.0.0.1:7600", cfg)
defer client.Close()

// Returns once the effect is confirmed in place.
err = dml.AcquireAndWait(ctx, client, "my-rule")
// ... the effect exists while this client (or any other) holds the resource ...
_, err = client.Release(ctx, "my-rule")
```

Things worth knowing:

- One connection is one session, and any disconnect releases it: there is no
  resume. A client that loses its connection must connect and acquire again.
- The host is the only owner of the store (a second host on the same file
  fails with `ErrStoreLocked`), and `HostConfig.OnError` receives what happens
  in the background (failed effects, rejected TLS handshakes, ...).
- `dml.Conn` from `host.Connect` is the in-process client: the same `Client`
  interface, no transport.
- `example_test.go` has runnable versions of these, which use in-memory pipes
  and need no sockets.

## TLS certificates

The wire transport over TCP is mutual TLS only. `cmd/dmlcert` makes the
certificates; it never overwrites a file and prints the `tls-sha256:`
Principal a `ResourcePolicy` lists for each client:

```cmd
go run ./cmd/dmlcert ca     -dir certs
go run ./cmd/dmlcert server -dir certs -name host1 -hosts localhost,127.0.0.1
go run ./cmd/dmlcert client -dir certs -name alice
```

In code: `LoadServerTLS` + `ListenTLS` on the host, `LoadClientTLS` + `DialTLS`
on the client. Keep every `*-key.pem` secret (they are git-ignored). `dmlcert`
creates every file readable only by you: mode 0600, and on Windows, where mode
bits mean nothing, an ACL granting only you and SYSTEM.

To rotate the server's certificate, or the file of client CAs, without
restarting the host, use `NewReloadingServerTLS` + its `Listen` instead:
replace the files and the next connection is served with the new ones (a bad
replacement is reported and ignored). Connections already open are not
affected. A client rotates by calling `LoadClientTLS` before each `DialTLS`;
since its Principal is its certificate's fingerprint, update the
`ResourcePolicy` first.

## License

This project and all of its contents (including source code and documentation) are 
licensed under the Apache License, Version 2.0. See the [LICENSE](LICENSE) file 
for the full license text.
