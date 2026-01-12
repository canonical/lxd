package main

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/termios"
)

type cmdPlacementGroup struct {
	global *cmdGlobal
}

func (c *cmdPlacementGroup) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("placement-group")
	cmd.Short = "Manage placement groups"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	// List.
	placementGroupListCmd := cmdPlacementGroupList{global: c.global, placementGroup: c}
	cmd.AddCommand(placementGroupListCmd.command())

	// Show.
	placementGroupShowCmd := cmdPlacementGroupShow{global: c.global, placementGroup: c}
	cmd.AddCommand(placementGroupShowCmd.command())

	// Create.
	placementGroupCreateCmd := cmdPlacementGroupCreate{global: c.global, placementGroup: c}
	cmd.AddCommand(placementGroupCreateCmd.command())

	// Edit.
	placementGroupEditCmd := cmdPlacementGroupEdit{global: c.global, placementGroup: c}
	cmd.AddCommand(placementGroupEditCmd.command())

	// Get.
	placementGroupGetCmd := cmdPlacementGroupGet{global: c.global, placementGroup: c}
	cmd.AddCommand(placementGroupGetCmd.command())

	// Set.
	placementGroupSetCmd := cmdPlacementGroupSet{global: c.global, placementGroup: c}
	cmd.AddCommand(placementGroupSetCmd.command())

	// Unset.
	placementGroupUnsetCmd := cmdPlacementGroupUnset{global: c.global, placementGroup: c, placementGroupSet: &placementGroupSetCmd}
	cmd.AddCommand(placementGroupUnsetCmd.command())

	// Delete.
	placementGroupDeleteCmd := cmdPlacementGroupDelete{global: c.global, placementGroup: c}
	cmd.AddCommand(placementGroupDeleteCmd.command())

	// Rename.
	placementGroupRenameCmd := cmdPlacementGroupRename{global: c.global, placementGroup: c}
	cmd.AddCommand(placementGroupRenameCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// List.
type cmdPlacementGroupList struct {
	global         *cmdGlobal
	placementGroup *cmdPlacementGroup

	flagFormat      string
	flagAllProjects bool
}

func (c *cmdPlacementGroupList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", "[<remote>:]")
	cmd.Aliases = []string{"ls"}
	cmd.Short = "List available placement groups"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.RunE = c.run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", cli.FormatStringFlagLabel("Format (csv|json|table|yaml|compact)"))
	cmd.Flags().BoolVar(&c.flagAllProjects, "all-projects", false, "Display placement groups from all projects")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(toComplete, ":", true, instanceServerRemoteCompletionFilters(*c.global.conf)...)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdPlacementGroupList) run(cmd *cobra.Command, args []string) error {
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

	// List the placement groups.
	if resource.name != "" {
		return errors.New("Filtering isn't supported yet")
	}

	var placementGroups []api.PlacementGroup
	if c.flagAllProjects {
		placementGroups, err = resource.server.GetPlacementGroupsAllProjects()
		if err != nil {
			return err
		}
	} else {
		placementGroups, err = resource.server.GetPlacementGroups()
		if err != nil {
			return err
		}
	}

	data := [][]string{}
	for _, placementGroup := range placementGroups {
		details := []string{
			placementGroup.Name,
			placementGroup.Description,
			placementGroup.Config["policy"],
			placementGroup.Config["rigor"],
			strconv.Itoa(len(placementGroup.UsedBy)),
		}

		if c.flagAllProjects {
			details = append([]string{placementGroup.Project}, details...)
		}

		data = append(data, details)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		"NAME",
		"DESCRIPTION",
		"POLICY",
		"RIGOR",
		"USED BY",
	}

	if c.flagAllProjects {
		header = append([]string{"PROJECT"}, header...)
	}

	return cli.RenderTable(c.flagFormat, header, data, placementGroups)
}

// Show.
type cmdPlacementGroupShow struct {
	global         *cmdGlobal
	placementGroup *cmdPlacementGroup
}

func (c *cmdPlacementGroupShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", "[<remote>:]<placement_group>")
	cmd.Short = "Show placement group configurations"
	cmd.Long = cli.FormatSection("Description", cmd.Short)
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("placement_group", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdPlacementGroupShow) run(cmd *cobra.Command, args []string) error {
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
		return errors.New("Missing placement group name")
	}

	// Show the placement group config.
	placementGroup, _, err := resource.server.GetPlacementGroup(resource.name)
	if err != nil {
		return err
	}

	sort.Strings(placementGroup.UsedBy)

	data, err := yaml.Marshal(&placementGroup)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Create.
type cmdPlacementGroupCreate struct {
	global          *cmdGlobal
	placementGroup  *cmdPlacementGroup
	flagConfig      []string
	flagDescription string
}

func (c *cmdPlacementGroupCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", "[<remote>:]<placement_group> [key=value...]")
	cmd.Short = "Create new placement groups"
	cmd.Long = cli.FormatSection("Description", cmd.Short)
	cmd.Example = cli.FormatSection("", `lxc placement-group create pg1 policy=spread rigor=strict

lxc placement-group create pg1 policy=compact rigor=permissive

lxc placement-group create pg1 < config.yaml
    Create placement group pg1 with configuration from config.yaml`)

	cmd.Flags().StringArrayVarP(&c.flagConfig, "config", "c", nil, cli.FormatStringFlagLabel("Config key/value to apply to the new placement group"))
	cmd.Flags().StringVar(&c.flagDescription, "description", "", cli.FormatStringFlagLabel("Description of the placement group"))
	cmd.RunE = c.run

	return cmd
}

func (c *cmdPlacementGroupCreate) run(cmd *cobra.Command, args []string) error {
	var stdinData api.PlacementGroupPut

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, -1)
	if exit {
		return err
	}

	// If stdin isn't a terminal, read yaml from it.
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		err = yaml.UnmarshalStrict(contents, &stdinData)
		if err != nil {
			return err
		}
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New("Missing placement group name")
	}

	// Create the placement group.
	placementGroup := api.PlacementGroupsPost{}
	placementGroup.Name = resource.name
	placementGroup.PlacementGroupPut = stdinData

	if placementGroup.Config == nil {
		placementGroup.Config = map[string]string{}
	}

	// Parse config from command line arguments.
	for _, entry := range args[1:] {
		key, value, found := strings.Cut(entry, "=")
		if !found {
			return fmt.Errorf("Bad key=value pair: %q", entry)
		}

		placementGroup.Config[key] = value
	}

	// Parse config from flags.
	for _, entry := range c.flagConfig {
		key, value, found := strings.Cut(entry, "=")
		if !found {
			return fmt.Errorf("Bad key=value pair: %q", entry)
		}

		placementGroup.Config[key] = value
	}

	if c.flagDescription != "" {
		placementGroup.Description = c.flagDescription
	}

	err = resource.server.CreatePlacementGroup(placementGroup)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf("Placement group %s created\n", resource.name)
	}

	return nil
}

