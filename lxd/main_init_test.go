package main

import (
	"testing"

	"github.com/lxc/lxd/shared/cmd"
	"github.com/stretchr/testify/suite"
)

type cmdInitTestSuite struct {
	lxdTestSuite
	streams *cmd.MemoryStreams
	context *cmd.Context
	args    *CmdInitArgs
	command *CmdInit
}

func (suite *cmdInitTestSuite) SetupTest() {
	suite.lxdTestSuite.SetupTest()
	suite.streams = cmd.NewMemoryStreams("")
	suite.context = cmd.NewMemoryContext(suite.streams)
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
	suite.Req.Equal("Init configuration is only valid with --auto", err.Error())
}

// If both --auto and --preseed are passed, an error is returned.
func (suite *cmdInitTestSuite) TestCmdInit_AutoAndPreseedIncompatible() {
	suite.args.Auto = true
	suite.args.Preseed = true
	err := suite.command.Run()
	suite.Req.Equal("Non-interactive mode supported by only one of --auto or --preseed", err.Error())
}

// If the YAML preseed data is invalid, an error is returned.
func (suite *cmdInitTestSuite) TestCmdInit_PreseedInvalidYAML() {
	suite.args.Preseed = true
	suite.streams.InputAppend("g@rblEd")
	err := suite.command.Run()
	suite.Req.Equal("Invalid preseed YAML content", err.Error())
}

// Preseed the network address and the trust password.
func (suite *cmdInitTestSuite) TestCmdInit_PreseedHTTPSAddressAndTrustPassword() {
	suite.args.Preseed = true
	suite.streams.InputAppend(`config:
  core.https_address: 127.0.0.1:9999
  core.trust_password: sekret
`)
	suite.Req.Nil(suite.command.Run())

	key, _ := daemonConfig["core.https_address"]
	suite.Req.Equal("127.0.0.1:9999", key.Get())
	suite.Req.Nil(suite.d.PasswordCheck("sekret"))
}

// Input network address and trust password interactively.
func (suite *cmdInitTestSuite) TestCmdInit_InteractiveHTTPSAddressAndTrustPassword() {
	suite.command.PasswordReader = func(int) ([]byte, error) {
		return []byte("sekret"), nil
	}
	answers := &cmdInitAnswers{
		WantAvailableOverNetwork: true,
		BindToAddress:            "127.0.0.1",
		BindToPort:               "9999",
	}
	answers.Render(suite.streams)

	suite.Req.Nil(suite.command.Run())

	key, _ := daemonConfig["core.https_address"]
	suite.Req.Equal("127.0.0.1:9999", key.Get())
	suite.Req.Nil(suite.d.PasswordCheck("sekret"))
}

// Pass network address and trust password via command line arguments.
func (suite *cmdInitTestSuite) TestCmdInit_AutoHTTPSAddressAndTrustPassword() {
	suite.args.Auto = true
	suite.args.NetworkAddress = "127.0.0.1"
	suite.args.NetworkPort = 9999
	suite.args.TrustPassword = "sekret"

	suite.Req.Nil(suite.command.Run())

	key, _ := daemonConfig["core.https_address"]
	suite.Req.Equal("127.0.0.1:9999", key.Get())
	suite.Req.Nil(suite.d.PasswordCheck("sekret"))
}

// Convenience for building the input text a user would enter for a certain
// sequence of answers.
type cmdInitAnswers struct {
	WantStoragePool          bool
	WantAvailableOverNetwork bool
	BindToAddress            string
	BindToPort               string
	WantImageAutoUpdate      bool
	WantNetworkBridge        bool
}

// Render the input text the user would type for the desired answers, populating
// the stdin of the given streams.
func (answers *cmdInitAnswers) Render(streams *cmd.MemoryStreams) {
	streams.InputAppendBoolAnswer(answers.WantStoragePool)
	streams.InputAppendBoolAnswer(answers.WantAvailableOverNetwork)
	if answers.WantAvailableOverNetwork {
		streams.InputAppendLine(answers.BindToAddress)
		streams.InputAppendLine(answers.BindToPort)
	}
	streams.InputAppendBoolAnswer(answers.WantImageAutoUpdate)
	streams.InputAppendBoolAnswer(answers.WantNetworkBridge)
}

func TestCmdInitTestSuite(t *testing.T) {
	suite.Run(t, new(cmdInitTestSuite))
}
