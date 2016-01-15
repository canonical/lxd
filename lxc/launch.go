package main

import (
	"fmt"
	"strings"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/i18n"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/gnuflag"
)

type launchCmd struct{}

func (c *launchCmd) showByDefault() bool {
	return true
}

func (c *launchCmd) usage() string {
	return i18n.G(
		`Launch a container from a particular image.

lxc launch [remote:]<image> [remote:][<name>] [--ephemeral|-e] [--profile|-p <profile>...] [--config|-c <key=value>...]

Launches a container using the specified image and name.

Not specifying -p will result in the default profile.
Specifying "-p" with no argument will result in no profile.

Example:
lxc launch ubuntu u1`)
}

func (c *launchCmd) flags() {
	massage_args()
	gnuflag.Var(&confArgs, "config", i18n.G("Config key/value to apply to the new container"))
	gnuflag.Var(&confArgs, "c", i18n.G("Config key/value to apply to the new container"))
	gnuflag.Var(&profArgs, "profile", i18n.G("Profile to apply to the new container"))
	gnuflag.Var(&profArgs, "p", i18n.G("Profile to apply to the new container"))
	gnuflag.BoolVar(&ephem, "ephemeral", false, i18n.G("Ephemeral container"))
	gnuflag.BoolVar(&ephem, "e", false, i18n.G("Ephemeral container"))
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
	 * requested_empty_profiles means user requested empty
	 * !requested_empty_profiles but len(profArgs) == 0 means use profile default
	 */
	var resp *lxd.Response
	profiles := []string{}
	for _, p := range profArgs {
		profiles = append(profiles, p)
	}

	if !requested_empty_profiles && len(profiles) == 0 {
		resp, err = d.Init(name, iremote, image, nil, configMap, ephem)
	} else {
		resp, err = d.Init(name, iremote, image, &profiles, configMap, ephem)
	}

	if err != nil {
		return err
	}

	initProgressTracker(d, resp.Operation)

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

	fmt.Printf(i18n.G("Starting %s")+"\n", name)
	resp, err = d.Action(name, shared.Start, -1, false)
	if err != nil {
		return err
	}

	err = d.WaitForSuccess(resp.Operation)
	if err != nil {
		return fmt.Errorf("%s\n"+i18n.G("Try `lxc info --show-log %s` for more info"), err, name)
	}

	return nil
}
