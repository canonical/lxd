package db_test

import (
	"testing"
	"time"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Containers are grouped by node address.
func TestContainersListByNodeAddress(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	nodeID1 := int64(1) // This is the default local node

	nodeID2, err := tx.NodeAdd("node2", "1.2.3.4:666")
	require.NoError(t, err)

	nodeID3, err := tx.NodeAdd("node3", "5.6.7.8:666")
	require.NoError(t, err)
	require.NoError(t, tx.NodeHeartbeat("5.6.7.8:666", time.Now().Add(-time.Minute)))

	addContainer(t, tx, nodeID2, "c1")
	addContainer(t, tx, nodeID1, "c2")
	addContainer(t, tx, nodeID3, "c3")
	addContainer(t, tx, nodeID2, "c4")

	result, err := tx.ContainersListByNodeAddress()
	require.NoError(t, err)
	assert.Equal(
		t,
		map[string][]string{
			"":            {"c2"},
			"1.2.3.4:666": {"c1", "c4"},
			"0.0.0.0":     {"c3"},
		}, result)
}

// Containers are associated with their node name.
func TestContainersByNodeName(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	nodeID1 := int64(1) // This is the default local node

	nodeID2, err := tx.NodeAdd("node2", "1.2.3.4:666")
	require.NoError(t, err)

	addContainer(t, tx, nodeID2, "c1")
	addContainer(t, tx, nodeID1, "c2")

	result, err := tx.ContainersByNodeName()
	require.NoError(t, err)
	assert.Equal(
		t,
		map[string]string{
			"c1": "node2",
			"c2": "none",
		}, result)
}

func TestContainerPool(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()

	poolID, err := cluster.StoragePoolCreate("default", "", "dir", nil)
	require.NoError(t, err)
	_, err = cluster.StoragePoolVolumeCreate("c1", "", db.StoragePoolVolumeTypeContainer, false, poolID, nil)
	require.NoError(t, err)

	args := db.ContainerArgs{
		Name: "c1",
		Devices: types.Devices{
			"root": types.Device{
				"path": "/",
				"pool": "default",
				"type": "disk",
			},
		},
	}
	_, err = cluster.ContainerCreate(args)
	require.NoError(t, err)
	poolName, err := cluster.ContainerPool("c1")
	require.NoError(t, err)
	assert.Equal(t, "default", poolName)
}

// Only containers running on the local node are returned.
func TestContainersNodeList(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()

	nodeID1 := int64(1) // This is the default local node

	// Add another node
	var nodeID2 int64
	err := cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		nodeID2, err = tx.NodeAdd("node2", "1.2.3.4:666")
		require.NoError(t, err)
		addContainer(t, tx, nodeID1, "c1")
		addContainer(t, tx, nodeID2, "c2")
		return nil
	})
	require.NoError(t, err)

	names, err := cluster.ContainersNodeList(db.CTypeRegular)
	require.NoError(t, err)
	assert.Equal(t, names, []string{"c1"})
}

// All containers are loaded in bulk.
func TestContainerArgsList(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	nodeID1 := int64(1) // This is the default local node

	nodeID2, err := tx.NodeAdd("node2", "1.2.3.4:666")
	require.NoError(t, err)

	addContainer(t, tx, nodeID2, "c1")
	addContainer(t, tx, nodeID1, "c2")
	addContainer(t, tx, nodeID1, "c3")
	addContainer(t, tx, nodeID1, "c4")

	addContainerConfig(t, tx, "c2", "x", "y")
	addContainerConfig(t, tx, "c3", "z", "w")
	addContainerConfig(t, tx, "c3", "a", "b")

	addContainerDevice(t, tx, "c2", "eth0", "nic", nil)
	addContainerDevice(t, tx, "c4", "root", "disk", map[string]string{"x": "y"})

	containers, err := tx.ContainerArgsList()
	require.NoError(t, err)
	assert.Len(t, containers, 4)

	assert.Equal(t, "c1", containers[0].Name)
	assert.Equal(t, "c2", containers[1].Name)
	assert.Equal(t, "c3", containers[2].Name)
	assert.Equal(t, "c4", containers[3].Name)

	assert.Equal(t, "node2", containers[0].Node)
	assert.Equal(t, "none", containers[1].Node)
	assert.Equal(t, "none", containers[2].Node)
	assert.Equal(t, "none", containers[3].Node)

	assert.Equal(t, map[string]string{}, containers[0].Config)
	assert.Equal(t, map[string]string{"x": "y"}, containers[1].Config)
	assert.Equal(t, map[string]string{"z": "w", "a": "b"}, containers[2].Config)
	assert.Equal(t, map[string]string{}, containers[3].Config)

	assert.Equal(t, types.Devices{}, containers[0].Devices)
	assert.Equal(t, types.Devices{"eth0": map[string]string{"type": "nic"}}, containers[1].Devices)
	assert.Equal(t, types.Devices{}, containers[2].Devices)
	assert.Equal(t, types.Devices{"root": map[string]string{"type": "disk", "x": "y"}}, containers[3].Devices)
}

