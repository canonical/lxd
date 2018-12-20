package main

import (
	"fmt"
	"testing"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/types"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/idmap"
	"github.com/stretchr/testify/suite"
)

type containerTestSuite struct {
	lxdTestSuite
}

func (suite *containerTestSuite) TestContainer_ProfilesDefault() {
	args := db.ContainerArgs{
		Ctype:     db.CTypeRegular,
		Ephemeral: false,
		Name:      "testFoo",
	}

	c, err := containerCreateInternal(suite.d.State(), args)
	suite.Req.Nil(err)
	defer c.Delete()

	profiles := c.Profiles()
	suite.Len(
		profiles,
		1,
		"No default profile created on containerCreateInternal.")

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
			Devices:     types.Devices{},
			Project:     "default",
		}
		_, err := tx.ProfileCreate(profile)
		return err
	})

	suite.Req.Nil(err, "Failed to create the unprivileged profile.")
	defer func() {
		suite.d.cluster.Transaction(func(tx *db.ClusterTx) error {
			return tx.ProfileDelete("default", "unpriviliged")
		})
	}()

	args := db.ContainerArgs{
		Ctype:     db.CTypeRegular,
		Ephemeral: false,
		Profiles:  []string{"default", "unprivileged"},
		Name:      "testFoo",
	}

	c, err := containerCreateInternal(suite.d.State(), args)
	suite.Req.Nil(err)
	defer c.Delete()

	profiles := c.Profiles()
	suite.Len(
		profiles,
		2,
		"Didn't get both profiles in containerCreateInternal.")

	suite.True(
		c.IsPrivileged(),
		"The container is not privileged (didn't apply the unprivileged profile?).")
}

func (suite *containerTestSuite) TestContainer_ProfilesOverwriteDefaultNic() {
	args := db.ContainerArgs{
		Ctype:     db.CTypeRegular,
		Ephemeral: false,
		Config:    map[string]string{"security.privileged": "true"},
		Devices: types.Devices{
			"eth0": types.Device{
				"type":    "nic",
				"nictype": "bridged",
				"parent":  "unknownbr0"}},
		Name: "testFoo",
	}

	c, err := containerCreateInternal(suite.d.State(), args)
	suite.Req.Nil(err)

	suite.True(c.IsPrivileged(), "This container should be privileged.")

	out, _, err := c.Render()
	suite.Req.Nil(err)

	state := out.(*api.Container)
	defer c.Delete()

	suite.Equal(
		"unknownbr0",
		state.Devices["eth0"]["parent"],
		"Container config doesn't overwrite profile config.")
}

func (suite *containerTestSuite) TestContainer_LoadFromDB() {
	args := db.ContainerArgs{
		Ctype:     db.CTypeRegular,
		Ephemeral: false,
		Config:    map[string]string{"security.privileged": "true"},
		Devices: types.Devices{
			"eth0": types.Device{
				"type":    "nic",
				"nictype": "bridged",
				"parent":  "unknownbr0"}},
		Name: "testFoo",
	}

	// Create the container
	c, err := containerCreateInternal(suite.d.State(), args)
	suite.Req.Nil(err)
	defer c.Delete()

	// Load the container and trigger initLXC()
	c2, err := containerLoadByProjectAndName(suite.d.State(), "default", "testFoo")
	c2.IsRunning()
	suite.Req.Nil(err)
	_, err = c2.StorageStart()
	suite.Req.Nil(err)

	// When loading from DB, we won't have a full LXC config
	c.(*containerLXC).cConfig = false

	suite.Exactly(
		c,
		c2,
		"The loaded container isn't excactly the same as the created one.")
}

func (suite *containerTestSuite) TestContainer_Path_Regular() {
	// Regular
	args := db.ContainerArgs{
		Ctype:     db.CTypeRegular,
		Ephemeral: false,
		Name:      "testFoo",
	}

	c, err := containerCreateInternal(suite.d.State(), args)
	suite.Req.Nil(err)
	defer c.Delete()

	suite.Req.False(c.IsSnapshot(), "Shouldn't be a snapshot.")
	suite.Req.Equal(shared.VarPath("containers", "testFoo"), c.Path())
	suite.Req.Equal(shared.VarPath("containers", "testFoo2"), containerPath("testFoo2", false))
}

func (suite *containerTestSuite) TestContainer_Path_Snapshot() {
	// Snapshot
	args := db.ContainerArgs{
		Ctype:     db.CTypeSnapshot,
		Ephemeral: false,
		Name:      "test/snap0",
	}

	c, err := containerCreateInternal(suite.d.State(), args)
	suite.Req.Nil(err)
	defer c.Delete()

	suite.Req.True(c.IsSnapshot(), "Should be a snapshot.")
	suite.Req.Equal(
		shared.VarPath("snapshots", "test", "snap0"),
		c.Path())
	suite.Req.Equal(
		shared.VarPath("snapshots", "test", "snap1"),
		containerPath("test/snap1", true))
}

