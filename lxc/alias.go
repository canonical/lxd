package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/canonical/lxd/shared"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/termios"
)

type cmdAlias struct {
	global *cmdGlobal
}

// Command is a method of the cmdAlias structure that returns a new cobra Command for managing command aliases.
// This includes commands for adding, listing, renaming, and removing aliases, along with their usage and descriptions.
func (c *cmdAlias) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("alias")
	cmd.Short = "Manage command aliases"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

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

	// Show
	aliasShowCmd := cmdAliasShow{global: c.global, alias: c}
	cmd.AddCommand(aliasShowCmd.command())

	// Edit
	aliasEditCmd := cmdAliasEdit{global: c.global, alias: c}
	cmd.AddCommand(aliasEditCmd.command())

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
	cmd.Use = usage("add", "<alias> <target>")
	cmd.Short = "Add new alias"
	cmd.Long = cli.FormatSection("Description", cmd.Short)
	cmd.Example = cli.FormatSection("", `lxc alias add list "list -c ns46S"
    Overwrite the "list" command to pass -c ns46S.`)

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
		return fmt.Errorf("Alias %s already exists", args[0])
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
	cmd.Short = "List aliases"
	cmd.Long = cli.FormatSection("Description", cmd.Short)
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", cli.FormatStringFlagLabel("Format (csv|json|table|yaml|compact)"))

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
		"ALIAS",
		"TARGET",
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
	cmd.Use = usage("rename", "<old alias> <new alias>")
	cmd.Aliases = []string{"mv"}
	cmd.Short = "Rename alias"
	cmd.Long = cli.FormatSection("Description", cmd.Short)
	cmd.Example = cli.FormatSection("", `lxc alias rename list my-list
    Rename existing alias "list" to "my-list".`)

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
		return fmt.Errorf("Alias %s doesn't exist", args[0])
	}

	// Check for the new alias
	_, ok = conf.Aliases[args[1]]
	if ok {
		return fmt.Errorf("Alias %s already exists", args[1])
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
	cmd.Use = usage("remove", "<alias>")
	cmd.Aliases = []string{"rm"}
	cmd.Short = "Remove alias"
	cmd.Long = cli.FormatSection("Description", cmd.Short)
	cmd.Example = cli.FormatSection("", `lxc alias remove my-list
    Remove the "my-list" alias.`)

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
		return fmt.Errorf("Alias %s doesn't exist", args[0])
	}

	// Delete the alias
	delete(conf.Aliases, args[0])

	// Save the config
	return conf.SaveConfig(c.global.confPath)
}

// Show.
type cmdAliasShow struct {
	global *cmdGlobal
	alias  *cmdAlias
}

// Command creates a Cobra command to show all aliases in YAML format.
func (c *cmdAliasShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show")
	cmd.Short = "Show aliases in YAML format"
	cmd.Long = cli.FormatSection("Description", cmd.Short)
	cmd.RunE = c.run

	return cmd
}

// Run executes the show command to display all aliases in YAML format.
func (c *cmdAliasShow) run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks
	exit, err := c.global.CheckArgs(cmd, args, 0, 0)
	if exit {
		return err
	}

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return nil, cobra.ShellCompDirectiveDefault
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	// Convert aliases to YAML and print
	data, err := yaml.Marshal(&conf.Aliases)
	if err != nil {
		return err
	}

	fmt.Print(string(data))
	return nil
}

// Edit.
type cmdAliasEdit struct {
	global *cmdGlobal
	alias  *cmdAlias
}

// Command creates a Cobra command to edit aliases either via interactive editor or via pipe to stdin.
func (c *cmdAliasEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit")
	cmd.Short = "Edit aliases"

	cmd.Example = cli.FormatSection("", `lxc alias edit
	Edit the aliases via interactive terminal.

lxc alias edit < aliases.yaml
	Edit the aliases from "aliases.yaml".`)

	cmd.RunE = c.run

	return cmd
}

// HelpTemplate returns a sample YAML representation of aliases and guidelines for editing.
func (c *cmdAliasEdit) helpTemplate() string {
	return `### This is a YAML representation of the aliases.
### Any line starting with a '#' will be ignored.
###
### A sample aliases configuration looks like:
### list: "list -c ns46S"
### my-list: "list -c ns46S"
### start-all: "start --all"
###
### Note that aliases are key-value pairs.`
}

// Run executes the alias edit command, allowing users to edit aliases via an interactive YAML editor.
func (c *cmdAliasEdit) run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 0)
	if exit {
		return err
	}

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return nil, cobra.ShellCompDirectiveDefault
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	// If stdin isn't a terminal, read text from it.
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newAliases := make(map[string]string)
		err = yaml.Unmarshal(contents, &newAliases)
		if err != nil {
			return err
		}

		importedCount := len(newAliases)
		// Prevent clearing all aliases if input is empty.
		if importedCount == 0 {
			return errors.New("No aliases found in input.")
		}

		// Update aliases and save config.
		conf.Aliases = newAliases

		fmt.Printf("Imported: %d alias(es)\n", importedCount)
		return conf.SaveConfig(c.global.confPath)
	}

	// Extract the current aliases.
	data, err := yaml.Marshal(&conf.Aliases)
	if err != nil {
		return err
	}

	// Spawn the editor.
	content, err := shared.TextEditor("", []byte(c.helpTemplate()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor.
		newAliases := make(map[string]string)
		err = yaml.Unmarshal(content, &newAliases)

		// Respawn the editor if there was an error.
		if err != nil {
			fmt.Fprintf(os.Stderr, "Alias parsing error: %v\n", err)
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
		// Update aliases and save config.
		conf.Aliases = newAliases
		return conf.SaveConfig(c.global.confPath)
	}
}
