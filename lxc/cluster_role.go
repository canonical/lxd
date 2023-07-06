package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lxc/lxd/shared"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
)

type cmdClusterRole struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

// Command configures the 'role' subcommand for managing cluster roles in a Cobra-based CLI application
// It provides functionality to add and remove cluster roles
func (c *cmdClusterRole) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("role")
	cmd.Short = i18n.G("Manage cluster roles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`Manage cluster roles`))

	// Add
	clusterRoleAddCmd := cmdClusterRoleAdd{global: c.global, cluster: c.cluster, clusterRole: c}
	cmd.AddCommand(clusterRoleAddCmd.Command())

	// Remove
	clusterRoleRemoveCmd := cmdClusterRoleRemove{global: c.global, cluster: c.cluster, clusterRole: c}
	cmd.AddCommand(clusterRoleRemoveCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

type cmdClusterRoleAdd struct {
	global      *cmdGlobal
	cluster     *cmdCluster
	clusterRole *cmdClusterRole
}

// Command configures the 'add' subcommand under 'role' for a Cobra-based CLI application
// It allows adding roles to a cluster member on the targeted remote server
func (c *cmdClusterRoleAdd) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("add", i18n.G("[<remote>:]<member> <role[,role...]>"))
	cmd.Short = i18n.G("Add roles to a cluster member")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Add roles to a cluster member`))

	cmd.RunE = c.Run

	return cmd
}

// Run executes the 'add' subcommand under 'role' for a Cobra-based CLI application
// It adds the specified roles to a cluster member on the targeted remote server
func (c *cmdClusterRoleAdd) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing cluster member name"))
	}

	// Extract the current value
	member, etag, err := resource.server.GetClusterMember(resource.name)
	if err != nil {
		return err
	}

	memberWritable := member.Writable()
	newRoles := shared.SplitNTrimSpace(args[1], ",", -1, false)
	for _, newRole := range newRoles {
		if shared.StringInSlice(newRole, memberWritable.Roles) {
			return fmt.Errorf(i18n.G("Member %q already has role %q"), resource.name, newRole)
		}
	}

	memberWritable.Roles = append(memberWritable.Roles, newRoles...)

	return resource.server.UpdateClusterMember(resource.name, memberWritable, etag)
}

type cmdClusterRoleRemove struct {
	global      *cmdGlobal
	cluster     *cmdCluster
	clusterRole *cmdClusterRole
}

// Command configures the 'remove' subcommand under 'role' for a Cobra-based CLI application
// It allows removing roles from a cluster member on the targeted remote server
func (c *cmdClusterRoleRemove) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("remove", i18n.G("[<remote>:]<member> <role[,role...]>"))
	cmd.Short = i18n.G("Remove roles from a cluster member")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Remove roles from a cluster member`))

	cmd.RunE = c.Run

	return cmd
}

// Run executes the 'remove' command under the 'role' subcommand
// It removes the specified roles from a cluster member on the targeted remote server
func (c *cmdClusterRoleRemove) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing cluster member name"))
	}

	// Extract the current value
	member, etag, err := resource.server.GetClusterMember(resource.name)
	if err != nil {
		return err
	}

	memberWritable := member.Writable()
	rolesToRemove := shared.SplitNTrimSpace(args[1], ",", -1, false)
	for _, roleToRemove := range rolesToRemove {
		if !shared.StringInSlice(roleToRemove, memberWritable.Roles) {
			return fmt.Errorf(i18n.G("Member %q does not have role %q"), resource.name, roleToRemove)
		}
	}

	memberWritable.Roles = shared.RemoveElementsFromStringSlice(memberWritable.Roles, rolesToRemove...)

	return resource.server.UpdateClusterMember(resource.name, memberWritable, etag)
}
