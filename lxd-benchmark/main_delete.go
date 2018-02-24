package main

import (
	"github.com/spf13/cobra"

	"github.com/lxc/lxd/lxd-benchmark/benchmark"
)

type cmdDelete struct {
	cmd    *cobra.Command
	global *cmdGlobal
}

func (c *cmdDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "delete"
	cmd.Short = "Delete containers"
	cmd.RunE = c.Run

	c.cmd = cmd
	return cmd
}

func (c *cmdDelete) Run(cmd *cobra.Command, args []string) error {
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
	duration, err := benchmark.DeleteContainers(c.global.srv, containers, c.global.flagParallel)
	if err != nil {
		return err
	}

	// Run shared reporting and teardown code
	err = c.global.Teardown("delete", duration)
	if err != nil {
		return err
	}

	return nil
}
