package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/i18n"
)

type profileList []string

var configMap map[string]string

func (f *profileList) String() string {
	return fmt.Sprint(*f)
}

type configList []string

func (f *configList) String() string {
	return fmt.Sprint(configMap)
}

func (f *configList) Set(value string) error {
	if value == "" {
		return fmt.Errorf(i18n.G("Invalid configuration key"))
	}

	items := strings.SplitN(value, "=", 2)
	if len(items) < 2 {
		return fmt.Errorf(i18n.G("Invalid configuration key"))
	}

	if configMap == nil {
		configMap = map[string]string{}
	}

	configMap[items[0]] = items[1]

	return nil
}

func (f *profileList) Set(value string) error {
	if value == "" {
		initRequestedEmptyProfiles = true
		return nil
	}
	if f == nil {
		*f = make(profileList, 1)
	} else {
		*f = append(*f, value)
	}
	return nil
}

var initRequestedEmptyProfiles bool

type initCmd struct {
	profArgs     profileList
	confArgs     configList
	ephem        bool
	network      string
	storagePool  string
	instanceType string
}

func (c *initCmd) showByDefault() bool {
	return false
}

func (c *initCmd) usage() string {
	return i18n.G(
		`Usage: lxc init [<remote>:]<image> [<remote>:][<name>] [--ephemeral|-e] [--profile|-p <profile>...] [--config|-c <key=value>...] [--network|-n <network>] [--storage|-s <pool>] [--type|-t <instance type>]

Create containers from images.

Not specifying -p will result in the default profile.
Specifying "-p" with no argument will result in no profile.

Examples:
    lxc init ubuntu:16.04 u1`)
}

func (c *initCmd) is_ephem(s string) bool {
	switch s {
	case "-e":
		return true
	case "--ephemeral":
		return true
	}
	return false
}

func (c *initCmd) is_profile(s string) bool {
	switch s {
	case "-p":
		return true
	case "--profile":
		return true
	}
	return false
}

func (c *initCmd) massage_args() {
	l := len(os.Args)
	if l < 2 {
		return
	}

	if c.is_profile(os.Args[l-1]) {
		initRequestedEmptyProfiles = true
		os.Args = os.Args[0 : l-1]
		return
	}

	if l < 3 {
		return
	}

	/* catch "lxc init ubuntu -p -e */
	if c.is_ephem(os.Args[l-1]) && c.is_profile(os.Args[l-2]) {
		initRequestedEmptyProfiles = true
		newargs := os.Args[0 : l-2]
		newargs = append(newargs, os.Args[l-1])
		os.Args = newargs
		return
	}
}

func (c *initCmd) flags() {
	c.massage_args()
	gnuflag.Var(&c.confArgs, "config", i18n.G("Config key/value to apply to the new container"))
	gnuflag.Var(&c.confArgs, "c", i18n.G("Config key/value to apply to the new container"))
	gnuflag.Var(&c.profArgs, "profile", i18n.G("Profile to apply to the new container"))
	gnuflag.Var(&c.profArgs, "p", i18n.G("Profile to apply to the new container"))
	gnuflag.BoolVar(&c.ephem, "ephemeral", false, i18n.G("Ephemeral container"))
	gnuflag.BoolVar(&c.ephem, "e", false, i18n.G("Ephemeral container"))
	gnuflag.StringVar(&c.network, "network", "", i18n.G("Network name"))
	gnuflag.StringVar(&c.network, "n", "", i18n.G("Network name"))
	gnuflag.StringVar(&c.storagePool, "storage", "", i18n.G("Storage pool name"))
	gnuflag.StringVar(&c.storagePool, "s", "", i18n.G("Storage pool name"))
	gnuflag.StringVar(&c.instanceType, "t", "", i18n.G("Instance type"))
}

func (c *initCmd) run(conf *config.Config, args []string) error {
	_, _, err := c.create(conf, args)
	return err
}

