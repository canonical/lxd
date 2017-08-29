package main

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/version"
)

// If the help flag is set in the command line, the usage message is printed
// and the runner exists without executing the command.
func TestRunSubCommand_Help(t *testing.T) {
	command := newFailingCommand(t)
	ctx, streams := newSubCommandContext()
	args := &Args{Help: true}

	assert.Equal(t, 0, RunSubCommand(command, ctx, args, nil))
	assert.Contains(t, streams.Out(), "Usage: lxd [command] [options]")
}

// If the version flag is set in the command line, the version is printed
// and the runner exists without executing the command.
func TestRunSubCommand_Version(t *testing.T) {
	command := newFailingCommand(t)
	ctx, streams := newSubCommandContext()
	args := &Args{Version: true}

	assert.Equal(t, 0, RunSubCommand(command, ctx, args, nil))
	assert.Contains(t, streams.Out(), version.Version)
}

// If the path set in LXD_DIR is too long, an error is printed.
func TestRunSubCommand_LxdDirTooLong(t *testing.T) {
	// Restore original LXD_DIR.
	if value, ok := os.LookupEnv("LXD_DIR"); ok {
		defer os.Setenv("LXD_DIR", value)
	} else {
		defer os.Unsetenv("LXD_DIR")
	}

	os.Setenv("LXD_DIR", strings.Repeat("x", 200))

	command := newFailingCommand(t)
	ctx, streams := newSubCommandContext()
	args := &Args{}

	assert.Equal(t, 1, RunSubCommand(command, ctx, args, nil))
	assert.Contains(t, streams.Err(), "error: LXD_DIR is too long")
}

// If the command being executed returns an error, it is printed on standard
// err.
func TestRunSubCommand_Error(t *testing.T) {
	command := func(*Args) error { return fmt.Errorf("boom") }
	ctx, streams := newSubCommandContext()
	args := &Args{}

	assert.Equal(t, 1, RunSubCommand(command, ctx, args, nil))
	assert.Equal(t, "error: boom\n", streams.Err())
}

// If the command being executed returns a SubCommandError, RunSubCommand
// returns the relevant status code.
func TestRunSubCommand_SubCommandError(t *testing.T) {
	command := func(*Args) error { return SubCommandErrorf(127, "") }
	ctx, streams := newSubCommandContext()
	args := &Args{}

	assert.Equal(t, 127, RunSubCommand(command, ctx, args, nil))
	assert.Equal(t, "", streams.Err())
}

// Create a new cmd.Context connected to in-memory input/output streams.
func newSubCommandContext() (*cmd.Context, *cmd.MemoryStreams) {
	streams := cmd.NewMemoryStreams("")
	context := cmd.NewMemoryContext(streams)
	return context, streams
}

// Return a command that makes the test fail if executed.
func newFailingCommand(t *testing.T) SubCommand {
	return func(*Args) error {
		t.Fatal("unexpected command execution")
		return nil
	}
}
