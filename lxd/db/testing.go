package db

import (
	"io/ioutil"
	"net"
	"os"
	"testing"
	"time"

	"github.com/CanonicalLtd/go-grpc-sql"
	"github.com/CanonicalLtd/go-sqlite3"
	"github.com/lxc/lxd/lxd/util"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// NewTestNode creates a new Node for testing purposes, along with a function
// that can be used to clean it up when done.
func NewTestNode(t *testing.T) (*Node, func()) {
	dir, err := ioutil.TempDir("", "lxd-db-test-node-")
	require.NoError(t, err)

	db, _, err := OpenNode(dir, nil, nil)
	require.NoError(t, err)

	cleanup := func() {
		require.NoError(t, db.Close())
		require.NoError(t, os.RemoveAll(dir))
	}

	return db, cleanup
}

// NewTestNodeTx returns a fresh NodeTx object, along with a function that can
// be called to cleanup state when done with it.
func NewTestNodeTx(t *testing.T) (*NodeTx, func()) {
	node, nodeCleanup := NewTestNode(t)

	var err error

	nodeTx := &NodeTx{}
	nodeTx.tx, err = node.db.Begin()
	require.NoError(t, err)

	cleanup := func() {
		require.NoError(t, nodeTx.tx.Commit())
		nodeCleanup()
	}

	return nodeTx, cleanup
}

// NewTestCluster creates a new Cluster for testing purposes, along with a function
// that can be used to clean it up when done.
func NewTestCluster(t *testing.T) (*Cluster, func()) {
	// Create an in-memory gRPC SQL server and dialer.
	server, dialer := newGrpcServer()

	cluster, err := OpenCluster(":memory:", dialer, "1", "/unused/db/dir")
	require.NoError(t, err)

	cleanup := func() {
		require.NoError(t, cluster.Close())
		server.Stop()
	}

	return cluster, cleanup
}

// NewTestClusterTx returns a fresh ClusterTx object, along with a function that can
// be called to cleanup state when done with it.
func NewTestClusterTx(t *testing.T) (*ClusterTx, func()) {
	cluster, clusterCleanup := NewTestCluster(t)

	var err error

	clusterTx := &ClusterTx{nodeID: cluster.nodeID}
	clusterTx.tx, err = cluster.db.Begin()
	require.NoError(t, err)

	cleanup := func() {
		err := clusterTx.tx.Commit()
		require.NoError(t, err)
		clusterCleanup()
	}

	return clusterTx, cleanup
}

// Create a new in-memory gRPC server attached to a grpc-sql gateway backed by a
// SQLite driver.
//
// Return the newly created gRPC server and a dialer that can be used to
// connect to it.
func newGrpcServer() (*grpc.Server, grpcsql.Dialer) {
	listener, dial := util.InMemoryNetwork()
	server := grpcsql.NewServer(&sqlite3.SQLiteDriver{})

	// Setup an in-memory gRPC dialer.
	options := []grpc.DialOption{
		grpc.WithInsecure(),
		grpc.WithDialer(func(string, time.Duration) (net.Conn, error) {
			return dial(), nil
		}),
	}
	dialer := func() (*grpc.ClientConn, error) {
		return grpc.Dial("", options...)
	}

	go server.Serve(listener)
	return server, dialer
}
