package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"

	"github.com/gosexy/gettext"
	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"gopkg.in/yaml.v2"
)

type configCmd struct {
	httpAddr string
}

func (c *configCmd) showByDefault() bool {
	return true
}

var profileEditHelp string = gettext.Gettext(
	"### This is a yaml representation of the profile.\n" +
		"### Any line starting with a '# will be ignored.\n" +
		"###\n" +
		"### A profile consists of a set of configuration items followed by a set of\n" +
		"### devices.\n" +
		"###\n" +
		"### An example would look like:\n" +
		"### name: onenic\n" +
		"### config:\n" +
		"###   raw.lxc: lxc.aa_profile=unconfined\n" +
		"### devices:\n" +
		"###   eth0:\n" +
		"###     nictype: bridged\n" +
		"###     parent: lxcbr0\n" +
		"###     type: nic\n" +
		"###\n" +
		"### Note that the name cannot be changed\n")

func (c *configCmd) usage() string {
	return gettext.Gettext(
		"Manage configuration.\n" +
			"\n" +
			"lxc config get <container> key                   Get configuration key\n" +
			"lxc config device add <resource> <name> <type> [key=value]...\n" +
			"               Add a device to a resource\n" +
			"lxc config device list <resource>                List devices for resource\n" +
			"lxc config device remove <resource> <name>       Remove device from resource\n" +
			"lxc config profile list [filters]                List profiles\n" +
			"lxc config profile create <profile>              Create profile\n" +
			"lxc config profile delete <profile>              Delete profile\n" +
			"lxc config profile device add <profile> <name> <type> [key=value]...\n" +
			"               Delete profile\n" +
			"lxc config profile edit <profile>                Edit profile in external editor\n" +
			"lxc config profile device list <profile>\n" +
			"lxc config profile device remove <profile> <name>\n" +
			"lxc config profile set <profile> <key> <value>   Set profile configuration\n" +
			"lxc config profile apply <resource> <profile>    Apply profile to container\n" +
			"lxc config set [remote] password <newpwd>        Set admin password\n" +
			"lxc config set <container> key [value]           Set container configuration key\n" +
			"lxc config show <container>                      Show container configuration\n" +
			"lxc config trust list [remote]                   List all trusted certs.\n" +
			"lxc config trust add [remote] [certfile.crt]     Add certfile.crt to trusted hosts.\n" +
			"lxc config trust remove [remote] [hostname|fingerprint]\n" +
			"               Remove the cert from trusted hosts.\n")
}

func (c *configCmd) flags() {}

func doSet(config *lxd.Config, args []string) error {
	// [[lxc config]] set dakara:c1 limits.memory 200000
	remote, container := config.ParseRemoteAndContainer(args[1])
	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	key := args[2]
	var value string
	if len(args) < 4 {
		value = ""
	} else {
		value = args[3]
	}
	resp, err := d.SetContainerConfig(container, key, value)
	if err != nil {
		return err
	}
	return d.WaitForSuccess(resp.Operation)
}

