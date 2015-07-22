package main

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/chai2010/gettext-go/gettext"
	"github.com/olekukonko/tablewriter"
	"golang.org/x/crypto/ssh/terminal"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
)

type configCmd struct {
	httpAddr string
}

func (c *configCmd) showByDefault() bool {
	return true
}

var configEditHelp string = gettext.Gettext(
	"### This is a yaml representation of the configuration.\n" +
		"### Any line starting with a '# will be ignored.\n" +
		"###\n" +
		"### A sample configuration looks like:\n" +
		"### name: container1\n" +
		"### profiles:\n" +
		"### - default\n" +
		"### config:\n" +
		"###   volatile.eth0.hwaddr: 00:16:3e:e9:f8:7f\n" +
		"### devices:\n" +
		"###   homedir:\n" +
		"###     path: /extra\n" +
		"###     source: /home/user\n" +
		"###     type: disk\n" +
		"### ephemeral: false\n" +
		"###\n" +
		"### Note that the name is shown but cannot be changed\n")

func (c *configCmd) usage() string {
	return gettext.Gettext(
		"Manage configuration.\n" +
			"\n" +
			"lxc config device add <[remote:]container> <name> <type> [key=value]...\n" +
			"               Add a device to a container\n" +
			"lxc config device list [remote:]<container>            List devices for container\n" +
			"lxc config device show [remote:]<container>            Show full device details for container\n" +
			"lxc config device remove [remote:]<container> <name>   Remove device from container\n" +
			"lxc config edit [remote:]<container>                   Edit container configuration in external editor\n" +
			"lxc config get [remote:]<container> key                Get configuration key\n" +
			"lxc config set [remote:]<container> key value          Set container configuration key\n" +
			"lxc config unset [remote:]<container> key              Unset container configuration key\n" +
			"lxc config set key value                               Set server configuration key\n" +
			"lxc config unset key                                   Unset server configuration key\n" +
			"lxc config show [remote:]<container>                   Show container configuration\n" +
			"lxc config trust list [remote]                         List all trusted certs.\n" +
			"lxc config trust add [remote] <certfile.crt>           Add certfile.crt to trusted hosts.\n" +
			"lxc config trust remove [remote] [hostname|fingerprint]\n" +
			"               Remove the cert from trusted hosts.\n" +
			"\n" +
			"Examples:\n" +
			"To mount host's /share/c1 onto /opt in the container:\n" +
			"\tlxc config device add [remote:]container1 <device-name> disk source=/share/c1 path=opt\n" +
			"To set an lxc config value:\n" +
			"\tlxc config set [remote:]<container> raw.lxc 'lxc.aa_allow_incomplete = 1'\n" +
			"To set the server trust password:\n" +
			"\tlxc config set core.trust_password blah\n")
}

func (c *configCmd) flags() {}

