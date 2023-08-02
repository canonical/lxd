package main

import (
	"github.com/spf13/cobra"

	"github.com/canonical/lxd/lxd-benchmark/benchmark"
)

type cmdDelete struct {
	global *cmdGlobal
}

// Returns a cobra.Command for deleting containers using the defined 'Run' method.
func (c *cmdDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "delete"
	cmd.Short = "Delete containers"
	cmd.RunE = c.Run

	return cmd
}

// Executes the deletion of containers command and reports the operation duration.
func (c *cmdDelete) Run(cmd *cobra.Command, args []string) error {
	// Get the containers
	containers, err := benchmark.GetContainers(c.global.srv)
	if err != nil {
		return err
	}

	// Run the test
	duration, err := benchmark.DeleteContainers(c.global.srv, containers, c.global.flagParallel)
	if err != nil {
		return err
	}

	c.global.reportDuration = duration

	return nil
}
