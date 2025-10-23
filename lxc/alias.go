package main

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
)

type cmdAlias struct {
	global *cmdGlobal
}

// Command is a method of the cmdAlias structure that returns a new cobra Command for managing command aliases.
// This includes commands for adding, listing, renaming, and removing aliases, along with their usage and descriptions.
func (c *cmdAlias) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("alias")
	cmd.Short = i18n.G("Manage command aliases")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage command aliases`))

	// Add
	aliasAddCmd := cmdAliasAdd{global: c.global, alias: c}
	cmd.AddCommand(aliasAddCmd.command())

	// List
	aliasListCmd := cmdAliasList{global: c.global, alias: c}
	cmd.AddCommand(aliasListCmd.command())

	// Rename
	aliasRenameCmd := cmdAliasRename{global: c.global, alias: c}
	cmd.AddCommand(aliasRenameCmd.command())

	// Remove
	aliasRemoveCmd := cmdAliasRemove{global: c.global, alias: c}
	cmd.AddCommand(aliasRemoveCmd.command())

	// Export
	aliasExportCmd := cmdAliasExport{global: c.global, alias: c}
	cmd.AddCommand(aliasExportCmd.command())

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

// Command is a method of the cmdAliasAdd structure that returns a new cobra Command for adding new command aliases.
// It specifies the command usage, description, and examples, and links it to the RunE method for execution logic.
func (c *cmdAliasAdd) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("add", i18n.G("<alias> <target>"))
	cmd.Short = i18n.G("Add new aliases")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Add new aliases`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc alias add list "list -c ns46S"
    Overwrite the "list" command to pass -c ns46S.`))

	cmd.RunE = c.run

	return cmd
}

// Run is a method of the cmdAliasAdd structure. It implements the logic to add a new alias command.
// The function checks for valid arguments, verifies if the alias already exists, and if not, adds the new alias to the configuration.
func (c *cmdAliasAdd) run(cmd *cobra.Command, args []string) error {
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

// Command is a method of the cmdAliasList structure that returns a new cobra Command for listing command aliases.
// It specifies the command usage, description, aliases, and output formatting options, and links it to the RunE method for execution logic.
func (c *cmdAliasList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list")
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List aliases")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List aliases`))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")

	cmd.RunE = c.run

	return cmd
}

// Run is a method of the cmdAliasList structure. It implements the logic to list existing command aliases.
// The function checks for valid arguments, collects all the aliases, sorts them, and renders them in the specified format.
func (c *cmdAliasList) run(cmd *cobra.Command, args []string) error {
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

// Command is a method of the cmdAliasRename structure. It returns a new cobra.Command object.
// This command allows a user to rename existing aliases in the CLI application.
func (c *cmdAliasRename) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rename", i18n.G("<old alias> <new alias>"))
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename aliases")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Rename aliases`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc alias rename list my-list
    Rename existing alias "list" to "my-list".`))

	cmd.RunE = c.run

	return cmd
}

// Run is a method of the cmdAliasRename structure. It takes a cobra command and a slice of strings as arguments.
// This method checks the validity of arguments, ensures the existence of the old alias, verifies the non-existence of the new alias, and then proceeds to rename the alias in the configuration.
func (c *cmdAliasRename) run(cmd *cobra.Command, args []string) error {
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

// Command is a method of the cmdAliasRemove structure. It configures and returns a cobra.Command object.
// This command enables the removal of a given alias from the command line interface.
func (c *cmdAliasRemove) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("remove", i18n.G("<alias>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Remove aliases")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Remove aliases`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc alias remove my-list
    Remove the "my-list" alias.`))

	cmd.RunE = c.run

	return cmd
}

// Run is a method of the cmdAliasRemove structure that executes the actual operation of the alias removal command.
// It takes as input the name of the alias to be removed and updates the global configuration file to reflect this change.
func (c *cmdAliasRemove) run(cmd *cobra.Command, args []string) error {
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

// Export.
type cmdAliasExport struct {
	global *cmdGlobal
	alias  *cmdAlias

	flagFormat string
}

// Command is a method of the cmdAliasExport structure. It configures and returns a cobra.Command object.
// This command enables export of aliases to a file.
func (c *cmdAliasExport) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("export", i18n.G("[<file>]"))
	cmd.Short = i18n.G("Export aliases to file")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Export aliases to YAML, JSON, or CSV file`))
	cmd.Example = cli.FormatSection("", i18n.G(`
lxc alias export
Export aliases to default file in current directory.

lxc alias export aliases.yml
    Export aliases to specific file in current directory.

lxc alias export /path/to/aliases.yml
    Export aliases to absolute path.

lxc alias export ../backups/aliases.yml
    Export aliases to relative path.

lxc alias export ~/backups/aliases.json
    Export aliases to home directory.`))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "auto", i18n.G("Format(yaml|json|csv)")+"``")

	cmd.RunE = c.run

	return cmd
}

// Run is a method of the cmdAliasExport structure that executes the actual operation of the alias export command.
// It exports aliases to a file in the specified format.
func (c *cmdAliasExport) run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks
	exit, err := c.global.CheckArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	// Determine filename
	filename := c.getExportFilename(args)

	// Debug: show current aliases before export
	c.logCurrentAliases(conf)

	// Export aliases
	exportedCount, err := c.exportAliases(conf.Aliases, filename)
	if err != nil {
		return err
	}

	fmt.Printf(i18n.G("Exported %d aliases to %s\n"), exportedCount, filename)
	return nil
}

