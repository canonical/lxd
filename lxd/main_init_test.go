package main

import (
	"fmt"
	"path"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/util"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
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

// If both --auto and --preseed are passed, an error is returned.
func (suite *cmdInitTestSuite) TestCmdInit_AutoAndPreseedIncompatible() {
	suite.args.Auto = true
	suite.args.Preseed = true
	err := suite.command.Run()
	suite.Req.Equal("Non-interactive mode supported by only one of --auto or --preseed", err.Error())
}

// Some arguments can only be passed together with --auto.
func (suite *cmdInitTestSuite) TestCmdInit_AutoSpecificArgs() {
	suite.args.StorageBackend = "dir"
	err := suite.command.Run()
	suite.Req.Equal("Init configuration is only valid with --auto", err.Error())
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
	port, err := shared.AllocatePort()
	suite.Req.Nil(err)

	suite.args.Preseed = true
	suite.streams.InputAppend(fmt.Sprintf(`config:
  core.https_address: 127.0.0.1:%d
  core.trust_password: sekret
`, port))
	suite.Req.Nil(suite.command.Run())

	key, _ := daemonConfig["core.https_address"]
	suite.Req.Equal(fmt.Sprintf("127.0.0.1:%d", port), key.Get())
	secret := daemonConfig["core.trust_password"].Get()
	suite.Req.Nil(util.PasswordCheck(secret, "sekret"))
}

// Input network address and trust password interactively.
func (suite *cmdInitTestSuite) TestCmdInit_InteractiveHTTPSAddressAndTrustPassword() {
	suite.command.PasswordReader = func(int) ([]byte, error) {
		return []byte("sekret"), nil
	}
	port, err := shared.AllocatePort()
	suite.Req.Nil(err)
	answers := &cmdInitAnswers{
		WantAvailableOverNetwork: true,
		BindToAddress:            "127.0.0.1",
		BindToPort:               strconv.Itoa(port),
	}
	answers.Render(suite.streams)

	suite.Req.Nil(suite.command.Run())

	key, _ := daemonConfig["core.https_address"]
	suite.Req.Equal(fmt.Sprintf("127.0.0.1:%d", port), key.Get())
	secret := daemonConfig["core.trust_password"].Get()
	suite.Req.Nil(util.PasswordCheck(secret, "sekret"))
}

// Pass network address and trust password via command line arguments.
func (suite *cmdInitTestSuite) TestCmdInit_AutoHTTPSAddressAndTrustPassword() {
	port, err := shared.AllocatePort()
	suite.Req.Nil(err)

	suite.args.Auto = true
	suite.args.NetworkAddress = "127.0.0.1"
	suite.args.NetworkPort = int64(port)
	suite.args.TrustPassword = "sekret"

	suite.Req.Nil(suite.command.Run())

	key, _ := daemonConfig["core.https_address"]
	suite.Req.Equal(fmt.Sprintf("127.0.0.1:%d", port), key.Get())
	secret := daemonConfig["core.trust_password"].Get()
	suite.Req.Nil(util.PasswordCheck(secret, "sekret"))
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

// If --storage-backend is set to "dir", and both of --storage-create-device
// or --storage-create-loop are given, an error is returned.
func (suite *cmdInitTestSuite) TestCmdInit_AutoWithNonDirBackendAndNoDeviceOrLoop() {
	suite.args.Auto = true
	suite.args.StorageBackend = "btrfs"
	suite.args.StorageCreateDevice = "/dev/sda4"
	suite.args.StorageCreateLoop = 1

	err := suite.command.Run()
	suite.Req.Equal("Only one of --storage-create-device or --storage-create-loop can be specified.", err.Error())
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

// If --storage-backend is set to "dir" a storage pool is created.
func (suite *cmdInitTestSuite) TestCmdInit_StoragePoolAuto() {
	// Clear the storage pool created by default by the test suite
	profile, _, err := suite.client.GetProfile("default")
	suite.Req.Nil(err)
	profileData := profile.Writable()
	delete(profileData.Devices, "root")
	err = suite.client.UpdateProfile("default", profileData, "")
	suite.Req.Nil(err)
	suite.Req.Nil(suite.client.DeleteStoragePool(lxdTestSuiteDefaultStoragePool))

	suite.args.Auto = true
	suite.args.StorageBackend = "dir"

	suite.Req.Nil(suite.command.Run())
	pool, _, err := suite.client.GetStoragePool("default")
	suite.Req.Nil(err)
	suite.Req.Equal("dir", pool.Driver)
	suite.Req.Equal(path.Join(suite.tmpdir, "storage-pools", "default"), pool.Config["source"])
}

// Preseed a new storage pool.
func (suite *cmdInitTestSuite) TestCmdInit_StoragePoolPreseed() {
	suite.args.Preseed = true
	suite.streams.InputAppend(`storage_pools:
- name: foo
  driver: dir
  config:
    source: ""
`)

	suite.Req.Nil(suite.command.Run())

	pool, _, err := suite.client.GetStoragePool("foo")
	suite.Req.Nil(err)
	suite.Req.Equal("dir", pool.Driver)
	suite.Req.Equal(path.Join(suite.tmpdir, "storage-pools", "foo"), pool.Config["source"])
}

// If an error occurs when creating a new storage pool, all new pools created
// so far get deleted. Any server config that got applied, gets reset too.
func (suite *cmdInitTestSuite) TestCmdInit_StoragePoolCreateRevert() {
	suite.args.Preseed = true
	suite.streams.InputAppend(`config:
images.auto_update_interval: 15
storage_pools:
- name: first
  driver: dir
  config:
    source: ""
- name: second
  driver: dir
  config:
    boom: garbage
`)

	err := suite.command.Run()
	suite.Req.Equal("Invalid storage pool configuration key: boom", err.Error())

	_, _, err = suite.client.GetStoragePool("first")
	suite.Req.Equal("not found", err.Error())

	_, _, err = suite.client.GetStoragePool("second")
	suite.Req.Equal("not found", err.Error())

	key, _ := daemonConfig["images.auto_update_interval"]
	suite.Req.NotEqual("15", key.Get())
}

// Updating a storage pool via preseed will fail, since it's not supported
// by the API.
func (suite *cmdInitTestSuite) TestCmdInit_StoragePoolPreseedUpdate() {
	post := api.StoragePoolsPost{
		Name:   "egg",
		Driver: "dir",
	}
	post.Config = map[string]string{
		"source": "",
	}
	err := suite.client.CreateStoragePool(post)
	suite.Req.Nil(err)

	suite.args.Preseed = true
	suite.streams.InputAppend(`storage_pools:
- name: egg
  driver: dir
  config:
    source: /egg
`)

	err = suite.command.Run()
	suite.Req.Error(err)
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

// Update a network via preseed.
func (suite *cmdInitTestSuite) TestCmdInit_NetworkPreseedUpdate() {
	post := api.NetworksPost{
		Name: "egg",
	}
	post.Config = map[string]string{
		"ipv4.address": "10.48.159.1/24",
		"ipv4.nat":     "true",
		"ipv6.address": "none",
	}
	err := suite.client.CreateNetwork(post)
	suite.Req.Nil(err)

	suite.args.Preseed = true
	suite.streams.InputAppend(`networks:
- name: egg
  type: bridge
  config:
    ipv4.address: none
    ipv4.nat: false
    ipv6.address: auto
`)

	suite.Req.Nil(suite.command.Run())

	network, _, err := suite.client.GetNetwork("egg")
	suite.Req.Nil(err)
	suite.Req.Equal("bridge", network.Type)
	suite.Req.Equal("none", network.Config["ipv4.address"])
	suite.Req.Equal("false", network.Config["ipv4.nat"])
	suite.Req.Nil(networkValidAddressCIDRV6(network.Config["ipv6.address"]))
}

// Updating a network via preseed and changing it's type to something else
// than "bridge" results in an error.
func (suite *cmdInitTestSuite) TestCmdInit_NetworkPreseedUpdateNonBridge() {
	post := api.NetworksPost{
		Name: "baz",
	}
	post.Config = map[string]string{
		"ipv4.address": "10.48.159.1/24",
		"ipv4.nat":     "true",
		"ipv6.address": "none",
	}
	err := suite.client.CreateNetwork(post)
	suite.Req.Nil(err)

	suite.args.Preseed = true
	suite.streams.InputAppend(`networks:
- name: baz
  type: physical
  config:
    ipv4.address: 10.48.159.1/24
    ipv4.nat: true
    ipv6.address: none
`)

	err = suite.command.Run()
	suite.Req.Equal("Only 'bridge' type networks are supported", err.Error())
}

// If an error occurs when creating a new network, all new networks created
// so far get deleted.
func (suite *cmdInitTestSuite) TestCmdInit_NetworkCreateRevert() {
	suite.args.Preseed = true
	suite.streams.InputAppend(`networks:
- name: first
  type: bridge
  config:
    ipv4.address: 10.48.159.1/24
    ipv4.nat: true
    ipv6.address: none
- name: second
  type: bridge
  config:
    boom: garbage
`)

	err := suite.command.Run()
	suite.Req.Equal("Invalid network configuration key: boom", err.Error())

	_, _, err = suite.client.GetNetwork("first")
	suite.Req.Equal("not found", err.Error())

	_, _, err = suite.client.GetNetwork("second")
	suite.Req.Equal("not found", err.Error())
}

// Preseed a new profile.
func (suite *cmdInitTestSuite) TestCmdInit_ProfilesPreseed() {
	suite.args.Preseed = true
	suite.streams.InputAppend(`profiles:
- name: bar
  description: "Bar profile"
  config:
    limits.memory: 2GB
  devices:
    data:
      path: /srv/data/
      source: /some/data
      type: disk
`)

	suite.Req.Nil(suite.command.Run())

	profile, _, err := suite.client.GetProfile("bar")
	suite.Req.Nil(err)
	suite.Req.Equal("Bar profile", profile.Description)
	suite.Req.Equal("2GB", profile.Config["limits.memory"])
	suite.Req.Equal("/srv/data/", profile.Devices["data"]["path"])
	suite.Req.Equal("/some/data", profile.Devices["data"]["source"])
	suite.Req.Equal("disk", profile.Devices["data"]["type"])
}

// If an error occurs while creating a new profile, all other profiles
// created in by the preseeded YAML get deleted.
func (suite *cmdInitTestSuite) TestCmdInit_ProfilesCreateRevert() {
	suite.args.Preseed = true
	suite.streams.InputAppend(`profiles:
- name: first
  description: "First profile"
  config:
    limits.memory: 2GB
  devices:
    data:
      path: /srv/data/
      source: /some/data
      type: disk
- name: second
  description: "Second profile"
  config:
    boom: garbage
  devices:
    data:
      path: /srv/data/
      source: /some/data
      type: disk
`)

	err := suite.command.Run()
	suite.Req.Equal("Unknown configuration key: boom", err.Error())
	_, _, err = suite.client.GetProfile("first")
	suite.Req.Equal("not found", err.Error())

	_, _, err = suite.client.GetProfile("second")
	suite.Req.Equal("not found", err.Error())
}

// If an error occurs while creating a new profile, all other profiles
// that have been updated in by the preseeded YAML get reverted.
func (suite *cmdInitTestSuite) TestCmdInit_ProfilesUpdateRevert() {
	post := api.ProfilesPost{
		Name: "first",
	}
	post.Description = "First profile profile"
	post.Config = map[string]string{
		"limits.memory": "2GB",
	}
	post.Devices = map[string]map[string]string{
		"data": {
			"path":   "/srv/data/",
			"source": "/some/data",
			"type":   "disk",
		},
	}
	err := suite.client.CreateProfile(post)

	suite.Req.Nil(err)

	suite.args.Preseed = true
	suite.streams.InputAppend(`profiles:
- name: first
  description: "First profile"
  config:
    limits.memory: 4GB
  devices:
    data:
      path: /srv/data/
      source: /some/data
      type: disk
- name: second
  description: "Second profile"
  config:
    boom: garbage
  devices:
    data:
      path: /srv/data/
      source: /some/data
      type: disk
`)

	err = suite.command.Run()
	suite.Req.Equal("Unknown configuration key: boom", err.Error())

	profile, _, err := suite.client.GetProfile("first")
	suite.Req.Nil(err)
	suite.Req.Equal("2GB", profile.Config["limits.memory"])

	_, _, err = suite.client.GetProfile("second")
	suite.Req.Equal("not found", err.Error())
}

// Update a profile via preseed.
func (suite *cmdInitTestSuite) TestCmdInit_ProfilesPreseedUpdate() {
	post := api.ProfilesPost{
		Name: "egg",
	}
	post.Description = "Egg profile"
	post.Config = map[string]string{
		"limits.memory": "2GB",
	}
	post.Devices = map[string]map[string]string{
		"data": {
			"path":   "/srv/data/",
			"source": "/some/data",
			"type":   "disk",
		},
	}
	err := suite.client.CreateProfile(post)
	suite.Req.Nil(err)

	suite.args.Preseed = true
	suite.streams.InputAppend(`profiles:
- name: egg
  description: "Egg profile enhanced"
  config:
    limits.memory: 4GB
  devices:
    data:
      path: /srv/more/data/
      source: /some/data
      type: disk
`)

	suite.Req.Nil(suite.command.Run())

	profile, _, err := suite.client.GetProfile("egg")
	suite.Req.Nil(err)
	suite.Req.Equal("Egg profile enhanced", profile.Description)
	suite.Req.Equal("4GB", profile.Config["limits.memory"])
	suite.Req.Equal("/srv/more/data/", profile.Devices["data"]["path"])
	suite.Req.Equal("/some/data", profile.Devices["data"]["source"])
	suite.Req.Equal("disk", profile.Devices["data"]["type"])
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
