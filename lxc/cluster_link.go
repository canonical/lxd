package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
	"github.com/canonical/lxd/shared/termios"
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

	// List
	clusterLinkListCmd := cmdClusterLinkList{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterLinkListCmd.command())

	// Remove
	clusterLinkRemoveCmd := cmdClusterLinkRemove{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterLinkRemoveCmd.command())

	// Edit
	clusterLinkEditCmd := cmdClusterLinkEdit{global: c.global, cluster: c.cluster}
	cmd.AddCommand(clusterLinkEditCmd.command())

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
	flagAddresses   []string
	flagAuthGroups  []string
	flagDescription string
}

func (c *cmdClusterLinkAdd) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("add", i18n.G("<name> [--token <trust_token>] [--address <addr1>,<addr2>,...] [--auth-group <group1,group2,...>] [--description <description>]"))
	cmd.Short = i18n.G("Add cluster links")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Add cluster links

When run with a token, creates an active cluster link.
When run without a token, creates a pending cluster link that must be activated by adding a cluster link on the remote cluster.`))
	cmd.Example = cli.FormatSection("", i18n.G(`lxc cluster link add backup-cluster --address 10.0.0.0:8443,10.0.0.1:8443 --auth-group backups
	Create a pending cluster link reachable at "10.0.0.0:8443" and "10.0.0.1:8443" called "backup-cluster", belonging to the authentication group "backups".

	lxc cluster link add main-cluster <token from backup-bluster> --auth-group backups
	Create a cluster link with "backup-cluster" called "main-cluster", belonging to the auth group "backups".

	lxc cluster link add recovery-cluster < config.yaml
	Create a pending cluster link with the configuration from "config.yaml" called "recovery-cluster".`))
	cmd.Flags().StringVarP(&c.flagToken, "token", "t", "", "Trust token to use when adding cluster link")
	cmd.Flags().StringSliceVarP(&c.flagAddresses, "address", "a", []string{}, "Optional IP addresses to override addresses inside token")
	cmd.Flags().StringSliceVarP(&c.flagAuthGroups, "auth-group", "g", []string{}, "Authentication groups to add the newly created cluster link identity to")
	cmd.Flags().StringVarP(&c.flagDescription, "description", "d", "", "Cluster link description")

	cmd.RunE = c.run

	return cmd
}

func (c *cmdClusterLinkAdd) run(cmd *cobra.Command, args []string) error {
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

	// Extract the actual link name from the resource
	linkName := resource.name

	clusterLink := api.ClusterLinkPost{
		Name: linkName,
	}

	// Validate addresses if provided
	if len(c.flagAddresses) > 0 {
		for _, address := range c.flagAddresses {
			ip := net.ParseIP(address)
			if ip == nil {
				return fmt.Errorf(i18n.G("Invalid IP address: %s"), address)
			}

			clusterLink.Addresses = append(clusterLink.Addresses, ip)
		}
	}

	if c.flagDescription != "" {
		clusterLink.Description = c.flagDescription
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
			return fmt.Errorf("Failed to encode identity token: %w", err)
		}

		// Print the base64 encoded token.
		fmt.Println(base64.StdEncoding.EncodeToString(tokenJSON))
		return nil
	}

	clusterLink.TrustToken = c.flagToken

	err = client.CreateClusterLink(clusterLink)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		if c.flagToken == "" {
			fmt.Printf(i18n.G("Cluster link %s created (pending)")+"\n", linkName)
		} else {
			fmt.Printf(i18n.G("Cluster link %s added")+"\n", linkName)
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
	cmd.Use = usage("list", i18n.G("[<remote>:]"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List cluster links")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List cluster links`))

	cmd.RunE = c.run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpRemotes(toComplete, false)
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
	resources, err := c.global.ParseServers(args[0])
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
	for _, link := range clusterLinks {
		addresses := make([]string, 0, len(link.Addresses))
		for _, address := range link.Addresses {
			addresses = append(addresses, address.String())
		}

		addressesStr := strings.Join(addresses, ", ")

		details := []string{
			link.Name,
			addressesStr,
			link.Description,
		}

		data = append(data, details)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		i18n.G("NAME"),
		i18n.G("ADDRESSES"),
		i18n.G("DESCRIPTION"),
	}

	return cli.RenderTable(c.flagFormat, header, data, clusterLinks)
}

// Remove.
type cmdClusterLinkRemove struct {
	global  *cmdGlobal
	cluster *cmdCluster
}

func (c *cmdClusterLinkRemove) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("remove", i18n.G("<name>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Remove cluster links")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Remove cluster links`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterLinks(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterLinkRemove) run(cmd *cobra.Command, args []string) error {
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
		fmt.Printf(i18n.G("Cluster link %s removed")+"\n", resource.name)
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
	cmd.Use = usage("edit", i18n.G("[<remote>:]<name>"))
	cmd.Short = i18n.G("Edit cluster link configurations as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit cluster link configurations as YAML`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc cluster link edit [<remote>:]<name> < link.yaml
    Update a cluster link using the content of link.yaml.`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpClusterLinks(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdClusterLinkEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of a cluster link.
### Any line starting with a '#' will be ignored.
###
### A cluster link consists of a set of configuration items.
###
### Editable properties:
### - description: A description for the cluster link
### - addresses: A list of addresses where the cluster is reachable
### - groups: A list of auth groups that this link belongs to
###
### An example would look like:
### description: backup cluster
### addresses: [10.0.0.1:8443, 10.0.0.2:8443]
### groups: [foo, bar]
###   `)
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
		return errors.New(i18n.G("Missing cluster link name"))
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
	linkPut := clusterLink.Writable()

	data, err := yaml.Marshal(&linkPut)
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

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Cluster link %s updated")+"\n", resource.name)
	}

	return nil
}
