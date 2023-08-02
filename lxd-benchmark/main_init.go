package main

import (
	"github.com/spf13/cobra"

	"github.com/canonical/lxd/lxd-benchmark/benchmark"
)

type cmdInit struct {
	global *cmdGlobal

	flagCount      int
	flagPrivileged bool
}

// Returns a cobra.Command for creating containers with specific flags and the defined 'Run' method.
func (c *cmdInit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "init [[<remote>:]<image>]"
	cmd.Short = "Create containers"
	cmd.RunE = c.Run
	cmd.Flags().IntVarP(&c.flagCount, "count", "C", 1, "Number of containers to create"+"``")
	cmd.Flags().BoolVar(&c.flagPrivileged, "privileged", false, "Use privileged containers")

	return cmd
}

// Launches specified number of containers and tracks the operation duration.
func (c *cmdInit) Run(cmd *cobra.Command, args []string) error {
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

	c.global.reportDuration = duration

	return nil
}
