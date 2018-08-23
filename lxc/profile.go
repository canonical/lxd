package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"strings"
	"syscall"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/termios"
)

type cmdProfile struct {
	global *cmdGlobal
}

func (c *cmdProfile) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("profile")
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

	return cmd
}

// Add
type cmdProfileAdd struct {
	global  *cmdGlobal
	profile *cmdProfile
}

func (c *cmdProfileAdd) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("add [<remote>:]<container> <profile>")
	cmd.Short = i18n.G("Add profiles to containers")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Add profiles to containers`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdProfileAdd) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
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
		return fmt.Errorf(i18n.G("Missing container.name name"))
	}

	// Add the profile
	container, etag, err := resource.server.GetContainer(resource.name)
	if err != nil {
		return err
	}

	container.Profiles = append(container.Profiles, args[1])

	op, err := resource.server.UpdateContainer(resource.name, container.Writable(), etag)
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

// Assign
type cmdProfileAssign struct {
	global  *cmdGlobal
	profile *cmdProfile
}

func (c *cmdProfileAssign) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("assign [<remote>:]<container> <profiles>")
	cmd.Aliases = []string{"apply"}
	cmd.Short = i18n.G("Assign sets of profiles to containers")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Assign sets of profiles to containers`))
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

func (c *cmdProfileAssign) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
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
		return fmt.Errorf(i18n.G("Missing container name"))
	}

	container, etag, err := resource.server.GetContainer(resource.name)
	if err != nil {
		return err
	}

	if args[1] != "" {
		container.Profiles = strings.Split(args[1], ",")
	} else {
		container.Profiles = nil
	}

	op, err := resource.server.UpdateContainer(resource.name, container.Writable(), etag)
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

// Copy
type cmdProfileCopy struct {
	global  *cmdGlobal
	profile *cmdProfile
}

func (c *cmdProfileCopy) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("copy [<remote>:]<profile> [<remote>:]<profile>")
	cmd.Aliases = []string{"cp"}
	cmd.Short = i18n.G("Copy profiles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Copy profiles`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdProfileCopy) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
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

	newProfile := api.ProfilesPost{
		ProfilePut: profile.Writable(),
		Name:       dest.name,
	}

	return dest.server.CreateProfile(newProfile)
}

// Create
type cmdProfileCreate struct {
	global  *cmdGlobal
	profile *cmdProfile
}

func (c *cmdProfileCreate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("create [<remote>:]<profile>")
	cmd.Short = i18n.G("Create profiles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Create profiles`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdProfileCreate) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
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

// Delete
type cmdProfileDelete struct {
	global  *cmdGlobal
	profile *cmdProfile
}

func (c *cmdProfileDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("delete [<remote>:]<profile>")
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete profiles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Delete profiles`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdProfileDelete) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
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

// Edit
type cmdProfileEdit struct {
	global  *cmdGlobal
	profile *cmdProfile
}

func (c *cmdProfileEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("edit [<remote>:]<profile>")
	cmd.Short = i18n.G("Edit profile configurations as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit profile configurations as YAML`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc profile edit <profile> < profile.yaml
    Update a profile using the content of profile.yaml`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdProfileEdit) helpTemplate() string {
	return i18n.G(
		`### This is a yaml representation of the profile.
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

func (c *cmdProfileEdit) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
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
	if !termios.IsTerminal(int(syscall.Stdin)) {
		contents, err := ioutil.ReadAll(os.Stdin)
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
			fmt.Println(i18n.G("Press enter to open the editor again"))

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

// Get
type cmdProfileGet struct {
	global  *cmdGlobal
	profile *cmdProfile
}

func (c *cmdProfileGet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("get [<remote>:]<profile> <key>")
	cmd.Short = i18n.G("Get values for profile configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Get values for profile configuration keys`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdProfileGet) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
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

	fmt.Printf("%s\n", profile.Config[args[1]])
	return nil
}

// List
type cmdProfileList struct {
	global  *cmdGlobal
	profile *cmdProfile
}

func (c *cmdProfileList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("list [<remote>:]")
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List profiles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List profiles`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdProfileList) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
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
		data = append(data, []string{profile.Name, strUsedBy})
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetAutoWrapText(false)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetRowLine(true)
	table.SetHeader([]string{
		i18n.G("NAME"),
		i18n.G("USED BY")})
	sort.Sort(byName(data))
	table.AppendBulk(data)
	table.Render()

	return nil
}

// Remove
type cmdProfileRemove struct {
	global  *cmdGlobal
	profile *cmdProfile
}

func (c *cmdProfileRemove) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("remove [<remote>:]<container> <profile>")
	cmd.Short = i18n.G("Remove profiles from containers")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Remove profiles from containers`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdProfileRemove) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
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
		return fmt.Errorf(i18n.G("Missing container name"))
	}

	// Remove the profile
	container, etag, err := resource.server.GetContainer(resource.name)
	if err != nil {
		return err
	}

	if !shared.StringInSlice(args[1], container.Profiles) {
		return fmt.Errorf(i18n.G("Profile %s isn't currently applied to %s"), args[1], resource.name)
	}

	profiles := []string{}
	for _, profile := range container.Profiles {
		if profile == args[1] {
			continue
		}

		profiles = append(profiles, profile)
	}

	container.Profiles = profiles

	op, err := resource.server.UpdateContainer(resource.name, container.Writable(), etag)
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

// Rename
type cmdProfileRename struct {
	global  *cmdGlobal
	profile *cmdProfile
}

func (c *cmdProfileRename) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("rename [<remote>:]<profile> <new-name>")
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename profiles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Rename profiles`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdProfileRename) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
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

// Set
type cmdProfileSet struct {
	global  *cmdGlobal
	profile *cmdProfile
}

func (c *cmdProfileSet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("set [<remote>:]<profile> <key> <value>")
	cmd.Short = i18n.G("Set profile configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Set profile configuration keys`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdProfileSet) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
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
		return fmt.Errorf(i18n.G("Missing profile name"))
	}

	// Set the configuration key
	key := args[1]
	value := args[2]

	if !termios.IsTerminal(int(syscall.Stdin)) && value == "-" {
		buf, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf(i18n.G("Can't read from stdin: %s"), err)
		}
		value = string(buf[:])
	}

	profile, etag, err := resource.server.GetProfile(resource.name)
	if err != nil {
		return err
	}

	profile.Config[key] = value

	return resource.server.UpdateProfile(resource.name, profile.Writable(), etag)
}

// Show
type cmdProfileShow struct {
	global  *cmdGlobal
	profile *cmdProfile
}

func (c *cmdProfileShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("show [<remote>:]<profile>")
	cmd.Short = i18n.G("Show profile configurations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show profile configurations`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdProfileShow) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
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

// Unset
type cmdProfileUnset struct {
	global     *cmdGlobal
	profile    *cmdProfile
	profileSet *cmdProfileSet
}

func (c *cmdProfileUnset) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("unset [<remote>:]<profile> <key>")
	cmd.Short = i18n.G("Unset profile configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Unset profile configuration keys`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdProfileUnset) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	args = append(args, "")
	return c.profileSet.Run(cmd, args)
}
