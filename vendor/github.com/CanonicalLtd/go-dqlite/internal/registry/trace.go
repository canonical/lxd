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

	"github.com/CanonicalLtd/go-dqlite/internal/bindings"
	"github.com/CanonicalLtd/go-dqlite/internal/trace"
)

// TracerFSM returns the tracer that should be used by the replication.FSM
// instance associated with this registry.
func (r *Registry) TracerFSM() *trace.Tracer {
	return r.tracers.Get("fsm")
}

// TracerConn returns the tracer that should be used by the replication.Methods
// instance associated with this registry when running the given hook for the
// given connection, which is assumed to be a registered leader connection.
func (r *Registry) TracerConn(conn *bindings.Conn, hook string) *trace.Tracer {
	tracer := r.tracers.Get(fmt.Sprintf("methods %d", r.ConnSerial(conn)))
	return tracer.With(trace.String("hook", hook))
}
