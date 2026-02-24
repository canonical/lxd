package main

import (
	"sort"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/shared"
	cli "github.com/canonical/lxd/shared/cmd"
)

type cmdImageRegistry struct {
	global *cmdGlobal
}

func (c *cmdImageRegistry) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("registry")
	cmd.Short = "Manage image registries"
	cmd.Long = cli.FormatSection("Description", `Manage image registries`)

	// List
	imageRegistryListCmd := cmdImageRegistryList{global: c.global, imageRegistry: c}
	cmd.AddCommand(imageRegistryListCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// List.
type cmdImageRegistryList struct {
	global        *cmdGlobal
	imageRegistry *cmdImageRegistry

	flagFormat string
}

func (c *cmdImageRegistryList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", "[<remote>:]")
	cmd.Aliases = []string{"ls"}
	cmd.Short = "List image registries"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.RunE = c.run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", cli.FormatStringFlagLabel("Format (csv|json|table|yaml|compact)"))

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 1 {
			return c.global.cmpRemotes(toComplete, ":", true, instanceServerRemoteCompletionFilters(*c.global.conf)...)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdImageRegistryList) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	// Parse remote.
	remote := ""
	if len(args) > 0 {
		remote = args[0]
	}

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]
	client := resource.server

	// Fetch the image registries.
	imageRegistries, err := client.GetImageRegistries()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, registry := range imageRegistries {
		registryPublic := "NO"
		if shared.IsTrue(registry.Config["public"]) {
			registryPublic = "YES"
		}

		registryBuiltin := "NO"
		if registry.Builtin {
			registryBuiltin = "YES"
		}

		details := []string{
			registry.Name,
			registry.Config["url"],
			registry.Protocol,
			registryPublic,
			registryBuiltin,
		}

		data = append(data, details)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		"NAME",
		"URL",
		"PROTOCOL",
		"PUBLIC",
		"BUILT-IN",
	}

	return cli.RenderTable(c.flagFormat, header, data, imageRegistries)
}
