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
	"fmt"
	"io"

	"github.com/CanonicalLtd/raft-test/internal/event"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
)

// Wrap a regular raft.Transport, adding support for trigger events at
// specific times.
type eventTransport struct {
	logger hclog.Logger

	// ID of of the raft server associated with this transport.
	id raft.ServerID

	// The regular raft.Transport beging wrapped.
	trans raft.Transport

	// Track the peers we are sending RPCs to.
	peers *peers

	// Schedule and event that should happen in this transport during a
	// term.
	schedule *schedule
}

// Create a new transport wrapper..
func newEventTransport(logger hclog.Logger, id raft.ServerID, trans raft.Transport) *eventTransport {
	return &eventTransport{
		logger:   logger,
		id:       id,
		trans:    trans,
		peers:    newPeers(),
		schedule: newSchedule(),
	}
}

// Consumer returns a channel that can be used to
// consume and respond to RPC requests.
func (t *eventTransport) Consumer() <-chan raft.RPC {
	return t.trans.Consumer()
}

// LocalAddr is used to return our local address to distinguish from our peers.
func (t *eventTransport) LocalAddr() raft.ServerAddress {
	return t.trans.LocalAddr()
}

// AppendEntriesPipeline returns an interface that can be used to pipeline
// AppendEntries requests.
func (t *eventTransport) AppendEntriesPipeline(
	id raft.ServerID, target raft.ServerAddress) (raft.AppendPipeline, error) {

	if t.peers.DisconnectedAndNotSyncing(id) {
		t.logger.Debug(fmt.Sprintf("[DEBUG] raft-test: server %s: transport: append to %s: not connected", t.id, id))
		return nil, fmt.Errorf("cannot reach server %s", id)
	}
	if !t.peers.Get(id).Connected() {
		t.logger.Debug(fmt.Sprintf("[DEBUG] raft-test: server %s: transport: append to %s: syncing logs", t.id, id))
	}

	pipeline, err := t.trans.AppendEntriesPipeline(id, target)
	if err != nil {
		return nil, err
	}

	pipeline = &eventPipeline{
		logger:     t.logger,
		source:     t.id,
		target:     id,
		pipeline:   pipeline,
		peers:      t.peers,
		schedule:   t.schedule,
		shutdownCh: make(chan struct{}),
	}

	return pipeline, nil
}

// AppendEntries sends the appropriate RPC to the target node.
func (t *eventTransport) AppendEntries(
	id raft.ServerID, target raft.ServerAddress, args *raft.AppendEntriesRequest,
	resp *raft.AppendEntriesResponse) error {

	peer := t.peers.Get(id)
	t.logger.Debug(fmt.Sprintf("[DEBUG] raft-test: server %s: transport: append to %s: %s", t.id, id, stringifyLogs(args.Entries)))

	// If a fault is set, check if this batch of entries contains a command
	// log matching the one configured in the fault.
	faulty := false
	if t.schedule != nil {
		n := peer.CommandLogsCount()
		args, faulty = t.schedule.FilterRequest(n, args)
		if faulty && t.schedule.IsEnqueueFault() {
			t.logger.Debug(fmt.Sprintf(
				"[DEBUG] raft-test: server %s: transport: append to %s: enqueue fault: command %d", t.id, id, t.schedule.Command()))
		}
	}

	if t.peers.DisconnectedAndNotSyncing(id) {
		t.logger.Debug(fmt.Sprintf("[DEBUG] raft-test: server %s: transport: append to %s: not connected", t.id, id))
		return fmt.Errorf("cannot reach server %s", id)
	}
	if !t.peers.Get(id).Connected() {
		t.logger.Debug(fmt.Sprintf("[DEBUG] raft-test: server %s: transport: append to %s: syncing logs", t.id, id))
	}

	if err := t.trans.AppendEntries(id, target, args, resp); err != nil {
		return err
	}

	// Check for a newer term, stop running
	if resp.Term > args.Term {
		t.logger.Debug(fmt.Sprintf("[DEBUG] raft-test: server %s: transport: append to %s: newer term", t.id, id))
	}

	peer.UpdateLogs(args.Entries)

	if faulty && t.schedule.IsEnqueueFault() {
		t.schedule.OccurredOn(id)
		t.schedule.event.Block()
		return fmt.Errorf("cannot reach server %s", id)
	}

	return nil
}

