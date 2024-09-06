package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
	"github.com/canonical/lxd/shared/termios"
)

type cmdConfig struct {
	global *cmdGlobal

	flagTarget string
}

// Command creates a Cobra command for managing instance and server configurations,
// including options for device, edit, get, metadata, profile, set, show, template, trust, and unset.
func (c *cmdConfig) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("config")
	cmd.Short = i18n.G("Manage instance and server configuration options")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage instance and server configuration options`))

	// Device
	configDeviceCmd := cmdConfigDevice{global: c.global, config: c}
	cmd.AddCommand(configDeviceCmd.command())

	// Edit
	configEditCmd := cmdConfigEdit{global: c.global, config: c}
	cmd.AddCommand(configEditCmd.command())

	// Get
	configGetCmd := cmdConfigGet{global: c.global, config: c}
	cmd.AddCommand(configGetCmd.command())

	// Metadata
	configMetadataCmd := cmdConfigMetadata{global: c.global, config: c}
	cmd.AddCommand(configMetadataCmd.command())

	// Profile
	configProfileCmd := cmdProfile{global: c.global}
	profileCmd := configProfileCmd.command()
	profileCmd.Hidden = true
	profileCmd.Deprecated = i18n.G("please use `lxc profile`")
	cmd.AddCommand(profileCmd)

	// Set
	configSetCmd := cmdConfigSet{global: c.global, config: c}
	cmd.AddCommand(configSetCmd.command())

	// Show
	configShowCmd := cmdConfigShow{global: c.global, config: c}
	cmd.AddCommand(configShowCmd.command())

	// Template
	configTemplateCmd := cmdConfigTemplate{global: c.global, config: c}
	cmd.AddCommand(configTemplateCmd.command())

	// Trust
	configTrustCmd := cmdConfigTrust{global: c.global, config: c}
	cmd.AddCommand(configTrustCmd.command())

	// Unset
	configUnsetCmd := cmdConfigUnset{global: c.global, config: c, configSet: &configSetCmd}
	cmd.AddCommand(configUnsetCmd.command())

	// Uefi
	configUefiCmd := cmdConfigUefi{global: c.global, config: c}
	cmd.AddCommand(configUefiCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// Edit.
type cmdConfigEdit struct {
	global *cmdGlobal
	config *cmdConfig
}

// Command creates a Cobra command to edit instance or server configurations using YAML, with optional flags for targeting cluster members.
func (c *cmdConfigEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:][<instance>[/<snapshot>]]"))
	cmd.Short = i18n.G("Edit instance or server configurations as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit instance or server configurations as YAML`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc config edit <instance> < instance.yaml
    Update the instance configuration from config.yaml.`))

	cmd.Flags().StringVar(&c.config.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// helpTemplate returns a sample YAML configuration and guidelines for editing instance configurations.
func (c *cmdConfigEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the configuration.
### Any line starting with a '# will be ignored.
###
### A sample configuration looks like:
### name: instance1
### profiles:
### - default
### config:
###   volatile.eth0.hwaddr: 00:16:3e:e9:f8:7f
### devices:
###   homedir:
###     path: /extra
###     source: /home/user
###     type: disk
### ephemeral: false
###
### Note that the name is shown but cannot be changed`)
}

