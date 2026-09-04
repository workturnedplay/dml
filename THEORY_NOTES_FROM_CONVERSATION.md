# Theory Notes From Conversation

This document records useful ideas, distinctions, experiments, and conclusions developed while discussing the graph model. It is intentionally **not** a replacement for the formal theory document and does not promote every explored idea to a final design decision.

Status labels:
- **Established** — currently part of the working model.
- **Strong candidate** — currently favored, but still open to revision.
- **Explored** — useful idea investigated without committing to it.
- **Obsolete** — an older representation that should not be treated as current.

## 1. Primitive graph: `(A,B)`

**Established:** The primitive fact is a directed relationship `(A,B)` between two existing node IDs.

The primitive graph does not inherently assign semantic meaning to the relationship. A processor or higher-level layer decides what a particular relationship means.

Important consequences:
- `(A,B)` can exist at most once.
- `(A,A)` is allowed.
- `(A,B)` and `(C,B)` can both exist.
- A node may have zero or more incoming and outgoing relationships.
- A node can therefore exist with no relationships at all.
- Parents and children are views/derivations of incoming/outgoing `(A,B)` facts rather than separate primitive concepts.

Useful primitive queries are `FindOutgoing(Y)`, `FindIncoming(Y)`, `FindRelationships()`, and `FindRelationship(A,B)`. The primitive graph should remain ignorant of higher-level concepts such as Sets, Lists, Pointers, Domains, or ROOT.

## 2. Node IDs and names

**Established:** A textual name and a NodeID are separate things. Names such as `ROOT`, `AllPointers`, and `AllCapsules` are bootstrap/debugging conveniences whose semantic identity ultimately comes from their assigned NodeID.

Current intended invariant:
- A named node must already have a NodeID.
- A name cannot be associated with more than one NodeID.
- A NodeID cannot have more than one name.

The primitive graph does not need to store names. Persistence of the registry is a future concern.

## 3. ROOT / RootGraph

**Established:** ROOT is a real NodeID with special meaning only at the `RootGraph` layer.

The primitive `Graph` does not know that the NodeID is ROOT. `RootGraph` is an overlay in which:

```text
ROOT -> every existing node except ROOT
```

is visible virtually without being physically stored.

A primitive `(ROOT,X)` is therefore redundant in the ROOT view and must not produce a duplicate. A primitive `(ROOT,ROOT)` may exist at the primitive level but is hidden by the ROOT overlay.

Ordinary relationships pointing **to** ROOT, such as `(X,ROOT)`, remain ordinary relationships and are visible. ROOT is therefore allowed to have parents.

ROOT itself is not listed as its own virtual child. The ROOT layer prevents deletion of ROOT.

The special behavior belongs to `RootGraph`, not to the primitive graph.

## 4. Sets: simplest interpretation

**Established:** Any node can be interpreted as a Set by a processor. The simplest Set interpretation is:

```text
(X,Y)  =>  Y is an element of Set X
```

No special primitive Set object is required.

`(X,X)` is therefore a valid self-containing Set. Duplicate membership is impossible because `(X,Y)` cannot exist more than once. A node can simultaneously be a Set and an element of another Set.

A Set containing another Set does **not** inherently imply that the outer Set recursively contains the inner Set's members. Direct membership and recursive/expanded views are processor-defined interpretations.

## 5. Structured / composite Sets

An older experiment mixed Domains, Domain Sets, Sets-of-Sets, intermediary nodes, and richer membership rules. That old representation is **obsolete as an implementation specification**.

Useful ideas survived it, particularly role-bearing intermediary nodes. A composite Set could, for example, refer to other Sets as additive or subtractive operands. The same Set node can then participate in different roles in different composite structures.

Ordered add/subtract operations are possible in principle, but are **explored**, not currently required. Caching derived membership would require higher-level invalidation/change-notification machinery and should not be pulled into the primitive graph prematurely.

## 6. Domains

A Domain was explored as essentially a constrained Set: membership determines which nodes are legal targets for some operation.

The interesting use is as a constraint for pointers and other structures, rather than as a fundamentally different primitive storage mechanism.

