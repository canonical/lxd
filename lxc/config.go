package main

import (
	"bytes"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"strings"
	"syscall"

	"github.com/olekukonko/tablewriter"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/termios"
)

type configCmd struct {
	expanded bool
}

func (c *configCmd) showByDefault() bool {
	return true
}

func (c *configCmd) flags() {
	gnuflag.BoolVar(&c.expanded, "expanded", false, i18n.G("Show the expanded configuration"))
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

func (c *configCmd) metadataEditHelp() string {
	return i18n.G(
		`### This is a yaml representation of the container metadata.
### Any line starting with a '# will be ignored.
###
### A sample configuration looks like:
###
### architecture: x86_64
### creation_date: 1477146654
### expiry_date: 0
### properties:
###   architecture: x86_64
###   description: Busybox x86_64
###   name: busybox-x86_64
###   os: Busybox
### templates:
###   /template:
###     when:
###     - ""
###     create_only: false
###     template: template.tpl
###     properties: {}`)
}

func (c *configCmd) usage() string {
	return i18n.G(
		`Usage: lxc config <subcommand> [options]

Change container or server configuration options.

*Container configuration*

lxc config get [<remote>:][container] <key>
    Get container or server configuration key.

lxc config set [<remote>:][container] <key> <value>
    Set container or server configuration key.

lxc config unset [<remote>:][container] <key>
    Unset container or server configuration key.

lxc config show [<remote>:][container] [--expanded]
    Show container or server configuration.

lxc config edit [<remote>:][container]
    Edit configuration, either by launching external editor or reading STDIN.

*Container metadata*

lxc config metadata show [<remote>:][container]
    Show the container metadata.yaml content.

lxc config metadata edit [<remote>:][container]
    Edit the container metadata.yaml, either by launching external editor or reading STDIN.

*Container templates*

lxc config template list [<remote>:][container]
    List the names of template files for a container.

lxc config template show [<remote>:][container] [template]
    Show the content of a template file for a container.

lxc config template create [<remote>:][container] [template]
    Add an empty template file for a container.

lxc config template edit [<remote>:][container] [template]
    Edit the content of a template file for a container, either by launching external editor or reading STDIN.

lxc config template delete [<remote>:][container] [template]
    Delete a template file for a container.


*Device management*

lxc config device add [<remote>:]<container> <device> <type> [key=value...]
    Add a device to a container.

lxc config device get [<remote>:]<container> <device> <key>
    Get a device property.

lxc config device set [<remote>:]<container> <device> <key> <value>
    Set a device property.

lxc config device unset [<remote>:]<container> <device> <key>
    Unset a device property.

lxc config device list [<remote>:]<container>
    List devices for container.

lxc config device show [<remote>:]<container>
    Show full device details for container.

lxc config device remove [<remote>:]<container> <name>...
    Remove device from container.

*Client trust store management*

lxc config trust list [<remote>:]
    List all trusted certs.

lxc config trust add [<remote>:] <certfile.crt>
    Add certfile.crt to trusted hosts.

lxc config trust remove [<remote>:] [hostname|fingerprint]
    Remove the cert from trusted hosts.

*Examples*

cat config.yaml | lxc config edit <container>
    Update the container configuration from config.yaml.

lxc config device add [<remote>:]container1 <device-name> disk source=/share/c1 path=opt
    Will mount the host's /share/c1 onto /opt in the container.

lxc config set [<remote>:]<container> limits.cpu 2
    Will set a CPU limit of "2" for the container.

lxc config set core.https_address [::]:8443
    Will have LXD listen on IPv4 and IPv6 port 8443.

lxc config set core.trust_password blah
    Will set the server's trust password to blah.`)
}

func (c *configCmd) doSet(conf *config.Config, args []string, unset bool) error {
	if len(args) != 4 {
		return errArgs
	}

	// [[lxc config]] set dakara:c1 limits.memory 200000
	remote, name, err := conf.ParseRemote(args[1])
	if err != nil {
		return err
	}

	d, err := conf.GetContainerServer(remote)
	if err != nil {
		return err
	}

	key := args[2]
	value := args[3]

	if !termios.IsTerminal(int(syscall.Stdin)) && value == "-" {
		buf, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf(i18n.G("Can't read from stdin: %s"), err)
		}
		value = string(buf[:])
	}

	container, etag, err := d.GetContainer(name)
	if err != nil {
		return err
	}

	if unset {
		_, ok := container.Config[key]
		if !ok {
			return fmt.Errorf(i18n.G("Can't unset key '%s', it's not currently set"), key)
		}

		delete(container.Config, key)
	} else {
		container.Config[key] = value
	}

	op, err := d.UpdateContainer(name, container.Writable(), etag)
	if err != nil {
		return err
	}

	return op.Wait()
}

