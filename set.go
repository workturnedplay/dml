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

// SetRegistry implements the minimal Set interpretation of
// theorystate.md section 9 / 9a (formalized further in section 79):
// (AllSets, S) tags S as Set-kind, and S's direct children in the
// underlying Graph are exactly its members.
//
// Unlike every intermediary-node-based structure elsewhere in this file
// (PointerRegistry's Representation B, CapsuleRegistry's role slots,
// PointerMetadataRegistry(D)'s subject/target slots), a Set needs no
// intermediary node at all. Two properties specific to Sets, neither of
// which holds for those other structures, make this safe:
//   - Sets carry no order (theorystate.md section 5), so there is no
//     positional/sequencing information any intermediary node would need
//     to carry.
//   - Primitive relationships are already unique pairs
//     (theorystate.md section 2.6): (S, X) cannot exist more than
//     once, so duplicate membership is structurally impossible without any
//     registry-level enforcement at all.
//
// A member is therefore simply a direct child of S; adding/removing a
// member is simply one (S, X) relationship add/remove, tag-gated by
// requiring S to already be tagged (AllSets, S). Each is performed as a
// one-step Graph.Transact call rather than a raw Graph call, so that
// commit-time Checkers observe every membership change: a domain
// pointer's validity depends on its domain's membership
// (theorystate.md section 86), and a raw call would bypass every Checker.
//
// Because a Set imposes no cardinality or structural invariant on its
// children beyond the tag itself, there is no analogue here of the
// adversarial out-of-band-mutation test suites written for
// CapsuleRegistry/ListRegistry: any child of a tagged node is, by
// definition, a valid member. There is nothing an out-of-band mutation
// could do to a tagged Set's children that this registry would need to
// detect or reject.
//
// Self-membership (Add(S, S)) is permitted, matching
// theorystate.md sections 2.8 and 9a.
//
// A Set containing another Set as a member does NOT, by itself, imply
// recursive membership expansion (theorystate.md section 9a):
// Members(S) returns S's own direct children only, and never expands into
// a member that happens to itself be tagged Set-kind. Recursive,
// operand-based expansion is a separate, higher-level structure -- see
// theorystate.md sections 80-83 for the deferred (not yet
// implemented) CompositeSetRegistry / CompositeSetLogRegistry designs that
// provide it, and for why expansion intent must be recorded explicitly per
// operand rather than inferred from an operand's own tags.
//
// theorystate.md section 79 additionally decides that a node may
// carry at most one of the three Set-representation tags (AllSets,
// AllCompositeSets, AllCompositeSetLogs) -- never more than one at a
// time. This is now enforced for all three: NewSetRegistry accepts the
// tag NodeIDs of every other currently-implemented Set representation
// (AllCompositeSets, as of CompositeSetRegistry's addition, and
// AllCompositeSetLogs, as of CompositeSetLogRegistry's), and TagAsSet
// refuses (ErrSetRepresentationConflict) to tag a node already carrying
// any of them.
type SetRegistry struct {
	allSets NodeID

	// otherSetTags holds the tag NodeIDs of every other
	// currently-implemented Set representation, checked by TagAsSet to
	// enforce theorystate.md section 79's mutual exclusivity. NewSet does
	// not need this check: it always tags a freshly created node, which
	// cannot already carry any other representation's tag.
	otherSetTags []NodeID
}

// NewSetRegistry creates a SetRegistry over graph, using allSets as the
// tagging node for the (AllSets, S) relationship. allSets must already
// exist -- typically via NameRegistry.EnsureNamedNode(NameAllSets) or
// NameRegistry.BootstrapNames(FoundationalNames).
//
// otherSetTags should list the tag NodeID of every other
// currently-implemented Set representation (e.g. AllCompositeSets,
// AllCompositeSetLogs), so that TagAsSet can enforce theorystate.md
// section 79's mutual exclusivity. Each, if given, must already exist.
// Passing none is valid (no cross-representation check is performed),
// which is only appropriate if no other Set representation exists in the
// calling program yet.
func NewSetRegistry(graph GraphAPI, allSets NodeID, otherSetTags ...NodeID) (*SetRegistry, error) {
	if existsErr := requireExists(graph, allSets); existsErr != nil {
		return nil, existsErr
	}

	if existsErr := requireAllExist(graph, otherSetTags...); existsErr != nil {
		return nil, existsErr
	}

	return &SetRegistry{
		allSets:      allSets,
		otherSetTags: otherSetTags,
	}, nil
}

// IsSet reports whether id is currently tagged (AllSets, id).
func (s *SetRegistry) IsSet(graph GraphReader, id NodeID) (bool, error) {
	has, err := graph.HasRelationship(s.allSets, id)
	return has, wrapInterfaceErr(err)
}

// requireSet checks that set exists and is tagged (AllSets, set),
// returning ErrNodeNotFound or ErrNotSet otherwise. Delegates to the
// shared requireTagged helper (see its doc comment on
// PointerRegistry.requirePointer).
func (s *SetRegistry) requireSet(graph GraphReader, set NodeID) error {
	return requireTagged(graph, set, s.allSets, ErrNotSet)
}

// requireSetMember is requireSet plus a check that member exists.
func (s *SetRegistry) requireSetMember(graph GraphReader, set, member NodeID) error {
	if requireErr := s.requireSet(graph, set); requireErr != nil {
		return requireErr
	}

	return requireExists(graph, member)
}

