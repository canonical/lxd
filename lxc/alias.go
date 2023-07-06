package main

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
)

type cmdAlias struct {
	global *cmdGlobal
}

// Command configures the 'alias' command of a Cobra-based CLI application
// It allows managing command aliases including adding, listing, renaming, and removing aliases
func (c *cmdAlias) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("alias")
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

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// Add.
type cmdAliasAdd struct {
	global *cmdGlobal
	alias  *cmdAlias
}

// Command configures the 'add' subcommand under 'alias' for a Cobra-based CLI application
// It allows adding new aliases to the application
func (c *cmdAliasAdd) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("add", i18n.G("<alias> <target>"))
	cmd.Short = i18n.G("Add new aliases")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Add new aliases`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc alias add list "list -c ns46S"
    Overwrite the "list" command to pass -c ns46S.`))

	cmd.RunE = c.Run

	return cmd
}

// Run executes the 'add' subcommand under 'alias' for a Cobra-based CLI application
// It adds a new alias to the application if it does not already exist, and saves the updated configuration
func (c *cmdAliasAdd) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
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

// List.
type cmdAliasList struct {
	global *cmdGlobal
	alias  *cmdAlias

	flagFormat string
}

// Command configures the 'list' subcommand under 'alias' for a Cobra-based CLI application
// It allows listing all aliases in the application, with various output format options (csv, json, table, yaml, compact)
func (c *cmdAliasList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list")
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List aliases")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List aliases`))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")

	cmd.RunE = c.Run

	return cmd
}

// Run executes the 'list' subcommand under 'alias' for a Cobra-based CLI application
// It lists all existing aliases and their corresponding targets in a specified output format
func (c *cmdAliasList) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 0)
	if exit {
		return err
	}

	// List the aliases
	data := [][]string{}
	for k, v := range conf.Aliases {
		data = append(data, []string{k, v})
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		i18n.G("ALIAS"),
		i18n.G("TARGET"),
	}

	return cli.RenderTable(c.flagFormat, header, data, conf.Aliases)
}

// Rename.
type cmdAliasRename struct {
	global *cmdGlobal
	alias  *cmdAlias
}

// Command configures the 'rename' subcommand under 'alias' for a Cobra-based CLI application
// It allows renaming existing aliases in the application
func (c *cmdAliasRename) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rename", i18n.G("<old alias> <new alias>"))
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

// Run executes the 'rename' subcommand under 'alias' for a Cobra-based CLI application
// It renames an existing alias to a new one if the new alias doesn't already exist, and saves the updated configuration
func (c *cmdAliasRename) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
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

// Remove.
type cmdAliasRemove struct {
	global *cmdGlobal
	alias  *cmdAlias
}

// Command configures the 'remove' subcommand under 'alias' for a Cobra-based CLI application
// It allows removing existing aliases from the application
func (c *cmdAliasRemove) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("remove", i18n.G("<alias>"))
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

// Run executes the 'remove' subcommand under 'alias' for a Cobra-based CLI application
// It removes an existing alias from the application and saves the updated configuration
func (c *cmdAliasRemove) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
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