func (suite *containerTestSuite) TestContainer_LogPath() {
	args := db.ContainerArgs{
		Ctype:     db.CTypeRegular,
		Ephemeral: false,
		Name:      "testFoo",
	}

	c, err := containerCreateInternal(suite.d.State(), args)
	suite.Req.Nil(err)
	defer c.Delete()

	suite.Req.Equal(shared.VarPath("logs", "testFoo"), c.LogPath())
}

func (suite *containerTestSuite) TestContainer_IsPrivileged_Privileged() {
	args := db.ContainerArgs{
		Ctype:     db.CTypeRegular,
		Ephemeral: false,
		Config:    map[string]string{"security.privileged": "true"},
		Name:      "testFoo",
	}

	c, err := containerCreateInternal(suite.d.State(), args)
	suite.Req.Nil(err)

	suite.Req.True(c.IsPrivileged(), "This container should be privileged.")
	suite.Req.Nil(c.Delete(), "Failed to delete the container.")
}

func (suite *containerTestSuite) TestContainer_IsPrivileged_Unprivileged() {
	args := db.ContainerArgs{
		Ctype:     db.CTypeRegular,
		Ephemeral: false,
		Config:    map[string]string{"security.privileged": "false"},
		Name:      "testFoo",
	}

	c, err := containerCreateInternal(suite.d.State(), args)
	suite.Req.Nil(err)

	suite.Req.False(c.IsPrivileged(), "This container should be unprivileged.")
	suite.Req.Nil(c.Delete(), "Failed to delete the container.")
}

func (suite *containerTestSuite) TestContainer_Rename() {
	args := db.ContainerArgs{
		Ctype:     db.CTypeRegular,
		Ephemeral: false,
		Name:      "testFoo",
	}

	c, err := containerCreateInternal(suite.d.State(), args)
	suite.Req.Nil(err)
	defer c.Delete()

	suite.Req.Nil(c.Rename("testFoo2"), "Failed to rename the container.")
	suite.Req.Equal(shared.VarPath("containers", "testFoo2"), c.Path())
}

func (suite *containerTestSuite) TestContainer_findIdmap_isolated() {
	c1, err := containerCreateInternal(suite.d.State(), db.ContainerArgs{
		Ctype: db.CTypeRegular,
		Name:  "isol-1",
		Config: map[string]string{
			"security.idmap.isolated": "true",
		},
	})
	suite.Req.Nil(err)
	defer c1.Delete()

	c2, err := containerCreateInternal(suite.d.State(), db.ContainerArgs{
		Ctype: db.CTypeRegular,
		Name:  "isol-2",
		Config: map[string]string{
			"security.idmap.isolated": "true",
		},
	})
	suite.Req.Nil(err)
	defer c2.Delete()

	map1, err := c1.(*containerLXC).NextIdmapSet()
	suite.Req.Nil(err)
	map2, err := c2.(*containerLXC).NextIdmapSet()
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
	c1, err := containerCreateInternal(suite.d.State(), db.ContainerArgs{
		Ctype: db.CTypeRegular,
		Name:  "isol-1",
		Config: map[string]string{
			"security.idmap.isolated": "false",
		},
	})
	suite.Req.Nil(err)
	defer c1.Delete()

	c2, err := containerCreateInternal(suite.d.State(), db.ContainerArgs{
		Ctype: db.CTypeRegular,
		Name:  "isol-2",
		Config: map[string]string{
			"security.idmap.isolated": "true",
		},
	})
	suite.Req.Nil(err)
	defer c2.Delete()

	map1, err := c1.(*containerLXC).NextIdmapSet()
	suite.Req.Nil(err)
	map2, err := c2.(*containerLXC).NextIdmapSet()
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
	c1, err := containerCreateInternal(suite.d.State(), db.ContainerArgs{
		Ctype: db.CTypeRegular,
		Name:  "isol-1",
		Config: map[string]string{
			"security.idmap.isolated": "false",
			"raw.idmap":               "both 1000 1000",
		},
	})
	suite.Req.Nil(err)
	defer c1.Delete()

	map1, err := c1.(*containerLXC).NextIdmapSet()
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
		c, err := containerCreateInternal(suite.d.State(), db.ContainerArgs{
			Ctype: db.CTypeRegular,
			Name:  fmt.Sprintf("isol-%d", i),
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

		m, err := c.(*containerLXC).NextIdmapSet()
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
