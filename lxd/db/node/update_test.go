package node_test

import (
	"testing"

	"github.com/lxc/lxd/lxd/db/node"
	"github.com/stretchr/testify/require"
)

func TestUpdateFromV36(t *testing.T) {
	schema := node.Schema()
	db, err := schema.ExerciseUpdate(37, nil)
	require.NoError(t, err)

	_, err = db.Exec("INSERT INTO raft_nodes VALUES (1, '1.2.3.4:666')")
	require.NoError(t, err)
}
