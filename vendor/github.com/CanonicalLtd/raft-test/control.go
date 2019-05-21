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
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/CanonicalLtd/raft-test/internal/election"
	"github.com/CanonicalLtd/raft-test/internal/event"
	"github.com/CanonicalLtd/raft-test/internal/fsms"
	"github.com/CanonicalLtd/raft-test/internal/network"
	"github.com/hashicorp/raft"
	"github.com/hashicorp/go-hclog"
)

// Control the events happening in a cluster of raft servers, such has leadership
// changes, failures and shutdowns.
type Control struct {
	t        testing.TB
	logger   hclog.Logger
	election *election.Tracker
	network  *network.Network
	watcher  *fsms.Watcher
	confs    map[raft.ServerID]*raft.Config
	servers  map[raft.ServerID]*raft.Raft
	errored  bool
	deposing chan struct{}

	// Current Term after Elect() was called, if any.
	term *Term

	// Future of any pending snapshot that has been scheduled with an
	// event.
	snapshotFuture raft.SnapshotFuture
}

// Close the control for this raft cluster, shutting down all servers and
// stopping all monitoring goroutines.
//
// It must be called by every test creating a test cluster with Cluster().
func (c *Control) Close() {
	c.logger.Debug("[DEBUG] raft-test: close: start")

	// First tell the election tracker that we don't care anymore about
	// notifications. Any value received from the NotifyCh's will be dropped
	// on the floor.
	c.election.Ignore()

	// Now shutdown the servers.
	c.shutdownServers()

	// Finally shutdown the election tracker since nothing will be
	// sending to NotifyCh's.
	c.election.Close()

	c.logger.Debug("[DEBUG] raft-test: close: done")
}

// Elect a server as leader.
//
// When calling this method there must be no leader in the cluster and server
// transports must all be disconnected from eacher.
func (c *Control) Elect(id raft.ServerID) *Term {
	c.t.Helper()

	c.logger.Debug(fmt.Sprintf("[DEBUG] raft-test: elect: start (server %s)", id))

	// Wait for the current leader (if any) to be fully deposed.
	if c.deposing != nil {
		<-c.deposing
	}

	// Sanity check that no server is the leader.
	for id, r := range c.servers {
		if r.State() == raft.Leader {
			c.t.Fatalf("raft-test: error: cluster has already a leader (server %s)", id)
		}
	}

	// We might need to repeat the logic below a few times in case a
	// follower hits its heartbeat timeout before the leader has chance to
	// append entries to it and refresh the last contact timestamp (hence
	// transitioning to candidate and starting a new election).
	for n := 0; n < maxElectionRounds; n++ {
		leadership := c.waitLeadershipAcquired(id)

		// We did not acquire leadership, let's retry.
		if leadership == nil {
			if n < maxElectionRounds {
				c.logger.Debug(fmt.Sprintf("[DEBUG] raft-test: elect: server %s: retry %d ", id, n+1))
				continue
			}
		}

		// The given node became the leader, let's make sure
		// that leadership is stable and that other nodes
		// become followers.
		if !c.waitLeadershipPropagated(id, leadership) {
			if n < maxElectionRounds {
				c.logger.Debug(fmt.Sprintf("[DEBUG] raft-test: elect: server %s: retry %d ", id, n+1))
				continue
			}
		}
		// Now establish all remaining connections. E.g. for three nodes:
		//
		// L  <--- F1
		// L  <--- F2
		//
		// and:
		//
		// F1 <--- F2
		// F1 ---> F2
		//
		// This way the cluster is fully connected. foo
		c.logger.Debug("[DEBUG] raft-test: elect: done")
		term := &Term{
			control:    c,
			id:         id,
			leadership: leadership,
		}
		c.term = term

		return term
	}
	c.t.Fatalf("raft-test: server %s: did not acquire stable leadership", id)

	return nil
}

