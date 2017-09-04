package node_test

import (
	"database/sql"
	"io/ioutil"
	"os"
	"testing"

	"github.com/lxc/lxd/lxd/db/node"
	"github.com/lxc/lxd/lxd/db/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpen(t *testing.T) {
	dir, cleanup := newDir(t)
	defer cleanup()

	db, err := node.Open(dir)
	defer db.Close()
	require.NoError(t, err)
}

// When the node-local database is not created from scratch, the value for the
// initial patch is not 0.
func TestEnsureSchema(t *testing.T) {
	dir, cleanup := newDir(t)
	defer cleanup()

	db, err := node.Open(dir)
	defer db.Close()

	schema := schema.New([]schema.Update{func(*sql.Tx) error { return nil }})

	hookHasRun := false
	hook := func(int, *sql.Tx) error {
		hookHasRun = true
		return nil
	}
	initial, err := node.EnsureSchema(db, dir, schema, hook)
	require.NoError(t, err)
	assert.Equal(t, 0, initial)
	assert.True(t, hookHasRun)
}

// Create a new temporary directory, along with a function to clean it up.
func newDir(t *testing.T) (string, func()) {
	dir, err := ioutil.TempDir("", "lxd-db-node-test-")
	require.NoError(t, err)

	cleanup := func() {
		require.NoError(t, os.RemoveAll(dir))
	}

	return dir, cleanup
}
