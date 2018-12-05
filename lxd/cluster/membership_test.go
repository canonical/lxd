package cluster_test

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/CanonicalLtd/go-dqlite"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/version"
	"github.com/mpvl/subtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBootstrap_UnmetPreconditions(t *testing.T) {
	cases := []struct {
		setup func(*membershipFixtures)
		error string
	}{
		{
			func(f *membershipFixtures) {
				f.ClusterAddress("1.2.3.4:666")
				f.RaftNode("5.6.7.8:666")
				filename := filepath.Join(f.state.OS.VarDir, "cluster.crt")
				ioutil.WriteFile(filename, []byte{}, 0644)
			},
			"inconsistent state: found leftover cluster certificate",
		},
		{
			func(*membershipFixtures) {},
			"no cluster.https_address config is set on this node",
		},
		{
			func(f *membershipFixtures) {
				f.ClusterAddress("1.2.3.4:666")
				f.RaftNode("5.6.7.8:666")
			},
			"the node is already part of a cluster",
		},
		{
			func(f *membershipFixtures) {
				f.RaftNode("5.6.7.8:666")
			},
			"inconsistent state: found leftover entries in raft_nodes",
		},
		{
			func(f *membershipFixtures) {
				f.ClusterAddress("1.2.3.4:666")
				f.ClusterNode("5.6.7.8:666")
			},
			"inconsistent state: found leftover entries in nodes",
		},
	}

	for _, c := range cases {
		subtest.Run(t, c.error, func(t *testing.T) {
			state, cleanup := state.NewTestState(t)
			defer cleanup()

			c.setup(&membershipFixtures{t: t, state: state})

			cert := shared.TestingKeyPair()
			gateway := newGateway(t, state.Node, cert)
			defer gateway.Shutdown()

			err := cluster.Bootstrap(state, gateway, "buzz")
			assert.EqualError(t, err, c.error)
		})
	}
}

func TestBootstrap(t *testing.T) {
	state, cleanup := state.NewTestState(t)
	defer cleanup()

	cert := shared.TestingKeyPair()
	gateway := newGateway(t, state.Node, cert)
	defer gateway.Shutdown()

	mux := http.NewServeMux()
	server := newServer(cert, mux)
	defer server.Close()

	address := server.Listener.Addr().String()
	f := &membershipFixtures{t: t, state: state}
	f.ClusterAddress(address)

	err := cluster.Bootstrap(state, gateway, "buzz")
	require.NoError(t, err)

	// The node-local database has now an entry in the raft_nodes table
	err = state.Node.Transaction(func(tx *db.NodeTx) error {
		nodes, err := tx.RaftNodes()
		require.NoError(t, err)
		require.Len(t, nodes, 1)
		assert.Equal(t, int64(1), nodes[0].ID)
		assert.Equal(t, address, nodes[0].Address)
		return nil
	})
	require.NoError(t, err)

	// The cluster database has now an entry in the nodes table
	err = state.Cluster.Transaction(func(tx *db.ClusterTx) error {
		nodes, err := tx.Nodes()
		require.NoError(t, err)
		require.Len(t, nodes, 1)
		assert.Equal(t, "buzz", nodes[0].Name)
		assert.Equal(t, address, nodes[0].Address)
		return nil
	})
	require.NoError(t, err)

	// The cluster certificate is in place.
	assert.True(t, shared.PathExists(filepath.Join(state.OS.VarDir, "cluster.crt")))

	// The dqlite driver is now exposed over the network.
	for path, handler := range gateway.HandlerFuncs() {
		mux.HandleFunc(path, handler)
	}

	count, err := cluster.Count(state)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	enabled, err := cluster.Enabled(state.Node)
	require.NoError(t, err)
	assert.True(t, enabled)
}

