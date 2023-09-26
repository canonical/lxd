package main

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/project"
	storagePools "github.com/canonical/lxd/lxd/storage"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/idmap"
)

type containerTestSuite struct {
	lxdTestSuite
}

// TestContainer_ProfilesDefault verifies that a new container has the default profile assigned to it.
func (suite *containerTestSuite) TestContainer_ProfilesDefault() {
	args := db.InstanceArgs{
		Type:      instancetype.Container,
		Ephemeral: false,
		Name:      "testFoo",
	}

	c, op, _, err := instance.CreateInternal(suite.d.State(), args, true)
	suite.Req.Nil(err)
	op.Done(nil)
	defer func() { _ = c.Delete(true) }()

	profiles := c.Profiles()
	suite.Len(
		profiles,
		1,
		"No default profile created on instanceCreateInternal.")

	suite.Equal(
		"default",
		profiles[0].Name,
		"First profile should be the default profile.")
}

// TestContainer_ProfilesMulti verifies that a container correctly applies multiple profiles, including the unprivileged one.
func (suite *containerTestSuite) TestContainer_ProfilesMulti() {
	// Create an unprivileged profile
	err := suite.d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		profile := cluster.Profile{
			Name:        "unprivileged",
			Description: "unprivileged",
			Project:     "default",
		}

		id, err := cluster.CreateProfile(ctx, tx.Tx(), profile)
		if err != nil {
			return err
		}

		err = cluster.CreateProfileConfig(ctx, tx.Tx(), id, map[string]string{"security.privileged": "true"})
		if err != nil {
			return err
		}

		return err
	})

	suite.Req.Nil(err, "Failed to create the unprivileged profile.")
	defer func() {
		_ = suite.d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return cluster.DeleteProfile(ctx, tx.Tx(), "default", "unprivileged")
		})
	}()

	testProfiles, err := suite.d.db.Cluster.GetProfiles("default", []string{"default", "unprivileged"})
	suite.Req.Nil(err)

	args := db.InstanceArgs{
		Type:      instancetype.Container,
		Ephemeral: false,
		Profiles:  testProfiles,
		Name:      "testFoo",
	}

	c, op, _, err := instance.CreateInternal(suite.d.State(), args, true)
	suite.Req.Nil(err)
	op.Done(nil)
	defer func() { _ = c.Delete(true) }()

	profiles := c.Profiles()
	suite.Len(
		profiles,
		2,
		"Didn't get both profiles in instanceCreateInternal.")

	suite.True(
		c.IsPrivileged(),
		"The container is not privileged (didn't apply the unprivileged profile?).")
}

// TestContainer_ProfilesOverwriteDefaultNic checks if the container's
// "eth0" NIC device configuration overwrites the profile's configuration.
func (suite *containerTestSuite) TestContainer_ProfilesOverwriteDefaultNic() {
	args := db.InstanceArgs{
		Type:      instancetype.Container,
		Ephemeral: false,
		Config:    map[string]string{"security.privileged": "true"},
		Devices: deviceConfig.Devices{
			"eth0": deviceConfig.Device{
				"type":    "nic",
				"nictype": "bridged",
				"parent":  "unknownbr0"}},
		Name: "testFoo",
	}

	_, err := suite.d.State().DB.Cluster.CreateNetwork(project.Default, "unknownbr0", "", db.NetworkTypeBridge, nil)
	suite.Req.Nil(err)

	c, op, _, err := instance.CreateInternal(suite.d.State(), args, true)
	suite.Req.Nil(err)
	op.Done(nil)
	suite.True(c.IsPrivileged(), "This container should be privileged.")

	out, _, err := c.Render()
	suite.Req.Nil(err)

	state := out.(*api.Instance)
	defer func() { _ = c.Delete(true) }()

	suite.Equal(
		"unknownbr0",
		state.Devices["eth0"]["parent"],
		"Container config doesn't overwrite profile config.")
}

