package main

import (
	"errors"
	"fmt"
	"net/url"
	"sort"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
)

type cmdImageRegistry struct {
	global *cmdGlobal
	image  *cmdImage
}

func (c *cmdImageRegistry) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("registry")
	cmd.Short = "Manage image registries"
	cmd.Long = cli.FormatSection("Description", `Manage image registries`)

	// Add
	imageRegistryAddCmd := cmdImageRegistryAdd{global: c.global, image: c.image}
	cmd.AddCommand(imageRegistryAddCmd.command())

	// List
	imageRegistryListCmd := cmdImageRegistryList{global: c.global, image: c.image}
	cmd.AddCommand(imageRegistryListCmd.command())

	// Rename
	imageRegistryRenameCmd := cmdImageRegistryRename{global: c.global, image: c.image}
	cmd.AddCommand(imageRegistryRenameCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// Add.
type cmdImageRegistryAdd struct {
	global *cmdGlobal
	image  *cmdImage

	flagProtocol string
	flagProject  string
	flagPublic   bool
	flagCluster  string
}

func (c *cmdImageRegistryAdd) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("add", "[<remote>:]<registry> <URL>")
	cmd.Short = "Add image registry"
	cmd.Long = cli.FormatSection("Description", cmd.Short+`
	
URL must be HTTPS (https://). Basic authentication is not allowed.
`)

	cmd.RunE = c.run
	cmd.Flags().StringVar(&c.flagProtocol, "protocol", "", cli.FormatStringFlagLabel("Registry server protocol (lxd or simplestreams)"))
	cmd.Flags().StringVar(&c.flagProject, "project", "", cli.FormatStringFlagLabel("Source project for the LXD registry"))
	cmd.Flags().BoolVar(&c.flagPublic, "public", false, "Public image registry")
	cmd.Flags().StringVar(&c.flagCluster, "cluster", "", cli.FormatStringFlagLabel("Cluster link name for the private LXD registry"))

	return cmd
}

func (c *cmdImageRegistryAdd) run(cmd *cobra.Command, args []string) error {
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
	client := resource.server

	registryName := resource.name
	registryURL := args[1]

	if registryName == "" {
		return errors.New("Image registry name must not be empty")
	}

	if registryURL == "" {
		return errors.New("Image registry URL must not be empty")
	}

	_, err = url.ParseRequestURI(registryURL)
	if err != nil {
		return errors.New(`Image registry URL must be an absolute URL with a "https://" scheme`)
	}

	// Create the image registry.
	imageRegistry := api.ImageRegistriesPost{}
	imageRegistry.Name = registryName
	imageRegistry.URL = registryURL
	imageRegistry.Protocol = c.flagProtocol
	imageRegistry.SourceProject = c.flagProject
	imageRegistry.Public = c.flagPublic
	imageRegistry.Cluster = c.flagCluster

	// Create the image registry.
	err = client.CreateImageRegistry(imageRegistry)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf("Image registry %s added\n", resource.name)
	}

	return nil
}

// List.
type cmdImageRegistryList struct {
	global *cmdGlobal
	image  *cmdImage

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
		public := "false"
		if registry.Public {
			public = "true"
		}

		details := []string{
			registry.Name,
			registry.URL,
			registry.Protocol,
			public,
		}

		data = append(data, details)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		"NAME",
		"URL",
		"PROTOCOL",
		"PUBLIC",
	}

	return cli.RenderTable(c.flagFormat, header, data, imageRegistries)
}

// Rename.
type cmdImageRegistryRename struct {
	global *cmdGlobal
	image  *cmdImage
}

func (c *cmdImageRegistryRename) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rename", "[<remote>:]<registry> <new_name>")
	cmd.Aliases = []string{"mv"}
	cmd.Short = "Rename image registry"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.RunE = c.run

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