// Barrier is used to wait for the cluster to settle to a stable state, where
// all in progress Apply() commands are committed across all FSM associated
// with servers that are not disconnected and all in progress snapshots and
// restores have been performed.
//
// Usually you don't wan't to concurrently keep invoking Apply() on the cluster
// raft instances while Barrier() is running.
func (c *Control) Barrier() {
	// Wait for snapshots to complete.
	if c.snapshotFuture != nil {
		if err := c.snapshotFuture.Error(); err != nil {
			c.t.Fatalf("raft-test: snapshot failed: %v", err)
		}
	}

	// Wait for inflight commands to be applied to the leader's FSM.
	if c.term.id != "" {
		// Set a relatively high timeout.
		//
		// TODO: let users specify the maximum amount of time a single
		// Apply() to their FSM should take, and calculate this value
		// accordingly.
		timeout := Duration(time.Second)

		if err := c.servers[c.term.id].Barrier(timeout).Error(); err != nil {
			c.t.Fatalf("raft-test: leader barrier: %v", err)
		}

		// Wait for follower FSMs to catch up.
		n := c.Commands(c.term.id)
		events := make([]*event.Event, 0)
		for id := range c.servers {
			if id == c.term.id {
				continue
			}
			// Skip disconnected followers.
			if !c.network.PeerConnected(c.term.id, id) {
				continue
			}
			event := c.watcher.WhenApplied(id, n)
			events = append(events, event)
		}
		for _, event := range events {
			<-event.Watch()
			event.Ack()
		}
	}
}

// Depose the current leader.
//
// When calling this method a leader must have been previously elected with
// Elect().
//
// It must not be called if the current term has scheduled a depose action with
// Action.Depose().
func (c *Control) Depose() {
	event := event.New()
	go c.deposeUponEvent(event, c.term.id, c.term.leadership)
	event.Fire()
	event.Block()
}

// Commands returns the total number of command logs applied by the FSM of the
// server with the given ID.
func (c *Control) Commands(id raft.ServerID) uint64 {
	return c.watcher.Commands(id)
}

// Snapshots returns the total number of snapshots performed by the FSM of the
// server with the given ID.
func (c *Control) Snapshots(id raft.ServerID) uint64 {
	return c.watcher.Snapshots(id)
}

// Restores returns the total number of restores performed by the FSM of the
// server with the given ID.
func (c *Control) Restores(id raft.ServerID) uint64 {
	return c.watcher.Restores(id)
}

// Shutdown all raft nodes and fail the test if any of them errors out while
// doing so.
func (c *Control) shutdownServers() {
	// Find the leader if there is one, and shut it down first. This should
	// prevent it from getting stuck on shutdown while trying to send RPCs
	// to the followers.
	//
	// TODO: this is arguably a workaround for a bug in the transport
	// wrapper.
	ids := make([]raft.ServerID, 0)
	for id, r := range c.servers {
		if r.State() == raft.Leader {
			c.shutdownServer(id)
			ids = append(ids, id)
		}
	}

	// Shutdown the rest.
	for id := range c.servers {
		hasShutdown := false
		for i := range ids {
			if ids[i] == id {
				hasShutdown = true
				break
			}
		}
		if !hasShutdown {
			c.shutdownServer(id)
			ids = append(ids, id)
		}
	}
}

// Shutdown a single server.
func (c *Control) shutdownServer(id raft.ServerID) {
	r := c.servers[id]
	future := r.Shutdown()

	// Expect the shutdown to happen within two seconds by default.
	timeout := Duration(2 * time.Second)

	// Watch for errors.
	ch := make(chan error, 1)
	go func(future raft.Future) {
		ch <- future.Error()
	}(future)

	var err error
	select {
	case err = <-ch:
		c.logger.Debug(fmt.Sprintf("[DEBUG] raft-test: close: server %s: shutdown done", id))
	case <-time.After(timeout):
		err = fmt.Errorf("timeout (%s)", timeout)
	}
	if err == nil {
		return
	}

	c.logger.Debug(fmt.Sprintf("[DEBUG] raft-test: close: server %s: shutdown failed: %s", id, err))

	buf := make([]byte, 1<<16)
	n := runtime.Stack(buf, true)

	c.t.Errorf("\n\t%s", buf[:n])
	c.t.Fatalf("raft-test: close: error: server %s: shutdown error: %v", id, err)
}

