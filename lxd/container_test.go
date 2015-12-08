package main

import (
	"github.com/lxc/lxd/shared"
)

func (suite *lxdTestSuite) TestContainer_ProfilesDefault() {
	args := containerArgs{
		Ctype:     cTypeRegular,
		Ephemeral: false,
		Name:      "testFoo",
	}

	c, err := containerCreateInternal(suite.d, args)
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

	args := containerArgs{
		Ctype:     cTypeRegular,
		Ephemeral: false,
		Profiles:  []string{"default", "unprivileged"},
		Name:      "testFoo",
	}

	c, err := containerCreateInternal(suite.d, args)
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

func (suite *lxdTestSuite) TestContainer_ProfilesOverwriteDefaultNic() {
	args := containerArgs{
		Ctype:     cTypeRegular,
		Ephemeral: false,
		Config:    map[string]string{"security.privileged": "true"},
		Devices: shared.Devices{
			"eth0": shared.Device{
				"type":    "nic",
				"nictype": "bridged",
				"parent":  "unknownbr0"}},
		Name: "testFoo",
	}

	c, err := containerCreateInternal(suite.d, args)
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
	args := containerArgs{
		Ctype:     cTypeRegular,
		Ephemeral: false,
		Config:    map[string]string{"security.privileged": "true"},
		Devices: shared.Devices{
			"eth0": shared.Device{
				"type":    "nic",
				"nictype": "bridged",
				"parent":  "unknownbr0"}},
		Name: "testFoo",
	}

	// Create the container
	c, err := containerCreateInternal(suite.d, args)
	suite.Req.Nil(err)
	defer c.Delete()

	// Load the container and trigger initLXC()
	c2, err := containerLoadByName(suite.d, "testFoo")
	c2.IsRunning()
	suite.Req.Nil(err)

	suite.Exactly(
		c,
		c2,
		"The loaded container isn't excactly the same as the created one.")
}

func (suite *lxdTestSuite) TestContainer_Path_Regular() {
	// Regular
	args := containerArgs{
		Ctype:     cTypeRegular,
		Ephemeral: false,
		Name:      "testFoo",
	}

	c, err := containerCreateInternal(suite.d, args)
	suite.Req.Nil(err)
	defer c.Delete()

	suite.Req.False(c.IsSnapshot(), "Shouldn't be a snapshot.")
	suite.Req.Equal(shared.VarPath("containers", "testFoo"), c.Path())
	suite.Req.Equal(shared.VarPath("containers", "testFoo2"), containerPath("testFoo2", false))
}

func (suite *lxdTestSuite) TestContainer_Path_Snapshot() {
	// Snapshot
	args := containerArgs{
		Ctype:     cTypeSnapshot,
		Ephemeral: false,
		Name:      "test/snap0",
	}

	c, err := containerCreateInternal(suite.d, args)
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

func (suite *lxdTestSuite) TestContainer_LogPath() {
	args := containerArgs{
		Ctype:     cTypeRegular,
		Ephemeral: false,
		Name:      "testFoo",
	}

	c, err := containerCreateInternal(suite.d, args)
	suite.Req.Nil(err)
	defer c.Delete()

	suite.Req.Equal(shared.VarPath("logs", "testFoo"), c.LogPath())
}

func (suite *lxdTestSuite) TestContainer_IsPrivileged_Privileged() {
	args := containerArgs{
		Ctype:     cTypeRegular,
		Ephemeral: false,
		Config:    map[string]string{"security.privileged": "true"},
		Name:      "testFoo",
	}

	c, err := containerCreateInternal(suite.d, args)
	suite.Req.Nil(err)
	defer c.Delete()

	suite.Req.True(c.IsPrivileged(), "This container should be privileged.")
	suite.Req.Nil(c.Delete(), "Failed to delete the container.")
}

func (suite *lxdTestSuite) TestContainer_IsPrivileged_Unprivileged() {
	args := containerArgs{
		Ctype:     cTypeRegular,
		Ephemeral: false,
		Config:    map[string]string{"security.privileged": "false"},
		Name:      "testFoo",
	}

	c, err := containerCreateInternal(suite.d, args)
	suite.Req.Nil(err)
	defer c.Delete()

	suite.Req.False(c.IsPrivileged(), "This container should be unprivileged.")
	suite.Req.Nil(c.Delete(), "Failed to delete the container.")
}

func (suite *lxdTestSuite) TestContainer_Rename() {
	args := containerArgs{
		Ctype:     cTypeRegular,
		Ephemeral: false,
		Name:      "testFoo",
	}

	c, err := containerCreateInternal(suite.d, args)
	suite.Req.Nil(err)
	defer c.Delete()

	suite.Req.Nil(c.Rename("testFoo2"), "Failed to rename the container.")
	suite.Req.Equal(shared.VarPath("containers", "testFoo2"), c.Path())
}
