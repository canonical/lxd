package main

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"strings"
	"syscall"

	"github.com/codegangsta/cli"
	"github.com/olekukonko/tablewriter"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/termios"
)

var commandConfigDevice = cli.Command{
	Name:  "device",
	Usage: i18n.G("Device manipulation."),
	Description: i18n.G(`Device manipulation

   lxc config device add <[remote:]container> <name> <type> [key=value]...
                   Add a device to a container
   lxc config device list [remote:]<container>            List devices for container
   lxc config device show [remote:]<container>            Show full device details for container
   lxc config device remove [remote:]<container> <name>   Remove device from container
`),
	Subcommands: []cli.Command{

		cli.Command{
			Name:      "add",
			ArgsUsage: i18n.G("<[remote:]container> <name> <type> [key=value]..."),
			Usage:     i18n.G("Add a device to a container."),

			Flags:  commandGlobalFlags,
			Action: commandWrapper(newConfigCmd().runActionDeviceAdd),
		},

		cli.Command{
			Name:      "list",
			ArgsUsage: i18n.G("[remote:]<container>"),
			Usage:     i18n.G("List devices for container."),

			Flags:  commandGlobalFlags,
			Action: commandWrapper(newConfigCmd().runActionDeviceList),
		},

		cli.Command{
			Name:      "show",
			ArgsUsage: i18n.G("[remote:]<container>"),
			Usage:     i18n.G("Show full device details for container."),

			Flags:  commandGlobalFlags,
			Action: commandWrapper(newConfigCmd().runActionDeviceShow),
		},

		cli.Command{
			Name:      "remove",
			ArgsUsage: i18n.G("[remote:]<container> <name>"),
			Usage:     i18n.G("Remove device from container."),

			Flags:  commandGlobalFlags,
			Action: commandWrapper(newConfigCmd().runActionDeviceRemove),
		},
	},
}

var commandConfigTrust = cli.Command{
	Name:  "trust",
	Usage: i18n.G("Trust manipulation."),
	Description: i18n.G(`Trust manipulation

   lxc config trust list [remote]                         List all trusted certs.
   lxc config trust add [remote] <certfile.crt>           Add certfile.crt to trusted hosts.
   lxc config trust remove [remote] [hostname|fingerprint]
                  Remove the cert from trusted hosts.
`),
	Subcommands: []cli.Command{

		cli.Command{
			Name:      "list",
			ArgsUsage: i18n.G("[remote]"),
			Usage:     i18n.G("List all trusted certs."),

			Flags:  commandGlobalFlags,
			Action: commandWrapper(newConfigCmd().runActionTrustList),
		},

		cli.Command{
			Name:      "add",
			ArgsUsage: i18n.G("[remote] <certfile.crt>"),
			Usage:     i18n.G("Add certfile.crt to trusted hosts."),

			Flags:  commandGlobalFlags,
			Action: commandWrapper(newConfigCmd().runActionTrustAdd),
		},

		cli.Command{
			Name:      "remove",
			ArgsUsage: i18n.G("[remote] [hostname|fingerprint]"),
			Usage:     i18n.G("Remove the cert from trusted hosts."),

			Flags:  commandGlobalFlags,
			Action: commandWrapper(newConfigCmd().runActionTrustRemove),
		},
	},
}

