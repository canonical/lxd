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

package election

import (
	"fmt"
	"time"

	"github.com/hashicorp/raft"
)

// Future represents a request to acquire leadership that will eventually
// succeed or fail.
type Future struct {
	// ID of the raft server that should acquire leadership.
	id raft.ServerID

	// If leadership is not acquire within this timeout, the future fails.
	timeout time.Duration

	// Notification about leadership being acquired.
	acquiredCh chan struct{}

	// Notification about leadership being lost.
	lostCh chan struct{}
}

// Creates a new leadership future of the given server.
func newFuture(id raft.ServerID, timeout time.Duration) *Future {
	future := &Future{
		id:         id,
		timeout:    timeout,
		acquiredCh: make(chan struct{}),
		lostCh:     make(chan struct{}),
	}
	return future
}

// Done returns a Leadership object if leadership was acquired withing the
// timeout, or an error otherwise.
func (f *Future) Done() (*Leadership, error) {
	select {
	case <-f.acquiredCh:
		leadership := newLeadership(f.id, f.lostCh)
		return leadership, nil
	case <-time.After(f.timeout):
		return nil, fmt.Errorf("server %s: leadership not acquired within %s", f.id, f.timeout)
	}
}
