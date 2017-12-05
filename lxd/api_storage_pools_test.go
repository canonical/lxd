package main

import (
	"testing"

	"github.com/lxc/lxd/shared/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Create a new pending storage pool using the targetNode query paramenter.
func TestStoragePoolsCreate_TargetNode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping storage-pools targetNode test in short mode.")
	}
	daemons, cleanup := newDaemons(t, 2)
	defer cleanup()

	f := clusterFixture{t: t}
	f.FormCluster(daemons)

	daemon := daemons[0]
	client := f.ClientUnix(daemon).ClusterTargetNode("rusp-0")

	poolPost := api.StoragePoolsPost{
		Name:   "mypool",
		Driver: "dir",
	}
	poolPost.Config = map[string]string{
		"source": "",
	}

	err := client.CreateStoragePool(poolPost)
	require.NoError(t, err)

	pool, _, err := client.GetStoragePool("mypool")
	require.NoError(t, err)

	assert.Equal(t, "PENDING", pool.State)
}
