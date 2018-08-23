package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
)

type cmdConfigDevice struct {
	global  *cmdGlobal
	config  *cmdConfig
	profile *cmdProfile
}

func (c *cmdConfigDevice) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("device")
	cmd.Short = i18n.G("Manage container devices")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage container devices`))

	// Add
	configDeviceAddCmd := cmdConfigDeviceAdd{global: c.global, config: c.config, profile: c.profile, configDevice: c}
	cmd.AddCommand(configDeviceAddCmd.Command())

	// Get
	configDeviceGetCmd := cmdConfigDeviceGet{global: c.global, config: c.config, profile: c.profile, configDevice: c}
	cmd.AddCommand(configDeviceGetCmd.Command())

	// List
	configDeviceListCmd := cmdConfigDeviceList{global: c.global, config: c.config, profile: c.profile, configDevice: c}
	cmd.AddCommand(configDeviceListCmd.Command())

	// Override
	if c.config != nil {
		configDeviceOverrideCmd := cmdConfigDeviceOverride{global: c.global, config: c.config, profile: c.profile, configDevice: c}
		cmd.AddCommand(configDeviceOverrideCmd.Command())
	}

	// Remove
	configDeviceRemoveCmd := cmdConfigDeviceRemove{global: c.global, config: c.config, profile: c.profile, configDevice: c}
	cmd.AddCommand(configDeviceRemoveCmd.Command())

	// Set
	configDeviceSetCmd := cmdConfigDeviceSet{global: c.global, config: c.config, profile: c.profile, configDevice: c}
	cmd.AddCommand(configDeviceSetCmd.Command())

	// Show
	configDeviceShowCmd := cmdConfigDeviceShow{global: c.global, config: c.config, profile: c.profile, configDevice: c}
	cmd.AddCommand(configDeviceShowCmd.Command())

	// Unset
	configDeviceUnsetCmd := cmdConfigDeviceUnset{global: c.global, config: c.config, profile: c.profile, configDevice: c, configDeviceSet: &configDeviceSetCmd}
	cmd.AddCommand(configDeviceUnsetCmd.Command())

	return cmd
}

// Add
type cmdConfigDeviceAdd struct {
	global       *cmdGlobal
	config       *cmdConfig
	configDevice *cmdConfigDevice
	profile      *cmdProfile
}

func (c *cmdConfigDeviceAdd) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("add [<remote>:]<container|profile> <device> <type> [key=value...]")
	cmd.Short = i18n.G("Add devices to containers or profiles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Add devices to containers or profiles`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc config device add [<remote>:]container1 <device-name> disk source=/share/c1 path=opt
    Will mount the host's /share/c1 onto /opt in the container.`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigDeviceAdd) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 3, -1)
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
		return fmt.Errorf(i18n.G("Missing name"))
	}

	// Add the device
	devname := args[1]
	device := map[string]string{}
	device["type"] = args[2]
	if len(args) > 3 {
		for _, prop := range args[3:] {
			results := strings.SplitN(prop, "=", 2)
			if len(results) != 2 {
				return fmt.Errorf(i18n.G("No value found in %q"), prop)
			}
			k := results[0]
			v := results[1]
			device[k] = v
		}
	}

	if c.profile != nil {
		profile, etag, err := resource.server.GetProfile(resource.name)
		if err != nil {
			return err
		}

		_, ok := profile.Devices[devname]
		if ok {
			return fmt.Errorf(i18n.G("The device already exists"))
		}

		profile.Devices[devname] = device

		err = resource.server.UpdateProfile(resource.name, profile.Writable(), etag)
		if err != nil {
			return err
		}
	} else {
		container, etag, err := resource.server.GetContainer(resource.name)
		if err != nil {
			return err
		}

		_, ok := container.Devices[devname]
		if ok {
			return fmt.Errorf(i18n.G("The device already exists"))
		}

		container.Devices[devname] = device

		op, err := resource.server.UpdateContainer(resource.name, container.Writable(), etag)
		if err != nil {
			return err
		}

		err = op.Wait()
		if err != nil {
			return err
		}
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Device %s added to %s")+"\n", devname, resource.name)
	}

	return nil
}

