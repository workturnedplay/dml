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

import (
	"maps"
	"slices"
)

// The graph handle a Host gives its owner process (theorystate.md section
// 112). Registries are passed this, never the raw GraphActor, so that a
// direct user of the graph cannot bypass what Conn already guarantees:
//   - after Close every call returns ErrHostClosed instead of sending on the
//     actor's closed channel (which panics), and Close waits for the calls
//     still running (the same enter/leave gate every Conn operation uses);
//   - a write that changes a resource's holder set wakes the reconciler, like
//     a write through Conn does, instead of waiting for the next resync.

// hostGraph is the guarded GraphAPI of one Host.
type hostGraph struct {
	host *Host
}

// Compile-time assertions: hostGraph is a GraphAPI and keeps the optional
// fast paths GraphActor has.
var (
	_ GraphAPI             = hostGraph{}
	_ outgoingPager        = hostGraph{}
	_ incomingPager        = hostGraph{}
	_ commonChildrenFinder = hostGraph{}
)

// hostRead runs read against the host's actor while the host is entered, or
// returns ErrHostClosed if it is closing or closed.
func hostRead[T any](h *Host, read func(actor *GraphActor) (T, error)) (T, error) {
	if !h.enter() {
		var zero T

		return zero, ErrHostClosed
	}
	defer h.leave()

	return read(h.actor)
}

func (g hostGraph) NodeExists(id NodeID) (bool, error) {
	return hostRead(g.host, func(actor *GraphActor) (bool, error) { return actor.NodeExists(id) })
}

func (g hostGraph) HasRelationship(a, b NodeID) (bool, error) {
	return hostRead(g.host, func(actor *GraphActor) (bool, error) { return actor.HasRelationship(a, b) })
}

func (g hostGraph) FindRelationship(from, to NodeID) (Relationship, bool, error) {
	type result struct {
		relationship Relationship
		exists       bool
	}

	found, err := hostRead(g.host, func(actor *GraphActor) (result, error) {
		relationship, exists, findErr := actor.FindRelationship(from, to)

		return result{relationship: relationship, exists: exists}, findErr
	})

	return found.relationship, found.exists, err
}

func (g hostGraph) FindOutgoing(from NodeID) ([]Relationship, error) {
	return hostRead(g.host, func(actor *GraphActor) ([]Relationship, error) { return actor.FindOutgoing(from) })
}

func (g hostGraph) FindIncoming(to NodeID) ([]Relationship, error) {
	return hostRead(g.host, func(actor *GraphActor) ([]Relationship, error) { return actor.FindIncoming(to) })
}

func (g hostGraph) FindRelationships() ([]Relationship, error) {
	return hostRead(g.host, func(actor *GraphActor) ([]Relationship, error) { return actor.FindRelationships() })
}

func (g hostGraph) FindNodes() ([]NodeID, error) {
	return hostRead(g.host, func(actor *GraphActor) ([]NodeID, error) { return actor.FindNodes() })
}

func (g hostGraph) findOutgoingAfter(from, after NodeID, hasAfter bool, limit int) ([]Relationship, error) {
	return hostRead(g.host, func(actor *GraphActor) ([]Relationship, error) {
		return actor.findOutgoingAfter(from, after, hasAfter, limit)
	})
}

func (g hostGraph) findIncomingAfter(to, after NodeID, hasAfter bool, limit int) ([]Relationship, error) {
	return hostRead(g.host, func(actor *GraphActor) ([]Relationship, error) {
		return actor.findIncomingAfter(to, after, hasAfter, limit)
	})
}

func (g hostGraph) findCommonChildren(a, b NodeID, limit int) ([]NodeID, error) {
	return hostRead(g.host, func(actor *GraphActor) ([]NodeID, error) {
		return actor.findCommonChildren(a, b, limit)
	})
}

// RegisterChecker registers c on the host's graph. It has no error result, so
// on a host that is closing or closed it does nothing: nothing could run the
// Checker any more.
func (g hostGraph) RegisterChecker(c Checker) {
	if !g.host.enter() {
		return
	}
	defer g.host.leave()

	g.host.actor.RegisterChecker(c)
}

