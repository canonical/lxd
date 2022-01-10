//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/lxc/lxd/lxd/project"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/shared/api"
)

func TestContainerList(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	nodeID1 := int64(1) // This is the default local node

	nodeID2, err := tx.CreateNode("node2", "1.2.3.4:666")
	require.NoError(t, err)

	addContainer(t, tx, nodeID2, "c1")
	addContainer(t, tx, nodeID1, "c2")
	addContainer(t, tx, nodeID2, "c3")

	addContainerConfig(t, tx, "c2", "x", "y")
	addContainerConfig(t, tx, "c3", "z", "w")
	addContainerConfig(t, tx, "c3", "a", "b")

	addContainerDevice(t, tx, "c2", "eth0", "nic", nil)
	addContainerDevice(t, tx, "c3", "root", "disk", map[string]string{"x": "y"})

	filter := db.InstanceTypeFilter(instancetype.Container)
	containers, err := tx.GetInstances(filter)
	require.NoError(t, err)
	assert.Len(t, containers, 3)

	c1 := containers[0]
	config, err := tx.GetInstanceConfig(c1.ID)
	require.NoError(t, err)
	devices, err := tx.GetInstanceDevices(c1.ID)
	require.NoError(t, err)
	assert.Equal(t, "c1", c1.Name)
	assert.Equal(t, "node2", c1.Node)
	assert.Equal(t, map[string]string{}, config)
	assert.Len(t, devices, 0)

	c2 := containers[1]
	config, err = tx.GetInstanceConfig(c2.ID)
	require.NoError(t, err)
	devices, err = tx.GetInstanceDevices(c2.ID)
	require.NoError(t, err)
	assert.Equal(t, "c2", c2.Name)
	assert.Equal(t, map[string]string{"x": "y"}, config)
	assert.Equal(t, "none", c2.Node)
	assert.Len(t, devices, 1)
	assert.Equal(t, "eth0", devices["eth0"].Name)
	assert.Equal(t, "nic", devices["eth0"].Type.String())

	c3 := containers[2]
	config, err = tx.GetInstanceConfig(c3.ID)
	require.NoError(t, err)
	devices, err = tx.GetInstanceDevices(c3.ID)
	require.NoError(t, err)
	assert.Equal(t, "c3", c3.Name)
	assert.Equal(t, map[string]string{"z": "w", "a": "b"}, config)
	assert.Equal(t, "node2", c3.Node)
	assert.Len(t, devices, 1)
	assert.Equal(t, "root", devices["root"].Name)
	assert.Equal(t, "disk", devices["root"].Type.String())
	assert.Equal(t, map[string]string{"x": "y"}, devices["root"].Config)
}

func TestContainerList_FilterByNode(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	nodeID1 := int64(1) // This is the default local node

	nodeID2, err := tx.CreateNode("node2", "1.2.3.4:666")
	require.NoError(t, err)

	addContainer(t, tx, nodeID2, "c1")
	addContainer(t, tx, nodeID1, "c2")
	addContainer(t, tx, nodeID2, "c3")

	filter := db.InstanceTypeFilter(instancetype.Container)
	project := "default"
	node := "node2"
	filter.Project = &project
	filter.Node = &node

	containers, err := tx.GetInstances(filter)
	require.NoError(t, err)
	assert.Len(t, containers, 2)

	assert.Equal(t, 1, containers[0].ID)
	assert.Equal(t, "c1", containers[0].Name)
	assert.Equal(t, "node2", containers[0].Node)
	assert.Equal(t, 3, containers[1].ID)
	assert.Equal(t, "c3", containers[1].Name)
	assert.Equal(t, "node2", containers[1].Node)
}

