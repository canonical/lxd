package cluster_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Basic creation and shutdown. By default, the gateway runs an in-memory gRPC
// server.
func TestGateway_Single(t *testing.T) {
	db, cleanup := db.NewTestNode(t)
	defer cleanup()

	cert := shared.TestingKeyPair()
	gateway := newGateway(t, db, cert)
	defer gateway.Shutdown()

	dialer := gateway.Dialer()
	conn, err := dialer()
	assert.NoError(t, err)
	assert.NotNil(t, conn)
}

// Create a new test Gateway with the given parameters, and ensure no error
// happens.
func newGateway(t *testing.T, db *db.Node, certInfo *shared.CertInfo) *cluster.Gateway {
	logging.Testing(t)
	require.NoError(t, os.Mkdir(filepath.Join(db.Dir(), "raft"), 0755))
	gateway, err := cluster.NewGateway(db, certInfo, 0.2)
	require.NoError(t, err)
	return gateway
}
