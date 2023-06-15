package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	lxd "github.com/canonical/lxd/client"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
)

type cmdNetworkListAllocations struct {
	global  *cmdGlobal
	network *cmdNetwork

	flagProject     string
	flagAllProjects bool
}

func (c *cmdNetworkListAllocations) pretty(input any) string {
	jsonData, err := json.MarshalIndent(input, "", "\t")
	if err != nil {
		return fmt.Sprintf("%v", input)
	}

	return string(jsonData)
}

func (c *cmdNetworkListAllocations) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list-allocations")
	cmd.Short = i18n.G("List network allocations in use")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("List network allocations in use"))

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.RunE = c.Run

	cmd.Flags().StringVarP(&c.flagProject, "project", "p", "default", i18n.G("Run again a specific project"))
	cmd.Flags().BoolVar(&c.flagAllProjects, "all-projects", false, i18n.G("Run against all projects"))
	return cmd
}

func (c *cmdNetworkListAllocations) Run(cmd *cobra.Command, args []string) error {
	d, err := lxd.ConnectLXDUnix("", nil)
	if err != nil {
		return nil
	}

	// Check if server is initialized.
	_, _, err = d.GetServer()
	if err != nil {
		return err
	}

	addresses, err := d.UseProject(c.flagProject).GetNetworkAllocations(c.flagAllProjects)
	if err != nil {
		return err
	}

	for _, address := range addresses {
		fmt.Println(c.pretty(address))
	}

	return nil
}
