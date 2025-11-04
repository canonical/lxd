package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/lxc/config"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
	"github.com/canonical/lxd/shared/logger"
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

	// Import
	aliasImportCmd := cmdAliasImport{global: c.global, alias: c}
	cmd.AddCommand(aliasImportCmd.command())

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

// Import.
type cmdAliasImport struct {
	global *cmdGlobal
	alias  *cmdAlias

	flagFormat    string
	flagOverwrite bool
	parsers       *ParserRegistry
}

// Command is a method of the cmdAliasImport structure. It configures and returns a cobra.Command object.
// This command enables import of the file which holds aliases.
func (c *cmdAliasImport) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("import", i18n.G("[<file>]"))
	cmd.Short = i18n.G("Import aliases from file")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Import aliases from YAML, JSON, or CSV file
If file is "-" or not specified, reads from stdin.`))

	cmd.Example = cli.FormatSection("", i18n.G(`
lxc alias import aliases.yml
	Import aliases from YAML file.

lxc alias import aliases.json --overwrite
	Import aliases from JSON file, overwriting existing ones.

lxc alias import aliases.csv --format=csv
	Import aliases from CSV file with explicit format.

lxc alias import -
    Import aliases from stdin.

lxc alias export - | lxc alias import -
    Export aliases to stdout and import from stdin via pipe.

