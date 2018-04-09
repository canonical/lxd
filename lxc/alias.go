package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"

	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
)

type cmdAlias struct {
	global *cmdGlobal
}

func (c *cmdAlias) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("alias")
	cmd.Short = i18n.G("Manage command aliases")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage command aliases`))

	// Add
	aliasAddCmd := cmdAliasAdd{global: c.global, alias: c}
	cmd.AddCommand(aliasAddCmd.Command())

	// List
	aliasListCmd := cmdAliasList{global: c.global, alias: c}
	cmd.AddCommand(aliasListCmd.Command())

	// Rename
	aliasRenameCmd := cmdAliasRename{global: c.global, alias: c}
	cmd.AddCommand(aliasRenameCmd.Command())

	// Remove
	aliasRemoveCmd := cmdAliasRemove{global: c.global, alias: c}
	cmd.AddCommand(aliasRemoveCmd.Command())

	return cmd
}

// Add
type cmdAliasAdd struct {
	global *cmdGlobal
	alias  *cmdAlias
}

func (c *cmdAliasAdd) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("add <alias> <target>")
	cmd.Short = i18n.G("Add new aliases")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Add new aliases`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc alias add list "list -c ns46S"
    Overwrite the "list" command to pass -c ns46S.`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdAliasAdd) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Look for an existing alias
	_, ok := conf.Aliases[args[0]]
	if ok {
		return fmt.Errorf(i18n.G("Alias %s already exists"), args[0])
	}

	// Add the new alias
	conf.Aliases[args[0]] = args[1]

	// Save the config
	return conf.SaveConfig(c.global.confPath)
}

// List
type cmdAliasList struct {
	global *cmdGlobal
	alias  *cmdAlias
}

func (c *cmdAliasList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("list")
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List aliases")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List aliases`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdAliasList) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 0, 0)
	if exit {
		return err
	}

	// List the aliases
	data := [][]string{}
	for k, v := range conf.Aliases {
		data = append(data, []string{k, v})
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetAutoWrapText(false)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetRowLine(true)
	table.SetHeader([]string{
		i18n.G("ALIAS"),
		i18n.G("TARGET")})
	sort.Sort(byName(data))
	table.AppendBulk(data)
	table.Render()

	return nil
}

// Rename
type cmdAliasRename struct {
	global *cmdGlobal
	alias  *cmdAlias
}

func (c *cmdAliasRename) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("rename <old alias> <new alias>")
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename aliases")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Rename aliases`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc alias rename list my-list
    Rename existing alias "list" to "my-list".`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdAliasRename) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Check for the existing alias
	target, ok := conf.Aliases[args[0]]
	if !ok {
		return fmt.Errorf(i18n.G("Alias %s doesn't exist"), args[0])
	}

	// Check for the new alias
	_, ok = conf.Aliases[args[1]]
	if ok {
		return fmt.Errorf(i18n.G("Alias %s already exists"), args[1])
	}

	// Rename the alias
	conf.Aliases[args[1]] = target
	delete(conf.Aliases, args[0])

	// Save the config
	return conf.SaveConfig(c.global.confPath)
}

// Remove
type cmdAliasRemove struct {
	global *cmdGlobal
	alias  *cmdAlias
}

func (c *cmdAliasRemove) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("remove <alias>")
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Remove aliases")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Remove aliases`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc alias remove my-list
    Remove the "my-list" alias.`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdAliasRemove) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Look for the alias
	_, ok := conf.Aliases[args[0]]
	if !ok {
		return fmt.Errorf(i18n.G("Alias %s doesn't exist"), args[0])
	}

	// Delete the alias
	delete(conf.Aliases, args[0])

	// Save the config
	return conf.SaveConfig(c.global.confPath)
}