func (c *configCmd) run(config *lxd.Config, args []string) error {
	if len(args) < 1 {
		return errArgs
	}

	switch args[0] {

	case "unset":
		if len(args) < 3 {
			return errArgs
		}
		return doSet(config, args)

	case "set":
		if len(args) < 2 {
			return errArgs
		}

		if args[1] == "password" {
			if len(args) != 3 {
				return errArgs
			}

			password := args[2]
			c, err := lxd.NewClient(config, "")
			if err != nil {
				return err
			}

			_, err = c.SetRemotePwd(password)
			return err
		}

		if len(args) < 3 {
			return errArgs
		}

		return doSet(config, args)

	case "trust":
		if len(args) < 2 {
			return errArgs
		}

		switch args[1] {
		case "list":
			var remote string
			if len(args) == 3 {
				remote = config.ParseRemote(args[2])
			} else {
				remote = config.DefaultRemote
			}

			d, err := lxd.NewClient(config, remote)
			if err != nil {
				return err
			}

			trust, err := d.CertificateList()
			if err != nil {
				return err
			}

			for host, fingerprint := range trust {
				fmt.Println(fmt.Sprintf("%s: %s", host, fingerprint))
			}

			return nil
		case "add":
			var remote string
			if len(args) < 3 {
				return fmt.Errorf(gettext.Gettext("No cert provided to add"))
			} else if len(args) == 4 {
				remote = config.ParseRemote(args[2])
			} else {
				remote = config.DefaultRemote
			}

			d, err := lxd.NewClient(config, remote)
			if err != nil {
				return err
			}

			fname := args[len(args)-1]
			cert, err := shared.ReadCert(fname)
			if err != nil {
				return err
			}

			name, _ := shared.SplitExt(fname)
			return d.CertificateAdd(cert, name)
		case "remove":
			var remote string
			if len(args) < 3 {
				return fmt.Errorf(gettext.Gettext("No fingerprint specified."))
			} else if len(args) == 4 {
				remote = config.ParseRemote(args[2])
			} else {
				remote = config.DefaultRemote
			}

			d, err := lxd.NewClient(config, remote)
			if err != nil {
				return err
			}

			toRemove := args[len(args)-1]
			trust, err := d.CertificateList()
			if err != nil {
				return err
			}

			/* Try to remove by hostname first. */
			for host, fingerprint := range trust {
				if host == toRemove {
					return d.CertificateRemove(fingerprint)
				}
			}

			return d.CertificateRemove(args[len(args)-1])
		default:
			return fmt.Errorf(gettext.Gettext("Unkonwn config trust command %s"), args[1])
		}

	case "show":
		if len(args) == 1 {
			return fmt.Errorf(gettext.Gettext("Show for server is not yet supported\n"))
		}
		remote, container := config.ParseRemoteAndContainer(args[1])
		if container == "" {
			return fmt.Errorf(gettext.Gettext("Show for remotes is not yet supported\n"))
		}
		d, err := lxd.NewClient(config, remote)
		if err != nil {
			return err
		}
		resp, err := d.GetContainerConfig(container)
		if err != nil {
			return err
		}
		fmt.Printf("%s\n", strings.Join(resp, "\n"))
		return nil

	case "get":
		if len(args) != 3 {
			return errArgs
		}

		remote, container := config.ParseRemoteAndContainer(args[1])
		d, err := lxd.NewClient(config, remote)
		if err != nil {
			return err
		}

		resp, err := d.ContainerStatus(container)
		if err != nil {
			return err
		}
		fmt.Printf("%s: %s\n", args[2], resp.Config[args[2]])
		return nil

	case "profile":
		if len(args) < 2 {
			return errArgs
		}
		if args[1] == "list" {
			return doProfileList(config, args)
		}
		if len(args) < 3 {
			return errArgs
		}

		if args[1] == "device" {
			return doProfileDevice(config, args)
		}

		remote, profile := config.ParseRemoteAndContainer(args[2])
		client, err := lxd.NewClient(config, remote)
		if err != nil {
			return err
		}

		switch args[1] {
		case "create":
			return doProfileCreate(client, profile)
		case "delete":
			return doProfileDelete(client, profile)
		case "edit":
			return doProfileEdit(client, profile)
		case "apply":
			container := profile
			switch len(args) {
			case 3:
				profile = ""
			case 4:
				profile = args[3]
			default:
				return errArgs
			}
			return doProfileApply(client, container, profile)
		case "get":
			return doProfileGet(client, profile, args[3:])
		case "set":
			return doProfileSet(client, profile, args[3:])
		case "unset":
			return doProfileSet(client, profile, args[3:])
		case "copy":
			return doProfileCopy(config, client, profile, args[3:])
		case "show":
			return doProfileShow(client, profile)
		}

	case "device":
		if len(args) < 2 {
			return errArgs
		}
		switch args[1] {
		case "list":
			return deviceList(config, "container", args)
		case "add":
			return deviceAdd(config, "container", args)
		case "remove":
			return deviceRm(config, "container", args)
		default:
			return errArgs
		}

	default:
		return errArgs
	}

	return errArgs
}

func doProfileCreate(client *lxd.Client, p string) error {
	err := client.ProfileCreate(p)
	if err == nil {
		fmt.Printf(gettext.Gettext("Profile %s created\n"), p)
	}
	return err
}

