package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/termios"
	"github.com/canonical/lxd/shared/version"
)

type cmdClusterLink struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

func (c *cmdClusterLink) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("link")
	cmd.Short = "Manage cluster links"
	cmd.Long = cli.FormatSection("Description", `Manage cluster links`)

	// Create
	clusterLinkCreateCmd := cmdClusterLinkCreate{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterLinkCreateCmd.command())

	// List
	clusterLinkListCmd := cmdClusterLinkList{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterLinkListCmd.command())

	// Delete
	clusterLinkDeleteCmd := cmdClusterLinkDelete{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterLinkDeleteCmd.command())

	// Edit
	clusterLinkEditCmd := cmdClusterLinkEdit{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterLinkEditCmd.command())

	// Show
	clusterLinkShowCmd := cmdClusterLinkShow{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterLinkShowCmd.command())

	// Info
	clusterLinkInfoCmd := cmdClusterLinkInfo{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterLinkInfoCmd.command())

	// Get
	clusterLinkGetCmd := cmdClusterLinkGet{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterLinkGetCmd.command())

	// Set
	clusterLinkSetCmd := cmdClusterLinkSet{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterLinkSetCmd.command())

	// Unset
	clusterLinkUnsetCmd := cmdClusterLinkUnset{global: c.global, cluster: c.cluster, clusterLinkSet: &clusterLinkSetCmd}
	cmd.AddCommand(clusterLinkUnsetCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// Create.
type cmdClusterLinkCreate struct {
	global  *cmdGlobal
	cluster *cmdCluster

	flagToken       string
	flagAuthGroups  []string
	flagDescription string
}

func (c *cmdClusterLinkCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", "[<remote>:]<link> [key=value...]")
	cmd.Short = "Create cluster links"
	cmd.Long = cli.FormatSection("Description", `Create cluster links

When run with the --token flag, creates an active cluster link.
When run without a token, creates a pending cluster link that must be activated by creating a cluster link on the remote cluster.`)
	cmd.Example = cli.FormatSection("", `lxc cluster link create backup-cluster --auth-group backups
	Create a pending cluster link reachable at "10.0.0.0:8443" and "10.0.0.1:8443" called "backup-cluster", belonging to the authentication group "backups".

	lxc cluster link create main-cluster --token <token from backup-cluster> --auth-group backups
	Create a cluster link with "backup-cluster" called "main-cluster", belonging to the auth group "backups".

	lxc cluster link create recovery-cluster < config.yaml
	Create a pending cluster link with the configuration from "config.yaml" called "recovery-cluster".`)
	cmd.Flags().StringVarP(&c.flagToken, "token", "t", "", cli.FormatStringFlagLabel("Trust token to use when creating cluster link"))
	cmd.Flags().StringSliceVarP(&c.flagAuthGroups, "auth-group", "g", []string{}, cli.FormatStringFlagLabel("Authentication groups to add the newly created cluster link identity to"))
	cmd.Flags().StringVarP(&c.flagDescription, "description", "d", "", cli.FormatStringFlagLabel("Cluster link description"))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(toComplete, ":", true, instanceServerRemoteCompletionFilters(*c.global.conf)...)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterLinkCreate) run(cmd *cobra.Command, args []string) error {
	var stdinData api.ClusterLinkPut

	// Quick checks
	exit, err := c.global.CheckArgs(cmd, args, 1, -1)
	if exit {
		return err
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		err = yaml.Unmarshal(contents, &stdinData)
		if err != nil {
			return err
		}
	}

	// Parse remote
	remoteName, clusterLinkName, err := c.global.conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	transporter, wrapper := newLocationHeaderTransportWrapper()
	client, err := c.global.conf.GetInstanceServerWithConnectionArgs(remoteName, &lxd.ConnectionArgs{TransportWrapper: wrapper})
	if err != nil {
		return err
	}

	clusterLink := api.ClusterLinkPost{
		Name:           clusterLinkName,
		ClusterLinkPut: stdinData,
	}

	if c.flagDescription != "" {
		clusterLink.Description = c.flagDescription
	}

	if stdinData.Config == nil {
		clusterLink.Config = map[string]string{}
	}

	for i := 1; i < len(args); i++ {
		entry := strings.SplitN(args[i], "=", 2)
		if len(entry) < 2 {
			return fmt.Errorf("Bad key=value pair: %s", args[i])
		}

		clusterLink.Config[entry[0]] = entry[1]
	}

	// Set auth groups if provided
	if len(c.flagAuthGroups) > 0 {
		clusterLink.AuthGroups = c.flagAuthGroups
	}

	if c.flagToken == "" {
		token, err := client.CreateIdentityClusterLinkToken(clusterLink)
		if err != nil {
			return err
		}

		// Encode certificate add token to JSON.
		tokenJSON, err := json.Marshal(token)
		if err != nil {
			return fmt.Errorf("Failed encoding identity token: %w", err)
		}

		if !c.global.flagQuiet {
			fmt.Printf("Cluster link %s created (pending)"+"\n", clusterLinkName)

			pendingIdentityURL, err := url.Parse(transporter.location)
			if err != nil {
				return fmt.Errorf("Received invalid location header %q: %w", transporter.location, err)
			}

			var pendingIdentityUUIDStr string
			identityURLPrefix := api.NewURL().Path(version.APIVersion, "auth", "identities", api.AuthenticationMethodTLS).String()
			_, err = fmt.Sscanf(pendingIdentityURL.Path, identityURLPrefix+"/%s", &pendingIdentityUUIDStr)
			if err != nil {
				return fmt.Errorf("Received unexpected location header %q: %w", transporter.location, err)
			}

			pendingIdentityUUID, err := uuid.Parse(pendingIdentityUUIDStr)
			if err != nil {
				return fmt.Errorf("Received invalid pending identity UUID %q: %w", pendingIdentityUUIDStr, err)
			}

			fmt.Printf("Cluster link %q (%s) pending identity token:"+"\n", clusterLinkName, pendingIdentityUUID.String())
		}

		// Print the base64 encoded token.
		fmt.Println(base64.StdEncoding.EncodeToString(tokenJSON))
	} else {
		clusterLink.TrustToken = c.flagToken
		err = client.CreateClusterLink(clusterLink)
		if err != nil {
			return err
		}

		if !c.global.flagQuiet {
			fmt.Printf("Cluster link %s created"+"\n", clusterLinkName)
		}
	}

	return nil
}

// List.
type cmdClusterLinkList struct {
	global  *cmdGlobal
	cluster *cmdCluster

	flagFormat string
}

func (c *cmdClusterLinkList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", "[<remote>:]")
	cmd.Aliases = []string{"ls"}
	cmd.Short = "List cluster links"
	cmd.Long = cli.FormatSection("Description", `List cluster links`)

	cmd.RunE = c.run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", cli.FormatStringFlagLabel("Format (csv|json|table|yaml|compact)"))

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 1 {
			return c.global.cmpRemotes(toComplete, ":", true, instanceServerRemoteCompletionFilters(*c.global.conf)...)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterLinkList) run(cmd *cobra.Command, args []string) error {
	// Quick checks
	exit, err := c.global.CheckArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	// Parse remote
	remote := ""
	if len(args) > 0 {
		remote = args[0]
	}

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]
	client := resource.server

	clusterLinks, err := client.GetClusterLinks()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, clusterLink := range clusterLinks {
		clusterLinkIdentity, _, err := client.GetIdentity(api.AuthenticationMethodTLS, clusterLink.Name)
		if err != nil {
			return err
		}

		identityStatus := "ACTIVE"
		if clusterLinkIdentity.Type == api.IdentityTypeCertificateClusterLinkPending {
			identityStatus = "PENDING"
		}

		details := []string{
			clusterLink.Name,
			clusterLink.Config["volatile.addresses"],
			clusterLink.Description,
			identityStatus,
			strings.ToUpper(clusterLink.Type),
		}

		data = append(data, details)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		"NAME",
		"ADDRESSES",
		"DESCRIPTION",
		"IDENTITY STATUS",
		"TYPE",
	}

	return cli.RenderTable(c.flagFormat, header, data, clusterLinks)
}

// Delete.
type cmdClusterLinkDelete struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

func (c *cmdClusterLinkDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", "[<remote>:]<link>")
	cmd.Aliases = []string{"rm"}
	cmd.Short = "Delete cluster links"
	cmd.Long = cli.FormatSection("Description", `Delete cluster links`)

	cmd.Example = cli.FormatSection("", `On lxd01: lxc cluster link delete lxd02
	Delete cluster link lxd02 and its associated identity.

		On lxd02: lxc cluster link delete lxd01
	Delete cluster link lxd01 and its associated identity.`)

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("cluster_link", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterLinkDelete) run(cmd *cobra.Command, args []string) error {
	// Quick checks
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
	client := resource.server

	err = client.DeleteClusterLink(resource.name)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf("Cluster link %s deleted"+"\n", resource.name)
	}

	return nil
}

// Edit.
type cmdClusterLinkEdit struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

func (c *cmdClusterLinkEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", "[<remote>:]<link>")
	cmd.Short = "Edit cluster link configurations as YAML"
	cmd.Long = cli.FormatSection("Description", `Edit cluster link configurations as YAML`)
	cmd.Example = cli.FormatSection("", `lxc cluster link edit [<remote>:]<name> < link.yaml
    Update a cluster link using the content of link.yaml.`)

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("cluster_link", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterLinkEdit) helpTemplate() string {
	return `### This is a YAML representation of a cluster link.
### Any line starting with a '#' will be ignored.
###
### A cluster link consists of a set of configuration items.
###
### An example would look like:
### description: backup cluster
### config:
###   user.key: value
###   `
}

func (c *cmdClusterLinkEdit) run(cmd *cobra.Command, args []string) error {
	// Quick checks
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
		return errors.New("Missing cluster link name")
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.ClusterLinkPut{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}

		return resource.server.UpdateClusterLink(resource.name, newdata, "")
	}

	// Extract the current value
	clusterLink, etag, err := resource.server.GetClusterLink(resource.name)
	if err != nil {
		return err
	}

	// Get the writable fields of the cluster link (ClusterLinkPut)
	clusterLinkPut := clusterLink.Writable()

	data, err := yaml.Marshal(&clusterLinkPut)
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
		newdata := api.ClusterLinkPut{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = resource.server.UpdateClusterLink(resource.name, newdata, etag)
		}

		// Respawn the editor
		if err != nil {
			fmt.Fprintf(os.Stderr, "Config parsing error: %s"+"\n", err)
			fmt.Println("Press enter to open the editor again or ctrl+c to abort change")

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

	if !c.global.flagQuiet {
		fmt.Printf("Cluster link %s updated"+"\n", resource.name)
	}

	return nil
}

// Show.
type cmdClusterLinkShow struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

func (c *cmdClusterLinkShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", "[<remote>:]<link>")
	cmd.Short = "Show cluster link configurations"
	cmd.Long = cli.FormatSection("Description", `Show cluster link configurations`)
	cmd.Example = cli.FormatSection("", `lxc cluster link show backup-cluster
    Will show the properties of a cluster link called "backup-cluster".`)

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("cluster_link", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterLinkShow) run(cmd *cobra.Command, args []string) error {
	// Quick checks
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
		return errors.New("Missing cluster link name")
	}

	clusterLink, _, err := resource.server.GetClusterLink(resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&clusterLink)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Info.
type cmdClusterLinkInfo struct {
	global  *cmdGlobal
	cluster *cmdCluster

	flagTarget string
}

func (c *cmdClusterLinkInfo) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("info", "[<remote>:]<link>")
	cmd.Short = "Get information on cluster links"
	cmd.Long = cli.FormatSection("Description", `Get information on cluster links`)
	cmd.Example = cli.FormatSection("", `lxc cluster link info backup-cluster
    Will show information for a cluster link called "backup-cluster".`)

	cmd.RunE = c.run
	cmd.Flags().StringVar(&c.flagTarget, "target", "", cli.FormatStringFlagLabel("Cluster member name"))

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("cluster_link", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterLinkInfo) run(cmd *cobra.Command, args []string) error {
	// Quick checks
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
		return errors.New("Missing cluster link name")
	}

	srcServer := resource.server
	if c.flagTarget != "" {
		srcServer = srcServer.UseTarget(c.flagTarget)
	}

	clusterLink, _, err := srcServer.GetClusterLink(resource.name)
	if err != nil {
		return err
	}

	fmt.Printf("Name: %s"+"\n", clusterLink.Name)
	if clusterLink.Description != "" {
		fmt.Printf("Description: %s"+"\n", clusterLink.Description)
	}

	clusterLinkState, _, err := srcServer.GetClusterLinkState(resource.name)
	if err != nil {
		return err
	}

	fmt.Println("Cluster link members:")

	// Render the table.
	data := [][]string{}
	for _, member := range clusterLinkState.ClusterLinkMembersState {
		line := []string{member.Address, member.ServerName, strings.ToUpper(member.Status)}
		data = append(data, line)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		"ADDRESS",
		"NAME",
		"STATE",
	}

	return cli.RenderTable(cli.TableFormatTable, header, data, clusterLinkState.ClusterLinkMembersState)
}

// Get.
type cmdClusterLinkGet struct {
	global  *cmdGlobal
	cluster *cmdCluster

	flagIsProperty bool
}

func (c *cmdClusterLinkGet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get", "[<remote>:]<link> <key>")
	cmd.Short = "Get values for cluster link configuration keys"
	cmd.Long = cli.FormatSection("Description", `Get values for cluster link configuration keys`)

	cmd.RunE = c.run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, "Get the key as a cluster link property")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("cluster_link", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpClusterLinkConfig(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterLinkGet) run(cmd *cobra.Command, args []string) error {
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
		return errors.New("Missing cluster link name")
	}

	// Get the configuration key
	clusterLink, _, err := resource.server.GetClusterLink(resource.name)
	if err != nil {
		return err
	}

	if c.flagIsProperty {
		w := clusterLink.Writable()
		res, err := getFieldByJSONTag(&w, args[1])
		if err != nil {
			return fmt.Errorf("The property %q does not exist on the cluster link %q: %v", args[1], resource.name, err)
		}

		fmt.Printf("%v\n", res)
	} else {
		fmt.Printf("%s\n", clusterLink.Config[args[1]])
	}

	return nil
}

// Set.
type cmdClusterLinkSet struct {
	global  *cmdGlobal
	cluster *cmdCluster

	flagIsProperty bool
}

func (c *cmdClusterLinkSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set", "[<remote>:]<link> <key>=<value>...")
	cmd.Short = "Set cluster link configuration keys"
	cmd.Long = cli.FormatSection("Description", `Set cluster link configuration keys

For backward compatibility, a single configuration key may still be set with
lxc cluster link set [<remote>:]<link> <key> <value>`)

	cmd.RunE = c.run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, "Set the key as a cluster link property")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("cluster_link", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpClusterLinkConfig(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterLinkSet) run(cmd *cobra.Command, args []string) error {
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

	if resource.name == "" {
		return errors.New("Missing cluster link name")
	}

	// Get the cluster link
	clusterLink, etag, err := resource.server.GetClusterLink(resource.name)
	if err != nil {
		return err
	}

	// Set the configuration key
	keys, err := getConfig(args[1:]...)
	if err != nil {
		return err
	}

	writable := clusterLink.Writable()
	if c.flagIsProperty {
		if cmd.Name() == "unset" {
			for k := range keys {
				err := unsetFieldByJSONTag(&writable, k)
				if err != nil {
					return fmt.Errorf("Error unsetting property: %v", err)
				}
			}
		} else {
			err := unpackKVToWritable(&writable, keys)
			if err != nil {
				return fmt.Errorf("Error setting properties: %v", err)
			}
		}
	} else {
		maps.Copy(writable.Config, keys)
	}

	return resource.server.UpdateClusterLink(resource.name, writable, etag)
}

// Unset.
type cmdClusterLinkUnset struct {
	global         *cmdGlobal
	cluster        *cmdCluster
	clusterLinkSet *cmdClusterLinkSet

	flagIsProperty bool
}

func (c *cmdClusterLinkUnset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset", "[<remote>:]<link> <key>")
	cmd.Short = "Unset cluster link configuration keys"
	cmd.Long = cli.FormatSection("Description", `Unset cluster link configuration keys`)

	cmd.RunE = c.run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, "Unset the key as a cluster link property")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("cluster_link", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpClusterLinkConfig(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterLinkUnset) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	c.clusterLinkSet.flagIsProperty = c.flagIsProperty

	// Get the current cluster link.
	args = append(args, "")
	return c.clusterLinkSet.run(cmd, args)
}
