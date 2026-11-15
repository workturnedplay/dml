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
and the "unaddressed" list in implementation_state.md. Only `cmd/*` and the
scripts (*.bat) have not been seen directly.

## Documents and config
- theorystate.md: theory, DECIDED/TENTATIVE/OPEN/REJECTED. Part D is the status summary. §116 = operations, §117 = notes.
- implementation_state.md: numbered changelog (items 1..N), "Planned next", "Currently unaddressed", "Explored and declined".
- .golangci.yml: lint rules (wrapcheck, govet shadow, errcheck check-blank, gocritic, revive).
- README.md: embedding the host, operations, TLS certificates.
- FILES.md: this file.

## Core engine
- doc.go: the package doc comment.
- graph.go: NodeID, Relationship, the node errors, concurrentAccessGuard, Graph (queries, *Core writes, Transact, RegisterChecker).
- graph_api.go: GraphReader, GraphStore, GraphAPI, Transactor, Tx.
- txn.go: Txn, txLog (undo log, hooks, savepoints), touchNodes.
- checker.go: Checker, graphCoreReader, runCheckers, checkerRelevant.
- paging.go: nodePager, outgoingPager, incomingPager, commonChildrenFinder, readViewer/readView, pageIterator.
- verify.go: VerifyAll, ErrLoadVerification.
- graph_actor.go: GraphActor and the reentrancy tripwire.
- bolt_view.go: BoltGraph's layout and errors, boltView (reads, paging, layout checks).
- bolt_txn.go: boltTxn (writes, undo, name records). bolt_graph.go: BoltGraph, OpenBoltGraph, open guard, format version, CheckStore, Transact.
- names.go: NameRegistry. foundational_names.go: the Name* constants, FoundationalNames, TagFoundational.
- root_graph.go: RootGraph and the ROOT overlay (rootReader, rootStore, rootTx).
- errors.go: the registry error sentinels.
- tx_helpers.go: txOps/txReader, wrapInterfaceErr, transactValue/transactBool, the *Tx wrap helpers, requireTagged/requireExists/requireAllExist.
- tagged_lookup.go: singleChildTarget, findUniqueTaggedParent/Child, exactlyOneTag.
- pointer.go: PointerRegistry. pointer_metadata.go: PointerMetadataRegistry(D).
- capsule.go: CapsuleRegistry, slotOwnersOf, forEachIncoming.
- ordered_list.go: the OrderedList interface. list.go: ListRegistry. list_shared.go: helpers both list types use.
- set.go: SetRegistry. operand_descriptor.go: shared operand-descriptor helpers and setReverseLookup.
- composite_set.go: CompositeSetRegistry. composite_set_log.go: CompositeSetLogRegistry.
- domain_pointer.go: domainConstraint, DomainPointerRegistryB/D. lease.go: LeaseRegistry. registries.go: Registries, NewRegistries.
- host_config.go: HostConfig. host.go: Host, OpenHost, Close, RegisterResource. conn.go: Client, Conn, connection lifecycle.
  reconciler.go: hostResource, the reconciler.
- registry_helpers.go: bindNames, taggedCheck, registerTaggedChecker.
- slotted_list.go: SlottedListRegistry.
- text.go: TextRegistry (interned string trie, prefix index, substring scan).
- text_forms.go: TextFormRegistry (text lists, forms, FormsContaining/TextsContaining).
- fields.go: FieldRegistry (records with named fields, Owners).
- scope.go: ScopeRegistry (ScopePath, Allow/Deny/Revoke/Allowed).
- notes.go: NoteRegistry (texts linked to any node).
- plan.go: Plan, PlanStep, Host.RunPlan (stage 1).
- operations.go: host-registered operation handlers (Register*Operation, RunOperation, grant helpers, limits).
- host_graph.go: hostGraph/hostTx, the guarded Host.Graph().

