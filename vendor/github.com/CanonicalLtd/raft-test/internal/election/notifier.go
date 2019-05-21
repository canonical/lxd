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

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
)

// Notifiy about leadership changes in a single raft server.
type notifier struct {
	// For debugging raft-test itself or its consumers.
	logger hclog.Logger

	// ID of the raft server we're observing.
	id raft.ServerID

	// Reference to the Config.NotifyCh object set in this server's Config.
	notifyCh chan bool

	// Channel used to tell the notification loop to expect the server to
	// acquire leadership. The leadership future sent to this channel will
	// be used both for notifying that leadership was acquired.
	futureCh chan *Future

	// Channel used to tell the notification loop to ignore any
	// notification received from the notifyCh.
	ignoreCh chan struct{}

	// Stop observing leadership changes when this channel gets closed.
	shutdownCh chan struct{}
}

// Create a new notifier.
func newNotifier(logger hclog.Logger, id raft.ServerID, notifyCh chan bool) *notifier {
	observer := &notifier{
		logger:     logger,
		id:         id,
		notifyCh:   notifyCh,
		futureCh:   make(chan *Future),
		ignoreCh:   make(chan struct{}),
		shutdownCh: make(chan struct{}),
	}
	go observer.start()
	return observer
}

// Ignore any notifications received on the notifyCh.
func (n *notifier) Ignore() {
	close(n.ignoreCh)
}

// Close stops observing leadership changes.
func (n *notifier) Close() {
	n.shutdownCh <- struct{}{}
	<-n.shutdownCh
}

// Acquired returns a Leadership object when the server acquires leadership, or
// an error if the timeout expires.
//
// It must be called before this server has any chance to become leader
// (e.g. it's disconnected from the other servers).
//
// Once called, it must not be called again until leadership is lost.
func (n *notifier) Acquired(timeout time.Duration) *Future {
	future := newFuture(n.id, timeout)
	n.futureCh <- future
	return future
}

// Start observing leadership changes using the notify channel of our server
// and eed notification to our consumers.
//
// The loop will be terminated once the stopCh is closed.
func (n *notifier) start() {
	// Record the last leadership change observation. For asserting that a
	// leadership lost notification always follows a leadership acquired
	// one.
	var last bool

	// Record the last request for leadership change for this server, if
	// any.
	var future *Future
	for {
		select {
		case f := <-n.futureCh:
			if future != nil {
				panic(fmt.Sprintf("server %s: duplicate leadership request", n.id))
			}
			future = f
		case acquired := <-n.notifyCh:
			ignore := false
			select {
			case <-n.ignoreCh:
				// Just drop the notification on the floor.
				ignore = true
			default:
			}
			if ignore {
				break
			}
			if future == nil {
				panic(fmt.Sprintf("server %s: unexpected leadership change", n.id))
			}
			verb := ""
			var ch chan struct{}
			if acquired {
				verb = "acquired"
				ch = future.acquiredCh
			} else {
				verb = "lost"
				ch = future.lostCh
				future = nil

			}
			if acquired == last {
				panic(fmt.Sprintf("server %s %s leadership twice in a row", n.id, verb))
			}
			last = acquired
			n.logger.Debug(fmt.Sprintf("[DEBUG] raft-test: server %s: leadership: %s", n.id, verb))
			select {
			case <-ch:
				panic(fmt.Sprintf("server %s: duplicate leadership %s notification", n.id, verb))
			default:
				close(ch)
			}
		case <-n.shutdownCh:
			n.logger.Debug(fmt.Sprintf("[DEBUG] raft-test: server %s: leadership: stop watching", n.id))
			close(n.shutdownCh)
			return
		}
	}
}
