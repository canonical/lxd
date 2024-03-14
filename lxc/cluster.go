package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	yaml "gopkg.in/yaml.v2"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
	"github.com/canonical/lxd/shared/termios"
)

type cmdCluster struct {
	global *cmdGlobal
}

func (c *cmdCluster) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("cluster")
	cmd.Short = i18n.G("Manage cluster members")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage cluster members`))

	// List
	clusterListCmd := cmdClusterList{global: c.global, cluster: c}
	cmd.AddCommand(clusterListCmd.command())

	// Rename
	clusterRenameCmd := cmdClusterRename{global: c.global, cluster: c}
	cmd.AddCommand(clusterRenameCmd.command())

	// Remove
	clusterRemoveCmd := cmdClusterRemove{global: c.global, cluster: c}
	cmd.AddCommand(clusterRemoveCmd.command())

	// Show
	clusterShowCmd := cmdClusterShow{global: c.global, cluster: c}
	cmd.AddCommand(clusterShowCmd.command())

	// Info
	clusterInfoCmd := cmdClusterInfo{global: c.global, cluster: c}
	cmd.AddCommand(clusterInfoCmd.command())

	// Get
	clusterGetCmd := cmdClusterGet{global: c.global, cluster: c}
	cmd.AddCommand(clusterGetCmd.command())

	// Set
	clusterSetCmd := cmdClusterSet{global: c.global, cluster: c}
	cmd.AddCommand(clusterSetCmd.command())

	// Unset
	clusterUnsetCmd := cmdClusterUnset{global: c.global, cluster: c, clusterSet: &clusterSetCmd}
	cmd.AddCommand(clusterUnsetCmd.command())

	// Enable
	clusterEnableCmd := cmdClusterEnable{global: c.global, cluster: c}
	cmd.AddCommand(clusterEnableCmd.command())

	// Edit
	clusterEditCmd := cmdClusterEdit{global: c.global, cluster: c}
	cmd.AddCommand(clusterEditCmd.command())

	// Add token
	cmdClusterAdd := cmdClusterAdd{global: c.global, cluster: c}
	cmd.AddCommand(cmdClusterAdd.command())

	// List tokens
	cmdClusterListTokens := cmdClusterListTokens{global: c.global, cluster: c}
	cmd.AddCommand(cmdClusterListTokens.command())

	// Revoke tokens
	cmdClusterRevokeToken := cmdClusterRevokeToken{global: c.global, cluster: c}
	cmd.AddCommand(cmdClusterRevokeToken.command())

	// Update certificate
	cmdClusterUpdateCertificate := cmdClusterUpdateCertificate{global: c.global, cluster: c}
	cmd.AddCommand(cmdClusterUpdateCertificate.command())

	// Evacuate cluster member
	cmdClusterEvacuate := cmdClusterEvacuate{global: c.global, cluster: c}
	cmd.AddCommand(cmdClusterEvacuate.command())

	// Restore cluster member
	cmdClusterRestore := cmdClusterRestore{global: c.global, cluster: c}
	cmd.AddCommand(cmdClusterRestore.command())

	clusterGroupCmd := cmdClusterGroup{global: c.global, cluster: c}
	cmd.AddCommand(clusterGroupCmd.command())

	clusterRoleCmd := cmdClusterRole{global: c.global, cluster: c}
	cmd.AddCommand(clusterRoleCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }

	return cmd
}

// List.
type cmdClusterList struct {
	global  *cmdGlobal
	cluster *cmdCluster

	flagFormat string
}

func (c *cmdClusterList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List all the cluster members")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List all the cluster members`))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterList) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("LXD server isn't part of a cluster"))
	}

	// Get the cluster members
	members, err := resource.server.GetClusterMembers()
	if err != nil {
		return err
	}

	// Render the table
	data := [][]string{}
	for _, member := range members {
		roles := member.Roles
		rolesDelimiter := "\n"
		if c.flagFormat == "csv" {
			rolesDelimiter = ","
		}

		line := []string{member.ServerName, member.URL, strings.Join(roles, rolesDelimiter), member.Architecture, member.FailureDomain, member.Description, strings.ToUpper(member.Status), member.Message}
		data = append(data, line)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		i18n.G("NAME"),
		i18n.G("URL"),
		i18n.G("ROLES"),
		i18n.G("ARCHITECTURE"),
		i18n.G("FAILURE DOMAIN"),
		i18n.G("DESCRIPTION"),
		i18n.G("STATE"),
		i18n.G("MESSAGE"),
	}

	return cli.RenderTable(c.flagFormat, header, data, members)
}

