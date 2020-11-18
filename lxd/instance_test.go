package main

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/lxc/lxd/lxd/db"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	instanceDrivers "github.com/lxc/lxd/lxd/instance/drivers"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/project"
	driver "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/idmap"
)

type containerTestSuite struct {
	lxdTestSuite
}

func (suite *containerTestSuite) TestContainer_ProfilesDefault() {
	args := db.InstanceArgs{
		Type:      instancetype.Container,
		Ephemeral: false,
		Name:      "testFoo",
	}

	c, err := instanceCreateInternal(suite.d.State(), args)
	suite.Req.Nil(err)
	defer c.Delete()

	profiles := c.Profiles()
	suite.Len(
		profiles,
		1,
		"No default profile created on instanceCreateInternal.")

	suite.Equal(
		"default",
		profiles[0],
		"First profile should be the default profile.")
}

func (suite *containerTestSuite) TestContainer_ProfilesMulti() {
	// Create an unprivileged profile
	err := suite.d.cluster.Transaction(func(tx *db.ClusterTx) error {
		profile := db.Profile{
			Name:        "unprivileged",
			Description: "unprivileged",
			Config:      map[string]string{"security.privileged": "true"},
			Project:     "default",
		}
		_, err := tx.CreateProfile(profile)
		return err
	})

	suite.Req.Nil(err, "Failed to create the unprivileged profile.")
	defer func() {
		suite.d.cluster.Transaction(func(tx *db.ClusterTx) error {
			return tx.DeleteProfile("default", "unpriviliged")
		})
	}()

	args := db.InstanceArgs{
		Type:      instancetype.Container,
		Ephemeral: false,
		Profiles:  []string{"default", "unprivileged"},
		Name:      "testFoo",
	}

	c, err := instanceCreateInternal(suite.d.State(), args)
	suite.Req.Nil(err)
	defer c.Delete()

	profiles := c.Profiles()
	suite.Len(
		profiles,
		2,
		"Didn't get both profiles in instanceCreateInternal.")

	suite.True(
		c.IsPrivileged(),
		"The container is not privileged (didn't apply the unprivileged profile?).")
}

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

	_, err := suite.d.State().Cluster.CreateNetwork(project.Default, "unknownbr0", "", db.NetworkTypeBridge, nil)
	suite.Req.Nil(err)

	c, err := instanceCreateInternal(suite.d.State(), args)
	suite.Req.Nil(err)

	suite.True(c.IsPrivileged(), "This container should be privileged.")

	out, _, err := c.Render()
	suite.Req.Nil(err)

	state := out.(*api.Instance)
	defer c.Delete()

	suite.Equal(
		"unknownbr0",
		state.Devices["eth0"]["parent"],
		"Container config doesn't overwrite profile config.")
}

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

	_, err := state.Cluster.CreateNetwork(project.Default, "unknownbr0", "", db.NetworkTypeBridge, nil)
	suite.Req.Nil(err)

	// Create the container
	c, err := instanceCreateInternal(state, args)
	suite.Req.Nil(err)
	defer c.Delete()

	// Load the container and trigger initLXC()
	c2, err := instance.LoadByProjectAndName(state, "default", "testFoo")
	c2.IsRunning()
	suite.Req.Nil(err)

	// This causes the mock storage pool to be loaded internally, allowing it to match the created container.
	err = c2.UpdateBackupFile()
	suite.Req.Nil(err)

	instanceDrivers.PrepareEqualTest(c, c2)
	suite.Exactly(
		c,
		c2,
		"The loaded container isn't excactly the same as the created one.")
}

func (suite *containerTestSuite) TestContainer_Path_Regular() {
	// Regular
	args := db.InstanceArgs{
		Type:      instancetype.Container,
		Ephemeral: false,
		Name:      "testFoo",
	}

	c, err := instanceCreateInternal(suite.d.State(), args)
	suite.Req.Nil(err)
	defer c.Delete()

	suite.Req.False(c.IsSnapshot(), "Shouldn't be a snapshot.")
	suite.Req.Equal(shared.VarPath("containers", "testFoo"), c.Path())
	suite.Req.Equal(shared.VarPath("containers", "testFoo2"), driver.InstancePath(instancetype.Container, "default", "testFoo2", false))
}

