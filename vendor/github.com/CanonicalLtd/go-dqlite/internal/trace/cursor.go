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

// A cursor holds the index of an entry of a circular buffer.
type cursor struct {
	position int // Current position of the cursor
	length   int // Lenght of the circular buffer.
}

func newCursor(position, length int) *cursor {
	return &cursor{
		position: position,
		length:   length,
	}
}

func (c *cursor) Position() int {
	return c.position
}

func (c *cursor) Advance() {
	c.position = (c.position + c.length + 1) % c.length
}

func (c *cursor) Retract() {
	c.position = (c.position + c.length - 1) % c.length
}