// Edit.
type cmdPlacementGroupEdit struct {
	global         *cmdGlobal
	placementGroup *cmdPlacementGroup
}

func (c *cmdPlacementGroupEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", "[<remote>:]<placement_group>")
	cmd.Short = "Edit placement group configurations as YAML"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("placement_group", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdPlacementGroupEdit) helpTemplate() string {
	return `### This is a YAML representation of the placement group.
### Any line starting with a '# will be ignored.
###
### An example placement group structure is shown below.
### The name, project, and used_by fields cannot be modified.
###
### name: my-placement-group
### project: default
### description: Spread instances across cluster members
### config:
###   policy: spread
###   rigor: strict
### used_by:
### - /1.0/instances/c1
### - /1.0/instances/c2
### - /1.0/profiles/p1
`
}

func (c *cmdPlacementGroupEdit) run(cmd *cobra.Command, args []string) error {
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
		return errors.New("Missing placement group name")
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		// Allow output of `lxc placement group show` command to be passed in here, but only take the contents
		// of the [api.PlacementGroupPut] fields when updating the placement group. The other fields are silently discarded.
		newdata := api.PlacementGroup{}
		err = yaml.UnmarshalStrict(contents, &newdata)
		if err != nil {
			return err
		}

		return resource.server.UpdatePlacementGroup(resource.name, newdata.Writable(), "")
	}

	// Get the current config.
	placementGroup, etag, err := resource.server.GetPlacementGroup(resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&placementGroup)
	if err != nil {
		return err
	}

	// Spawn the editor.
	content, err := shared.TextEditor("", []byte(c.helpTemplate()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor.
		newdata := api.PlacementGroup{} // We show the full placement group info, but only send the writable fields.
		err = yaml.UnmarshalStrict(content, &newdata)
		if err == nil {
			err = resource.server.UpdatePlacementGroup(resource.name, newdata.Writable(), etag)
		}

		// Respawn the editor.
		if err != nil {
			fmt.Fprintf(os.Stderr, "Config parsing error: %s\n", err)
			fmt.Println("Press enter to open the editor again or ctrl+c to abort change")

			_, err := os.Stdin.Read(make([]byte, 1))
			if err != nil {
				return err
			}

			content, err = shared.TextEditor("", content)
			if err != nil {
				return err
			}

			continue
		}

		break
	}

	return nil
}

// Get.
type cmdPlacementGroupGet struct {
	global         *cmdGlobal
	placementGroup *cmdPlacementGroup
}

func (c *cmdPlacementGroupGet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get", "[<remote>:]<placement_group> <key>")
	cmd.Short = "Get values for placement group configuration keys"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("placement_group", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpPlacementGroupConfigs(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdPlacementGroupGet) run(cmd *cobra.Command, args []string) error {
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
		return errors.New("Missing placement group name")
	}

	// Get the configuration key.
	placementGroup, _, err := resource.server.GetPlacementGroup(resource.name)
	if err != nil {
		return err
	}

	fmt.Printf("%s\n", placementGroup.Config[args[1]])

	return nil
}

// Set.
type cmdPlacementGroupSet struct {
	global         *cmdGlobal
	placementGroup *cmdPlacementGroup
}

func (c *cmdPlacementGroupSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set", "[<remote>:]<placement_group> <key>=<value>...")
	cmd.Short = "Set placement group configuration keys"
	cmd.Long = cli.FormatSection("Description", `Set placement group configuration keys

For backward compatibility, a single configuration key may still be set with:
    lxc placement-group set [<remote>:]<placement_group> <key> <value>`)

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("placement_group", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdPlacementGroupSet) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, -1)
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
		return errors.New("Missing placement group name")
	}

	// Get the placement group.
	placementGroup, etag, err := resource.server.GetPlacementGroup(resource.name)
	if err != nil {
		return err
	}

	// Set the configuration key.
	keys, err := getConfig(args[1:]...)
	if err != nil {
		return err
	}

	writable := placementGroup.Writable()
	maps.Copy(writable.Config, keys)

	return resource.server.UpdatePlacementGroup(resource.name, writable, etag)
}

