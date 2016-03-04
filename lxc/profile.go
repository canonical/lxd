package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"syscall"

	"github.com/codegangsta/cli"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/termios"
)

var commandProfile = cli.Command{
	Name:  "profile",
	Usage: i18n.G("Manage configuration profiles."),
	Description: i18n.G(`Manage configuration profiles.

   lxc profile list [filters]                     List available profiles
   lxc profile show <profile>                     Show details of a profile
   lxc profile create <profile>                   Create a profile
   lxc profile edit <profile>                     Edit profile in external editor
   lxc profile copy <profile> <remote>            Copy the profile to the specified remote
	 lxc profile get <profile> <key>                Get profile configuration
   lxc profile set <profile> <key> <value>        Set profile configuration
   lxc profile delete <profile>                   Delete a profile
   lxc profile apply <container> <profiles>
       Apply a comma-separated list of profiles to a container, in order.
       All profiles passed in this call (and only those) will be applied
       to the specified container.
       Example: lxc profile apply foo default,bar # Apply default and bar
                lxc profile apply foo default # Only default is active
                lxc profile apply '' # no profiles are applied anymore
                lxc profile apply bar,default # Apply default second now

   Devices:
   lxc profile device list <profile>              List devices in the given profile.
   lxc profile device show <profile>              Show full device details in the given profile.
   lxc profile device remove <profile> <name>     Remove a device from a profile.
   lxc profile device get <[remote:]profile> <name> <key>              Get a device property.
   lxc profile device set <[remote:]profile> <name> <key> <value>      Set a device property.
   lxc profile device unset <[remote:]profile> <name> <key>            Unset a device property.
   lxc profile device add <profile name> <device name> <device type> [key=value]...
       Add a profile device, such as a disk or a nic, to the containers
       using the specified profile.`),

	Subcommands: []cli.Command{

		cli.Command{
			Name:      "list",
			ArgsUsage: i18n.G("[filters]"),
			Usage:     i18n.G("List available profiles."),

			Flags:  commandGlobalFlags,
			Action: commandWrapper(commandActionProfileList),
		},

		cli.Command{
			Name:      "show",
			ArgsUsage: i18n.G("<profile>"),
			Usage:     i18n.G("Show details of a profile."),

			Flags:  commandGlobalFlags,
			Action: commandWrapper(commandActionProfileShow),
		},

		cli.Command{
			Name:      "create",
			ArgsUsage: i18n.G("<profile>"),
			Usage:     i18n.G("Create a profile."),

			Flags:  commandGlobalFlags,
			Action: commandWrapper(commandActionProfileCreate),
		},

		cli.Command{
			Name:      "edit",
			ArgsUsage: i18n.G("<profile>"),
			Usage:     i18n.G("Edit a profile."),

			Flags:  commandGlobalFlags,
			Action: commandWrapper(commandActionProfileEdit),
		},

		cli.Command{
			Name:      "copy",
			ArgsUsage: i18n.G("<profile> <remote>"),
			Usage:     i18n.G("Copy the profile to the specified remote."),

			Flags:  commandGlobalFlags,
			Action: commandWrapper(commandActionProfileCopy),
		},

		cli.Command{
			Name:      "get",
			ArgsUsage: i18n.G("<profile> <key>"),
			Usage:     i18n.G("Get profile configuration."),

			Flags:  commandGlobalFlags,
			Action: commandWrapper(commandActionProfileGet),
		},

		cli.Command{
			Name:      "set",
			ArgsUsage: i18n.G("<profile> <key> <value>"),
			Usage:     i18n.G("Set profile configuration."),

			Flags:  commandGlobalFlags,
			Action: commandWrapper(commandActionProfileSet),
		},

		cli.Command{
			Name:      "unset",
			ArgsUsage: i18n.G("<profile> <key>"),
			Usage:     i18n.G("Unset profile configuration."),

			Flags:  commandGlobalFlags,
			Action: commandWrapper(commandActionProfileUnset),
		},

		cli.Command{
			Name:      "delete",
			ArgsUsage: i18n.G("<profile>"),
			Usage:     i18n.G("Delete a profile."),

			Flags:  commandGlobalFlags,
			Action: commandWrapper(commandActionProfileDelete),
		},

		cli.Command{
			Name:      "apply",
			ArgsUsage: i18n.G("<container> <profiles>"),
			Usage:     i18n.G("Apply a comma-separated list of profiles to a container."),
			Description: i18n.G(`Apply a comma-separated list of profiles to a container.

   Apply a comma-separated list of profiles to a container, in order.
   All profiles passed in this call (and only those) will be applied
   to the specified container.
   Example: lxc profile apply foo default,bar # Apply default and bar
            lxc profile apply foo default # Only default is active
            lxc profile apply '' # no profiles are applied anymore
            lxc profile apply bar,default # Apply default second now
`),

			Flags:  commandGlobalFlags,
			Action: commandWrapper(commandActionProfileApply),
		},

		cli.Command{
			Name:  "device",
			Usage: i18n.G("Profile device manipulation."),

			Subcommands: []cli.Command{
				cli.Command{
					Name:      "list",
					ArgsUsage: i18n.G("<profile>"),
					Usage:     i18n.G("List devices in the given profile."),
					Flags:     commandGlobalFlags,
					Action:    commandWrapper(commandActionProfileDeviceList),
				},

				cli.Command{
					Name:      "show",
					ArgsUsage: i18n.G("<profile>"),
					Usage:     i18n.G("Show full device details in the given profile."),
					Flags:     commandGlobalFlags,
					Action:    commandWrapper(commandActionProfileDeviceShow),
				},

				cli.Command{
					Name:      "remove",
					ArgsUsage: i18n.G("<profile> <name>"),
					Usage:     i18n.G("Remove a device from a profile."),
					Flags:     commandGlobalFlags,
					Action:    commandWrapper(commandActionProfileDeviceRemove),
				},

				cli.Command{
					Name:      "add",
					ArgsUsage: i18n.G("<profile name> <device name> <device type> [key=value]..."),
					Usage: i18n.G(`Add a profile device, such as a disk or a nic, to
 the containers using the specified profile.`),
					Flags:  commandGlobalFlags,
					Action: commandWrapper(commandActionProfileDeviceAdd),
				},
			},
		},
	},
}

