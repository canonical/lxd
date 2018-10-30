package db_test

import (
	"testing"
	"time"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/types"
	"github.com/lxc/lxd/shared/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContainerList(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	nodeID1 := int64(1) // This is the default local node

	nodeID2, err := tx.NodeAdd("node2", "1.2.3.4:666")
	require.NoError(t, err)

	addContainer(t, tx, nodeID2, "c1")
	addContainer(t, tx, nodeID1, "c2")
	addContainer(t, tx, nodeID2, "c3")

	addContainerConfig(t, tx, "c2", "x", "y")
	addContainerConfig(t, tx, "c3", "z", "w")
	addContainerConfig(t, tx, "c3", "a", "b")

	addContainerDevice(t, tx, "c2", "eth0", "nic", nil)
	addContainerDevice(t, tx, "c3", "root", "disk", map[string]string{"x": "y"})

	filter := db.ContainerFilter{Type: int(db.CTypeRegular)}
	containers, err := tx.ContainerList(filter)
	require.NoError(t, err)
	assert.Len(t, containers, 3)

	c1 := containers[0]
	assert.Equal(t, "c1", c1.Name)
	assert.Equal(t, "node2", c1.Node)
	assert.Equal(t, map[string]string{}, c1.Config)
	assert.Len(t, c1.Devices, 0)

	c2 := containers[1]
	assert.Equal(t, "c2", c2.Name)
	assert.Equal(t, map[string]string{"x": "y"}, c2.Config)
	assert.Equal(t, "none", c2.Node)
	assert.Len(t, c2.Devices, 1)
	assert.Equal(t, map[string]string{"type": "nic"}, c2.Devices["eth0"])

	c3 := containers[2]
	assert.Equal(t, "c3", c3.Name)
	assert.Equal(t, map[string]string{"z": "w", "a": "b"}, c3.Config)
	assert.Equal(t, "node2", c3.Node)
	assert.Len(t, c3.Devices, 1)
	assert.Equal(t, map[string]string{"type": "disk", "x": "y"}, c3.Devices["root"])
}

func TestContainerList_FilterByNode(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	nodeID1 := int64(1) // This is the default local node

	nodeID2, err := tx.NodeAdd("node2", "1.2.3.4:666")
	require.NoError(t, err)

	addContainer(t, tx, nodeID2, "c1")
	addContainer(t, tx, nodeID1, "c2")
	addContainer(t, tx, nodeID2, "c3")

	filter := db.ContainerFilter{
		Project: "default",
		Node:    "node2",
		Type:    int(db.CTypeRegular),
	}

	containers, err := tx.ContainerList(filter)
	require.NoError(t, err)
	assert.Len(t, containers, 2)

	assert.Equal(t, 1, containers[0].ID)
	assert.Equal(t, "c1", containers[0].Name)
	assert.Equal(t, "node2", containers[0].Node)
	assert.Equal(t, 3, containers[1].ID)
	assert.Equal(t, "c3", containers[1].Name)
	assert.Equal(t, "node2", containers[1].Node)
}

func TestContainerList_ContainerWithSameNameInDifferentProjects(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	// Create a project with no features
	project1 := api.ProjectsPost{}
	project1.Name = "blah"
	_, err := tx.ProjectCreate(project1)
	require.NoError(t, err)

	// Create a project with the profiles feature and a custom profile.
	project2 := api.ProjectsPost{}
	project2.Name = "test"
	project2.Config = map[string]string{"features.profiles": "true"}
	_, err = tx.ProjectCreate(project2)
	require.NoError(t, err)

	profile := db.Profile{
		Project: "test",
		Name:    "intranet",
	}
	_, err = tx.ProfileCreate(profile)
	require.NoError(t, err)

	// Create a container in project1 using the default profile from the
	// default project.
	c1p1 := db.Container{
		Project:      "blah",
		Name:         "c1",
		Node:         "none",
		Type:         int(db.CTypeRegular),
		Architecture: 1,
		Ephemeral:    false,
		Stateful:     true,
		Profiles:     []string{"default"},
	}
	_, err = tx.ContainerCreate(c1p1)
	require.NoError(t, err)

	// Create a container in project2 using the custom profile from the
	// project.
	c1p2 := db.Container{
		Project:      "test",
		Name:         "c1",
		Node:         "none",
		Type:         int(db.CTypeRegular),
		Architecture: 1,
		Ephemeral:    false,
		Stateful:     true,
		Profiles:     []string{"intranet"},
	}
	_, err = tx.ContainerCreate(c1p2)
	require.NoError(t, err)

	containers, err := tx.ContainerList(db.ContainerFilter{})
	require.NoError(t, err)

	assert.Len(t, containers, 2)

	assert.Equal(t, "blah", containers[0].Project)
	assert.Equal(t, []string{"default"}, containers[0].Profiles)

	assert.Equal(t, "test", containers[1].Project)
	assert.Equal(t, []string{"intranet"}, containers[1].Profiles)
}

