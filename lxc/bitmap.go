package main

import (
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v2"

	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
)

type cmdBitmap struct {
	global *cmdGlobal
}

func (c *cmdBitmap) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("bitmap")
	cmd.Short = "Manage the dirty bitmaps of virtual machines"
	cmd.Long = cli.FormatSection("Description", cmd.Short+`

A dirty bitmap records which blocks of the block volumes of a virtual machine change after it is created.
A bitmap is created with a snapshot, named after it and kept across a stop and a start of the instance.
An instance snapshot keeps a copy of the bitmaps that existed when it was created, which its NBD export publishes.`)

	// List
	bitmapListCmd := cmdBitmapList{global: c.global}
	cmd.AddCommand(bitmapListCmd.command())

	// Show
	bitmapShowCmd := cmdBitmapShow{global: c.global}
	cmd.AddCommand(bitmapShowCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// List.
type cmdBitmapList struct {
	global *cmdGlobal

	flagTarget  string
	flagFormat  string
	flagColumns string
}

// bitmapVolumeRow is one row of the bitmap list, a bitmap on one volume.
type bitmapVolumeRow struct {
	Name   string
	UUID   string
	Volume api.InstanceBitmapVolume
}

// columns returns the ordered column definitions for bitmap list.
func (c *cmdBitmapList) columns() []cli.ShorthandColumn[bitmapVolumeRow] {
	return []cli.ShorthandColumn[bitmapVolumeRow]{
		{Shorthand: 'n', Name: "NAME", Data: c.nameColumnData},
		{Shorthand: 'u', Name: "UUID", Data: c.uuidColumnData},
		{Shorthand: 'd', Name: "DEVICE", Data: c.deviceColumnData},
		{Shorthand: 'p', Name: "POOL", Data: c.poolColumnData},
		{Shorthand: 't', Name: "TYPE", Data: c.typeColumnData},
		{Shorthand: 'v', Name: "VOLUME", Data: c.volumeColumnData},
		{Shorthand: 'g', Name: "GRANULARITY", Data: c.granularityColumnData},
		{Shorthand: 'r', Name: "RECORDING", Data: c.recordingColumnData},
	}
}

func (c *cmdBitmapList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", "[<remote>:]<instance>/<snapshot>")
	cmd.Aliases = []string{"ls"}
	cmd.Short = "List the dirty bitmaps of an instance snapshot"
	cmd.Long = cli.FormatSection("Description", cmd.Short+`

One row is printed per bitmap and volume.

The -c option takes a (optionally comma-separated) list of arguments
that control which bitmap attributes to output when displaying in table
or csv format.

Column shorthand chars:
    n - Name
    u - UUID
    d - Device
    p - Pool
    t - Volume type
    v - Volume name
    g - Granularity in bytes
    r - Recording state`)
	cmd.Example = cli.FormatSection("", `lxc bitmap list vm1/snap1
    List the bitmaps of snapshot "snap1" of virtual machine "vm1".`)

	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", cli.FormatStringFlagLabel("Format (csv|json|table|yaml|compact)"))
	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", cli.DefaultColumnString(c.columns()), cli.FormatStringFlagLabel("Columns"))
	cmd.Flags().StringVar(&c.flagTarget, "target", "", cli.FormatStringFlagLabel("Cluster member name"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpInstancesAndSnapshots(toComplete)
	}

	return cmd
}

func (c *cmdBitmapList) run(cmd *cobra.Command, args []string) error {
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

	instName, snapName, isSnapshot := api.GetParentAndSnapshotName(resource.name)
	if !isSnapshot {
		return errors.New("Missing instance snapshot name")
	}

	client := resource.server
	if c.flagTarget != "" {
		client = client.UseTarget(c.flagTarget)
	}

	bitmaps, err := client.GetInstanceSnapshotBitmaps(instName, snapName)
	if err != nil {
		return err
	}

	rows := []bitmapVolumeRow{}
	for _, bitmap := range bitmaps {
		for _, volume := range bitmap.Volumes {
			rows = append(rows, bitmapVolumeRow{Name: bitmap.Name, UUID: bitmap.UUID, Volume: volume})
		}
	}

	// Parse column flags.
	columns, err := cli.ParseShorthandColumns(c.flagColumns, c.columns())
	if err != nil {
		return err
	}

	data := cli.ColumnData(columns, rows)
	sort.Sort(cli.SortColumnsNaturally(data))
	header := cli.ColumnHeaders(columns)

	return cli.RenderTable(c.flagFormat, header, data, bitmaps)
}

func (c *cmdBitmapList) nameColumnData(row bitmapVolumeRow) string {
	return row.Name
}

func (c *cmdBitmapList) uuidColumnData(row bitmapVolumeRow) string {
	return row.UUID
}

func (c *cmdBitmapList) deviceColumnData(row bitmapVolumeRow) string {
	return row.Volume.Device
}

func (c *cmdBitmapList) poolColumnData(row bitmapVolumeRow) string {
	return row.Volume.Pool
}

func (c *cmdBitmapList) typeColumnData(row bitmapVolumeRow) string {
	return row.Volume.Type
}

func (c *cmdBitmapList) volumeColumnData(row bitmapVolumeRow) string {
	return row.Volume.Name
}

func (c *cmdBitmapList) granularityColumnData(row bitmapVolumeRow) string {
	return strconv.FormatInt(row.Volume.Granularity, 10)
}

func (c *cmdBitmapList) recordingColumnData(row bitmapVolumeRow) string {
	if row.Volume.Recording {
		return "YES"
	}

	return "NO"
}

// Show.
type cmdBitmapShow struct {
	global *cmdGlobal

	flagTarget string
}

func (c *cmdBitmapShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", "[<remote>:]<instance>/<snapshot> <bitmap>")
	cmd.Short = "Show a dirty bitmap of an instance snapshot"
	cmd.Long = cli.FormatSection("Description", cmd.Short)
	cmd.Example = cli.FormatSection("", `lxc bitmap show vm1/snap1 snap0
    Show the bitmap "snap0" of snapshot "snap1" of virtual machine "vm1".`)

	cmd.Flags().StringVar(&c.flagTarget, "target", "", cli.FormatStringFlagLabel("Cluster member name"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstancesAndSnapshots(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpInstanceBitmaps(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdBitmapShow) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
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

	instName, snapName, isSnapshot := api.GetParentAndSnapshotName(resource.name)
	if !isSnapshot {
		return errors.New("Missing instance snapshot name")
	}

	if args[1] == "" {
		return errors.New("Missing bitmap name")
	}

	client := resource.server
	if c.flagTarget != "" {
		client = client.UseTarget(c.flagTarget)
	}

	bitmap, err := client.GetInstanceSnapshotBitmap(instName, snapName, args[1])
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&bitmap)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}
