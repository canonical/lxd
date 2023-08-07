package main

import (
	"github.com/spf13/cobra"

	"github.com/canonical/lxd/lxd-benchmark/benchmark"
)

type cmdStart struct {
	global *cmdGlobal
}

// Creates a 'start' command to initialize container startup operation.
func (c *cmdStart) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "start"
	cmd.Short = "Start containers"
	cmd.RunE = c.Run

	return cmd
}

// Executes the 'start' command to begin all retrieved containers in parallel.
func (c *cmdStart) Run(cmd *cobra.Command, args []string) error {
	// Get the containers
	containers, err := benchmark.GetContainers(c.global.srv)
	if err != nil {
		return err
	}

	// Run the test
	duration, err := benchmark.StartContainers(c.global.srv, containers, c.global.flagParallel)
	if err != nil {
		return err
	}

	c.global.reportDuration = duration

	return nil
}
