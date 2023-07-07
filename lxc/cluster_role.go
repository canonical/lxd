package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/shared"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
)

type cmdClusterRole struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

// It uses the cmdGlobal, cmdCluster, and cmdClusterRole structs for context and operation.
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

// Setting up the usage, short description, and long description of the command, as well as its RunE method.
func (c *cmdClusterRoleAdd) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("add", i18n.G("[<remote>:]<member> <role[,role...]>"))
	cmd.Short = i18n.G("Add roles to a cluster member")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Add roles to a cluster member`))

	cmd.RunE = c.Run

	return cmd
}

// It checks and parses input arguments, verifies role assignment, and updates the member's roles.
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

// Removing the roles from a cluster member, setting up usage, descriptions, and the RunE method.
func (c *cmdClusterRoleRemove) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("remove", i18n.G("[<remote>:]<member> <role[,role...]>"))
	cmd.Short = i18n.G("Remove roles from a cluster member")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Remove roles from a cluster member`))

	cmd.RunE = c.Run

	return cmd
}

// Run executes the removal of specified roles from a cluster member, checking inputs, validating role assignment, and updating the member's roles.
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
