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
on the client. Keep every `*-key.pem` secret (they are git-ignored).

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
