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
	"fmt"

	"github.com/CanonicalLtd/raft-test/internal/election"
	"github.com/CanonicalLtd/raft-test/internal/event"
	"github.com/hashicorp/raft"
)

// A Term holds information about an event that should happen while a certain
// node is the leader.
type Term struct {
	control    *Control
	id         raft.ServerID
	leadership *election.Leadership
	events     []*Event

	// Server ID of a follower that has been disconnect.
	disconnected raft.ServerID
}

// When can be used to schedule a certain action when a certain expected
// event occurs in the cluster during this Term.
func (t *Term) When() *Event {
	// TODO: check that we're not using Connect()
	t.control.t.Helper()

	event := &Event{
		term: t,
	}

	t.events = append(t.events, event)
	return event
}

// Disconnect a follower, which will stop receiving RPCs.
func (t *Term) Disconnect(id raft.ServerID) {
	t.control.t.Helper()

	if t.disconnected != "" {
		t.control.t.Fatalf("raft-test: term: disconnecting more than one server is not supported")
	}

	if id == t.id {
		t.control.t.Fatalf("raft-test: term: disconnect error: server %s is the leader", t.id)
	}

	t.control.logger.Debug(fmt.Sprintf("[DEBUG] raft-test: term: disconnect %s", id))

	t.disconnected = id
	t.control.network.Disconnect(t.id, id)
}

// Reconnect a previously disconnected follower.
func (t *Term) Reconnect(id raft.ServerID) {
	t.control.t.Helper()

	if id != t.disconnected {
		t.control.t.Fatalf("raft-test: term: reconnect error: server %s was not disconnected", id)
	}

	// Reconnecting a server might end up in a new election round, so we
	// have to be prepared for that.
	t.control.network.Reconnect(t.id, id)
	if t.control.waitLeadershipPropagated(t.id, t.leadership) {
		// Leadership was not lost and all followers are back
		// on track.
		return
	}

	// Leadership was lost, we must undergo a new election.
	//
	// FIXME: this prevents When() hooks to function properly. It's not a
	// big deal at the moment, since Disconnect() is mainly used for
	// snapshots, but it should be sorted.
	term := t.control.Elect(t.id)
	t.leadership = term.leadership
}

// Snapshot performs a snapshot on the given server.
func (t *Term) Snapshot(id raft.ServerID) {
	t.control.t.Helper()

	r := t.control.servers[id]
	if err := r.Snapshot().Error(); err != nil {
		t.control.t.Fatalf("raft-test: term: snapshot error on server %s: %v", id, err)
	}
}

// Event that is expected to happen during a Term.
type Event struct {
	term        *Term
	isScheduled bool
}

// Command schedules the event to occur when the Raft.Apply() method is called
// on the leader raft instance in order to apply the n'th command log during
// the current term.
func (e *Event) Command(n uint64) *Dispatch {
	e.term.control.t.Helper()

	if e.isScheduled {
		e.term.control.t.Fatal("raft-test: error: term event already scheduled")
	}
	e.isScheduled = true

	return &Dispatch{
		term: e.term,
		n:    n,
	}
}

// Dispatch defines at which phase of the dispatch process a command log event
// should fire.
type Dispatch struct {
	term  *Term
	n     uint64
	event *event.Event
}

// Enqueued configures the command log event to occurr when the command log is
// enqueued, but not yet appended by the followers.
func (d *Dispatch) Enqueued() *Action {
	d.term.control.t.Helper()

	if d.event != nil {
		d.term.control.t.Fatal("raft-test: error: dispatch event already defined")
	}
	d.event = d.term.control.whenCommandEnqueued(d.term.id, d.n)

	return &Action{
		term:  d.term,
		event: d.event,
	}
}

// Appended configures the command log event to occurr when the command log is
// appended by all followers, but not yet committed by the leader.
func (d *Dispatch) Appended() *Action {
	d.term.control.t.Helper()

	if d.event != nil {
		d.term.control.t.Fatal("raft-test: error: dispatch event already defined")
	}

	d.event = d.term.control.whenCommandAppended(d.term.id, d.n)

	return &Action{
		term:  d.term,
		event: d.event,
	}
}

// Committed configures the command log event to occurr when the command log is
// committed.
func (d *Dispatch) Committed() *Action {
	d.term.control.t.Helper()

	if d.event != nil {
		d.term.control.t.Fatal("raft-test: error: dispatch event already defined")
	}

	d.event = d.term.control.whenCommandCommitted(d.term.id, d.n)

	return &Action{
		term:  d.term,
		event: d.event,
	}
}

// Action defines what should happen when the event defined in the term occurs.
type Action struct {
	term  *Term
	event *event.Event
}

// Depose makes the action depose the current leader.
func (a *Action) Depose() {
	a.term.control.t.Helper()
	//a.control.t.Logf(
	//"raft-test: event: schedule depose server %s when command %d gets %s", a.id, a.n, a.phase)

	a.term.control.deposing = make(chan struct{})

	go func() {
		//c.t.Logf("raft-test: node %d: fsm: wait log command %d", i, n)
		a.term.control.deposeUponEvent(a.event, a.term.id, a.term.leadership)
	}()
}

// Snapshot makes the action trigger a snapshot on the leader.
//
// The typical use is to take the snapshot after a certain command log gets
// committed (see Dispatch.Committed()).
func (a *Action) Snapshot() {
	a.term.control.t.Helper()
	// a.control.t.Logf(
	// 	"raft-test: event: schedule snapshot server %s when command %d gets %s", a.id, a.n, a.phase)

	go func() {
		//c.t.Logf("raft-test: node %d: fsm: wait log command %d", i, n)
		a.term.control.snapshotUponEvent(a.event, a.term.id)
	}()
}
