package main

import (
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

type cmdProfile struct {
	global *cmdGlobal
}

// Command() orchestrates profile management subcommands in a cobra.Command object.
func (c *cmdProfile) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("profile")
	cmd.Short = i18n.G("Manage profiles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage profiles`))

	// Add
	profileAddCmd := cmdProfileAdd{global: c.global, profile: c}
	cmd.AddCommand(profileAddCmd.Command())

	// Assign
	profileAssignCmd := cmdProfileAssign{global: c.global, profile: c}
	cmd.AddCommand(profileAssignCmd.Command())

	// Copy
	profileCopyCmd := cmdProfileCopy{global: c.global, profile: c}
	cmd.AddCommand(profileCopyCmd.Command())

	// Create
	profileCreateCmd := cmdProfileCreate{global: c.global, profile: c}
	cmd.AddCommand(profileCreateCmd.Command())

	// Delete
	profileDeleteCmd := cmdProfileDelete{global: c.global, profile: c}
	cmd.AddCommand(profileDeleteCmd.Command())

	// Device
	profileDeviceCmd := cmdConfigDevice{global: c.global, profile: c}
	cmd.AddCommand(profileDeviceCmd.Command())

	// Edit
	profileEditCmd := cmdProfileEdit{global: c.global, profile: c}
	cmd.AddCommand(profileEditCmd.Command())

	// Get
	profileGetCmd := cmdProfileGet{global: c.global, profile: c}
	cmd.AddCommand(profileGetCmd.Command())

	// List
	profileListCmd := cmdProfileList{global: c.global, profile: c}
	cmd.AddCommand(profileListCmd.Command())

	// Remove
	profileRemoveCmd := cmdProfileRemove{global: c.global, profile: c}
	cmd.AddCommand(profileRemoveCmd.Command())

	// Rename
	profileRenameCmd := cmdProfileRename{global: c.global, profile: c}
	cmd.AddCommand(profileRenameCmd.Command())

	// Set
	profileSetCmd := cmdProfileSet{global: c.global, profile: c}
	cmd.AddCommand(profileSetCmd.Command())

	// Show
	profileShowCmd := cmdProfileShow{global: c.global, profile: c}
	cmd.AddCommand(profileShowCmd.Command())

	// Unset
	profileUnsetCmd := cmdProfileUnset{global: c.global, profile: c, profileSet: &profileSetCmd}
	cmd.AddCommand(profileUnsetCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// Add.
type cmdProfileAdd struct {
	global  *cmdGlobal
	profile *cmdProfile
}

// Command() manages the 'add' subcommand to append profiles to specified instances.
func (c *cmdProfileAdd) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("add", i18n.G("[<remote>:]<instance> <profile>"))
	cmd.Short = i18n.G("Add profiles to instances")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Add profiles to instances`))

	cmd.RunE = c.Run

	return cmd
}

// Run() executes the 'add' subcommand logic, appending a specified profile to a given instance and optionally returning a message.
func (c *cmdProfileAdd) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing instance name"))
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

// Command() creates the 'assign' subcommand to set or reset the profile associations for a given instance.
func (c *cmdProfileAssign) Command() *cobra.Command {
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

	cmd.RunE = c.Run

	return cmd
}

// Run() executes the 'assign' subcommand, updating the associated profiles of a given instance.
func (c *cmdProfileAssign) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing instance name"))
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

// Command() returns the 'copy' subcommand, allowing the copying of profiles between different instances or projects.
func (c *cmdProfileCopy) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("copy", i18n.G("[<remote>:]<profile> [<remote>:]<profile>"))
	cmd.Aliases = []string{"cp"}
	cmd.Short = i18n.G("Copy profiles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Copy profiles`))
	cmd.Flags().StringVar(&c.flagTargetProject, "target-project", "", i18n.G("Copy to a project different from the source")+"``")
	cmd.Flags().BoolVar(&c.flagRefresh, "refresh", false, i18n.G("Update the target profile from the source if it already exists"))

	cmd.RunE = c.Run

	return cmd
}

// Run() executes the 'copy' subcommand, which retrieves the source profile and creates a copy of it in the desired destination.
func (c *cmdProfileCopy) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing source profile name"))
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

// Command() returns a Cobra command for 'create', which initializes a new profile on the specified remote.
func (c *cmdProfileCreate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", i18n.G("[<remote>:]<profile>"))
	cmd.Short = i18n.G("Create profiles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Create profiles`))

	cmd.RunE = c.Run

	return cmd
}

// Run() executes the profile creation command, creating a new profile on the remote server with the specified name.
func (c *cmdProfileCreate) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing profile name"))
	}

	// Create the profile
	profile := api.ProfilesPost{}
	profile.Name = resource.name

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