func TestContainerListExpanded(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	profile := db.Profile{
		Project: "default",
		Name:    "profile1",
		Config:  map[string]string{"a": "1"},
		Devices: map[string]map[string]string{"root": {"type": "disk", "b": "2"}},
	}

	_, err := tx.ProfileCreate(profile)
	require.NoError(t, err)

	container := db.Container{
		Project:      "default",
		Name:         "c1",
		Node:         "none",
		Type:         int(db.CTypeRegular),
		Architecture: 1,
		Ephemeral:    false,
		Stateful:     true,
		Config:       map[string]string{"c": "3"},
		Devices:      map[string]map[string]string{"eth0": {"type": "nic", "d": "4"}},
		Profiles:     []string{"default", "profile1"},
	}

	_, err = tx.ContainerCreate(container)
	require.NoError(t, err)

	containers, err := tx.ContainerListExpanded()
	require.NoError(t, err)

	assert.Len(t, containers, 1)

	assert.Equal(t, containers[0].Config, map[string]string{"a": "1", "c": "3"})
	assert.Equal(t, containers[0].Devices, map[string]map[string]string{
		"root": {"type": "disk", "b": "2"},
		"eth0": {"type": "nic", "d": "4"},
	})
}

func TestContainerCreate(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	object := db.Container{
		Project:      "default",
		Name:         "c1",
		Type:         0,
		Node:         "none",
		Architecture: 1,
		Ephemeral:    true,
		Stateful:     true,
		LastUseDate:  time.Now(),
		Description:  "container 1",
		Config:       map[string]string{"x": "y"},
		Devices:      map[string]map[string]string{"root": {"type": "disk", "x": "y"}},
		Profiles:     []string{"default"},
	}

	id, err := tx.ContainerCreate(object)
	require.NoError(t, err)

	assert.Equal(t, int64(1), id)

	c1, err := tx.ContainerGet("default", "c1")
	require.NoError(t, err)

	assert.Equal(t, "c1", c1.Name)
	assert.Equal(t, map[string]string{"x": "y"}, c1.Config)
	assert.Len(t, c1.Devices, 1)
	assert.Equal(t, map[string]string{"type": "disk", "x": "y"}, c1.Devices["root"])
	assert.Equal(t, []string{"default"}, c1.Profiles)
}

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

	result, err := tx.ContainersListByNodeAddress("default")
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

	result, err := tx.ContainersByNodeName("default")
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
	_, err = cluster.StoragePoolVolumeCreate("default", "c1", "", db.StoragePoolVolumeTypeContainer, false, poolID, nil)
	require.NoError(t, err)

	err = cluster.Transaction(func(tx *db.ClusterTx) error {
		container := db.Container{
			Project: "default",
			Name:    "c1",
			Node:    "none",
			Devices: types.Devices{
				"root": types.Device{
					"path": "/",
					"pool": "default",
					"type": "disk",
				},
			},
		}
		_, err := tx.ContainerCreate(container)
		return err
	})
	require.NoError(t, err)

	poolName, err := cluster.ContainerPool("default", "c1")
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

// All containers on a node are loaded in bulk.
func TestContainerNodeList(t *testing.T) {
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

	containers, err := tx.ContainerNodeList()
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
	assert.Len(t, containers[2].Config, 0)

	assert.Equal(t, map[string]map[string]string{"eth0": {"type": "nic"}}, containers[0].Devices)
	assert.Len(t, containers[1].Devices, 0)
	assert.Equal(t, map[string]map[string]string{"root": {"type": "disk", "x": "y"}}, containers[2].Devices)
}

func addContainer(t *testing.T, tx *db.ClusterTx, nodeID int64, name string) {
	stmt := `
INSERT INTO containers(node_id, name, architecture, type, project_id) VALUES (?, ?, 1, ?, 1)
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
