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

	"github.com/CanonicalLtd/raft-test/internal/event"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
)

// Network provides control over all transports of a cluster, injecting
// disconnections and failures.
type Network struct {
	logger hclog.Logger

	// Transport wrappers.
	transports map[raft.ServerID]*eventTransport
}

// New create a new network for controlling the underlying transports.
func New(logger hclog.Logger) *Network {
	return &Network{
		logger:     logger,
		transports: make(map[raft.ServerID]*eventTransport),
	}
}

// Add a new transport to the network. Returns a transport that wraps the given
// transport with instrumentation to inject disconnections and failures.
func (n *Network) Add(id raft.ServerID, trans raft.Transport) raft.Transport {
	transport := newEventTransport(n.logger, id, trans)

	for _, other := range n.transports {
		transport.AddPeer(other)
		other.AddPeer(transport)
	}

	n.transports[id] = transport
	return transport
}

// Electing resets any leader-related state in the transport associated with
// given server ID (such as the track of logs appended by the peers), and it
// connects the transport to all its peers, enabling it to send them RPCs. It
// must be called whenever the server associated with this transport is about
// to transition to the leader state, and before any append entries RPC is
// made.
func (n *Network) Electing(id raft.ServerID) {
	n.logger.Debug(fmt.Sprintf("[DEBUG] raft-test: server %s: establish outbound connection to all other nodes", id))

	// Sanity check that the network is fully disconnected at this time.
	for id, transport := range n.transports {
		if transport.Connected() {
			panic(fmt.Sprintf("expected a fully disconected network, but server %s is connected", id))
		}
	}

	transport := n.transports[id]
	transport.Electing()
}

// Deposing disables connectivity from the transport of the server with the
// given ID to all its peers, allowing only append entries RPCs for peers that
// are lagging behind in terms of applied logs to be performed.
func (n *Network) Deposing(id raft.ServerID) {
	n.logger.Debug(fmt.Sprintf("[DEBUG] raft-test: server %s: dropping outbound connection to all other nodes", id))
	n.transports[id].Deposing()
}

// ConnectAllServers establishes full cluster connectivity after an
// election. The given ID is the one of the leader, which is already connected.
func (n *Network) ConnectAllServers(id raft.ServerID) {
	// Sanity check that the network is fully disconnected at this time.
	for other, transport := range n.transports {
		if other == id {
			continue
		}
		transport.peers.Connect()
	}
}

// Disconnect disables connectivity from the transport of the leader
// server with the given ID to the peer with the given ID.
func (n *Network) Disconnect(id, follower raft.ServerID) {
	n.logger.Debug(fmt.Sprintf("[DEBUG] raft-test: server %s: disconnecting follower %s", id, follower))
	n.transports[id].Disconnect(follower)
}

// Reconnect re-enables connectivity from the transport of the leader
// server with the given ID to the peer with the given ID.
func (n *Network) Reconnect(id, follower raft.ServerID) {
	n.logger.Debug(fmt.Sprintf("[DEBUG] raft-test: server %s: reconnecting follower %s", id, follower))
	n.transports[id].Reconnect(follower)
}

// PeerConnected returns whether the peer with the given server ID is connected
// with the transport of the server with the given ID.
func (n *Network) PeerConnected(id, peer raft.ServerID) bool {
	return n.transports[id].PeerConnected(peer)
}

// Address returns the address of the server with the given id.
func (n *Network) Address(id raft.ServerID) raft.ServerAddress {
	return n.transports[id].LocalAddr()
}

// HasAppendedLogsFromTo returns true if at least one log entry has been appended
// by server with id1 to server with id2.
//
// It is assumed that id1 is a leader that has just been elected and has been
// trying to append a noop log to all its followers.
func (n *Network) HasAppendedLogsFromTo(id1, id2 raft.ServerID) bool {
	transport := n.transports[id1]
	return transport.HasAppendedLogsTo(id2)
}

// ScheduleEnqueueFailure will make all followers of the given server fail when
// the leader tries to append the n'th log command. Return an event that will
// fire when all of them have failed and will block them all until
// acknowledged.
func (n *Network) ScheduleEnqueueFailure(id raft.ServerID, command uint64) *event.Event {
	transport := n.transports[id]
	return transport.ScheduleEnqueueFailure(command)
}

// ScheduleAppendFailure will make all followers of the given leader server
// append the n'th log command sent by the leader, but they will fail to
// acknowledge the leader about it. Return an event that will fire when all of
// them have failed and will block them all until acknowledged.
func (n *Network) ScheduleAppendFailure(id raft.ServerID, command uint64) *event.Event {
	transport := n.transports[id]
	return transport.ScheduleAppendFailure(command)
}
