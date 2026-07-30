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

type cmdStorageVolumeBitmap struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume
}

func (c *cmdStorageVolumeBitmap) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("bitmap")
	cmd.Short = "Manage storage volume dirty bitmaps"
	cmd.Long = cli.FormatSection("Description", cmd.Short+`

A dirty bitmap records which blocks of a block volume change after its creation.
Bitmaps live in the QEMU process of a running virtual machine and are lost when it stops.`)

	// Create
	storageVolumeBitmapCreateCmd := cmdStorageVolumeBitmapCreate{global: c.global, storage: c.storage, storageVolume: c.storageVolume, storageVolumeBitmap: c}
	cmd.AddCommand(storageVolumeBitmapCreateCmd.command())

	// Delete
	storageVolumeBitmapDeleteCmd := cmdStorageVolumeBitmapDelete{global: c.global, storage: c.storage, storageVolume: c.storageVolume, storageVolumeBitmap: c}
	cmd.AddCommand(storageVolumeBitmapDeleteCmd.command())

	// List
	storageVolumeBitmapListCmd := cmdStorageVolumeBitmapList{global: c.global, storage: c.storage, storageVolume: c.storageVolume, storageVolumeBitmap: c}
	cmd.AddCommand(storageVolumeBitmapListCmd.command())

	// Show
	storageVolumeBitmapShowCmd := cmdStorageVolumeBitmapShow{global: c.global, storage: c.storage, storageVolume: c.storageVolume, storageVolumeBitmap: c}
	cmd.AddCommand(storageVolumeBitmapShowCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// Create.
type cmdStorageVolumeBitmapCreate struct {
	global              *cmdGlobal
	storage             *cmdStorage
	storageVolume       *cmdStorageVolume
	storageVolumeBitmap *cmdStorageVolumeBitmap
}

func (c *cmdStorageVolumeBitmapCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", "[<remote>:]<pool> [<type>/]<volume> <bitmap>")
	cmd.Short = "Create a dirty bitmap on a storage volume"
	cmd.Long = cli.FormatSection("Description", cmd.Short+`

The volume must be a block volume attached to a running virtual machine.`)
	cmd.Example = cli.FormatSection("", `lxc storage volume bitmap create default virtual-machine/vm1 backup0
    Create a bitmap called "backup0" on the root disk of virtual machine "vm1" in pool "default".

lxc storage volume bitmap create default data backup0
    Create a bitmap called "backup0" on custom volume "data" in pool "default".`)

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", cli.FormatStringFlagLabel("Cluster member name"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("storage_pool", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolVolumes(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeBitmapCreate) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, 3)
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
		return errors.New("Missing pool name")
	}

	if args[2] == "" {
		return errors.New("Missing bitmap name")
	}

	client := resource.server

	// If a target member was specified, create the bitmap on that member.
	if c.storage.flagTarget != "" {
		client = client.UseTarget(c.storage.flagTarget)
	}

	// Parse the input
	volName, volType := parseVolume("custom", args[1])

	bitmap := api.StorageVolumeBitmapsPost{
		Name: args[2],
	}

	op, err := client.CreateStorageVolumeBitmap(resource.name, volType, volName, bitmap)
	if err != nil {
		return err
	}

	return op.Wait()
}

// Delete.
type cmdStorageVolumeBitmapDelete struct {
	global              *cmdGlobal
	storage             *cmdStorage
	storageVolume       *cmdStorageVolume
	storageVolumeBitmap *cmdStorageVolumeBitmap
}

func (c *cmdStorageVolumeBitmapDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", "[<remote>:]<pool> [<type>/]<volume> <bitmap>")
	cmd.Aliases = []string{"rm"}
	cmd.Short = "Delete a dirty bitmap from a storage volume"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", cli.FormatStringFlagLabel("Cluster member name"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("storage_pool", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolVolumes(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeBitmapDelete) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, 3)
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
		return errors.New("Missing pool name")
	}

	if args[2] == "" {
		return errors.New("Missing bitmap name")
	}

	client := resource.server

	// If a target member was specified, delete the bitmap on that member.
	if c.storage.flagTarget != "" {
		client = client.UseTarget(c.storage.flagTarget)
	}

	// Parse the input
	volName, volType := parseVolume("custom", args[1])

	op, err := client.DeleteStorageVolumeBitmap(resource.name, volType, volName, args[2])
	if err != nil {
		return err
	}

	err = op.Wait()
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf("Storage volume bitmap %s deleted\n", args[2])
	}

	return nil
}

