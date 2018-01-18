package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A LXD node which is already configured for networking can be converted to a
// single-node LXD cluster.
func TestCluster_Bootstrap(t *testing.T) {
	daemon, cleanup := newDaemon(t)
	defer cleanup()

	f := clusterFixture{t: t}
	f.EnableNetworking(daemon, "")

	client := f.ClientUnix(daemon)

	op, err := client.BootstrapCluster("buzz")
	require.NoError(t, err)
	require.NoError(t, op.Wait())

	_, _, err = client.GetServer()
	require.NoError(t, err)
	assert.True(t, client.IsClustered())
	assert.Equal(t, "buzz", client.ClusterNodeName())
}

func TestCluster_Get(t *testing.T) {
	daemon, cleanup := newDaemon(t)
	defer cleanup()

	client, err := lxd.ConnectLXDUnix(daemon.UnixSocket(), nil)
	require.NoError(t, err)

	// Create a pool and check that the information returned by GetCluster
	// does not contain node-specific keys.
	os.Setenv("LXD_DIR", filepath.Join(daemon.State().OS.VarDir))
	pool := api.StoragePoolsPost{
		Name:   "mypool",
		Driver: "dir",
	}
	pool.Config = map[string]string{
		"source": "",
	}
	err = client.CreateStoragePool(pool)
	require.NoError(t, err)

	cluster, err := client.GetCluster("")
	require.NoError(t, err)
	assert.Len(t, cluster.StoragePools, 1)

	_, ok := cluster.StoragePools[0].Config["source"]
	assert.False(t, ok, "should have not contained the node-specific 'source' key")
}

// A LXD node which is already configured for networking can join an existing
// cluster.
func TestCluster_Join(t *testing.T) {
	daemons, cleanup := newDaemons(t, 2)
	defer cleanup()

	f := clusterFixture{t: t}
	passwords := []string{"sekret", ""}

	for i, daemon := range daemons {
		f.EnableNetworking(daemon, passwords[i])
	}

	// Bootstrap the cluster using the first node.
	client := f.ClientUnix(daemons[0])
	op, err := client.BootstrapCluster("buzz")
	require.NoError(t, err)
	require.NoError(t, op.Wait())

	// Make the second node join the cluster.
	address := daemons[0].endpoints.NetworkAddress()
	cert := string(daemons[0].endpoints.NetworkPublicKey())
	client = f.ClientUnix(daemons[1])
	op, err = client.JoinCluster(address, "sekret", cert, "rusp")
	require.NoError(t, err)
	require.NoError(t, op.Wait())

	// Both nodes are listed as database nodes in the second node's sqlite
	// database.
	state := daemons[1].State()
	err = state.Node.Transaction(func(tx *db.NodeTx) error {
		nodes, err := tx.RaftNodes()
		require.NoError(t, err)
		require.Len(t, nodes, 2)
		assert.Equal(t, int64(1), nodes[0].ID)
		assert.Equal(t, int64(2), nodes[1].ID)
		assert.Equal(t, daemons[0].endpoints.NetworkAddress(), nodes[0].Address)
		assert.Equal(t, daemons[1].endpoints.NetworkAddress(), nodes[1].Address)
		return nil
	})
	require.NoError(t, err)

	// Changing the configuration on the second node also updates it on the
	// first, via internal notifications.
	server, _, err := client.GetServer()
	require.NoError(t, err)
	serverPut := server.Writable()
	serverPut.Config["core.macaroon.endpoint"] = "foo.bar"
	require.NoError(t, client.UpdateServer(serverPut, ""))

	for _, daemon := range daemons {
		assert.NotNil(t, daemon.externalAuth)
	}

	// The GetNodes client method returns both nodes.
	nodes, err := client.GetNodes()
	require.NoError(t, err)
	assert.Len(t, nodes, 2)
	assert.Equal(t, "buzz", nodes[0].Name)
	assert.Equal(t, "rusp", nodes[1].Name)
	assert.Equal(t, "ONLINE", nodes[0].State)
	assert.Equal(t, "ONLINE", nodes[1].State)

	// The GetNode method returns the requested node.
	node, err := client.GetNode("buzz")
	require.NoError(t, err)
	assert.Equal(t, "buzz", node.Name)
}

// If the wrong trust password is given, the join request fails.
func TestCluster_JoinWrongTrustPassword(t *testing.T) {
	daemons, cleanup := newDaemons(t, 2)
	defer cleanup()

	f := clusterFixture{t: t}
	passwords := []string{"sekret", ""}

	for i, daemon := range daemons {
		f.EnableNetworking(daemon, passwords[i])
	}

	// Bootstrap the cluster using the first node.
	client := f.ClientUnix(daemons[0])
	op, err := client.BootstrapCluster("buzz")
	require.NoError(t, err)
	require.NoError(t, op.Wait())

	// Make the second node join the cluster.
	address := daemons[0].endpoints.NetworkAddress()
	cert := string(daemons[0].endpoints.NetworkPublicKey())
	client = f.ClientUnix(daemons[1])
	op, err = client.JoinCluster(address, "noop", cert, "rusp")
	require.NoError(t, err)
	assert.EqualError(t, op.Wait(), "failed to request to add node: not authorized")
}

