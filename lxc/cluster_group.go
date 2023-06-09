package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	yaml "gopkg.in/yaml.v2"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/termios"
)

type cmdClusterGroup struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

func (c *cmdClusterGroup) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("group")
	cmd.Short = i18n.G("Manage cluster groups")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage cluster groups`))

	// Assign
	clusterGroupAssignCmd := cmdClusterGroupAssign{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterGroupAssignCmd.Command())

	// Create
	clusterGroupCreateCmd := cmdClusterGroupCreate{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterGroupCreateCmd.Command())

	// Delete
	clusterGroupDeleteCmd := cmdClusterGroupDelete{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterGroupDeleteCmd.Command())

	// Edit
	clusterGroupEditCmd := cmdClusterGroupEdit{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterGroupEditCmd.Command())

	// List
	clusterGroupListCmd := cmdClusterGroupList{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterGroupListCmd.Command())

	// Remove
	clusterGroupRemoveCmd := cmdClusterGroupRemove{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterGroupRemoveCmd.Command())

	// Rename
	clusterGroupRenameCmd := cmdClusterGroupRename{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterGroupRenameCmd.Command())

	// Show
	clusterGroupShowCmd := cmdClusterGroupShow{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterGroupShowCmd.Command())

	return cmd
}

// Assign.
type cmdClusterGroupAssign struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

func (c *cmdClusterGroupAssign) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("assign", i18n.G("[<remote>:]<member> <group>"))
	cmd.Aliases = []string{"apply"}
	cmd.Short = i18n.G("Assign sets of groups to cluster members")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Assign sets of groups to cluster members`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc cluster group assign foo default,bar
    Set the groups for "foo" to "default" and "bar".

lxc cluster group assign foo default
    Reset "foo" to only using the "default" cluster group.`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdClusterGroupAssign) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	// Assign the cluster group
	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing cluster member name"))
	}

	member, etag, err := resource.server.GetClusterMember(resource.name)
	if err != nil {
		return err
	}

	if args[1] != "" {
		member.Groups = strings.Split(args[1], ",")
	} else {
		member.Groups = nil
	}

	err = resource.server.UpdateClusterMember(resource.name, member.Writable(), etag)
	if err != nil {
		return err
	}

	if args[1] == "" {
		args[1] = i18n.G("(none)")
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Cluster member %s added to cluster groups %s")+"\n", resource.name, args[1])
	}

	return nil
}

// Create.
type cmdClusterGroupCreate struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

func (c *cmdClusterGroupCreate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", i18n.G("[<remote>:]<group>"))
	cmd.Short = i18n.G("Create a cluster group")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Create a cluster group`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdClusterGroupCreate) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing cluster group name"))
	}

	// Create the cluster group
	group := api.ClusterGroupsPost{
		Name: resource.name,
	}

	err = resource.server.CreateClusterGroup(group)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Cluster group %s created")+"\n", resource.name)
	}

	return nil
}

// Delete.
type cmdClusterGroupDelete struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

func (c *cmdClusterGroupDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<group>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete a cluster group")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Delete a cluster group`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdClusterGroupDelete) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing cluster group name"))
	}

	// Delete the cluster group
	err = resource.server.DeleteClusterGroup(resource.name)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Cluster group %s deleted")+"\n", resource.name)
	}

	return nil
}

// Edit.
type cmdClusterGroupEdit struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

func (c *cmdClusterGroupEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<group>"))
	cmd.Short = i18n.G("Edit a cluster group")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit a cluster group`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdClusterGroupEdit) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing cluster group name"))
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.ClusterGroupPut{}

		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}

		return resource.server.UpdateClusterGroup(resource.name, newdata, "")
	}

	// Extract the current value
	group, etag, err := resource.server.GetClusterGroup(resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(group)
	if err != nil {
		return err
	}

	// Spawn the editor
	content, err := shared.TextEditor("", []byte(c.helpTemplate()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor
		newdata := api.ClusterGroupPut{}

		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = resource.server.UpdateClusterGroup(resource.name, newdata, etag)
		}

		// Respawn the editor
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.G("Config parsing error: %s")+"\n", err)
			fmt.Println(i18n.G("Press enter to open the editor again or ctrl+c to abort change"))

			_, err := os.Stdin.Read(make([]byte, 1))
			if err != nil {
				return err
			}

			content, err = shared.TextEditor("", content)
			if err != nil {
				return err
			}

			continue
		}

		break
	}

	return nil
}

func (c *cmdClusterGroupEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the cluster group.
### Any line starting with a '# will be ignored.`)
}

// List.
type cmdClusterGroupList struct {
	global  *cmdGlobal
	cluster *cmdCluster

	flagFormat string
}

func (c *cmdClusterGroupList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List all the cluster groups")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List all the cluster groups`))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdClusterGroupList) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	// Parse remote
	remote := ""
	if len(args) == 1 {
		remote = args[0]
	}

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]

	// Check if clustered
	cluster, _, err := resource.server.GetCluster()
	if err != nil {
		return err
	}

	if !cluster.Enabled {
		return fmt.Errorf(i18n.G("LXD server isn't part of a cluster"))
	}

	groups, err := resource.server.GetClusterGroups()
	if err != nil {
		return err
	}

	// Render the table
	data := [][]string{}
	for _, group := range groups {
		line := []string{group.Name, group.Description, fmt.Sprintf("%d", len(group.Members))}
		data = append(data, line)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		i18n.G("NAME"),
		i18n.G("DESCRIPTION"),
		i18n.G("MEMBERS"),
	}

	return cli.RenderTable(c.flagFormat, header, data, groups)
}

// Remove.
type cmdClusterGroupRemove struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

func (c *cmdClusterGroupRemove) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("remove", i18n.G("[<remote>:]<member> <group>"))
	cmd.Short = i18n.G("Remove member from group")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Remove a cluster member from a cluster group`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdClusterGroupRemove) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing cluster member name"))
	}

	// Remove the cluster group
	member, etag, err := resource.server.GetClusterMember(resource.name)
	if err != nil {
		return err
	}

	if !shared.StringInSlice(args[1], member.Groups) {
		return fmt.Errorf(i18n.G("Cluster group %s isn't currently applied to %s"), args[1], resource.name)
	}

	groups := []string{}
	for _, group := range member.Groups {
		if group == args[1] {
			continue
		}

		groups = append(groups, group)
	}

	member.Groups = groups

	err = resource.server.UpdateClusterMember(resource.name, member.Writable(), etag)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Cluster member %s removed from group %s")+"\n", resource.name, args[1])
	}

	return nil
}

// Rename.
type cmdClusterGroupRename struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

func (c *cmdClusterGroupRename) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rename", i18n.G("[<remote>:]<group> <new-name>"))
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename a cluster group")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Rename a cluster group`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdClusterGroupRename) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	// Perform the rename
	err = resource.server.RenameClusterGroup(resource.name, api.ClusterGroupPost{Name: args[1]})
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Cluster group %s renamed to %s")+"\n", resource.name, args[1])
	}

	return nil
}

// Show.
type cmdClusterGroupShow struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

func (c *cmdClusterGroupShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<group>"))
	cmd.Short = i18n.G("Show cluster group configurations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show cluster group configurations`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdClusterGroupShow) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing cluster group name"))
	}

	// Show the cluster group
	group, _, err := resource.server.GetClusterGroup(resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&group)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}
