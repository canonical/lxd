package main

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
)

type cmdBitmap struct {
	global *cmdGlobal
}

func (c *cmdBitmap) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("bitmap", "[<remote>:]<instance> <bitmap>")
	cmd.Short = "Create a dirty bitmap on every block disk of an instance"
	cmd.Long = cli.FormatSection("Description", cmd.Short+`

One bitmap of the given name is created on the root disk and on every non-shared block disk
device in a single call, so that all of them start recording at the same instant.
The instance must be a running virtual machine.

Bitmaps on the individual disks of an instance are managed through "lxc storage volume bitmap".`)
	cmd.Example = cli.FormatSection("", `lxc bitmap vm1 backup0
    Create a bitmap called "backup0" on every block disk of virtual machine "vm1".`)

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpTopLevelResource("instance", toComplete)
	}

	return cmd
}

func (c *cmdBitmap) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New("Missing instance name")
	}

	if args[1] == "" {
		return errors.New("Missing bitmap name")
	}

	bitmap := api.StorageVolumeBitmapsPost{
		Name: args[1],
	}

	op, err := resource.server.CreateInstanceBitmap(resource.name, bitmap)
	if err != nil {
		return err
	}

	return op.Wait()
}
