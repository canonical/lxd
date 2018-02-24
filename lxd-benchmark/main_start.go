package main

import (
	"github.com/spf13/cobra"

	"github.com/lxc/lxd/lxd-benchmark/benchmark"
)

type cmdStart struct {
	cmd    *cobra.Command
	global *cmdGlobal
}

func (c *cmdStart) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "start"
	cmd.Short = "Start containers"
	cmd.RunE = c.Run

	c.cmd = cmd
	return cmd
}

func (c *cmdStart) Run(cmd *cobra.Command, args []string) error {
	// Run shared setup code
	err := c.global.Setup()
	if err != nil {
		return err
	}

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

	// Run shared reporting and teardown code
	err = c.global.Teardown("start", duration)
	if err != nil {
		return err
	}

	return nil
}