// Command() sets up the 'profile delete' command, which is used to delete a specific profile from a remote server.
func (c *cmdProfileDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<profile>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete profiles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Delete profiles`))

	cmd.RunE = c.Run

	return cmd
}

// Run() executes the 'profile delete' command, deleting the specified profile from the remote server.
func (c *cmdProfileDelete) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing profile name"))
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

// Command() generates the 'profile edit' command, enabling users to modify profile configurations using YAML.
func (c *cmdProfileEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<profile>"))
	cmd.Short = i18n.G("Edit profile configurations as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit profile configurations as YAML`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc profile edit <profile> < profile.yaml
    Update a profile using the content of profile.yaml`))

	cmd.RunE = c.Run

	return cmd
}

// helpTemplate() returns a string representation of a YAML profile template, serving as a guide for profile creation and modification.
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

// Run() method for cmdProfileEdit, allowing users to edit the YAML configuration of a profile, either directly or using an interactive text editor.
func (c *cmdProfileEdit) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing profile name"))
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

// Command() method for cmdProfileGet, creates a cobra command to retrieve the values of specified profile configuration keys.
func (c *cmdProfileGet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get", i18n.G("[<remote>:]<profile> <key>"))
	cmd.Short = i18n.G("Get values for profile configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Get values for profile configuration keys`))

	cmd.RunE = c.Run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a profile property"))
	return cmd
}

// Run() method for cmdProfileGet, fetches and prints the value for a specified configuration key from a given profile.
func (c *cmdProfileGet) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing profile name"))
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
	global     *cmdGlobal
	profile    *cmdProfile
	flagFormat string
}

// Command() method for cmdProfileList, provides a command that lists the profiles with support for multiple output formats.
func (c *cmdProfileList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List profiles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List profiles`))

	cmd.RunE = c.Run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")

	return cmd
}

// Run() method for cmdProfileList, retrieves the list of profiles and displays them in the selected output format.
func (c *cmdProfileList) Run(cmd *cobra.Command, args []string) error {
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
	profiles, err := resource.server.GetProfiles()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, profile := range profiles {
		strUsedBy := fmt.Sprintf("%d", len(profile.UsedBy))
		data = append(data, []string{profile.Name, profile.Description, strUsedBy})
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		i18n.G("NAME"),
		i18n.G("DESCRIPTION"),
		i18n.G("USED BY")}

	return cli.RenderTable(c.flagFormat, header, data, profiles)
}

// Remove.
type cmdProfileRemove struct {
	global  *cmdGlobal
	profile *cmdProfile
}

// Generates a cobra.Command for cmdProfileRemove, which removes a specified profile from an instance.
func (c *cmdProfileRemove) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("remove", i18n.G("[<remote>:]<instance> <profile>"))
	cmd.Short = i18n.G("Remove profiles from instances")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Remove profiles from instances`))

	cmd.RunE = c.Run

	return cmd
}

// Implements the execution of cmdProfileRemove, effectively removing a specified profile from a given instance.
func (c *cmdProfileRemove) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing instance name"))
	}

	// Remove the profile
	inst, etag, err := resource.server.GetInstance(resource.name)
	if err != nil {
		return err
	}

	if !shared.StringInSlice(args[1], inst.Profiles) {
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

// Defines cmdProfileRename command, enabling renaming of existing profiles in the remote.
func (c *cmdProfileRename) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rename", i18n.G("[<remote>:]<profile> <new-name>"))
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename profiles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Rename profiles`))

	cmd.RunE = c.Run

	return cmd
}

// Executes profile renaming process, handling checks, remote parsing, renaming operation, and feedback to the user.
func (c *cmdProfileRename) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing profile name"))
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

// Generates a command for setting profile configuration keys, supporting both single and multiple key-value pair changes.
func (c *cmdProfileSet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set", i18n.G("[<remote>:]<profile> <key><value>..."))
	cmd.Short = i18n.G("Set profile configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Set profile configuration keys

For backward compatibility, a single configuration key may still be set with:
    lxc profile set [<remote>:]<profile> <key> <value>`))

	cmd.RunE = c.Run
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a profile property"))
	return cmd
}

// Updates profile configuration using provided key-value pairs, supporting both single and multiple key-value pair updates.
func (c *cmdProfileSet) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing profile name"))
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

// Initializes and returns a command that displays the configurations of a specified profile.
func (c *cmdProfileShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<profile>"))
	cmd.Short = i18n.G("Show profile configurations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show profile configurations`))

	cmd.RunE = c.Run

	return cmd
}

// Retrieves and prints the YAML representation of a specified profile's configuration.
func (c *cmdProfileShow) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing profile name"))
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

// Creates a command that unsets a specified profile configuration key.
func (c *cmdProfileUnset) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset", i18n.G("[<remote>:]<profile> <key>"))
	cmd.Short = i18n.G("Unset profile configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Unset profile configuration keys`))

	cmd.RunE = c.Run
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a profile property"))

	return cmd
}

// Executes the unset command by calling the set method with a blank value.
func (c *cmdProfileUnset) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	c.profileSet.flagIsProperty = c.flagIsProperty

	args = append(args, "")
	return c.profileSet.Run(cmd, args)
}
