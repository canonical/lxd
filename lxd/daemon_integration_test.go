package main

import (
	"io/ioutil"
	"os"
	"path/filepath"
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

	// Test directory
	dir, err := ioutil.TempDir("", "lxd-integration-test")
	require.NoError(t, err)

	// Test certificates
	require.NoError(t, os.Mkdir(filepath.Join(dir, "var"), 0755))
	require.NoError(t, setupTestCerts(filepath.Join(dir, "var")))

	// Daemon
	daemon := NewDaemon(newConfig(), newOS(dir))
	require.NoError(t, daemon.Init())

	cleanup := func() {
		require.NoError(t, daemon.Stop())
		require.NoError(t, os.RemoveAll(dir))
		resetLogger()
	}

	return daemon, cleanup
}

// Create a new DaemonConfig object for testing purposes.
func newConfig() *DaemonConfig {
	return &DaemonConfig{}
}

// Create a new sys.OS object for testing purposes.
func newOS(dir string) *sys.OS {
	return &sys.OS{
		// FIXME: setting mock mode can be avoided once daemon tasks
		// are fixed to exit gracefully. See daemon.go.
		MockMode: true,

		VarDir:   filepath.Join(dir, "var"),
		CacheDir: filepath.Join(dir, "cache"),
		LogDir:   filepath.Join(dir, "log"),
	}
}

// Populate the given test LXD directory with server certificates.
//
// Since generating certificates is CPU intensive, they will be simply
// symlink'ed from the test/deps/ directory.
func setupTestCerts(dir string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	deps := filepath.Join(cwd, "..", "test", "deps")
	for _, f := range []string{"server.crt", "server.key"} {
		err := os.Symlink(filepath.Join(deps, f), filepath.Join(dir, f))
		if err != nil {
			return err
		}
	}
	return nil
}
