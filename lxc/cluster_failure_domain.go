package main

import (
	"errors"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	cli "github.com/canonical/lxd/shared/cmd"
)

type cmdClusterFailureDomain struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

func (c *cmdClusterFailureDomain) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("failure-domain")
	cmd.Short = "Manage cluster member failure domains"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	// List
	clusterFailureDomainListCmd := cmdClusterFailureDomainList{global: c.global, cluster: c.cluster, clusterFailureDomain: c}
	cmd.AddCommand(clusterFailureDomainListCmd.command())

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
	cmd.Use = usage("get", "[<remote>:]<member>")
	cmd.Short = "Get the failure domain for a cluster member"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

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
		return errors.New("Missing cluster member name")
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
	cmd.Use = usage("set", "[<remote>:]<member> <domain>")
	cmd.Short = "Set the failure domain for a cluster member"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

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
		return errors.New("Missing cluster member name")
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
		fmt.Printf("Failure domain set to %q for member %s\n", args[1], resource.name)
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
	cmd.Use = usage("unset", "[<remote>:]<member>")
	cmd.Short = "Unset the failure domain for a cluster member"
	cmd.Long = cli.FormatSection("Description", cmd.Short+` (resets to "default")`)

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
		return errors.New("Missing cluster member name")
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
		fmt.Printf("Failure domain unset for member %s\n", resource.name)
	}

	return nil
}

type cmdClusterFailureDomainList struct {
	global               *cmdGlobal
	cluster              *cmdCluster
	clusterFailureDomain *cmdClusterFailureDomain

	flagFormat string
}

func (c *cmdClusterFailureDomainList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", "[<remote>:]")
	cmd.Aliases = []string{"ls"}
	cmd.Short = "List the cluster's known failure domains"
	cmd.Long = cli.FormatSection("Description", cmd.Short)
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", cli.FormatStringFlagLabel("Format (csv|json|table|yaml|compact)"))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(toComplete, ":", false, instanceServerRemoteCompletionFilters(*c.global.conf)...)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterFailureDomainList) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	// Parse remote.
	remote := ""
	if len(args) == 1 {
		remote = args[0]
	}

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]

	// Get the known failure domains.
	names, err := resource.server.GetClusterFailureDomains()
	if err != nil {
		return err
	}

	// Render the table.
	data := make([][]string, 0, len(names))
	for _, name := range names {
		data = append(data, []string{name})
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{"NAME"}

	return cli.RenderTable(c.flagFormat, header, data, names)
}
