package cluster_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/version"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"
)

// After a heartbeat request is completed, the leader updates the heartbeat
// timestamp column, and the serving node updates its cache of raft nodes.
func TestHeartbeat(t *testing.T) {
	f := heartbeatFixture{t: t}
	defer f.Cleanup()

	gateway0 := f.Bootstrap()
	gateway1 := f.Grow()
	f.Grow()

	state0 := f.State(gateway0)
	state1 := f.State(gateway1)

	// Artificially mark all nodes as down
	err := state0.Cluster.Transaction(func(tx *db.ClusterTx) error {
		nodes, err := tx.Nodes()
		require.NoError(t, err)
		for _, node := range nodes {
			err := tx.NodeHeartbeat(node.Address, time.Now().Add(-time.Minute))
			require.NoError(t, err)
		}
		return nil
	})
	require.NoError(t, err)

	// Perform the heartbeat requests.
	heartbeat, _ := cluster.Heartbeat(gateway0, state0.Cluster)
	ctx := context.Background()
	heartbeat(ctx)

	// The second node that initially did not know about the third, now
	// does.
	err = state1.Node.Transaction(func(tx *db.NodeTx) error {
		nodes, err := tx.RaftNodes()
		require.NoError(t, err)
		assert.Len(t, nodes, 3)
		return nil
	})
	require.NoError(t, err)

	// The heartbeat timestamps of all nodes got updated
	err = state0.Cluster.Transaction(func(tx *db.ClusterTx) error {
		nodes, err := tx.Nodes()
		require.NoError(t, err)
		for _, node := range nodes {
			assert.False(t, node.IsDown())
		}
		return nil
	})
	require.NoError(t, err)
}

// If a certain node does not successfully respond to the heartbeat, its
// timestamp does not get updated.
func TestHeartbeat_MarkAsDown(t *testing.T) {
	f := heartbeatFixture{t: t}
	defer f.Cleanup()

	gateway0 := f.Bootstrap()
	gateway1 := f.Grow()

	state0 := f.State(gateway0)

	// Artificially mark all nodes as down
	err := state0.Cluster.Transaction(func(tx *db.ClusterTx) error {
		nodes, err := tx.Nodes()
		require.NoError(t, err)
		for _, node := range nodes {
			err := tx.NodeHeartbeat(node.Address, time.Now().Add(-time.Minute))
			require.NoError(t, err)
		}
		return nil
	})
	require.NoError(t, err)

	// Shutdown the second node and perform the heartbeat requests.
	f.Server(gateway1).Close()
	heartbeat, _ := cluster.Heartbeat(gateway0, state0.Cluster)
	ctx := context.Background()
	heartbeat(ctx)

	// The heartbeat timestamp of the second node did not get updated
	err = state0.Cluster.Transaction(func(tx *db.ClusterTx) error {
		nodes, err := tx.Nodes()
		require.NoError(t, err)
		assert.True(t, nodes[1].IsDown())
		return nil
	})
	require.NoError(t, err)
}

// Helper for testing heartbeat-related code.
type heartbeatFixture struct {
	t        *testing.T
	gateways map[int]*cluster.Gateway              // node index to gateway
	states   map[*cluster.Gateway]*state.State     // gateway to its state handle
	servers  map[*cluster.Gateway]*httptest.Server // gateway to its HTTP server
	cleanups []func()
}

// Bootstrap the first node of the cluster.
func (f *heartbeatFixture) Bootstrap() *cluster.Gateway {
	state, gateway, _ := f.node()

	err := cluster.Bootstrap(state, gateway, "buzz")
	require.NoError(f.t, err)

	return gateway
}

// Grow adds a new node to the cluster.
func (f *heartbeatFixture) Grow() *cluster.Gateway {
	state, gateway, address := f.node()
	name := address

	target := f.gateways[0]
	targetState := f.states[target]

	nodes, err := cluster.Accept(
		targetState, name, address, cluster.SchemaVersion, len(version.APIExtensions))

	err = cluster.Join(state, gateway, target.Cert(), name, nodes)
	require.NoError(f.t, err)

	return gateway
}

// Return the state associated with the given gateway.
func (f *heartbeatFixture) State(gateway *cluster.Gateway) *state.State {
	return f.states[gateway]
}

// Return the HTTP server associated with the given gateway.
func (f *heartbeatFixture) Server(gateway *cluster.Gateway) *httptest.Server {
	return f.servers[gateway]
}

// Creates a new node, without either bootstrapping or joining it.
//
// Return the associated gateway and network address.
func (f *heartbeatFixture) node() (*state.State, *cluster.Gateway, string) {
	if f.gateways == nil {
		f.gateways = make(map[int]*cluster.Gateway)
		f.states = make(map[*cluster.Gateway]*state.State)
		f.servers = make(map[*cluster.Gateway]*httptest.Server)
	}

	state, cleanup := state.NewTestState(f.t)
	f.cleanups = append(f.cleanups, cleanup)

	cert := shared.TestingKeyPair()
	gateway := newGateway(f.t, state.Node, cert)
	f.cleanups = append(f.cleanups, func() { gateway.Shutdown() })

	mux := http.NewServeMux()
	server := newServer(cert, mux)

	for path, handler := range gateway.HandlerFuncs() {
		mux.HandleFunc(path, handler)
	}

	address := server.Listener.Addr().String()
	mf := &membershipFixtures{t: f.t, state: state}
	mf.NetworkAddress(address)

	var err error
	require.NoError(f.t, state.Cluster.Close())
	state.Cluster, err = db.OpenCluster("db.bin", gateway.Dialer(), address)
	require.NoError(f.t, err)

	f.gateways[len(f.gateways)] = gateway
	f.states[gateway] = state
	f.servers[gateway] = server

	return state, gateway, address
}

func (f *heartbeatFixture) Cleanup() {
	// Run the cleanups in reverse order
	for i := len(f.cleanups) - 1; i >= 0; i-- {
		f.cleanups[i]()
	}
	for _, server := range f.servers {
		server.Close()
	}
}
