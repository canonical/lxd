package main

import (
	"strconv"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
)

// networkAllocationsColumn represents a column in the network allocations list output.
type cmdNetworkListAllocations struct {
	global  *cmdGlobal
	network *cmdNetwork

	flagFormat      string
	flagColumns     string
	flagAllProjects bool
}

// columns returns the ordered column definitions for network allocations list.
func (c *cmdNetworkListAllocations) columns() []cli.ShorthandColumn[api.NetworkAllocations] {
	return []cli.ShorthandColumn[api.NetworkAllocations]{
		{Shorthand: 'u', Name: "USED BY", Data: c.usedByColumnData},
		{Shorthand: 'a', Name: "ADDRESS", Data: c.addressColumnData},
		{Shorthand: 'n', Name: "NETWORK", Data: c.networkColumnData},
		{Shorthand: 't', Name: "TYPE", Data: c.typeColumnData},
		{Shorthand: 'N', Name: "NAT", Data: c.natColumnData},
		{Shorthand: 'h', Name: "HARDWARE ADDRESS", Data: c.hwaddrColumnData},
	}
}

func (c *cmdNetworkListAllocations) pretty(allocs []api.NetworkAllocations) error {
	// Parse column flags.
	columns, err := cli.ParseShorthandColumns(c.flagColumns, c.columns())
	if err != nil {
		return err
	}

	data := cli.ColumnData(columns, allocs)
	header := cli.ColumnHeaders(columns)

	return cli.RenderTable(c.flagFormat, header, data, allocs)
}

func (c *cmdNetworkListAllocations) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list-allocations")
	cmd.Short = "List network allocations in use"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.MaximumNArgs(1)
	cmd.RunE = c.run

	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", cli.FormatStringFlagLabel("Format (csv|json|table|yaml|compact)"))
	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", cli.DefaultColumnString(c.columns()), cli.FormatStringFlagLabel("Columns"))
	cmd.Flags().BoolVar(&c.flagAllProjects, "all-projects", false, "Run against all projects")
	return cmd
}

func (c *cmdNetworkListAllocations) run(cmd *cobra.Command, args []string) error {
	remote := ""
	if len(args) > 0 {
		remote = args[0]
	}

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]
	addresses, err := resource.server.GetNetworkAllocations(c.flagAllProjects)
	if err != nil {
		return err
	}

	return c.pretty(addresses)
}

func (c *cmdNetworkListAllocations) usedByColumnData(alloc api.NetworkAllocations) string {
	return alloc.UsedBy
}

func (c *cmdNetworkListAllocations) addressColumnData(alloc api.NetworkAllocations) string {
	return alloc.Address
}

func (c *cmdNetworkListAllocations) networkColumnData(alloc api.NetworkAllocations) string {
	return alloc.Network
}

func (c *cmdNetworkListAllocations) typeColumnData(alloc api.NetworkAllocations) string {
	return alloc.Type
}

func (c *cmdNetworkListAllocations) natColumnData(alloc api.NetworkAllocations) string {
	return strconv.FormatBool(alloc.NAT)
}

func (c *cmdNetworkListAllocations) hwaddrColumnData(alloc api.NetworkAllocations) string {
	return alloc.Hwaddr
}
