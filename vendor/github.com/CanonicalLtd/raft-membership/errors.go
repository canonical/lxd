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
	"fmt"
)

// ErrDifferentLeader is returned by ChangeRequest.Error() when the
// request to join or leave a cluster failed because the target peer
// is not the leader. The network address of the leader as currently
// known by the target peer is attached to the error, so clients can
// perform again the request, this time using the given leader address
// as target peer.
type ErrDifferentLeader struct {
	leader string
}

// Leader is the address of the leader as currently known.
func (e *ErrDifferentLeader) Leader() string {
	return e.leader
}

func (e *ErrDifferentLeader) Error() string {
	return fmt.Sprintf("node is not leader, current leader at: %s", e.leader)
}

// ErrUnknownLeader is returned by ChangeRequest.Error() when the
// request to join or leave a cluster failed because the target peer
// is not the leader, and at the moment it also does not know the
// address of the leader (this can happen for example during leader
// elections). Clients typically want to retry after a short time.
type ErrUnknownLeader struct{}

func (e *ErrUnknownLeader) Error() string {
	return "node is not leader, current leader unknown"
}