func TestInstanceList_ContainerWithSameNameInDifferentProjects(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	// Create a project with no features
	project1 := db.Project{}
	project1.Name = "blah"
	_, err := tx.CreateProject(project1)
	require.NoError(t, err)

	// Create a project with the profiles feature and a custom profile.
	project2 := db.Project{}
	project2.Name = "test"
	project2Config := map[string]string{"features.profiles": "true"}
	id, err := tx.CreateProject(project2)
	require.NoError(t, err)

	err = tx.CreateProjectConfig(id, project2Config)
	require.NoError(t, err)

	profile := db.Profile{
		Project: "test",
		Name:    "intranet",
	}
	_, err = tx.CreateProfile(profile)
	require.NoError(t, err)

	// Create a container in project1 using the default profile from the
	// default project.
	c1p1 := db.Instance{
		Project:      "blah",
		Name:         "c1",
		Node:         "none",
		Type:         instancetype.Container,
		Architecture: 1,
		Ephemeral:    false,
		Stateful:     true,
	}

	id, err = tx.CreateInstance(c1p1)
	require.NoError(t, err)

	c1p1.ID = int(id)
	err = tx.UpdateInstanceProfiles(c1p1, []string{"default"})
	require.NoError(t, err)

	// Create a container in project2 using the custom profile from the
	// project.
	c1p2 := db.Instance{
		Project:      "test",
		Name:         "c1",
		Node:         "none",
		Type:         instancetype.Container,
		Architecture: 1,
		Ephemeral:    false,
		Stateful:     true,
	}
	id, err = tx.CreateInstance(c1p2)
	require.NoError(t, err)

	c1p2.ID = int(id)
	err = tx.UpdateInstanceProfiles(c1p2, []string{"intranet"})
	require.NoError(t, err)

	containers, err := tx.GetInstances(db.InstanceFilter{})
	require.NoError(t, err)
	assert.Len(t, containers, 2)

	profiles, err := tx.GetInstanceProfiles(containers[0])
	require.NoError(t, err)
	assert.Equal(t, 1, len(profiles))
	assert.Equal(t, "blah", containers[0].Project)
	assert.Equal(t, "default", profiles[0].Name)

	profiles, err = tx.GetInstanceProfiles(containers[1])
	require.NoError(t, err)
	assert.Equal(t, 1, len(profiles))
	assert.Equal(t, "test", containers[1].Project)
	assert.Equal(t, "intranet", profiles[0].Name)

}

func TestInstanceList(t *testing.T) {
	cluster, clusterCleanup := db.NewTestCluster(t)
	defer clusterCleanup()

	err := cluster.Transaction(func(tx *db.ClusterTx) error {
		profile := db.Profile{
			Project: "default",
			Name:    "profile1",
		}

		profileConfig := map[string]string{"a": "1"}
		profileDevices := map[string]db.Device{
			"root": {
				Name:   "root",
				Type:   db.TypeDisk,
				Config: map[string]string{"b": "2"},
			},
		}

		id, err := tx.CreateProfile(profile)
		if err != nil {
			return err
		}

		err = tx.CreateProfileConfig(id, profileConfig)
		require.NoError(t, err)

		err = tx.CreateProfileDevice(id, profileDevices["root"])
		require.NoError(t, err)

		container := db.Instance{
			Project:      "default",
			Name:         "c1",
			Node:         "none",
			Type:         instancetype.Container,
			Architecture: 1,
			Ephemeral:    false,
			Stateful:     true,
		}

		instanceConfig := map[string]string{"c": "3"}
		instanceDevices := map[string]db.Device{
			"eth0": {
				Name:   "eth0",
				Type:   db.TypeNIC,
				Config: map[string]string{"d": "4"},
			},
		}

		id, err = tx.CreateInstance(container)
		if err != nil {
			return err
		}

		err = tx.CreateInstanceConfig(id, instanceConfig)
		require.NoError(t, err)

		err = tx.CreateInstanceDevice(id, instanceDevices["eth0"])
		require.NoError(t, err)

		container.ID = int(id)
		err = tx.UpdateInstanceProfiles(container, []string{"default", "profile1"})
		require.NoError(t, err)

		return nil
	})
	require.NoError(t, err)

	var instances []db.InstanceArgs
	err = cluster.InstanceList(nil, func(instanceInfo db.InstanceArgs, apiProject api.Project, profiles []api.Profile) error {
		instanceInfo.Config = db.ExpandInstanceConfig(instanceInfo.Config, profiles)
		instanceInfo.Devices = deviceConfig.NewDevices(db.ExpandInstanceDevices(instanceInfo.Devices.CloneNative(), profiles))
		instances = append(instances, instanceInfo)
		return nil
	})
	require.NoError(t, err)

	assert.Len(t, instances, 1)

	assert.Equal(t, instances[0].Config, map[string]string{"a": "1", "c": "3"})
	assert.Equal(t, instances[0].Devices.CloneNative(), map[string]map[string]string{
		"root": {"type": "disk", "b": "2"},
		"eth0": {"type": "nic", "d": "4"},
	})
}

