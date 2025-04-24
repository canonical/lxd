package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
	"github.com/canonical/lxd/shared/termios"
)

type profileColumn struct {
	Name string
	Data func(api.Profile) string
}

type cmdProfile struct {
	global *cmdGlobal
}

func (c *cmdProfile) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("profile")
	cmd.Short = i18n.G("Manage profiles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage profiles`))

	// Add
	profileAddCmd := cmdProfileAdd{global: c.global, profile: c}
	cmd.AddCommand(profileAddCmd.command())

	// Assign
	profileAssignCmd := cmdProfileAssign{global: c.global, profile: c}
	cmd.AddCommand(profileAssignCmd.command())

	// Copy
	profileCopyCmd := cmdProfileCopy{global: c.global, profile: c}
	cmd.AddCommand(profileCopyCmd.command())

	// Create
	profileCreateCmd := cmdProfileCreate{global: c.global, profile: c}
	cmd.AddCommand(profileCreateCmd.command())

	// Delete
	profileDeleteCmd := cmdProfileDelete{global: c.global, profile: c}
	cmd.AddCommand(profileDeleteCmd.command())

	// Device
	profileDeviceCmd := cmdConfigDevice{global: c.global, profile: c}
	cmd.AddCommand(profileDeviceCmd.command())

	// Edit
	profileEditCmd := cmdProfileEdit{global: c.global, profile: c}
	cmd.AddCommand(profileEditCmd.command())

	// Get
	profileGetCmd := cmdProfileGet{global: c.global, profile: c}
	cmd.AddCommand(profileGetCmd.command())

	// List
	profileListCmd := cmdProfileList{global: c.global, profile: c}
	cmd.AddCommand(profileListCmd.command())

	// Remove
	profileRemoveCmd := cmdProfileRemove{global: c.global, profile: c}
	cmd.AddCommand(profileRemoveCmd.command())

	// Rename
	profileRenameCmd := cmdProfileRename{global: c.global, profile: c}
	cmd.AddCommand(profileRenameCmd.command())

	// Set
	profileSetCmd := cmdProfileSet{global: c.global, profile: c}
	cmd.AddCommand(profileSetCmd.command())

	// Show
	profileShowCmd := cmdProfileShow{global: c.global, profile: c}
	cmd.AddCommand(profileShowCmd.command())

	// Unset
	profileUnsetCmd := cmdProfileUnset{global: c.global, profile: c, profileSet: &profileSetCmd}
	cmd.AddCommand(profileUnsetCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// Add.
type cmdProfileAdd struct {
	global  *cmdGlobal
	profile *cmdProfile
}

func (c *cmdProfileAdd) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("add", i18n.G("[<remote>:]<instance> <profile>"))
	cmd.Short = i18n.G("Add profiles to instances")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Add profiles to instances`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 1 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		if len(args) == 0 {
			return c.global.cmpTopLevelResource("instance", toComplete)
		}

		remote, _, err := c.global.conf.ParseRemote(args[0])
		if err != nil {
			return handleCompletionError(err)
		}

		return c.global.cmpTopLevelResourceInRemote(remote, "profile", toComplete)
	}

	return cmd
}

func (c *cmdProfileAdd) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing instance name"))
	}

	// Add the profile
	inst, etag, err := resource.server.GetInstance(resource.name)
	if err != nil {
		return err
	}

	inst.Profiles = append(inst.Profiles, args[1])

	op, err := resource.server.UpdateInstance(resource.name, inst.Writable(), etag)
	if err != nil {
		return err
	}

	err = op.Wait()
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Profile %s added to %s")+"\n", args[1], resource.name)
	}

	return nil
}

// Assign.
type cmdProfileAssign struct {
	global  *cmdGlobal
	profile *cmdProfile
}

func (c *cmdProfileAssign) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("assign", i18n.G("[<remote>:]<instance> <profiles>"))
	cmd.Aliases = []string{"apply"}
	cmd.Short = i18n.G("Assign sets of profiles to instances")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Assign sets of profiles to instances`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc profile assign foo default,bar
    Set the profiles for "foo" to "default" and "bar".

lxc profile assign foo default
    Reset "foo" to only using the "default" profile.

lxc profile assign foo ''
    Remove all profile from "foo"`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("instance", toComplete)
		}

		remote, _, err := c.global.conf.ParseRemote(args[0])
		if err != nil {
			return handleCompletionError(err)
		}

		return c.global.cmpTopLevelResourceInRemote(remote, "profile", toComplete)
	}

	return cmd
}

func (c *cmdProfileAssign) run(cmd *cobra.Command, args []string) error {
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

	// Assign the profiles
	if resource.name == "" {
		return errors.New(i18n.G("Missing instance name"))
	}

	inst, etag, err := resource.server.GetInstance(resource.name)
	if err != nil {
		return err
	}

	if args[1] != "" {
		inst.Profiles = strings.Split(args[1], ",")
	} else {
		inst.Profiles = nil
	}

	op, err := resource.server.UpdateInstance(resource.name, inst.Writable(), etag)
	if err != nil {
		return err
	}

	err = op.Wait()
	if err != nil {
		return err
	}

	if args[1] == "" {
		args[1] = i18n.G("(none)")
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Profiles %s applied to %s")+"\n", args[1], resource.name)
	}

	return nil
}

// Copy.
type cmdProfileCopy struct {
	global  *cmdGlobal
	profile *cmdProfile

	flagTargetProject string
	flagRefresh       bool
}

func (c *cmdProfileCopy) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("copy", i18n.G("[<remote>:]<profile> [<remote>:]<profile>"))
	cmd.Aliases = []string{"cp"}
	cmd.Short = i18n.G("Copy profiles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Copy profiles`))
	cmd.Flags().StringVar(&c.flagTargetProject, "target-project", "", i18n.G("Copy to a project different from the source")+"``")
	cmd.Flags().BoolVar(&c.flagRefresh, "refresh", false, i18n.G("Update the target profile from the source if it already exists"))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("profile", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpTopLevelResource("profile", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdProfileCopy) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args...)
	if err != nil {
		return err
	}

	source := resources[0]
	dest := resources[1]

	if source.name == "" {
		return errors.New(i18n.G("Missing source profile name"))
	}

	if dest.name == "" {
		dest.name = source.name
	}

	// Copy the profile
	profile, _, err := source.server.GetProfile(source.name)
	if err != nil {
		return err
	}

	if c.flagTargetProject != "" {
		dest.server = dest.server.UseProject(c.flagTargetProject)
	}

	// Refresh the profile if requested.
	if c.flagRefresh {
		err := dest.server.UpdateProfile(dest.name, profile.Writable(), "")
		if err == nil || !api.StatusErrorCheck(err, http.StatusNotFound) {
			return err
		}
	}

	newProfile := api.ProfilesPost{
		ProfilePut: profile.Writable(),
		Name:       dest.name,
	}

	return dest.server.CreateProfile(newProfile)
}