// In a cluster for 3 nodes, if the leader goes down another one is elected the
// other two nodes continue to operate fine.
func TestCluster_Failover(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping cluster failover test in short mode.")
	}
	daemons, cleanup := newDaemons(t, 3)
	defer cleanup()

	f := clusterFixture{t: t}
	f.FormCluster(daemons)

	require.NoError(t, daemons[0].Stop())

	for i, daemon := range daemons[1:] {
		t.Logf("Invoking GetServer API against daemon %d", i)
		client := f.ClientUnix(daemon)
		server, _, err := client.GetServer()
		require.NoError(f.t, err)
		serverPut := server.Writable()
		serverPut.Config["core.trust_password"] = fmt.Sprintf("sekret-%d", i)

		t.Logf("Invoking UpdateServer API against daemon %d", i)
		require.NoError(f.t, client.UpdateServer(serverPut, ""))
	}
}

// A node can leave a cluster gracefully.
func TestCluster_Leave(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping cluster leave test in short mode.")
	}
	daemons, cleanup := newDaemons(t, 2)
	defer cleanup()

	f := clusterFixture{t: t}
	f.FormCluster(daemons)

	client := f.ClientUnix(daemons[1])
	err := client.LeaveCluster("rusp-0", false)
	require.NoError(t, err)

	_, _, err = client.GetServer()
	require.NoError(t, err)
	assert.False(t, client.IsClustered())

	nodes, err := client.GetNodes()
	require.NoError(t, err)
	assert.Len(t, nodes, 1)
	assert.Equal(t, "none", nodes[0].Name)
}

// A LXD node can be renamed.
func TestCluster_NodeRename(t *testing.T) {
	daemon, cleanup := newDaemon(t)
	defer cleanup()

	f := clusterFixture{t: t}
	f.EnableNetworking(daemon, "")

	client := f.ClientUnix(daemon)

	op, err := client.BootstrapCluster("buzz")
	require.NoError(t, err)
	require.NoError(t, op.Wait())

	node := api.NodePost{Name: "rusp"}
	err = client.RenameNode("buzz", node)
	require.NoError(t, err)

	_, err = client.GetNode("rusp")
	require.NoError(t, err)
}

// Test helper for cluster-related APIs.
type clusterFixture struct {
	t       *testing.T
	clients map[*Daemon]lxd.ContainerServer
}

// Form a cluster using the given daemons. The first daemon will be the leader.
func (f *clusterFixture) FormCluster(daemons []*Daemon) {
	for i, daemon := range daemons {
		password := ""
		if i == 0 {
			password = "sekret"
		}
		f.EnableNetworking(daemon, password)
	}

	// Bootstrap the cluster using the first node.
	client := f.ClientUnix(daemons[0])
	op, err := client.BootstrapCluster("buzz")
	require.NoError(f.t, err)
	require.NoError(f.t, op.Wait())

	// Make the other nodes join the cluster.
	address := daemons[0].endpoints.NetworkAddress()
	cert := string(daemons[0].endpoints.NetworkPublicKey())
	for i, daemon := range daemons[1:] {
		client = f.ClientUnix(daemon)
		op, err := client.JoinCluster(address, "sekret", cert, fmt.Sprintf("rusp-%d", i))
		require.NoError(f.t, err)
		require.NoError(f.t, op.Wait())
	}
}

// Enable networking in the given daemon. The password is optional and can be
// an empty string.
func (f *clusterFixture) EnableNetworking(daemon *Daemon, password string) {
	port, err := shared.AllocatePort()
	require.NoError(f.t, err)

	address := fmt.Sprintf("127.0.0.1:%d", port)

	client := f.ClientUnix(daemon)
	server, _, err := client.GetServer()
	require.NoError(f.t, err)
	serverPut := server.Writable()
	serverPut.Config["core.https_address"] = address
	serverPut.Config["core.trust_password"] = password

	require.NoError(f.t, client.UpdateServer(serverPut, ""))
}

// Get a client for the given daemon connected via UNIX socket, creating one if
// needed.
func (f *clusterFixture) ClientUnix(daemon *Daemon) lxd.ContainerServer {
	if f.clients == nil {
		f.clients = make(map[*Daemon]lxd.ContainerServer)
	}
	client, ok := f.clients[daemon]
	if !ok {
		var err error
		client, err = lxd.ConnectLXDUnix(daemon.UnixSocket(), nil)
		require.NoError(f.t, err)
	}
	return client
}
