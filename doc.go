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

// Package dml implements the dml graph engine: a primitive directed graph
// plus the higher-level registries (names, pointers, lists, sets, domain
// pointers) built on it. It is the foundation the rest of the project is
// built on; see theorystate.md section 7b for the correctness discipline
// this code follows.
package dml
