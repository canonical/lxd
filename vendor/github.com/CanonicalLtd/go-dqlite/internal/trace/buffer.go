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
	"time"
)

// A circular buffer of trace entries.
type buffer struct {
	// Fixed-size slice of entries in the buffer. When the slice fills, new
	// entries will replace old ones at the beginning on the slice.
	entries []entry

	// Track the position of the last entry in the buffer.
	cursor *cursor
}

// Create a new circular buffer of trace entries, retaining at most the given
// number of entries.
func newBuffer(n int) *buffer {
	return &buffer{
		entries: make([]entry, n),
		cursor:  newCursor(0, n),
	}
}

// Append a new entry to the buffer, possibly replacing an older one.
func (b *buffer) Append(timestamp time.Time, message string, args []interface{}, err error, fields *fields) {
	i := b.cursor.Position()

	b.entries[i].timestamp = timestamp
	b.entries[i].message = message
	for j := range b.entries[i].args {
		// Set arg j to either the provided arg or nil
		if j < len(args) {
			b.entries[i].args[j] = args[j]
		} else {
			b.entries[i].args[j] = nil
		}
	}
	b.entries[i].error = err
	b.entries[i].fields = fields

	b.cursor.Advance()
}

// Return the last inserted entry
func (b *buffer) Last() entry {
	cursor := newCursor(b.cursor.Position(), len(b.entries))
	cursor.Retract()
	return b.entries[cursor.Position()]
}

// Return the list of current entries in the buffer.
func (b *buffer) Entries() []entry {
	entries := make([]entry, 0)

	// We don't keep track of the actual number of entries in the buffer,
	// instead we iterate them backwards until we find a "null" entry.
	//
	// A "null" entry is detected by looking at its timestamp and seeting
	// that it's set to the Unix epoch.
	n := len(b.entries)
	cursor := newCursor(b.cursor.Position(), n)
	for i := 0; i < n; i++ {
		cursor.Retract()
		previous := b.entries[cursor.Position()]
		if previous.timestamp.Unix() == epoch {
			break
		}
		entries = append([]entry{previous}, entries...)
	}

	return entries
}