// Get
type cmdConfigDeviceGet struct {
	global       *cmdGlobal
	config       *cmdConfig
	configDevice *cmdConfigDevice
	profile      *cmdProfile
}

func (c *cmdConfigDeviceGet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("get [<remote>:]<container|profile> <device> <key>")
	cmd.Short = i18n.G("Get values for container device configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Get values for container device configuration keys`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigDeviceGet) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing name"))
	}

	// Get the config key
	devname := args[1]
	key := args[2]

	if c.profile != nil {
		profile, _, err := resource.server.GetProfile(resource.name)
		if err != nil {
			return err
		}

		dev, ok := profile.Devices[devname]
		if !ok {
			return fmt.Errorf(i18n.G("The device doesn't exist"))
		}

		fmt.Println(dev[key])
	} else {
		container, _, err := resource.server.GetContainer(resource.name)
		if err != nil {
			return err
		}

		dev, ok := container.Devices[devname]
		if !ok {
			return fmt.Errorf(i18n.G("The device doesn't exist"))
		}

		fmt.Println(dev[key])
	}

	return nil
}

// List
type cmdConfigDeviceList struct {
	global       *cmdGlobal
	config       *cmdConfig
	configDevice *cmdConfigDevice
	profile      *cmdProfile
}

func (c *cmdConfigDeviceList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("list [<remote>:]<container|profile>")
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List container devices")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List container devices`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigDeviceList) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing name"))
	}

	// List the devices
	var devices []string
	if c.profile != nil {
		profile, _, err := resource.server.GetProfile(resource.name)
		if err != nil {
			return err
		}

		for k := range profile.Devices {
			devices = append(devices, k)
		}
	} else {
		container, _, err := resource.server.GetContainer(resource.name)
		if err != nil {
			return err
		}

		for k := range container.Devices {
			devices = append(devices, k)
		}
	}

	fmt.Printf("%s\n", strings.Join(devices, "\n"))

	return nil
}

// Override
type cmdConfigDeviceOverride struct {
	global       *cmdGlobal
	config       *cmdConfig
	configDevice *cmdConfigDevice
	profile      *cmdProfile
}

func (c *cmdConfigDeviceOverride) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("override [<remote>:]<container> <device> [key=value...]")
	cmd.Short = i18n.G("Copy profile inherited devices and override configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Copy profile inherited devices and override configuration keys`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigDeviceOverride) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
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
		return fmt.Errorf(i18n.G("Missing name"))
	}

	// Override the device
	container, etag, err := resource.server.GetContainer(resource.name)
	if err != nil {
		return err
	}

	devname := args[1]
	_, ok := container.Devices[devname]
	if ok {
		return fmt.Errorf(i18n.G("The device already exists"))
	}

	device, ok := container.ExpandedDevices[devname]
	if !ok {
		return fmt.Errorf(i18n.G("The profile device doesn't exist"))
	}

	if len(args) > 2 {
		for _, prop := range args[2:] {
			results := strings.SplitN(prop, "=", 2)
			if len(results) != 2 {
				return fmt.Errorf(i18n.G("No value found in %q"), prop)
			}

			k := results[0]
			v := results[1]
			device[k] = v
		}
	}

	container.Devices[devname] = device

	op, err := resource.server.UpdateContainer(resource.name, container.Writable(), etag)
	if err != nil {
		return err
	}

	err = op.Wait()
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Device %s overridden for %s")+"\n", devname, resource.name)
	}

	return nil
}

// Remove
type cmdConfigDeviceRemove struct {
	global       *cmdGlobal
	config       *cmdConfig
	configDevice *cmdConfigDevice
	profile      *cmdProfile
}

