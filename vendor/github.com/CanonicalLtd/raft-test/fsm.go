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
	"io"

	"github.com/hashicorp/raft"
)

// FSM create a dummy FSMs.
func FSM() raft.FSM {
	return &fsm{}
}

// FSMs creates the given number of dummy FSMs.
func FSMs(n int) []raft.FSM {
	fsms := make([]raft.FSM, n)
	for i := range fsms {
		fsms[i] = FSM()
	}
	return fsms
}

// fsm is a dummy raft finite state machine that does nothing and
// always no-ops.
type fsm struct{}

// Apply always return a nil error without doing anything.
func (f *fsm) Apply(*raft.Log) interface{} { return nil }

// Snapshot always return a dummy snapshot and no error without doing
// anything.
func (f *fsm) Snapshot() (raft.FSMSnapshot, error) { return &fsmSnapshot{}, nil }

// Restore always return a nil error without reading anything from
// the reader.
func (f *fsm) Restore(io.ReadCloser) error { return nil }

// fsmSnapshot a dummy implementation of an fsm snapshot.
type fsmSnapshot struct{}

// Persist always return a nil error without writing anything
// to the sink.
func (s *fsmSnapshot) Persist(sink raft.SnapshotSink) error { return nil }

// Release is a no-op.
func (s *fsmSnapshot) Release() {}
