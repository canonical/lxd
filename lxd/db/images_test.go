// +build linux,cgo,!agent

package db_test

import (
	"testing"
	"time"

	"github.com/lxc/lxd/lxd/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocateImage(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()

	err := cluster.CreateImage(
		"default", "abc", "x.gz", 16, false, false, "amd64", time.Now(), time.Now(), map[string]string{}, "container")
	require.NoError(t, err)

	address, err := cluster.LocateImage("abc")
	require.NoError(t, err)
	assert.Equal(t, "", address)

	// Pretend that the function is being run on another node.
	cluster.NodeID(2)
	address, err = cluster.LocateImage("abc")
	require.NoError(t, err)
	assert.Equal(t, "0.0.0.0", address)

	// Pretend that the target node is down
	err = cluster.Transaction(func(tx *db.ClusterTx) error {
		return tx.SetNodeHeartbeat("0.0.0.0", time.Now().Add(-time.Minute))
	})
	require.NoError(t, err)

	address, err = cluster.LocateImage("abc")
	require.Equal(t, "", address)
	require.EqualError(t, err, "Image not available on any online node")
}

func TestImageExists(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()

	exists, err := cluster.ImageExists("default", "abc")
	require.NoError(t, err)

	assert.False(t, exists)

	err = cluster.CreateImage(
		"default", "abc", "x.gz", 16, false, false, "amd64", time.Now(), time.Now(), map[string]string{}, "container")
	require.NoError(t, err)

	exists, err = cluster.ImageExists("default", "abc")
	require.NoError(t, err)

	assert.True(t, exists)
}