cat aliases.json | lxc alias import -
    Import aliases from JSON file via pipe.`))

	// Initialize parser registry and get available format names.
	registry, formatNames := GetFormatNames()
	// Build the format string with proper i18n
	formatOptions := "auto|" + strings.Join(formatNames, "|")
	formatFlagUsage := i18n.G("Format") + " (" + formatOptions + ")``"
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "auto", formatFlagUsage)
	cmd.Flags().BoolVarP(&c.flagOverwrite, "overwrite", "", false, i18n.G("Overwrite existing aliases"))

	cmd.RunE = c.run
	c.parsers = registry

	return cmd
}

// Run is a method of the cmdAliasImport structure that executes the actual operation of the alias import command.
// It takes as input as the name of the alias file to be imported and updates the global configuration file to reflect this change.
func (c *cmdAliasImport) run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks - allow 0 or 1 args for stdin support
	exit, err := c.global.CheckArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	// Determine input source
	var inputSource string
	useStdin := false

	if len(args) == 0 || args[0] == "-" {
		useStdin = true
		inputSource = "stdin"
	} else {
		inputSource = args[0]
	}

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return nil, cobra.ShellCompDirectiveDefault
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	// Debug: show current aliases before import
	c.logCurrentAliases(conf)

	// Read and parse file
	// Read and parse file or stdin
	newAliases, err := c.readAndParseAliases(inputSource, useStdin)
	if err != nil {
		return err
	}

	// Import Aliases
	importedCount, skippedCount, overwrittenCount := c.importAliases(conf, newAliases)

	// Debug show final aliases
	c.logFinalAliases(conf)

	// Debug show what has been parsed
	logger.Debugf("Parsed %d aliases from file\n", len(newAliases))
	for alias, target := range newAliases {
		logger.Debugf("New alias: %s -> %s\n", alias, target)
	}

	// Save config
	err = conf.SaveConfig(c.global.confPath)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to save config: %v"), err)
	}

	// Report results
	fmt.Printf(i18n.G("Imported: %d, Skipped: %d, Overwritten: %d aliases\n"), importedCount, skippedCount, overwrittenCount)
	return nil
}

// readAndParseAliases reads the file and parses aliases based on format.
func (c *cmdAliasImport) readAndParseAliases(source string, useStdin bool) (map[string]string, error) {
	var data []byte
	var err error

	if useStdin {
		// Read from stdin
		data, err = io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf(i18n.G("Failed to read from stdin: %v"), err)
		}

		if len(data) == 0 {
			return nil, fmt.Errorf("%s", i18n.G("No data received from stdin"))
		}
	} else {
		// Read from file
		data, err = os.ReadFile(source)
		if err != nil {
			return nil, fmt.Errorf(i18n.G("Failed to read file: %v"), err)
		}
	}

	var parser FormatParser
	if c.flagFormat == "auto" {
		if useStdin {
			// For stdin without explicit format, default to YAML but try to detect
			parser = c.parsers.GetParser("yaml") // default

			// Try to auto-detect from content
			for _, p := range c.parsers.parsers {
				if p.Detect("", data) {
					parser = p
					logger.Debugf("Auto-detected format from content: %s", parser.Name())
					break
				}
			}
		} else {
			// Auto-detect format from filename
			parser, err = c.parsers.DetectFormat(source, data)
			if err != nil {
				return nil, err
			}

			logger.Debugf("Auto-detected format: %s", parser.Name())
		}
	} else {
		// Use specified format
		parser = c.parsers.GetParser(c.flagFormat)
		if parser == nil {
			return nil, fmt.Errorf(i18n.G("unsupported format: %s"), c.flagFormat)
		}

		logger.Debugf("Using specified format: %s", parser.Name())
	}

	newAliases, err := parser.Parse(data)
	if err != nil {
		if useStdin {
			return nil, fmt.Errorf(i18n.G("Failed to parse stdin as %s: %v"), parser.Name(), err)
		}

		return nil, fmt.Errorf(i18n.G("Failed to parse %s file: %v"), parser.Name(), err)
	}

	return newAliases, nil
}

// importAliases imports new aliases into configuration.
func (c *cmdAliasImport) importAliases(conf *config.Config, newAliases map[string]string) (importedCount, skippedCount, overwrittenCount int) {
	for alias, target := range newAliases {
		existingTarget, exists := conf.Aliases[alias]
		if exists {
			if c.flagOverwrite {
				// Overwrite existing alias
				conf.Aliases[alias] = target
				overwrittenCount++
				logger.Infof("Overwritten alias %s: %s -> %s", alias, existingTarget, target)
			} else {
				// Skip existing alias (no overwrite)
				skippedCount++
				logger.Infof("Skipped existing alias %s (use --overwrite to replace)", alias)
			}
		} else {
			// Add new alias
			conf.Aliases[alias] = target
			importedCount++
			logger.Infof("Added new alias %s -> %s", alias, target)
		}
	}
	return importedCount, skippedCount, overwrittenCount
}

// logCurrentAliases logs current aliases for debugging.
func (c *cmdAliasImport) logCurrentAliases(conf *config.Config) {
	logger.Debugf("Current aliases count before import: %d\n", len(conf.Aliases))
	for alias, target := range conf.Aliases {
		logger.Debugf("Current alias %s -> %s\n", alias, target)
	}
}

// logFinalAliases logs final aliases for debugging.
func (c *cmdAliasImport) logFinalAliases(conf *config.Config) {
	logger.Debugf("Final aliases count: %d", len(conf.Aliases))
	for alias, target := range conf.Aliases {
		logger.Debugf("Final alias: %s -> %s", alias, target)
	}
}

// Export.
type cmdAliasExport struct {
	global *cmdGlobal
	alias  *cmdAlias

	flagFormat string
	parsers    *ParserRegistry
}

// Command is a method of the cmdAliasExport structure. It configures and returns a cobra.Command object.
// This command enables export of aliases to file.
func (c *cmdAliasExport) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("export", i18n.G("[<file>]"))
	cmd.Short = i18n.G("Export aliases to file")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Export aliases to YAML, JSON, or CSV file

If file is "-", aliases are written to stdout.`))
	cmd.Example = cli.FormatSection("", i18n.G(`
lxc alias export
	Export aliases to default file ni current directory.

lxc alias export -
		Export aliases to stdout.

lxc alias export aliases.json --format=json
    Export aliases as JSON to aliases.json.

lxc alias export aliases.2025_10_22
    Export aliases to file with custom extension (defaults to YAML).

lxc alias export aliases.txt --format=json
    Export aliases as JSON to aliases.txt.json.`))

	// Initialize parser registry
	registry, formatNames := GetFormatNames()
	formatOptions := "auto|" + strings.Join(formatNames, "|")
	formatFlagUsage := i18n.G("Format") + " (" + formatOptions + ")``"
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "auto", formatFlagUsage)
	cmd.RunE = c.run
	c.parsers = registry

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

	// Determine filename and outpu type
	filename := ""
	useStdout := false

	if len(args) == 0 {
		// No filename provided, generate default.
		filename = c.generateDefaultFilename()
	} else if args[0] == "-" {
		// Use stdout.
		useStdout = true
	} else {
		filename = args[0]
	}

	// Debug: show current aliases before export
	c.logCurrentAliases(conf)

	// Export aliases
	exportedCount, finalFilename, err := c.exportAliases(conf.Aliases, filename, useStdout)
	if err != nil {
		return err
	}

	if !useStdout {
		fmt.Printf(i18n.G("Exported %d aliases to %s\n"), exportedCount, finalFilename)
	}

	return nil
}