// Create.
type cmdProfileCreate struct {
	global  *cmdGlobal
	profile *cmdProfile
}

func (c *cmdProfileCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", i18n.G("[<remote>:]<profile>"))
	cmd.Short = i18n.G("Create profiles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Create profiles`))
	cmd.Example = cli.FormatSection("", i18n.G(`lxc profile create p1

lxc profile create p1 < config.yaml
    Create profile with configuration from config.yaml`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(toComplete, ":", true, instanceServerRemoteCompletionFilters(*c.global.conf)...)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdProfileCreate) run(cmd *cobra.Command, args []string) error {
	var stdinData api.ProfilePut

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		err = yaml.Unmarshal(contents, &stdinData)
		if err != nil {
			return err
		}
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf("%s", i18n.G("Missing project name"))
	}

	// Create the profile
	profile := api.ProfilesPost{}
	profile.Name = resource.name
	profile.ProfilePut = stdinData

	err = resource.server.CreateProfile(profile)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Profile %s created")+"\n", resource.name)
	}

	return nil
}

// Delete.
type cmdProfileDelete struct {
	global  *cmdGlobal
	profile *cmdProfile
}

func (c *cmdProfileDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<profile>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete profiles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Delete profiles`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("profile", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdProfileDelete) run(cmd *cobra.Command, args []string) error {
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

	if resource.name == "" {
		return errors.New(i18n.G("Missing profile name"))
	}

	// Delete the profile
	err = resource.server.DeleteProfile(resource.name)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Profile %s deleted")+"\n", resource.name)
	}

	return nil
}

// Edit.
type cmdProfileEdit struct {
	global  *cmdGlobal
	profile *cmdProfile
}

func (c *cmdProfileEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<profile>"))
	cmd.Short = i18n.G("Edit profile configurations as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit profile configurations as YAML`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc profile edit <profile> < profile.yaml
    Update a profile using the content of profile.yaml`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("profile", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdProfileEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the profile.
### Any line starting with a '# will be ignored.
###
### A profile consists of a set of configuration items followed by a set of
### devices.
###
### An example would look like:
### name: onenic
### config:
###   raw.lxc: lxc.aa_profile=unconfined
### devices:
###   eth0:
###     nictype: bridged
###     parent: lxdbr0
###     type: nic
###
### Note that the name is shown but cannot be changed`)
}

