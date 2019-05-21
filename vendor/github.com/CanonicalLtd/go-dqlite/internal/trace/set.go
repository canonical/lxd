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

package trace

import (
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"
)

// Set manages the lifecycle of a set of Tracers.
//
// When Tracer.Panic() is invoked on any of the tracers of this, the entries of
// all tracers in the set will be dumped as part of the panic message, ordered
// by timestamp.
type Set struct {
	tracers map[string]*Tracer // Index of available tracers by name.
	retain  int                // Number of entries each tracer will retain.

	mu sync.RWMutex

	// For testing only.
	now     now        // Function returning the current time.
	testing testing.TB // Emitted entries will also be sent to the test logger.
	node    int        // Index of the node emitting the entries.
}

// NewSet creates a new tracer Set.
//
// Each Set has a number of 'tracers', each holding a different buffer
// of trace entries, and each retaining at most 'retain' entrier.
func NewSet(retain int) *Set {
	return &Set{
		tracers: make(map[string]*Tracer),
		retain:  retain,
		now:     time.Now,
	}
}

// Add a new tracer to the registry.
func (s *Set) Add(name string) *Tracer {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, ok := s.tracers[name]
	if ok {
		panic(fmt.Sprintf("a tracer named %s is already registered", name))
	}
	buffer := newBuffer(s.retain)
	tracer := newTracer(s, name, buffer)
	s.tracers[name] = tracer
	return tracer
}

// Get the tracer with the given name, add one if does not exists.
func (s *Set) Get(name string) *Tracer {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tracer, ok := s.tracers[name]
	if !ok {
		panic(fmt.Sprintf("no tracer named %s is registered", name))
	}
	return tracer
}

// Del removes the tracer with the given name.
func (s *Set) Del(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, ok := s.tracers[name]
	if !ok {
		panic(fmt.Sprintf("no tracer named %s is registered", name))
	}
	delete(s.tracers, name)
}

// String returns a string representing all current entries, in all current
// tracers, ordered by timestamp.
func (s *Set) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries := make([]struct {
		e entry   // Actual entry object
		t *Tracer // Tracer that emitted the entry
	}, 0)

	for _, tracer := range s.tracers {
		for _, e := range tracer.buffer.Entries() {
			entries = append(entries, struct {
				e entry
				t *Tracer
			}{e, tracer})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].e.timestamp.Before(entries[j].e.timestamp)
	})

	result := ""

	for _, entry := range entries {
		result += fmt.Sprintf(
			"%s: %s: %s\n", entry.e.Timestamp(), entry.t.name, entry.e.Message())
	}

	return result
}

// Testing sets the tracers to log emitted entries through the given testing
// instance.
func (s *Set) Testing(t testing.TB, node int) {
	s.testing = t
	s.node = node
}
