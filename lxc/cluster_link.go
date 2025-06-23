package main

import (
	"github.com/spf13/cobra"

	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
)

type cmdClusterLink struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

func (c *cmdClusterLink) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("link")
	cmd.Short = i18n.G("Manage cluster links")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage cluster links`))

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}
