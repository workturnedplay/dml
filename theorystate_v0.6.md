# Theory State v0.6 (Coalesced)

**Status:** Exploratory / pre-implementation
**Purpose:** Single source of truth, merging v0.1–v0.5 and subsequent session
findings. Sections 1–19 and 26–35 (single-graph foundation) are stable.
Sections 36+ (distributed/cross-graph) contain genuine unresolved forks —
these are marked explicitly rather than smoothed into false consensus.

---

## PART A — SINGLE-GRAPH FOUNDATION (stable)

## 1. Core idea

The system is a universe whose most fundamental addressable thing is a
**NodeID**. Nodes contain no inherent textual meaning. Meaning emerges from
relationships between NodeIDs and higher-level structures built on them. The
long-term goal: increasingly sophisticated concepts (data structures,
programs, processors, transactions, views, communication) become
representable and operable inside this same universe. The initial Go program
has bootstrap knowledge that should progressively migrate into the universe
itself.

## 2. NodeID and relationship primitives

**2.1** NodeID is the fundamental addressable object. One NodeID → at most
one existing node. A node can exist without relationships.

**2.2** `createNode() → NodeID`, returning a NodeID not currently identifying
an existing node. Exact representation undecided; monotonically increasing
is TENTATIVE.

**2.3** Deleted NodeIDs may eventually be reused (no global permanent
uniqueness requirement) — **amended by §40/§59-61 for exported IDs; see
Part B.**

**2.4** A relationship is an ordered pair `(A,B)`; `(A,B) ≠ (B,A)`. The
primitive itself carries no meaning beyond "A related to B in this
direction."

**2.5** Relationships have no independent NodeID/identity. Multiplicity
requires intermediary nodes (e.g. `List → E1 → A`, `List → E2 → A`).

**2.6** `(A,B)` written twice is one relationship, not two.

**2.7** Left side is source/meaning-giving; right side is target.
`(AllPointers,P258)` ≠ `(P258,AllPointers)`.

## 3. Parents and children

Not primitive. Views/indexes over `(A,B)`: `children(A)` = `{X : (A,X)}`,
`parents(B)` = `{X : (X,B)}`. Physical storage of both directions is an
implementation choice.

## 4. Bidirectional consistency

If `A.children` contains `B`, `B.parents` must contain `A`, and vice versa —
**within a single graph**. No half-committed relationship may be visible.
*(Note: this guarantee does not and cannot extend across graph boundaries —
see §67.)*

## 5. No semantic order in the primitive graph

Storage/creation order carries no meaning. Ordering, if needed, must be
built explicitly via higher-level structure.

## 6. Tags are relationships, not fields

`(AllPointers,P258)` tags P258 as Pointer-kind. `AllPointers`, `AllSets`,
etc. are ordinary NodeIDs; the bootstrap orchestrator maintains the
text↔NodeID association outside the graph.

## 7. Higher-level structures — core discipline

> If an abstraction can be constructed from existing primitives, prefer
> constructing it rather than introducing a new primitive kind of object.

## 8. Intermediary/relationship-object nodes

Ordinary nodes whose meaning comes entirely from their relationships.
Example shape for a richer relationship-object: `R→L, R→R1,
AllRelationships→R, AllLefts→L, AllRights→R1, L→A, R1→B`. Not decided as
canonical; illustrative only.

## 9. Sets — not fully defined

Minimal interpretation: `(A,B),(A,C),(A,D)` ⇒ B,C,D are members of A.
Optionally `(AllSets,A)`. Not established as complete.

## 10. Pointers

`(AllPointers,P)` marks P as Pointer-kind; schema then imposes invariants
(e.g. "exactly one target") that the *primitive* layer does not enforce —
enforcement is a higher-layer job. See §68 for the generalized mechanism
this implies.

## 11. Ordered Lists

Higher-level than Sets; needs explicit ordering structure (head/tail/
next/previous, intermediary elements). Not decided. A Set-like index atop
a List may be added later for `doesElementExist(X)`.

## 12. ROOT and discoverability (single-graph)

ROOT as virtual/overlay node: `ROOT → every existing NodeID`, without
necessarily storing all edges physically. Exact semantics OPEN in general —
**resolved into two distinct concepts for the distributed case, see §57.**

