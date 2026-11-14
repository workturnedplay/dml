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

// LeaseRegistry implements sessions and holds (theorystate.md section
// 111): reference counting that knows WHO holds a resource, so a holder
// that dies can be found and released, which a bare counter cannot do.
//
// A session is an ordinary node tagged (AllSessions, session): the
// liveness identity of one client of the graph (a goroutine, a process,
// eventually a remote machine). A resource is a plain Set (SetRegistry)
// used as a holder set: a session holds the resource exactly when it is a
// member. Nothing new is stored:
//   - membership is a unique (resource, session) pair (theorystate.md
//     section 2.6), so acquiring twice cannot double count;
//   - "what does this session hold" is FindIncoming(session) filtered to
//     Set-kind parents, a reverse lookup with no index to keep in sync;
//   - the resource is wanted exactly when its holder set is non-empty.
//
// Acquire and Release report first/last: whether this call made the
// holder set non-empty or empty. These are hints about a transition, and
// an effect outside the graph (say, adding a rule in another system) must NOT be driven
// only from them: a crash between commit and effect would lose the effect
// forever. Drive effects from the level instead (Held), with a reconciler
// that compares desired and actual state and repeats. Likewise "first ==
// false" means the resource is wanted, not that its effect is already in
// place; a client that needs the effect must wait for the reconciler.
//
// Sessions are ephemeral. A client's death is detected by whoever owns the
// connection (the host) and answered with CloseSession; the host's own
// death is answered at startup with CloseAllSessions, since every client
// connection died with it.
//
// A session holds a given resource at most once (Sets are idempotent).
// Several independent holds by one session would need the occurrence-
// descriptor pattern of theorystate.md section 75.
//
// Every mutator takes a Transactor, so it works standalone and composes
// inside a larger transaction; inside one, first/last/freed are
// provisional until the outermost commit (see Transactor). Like every
// registry here, LeaseRegistry stores no graph reference (section 90).
type LeaseRegistry struct {
	sets        *SetRegistry
	allSessions NodeID
}

// NewLeaseRegistry creates a LeaseRegistry over graph. sets holds and
// resolves the holder sets; allSessions tags session nodes and must
// already exist (NameAllSessions via NameRegistry.BootstrapNames).
func NewLeaseRegistry(graph GraphAPI, sets *SetRegistry, allSessions NodeID) (*LeaseRegistry, error) {
	if existsErr := requireExists(graph, allSessions); existsErr != nil {
		return nil, existsErr
	}

	return &LeaseRegistry{
		sets:        sets,
		allSessions: allSessions,
	}, nil
}

// IsSession reports whether id is currently tagged (AllSessions, id).
func (l *LeaseRegistry) IsSession(graph GraphReader, id NodeID) (bool, error) {
	has, err := graph.HasRelationship(l.allSessions, id)
	return has, wrapInterfaceErr(err)
}

// requireSession checks that session exists and is tagged
// (AllSessions, session), returning ErrNodeNotFound or ErrNotSession
// otherwise.
func (l *LeaseRegistry) requireSession(graph GraphReader, session NodeID) error {
	return requireTagged(graph, session, l.allSessions, ErrNotSession)
}

// NewSession creates a fresh node and tags it (AllSessions, id).
func (l *LeaseRegistry) NewSession(graph Transactor) (NodeID, error) {
	return transactValue(graph, func(tx Tx) (NodeID, error) {
		return createTaggedNodeTx(tx, l.allSessions)
	})
}

// Sessions returns every currently open session, sorted ascending. The
// number of live sessions is small by nature (it is bounded by the number
// of connected clients), so this reads the tag's children in full.
func (l *LeaseRegistry) Sessions(graph GraphReader) ([]NodeID, error) {
	outgoing, err := graph.FindOutgoing(l.allSessions)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}

	sessions := make([]NodeID, 0, len(outgoing))
	for _, rel := range outgoing {
		sessions = append(sessions, rel.To)
	}

	return sessions, nil
}

// isEmpty reports whether resource's holder set has no members. resource
// must be a Set (ErrNotSet / ErrNodeNotFound otherwise).
func (l *LeaseRegistry) isEmpty(graph GraphReader, resource NodeID) (bool, error) {
	size, err := l.sets.Size(graph, resource)
	return size == 0, err
}

// Held reports whether resource currently has at least one holder: the
// level-triggered "desired state" a reconciler should compare against the
// outside world.
func (l *LeaseRegistry) Held(graph GraphReader, resource NodeID) (bool, error) {
	empty, err := l.isEmpty(graph, resource)
	if err != nil {
		return false, err
	}

	return !empty, nil
}

// Holders returns resource's current holders (sessions), sorted
// ascending.
func (l *LeaseRegistry) Holders(graph GraphReader, resource NodeID) ([]NodeID, error) {
	return l.sets.Members(graph, resource)
}

