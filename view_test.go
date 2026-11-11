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
	"errors"
	"reflect"
	"testing"
)

func TestReadViewSeesTheGraphOnEveryBackendAndThroughGraphActor(t *testing.T) {
	forEachBackendAndActor(t, func(t *testing.T, graph testGraph) {
		a := mustCreateNode(t, graph)
		b := mustCreateNode(t, graph)

		if _, linkErr := addRelationshipVia(graph, a, b); linkErr != nil {
			t.Fatalf("AddRelationship(a, b): %v", linkErr)
		}

		var (
			linked   bool
			children []Relationship
		)

		err := readView(graph, func(r GraphReader) error {
			var readErr error

			linked, readErr = r.HasRelationship(a, b)
			if readErr != nil {
				return wrapInterfaceErr(readErr)
			}

			children, readErr = r.FindOutgoing(a)

			return wrapInterfaceErr(readErr)
		})
		if err != nil {
			t.Fatalf("readView(): %v", err)
		}

		if want := []Relationship{{From: a, To: b}}; !linked || !reflect.DeepEqual(children, want) {
			t.Fatalf("the view saw linked=%v children=%v, want true and %v", linked, children, want)
		}
	})
}

func TestReadViewReturnsTheErrorOfFn(t *testing.T) {
	errFn := errors.New("fn failure")

	forEachBackendAndActor(t, func(t *testing.T, graph testGraph) {
		err := readView(graph, func(GraphReader) error { return errFn })
		if !errors.Is(err, errFn) {
			t.Fatalf("readView() error = %v, want %v", err, errFn)
		}
	})
}

func TestReadViewOverBoltGraphIsOneBoltReadTransaction(t *testing.T) {
	g := newBoltTestGraph(t)

	isBoltView := false

	err := readView(g, func(r GraphReader) error {
		_, isBoltView = r.(boltView)

		return nil
	})
	if err != nil {
		t.Fatalf("readView(): %v", err)
	}

	if !isBoltView {
		t.Fatal("the reader given to fn is not a view over a bolt transaction")
	}
}

func TestReadViewThroughRootGraphInsideGraphActorShowsTheOverlay(t *testing.T) {
	g := newBoltTestGraph(t)
	root := mustCreateNode(t, g)
	a := mustCreateNode(t, g)

	rootGraph, err := NewRootGraph(g, root)
	if err != nil {
		t.Fatalf("NewRootGraph(): %v", err)
	}

	actor := NewGraphActor(rootGraph)
	defer actor.Close()

	virtual := false

	viewErr := readView(actor, func(r GraphReader) error {
		var readErr error

		virtual, readErr = r.HasRelationship(root, a)

		return wrapInterfaceErr(readErr)
	})
	if viewErr != nil {
		t.Fatalf("readView(): %v", viewErr)
	}

	if !virtual {
		t.Fatal("the view does not present the virtual (ROOT, a) relationship")
	}
}

func TestReadViewOfTheHostGraphFailsAfterClose(t *testing.T) {
	h := openTestHost(t, boltTestPath(t), nil)
	graph := h.Graph()

	ran := false

	err := readView(graph, func(GraphReader) error {
		ran = true

		return nil
	})
	if err != nil || !ran {
		t.Fatalf("readView() on an open host = (ran %v, %v), want (true, nil)", ran, err)
	}

	if closeErr := h.Close(); closeErr != nil {
		t.Fatalf("Close(): %v", closeErr)
	}

	ran = false

	err = readView(graph, func(GraphReader) error {
		ran = true

		return nil
	})
	if !errors.Is(err, ErrHostClosed) || ran {
		t.Fatalf("readView() after Close = (ran %v, %v), want (false, %v)", ran, err, ErrHostClosed)
	}
}