var commandConfig = cli.Command{
	Name:  "config",
	Usage: i18n.G("Manage configuration."),
	Description: i18n.G(`Manage configuration.

   lxc config device add <[remote:]container> <name> <type> [key=value]...     Add a device to a container.
   lxc config device get <[remote:]container> <name> <key>                     Get a device property.
   lxc config device set <[remote:]container> <name> <key> <value>             Set a device property.
   lxc config device unset <[remote:]container> <name> <key>                   Unset a device property.
   lxc config device list <[remote:]container>                                 List devices for container.
   lxc config device show <[remote:]container>                                 Show full device details for container.
   lxc config device remove <[remote:]container> <name>                        Remove device from container.

   lxc config get [remote:]<container> key                                     Get configuration key.
   lxc config set [remote:]<container> key value                               Set container configuration key.
   lxc config unset [remote:]<container> key                                   Unset container configuration key.
   lxc config set key value                                                    Set server configuration key.
   lxc config unset key                                                        Unset server configuration key.
   lxc config show [--expanded] [remote:]<container>                           Show container configuration.
   lxc config edit [remote:][container]                                        Edit container configuration in external editor.
   	Edit configuration, either by launching external editor or reading STDIN.
   	Example: lxc config edit <container> # launch editor
   					 cat config.yml | lxc config edit <config> # read from config.yml

   lxc config trust list [remote]                                              List all trusted certs.
   lxc config trust add [remote] <certfile.crt>                                Add certfile.crt to trusted hosts.
   lxc config trust remove [remote] [hostname|fingerprint]                     Remove the cert from trusted hosts.

   Examples:
   To mount host's /share/c1 onto /opt in the container:
    lxc config device add [remote:]container1 <device-name> disk source=/share/c1 path=opt

   To set an lxc config value:
	   lxc config set [remote:]<container> raw.lxc 'lxc.aa_allow_incomplete = 1'

   To listen on IPv4 and IPv6 port 8443 (you can omit the 8443 its the default):
	   lxc config set core.https_address [::]:8443

   To set the server trust password:
 	   lxc config set core.trust_password blah`),

	Flags: commandGlobalFlags,
	Subcommands: []cli.Command{

		commandConfigDevice,

		cli.Command{
			Name:      "edit",
			ArgsUsage: i18n.G("[remote:]<container>"),
			Usage:     i18n.G("Edit container configuration in external editor."),

			Flags:  commandGlobalFlags,
			Action: commandWrapper(newConfigCmd().runActionEdit),
		},

		cli.Command{
			Name:      "get",
			ArgsUsage: i18n.G("[[remote:]<container>] key"),
			Usage:     i18n.G("Get configuration key."),

			Flags:  commandGlobalFlags,
			Action: commandWrapper(newConfigCmd().runActionGet),
		},

		cli.Command{
			Name:      "unset",
			ArgsUsage: i18n.G("key"),
			Usage:     i18n.G("Unset server configuration key."),

			Flags:  commandGlobalFlags,
			Action: commandWrapper(newConfigCmd().runActionUnset),
		},

		cli.Command{
			Name:      "set",
			ArgsUsage: i18n.G("[[remote:]<container>] key value"),
			Usage:     i18n.G("Set server/container configuration key."),

			Flags:  commandGlobalFlags,
			Action: commandWrapper(newConfigCmd().runActionSet),
		},

		cli.Command{
			Name:      "show",
			ArgsUsage: i18n.G("[--expanded] [remote:]<container>"),
			Usage:     i18n.G("Show container configuration."),

			Flags: commandGlobalFlagsWrapper(cli.BoolFlag{
				Name:  "expanded",
				Usage: i18n.G("Whether to show the expanded configuration."),
			}),
			Action: commandWrapper(newConfigCmd().runActionShow),
		},

		commandConfigTrust,
		commandProfile,
	},
}

func newConfigCmd() *configCmd {
	return &configCmd{}
}

type configCmd struct {
	httpAddr string
}

func (c *configCmd) showByDefault() bool {
	return true
}

func (c *configCmd) configEditHelp() string {
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

func (c *configCmd) doSet(config *lxd.Config, args []string, unset bool) error {
	if len(args) != 3 {
		return errArgs
	}

	// [[lxc config]] set dakara:c1 limits.memory 200000
	remote, container := config.ParseRemoteAndContainer(args[0])
	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	key := args[1]
	value := args[2]

	if !termios.IsTerminal(int(syscall.Stdin)) && value == "-" {
		buf, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf(i18n.G("Can't read from stdin: %s"), err)
		}
		value = string(buf[:])
	}

	if unset {
		st, err := d.ContainerInfo(container)
		if err != nil {
			return err
		}

		_, ok := st.Config[key]
		if !ok {
			return fmt.Errorf(i18n.G("Can't unset key '%s', it's not currently set."), key)
		}
	}

	return d.SetContainerConfig(container, key, value)
}