// Wait for the given server to acquire leadership. Returns true on success,
// false otherwise (i.e. if the timeout expires).
func (c *Control) waitLeadershipAcquired(id raft.ServerID) *election.Leadership {
	timeout := maximumElectionTimeout(c.confs) * maxElectionRounds
	future := c.election.Expect(id, timeout)

	c.watcher.Electing(id)

	// Reset any leader-related state on the transport of the given server
	// and connect it to all other servers, letting it send them RPCs
	// messages but not viceversa. E.g. for three nodes:
	//
	// L ---> F1
	// L ---> F2
	//
	// This way we are sure we are the only server that can possibly acquire
	// leadership.
	c.network.Electing(id)

	// First wait for the given node to become leader.
	c.logger.Debug(fmt.Sprintf("[DEBUG] raft-test: elect: server %s: wait to become leader within %s", id, timeout))

	leadership, err := future.Done()
	if err != nil {
		c.logger.Debug(fmt.Sprintf("[DEBUG] raft-test: elect: server %s: did not become leader", id))
	}
	return leadership

}

// Wait that the leadership just acquired by server with the given id is
// acknowledged by all other servers and they all permanently transition to the
// follower state.
func (c *Control) waitLeadershipPropagated(id raft.ServerID, leadership *election.Leadership) bool {
	// The leadership propagation needs to happen within the leader lease
	// timeout, otherwise the newly elected leader will step down.
	timeout := maximumLeaderLeaseTimeout(c.confs)
	c.logger.Debug(fmt.Sprintf("[DEBUG] raft-test: elect: server %s: wait for other servers to become followers within %s", id, timeout))

	// Get the current configuration, so we wait only for servers that are
	// actually currently part of the cluster (some of them might have been
	// excluded with the Servers option).
	r := c.servers[id]
	future := r.GetConfiguration()
	if err := future.Error(); err != nil {
		c.t.Fatalf("raft-test: control: server %s: failed to get configuration: %v", id, err)
	}
	servers := future.Configuration().Servers

	timer := time.After(timeout)
	address := c.network.Address(id)
	for _, server := range servers {
		other := server.ID
		if other == id {
			continue
		}
		r := c.servers[server.ID]
		for {
			// Check that we didn't lose leadership in the meantime.
			select {
			case <-leadership.Lost():
				c.network.Deposing(id)
				c.logger.Debug(fmt.Sprintf("[DEBUG] raft-test: elect: server %s: lost leadership", id))
				return false
			case <-timer:
				c.t.Fatalf("raft-test: elect: server %s: followers did not settle", id)
			default:
			}

			// Check that this server is in follower mode, that it
			// has set the elected sever as leader and that we were
			// able to append at least one log entry to it (when a
			// server becomes leader, it always sends a LogNoop).
			if r.State() == raft.Follower && r.Leader() == address && c.network.HasAppendedLogsFromTo(id, other) {
				c.logger.Debug(fmt.Sprintf("[DEBUG] raft-test: elect: server %s: became follower", other))
				break
			}
			time.Sleep(time.Millisecond)
		}
	}

	return true
}

// Return an event that gets fired when the n'th log command gets enqueued by
// the given leader server.
func (c *Control) whenCommandEnqueued(id raft.ServerID, n uint64) *event.Event {
	return c.network.ScheduleEnqueueFailure(id, n)
}

// Return an event that gets fired when the n'th log command gets appended by
// server with the given ID (which is supposed to be the leader) to all other
// servers.
func (c *Control) whenCommandAppended(id raft.ServerID, n uint64) *event.Event {
	return c.network.ScheduleAppendFailure(id, n)
}

