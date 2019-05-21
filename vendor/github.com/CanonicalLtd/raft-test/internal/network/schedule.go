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

package network

import (
	"sync"

	"github.com/CanonicalLtd/raft-test/internal/event"
	"github.com/hashicorp/raft"
)

// Schedule contains details about under when a certain event should occur.
type schedule struct {
	// List of peers that the event should occurr on.
	peers []raft.ServerID

	// The event should fire when the transport tries to append n'th
	// command log command in this term.
	n uint64

	// Event object that should be fired when all peers have been trying to
	// append the given command.
	event *event.Event

	// Track peers where the event already occurred.
	occurred []bool

	// If true, the event should occur after the command log has been
	// appended to all followers.
	append bool

	// Serialize access to internal state.
	mu sync.RWMutex
}

// Return a zero value fault that will never occurr.
func newSchedule() *schedule {
	return &schedule{}
}

// Add a server to the list of peers where the event should occurr.
func (s *schedule) AddPeer(id raft.ServerID) {
	s.peers = append(s.peers, id)
	s.occurred = append(s.occurred, false)
}

// Resets this fault to not occur.
func (s *schedule) NoEvent() {
	s.n = 0
	s.event = nil
	for i := range s.occurred {
		s.occurred[i] = false
	}
	s.append = false
}

// Configure this scheduler to fire the given event when the append entries RPC to
// apply the n'th command log has failed on all given peers.
func (s *schedule) EnqueueFailure(n uint64, event *event.Event) {
	s.n = n
	s.event = event
	for i := range s.occurred {
		s.occurred[i] = false
	}
}

// Configure this scheduler to fire the given event after the n'th command log has
// been appended by all peers but has a failed to be notified to all consumers.
func (s *schedule) AppendFailure(n uint64, event *event.Event) {
	s.n = n
	s.event = event
	for i := range s.occurred {
		s.occurred[i] = false
	}
	s.append = true
}

// FilterRequest scans the entries in the given append request, to see whether they
// contain the command log that this fault is supposed to trigger upon.
//
// The n parameter is the number of command logs successfully appended so far
// in the current term.
//
// It returns a request object and a boolean value.
//
// If the fault should not be triggered by this request, the returned request
// object is the same as the given one and the boolean value is false.
//
// If the fault should be be triggered by this request, the bolean value will
// be true and for the returned request object the are two cases:
//
// 1) If this is an enqueue fault, the returned request object will have its
//    Entries truncated to exclude the failing command log entry and every
//    entry beyond that. This way all logs preceeding the failing command log
//    will still be appended to the peer and the associated apply futures will
//    succeed, although the failing command log won't be applied and its apply
//    future will fail with ErrLeadershipLost.
//
// 1) If this is an append fault, the returned request object will be the same
//    as the given one. This way all logs willl be appended to the peer,
//    although the transport pretend that the append entries RPC has failed,
//    simulating a disconnection when delivering the RPC reply.
//
func (s *schedule) FilterRequest(n uint64, args *raft.AppendEntriesRequest) (*raft.AppendEntriesRequest, bool) {
	if s.n == 0 {
		return args, false
	}

	for i, log := range args.Entries {
		// Only consider command log entries.
		if log.Type != raft.LogCommand {
			continue
		}
		n++
		if n != s.n {
			continue
		}

		// We found a match.
		if !s.append {
			truncatedArgs := *args
			truncatedArgs.Entries = args.Entries[:i]
			args = &truncatedArgs
		}
		return args, true

	}
	return args, false
}

// Return the command log sequence number that should trigger this fault.
//
// For example if the fault was set to fail at the n'th command log appended
// during the term, the n is returned.
func (s *schedule) Command() uint64 {
	return s.n
}

// Return true if this is an enqueue fault.
func (s *schedule) IsEnqueueFault() bool {
	return !s.append
}

// Return true if this is an append fault.
func (s *schedule) IsAppendFault() bool {
	return s.append
}

// Mark the fault as occurred on the given server, and fire the event if it has
// occurred on all servers.
func (s *schedule) OccurredOn(id raft.ServerID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, other := range s.peers {
		if other == id {
			s.occurred[i] = true
		}
	}

	for _, flag := range s.occurred {
		if !flag {
			return
		}
	}
	s.event.Fire()
}