func (c *configCmd) runActionUnset(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()

	if len(args) < 1 {
		return errArgs
	}

	// Deal with local server
	if len(args) == 1 {
		c, err := lxd.NewClient(config, config.DefaultRemote)
		if err != nil {
			return err
		}

		ss, err := c.ServerStatus()
		if err != nil {
			return err
		}

		_, ok := ss.Config[args[0]]
		if !ok {
			return fmt.Errorf(i18n.G("Can't unset key '%s', it's not currently set."), args[0])
		}

		_, err = c.SetServerConfig(args[0], "")
		return err
	}

	// Deal with remote server
	remote, container := config.ParseRemoteAndContainer(args[0])
	if container == "" {
		c, err := lxd.NewClient(config, remote)
		if err != nil {
			return err
		}

		ss, err := c.ServerStatus()
		if err != nil {
			return err
		}

		_, ok := ss.Config[args[0]]
		if !ok {
			return fmt.Errorf(i18n.G("Can't unset key '%s', it's not currently set."), args[0])
		}

		_, err = c.SetServerConfig(args[1], "")
		return err
	}

	// Deal with container
	args = append(args, "")
	return c.doSet(config, args, true)
}

func (c *configCmd) runActionSet(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()

	if len(args) < 2 {
		return errArgs
	}

	// Deal with local server
	if len(args) == 2 {
		c, err := lxd.NewClient(config, config.DefaultRemote)
		if err != nil {
			return err
		}

		_, err = c.SetServerConfig(args[0], args[1])
		return err
	}

	// Deal with remote server
	remote, container := config.ParseRemoteAndContainer(args[0])
	if container == "" {
		c, err := lxd.NewClient(config, remote)
		if err != nil {
			return err
		}

		_, err = c.SetServerConfig(args[1], args[2])
		return err
	}

	// Deal with container
	return c.doSet(config, args, false)
}

func (c *configCmd) runActionGet(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()

	if len(args) > 2 || len(args) < 1 {
		return errArgs
	}

	remote := config.DefaultRemote
	container := ""
	key := args[0]
	if len(args) > 1 {
		remote, container = config.ParseRemoteAndContainer(args[0])
		key = args[1]
	}

	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	if container != "" {
		resp, err := d.ContainerInfo(container)
		if err != nil {
			return err
		}
		fmt.Printf("%s: %s\n", key, resp.Config[key])
	} else {
		resp, err := d.ServerStatus()
		if err != nil {
			return err
		}

		value := resp.Config[key]
		if value == nil {
			value = ""
		} else if value == true {
			value = "true"
		} else if value == false {
			value = "false"
		}

		fmt.Printf("%s: %s\n", key, value)
	}
	return nil
}

func (c *configCmd) runActionShow(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()

	remote := config.DefaultRemote
	container := ""
	if len(args) > 0 {
		remote, container = config.ParseRemoteAndContainer(args[0])
	}

	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	var data []byte

	if len(args) == 0 || container == "" {
		config, err := d.ServerStatus()
		if err != nil {
			return err
		}

		brief := config.Brief()
		data, err = yaml.Marshal(&brief)
	} else {
		config, err := d.ContainerInfo(container)
		if err != nil {
			return err
		}

		brief := config.Brief()
		if context.Bool("expanded") {
			brief = config.BriefExpanded()
		}
		data, err = yaml.Marshal(&brief)
	}

	fmt.Printf("%s", data)

	return nil
}

func (c *configCmd) runActionEdit(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()

	if len(args) == 0 {
		return errArgs
	}

	remote := config.DefaultRemote
	container := ""
	if len(args) > 1 {
		remote, container = config.ParseRemoteAndContainer(args[0])
	}

	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	if len(args) == 0 || container == "" {
		return c.doDaemonConfigEdit(d)
	}

	return c.doContainerConfigEdit(d, container)
}