**§12a — ROOT irreflexivity (DECIDED, validated by `wtw` implementation).**
ROOT's virtual outgoing relationship excludes ROOT itself: `ROOT → X` is
visible for every existing `X != ROOT`, never for `X = ROOT`. A primitive
`(ROOT,ROOT)` fact may still exist in underlying storage (it's an ordinary
ID pair like any other, and the primitive layer has no reason to forbid
it), but the ROOT overlay must hide it from `FindOutgoing(ROOT)`,
`FindIncoming(ROOT)`, and `FindRelationships()`. Consequence: ROOT is never
its own virtual child, so ROOT can never be blocked from deletion by a
self-loop — only by genuine incoming relationships from other nodes, which
remain ordinary and permitted (ROOT may have parents, per the existing text
below).

## 13. Virtual/overlay structures

General concept: a layer can expose a derived universe over the underlying
one, intercepting/deriving without necessarily storing physically. Not yet
a defined general mechanism. Relevant to ROOT, transactions, external
resources, processors, remote graphs.

## 14. Transactions

Multiple primitive operations become visible atomically. Exact location in
the abstraction hierarchy (primitive vs. above) undecided.

> A successful committed transition takes the system from one valid state
> to another, atomically, from every observer's perspective.

## 15. Transaction overlays

In-progress transaction as overlay: committed state + transaction-local
changes = transaction view. Conceptual only. *(Not the right mechanism for
cross-graph pending state — see §63.)*

## 16. Layering / rebuilding the foundation

> Once the system becomes expressive enough, use the system to define a
> richer notion of primitive.

Not a simple linear stack — higher abstractions may depend on several
priors. Eventually a new, richer "Layer 0" may be built using earlier
higher-level structures (self-hosting).

## 17. Lowest level

Possibly even simpler than the graph: NodeID existence alone, or an
operation/history log from which existence is derived. Undecided whether a
log is semantically meaningful or merely one storage option.

> semantic model ≠ storage implementation

## 18. Delete semantics

`deleteNode(B)` fails if any relationship involving B still exists.
`deleteNodeAndRelationships(B)` (atomic cascade) is explicitly **not**
part of the foundation — rejected as bug-hiding. "Delete only if empty"
is the safe baseline.

**§18a — Structural protection is a distinct failure mode from
non-emptiness (DECIDED, validated by `wtw`).** A layer refusing deletion
because relationships still exist (the rule above) and a layer refusing to
delete a structurally load-bearing identity (e.g. ROOT) are two different
reasons and must surface as two different errors. Collapsing them into one
"not empty" error is misleading: a caller who clears all relationships and
retries will succeed in the emptiness case, but can never succeed in the
structural-protection case no matter what they clear. Any higher layer
that protects a specific NodeID's identity — ROOT today; possibly others
later, e.g. a reserved GraphID-zero slot under Model 2 — should raise its
own dedicated error rather than reusing the emptiness error.

## 19. Serialization vs. concurrency

First implementation may be a single serialized execution stream — not a
design failure. Higher-level processors may still be logically concurrent
and can still deadlock; deadlock avoidance belongs to the relevant
abstraction level, not the primitive layer.

---

## PART B — DISTRIBUTION FOUNDATION (stable)

## 20. Distributed participants — goal

The system should scale from one process → multiple logical participants in
one process → multiple processes → multiple machines → Internet, without
"remote" becoming a fundamentally different kind of object.

> If two things communicate locally, the system should be able to make
> that look like communication between remote things.

> Remote things should not have to be represented as fundamentally
> different kinds of things merely because a network exists between them.

Suggests explicit messages/operations rather than shared mutable memory.
Exact message semantics OPEN.

## 21. Graph isolation

Separate graphs/universes may coexist and interoperate; supports
security/isolation (a secrets-holding graph can stay offline). Exact
cross-graph reference mechanism unresolved — **see Part C.**

## 22. Mirrors/views — early framing (superseded)

Early idea: A caches a *correspondence* to B's NodeID rather than B's whole
graph. Superseded in detail by Part C, but the instinct (don't cache
everything, cache narrow correspondences) survives into the mirror-row
concept.

## 23. Descriptive operations / smart patches

Patches should describe *intent* ("if (A,B) exists, remove it") rather than
raw historical IDs, enabling replay against a diverged state. Language/
semantics of such patches undefined.

## 24. Rebase

A future rebase mechanism inspects current state and maps a patch's
intended meaning onto it, rather than blind ID/diff substitution. Major
open research topic.

## 25. Persistence and restart

Orchestrator maintains persistent name→NodeID associations (`"AllPointers"
→ NodeID`) across restarts. This is bootstrap knowledge outside the graph,
for now — same precedent used later for cross-graph correspondence
bookkeeping.

