package main

import (
	"testing"

	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/sys"
	"github.com/lxc/lxd/shared/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The daemon is started and a client can connect to it via unix socket.
func TestIntegration_UnixSocket(t *testing.T) {
	daemon, cleanup := newDaemon(t)
	defer cleanup()
	client, err := lxd.ConnectLXDUnix(daemon.UnixSocket(), nil)
	require.NoError(t, err)

	server, _, err := client.GetServer()
	require.NoError(t, err)
	assert.Equal(t, "trusted", server.Auth)
}

// Create a new daemon for testing.
//
// Return a function that can be used to cleanup every associated state.
func newDaemon(t *testing.T) (*Daemon, func()) {
	// Logging
	resetLogger := logging.Testing(t)

	// OS
	os, osCleanup := sys.NewTestOS(t)

	// Daemon
	daemon := NewDaemon(newConfig(), os)
	require.NoError(t, daemon.Init())

	cleanup := func() {
		require.NoError(t, daemon.Stop())
		osCleanup()
		resetLogger()
	}

	return daemon, cleanup
}

// Create a new DaemonConfig object for testing purposes.
func newConfig() *DaemonConfig {
	return &DaemonConfig{
		RaftLatency: 0.2,
	}
}