// RequestVote sends the appropriate RPC to the target node.
func (t *eventTransport) RequestVote(
	id raft.ServerID, target raft.ServerAddress, args *raft.RequestVoteRequest,
	resp *raft.RequestVoteResponse) error {

	if !t.peers.Get(id).Connected() {
		return fmt.Errorf("connectivity to server %s is down", id)
	}

	return t.trans.RequestVote(id, target, args, resp)
}

// InstallSnapshot is used to push a snapshot down to a follower. The data is read from
// the ReadCloser and streamed to the client.
func (t *eventTransport) InstallSnapshot(
	id raft.ServerID, target raft.ServerAddress, args *raft.InstallSnapshotRequest,
	resp *raft.InstallSnapshotResponse, data io.Reader) error {

	if !t.peers.Get(id).Connected() {
		return fmt.Errorf("connectivity to server %s is down", id)
	}
	return t.trans.InstallSnapshot(id, target, args, resp, data)
}

// EncodePeer is used to serialize a peer's address.
func (t *eventTransport) EncodePeer(id raft.ServerID, addr raft.ServerAddress) []byte {
	return t.trans.EncodePeer(id, addr)
}

// DecodePeer is used to deserialize a peer's address.
func (t *eventTransport) DecodePeer(data []byte) raft.ServerAddress {
	return t.trans.DecodePeer(data)
}

// SetHeartbeatHandler is used to setup a heartbeat handler
// as a fast-pass. This is to avoid head-of-line blocking from
// disk IO. If a Transport does not support this, it can simply
// ignore the call, and push the heartbeat onto the Consumer channel.
func (t *eventTransport) SetHeartbeatHandler(cb func(rpc raft.RPC)) {
	t.trans.SetHeartbeatHandler(cb)
}

func (t *eventTransport) Close() error {
	if closer, ok := t.trans.(raft.WithClose); ok {
		return closer.Close()
	}
	return nil
}

// AddPeer adds a new transport as peer of this transport. Once the other
// transport has become a peer, this transport will be able to send RPCs to it,
// if the peer object 'connected' flag is on.
func (t *eventTransport) AddPeer(transport *eventTransport) {
	t.peers.Add(t.id, transport.id)
	t.schedule.AddPeer(transport.id)
}

// Electing resets any leader-related state in this transport (such as the
// track of logs appended by the peers), and it connects the transport to all
// its peers, enabling it to send them RPCs. It must be called whenever the
// server associated with this transport is about to transition to the leader
// state, and before any append entries RPC is made.
func (t *eventTransport) Electing() {
	t.schedule.NoEvent()
	for _, peer := range t.peers.All() {
		peer.ResetLogs()
	}
	t.peers.Connect()
}

// Deposing disables connectivity from this transport to all its peers,
// allowing only append entries RPCs for peers that are lagging behind in terms
// of applied logs to be performed.
func (t *eventTransport) Deposing() {
	t.peers.SoftDisconnect()
}

// Disable connectivity from this transport to the given peer.
func (t *eventTransport) Disconnect(id raft.ServerID) {
	t.peers.Get(id).Disconnect()
}

// Re-nable connectivity from this transport to the given peer.
func (t *eventTransport) Reconnect(id raft.ServerID) {
	t.peers.Get(id).Reconnect()
}

// Returns true if all peers are connected, false otherwise.
//
// It panics if some nodes are connected and others are not.
func (t *eventTransport) Connected() bool {
	return t.peers.Connected()
}

// Returns true if the given peer is connected.
func (t *eventTransport) PeerConnected(id raft.ServerID) bool {
	return t.peers.Get(id).Connected()
}

// Returns true if this transport has appended logs to the given peer during
// the term.
func (t *eventTransport) HasAppendedLogsTo(id raft.ServerID) bool {
	peer := t.peers.Get(id)
	return peer.LogsCount() > 0
}

// Schedule the n'th command log to fail to be appended to the
// followers. Return an event that will fire when all followers have reached
// this failure.
func (t *eventTransport) ScheduleEnqueueFailure(n uint64) *event.Event {
	event := event.New()
	t.schedule.EnqueueFailure(n, event)
	return event
}

// Schedule the n'th command log to fail to acknowledge that it has been
// appended to the followers. Return an event that will fire when all followers
// have reached this failure.
func (t *eventTransport) ScheduleAppendFailure(n uint64) *event.Event {
	event := event.New()
	t.schedule.AppendFailure(n, event)
	return event
}
