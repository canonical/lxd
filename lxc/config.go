package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/termios"
)

type cmdConfig struct {
	global *cmdGlobal
}

func (c *cmdConfig) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("config")
	cmd.Short = i18n.G("Manage container and server configuration options")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage container and server configuration options`))

	// Device
	configDeviceCmd := cmdConfigDevice{global: c.global, config: c}
	cmd.AddCommand(configDeviceCmd.Command())

	// Edit
	configEditCmd := cmdConfigEdit{global: c.global, config: c}
	cmd.AddCommand(configEditCmd.Command())

	// Get
	configGetCmd := cmdConfigGet{global: c.global, config: c}
	cmd.AddCommand(configGetCmd.Command())

	// Metadata
	configMetadataCmd := cmdConfigMetadata{global: c.global, config: c}
	cmd.AddCommand(configMetadataCmd.Command())

	// Profile
	configProfileCmd := cmdProfile{global: c.global}
	profileCmd := configProfileCmd.Command()
	profileCmd.Hidden = true
	profileCmd.Deprecated = i18n.G("please use `lxc profile`")
	cmd.AddCommand(profileCmd)

	// Set
	configSetCmd := cmdConfigSet{global: c.global, config: c}
	cmd.AddCommand(configSetCmd.Command())

	// Show
	configShowCmd := cmdConfigShow{global: c.global, config: c}
	cmd.AddCommand(configShowCmd.Command())

	// Template
	configTemplateCmd := cmdConfigTemplate{global: c.global, config: c}
	cmd.AddCommand(configTemplateCmd.Command())

	// Trust
	configTrustCmd := cmdConfigTrust{global: c.global, config: c}
	cmd.AddCommand(configTrustCmd.Command())

	// Unset
	configUnsetCmd := cmdConfigUnset{global: c.global, config: c, configSet: &configSetCmd}
	cmd.AddCommand(configUnsetCmd.Command())

	return cmd
}

// Edit
type cmdConfigEdit struct {
	global *cmdGlobal
	config *cmdConfig
}

func (c *cmdConfigEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("edit [<remote>:][<container>]")
	cmd.Short = i18n.G("Edit container or server configurations as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit container or server configurations as YAML`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc config edit <container> < container.yaml
    Update the container configuration from config.yaml.`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigEdit) helpTemplate() string {
	return i18n.G(
		`### This is a yaml representation of the configuration.
### Any line starting with a '# will be ignored.
###
### A sample configuration looks like:
### name: container1
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