// TestContainer_LoadFromDB tests loading a container from the database and
// verifying its configuration matches the initial creation.
func (suite *containerTestSuite) TestContainer_LoadFromDB() {
	args := db.InstanceArgs{
		Type:      instancetype.Container,
		Ephemeral: false,
		Config:    map[string]string{"security.privileged": "true"},
		Devices: deviceConfig.Devices{
			"eth0": deviceConfig.Device{
				"type":    "nic",
				"nictype": "bridged",
				"parent":  "unknownbr0"}},
		Name: "testFoo",
	}

	state := suite.d.State()

	_, err := state.DB.Cluster.CreateNetwork(project.Default, "unknownbr0", "", db.NetworkTypeBridge, nil)
	suite.Req.Nil(err)

	// Create the container
	c, op, _, err := instance.CreateInternal(suite.d.State(), args, true)
	suite.Req.Nil(err)
	op.Done(nil)
	defer func() { _ = c.Delete(true) }()

	poolName, err := c.StoragePool()
	suite.Req.Nil(err)

	pool, err := storagePools.LoadByName(state, poolName)
	suite.Req.Nil(err)

	_, err = state.DB.Cluster.CreateStoragePoolVolume(c.Project().Name, c.Name(), "", db.StoragePoolVolumeContentTypeFS, pool.ID(), nil, db.StoragePoolVolumeContentTypeFS, time.Now())
	suite.Req.Nil(err)

	// Load the container and trigger initLXC()
	c2, err := instance.LoadByProjectAndName(state, "default", "testFoo")
	c2.IsRunning()
	suite.Req.Nil(err)

	hostInterfaces, _ := net.Interfaces()

	apiC1, etagC1, err := c.RenderFull(hostInterfaces)
	suite.Req.Nil(err)

	apiC2, etagC2, err := c2.RenderFull(hostInterfaces)
	suite.Req.Nil(err)

	suite.Equal(etagC1, etagC2)
	suite.Exactly(
		apiC1,
		apiC2,
		"The loaded container isn't excactly the same as the created one.",
	)
}

// TestContainer_Path_Regular tests the container path generation for a regular container.
func (suite *containerTestSuite) TestContainer_Path_Regular() {
	// Regular
	args := db.InstanceArgs{
		Type:      instancetype.Container,
		Ephemeral: false,
		Name:      "testFoo",
	}

	c, op, _, err := instance.CreateInternal(suite.d.State(), args, true)
	suite.Req.Nil(err)
	op.Done(nil)
	defer func() { _ = c.Delete(true) }()

	suite.Req.False(c.IsSnapshot(), "Shouldn't be a snapshot.")
	suite.Req.Equal(shared.VarPath("containers", "testFoo"), c.Path())
	suite.Req.Equal(shared.VarPath("containers", "testFoo2"), storagePools.InstancePath(instancetype.Container, "default", "testFoo2", false))
}

// TestContainer_LogPath tests the log path generation for a container.
func (suite *containerTestSuite) TestContainer_LogPath() {
	args := db.InstanceArgs{
		Type:      instancetype.Container,
		Ephemeral: false,
		Name:      "testFoo",
	}

	c, op, _, err := instance.CreateInternal(suite.d.State(), args, true)
	suite.Req.Nil(err)
	op.Done(nil)
	defer func() { _ = c.Delete(true) }()

	suite.Req.Equal(shared.VarPath("logs", "testFoo"), c.LogPath())
}

// TestContainer_IsPrivileged_Privileged tests whether a privileged container is correctly identified as privileged.
func (suite *containerTestSuite) TestContainer_IsPrivileged_Privileged() {
	args := db.InstanceArgs{
		Type:      instancetype.Container,
		Ephemeral: false,
		Config:    map[string]string{"security.privileged": "true"},
		Name:      "testFoo",
	}

	c, op, _, err := instance.CreateInternal(suite.d.State(), args, true)
	suite.Req.Nil(err)
	op.Done(nil)
	suite.Req.True(c.IsPrivileged(), "This container should be privileged.")
	suite.Req.Nil(c.Delete(true), "Failed to delete the container.")
}

// Tests the validation of adding routed NIC devices to a container with different configurations.
func (suite *containerTestSuite) TestContainer_AddRoutedNicValidation() {
	eth0 := deviceConfig.Device{"name": "eth0", "type": "nic", "ipv4.gateway": "none",
		"ipv6.gateway": "none", "nictype": "routed", "parent": "unknownbr0"}
	eth1 := deviceConfig.Device{"name": "eth1", "type": "nic", "ipv4.gateway": "none",
		"ipv6.gateway": "none", "nictype": "routed", "parent": "unknownbr0"}
	eth2 := deviceConfig.Device{"name": "eth2", "type": "nic", "nictype": "bridged", "parent": "unknownbr0"}

	testProfiles, err := suite.d.db.Cluster.GetProfiles("default", []string{"default"})
	suite.Req.Nil(err)

	args := db.InstanceArgs{
		Type:     instancetype.Container,
		Profiles: testProfiles,
		Devices: deviceConfig.Devices{
			"eth0": eth0,
		},
		Name: "testFoo",
	}

	c, op, _, err := instance.CreateInternal(suite.d.State(), args, true)
	suite.Req.NoError(err)
	op.Done(nil)
	err = c.Update(db.InstanceArgs{
		Type:     instancetype.Container,
		Profiles: testProfiles,
		Config:   c.LocalConfig(),
		Devices: deviceConfig.Devices{
			"eth0": eth0,
			"eth1": eth1,
		},
		Name: "testFoo",
	}, true)
	suite.Req.NoError(err, fmt.Errorf("Adding multiple routed with gateway mode ['none'] should succeed. "))

	eth0["ipv6.gateway"] = "auto"
	eth1["ipv6.gateway"] = ""
	err = c.Update(db.InstanceArgs{
		Type:     instancetype.Container,
		Profiles: testProfiles,
		Config:   c.LocalConfig(),
		Devices: deviceConfig.Devices{
			"eth0": eth0,
			"eth1": eth1,
		},
		Name: "testFoo",
	}, true)
	suite.Req.Error(err,
		fmt.Errorf("Adding multiple routed nic devices with any gateway mmode ['auto',''] should throw error. "))

	err = c.Update(db.InstanceArgs{
		Type:     instancetype.Container,
		Profiles: testProfiles,
		Config:   c.LocalConfig(),
		Devices: deviceConfig.Devices{
			"eth0": eth0,
			"eth2": eth2,
		},
		Name: "testFoo",
	}, true)
	suite.Req.NoError(err,
		fmt.Errorf("Adding multiple nic devices with unicque nictype ['routed'] should throw error. "))
}

