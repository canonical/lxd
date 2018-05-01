package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lxc/lxd/client"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/cmd"
	"github.com/stretchr/testify/suite"
)

type cmdInitTestSuite struct {
	lxdTestSuite
	streams *cmd.MemoryStreams
	context *cmd.Context
	args    *Args
	command *CmdInit
	client  lxd.ContainerServer
}

func (suite *cmdInitTestSuite) SetupTest() {
	suite.lxdTestSuite.SetupTest()
	suite.streams = cmd.NewMemoryStreams("")
	suite.context = cmd.NewMemoryContext(suite.streams)
	suite.args = &Args{
		NetworkPort:       -1,
		StorageCreateLoop: -1,
	}
	suite.command = &CmdInit{
		Context:         suite.context,
		Args:            suite.args,
		RunningInUserns: false,
		SocketPath:      filepath.Join(shared.VarPath(), "unix.socket"),
	}
	client, err := lxd.ConnectLXDUnix(suite.command.SocketPath, nil)
	suite.Req.Nil(err)
	suite.client = client
}

// If any argument intended for --auto is passed in interactive mode, an
// error is returned.
func (suite *cmdInitTestSuite) TestCmdInit_InteractiveWithAutoArgs() {
	suite.args.NetworkPort = 9999
	err := suite.command.Run()
	suite.Req.Equal("Init configuration is only valid with --auto", err.Error())
}

// Some arguments can only be passed together with --auto.
func (suite *cmdInitTestSuite) TestCmdInit_AutoSpecificArgs() {
	suite.args.StorageBackend = "dir"
	err := suite.command.Run()
	suite.Req.Equal("Init configuration is only valid with --auto", err.Error())
}

// If an invalid backend type is passed with --storage-backend, an
// error is returned.
func (suite *cmdInitTestSuite) TestCmdInit_AutoWithInvalidBackendType() {
	suite.args.Auto = true
	suite.args.StorageBackend = "foo"

	err := suite.command.Run()
	suite.Req.Equal("The requested backend 'foo' isn't supported by lxd init.", err.Error())
}

// If an backend type that is not available on the system is passed
// with --storage-backend, an error is returned.
func (suite *cmdInitTestSuite) TestCmdInit_AutoWithUnavailableBackendType() {
	suite.args.Auto = true
	suite.args.StorageBackend = "zfs"
	suite.command.RunningInUserns = true // This makes zfs unavailable

	err := suite.command.Run()
	suite.Req.Equal("The requested backend 'zfs' isn't available on your system (missing tools).", err.Error())
}

// If --storage-backend is set to "dir", --storage-create-device can't be passed.
func (suite *cmdInitTestSuite) TestCmdInit_AutoWithDirStorageBackendAndCreateDevice() {
	suite.args.Auto = true
	suite.args.StorageBackend = "dir"
	suite.args.StorageCreateDevice = "/dev/sda4"

	err := suite.command.Run()
	suite.Req.Equal("None of --storage-pool, --storage-create-device or --storage-create-loop may be used with the 'dir' backend.", err.Error())
}

// Convenience for building the input text a user would enter for a certain
// sequence of answers.
type cmdInitAnswers struct {
	StoragePoolDriver        string
	WantAvailableOverNetwork bool
	BindToAddress            string
	BindToPort               string
}

// Render the input text the user would type for the desired answers, populating
// the stdin of the given streams.
func (answers *cmdInitAnswers) Render(streams *cmd.MemoryStreams) {
	streams.InputAppendLine(answers.StoragePoolDriver)
	if answers.WantAvailableOverNetwork {
		streams.InputAppendLine(answers.BindToAddress)
		streams.InputAppendLine(answers.BindToPort)
	}
}

func TestCmdInitTestSuite(t *testing.T) {
	if os.Geteuid() != 0 {
		return
	}

	suite.Run(t, new(cmdInitTestSuite))
}
