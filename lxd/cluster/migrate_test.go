package cluster_test

import (
	"database/sql/driver"
	"io/ioutil"
	"net"
	"os"
	"testing"

	dqlite "github.com/CanonicalLtd/go-dqlite"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"
)

// Test migrating legacy db data to the dqlite 1.0 format.
func TestMigrateToDqlite10(t *testing.T) {
	dir, cleanup := newLegacyRaftDir(t)
	defer cleanup()

	err := cluster.MigrateToDqlite10(dir)
	assert.NoError(t, err)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	address := listener.Addr().String()
	require.NoError(t, err)
	info := dqlite.ServerInfo{ID: uint64(1), Address: address}
	server, err := dqlite.NewServer(info, dir)
	require.NoError(t, err)
	defer server.Close()

	err = server.Bootstrap([]dqlite.ServerInfo{info})
	assert.EqualError(t, err, dqlite.ErrServerCantBootstrap.Error())

	err = server.Start(listener)
	require.NoError(t, err)

	store, err := dqlite.DefaultServerStore(":memory:")
	require.NoError(t, err)

	require.NoError(t, store.Set(context.Background(), []dqlite.ServerInfo{info}))

	drv, err := dqlite.NewDriver(store)
	require.NoError(t, err)

	conn, err := drv.Open("db.bin")
	require.NoError(t, err)
	defer conn.Close()

	queryer := conn.(driver.Queryer)
	rows, err := queryer.Query("SELECT name FROM containers", nil)
	require.NoError(t, err)

	values := make([]driver.Value, 1)
	require.NoError(t, rows.Next(values))

	assert.Equal(t, values[0], "c1")
}

// Return a new temporary directory with legacy raft data.
func newLegacyRaftDir(t *testing.T) (string, func()) {
	t.Helper()

	dir, err := ioutil.TempDir("", "lxd-cluster-test-")
	assert.NoError(t, err)

	err = shared.DirCopy("testdata/pre10", dir)
	assert.NoError(t, err)

	cleanup := func() {
		os.RemoveAll(dir)
	}

	return dir, cleanup
}
