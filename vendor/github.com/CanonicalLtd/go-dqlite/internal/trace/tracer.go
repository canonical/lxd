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
)

// Tracer holds a buffer of recent trace entries in a trace Registry.
type Tracer struct {
	set    *Set    // Set this tracer is part of.
	name   string  // Name of the tracer.
	buffer *buffer // Ring buffer for trace entries.
	fields fields  // Tracer-specific key/value pairs.
}

// Creates a new tracer.
func newTracer(set *Set, name string, buffer *buffer) *Tracer {
	return &Tracer{
		set:    set,
		name:   name,
		buffer: buffer,
		fields: fields{},
	}
}

// Message emits a new trace message.
func (t *Tracer) Message(message string, args ...interface{}) {
	if n := len(args); n > maxArgs {
		panic(fmt.Sprintf("a trace entry can have at most %d args, but %d were given", maxArgs, n))
	}
	t.emit(message, args, nil)
}

// Emit a new trace entry with an error attached.
func (t *Tracer) Error(message string, err error) {
	t.emit(message, nil, err)
}

// Panic causes a Go panic which will print all trace entries across all
// tracers.
func (t *Tracer) Panic(message string, v ...interface{}) {
	message = fmt.Sprintf(message, v...)
	if t.set.testing == nil {
		message += "\n\ntrace:\n" + t.set.String()
	}
	panic(message)
}

// With returns a new Tracer instance emitting entries in the same buffer of this
// tracer, but with additional predefined fields.
func (t *Tracer) With(fields ...Field) *Tracer {
	if n := len(fields); n > maxFields {
		panic(fmt.Sprintf("a trace entry can have at most %d fields, but %d were given", maxFields, n))
	}

	// Create the child tracer, cloning the parent and using its entries
	// buffer.
	tracer := newTracer(t.set, t.name, t.buffer)

	// Copy the fields of the parent into the child.
	i := 0
	for ; t.fields[i].key != ""; i++ {
		tracer.fields[i] = t.fields[i]
	}

	// Add the child fields.
	for j := range fields {
		tracer.fields[i+j] = fields[j]
	}

	return tracer
}

// Emit a new trace entry.
func (t *Tracer) emit(message string, args []interface{}, err error) {
	t.buffer.Append(t.set.now(), message, args, err, &t.fields)

	if t.set.testing != nil {
		entry := t.buffer.Last()
		format := "%d: %s: %s: %s\n"
		t.set.testing.Logf(format, t.set.node, entry.Timestamp(), t.name, entry.Message())
	}
}