// Run executes the config edit command, allowing users to edit instance or server configurations via an interactive YAML editor.
func (c *cmdConfigEdit) run(cmd *cobra.Command, args []string) error {
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

	fields := strings.SplitN(resource.name, "/", 2)
	isSnapshot := len(fields) == 2

	// Edit the config
	if resource.name != "" {
		// Quick checks.
		if c.config.flagTarget != "" {
			return errors.New(i18n.G("--target cannot be used with instances"))
		}

		// If stdin isn't a terminal, read text from it
		if !termios.IsTerminal(getStdinFd()) {
			contents, err := io.ReadAll(os.Stdin)
			if err != nil {
				return err
			}

			var op lxd.Operation

			if isSnapshot {
				newdata := api.InstanceSnapshotPut{}

				err = yaml.Unmarshal(contents, &newdata)
				if err != nil {
					return err
				}

				op, err = resource.server.UpdateInstanceSnapshot(fields[0], fields[1], newdata, "")
				if err != nil {
					return err
				}
			} else {
				newdata := api.InstancePut{}
				err = yaml.Unmarshal(contents, &newdata)
				if err != nil {
					return err
				}

				op, err = resource.server.UpdateInstance(resource.name, newdata, "")
				if err != nil {
					return err
				}
			}

			return op.Wait()
		}

		var data []byte
		var etag string

		// Extract the current value
		if isSnapshot {
			var inst *api.InstanceSnapshot

			inst, etag, err = resource.server.GetInstanceSnapshot(fields[0], fields[1])
			if err != nil {
				return err
			}

			// Empty expanded config so it isn't shown in edit screen (relies on omitempty tag).
			inst.ExpandedConfig = nil
			inst.ExpandedDevices = nil

			data, err = yaml.Marshal(&inst)
			if err != nil {
				return err
			}
		} else {
			var inst *api.Instance

			inst, etag, err = resource.server.GetInstance(resource.name)
			if err != nil {
				return err
			}

			// Empty expanded config so it isn't shown in edit screen (relies on omitempty tag).
			inst.ExpandedConfig = nil
			inst.ExpandedDevices = nil

			data, err = yaml.Marshal(&inst)
			if err != nil {
				return err
			}
		}

		// Spawn the editor
		content, err := shared.TextEditor("", []byte(c.helpTemplate()+"\n\n"+string(data)))
		if err != nil {
			return err
		}

		for {
			// Parse the text received from the editor
			if isSnapshot {
				newdata := api.InstanceSnapshotPut{}
				err = yaml.Unmarshal(content, &newdata)
				if err == nil {
					var op lxd.Operation
					op, err = resource.server.UpdateInstanceSnapshot(fields[0], fields[1],
						newdata, etag)
					if err == nil {
						err = op.Wait()
					}
				}
			} else {
				newdata := api.InstancePut{}
				err = yaml.Unmarshal(content, &newdata)
				if err == nil {
					var op lxd.Operation
					op, err = resource.server.UpdateInstance(resource.name, newdata, etag)
					if err == nil {
						err = op.Wait()
					}
				}
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

	// Targeting
	if c.config.flagTarget != "" {
		if !resource.server.IsClustered() {
			return errors.New(i18n.G("To use --target, the destination remote must be a cluster"))
		}

		resource.server = resource.server.UseTarget(c.config.flagTarget)
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.ServerPut{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}

		return resource.server.UpdateServer(newdata, "")
	}

	// Extract the current value
	server, etag, err := resource.server.GetServer()
	if err != nil {
		return err
	}

	brief := server.Writable()
	data, err := yaml.Marshal(&brief)
	if err != nil {
		return err
	}

	// Spawn the editor
	content, err := shared.TextEditor("", data)
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor
		newdata := api.ServerPut{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = resource.server.UpdateServer(newdata, etag)
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
type cmdConfigGet struct {
	global *cmdGlobal
	config *cmdConfig

	flagExpanded   bool
	flagIsProperty bool
}

// Command creates a Cobra command to fetch values for given instance or server configuration keys,
// with optional flags for expanded configuration and cluster targeting.
func (c *cmdConfigGet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get", i18n.G("[<remote>:][<instance>] <key>"))
	cmd.Short = i18n.G("Get values for instance or server configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Get values for instance or server configuration keys`))

	cmd.Flags().BoolVarP(&c.flagExpanded, "expanded", "e", false, i18n.G("Access the expanded configuration"))
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as an instance property"))
	cmd.Flags().StringVar(&c.config.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpInstanceAllKeys(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run fetches and prints the specified configuration key's value for an instance or server, also handling target and expansion flags.
func (c *cmdConfigGet) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 2)
	if exit {
		return err
	}

	// Parse remote
	remote := ""
	if len(args) > 1 {
		remote = args[0]
	}

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]
	fields := strings.SplitN(resource.name, "/", 2)
	isSnapshot := len(fields) == 2

	// Get the config key
	if resource.name != "" {
		// Quick checks.
		if c.config.flagTarget != "" {
			return errors.New(i18n.G("--target cannot be used with instances"))
		}

		if isSnapshot {
			inst, _, err := resource.server.GetInstanceSnapshot(fields[0], fields[1])
			if err != nil {
				return err
			}

			if c.flagIsProperty {
				res, err := getFieldByJsonTag(inst, args[len(args)-1])
				if err != nil {
					return fmt.Errorf(i18n.G("The property %q does not exist on the instance snapshot %s/%s: %v"), args[len(args)-1], fields[0], fields[1], err)
				}

				fmt.Printf("%v\n", res)
			} else {
				if c.flagExpanded {
					fmt.Println(inst.ExpandedConfig[args[len(args)-1]])
				} else {
					fmt.Println(inst.Config[args[len(args)-1]])
				}
			}

			return nil
		}

		resp, _, err := resource.server.GetInstance(resource.name)
		if err != nil {
			return err
		}

		if c.flagIsProperty {
			w := resp.Writable()
			res, err := getFieldByJsonTag(&w, args[len(args)-1])
			if err != nil {
				return fmt.Errorf(i18n.G("The property %q does not exist on the instance %q: %v"), args[len(args)-1], resource.name, err)
			}

			fmt.Printf("%v\n", res)
		} else {
			if c.flagExpanded {
				fmt.Println(resp.ExpandedConfig[args[len(args)-1]])
			} else {
				fmt.Println(resp.Config[args[len(args)-1]])
			}
		}
	} else {
		// Quick check.
		if c.flagExpanded {
			return errors.New(i18n.G("--expanded cannot be used with a server"))
		}

		// Targeting
		if c.config.flagTarget != "" {
			if !resource.server.IsClustered() {
				return errors.New(i18n.G("To use --target, the destination remote must be a cluster"))
			}

			resource.server = resource.server.UseTarget(c.config.flagTarget)
		}

		resp, _, err := resource.server.GetServer()
		if err != nil {
			return err
		}

		value := resp.Config[args[len(args)-1]]
		if value == nil {
			value = ""
		} else if value == true { //nolint:revive
			value = "true"
		} else if value == false { //nolint:revive
			value = "false"
		}

		fmt.Println(value)
	}

	return nil
}

// Set.
type cmdConfigSet struct {
	global *cmdGlobal
	config *cmdConfig

	flagIsProperty bool
}

// Command creates a new Cobra command to set instance or server configuration keys and returns it.
func (c *cmdConfigSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set", i18n.G("[<remote>:][<instance>] <key>=<value>..."))
	cmd.Short = i18n.G("Set instance or server configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Set instance or server configuration keys

For backward compatibility, a single configuration key may still be set with:
    lxc config set [<remote>:][<instance>] <key> <value>`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc config set [<remote>:]<instance> limits.cpu=2
    Will set a CPU limit of "2" for the instance.

lxc config set core.https_address=[::]:8443
    Will have LXD listen on IPv4 and IPv6 port 8443.`))

	cmd.Flags().StringVar(&c.config.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as an instance property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpInstanceAllKeys(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run executes the "set" command, updating instance or server configuration keys based on provided arguments.
func (c *cmdConfigSet) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, -1)
	if exit {
		return err
	}

	hasKeyValue := func(args []string) bool {
		for _, arg := range args {
			if strings.Contains(arg, "=") {
				return true
			}
		}

		return false
	}

	onlyKeyValue := func(args []string) bool {
		for _, arg := range args {
			if !strings.Contains(arg, "=") {
				return false
			}
		}

		return true
	}

	isConfig := func(value string) bool {
		fields := strings.SplitN(value, ":", 2)
		key := fields[len(fields)-1]
		return strings.Contains(key, ".")
	}

	// Parse remote
	remote := ""
	if onlyKeyValue(args) || isConfig(args[0]) {
		// server set with: <key>=<value>...
		remote = ""
	} else if len(args) == 2 && !hasKeyValue(args) {
		// server set with: <key> <value>
		remote = ""
	} else {
		remote = args[0]
	}

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]
	fields := strings.SplitN(resource.name, "/", 2)
	isSnapshot := len(fields) == 2

	// Set the config keys
	if resource.name != "" {
		// Quick checks.
		if c.config.flagTarget != "" {
			return errors.New(i18n.G("--target cannot be used with instances"))
		}

		keys, err := getConfig(args[1:]...)
		if err != nil {
			return err
		}

		if isSnapshot {
			inst, etag, err := resource.server.GetInstanceSnapshot(fields[0], fields[1])
			if err != nil {
				return err
			}

			writable := inst.Writable()
			if c.flagIsProperty {
				if cmd.Name() == "unset" {
					for k := range keys {
						err := unsetFieldByJsonTag(&writable, k)
						if err != nil {
							return fmt.Errorf(i18n.G("Error unsetting properties: %v"), err)
						}
					}
				} else {
					err := unpackKVToWritable(&writable, keys)
					if err != nil {
						return fmt.Errorf(i18n.G("Error setting properties: %v"), err)
					}
				}

				op, err := resource.server.UpdateInstanceSnapshot(fields[0], fields[1], writable, etag)
				if err != nil {
					return err
				}

				return op.Wait()
			}

			return errors.New(i18n.G("There is no config key to set on an instance snapshot."))
		}

		inst, etag, err := resource.server.GetInstance(resource.name)
		if err != nil {
			return err
		}

		writable := inst.Writable()
		if c.flagIsProperty {
			if cmd.Name() == "unset" {
				for k := range keys {
					err := unsetFieldByJsonTag(&writable, k)
					if err != nil {
						return fmt.Errorf(i18n.G("Error unsetting properties: %v"), err)
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
				if cmd.Name() == "unset" {
					_, ok := writable.Config[k]
					if !ok {
						return fmt.Errorf(i18n.G("Can't unset key '%s', it's not currently set"), k)
					}

					delete(writable.Config, k)
				} else {
					writable.Config[k] = v
				}
			}
		}

		op, err := resource.server.UpdateInstance(resource.name, writable, etag)
		if err != nil {
			return err
		}

		return op.Wait()
	}

	// Targeting
	if c.config.flagTarget != "" {
		if !resource.server.IsClustered() {
			return errors.New(i18n.G("To use --target, the destination remote must be a cluster"))
		}

		resource.server = resource.server.UseTarget(c.config.flagTarget)
	}

	// Server keys
	server, etag, err := resource.server.GetServer()
	if err != nil {
		return err
	}

	var keys map[string]string
	if remote == "" {
		keys, err = getConfig(args[0:]...)
		if err != nil {
			return err
		}
	} else {
		keys, err = getConfig(args[1:]...)
		if err != nil {
			return err
		}
	}

	if server.Config == nil {
		server.Config = map[string]any{}
	}

	for k, v := range keys {
		server.Config[k] = v
	}

	return resource.server.UpdateServer(server.Writable(), etag)
}

// Show.
type cmdConfigShow struct {
	global *cmdGlobal
	config *cmdConfig

	flagExpanded bool
}

// Command sets up the "show" command, which displays instance or server configurations based on the provided arguments.
func (c *cmdConfigShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:][<instance>[/<snapshot>]]"))
	cmd.Short = i18n.G("Show instance or server configurations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show instance or server configurations`))

	cmd.Flags().BoolVarP(&c.flagExpanded, "expanded", "e", false, i18n.G("Show the expanded configuration"))
	cmd.Flags().StringVar(&c.config.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpInstances(toComplete)
	}

	return cmd
}

// Run executes the "show" command, displaying the YAML-formatted configuration of a specified server or instance.
func (c *cmdConfigShow) run(cmd *cobra.Command, args []string) error {
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

	// Show configuration
	var data []byte

	if resource.name == "" {
		// Quick check.
		if c.flagExpanded {
			return errors.New(i18n.G("--expanded cannot be used with a server"))
		}

		// Targeting
		if c.config.flagTarget != "" {
			if !resource.server.IsClustered() {
				return errors.New(i18n.G("To use --target, the destination remote must be a cluster"))
			}

			resource.server = resource.server.UseTarget(c.config.flagTarget)
		}

		// Server config
		server, _, err := resource.server.GetServer()
		if err != nil {
			return err
		}

		brief := server.Writable()
		data, err = yaml.Marshal(&brief)
		if err != nil {
			return err
		}
	} else {
		// Quick checks.
		if c.config.flagTarget != "" {
			return errors.New(i18n.G("--target cannot be used with instances"))
		}

		// Instance or snapshot config
		var brief any

		if shared.IsSnapshot(resource.name) {
			// Snapshot
			fields := strings.Split(resource.name, shared.SnapshotDelimiter)

			snap, _, err := resource.server.GetInstanceSnapshot(fields[0], fields[1])
			if err != nil {
				return err
			}

			brief = snap
			if c.flagExpanded {
				brief.(*api.InstanceSnapshot).Config = snap.ExpandedConfig
				brief.(*api.InstanceSnapshot).Devices = snap.ExpandedDevices
			}
		} else {
			// Instance
			inst, _, err := resource.server.GetInstance(resource.name)
			if err != nil {
				return err
			}

			writable := inst.Writable()
			brief = &writable

			if c.flagExpanded {
				brief.(*api.InstancePut).Config = inst.ExpandedConfig
				brief.(*api.InstancePut).Devices = inst.ExpandedDevices
			}
		}

		data, err = yaml.Marshal(&brief)
		if err != nil {
			return err
		}
	}

	fmt.Printf("%s", data)

	return nil
}

// Unset.
type cmdConfigUnset struct {
	global    *cmdGlobal
	config    *cmdConfig
	configSet *cmdConfigSet

	flagIsProperty bool
}

// Command generates a new "unset" command to remove specific configuration keys for an instance or server.
func (c *cmdConfigUnset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset", i18n.G("[<remote>:][<instance>] <key>"))
	cmd.Short = i18n.G("Unset instance or server configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Unset instance or server configuration keys`))

	cmd.Flags().StringVar(&c.config.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as an instance property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpInstanceAllKeys(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run executes the "unset" command, delegating to the "set" command to remove specific configuration keys.
func (c *cmdConfigUnset) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 2)
	if exit {
		return err
	}

	c.configSet.flagIsProperty = c.flagIsProperty

	args = append(args, "")
	return c.configSet.run(cmd, args)
}

type cmdConfigUefi struct {
	global *cmdGlobal
	config *cmdConfig
}

// Command creates a Cobra command for managing virtual machine instance UEFI variables,
// including options for get, set, unset, show, edit.
func (c *cmdConfigUefi) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("uefi")
	cmd.Short = i18n.G("Manage instance UEFI variables")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage instance UEFI variables`))

	// Get
	configUefiGetCmd := cmdConfigUefiGet{global: c.global, configUefi: c}
	cmd.AddCommand(configUefiGetCmd.command())

	// Set
	configUefiSetCmd := cmdConfigUefiSet{global: c.global, configUefi: c}
	cmd.AddCommand(configUefiSetCmd.command())

	// Unset
	configUefiUnsetCmd := cmdConfigUefiUnset{global: c.global, configUefi: c, configSet: &configUefiSetCmd}
	cmd.AddCommand(configUefiUnsetCmd.command())

	// Show
	configUefiShowCmd := cmdConfigUefiShow{global: c.global, configUefi: c}
	cmd.AddCommand(configUefiShowCmd.command())

	// Edit
	configUefiEditCmd := cmdConfigUefiEdit{global: c.global, configUefi: c}
	cmd.AddCommand(configUefiEditCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// Get.
type cmdConfigUefiGet struct {
	global     *cmdGlobal
	configUefi *cmdConfigUefi
}

// Command creates a Cobra command to fetch virtual machine instance UEFI variables.
func (c *cmdConfigUefiGet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get", i18n.G("[<remote>:]<instance> <key>"))
	cmd.Short = i18n.G("Get UEFI variables for instance")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Get UEFI variables for instance`))

	cmd.RunE = c.run

	return cmd
}

// Run fetches and prints the specified UEFI variable's value.
func (c *cmdConfigUefiGet) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote
	remote := args[0]
	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]
	if resource.name == "" {
		return errors.New(i18n.G("Instance name must be specified"))
	}

	// Get the UEFI variable
	resp, _, err := resource.server.GetInstanceUEFIVars(resource.name)
	if err != nil {
		return err
	}

	efiVariable, ok := resp.Variables[args[len(args)-1]]
	if !ok {
		return errors.New(i18n.G("Requested UEFI variable does not exist"))
	}

	fmt.Println(efiVariable.Data)

	return nil
}

// Set.
type cmdConfigUefiSet struct {
	global     *cmdGlobal
	configUefi *cmdConfigUefi
}

// Command creates a new Cobra command to set virtual machine instance UEFI variables.
func (c *cmdConfigUefiSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set", i18n.G("[<remote>:]<instance> <key>=<value>..."))
	cmd.Short = i18n.G("Set UEFI variables for instance")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Set UEFI variables for instance`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc config uefi set [<remote>:]<instance> testvar-9073e4e0-60ec-4b6e-9903-4c223c260f3c=aabb
    Set a UEFI variable with name "testvar", GUID 9073e4e0-60ec-4b6e-9903-4c223c260f3c and value "aabb" (HEX-encoded) for the instance.`))

	cmd.RunE = c.run

	return cmd
}

// Run executes the "set" command, updating virtual machine instance UEFI variables based on provided arguments.
func (c *cmdConfigUefiSet) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, -1)
	if exit {
		return err
	}

	// Parse remote
	remote := args[0]
	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]
	if resource.name == "" {
		return errors.New(i18n.G("Instance name must be specified"))
	}

	// Set the config keys
	keys, err := getConfig(args[1:]...)
	if err != nil {
		return err
	}

	instUEFI, etag, err := resource.server.GetInstanceUEFIVars(resource.name)
	if err != nil {
		return err
	}

	for k, v := range keys {
		if cmd.Name() == "unset" {
			_, ok := instUEFI.Variables[k]
			if !ok {
				return fmt.Errorf(i18n.G("Can't unset key '%s', it's not currently set"), k)
			}

			delete(instUEFI.Variables, k)
		} else {
			uefiVar, ok := instUEFI.Variables[k]

			// Initialize UEFI variable attributes with the default value
			if !ok {
				uefiVar = api.InstanceUEFIVariable{
					// The following attribute mask is used for UEFI variables
					// BootOrder, DriverOrder, BootNext (and many others):
					// EFI_VARIABLE_NON_VOLATILE | EFI_VARIABLE_BOOTSERVICE_ACCESS | EFI_VARIABLE_RUNTIME_ACCESS
					// Let's set it as a default attribute value in case when user
					// sets a new UEFI variable.
					Attr: 7,
				}
			}

			uefiVar.Data = v

			instUEFI.Variables[k] = uefiVar
		}
	}

	err = resource.server.UpdateInstanceUEFIVars(resource.name, *instUEFI, etag)
	if err != nil {
		return err
	}

	return nil
}