func (c *cmdConfigDeviceRemove) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("remove [<remote>:]<container|profile> <name>...")
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Remove container devices")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Remove container devices`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigDeviceRemove) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
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
		return fmt.Errorf(i18n.G("Missing name"))
	}

	// Remove the device
	if c.profile != nil {
		profile, etag, err := resource.server.GetProfile(resource.name)
		if err != nil {
			return err
		}

		for _, devname := range args[1:] {
			_, ok := profile.Devices[devname]
			if !ok {
				return fmt.Errorf(i18n.G("The device doesn't exist"))
			}
			delete(profile.Devices, devname)
		}

		err = resource.server.UpdateProfile(resource.name, profile.Writable(), etag)
		if err != nil {
			return err
		}
	} else {
		container, etag, err := resource.server.GetContainer(resource.name)
		if err != nil {
			return err
		}

		for _, devname := range args[1:] {
			_, ok := container.Devices[devname]
			if !ok {
				return fmt.Errorf(i18n.G("The device doesn't exist"))
			}
			delete(container.Devices, devname)
		}

		op, err := resource.server.UpdateContainer(resource.name, container.Writable(), etag)
		if err != nil {
			return err
		}

		err = op.Wait()
		if err != nil {
			return err
		}
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Device %s removed from %s")+"\n", strings.Join(args[1:], ", "), resource.name)
	}

	return nil
}

// Set
type cmdConfigDeviceSet struct {
	global       *cmdGlobal
	config       *cmdConfig
	configDevice *cmdConfigDevice
	profile      *cmdProfile
}

func (c *cmdConfigDeviceSet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("set [<remote>:]<container|profile> <device> <key> <value>")
	cmd.Short = i18n.G("Set container device configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Set container device configuration keys`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigDeviceSet) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 4, 4)
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
		return fmt.Errorf(i18n.G("Missing name"))
	}

	// Set the device config key
	devname := args[1]
	key := args[2]
	value := args[3]

	if c.profile != nil {
		profile, etag, err := resource.server.GetProfile(resource.name)
		if err != nil {
			return err
		}

		dev, ok := profile.Devices[devname]
		if !ok {
			return fmt.Errorf(i18n.G("The device doesn't exist"))
		}

		dev[key] = value
		profile.Devices[devname] = dev

		err = resource.server.UpdateProfile(resource.name, profile.Writable(), etag)
		if err != nil {
			return err
		}
	} else {
		container, etag, err := resource.server.GetContainer(resource.name)
		if err != nil {
			return err
		}
		dev, ok := container.Devices[devname]
		if !ok {
			return fmt.Errorf(i18n.G("The device doesn't exist"))
		}

		dev[key] = value
		container.Devices[devname] = dev

		op, err := resource.server.UpdateContainer(resource.name, container.Writable(), etag)
		if err != nil {
			return err
		}

		err = op.Wait()
		if err != nil {
			return err
		}
	}

	return nil
}

// Show
type cmdConfigDeviceShow struct {
	global       *cmdGlobal
	config       *cmdConfig
	configDevice *cmdConfigDevice
	profile      *cmdProfile
}

func (c *cmdConfigDeviceShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("show [<remote>:]<container|profile>")
	cmd.Short = i18n.G("Show full device configuration for containers or profiles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show full device configuration for containers or profiles`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigDeviceShow) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing name"))
	}

	// Show the devices
	var devices map[string]map[string]string
	if c.profile != nil {
		profile, _, err := resource.server.GetProfile(resource.name)
		if err != nil {
			return err
		}

		devices = profile.Devices
	} else {
		container, _, err := resource.server.GetContainer(resource.name)
		if err != nil {
			return err
		}

		devices = container.Devices
	}

	data, err := yaml.Marshal(&devices)
	if err != nil {
		return err
	}

	fmt.Printf(string(data))

	return nil
}

// Unset
type cmdConfigDeviceUnset struct {
	global          *cmdGlobal
	config          *cmdConfig
	configDevice    *cmdConfigDevice
	configDeviceSet *cmdConfigDeviceSet
	profile         *cmdProfile
}

func (c *cmdConfigDeviceUnset) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("unset [<remote>:]<container|profile> <device> <key>")
	cmd.Short = i18n.G("Unset container device configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Unset container device configuration keys`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigDeviceUnset) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 3, 3)
	if exit {
		return err
	}

	args = append(args, "")
	return c.configDeviceSet.Run(cmd, args)
}