// If pre-conditions are not met, a descriptive error is returned.
func TestAccept_UnmetPreconditions(t *testing.T) {
	cases := []struct {
		name    string
		address string
		schema  int
		api     int
		setup   func(*membershipFixtures)
		error   string
	}{
		{
			"buzz",
			"1.2.3.4:666",
			cluster.SchemaVersion,
			len(version.APIExtensions),
			func(f *membershipFixtures) {},
			"clustering not enabled",
		},
		{
			"rusp",
			"1.2.3.4:666",
			cluster.SchemaVersion,
			len(version.APIExtensions),
			func(f *membershipFixtures) {
				f.ClusterNode("5.6.7.8:666")
			},
			"cluster already has node with name rusp",
		},
		{
			"buzz",
			"5.6.7.8:666",
			cluster.SchemaVersion,
			len(version.APIExtensions),
			func(f *membershipFixtures) {
				f.ClusterNode("5.6.7.8:666")
			},
			"cluster already has node with address 5.6.7.8:666",
		},
		{
			"buzz",
			"1.2.3.4:666",
			cluster.SchemaVersion - 1,
			len(version.APIExtensions),
			func(f *membershipFixtures) {
				f.ClusterNode("5.6.7.8:666")
			},
			fmt.Sprintf("schema version mismatch: cluster has %d", cluster.SchemaVersion),
		},
		{
			"buzz",
			"1.2.3.4:666",
			cluster.SchemaVersion,
			len(version.APIExtensions) - 1,
			func(f *membershipFixtures) {
				f.ClusterNode("5.6.7.8:666")
			},
			fmt.Sprintf("API version mismatch: cluster has %d", len(version.APIExtensions)),
		},
	}
	for _, c := range cases {
		subtest.Run(t, c.error, func(t *testing.T) {
			state, cleanup := state.NewTestState(t)
			defer cleanup()

			cert := shared.TestingKeyPair()
			gateway := newGateway(t, state.Node, cert)
			defer gateway.Shutdown()

			c.setup(&membershipFixtures{t: t, state: state})

			_, err := cluster.Accept(state, gateway, c.name, c.address, c.schema, c.api)
			assert.EqualError(t, err, c.error)
		})
	}
}

// When a node gets accepted, it gets included in the raft nodes.
func TestAccept(t *testing.T) {
	state, cleanup := state.NewTestState(t)
	defer cleanup()

	cert := shared.TestingKeyPair()
	gateway := newGateway(t, state.Node, cert)
	defer gateway.Shutdown()

	f := &membershipFixtures{t: t, state: state}
	f.RaftNode("1.2.3.4:666")
	f.ClusterNode("1.2.3.4:666")

	nodes, err := cluster.Accept(
		state, gateway, "buzz", "5.6.7.8:666", cluster.SchemaVersion, len(version.APIExtensions))
	assert.NoError(t, err)
	assert.Len(t, nodes, 2)
	assert.Equal(t, int64(1), nodes[0].ID)
	assert.Equal(t, int64(2), nodes[1].ID)
	assert.Equal(t, "1.2.3.4:666", nodes[0].Address)
	assert.Equal(t, "5.6.7.8:666", nodes[1].Address)
}

