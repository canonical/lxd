package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"

	"github.com/lxc/lxd/shared/api"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
)

type cmdImageAlias struct {
	global *cmdGlobal
	image  *cmdImage
}

func (c *cmdImageAlias) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("alias")
	cmd.Short = i18n.G("Manage image aliases")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage image aliases`))

	// Create
	imageAliasCreateCmd := cmdImageAliasCreate{global: c.global, image: c.image, imageAlias: c}
	cmd.AddCommand(imageAliasCreateCmd.Command())

	// Delete
	imageAliasDeleteCmd := cmdImageAliasDelete{global: c.global, image: c.image, imageAlias: c}
	cmd.AddCommand(imageAliasDeleteCmd.Command())

	// List
	imageAliasListCmd := cmdImageAliasList{global: c.global, image: c.image, imageAlias: c}
	cmd.AddCommand(imageAliasListCmd.Command())

	// Rename
	imageAliasRenameCmd := cmdImageAliasRename{global: c.global, image: c.image, imageAlias: c}
	cmd.AddCommand(imageAliasRenameCmd.Command())

	return cmd
}

// Create
type cmdImageAliasCreate struct {
	global     *cmdGlobal
	image      *cmdImage
	imageAlias *cmdImageAlias
}

func (c *cmdImageAliasCreate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("create [<remote>:]<alias> <fingerprint>")
	cmd.Short = i18n.G("Create aliases for existing images")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Create aliases for existing images`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdImageAliasCreate) Run(cmd *cobra.Command, args []string) error {
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

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Alias name missing"))
	}

	// Create the alias
	alias := api.ImageAliasesPost{}
	alias.Name = resource.name
	alias.Target = args[1]

	return resource.server.CreateImageAlias(alias)
}

// Delete
type cmdImageAliasDelete struct {
	global     *cmdGlobal
	image      *cmdImage
	imageAlias *cmdImageAlias
}

func (c *cmdImageAliasDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("delete [<remote>:]<alias>")
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete image aliases")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Delete image aliases`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdImageAliasDelete) Run(cmd *cobra.Command, args []string) error {
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

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Alias name missing"))
	}

	// Delete the alias
	return resource.server.DeleteImageAlias(resource.name)
}

// List
type cmdImageAliasList struct {
	global     *cmdGlobal
	image      *cmdImage
	imageAlias *cmdImageAlias
}

func (c *cmdImageAliasList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("list [<remote>:] [<filters>...]")
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List image aliases")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List image aliases

Filters may be part of the image hash or part of the image alias name.
`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdImageAliasList) aliasShouldShow(filters []string, state *api.ImageAliasesEntry) bool {
	if len(filters) == 0 {
		return true
	}

	for _, filter := range filters {
		if strings.Contains(state.Name, filter) || strings.Contains(state.Target, filter) {
			return true
		}
	}

	return false
}

func (c *cmdImageAliasList) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 0, -1)
	if exit {
		return err
	}

	// Parse remote
	remote := ""
	if len(args) > 0 {
		remote = args[0]
	}

	remoteName, name, err := c.global.conf.ParseRemote(remote)
	if err != nil {
		return err
	}

	remoteServer, err := c.global.conf.GetImageServer(remoteName)
	if err != nil {
		return err
	}

	// Process the filters
	filters := []string{}
	if name != "" {
		filters = append(filters, name)
	}

	if len(args) > 1 {
		filters = append(filters, args[1:]...)
	}

	// List the aliases
	aliases, err := remoteServer.GetImageAliases()
	if err != nil {
		return err
	}

	// Render the table
	data := [][]string{}
	for _, alias := range aliases {
		if !c.aliasShouldShow(filters, &alias) {
			continue
		}

		data = append(data, []string{alias.Name, alias.Target[0:12], alias.Description})
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetAutoWrapText(false)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetRowLine(true)
	table.SetHeader([]string{
		i18n.G("ALIAS"),
		i18n.G("FINGERPRINT"),
		i18n.G("DESCRIPTION")})
	sort.Sort(stringList(data))
	table.AppendBulk(data)
	table.Render()

	return nil
}

// Rename
type cmdImageAliasRename struct {
	global     *cmdGlobal
	image      *cmdImage
	imageAlias *cmdImageAlias
}

func (c *cmdImageAliasRename) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("rename [<remote>:]<alias> <new-name>")
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename aliases")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Rename aliases`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdImageAliasRename) Run(cmd *cobra.Command, args []string) error {
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

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Alias name missing"))
	}

	// Rename the alias
	return resource.server.RenameImageAlias(resource.name, api.ImageAliasesEntryPost{Name: args[1]})
}