// Tests the creation of an unprivileged container with "security.privileged" set to "false" in its configuration.
func (suite *containerTestSuite) TestContainer_IsPrivileged_Unprivileged() {
	args := db.InstanceArgs{
		Type:      instancetype.Container,
		Ephemeral: false,
		Config:    map[string]string{"security.privileged": "false"},
		Name:      "testFoo",
	}

	c, op, _, err := instance.CreateInternal(suite.d.State(), args, true)
	suite.Req.Nil(err)
	op.Done(nil)
	suite.Req.False(c.IsPrivileged(), "This container should be unprivileged.")
	suite.Req.Nil(c.Delete(true), "Failed to delete the container.")
}

// TestContainer_Rename tests the renaming of a container and
// verifies that the container's path is updated accordingly.
func (suite *containerTestSuite) TestContainer_Rename() {
	args := db.InstanceArgs{
		Type:      instancetype.Container,
		Ephemeral: false,
		Name:      "testFoo",
	}

	c, op, _, err := instance.CreateInternal(suite.d.State(), args, true)
	suite.Req.Nil(err)
	op.Done(nil)
	defer func() { _ = c.Delete(true) }()

	suite.Req.Nil(c.Rename("testFoo2", true), "Failed to rename the container.")
	suite.Req.Equal(shared.VarPath("containers", "testFoo2"), c.Path())
}

// TestContainer_findIdmap_isolated tests idmap isolation for two containers,
// verifying their idmap settings are correctly applied.
func (suite *containerTestSuite) TestContainer_findIdmap_isolated() {
	c1, op, _, err := instance.CreateInternal(suite.d.State(), db.InstanceArgs{
		Type: instancetype.Container,
		Name: "isol-1",
		Config: map[string]string{
			"security.idmap.isolated": "true",
		},
	}, true)
	suite.Req.Nil(err)
	op.Done(nil)
	defer func() { _ = c1.Delete(true) }()

	c2, op, _, err := instance.CreateInternal(suite.d.State(), db.InstanceArgs{
		Type: instancetype.Container,
		Name: "isol-2",
		Config: map[string]string{
			"security.idmap.isolated": "true",
		},
	}, true)
	suite.Req.Nil(err)
	op.Done(nil)
	defer func() { _ = c2.Delete(true) }()

	map1, err := c1.(instance.Container).NextIdmap()
	suite.Req.Nil(err)
	map2, err := c2.(instance.Container).NextIdmap()
	suite.Req.Nil(err)

	host := suite.d.os.IdmapSet.Idmap[0]

	for i := 0; i < 2; i++ {
		suite.Req.Equal(host.Hostid+65536, map1.Idmap[i].Hostid, "hostids don't match %d", i)
		suite.Req.Equal(int64(0), map1.Idmap[i].Nsid, "nsid nonzero")
		suite.Req.Equal(int64(65536), map1.Idmap[i].Maprange, "incorrect maprange")
	}

	for i := 0; i < 2; i++ {
		suite.Req.Equal(host.Hostid+65536*2, map2.Idmap[i].Hostid, "hostids don't match")
		suite.Req.Equal(int64(0), map2.Idmap[i].Nsid, "nsid nonzero")
		suite.Req.Equal(int64(65536), map2.Idmap[i].Maprange, "incorrect maprange")
	}
}