// Transact runs fn as one transaction on the host's graph. The Tx fn receives
// notes every node that is the source of a relationship it adds or removes (a
// resource's holder set is such a node), and one OnCommit hook marks the
// registered resources among them dirty once the outermost transaction has
// committed, so the reconciler looks at them without waiting for a resync.
func (g hostGraph) Transact(fn func(tx Tx) error) error {
	if !g.host.enter() {
		return ErrHostClosed
	}
	defer g.host.leave()

	return wrapInterfaceErr(g.host.actor.Transact(func(tx Tx) error {
		// Fresh on every run of the closure (it may be re-run); the hook of
		// a discarded run is discarded with it.
		changed := make(map[NodeID]struct{})

		tx.OnCommit(func() { g.host.markDirtyNodes(slices.Collect(maps.Keys(changed))) })

		return fn(hostTx{Tx: tx, changed: changed})
	}))
}

// hostTx is the Tx a hostGraph.Transact closure works through: the
// underlying transaction, plus a record of the nodes whose relationships it
// changed. Everything not overridden is promoted from the embedded Tx.
type hostTx struct {
	Tx

	// changed is shared with every nested transaction of the same outermost
	// one. A nested rollback leaves its entries in, which only costs the
	// reconciler one unnecessary look.
	changed map[NodeID]struct{}
}

// Compile-time assertions: hostTx is a Tx, and it forwards what rootTx and
// the layers under it provide beyond the Tx interface.
var (
	_ Tx                   = hostTx{}
	_ nameRecordProvider   = hostTx{}
	_ nodePager            = hostTx{}
	_ outgoingPager        = hostTx{}
	_ incomingPager        = hostTx{}
	_ commonChildrenFinder = hostTx{}
)

// note records node as changed.
func (t hostTx) note(node NodeID) {
	t.changed[node] = struct{}{}
}

func (t hostTx) AddRelationship(a, b NodeID) (bool, error) {
	created, err := t.Tx.AddRelationship(a, b)
	if created {
		t.note(a)
	}

	return created, wrapInterfaceErr(err)
}

func (t hostTx) RemoveRelationship(a, b NodeID) (bool, error) {
	removed, err := t.Tx.RemoveRelationship(a, b)
	if removed {
		t.note(a)
	}

	return removed, wrapInterfaceErr(err)
}

func (t hostTx) DeleteNode(id NodeID) error {
	err := t.Tx.DeleteNode(id)
	if err == nil {
		t.note(id)
	}

	return wrapInterfaceErr(err)
}

// Transact opens a nested transaction whose handle records into the same set.
func (t hostTx) Transact(fn func(nested Tx) error) error {
	return wrapInterfaceErr(t.Tx.Transact(func(inner Tx) error {
		return fn(hostTx{Tx: inner, changed: t.changed})
	}))
}

// nameRecords forwards to the underlying transaction, so name records are
// stored durably exactly as they would be without this wrapper.
func (t hostTx) nameRecords() nameRecordStore {
	return nameStoreOf(t.Tx)
}

// The four methods below forward the optional reader interfaces of the
// underlying transaction. Without them a type assertion on this wrapper would
// fail and findUniqueTaggedChild, the paged reads and VerifyAll would fall
// back to full scans inside every transaction. An underlying transaction that
// lacks one reports errPagingUnsupported, which is the callers' signal to fall
// back.

func (t hostTx) findNodesAfter(after NodeID, hasAfter bool, limit int) ([]NodeID, error) {
	pager, ok := t.Tx.(nodePager)
	if !ok {
		return nil, errPagingUnsupported
	}

	ids, err := pager.findNodesAfter(after, hasAfter, limit)

	return ids, wrapInterfaceErr(err)
}

func (t hostTx) findOutgoingAfter(from, after NodeID, hasAfter bool, limit int) ([]Relationship, error) {
	pager, ok := t.Tx.(outgoingPager)
	if !ok {
		return nil, errPagingUnsupported
	}

	relationships, err := pager.findOutgoingAfter(from, after, hasAfter, limit)

	return relationships, wrapInterfaceErr(err)
}

func (t hostTx) findIncomingAfter(to, after NodeID, hasAfter bool, limit int) ([]Relationship, error) {
	pager, ok := t.Tx.(incomingPager)
	if !ok {
		return nil, errPagingUnsupported
	}

	relationships, err := pager.findIncomingAfter(to, after, hasAfter, limit)

	return relationships, wrapInterfaceErr(err)
}

func (t hostTx) findCommonChildren(a, b NodeID, limit int) ([]NodeID, error) {
	finder, ok := t.Tx.(commonChildrenFinder)
	if !ok {
		return nil, errPagingUnsupported
	}

	common, err := finder.findCommonChildren(a, b, limit)

	return common, wrapInterfaceErr(err)
}