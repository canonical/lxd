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
	"strconv"
	"testing"
	"time"

	"github.com/CanonicalLtd/raft-test/internal/election"
	"github.com/CanonicalLtd/raft-test/internal/fsms"
	"github.com/CanonicalLtd/raft-test/internal/logging"
	"github.com/CanonicalLtd/raft-test/internal/network"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
)

// Cluster creates n raft servers, one for each of the given FSMs, and returns
// a Control object that can be used to create deterministic test scenarios,
// deciding which server is elected as leader and if and when a failure should
// happen during its term.
//
// Each raft.Raft instance is created with sane test-oriented default
// dependencies, which include:
//
// - very low configuration timeouts
// - in-memory transports
// - in-memory log and stable stores
// - in-memory snapshot stores
//
// You can tweak the default dependencies using the Config, Transport and
// LogStore options.
//
// All created raft servers will be part of the cluster and act as voting
// servers, unless the Servers option is used.
//
// If a GO_RAFT_TEST_LATENCY environment is found, the default configuration
// timeouts will be scaled up accordingly (useful when running tests on slow
// hardware). A latency of 1.0 is a no-op, since it just keeps the default
// values unchanged. A value greater than 1.0 increases the default timeouts by
// that factor. See also the Duration helper.
func Cluster(t testing.TB, fsms []raft.FSM, options ...Option) (map[raft.ServerID]*raft.Raft, *Control) {
	logger := logging.New(t, "DEBUG")
	logger.Debug(fmt.Sprintf("[DEBUG] raft-test: setup: start (%d servers)", len(fsms)))

	// Create a set of default dependencies for each server.
	dependencies := make([]*dependencies, len(fsms))
	for i, fsm := range fsms {
		dependencies[i] = newDefaultDependencies(t, logger, i, fsm)
	}

	// Customize the default dependencies by applying the given options.
	for _, option := range options {
		option(dependencies)
	}

	// Honor the GO_RAFT_TEST_LATENCY env var, if set.
	setTimeouts(dependencies)

	// Instrument the Config of each server with a NotifyCh and return a
	// leadership object for watching them.
	leadership := instrumentConfigs(t, logger, dependencies)

	// Instrument all servers by replacing their transports with transport
	// wrappers, creating a network object to control them.
	network := instrumentTransports(logger, dependencies)

	// Instrument all servers by replacing their fsms with wrapper fsms,
	// creating a watcher to observe them.
	watcher := instrumentFSMs(logger, dependencies)

	// Bootstrap the initial cluster configuration.
	bootstrapCluster(t, logger, dependencies)

	// Start the individual servers.
	servers := make(map[raft.ServerID]*raft.Raft)
	confs := make(map[raft.ServerID]*raft.Config)
	for _, d := range dependencies {
		id := d.Conf.LocalID
		logger.Debug(fmt.Sprintf("[DEBUG] raft-test: setup: server %s: start", id))
		raft, err := newRaft(d)
		if err != nil {
			logger.Debug(fmt.Sprintf("[DEBUG] raft-test: setup: error: server %s failed to start: %v", id, err))
		}
		confs[id] = d.Conf
		servers[id] = raft
	}

	// Create the Control instance for this cluster
	control := &Control{
		t:        t,
		logger:   logger,
		election: leadership,
		network:  network,
		watcher:  watcher,
		confs:    confs,
		servers:  servers,
	}

	logger.Debug("[DEBUG] raft-test: setup: done")

	return servers, control
}

// Option can be used to tweak the dependencies of test Raft servers created with
// Cluster() or Server().
type Option func([]*dependencies)

// Hold dependencies for a single dependencies.
type dependencies struct {
	Conf          *raft.Config
	FSM           raft.FSM
	Logs          raft.LogStore
	Stable        raft.StableStore
	Snaps         raft.SnapshotStore
	Configuration *raft.Configuration
	Trans         raft.Transport
	Voter         bool // Whether this is voter server in the initial configuration
}

// Create default dependencies for a single raft server.
func newDefaultDependencies(t testing.TB, logger hclog.Logger, i int, fsm raft.FSM) *dependencies {
	// Use the server's index as its server ID and address.
	addr := strconv.Itoa(i)
	_, transport := raft.NewInmemTransport(raft.ServerAddress(addr))

	conf := raft.DefaultConfig()
	conf.LocalID = raft.ServerID(addr)
	conf.Logger = logger

	// Set low timeouts.
	conf.HeartbeatTimeout = 15 * time.Millisecond
	conf.ElectionTimeout = 15 * time.Millisecond
	conf.CommitTimeout = 1 * time.Millisecond
	conf.LeaderLeaseTimeout = 10 * time.Millisecond

	// Set very high values to prevent snapshots to happen randomly.
	conf.SnapshotInterval = 24 * time.Hour
	conf.SnapshotThreshold = 4096

	// Set the snapshot to retain only one log, since the most common use
	// case is to test an FSM restore from a snapshot.
	conf.TrailingLogs = 1

	store := raft.NewInmemStore()
	return &dependencies{
		Conf:   conf,
		FSM:    fsm,
		Logs:   store,
		Stable: store,
		Snaps:  raft.NewInmemSnapshotStore(),
		Trans:  transport,
		Voter:  true,
	}
}

