package db_test

import (
	"testing"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/db/query"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Node database objects automatically initialize their schema as needed.
func TestNode_Schema(t *testing.T) {
	node, cleanup := db.NewTestNode(t)
	defer cleanup()

	// The underlying node-level database has exactly one row in the schema
	// table.
	db := node.DB()
	tx, err := db.Begin()
	require.NoError(t, err)
	n, err := query.Count(tx, "schema", "")
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	assert.NoError(t, tx.Commit())
	assert.NoError(t, db.Close())
}

// A gRPC SQL connection is established when starting to interact with the
// cluster database.
func TestCluster_Setup(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()

	// The underlying node-level database has exactly one row in the schema
	// table.
	db := cluster.DB()
	tx, err := db.Begin()
	require.NoError(t, err)
	n, err := query.Count(tx, "schema", "")
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	assert.NoError(t, tx.Commit())
	assert.NoError(t, db.Close())
}
