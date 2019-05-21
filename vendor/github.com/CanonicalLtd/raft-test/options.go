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
	"io/ioutil"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
)

// Config sets a hook for tweaking the raft configuration of individual nodes.
func Config(f func(int, *raft.Config)) Option {
	return func(nodes []*dependencies) {
		for i, node := range nodes {
			f(i, node.Conf)
		}
	}
}

// LogStore can be used to create custom log stores.
//
// The given function takes a node index as argument and returns the LogStore
// that the node should use.
func LogStore(factory func(int) raft.LogStore) Option {
	return func(nodes []*dependencies) {
		for i, node := range nodes {
			node.Logs = factory(i)
		}
	}
}

// Transport can be used to create custom transports.
//
// The given function takes a node index as argument and returns the Transport
// that the node should use.
//
// If the transports returned by the factory do not implement
// LoopbackTransport, the Disconnect API won't work.
func Transport(factory func(int) raft.Transport) Option {
	return func(nodes []*dependencies) {
		for i, node := range nodes {
			node.Trans = factory(i)
		}
	}
}

// Latency is a convenience around Config that scales the values of the various
// raft timeouts that would be set by default by Cluster.
//
// This option is orthogonal to the GO_RAFT_TEST_LATENCY environment
// variable. If this option is used and GO_RAFT_TEST_LATENCY is set, they will
// compound. E.g. passing a factor of 2.0 to this option and setting
// GO_RAFT_TEST_LATENCY to 3.0 will have the net effect that default timeouts
// are scaled by a factor of 6.0.
func Latency(factor float64) Option {
	return Config(func(i int, config *raft.Config) {
		timeouts := []*time.Duration{
			&config.HeartbeatTimeout,
			&config.ElectionTimeout,
			&config.LeaderLeaseTimeout,
			&config.CommitTimeout,
		}
		for _, timeout := range timeouts {
			*timeout = scaleDuration(*timeout, factor)
		}
	})
}

// DiscardLogger is a convenience around Config that sets the output stream of
// raft's logger to ioutil.Discard.
func DiscardLogger() Option {
	return Config(func(i int, config *raft.Config) {
		config.Logger = hclog.New(&hclog.LoggerOptions{
			Name: "raft-test",
			Output: ioutil.Discard})
	})
}

// Servers can be used to indicate which nodes should be initially part of the
// created cluster.
//
// If this option is not used, the default is to have all nodes be part of the
// cluster.
func Servers(indexes ...int) Option {
	return func(nodes []*dependencies) {
		for _, node := range nodes {
			node.Voter = false
		}
		for _, index := range indexes {
			nodes[index].Voter = true
		}
	}
}
