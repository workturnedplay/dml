Project Implementation State

Current implementation:
- Toy implementation in Go 1.27.
- Implementation consolidated into main.go and main_test.go.
- Primitive Graph implemented and tested.
- RootGraph implemented and tested.
- Named nodes use an external name -> NodeID registry.
- ROOT is a real NodeID with special semantics only in RootGraph.

Completed milestones:
1. Primitive NodeID/relationship graph
2. Relationship queries
3. Node deletion rules
4. ROOT virtual overlay semantics
5. Consolidation into main.go / main_test.go

Current next task:
- Set up AllPointers and related foundational names.
- Each name gets its own newly allocated NodeID, like ROOT.
- These are ordinary nodes; their special meaning comes from higher-level
  relationships/processors.
- Do not make them primitive Graph concepts.

After that:
- Continue building the generic tagging machinery.
- Use relationships such as (AllPointers, P) to express tags.
- Do not prematurely implement Set/List/Pointer semantics in the primitive layer.

Important constraints:
- Primitive Graph must remain unaware of ROOT and higher-level tags.
- NodeID width is an implementation detail, not a theory decision.
- No NoNode == 0 sentinel.
- Named node => exactly one NodeID; no multiple names for one NodeID.
- Cardinality constraints belong to higher layers.
- Keep alternative representations open where theory has not decided them.

Editing convention:
- Keep implementation consolidated in main.go and main_test.go.
- For changes, give exact:
  Find this text:
  ...
  Replace with:
  ...
  
  
Currently unaddressed yet:
1. Node deletion and the name registry currently have no automatic interaction.
 NameRegistry can associate a name with a NodeID, while Graph.DeleteNode can subsequently delete that node once its relationships are gone. That leaves a stale name → NodeID / NodeID → name association pointing at a nonexistent node. The theory explicitly says the name registry is bootstrap metadata outside the primitive graph, so this is exactly the sort of boundary we should make explicit rather than accidentally letting it become inconsistent.
 I would not make Graph know about NameRegistry; that would violate the layering we've established. Instead, the next thing I'd implement is a higher-level operation on the registry/layer that performs the two pieces coherently, with tests for the failure and success cases.
