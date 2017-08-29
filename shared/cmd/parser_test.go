package cmd_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/subtest"
)

// Sample command line arguments specification.
type Args struct {
	Subcommand string
	Params     []string
	Extra      []string

	Help      bool   `flag:"help"`
	Text      string `flag:"text"`
	Number    int    `flag:"number"`
	BigNumber int64  `flag:"big-number"`
}

// Check the default values of all command line args.
func TestParser_ArgsDefaults(t *testing.T) {
	line := []string{"cmd"}
	args := &Args{}
	parser := newParser()

	assert.NoError(t, parser.Parse(line, args))

	assert.Equal(t, "", args.Text)
	assert.Equal(t, false, args.Help)
	assert.Equal(t, -1, args.Number)
	assert.Equal(t, int64(-1), args.BigNumber)
}

// Check that parsing the command line results in the correct attributes
// being set.
func TestParser_ArgsCustom(t *testing.T) {
	line := []string{
		"cmd",
		"--text", "hello",
		"--help",
		"--number", "10",
		"--big-number", "666",
	}
	args := &Args{}
	parser := newParser()

	assert.NoError(t, parser.Parse(line, args))

	assert.Equal(t, "hello", args.Text)
	assert.Equal(t, true, args.Help)
	assert.Equal(t, 10, args.Number)
	assert.Equal(t, int64(666), args.BigNumber)
}

// Check that the subcommand is properly set.
func TestParser_Subcommand(t *testing.T) {
	cases := []struct {
		line       []string
		subcommand string
	}{
		{[]string{"cmd"}, ""},
		{[]string{"cmd", "--help"}, ""},
		{[]string{"cmd", "subcmd"}, "subcmd"},
		{[]string{"cmd", "subcmd", "--help"}, "subcmd"},
		{[]string{"cmd", "--help", "subcmd"}, ""},
	}
	for _, c := range cases {
		subtest.Run(t, strings.Join(c.line, "_"), func(t *testing.T) {
			args := &Args{}
			parser := newParser()
			assert.NoError(t, parser.Parse(c.line, args))
			assert.Equal(t, c.subcommand, args.Subcommand)
		})
	}
}

// Check that subcommand params are properly set.
func TestParser_Params(t *testing.T) {
	cases := []struct {
		line   []string
		params []string
	}{
		{[]string{"cmd"}, []string{}},
		{[]string{"cmd", "--help"}, []string{}},
		{[]string{"cmd", "subcmd"}, []string{}},
		{[]string{"cmd", "subcmd", "param"}, []string{"param"}},
		{[]string{"cmd", "subcmd", "param1", "param2"}, []string{"param1", "param2"}},
		{[]string{"cmd", "subcmd", "param", "--help"}, []string{"param"}},
		{[]string{"cmd", "subcmd", "--help", "param"}, []string{}},
	}
	for _, c := range cases {
		subtest.Run(t, strings.Join(c.line, "_"), func(t *testing.T) {
			args := &Args{}
			parser := newParser()
			assert.NoError(t, parser.Parse(c.line, args))
			assert.Equal(t, c.params, args.Params)
		})
	}
}

// Check that extra params are properly set.
func TestParser_Extra(t *testing.T) {
	cases := []struct {
		line  []string
		extra []string
	}{
		{[]string{"cmd"}, []string{}},
		{[]string{"cmd", "--help"}, []string{}},
		{[]string{"cmd", "subcmd"}, []string{}},
		{[]string{"cmd", "subcmd", "--"}, []string{}},
		{[]string{"cmd", "subcmd", "--", "extra"}, []string{"extra"}},
		{[]string{"cmd", "subcmd", "--", "extra1", "--extra2"}, []string{"extra1", "--extra2"}},
	}
	for _, c := range cases {
		subtest.Run(t, strings.Join(c.line, "_"), func(t *testing.T) {
			args := &Args{}
			parser := newParser()
			assert.NoError(t, parser.Parse(c.line, args))
			assert.Equal(t, c.extra, args.Extra)
		})
	}
}

// If a flag doesn't exist, an error is returned.
func TestParser_Error(t *testing.T) {
	line := []string{"cmd", "--boom"}
	args := &Args{}
	parser := newParser()

	assert.Error(t, parser.Parse(line, args))
}

// If a usage string is passed, and the command line has the help flag, the
// message is printed out.
func TestParser_Usage(t *testing.T) {
	line := []string{"cmd", "-h"}
	args := &Args{}
	streams := cmd.NewMemoryStreams("")

	parser := newParserWithStreams(streams)
	parser.UsageMessage = "usage message"

	assert.Error(t, parser.Parse(line, args))
	assert.Equal(t, parser.UsageMessage, streams.Out())
}

// Return a new test parser
func newParser() *cmd.Parser {
	return newParserWithStreams(cmd.NewMemoryStreams(""))
}

// Return a new test parser using the given streams for its context.
func newParserWithStreams(streams *cmd.MemoryStreams) *cmd.Parser {
	return &cmd.Parser{
		Context: cmd.NewMemoryContext(streams),
	}
}
