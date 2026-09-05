# Theory Notes From Conversation — RETIRED

**This file is retired.** Every idea it contained has been coalesced into
`theorystate_v0.6.md`, per its own "Provenance note" at the top. Kept as a
tombstone (not deleted) because existing code comments in main.go cite this
file by name and section number; the map below tells you where each old
section landed.

| Old section (this file)                     | New location in theorystate_v0.6.md          |
|----------------------------------------------|-----------------------------------------------|
| §1 Primitive graph `(A,B)`                    | §1–2, §2.8                                     |
| §2 Node IDs and names                         | §6, §6a                                        |
| §3 ROOT / RootGraph                           | §12, §12a                                      |
| §4 Sets: simplest interpretation              | §9, §9a                                        |
| §5 Structured/composite Sets                  | §9b                                            |
| §6 Domains                                    | §9c                                            |
| §7 Pointers: three representations            | §10, §10a, §10b                                |
| §8 Domain Pointers                            | §10c                                           |
| §9 Cardinality constraints                    | §7a, §10b                                      |
| §10 Metadata construction (+ correction)      | §10a (includes the corrected diagram)          |
| §11 Ordered Lists                             | §11, §11a                                      |
| §12 Element identity vs. value identity       | §75                                            |
| §13 Tags are ordinary graph structures        | §6                                             |
| §14 Interpretation belongs above storage      | §7a                                            |
| §15 Higher-level processors and invariants    | §7a                                            |
| §16 Transactions                              | §14–15                                         |
| §17 Single graph vs. multiple processors      | §19a                                           |
| §18 Remote / multiple graphs                  | Part B, §20–21                                 |
| §19 Why the single-graph foundation matters   | Part E ("Recurring construction pattern")      |
| §20 Old Domain/Set diagram                    | §9b                                            |
| §21 Current higher-level vocabulary           | §76a                                           |
| §22 Explicitly not decided                    | Part D (status summary)                        |
| §23 Current implementation direction          | `implementation_state.md`; stale-binding note → §74a |
| §24 General design pattern                    | Part E ("Recurring construction pattern")      |

If you're reading this from a `main.go` comment that cites
`THEORY_NOTES_FROM_CONVERSATION.md section N`, look up N above and go to
`theorystate_v0.6.md`.