func (c *configCmd) runActionTrustList(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()
	var remote string
	if len(args) == 1 {
		remote = config.ParseRemote(args[0])
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
	table.SetAutoWrapText(false)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetRowLine(true)
	table.SetHeader([]string{
		i18n.G("FINGERPRINT"),
		i18n.G("COMMON NAME"),
		i18n.G("ISSUE DATE"),
		i18n.G("EXPIRY DATE")})
	sort.Sort(sortImage(data))
	table.AppendBulk(data)
	table.Render()

	return nil
}

func (c *configCmd) runActionTrustAdd(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()
	var remote string
	if len(args) < 1 {
		return fmt.Errorf(i18n.G("No certificate provided to add"))
	} else if len(args) == 2 {
		remote = config.ParseRemote(args[0])
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
}

func (c *configCmd) runActionTrustRemove(config *lxd.Config, context *cli.Context) error {
	var args = context.Args()
	var remote string
	if len(args) < 1 {
		return fmt.Errorf(i18n.G("No fingerprint specified."))
	} else if len(args) == 2 {
		remote = config.ParseRemote(args[0])
	} else {
		remote = config.DefaultRemote
	}

	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	return d.CertificateRemove(args[len(args)-1])
}

func (c *configCmd) runActionDeviceAdd(config *lxd.Config, context *cli.Context) error {
	return c.deviceAdd(config, "container", context.Args())
}

func (c *configCmd) runActionDeviceList(config *lxd.Config, context *cli.Context) error {
	return c.deviceList(config, "container", context.Args())
}

func (c *configCmd) runActionDeviceShow(config *lxd.Config, context *cli.Context) error {
	return c.deviceShow(config, "container", context.Args())
}

func (c *configCmd) runActionDeviceRemove(config *lxd.Config, context *cli.Context) error {
	return c.deviceRm(config, "container", context.Args())
}

func (c *configCmd) doContainerConfigEdit(client *lxd.Client, cont string) error {
	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(int(syscall.Stdin)) {
		contents, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := shared.BriefContainerInfo{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}
		return client.UpdateContainerConfig(cont, newdata)
	}

	// Extract the current value
	config, err := client.ContainerInfo(cont)
	if err != nil {
		return err
	}

	brief := config.Brief()
	data, err := yaml.Marshal(&brief)
	if err != nil {
		return err
	}

	// Spawn the editor
	content, err := shared.TextEditor("", []byte(c.configEditHelp()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor
		newdata := shared.BriefContainerInfo{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = client.UpdateContainerConfig(cont, newdata)
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

func (c *configCmd) doDaemonConfigEdit(client *lxd.Client) error {
	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(int(syscall.Stdin)) {
		contents, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := shared.BriefServerState{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}

		_, err = client.UpdateServerConfig(newdata)
		return err
	}

	// Extract the current value
	config, err := client.ServerStatus()
	if err != nil {
		return err
	}

	brief := config.Brief()
	data, err := yaml.Marshal(&brief)
	if err != nil {
		return err
	}

	// Spawn the editor
	content, err := shared.TextEditor("", []byte(c.configEditHelp()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor
		newdata := shared.BriefServerState{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			_, err = client.UpdateServerConfig(newdata)
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

func (c *configCmd) deviceAdd(config *lxd.Config, which string, args []string) error {
	if len(args) < 3 {
		return errArgs
	}
	remote, name := config.ParseRemoteAndContainer(args[0])

	client, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	devname := args[1]
	devtype := args[2]
	var props []string
	if len(args) > 3 {
		props = args[3:]
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
	if which != "profile" {
		err = client.WaitForSuccess(resp.Operation)
	}
	if err == nil {
		fmt.Printf(i18n.G("Device %s added to %s")+"\n", devname, name)
	}
	return err
}

func (c *configCmd) deviceGet(config *lxd.Config, which string, args []string) error {
	if len(args) < 5 {
		return errArgs
	}

	remote, name := config.ParseRemoteAndContainer(args[2])

	client, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	devname := args[3]
	key := args[4]

	if which == "profile" {
		st, err := client.ProfileConfig(name)
		if err != nil {
			return err
		}

		dev, ok := st.Devices[devname]
		if !ok {
			return fmt.Errorf(i18n.G("The device doesn't exist"))
		}

		fmt.Println(dev[key])
	} else {
		st, err := client.ContainerInfo(name)
		if err != nil {
			return err
		}

		dev, ok := st.Devices[devname]
		if !ok {
			return fmt.Errorf(i18n.G("The device doesn't exist"))
		}

		fmt.Println(dev[key])
	}

	return nil
}

func (c *configCmd) deviceSet(config *lxd.Config, which string, args []string) error {
	if len(args) < 6 {
		return errArgs
	}

	remote, name := config.ParseRemoteAndContainer(args[2])

	client, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	devname := args[3]
	key := args[4]
	value := args[5]

	if which == "profile" {
		st, err := client.ProfileConfig(name)
		if err != nil {
			return err
		}

		dev, ok := st.Devices[devname]
		if !ok {
			return fmt.Errorf(i18n.G("The device doesn't exist"))
		}

		dev[key] = value
		st.Devices[devname] = dev

		err = client.PutProfile(name, *st)
		if err != nil {
			return err
		}
	} else {
		st, err := client.ContainerInfo(name)
		if err != nil {
			return err
		}

		dev, ok := st.Devices[devname]
		if !ok {
			return fmt.Errorf(i18n.G("The device doesn't exist"))
		}

		dev[key] = value
		st.Devices[devname] = dev

		err = client.UpdateContainerConfig(name, st.Brief())
		if err != nil {
			return err
		}
	}

	return err
}

func (c *configCmd) deviceUnset(config *lxd.Config, which string, args []string) error {
	if len(args) < 5 {
		return errArgs
	}

	remote, name := config.ParseRemoteAndContainer(args[2])

	client, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	devname := args[3]
	key := args[4]

	if which == "profile" {
		st, err := client.ProfileConfig(name)
		if err != nil {
			return err
		}

		dev, ok := st.Devices[devname]
		if !ok {
			return fmt.Errorf(i18n.G("The device doesn't exist"))
		}

		delete(dev, key)
		st.Devices[devname] = dev

		err = client.PutProfile(name, *st)
		if err != nil {
			return err
		}
	} else {
		st, err := client.ContainerInfo(name)
		if err != nil {
			return err
		}

		dev, ok := st.Devices[devname]
		if !ok {
			return fmt.Errorf(i18n.G("The device doesn't exist"))
		}

		delete(dev, key)
		st.Devices[devname] = dev

		err = client.UpdateContainerConfig(name, st.Brief())
		if err != nil {
			return err
		}
	}

	return err
}

func (c *configCmd) deviceRm(config *lxd.Config, which string, args []string) error {
	if len(args) < 2 {
		return errArgs
	}
	remote, name := config.ParseRemoteAndContainer(args[0])

	client, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	devname := args[1]
	var resp *lxd.Response
	if which == "profile" {
		resp, err = client.ProfileDeviceDelete(name, devname)
	} else {
		resp, err = client.ContainerDeviceDelete(name, devname)
	}
	if err != nil {
		return err
	}
	if which != "profile" {
		err = client.WaitForSuccess(resp.Operation)
	}
	if err == nil {
		fmt.Printf(i18n.G("Device %s removed from %s")+"\n", devname, name)
	}
	return err
}

func (c *configCmd) deviceList(config *lxd.Config, which string, args []string) error {
	if len(args) < 1 {
		return errArgs
	}
	remote, name := config.ParseRemoteAndContainer(args[0])

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

func (c *configCmd) deviceShow(config *lxd.Config, which string, args []string) error {
	if len(args) < 1 {
		return errArgs
	}
	remote, name := config.ParseRemoteAndContainer(args[0])

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
		resp, err := client.ContainerInfo(name)
		if err != nil {
			return err
		}

		devices = resp.Devices
	}

	data, err := yaml.Marshal(&devices)
	if err != nil {
		return err
	}

	fmt.Printf(string(data))

	return nil
}
