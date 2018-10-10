package db

import (
	"context"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"testing"
	"time"

	"github.com/CanonicalLtd/go-dqlite"
	"github.com/CanonicalLtd/raft-test"
	"github.com/hashicorp/raft"
	"github.com/stretchr/testify/require"
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
	// Create an in-memory dqlite SQL server and associated store.
	store, serverCleanup := newDqliteServer(t)

	log := newLogFunc(t)

	dial := func(ctx context.Context, address string) (net.Conn, error) {
		return net.Dial("unix", address)
	}

	cluster, err := OpenCluster(
		"test.db", store, "1", "/unused/db/dir", 5*time.Second,
		dqlite.WithLogFunc(log), dqlite.WithDialFunc(dial))
	require.NoError(t, err)

	cleanup := func() {
		require.NoError(t, cluster.Close())
		serverCleanup()
	}

	return cluster, cleanup
}

// NewTestClusterTx returns a fresh ClusterTx object, along with a function that can
// be called to cleanup state when done with it.
func NewTestClusterTx(t *testing.T) (*ClusterTx, func()) {
	cluster, clusterCleanup := NewTestCluster(t)

	var err error

	clusterTx := &ClusterTx{nodeID: cluster.nodeID, stmts: cluster.stmts}
	clusterTx.tx, err = cluster.db.Begin()
	require.NoError(t, err)

	cleanup := func() {
		err := clusterTx.tx.Commit()
		require.NoError(t, err)
		clusterCleanup()
	}

	return clusterTx, cleanup
}

// Create a new in-memory dqlite server.
//
// Return the newly created server store can be used to connect to it.
func newDqliteServer(t *testing.T) (*dqlite.DatabaseServerStore, func()) {
	t.Helper()

	listener, err := net.Listen("unix", "")
	require.NoError(t, err)

	address := listener.Addr().String()

	store, err := dqlite.DefaultServerStore(":memory:")
	require.NoError(t, err)
	require.NoError(t, store.Set(context.Background(), []dqlite.ServerInfo{{Address: address}}))

	id := fmt.Sprintf("%d", dqliteSerial)
	dqliteSerial++
	registry := dqlite.NewRegistry(id)

	fsm := dqlite.NewFSM(registry)

	r, raftCleanup := rafttest.Server(t, fsm, rafttest.Transport(func(i int) raft.Transport {
		require.Equal(t, i, 0)
		address := raft.ServerAddress(listener.Addr().String())
		_, transport := raft.NewInmemTransport(address)
		return transport
	}))

	log := newLogFunc(t)

	server, err := dqlite.NewServer(
		r, registry, listener, dqlite.WithServerLogFunc(log))
	require.NoError(t, err)

	cleanup := func() {
		require.NoError(t, server.Close())
		raftCleanup()
	}

	return store, cleanup
}

var dqliteSerial = 0

func newLogFunc(t *testing.T) dqlite.LogFunc {
	return func(l dqlite.LogLevel, format string, a ...interface{}) {
		format = fmt.Sprintf("%s: %s", l.String(), format)
		t.Logf(format, a...)
	}

}