// Show.
type cmdClusterShow struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

func (c *cmdClusterShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<member>"))
	cmd.Short = i18n.G("Show details of a cluster member")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show details of a cluster member`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterShow) run(cmd *cobra.Command, args []string) error {
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

// Info.
type cmdClusterInfo struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

func (c *cmdClusterInfo) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("info", i18n.G("[<remote>:]<member>"))
	cmd.Short = i18n.G("Show useful information about a cluster member")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show useful information about a cluster member`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterInfo) run(cmd *cobra.Command, args []string) error {
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

	// Get the member state information.
	member, _, err := resource.server.GetClusterMemberState(resource.name)
	if err != nil {
		return err
	}

	// Render as YAML.
	data, err := yaml.Marshal(&member)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)
	return nil
}

// Get.
type cmdClusterGet struct {
	global  *cmdGlobal
	cluster *cmdCluster

	flagIsProperty bool
}

func (c *cmdClusterGet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get", i18n.G("[<remote>:]<member> <key>"))
	cmd.Short = i18n.G("Get values for cluster member configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), cmd.Short)

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a cluster property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpClusterMemberConfigs(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterGet) run(cmd *cobra.Command, args []string) error {
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

	// Get the member information
	member, _, err := resource.server.GetClusterMember(resource.name)
	if err != nil {
		return err
	}

	if c.flagIsProperty {
		w := member.Writable()
		res, err := getFieldByJsonTag(&w, args[1])
		if err != nil {
			return fmt.Errorf(i18n.G("The property %q does not exist on the cluster member %q: %v"), args[1], resource.name, err)
		}

		fmt.Printf("%v\n", res)
		return nil
	}

	value, ok := member.Config[args[1]]
	if !ok {
		return fmt.Errorf(i18n.G("The key %q does not exist on cluster member %q"), args[1], resource.name)
	}

	fmt.Printf("%s\n", value)
	return nil
}

// Set.
type cmdClusterSet struct {
	global  *cmdGlobal
	cluster *cmdCluster

	flagIsProperty bool
}

func (c *cmdClusterSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set", i18n.G("[<remote>:]<member> <key>=<value>..."))
	cmd.Short = i18n.G("Set a cluster member's configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), cmd.Short)

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a cluster property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterSet) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, -1)
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

	// Get the new config keys
	keys, err := getConfig(args[1:]...)
	if err != nil {
		return err
	}

	writable := member.Writable()
	if c.flagIsProperty {
		if cmd.Name() == "unset" {
			for k := range keys {
				err := unsetFieldByJsonTag(&writable, k)
				if err != nil {
					return fmt.Errorf(i18n.G("Error unsetting property: %v"), err)
				}
			}
		} else {
			err := unpackKVToWritable(&writable, keys)
			if err != nil {
				return fmt.Errorf(i18n.G("Error setting properties: %v"), err)
			}
		}
	} else {
		for k, v := range keys {
			writable.Config[k] = v
		}
	}

	return resource.server.UpdateClusterMember(resource.name, writable, "")
}

// Unset.
type cmdClusterUnset struct {
	global     *cmdGlobal
	cluster    *cmdCluster
	clusterSet *cmdClusterSet

	flagIsProperty bool
}

func (c *cmdClusterUnset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset", i18n.G("[<remote>:]<member> <key>"))
	cmd.Short = i18n.G("Unset a cluster member's configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), cmd.Short)

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a cluster property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpClusterMemberConfigs(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterUnset) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	c.clusterSet.flagIsProperty = c.flagIsProperty

	args = append(args, "")
	return c.clusterSet.run(cmd, args)
}

// Rename.
type cmdClusterRename struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

func (c *cmdClusterRename) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rename", i18n.G("[<remote>:]<member> <new-name>"))
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename a cluster member")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Rename a cluster member`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterRename) run(cmd *cobra.Command, args []string) error {
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
	err = resource.server.RenameClusterMember(resource.name, api.ClusterMemberPost{ServerName: args[1]})
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Member %s renamed to %s")+"\n", resource.name, args[1])
	}

	return nil
}