`DomainSets` were explored as a way of combining multiple Domains into one effective allowed-membership universe. This remains a possible higher-level construction, not a current primitive concept.

## 7. Pointers: three representations

Several Pointer representations were explored. They need not be mutually exclusive; different representations can have different traversal/query costs.

### A. Direct child

```text
(AllPointers,P)
(P,X)
```

The processor interprets P as a pointer and enforces zero-or-one relevant target.

This is direct and cheap to traverse.

### B. Intermediary pointer node

```text
(P,U)
(U,X)
(AllSubPointers,U)
```

U represents the particular pointer relationship/role. This costs another node/traversal but leaves P's other direct children available for unrelated information.

### C. Metadata structure

P remains the identity of the pointer while target information is stored through a separate metadata structure associated with P. This allows P's direct children to remain unconstrained by the pointer representation.

Note: the general metadata construction this representation builds on (section 10 below) originally had a real bug — identifying the subject and the information node as M's only two children, distinguished by exclusion, breaks when they collide or when M later needs to grow other children. See section 10's correction and theorystate_v0.6.md section 10a. The as-implemented Representation C (`PointerMetadataRegistry` in the Go code) still has this limitation, kept deliberately for now; Representation D (`PointerMetadataRegistryD`) corrects it.

These three are possible representation techniques, not three different primitive graph mechanisms.

## 8. Domain Pointers

A normal Pointer can potentially also be interpreted as a Domain Pointer:

```text
(AllDomainPointers,P)
```

Additional domain information can be associated with P. A processor can then enforce that the pointer's target belongs to the permitted domain.

This illustrates a broader property: the same node identity can participate in multiple higher-level interpretations without changing its primitive facts.

## 9. Cardinality constraints

Constraints such as "at most one target" or "exactly one relevant child" are not primitive graph properties. They belong to the interpretation/processor layer.

A processor can consider only specially tagged children relevant to its structure. Alternatively, intermediary nodes can isolate a constrained role from arbitrary metadata children.

This means a node may have many primitive children while a particular processor still considers a structure valid because only a specific subset is relevant.

## 10. Metadata without disturbing the primary structure

A node P can be described without consuming P's direct-child representation. A separate fresh intermediary (M) can associate P with an information node (I), with tags indicating each intermediary's role.

**Correction (supersedes the original diagram below):** the original sketch here was `M -> P, M -> I`, identifying P and I directly as M's two children. This is wrong, not merely simplified: primitive relationships are unique pairs (section 1), so `M -> P` and `M -> I` collapse into the *same* single relationship whenever `I == P` (self-reference), silently losing the distinction between "M has a subject" and "M has an information node." The same shape of bug also appears even when `P != I`: identifying "the subject" and "the information node" as literally *M's only two children* means M can never safely acquire any other child later without breaking whichever lookup assumed there'd be exactly one remaining/excluded child. This was found and corrected during work on Pointer Representation C (section 7C); see theorystate_v0.6.md section 10a and section 75 for the general role/occurrence-identity pattern this is an instance of.

Corrected conceptually:

```text
M -> U1
U1 -> P
M -> U2
U2 -> I
```

where M identifies the metadata relationship, U1 and U2 are fresh, uniquely-tagged intermediary nodes carrying no meaning of their own, `U1 -> P` identifies the subject, and `U2 -> I` identifies the information/target. Because U1 and U2 are each freshly minted and distinct from P, I, and each other, `U1 -> P` and `U2 -> I` can never collide, even when `I == P`. Because U1 and U2 are each discovered *by their own tag* rather than by exclusion, M remains free to carry any number of additional, unrelated children — now or added later — without disturbing subject or information discovery.

This is a general construction, not merely a pointer trick.

## 11. Ordered Lists

The ordered-list experiment is one of the more mature higher-level constructions explored.

A list node can point to ElementCapsule nodes:

```text
list1 -> ElementCapsule1
list1 -> ElementCapsule2
list1 -> ElementCapsule3
```

Each ElementCapsule is a fresh, unique NodeID created for that particular occurrence/position. The capsule is therefore the identity of the list-element occurrence rather than necessarily the value itself.

