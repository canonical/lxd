package cluster_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/CanonicalLtd/go-dqlite"
	"github.com/hashicorp/raft"
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

	f.Bootstrap()
	f.Grow()
	f.Grow()

	leader := f.Leader()
	leaderState := f.State(leader)

	// Artificially mark all nodes as down
	err := leaderState.Cluster.Transaction(func(tx *db.ClusterTx) error {
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
	heartbeat, _ := cluster.Heartbeat(leader, leaderState.Cluster)
	ctx := context.Background()
	heartbeat(ctx)

	// The heartbeat timestamps of all nodes got updated
	err = leaderState.Cluster.Transaction(func(tx *db.ClusterTx) error {
		nodes, err := tx.Nodes()
		require.NoError(t, err)

		offlineThreshold, err := tx.NodeOfflineThreshold()
		require.NoError(t, err)

		for _, node := range nodes {
			assert.False(t, node.IsOffline(offlineThreshold))
		}
		return nil
	})
	require.NoError(t, err)
}

// If a certain node does not successfully respond to the heartbeat, its
// timestamp does not get updated.
func DISABLE_TestHeartbeat_MarkAsDown(t *testing.T) {
	f := heartbeatFixture{t: t}
	defer f.Cleanup()

	f.Bootstrap()
	f.Grow()

	leader := f.Leader()
	leaderState := f.State(leader)

	// Artificially mark all nodes as down
	t.Logf("marking all nodes as down")
	err := leaderState.Cluster.Transaction(func(tx *db.ClusterTx) error {
		nodes, err := tx.Nodes()
		require.NoError(t, err)
		for _, node := range nodes {
			err := tx.NodeHeartbeat(node.Address, time.Now().Add(-time.Minute))
			require.NoError(t, err)
		}
		return nil
	})
	require.NoError(t, err)

	follower := f.Follower()

	// Shutdown the follower node and perform the heartbeat requests.
	f.Server(follower).Close()
	heartbeat, _ := cluster.Heartbeat(leader, leaderState.Cluster)
	ctx := context.Background()
	heartbeat(ctx)

	// The heartbeat timestamp of the second node did not get updated
	err = leaderState.Cluster.Transaction(func(tx *db.ClusterTx) error {
		nodes, err := tx.Nodes()
		require.NoError(t, err)

		offlineThreshold, err := tx.NodeOfflineThreshold()
		require.NoError(t, err)

		i := f.Index(follower)
		assert.True(t, nodes[i].IsOffline(offlineThreshold))
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
	f.t.Logf("create bootstrap node for test cluster")
	state, gateway, _ := f.node()

	err := cluster.Bootstrap(state, gateway, "buzz")
	require.NoError(f.t, err)

	return gateway
}

// Grow adds a new node to the cluster.
func (f *heartbeatFixture) Grow() *cluster.Gateway {
	// Figure out the current leader
	f.t.Logf("adding another node to the test cluster")
	target := f.Leader()
	targetState := f.states[target]

	state, gateway, address := f.node()
	name := address

	nodes, err := cluster.Accept(
		targetState, target, name, address, cluster.SchemaVersion, len(version.APIExtensions))
	require.NoError(f.t, err)

	err = cluster.Join(state, gateway, target.Cert(), name, nodes)
	require.NoError(f.t, err)

	return gateway
}

// Return the leader gateway in the cluster.
func (f *heartbeatFixture) Leader() *cluster.Gateway {
	timeout := time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		for _, gateway := range f.gateways {
			if gateway.Raft().State() == raft.Leader {
				return gateway
			}
		}

		select {
		case <-ctx.Done():
			f.t.Fatalf("no leader was elected within %s", timeout)
		default:
		}

		// Wait a bit for election to take place
		time.Sleep(10 * time.Millisecond)
	}
}

// Return a follower gateway in the cluster.
func (f *heartbeatFixture) Follower() *cluster.Gateway {
	timeout := time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		for _, gateway := range f.gateways {
			if gateway.Raft().State() == raft.Follower {
				return gateway
			}
		}

		select {
		case <-ctx.Done():
			f.t.Fatalf("no node running as follower")
		default:
		}

		// Wait a bit for election to take place
		time.Sleep(10 * time.Millisecond)
	}
}

// Return the cluster index of the given gateway.
func (f *heartbeatFixture) Index(gateway *cluster.Gateway) int {
	for i := range f.gateways {
		if f.gateways[i] == gateway {
			return i
		}
	}
	return -1
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
	mf.ClusterAddress(address)

	var err error
	require.NoError(f.t, state.Cluster.Close())
	store := gateway.ServerStore()
	dial := gateway.DialFunc()
	state.Cluster, err = db.OpenCluster(
		"db.bin", store, address, "/unused/db/dir", 5*time.Second, dqlite.WithDialFunc(dial))
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