// Remove.
type cmdClusterRemove struct {
	global  *cmdGlobal
	cluster *cmdCluster

	flagForce          bool
	flagNonInteractive bool
}

func (c *cmdClusterRemove) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("remove", i18n.G("[<remote>:]<member>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Remove a member from the cluster")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Remove a member from the cluster`))

	cmd.RunE = c.run
	cmd.Flags().BoolVarP(&c.flagForce, "force", "f", false, i18n.G("Force removing a member, even if degraded"))
	cmd.Flags().BoolVar(&c.flagNonInteractive, "yes", false, i18n.G("Don't require user confirmation for using --force"))

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterRemove) promptConfirmation(name string) error {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf(i18n.G(`Forcefully removing a server from the cluster should only be done as a last
resort.

The removed server will not be functional after this action and will require a
full reset of LXD, losing any remaining instance, image or storage volume
that the server may have held.

When possible, a graceful removal should be preferred, this will require you to
move any affected instance, image or storage volume to another server prior to
the server being cleanly removed from the cluster.

The --force flag should only be used if the server has died, been reinstalled
or is otherwise never expected to come back up.

Are you really sure you want to force removing %s? (yes/no): `), name)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSuffix(input, "\n")

	if !shared.ValueInSlice(strings.ToLower(input), []string{i18n.G("yes")}) {
		return errors.New(i18n.G("User aborted delete operation"))
	}

	return nil
}

func (c *cmdClusterRemove) run(cmd *cobra.Command, args []string) error {
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

	// Prompt for confirmation if --force is used.
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

// Enable.
type cmdClusterEnable struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

func (c *cmdClusterEnable) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("enable", i18n.G("[<remote>:] <name>"))
	cmd.Short = i18n.G("Enable clustering on a single non-clustered LXD server")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Enable clustering on a single non-clustered LXD server

  This command turns a non-clustered LXD server into the first member of a new
  LXD cluster, which will have the given name.

  It's required that the LXD is already available on the network. You can check
  that by running 'lxc config get core.https_address', and possibly set a value
  for the address if not yet set.`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterEnable) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
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

	// Check if the LXD server is available on the network.
	server, _, err := resource.server.GetServer()
	if err != nil {
		return fmt.Errorf("Failed to retrieve current server config: %w", err)
	}

	if server.Config["core.https_address"] == "" && server.Config["cluster.https_address"] == "" {
		return errors.New(i18n.G("This LXD server is not available on the network"))
	}

	// Check if already enabled
	currentCluster, etag, err := resource.server.GetCluster()
	if err != nil {
		return fmt.Errorf("Failed to retrieve current cluster config: %w", err)
	}

	if currentCluster.Enabled {
		return errors.New(i18n.G("This LXD server is already clustered"))
	}

	// Enable clustering.
	req := api.ClusterPut{}
	req.ServerName = name
	req.Enabled = true
	op, err := resource.server.UpdateCluster(req, etag)
	if err != nil {
		return fmt.Errorf("Failed to configure cluster: %w", err)
	}

	err = op.Wait()
	if err != nil {
		return fmt.Errorf("Failed to configure cluster: %w", err)
	}

	fmt.Println(i18n.G("Clustering enabled"))
	return nil
}

