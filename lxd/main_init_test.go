package main

import (
	"testing"

	"github.com/lxc/lxd/shared/cmd"
	"github.com/stretchr/testify/suite"
)

type cmdInitTestSuite struct {
	lxdTestSuite
	context *cmd.Context
	args    *CmdInitArgs
	command *CmdInit
}

func (suite *cmdInitTestSuite) SetupSuite() {
	suite.lxdTestSuite.SetupSuite()
	suite.context = cmd.NewMemoryContext(cmd.NewMemoryStreams(""))
	suite.args = &CmdInitArgs{
		NetworkPort:       -1,
		StorageCreateLoop: -1,
	}
	suite.command = &CmdInit{
		Context:         suite.context,
		Args:            suite.args,
		RunningInUserns: false,
		SocketPath:      suite.d.UnixSocket.Socket.Addr().String(),
	}
}

// If any argument intended for --auto is passed in interactive mode, an
// error is returned.
func (suite *cmdInitTestSuite) TestCmdInit_InteractiveWithAutoArgs() {
	suite.args.NetworkPort = 9999
	err := suite.command.Run()
	suite.Req.Equal(err.Error(), "Init configuration is only valid with --auto")
}

func TestCmdInitTestSuite(t *testing.T) {
	suite.Run(t, new(cmdInitTestSuite))
}