func TestJoin(t *testing.T) {
	// Setup a target node running as leader of a cluster.
	targetCert := shared.TestingKeyPair()
	targetMux := http.NewServeMux()
	targetServer := newServer(targetCert, targetMux)
	defer targetServer.Close()

	targetState, cleanup := state.NewTestState(t)
	defer cleanup()

	targetGateway := newGateway(t, targetState.Node, targetCert)
	defer targetGateway.Shutdown()

	for path, handler := range targetGateway.HandlerFuncs() {
		targetMux.HandleFunc(path, handler)
	}

	targetAddress := targetServer.Listener.Addr().String()

	require.NoError(t, targetState.Cluster.Close())

	targetStore := targetGateway.ServerStore()
	targetDialFunc := targetGateway.DialFunc()

	var err error
	targetState.Cluster, err = db.OpenCluster(
		"db.bin", targetStore, targetAddress, "/unused/db/dir",
		10*time.Second,
		dqlite.WithDialFunc(targetDialFunc))
	require.NoError(t, err)

	targetF := &membershipFixtures{t: t, state: targetState}
	targetF.ClusterAddress(targetAddress)

	err = cluster.Bootstrap(targetState, targetGateway, "buzz")
	require.NoError(t, err)
	_, err = targetState.Cluster.Networks()
	require.NoError(t, err)

	// Setup a joining node
	mux := http.NewServeMux()
	server := newServer(targetCert, mux)
	defer server.Close()

	state, cleanup := state.NewTestState(t)
	defer cleanup()

	cert := shared.TestingAltKeyPair()
	gateway := newGateway(t, state.Node, cert)

	defer gateway.Shutdown()

	for path, handler := range gateway.HandlerFuncs() {
		mux.HandleFunc(path, handler)
	}

	address := server.Listener.Addr().String()

	require.NoError(t, state.Cluster.Close())

	store := gateway.ServerStore()
	dialFunc := gateway.DialFunc()

	state.Cluster, err = db.OpenCluster(
		"db.bin", store, address, "/unused/db/dir", 5*time.Second, dqlite.WithDialFunc(dialFunc))
	require.NoError(t, err)

	f := &membershipFixtures{t: t, state: state}
	f.ClusterAddress(address)

	// Accept the joining node.
	raftNodes, err := cluster.Accept(
		targetState, targetGateway, "rusp", address, cluster.SchemaVersion, len(version.APIExtensions))
	require.NoError(t, err)

	// Actually join the cluster.
	err = cluster.Join(state, gateway, targetCert, "rusp", raftNodes)
	require.NoError(t, err)

	// The leader now returns an updated list of raft nodes.
	raftNodes, err = targetGateway.RaftNodes()
	require.NoError(t, err)
	assert.Len(t, raftNodes, 2)
	assert.Equal(t, int64(1), raftNodes[0].ID)
	assert.Equal(t, targetAddress, raftNodes[0].Address)
	assert.Equal(t, int64(2), raftNodes[1].ID)
	assert.Equal(t, address, raftNodes[1].Address)

	// The List function returns all nodes in the cluster.
	nodes, err := cluster.List(state)
	require.NoError(t, err)
	assert.Len(t, nodes, 2)
	assert.Equal(t, "Online", nodes[0].Status)
	assert.Equal(t, "Online", nodes[1].Status)
	assert.True(t, nodes[0].Database)
	assert.True(t, nodes[1].Database)

	// The Count function returns the number of nodes.
	count, err := cluster.Count(state)
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	// Leave the cluster.
	leaving, err := cluster.Leave(state, gateway, "rusp", false /* force */)
	require.NoError(t, err)
	assert.Equal(t, address, leaving)
	err = cluster.Purge(state.Cluster, "rusp")
	require.NoError(t, err)

	// The node has gone from the cluster db.
	err = targetState.Cluster.Transaction(func(tx *db.ClusterTx) error {
		nodes, err := tx.Nodes()
		require.NoError(t, err)
		assert.Len(t, nodes, 1)
		return nil
	})
	require.NoError(t, err)

	// The node has gone from the raft cluster.
	raft := targetGateway.Raft()
	future := raft.GetConfiguration()
	require.NoError(t, future.Error())
	assert.Len(t, future.Configuration().Servers, 1)

	count, err = cluster.Count(state)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func FLAKY_TestPromote(t *testing.T) {
	// Setup a target node running as leader of a cluster.
	targetCert := shared.TestingKeyPair()
	targetMux := http.NewServeMux()
	targetServer := newServer(targetCert, targetMux)
	defer targetServer.Close()

	targetState, cleanup := state.NewTestState(t)
	defer cleanup()

	targetGateway := newGateway(t, targetState.Node, targetCert)
	defer targetGateway.Shutdown()

	for path, handler := range targetGateway.HandlerFuncs() {
		targetMux.HandleFunc(path, handler)
	}

	targetAddress := targetServer.Listener.Addr().String()
	var err error
	require.NoError(t, targetState.Cluster.Close())
	store := targetGateway.ServerStore()
	dialFunc := targetGateway.DialFunc()
	targetState.Cluster, err = db.OpenCluster(
		"db.bin", store, targetAddress, "/unused/db/dir", 5*time.Second, dqlite.WithDialFunc(dialFunc))
	require.NoError(t, err)
	targetF := &membershipFixtures{t: t, state: targetState}
	targetF.ClusterAddress(targetAddress)

	err = cluster.Bootstrap(targetState, targetGateway, "buzz")
	require.NoError(t, err)

	// Setup a node to be promoted.
	mux := http.NewServeMux()
	server := newServer(targetCert, mux) // Use the same cert, as we're already part of the cluster
	defer server.Close()

	state, cleanup := state.NewTestState(t)
	defer cleanup()

	address := server.Listener.Addr().String()
	targetF.ClusterNode(address) // Add the non database node to the cluster database
	f := &membershipFixtures{t: t, state: state}
	f.ClusterAddress(address)
	f.RaftNode(targetAddress) // Insert the leader in our local list of database nodes

	gateway := newGateway(t, state.Node, targetCert)
	defer gateway.Shutdown()

	for path, handler := range gateway.HandlerFuncs() {
		mux.HandleFunc(path, handler)
	}

	// Promote the node.
	targetF.RaftNode(address) // Add the address of the node to be promoted in the leader's db
	raftNodes := targetF.RaftNodes()
	err = cluster.Promote(state, gateway, raftNodes)
	require.NoError(t, err)

	// The leader now returns an updated list of raft nodes.
	raftNodes, err = targetGateway.RaftNodes()
	require.NoError(t, err)
	assert.Len(t, raftNodes, 2)
	assert.Equal(t, int64(1), raftNodes[0].ID)
	assert.Equal(t, targetAddress, raftNodes[0].Address)
	assert.Equal(t, int64(2), raftNodes[1].ID)
	assert.Equal(t, address, raftNodes[1].Address)

	// The List function returns all nodes in the cluster.
	nodes, err := cluster.List(state)
	require.NoError(t, err)
	assert.Len(t, nodes, 2)
	assert.Equal(t, "Online", nodes[0].Status)
	assert.Equal(t, "Online", nodes[1].Status)
	assert.True(t, nodes[0].Database)
	assert.True(t, nodes[1].Database)
}

// Helper for setting fixtures for Bootstrap tests.
type membershipFixtures struct {
	t     *testing.T
	state *state.State
}

// Set core.https_address to the given value.
func (h *membershipFixtures) CoreAddress(address string) {
	err := h.state.Node.Transaction(func(tx *db.NodeTx) error {
		config := map[string]string{
			"core.https_address": address,
		}
		return tx.UpdateConfig(config)
	})
	require.NoError(h.t, err)
}

// Set cluster.https_address to the given value.
func (h *membershipFixtures) ClusterAddress(address string) {
	err := h.state.Node.Transaction(func(tx *db.NodeTx) error {
		config := map[string]string{
			"cluster.https_address": address,
		}
		return tx.UpdateConfig(config)
	})
	require.NoError(h.t, err)
}

// Add the given address to the raft_nodes table.
func (h *membershipFixtures) RaftNode(address string) {
	err := h.state.Node.Transaction(func(tx *db.NodeTx) error {
		_, err := tx.RaftNodeAdd(address)
		return err
	})
	require.NoError(h.t, err)
}

// Get the current list of the raft nodes in the raft_nodes table.
func (h *membershipFixtures) RaftNodes() []db.RaftNode {
	var nodes []db.RaftNode
	err := h.state.Node.Transaction(func(tx *db.NodeTx) error {
		var err error
		nodes, err = tx.RaftNodes()
		return err
	})
	require.NoError(h.t, err)
	return nodes
}

// Add the given address to the nodes table of the cluster database.
func (h *membershipFixtures) ClusterNode(address string) {
	err := h.state.Cluster.Transaction(func(tx *db.ClusterTx) error {
		_, err := tx.NodeAdd("rusp", address)
		return err
	})
	require.NoError(h.t, err)
}
