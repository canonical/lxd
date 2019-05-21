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

package registry

import (
	"fmt"
	"sync/atomic"

	"github.com/CanonicalLtd/go-dqlite/internal/bindings"
)

// ConnLeaderAdd adds a new leader connection to the registry.
func (r *Registry) ConnLeaderAdd(filename string, conn *bindings.Conn) {
	r.connAdd(conn)
	r.leaders[conn] = filename

	// Add a tracer specific to this connection. It will be used by
	// replication.Methods when firing replication hooks triggered by WAL
	// events on this connection.
	r.tracers.Add(fmt.Sprintf("methods %d", r.ConnSerial(conn)))
}

// ConnLeaderDel removes the given leader connection from the registry.
func (r *Registry) ConnLeaderDel(conn *bindings.Conn) {
	// Dell the connection-specific tracer.
	r.tracers.Del(fmt.Sprintf("methods %d", r.ConnSerial(conn)))

	r.connDel(conn)
	delete(r.leaders, conn)

}

// ConnLeaderFilename returns the filename of the database associated with the
// given leader connection.
//
// If conn is not a registered leader connection, this method will panic.
func (r *Registry) ConnLeaderFilename(conn *bindings.Conn) string {
	name, ok := r.leaders[conn]
	if !ok {
		panic("no database for the given connection")
	}
	return name
}

// ConnLeaders returns all open leader connections for the database with
// the given filename.
func (r *Registry) ConnLeaders(filename string) []*bindings.Conn {
	conns := []*bindings.Conn{}
	for conn := range r.leaders {
		if r.leaders[conn] == filename {
			conns = append(conns, conn)
		}
	}
	return conns
}

// ConnFollowerAdd adds a new follower connection to the registry.
//
// If a follower connection for the database with the given filename is already
// registered, this method panics.
func (r *Registry) ConnFollowerAdd(filename string, conn *bindings.Conn) {
	r.connAdd(conn)
	r.followers[filename] = conn
}

// ConnFollowerDel removes the follower registered against the database with the
// given filename.
func (r *Registry) ConnFollowerDel(filename string) {
	conn, ok := r.followers[filename]
	if !ok {
		panic(fmt.Sprintf("follower connection for '%s' is not registered", filename))
	}

	delete(r.followers, filename)
	r.connDel(conn)
}

// ConnFollowerFilenames returns the filenames for all databases which currently
// have registered follower connections.
func (r *Registry) ConnFollowerFilenames() []string {
	names := []string{}
	for name := range r.followers {
		names = append(names, name)
	}
	return names
}

// ConnFollower returns the follower connection used to replicate the
// database identified by the given filename.
//
// If there's no follower connection registered for the database with the given
// filename, this method panics.
func (r *Registry) ConnFollower(filename string) *bindings.Conn {
	conn, ok := r.followers[filename]
	if !ok {
		panic(fmt.Sprintf("no follower connection for '%s'", filename))
	}
	return conn
}

// ConnFollowerExists checks whether the registry has a follower connection registered
// against the database with the given filename.
func (r *Registry) ConnFollowerExists(filename string) bool {
	_, ok := r.followers[filename]
	return ok
}

// ConnSerial returns a serial number uniquely identifying the given registered
// connection.
func (r *Registry) ConnSerial(conn *bindings.Conn) uint64 {
	serial, ok := r.serial[conn]

	if !ok {
		panic("connection is not registered")
	}

	return serial
}

// Add a new connection (either leader or follower) to the registry and assign
// it a serial number.
func (r *Registry) connAdd(conn *bindings.Conn) {
	if serial, ok := r.serial[conn]; ok {
		panic(fmt.Sprintf("connection is already registered with serial %d", serial))
	}

	atomic.AddUint64(&serial, 1)
	r.serial[conn] = serial
}

// Delete a connection (either leader or follower) from the registry
func (r *Registry) connDel(conn *bindings.Conn) {
	if _, ok := r.serial[conn]; !ok {
		panic("connection is not registered")
	}

	delete(r.serial, conn)
}

// Monotonic counter for identifying connections for tracing and debugging
// purposes.
var serial uint64
