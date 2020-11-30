package cluster_test

import (
	"context"
	sqldriver "database/sql/driver"
	"io/ioutil"
	"os"
	"testing"

	dqlite "github.com/canonical/go-dqlite"
	"github.com/canonical/go-dqlite/driver"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test migrating legacy db data to the dqlite 1.0 format.
func TestMigrateToDqlite10(t *testing.T) {
	dir, cleanup := newLegacyRaftDir(t)
	defer cleanup()

	err := cluster.MigrateToDqlite10(dir)
	assert.NoError(t, err)

	require.NoError(t, err)
	id := uint64(1)
	address := "@1"
	server, err := dqlite.New(id, address, dir, dqlite.WithBindAddress(address))
	require.NoError(t, err)
	defer server.Close()

	err = server.Start()
	require.NoError(t, err)

	store, err := driver.DefaultNodeStore(":memory:")
	require.NoError(t, err)

	require.NoError(t, store.Set(context.Background(), []driver.NodeInfo{{ID: id, Address: address}}))

	drv, err := driver.New(store)
	require.NoError(t, err)

	conn, err := drv.Open("db.bin")
	require.NoError(t, err)
	defer conn.Close()

	queryer := conn.(sqldriver.Queryer)
	rows, err := queryer.Query("SELECT name FROM containers", nil)
	require.NoError(t, err)

	values := make([]sqldriver.Value, 1)
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