func (c *cmdProfileEdit) run(cmd *cobra.Command, args []string) error {
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

	if resource.name == "" {
		return errors.New(i18n.G("Missing profile name"))
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.ProfilePut{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}

		return resource.server.UpdateProfile(resource.name, newdata, "")
	}

	// Extract the current value
	profile, etag, err := resource.server.GetProfile(resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&profile)
	if err != nil {
		return err
	}

	// Spawn the editor
	content, err := shared.TextEditor("", []byte(c.helpTemplate()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor
		newdata := api.ProfilePut{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = resource.server.UpdateProfile(resource.name, newdata, etag)
		}

		// Respawn the editor
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.G("Config parsing error: %s")+"\n", err)
			fmt.Println(i18n.G("Press enter to open the editor again or ctrl+c to abort change"))

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
type cmdProfileGet struct {
	global  *cmdGlobal
	profile *cmdProfile

	flagIsProperty bool
}

func (c *cmdProfileGet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get", i18n.G("[<remote>:]<profile> <key>"))
	cmd.Short = i18n.G("Get values for profile configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Get values for profile configuration keys`))

	cmd.RunE = c.run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a profile property"))

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("profile", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpProfileConfigs(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdProfileGet) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing profile name"))
	}

	// Get the configuration key
	profile, _, err := resource.server.GetProfile(resource.name)
	if err != nil {
		return err
	}

	if c.flagIsProperty {
		w := profile.Writable()
		res, err := getFieldByJsonTag(&w, args[1])
		if err != nil {
			return fmt.Errorf(i18n.G("The property %q does not exist on the profile %q: %v"), args[1], resource.name, err)
		}

		fmt.Printf("%v\n", res)
	} else {
		fmt.Printf("%s\n", profile.Config[args[1]])
	}

	return nil
}

// List.
type cmdProfileList struct {
	global          *cmdGlobal
	profile         *cmdProfile
	flagFormat      string
	flagColumns     string
	flagAllProjects bool
}

func (c *cmdProfileList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List profiles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List profiles

The -c option takes a (optionally comma-separated) list of arguments
that control which profile attributes to output when displaying in table
or csv format.

Default column layout is: ndu

Column shorthand chars:
n - Profile Name
d - Description
u - Used By`))

	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", defaultProfileColumns, i18n.G("Columns")+"``")

	cmd.RunE = c.run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")
	cmd.Flags().BoolVar(&c.flagAllProjects, "all-projects", false, i18n.G("Display profiles from all projects"))

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(toComplete, ":", true, instanceServerRemoteCompletionFilters(*c.global.conf)...)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

const (
	defaultProfileColumns            = "ndu"
	defaultProfileColumnsAllProjects = "endu"
)

func (c *cmdProfileList) parseColumns() ([]profileColumn, error) {
	columnsShorthandMap := map[rune]profileColumn{
		'n': {i18n.G("NAME"), c.profileNameColumnData},
		'e': {i18n.G("PROJECT"), c.projectNameColumnData},
		'd': {i18n.G("DESCRIPTION"), c.descriptionColumnData},
		'u': {i18n.G("USED BY"), c.usedByColumnData},
	}

	// Add project column if --all-projects flag specified and no custom column was passed.
	if c.flagAllProjects {
		if c.flagColumns == defaultProfileColumns {
			c.flagColumns = defaultProfileColumnsAllProjects
		}
	}

	columnList := strings.Split(c.flagColumns, ",")
	columns := []profileColumn{}

	for _, columnEntry := range columnList {
		if columnEntry == "" {
			return nil, fmt.Errorf(i18n.G("Empty column entry (redundant, leading or trailing command) in '%s'"), c.flagColumns)
		}

		for _, columnRune := range columnEntry {
			column, ok := columnsShorthandMap[columnRune]
			if !ok {
				return nil, fmt.Errorf(i18n.G("Unknown column shorthand char '%c' in '%s'"), columnRune, columnEntry)
			}

			columns = append(columns, column)
		}
	}

	return columns, nil
}

func (c *cmdProfileList) profileNameColumnData(profile api.Profile) string {
	return profile.Name
}

func (c *cmdProfileList) descriptionColumnData(profile api.Profile) string {
	return profile.Description
}

func (c *cmdProfileList) projectNameColumnData(profile api.Profile) string {
	return profile.Project
}

func (c *cmdProfileList) usedByColumnData(profile api.Profile) string {
	return fmt.Sprint(len(profile.UsedBy))
}

func (c *cmdProfileList) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	// Parse remote
	remote := ""
	if len(args) > 0 {
		remote = args[0]
	}

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]

	// List profiles
	var profiles []api.Profile
	if c.flagAllProjects {
		profiles, err = resource.server.GetProfilesAllProjects()
		if err != nil {
			return err
		}
	} else {
		profiles, err = resource.server.GetProfiles()
		if err != nil {
			return err
		}
	}

	columns, err := c.parseColumns()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, profile := range profiles {
		line := []string{}
		for _, column := range columns {
			line = append(line, column.Data(profile))
		}

		data = append(data, line)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{}
	for _, column := range columns {
		header = append(header, column.Name)
	}

	return cli.RenderTable(c.flagFormat, header, data, profiles)
}

// Remove.
type cmdProfileRemove struct {
	global  *cmdGlobal
	profile *cmdProfile
}

func (c *cmdProfileRemove) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("remove", i18n.G("[<remote>:]<instance> <profile>"))
	cmd.Short = i18n.G("Remove profiles from instances")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Remove profiles from instances`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 1 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		if len(args) == 0 {
			return c.global.cmpTopLevelResource("instance", toComplete)
		}

		remote, _, err := c.global.conf.ParseRemote(args[0])
		if err != nil {
			return handleCompletionError(err)
		}

		return c.global.cmpTopLevelResourceInRemote(remote, "profile", toComplete)
	}

	return cmd
}

func (c *cmdProfileRemove) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing instance name"))
	}

	// Remove the profile
	inst, etag, err := resource.server.GetInstance(resource.name)
	if err != nil {
		return err
	}

	if !shared.ValueInSlice(args[1], inst.Profiles) {
		return fmt.Errorf(i18n.G("Profile %s isn't currently applied to %s"), args[1], resource.name)
	}

	profiles := []string{}
	for _, profile := range inst.Profiles {
		if profile == args[1] {
			continue
		}

		profiles = append(profiles, profile)
	}

	inst.Profiles = profiles

	op, err := resource.server.UpdateInstance(resource.name, inst.Writable(), etag)
	if err != nil {
		return err
	}

	err = op.Wait()
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Profile %s removed from %s")+"\n", args[1], resource.name)
	}

	return nil
}