// Edit.
type cmdClusterEdit struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

func (c *cmdClusterEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<member>"))
	cmd.Short = i18n.G("Edit cluster member configurations as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit cluster member configurations as YAML`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc cluster edit <cluster member> < member.yaml
    Update a cluster member using the content of member.yaml`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterEdit) helpTemplate() string {
	return i18n.G(
		`### This is a yaml representation of the cluster member.
### Any line starting with a '# will be ignored.`)
}

func (c *cmdClusterEdit) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing cluster member name"))
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.ClusterMemberPut{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}

		return resource.server.UpdateClusterMember(resource.name, newdata, "")
	}

	// Extract the current value
	member, etag, err := resource.server.GetClusterMember(resource.name)
	if err != nil {
		return err
	}

	memberWritable := member.Writable()

	data, err := yaml.Marshal(&memberWritable)
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
		newdata := api.ClusterMemberPut{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = resource.server.UpdateClusterMember(resource.name, newdata, etag)
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

// Add.
type cmdClusterAdd struct {
	global  *cmdGlobal
	cluster *cmdCluster

	flagName string
}

func (c *cmdClusterAdd) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("add", i18n.G("[[<remote>:]<member>]"))
	cmd.Short = i18n.G("Request a join token for adding a cluster member")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`Request a join token for adding a cluster member`))
	cmd.Flags().StringVar(&c.flagName, "name", "", i18n.G("Cluster member name (alternative to passing it as an argument)")+"``")

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterAdd) run(cmd *cobra.Command, args []string) error {
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

	// Determine the machine name.
	if resource.name != "" && c.flagName != "" && resource.name != c.flagName {
		return errors.New(i18n.G("Cluster member name was provided as both a flag and as an argument"))
	}

	if resource.name == "" {
		if c.flagName == "" {
			resource.name, err = c.global.asker.AskString(i18n.G("Please provide cluster member name: "), "", nil)
			if err != nil {
				return err
			}
		} else {
			resource.name = c.flagName
		}
	}

	// Request the join token.
	member := api.ClusterMembersPost{
		ServerName: resource.name,
	}

	op, err := resource.server.CreateClusterMember(member)
	if err != nil {
		return err
	}

	opAPI := op.Get()
	joinToken, err := opAPI.ToClusterJoinToken()
	if err != nil {
		return fmt.Errorf("Failed converting token operation to join token: %w", err)
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Member %s join token:")+"\n", resource.name)
	}

	fmt.Println(joinToken.String())

	return nil
}

// List Tokens.
type cmdClusterListTokens struct {
	global  *cmdGlobal
	cluster *cmdCluster

	flagFormat string
}

func (c *cmdClusterListTokens) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list-tokens", i18n.G("[<remote>:]"))
	cmd.Short = i18n.G("List all active cluster member join tokens")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`List all active cluster member join tokens`))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterListTokens) run(cmd *cobra.Command, args []string) error {
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

	// Check if clustered.
	cluster, _, err := resource.server.GetCluster()
	if err != nil {
		return err
	}

	if !cluster.Enabled {
		return errors.New(i18n.G("LXD server isn't part of a cluster"))
	}

	// Get the cluster member join tokens. Use default project as join tokens are created in default project.
	ops, err := resource.server.UseProject("default").GetOperations()
	if err != nil {
		return err
	}

	// Convert the join token operation into encoded form for display.
	type displayToken struct {
		ServerName string
		Token      string
		ExpiresAt  string
	}

	displayTokens := make([]displayToken, 0)

	for _, op := range ops {
		if op.Class != api.OperationClassToken {
			continue
		}

		if op.StatusCode != api.Running {
			continue // Tokens are single use, so if cancelled but not deleted yet its not available.
		}

		joinToken, err := op.ToClusterJoinToken()
		if err != nil {
			continue // Operation is not a valid cluster member join token operation.
		}

		displayTokens = append(displayTokens, displayToken{
			ServerName: joinToken.ServerName,
			Token:      joinToken.String(),
			ExpiresAt:  joinToken.ExpiresAt.Format("2006/01/02 15:04 MST"),
		})
	}

	// Render the table.
	data := [][]string{}
	for _, token := range displayTokens {
		line := []string{token.ServerName, token.Token, token.ExpiresAt}
		data = append(data, line)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		i18n.G("NAME"),
		i18n.G("TOKEN"),
		i18n.G("EXPIRES AT"),
	}

	return cli.RenderTable(c.flagFormat, header, data, displayTokens)
}

