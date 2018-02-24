package main

import (
	"github.com/spf13/cobra"

	"github.com/lxc/lxd/lxd-benchmark/benchmark"
)

type cmdInit struct {
	cmd    *cobra.Command
	global *cmdGlobal

	flagCount      int
	flagPrivileged bool
}

func (c *cmdInit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "init [[<remote>:]<image>]"
	cmd.Short = "Create containers"
	cmd.RunE = c.Run
	cmd.Flags().IntVarP(&c.flagCount, "count", "C", 1, "Number of containers to create"+"``")
	cmd.Flags().BoolVar(&c.flagPrivileged, "privileged", false, "Use privileged containers")

	c.cmd = cmd
	return cmd
}

func (c *cmdInit) Run(cmd *cobra.Command, args []string) error {
	// Run shared setup code
	err := c.global.Setup()
	if err != nil {
		return err
	}

	// Choose the image
	image := "ubuntu:"
	if len(args) > 0 {
		image = args[0]
	}

	// Run the test
	duration, err := benchmark.LaunchContainers(c.global.srv, c.flagCount, c.global.flagParallel, image, c.flagPrivileged, false, false)
	if err != nil {
		return err
	}

	// Run shared reporting and teardown code
	err = c.global.Teardown("init", duration)
	if err != nil {
		return err
	}

	return nil
}