// Rename.
type cmdProfileRename struct {
	global  *cmdGlobal
	profile *cmdProfile
}

func (c *cmdProfileRename) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rename", i18n.G("[<remote>:]<profile> <new-name>"))
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename profiles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Rename profiles`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("profile", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdProfileRename) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing profile name"))
	}

	// Rename the profile
	err = resource.server.RenameProfile(resource.name, api.ProfilePost{Name: args[1]})
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Profile %s renamed to %s")+"\n", resource.name, args[1])
	}

	return nil
}

// Set.
type cmdProfileSet struct {
	global  *cmdGlobal
	profile *cmdProfile

	flagIsProperty bool
}

func (c *cmdProfileSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set", i18n.G("[<remote>:]<profile> <key>=<value>..."))
	cmd.Short = i18n.G("Set profile configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Set profile configuration keys

For backward compatibility, a single configuration key may still be set with:
    lxc profile set [<remote>:]<profile> <key> <value>`))

	cmd.RunE = c.run
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a profile property"))

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 1 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		if len(args) == 0 {
			return c.global.cmpTopLevelResource("profile", toComplete)
		}

		return c.global.cmpInstanceKeysByType(api.InstanceTypeAny, "=", toComplete)
	}

	return cmd
}

func (c *cmdProfileSet) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, -1)
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
		return errors.New(i18n.G("Missing profile name"))
	}

	// Get the profile
	profile, etag, err := resource.server.GetProfile(resource.name)
	if err != nil {
		return err
	}

	// Set the configuration key
	keys, err := getConfig(args[1:]...)
	if err != nil {
		return err
	}

	writable := profile.Writable()
	if c.flagIsProperty {
		if cmd.Name() == "unset" {
			for k := range keys {
				err := unsetFieldByJsonTag(&writable, k)
				if err != nil {
					return fmt.Errorf(i18n.G("Error unsetting property: %v"), err)
				}
			}
		} else {
			err := unpackKVToWritable(&writable, keys)
			if err != nil {
				return fmt.Errorf(i18n.G("Error setting properties: %v"), err)
			}
		}
	} else {
		for k, v := range keys {
			writable.Config[k] = v
		}
	}

	return resource.server.UpdateProfile(resource.name, writable, etag)
}

// Show.
type cmdProfileShow struct {
	global  *cmdGlobal
	profile *cmdProfile
}

func (c *cmdProfileShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<profile>"))
	cmd.Short = i18n.G("Show profile configurations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show profile configurations`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("profile", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdProfileShow) run(cmd *cobra.Command, args []string) error {
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

	if resource.name == "" {
		return errors.New(i18n.G("Missing profile name"))
	}

	// Show the profile
	profile, _, err := resource.server.GetProfile(resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&profile)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Unset.
type cmdProfileUnset struct {
	global     *cmdGlobal
	profile    *cmdProfile
	profileSet *cmdProfileSet

	flagIsProperty bool
}

func (c *cmdProfileUnset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset", i18n.G("[<remote>:]<profile> <key>"))
	cmd.Short = i18n.G("Unset profile configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Unset profile configuration keys`))

	cmd.RunE = c.run
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a profile property"))

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("profile", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpProfileConfigs(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdProfileUnset) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	c.profileSet.flagIsProperty = c.flagIsProperty

	args = append(args, "")
	return c.profileSet.run(cmd, args)
}
