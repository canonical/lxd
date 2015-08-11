package main

import (
	"github.com/lxc/lxd/shared"
)

func (suite *lxdTestSuite) TestContainer_ProfilesDefault() {
	args := containerLXDArgs{
		Ctype:     cTypeRegular,
		Ephemeral: false,
	}

	c, err := containerLXDCreateInternal(suite.d, "testFoo", args)
	suite.Req.Nil(err)
	defer c.Delete()

	config := c.ConfigGet()
	suite.Len(
		config.Profiles,
		1,
		"No default profile created on containerLXDCreateInternal.")

	suite.Equal(
		"default",
		config.Profiles[0],
		"First profile should be the default profile.")
}

func (suite *lxdTestSuite) TestContainer_ProfilesMulti() {
	// Create an unprivileged profile
	_, err := dbProfileCreate(
		suite.d.db,
		"unprivileged",
		map[string]string{"security.privileged": "true"},
		shared.Devices{})

	suite.Req.Nil(err, "Failed to create the unprivileged profile.")
	defer func() {
		dbProfileDelete(suite.d.db, "unprivileged")
	}()

	args := containerLXDArgs{
		Ctype:     cTypeRegular,
		Ephemeral: false,
		Profiles:  []string{"default", "unprivileged"},
	}

	c, err := containerLXDCreateInternal(suite.d, "testFoo", args)
	suite.Req.Nil(err)
	defer c.Delete()

	config := c.ConfigGet()
	suite.Len(
		config.Profiles,
		2,
		"Didn't get both profiles in containerLXDCreateInternal.")

	suite.True(
		c.IsPrivileged(),
		"The container is not privileged (didn't apply the unprivileged profile?).")
}

func (suite *lxdTestSuite) TestContainer_ProfilesOverwriteDefaultNic() {
	args := containerLXDArgs{
		Ctype:     cTypeRegular,
		Ephemeral: false,
		Config:    map[string]string{"security.privileged": "true"},
		Devices: shared.Devices{
			"eth0": shared.Device{
				"type":    "nic",
				"nictype": "bridged",
				"parent":  "unknownbr0"}},
	}

	c, err := containerLXDCreateInternal(suite.d, "testFoo", args)
	suite.Req.Nil(err)

	suite.True(c.IsPrivileged(), "This container should be privileged.")

	state, err := c.RenderState()
	suite.Req.Nil(err)
	defer c.Delete()

	suite.Equal(
		"unknownbr0",
		state.Devices["eth0"]["parent"],
		"Container config doesn't overwrite profile config.")
}

func (suite *lxdTestSuite) TestContainer_LoadFromDB() {
	args := containerLXDArgs{
		Ctype:     cTypeRegular,
		Ephemeral: false,
		Config:    map[string]string{"security.privileged": "true"},
		Devices: shared.Devices{
			"eth0": shared.Device{
				"type":    "nic",
				"nictype": "bridged",
				"parent":  "unknownbr0"}},
	}

	c, err := containerLXDCreateInternal(suite.d, "testFoo", args)
	suite.Req.Nil(err)
	defer c.Delete()

	c2, err := containerLXDLoad(suite.d, "testFoo")
	suite.Exactly(
		c,
		c2,
		"The loaded container isn't excactly the same as the created one.")
}

func (suite *lxdTestSuite) TestContainer_PathGet_Regular() {
	// Regular
	args := containerLXDArgs{
		Ctype:     cTypeRegular,
		Ephemeral: false,
	}

	c, err := containerLXDCreateInternal(suite.d, "testFoo", args)
	suite.Req.Nil(err)
	defer c.Delete()

	suite.Req.False(c.IsSnapshot(), "Shouldn't be a snapshot.")
	suite.Req.Equal(shared.VarPath("containers", "testFoo"), c.PathGet(""))
	suite.Req.Equal(shared.VarPath("containers", "testFoo2"), c.PathGet("testFoo2"))
}

func (suite *lxdTestSuite) TestContainer_PathGet_Snapshot() {
	// Snapshot
	args := containerLXDArgs{
		Ctype:     cTypeSnapshot,
		Ephemeral: false,
	}

	c, err := containerLXDCreateInternal(suite.d, "test/snap0", args)
	suite.Req.Nil(err)
	defer c.Delete()

	suite.Req.True(c.IsSnapshot(), "Should be a snapshot.")
	suite.Req.Equal(
		shared.VarPath("snapshots", "test", "snap0"),
		c.PathGet(""))
	suite.Req.Equal(
		shared.VarPath("snapshots", "test", "snap1"),
		c.PathGet("test/snap1"))
}

func (suite *lxdTestSuite) TestContainer_LogPathGet() {
	args := containerLXDArgs{
		Ctype:     cTypeRegular,
		Ephemeral: false,
	}

	c, err := containerLXDCreateInternal(suite.d, "testFoo", args)
	suite.Req.Nil(err)
	defer c.Delete()

	suite.Req.Equal(shared.VarPath("logs", "testFoo"), c.LogPathGet())
}

func (suite *lxdTestSuite) TestContainer_IsPrivileged_Privileged() {
	args := containerLXDArgs{
		Ctype:     cTypeRegular,
		Ephemeral: false,
		Config:    map[string]string{"security.privileged": "true"},
	}

	c, err := containerLXDCreateInternal(suite.d, "testFoo", args)
	suite.Req.Nil(err)
	defer c.Delete()

	suite.Req.True(c.IsPrivileged(), "This container should be privileged.")
	suite.Req.Nil(c.Delete(), "Failed to delete the container.")
}

func (suite *lxdTestSuite) TestContainer_IsPrivileged_Unprivileged() {
	args := containerLXDArgs{
		Ctype:     cTypeRegular,
		Ephemeral: false,
		Config:    map[string]string{"security.privileged": "false"},
	}

	c, err := containerLXDCreateInternal(suite.d, "testFoo", args)
	suite.Req.Nil(err)
	defer c.Delete()

	suite.Req.False(c.IsPrivileged(), "This container should be unprivileged.")
	suite.Req.Nil(c.Delete(), "Failed to delete the container.")
}

func (suite *lxdTestSuite) TestContainer_Rename() {
	args := containerLXDArgs{
		Ctype:     cTypeRegular,
		Ephemeral: false,
	}

	c, err := containerLXDCreateInternal(suite.d, "testFoo", args)
	suite.Req.Nil(err)
	defer c.Delete()

	suite.Req.Nil(c.Rename("testFoo2"), "Failed to rename the container.")
	suite.Req.Equal(shared.VarPath("containers", "testFoo2"), c.PathGet(""))
}
