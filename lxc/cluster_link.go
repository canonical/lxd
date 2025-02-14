package main

import (
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
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 1)
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

	clusterLinks, err := client.GetClusterLinks()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, link := range clusterLinks {
		details := []string{
			link.Name,
			strings.Join(link.Addresses, ","),
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
### An example would look like:
### description: backup cluster
### addresses: [10.0.0.1:8443, 10.0.0.2:8443]
### config:
###   `)
}

func (c *cmdClusterLinkEdit) run(cmd *cobra.Command, args []string) error {
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

	data, err := yaml.Marshal(&clusterLink)
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

	return nil
}