## 26. External world ↔ graph

Long-term: external things (time, keyboard, files, processes, goroutines,
network participants, GPU, pixels) representable via virtual nodes: graph
operations cause real-world effects and vice versa. Mapping mechanism fully
OPEN.

## 27. Processors inside the graph

Eventually: processor = graph structure + defined semantics + execution,
not a bare Go function. Self-hosting direction.

## 28. Concurrency model — long-term direction

> communicate, don't share mutable memory

Compatible with Rust-style ownership/channel thinking; Go is an
implementation choice, not a semantic constraint. *(See §65 for a
precise scoping of what this principle does and doesn't cover.)*

## 29. UI — long-term goal

Graph directly explorable, node-by-node, across abstraction levels;
tree-like nested views/tabs for branch-and-return exploration. Deferred.

## 30. Semantics before storage

> First determine what must be semantically true. Then determine what
> implementation can provide those semantics.

Also true in reverse: an implementation constraint revealing a missing
semantic requirement should be recorded, not dismissed as "just
implementation."

## 31. Current foundation, one picture
Universe
├── NodeID exists / does not exist
└── directed relationship (A,B)
├── unique ordered pair
├── no relationship NodeID
└── meaning only: "A is related to B in this direction"

Everything richer (Pointers, Sets, Lists) is built above this and unknown
to the primitive layer.

## 32. Current most important single-graph open question

> Given only NodeID existence and directed (A,B), what is the smallest
> rigorous higher-level abstraction that deserves to be called a Set?

(12 sub-questions preserved from v0.1 — membership, self-containment,
multiplicity, invariants, etc. — unchanged, still open.)

## 33. Design discipline

For every proposed concept, separate: **SEMANTIC** (what it means) /
**REPRESENTATION** (how it's built from existing concepts) /
**IMPLEMENTATION** (how it's physically stored/executed). Also tag every
claim as **DECIDED** / **TENTATIVE** / **OPEN** / **REJECTED**. A tentative
idea must never silently become a requirement.

## 34. Storage deviation principle

**DECIDED.** Storage may deviate from the literal semantic model for
performance, but only if the deviation is completely unobservable from
every level above it.

## 35. Selective provenance retention

**TENTATIVE.** Some functions/processors may be tagged to retain
causal history even after the result is unreachable; most do not by
default. Exact scope/tagging/GC relationship OPEN. (§66 is one specific
instance of this.)

---

## PART C — CROSS-GRAPH / DISTRIBUTED IDENTITY: **UNRESOLVED, MULTIPLE COMPETING MODELS**

This entire part is in active flux. Two structurally different identity
schemes have been proposed (§36–48 vs. §59–61), and a orthogonal permission
question was found layered across both. None of it is settled. What
follows documents the state of the disagreement precisely, rather than
picking a winner.

## 36. Communication model — git-like, not shared memory

**TENTATIVE.** No participant writes directly into another's graph.
Cross-participant change is either a broadcast of one's own committed
change, or a request to the node's owner. Same discipline should work for
in-process channels before any network exists.

## 37. Node ownership

**DECIDED (tentative).** Every NodeID has exactly one owning participant
(its creator). Only the owner may originate new outgoing relationships from
a node it owns. Reads of another's nodes go through a local mirror, never
direct remote access.

*(Note: §37 governs write authority over outgoing edges only. It has never
governed whether merely referencing/tagging a foreign node requires the
owner's consent — that is the separate, still-open permission-model
question, §64.)*

## 38. ROOT splits: local vs. global

**Local ROOT** (DECIDED-tentative): "every node I currently own" — cheap,
complete, always current, no networking.
**Global discovery** (OPEN, permanently): "every node across every
participant" is not a live, complete primitive and probably never can be —
same reason nothing on the internet holds a complete page index. Any global
listing is best-effort, explicitly approximate.

---

### MODEL 1 — S/P PROXY (§39–48, §58)

## 39. Cross-graph references — stubs (superseded framing, kept for context)

Original framing: G_A mints a local stub S for foreign X; the S↔X
correspondence is orchestrator bookkeeping, not a graph fact; `(A,S)` is an
ordinary local fact. S tagged `(AllMirrors,S)`. Foreign address encoding
deferred to a §16-style rebuilt layer (needs Ordered Lists, §11) — **this
deferral reasoning (§39a) still holds regardless of which identity model is
chosen.**

## 40. NodeID reuse — refinement

Reuse remains safe for IDs that never cross a graph boundary. Exported IDs
need a **never-reuse** policy: drawn from an ever-incrementing counter that
never returns freed numbers, cheap given adequate width (§42).

## 41. The ABA problem at graph boundaries

Deleting X (id 47) and later reusing 47 for Y silently misidentifies Y as X
to any holder of a stale reference. Generation-tagging (costly, needs a
live table) rejected in favor of never-reuse for exported IDs (§40).

## 42. Bit width for exported IDs

**DECIDED.** Durably-persisted, batch-reserved, monotonic, never-reset
counter gives exact (not probabilistic) zero-collision within a graph's own
export namespace. Timestamp/debug fields carry no safety weight (clocks can
move backward).

**§42a — Counter mechanics.** Batch-reserve (e.g. +1000 at a time) to avoid
per-ID fsync cost; start at 1 (0 reserved for ROOT). Existence check before
finalizing each ID as cheap defense against implementation bugs, not
against the counter's own math. Timestamp/originating-.exe stored as
explicitly non-authoritative debug metadata only.

## 43. Cross-graph teardown — sever, then delete, separately

Two independent actions: (1) sever the correspondence (handshake — correct
but can block forever on an unreachable peer; or lease — auto-expires,
never hangs, briefly stale); (2) delete locally under ordinary §18 rules.
Atomic 2PC-style cross-graph deletion **REJECTED** (blocks indefinitely on
partition). Preferred: each side deletes locally, sends a courtesy
notification afterward.

## 44. Pending state requires relationship-objects

Because primitive relationships have no identity to attach state to (§2.5),
"pending" cross-graph facts require becoming real relationship-object nodes
(§8): a proposed relationship becomes node R, taggable `(AllPending,R)`,
later resolved into a plain committed fact.

## 45. Nested transactions

**OPEN**, split from §14. A composite outer transaction (e.g. delete a
list element) may require multiple lower-level sub-operations. Whether
genuine nested-transaction support is needed, or a flatter mechanism
suffices, is unresolved.

Nested transactional views may provide a useful abstraction for composing higher-level operations and may have structural similarities to isolated/remote views. Investigate later.


The primitive relationship has no intrinsic domain-specific meaning, but a higher-level semantic system may assign meaning to it according to its schema and context.
Because (A,B) still has primitive semantics: it asserts that the directed relationship exists.

What it doesn't assert is whether that relationship means:

A contains B
A points to B
B is the next element after A
B is a member of A
A is a parent of B
A references B
...

Those meanings belong to higher-level interpreters.


## 47. Relationship mirroring via paired stand-ins (S and P)

A cross-graph relationship `(A,X)` is mirrored as two local facts:
`G_A: A→S` (S stands for foreign X), `G_B: P→X` (P stands for foreign A).
S/P correspondence is orchestrator bookkeeping; `(A,S)` and `(P,X)` are each
ordinary, fully committed local facts.

**§47a Establishment protocol.** (1) G_A requests "A wants to point at X."
(2) G_B creates/reuses P, writes `(P,X)` unilaterally (it owns both) — may
reject. (3) G_B confirms. (4) G_A creates/reuses S, writes `(A,S)`, records
S↔P mapping. Crash recovery: G_A durably logs "request sent, awaiting
confirm" before sending, so restart can determine whether to retry or
resume; an unresolved request simply never gets its lease renewed and ages
out via the normal STALE path. Once step 2 commits, `(P,X)` is permanent
and independent of G_A's future availability.

**§47b Pending applies only to the requester.** G_B never has a pending
state — it either commits atomically or doesn't. Only G_A (the requester)
has an in-between state.

**§47c (as revised) Leases change state, never trigger deletion.** Lease
lapse moves GREEN→STALE only (§58). Auto-deletion is never triggered —
§18's normal rules (delete only if empty) apply to a stale mirror row like
any other node; a mirror row may have accumulated further local structure
that must be cleared first.

**§47d Backlogged requests are ordinary, independent requests.** A batch of
establishment requests arriving after reconnection is not a special merge
event — each is evaluated independently under normal accept/reject and
serialization (§19) rules, exactly as if they'd arrived live from different
participants.

## 48. "T" is shorthand only, not a literal shared node

**REJECTED as literal.** S and P are two separate, independently-owned
nodes; a single node cannot exist identically in two graphs (violates
§37). The bridge is purely an orchestrator-level mapping.

## 58. Correspondence state model — four states

PENDING → GREEN → STALE → GONE.
- PENDING: proposed, unconfirmed.
- GREEN: confirmed; any number of new local facts pointing at S are
  automatically valid with no new propagation (they're new local uses of an
  already-confirmed bridge).
- STALE: lease expired without renewal; distinct from GONE because, per
  CAP, silence during partition can't be distinguished from deletion.
- GONE: reached only via explicit signal (courtesy notification or explicit
  reconnect report) — never inferred from silence alone.

**Only the S↔P correspondence itself carries this state** — individual
local facts reusing S inherit it, they don't carry their own.

**Open fault line found in this model (unresolved):** §47a's protocol keys
P specifically to *a single foreign source* ("reuses an existing P
representing A"). A second G_A node (C) referencing the same X requires a
*second*, fully negotiated P′ — the "no new propagation needed" property in
§58 only actually holds on the S (target) side, not the P (source) side.
Whether G_B needs fine-grained per-source visibility (current spec, costly)
or only coarse per-graph visibility (cheaper, symmetric with S) is
**undecided and should be picked explicitly.**

**Rejected variant (tried and found worse):** a scheme where P exposes only
X's outward-facing edges and S collects only A's inward-facing ones,
without either side durably recording *who references what*. Traced
against the G_A-offline case: it degrades to a live query channel, not a
mirror — G_B loses all record of who referenced X the moment G_A goes
offline, unlike the original S/P model (which durably records the fact on
both sides) or the real-ID model (which also copies). **General principle
extracted:** a mirror's value is proportional to how much of the fact gets
durably copied into local storage independent of the other side's
liveness; a scheme that only exposes information live is an RPC channel
wearing a mirror's clothing.

---

### MODEL 2 — REAL COMPOSITE IDS (§59–61)

## 59. Composite global ID: GraphID + local counter

**TENTATIVE.** An exported node's real ID is `(GraphID, local monotonic
counter)`, not a bare counter with graph context tracked separately. Any
two exported IDs are comparable/collision-free by construction with zero
coordination; origin graph is readable from the ID itself.

## 60 (revised). Mirroring uses real IDs directly — no proxy on either side

**Supersedes S/P.** Given §59, every node's ID is already foreign-safe from
birth. Neither side needs a dedicated proxy:

G_A: (A,X) — A's own outgoing edge, X by its real ID
G_B: (A,X) — mirror row, same fact, A by its real ID

S and P both collapse — the plain fact is tagged to mark the foreign end,
e.g. `(AllMirror,A)` inside G_B.

**Enforcement rule (REQUIRED).** Local storage must reject any outgoing
edge originated from a node whose GraphID prefix doesn't match the local
graph's own — mirrored nodes may only ever be targets locally, never
sources. This is what preserves §37 despite the shared ID value. As a
consequence, a mirror row never automatically accumulates the real node's
*other* relationships — each additional cross-graph fact needs its own
establishment.

Does **not** reintroduce §48's rejected "T": only the real owner ever has
write authority over a given ID's outgoing edges.

## 61. Open policy: universal wide IDs vs. cheap-by-default + retroactive proxy

**OPEN, load-bearing.** Two options:
(a) every node gets a wide, foreign-safe ID from creation (§59/§60
assume this);
(b) keep §42's cheap/narrow default, only introduce a proxy for a node
*retroactively* exported after already existing with a cheap ID (since
NodeID can't be renamed mid-life, §2.1–2.3).

**Resolved sub-point (session finding, see §66):** the earlier worry that
(a) conflicts with the "pure Layer 0 knows nothing of GraphID" philosophy
(§16) is **not correct as stated** — Layer 0 can use wide IDs from birth
and remain fully opaque to their internal structure, provided it only ever
compares them for equality and never parses them; GraphID-*interpretation*
can live entirely at a higher layer. So the (a) vs (b) choice reduces to a
genuine, still-open **storage/allocation cost** question (does every
node — including short-lived, purely-internal ones, potentially the
overwhelming majority given this project's pixel/process-level ambitions —
pay for a wide ID it may never need), not a philosophical one.

**New hard requirement found this session (§66a):** if wide composite IDs
are used, the GraphID and counter subfields **must** be fixed-width (or
otherwise strictly self-delimiting). Naive variable-width concatenation is
a real collision bug: `(GraphID=12, Counter=1)` and `(GraphID=1,
Counter=21)` can produce an identical encoded byte-string. This must be a
named, explicit DECIDED constraint on any concrete encoding, not left
implicit.

## 62. GraphID vs. exported NodeID — different uniqueness scopes

**TENTATIVE.** A cross-graph reference is always addressed as (which
graph, which node within it) — never a flat global space. Exported node-ID
uniqueness only needs to hold *within* its own graph's export namespace
(single-writer, cheap — §40's counter suffices). GraphID has no single
authority and is the one place true uncoordinated global uniqueness is
required — heavier machinery belongs there, not at the per-node level.

## 63. GraphID allocation — three candidates, OPEN

- **Registry/DHT-checked allocation** — requires a permanent, ever-growing
  ledger of every graph ever registered; same shape of permanent-global-
  authority problem §38 already declared unachievable. Recorded as in
  tension with that precedent.
- **Pure random wide ID** — negligible collision probability at 128–256
  bits; actual weak point is RNG quality, not width (precedent: Debian
  OpenSSL 2006–2008).
- **Self-certifying pubkey-as-GraphID** — same collision math as random,
  but adds provable continuity across reconnection (precedent: Tor v3,
  IPFS peer IDs, SSH host-key fingerprints). A master/subkey rotation
  pattern is a noted possible future refinement, not decided.

Explicitly out of scope here: how two graphs learn each other's GraphID in
the first place (first contact) — tied to §20's open "what is a message"
and §38's permanent global-discovery limit.

## 64. GraphID as NodeID — deferred, not rejected

Representing GraphID as an ordinary tagged NodeID is the natural
continuation of §39a, but requires an encoding for arbitrary external data
(a wide ID/key) built on Ordered Lists (§11), which don't exist at the
primitive layer yet. Deferred to a §16-style rebuild, not rejected.

## 65. AllMirror clarification

`(AllMirror,A)` inside G_B tags A as foreign-owned, exactly analogous to
`(AllPointers,P)` tagging Pointer-kind (§6/§10) — ordinary local structure,
no information about G_A's internals exposed. What stays orchestrator-only
is specifically the address-resolution detail, per §39a's reasoning.

## 66. Content-addressed versioning — separate from identity, opt-in

**TENTATIVE**, explicitly not a NodeID mechanism. Hashing a node's current
relationships into its own ID is rejected (git's model works because blobs
are immutable; applying it to a mutable NodeID would cascade identity
changes through every existing reference). Kept instead as an opt-in tag
(`(AllVersioned,N)`) accumulating hash-addressed snapshot-nodes via
ordinary relationship-object machinery (§8/§44) — N's own identity stays
stable; untagged nodes pay nothing. One instance of the general §35
selective-provenance-retention mechanism.

## 67. GraphID collision — stakes clarification

Not a new risk beyond §63's open allocation problem, but §59 raises the
stakes: because GraphID is now baked into every node's own identity rather
than tracked only in orchestrator bookkeeping, a GraphID collision between
two graphs collides *every* counter value both have ever used, pairwise —
not one corrupted correspondence entry. Argues for weighting §63's
candidates toward maximum collision resistance; doesn't change which
candidates are on the table.

---

### CROSS-CUTTING FAULT LINES — found this session, apply regardless of which model above is chosen

## 68. Reference-facts vs. schema-claims about foreign nodes are epistemically different

A plain reference fact (`(C,X)`, "I point at X") makes no claim about X's
internal state and can never be "wrong." A schema/invariant-asserting tag
(`(AllPointers_A,X)`, "X conforms to Pointer's invariants") **can** be
false relative to X's true current state, and the tagging graph has no
synchronous way to check this if the owning graph is offline or simply
unqueried. This is not a smaller version of certainty — it's a different
epistemic category: a claim *as of last observation*, not a verified fact.
**Not yet decided:** whether such claims should carry an explicit
freshness/confidence marker (generalizing §58's GREEN/STALE/GONE beyond
correspondence-liveness to cover claim-staleness generally), and what
happens on reconciliation when a stale claim turns out to have been false
the whole time (silently drop it? flag it as a recorded violation? something
else?).

## 69. Truth about a foreign node fragments permanently across observers

Even though tagging a foreign node is not a shared-memory write (mechanically
confirmed: it writes only to the tagging graph's own storage, the target
graph's storage is untouched), §4's bidirectional-consistency guarantee
does not and cannot extend across the graph boundary — a fact stored only
in G_A is invisible to any query run purely inside G_B. Two graphs can hold
permanently different, simultaneously "locally correct," never-reconciled
opinions about the same foreign node, with no forced moment where this gets
compared or caught — an unavoidable consequence of §38 (no participant can
enumerate everyone who might hold an opinion), not a bug in any one model.
Worth naming as an accepted cost rather than leaving implicit.

## 70. Identity scheme and permission model are orthogonal axes

Two independent questions have been getting bundled together:
1. **Identity/addressing** — proxy nodes (S/P) vs. real composite IDs.
2. **Permission** — capability model (knowing an ID is sufficient to
   reference it, no owner grant needed) vs. access-control model
   (referencing requires the owner's explicit prior grant, request/confirm
   style).

Four combinations exist, not two competing packages. The original S/P
model bundled proxies with a request/confirm handshake, making it
impossible to tell which part of the ceremony was "proxies need the target
owner to create them" vs. "we wanted consent." Real IDs can be paired with
either permission model:
- **Real ID + capability** (current leaning, tentative): referencing and
  tagging are both free/unilateral/always-available, at the cost of §69's
  truth-fragmentation and no forced mechanism for G_B to ever learn it's
  being referenced (see §71).
- **Real ID + access-control**: keeps the "no proxy node" saving, but still
  requires an asynchronous request/confirm exchange isomorphic to §47a —
  meaning the oft-cited "syntactically identical whether local or foreign"
  win only fully holds under the capability-model choice; under
  access-control it's a partial win (no extra node, but not a free write
  either).

**Not yet decided which to adopt long-term** — current leaning is real-ID +
capability, but this has not been stress-tested as thoroughly as the
alternative.

## 71. Pending cross-graph proposals must be visible, ordinary facts — not invisible overlays

If an access-control-style request can sit unconfirmed for a long,
unbounded time (peer offline, slow, etc.), it must **not** be modeled as an
invisible §15-style transaction overlay — that risks silent duplicate
retries, since G_A itself has no visible record to check against before
retrying. It should instead be an ordinary, fully committed,
queryable local fact from the moment of creation — a relationship-object
(§8) tagged `(AllPending,R)`, exactly per §44 — so that dedup/retry logic
can check existing visible state before acting. This machinery is only
needed under the access-control permission choice; capability-model
referencing has no pending step to represent.

## 72. Courtesy notification is optional under capability model, and something depends on it

Under real-ID + capability, nothing forces G_A to ever tell G_B that
`(A,X)` exists. This means §60(revised)'s claim that G_B keeps a local
mirror row for its own `parents(X)` queries only holds if some optional,
non-blocking courtesy-notification step is added on top — otherwise G_B's
own local view of itself stays permanently, structurally partial (which
may be an acceptable, explicitly-accepted cost — see §69 — or may not be).
**Not decided:** whether this courtesy notification is worth building as a
real (best-effort, non-required) feature.

## 73. Commit-time pluggable invariant checkers — a unifying mechanism

§10 already implies this for Pointers: the primitive layer permits a
schema-violating write; a higher-level checker, hooked into the transaction
-commit boundary, may reject the whole transaction (atomicity per §14).
This same single mechanism — not three separate ones — can also cover:
GraphID-ownership enforcement (§60's "reject outgoing edges with mismatched
GraphID prefix" rule), and revalidation of a foreign schema-claim (§68)
whenever fresh information about the foreign node actually arrives.
**Principle:** one primitive mechanism (commit-time interception, pluggable
per higher-level policy) supporting an open-ended set of higher-level
invariants, consistent with §7's "construct, don't add new primitives."

## 74. External bookkeeping can silently outlive the NodeID it describes

**Cross-cutting principle, found via `wtw`'s NameRegistry.** Any metadata
living *outside* the primitive graph but keyed by NodeID — name registries,
mirror-row bookkeeping (§47/§60), orchestrator persistence (§25) — can go
stale the instant `deleteNode` succeeds, because §18 only guarantees the
primitive graph's *own* consistency; it says nothing about external tables
still referencing that ID. This is the same shape of problem already known
from cross-graph mirror-row cleanup (§43) — it just surfaces first in the
single-graph case, where there's no network partition to blame it on.

**Resolution pattern (validated by `wtw`).** Don't teach the primitive
graph about the external structure — that would violate §7. Instead, the
external structure's own layer gets a coordinating delete operation that
performs the primitive delete and its own cleanup together, and only
commits the cleanup if the primitive delete actually succeeded. Raw
`deleteNode` remains available and does not itself enforce this; callers
who need a specific external structure kept in sync must go through that
structure's aware layer instead. Every future NodeID-keyed external
structure should expect to need its own version of this, not assume the
primitive graph will notify it of deletions.

## 75. Occurrence/role identity vs. value identity (general principle)

**Explored, generalized from ordered-list design work.** Whenever the same
underlying value/node needs to participate more than once, or in more than
one role, a fresh intermediary NodeID should carry that particular
occurrence's identity rather than reusing the value's own ID. The
ElementCapsule pattern for list positions is one instance (the same value
may occur at multiple positions, each needing independent identity); the
S/P proxy pattern in cross-graph Model 1 (§39–48) and per-role
relationship-object nodes (§8) are structurally the same move, applied to
cross-graph identity and generic tagging respectively. Named once here so
future structures can recognize the pattern rather than re-deriving it.

---

## PART D — STATUS SUMMARY (consolidated)

### DECIDED
- NodeID is the fundamental addressable object; uniqueness among existing
  nodes; nodes may exist relationship-free.
- Primitive relationship is the ordered pair `(A,B)`; no independent
  identity; no duplicate distinguishable instances.
- Creation/storage order carries no semantic meaning.
- Parents/children are views, not necessarily primitive fields.
- Higher-level invariants are enforced above the primitive layer, which
  stays ignorant of them (§10, generalized in §73).
- Storage may deviate from the semantic model only if completely
  unobservable from above (§34).
- Node ownership: exactly one owning participant per NodeID; only the
  owner originates outgoing edges (§37) — governs *write* authority only,
  not reference/tag authority (§70 open).
- Exported-ID uniqueness via durable, batch-reserved, monotonic,
  never-reset counter — exact, not probabilistic (§42/§42a).
- Any composite real-ID encoding must use fixed-width or otherwise
  self-delimiting subfields (§66a) — new this session.
- Local ROOT (per-owner) is cheap/complete/current; global discovery is
  permanently best-effort only (§38).

### TENTATIVE
- Monotonically increasing NodeIDs; serialized first implementation.
- Git-like push/pull cross-participant communication (§36).
- Composite GraphID+counter real IDs (§59); real-ID mirroring without
  proxies (§60 revised) — **competing, unresolved against S/P (§39–48/§58)**.
- Wide-IDs-from-birth is philosophically compatible with pure-Layer-0
  opaqueness (§61 resolved sub-point) — remaining question is cost only.
- Real-ID + capability-model permissions (current lean, §70) — untested
  against real-ID + access-control.
- Selective, opt-in, tag-gated content-addressed versioning (§66).

### OPEN
- Exact Set/Pointer/List definitions; exact primitive storage; whether a
  history log is part of the conceptual foundation.
- Whether S/P proxies or real composite IDs (or some third option) is the
  cross-graph identity scheme — **unresolved as of this document.**
- Whether referencing/tagging a foreign node requires owner consent
  (capability vs. access-control) — **unresolved, §70.**
- Per-source vs. per-graph granularity for cross-graph reference visibility
  (§58 fault line).
- Freshness/confidence marking for foreign schema-claims (§68).
- Whether courtesy notification for mirror-row consistency is worth
  building (§72).
- GraphID allocation mechanism (3 candidates, §63); first-contact/discovery
  between graphs.
- Universal wide IDs vs. cheap-by-default + retroactive proxy — cost
  question only now (§61).
- Nested transaction semantics (§45); exact cross-graph teardown protocol
  (§43); rebase algorithm (§24); processor execution semantics.

### REJECTED FOR NOW
- Giving primitive relationships their own NodeIDs.
- Treating creation/storage order as semantic.
- `deleteNodeAndRelationships` atomic cascade delete.
- Two-phase-commit-style atomic cross-graph deletion (§43).
- Generation-tagging as the default ABA fix (superseded by never-reuse,
  §41).
- "T" as a single node literally shared across two graphs (§48).
- Content-hashing NodeID itself as identity (§66).
- The direction/target-only proxy variant traced this session (§58) — degrades
  to a live query channel under partition, not a true mirror.

---

## PART E — GUIDING HYPOTHESIS (unchanged)

> Can an extremely small semantic foundation — unique NodeIDs plus directed
> relationships — be made expressive enough that increasingly sophisticated
> data structures, programs, processors, transactions, communication
> mechanisms, and eventually parts of the system's own infrastructure can be
> constructed from it, without continually introducing new primitive kinds
> of things?

The single-graph portion of this hypothesis (Parts A/B) remains coherent
and largely stable. The cross-graph portion (Part C) is where the real,
currently-unresolved difficulty lives — not a failure of the hypothesis,
but the expected shape of the hard part: multi-owner autonomy without a
central authority costs a unified semantic space, the same way §38 already
found for ROOT. No implementation is implied by this document.