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

import "fmt"

// Small pieces the registries built from foundational names share.

// nameBinding says where the NodeID of one foundational name goes.
type nameBinding struct {
	name string
	dest *NodeID
}

// bindNames copies the NodeID of every wanted name out of ids into its dest
// and returns them all, in order, so the caller can check that they exist. A
// name missing from ids is ErrNameNotFound, never a silent NodeID 0.
func bindNames(ids map[string]NodeID, wanted []nameBinding) ([]NodeID, error) {
	nodes := make([]NodeID, 0, len(wanted))

	for _, want := range wanted {
		id, ok := ids[want.name]
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrNameNotFound, want.name)
		}

		*want.dest = id
		nodes = append(nodes, id)
	}

	return nodes, nil
}

// taggedCheck is one commit-time check, run for a touched node carrying tag.
type taggedCheck struct {
	tag NodeID
	run func(g GraphReader, node NodeID) error
}

// runTaggedChecks runs, for every touched node, every check whose tag the
// node carries, and returns the first error.
func runTaggedChecks(g GraphReader, checks []taggedCheck, touched map[NodeID]struct{}) error {
	for node := range touched {
		for _, check := range checks {
			tagged, tagErr := g.HasRelationship(check.tag, node)
			if tagErr != nil {
				return wrapInterfaceErr(tagErr)
			}
			if !tagged {
				continue
			}

			if checkErr := check.run(g, node); checkErr != nil {
				return checkErr
			}
		}
	}

	return nil
}

// registerTaggedChecker registers a Checker called name that runs checks (see
// runTaggedChecks); its relevance tags are the tags of the checks.
func registerTaggedChecker(graph GraphAPI, name string, checks []taggedCheck) {
	tags := make([]NodeID, 0, len(checks))
	for _, check := range checks {
		tags = append(tags, check.tag)
	}

	graph.RegisterChecker(Checker{
		Name: name,
		Tags: tags,
		Check: func(g GraphReader, touched map[NodeID]struct{}) error {
			return runTaggedChecks(g, checks, touched)
		},
	})
}