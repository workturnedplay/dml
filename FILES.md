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

# FILES.md: what each file contains, and which to load for which task

Package `dml` (Go 1.27). Theory lives in theorystate.md, the numbered changelog
and the "unaddressed" list in implementation_state.md. Entries marked (inferred)
describe files not seen directly, only mentioned in those two documents.

## Documents and config
- theorystate.md: theory, DECIDED/TENTATIVE/OPEN/REJECTED. Part D is the status summary. §116 = operations (stage 2).
- implementation_state.md: numbered changelog (items 1..N), "Planned next", "Currently unaddressed", "Explored and declined".
- .golangci.yml: lint rules (wrapcheck, govet shadow, errcheck check-blank, gocritic, revive).
- FILES.md: this file.

## Core engine
- main.go (very large): primitive Graph and its guard; GraphReader/GraphStore/GraphAPI/Transactor/Tx;
  Txn, txLog, Checker, runCheckers; pagers (nodePager, outgoingPager, incomingPager,
  commonChildrenFinder, readViewer/readView, pageIterator); VerifyAll; GraphActor; BoltGraph/boltTxn/
  boltView; NameRegistry and FoundationalNames; RootGraph/rootReader/rootStore/rootTx;
  PointerRegistry, PointerMetadataRegistry(D); CapsuleRegistry; ListRegistry and the OrderedList
  interface; SetRegistry, CompositeSetRegistry, CompositeSetLogRegistry; domainConstraint and
  DomainPointerRegistryB/D; LeaseRegistry; Registries/NewRegistries; HostConfig, Host, Conn, Client,
  the reconciler, hostResource, Effect.
- registry_helpers.go: bindNames, taggedCheck, registerTaggedChecker.
- slotted_list.go: SlottedListRegistry, plus findOwnedSlot, checkCapsuleInChain, validateChain,
  stepBackWithin, verifyLists shared with ListRegistry.
- text.go: TextRegistry (interned string trie, prefix index).
- text_forms.go: TextFormRegistry (text lists, forms, FormsContaining/TextsContaining).
- fields.go: FieldRegistry (records with named fields, Owners).
- scope.go: ScopeRegistry (ScopePath, Allow/Deny/Revoke/Allowed).
- plan.go: Plan, PlanStep, Host.RunPlan (stage 1).
- operations.go: host-registered operation handlers (Register*Operation, RunOperation, grant helpers, limits).
- host_graph.go: hostGraph/hostTx, the guarded Host.Graph().

## Wire transport
- wire.go: framing, WireServer, WireClient, error codes, options (WithMaxConns, WithAuthorizer, WithPlans...).
- wire_auth.go: Principal, Authorizer, ResourcePolicy, MutablePolicy, AuthRequest, WireOperation.
- wire_tls.go: mutual TLS (ListenTLS, DialTLS, TLSPeerPrincipal, isPlainTCP, completeTLS).
- wire_tls_files.go: LoadServerTLS/LoadClientTLS, ReloadingServerTLS, PEM helpers.
- wire_tls_pki.go: CertificateAuthority (dmlcert's library).
- pipe_windows.go: Windows named pipe (ListenPipe, DialPipe, PipePeerPrincipal).
- private_file.go, private_file_windows.go, private_file_other.go (inferred): CreatePrivateFile.
- cmd/dmlcert/main_cert.go (inferred): certificate command; cmd/*/main_demo.go (inferred): demo.

## Tests (present)
- main_test.go (huge): test-only write methods, must* helpers, tests for Graph, Txn, registries,
  stagedGraph, BoltGraph, GraphActor, NameRegistry, LeaseRegistry, Host, benchmarks. Helpers:
  openTestHost, boltTestPath, hostTestContext, eventually, errorLog, fakeEffect, newTestNode,
  mustCreateNode, mustFindNodes, testBackends, forEachBackendAndActor.
- registry_backends_test.go: testGraph, newTestGraph, registryTestsOnEveryBackend (add new
  fixture-based tests to this list).
- list_differential_test.go: randomized model tests for lists and logs.
- text_test.go, text_forms_test.go, text_forms_containing_test.go, fields_test.go, scope_test.go,
  plan_test.go, wire_plan_test.go, operations_test.go (package dml_test): tests of the file of the same name.

## Tests (removed from the project right now, inferred)
- ordered_list_test.go, slotted_list_test.go, composite_set_log_lists_test.go (both list types),
  host_graph_test.go, view_test.go, wire_test.go, wire_policy_test.go, wire_plain_tcp_test.go,
  wire_firewalled_test.go, wire_tls_test.go, wire_tls_files_test.go, wire_tls_firewalled_test.go,
  pipe_windows_test.go, private_file*_test.go, example_test.go, cmd/*/main_cert_test.go.
- Scripts and docs: test.bat, measure.bat, prebuilt_check.bat, readme.md.

## Which files to load for which work
- New registry / foundational names: main.go (FoundationalNames, NewRegistries), registry_helpers.go,
  one similar registry (scope.go or fields.go), registry_backends_test.go, a similar *_test.go.
- Operations (stage 2; the wire OpRun is batch 4): operations.go, wire.go, main.go (Host parts), host_graph.go, scope.go,
  plan.go, wire_auth.go, text.go.
- Wire / authorization changes: wire.go, wire_auth.go, plan.go, wire_plan_test.go, wire_test.go.
- TLS / PKI: wire_tls*.go, wire_tls*_test.go, cmd/dmlcert.
- Lists / logs: main.go (ListRegistry, composites), slotted_list.go, ordered_list_test.go,
  list_differential_test.go.
- Persistence / BoltGraph: main.go, main_test.go (Bolt tests only).
- Docs-only changes: theorystate.md, implementation_state.md.
