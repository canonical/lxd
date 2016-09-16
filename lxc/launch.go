package main

import (
	"fmt"
	"strings"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/i18n"
)

type launchCmd struct {
	init initCmd
}

func (c *launchCmd) showByDefault() bool {
	return true
}

func (c *launchCmd) usage() string {
	return i18n.G(
		`Launch a container from a particular image.

lxc launch [remote:]<image> [remote:][<name>] [--ephemeral|-e] [--profile|-p <profile>...] [--config|-c <key=value>...] [--network|-n <network>]

Launches a container using the specified image and name.

Not specifying -p will result in the default profile.
Specifying "-p" with no argument will result in no profile.

Example:
lxc launch ubuntu:16.04 u1`)
}

func (c *launchCmd) flags() {
	c.init = initCmd{}

	c.init.massage_args()
	gnuflag.Var(&c.init.confArgs, "config", i18n.G("Config key/value to apply to the new container"))
	gnuflag.Var(&c.init.confArgs, "c", i18n.G("Config key/value to apply to the new container"))
	gnuflag.Var(&c.init.profArgs, "profile", i18n.G("Profile to apply to the new container"))
	gnuflag.Var(&c.init.profArgs, "p", i18n.G("Profile to apply to the new container"))
	gnuflag.BoolVar(&c.init.ephem, "ephemeral", false, i18n.G("Ephemeral container"))
	gnuflag.BoolVar(&c.init.ephem, "e", false, i18n.G("Ephemeral container"))
	gnuflag.StringVar(&c.init.network, "network", "", i18n.G("Network name"))
	gnuflag.StringVar(&c.init.network, "n", "", i18n.G("Network name"))
}

func (c *launchCmd) run(config *lxd.Config, args []string) error {
	if len(args) > 2 || len(args) < 1 {
		return errArgs
	}

	iremote, image := config.ParseRemoteAndContainer(args[0])

	var name string
	var remote string
	if len(args) == 2 {
		remote, name = config.ParseRemoteAndContainer(args[1])
	} else {
		remote, name = config.ParseRemoteAndContainer("")
	}

	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	/*
	 * initRequestedEmptyProfiles means user requested empty
	 * !initRequestedEmptyProfiles but len(profArgs) == 0 means use profile default
	 */
	var resp *lxd.Response
	profiles := []string{}
	for _, p := range c.init.profArgs {
		profiles = append(profiles, p)
	}

	iremote, image = c.init.guessImage(config, d, remote, iremote, image)

	devicesMap := map[string]shared.Device{}
	if c.init.network != "" {
		network, err := d.NetworkGet(c.init.network)
		if err != nil {
			return err
		}

		if network.Type == "bridge" {
			devicesMap[c.init.network] = shared.Device{"type": "nic", "nictype": "bridge", "parent": c.init.network}
		} else {
			devicesMap[c.init.network] = shared.Device{"type": "nic", "nictype": "macvlan", "parent": c.init.network}
		}
	}

	if !initRequestedEmptyProfiles && len(profiles) == 0 {
		resp, err = d.Init(name, iremote, image, nil, configMap, devicesMap, c.init.ephem)
	} else {
		resp, err = d.Init(name, iremote, image, &profiles, configMap, devicesMap, c.init.ephem)
	}

	if err != nil {
		return err
	}

	c.init.initProgressTracker(d, resp.Operation)

	if name == "" {
		op, err := resp.MetadataAsOperation()
		if err != nil {
			return fmt.Errorf(i18n.G("didn't get any affected image, container or snapshot from server"))
		}

		containers, ok := op.Resources["containers"]
		if !ok || len(containers) == 0 {
			return fmt.Errorf(i18n.G("didn't get any affected image, container or snapshot from server"))
		}

		var version string
		toScan := strings.Replace(containers[0], "/", " ", -1)
		count, err := fmt.Sscanf(toScan, " %s containers %s", &version, &name)
		if err != nil {
			return err
		}

		if count != 2 {
			return fmt.Errorf(i18n.G("bad number of things scanned from image, container or snapshot"))
		}

		if version != shared.APIVersion {
			return fmt.Errorf(i18n.G("got bad version"))
		}
	}
	fmt.Printf(i18n.G("Creating %s")+"\n", name)

	if err = d.WaitForSuccess(resp.Operation); err != nil {
		return err
	}

	c.init.checkNetwork(d, name)

	fmt.Printf(i18n.G("Starting %s")+"\n", name)
	resp, err = d.Action(name, shared.Start, -1, false, false)
	if err != nil {
		return err
	}

	err = d.WaitForSuccess(resp.Operation)
	if err != nil {
		prettyName := name
		if remote != "" {
			prettyName = fmt.Sprintf("%s:%s", remote, name)
		}

		return fmt.Errorf("%s\n"+i18n.G("Try `lxc info --show-log %s` for more info"), err, prettyName)
	}

	return nil
}
