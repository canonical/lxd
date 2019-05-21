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

package raftmembership

import (
	"time"

	"github.com/hashicorp/raft"
)

// Changer is an API that can be used by a raft server to change its
// membership in a cluster (i.e. either join it or leave it).
//
// It works by using some transport layer (e.g. HTTP, TCP, etc) to
// send a membership change request to a target server that is part of
// the cluster and that can handle such requests, possibly redirecting
// the requesting server to another server (e.g. the cluster leader).
//
// It is effectively an extensions of the raft.Transport interface,
// with additional semantics for joining/leaving a raft cluster.
type Changer interface {
	Join(raft.ServerID, raft.ServerAddress, time.Duration) error
	Leave(raft.ServerID, raft.ServerAddress, time.Duration) error
}
