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

import "fmt"

// String returns a Field with a string value.
func String(key string, value string) Field {
	return Field{
		key:      key,
		isString: true,
		string:   value,
	}
}

// Integer returns a Field with an integer value.
func Integer(key string, value int64) Field {
	return Field{
		key:     key,
		integer: value,
	}
}

// Field holds a single key/value pair in a trace Entry.
type Field struct {
	key      string // Name of the key
	isString bool   // Whether the value is a string or an integer
	string   string // String value
	integer  int64  // Integer value
}

func (f Field) String() string {
	format := "%s="
	args := []interface{}{f.key}
	if f.isString {
		format += "%s"
		args = append(args, f.string)
	} else {
		format += "%d"
		args = append(args, f.integer)
	}

	return fmt.Sprintf(format, args...)
}
