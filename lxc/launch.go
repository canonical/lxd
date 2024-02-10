package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
)

type cmdLaunch struct {
	global *cmdGlobal
	init   *cmdInit

	flagConsole string
}

func (c *cmdLaunch) command() *cobra.Command {
	cmd := c.init.command()
	cmd.Use = usage("launch", i18n.G("[<remote>:]<image> [<remote>:][<name>]"))
	cmd.Short = i18n.G("Create and start instances from images")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Create and start instances from images`))
	cmd.Example = cli.FormatSection("", i18n.G(`lxc launch ubuntu:24.04 u1
    Create and start a container

lxc launch ubuntu:24.04 u1 < config.yaml
    Create and start a container with configuration from config.yaml

lxc launch ubuntu:24.04 u2 -t aws:t2.micro
    Create and start a container using the same size as an AWS t2.micro (1 vCPU, 1GiB of RAM)

lxc launch ubuntu:24.04 v1 --vm -c limits.cpu=4 -c limits.memory=4GiB
    Create and start a virtual machine with 4 vCPUs and 4GiB of RAM

lxc launch ubuntu:24.04 v1 --vm -c limits.cpu=2 -c limits.memory=8GiB -d root,size=32GiB
    Create and start a virtual machine with 2 vCPUs, 8GiB of RAM and a root disk of 32GiB`))

	cmd.Hidden = false

	cmd.RunE = c.run

	cmd.Flags().StringVar(&c.flagConsole, "console", "", i18n.G("Immediately attach to the console")+"``")
	cmd.Flags().Lookup("console").NoOptDefVal = "console"

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpImages(toComplete)
	}

	return cmd
}

func (c *cmdLaunch) run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 2)
	if exit {
		return err
	}

	// Call the matching code from init
	d, name, err := c.init.create(conf, args, true)
	if err != nil {
		return err
	}

	// Check if the instance was started by the server.
	if d.HasExtension("instance_create_start") {
		return nil
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

	progress := cli.ProgressRenderer{
		Quiet: c.global.flagQuiet,
	}

	_, err = op.AddHandler(progress.UpdateOp)
	if err != nil {
		progress.Done("")
		return err
	}

	// Wait for operation to finish
	err = cli.CancelableWait(op, &progress)
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
		return console.runConsole(d, name)
	}

	return nil
}