func (suite *containerTestSuite) TestContainer_LogPath() {
	args := db.InstanceArgs{
		Type:      instancetype.Container,
		Ephemeral: false,
		Name:      "testFoo",
	}

	c, err := instanceCreateInternal(suite.d.State(), args)
	suite.Req.Nil(err)
	defer c.Delete()

	suite.Req.Equal(shared.VarPath("logs", "testFoo"), c.LogPath())
}

func (suite *containerTestSuite) TestContainer_IsPrivileged_Privileged() {
	args := db.InstanceArgs{
		Type:      instancetype.Container,
		Ephemeral: false,
		Config:    map[string]string{"security.privileged": "true"},
		Name:      "testFoo",
	}

	c, err := instanceCreateInternal(suite.d.State(), args)
	suite.Req.Nil(err)

	suite.Req.True(c.IsPrivileged(), "This container should be privileged.")
	suite.Req.Nil(c.Delete(), "Failed to delete the container.")
}

func (suite *containerTestSuite) TestContainer_IsPrivileged_Unprivileged() {
	args := db.InstanceArgs{
		Type:      instancetype.Container,
		Ephemeral: false,
		Config:    map[string]string{"security.privileged": "false"},
		Name:      "testFoo",
	}

	c, err := instanceCreateInternal(suite.d.State(), args)
	suite.Req.Nil(err)

	suite.Req.False(c.IsPrivileged(), "This container should be unprivileged.")
	suite.Req.Nil(c.Delete(), "Failed to delete the container.")
}

func (suite *containerTestSuite) TestContainer_Rename() {
	args := db.InstanceArgs{
		Type:      instancetype.Container,
		Ephemeral: false,
		Name:      "testFoo",
	}

	c, err := instanceCreateInternal(suite.d.State(), args)
	suite.Req.Nil(err)
	defer c.Delete()

	suite.Req.Nil(c.Rename("testFoo2"), "Failed to rename the container.")
	suite.Req.Equal(shared.VarPath("containers", "testFoo2"), c.Path())
}

func (suite *containerTestSuite) TestContainer_findIdmap_isolated() {
	c1, err := instanceCreateInternal(suite.d.State(), db.InstanceArgs{
		Type: instancetype.Container,
		Name: "isol-1",
		Config: map[string]string{
			"security.idmap.isolated": "true",
		},
	})
	suite.Req.Nil(err)
	defer c1.Delete()

	c2, err := instanceCreateInternal(suite.d.State(), db.InstanceArgs{
		Type: instancetype.Container,
		Name: "isol-2",
		Config: map[string]string{
			"security.idmap.isolated": "true",
		},
	})
	suite.Req.Nil(err)
	defer c2.Delete()

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

func (suite *containerTestSuite) TestContainer_findIdmap_mixed() {
	c1, err := instanceCreateInternal(suite.d.State(), db.InstanceArgs{
		Type: instancetype.Container,
		Name: "isol-1",
		Config: map[string]string{
			"security.idmap.isolated": "false",
		},
	})
	suite.Req.Nil(err)
	defer c1.Delete()

	c2, err := instanceCreateInternal(suite.d.State(), db.InstanceArgs{
		Type: instancetype.Container,
		Name: "isol-2",
		Config: map[string]string{
			"security.idmap.isolated": "true",
		},
	})
	suite.Req.Nil(err)
	defer c2.Delete()

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

func (suite *containerTestSuite) TestContainer_findIdmap_raw() {
	c1, err := instanceCreateInternal(suite.d.State(), db.InstanceArgs{
		Type: instancetype.Container,
		Name: "isol-1",
		Config: map[string]string{
			"security.idmap.isolated": "false",
			"raw.idmap":               "both 1000 1000",
		},
	})
	suite.Req.Nil(err)
	defer c1.Delete()

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

func (suite *containerTestSuite) TestContainer_findIdmap_maxed() {
	maps := []*idmap.IdmapSet{}

	for i := 0; i < 7; i++ {
		c, err := instanceCreateInternal(suite.d.State(), db.InstanceArgs{
			Type: instancetype.Container,
			Name: fmt.Sprintf("isol-%d", i),
			Config: map[string]string{
				"security.idmap.isolated": "true",
			},
		})

		/* we should fail if there are no ids left */
		if i != 6 {
			suite.Req.Nil(err)
		} else {
			suite.Req.NotNil(err)
			return
		}

		defer c.Delete()

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

func TestContainerTestSuite(t *testing.T) {
	suite.Run(t, new(containerTestSuite))
}
