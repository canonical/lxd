package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	yaml "gopkg.in/yaml.v2"

	"github.com/lxc/lxd/lxc/utils"
	"github.com/lxc/lxd/shared/api"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
)

type warningColumn struct {
	Name string
	Data func(api.Warning) string
}

type cmdWarning struct {
	global *cmdGlobal
}

func (c *cmdWarning) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("warning")
	cmd.Short = i18n.G("Manage warnings")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage warnings`))

	// List
	warningListCmd := cmdWarningList{global: c.global, warning: c}
	cmd.AddCommand(warningListCmd.Command())

	// Acknowledge
	warningAcknowledgeCmd := cmdWarningAcknowledge{global: c.global, warning: c}
	cmd.AddCommand(warningAcknowledgeCmd.Command())

	// Show
	warningShowCmd := cmdWarningShow{global: c.global, warning: c}
	cmd.AddCommand(warningShowCmd.Command())

	// Delete
	warningDeleteCmd := cmdWarningDelete{global: c.global, warning: c}
	cmd.AddCommand(warningDeleteCmd.Command())

	return cmd
}

// List
type cmdWarningList struct {
	global  *cmdGlobal
	warning *cmdWarning

	flagColumns string
	flagFormat  string
	flagAll     bool
}

func (c *cmdWarningList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List warnings")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List warnings

The -c option takes a (optionally comma-separated) list of arguments
that control which warning attributes to output when displaying in table
or csv format.

Default column layout is: utSscpLl

Column shorthand chars:

    c - Count
    l - Last seen
    L - Location
    f - First seen
    p - Project
    s - Severity
    S - Status
    u - UUID
    t - Type`))

	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", "utSscpLl", i18n.G("Columns")+"``")
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml)")+"``")
	cmd.Flags().BoolVarP(&c.flagAll, "all", "a", false, i18n.G("List all warnings")+"``")

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdWarningList) Run(cmd *cobra.Command, args []string) error {
	// Parse remote
	remote := ""
	if len(args) > 0 {
		remote = args[0]
	}

	remoteName, _, err := c.global.conf.ParseRemote(remote)
	if err != nil {
		return err
	}

	remoteServer, err := c.global.conf.GetInstanceServer(remoteName)
	if err != nil {
		return err
	}

	allWarnings, err := remoteServer.GetWarnings()
	if err != nil {
		return err
	}

	// Per default, acknowledged and resolved warnings are not shown. Using the --all flag will show
	// those as well.
	var warnings []api.Warning

	if c.flagAll {
		warnings = allWarnings
	} else {
		for _, warning := range allWarnings {
			if warning.Status == "acknowledged" || warning.Status == "resolved" {
				continue
			}

			warnings = append(warnings, warning)
		}
	}

	// Process the columns
	columns, err := c.parseColumns()
	if err != nil {
		return err
	}

	// Render the table
	data := [][]string{}
	for _, warning := range warnings {
		row := []string{}
		for _, column := range columns {
			row = append(row, column.Data(warning))
		}
		data = append(data, row)
	}
	sort.Sort(stringList(data))

	rawData := make([]*api.Warning, len(warnings))
	for i := range warnings {
		rawData[i] = &warnings[i]
	}

	headers := []string{}
	for _, column := range columns {
		headers = append(headers, column.Name)
	}

	return utils.RenderTable(c.flagFormat, headers, data, rawData)
}

func (c *cmdWarningList) countColumnData(warning api.Warning) string {
	return fmt.Sprintf("%d", warning.Count)
}

func (c *cmdWarningList) firstSeenColumnData(warning api.Warning) string {
	return warning.FirstSeenAt.UTC().Format("Jan 2, 2006 at 3:04pm (MST)")
}

func (c *cmdWarningList) lastSeenColumnData(warning api.Warning) string {
	return warning.LastSeenAt.UTC().Format("Jan 2, 2006 at 3:04pm (MST)")
}

