package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v2"

	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
)

type cmdOperation struct {
	global *cmdGlobal
}

func (c *cmdOperation) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("operation")
	cmd.Short = "Manage background operations"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	// Delete
	operationDeleteCmd := cmdOperationDelete{global: c.global, operation: c}
	cmd.AddCommand(operationDeleteCmd.command())

	// List
	operationListCmd := cmdOperationList{global: c.global, operation: c}
	cmd.AddCommand(operationListCmd.command())

	// Show
	operationShowCmd := cmdOperationShow{global: c.global, operation: c}
	cmd.AddCommand(operationShowCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// Delete.
type cmdOperationDelete struct {
	global    *cmdGlobal
	operation *cmdOperation
}

func (c *cmdOperationDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", "[<remote>:]<operation>")
	cmd.Aliases = []string{"cancel", "rm"}
	cmd.Short = "Delete a background operation (will attempt to cancel)"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.RunE = c.run

	return cmd
}

func (c *cmdOperationDelete) run(cmd *cobra.Command, args []string) error {
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

	// Delete the operation
	err = resource.server.DeleteOperation(resource.name)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf("Operation %s deleted\n", resource.name)
	}

	return nil
}

// List.
type cmdOperationList struct {
	global    *cmdGlobal
	operation *cmdOperation

	flagFormat      string
	flagColumns     string
	flagAllProjects bool
}

const defaultOperationColumns = "itdscC"

func (c *cmdOperationList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", "[<remote>:]")
	cmd.Aliases = []string{"ls"}
	cmd.Short = "List background operations"
	cmd.Long = cli.FormatSection("Description", "List background operations")
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", cli.FormatStringFlagLabel("Format (csv|json|table|yaml|compact)"))
	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", defaultOperationColumns, cli.FormatStringFlagLabel("Columns"))

	cmd.Flags().BoolVar(&c.flagAllProjects, "all-projects", false, "List operations from all projects")

	cmd.RunE = c.run

	return cmd
}

func (c *cmdOperationList) run(cmd *cobra.Command, args []string) error {
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
	if resource.name != "" {
		return errors.New("Filtering isn't supported yet")
	}

	// Get operations
	var operations []api.Operation
	if c.flagAllProjects {
		operations, err = resource.server.GetOperationsAllProjects()
	} else {
		operations, err = resource.server.GetOperations()
	}

	if err != nil {
		return err
	}

	clustered := resource.server.IsClustered()

	// Parse column flags.
	columns, err := c.parseColumns(clustered)
	if err != nil {
		return err
	}

	// Render the table
	data := cli.ColumnData(columns, operations)
	sort.Sort(cli.SortColumnsNaturally(data))
	header := cli.ColumnHeaders(columns)

	return cli.RenderTable(c.flagFormat, header, data, operations)
}

func (c *cmdOperationList) parseColumns(clustered bool) ([]cli.TypedColumn[api.Operation], error) {
	columnsShorthandMap := map[rune]cli.TypedColumn[api.Operation]{
		'i': {Name: "ID", Data: c.idColumnData},
		't': {Name: "TYPE", Data: c.typeColumnData},
		'd': {Name: "DESCRIPTION", Data: c.descriptionColumnData},
		's': {Name: "STATUS", Data: c.statusColumnData},
		'c': {Name: "CANCELABLE", Data: c.cancelableColumnData},
		'C': {Name: "CREATED", Data: c.createdColumnData},
	}

	if clustered {
		columnsShorthandMap['L'] = cli.TypedColumn[api.Operation]{Name: "LOCATION", Data: c.locationColumnData}
	} else {
		if c.flagColumns != defaultOperationColumns {
			if strings.ContainsAny(c.flagColumns, "L") {
				return nil, errors.New("Can't use column shorthand char 'L' (LOCATION) when not clustered")
			}
		}

		c.flagColumns = strings.ReplaceAll(c.flagColumns, "L", "")
	}

	return cli.ParseColumns(c.flagColumns, columnsShorthandMap)
}

func (c *cmdOperationList) idColumnData(op api.Operation) string {
	return op.ID
}

func (c *cmdOperationList) typeColumnData(op api.Operation) string {
	return strings.ToUpper(op.Class)
}

func (c *cmdOperationList) descriptionColumnData(op api.Operation) string {
	return op.Description
}

func (c *cmdOperationList) statusColumnData(op api.Operation) string {
	return strings.ToUpper(op.Status)
}

func (c *cmdOperationList) cancelableColumnData(op api.Operation) string {
	if op.MayCancel {
		return "YES"
	}

	return "NO"
}

func (c *cmdOperationList) createdColumnData(op api.Operation) string {
	return op.CreatedAt.UTC().Format("2006/01/02 15:04 UTC")
}

func (c *cmdOperationList) locationColumnData(op api.Operation) string {
	return op.Location
}

// Show.
type cmdOperationShow struct {
	global    *cmdGlobal
	operation *cmdOperation
}

func (c *cmdOperationShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", "[<remote>:]<operation>")
	cmd.Short = "Show details of a background operation"
	cmd.Long = cli.FormatSection("Description", cmd.Short)
	cmd.Example = cli.FormatSection("", `lxc operation show 344a79e4-d88a-45bf-9c39-c72c26f6ab8a
    Show details on that operation UUID`)

	cmd.RunE = c.run

	return cmd
}

func (c *cmdOperationShow) run(cmd *cobra.Command, args []string) error {
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

	// Get the operation
	op, _, err := resource.server.GetOperation(resource.name)
	if err != nil {
		return err
	}

	// Render as YAML
	data, err := yaml.Marshal(&op)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}
