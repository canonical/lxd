package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

type cmdImport struct {
	global *cmdGlobal
}

// Deprecated command now pointing to the "lxd recover" command.
func (c *cmdImport) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "import"
	cmd.Short = `Command has been replaced with "lxd recover"`
	cmd.Long = `Description:
  This command has been replaced with "lxd recover". Please use that instead.
`
	cmd.RunE = c.Run
	return cmd
}

// Redirects to "lxd recover" when invoked, as this function has been replaced.
func (c *cmdImport) Run(cmd *cobra.Command, args []string) error {
	return fmt.Errorf(`Command has been replaced with "lxd recover"`)
}
