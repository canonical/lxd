package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"time"

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

	flagTarget string
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
	cmd.Use = i18n.G("edit [<remote>:][<container>[/<snapshot>]]")
	cmd.Short = i18n.G("Edit container or server configurations as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit container or server configurations as YAML`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc config edit <container> < container.yaml
    Update the container configuration from config.yaml.`))

	cmd.Flags().StringVar(&c.config.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
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

	fields := strings.SplitN(resource.name, "/", 2)
	isSnapshot := len(fields) == 2

	// Edit the config
	if resource.name != "" {
		// Sanity checks
		if c.config.flagTarget != "" {
			return fmt.Errorf(i18n.G("--target cannot be used with containers"))
		}

		// If stdin isn't a terminal, read text from it
		if !termios.IsTerminal(getStdinFd()) {
			contents, err := ioutil.ReadAll(os.Stdin)
			if err != nil {
				return err
			}

			var op lxd.Operation

			if isSnapshot {
				snapshot, _, err := resource.server.GetContainerSnapshot(fields[0], fields[1])
				if err != nil {
					return err
				}

				// The current expiry date needs to be set here explicitly, otherwise failing to
				// provide a new value will reset the expiry date to zero time (no expiry).
				newdata := api.ContainerSnapshotPut{
					ExpiresAt: snapshot.ExpiresAt,
				}

				err = yaml.Unmarshal(contents, &newdata)
				if err != nil {
					return err
				}

				op, err = resource.server.UpdateContainerSnapshot(fields[0], fields[1], newdata, "")
				if err != nil {
					return err
				}
			} else {
				newdata := api.ContainerPut{}
				err = yaml.Unmarshal(contents, &newdata)
				if err != nil {
					return err
				}

				op, err = resource.server.UpdateContainer(resource.name, newdata, "")
				if err != nil {
					return err
				}
			}

			return op.Wait()
		}

		var data []byte
		var etag string
		var currentExpiryDate time.Time

		// Extract the current value
		if isSnapshot {
			var container *api.ContainerSnapshot

			container, etag, err = resource.server.GetContainerSnapshot(fields[0], fields[1])
			if err != nil {
				return err
			}

			brief := container.Writable()
			data, err = yaml.Marshal(&brief)
			if err != nil {
				return err
			}

			currentExpiryDate = brief.ExpiresAt
		} else {
			var container *api.Container

			container, etag, err = resource.server.GetContainer(resource.name)
			if err != nil {
				return err
			}

			brief := container.Writable()
			data, err = yaml.Marshal(&brief)
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
				newdata := api.ContainerSnapshotPut{
					ExpiresAt: currentExpiryDate,
				}

				err = yaml.Unmarshal(content, &newdata)
				if err == nil {
					var op lxd.Operation
					op, err = resource.server.UpdateContainerSnapshot(fields[0], fields[1],
						newdata, etag)
					if err == nil {
						err = op.Wait()
					}
				}
			} else {
				newdata := api.ContainerPut{}
				err = yaml.Unmarshal(content, &newdata)
				if err == nil {
					var op lxd.Operation
					op, err = resource.server.UpdateContainer(resource.name, newdata, etag)
					if err == nil {
						err = op.Wait()
					}
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

	// Targeting
	if c.config.flagTarget != "" {
		if !resource.server.IsClustered() {
			return fmt.Errorf(i18n.G("To use --target, the destination remote must be a cluster"))
		}

		resource.server = resource.server.UseTarget(c.config.flagTarget)
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
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

	cmd.Flags().StringVar(&c.config.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
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
		// Sanity checks
		if c.config.flagTarget != "" {
			return fmt.Errorf(i18n.G("--target cannot be used with containers"))
		}

		resp, _, err := resource.server.GetContainer(resource.name)
		if err != nil {
			return err
		}
		fmt.Println(resp.Config[args[len(args)-1]])
	} else {
		// Targeting
		if c.config.flagTarget != "" {
			if !resource.server.IsClustered() {
				return fmt.Errorf(i18n.G("To use --target, the destination remote must be a cluster"))
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
	cmd.Use = i18n.G("set [<remote>:][<container>] <key>=<value>…")
	cmd.Short = i18n.G("Set container or server configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Set container or server configuration keys

For backward compatibility, a single configuration key may still be set with:
    lxc config set [<remote>:][<container>] <key> <value>`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc config set [<remote>:]<container> limits.cpu=2
    Will set a CPU limit of "2" for the container.

lxc config set core.https_address=[::]:8443
    Will have LXD listen on IPv4 and IPv6 port 8443.

lxc config set core.trust_password=blah
    Will set the server's trust password to blah.`))

	cmd.Flags().StringVar(&c.config.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigSet) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 1, -1)
	if exit {
		return err
	}

	// Parse remote
	remote := ""
	if len(args) != 2 && !strings.Contains(args[0], "=") {
		remote = args[0]
	}

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]

	// Set the config keys
	if resource.name != "" {
		// Sanity checks
		if c.config.flagTarget != "" {
			return fmt.Errorf(i18n.G("--target cannot be used with containers"))
		}

		keys, err := getConfig(args[1:]...)
		if err != nil {
			return err
		}

		container, etag, err := resource.server.GetContainer(resource.name)
		if err != nil {
			return err
		}

		for k, v := range keys {
			if cmd.Name() == "unset" {
				_, ok := container.Config[k]
				if !ok {
					return fmt.Errorf(i18n.G("Can't unset key '%s', it's not currently set"), k)
				}

				delete(container.Config, k)
			} else {
				container.Config[k] = v
			}
		}

		op, err := resource.server.UpdateContainer(resource.name, container.Writable(), etag)
		if err != nil {
			return err
		}

		return op.Wait()
	}

	// Targeting
	if c.config.flagTarget != "" {
		if !resource.server.IsClustered() {
			return fmt.Errorf(i18n.G("To use --target, the destination remote must be a cluster"))
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

	for k, v := range keys {
		server.Config[k] = v
	}

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
	cmd.Use = i18n.G("show [<remote>:][<container>[/<snapshot>]]")
	cmd.Short = i18n.G("Show container or server configurations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show container or server configurations`))

	cmd.Flags().BoolVar(&c.flagExpanded, "expanded", false, i18n.G("Show the expanded configuration"))
	cmd.Flags().StringVar(&c.config.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
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
		// Targeting
		if c.config.flagTarget != "" {
			if !resource.server.IsClustered() {
				return fmt.Errorf(i18n.G("To use --target, the destination remote must be a cluster"))
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
		// Sanity checks
		if c.config.flagTarget != "" {
			return fmt.Errorf(i18n.G("--target cannot be used with containers"))
		}

		// Container or snapshot config
		var brief interface{}

		if shared.IsSnapshot(resource.name) {
			// Snapshot
			fields := strings.Split(resource.name, shared.SnapshotDelimiter)

			snap, _, err := resource.server.GetContainerSnapshot(fields[0], fields[1])
			if err != nil {
				return err
			}

			writable := snap.Writable()
			brief = &writable

			brief.(*api.ContainerSnapshotPut).ExpiresAt = snap.ExpiresAt

			if c.flagExpanded {
				brief.(*api.ContainerSnapshotPut).Config = snap.ExpandedConfig
				brief.(*api.ContainerSnapshotPut).Devices = snap.ExpandedDevices
			}
		} else {
			// Container
			container, _, err := resource.server.GetContainer(resource.name)
			if err != nil {
				return err
			}

			writable := container.Writable()
			brief = &writable

			if c.flagExpanded {
				brief.(*api.ContainerPut).Config = container.ExpandedConfig
				brief.(*api.ContainerPut).Devices = container.ExpandedDevices
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

	cmd.Flags().StringVar(&c.config.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
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