// This test checks idmap configurations in containers with different isolated settings.
func (suite *containerTestSuite) TestContainer_findIdmap_mixed() {
	c1, op, _, err := instance.CreateInternal(suite.d.State(), db.InstanceArgs{
		Type: instancetype.Container,
		Name: "isol-1",
		Config: map[string]string{
			"security.idmap.isolated": "false",
		},
	}, true)
	suite.Req.Nil(err)
	op.Done(nil)
	defer func() { _ = c1.Delete(true) }()

	c2, op, _, err := instance.CreateInternal(suite.d.State(), db.InstanceArgs{
		Type: instancetype.Container,
		Name: "isol-2",
		Config: map[string]string{
			"security.idmap.isolated": "true",
		},
	}, true)
	suite.Req.Nil(err)
	op.Done(nil)
	defer func() { _ = c2.Delete(true) }()

	map1, err := c1.(instance.Container).NextIdmap()
	suite.Req.Nil(err)
	map2, err := c2.(instance.Container).NextIdmap()
	suite.Req.Nil(err)

	host := suite.d.os.IdmapSet.Idmap[0]

	for i := 0; i < 2; i++ {
		suite.Req.Equal(host.Hostid, map1.Idmap[i].Hostid, "hostids don't match %d", i)
		suite.Req.Equal(int64(0), map1.Idmap[i].Nsid, "nsid nonzero")
		suite.Req.Equal(host.Maprange, map1.Idmap[i].Maprange, "incorrect maprange")
	}

	for i := 0; i < 2; i++ {
		suite.Req.Equal(host.Hostid+65536, map2.Idmap[i].Hostid, "hostids don't match")
		suite.Req.Equal(int64(0), map2.Idmap[i].Nsid, "nsid nonzero")
		suite.Req.Equal(int64(65536), map2.Idmap[i].Maprange, "incorrect maprange")
	}
}

// This test verifies the idmap configuration in a container with raw idmap settings.
func (suite *containerTestSuite) TestContainer_findIdmap_raw() {
	c1, op, _, err := instance.CreateInternal(suite.d.State(), db.InstanceArgs{
		Type: instancetype.Container,
		Name: "isol-1",
		Config: map[string]string{
			"security.idmap.isolated": "false",
			"raw.idmap":               "both 1000 1000",
		},
	}, true)
	suite.Req.Nil(err)
	op.Done(nil)
	defer func() { _ = c1.Delete(true) }()

	map1, err := c1.(instance.Container).NextIdmap()
	suite.Req.Nil(err)

	host := suite.d.os.IdmapSet.Idmap[0]

	for _, i := range []int{0, 3} {
		suite.Req.Equal(host.Hostid, map1.Idmap[i].Hostid, "hostids don't match")
		suite.Req.Equal(int64(0), map1.Idmap[i].Nsid, "nsid nonzero")
		suite.Req.Equal(int64(1000), map1.Idmap[i].Maprange, "incorrect maprange")
	}

	for _, i := range []int{1, 4} {
		suite.Req.Equal(int64(1000), map1.Idmap[i].Hostid, "hostids don't match")
		suite.Req.Equal(int64(1000), map1.Idmap[i].Nsid, "invalid nsid")
		suite.Req.Equal(int64(1), map1.Idmap[i].Maprange, "incorrect maprange")
	}

	for _, i := range []int{2, 5} {
		suite.Req.Equal(host.Hostid+1001, map1.Idmap[i].Hostid, "hostids don't match")
		suite.Req.Equal(int64(1001), map1.Idmap[i].Nsid, "invalid nsid")
		suite.Req.Equal(host.Maprange-1000-1, map1.Idmap[i].Maprange, "incorrect maprange")
	}
}

// TestContainer_findIdmap_maxed ensures that isolated idmap sets of multiple containers do not intersect up to a maximum limit.
func (suite *containerTestSuite) TestContainer_findIdmap_maxed() {
	maps := []*idmap.IdmapSet{}

	for i := 0; i < 7; i++ {
		c, op, _, err := instance.CreateInternal(suite.d.State(), db.InstanceArgs{
			Type: instancetype.Container,
			Name: fmt.Sprintf("isol-%d", i),
			Config: map[string]string{
				"security.idmap.isolated": "true",
			},
		}, true)

		/* we should fail if there are no ids left */
		if i != 6 {
			suite.Req.Nil(err)
		} else {
			suite.Req.NotNil(err)
			return
		}

		op.Done(nil)
		defer func() { _ = c.Delete(true) }()

		m, err := c.(instance.Container).NextIdmap()
		suite.Req.Nil(err)

		maps = append(maps, m)
	}

	for i, m1 := range maps {
		for j, m2 := range maps {
			if m1 == m2 {
				continue
			}

			for _, e := range m2.Idmap {
				suite.Req.False(m1.HostidsIntersect(e), "%d and %d's idmaps intersect %v %v", i, j, m1, m2)
			}
		}
	}
}

// TestContainerTestSuite runs the test suite for the container package.
func TestContainerTestSuite(t *testing.T) {
	suite.Run(t, new(containerTestSuite))
}