func doSet(config *lxd.Config, args []string) error {
	if len(args) != 4 {
		return errArgs
	}

	// [[lxc config]] set dakara:c1 limits.memory 200000
	remote, container := config.ParseRemoteAndContainer(args[1])
	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	key := args[2]
	value := args[3]
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
		if len(args) < 2 {
			return errArgs
		}

		// 2 args means we're unsetting a server key
		if len(args) == 2 {
			key := args[1]
			c, err := lxd.NewClient(config, "")
			if err != nil {
				return err
			}
			_, err = c.SetServerConfig(key, "")
			return err
		}

		// 3 args is a container config key
		args = append(args, "")
		return doSet(config, args)

	case "set":
		if len(args) < 3 {
			return errArgs
		}

		// 3 args means we're setting a server key
		if len(args) == 3 {
			key := args[1]
			c, err := lxd.NewClient(config, "")
			if err != nil {
				return err
			}
			_, err = c.SetServerConfig(key, args[2])
			return err
		}

		// 4 args is a container config key
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

			data := [][]string{}
			for _, cert := range trust {
				fp := cert.Fingerprint[0:12]

				certBlock, _ := pem.Decode([]byte(cert.Certificate))
				cert, err := x509.ParseCertificate(certBlock.Bytes)
				if err != nil {
					return err
				}

				const layout = "Jan 2, 2006 at 3:04pm (MST)"
				issue := cert.NotBefore.Format(layout)
				expiry := cert.NotAfter.Format(layout)
				data = append(data, []string{fp, cert.Subject.CommonName, issue, expiry})
			}

			table := tablewriter.NewWriter(os.Stdout)
			table.SetHeader([]string{"FINGERPRINT", "COMMON NAME", "ISSUE DATE", "EXPIRY DATE"})

			for _, v := range data {
				table.Append(v)
			}
			table.Render()

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

			return d.CertificateRemove(args[len(args)-1])
		default:
			return fmt.Errorf(gettext.Gettext("Unkonwn config trust command %s"), args[1])
		}

	case "show":
		remote := ""
		container := ""
		if len(args) > 1 {
			remote, container = config.ParseRemoteAndContainer(args[1])
			if container == "" {
				return fmt.Errorf(gettext.Gettext("Show for remotes is not yet supported\n"))
			}
		}

		d, err := lxd.NewClient(config, remote)
		if err != nil {
			return err
		}

		var data []byte

		if len(args) == 1 || container == "" {
			config, err := d.ServerStatus()
			if err != nil {
				return err
			}

			brief := config.BriefState()
			data, err = yaml.Marshal(&brief)
		} else {
			config, err := d.ContainerStatus(container, false)
			if err != nil {
				return err
			}

			brief := config.BriefState()
			data, err = yaml.Marshal(&brief)
		}

		fmt.Printf("%s", data)

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

		resp, err := d.ContainerStatus(container, false)
		if err != nil {
			return err
		}
		fmt.Printf("%s: %s\n", args[2], resp.Config[args[2]])
		return nil

	case "profile":
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
		case "show":
			return deviceShow(config, "container", args)
		default:
			return errArgs
		}

	case "edit":
		if len(args) != 2 {
			return errArgs
		}

		remote, container := config.ParseRemoteAndContainer(args[1])
		d, err := lxd.NewClient(config, remote)
		if err != nil {
			return err
		}

		return doConfigEdit(d, container)

	default:
		return errArgs
	}

	return errArgs
}

func doConfigEdit(client *lxd.Client, cont string) error {
	if !terminal.IsTerminal(syscall.Stdin) {
		contents, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := shared.BriefContainerState{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}
		return client.UpdateContainerConfig(cont, newdata)
	}

	config, err := client.ContainerStatus(cont, false)
	if err != nil {
		return err
	}

	brief := config.BriefState()

	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
		if editor == "" {
			editor = "vi"
		}
	}
	data, err := yaml.Marshal(&brief)
	f, err := ioutil.TempFile("", "lxd_lxc_config_")
	if err != nil {
		return err
	}
	fname := f.Name()
	if err = f.Chmod(0600); err != nil {
		f.Close()
		os.Remove(fname)
		return err
	}
	f.Write([]byte(configEditHelp))
	f.Write(data)
	f.Close()
	defer os.Remove(fname)

	for {
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
		newdata := shared.BriefContainerState{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			fmt.Fprintf(os.Stderr, gettext.Gettext("YAML parse error %v\n"), err)
			fmt.Printf("Press enter to play again ")
			_, err := os.Stdin.Read(make([]byte, 1))
			if err != nil {
				return err
			}

			continue
		}
		err = client.UpdateContainerConfig(cont, newdata)
		break
	}
	return err
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

func deviceShow(config *lxd.Config, which string, args []string) error {
	if len(args) < 3 {
		return errArgs
	}
	remote, name := config.ParseRemoteAndContainer(args[2])

	client, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	var devices map[string]shared.Device
	if which == "profile" {
		resp, err := client.ProfileConfig(name)
		if err != nil {
			return err
		}

		devices = resp.Devices

	} else {
		resp, err := client.ContainerStatus(name, false)
		if err != nil {
			return err
		}

		devices = resp.Devices
	}

	for n, d := range devices {
		fmt.Printf("%s\n", n)
		for attr, val := range d {
			fmt.Printf("  %s: %s\n", attr, val)
		}
	}

	return nil
}
