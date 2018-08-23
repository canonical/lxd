package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lxc/lxd/shared/api"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
)

type cmdLaunch struct {
	global *cmdGlobal
	init   *cmdInit
}

func (c *cmdLaunch) Command() *cobra.Command {
	cmd := c.init.Command()
	cmd.Use = i18n.G("launch [<remote>:]<image> [<remote>:][<name>]")
	cmd.Short = i18n.G("Create and start containers from images")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Create and start containers from images`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc launch ubuntu:16.04 u1`))
	cmd.Hidden = false

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdLaunch) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 1, 2)
	if exit {
		return err
	}

	// Call the matching code from init
	d, name, err := c.init.create(conf, args)
	if err != nil {
		return err
	}

	// Get the remote
	var remote string
	if len(args) == 2 {
		remote, _, err = conf.ParseRemote(args[1])
		if err != nil {
			return err
		}
	} else {
		remote, _, err = conf.ParseRemote("")
		if err != nil {
			return err
		}
	}

	// Start the container
	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Starting %s")+"\n", name)
	}

	req := api.ContainerStatePut{
		Action:  "start",
		Timeout: -1,
	}

	op, err := d.UpdateContainerState(name, req, "")
	if err != nil {
		return err
	}

	err = op.Wait()
	if err != nil {
		prettyName := name
		if remote != "" {
			prettyName = fmt.Sprintf("%s:%s", remote, name)
		}

		return fmt.Errorf("%s\n"+i18n.G("Try `lxc info --show-log %s` for more info"), err, prettyName)
	}

	return nil
}
