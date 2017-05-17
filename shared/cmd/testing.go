// Utilities for testing cmd-related code.

package cmd

import (
	"bytes"
	"io/ioutil"
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

// InputRead returns the current input string.
func (s *MemoryStreams) InputRead() string {
	bytes, _ := ioutil.ReadAll(s.in)
	return string(bytes)
}

// InputReset replaces the data in the input stream.
func (s *MemoryStreams) InputReset(input string) {
	// XXX This is what the stdlib strings.Reader.Reset() does, however
	//     this method is not available in Go 1.6.
	*s.in = *strings.NewReader(input)
}

// InputAppend adds the given text to the current input.
func (s *MemoryStreams) InputAppend(text string) {
	s.InputReset(s.InputRead() + text)
}

// InputAppendLine adds a single line to the input stream.
func (s *MemoryStreams) InputAppendLine(line string) {
	s.InputAppend(line + "\n")
}

// InputAppendBoolAnswer adds a new "yes" or "no" line depending on the answer.
func (s *MemoryStreams) InputAppendBoolAnswer(answer bool) {
	var line string
	if answer {
		line = "yes"
	} else {
		line = "no"
	}
	s.InputAppendLine(line)
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
