package main

import (
	"fmt"

	"github.com/spf13/cobra"

	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/version"
)

type cmdVersion struct {
	global *cmdGlobal
}

// Command returns a Cobra command for "version" that shows the server version.
func (c *cmdVersion) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "version"
	cmd.Short = "Show the server version"
	cmd.Long = cli.FormatSection("Description",
		`Show the server version`)

	cmd.RunE = c.Run

	return cmd
}

// Run executes the "version" command, printing the server version to the console.
func (c *cmdVersion) Run(cmd *cobra.Command, args []string) error {
	fmt.Println(version.Version)

	return nil
}