// List.
type cmdStorageVolumeBitmapList struct {
	global              *cmdGlobal
	storage             *cmdStorage
	storageVolume       *cmdStorageVolume
	storageVolumeBitmap *cmdStorageVolumeBitmap

	flagFormat  string
	flagColumns string
}

// columns returns the ordered column definitions for storage volume bitmap list.
func (c *cmdStorageVolumeBitmapList) columns() []cli.ShorthandColumn[api.StorageVolumeBitmap] {
	return []cli.ShorthandColumn[api.StorageVolumeBitmap]{
		{Shorthand: 'n', Name: "NAME", Data: c.nameColumnData},
		{Shorthand: 'c', Name: "DIRTY BYTES", Data: c.countColumnData},
		{Shorthand: 'g', Name: "GRANULARITY", Data: c.granularityColumnData},
		{Shorthand: 'b', Name: "BUSY", Data: c.busyColumnData},
	}
}

func (c *cmdStorageVolumeBitmapList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", "[<remote>:]<pool> [<type>/]<volume>")
	cmd.Aliases = []string{"ls"}
	cmd.Short = "List storage volume dirty bitmaps"
	cmd.Long = cli.FormatSection("Description", cmd.Short+`

The -c option takes a (optionally comma-separated) list of arguments
that control which bitmap attributes to output when displaying in table
or csv format.

Column shorthand chars:
    n - Name
    c - Number of dirty bytes
    g - Granularity in bytes
    b - Busy state`)

	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", cli.FormatStringFlagLabel("Format (csv|json|table|yaml|compact)"))
	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", cli.DefaultColumnString(c.columns()), cli.FormatStringFlagLabel("Columns"))
	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", cli.FormatStringFlagLabel("Cluster member name"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("storage_pool", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolVolumes(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeBitmapList) run(cmd *cobra.Command, args []string) error {
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

	if resource.name == "" {
		return errors.New("Missing pool name")
	}

	client := resource.server

	// If a target member was specified, list the bitmaps on that member.
	if c.storage.flagTarget != "" {
		client = client.UseTarget(c.storage.flagTarget)
	}

	// Parse the input
	volName, volType := parseVolume("custom", args[1])

	bitmaps, err := client.GetStorageVolumeBitmaps(resource.name, volType, volName)
	if err != nil {
		return err
	}

	// Parse column flags.
	columns, err := cli.ParseShorthandColumns(c.flagColumns, c.columns())
	if err != nil {
		return err
	}

	data := cli.ColumnData(columns, bitmaps)
	sort.Sort(cli.SortColumnsNaturally(data))
	header := cli.ColumnHeaders(columns)

	return cli.RenderTable(c.flagFormat, header, data, bitmaps)
}

func (c *cmdStorageVolumeBitmapList) nameColumnData(bitmap api.StorageVolumeBitmap) string {
	return bitmap.Name
}

func (c *cmdStorageVolumeBitmapList) countColumnData(bitmap api.StorageVolumeBitmap) string {
	return strconv.FormatInt(bitmap.Count, 10)
}

func (c *cmdStorageVolumeBitmapList) granularityColumnData(bitmap api.StorageVolumeBitmap) string {
	return strconv.Itoa(bitmap.Granularity)
}

func (c *cmdStorageVolumeBitmapList) busyColumnData(bitmap api.StorageVolumeBitmap) string {
	if bitmap.Busy {
		return "YES"
	}

	return "NO"
}

// Show.
type cmdStorageVolumeBitmapShow struct {
	global              *cmdGlobal
	storage             *cmdStorage
	storageVolume       *cmdStorageVolume
	storageVolumeBitmap *cmdStorageVolumeBitmap
}

func (c *cmdStorageVolumeBitmapShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", "[<remote>:]<pool> [<type>/]<volume> <bitmap>")
	cmd.Short = "Show storage volume dirty bitmap details"
	cmd.Long = cli.FormatSection("Description", cmd.Short)
	cmd.Example = cli.FormatSection("", `lxc storage volume bitmap show default virtual-machine/vm1 backup0
    Will show the details of bitmap "backup0" on the root disk of virtual machine "vm1" in the "default" pool.`)

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", cli.FormatStringFlagLabel("Cluster member name"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("storage_pool", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolVolumes(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeBitmapShow) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, 3)
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
		return errors.New("Missing pool name")
	}

	if args[2] == "" {
		return errors.New("Missing bitmap name")
	}

	client := resource.server

	// If a target member was specified, show the bitmap on that member.
	if c.storage.flagTarget != "" {
		client = client.UseTarget(c.storage.flagTarget)
	}

	// Parse the input
	volName, volType := parseVolume("custom", args[1])

	bitmap, err := client.GetStorageVolumeBitmap(resource.name, volType, volName, args[2])
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