func TestCreateInstance(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	object := db.Instance{
		Project:      "default",
		Name:         "c1",
		Type:         0,
		Node:         "none",
		Architecture: 1,
		Ephemeral:    true,
		Stateful:     true,
		LastUseDate:  sql.NullTime{Time: time.Now(), Valid: true},
		Description:  "container 1",
	}

	instanceConfig := map[string]string{"x": "y"}
	instanceDevices := map[string]db.Device{
		"root": {
			Name:   "root",
			Config: map[string]string{"type": "disk", "x": "y"},
		},
	}

	id, err := tx.CreateInstance(object)
	require.NoError(t, err)
	assert.Equal(t, int64(1), id)

	c1, err := tx.GetInstance("default", "c1")
	require.NoError(t, err)

	err = tx.CreateInstanceConfig(id, instanceConfig)
	require.NoError(t, err)

	err = tx.CreateInstanceDevice(id, instanceDevices["root"])
	require.NoError(t, err)

	err = tx.UpdateInstanceProfiles(*c1, []string{"default"})
	require.NoError(t, err)

	config, err := tx.GetInstanceConfig(c1.ID)
	require.NoError(t, err)

	devices, err := tx.GetInstanceDevices(c1.ID)
	require.NoError(t, err)

	profiles, err := tx.GetInstanceProfiles(*c1)
	assert.Equal(t, 1, len(profiles))
	require.NoError(t, err)

	assert.Equal(t, "c1", c1.Name)
	assert.Equal(t, map[string]string{"x": "y"}, config)
	assert.Len(t, devices, 1)
	assert.Equal(t, "root", devices["root"].Name)
	assert.Equal(t, map[string]string{"type": "disk", "x": "y"}, devices["root"].Config)
	assert.Equal(t, "default", profiles[0].Name)
}

func TestCreateInstance_Snapshot(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	instance := db.Instance{
		Project:      "default",
		Name:         "foo",
		Type:         0,
		Node:         "none",
		Architecture: 2,
		Ephemeral:    false,
		Stateful:     false,
		LastUseDate:  sql.NullTime{Time: time.Now(), Valid: true},
		Description:  "container 1",
	}

	instanceConfig := map[string]string{
		"image.architecture":  "x86_64",
		"image.description":   "BusyBox x86_64",
		"image.name":          "busybox-x86_64",
		"image.os":            "BusyBox",
		"volatile.base_image": "1f7f054e6ccb",
	}

	id, err := tx.CreateInstance(instance)
	require.NoError(t, err)
	assert.Equal(t, int64(1), id)

	c1, err := tx.GetInstance("default", "foo")
	require.NoError(t, err)

	err = tx.CreateInstanceConfig(id, instanceConfig)
	require.NoError(t, err)

	err = tx.CreateInstanceDevice(id, db.Device{})
	require.NoError(t, err)

	err = tx.UpdateInstanceProfiles(*c1, []string{"default"})
	require.NoError(t, err)

	profiles, err := tx.GetInstanceProfiles(*c1)
	assert.Equal(t, 1, len(profiles))
	require.NoError(t, err)

	snapshot := db.Instance{
		Project:      "default",
		Name:         "foo/snap0",
		Type:         1,
		Node:         "none",
		Architecture: 2,
		Ephemeral:    false,
		Stateful:     false,
		LastUseDate:  sql.NullTime{Time: time.Now(), Valid: true},
		Description:  "container 1",
	}

	snapshotConfig := map[string]string{
		"image.architecture":      "x86_64",
		"image.description":       "BusyBox x86_64",
		"image.name":              "busybox-x86_64",
		"image.os":                "BusyBox",
		"volatile.apply_template": "create",
		"volatile.base_image":     "1f7f054e6ccb",
		"volatile.eth0.hwaddr":    "00:16:3e:2a:3f:e2",
		"volatile.idmap.base":     "0",
	}

	id, err = tx.CreateInstance(snapshot)
	require.NoError(t, err)

	assert.Equal(t, int64(2), id)

	err = tx.CreateInstanceConfig(id, snapshotConfig)
	require.NoError(t, err)

	snap, err := tx.GetInstance("default", "foo/snap0")
	require.NoError(t, err)

	err = tx.UpdateInstanceProfiles(*snap, []string{"default"})
	require.NoError(t, err)
}

