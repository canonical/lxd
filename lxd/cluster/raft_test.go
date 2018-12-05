package cluster_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/CanonicalLtd/go-dqlite"
	"github.com/CanonicalLtd/raft-test"
	"github.com/hashicorp/raft"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// By default a node starts in single mode.
func TestRaftFactory_Single(t *testing.T) {
	db, cleanup := db.NewTestNode(t)
	defer cleanup()

	cert := shared.TestingKeyPair()

	instance := newRaft(t, db, cert)
	defer instance.Shutdown()

	rafttest.WaitLeader(t, instance.Raft(), time.Second)
	assert.Equal(t, raft.Leader, instance.Raft().State())
}

// If there's a network address configured, but we are the only raft node in
// the factory starts raft in single mode.
func TestRaftFactory_SingleWithNetworkAddress(t *testing.T) {
	db, cleanup := db.NewTestNode(t)
	defer cleanup()

	cert := shared.TestingKeyPair()

	setRaftRole(t, db, "1.2.3.4:666")

	instance := newRaft(t, db, cert)
	defer instance.Shutdown()

	rafttest.WaitLeader(t, instance.Raft(), time.Second)
	assert.Equal(t, raft.Leader, instance.Raft().State())
}

// When the factory is started the first time on a non-clustered node, it will
// use the memory transport and the raft node will not have a real network
// address. The in-memory address gets saved in the first log committed in the
// store as the address of the server with ID "1". If the LXD instance is then
// reconfigured to enable clustering, we now use a real network transport and
// setup a ServerAddressProvider that will override the initial in-memory
// address of node "1" with its real network address, as configured in the
// raft_nodes table.
func TestRaftFactory_TransitionToClusteredMode(t *testing.T) {
	db, cleanup := db.NewTestNode(t)
	defer cleanup()

	cert := shared.TestingKeyPair()

	instance := newRaft(t, db, cert)
	instance.Shutdown()

	setRaftRole(t, db, "1.2.3.4:666")

	instance = newRaft(t, db, cert)
	defer instance.Shutdown()

	rafttest.WaitLeader(t, instance.Raft(), time.Second)
	assert.Equal(t, raft.Leader, instance.Raft().State())
}

// If there is more than one node, the raft object is created with
// cluster-compatible parameters..
func TestRaftFactory_MultiNode(t *testing.T) {
	cert := shared.TestingKeyPair()

	leader := ""
	for i := 0; i < 2; i++ {
		db, cleanup := db.NewTestNode(t)
		defer cleanup()

		mux := http.NewServeMux()
		server := newServer(cert, mux)
		defer server.Close()

		address := server.Listener.Addr().String()
		setRaftRole(t, db, address)

		instance := newRaft(t, db, cert)
		defer instance.Shutdown()
		if i == 0 {
			leader = address
			rafttest.WaitLeader(t, instance.Raft(), time.Second)
		}

		mux.HandleFunc("/internal/raft", instance.HandlerFunc())

		if i > 0 {
			id := raft.ServerID(strconv.Itoa(i + 1))
			target := raft.ServerAddress(leader)
			err := instance.MembershipChanger().Join(id, target, 5*time.Second)
			require.NoError(t, err)
		}
	}
}

// Create a new test RaftInstance.
func newRaft(t *testing.T, db *db.Node, cert *shared.CertInfo) *cluster.RaftInstance {
	logging.Testing(t)
	instance, err := cluster.NewRaft(db, cert, 0.2)
	require.NoError(t, err)
	return instance
}

// Set the cluster.https_address config key to the given address, and insert the
// address into the raft_nodes table.
//
// This effectively makes the node act as a database raft node.
func setRaftRole(t *testing.T, database *db.Node, address string) *dqlite.DatabaseServerStore {
	require.NoError(t, database.Transaction(func(tx *db.NodeTx) error {
		err := tx.UpdateConfig(map[string]string{"cluster.https_address": address})
		if err != nil {
			return err
		}
		_, err = tx.RaftNodeAdd(address)
		return err
	}))

	store := dqlite.NewServerStore(database.DB(), "main", "raft_nodes", "address")
	return store
}

// Create a new test HTTP server configured with the given TLS certificate and
// using the given handler.
func newServer(cert *shared.CertInfo, handler http.Handler) *httptest.Server {
	server := httptest.NewUnstartedServer(handler)
	server.TLS = util.ServerTLSConfig(cert)
	server.StartTLS()
	return server
}