func (c *configCmd) run(conf *config.Config, args []string) error {
	if len(args) < 1 {
		return errUsage
	}

	switch args[0] {

	case "unset":
		if len(args) < 2 {
			return errArgs
		}

		// Deal with local server
		if len(args) == 2 {
			c, err := conf.GetContainerServer(conf.DefaultRemote)
			if err != nil {
				return err
			}

			server, etag, err := c.GetServer()
			if err != nil {
				return err
			}

			_, ok := server.Config[args[1]]
			if !ok {
				return fmt.Errorf(i18n.G("Can't unset key '%s', it's not currently set."), args[1])
			}

			delete(server.Config, args[1])
			return c.UpdateServer(server.Writable(), etag)
		}

		// Deal with remote server
		remote, container, err := conf.ParseRemote(args[1])
		if err != nil {
			return err
		}

		if container == "" {
			c, err := conf.GetContainerServer(remote)
			if err != nil {
				return err
			}

			server, etag, err := c.GetServer()
			if err != nil {
				return err
			}

			_, ok := server.Config[args[2]]
			if !ok {
				return fmt.Errorf(i18n.G("Can't unset key '%s', it's not currently set."), args[1])
			}

			delete(server.Config, args[2])
			return c.UpdateServer(server.Writable(), etag)
		}

		// Deal with container
		args = append(args, "")
		return c.doSet(conf, args, true)

	case "set":
		if len(args) < 3 {
			return errArgs
		}

		// Deal with local server
		if len(args) == 3 {
			c, err := conf.GetContainerServer(conf.DefaultRemote)
			if err != nil {
				return err
			}

			server, etag, err := c.GetServer()
			if err != nil {
				return err
			}

			server.Config[args[1]] = args[2]

			return c.UpdateServer(server.Writable(), etag)
		}

		// Deal with remote server
		remote, container, err := conf.ParseRemote(args[1])
		if err != nil {
			return err
		}

		if container == "" {
			c, err := conf.GetContainerServer(remote)
			if err != nil {
				return err
			}

			server, etag, err := c.GetServer()
			if err != nil {
				return err
			}

			server.Config[args[2]] = args[3]

			return c.UpdateServer(server.Writable(), etag)
		}

		// Deal with container
		return c.doSet(conf, args, false)

	case "trust":
		if len(args) < 2 {
			return errArgs
		}

		switch args[1] {
		case "list":
			var remote string
			if len(args) == 3 {
				var err error
				remote, _, err = conf.ParseRemote(args[2])
				if err != nil {
					return err
				}
			} else {
				remote = conf.DefaultRemote
			}

			d, err := conf.GetContainerServer(remote)
			if err != nil {
				return err
			}

			trust, err := d.GetCertificates()
			if err != nil {
				return err
			}

			data := [][]string{}
			for _, cert := range trust {
				fp := cert.Fingerprint[0:12]

				certBlock, _ := pem.Decode([]byte(cert.Certificate))
				if certBlock == nil {
					return fmt.Errorf(i18n.G("Invalid certificate"))
				}

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
			sort.Sort(StringList(data))
			table.AppendBulk(data)
			table.Render()

			return nil
		case "add":
			var remote string
			if len(args) < 3 {
				return fmt.Errorf(i18n.G("No certificate provided to add"))
			} else if len(args) == 4 {
				var err error
				remote, _, err = conf.ParseRemote(args[2])
				if err != nil {
					return err
				}
			} else {
				remote = conf.DefaultRemote
			}

			d, err := conf.GetContainerServer(remote)
			if err != nil {
				return err
			}

			fname := args[len(args)-1]
			x509Cert, err := shared.ReadCert(fname)
			if err != nil {
				return err
			}
			name, _ := shared.SplitExt(fname)

			cert := api.CertificatesPost{}
			cert.Certificate = base64.StdEncoding.EncodeToString(x509Cert.Raw)
			cert.Name = name
			cert.Type = "client"

			return d.CreateCertificate(cert)
		case "remove":
			var remote string
			if len(args) < 3 {
				return fmt.Errorf(i18n.G("No fingerprint specified."))
			} else if len(args) == 4 {
				var err error
				remote, _, err = conf.ParseRemote(args[2])
				if err != nil {
					return err
				}
			} else {
				remote = conf.DefaultRemote
			}

			d, err := conf.GetContainerServer(remote)
			if err != nil {
				return err
			}

			return d.DeleteCertificate(args[len(args)-1])
		default:
			return errArgs
		}

	case "show":
		remote := conf.DefaultRemote
		container := ""
		if len(args) > 1 {
			var err error
			remote, container, err = conf.ParseRemote(args[1])
			if err != nil {
				return err
			}
		}

		d, err := conf.GetContainerServer(remote)
		if err != nil {
			return err
		}

		var data []byte

		if len(args) == 1 || container == "" {
			server, _, err := d.GetServer()
			if err != nil {
				return err
			}

			brief := server.Writable()
			data, err = yaml.Marshal(&brief)
			if err != nil {
				return err
			}
		} else {
			var brief api.ContainerPut
			if shared.IsSnapshot(container) {
				fields := strings.Split(container, shared.SnapshotDelimiter)

				snap, _, err := d.GetContainerSnapshot(fields[0], fields[1])
				if err != nil {
					return err
				}

				brief = api.ContainerPut{
					Profiles:  snap.Profiles,
					Config:    snap.Config,
					Devices:   snap.Devices,
					Ephemeral: snap.Ephemeral,
				}
				if c.expanded {
					brief = api.ContainerPut{
						Profiles:  snap.Profiles,
						Config:    snap.ExpandedConfig,
						Devices:   snap.ExpandedDevices,
						Ephemeral: snap.Ephemeral,
					}
				}
			} else {
				container, _, err := d.GetContainer(container)
				if err != nil {
					return err
				}

				brief = container.Writable()
				if c.expanded {
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

	case "get":
		if len(args) > 3 || len(args) < 2 {
			return errArgs
		}

		remote := conf.DefaultRemote
		container := ""
		key := args[1]
		if len(args) > 2 {
			var err error
			remote, container, err = conf.ParseRemote(args[1])
			if err != nil {
				return err
			}
			key = args[2]
		}

		d, err := conf.GetContainerServer(remote)
		if err != nil {
			return err
		}

		if container != "" {
			resp, _, err := d.GetContainer(container)
			if err != nil {
				return err
			}
			fmt.Println(resp.Config[key])
		} else {
			resp, _, err := d.GetServer()
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

			fmt.Println(value)
		}
		return nil

	case "profile":
	case "device":
		if len(args) < 2 {
			return errArgs
		}
		switch args[1] {
		case "list":
			return c.deviceList(conf, "container", args)
		case "add":
			return c.deviceAdd(conf, "container", args)
		case "remove":
			return c.deviceRm(conf, "container", args)
		case "get":
			return c.deviceGet(conf, "container", args)
		case "set":
			return c.deviceSet(conf, "container", args)
		case "unset":
			return c.deviceUnset(conf, "container", args)
		case "show":
			return c.deviceShow(conf, "container", args)
		default:
			return errArgs
		}

	case "edit":
		if len(args) < 1 {
			return errArgs
		}

		remote := conf.DefaultRemote
		container := ""
		if len(args) > 1 {
			var err error
			remote, container, err = conf.ParseRemote(args[1])
			if err != nil {
				return err
			}
		}

		d, err := conf.GetContainerServer(remote)
		if err != nil {
			return err
		}

		if len(args) == 1 || container == "" {
			return c.doDaemonConfigEdit(d)
		}

		return c.doContainerConfigEdit(d, container)

	case "metadata":
		if len(args) < 3 {
			return errArgs
		}

		remote, container, err := conf.ParseRemote(args[2])
		if err != nil {
			return err
		}

		d, err := conf.GetContainerServer(remote)
		if err != nil {
			return err
		}

		switch args[1] {
		case "show":
			metadata, _, err := d.GetContainerMetadata(container)
			if err != nil {
				return err
			}
			content, err := yaml.Marshal(metadata)
			if err != nil {
				return err
			}
			fmt.Printf("%s", content)
			return nil

		case "edit":
			return c.doContainerMetadataEdit(d, container)

		default:
			return errArgs
		}

	case "template":
		if len(args) < 3 {
			return errArgs
		}

		remote, container, err := conf.ParseRemote(args[2])
		if err != nil {
			return err
		}

		d, err := conf.GetContainerServer(remote)
		if err != nil {
			return err
		}

		switch args[1] {
		case "list":
			templates, err := d.GetContainerTemplateFiles(container)
			if err != nil {
				return err
			}

			c.listTemplateFiles(templates)
			return nil

		case "show":
			if len(args) != 4 {
				return errArgs
			}
			templateName := args[3]

			template, err := d.GetContainerTemplateFile(container, templateName)
			if err != nil {
				return err
			}
			content, err := ioutil.ReadAll(template)
			if err != nil {
				return err
			}
			fmt.Printf("%s", content)
			return nil

		case "create":
			if len(args) != 4 {
				return errArgs
			}
			templateName := args[3]
			return c.doContainerTemplateFileCreate(d, container, templateName)

		case "edit":
			if len(args) != 4 {
				return errArgs
			}
			templateName := args[3]
			return c.doContainerTemplateFileEdit(d, container, templateName)

		case "delete":
			if len(args) != 4 {
				return errArgs
			}
			templateName := args[3]
			return d.DeleteContainerTemplateFile(container, templateName)

		default:
			return errArgs
		}

	default:
		return errArgs
	}

	return errArgs
}

func (c *configCmd) listTemplateFiles(templates []string) {
	data := [][]string{}
	for _, template := range templates {
		data = append(data, []string{template})
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetAutoWrapText(false)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetRowLine(true)
	table.SetHeader([]string{i18n.G("FILENAME")})
	sort.Sort(byName(data))
	table.AppendBulk(data)
	table.Render()
}

func (c *configCmd) doContainerConfigEdit(client lxd.ContainerServer, cont string) error {
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

		op, err := client.UpdateContainer(cont, newdata, "")
		if err != nil {
			return err
		}

		return op.Wait()
	}

	// Extract the current value
	container, etag, err := client.GetContainer(cont)
	if err != nil {
		return err
	}

	brief := container.Writable()
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
		newdata := api.ContainerPut{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			var op *lxd.Operation
			op, err = client.UpdateContainer(cont, newdata, etag)
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

func (c *configCmd) doDaemonConfigEdit(client lxd.ContainerServer) error {
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

		return client.UpdateServer(newdata, "")
	}

	// Extract the current value
	server, etag, err := client.GetServer()
	if err != nil {
		return err
	}

	brief := server.Writable()
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
		newdata := api.ServerPut{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = client.UpdateServer(newdata, etag)
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

func (c *configCmd) deviceAdd(conf *config.Config, which string, args []string) error {
	if len(args) < 5 {
		return errArgs
	}

	remote, name, err := conf.ParseRemote(args[2])
	if err != nil {
		return err
	}

	client, err := conf.GetContainerServer(remote)
	if err != nil {
		return err
	}

	devname := args[3]
	device := map[string]string{}
	device["type"] = args[4]
	if len(args) > 5 {
		for _, prop := range args[5:] {
			results := strings.SplitN(prop, "=", 2)
			if len(results) != 2 {
				return fmt.Errorf("No value found in %q", prop)
			}
			k := results[0]
			v := results[1]
			device[k] = v
		}
	}

	if which == "profile" {
		profile, etag, err := client.GetProfile(name)
		if err != nil {
			return err
		}

		_, ok := profile.Devices[devname]
		if ok {
			return fmt.Errorf(i18n.G("The device already exists"))
		}

		profile.Devices[devname] = device

		err = client.UpdateProfile(name, profile.Writable(), etag)
		if err != nil {
			return err
		}
	} else {
		container, etag, err := client.GetContainer(name)
		if err != nil {
			return err
		}

		_, ok := container.Devices[devname]
		if ok {
			return fmt.Errorf(i18n.G("The device already exists"))
		}

		container.Devices[devname] = device

		op, err := client.UpdateContainer(name, container.Writable(), etag)
		if err != nil {
			return err
		}

		err = op.Wait()
		if err != nil {
			return err
		}
	}

	fmt.Printf(i18n.G("Device %s added to %s")+"\n", devname, name)
	return nil
}

func (c *configCmd) deviceGet(conf *config.Config, which string, args []string) error {
	if len(args) < 5 {
		return errArgs
	}

	remote, name, err := conf.ParseRemote(args[2])
	if err != nil {
		return err
	}

	client, err := conf.GetContainerServer(remote)
	if err != nil {
		return err
	}

	devname := args[3]
	key := args[4]

	if which == "profile" {
		profile, _, err := client.GetProfile(name)
		if err != nil {
			return err
		}

		dev, ok := profile.Devices[devname]
		if !ok {
			return fmt.Errorf(i18n.G("The device doesn't exist"))
		}

		fmt.Println(dev[key])
	} else {
		container, _, err := client.GetContainer(name)
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

func (c *configCmd) deviceSet(conf *config.Config, which string, args []string) error {
	if len(args) < 6 {
		return errArgs
	}

	remote, name, err := conf.ParseRemote(args[2])
	if err != nil {
		return err
	}

	client, err := conf.GetContainerServer(remote)
	if err != nil {
		return err
	}

	devname := args[3]
	key := args[4]
	value := args[5]

	if which == "profile" {
		profile, etag, err := client.GetProfile(name)
		if err != nil {
			return err
		}

		dev, ok := profile.Devices[devname]
		if !ok {
			return fmt.Errorf(i18n.G("The device doesn't exist"))
		}

		dev[key] = value
		profile.Devices[devname] = dev

		err = client.UpdateProfile(name, profile.Writable(), etag)
		if err != nil {
			return err
		}
	} else {
		container, etag, err := client.GetContainer(name)
		if err != nil {
			return err
		}

		dev, ok := container.Devices[devname]
		if !ok {
			return fmt.Errorf(i18n.G("The device doesn't exist"))
		}

		dev[key] = value
		container.Devices[devname] = dev

		op, err := client.UpdateContainer(name, container.Writable(), etag)
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

func (c *configCmd) deviceUnset(conf *config.Config, which string, args []string) error {
	if len(args) < 5 {
		return errArgs
	}

	remote, name, err := conf.ParseRemote(args[2])
	if err != nil {
		return err
	}

	client, err := conf.GetContainerServer(remote)
	if err != nil {
		return err
	}

	devname := args[3]
	key := args[4]

	if which == "profile" {
		profile, etag, err := client.GetProfile(name)
		if err != nil {
			return err
		}

		dev, ok := profile.Devices[devname]
		if !ok {
			return fmt.Errorf(i18n.G("The device doesn't exist"))
		}
		delete(dev, key)
		profile.Devices[devname] = dev

		err = client.UpdateProfile(name, profile.Writable(), etag)
		if err != nil {
			return err
		}
	} else {
		container, etag, err := client.GetContainer(name)
		if err != nil {
			return err
		}

		dev, ok := container.Devices[devname]
		if !ok {
			return fmt.Errorf(i18n.G("The device doesn't exist"))
		}
		delete(dev, key)
		container.Devices[devname] = dev

		op, err := client.UpdateContainer(name, container.Writable(), etag)
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

func (c *configCmd) deviceRm(conf *config.Config, which string, args []string) error {
	if len(args) < 4 {
		return errArgs
	}

	remote, name, err := conf.ParseRemote(args[2])
	if err != nil {
		return err
	}

	client, err := conf.GetContainerServer(remote)
	if err != nil {
		return err
	}

	if which == "profile" {
		profile, etag, err := client.GetProfile(name)
		if err != nil {
			return err
		}

		for _, devname := range args[3:] {
			_, ok := profile.Devices[devname]
			if !ok {
				return fmt.Errorf(i18n.G("The device doesn't exist"))
			}
			delete(profile.Devices, devname)
		}

		err = client.UpdateProfile(name, profile.Writable(), etag)
		if err != nil {
			return err
		}
	} else {
		container, etag, err := client.GetContainer(name)
		if err != nil {
			return err
		}

		for _, devname := range args[3:] {
			_, ok := container.Devices[devname]
			if !ok {
				return fmt.Errorf(i18n.G("The device doesn't exist"))
			}
			delete(container.Devices, devname)
		}

		op, err := client.UpdateContainer(name, container.Writable(), etag)
		if err != nil {
			return err
		}

		err = op.Wait()
		if err != nil {
			return err
		}
	}

	fmt.Printf(i18n.G("Device %s removed from %s")+"\n", strings.Join(args[3:], ", "), name)
	return nil
}

func (c *configCmd) deviceList(conf *config.Config, which string, args []string) error {
	if len(args) < 3 {
		return errArgs
	}

	remote, name, err := conf.ParseRemote(args[2])
	if err != nil {
		return err
	}

	client, err := conf.GetContainerServer(remote)
	if err != nil {
		return err
	}

	var devices []string
	if which == "profile" {
		profile, _, err := client.GetProfile(name)
		if err != nil {
			return err
		}

		for k := range profile.Devices {
			devices = append(devices, k)
		}
	} else {
		container, _, err := client.GetContainer(name)
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

func (c *configCmd) deviceShow(conf *config.Config, which string, args []string) error {
	if len(args) < 3 {
		return errArgs
	}

	remote, name, err := conf.ParseRemote(args[2])
	if err != nil {
		return err
	}

	client, err := conf.GetContainerServer(remote)
	if err != nil {
		return err
	}

	var devices map[string]map[string]string
	if which == "profile" {
		profile, _, err := client.GetProfile(name)
		if err != nil {
			return err
		}

		devices = profile.Devices
	} else {
		container, _, err := client.GetContainer(name)
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

func (c *configCmd) doContainerMetadataEdit(client lxd.ContainerServer, name string) error {
	if !termios.IsTerminal(int(syscall.Stdin)) {
		metadata := api.ImageMetadata{}
		content, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		err = yaml.Unmarshal(content, &metadata)
		if err != nil {
			return err
		}
		return client.SetContainerMetadata(name, metadata, "")
	}

	metadata, etag, err := client.GetContainerMetadata(name)
	if err != nil {
		return err
	}
	origContent, err := yaml.Marshal(metadata)
	if err != nil {
		return err
	}

	// Spawn the editor
	content, err := shared.TextEditor("", []byte(c.metadataEditHelp()+"\n\n"+string(origContent)))
	if err != nil {
		return err
	}

	for {
		err = yaml.Unmarshal(content, &metadata)
		if err == nil {
			err = client.SetContainerMetadata(name, *metadata, etag)
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

func (c *configCmd) doContainerTemplateFileCreate(client lxd.ContainerServer, containerName string, templateName string) error {
	return client.CreateContainerTemplateFile(containerName, templateName, nil)
}

func (c *configCmd) doContainerTemplateFileEdit(client lxd.ContainerServer, containerName string, templateName string) error {
	if !termios.IsTerminal(int(syscall.Stdin)) {
		return client.UpdateContainerTemplateFile(containerName, templateName, os.Stdin)
	}

	reader, err := client.GetContainerTemplateFile(containerName, templateName)
	if err != nil {
		return err
	}
	content, err := ioutil.ReadAll(reader)
	if err != nil {
		return err
	}

	// Spawn the editor
	content, err = shared.TextEditor("", content)
	if err != nil {
		return err
	}

	for {
		reader := bytes.NewReader(content)
		err := client.UpdateContainerTemplateFile(containerName, templateName, reader)
		// Respawn the editor
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.G("Error updating template file: %s")+"\n", err)
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