// Unset.
type cmdPlacementGroupUnset struct {
	global            *cmdGlobal
	placementGroup    *cmdPlacementGroup
	placementGroupSet *cmdPlacementGroupSet
}

func (c *cmdPlacementGroupUnset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset", "[<remote>:]<placement_group> <key>")
	cmd.Short = "Unset placement group configuration keys"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("placement_group", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpPlacementGroupConfigs(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdPlacementGroupUnset) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	args = append(args, "")
	return c.placementGroupSet.run(cmd, args)
}

// Delete.
type cmdPlacementGroupDelete struct {
	global         *cmdGlobal
	placementGroup *cmdPlacementGroup
}

func (c *cmdPlacementGroupDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", "[<remote>:]<placement_group>")
	cmd.Aliases = []string{"rm"}
	cmd.Short = "Delete placement groups"
	cmd.Long = cli.FormatSection("Description", cmd.Short)
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("placement_group", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdPlacementGroupDelete) run(cmd *cobra.Command, args []string) error {
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
		return errors.New("Missing placement group name")
	}

	// Delete the placement group.
	err = resource.server.DeletePlacementGroup(resource.name)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf("Placement group %s deleted\n", resource.name)
	}

	return nil
}

// Rename.
type cmdPlacementGroupRename struct {
	global         *cmdGlobal
	placementGroup *cmdPlacementGroup
}

func (c *cmdPlacementGroupRename) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rename", "[<remote>:]<old_name> <new_name>")
	cmd.Aliases = []string{"mv"}
	cmd.Short = "Rename placement groups"
	cmd.Long = cli.FormatSection("Description", cmd.Short)
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("placement_group", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdPlacementGroupRename) run(cmd *cobra.Command, args []string) error {
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
		return errors.New("Missing placement group name")
	}

	// Delete the placement group.
	err = resource.server.RenamePlacementGroup(resource.name, api.PlacementGroupPost{Name: args[1]})
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf("Placement group %s renamed to %s\n", resource.name, args[1])
	}

	return nil
}