func commandActionProfileList(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()
	var remote string
	if len(args) > 0 {
		var name string
		remote, name = config.ParseRemoteAndContainer(args[0])
		if name != "" {
			return fmt.Errorf(i18n.G("Cannot provide container name to list"))
		}
	} else {
		remote = config.DefaultRemote
	}

	client, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	profiles, err := client.ListProfiles()
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", strings.Join(profiles, "\n"))
	return nil
}

func commandActionProfileCreate(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()

	if len(args) < 1 {
		return errArgs
	}

	remote, profile := config.ParseRemoteAndContainer(args[0])
	client, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	err = client.ProfileCreate(profile)
	if err == nil {
		fmt.Printf(i18n.G("Profile %s created")+"\n", profile)
	}
	return err
}

func commandActionProfileShow(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()

	if len(args) < 1 {
		return errArgs
	}

	remote, profile := config.ParseRemoteAndContainer(args[0])
	client, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	profileData, err := client.ProfileConfig(profile)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&profileData)
	fmt.Printf("%s", data)

	return nil
}

func commandActionProfileDelete(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()

	if len(args) < 1 {
		return errArgs
	}

	remote, profile := config.ParseRemoteAndContainer(args[0])
	client, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	err = client.ProfileDelete(profile)
	if err == nil {
		fmt.Printf(i18n.G("Profile %s deleted")+"\n", profile)
	}
	return err
}

func profileEditHelp() string {
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
###     parent: lxcbr0
###     type: nic
###
### Note that the name is shown but cannot be changed`)
}

func commandActionProfileEdit(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()

	if len(args) < 1 {
		return errArgs
	}

	remote, profile := config.ParseRemoteAndContainer(args[0])
	client, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(int(syscall.Stdin)) {
		contents, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := shared.ProfileConfig{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}
		return client.PutProfile(profile, newdata)
	}

	// Extract the current value
	profileData, err := client.ProfileConfig(profile)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&profileData)
	if err != nil {
		return err
	}

	// Spawn the editor
	content, err := shared.TextEditor("", []byte(profileEditHelp()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor
		newdata := shared.ProfileConfig{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = client.PutProfile(profile, newdata)
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

func commandActionProfileCopy(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()

	if len(args) != 2 {
		return errArgs
	}

	remote, profile := config.ParseRemoteAndContainer(args[0])
	client, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	remote, newname := config.ParseRemoteAndContainer(args[1])
	if newname == "" {
		newname = profile
	}

	dest, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	return client.ProfileCopy(profile, newname, dest)
}

func commandActionProfileGet(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()

	if len(args) < 2 {
		return errArgs
	}

	remote, profile := config.ParseRemoteAndContainer(args[0])
	client, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	resp, err := client.GetProfileConfig(profile)
	if err != nil {
		return err
	}
	for k, v := range resp {
		if k == args[1] {
			fmt.Printf("%s\n", v)
		}
	}

	return nil
}

func commandActionProfileSet(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()

	if len(args) < 3 {
		return errArgs
	}

	remote, profile := config.ParseRemoteAndContainer(args[0])
	client, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	key := args[1]
	var value string
	if len(args) < 3 {
		value = ""
	} else {
		value = args[2]
	}

	if !termios.IsTerminal(int(syscall.Stdin)) && value == "-" {
		buf, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("Can't read from stdin: %s", err)
		}
		value = string(buf[:])
	}

	err = client.SetProfileConfigItem(profile, key, value)
	return err
}

func commandActionProfileUnset(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()

	if len(args) < 2 {
		return errArgs
	}

	remote, profile := config.ParseRemoteAndContainer(args[0])
	client, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	key := args[1]
	err = client.SetProfileConfigItem(profile, key, "")
	return err
}

func commandActionProfileApply(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()

	if len(args) < 1 {
		return errArgs
	}

	remote, profile := config.ParseRemoteAndContainer(args[0])
	client, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	container := profile
	switch len(args) {
	case 1:
		profile = ""
	case 2:
		profile = args[1]
	default:
		return errArgs
	}

	resp, err := client.ApplyProfile(container, profile)
	if err != nil {
		return err
	}

	err = client.WaitForSuccess(resp.Operation)
	if err == nil {
		if profile == "" {
			profile = i18n.G("(none)")
		}
		fmt.Printf(i18n.G("Profile %s applied to %s")+"\n", profile, container)
	}

	return err
}

func commandActionProfileDeviceAdd(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()
	if len(args) < 1 {
		return errArgs
	}

	cfg := configCmd{}
	return cfg.deviceAdd(config, "profile", args)
}

func commandActionProfileDeviceRemove(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()
	if len(args) < 1 {
		return errArgs
	}

	cfg := configCmd{}
	return cfg.deviceRm(config, "profile", args)
}

func commandActionProfileDeviceList(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()
	if len(args) < 1 {
		return errArgs
	}

	cfg := configCmd{}
	return cfg.deviceList(config, "profile", args)
}

func commandActionProfileDeviceShow(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()
	if len(args) < 1 {
		return errArgs
	}

	cfg := configCmd{}
	return cfg.deviceShow(config, "profile", args)
}
