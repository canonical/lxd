// Utilities for testing cmd-related code.

package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// MemoryStreams provide an in-memory version of the system
// stdin/stdout/stderr streams.
type MemoryStreams struct {
	in  *strings.Reader
	out *bytes.Buffer
	err *bytes.Buffer
}

// NewMemoryStreams creates a new set of in-memory streams with the given
// user input.
func NewMemoryStreams(input string) *MemoryStreams {
	return &MemoryStreams{
		in:  strings.NewReader(input),
		out: new(bytes.Buffer),
		err: new(bytes.Buffer),
	}
}

// AssertOutEqual checks that the given text matches the the out stream.
func (s *MemoryStreams) AssertOutEqual(t *testing.T, expected string) {
	assert.Equal(t, expected, s.out.String(), "Unexpected output stream")
}

// AssertErrEqual checks that the given text matches the the err stream.
func (s *MemoryStreams) AssertErrEqual(t *testing.T, expected string) {
	assert.Equal(t, expected, s.err.String(), "Unexpected error stream")
}

// NewMemoryContext creates a new command Context using the given in-memory
// streams.
func NewMemoryContext(streams *MemoryStreams) *Context {
	return NewContext(streams.in, streams.out, streams.err)
}
