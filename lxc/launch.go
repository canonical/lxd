package main

import (
	"fmt"
	"strings"

	"github.com/chai2010/gettext-go/gettext"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/gnuflag"
)

type launchCmd struct{}

func (c *launchCmd) showByDefault() bool {
	return true
}

func (c *launchCmd) usage() string {
	return gettext.Gettext(
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
	gnuflag.Var(&confArgs, "config", gettext.Gettext("Config key/value to apply to the new container"))
	gnuflag.Var(&confArgs, "c", gettext.Gettext("Config key/value to apply to the new container"))
	gnuflag.Var(&profArgs, "profile", gettext.Gettext("Profile to apply to the new container"))
	gnuflag.Var(&profArgs, "p", gettext.Gettext("Profile to apply to the new container"))
	gnuflag.BoolVar(&ephem, "ephemeral", false, gettext.Gettext("Ephemeral container"))
	gnuflag.BoolVar(&ephem, "e", false, gettext.Gettext("Ephemeral container"))
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

	if name == "" {
		if resp.Resources == nil {
			return fmt.Errorf(gettext.Gettext("didn't get any affected image, container or snapshot from server"))
		}

		containers, ok := resp.Resources["containers"]
		if !ok || len(containers) == 0 {
			return fmt.Errorf(gettext.Gettext("didn't get any affected image, container or snapshot from server"))
		}

		var version string
		toScan := strings.Replace(containers[0], "/", " ", -1)
		count, err := fmt.Sscanf(toScan, " %s containers %s", &version, &name)
		if err != nil {
			return err
		}

		if count != 2 {
			return fmt.Errorf(gettext.Gettext("bad number of things scanned from image, container or snapshot"))
		}

		if version != shared.APIVersion {
			return fmt.Errorf(gettext.Gettext("got bad version"))
		}
	}
	fmt.Printf(gettext.Gettext("Creating %s")+" ", name)

	if err = d.WaitForSuccess(resp.Operation); err != nil {
		return err
	}
	fmt.Println(gettext.Gettext("done."))

	fmt.Printf(gettext.Gettext("Starting %s")+" ", name)
	resp, err = d.Action(name, shared.Start, -1, false)
	if err != nil {
		return err
	}

	err = d.WaitForSuccess(resp.Operation)
	if err != nil {
		fmt.Println(gettext.Gettext("error."))
	} else {
		fmt.Println(gettext.Gettext("done."))
	}

	return err
}
