package main

import (
	"fmt"
	"testing"

	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/shared"
	"github.com/stretchr/testify/require"
)

// A LXD node which is already configured for networking can be coverted to a
// single-node LXD cluster.
func TestCluster_Bootstrap(t *testing.T) {
	daemon, cleanup := newDaemon(t)
	defer cleanup()

	client, err := lxd.ConnectLXDUnix(daemon.UnixSocket(), nil)
	require.NoError(t, err)

	server, _, err := client.GetServer()
	require.NoError(t, err)

	port, err := shared.AllocatePort()
	require.NoError(t, err)

	serverPut := server.Writable()
	serverPut.Config["core.https_address"] = fmt.Sprintf("localhost:%d", port)

	require.NoError(t, client.UpdateServer(serverPut, ""))

	op, err := client.BootstrapCluster("buzz")
	require.NoError(t, err)
	require.NoError(t, op.Wait())
}
