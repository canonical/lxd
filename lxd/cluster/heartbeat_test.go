package cluster_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/canonical/go-dqlite/v3/driver"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/cluster"
	clusterConfig "github.com/canonical/lxd/lxd/cluster/config"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/identity"
	"github.com/canonical/lxd/lxd/node"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/osarch"
	"github.com/canonical/lxd/shared/version"
)

// After a heartbeat request is completed, the leader updates the heartbeat
// timestamp column, and the serving node updates its cache of raft nodes.
func TestHeartbeat(t *testing.T) {
	f := heartbeatFixture{t: t}
	defer f.Cleanup()

	f.Bootstrap()
	f.Grow()
	f.Grow()

	time.Sleep(1 * time.Second) // Wait for join notifiation triggered heartbeats to complete.

	leader := f.Leader()
	leaderState := f.State(leader)

	// Artificially mark all nodes as down
	err := leaderState.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		members, err := tx.GetNodes(ctx)
		require.NoError(t, err)
		for _, member := range members {
			err := tx.SetNodeHeartbeat(member.Address, time.Now().Add(-time.Minute))
			require.NoError(t, err)
		}

		return nil
	})
	require.NoError(t, err)

	// Perform the heartbeat requests.
	leader.Cluster = leaderState.DB.Cluster
	heartbeat, _ := cluster.HeartbeatTask(leader)
	ctx := context.Background()
	heartbeat(ctx)

	// The heartbeat timestamps of all nodes got updated
	err = leaderState.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		members, err := tx.GetNodes(ctx)
		require.NoError(t, err)

		offlineThreshold, err := tx.GetNodeOfflineThreshold(ctx)
		require.NoError(t, err)

		for _, member := range members {
			assert.False(t, member.IsOffline(offlineThreshold))
		}

		return nil
	})
	require.NoError(t, err)
}

// A member that appears in the database while a heartbeat round is in progress must not change the round's
// results for the existing members. Regression test for #19071: re-reading the member list mid-round reset
// every existing member's heartbeat result, so the leader reported all of them, itself included, as unavailable.
func TestHeartbeatMemberAddedMidRound(t *testing.T) {
	f := heartbeatFixture{t: t}
	defer f.Cleanup()

	f.Bootstrap()
	f.Grow() // Returns once the join's notification heartbeats have completed.

	leader := f.Leader()
	leaderState := f.State(leader)

	// Artificially mark all members as down, so that the heartbeat times written by the round can be checked.
	var existingAddresses []string
	err := leaderState.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		members, err := tx.GetNodes(ctx)
		require.NoError(t, err)
		for _, member := range members {
			existingAddresses = append(existingAddresses, member.Address)
			err := tx.SetNodeHeartbeat(member.Address, time.Now().Add(-time.Minute))
			require.NoError(t, err)
		}

		return nil
	})
	require.NoError(t, err)
	require.Len(t, existingAddresses, 2)

	// When the follower receives the leader's heartbeat, add a member to the database before replying.
	// This leaves the database as a join landing mid-round would. The member's address refuses connections.
	var addMember sync.Once
	f.SetRequestHook(func(r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/internal/database" {
			return
		}

		addMember.Do(func() {
			err := leaderState.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				_, err := tx.CreateNode("joiner", "127.0.0.1:1")
				return err
			})
			assert.NoError(t, err)
		})
	})

	// Capture what the round passes to the leader's member refresh task.
	var hookCalled bool
	var unavailableMembers []string
	var hookMembers map[int64]cluster.APIHeartbeatMember
	leader.HeartbeatNodeHook = func(heartbeatData *cluster.APIHeartbeat, isLeader bool, unavailable []string, mode cluster.HeartbeatMode) {
		hookCalled = true
		unavailableMembers = unavailable
		hookMembers = heartbeatData.Members
	}

	// Perform a heartbeat round. The heartbeat interval is half the offline threshold, and a normal round
	// spreads its heartbeats over the interval minus 3s, so this threshold sends them all at once.
	leader.HeartbeatOfflineThreshold = 6 * time.Second
	leader.Cluster = leaderState.DB.Cluster
	heartbeat, _ := cluster.HeartbeatTask(leader)
	heartbeat(context.Background())

	// The member must have been added during the round, or this test checks nothing.
	err = leaderState.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		members, err := tx.GetNodes(ctx)
		require.NoError(t, err)
		assert.Len(t, members, 3)
		return nil
	})
	require.NoError(t, err)

	// Every existing member answered, so none is unavailable and all are online.
	require.True(t, hookCalled)
	assert.Empty(t, unavailableMembers)
	for _, member := range hookMembers {
		assert.True(t, member.Online, "member %q is not online", member.Address)
	}

	// The heartbeat times of the existing members got written.
	err = leaderState.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		members, err := tx.GetNodes(ctx)
		require.NoError(t, err)

		offlineThreshold, err := tx.GetNodeOfflineThreshold(ctx)
		require.NoError(t, err)

		for _, member := range members {
			if slices.Contains(existingAddresses, member.Address) {
				assert.False(t, member.IsOffline(offlineThreshold), "member %q is offline", member.Address)
			}
		}

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

	requestHook atomic.Pointer[func(*http.Request)] // Runs before every request to any member's HTTP server.
}

