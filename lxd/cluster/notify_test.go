package cluster_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/node"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The returned notifier connects to all nodes.
func TestNewNotifier(t *testing.T) {
	state, cleanup := state.NewTestState(t)
	defer cleanup()

	cert := shared.TestingKeyPair()

	f := notifyFixtures{t: t, state: state}
	defer f.Nodes(cert, 3)()

	notifier, err := cluster.NewNotifier(state, cert, cluster.NotifyAll)
	require.NoError(t, err)

	peers := make(chan string, 2)
	hook := func(client lxd.ContainerServer) error {
		server, _, err := client.GetServer()
		require.NoError(t, err)
		peers <- server.Config["cluster.https_address"].(string)
		return nil
	}
	assert.NoError(t, notifier(hook))

	addresses := make([]string, 2)
	for i := range addresses {
		select {
		case addresses[i] = <-peers:
		default:
		}
	}
	require.NoError(t, err)
	for i := range addresses {
		assert.True(t, shared.StringInSlice(f.Address(i+1), addresses))
	}
}

// Creating a new notifier fails if the policy is set to NotifyAll and one of
// the nodes is down.
func TestNewNotify_NotifyAllError(t *testing.T) {
	state, cleanup := state.NewTestState(t)
	defer cleanup()

	cert := shared.TestingKeyPair()

	f := notifyFixtures{t: t, state: state}
	defer f.Nodes(cert, 3)()

	f.Down(1)
	notifier, err := cluster.NewNotifier(state, cert, cluster.NotifyAll)
	assert.Nil(t, notifier)
	require.Error(t, err)
	assert.Regexp(t, "peer node .+ is down", err.Error())
}

// Creating a new notifier does not fail if the policy is set to NotifyAlive
// and one of the nodes is down, however dead nodes are ignored.
func TestNewNotify_NotifyAlive(t *testing.T) {
	state, cleanup := state.NewTestState(t)
	defer cleanup()

	cert := shared.TestingKeyPair()

	f := notifyFixtures{t: t, state: state}
	defer f.Nodes(cert, 3)()

	f.Down(1)
	notifier, err := cluster.NewNotifier(state, cert, cluster.NotifyAlive)
	assert.NoError(t, err)

	i := 0
	hook := func(client lxd.ContainerServer) error {
		i++
		return nil
	}
	assert.NoError(t, notifier(hook))
	assert.Equal(t, 1, i)
}

// Helper for setting fixtures for Notify tests.
type notifyFixtures struct {
	t     *testing.T
	state *state.State
}

// Spawn the given number of fake nodes, save in them in the database and
// return a cleanup function.
//
// The address of the first node spawned will be saved as local
// cluster.https_address.
func (h *notifyFixtures) Nodes(cert *shared.CertInfo, n int) func() {
	servers := make([]*httptest.Server, n)
	for i := 0; i < n; i++ {
		servers[i] = newRestServer(cert)
	}

	// Insert new entries in the nodes table of the cluster database.
	err := h.state.Cluster.Transaction(func(tx *db.ClusterTx) error {
		for i := 0; i < n; i++ {
			name := strconv.Itoa(i)
			address := servers[i].Listener.Addr().String()
			var err error
			if i == 0 {
				err = tx.NodeUpdate(int64(1), name, address)
			} else {
				_, err = tx.NodeAdd(name, address)
			}
			require.NoError(h.t, err)
		}
		return nil
	})
	require.NoError(h.t, err)

	// Set the address in the config table of the node database.
	err = h.state.Node.Transaction(func(tx *db.NodeTx) error {
		config, err := node.ConfigLoad(tx)
		require.NoError(h.t, err)
		address := servers[0].Listener.Addr().String()
		values := map[string]interface{}{"cluster.https_address": address}
		_, err = config.Patch(values)
		require.NoError(h.t, err)
		return nil
	})
	require.NoError(h.t, err)

	cleanup := func() {
		for _, server := range servers {
			server.Close()
		}
	}

	return cleanup
}

// Return the network address of the i-th node.
func (h *notifyFixtures) Address(i int) string {
	var address string
	err := h.state.Cluster.Transaction(func(tx *db.ClusterTx) error {
		nodes, err := tx.Nodes()
		require.NoError(h.t, err)
		address = nodes[i].Address
		return nil
	})
	require.NoError(h.t, err)
	return address
}

// Mark the i'th node as down.
func (h *notifyFixtures) Down(i int) {
	err := h.state.Cluster.Transaction(func(tx *db.ClusterTx) error {
		nodes, err := tx.Nodes()
		require.NoError(h.t, err)
		err = tx.NodeHeartbeat(nodes[i].Address, time.Now().Add(-time.Minute))
		require.NoError(h.t, err)
		return nil
	})
	require.NoError(h.t, err)
}

// Returns a minimal stub for the LXD RESTful API server, just realistic
// enough to make lxd.ConnectLXD succeed.
func newRestServer(cert *shared.CertInfo) *httptest.Server {
	mux := http.NewServeMux()

	server := httptest.NewUnstartedServer(mux)
	server.TLS = util.ServerTLSConfig(cert)
	server.StartTLS()

	mux.HandleFunc("/1.0/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		config := map[string]interface{}{"cluster.https_address": server.Listener.Addr().String()}
		metadata := api.ServerPut{Config: config}
		util.WriteJSON(w, api.ResponseRaw{Metadata: metadata}, false)
	})

	return server
}