// Revoke Tokens.
type cmdClusterRevokeToken struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

func (c *cmdClusterRevokeToken) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("revoke-token", i18n.G("[<remote>:]<member>"))
	cmd.Short = i18n.G("Revoke cluster member join token")
	cmd.Long = cli.FormatSection(i18n.G("Description"), cmd.Short)

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterRevokeToken) run(cmd *cobra.Command, args []string) error {
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

	// Check if clustered.
	cluster, _, err := resource.server.GetCluster()
	if err != nil {
		return err
	}

	if !cluster.Enabled {
		return errors.New(i18n.G("LXD server isn't part of a cluster"))
	}

	// Get the cluster member join tokens. Use default project as join tokens are created in default project.
	ops, err := resource.server.UseProject("default").GetOperations()
	if err != nil {
		return err
	}

	for _, op := range ops {
		if op.Class != api.OperationClassToken {
			continue
		}

		if op.StatusCode != api.Running {
			continue // Tokens are single use, so if cancelled but not deleted yet its not available.
		}

		joinToken, err := op.ToClusterJoinToken()
		if err != nil {
			continue // Operation is not a valid cluster member join token operation.
		}

		if joinToken.ServerName == resource.name {
			// Delete the operation
			err = resource.server.DeleteOperation(op.ID)
			if err != nil {
				return err
			}

			if !c.global.flagQuiet {
				fmt.Printf(i18n.G("Cluster join token for %s:%s deleted")+"\n", resource.remote, resource.name)
			}

			return nil
		}
	}

	return fmt.Errorf(i18n.G("No cluster join token for member %s on remote: %s"), resource.name, resource.remote)
}

// Update Certificates.
type cmdClusterUpdateCertificate struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

func (c *cmdClusterUpdateCertificate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("update-certificate", i18n.G("[<remote>:] <cert.crt> <cert.key>"))
	cmd.Aliases = []string{"update-cert"}
	cmd.Short = i18n.G("Update cluster certificate")
	cmd.Long = cli.FormatSection(i18n.G("Description"),
		i18n.G("Update cluster certificate with PEM certificate and key read from input files."))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		if len(args) == 1 {
			return nil, cobra.ShellCompDirectiveDefault
		}

		if len(args) == 2 {
			return nil, cobra.ShellCompDirectiveDefault
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterUpdateCertificate) run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	exit, err := c.global.CheckArgs(cmd, args, 2, 3)
	if exit {
		return err
	}

	// Parse remote
	remote := ""
	certFile := args[0]
	keyFile := args[1]
	if len(args) == 3 {
		remote = args[0]
		certFile = args[1]
		keyFile = args[2]
	}

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]

	// Check if clustered.
	cluster, _, err := resource.server.GetCluster()
	if err != nil {
		return err
	}

	if !cluster.Enabled {
		return errors.New(i18n.G("LXD server isn't part of a cluster"))
	}

	if !shared.PathExists(certFile) {
		return fmt.Errorf(i18n.G("Could not find certificate file path: %s"), certFile)
	}

	if !shared.PathExists(keyFile) {
		return fmt.Errorf(i18n.G("Could not find certificate key file path: %s"), keyFile)
	}

	cert, err := os.ReadFile(certFile)
	if err != nil {
		return fmt.Errorf(i18n.G("Could not read certificate file: %s with error: %v"), certFile, err)
	}

	key, err := os.ReadFile(keyFile)
	if err != nil {
		return fmt.Errorf(i18n.G("Could not read certificate key file: %s with error: %v"), keyFile, err)
	}

	certificates := api.ClusterCertificatePut{
		ClusterCertificate:    string(cert),
		ClusterCertificateKey: string(key),
	}

	err = resource.server.UpdateClusterCertificate(certificates, "")
	if err != nil {
		return err
	}

	certf := conf.ServerCertPath(resource.remote)
	if shared.PathExists(certf) {
		err = os.WriteFile(certf, cert, 0644)
		if err != nil {
			return fmt.Errorf(i18n.G("Could not write new remote certificate for remote '%s' with error: %v"), resource.remote, err)
		}
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Successfully updated cluster certificates for remote %s")+"\n", resource.remote)
	}

	return nil
}

