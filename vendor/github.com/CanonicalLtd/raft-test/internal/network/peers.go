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
	"sync"

	"github.com/hashicorp/raft"
)

// Small wrapper around a map of raft.ServerID->peer, offering concurrency
// safety. This bit of information is not on faultyTransport directly, since it
// needs to be shared between faultyTransport and faultyPipeline.
type peers struct {
	peers map[raft.ServerID]*peer
	mu    sync.RWMutex
}

// Create a new empty peers map.
func newPeers() *peers {
	return &peers{
		peers: make(map[raft.ServerID]*peer),
	}
}

// Add a new peer for the given source and target server IDs.
func (p *peers) Add(source, target raft.ServerID) {
	p.peers[target] = newPeer(source, target)
}

// Get the peer with the given ID.
func (p *peers) Get(id raft.ServerID) *peer {
	// Sinces peers entries are inserted at initialization time by the
	// Cluster() function, and currently they never change afterwise,
	// there's no need to protect this method with the mutex.
	return p.peers[id]
}

// Return all the peers
func (p *peers) All() map[raft.ServerID]*peer {
	// Sinces peers entries are inserted at initialization time by the
	// Cluster() function, and currently they never change afterwise,
	// there's no need to protect this method with the mutex.
	return p.peers
}

// Enable connectivity to all the peers in this map.
func (p *peers) Connect() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, peer := range p.peers {
		peer.Connect()
	}
}

// Returns true if all peers are connected, false otherwise.
//
// It panics if some nodes are connected and others are not.
func (p *peers) Connected() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	connected := false
	for id, peer := range p.peers {
		if !connected {
			connected = peer.Connected()
		} else if !peer.Connected() {
			panic(fmt.Sprintf("server %s is not not connected while some others are", id))
		}
	}
	return connected
}

// Disable connectivity to all the peers in this map. However allow for peers
// that are lagging behind in terms of received entries to still receive
// AppendEntries RPCs.
func (p *peers) SoftDisconnect() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, peer := range p.peers {
		peer.SoftDisconnect()
	}
}

// Whether the given target peer is both disconnected from its source
// transport, and it's not syncing logs with other peers (i.e. either they are
// at the same index of the peer with the highest index of appended logs, or
// the peer has been hard-disconnected)
func (p *peers) DisconnectedAndNotSyncing(id raft.ServerID) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	for _, peer := range p.peers {
		peer.mu.RLock()
		defer peer.mu.RUnlock()
	}

	this := p.peers[id]
	if this.connected {
		return false
	}

	if !this.allowSyncing {
		return true
	}

	count := this.LogsCount()

	for _, other := range p.peers {
		if other.target == this.target {
			continue
		}
		if count < other.LogsCount() {
			return false
		}
	}

	return true
}

// Hold information about a single peer server that a faultyTransport is
// sending RPCs to.
type peer struct {
	// Server ID of the server sending RPCs to the peer.
	source raft.ServerID

	// Server ID of the peer server.
	target raft.ServerID

	// Whether connectivity is up. The transport can send RPCs to the peer
	// server only if this value is true.
	connected bool

	// Whether to allow appending entries to this peer even if the
	// connected field is false. Used for bringing the logs appended by a
	// peer in sync with the others.
	allowSyncing bool

	// Logs successfully appended to this peer since the server of the
	// transport we're associated with has acquired leadership. This keeps
	// only logs tagged with the same term the leader was elected at.
	logs []*raft.Log

	// Serialize access to internal state.
	mu sync.RWMutex
}

// Create a new peer for the given server.
func newPeer(source, target raft.ServerID) *peer {
	return &peer{
		target: target,
		logs:   make([]*raft.Log, 0),
	}
}

// Enable connectivity between the source transport and the target peer.
func (p *peer) Connect() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.connected {
		panic(fmt.Sprintf("server %s is already connected with server %s", p.source, p.target))
	}
	p.connected = true
	p.allowSyncing = false
}

// Disable connectivity between the source transport and the target
// peer.
func (p *peer) Disconnect() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.connected {
		panic(fmt.Sprintf("server %s is already disconnected from server %s", p.source, p.target))
	}
	p.connected = false
	p.allowSyncing = false
}

// Re-enables connectivity between the source transport and the target
// peer.
func (p *peer) Reconnect() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.connected {
		panic(fmt.Sprintf("server %s is already connected with server %s", p.source, p.target))
	}
	p.connected = true
	p.allowSyncing = false
}

// Disable connectivity between the source transport and the target
// peer. However allow for peers that are lagging behind in terms of received
// entries to still receive AppendEntries RPCs.
func (p *peer) SoftDisconnect() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.connected {
		panic(fmt.Sprintf("server %s is already disconnected from server %s", p.source, p.target))
	}
	p.connected = false
	p.allowSyncing = true
}

// Return whether this source transport is connected to the target peer.
func (p *peer) Connected() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.connected
}

// Reset all recorded logs. Should be called when a new server is elected.
func (p *peer) ResetLogs() {
	p.logs = p.logs[:0]
}

// This method updates the logs that the peer successfully appended. It must be
// called whenever the transport is confident that logs have been
// appended. There are two cases:
//
// - Transport.AppendEntries(): this is synchronous so UpdateLogs() can be invoked
//   as soon as the AppendEntries() call returns.
//
// - AppendPipeline.AppendEntries(): this is asynchronous, so UpdateLogs() should
//   be invoked only when the AppendFuture returned by AppendEntries() completes.
//
// In practice, the current implementation of faultyTransport and
// faultyPipeline is a bit sloppy about the above rules, since we can make some
// assumptions about the flow of entries. See comments in faultyTransport and
// faultyPipeline for more details.
func (p *peer) UpdateLogs(logs []*raft.Log) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(logs) == 0 {
		return // Nothing to do.
	}

	// Discard any log with an older term (relative to the others).
	newLogs := filterLogsWithOlderTerms(logs)

	// If no logs have been received yet, just append everything.
	if len(p.logs) == 0 {
		p.logs = newLogs
		return
	}

	// Check if we have stored entries for older terms, and if so, discard
	// them.
	//
	// We only need to check the first entry, because we always store
	// entries that all have the same term.
	if p.logs[0].Term < newLogs[0].Term {
		p.logs = p.logs[:0]
	}

	// Append new logs that aren't duplicates.
	for _, newLog := range newLogs {
		duplicate := false
		for _, log := range p.logs {
			if newLog.Index == log.Index {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		p.logs = append(p.logs, newLog)
	}
}

// Return then number of all logs appended so far to this peer.
func (p *peer) LogsCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return len(p.logs)
}

// Return then number of command logs appended so far to this peer.
func (p *peer) CommandLogsCount() uint64 {
	p.mu.RLock()
	defer p.mu.RUnlock()

	n := uint64(0)
	for _, log := range p.logs {
		if log.Type == raft.LogCommand {
			n++
		}
	}
	return n
}
