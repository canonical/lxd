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
	"time"
)

// A single trace entry.
type entry struct {
	timestamp time.Time // Time at which the entry was created.
	message   string    // Message of the entry.
	args      args      // Additional format arguments for the message.
	error     error     // Error associated with the entry.

	// Key/value fields associated with the entry. This is a pointer
	// because all entries of a specific tracer share the same fields.
	fields *fields
}

// Timestamp returns a string representation of the entry's timestamp.
func (e entry) Timestamp() string {
	return e.timestamp.Format("2006-01-02 15:04:05.00000")
}

// Message returns a string with the entry's message along with its fields,
// arguments and error.
func (e entry) Message() string {
	message := e.message

	if e.args[0] != nil {
		args := make([]interface{}, 0)
		for i := 0; e.args[i] != nil; i++ {
			args = append(args, e.args[i])
		}
		message = fmt.Sprintf(message, args...)
	}

	fields := ""
	for i := 0; i < len(e.fields) && e.fields[i].key != ""; i++ {
		fields += fmt.Sprintf("%s ", e.fields[i])
	}

	if e.error != nil {
		message += fmt.Sprintf(": %v", e.error)
	}

	return fmt.Sprintf("%s%s", fields, message)
}

type args [maxArgs]interface{}
type fields [maxFields]Field
