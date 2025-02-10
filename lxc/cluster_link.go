package main

import (
	"fmt"
	"net"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/shared/api"
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

	// Add
	clusterLinkAddCmd := cmdClusterLinkAdd{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterLinkAddCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// Add.
type cmdClusterLinkAdd struct {
	global  *cmdGlobal
	cluster *cmdCluster

	flagToken       string
	flagAddress     string
	flagIdentity    string
	flagDescription string
}

func (c *cmdClusterLinkAdd) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("add", i18n.G("<name> [--token <trust_token>] [--address <ip_address>] [--identity <identity_name>]"))
	cmd.Short = i18n.G("Add a cluster link")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Add a cluster link`))
	cmd.Flags().StringVarP(&c.flagToken, "token", "t", "", "Trust token to use when adding cluster link")
	cmd.Flags().StringVarP(&c.flagAddress, "address", "a", "", "Optional IP to override addresses inside token")
	cmd.Flags().StringVarP(&c.flagIdentity, "identity", "i", "", "Pending identity to use for cluster link")
	_ = cmd.MarkFlagRequired("token")
	_ = cmd.MarkFlagRequired("identity")
	cmd.Flags().StringVarP(&c.flagDescription, "description", "d", "", "Cluster link description")

	cmd.RunE = c.run

	return cmd
}

func (c *cmdClusterLinkAdd) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]
	client := resource.server

	clusterLink := api.ClusterLinkPost{
		Name:         args[0],
		TrustToken:   c.flagToken,
		IdentityName: c.flagIdentity,
	}

	if c.flagAddress != "" {
		if net.ParseIP(c.flagAddress) == nil {
			return fmt.Errorf(i18n.G("Invalid IP address: %s"), c.flagAddress)
		}

		clusterLink.Address = c.flagAddress
	}

	if c.flagDescription != "" {
		clusterLink.Description = c.flagDescription
	}

	if clusterLink.Config == nil {
		clusterLink.Config = map[string]string{}
	}

	err = client.AddClusterLink(args[0], clusterLink)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Cluster link %s added")+"\n", args[0])
	}

	return nil
}