// Unset.
type cmdConfigUefiUnset struct {
	global     *cmdGlobal
	configUefi *cmdConfigUefi
	configSet  *cmdConfigUefiSet
}

// Command generates a new "unset" command to remove specific virtual machine instance UEFI variable.
func (c *cmdConfigUefiUnset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset", i18n.G("[<remote>:]<instance> <key>"))
	cmd.Short = i18n.G("Unset UEFI variables for instance")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Unset UEFI variables for instance`))

	cmd.RunE = c.run

	return cmd
}

// Run executes the "unset" command, delegating to the "set" command to remove specific UEFI variable.
func (c *cmdConfigUefiUnset) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	args = append(args, "")
	return c.configSet.run(cmd, args)
}

// Show.
type cmdConfigUefiShow struct {
	global     *cmdGlobal
	configUefi *cmdConfigUefi
}

// Command sets up the "show" command, which displays virtual machine instance UEFI variables based on the provided arguments.
func (c *cmdConfigUefiShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<instance>"))
	cmd.Short = i18n.G("Show instance UEFI variables")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show instance UEFI variables`))

	cmd.RunE = c.run

	return cmd
}

// Run executes the "show" command, displaying the YAML-formatted configuration of a virtual machine instance UEFI variables.
func (c *cmdConfigUefiShow) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	remote := args[0]
	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]
	if resource.name == "" {
		return errors.New(i18n.G("Instance name must be specified"))
	}

	instEFI, _, err := resource.server.GetInstanceUEFIVars(resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&instEFI)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Edit.
