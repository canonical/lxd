package node_test

import (
	"database/sql"
	"testing"

	"github.com/lxc/lxd/lxd/db/node"
	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateFromV36_RaftNodes(t *testing.T) {
	schema := node.Schema()
	db, err := schema.ExerciseUpdate(37, nil)
	require.NoError(t, err)

	_, err = db.Exec("INSERT INTO raft_nodes VALUES (1, '1.2.3.4:666')")
	require.NoError(t, err)
}

// All model tables previously in the node database have been migrated to the
// cluster database, and dropped from the node database.
func TestUpdateFromV36_DropTables(t *testing.T) {
	schema := node.Schema()
	db, err := schema.ExerciseUpdate(37, nil)
	require.NoError(t, err)

	var current []string
	err = query.Transaction(db, func(tx *sql.Tx) error {
		var err error
		stmt := "SELECT name FROM sqlite_master WHERE type='table'"
		current, err = query.SelectStrings(tx, stmt)
		return err
	})
	require.NoError(t, err)
	deleted := []string{
		"networks",
		"networks_config",
	}
	for _, name := range deleted {
		assert.False(t, shared.StringInSlice(name, current))
	}
}
