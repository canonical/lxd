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
	"github.com/hashicorp/raft"
)

// Leadership represents the leadership acquired by a server that was elected
// as leader. It exposes methods to be notified about its loss, with the server
// stepping down as leader.
type Leadership struct {
	// ID of the raft server that acquired the leadership.
	id raft.ServerID

	// Notification about leadership being lost.
	lostCh chan struct{}
}

// Create new leadership object.
func newLeadership(id raft.ServerID, lostCh chan struct{}) *Leadership {
	return &Leadership{
		id:     id,
		lostCh: lostCh,
	}
}

// Lost returns a channel that gets closed when leadership is lost.
func (l *Leadership) Lost() chan struct{} {
	return l.lostCh
}
