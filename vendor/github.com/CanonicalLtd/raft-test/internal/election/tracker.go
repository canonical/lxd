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
	"sync"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
)

// Tracker consumes the raft.Config.NotifyCh set on each server of a cluster,
// tracking when elections occur.
type Tracker struct {
	// For debugging raft-test itself or its consumers.
	logger hclog.Logger

	// Watchers for individual servers.
	//
	// Note that this map is not protected by a mutex, since it should be
	// written once when the cluster is created, and never written again.
	observers map[raft.ServerID]*notifier

	// Flag indicating if Acquired() has been called on this Observer. It's
	// used to as sanity check that Add() is not called after the first
	// call to Acquired().
	observing bool

	// Current leadership future, if any. It's used as sanity check to
	// prevent further leadership requests.
	future *Future

	// Serialize access to internal state.
	mu sync.Mutex
}

// NewTracker creates a new Tracker for watching leadership
// changes in a raft cluster.
func NewTracker(logger hclog.Logger) *Tracker {
	return &Tracker{
		logger:    logger,
		observers: make(map[raft.ServerID]*notifier),
	}
}

// Ignore stops propagating leadership change notifications, which will be
// simply dropped on the floor. Should be called before the final Close().
func (t *Tracker) Ignore() {
	for _, observer := range t.observers {
		observer.Ignore()
	}
}

// Close stops watching for leadership changes in the cluster.
func (t *Tracker) Close() {
	for _, observer := range t.observers {
		observer.Close()
	}
}

// Track leadership changes on the server with the given ID using the given
// Config.NotifyCh.
func (t *Tracker) Track(id raft.ServerID, notifyCh chan bool) {
	if t.observing {
		panic("can't track new server while observing")
	}
	if _, ok := t.observers[id]; ok {
		panic(fmt.Sprintf("an observer for server %s is already registered", id))
	}
	t.observers[id] = newNotifier(t.logger, id, notifyCh)
}

// Expect returns an election Future object whose Done() method will return
// a Leadership object when the server with the given ID acquires leadership,
// or an error if the given timeout expires.
//
// It must be called before this server has any chance to become leader
// (e.g. it's disconnected from the other servers).
//
// Once called, it must not be called again until leadership is lost.
func (t *Tracker) Expect(id raft.ServerID, timeout time.Duration) *Future {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.observing = true

	if t.future != nil {
		select {
		case <-t.future.lostCh:
			// Leadership was acquired, but has been lost, so let's proceed.
			t.future = nil
		default:
			panic(fmt.Sprintf("server %s has already requested leadership", t.future.id))
		}
	}

	t.future = t.observers[id].Acquired(timeout)
	return t.future
}
