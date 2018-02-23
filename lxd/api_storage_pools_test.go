package main

import (
	"testing"

	"github.com/lxc/lxd/shared/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Create a new pending storage pool using the targetNode query paramenter.
func TestStoragePoolsCreate_TargetNode(t *testing.T) {
	daemons, cleanup := newDaemons(t, 2)
	defer cleanup()

	f := clusterFixture{t: t}
	f.FormCluster(daemons)

	daemon := daemons[0]
	client := f.ClientUnix(daemon).UseTarget("rusp-0")

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

	assert.Equal(t, "Pending", pool.Status)

	// If a storage pool is pending, deleting it just means removing the
	// relevant rows from the database.
	err = client.DeleteStoragePool("mypool")
	require.NoError(t, err)

	_, _, err = client.GetStoragePool("mypool")
	require.EqualError(t, err, "not found")
}

// An error is returned when trying to create a new storage pool in a cluster
// where the pool was not defined on any node nodes.
func TestStoragePoolsCreate_NotDefined(t *testing.T) {
	daemons, cleanup := newDaemons(t, 2)
	defer cleanup()

	f := clusterFixture{t: t}
	f.FormCluster(daemons)

	// Trying to create the pool now results in an error, since it's not
	// defined on any node.
	poolPost := api.StoragePoolsPost{
		Name:   "mypool",
		Driver: "dir",
	}
	client := f.ClientUnix(daemons[0])
	err := client.CreateStoragePool(poolPost)
	require.EqualError(t, err, "Pool not pending on any node (use --target <node> first)")
}

// An error is returned when trying to create a new storage pool in a cluster
// where the pool was not defined on all nodes.
func TestStoragePoolsCreate_MissingNodes(t *testing.T) {
	daemons, cleanup := newDaemons(t, 2)
	defer cleanup()

	f := clusterFixture{t: t}
	f.FormCluster(daemons)

	// Define the pool on rusp-0.
	daemon := daemons[0]
	client := f.ClientUnix(daemon).UseTarget("rusp-0")

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