// generateDefaultFilename generates a default filename with timestamp.
func (c *cmdAliasExport) generateDefaultFilename() string {
	timestamp := time.Now().Format("20060102_150405")
	format := c.determineFinalFormat("")
	extension := c.getExtensionForFormat(format)
	return fmt.Sprintf("lxc_aliases_%s.%s", timestamp, extension)
}

// determineFinalFormat determines the final format to use for export.
func (c *cmdAliasExport) determineFinalFormat(filename string) string {
	// If format explicitly specified, use it.
	if c.flagFormat != "auto" {
		return c.flagFormat
	}

	// If no filename, default to YAML
	if filename == "" {
		return "yaml"
	}

	// Try to detect format from file name extension
	parser := c.parsers.GetParserByExtension(filename)
	if parser != nil {
		return parser.Name()
	}

	// Default to YAML for unknown extensions
	return "yaml"
}

// getExtensionForFormat returns the file extension for a format.
func (c *cmdAliasExport) getExtensionForFormat(format string) string {
	parser := c.parsers.GetParser(format)
	if parser != nil {
		return parser.Name()
	}

	return "yaml" // fallback
}

// exportAliases exports aliases to a file or stdout in the specified format.
func (c *cmdAliasExport) exportAliases(aliases map[string]string, filename string, useStdout bool) (int, string, error) {
	// Determine final filename and format
	finalFormat := c.determineFinalFormat(filename)
	finalFilename := filename

	// Adjust filename if not using stdout and format doesn't match extension
	if !useStdout {
		finalFilename = c.adjustFilenameForFormat(filename, finalFormat)
	}

	logger.Infof("Exporting %d aliases in %s format to %s", len(aliases), finalFormat, finalFilename)

	// Get the appropriate parser
	parser := c.parsers.GetParser(finalFormat)
	if parser == nil {
		return 0, "", fmt.Errorf(i18n.G("unsupported export format: %s"), finalFormat)
	}

	// Generarte export data
	data, err := parser.Serialize(aliases)
	if err != nil {
		return 0, "", fmt.Errorf(i18n.G("failed to serialize aliases: %v"), err)
	}

	// Write to file or stdout
	if useStdout {
		_, err = os.Stdout.Write(data)
	} else {
		err = os.WriteFile(finalFilename, data, 0644)
	}

	if err != nil {
		return 0, "", fmt.Errorf(i18n.G("failed to write output: %v"), err)
	}

	return len(aliases), finalFilename, nil
}

// adjustFilenameForFormat adjusts the filename to match the export format.
func (c *cmdAliasExport) adjustFilenameForFormat(filename, format string) string {
	desiredExtension := "." + c.getExtensionForFormat(format)
	currentExtension := filepath.Ext(filename)

	// If no current extension, append desired extension
	if currentExtension == "" {
		return filename + desiredExtension
	}

	// Check if current extensionmatches the format
	currentExtension = strings.ToLower(currentExtension)
	desiredExtension = strings.ToLower(desiredExtension)

	// Special handling for YAML format - both .yml and .yaml are valid
	if format == "yaml" {
		// If current extension is a valid YAML extension
		if currentExtension == ".yml" || currentExtension == ".yaml" {
			// Keep the original filename with its existing YAML extension
			return filename
		}
	}

	// For other formats, check if extensions match exactly
	if currentExtension == desiredExtension {
		return filename
	}

	// Extensions don't match, append desired extension
	newFilename := filename + desiredExtension
	logger.Debugf("Appending format extension: %s -> %s", filename, newFilename)
	return newFilename
}

// logCurrentAliases logs current aliases for debugging.
func (c *cmdAliasExport) logCurrentAliases(conf *config.Config) {
	logger.Debugf("Current aliases count: %d", len(conf.Aliases))
	for alias, target := range conf.Aliases {
		logger.Debugf("Current alias: %s -> %s", alias, target)
	}
}
