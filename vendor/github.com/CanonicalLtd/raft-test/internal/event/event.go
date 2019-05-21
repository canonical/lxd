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

package event

// An Event that occurrs when certain log command is either enqueued, appended
// or committed. Events may be fired in the transport layer (i.e. in the
// eventTransport wrappers) or in the state machine layer (i.e. in the eventFSM
// wrapper).
type Event struct {
	fireCh chan struct{}
	ackCh  chan struct{}
}

// New creates a new event.
func New() *Event {
	return &Event{
		fireCh: make(chan struct{}),
		ackCh:  make(chan struct{}),
	}
}

// Watch the event. Return a channel that gets closed when the event gets
// fired.
func (e *Event) Watch() <-chan struct{} {
	return e.fireCh
}

// Fire the event. A watcher on the event will be awaken.
func (e *Event) Fire() {
	close(e.fireCh)
}

// Block until the watcher of the event has acknowledged that the event has
// been handled.
func (e *Event) Block() {
	<-e.ackCh
}

// Ack acknowledges that the event has been handled.
func (e *Event) Ack() {
	close(e.ackCh)
}
