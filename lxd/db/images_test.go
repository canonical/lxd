package db_test

import (
	"testing"
	"time"

	"github.com/lxc/lxd/lxd/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImageLocate(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()

	err := cluster.ImageInsert(
		"default", "abc", "x.gz", 16, false, false, "amd64", time.Now(), time.Now(), map[string]string{})
	require.NoError(t, err)

	address, err := cluster.ImageLocate("abc")
	require.NoError(t, err)
	assert.Equal(t, "", address)

	// Pretend that the function is being run on another node.
	cluster.NodeID(2)
	address, err = cluster.ImageLocate("abc")
	require.NoError(t, err)
	assert.Equal(t, "0.0.0.0", address)

	// Pretend that the target node is down
	err = cluster.Transaction(func(tx *db.ClusterTx) error {
		return tx.NodeHeartbeat("0.0.0.0", time.Now().Add(-time.Minute))
	})
	require.NoError(t, err)

	address, err = cluster.ImageLocate("abc")
	require.Equal(t, "", address)
	require.EqualError(t, err, "image not available on any online node")
}
