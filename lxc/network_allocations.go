package main

import (
	"strconv"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
)

type cmdNetworkListAllocations struct {
	global  *cmdGlobal
	network *cmdNetwork

	flagFormat      string
	flagAllProjects bool
}

func (c *cmdNetworkListAllocations) pretty(allocs []api.NetworkAllocations) error {
	header := []string{
		i18n.G("USED BY"),
		i18n.G("ADDRESS"),
		i18n.G("NETWORK"),
		i18n.G("TYPE"),
		i18n.G("NAT"),
		i18n.G("HARDWARE ADDRESS"),
	}

	data := [][]string{}
	for _, alloc := range allocs {
		row := []string{
			alloc.UsedBy,
			alloc.Address,
			alloc.Network,
			alloc.Type,
			strconv.FormatBool(alloc.NAT),
			alloc.Hwaddr,
		}

		data = append(data, row)
	}

	return cli.RenderTable(c.flagFormat, header, data, allocs)
}

func (c *cmdNetworkListAllocations) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list-allocations")
	cmd.Short = i18n.G("List network allocations in use")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("List network allocations in use"))

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.MaximumNArgs(1)
	cmd.RunE = c.run

	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")
	cmd.Flags().BoolVar(&c.flagAllProjects, "all-projects", false, i18n.G("Run against all projects"))
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
