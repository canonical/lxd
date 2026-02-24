package main

import (
	"github.com/spf13/cobra"

	cli "github.com/canonical/lxd/shared/cmd"
)

type cmdImageRegistry struct {
	global *cmdGlobal
	image  *cmdImage
}

func (c *cmdImageRegistry) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("registry")
	cmd.Short = "Manage image registries"
	cmd.Long = cli.FormatSection("Description", `Manage image registries`)

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}
