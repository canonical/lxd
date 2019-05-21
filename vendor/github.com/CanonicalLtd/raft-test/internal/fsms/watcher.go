// Copyright 2017 Canonical Ld.
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

package fsms

import (
	"github.com/CanonicalLtd/raft-test/internal/event"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
)

// Watcher watches all FSMs of a cluster, firing events at certain moments.
type Watcher struct {
	logger hclog.Logger

	// FSM wrappers.
	fsms map[raft.ServerID]*fsmWrapper
}

// New create a new FSMs watcher for watching the underlying FSMs.
func New(logger hclog.Logger) *Watcher {
	return &Watcher{
		logger: logger,
		fsms:   make(map[raft.ServerID]*fsmWrapper),
	}
}

// Add an FSM to the watcher. Returns an FSM that wraps the given FSM with
// instrumentation for firing events.
func (w *Watcher) Add(id raft.ServerID, fsm raft.FSM) raft.FSM {
	w.fsms[id] = newFSMWrapper(w.logger, id, fsm)
	return w.fsms[id]
}

// WhenApplied returns an event that will fire when the n'th command log for
// the term is applied on the FSM associated with the server with the given
// ID. It's that such server is currently the leader.
func (w *Watcher) WhenApplied(id raft.ServerID, n uint64) *event.Event {
	return w.fsms[id].whenApplied(n)
}

// Commands returns the total number of command logs applied by the FSM of
// the server with the given ID.
func (w *Watcher) Commands(id raft.ServerID) uint64 {
	return w.fsms[id].Commands()
}

// Snapshots returns the total number of snapshots performed by the FSM of the
// server with the given ID.
func (w *Watcher) Snapshots(id raft.ServerID) uint64 {
	return w.fsms[id].Snapshots()
}

// Restores returns the total number of restores performed by the FSM of the
// server with the given ID.
func (w *Watcher) Restores(id raft.ServerID) uint64 {
	return w.fsms[id].Restores()
}

// Electing must be called whenever the given server is about to transition to
// the leader state, and before any new command log is applied.
//
// It resets the internal state of the FSN, such the the commands counter.
func (w *Watcher) Electing(id raft.ServerID) {
	w.fsms[id].electing()
}