type cmdClusterEvacuateAction struct {
	global *cmdGlobal

	flagAction string
	flagForce  bool
}

// Cluster member evacuation.
type cmdClusterEvacuate struct {
	global  *cmdGlobal
	cluster *cmdCluster
	action  *cmdClusterEvacuateAction
}

func (c *cmdClusterEvacuate) command() *cobra.Command {
	cmdAction := cmdClusterEvacuateAction{global: c.global}
	c.action = &cmdAction

	cmd := c.action.command("evacuate")
	cmd.Aliases = []string{"evac"}
	cmd.Use = usage("evacuate", i18n.G("[<remote>:]<member>"))
	cmd.Short = i18n.G("Evacuate cluster member")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`Evacuate cluster member`))

	cmd.Flags().BoolVar(&c.action.flagForce, "force", false, i18n.G(`Force evacuation without user confirmation`)+"``")
	cmd.Flags().StringVar(&c.action.flagAction, "action", "", i18n.G(`Force a particular evacuation action`)+"``")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Cluster member restore.
type cmdClusterRestore struct {
	global  *cmdGlobal
	cluster *cmdCluster
	action  *cmdClusterEvacuateAction
}

func (c *cmdClusterRestore) command() *cobra.Command {
	cmdAction := cmdClusterEvacuateAction{global: c.global}
	c.action = &cmdAction

	cmd := c.action.command("restore")
	cmd.Use = usage("restore", i18n.G("[<remote>:]<member>"))
	cmd.Short = i18n.G("Restore cluster member")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`Restore cluster member`))

	cmd.Flags().BoolVar(&c.action.flagForce, "force", false, i18n.G(`Force restoration without user confirmation`)+"``")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterMembers(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterEvacuateAction) command(action string) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.RunE = c.run

	return cmd
}

func (c *cmdClusterEvacuateAction) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return fmt.Errorf("Failed to parse servers: %w", err)
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing cluster member name"))
	}

	if !c.flagForce {
		evacuate, err := c.global.asker.AskBool(fmt.Sprintf(i18n.G("Are you sure you want to %s cluster member %q? (yes/no) [default=no]: "), cmd.Name(), resource.name), "no")
		if err != nil {
			return err
		}

		if !evacuate {
			return nil
		}
	}

	state := api.ClusterMemberStatePost{
		Action: cmd.Name(),
		Mode:   c.flagAction,
	}

	op, err := resource.server.UpdateClusterMemberState(resource.name, state)
	if err != nil {
		return fmt.Errorf("Failed to update cluster member state: %w", err)
	}

	var format string

	if cmd.Name() == "restore" {
		format = i18n.G("Restoring cluster member: %s")
	} else {
		format = i18n.G("Evacuating cluster member: %s")
	}

	progress := cli.ProgressRenderer{
		Format: format,
		Quiet:  c.global.flagQuiet,
	}

	_, err = op.AddHandler(progress.UpdateOp)
	if err != nil {
		progress.Done("")
		return err
	}

	err = op.Wait()
	if err != nil {
		progress.Done("")
		return err
	}

	progress.Done("")
	return nil
}
