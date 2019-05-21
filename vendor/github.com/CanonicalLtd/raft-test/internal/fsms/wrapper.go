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

package fsms

import (
	"encoding/binary"
	"fmt"
	"io"
	"sync"

	"github.com/CanonicalLtd/raft-test/internal/event"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
	"github.com/pkg/errors"
)

// Wraps a raft.FSM, adding control on logs, snapshots and restores.
type fsmWrapper struct {
	logger hclog.Logger

	// ID of of the raft server associated with this FSM.
	id raft.ServerID

	// Wrapped FSM
	fsm raft.FSM

	// Total number of commands applied by this FSM.
	commands uint64

	// Total number of snapshots performed on this FSM.
	snapshots uint64

	// Total number of restores performed on this FSM.
	restores uint64

	// Events that should be fired when a certain command log is events.
	events map[uint64][]*event.Event

	mu sync.RWMutex
}

func newFSMWrapper(logger hclog.Logger, id raft.ServerID, fsm raft.FSM) *fsmWrapper {
	return &fsmWrapper{
		logger: logger,
		id:     id,
		fsm:    fsm,
		events: make(map[uint64][]*event.Event),
	}
}

func (f *fsmWrapper) Apply(log *raft.Log) interface{} {
	result := f.fsm.Apply(log)

	f.mu.Lock()
	f.commands++
	f.mu.Unlock()

	f.logger.Debug(fmt.Sprintf("[DEBUG] raft-test: fsm %s: applied %d", f.id, f.commands))
	if events, ok := f.events[f.commands]; ok {
		for _, event := range events {
			event.Fire()
			event.Block()
		}
	}

	return result
}

// Snapshot always return a dummy snapshot and no error without doing
// anything.
func (f *fsmWrapper) Snapshot() (raft.FSMSnapshot, error) {
	snapshot, err := f.fsm.Snapshot()

	if snapshot != nil {
		f.mu.Lock()
		f.snapshots++
		snapshot = &fsmSnapshotWrapper{
			commands: f.commands,
			snapshot: snapshot,
		}
		f.mu.Unlock()
	}

	return snapshot, err
}

// Restore always return a nil error without reading anything from
// the reader.
func (f *fsmWrapper) Restore(reader io.ReadCloser) error {
	if err := binary.Read(reader, binary.LittleEndian, &f.commands); err != nil {
		return errors.Wrap(err, "failed to restore commands count")
	}
	if err := f.fsm.Restore(reader); err != nil {
		return errors.Wrap(err, "failed to perform restore on user's FSM")
	}

	if events, ok := f.events[f.commands]; ok {
		for _, event := range events {
			event.Fire()
			event.Block()
		}
	}

	f.mu.Lock()
	f.restores++
	f.mu.Unlock()

	return nil
}

// This method must be called whenever the server associated with this FSM is
// about to transition to the leader state, and before any new command log is
// applied.
//
// It resets the internal state of the fsm, such as the list of applied command
// logs and the scheduled events.
func (f *fsmWrapper) electing() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for n := range f.events {
		delete(f.events, n)
	}
}

// Return an event that will fire when the n'th command log for the term is
// applied on this FSM. It's assumed that this FSM is associated with the
// current leader.
func (f *fsmWrapper) whenApplied(n uint64) *event.Event {
	e := event.New()
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.commands >= n {
		// Fire immediately.
		go e.Fire()
	} else {
		_, ok := f.events[n]
		if !ok {
			f.events[n] = make([]*event.Event, 0)
		}
		f.events[n] = append(f.events[n], e)
	}
	return e
}

// Return the total number of command logs applied by this FSM.
func (f *fsmWrapper) Commands() uint64 {
	return f.commands
}

// Return the total number of snapshots performed by this FSM.
func (f *fsmWrapper) Snapshots() uint64 {
	return f.snapshots
}

// Return the total number of restores performed by this FSM.
func (f *fsmWrapper) Restores() uint64 {
	return f.restores
}

type fsmSnapshotWrapper struct {
	commands uint64
	snapshot raft.FSMSnapshot
}

func (s *fsmSnapshotWrapper) Persist(sink raft.SnapshotSink) error {
	// Augment the snapshot with the current command count.
	if err := binary.Write(sink, binary.LittleEndian, s.commands); err != nil {
		return errors.Wrap(err, "failed to augment snapshot with commands count")
	}
	if err := s.snapshot.Persist(sink); err != nil {
		return errors.Wrap(err, "failed to perform snapshot on user's FSM")
	}
	return nil
}

func (s *fsmSnapshotWrapper) Release() {}