// Return an event that gets fired when the n'th log command gets committed on
// server with the given ID (which is supposed to be the leader).
func (c *Control) whenCommandCommitted(id raft.ServerID, n uint64) *event.Event {
	return c.watcher.WhenApplied(id, n)
}

// Depose the server with the given ID when the given event fires.
func (c *Control) deposeUponEvent(event *event.Event, id raft.ServerID, leadership *election.Leadership) {
	// Sanity checks.
	r := c.servers[id]
	if r.State() != raft.Leader {
		panic(fmt.Errorf("raft-test: server %s: is not leader", id))
	}

	<-event.Watch()

	c.network.Deposing(id)

	timeout := maximumLeaderLeaseTimeout(c.confs)

	c.logger.Debug(fmt.Sprintf("[DEBUG] raft-test: node %s: state: wait leadership lost (timeout=%s)", id, timeout))

	select {
	case <-leadership.Lost():
	case <-time.After(timeout):
		c.t.Errorf("raft-test: server %s: error: timeout: leadership not lost", id)
		c.errored = true
	}
	event.Ack()

	if !c.errored {
		c.logger.Debug(fmt.Sprintf("[DEBUG] raft-test: server %s: leadership lost", id))
	}

	c.deposing <- struct{}{}
	c.deposing = nil
	c.term = nil
}

// Take a snapshot on the server with the given ID when the given event fires.
func (c *Control) snapshotUponEvent(event *event.Event, id raft.ServerID) {
	<-event.Watch()

	c.logger.Debug(fmt.Sprintf("[DEBUG] raft-test: server %s: control: take snapshot", id))

	r := c.servers[id]
	c.snapshotFuture = r.Snapshot()

	event.Ack()
}

// Compute the maximum time a leader election should take, according to the
// given nodes configs.
func maximumElectionTimeout(confs map[raft.ServerID]*raft.Config) time.Duration {
	timeout := time.Duration(0)

	for _, conf := range confs {
		if conf.ElectionTimeout > timeout {
			timeout = conf.ElectionTimeout
		}
	}

	return timeout * timeoutRandomizationFactor
}

// Return the maximum leader lease timeout among the given nodes configs.
func maximumLeaderLeaseTimeout(confs map[raft.ServerID]*raft.Config) time.Duration {
	timeout := time.Duration(0)

	for _, conf := range confs {
		if conf.LeaderLeaseTimeout > timeout {
			timeout = conf.LeaderLeaseTimeout
		}
	}

	// Multiply the timeout by three to account for randomization.
	return timeout * timeoutRandomizationFactor
}

const (
	// Assume that a leader is elected within 25 rounds. Should be safe enough.
	maxElectionRounds = 25

	// Hashicorp's raft implementation randomizes timeouts between 1x and
	// 2x. Multiplying by 4x makes it sure to expire the timeout.
	timeoutRandomizationFactor = 4
)

// WaitLeader blocks until the given raft instance sets a leader (which
// could possibly be the instance itself).
//
// It fails the test if this doesn't happen within the specified timeout.
func WaitLeader(t testing.TB, raft *raft.Raft, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	waitLeader(ctx, t, raft)
}

func waitLeader(ctx context.Context, t testing.TB, raft *raft.Raft) {
	t.Helper()

	check := func() bool {
		return raft.Leader() != ""
	}
	wait(ctx, t, check, 25*time.Millisecond, "no leader was set")
}

// Poll the given function at the given internval, until it returns true, or
// the given context expires.
func wait(ctx context.Context, t testing.TB, f func() bool, interval time.Duration, message string) {
	t.Helper()

	start := time.Now()
	for {
		select {
		case <-ctx.Done():
			if err := ctx.Err(); err == context.Canceled {
				return
			}
			t.Fatalf("%s within %s", message, time.Since(start))
		default:
		}
		if f() {
			return
		}
		time.Sleep(interval)
	}
}