func (c *cmdConfigEdit) Run(cmd *cobra.Command, args []string) error {
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

	// Edit the config
	if resource.name != "" {
		// If stdin isn't a terminal, read text from it
		if !termios.IsTerminal(int(syscall.Stdin)) {
			contents, err := ioutil.ReadAll(os.Stdin)
			if err != nil {
				return err
			}

			newdata := api.ContainerPut{}
			err = yaml.Unmarshal(contents, &newdata)
			if err != nil {
				return err
			}

			op, err := resource.server.UpdateContainer(resource.name, newdata, "")
			if err != nil {
				return err
			}

			return op.Wait()
		}

		// Extract the current value
		container, etag, err := resource.server.GetContainer(resource.name)
		if err != nil {
			return err
		}

		brief := container.Writable()
		data, err := yaml.Marshal(&brief)
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
			newdata := api.ContainerPut{}
			err = yaml.Unmarshal(content, &newdata)
			if err == nil {
				var op lxd.Operation
				op, err = resource.server.UpdateContainer(resource.name, newdata, etag)
				if err == nil {
					err = op.Wait()
				}
			}

			// Respawn the editor
			if err != nil {
				fmt.Fprintf(os.Stderr, i18n.G("Config parsing error: %s")+"\n", err)
				fmt.Println(i18n.G("Press enter to start the editor again"))

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

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(int(syscall.Stdin)) {
		contents, err := ioutil.ReadAll(os.Stdin)
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
			fmt.Println(i18n.G("Press enter to start the editor again"))

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
type cmdConfigGet struct {
	global *cmdGlobal
	config *cmdConfig
}

func (c *cmdConfigGet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("get [<remote>:][<container>] <key>")
	cmd.Short = i18n.G("Get values for container or server configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Get values for container or server configuration keys`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigGet) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
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

	// Get the config key
	if resource.name != "" {
		resp, _, err := resource.server.GetContainer(resource.name)
		if err != nil {
			return err
		}
		fmt.Println(resp.Config[args[len(args)-1]])
	} else {
		resp, _, err := resource.server.GetServer()
		if err != nil {
			return err
		}

		value := resp.Config[args[len(args)-1]]
		if value == nil {
			value = ""
		} else if value == true {
			value = "true"
		} else if value == false {
			value = "false"
		}

		fmt.Println(value)
	}

	return nil
}

// Set
type cmdConfigSet struct {
	global *cmdGlobal
	config *cmdConfig
}

func (c *cmdConfigSet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("set [<remote>:][<container>] <key> <value>")
	cmd.Short = i18n.G("Set container or server configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Set container or server configuration keys`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc config set [<remote>:]<container> limits.cpu 2
    Will set a CPU limit of "2" for the container.

lxc config set core.https_address [::]:8443
    Will have LXD listen on IPv4 and IPv6 port 8443.

lxc config set core.trust_password blah
    Will set the server's trust password to blah.`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigSet) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 2, 3)
	if exit {
		return err
	}

	// Parse remote
	remote := ""
	if len(args) > 2 {
		remote = args[0]
	}

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]

	// Set the config key
	if resource.name != "" {
		key := args[len(args)-2]
		value := args[len(args)-1]

		if !termios.IsTerminal(int(syscall.Stdin)) && value == "-" {
			buf, err := ioutil.ReadAll(os.Stdin)
			if err != nil {
				return fmt.Errorf(i18n.G("Can't read from stdin: %s"), err)
			}
			value = string(buf[:])
		}

		container, etag, err := resource.server.GetContainer(resource.name)
		if err != nil {
			return err
		}

		if cmd.Name() == "unset" {
			_, ok := container.Config[key]
			if !ok {
				return fmt.Errorf(i18n.G("Can't unset key '%s', it's not currently set"), key)
			}

			delete(container.Config, key)
		} else {
			container.Config[key] = value
		}

		op, err := resource.server.UpdateContainer(resource.name, container.Writable(), etag)
		if err != nil {
			return err
		}

		return op.Wait()
	}

	// Server keys
	server, etag, err := resource.server.GetServer()
	if err != nil {
		return err
	}

	server.Config[args[len(args)-2]] = args[len(args)-1]

	return resource.server.UpdateServer(server.Writable(), etag)
}

// Show
type cmdConfigShow struct {
	global *cmdGlobal
	config *cmdConfig

	flagExpanded bool
}

func (c *cmdConfigShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("show [<remote>:][<container>]")
	cmd.Short = i18n.G("Show container or server configurations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show container or server configurations`))

	cmd.Flags().BoolVar(&c.flagExpanded, "expanded", false, i18n.G("Show the expanded configuration"))
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigShow) Run(cmd *cobra.Command, args []string) error {
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

	// Show configuration
	var data []byte

	if resource.name == "" {
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
		// Container config
		var brief api.ContainerPut

		if shared.IsSnapshot(resource.name) {
			// Snapshot
			fields := strings.Split(resource.name, shared.SnapshotDelimiter)

			snap, _, err := resource.server.GetContainerSnapshot(fields[0], fields[1])
			if err != nil {
				return err
			}

			brief = api.ContainerPut{
				Profiles:  snap.Profiles,
				Config:    snap.Config,
				Devices:   snap.Devices,
				Ephemeral: snap.Ephemeral,
			}

			if c.flagExpanded {
				brief = api.ContainerPut{
					Profiles:  snap.Profiles,
					Config:    snap.ExpandedConfig,
					Devices:   snap.ExpandedDevices,
					Ephemeral: snap.Ephemeral,
				}
			}
		} else {
			// Container
			container, _, err := resource.server.GetContainer(resource.name)
			if err != nil {
				return err
			}

			brief = container.Writable()
			if c.flagExpanded {
				brief.Config = container.ExpandedConfig
				brief.Devices = container.ExpandedDevices
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

// Unset
type cmdConfigUnset struct {
	global    *cmdGlobal
	config    *cmdConfig
	configSet *cmdConfigSet
}

func (c *cmdConfigUnset) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("unset [<remote>:][<container>] <key>")
	cmd.Short = i18n.G("Unset container or server configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Unset container or server configuration keys`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigUnset) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 1, 2)
	if exit {
		return err
	}

	args = append(args, "")
	return c.configSet.Run(cmd, args)
}
