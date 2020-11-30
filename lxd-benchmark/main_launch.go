package main

import (
	"github.com/spf13/cobra"

	"github.com/lxc/lxd/lxd-benchmark/benchmark"
)

type cmdLaunch struct {
	global *cmdGlobal
	init   *cmdInit

	flagFreeze bool
}

func (c *cmdLaunch) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "launch [[<remote>:]<image>]"
	cmd.Short = "Create and start containers"
	cmd.RunE = c.Run
	cmd.Flags().AddFlagSet(c.init.Command().Flags())
	cmd.Flags().BoolVarP(&c.flagFreeze, "freeze", "F", false, "Freeze the container right after start")

	return cmd
}

func (c *cmdLaunch) Run(cmd *cobra.Command, args []string) error {
	// Choose the image
	image := "ubuntu:"
	if len(args) > 0 {
		image = args[0]
	}

	// Run the test
	duration, err := benchmark.LaunchContainers(c.global.srv, c.init.flagCount, c.global.flagParallel, image, c.init.flagPrivileged, true, c.flagFreeze)
	if err != nil {
		return err
	}

	c.global.reportDuration = duration

	return nil
}
