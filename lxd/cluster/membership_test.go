package cluster_test

import (
	"io/ioutil"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/CanonicalLtd/go-grpc-sql"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
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
				f.NetworkAddress("1.2.3.4:666")
				filename := filepath.Join(f.state.OS.VarDir, "cluster.crt")
				ioutil.WriteFile(filename, []byte{}, 0644)
			},
			"inconsistent state: found leftover cluster certificate",
		},
		{
			func(*membershipFixtures) {},
			"no core.https_address config is set on this node",
		},
		{
			func(f *membershipFixtures) {
				f.NetworkAddress("1.2.3.4:666")
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
				f.NetworkAddress("1.2.3.4:666")
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
	f.NetworkAddress(address)

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

	driver := grpcsql.NewDriver(gateway.Dialer())
	conn, err := driver.Open("test.db")
	require.NoError(t, err)
	require.NoError(t, conn.Close())
}

// Helper for setting fixtures for Bootstrap tests.
type membershipFixtures struct {
	t     *testing.T
	state *state.State
}

// Set core.https_address to the given value.
func (h *membershipFixtures) NetworkAddress(address string) {
	err := h.state.Node.Transaction(func(tx *db.NodeTx) error {
		config := map[string]string{
			"core.https_address": address,
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

// Add the given address to the nodes table of the cluster database.
func (h *membershipFixtures) ClusterNode(address string) {
	err := h.state.Cluster.Transaction(func(tx *db.ClusterTx) error {
		_, err := tx.NodeAdd("rusp", address)
		return err
	})
	require.NoError(h.t, err)
}