// NewSet creates a fresh NodeID and tags it (AllSets, id). The new set
// starts empty.
func (s *SetRegistry) NewSet(graph Transactor) (NodeID, error) {
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		return createTaggedNodeTx(tx, s.allSets)
	})
}

// TagAsSet tags an existing node id as Set-kind.
//
// Unlike PointerRegistry.TagAsPointer, no cardinality invariant needs to
// be checked before tagging: a Set imposes no cardinality constraint on
// its children, so id's existing children, however many, simply become
// its members once tagged. Tagging an id that is already tagged Set-kind
// is an idempotent success, exactly like the underlying
// Graph.AddRelationship being idempotent for an already-existing
// relationship.
//
// theorystate.md section 79's mutual-exclusivity rule is enforced here
// via otherSetTags, supplied at construction (see NewSetRegistry):
// ErrSetRepresentationConflict is returned if id already carries any of
// them.
func (s *SetRegistry) TagAsSet(graph Transactor, id NodeID) error {
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		if existsErr := requireExists(tx, id); existsErr != nil {
			return existsErr
		}

		for _, tag := range s.otherSetTags {
			conflict, conflictErr := tx.HasRelationship(tag, id)
			if conflictErr != nil {
				return wrapInterfaceErr(conflictErr)
			}
			if conflict {
				return ErrSetRepresentationConflict
			}
		}

		return addRelationshipTx(tx, s.allSets, id)
	}))
}

// Add adds member to set. Both must already exist, and set must already
// be tagged (AllSets, set).
//
// added reports whether member was newly added. Adding an
// already-present member -- including self-membership, Add(set, set),
// which is permitted (theorystate.md section 2.8) -- is an
// idempotent no-op reporting added == false on the repeat call.
func (s *SetRegistry) Add(graph Transactor, set, member NodeID) (added bool, err error) {
	// The checks run inside the transaction, against the same state the
	// write sees. At top level a declined commit is rolled back, so
	// transactBool reports false (nothing was added) in that case. If
	// graph is a Tx, Checkers only judge the result at the outermost
	// commit, so added is provisional until then (see Transactor).
	return transactBool(graph, func(tx Tx) (bool, error) {
		if requireErr := s.requireSetMember(tx, set, member); requireErr != nil {
			return false, requireErr
		}

		created, txErr := tx.AddRelationship(set, member)
		return created, wrapInterfaceErr(txErr)
	})
}

// Remove removes member from set, if present.
//
// removed reports whether member was actually a member and was removed;
// removing a member that was never present is a no-op reporting
// removed == false, not an error.
func (s *SetRegistry) Remove(graph Transactor, set, member NodeID) (removed bool, err error) {
	// The checks run inside the transaction, against the same state the
	// write sees. At top level a declined commit is rolled back, so
	// transactBool reports false (nothing was removed) in that case. If
	// graph is a Tx, Checkers only judge the result at the outermost
	// commit, so removed is provisional until then (see Transactor).
	return transactBool(graph, func(tx Tx) (bool, error) {
		if requireErr := s.requireSetMember(tx, set, member); requireErr != nil {
			return false, requireErr
		}

		dropped, txErr := tx.RemoveRelationship(set, member)
		return dropped, wrapInterfaceErr(txErr)
	})
}

// Contains reports whether member currently belongs to set.
func (s *SetRegistry) Contains(graph GraphReader, set, member NodeID) (bool, error) {
	if requireErr := s.requireSetMember(graph, set, member); requireErr != nil {
		return false, requireErr
	}

	has, err := graph.HasRelationship(set, member)
	return has, wrapInterfaceErr(err)
}

// Members returns every current member of set, i.e. every direct child of
// set in the underlying Graph.
//
// This does NOT recurse into any member that happens to itself be tagged
// Set-kind -- see the SetRegistry doc comment.
func (s *SetRegistry) Members(graph GraphReader, set NodeID) ([]NodeID, error) {
	if requireErr := s.requireSet(graph, set); requireErr != nil {
		return nil, requireErr
	}

	outgoing, err := graph.FindOutgoing(set)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}

	members := make([]NodeID, 0, len(outgoing))
	for _, rel := range outgoing {
		members = append(members, rel.To)
	}

	return members, nil
}

// Size returns the number of current members of set.
func (s *SetRegistry) Size(graph GraphReader, set NodeID) (int, error) {
	members, err := s.Members(graph, set)
	if err != nil {
		return 0, err
	}

	return len(members), nil
}

// DeleteSet deletes set from the underlying graph, additionally removing
// its (AllSets, set) tag as part of the same transaction -- mirroring
// ListRegistry.DeleteList: the AllSets tag is itself an ordinary
// primitive relationship *into* set, and therefore itself counts toward
// set's relationship count, so it must be removed before Graph.DeleteNode
// can succeed, not after.
//
// Per theorystate.md section 18, deletion is deliberately "delete
// only if empty," not cascade: DeleteSet refuses with ErrNodeNotEmpty
// (resolvable by removing every member and retrying) if set currently has
// any members, or is itself currently a member of some other Set or
// otherwise referenced elsewhere -- Graph.DeleteNode requires both
// outgoing and incoming relationships to be empty.
//
// set must currently be tagged (AllSets, set).
func (s *SetRegistry) DeleteSet(graph Transactor, set NodeID) error {
	return wrapInterfaceErr(graph.Transact(func(tx Tx) error {
		if requireErr := s.requireSet(tx, set); requireErr != nil {
			return requireErr
		}
		return untagAndDeleteNodeTx(tx, set, s.allSets)
	}))
}