// Set scaled timeouts on all servers, to match GO_RAFT_TEST_LATENCY (if set).
func setTimeouts(dependencies []*dependencies) {
	for _, d := range dependencies {
		d.Conf.HeartbeatTimeout = Duration(d.Conf.HeartbeatTimeout)
		d.Conf.ElectionTimeout = Duration(d.Conf.ElectionTimeout)
		d.Conf.CommitTimeout = Duration(d.Conf.CommitTimeout)
		d.Conf.LeaderLeaseTimeout = Duration(d.Conf.LeaderLeaseTimeout)
	}
}

// Set leader notification channels on all servers.
func instrumentConfigs(t testing.TB, logger hclog.Logger, dependencies []*dependencies) *election.Tracker {
	t.Helper()

	tracker := election.NewTracker(logger)

	for _, d := range dependencies {
		id := d.Conf.LocalID
		if d.Conf.NotifyCh != nil {
			t.Fatalf("raft-test: setup: error: found NotifyCh on server %s set via Config option", id)
		}
		// Use an unbuffered channel, so raft will block on us.
		notifyCh := make(chan bool)
		d.Conf.NotifyCh = notifyCh
		tracker.Track(id, notifyCh)
	}

	return tracker
}

// Replace the dependencies.Trans object on each server with a faulty transport
// that wraps the real transport. Return a network object that knows about the
// these wrappers and that inject various kind of failures.
func instrumentTransports(logger hclog.Logger, dependencies []*dependencies) *network.Network {
	// Connect to each others all the servers that use a LoopbackTransport
	// (the default). However, actual connectivity control will be
	// performed by the network object
	connectLoobackTransports(dependencies)

	network := network.New(logger)

	for _, d := range dependencies {
		d.Trans = network.Add(d.Conf.LocalID, d.Trans)
	}

	return network
}

// Replace the dependencies.FSM object on each server with a wrapper FSM that
// wraps the real FSM. Return a watcher object that can be used to get notified
// of various events.
func instrumentFSMs(logger hclog.Logger, dependencies []*dependencies) *fsms.Watcher {
	watcher := fsms.New(logger)

	for _, d := range dependencies {
		d.FSM = watcher.Add(d.Conf.LocalID, d.FSM)
	}

	return watcher
}

// Connect loopback transports from servers that have them.
func connectLoobackTransports(dependencies []*dependencies) {
	loopbacks := make([]raft.LoopbackTransport, 0)
	for _, d := range dependencies {
		loopback, ok := d.Trans.(raft.LoopbackTransport)
		if ok {
			loopbacks = append(loopbacks, loopback)
		}
	}

	for i, t1 := range loopbacks {
		for j, t2 := range loopbacks {
			if i == j {
				continue
			}
			t1.Connect(t2.LocalAddr(), t2)
			t2.Connect(t1.LocalAddr(), t1)
		}
	}
}

// Bootstrap the cluster, including in the initial configuration of each voting
// server.
func bootstrapCluster(t testing.TB, logger hclog.Logger, dependencies []*dependencies) {
	t.Helper()

	// Figure out which servers should be part of the initial
	// configuration.
	servers := make([]raft.Server, 0)
	for _, d := range dependencies {
		id := d.Conf.LocalID
		if !d.Voter {
			// If the server is not initially part of the cluster,
			// there's nothing to do.
			logger.Debug(fmt.Sprintf("[DEBUG] raft-test: setup: server %s: skip bootstrap (not part of initial configuration)", id))
			continue
		}
		server := raft.Server{
			ID:      id,
			Address: d.Trans.LocalAddr(),
		}
		servers = append(servers, server)
	}

	// Create the initial cluster configuration.
	configuration := raft.Configuration{Servers: servers}
	for i := 0; i < len(dependencies); i++ {
		d := dependencies[i]
		id := d.Conf.LocalID
		if !d.Voter {
			continue
		}
		logger.Debug(fmt.Sprintf("[DEBUG] raft-test: setup: server %s: bootstrap", id))
		err := raft.BootstrapCluster(
			d.Conf,
			d.Logs,
			d.Stable,
			d.Snaps,
			d.Trans,
			configuration,
		)
		if err != nil {
			t.Fatalf("raft-test: setup: error: server %s failed to bootstrap: %v", id, err)
		}
	}

}

// Convenience around raft.NewRaft for creating a new Raft instance using the
// given dependencies.
func newRaft(d *dependencies) (*raft.Raft, error) {
	return raft.NewRaft(d.Conf, d.FSM, d.Logs, d.Stable, d.Snaps, d.Trans)
}
