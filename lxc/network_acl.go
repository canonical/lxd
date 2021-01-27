package main

import (
	"github.com/spf13/cobra"

	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
)

type cmdNetworkACL struct {
	global *cmdGlobal
}

func (c *cmdNetworkACL) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("acl")
	cmd.Short = i18n.G("Manage network ACLs")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Manage network ACLs"))

	return cmd
}