// Containers are grouped by node address.
func TestGetInstanceNamesByNodeAddress(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	nodeID1 := int64(1) // This is the default local node

	nodeID2, err := tx.CreateNode("node2", "1.2.3.4:666")
	require.NoError(t, err)

	nodeID3, err := tx.CreateNode("node3", "5.6.7.8:666")
	require.NoError(t, err)
	require.NoError(t, tx.SetNodeHeartbeat("5.6.7.8:666", time.Now().Add(-time.Minute)))

	addContainer(t, tx, nodeID2, "c1")
	addContainer(t, tx, nodeID1, "c2")
	addContainer(t, tx, nodeID3, "c3")
	addContainer(t, tx, nodeID2, "c4")

	result, err := tx.GetProjectAndInstanceNamesByNodeAddress([]string{"default"}, db.InstanceTypeFilter(instancetype.Container))
	require.NoError(t, err)
	assert.Equal(
		t,
		map[string][][2]string{
			"":            {{project.Default, "c2"}},
			"1.2.3.4:666": {{project.Default, "c1"}, {project.Default, "c4"}},
			"0.0.0.0":     {{project.Default, "c3"}},
		}, result)
}

// Instances are associated with their node name.
func TestGetInstanceToNodeMap(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	nodeID1 := int64(1) // This is the default local node

	nodeID2, err := tx.CreateNode("node2", "1.2.3.4:666")
	require.NoError(t, err)

	addContainer(t, tx, nodeID2, "c1")
	addContainer(t, tx, nodeID1, "c2")

	result, err := tx.GetProjectInstanceToNodeMap([]string{"default"}, db.InstanceTypeFilter(instancetype.Container))
	require.NoError(t, err)
	assert.Equal(
		t,
		map[[2]string]string{
			{project.Default, "c1"}: "node2",
			{project.Default, "c2"}: "none",
		}, result)
}

func TestGetInstancePool(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()

	poolID, err := cluster.CreateStoragePool("default", "", "dir", nil)
	require.NoError(t, err)
	_, err = cluster.CreateStoragePoolVolume("default", "c1", "", db.StoragePoolVolumeTypeContainer, poolID, nil, db.StoragePoolVolumeContentTypeFS)
	require.NoError(t, err)

	err = cluster.Transaction(func(tx *db.ClusterTx) error {
		container := db.Instance{
			Project: "default",
			Name:    "c1",
			Node:    "none",
		}
		id, err := tx.CreateInstance(container)
		require.NoError(t, err)

		instanceDevices := map[string]db.Device{
			"root": {
				Name: "root",
				Config: map[string]string{
					"path": "/",
					"pool": "default",
					"type": "disk",
				},
			},
		}

		err = tx.CreateInstanceDevice(id, instanceDevices["root"])
		return err
	})
	require.NoError(t, err)

	poolName, err := cluster.GetInstancePool("default", "c1")
	require.NoError(t, err)
	assert.Equal(t, "default", poolName)
}

