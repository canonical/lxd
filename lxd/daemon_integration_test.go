package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/sys"
	"github.com/canonical/lxd/shared/api"
)

// TestDaemon_Stop_NilClusterDB reproduces the nil-pointer panic that occurs
// when Stop() is called after Init() fails in the window between gateway
// creation and cluster-DB open (e.g. a dqlite race or schema mismatch), on a
// cluster member. In that window d.gateway is non-nil but d.db.Cluster is
// nil; before the fix, handoverMemberRole would reach
// getClusterMemberRolesAndEvacuatedMembers(), which calls
// s.DB.Cluster.Transaction() on the nil *db.Cluster and SIGSEGV. The fix
// makes Stop() skip the member-role handover when the cluster DB never
// opened, while still tearing the gateway down. Since handoverMemberRole()
// only touches the cluster DB when the member is clustered, the test forces
// d.serverClustered = true after Init() fails, to exercise that path (a
// freshly-initialized test daemon is never clustered by default).
func TestDaemon_Stop_NilClusterDB(t *testing.T) {
	testOS, osCleanup := sys.NewTestOS(t)
	defer osCleanup()

	// Block the unix socket path with a non-empty directory so that
	// endpoints.Up() fails deterministically inside Daemon.init(), after the
	// gateway is created but before the cluster database is opened. This
	// reproduces the window the fix targets: d.gateway is non-nil while
	// d.db.Cluster is still nil. A plain (empty) directory would not do, since
	// os.Remove() (used to clear stale sockets) succeeds on those.
	require.NoError(t, os.MkdirAll(testOS.GetUnixSocket(), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(testOS.GetUnixSocket(), "blocker"), nil, 0o600))

	daemon := newDaemon(newConfig(), testOS)

	err := daemon.Init()
	require.Error(t, err)

	// The window the fix targets: init() failed with a live gateway but no
	// cluster DB. The gateway is left in place for Stop() to tear down in the
	// correct order.
	require.Nil(t, daemon.db.Cluster)
	require.NotNil(t, daemon.gateway)

	// Simulate a cluster member so Stop() actually exercises the
	// handoverMemberRole() path that dereferences the cluster DB; that path
	// is skipped entirely for standalone servers, which a freshly
	// initialized test daemon always is.
	daemon.serverClustered = true

	// Must not panic; the originally-reported symptom.
	var stopErr error
	require.NotPanics(t, func() {
		stopErr = daemon.Stop(context.Background(), unix.SIGINT)
	})
	require.NoError(t, stopErr)
}

// The daemon is started and a client can connect to it via unix socket.
func TestIntegration_UnixSocket(t *testing.T) {
	daemon, cleanup := newTestDaemon(t)
	defer cleanup()
	client, err := lxd.ConnectLXDUnix(daemon.os.GetUnixSocket(), nil)
	require.NoError(t, err)

	server, _, err := client.GetServer()
	require.NoError(t, err)
	assert.Equal(t, api.AuthTrusted, server.Auth)
	assert.False(t, server.Environment.ServerClustered)
	assert.False(t, client.IsClustered())
}

// Secret server configuration values are only hidden when the redacted query parameter is set.
func TestIntegration_ServerRedacted(t *testing.T) {
	daemon, cleanup := newTestDaemon(t)
	defer cleanup()
	client, err := lxd.ConnectLXDUnix(daemon.os.GetUnixSocket(), nil)
	require.NoError(t, err)

	server, etag, err := client.GetServer()
	require.NoError(t, err)
	assert.Contains(t, server.APIExtensions, "server_config_redacted")

	secrets := map[string]string{
		"loki.auth.password":     "loki-pass",
		"network.ovn.client_key": "ovn-key",
		"oidc.client.secret":     "oidc-secret",
	}

	put := server.Writable()
	for key, value := range secrets {
		put.Config[key] = value
	}

	put.Config["loki.auth.username"] = "loki-user"
	put.Config["core.proxy_http"] = "http://proxy-user:proxy-pass@127.0.0.1:3128"
	require.NoError(t, client.UpdateServer(put, etag))

	server, _, err = client.GetServer()
	require.NoError(t, err)
	for key, value := range secrets {
		assert.Equal(t, value, server.Config[key])
	}

	server, _, err = client.GetServerRedacted()
	require.NoError(t, err)
	for key := range secrets {
		assert.Equal(t, "[redacted]", server.Config[key])
	}

	assert.Equal(t, "loki-user", server.Config["loki.auth.username"])
	assert.Equal(t, "http://proxy-user:[redacted]@127.0.0.1:3128", server.Config["core.proxy_http"])

	// Redaction only affects the response, the stored values are unchanged.
	server, _, err = client.GetServer()
	require.NoError(t, err)
	assert.Equal(t, "http://proxy-user:proxy-pass@127.0.0.1:3128", server.Config["core.proxy_http"])
	for key, value := range secrets {
		assert.Equal(t, value, server.Config[key])
	}
}

// Create a new daemon for testing.
//
// Return a function that can be used to cleanup every associated state.
func newTestDaemon(t *testing.T) (*Daemon, func()) {
	// OS
	testOS, osCleanup := sys.NewTestOS(t)

	// Daemon
	daemon := newDaemon(newConfig(), testOS)
	require.NoError(t, daemon.Init())

	cleanup := func() {
		assert.NoError(t, daemon.Stop(context.Background(), unix.SIGQUIT))
		osCleanup()
	}

	return daemon, cleanup
}

// Create a new DaemonConfig object for testing purposes.
func newConfig() *DaemonConfig {
	return &DaemonConfig{
		RaftLatency:        0.8,
		Trace:              []string{"dqlite"},
		DqliteSetupTimeout: 10 * time.Second,
	}
}