This permits the same underlying value to occur multiple times in a list using different capsules.

### Capsule relationships

A capsule's previous-capsule, element/value, and next-capsule roles are
represented by three separate, freshly-minted intermediary nodes. The
intermediary nodes are not identified by their position among the capsule's
children; they are identified by their respective role tags.

Conceptually:

```text
ElementCapsule -> UPrev
ElementCapsule -> UValue
ElementCapsule -> UNext

allPrevElementCapsules -> UPrev
"allElements of ElementCapsules" -> UValue
allNextElementCapsules -> UNext

### Heads and tails

The old representation used tags/index-like nodes such as:

```text
allHEADs
allTAILs
```

to identify list boundaries.

### Identifying list elements

The list processor does not need to assume that every direct child of a list node is an ElementCapsule. It can identify relevant capsules through the appropriate tag, conceptually something like:

```text
(AllCapsules,Capsule)
```

This leaves room for unrelated children such as list comments or metadata:

```text
(list1,Y)
```

The list processor simply ignores children that are not relevant capsules.

### Doubly linked list

The intended structure is a doubly linked ordered list. A naive implementation traverses from head to tail to answer membership questions, which is the right default access pattern for *positional* queries (visit every element in order).

**Correction (supersedes the paragraph below in the original writeup):** value *membership* -- "does X occur in this list" -- does not need a separate Set/index structure built on top of the list at all, and does not need list traversal either. Because each occurrence already has its own freshly-minted, tagged value-slot node (see "Element identity versus value identity" below), asking the question from the *value's* side -- which nodes currently point at X, and are any of them a value-slot belonging to a capsule that is a member of this particular list -- answers it directly via the existing reverse-relationship index, in time proportional to how many places X is referenced, not to list length. This is the same kind of reverse-lookup already used to verify list membership of a specific capsule (a direct (list,capsule) relationship check), just applied one hop further back, from value to its owning capsule. No new node, tag, or index structure was actually needed.

### Multiple structures over one list

An ordered list can later acquire additional structures such as membership indexes, comments, metadata, or domain restrictions without changing the primitive relationship model.

## 12. Element identity versus value identity

The list experiment exposed an important distinction between:

```text
value identity
```

and:

```text
occurrence / role identity
```

A fresh intermediary node is useful when a particular occurrence or relationship needs its own identity. This is why ElementCapsule nodes are fresh IDs rather than simply reusing the value's NodeID.

The same technique can be used anywhere the same value/node participates in multiple roles.

## 13. Tags are ordinary graph structures

Names such as `AllPointers`, `AllCapsules`, `allHEADs`, `allTAILs`, `AllLists`, and `AllDomainPointers` are not primitive language features.

For example:

```text
(AllPointers,P)
```

can be interpreted as "P is a Pointer" by a processor. The primitive graph only knows that two nodes exist and that a directed fact connects them.

## 14. Interpretation belongs above storage

A recurring design hazard is mixing:

```text
what exists in the graph
```

with:

```text
what a processor believes it means
```

The primitive graph stores nodes and directed facts. A processor can interpret those facts as Set membership, List membership, Pointer targets, Domain membership, metadata, instructions, or eventually executable structures.

A fact therefore does not acquire a universal semantic meaning merely because one processor gives it meaning.

## 15. Higher-level processors and invariants

The primitive graph can express structures but does not itself know their invariants.

Examples:

```text
Pointer:
    at most one target

DomainPointer:
    target must belong to a domain

Ordered list:
    previous/next relationships must be coherent

Composite Set:
    operand roles/order must be valid
```

These invariants are expected to be enforced by higher-level code initially. Eventually some machinery could itself be represented inside the graph, but that is a future architectural possibility.

## 16. Transactions

Transactions become necessary when a higher-level operation requires multiple primitive changes to succeed atomically.

A higher-level transaction may contain lower-level transactions. A lower-level transaction can appear committed from its own local perspective while actually committing only into its parent transaction.

This suggests an isolated tentative graph view:

```text
high-level transaction
    -> lower-level transaction
        -> primitive changes
