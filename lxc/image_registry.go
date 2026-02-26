package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
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

	// Create
	imageRegistryCreateCmd := cmdImageRegistryCreate{global: c.global, imageRegistry: c}
	cmd.AddCommand(imageRegistryCreateCmd.command())

	// List
	imageRegistryListCmd := cmdImageRegistryList{global: c.global, imageRegistry: c}
	cmd.AddCommand(imageRegistryListCmd.command())

	// Rename
	imageRegistryRenameCmd := cmdImageRegistryRename{global: c.global, imageRegistry: c}
	cmd.AddCommand(imageRegistryRenameCmd.command())

	// Delete
	imageRegistryDeleteCmd := cmdImageRegistryDelete{global: c.global, imageRegistry: c}
	cmd.AddCommand(imageRegistryDeleteCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// Create.
type cmdImageRegistryCreate struct {
	global        *cmdGlobal
	imageRegistry *cmdImageRegistry

	flagDescription string
	flagProtocol    string
	flagProject     string
}

func (c *cmdImageRegistryCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", "[<remote>:]<registry> [key=value...]")
	cmd.Short = "Create image registry"
	cmd.Long = cli.FormatSection("Description", cmd.Short+`
	
URL must be HTTPS (https://). Basic authentication is not allowed.
`)

	cmd.RunE = c.run
	cmd.Flags().StringVar(&c.flagDescription, "description", "", cli.FormatStringFlagLabel("Description for the image registry"))
	cmd.Flags().StringVar(&c.flagProtocol, "protocol", "", cli.FormatStringFlagLabel("Registry server protocol (lxd or simplestreams)"))
	cmd.Flags().StringVar(&c.flagProject, "project", "", cli.FormatStringFlagLabel("Source project for the LXD registry"))

	return cmd
}

func (c *cmdImageRegistryCreate) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, -1)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]
	client := resource.server

	registryName := resource.name
	if registryName == "" {
		return errors.New("Image registry name must not be empty")
	}

	// Create the image registry.
	imageRegistry := api.ImageRegistriesPost{}
	imageRegistry.Name = registryName
	imageRegistry.Description = c.flagDescription
	imageRegistry.Protocol = c.flagProtocol
	imageRegistry.Config = map[string]string{}

	// Populate config.
	for i := 1; i < len(args); i++ {
		key, value, found := strings.Cut(args[i], "=")
		if !found {
			return fmt.Errorf("Bad key/value pair: %s", args[i])
		}

		imageRegistry.Config[key] = value
	}

	if c.flagProject != "" {
		imageRegistry.Config["source_project"] = c.flagProject
	}

	// Create the image registry.
	err = client.CreateImageRegistry(imageRegistry)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf("Image registry %s created\n", registryName)
	}

	return nil
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

// Rename.
type cmdImageRegistryRename struct {
	global        *cmdGlobal
	imageRegistry *cmdImageRegistry
}

func (c *cmdImageRegistryRename) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rename", "[<remote>:]<registry> <new_name>")
	cmd.Aliases = []string{"mv"}
	cmd.Short = "Rename image registry"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("image_registry", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdImageRegistryRename) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New("Missing image registry name")
	}

	// Rename the image registry.
	err = resource.server.RenameImageRegistry(resource.name, api.ImageRegistryPost{Name: args[1]})
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf("Image registry %s renamed to %s\n", resource.name, args[1])
	}

	return nil
}

// Delete.
type cmdImageRegistryDelete struct {
	global        *cmdGlobal
	imageRegistry *cmdImageRegistry
}

func (c *cmdImageRegistryDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", "[<remote>:]<registry>")
	cmd.Aliases = []string{"rm"}
	cmd.Short = "Delete image registry"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("image_registry", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdImageRegistryDelete) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New("Missing image registry name")
	}

	// Delete the image registry.
	err = resource.server.DeleteImageRegistry(resource.name)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf("Image registry %s deleted\n", resource.name)
	}

	return nil
}