type cmdConfigUefiEdit struct {
	global     *cmdGlobal
	configUefi *cmdConfigUefi
}

// Command creates a Cobra command to edit virtual machine instance UEFI variables.
func (c *cmdConfigUefiEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<instance>"))
	cmd.Short = i18n.G("Edit instance UEFI variables")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit instance UEFI variables`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc config uefi edit <instance> < instance_uefi_vars.yaml
    Set the instance UEFI variables from instance_uefi_vars.yaml.`))

	cmd.RunE = c.run

	return cmd
}

// helpTemplate returns a sample YAML UEFI variables configuration.
func (c *cmdConfigUefiEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the UEFI variables configuration.
### Any line starting with a '# will be ignored.
###
### A sample UEFI variables configuration looks like:
### variables:
###   00163E0BD47A-5b446ed1-e30b-4faa-871a-3654eca36080:
###     data: 3a5001001000afaf040000000100000000000000
###     attr: 3
###     timestamp: ""
###     digest: ""
###   00163E0BD47A-937fe521-95ae-4d1a-8929-48bcd90ad31a:
###     data: df7f3f
###     attr: 3
###     timestamp: ""
###     digest: ""
###   BootOrder-8be4df61-93ca-11d2-aa0d-00e098032b8c:
###     data: "07000100020003000400050000000600"
###     attr: 7
###     timestamp: ""
###     digest: ""
###   ClientId-9fb9a8a1-2f4a-43a6-889c-d0f7b6c47ad5:
###     data: 0e00000100012cb0289c00163e0bd47a
###     attr: 3
###     timestamp: ""
###     digest: ""
###
### Note that the format of the key in the variables map is "<EFI variable name>-<UUID>".
### Fields "data", "timestamp", "digest" are HEX-encoded.
### Field "attr" is an unsigned 32-bit integer.
###`)
}

// Run executes the config edit command, allowing users to edit virtual machine instance UEFI variables via an interactive YAML editor.
func (c *cmdConfigUefiEdit) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	remote := args[0]
	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]
	if resource.name == "" {
		return errors.New(i18n.G("Instance name must be specified"))
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newUEFIVarsSet := api.InstanceUEFIVars{}
		err = yaml.Unmarshal(contents, &newUEFIVarsSet)
		if err != nil {
			return err
		}

		err = resource.server.UpdateInstanceUEFIVars(resource.name, newUEFIVarsSet, "")
		if err != nil {
			return err
		}

		return nil
	}

	instEFI, etag, err := resource.server.GetInstanceUEFIVars(resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&instEFI)
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
		newUEFIVarsSet := api.InstanceUEFIVars{}
		err = yaml.Unmarshal(content, &newUEFIVarsSet)
		if err == nil {
			err = resource.server.UpdateInstanceUEFIVars(resource.name, newUEFIVarsSet, etag)
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