func doProfileEdit(client *lxd.Client, p string) error {
	profile, err := client.ProfileConfig(p)
	if err != nil {
		return err
	}
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
		if editor == "" {
			editor = "vi"
		}
	}
	data, err := yaml.Marshal(&profile)
	f, err := ioutil.TempFile("", "lxc_profile_")
	if err != nil {
		return err
	}
	fname := f.Name()
	if err = f.Chmod(0700); err != nil {
		f.Close()
		os.Remove(fname)
		return err
	}
	f.Write([]byte(profileEditHelp))
	f.Write(data)
	f.Close()
	defer os.Remove(fname)
	cmd := exec.Command(editor, fname)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if err != nil {
		return err
	}
	contents, err := ioutil.ReadFile(fname)
	if err != nil {
		return err
	}
	newdata := shared.ProfileConfig{}
	err = yaml.Unmarshal(contents, &newdata)
	if err != nil {
		return err
	}
	err = client.PutProfile(p, newdata)
	return err
}

func doProfileDelete(client *lxd.Client, p string) error {
	err := client.ProfileDelete(p)
	if err == nil {
		fmt.Printf(gettext.Gettext("Profile %s deleted\n"), p)
	}
	return err
}

func doProfileApply(client *lxd.Client, c string, p string) error {
	resp, err := client.ApplyProfile(c, p)
	if err == nil {
		if p == "" {
			p = "(none)"
		}
		fmt.Printf(gettext.Gettext("Profile %s applied to %s\n"), p, c)
	} else {
		return err
	}
	return client.WaitForSuccess(resp.Operation)
}

func doProfileShow(client *lxd.Client, p string) error {
	resp, err := client.GetProfileConfig(p)
	if err != nil {
		return err
	}
	for k, v := range resp {
		fmt.Printf("%s = %s\n", k, v)
	}

	dresp, err := client.ProfileListDevices(p)
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", strings.Join(dresp, "\n"))

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
	// profile device add b1 eth0 nic type=bridged
	// profile device list b1
	// profile device remove b1 eth0
	if len(args) < 4 {
		return errArgs
	}
	switch args[2] {
	case "add":
		return deviceAdd(config, "profile", args[1:])
	case "remove":
		return deviceRm(config, "profile", args[1:])
	case "list":
		return deviceList(config, "profile", args[1:])
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
	err := client.SetProfileConfigItem(p, key, value)
	return err
}

func doProfileList(config *lxd.Config, args []string) error {
	var remote string
	if len(args) > 2 {
		var name string
		remote, name = config.ParseRemoteAndContainer(args[2])
		if name != "" {
			return fmt.Errorf(gettext.Gettext("Cannot provide container name to list"))
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

func deviceAdd(config *lxd.Config, which string, args []string) error {
	if len(args) < 5 {
		return errArgs
	}
	remote, name := config.ParseRemoteAndContainer(args[2])

	client, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	devname := args[3]
	devtype := args[4]
	var props []string
	if len(args) > 5 {
		props = args[5:]
	} else {
		props = []string{}
	}

	var resp *lxd.Response
	if which == "profile" {
		resp, err = client.ProfileDeviceAdd(name, devname, devtype, props)
	} else {
		resp, err = client.ContainerDeviceAdd(name, devname, devtype, props)
	}
	if err != nil {
		return err
	}
	fmt.Printf(gettext.Gettext("Device %s added to %s\n"), devname, name)
	if which == "profile" {
		return nil
	}
	return client.WaitForSuccess(resp.Operation)
}

func deviceRm(config *lxd.Config, which string, args []string) error {
	if len(args) < 4 {
		return errArgs
	}
	remote, name := config.ParseRemoteAndContainer(args[2])

	client, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	devname := args[3]
	var resp *lxd.Response
	if which == "profile" {
		resp, err = client.ProfileDeviceDelete(name, devname)
	} else {
		resp, err = client.ContainerDeviceDelete(name, devname)
	}
	if err != nil {
		return err
	}
	fmt.Printf(gettext.Gettext("Device %s removed from %s\n"), devname, name)
	if which == "profile" {
		return nil
	}
	return client.WaitForSuccess(resp.Operation)
}

func deviceList(config *lxd.Config, which string, args []string) error {
	if len(args) < 3 {
		return errArgs
	}
	remote, name := config.ParseRemoteAndContainer(args[2])

	client, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	var resp []string
	if which == "profile" {
		resp, err = client.ProfileListDevices(name)
	} else {
		resp, err = client.ContainerListDevices(name)
	}
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", strings.Join(resp, "\n"))

	return nil
}