// Bootstrap the first node of the cluster.
func (f *heartbeatFixture) Bootstrap() *cluster.Gateway {
	f.t.Logf("create bootstrap node for test cluster")
	state, gateway, _ := f.node()
	state.ServerClustered = true

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
		targetState, target, name, address, cluster.SchemaVersion, len(version.APIExtensions), osarch.ARCH_64BIT_INTEL_X86)
	require.NoError(f.t, err)

	err = cluster.Join(state, gateway, target.NetworkCert(), target.ServerCert(), name, nodes)
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
			isLeader, err := gateway.IsLeader()
			if err != nil {
				f.t.Errorf("failed checking leadership: %v", err)
			}

			if isLeader {
				return gateway
			}
		}

		select {
		case <-ctx.Done():
			f.t.Errorf("no leader was elected within %s", timeout)
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
			isLeader, err := gateway.IsLeader()
			if err != nil {
				f.t.Errorf("failed checking leadership: %v", err)
			}

			if !isLeader {
				return gateway
			}
		}

		select {
		case <-ctx.Done():
			f.t.Errorf("no node running as follower")
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

	serverCert := shared.TestingKeyPair()
	state.ServerCert = func() *shared.CertInfo { return serverCert }

	gateway := newGateway(f.t, state.DB.Node, serverCert, state)
	f.cleanups = append(f.cleanups, func() { _ = gateway.Shutdown() })

	mux := http.NewServeMux()
	server := newServer(serverCert, f.withRequestHook(mux))

	for path, handler := range gateway.HandlerFuncs(nil, &identity.Cache{}) {
		mux.HandleFunc(path, handler)
	}

	address := server.Listener.Addr().String()
	mf := &membershipFixtures{t: f.t, state: state}
	mf.ClusterAddress(address)

	serverUUID, err := uuid.NewV7()
	require.NoError(f.t, err)

	require.NoError(f.t, state.DB.Cluster.Close())
	store := gateway.NodeStore()
	dial := gateway.DialFunc()
	state.DB.Cluster, err = db.OpenCluster(context.Background(), "db.bin", store, address, "/unused/db/dir", 5*time.Second, serverUUID.String(), driver.WithDialFunc(dial))
	require.NoError(f.t, err)

	err = state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		state.GlobalConfig, err = clusterConfig.Load(ctx, tx)
		if err != nil {
			return err
		}

		// Get the local node (will be used if clustered).
		state.ServerName, err = tx.GetLocalNodeName(ctx)
		if err != nil {
			return err
		}

		return nil
	})
	require.NoError(f.t, err)

	err = state.DB.Node.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		state.LocalConfig, err = node.ConfigLoad(ctx, tx)
		return err
	})
	require.NoError(f.t, err)

	f.gateways[len(f.gateways)] = gateway
	f.states[gateway] = state
	f.servers[gateway] = server

	return state, gateway, address
}

// SetRequestHook sets a function that runs before every request to any member's HTTP server is handled.
func (f *heartbeatFixture) SetRequestHook(hook func(*http.Request)) {
	if hook == nil {
		f.requestHook.Store(nil)
		return
	}

	f.requestHook.Store(&hook)
}

// withRequestHook wraps a member's HTTP handler so that it runs the hook set by SetRequestHook first.
func (f *heartbeatFixture) withRequestHook(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hook := f.requestHook.Load()
		if hook != nil {
			(*hook)(r)
		}

		handler.ServeHTTP(w, r)
	})
}

func (f *heartbeatFixture) Cleanup() {
	// Run the cleanups in reverse order
	for _, v := range slices.Backward(f.cleanups) {
		v()
	}

	for _, server := range f.servers {
		server.Close()
	}
}
