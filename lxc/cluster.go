package main

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	yaml "gopkg.in/yaml.v2"

	"github.com/lxc/lxd/lxc/utils"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
)

type cmdCluster struct {
	global *cmdGlobal
}

func (c *cmdCluster) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("cluster")
	cmd.Short = i18n.G("Manage cluster members")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage cluster members`))

	// List
	clusterListCmd := cmdClusterList{global: c.global, cluster: c}
	cmd.AddCommand(clusterListCmd.Command())

	// Rename
	clusterRenameCmd := cmdClusterRename{global: c.global, cluster: c}
	cmd.AddCommand(clusterRenameCmd.Command())

	// Remove
	clusterRemoveCmd := cmdClusterRemove{global: c.global, cluster: c}
	cmd.AddCommand(clusterRemoveCmd.Command())

	// Show
	clusterShowCmd := cmdClusterShow{global: c.global, cluster: c}
	cmd.AddCommand(clusterShowCmd.Command())

	// Enable
	clusterEnableCmd := cmdClusterEnable{global: c.global, cluster: c}
	cmd.AddCommand(clusterEnableCmd.Command())

	return cmd
}

// List
type cmdClusterList struct {
	global  *cmdGlobal
	cluster *cmdCluster

	flagFormat string
}

func (c *cmdClusterList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("list [<remote>:]")
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List all the cluster members")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List all the cluster members`))
	cmd.Flags().StringVar(&c.flagFormat, "format", "table", i18n.G("Format (csv|json|table|yaml)")+"``")

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdClusterList) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
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

	// Get the cluster members
	members, err := resource.server.GetClusterMembers()
	if err != nil {
		return err
	}

	// Render the table
	data := [][]string{}
	for _, member := range members {
		database := "NO"
		if member.Database {
			database = "YES"
		}
		line := []string{member.ServerName, member.URL, database, strings.ToUpper(member.Status), member.Message}
		data = append(data, line)
	}
	sort.Sort(byName(data))

	header := []string{
		i18n.G("NAME"),
		i18n.G("URL"),
		i18n.G("DATABASE"),
		i18n.G("STATE"),
		i18n.G("MESSAGE"),
	}

	return utils.RenderTable(c.flagFormat, header, data, members)
}

// Show
type cmdClusterShow struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

func (c *cmdClusterShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("show [<remote>:]<member>")
	cmd.Short = i18n.G("Show details of a cluster member")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show details of a cluster member`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdClusterShow) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
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

	// Get the member information
	member, _, err := resource.server.GetClusterMember(resource.name)
	if err != nil {
		return err
	}

	// Render as YAML
	data, err := yaml.Marshal(&member)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)
	return nil
}

// Rename
type cmdClusterRename struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

func (c *cmdClusterRename) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("rename [<remote>:]<member> <new-name>")
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename a cluster member")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Rename a cluster member`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdClusterRename) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
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
	err = resource.server.RenameClusterMember(resource.name, api.ClusterMemberPost{ServerName: args[1]})
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Member %s renamed to %s")+"\n", resource.name, args[1])
	}

	return nil
}

// Remove
type cmdClusterRemove struct {
	global  *cmdGlobal
	cluster *cmdCluster

	flagForce          bool
	flagNonInteractive bool
}

func (c *cmdClusterRemove) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("remove [<remote>:]<member>")
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Remove a member from the cluster")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Remove a member from the cluster`))

	cmd.RunE = c.Run
	cmd.Flags().BoolVarP(&c.flagForce, "force", "f", false, i18n.G("Force removing a member, even if degraded"))
	cmd.Flags().BoolVarP(&c.flagNonInteractive, "quiet", "q", false, i18n.G("Don't require user confirmation for using --force"))

	return cmd
}

func (c *cmdClusterRemove) promptConfirmation(name string) error {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf(i18n.G(`Forcefully removing a server from the cluster should only be done as a last
resort.

The removed server will not be functional after this action and will require a
full reset of LXD, losing any remaining container, image or storage volume
that the server may have held.

When possible, a graceful removal should be preferred, this will require you to
move any affected container, image or storage volume to another server prior to
the server being cleanly removed from the cluster.

The --force flag should only be used if the server has died, been reinstalled
or is otherwise never expected to come back up.

Are you really sure you want to force removing %s? (yes/no): `), name)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSuffix(input, "\n")

	if !shared.StringInSlice(strings.ToLower(input), []string{i18n.G("yes")}) {
		return fmt.Errorf(i18n.G("User aborted delete operation"))
	}

	return nil
}

func (c *cmdClusterRemove) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
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

	// Prompt for confiromation if --force is used.
	if !c.flagNonInteractive && c.flagForce {
		err := c.promptConfirmation(resource.name)
		if err != nil {
			return err
		}
	}

	// Delete the cluster member
	err = resource.server.DeleteClusterMember(resource.name, c.flagForce)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Member %s removed")+"\n", resource.name)
	}

	return nil
}

// Enable
type cmdClusterEnable struct {
	global  *cmdGlobal
	cluster *cmdCluster

	flagForce bool
}

func (c *cmdClusterEnable) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("enable [<remote>:] <name>")
	cmd.Short = i18n.G("Enable clustering on a single non-clustered LXD instance")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Enable clustering on a single non-clustered LXD instance

  This command turns a non-clustered LXD instance into the first member of a new
  LXD cluster, which will have the given name.

  It's required that the LXD is already available on the network. You can check
  that by running 'lxc config get core.https_address', and possibly set a value
  for the address if not yet set.`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdClusterEnable) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 1, 2)
	if exit {
		return err
	}

	// Parse remote
	remote := ""
	name := args[0]
	if len(args) == 2 {
		remote = args[0]
		name = args[1]
	}

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]

	// Check if the LXD instance is available on the network.
	server, _, err := resource.server.GetServer()
	if err != nil {
		return errors.Wrap(err, "Failed to retrieve current server config")
	}

	if server.Config["core.https_address"] == "" {
		return fmt.Errorf("This LXD instance is not available on the network")
	}

	// Check if already enabled
	currentCluster, etag, err := resource.server.GetCluster()
	if err != nil {
		return errors.Wrap(err, "Failed to retrieve current cluster config")
	}

	if currentCluster.Enabled {
		return fmt.Errorf("This LXD instance is already clustered")
	}

	// Enable clustering.
	req := api.ClusterPut{}
	req.ServerName = name
	req.Enabled = true
	op, err := resource.server.UpdateCluster(req, etag)
	if err != nil {
		return errors.Wrap(err, "Failed to configure cluster")
	}

	err = op.Wait()
	if err != nil {
		return errors.Wrap(err, "Failed to configure cluster")
	}

	fmt.Println(i18n.G("Clustering enabled"))
	return nil
}
