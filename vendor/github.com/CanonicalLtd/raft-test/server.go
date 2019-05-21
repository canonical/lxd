// Copyright 2017 Canonical Ltd.
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

package rafttest

import (
	"testing"

	"github.com/hashicorp/raft"
)

// Server is a convenience for creating a cluster with a single raft.Raft server
// that immediately be elected as leader.
//
// The default network address of a test node is "0".
//
// Dependencies can be replaced or mutated using the various options.
func Server(t *testing.T, fsm raft.FSM, options ...Option) (*raft.Raft, func()) {
	fsms := []raft.FSM{fsm}

	rafts, control := Cluster(t, fsms, options...)
	control.Elect("0")

	return rafts["0"], control.Close
}
