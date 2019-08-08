package cluster_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	dqlite "github.com/canonical/go-dqlite"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logging"
	"github.com/stretchr/testify/require"
)

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