```

The isolation idea may eventually be useful for remote/distributed graph abstractions too. The transaction system is not yet part of the primitive graph contract.

## 17. Single graph versus multiple processors

The current implementation is deliberately a single in-memory graph with higher-level Go code driving it.

Future concerns include multiple goroutines/processors, shared mutable state, semantic deadlocks, and communication between processors.

Simply adding locks to the primitive graph may not produce the desired abstraction. A longer-term goal is potentially to make processors communicate through graph operations rather than sharing arbitrary mutable state directly.

This is unresolved.

## 18. Remote / multiple graphs

Remote graphs are a later problem.

The attractive goal is to make local communication behave as though it were communication with another graph, so the remote case does not require a fundamentally different semantic model.

Possible future identity schemes include a graph ID combined with a node ID, but no such scheme is decided. The current toy NodeID representation must not be mistaken for the eventual theoretical representation.

## 19. Why the single-graph foundation matters

So far, the explored structures appear expressible using:

```text
NodeID existence
+
unique directed (A,B) facts
```

plus higher-level processor code that interprets those facts and enforces invariants.

The relevant question is therefore not whether the primitive graph itself understands Sets, Lists, or Pointers. It should not.

The useful question is whether each higher-level structure can be represented using the primitive graph and whether a processor can recover and enforce its intended semantics.

So far the answer appears to be yes for the structures explored, subject to implementation and edge-case testing.

## 20. Old Domain/Set diagram

The old Domain/Set diagram mixed:

1. Domains
2. Sets
3. Sets containing Sets
4. Domain Sets
5. Pointer-to-Set/domain relationships
6. intermediary unique nodes used to distinguish roles

It should not be treated as the current Set design.

Its useful contribution is that it demonstrated the need for role-bearing intermediary nodes when the same underlying Set/Domain may participate in different roles.

## 21. Current higher-level vocabulary

The currently discussed foundational names include ideas such as:

```text
AllPointers
AllCapsules
allHEADs
allTAILs
AllLists / ordered-list collection
AllDomainPointers
```

These are examples of higher-level vocabulary, not primitive reserved keywords.

They should be created as ordinary named nodes using the same name-to-NodeID mechanism used for ROOT.

The immediate implementation direction after ROOT is to establish the foundational named nodes needed for Pointer work.

## 22. Explicitly not decided

The following remain open:

- final NodeID representation/width
- persistence format
- remote graph identity scheme
- concurrency model
- processor scheduling
- transaction implementation
- whether processors themselves should live in the graph
- exact Domain representation
- exact DomainSet representation
- recursive Set expansion semantics
- composite Set evaluation order
- caching/invalidation mechanisms
- final Pointer representation
- whether all three Pointer representations will actually be used
- exact cardinality/invariant machinery
- whether local operations should ultimately use a remote-like protocol

The primitive `(A,B)` model is being tested as a foundation, not declared to solve all of these problems already.

## 23. Current implementation direction

The implementation is intentionally small and consolidated:

```text
main.go
main_test.go
go.mod
```

The toy implementation uses Go 1.27.

The preferred development method is to keep the primitive implementation simple and add higher-level behavior in explicit layers.

The immediate implementation milestone is to create the foundational named nodes needed for Pointer work, assigning each a fresh NodeID through the existing name registry.

There is also an identified but currently unaddressed boundary issue: deleting a node from `Graph` can leave a stale name registry entry. `Graph` should not know about `NameRegistry`; a higher-level operation should eventually coordinate deletion and registry cleanup.

## 24. General design pattern

A recurring construction can be summarized as:

```text
primitive facts
    ↓
tags / named nodes
    ↓
role-bearing intermediary nodes where needed
    ↓
processor interpretation
    ↓
invariants / queries / derived views
```

This allows the same primitive node to participate in many structures simultaneously.

The main discipline going forward is to keep asking two separate questions:

1. **What primitive nodes and `(A,B)` facts physically represent this?**
2. **What processor interprets those facts, and what invariants does it enforce?**

Keeping those questions separate has repeatedly made the design clearer.