func (c *initCmd) create(conf *config.Config, args []string) (lxd.ContainerServer, string, error) {
	if len(args) > 2 || len(args) < 1 {
		return nil, "", errArgs
	}

	iremote, image, err := conf.ParseRemote(args[0])
	if err != nil {
		return nil, "", err
	}

	var name string
	var remote string
	if len(args) == 2 {
		remote, name, err = conf.ParseRemote(args[1])
		if err != nil {
			return nil, "", err
		}
	} else {
		remote, name, err = conf.ParseRemote("")
		if err != nil {
			return nil, "", err
		}
	}

	d, err := conf.GetContainerServer(remote)
	if err != nil {
		return nil, "", err
	}

	/*
	 * initRequestedEmptyProfiles means user requested empty
	 * !initRequestedEmptyProfiles but len(profArgs) == 0 means use profile default
	 */
	profiles := []string{}
	for _, p := range c.profArgs {
		profiles = append(profiles, p)
	}

	if name == "" {
		fmt.Printf(i18n.G("Creating the container") + "\n")
	} else {
		fmt.Printf(i18n.G("Creating %s")+"\n", name)
	}

	devicesMap := map[string]map[string]string{}
	if c.network != "" {
		network, _, err := d.GetNetwork(c.network)
		if err != nil {
			return nil, "", err
		}

		if network.Type == "bridge" {
			devicesMap[c.network] = map[string]string{"type": "nic", "nictype": "bridged", "parent": c.network}
		} else {
			devicesMap[c.network] = map[string]string{"type": "nic", "nictype": "macvlan", "parent": c.network}
		}
	}

	// Check if the specified storage pool exists.
	if c.storagePool != "" {
		_, _, err := d.GetStoragePool(c.storagePool)
		if err != nil {
			return nil, "", err
		}

		devicesMap["root"] = map[string]string{
			"type": "disk",
			"path": "/",
			"pool": c.storagePool,
		}
	}

	// Get the image server and image info
	iremote, image = c.guessImage(conf, d, remote, iremote, image)
	var imgRemote lxd.ImageServer
	var imgInfo *api.Image

	// Connect to the image server
	if iremote == remote {
		imgRemote = d
	} else {
		imgRemote, err = conf.GetImageServer(iremote)
		if err != nil {
			return nil, "", err
		}
	}

	// Deal with the default image
	if image == "" {
		image = "default"
	}

	// Setup container creation request
	req := api.ContainersPost{
		Name:         name,
		InstanceType: c.instanceType,
	}
	req.Config = configMap
	req.Devices = devicesMap
	if !initRequestedEmptyProfiles && len(profiles) == 0 {
		req.Profiles = nil
	} else {
		req.Profiles = profiles
	}
	req.Ephemeral = c.ephem

	// Optimisation for simplestreams
	if conf.Remotes[iremote].Protocol == "simplestreams" {
		imgInfo = &api.Image{}
		imgInfo.Fingerprint = image
		imgInfo.Public = true
		req.Source.Alias = image
	} else {
		// Attempt to resolve an image alias
		alias, _, err := imgRemote.GetImageAlias(image)
		if err == nil {
			req.Source.Alias = image
			image = alias.Target
		}

		// Get the image info
		imgInfo, _, err = imgRemote.GetImage(image)
		if err != nil {
			return nil, "", err
		}
	}

	// Create the container
	op, err := d.CreateContainerFromImage(imgRemote, *imgInfo, req)
	if err != nil {
		return nil, "", err
	}

	// Watch the background operation
	progress := ProgressRenderer{Format: i18n.G("Retrieving image: %s")}
	_, err = op.AddHandler(progress.UpdateOp)
	if err != nil {
		progress.Done("")
		return nil, "", err
	}

	err = cancelableWait(op, &progress)
	if err != nil {
		progress.Done("")
		return nil, "", err
	}
	progress.Done("")

	// Extract the container name
	opInfo, err := op.GetTarget()
	if err != nil {
		return nil, "", err
	}

	containers, ok := opInfo.Resources["containers"]
	if !ok || len(containers) == 0 {
		return nil, "", fmt.Errorf(i18n.G("didn't get any affected image, container or snapshot from server"))
	}

	if len(containers) == 1 && name == "" {
		fields := strings.Split(containers[0], "/")
		name = fields[len(fields)-1]
		fmt.Printf(i18n.G("Container name is: %s")+"\n", name)
	}

	// Validate the network setup
	c.checkNetwork(d, name)

	return d, name, nil
}

func (c *initCmd) guessImage(conf *config.Config, d lxd.ContainerServer, remote string, iremote string, image string) (string, string) {
	if remote != iremote {
		return iremote, image
	}

	_, ok := conf.Remotes[image]
	if !ok {
		return iremote, image
	}

	_, _, err := d.GetImageAlias(image)
	if err == nil {
		return iremote, image
	}

	_, _, err = d.GetImage(image)
	if err == nil {
		return iremote, image
	}

	fmt.Fprintf(os.Stderr, i18n.G("The local image '%s' couldn't be found, trying '%s:' instead.")+"\n", image, image)
	return image, "default"
}

func (c *initCmd) checkNetwork(d lxd.ContainerServer, name string) {
	ct, _, err := d.GetContainer(name)
	if err != nil {
		return
	}

	for _, d := range ct.ExpandedDevices {
		if d["type"] == "nic" {
			return
		}
	}

	fmt.Fprintf(os.Stderr, "\n"+i18n.G("The container you are starting doesn't have any network attached to it.")+"\n")
	fmt.Fprintf(os.Stderr, "  "+i18n.G("To create a new network, use: lxc network create")+"\n")
	fmt.Fprintf(os.Stderr, "  "+i18n.G("To attach a network to a container, use: lxc network attach")+"\n\n")
}
