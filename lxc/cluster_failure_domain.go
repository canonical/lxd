package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
)

type cmdClusterFailureDomain struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

func (c *cmdClusterFailureDomain) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("failure-domain")
	cmd.Short = i18n.G("Manage cluster member failure domains")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`Manage cluster member failure domains`))

	// Get
	clusterFailureDomainGetCmd := cmdClusterFailureDomainGet{global: c.global, cluster: c.cluster, clusterFailureDomain: c}
	cmd.AddCommand(clusterFailureDomainGetCmd.command())

	// Set
	clusterFailureDomainSetCmd := cmdClusterFailureDomainSet{global: c.global, cluster: c.cluster, clusterFailureDomain: c}
	cmd.AddCommand(clusterFailureDomainSetCmd.command())

	// Unset
	clusterFailureDomainUnsetCmd := cmdClusterFailureDomainUnset{global: c.global, cluster: c.cluster, clusterFailureDomain: c}
	cmd.AddCommand(clusterFailureDomainUnsetCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

type cmdClusterFailureDomainGet struct {
	global               *cmdGlobal
	cluster              *cmdCluster
	clusterFailureDomain *cmdClusterFailureDomain
}

func (c *cmdClusterFailureDomainGet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get", i18n.G("[<remote>:]<member>"))
	cmd.Short = i18n.G("Get the failure domain for a cluster member")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Get the failure domain for a cluster member`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("cluster_member", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterFailureDomainGet) run(cmd *cobra.Command, args []string) error {
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing cluster member name"))
	}

	member, _, err := resource.server.GetClusterMember(resource.name)
	if err != nil {
		return err
	}

	fmt.Println(member.FailureDomain)
	return nil
}

type cmdClusterFailureDomainSet struct {
	global               *cmdGlobal
	cluster              *cmdCluster
	clusterFailureDomain *cmdClusterFailureDomain
}

func (c *cmdClusterFailureDomainSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set", i18n.G("[<remote>:]<member> <domain>"))
	cmd.Short = i18n.G("Set the failure domain for a cluster member")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Set the failure domain for a cluster member`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("cluster_member", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterFailureDomainSet) run(cmd *cobra.Command, args []string) error {
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing cluster member name"))
	}

	member, etag, err := resource.server.GetClusterMember(resource.name)
	if err != nil {
		return err
	}

	memberWritable := member.Writable()
	memberWritable.FailureDomain = args[1]

	err = resource.server.UpdateClusterMember(resource.name, memberWritable, etag)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Failure domain set to %q for member %s")+"\n", args[1], resource.name)
	}

	return nil
}

type cmdClusterFailureDomainUnset struct {
	global               *cmdGlobal
	cluster              *cmdCluster
	clusterFailureDomain *cmdClusterFailureDomain
}

func (c *cmdClusterFailureDomainUnset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset", i18n.G("[<remote>:]<member>"))
	cmd.Short = i18n.G("Unset the failure domain for a cluster member")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Unset the failure domain for a cluster member (resets to "default")`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("cluster_member", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterFailureDomainUnset) run(cmd *cobra.Command, args []string) error {
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing cluster member name"))
	}

	member, etag, err := resource.server.GetClusterMember(resource.name)
	if err != nil {
		return err
	}

	memberWritable := member.Writable()
	memberWritable.FailureDomain = "default"

	err = resource.server.UpdateClusterMember(resource.name, memberWritable, etag)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Failure domain unset for member %s")+"\n", resource.name)
	}

	return nil
}
