package cmd_test

import (
	"fmt"
	"testing"

	"github.com/lxc/lxd/shared/cmd"
	"github.com/stretchr/testify/assert"
)

// AssertOutEqual checks that the given text matches the the out stream.
func AssertOutEqual(t *testing.T, stream *cmd.MemoryStreams, expected string) {
	assert.Equal(t, expected, stream.Out(), "Unexpected output stream")
}

// AssertErrEqual checks that the given text matches the the err stream.
func AssertErrEqual(t *testing.T, stream *cmd.MemoryStreams, expected string) {
	assert.Equal(t, expected, stream.Err(), "Unexpected error stream")
}

// Output prints the given message on standard output
func TestOutput(t *testing.T) {
	streams := cmd.NewMemoryStreams("")
	context := cmd.NewMemoryContext(streams)
	context.Output("Hello %s", "world")
	AssertOutEqual(t, streams, "Hello world")
}

// AskBool returns a boolean result depending on the user input.
func TestAskBool(t *testing.T) {
	cases := []struct {
		question      string
		defaultAnswer string
		output        string
		error         string
		input         string
		result        bool
	}{
		{"Do you code?", "yes", "Do you code?", "", "\n", true},
		{"Do you code?", "yes", "Do you code?", "", "yes\n", true},
		{"Do you code?", "yes", "Do you code?", "", "y\n", true},
		{"Do you code?", "yes", "Do you code?", "", "no\n", false},
		{"Do you code?", "yes", "Do you code?", "", "n\n", false},
		{"Do you code?", "yes", "Do you code?Do you code?", "Invalid input, try again.\n\n", "foo\nyes\n", true},
	}
	for _, c := range cases {
		streams := cmd.NewMemoryStreams(c.input)
		context := cmd.NewMemoryContext(streams)
		result := context.AskBool(c.question, c.defaultAnswer)

		assert.Equal(t, c.result, result, "Unexpected answer result")
		AssertOutEqual(t, streams, c.output)
		AssertErrEqual(t, streams, c.error)
	}
}

// AskChoice returns one of the given choices
func TestAskChoice(t *testing.T) {
	cases := []struct {
		question      string
		choices       []string
		defaultAnswer string
		output        string
		error         string
		input         string
		result        string
	}{
		{"Best food?", []string{"pizza", "rice"}, "rice", "Best food?", "", "\n", "rice"},
		{"Best food?", []string{"pizza", "rice"}, "rice", "Best food?", "", "pizza\n", "pizza"},
		{"Best food?", []string{"pizza", "rice"}, "rice", "Best food?Best food?", "Invalid input, try again.\n\n", "foo\npizza\n", "pizza"},
	}
	for _, c := range cases {
		streams := cmd.NewMemoryStreams(c.input)
		context := cmd.NewMemoryContext(streams)
		result := context.AskChoice(c.question, c.choices, c.defaultAnswer)

		assert.Equal(t, c.result, result, "Unexpected answer result")
		AssertOutEqual(t, streams, c.output)
		AssertErrEqual(t, streams, c.error)
	}
}

// AskInt returns an integer within the given bounds
func TestAskInt(t *testing.T) {
	cases := []struct {
		question      string
		min           int64
		max           int64
		defaultAnswer string
		output        string
		error         string
		input         string
		result        int64
	}{
		{"Age?", 0, 100, "30", "Age?", "", "\n", 30},
		{"Age?", 0, 100, "30", "Age?", "", "40\n", 40},
		{"Age?", 0, 100, "30", "Age?Age?", "Invalid input, try again.\n\n", "foo\n40\n", 40},
		{"Age?", 18, 65, "30", "Age?Age?", "Invalid input, try again.\n\n", "10\n30\n", 30},
		{"Age?", 18, 65, "30", "Age?Age?", "Invalid input, try again.\n\n", "70\n30\n", 30},
		{"Age?", 0, -1, "30", "Age?", "", "120\n", 120},
	}
	for _, c := range cases {
		streams := cmd.NewMemoryStreams(c.input)
		context := cmd.NewMemoryContext(streams)
		result := context.AskInt(c.question, c.min, c.max, c.defaultAnswer)

		assert.Equal(t, c.result, result, "Unexpected answer result")
		AssertOutEqual(t, streams, c.output)
		AssertErrEqual(t, streams, c.error)
	}
}

// AskString returns a string conforming the validation function.
func TestAskString(t *testing.T) {
	cases := []struct {
		question      string
		defaultAnswer string
		validate      func(string) error
		output        string
		error         string
		input         string
		result        string
	}{
		{"Name?", "Joe", nil, "Name?", "", "\n", "Joe"},
		{"Name?", "Joe", nil, "Name?", "", "John\n", "John"},
		{"Name?", "Joe", func(s string) error {
			if s[0] != 'J' {
				return fmt.Errorf("ugly name")
			}
			return nil
		}, "Name?Name?", "Invalid input: ugly name\n\n", "Ted\nJohn", "John"},
	}
	for _, c := range cases {
		streams := cmd.NewMemoryStreams(c.input)
		context := cmd.NewMemoryContext(streams)
		result := context.AskString(c.question, c.defaultAnswer, c.validate)

		assert.Equal(t, c.result, result, "Unexpected answer result")
		AssertOutEqual(t, streams, c.output)
		AssertErrEqual(t, streams, c.error)
	}
}

// AskPassword returns the password entered twice by the user.
func TestAskPassword(t *testing.T) {
	cases := []struct {
		question string
		reader   func(int) ([]byte, error)
		output   string
		error    string
		result   string
	}{
		{"Pass?", func(int) ([]byte, error) {
			return []byte("pwd"), nil
		}, "Pass?\nAgain: \n", "", "pwd"},
	}
	for _, c := range cases {
		streams := cmd.NewMemoryStreams("")
		context := cmd.NewMemoryContext(streams)
		result := context.AskPassword(c.question, c.reader)

		assert.Equal(t, c.result, result, "Unexpected answer result")
		AssertOutEqual(t, streams, c.output)
		AssertErrEqual(t, streams, c.error)
	}
}

// InputYAML parses the YAML content passed via stdin.
func TestInputYAML(t *testing.T) {
	streams := cmd.NewMemoryStreams("field: foo")
	context := cmd.NewMemoryContext(streams)

	type Schema struct {
		Field string
	}
	schema := Schema{}

	assert.Nil(t, context.InputYAML(&schema))
	assert.Equal(t, "foo", schema.Field, "Unexpected field value")
}
