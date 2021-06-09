package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lxc/lxd/lxc/utils"
	"github.com/lxc/lxd/shared/api"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
)

type cmdLaunch struct {
	global *cmdGlobal
	init   *cmdInit

	flagConsole string
}

func (c *cmdLaunch) Command() *cobra.Command {
	cmd := c.init.Command()
	cmd.Use = usage("launch", i18n.G("[<remote>:]<image> [<remote>:][<name>]"))
	cmd.Short = i18n.G("Create and start instances from images")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Create and start instances from images`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc launch ubuntu:18.04 u1

lxc launch ubuntu:18.04 u1 < config.yaml
    Create and start the instance with configuration from config.yaml`))
	cmd.Hidden = false

	cmd.RunE = c.Run

	cmd.Flags().StringVar(&c.flagConsole, "console", "", i18n.G("Immediately attach to the console")+"``")
	cmd.Flags().Lookup("console").NoOptDefVal = "console"

	return cmd
}

func (c *cmdLaunch) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
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

	// Start the instance
	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Starting %s")+"\n", name)
	}

	req := api.InstanceStatePut{
		Action:  "start",
		Timeout: -1,
	}

	op, err := d.UpdateInstanceState(name, req, "")
	if err != nil {
		return err
	}

	progress := utils.ProgressRenderer{
		Quiet: c.global.flagQuiet,
	}
	_, err = op.AddHandler(progress.UpdateOp)
	if err != nil {
		progress.Done("")
		return err
	}

	// Wait for operation to finish
	err = utils.CancelableWait(op, &progress)
	if err != nil {
		progress.Done("")
		prettyName := name
		if remote != "" {
			prettyName = fmt.Sprintf("%s:%s", remote, name)
		}

		return fmt.Errorf("%s\n"+i18n.G("Try `lxc info --show-log %s` for more info"), err, prettyName)
	}
	progress.Done("")

	// Handle console attach
	if c.flagConsole != "" {
		console := cmdConsole{}
		console.global = c.global
		console.flagType = c.flagConsole
		return console.Console(d, name)
	}

	return nil
}
