package main

import (
	"fmt"
	"strings"

	"github.com/codegangsta/cli"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/i18n"
)

var commandLaunch = cli.Command{
	Name:      "launch",
	Usage:     i18n.G("Launch a container from a particular image."),
	ArgsUsage: i18n.G("[remote:]<image> [remote:][<name>] [--ephemeral|-e] [--profile|-p <profile>...]"),
	Description: i18n.G(`Launches a container using the specified image and name.

   Not specifying -p will result in the default profile.
   Specifying "-p" with no argument will result in no profile.`),

	Flags: []cli.Flag{
		cli.BoolFlag{
			Name:  "debug",
			Usage: i18n.G("Print debug information."),
		},

		cli.BoolFlag{
			Name:  "verbose",
			Usage: i18n.G("Print verbose information."),
		},

		cli.BoolFlag{
			Name:  "ephemeral, e",
			Usage: i18n.G("Ephemeral."),
		},

		cli.StringSliceFlag{
			Name:  "config, c",
			Usage: i18n.G("Config key/value to apply to the new container."),
		},

		cli.StringSliceFlag{
			Name:  "profile, p",
			Usage: i18n.G("Profile to apply to the new container."),
		},
	},

	Action: commandWrapper(commandActionLaunch),
}

func commandActionLaunch(config *lxd.Config, c *cli.Context) error {
	var cmd = &launchCmd{}
	cmd.init.confArgs = c.StringSlice("config")
	var profiles = c.StringSlice("profile")
	if len(profiles) == 1 && profiles[0] == "" {
		initRequestedEmptyProfiles = true
		profiles = []string{}
	}
	cmd.init.profArgs = profiles

	cmd.init.ephem = c.Bool("ephemeral")

	return cmd.run(config, c.Args())
}

type launchCmd struct {
	init initCmd
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

	if !initRequestedEmptyProfiles && len(profiles) == 0 {
		resp, err = d.Init(name, iremote, image, nil, configMap, c.init.ephem)
	} else {
		resp, err = d.Init(name, iremote, image, &profiles, configMap, c.init.ephem)
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

	fmt.Printf(i18n.G("Starting %s")+"\n", name)
	resp, err = d.Action(name, shared.Start, -1, false, false)
	if err != nil {
		return err
	}

	err = d.WaitForSuccess(resp.Operation)
	if err != nil {
		return fmt.Errorf("%s\n"+i18n.G("Try `lxc info --show-log %s` for more info"), err, name)
	}

	return nil
}
