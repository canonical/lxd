package main

import (
	"testing"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/shared/cmd"
	"github.com/stretchr/testify/suite"
)

type cmdInitTestSuite struct {
	lxdTestSuite
	streams *cmd.MemoryStreams
	context *cmd.Context
	args    *CmdInitArgs
	command *CmdInit
	client  lxd.ContainerServer
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

// The images auto-update interval can be interactively set by simply accepting
// the answer "yes" to the relevant question.
func (suite *cmdInitTestSuite) TestCmdInit_ImagesAutoUpdateAnswerYes() {
	answers := &cmdInitAnswers{
		WantImageAutoUpdate: true,
	}
	answers.Render(suite.streams)

	suite.Req.Nil(suite.command.Run())

	key, _ := daemonConfig["images.auto_update_interval"]
	suite.Req.Equal("6", key.Get())
}

// If the images auto-update interval value is already set to non-zero, it
// won't be overwritten.
func (suite *cmdInitTestSuite) TestCmdInit_ImagesAutoUpdateNoOverwrite() {
	key, _ := daemonConfig["images.auto_update_interval"]
	err := key.Set(suite.d, "10")
	suite.Req.Nil(err)

	answers := &cmdInitAnswers{
		WantImageAutoUpdate: true,
	}
	answers.Render(suite.streams)

	suite.Req.Nil(suite.command.Run())

	suite.Req.Equal("10", key.Get())
}

// If the user answers "no" to the images auto-update question, the value will
// be set to 0.
func (suite *cmdInitTestSuite) TestCmdInit_ImagesAutoUpdateAnswerNo() {
	answers := &cmdInitAnswers{
		WantImageAutoUpdate: false,
	}
	answers.Render(suite.streams)

	suite.Req.Nil(suite.command.Run())

	key, _ := daemonConfig["images.auto_update_interval"]
	suite.Req.Equal("0", key.Get())
}

// If the user answers "no" to the images auto-update question, the value will
// be set to 0, even it was already set to some value.
func (suite *cmdInitTestSuite) TestCmdInit_ImagesAutoUpdateOverwriteIfZero() {
	key, _ := daemonConfig["images.auto_update_interval"]
	key.Set(suite.d, "10")

	answers := &cmdInitAnswers{
		WantImageAutoUpdate: false,
	}
	answers.Render(suite.streams)

	suite.Req.Nil(suite.command.Run())
	suite.Req.Equal("0", key.Get())
}

// Preseed the image auto-update interval.
func (suite *cmdInitTestSuite) TestCmdInit_ImagesAutoUpdatePreseed() {
	suite.args.Preseed = true
	suite.streams.InputAppend(`config:
  images.auto_update_interval: 15
`)
	suite.Req.Nil(suite.command.Run())

	key, _ := daemonConfig["images.auto_update_interval"]
	suite.Req.Equal("15", key.Get())
}

// It's possible to configure a network bridge interactively.
func (suite *cmdInitTestSuite) TestCmdInit_NetworkInteractive() {
	answers := &cmdInitAnswers{
		WantNetworkBridge: true,
		BridgeName:        "foo",
		BridgeIPv4:        "auto",
		BridgeIPv6:        "auto",
	}
	answers.Render(suite.streams)

	suite.Req.Nil(suite.command.Run())

	network, _, err := suite.client.GetNetwork("foo")
	suite.Req.Nil(err)
	suite.Req.Equal("bridge", network.Type)
	suite.Req.Nil(networkValidAddressCIDRV4(network.Config["ipv4.address"]))
	suite.Req.Nil(networkValidAddressCIDRV6(network.Config["ipv6.address"]))
}

// Preseed a network of type bridge.
func (suite *cmdInitTestSuite) TestCmdInit_NetworkPreseed() {
	suite.args.Preseed = true
	suite.streams.InputAppend(`networks:
- name: bar
  type: bridge
  config:
    ipv4.address: 10.48.159.1/24
    ipv4.nat: true
    ipv6.address: none
`)

	suite.Req.Nil(suite.command.Run())

	network, _, err := suite.client.GetNetwork("bar")
	suite.Req.Nil(err)
	suite.Req.Equal("bridge", network.Type)
	suite.Req.Equal("10.48.159.1/24", network.Config["ipv4.address"])
	suite.Req.Equal("true", network.Config["ipv4.nat"])
	suite.Req.Equal("none", network.Config["ipv6.address"])
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
	BridgeName               string
	BridgeIPv4               string
	BridgeIPv6               string
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
	if answers.WantNetworkBridge {
		streams.InputAppendLine(answers.BridgeName)
		streams.InputAppendLine(answers.BridgeIPv4)
		streams.InputAppendLine(answers.BridgeIPv6)
	}
}

func TestCmdInitTestSuite(t *testing.T) {
	suite.Run(t, new(cmdInitTestSuite))
}
