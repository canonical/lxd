package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"syscall"

	"golang.org/x/crypto/ssh/terminal"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/i18n"
	"github.com/lxc/lxd/shared"
)

type profileCmd struct {
	httpAddr string
}

func (c *profileCmd) showByDefault() bool {
	return true
}

var profileEditHelp string = i18n.G(
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

func (c *profileCmd) usage() string {
	return i18n.G(
		`Manage configuration profiles.

lxc profile list [filters]                     List available profiles.
lxc profile show <profile>                     Show details of a profile.
lxc profile create <profile>                   Create a profile.
lxc profile copy <profile> <remote>            Copy the profile to the specified remote.
lxc profile get <profile> <key>                Get profile configuration.
lxc profile set <profile> <key> <value>        Set profile configuration.
lxc profile delete <profile>                   Delete a profile.
lxc profile edit <profile>
    Edit profile, either by launching external editor or reading STDIN.
    Example: lxc profile edit <profile> # launch editor
             cat profile.yml | lxc profile edit <profile> # read from profile.yml
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
lxc profile device add <profile name> <device name> <device type> [key=value]...
    Add a profile device, such as a disk or a nic, to the containers
    using the specified profile.`)
}

func (c *profileCmd) flags() {}

func (c *profileCmd) run(config *lxd.Config, args []string) error {
	if len(args) < 1 {
		return errArgs
	}

	if args[0] == "list" {
		return doProfileList(config, args)
	}

	if len(args) < 2 {
		return errArgs
	}

	remote, profile := config.ParseRemoteAndContainer(args[1])
	client, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	switch args[0] {
	case "create":
		return doProfileCreate(client, profile)
	case "delete":
		return doProfileDelete(client, profile)
	case "device":
		return doProfileDevice(config, args)
	case "edit":
		return doProfileEdit(client, profile)
	case "apply":
		container := profile
		switch len(args) {
		case 2:
			profile = ""
		case 3:
			profile = args[2]
		default:
			return errArgs
		}
		return doProfileApply(client, container, profile)
	case "get":
		return doProfileGet(client, profile, args[2:])
	case "set":
		return doProfileSet(client, profile, args[2:])
	case "unset":
		return doProfileSet(client, profile, args[2:])
	case "copy":
		return doProfileCopy(config, client, profile, args[2:])
	case "show":
		return doProfileShow(client, profile)
	default:
		return errArgs
	}
}

func doProfileCreate(client *lxd.Client, p string) error {
	err := client.ProfileCreate(p)
	if err == nil {
		fmt.Printf(i18n.G("Profile %s created")+"\n", p)
	}
	return err
}

func doProfileEdit(client *lxd.Client, p string) error {
	// If stdin isn't a terminal, read text from it
	if !terminal.IsTerminal(int(syscall.Stdin)) {
		contents, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := shared.ProfileConfig{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}
		return client.PutProfile(p, newdata)
	}

	// Extract the current value
	profile, err := client.ProfileConfig(p)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&profile)
	if err != nil {
		return err
	}

	// Spawn the editor
	content, err := shared.TextEditor("", []byte(profileEditHelp+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor
		newdata := shared.ProfileConfig{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = client.PutProfile(p, newdata)
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

func doProfileDelete(client *lxd.Client, p string) error {
	err := client.ProfileDelete(p)
	if err == nil {
		fmt.Printf(i18n.G("Profile %s deleted")+"\n", p)
	}
	return err
}

func doProfileApply(client *lxd.Client, c string, p string) error {
	resp, err := client.ApplyProfile(c, p)
	if err == nil {
		if p == "" {
			p = i18n.G("(none)")
		}
		fmt.Printf(i18n.G("Profile %s applied to %s")+"\n", p, c)
	} else {
		return err
	}
	return client.WaitForSuccess(resp.Operation)
}

func doProfileShow(client *lxd.Client, p string) error {
	profile, err := client.ProfileConfig(p)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&profile)
	fmt.Printf("%s", data)

	return nil
}

func doProfileCopy(config *lxd.Config, client *lxd.Client, p string, args []string) error {
	if len(args) != 1 {
		return errArgs
	}
	remote, newname := config.ParseRemoteAndContainer(args[0])
	if newname == "" {
		newname = p
	}

	dest, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	return client.ProfileCopy(p, newname, dest)
}

func doProfileDevice(config *lxd.Config, args []string) error {
	// device add b1 eth0 nic type=bridged
	// device list b1
	// device remove b1 eth0
	if len(args) < 3 {
		return errArgs
	}
	switch args[1] {
	case "add":
		return deviceAdd(config, "profile", args)
	case "remove":
		return deviceRm(config, "profile", args)
	case "list":
		return deviceList(config, "profile", args)
	case "show":
		return deviceShow(config, "profile", args)
	default:
		return errArgs
	}
}

func doProfileGet(client *lxd.Client, p string, args []string) error {
	// we shifted @args so so it should read "<key>"
	if len(args) != 1 {
		return errArgs
	}

	resp, err := client.GetProfileConfig(p)
	if err != nil {
		return err
	}
	for k, v := range resp {
		if k == args[0] {
			fmt.Printf("%s\n", v)
		}
	}
	return nil
}

func doProfileSet(client *lxd.Client, p string, args []string) error {
	// we shifted @args so so it should read "<key> [<value>]"
	if len(args) < 1 {
		return errArgs
	}

	key := args[0]
	var value string
	if len(args) < 2 {
		value = ""
	} else {
		value = args[1]
	}

	if !terminal.IsTerminal(int(syscall.Stdin)) && value == "-" {
		buf, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("Can't read from stdin: %s", err)
		}
		value = string(buf[:])
	}

	err := client.SetProfileConfigItem(p, key, value)
	return err
}

func doProfileList(config *lxd.Config, args []string) error {
	var remote string
	if len(args) > 1 {
		var name string
		remote, name = config.ParseRemoteAndContainer(args[1])
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