func (c *cmdWarningList) locationColumnData(warning api.Warning) string {
	return warning.Location
}

func (c *cmdWarningList) projectColumnData(warning api.Warning) string {
	return warning.Project
}

func (c *cmdWarningList) severityColumnData(warning api.Warning) string {
	return strings.ToUpper(warning.Severity)
}

func (c *cmdWarningList) statusColumnData(warning api.Warning) string {
	return strings.ToUpper(warning.Status)
}

func (c *cmdWarningList) typeColumnData(warning api.Warning) string {
	return warning.Type
}

func (c *cmdWarningList) uuidColumnData(warning api.Warning) string {
	return warning.UUID
}

func (c *cmdWarningList) parseColumns() ([]warningColumn, error) {
	columnsShorthandMap := map[rune]warningColumn{
		'c': {i18n.G("COUNT"), c.countColumnData},
		'f': {i18n.G("FIRST SEEN"), c.firstSeenColumnData},
		'l': {i18n.G("LAST SEEN"), c.lastSeenColumnData},
		'L': {i18n.G("LOCATION"), c.locationColumnData},
		'p': {i18n.G("PROJECT"), c.projectColumnData},
		's': {i18n.G("SEVERITY"), c.severityColumnData},
		'S': {i18n.G("STATUS"), c.statusColumnData},
		't': {i18n.G("TYPE"), c.typeColumnData},
		'u': {i18n.G("UUID"), c.uuidColumnData},
	}

	columnList := strings.Split(c.flagColumns, ",")

	columns := []warningColumn{}
	for _, columnEntry := range columnList {
		if columnEntry == "" {
			return nil, fmt.Errorf(i18n.G("Empty column entry (redundant, leading or trailing command) in '%s'"), c.flagColumns)
		}

		for _, columnRune := range columnEntry {
			if column, ok := columnsShorthandMap[columnRune]; ok {
				columns = append(columns, column)
			} else {
				return nil, fmt.Errorf(i18n.G("Unknown column shorthand char '%c' in '%s'"), columnRune, columnEntry)
			}
		}
	}

	return columns, nil
}

// Acknowledge
type cmdWarningAcknowledge struct {
	global  *cmdGlobal
	warning *cmdWarning

	flagAll bool
}

func (c *cmdWarningAcknowledge) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("acknowledge", i18n.G("[<remote>:]<warning-uuid>"))
	cmd.Aliases = []string{"ack"}
	cmd.Short = i18n.G("Acknowledge warning")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Acknowledge warning`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdWarningAcknowledge) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	remoteName, UUID, err := c.global.conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	remoteServer, err := c.global.conf.GetInstanceServer(remoteName)
	if err != nil {
		return err
	}

	warning := api.WarningPut{Status: "acknowledged"}

	return remoteServer.UpdateWarning(UUID, warning, "")
}

// Show
type cmdWarningShow struct {
	global  *cmdGlobal
	warning *cmdWarning
}

func (c *cmdWarningShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<warning-uuid>"))
	cmd.Short = i18n.G("Show warning")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show warning`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdWarningShow) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	remoteName, UUID, err := c.global.conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	remoteServer, err := c.global.conf.GetInstanceServer(remoteName)
	if err != nil {
		return err
	}

	warning, _, err := remoteServer.GetWarning(UUID)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&warning)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Delete
type cmdWarningDelete struct {
	global  *cmdGlobal
	warning *cmdWarning

	flagAll bool
}

func (c *cmdWarningDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<warning-uuid>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete warning")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Delete warning`))

	cmd.Flags().BoolVarP(&c.flagAll, "all", "a", false, i18n.G("Delete all warnings")+"``")

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdWarningDelete) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	remoteName, UUID, err := c.global.conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	remoteServer, err := c.global.conf.GetInstanceServer(remoteName)
	if err != nil {
		return err
	}

	return remoteServer.DeleteWarning(UUID)
}