// All containers on a node are loaded in bulk.
func TestGetLocalInstancesInProject(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	nodeID1 := int64(1) // This is the default local node

	nodeID2, err := tx.CreateNode("node2", "1.2.3.4:666")
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

	containers, err := tx.GetLocalInstancesInProject(db.InstanceTypeFilter(instancetype.Container))
	require.NoError(t, err)
	assert.Len(t, containers, 3)

	assert.Equal(t, "c2", containers[0].Name)
	assert.Equal(t, "c3", containers[1].Name)
	assert.Equal(t, "c4", containers[2].Name)

	assert.Equal(t, "none", containers[0].Node)
	assert.Equal(t, "none", containers[1].Node)
	assert.Equal(t, "none", containers[2].Node)

	config, err := tx.GetInstanceConfig(containers[0].ID)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"x": "y"}, config)
	devices, err := tx.GetInstanceDevices(containers[0].ID)
	require.NoError(t, err)
	assert.Equal(t, map[string]map[string]string{"eth0": {"type": "nic"}}, db.DevicesToAPI(devices))
	config, err = tx.GetInstanceConfig(containers[1].ID)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"z": "w", "a": "b"}, config)

	devices, err = tx.GetInstanceDevices(containers[1].ID)
	require.NoError(t, err)
	assert.Len(t, devices, 0)

	config, err = tx.GetInstanceConfig(containers[2].ID)
	require.NoError(t, err)
	assert.Len(t, config, 0)
	devices, err = tx.GetInstanceDevices(containers[2].ID)
	require.NoError(t, err)
	assert.Equal(t, map[string]map[string]string{"root": {"type": "disk", "x": "y"}}, db.DevicesToAPI(devices))
}

func addContainer(t *testing.T, tx *db.ClusterTx, nodeID int64, name string) {
	stmt := `
INSERT INTO instances(node_id, name, architecture, type, project_id, description) VALUES (?, ?, 1, ?, 1, '')
`
	_, err := tx.Tx().Exec(stmt, nodeID, name, instancetype.Container)
	require.NoError(t, err)
}

func addContainerConfig(t *testing.T, tx *db.ClusterTx, container, key, value string) {
	id := getContainerID(t, tx, container)

	stmt := `
INSERT INTO instances_config(instance_id, key, value) VALUES (?, ?, ?)
`
	_, err := tx.Tx().Exec(stmt, id, key, value)
	require.NoError(t, err)
}

func addContainerDevice(t *testing.T, tx *db.ClusterTx, container, name, typ string, config map[string]string) {
	id := getContainerID(t, tx, container)

	code, err := db.NewDeviceType(typ)
	require.NoError(t, err)

	stmt := `
INSERT INTO instances_devices(instance_id, name, type) VALUES (?, ?, ?)
`
	_, err = tx.Tx().Exec(stmt, id, name, code)
	require.NoError(t, err)

	deviceID := getDeviceID(t, tx, id, name)

	for key, value := range config {
		stmt := `
INSERT INTO instances_devices_config(instance_device_id, key, value) VALUES (?, ?, ?)
`
		_, err = tx.Tx().Exec(stmt, deviceID, key, value)
		require.NoError(t, err)
	}
}

// Return the container ID given its name.
func getContainerID(t *testing.T, tx *db.ClusterTx, name string) int64 {
	var id int64

	stmt := "SELECT id FROM instances WHERE name=?"
	row := tx.Tx().QueryRow(stmt, name)
	err := row.Scan(&id)
	require.NoError(t, err)

	return id
}

// Return the device ID given its container ID and name.
func getDeviceID(t *testing.T, tx *db.ClusterTx, containerID int64, name string) int64 {
	var id int64

	stmt := "SELECT id FROM instances_devices WHERE instance_id=? AND name=?"
	row := tx.Tx().QueryRow(stmt, containerID, name)
	err := row.Scan(&id)
	require.NoError(t, err)

	return id
}