// All containers on a node are loaded in bulk.
func TestContainerArgsNodeList(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	nodeID1 := int64(1) // This is the default local node

	nodeID2, err := tx.NodeAdd("node2", "1.2.3.4:666")
	require.NoError(t, err)

	addContainer(t, tx, nodeID2, "c1")
	addContainer(t, tx, nodeID1, "c2")
	addContainer(t, tx, nodeID1, "c3")
	addContainer(t, tx, nodeID1, "c4")

	addContainerConfig(t, tx, "c2", "x", "y")
	addContainerConfig(t, tx, "c3", "z", "w")
	addContainerConfig(t, tx, "c3", "a", "b")

	addContainerDevice(t, tx, "c2", "eth0", "nic", nil)
	addContainerDevice(t, tx, "c4", "root", "disk", map[string]string{"x": "y"})

	containers, err := tx.ContainerArgsNodeList()
	require.NoError(t, err)
	assert.Len(t, containers, 3)

	assert.Equal(t, "c2", containers[0].Name)
	assert.Equal(t, "c3", containers[1].Name)
	assert.Equal(t, "c4", containers[2].Name)

	assert.Equal(t, "none", containers[0].Node)
	assert.Equal(t, "none", containers[1].Node)
	assert.Equal(t, "none", containers[2].Node)

	assert.Equal(t, map[string]string{"x": "y"}, containers[0].Config)
	assert.Equal(t, map[string]string{"z": "w", "a": "b"}, containers[1].Config)
	assert.Equal(t, map[string]string{}, containers[2].Config)

	assert.Equal(t, types.Devices{"eth0": map[string]string{"type": "nic"}}, containers[0].Devices)
	assert.Equal(t, types.Devices{}, containers[1].Devices)
	assert.Equal(t, types.Devices{"root": map[string]string{"type": "disk", "x": "y"}}, containers[2].Devices)
}

func addContainer(t *testing.T, tx *db.ClusterTx, nodeID int64, name string) {
	stmt := `
INSERT INTO containers(node_id, name, architecture, type) VALUES (?, ?, 1, ?)
`
	_, err := tx.Tx().Exec(stmt, nodeID, name, db.CTypeRegular)
	require.NoError(t, err)
}

func addContainerConfig(t *testing.T, tx *db.ClusterTx, container, key, value string) {
	id := getContainerID(t, tx, container)

	stmt := `
INSERT INTO containers_config(container_id, key, value) VALUES (?, ?, ?)
`
	_, err := tx.Tx().Exec(stmt, id, key, value)
	require.NoError(t, err)
}

func addContainerDevice(t *testing.T, tx *db.ClusterTx, container, name, typ string, config map[string]string) {
	id := getContainerID(t, tx, container)

	code, err := db.DeviceTypeToInt(typ)
	require.NoError(t, err)

	stmt := `
INSERT INTO containers_devices(container_id, name, type) VALUES (?, ?, ?)
`
	_, err = tx.Tx().Exec(stmt, id, name, code)
	require.NoError(t, err)

	deviceID := getDeviceID(t, tx, id, name)

	for key, value := range config {
		stmt := `
INSERT INTO containers_devices_config(container_device_id, key, value) VALUES (?, ?, ?)
`
		_, err = tx.Tx().Exec(stmt, deviceID, key, value)
		require.NoError(t, err)
	}
}

// Return the container ID given its name.
func getContainerID(t *testing.T, tx *db.ClusterTx, name string) int64 {
	var id int64

	stmt := "SELECT id FROM containers WHERE name=?"
	row := tx.Tx().QueryRow(stmt, name)
	err := row.Scan(&id)
	require.NoError(t, err)

	return id
}

// Return the device ID given its container ID and name.
func getDeviceID(t *testing.T, tx *db.ClusterTx, containerID int64, name string) int64 {
	var id int64

	stmt := "SELECT id FROM containers_devices WHERE container_id=? AND name=?"
	row := tx.Tx().QueryRow(stmt, containerID, name)
	err := row.Scan(&id)
	require.NoError(t, err)

	return id
}
