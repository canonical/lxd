package main

import (
	"testing"
	"time"

	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/sys"
	"github.com/lxc/lxd/shared"
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
	assert.False(t, server.Environment.ServerClustered)
	assert.False(t, client.IsClustered())
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
		daemon.Stop()
		osCleanup()
		resetLogger()
	}

	return daemon, cleanup
}

// Create the given numbers of test Daemon instances.
//
// Return a function that can be used to cleanup every associated state.
func newDaemons(t *testing.T, n int) ([]*Daemon, func()) {
	daemons := make([]*Daemon, n)
	cleanups := make([]func(), n)

	for i := 0; i < n; i++ {
		daemons[i], cleanups[i] = newDaemon(t)
		if i > 0 {
			// Use a different server certificate
			cert := shared.TestingAltKeyPair()
			daemons[i].endpoints.NetworkUpdateCert(cert)
		}
	}

	cleanup := func() {
		for _, cleanup := range cleanups {
			cleanup()
		}
	}

	return daemons, cleanup
}

// Create a new DaemonConfig object for testing purposes.
func newConfig() *DaemonConfig {
	return &DaemonConfig{
		RaftLatency:        0.8,
		Trace:              []string{"dqlite"},
		DqliteSetupTimeout: 10 * time.Second,
	}
}