// Acquire makes session a holder of resource. first reports whether this
// call made the holder set non-empty (resource was unheld and session was
// not already a holder); acquiring again is an idempotent no-op reporting
// false. session must be an open session and resource a Set.
//
// first is a transition hint, not "the effect is in place": see the
// LeaseRegistry doc comment.
func (l *LeaseRegistry) Acquire(graph Transactor, resource, session NodeID) (first bool, err error) {
	return transactBool(graph, func(tx Tx) (bool, error) {
		if requireErr := l.requireSession(tx, session); requireErr != nil {
			return false, requireErr
		}

		wasEmpty, emptyErr := l.isEmpty(tx, resource)
		if emptyErr != nil {
			return false, emptyErr
		}

		added, addErr := l.sets.Add(tx, resource, session)
		if addErr != nil {
			return false, addErr
		}

		return added && wasEmpty, nil
	})
}

// Release removes session from resource's holders. last reports whether
// this call made the holder set empty (session was a holder and nobody
// else is). Releasing a hold that was never taken, or was already
// released, is a no-op reporting false. session must be an open session
// and resource a Set.
func (l *LeaseRegistry) Release(graph Transactor, resource, session NodeID) (last bool, err error) {
	return transactBool(graph, func(tx Tx) (bool, error) {
		if requireErr := l.requireSession(tx, session); requireErr != nil {
			return false, requireErr
		}

		removed, removeErr := l.sets.Remove(tx, resource, session)
		if removeErr != nil {
			return false, removeErr
		}
		if !removed {
			return false, nil
		}

		return l.isEmpty(tx, resource)
	})
}

// heldBy returns every resource session currently holds: the Set-kind
// parents of session, sorted ascending. Other parents (the AllSessions tag,
// the virtual ROOT parent under a RootGraph, anything unrelated) are not
// Sets and are skipped.
func (l *LeaseRegistry) heldBy(graph GraphReader, session NodeID) ([]NodeID, error) {
	incoming, err := graph.FindIncoming(session)
	if err != nil {
		return nil, wrapInterfaceErr(err)
	}

	resources := make([]NodeID, 0, len(incoming))

	for _, rel := range incoming {
		isSet, setErr := l.sets.IsSet(graph, rel.From)
		if setErr != nil {
			return nil, setErr
		}
		if isSet {
			resources = append(resources, rel.From)
		}
	}

	return resources, nil
}

// CloseSession ends session: every hold it has is released and the
// session node is deleted, all in one transaction. freed lists the
// resources whose holder set this made empty, in ascending resource
// order, which is exactly what a host must hand to its reconciler when a
// client dies.
//
// Deletion follows the usual "delete only if empty" rule (theorystate.md
// section 18): if something other than holder sets still references the
// session, CloseSession fails with ErrNodeNotEmpty and nothing at all is
// changed, the holds included.
func (l *LeaseRegistry) CloseSession(graph Transactor, session NodeID) (freed []NodeID, err error) {
	return transactValue(graph, func(tx Tx) ([]NodeID, error) {
		if requireErr := l.requireSession(tx, session); requireErr != nil {
			return nil, requireErr
		}

		resources, heldErr := l.heldBy(tx, session)
		if heldErr != nil {
			return nil, heldErr
		}

		emptied := make([]NodeID, 0, len(resources))

		for _, resource := range resources {
			removed, removeErr := l.sets.Remove(tx, resource, session)
			if removeErr != nil {
				return nil, removeErr
			}
			if !removed {
				continue
			}

			empty, emptyErr := l.isEmpty(tx, resource)
			if emptyErr != nil {
				return nil, emptyErr
			}
			if empty {
				emptied = append(emptied, resource)
			}
		}

		if deleteErr := untagAndDeleteNodeTx(tx, session, l.allSessions); deleteErr != nil {
			return nil, deleteErr
		}

		return emptied, nil
	})
}

// CloseAllSessions closes every open session in one transaction and
// returns the resources that lost their last holder (in closing order,
// each once). A host calls it at startup: sessions are ephemeral, and
// every client connection died with the previous process. It is
// fail-closed: if any session cannot be closed, none is.
func (l *LeaseRegistry) CloseAllSessions(graph Transactor) (freed []NodeID, err error) {
	return transactValue(graph, func(tx Tx) ([]NodeID, error) {
		sessions, sessionsErr := l.Sessions(tx)
		if sessionsErr != nil {
			return nil, sessionsErr
		}

		emptied := make([]NodeID, 0)

		for _, session := range sessions {
			closed, closeErr := l.CloseSession(tx, session)
			if closeErr != nil {
				return nil, closeErr
			}

			emptied = append(emptied, closed...)
		}

		return emptied, nil
	})
}
