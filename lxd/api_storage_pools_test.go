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

// An error is returned when trying to create a new storage pool in a cluster
// where the pool was not defined on all nodes.
func TestStoragePoolsCreate_MissingNodes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping storage-pools targetNode test in short mode.")
	}
	daemons, cleanup := newDaemons(t, 2)
	defer cleanup()

	f := clusterFixture{t: t}
	f.FormCluster(daemons)

	// Define the pool on rusp-0.
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

	// Trying to create the pool now results in an error, since it's not
	// defined on all nodes.
	poolPost = api.StoragePoolsPost{
		Name:   "mypool",
		Driver: "dir",
	}
	client = f.ClientUnix(daemon)
	err = client.CreateStoragePool(poolPost)
	require.EqualError(t, err, "Pool not defined on nodes: buzz")
}