## Wire transport
- wire.go: framing, WireServer, WireClient, error codes, options (WithMaxConns, WithAuthorizer, WithPlans, WithOperations...), RunPlan/RunOperation.
- wire_auth.go: Principal, Authorizer, ResourcePolicy, MutablePolicy, AuthRequest, WireOperation.
- wire_tls.go: mutual TLS (ListenTLS, DialTLS, TLSPeerPrincipal, isPlainTCP, completeTLS).
- wire_tls_files.go: LoadServerTLS/LoadClientTLS, ReloadingServerTLS, PEM helpers.
- wire_tls_pki.go: CertificateAuthority (dmlcert's library).
- pipe_windows.go: Windows named pipe (ListenPipe, DialPipe, PipePeerPrincipal).
- private_file.go, private_file_windows.go, private_file_other.go: CreatePrivateFile.
- cmd/dmlcert/main_cert.go (not seen): certificate command; cmd/*/main_demo.go (not seen): demo.

## Test helpers (load these first for any new test file)
- testutil_test.go: TestMain, test-only write methods (Graph, GraphActor, RootGraph, BoltGraph),
  *Via helpers, must* helpers, mustCreateNode, newTestNode, sortedNodeIDs, testBackends,
  forEachBackendAndActor, runConcurrently, boltTestPath/openBoltTestGraph/newBoltTestGraph,
  rawBackend/rawValue/rawBoltGraph (raw writes that bypass Checkers; rawStagedGraph is in
  staged_graph_test.go and both are what newTestGraph builds).
- host_testutil_test.go: fakeEffect, eventually, hostTestContext, openTestHost, registerTestResource,
  connectTest, requireEffectCounts, errorLog.
- registry_backends_test.go: testGraph, newTestGraph, registryTestsOnEveryBackend (add new
  fixture-based tests to this list).

## Tests by topic
- graph_test.go: primitive Graph, Transact, nested transactions, hooks, Touch, access guard.
- names_test.go: NameRegistry (binding, retirement, Purge, LoadNames, staging, Bolt persistence).
- root_test.go: RootGraph overlay.
- pointer_test.go: PointerRegistry and PointerMetadataRegistry(D).
- capsule_test.go, list_test.go: CapsuleRegistry, ListRegistry (incl. adversarial tests).
- set_test.go: SetRegistry, CompositeSetRegistry, CompositeSetLogRegistry.
- domain_pointer_test.go: domain pointers B/D, staleness, cross-role tests.
- staged_graph_test.go: the test-only stagedGraph backend and its tests.
- bolt_graph_test.go: BoltGraph, store format, open guard, commit measurements.
- paging_test.go: pagers, page iterators, common-children lookup.
- verify_test.go: VerifyAll, CheckStore, startup sequence.
- actor_test.go: GraphActor behaviour and concurrency.
- lease_test.go: LeaseRegistry.
- list_bench_test.go: BenchmarkListCommitCost.
- host_test.go: Host, reconciler, effect deadlines.
- ordered_list_test.go: behaviour every OrderedList must have. slotted_list_test.go: slotted list structure and Checker.
- composite_set_log_lists_test.go: logs over each list type. list_differential_test.go: randomized model tests.
- text_test.go, text_forms_test.go, text_forms_containing_test.go, fields_test.go, scope_test.go, notes_test.go,
  plan_test.go, view_test.go, host_graph_test.go: tests of the file/feature of the same name.
- operations_test.go, wire_operations_test.go, example_test.go (package dml_test): exported API only.
- wire_test.go, wire_policy_test.go, wire_plan_test.go, wire_plain_tcp_test.go, wire_tls_test.go,
  wire_tls_files_test.go: wire transport over net.Pipe (no sockets).
- wire_firewalled_test.go, wire_tls_firewalled_test.go (tag portmasterFirewalled): real loopback TCP.
- pipe_windows_test.go, private_file_test.go, private_file_windows_test.go: platform tests.
- Not seen: cmd/*/main_cert_test.go, test.bat, measure.bat, prebuilt_check.bat.

## Which files to load for which work
- New registry / foundational names: foundational_names.go, registries.go, tx_helpers.go, registry_helpers.go,
  one similar registry (scope.go or fields.go), registry_backends_test.go, testutil_test.go, a similar *_test.go.
- Operations: operations.go, wire.go, host.go, conn.go, host_graph.go, scope.go, plan.go, wire_auth.go, text.go.
- Wire / authorization changes: wire.go, wire_auth.go, plan.go, wire_test.go, wire_plan_test.go, host_testutil_test.go.
- TLS / PKI: wire_tls*.go, wire_tls*_test.go, cmd/dmlcert.
- Lists / logs: list.go, list_shared.go, slotted_list.go, ordered_list.go, composite_set_log.go, ordered_list_test.go,
  list_test.go, list_differential_test.go.
- Persistence / BoltGraph: bolt_view.go, bolt_txn.go, bolt_graph.go, names.go, bolt_graph_test.go, verify_test.go, testutil_test.go.
- Host / reconciler: host.go, conn.go, reconciler.go, host_config.go, host_test.go, host_testutil_test.go.
- Docs-only changes: theorystate.md, implementation_state.md.