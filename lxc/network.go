package main

import (
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
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/termios"
)

type networkCmd struct {
}

func (c *networkCmd) showByDefault() bool {
	return true
}

func (c *networkCmd) networkEditHelp() string {
	return i18n.G(
		`### This is a yaml representation of the network.
### Any line starting with a '# will be ignored.
###
### A network consists of a set of configuration items.
###
### An example would look like:
### name: lxdbr0
### config:
###   ipv4.address: 10.62.42.1/24
###   ipv4.nat: true
###   ipv6.address: fd00:56ad:9f7a:9800::1/64
###   ipv6.nat: true
### managed: true
### type: bridge
###
### Note that only the configuration can be changed.`)
}

func (c *networkCmd) usage() string {
	return i18n.G(
		`Usage: lxc network <subcommand> [options]

Manage and attach containers to networks.

lxc network list [<remote>:]
    List available networks.

lxc network show [<remote>:]<network>
    Show details of a network.

lxc network create [<remote>:]<network> [key=value...]
    Create a network.

lxc network get [<remote>:]<network> <key>
    Get network configuration.

lxc network set [<remote>:]<network> <key> <value>
    Set network configuration.

lxc network unset [<remote>:]<network> <key>
    Unset network configuration.

lxc network delete [<remote>:]<network>
    Delete a network.

lxc network edit [<remote>:]<network>
    Edit network, either by launching external editor or reading STDIN.

lxc network rename [<remote>:]<network> <new-name>
    Rename a network.

lxc network attach [<remote>:]<network> <container> [device name] [interface name]
    Attach a network interface connecting the network to a specified container.

lxc network attach-profile [<remote>:]<network> <profile> [device name] [interface name]
    Attach a network interface connecting the network to a specified profile.

lxc network detach [<remote>:]<network> <container> [device name]
    Remove a network interface connecting the network to a specified container.

lxc network detach-profile [<remote>:]<network> <container> [device name]
    Remove a network interface connecting the network to a specified profile.

*Examples*
cat network.yaml | lxc network edit <network>
    Update a network using the content of network.yaml`)
}

func (c *networkCmd) flags() {}

func (c *networkCmd) run(conf *config.Config, args []string) error {
	if len(args) < 1 {
		return errUsage
	}

	if args[0] == "list" {
		return c.doNetworkList(conf, args)
	}

	if len(args) < 2 {
		return errArgs
	}

	remote, network, err := conf.ParseRemote(args[1])
	if err != nil {
		return err
	}

	client, err := conf.GetContainerServer(remote)
	if err != nil {
		return err
	}

	switch args[0] {
	case "attach":
		return c.doNetworkAttach(client, network, args[2:])
	case "attach-profile":
		return c.doNetworkAttachProfile(client, network, args[2:])
	case "create":
		return c.doNetworkCreate(client, network, args[2:])
	case "delete":
		return c.doNetworkDelete(client, network)
	case "detach":
		return c.doNetworkDetach(client, network, args[2:])
	case "detach-profile":
		return c.doNetworkDetachProfile(client, network, args[2:])
	case "edit":
		return c.doNetworkEdit(client, network)
	case "rename":
		if len(args) != 3 {
			return errArgs
		}
		return c.doNetworkRename(client, network, args[2])
	case "get":
		return c.doNetworkGet(client, network, args[2:])
	case "set":
		return c.doNetworkSet(client, network, args[2:])
	case "unset":
		return c.doNetworkSet(client, network, args[2:])
	case "show":
		return c.doNetworkShow(client, network)
	default:
		return errArgs
	}
}

func (c *networkCmd) doNetworkAttach(client lxd.ContainerServer, name string, args []string) error {
	if len(args) < 1 || len(args) > 3 {
		return errArgs
	}

	// Default name is same as network
	devName := name
	if len(args) > 1 {
		devName = args[1]
	}

	// Get the network entry
	network, _, err := client.GetNetwork(name)
	if err != nil {
		return err
	}

	// Prepare the container's device entry
	device := map[string]string{
		"type":    "nic",
		"nictype": "macvlan",
		"parent":  name,
	}

	if network.Type == "bridge" {
		device["nictype"] = "bridged"
	}

	if len(args) > 2 {
		device["name"] = args[2]
	}

	// Add the device to the container
	err = containerDeviceAdd(client, args[0], devName, device)
	if err != nil {
		return err
	}

	return nil
}

func (c *networkCmd) doNetworkAttachProfile(client lxd.ContainerServer, name string, args []string) error {
	if len(args) < 1 || len(args) > 3 {
		return errArgs
	}

	// Default name is same as network
	devName := name
	if len(args) > 1 {
		devName = args[1]
	}

	// Get the network entry
	network, _, err := client.GetNetwork(name)
	if err != nil {
		return err
	}

	// Prepare the profile's device entry
	device := map[string]string{
		"type":    "nic",
		"nictype": "macvlan",
		"parent":  name,
	}

	if network.Type == "bridge" {
		device["nictype"] = "bridged"
	}

	if len(args) > 2 {
		device["name"] = args[2]
	}

	// Add the device to the profile
	err = profileDeviceAdd(client, args[0], devName, device)
	if err != nil {
		return err
	}

	return nil
}

func (c *networkCmd) doNetworkCreate(client lxd.ContainerServer, name string, args []string) error {
	network := api.NetworksPost{}
	network.Name = name
	network.Config = map[string]string{}

	for i := 0; i < len(args); i++ {
		entry := strings.SplitN(args[i], "=", 2)
		if len(entry) < 2 {
			return errArgs
		}

		network.Config[entry[0]] = entry[1]
	}

	err := client.CreateNetwork(network)
	if err != nil {
		return err
	}

	fmt.Printf(i18n.G("Network %s created")+"\n", name)
	return nil
}

func (c *networkCmd) doNetworkDetach(client lxd.ContainerServer, name string, args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return errArgs
	}

	// Default name is same as network
	devName := ""
	if len(args) > 1 {
		devName = args[1]
	}

	// Get the container entry
	container, etag, err := client.GetContainer(args[0])
	if err != nil {
		return err
	}

	// Find the device
	if devName == "" {
		for n, d := range container.Devices {
			if d["type"] == "nic" && d["parent"] == name {
				if devName != "" {
					return fmt.Errorf(i18n.G("More than one device matches, specify the device name."))
				}

				devName = n
			}
		}
	}

	if devName == "" {
		return fmt.Errorf(i18n.G("No device found for this network"))
	}

	device, ok := container.Devices[devName]
	if !ok {
		return fmt.Errorf(i18n.G("The specified device doesn't exist"))
	}

	if device["type"] != "nic" || device["parent"] != name {
		return fmt.Errorf(i18n.G("The specified device doesn't match the network"))
	}

	// Remove the device
	delete(container.Devices, devName)
	op, err := client.UpdateContainer(args[0], container.Writable(), etag)
	if err != nil {
		return err
	}

	return op.Wait()
}

func (c *networkCmd) doNetworkDetachProfile(client lxd.ContainerServer, name string, args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return errArgs
	}

	// Default name is same as network
	devName := ""
	if len(args) > 1 {
		devName = args[1]
	}

	// Get the profile entry
	profile, etag, err := client.GetProfile(args[0])
	if err != nil {
		return err
	}

	// Find the device
	if devName == "" {
		for n, d := range profile.Devices {
			if d["type"] == "nic" && d["parent"] == name {
				if devName != "" {
					return fmt.Errorf(i18n.G("More than one device matches, specify the device name."))
				}

				devName = n
			}
		}
	}

	if devName == "" {
		return fmt.Errorf(i18n.G("No device found for this network"))
	}

	device, ok := profile.Devices[devName]
	if !ok {
		return fmt.Errorf(i18n.G("The specified device doesn't exist"))
	}

	if device["type"] != "nic" || device["parent"] != name {
		return fmt.Errorf(i18n.G("The specified device doesn't match the network"))
	}

	// Remove the device
	delete(profile.Devices, devName)
	err = client.UpdateProfile(args[0], profile.Writable(), etag)
	if err != nil {
		return err
	}

	return nil
}

func (c *networkCmd) doNetworkDelete(client lxd.ContainerServer, name string) error {
	err := client.DeleteNetwork(name)
	if err != nil {
		return err
	}

	fmt.Printf(i18n.G("Network %s deleted")+"\n", name)
	return nil
}

func (c *networkCmd) doNetworkEdit(client lxd.ContainerServer, name string) error {
	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(int(syscall.Stdin)) {
		contents, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.NetworkPut{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}

		return client.UpdateNetwork(name, newdata, "")
	}

	// Extract the current value
	network, etag, err := client.GetNetwork(name)
	if err != nil {
		return err
	}

	if !network.Managed {
		return fmt.Errorf(i18n.G("Only managed networks can be modified."))
	}

	data, err := yaml.Marshal(&network)
	if err != nil {
		return err
	}

	// Spawn the editor
	content, err := shared.TextEditor("", []byte(c.networkEditHelp()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor
		newdata := api.NetworkPut{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = client.UpdateNetwork(name, newdata, etag)
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

func (c *networkCmd) doNetworkRename(client lxd.ContainerServer, name string, newName string) error {
	err := client.RenameNetwork(name, api.NetworkPost{Name: newName})
	if err != nil {
		return err
	}

	fmt.Printf(i18n.G("Network %s renamed to %s")+"\n", name, newName)
	return nil
}

func (c *networkCmd) doNetworkGet(client lxd.ContainerServer, name string, args []string) error {
	// we shifted @args so so it should read "<key>"
	if len(args) != 1 {
		return errArgs
	}

	resp, _, err := client.GetNetwork(name)
	if err != nil {
		return err
	}

	for k, v := range resp.Config {
		if k == args[0] {
			fmt.Printf("%s\n", v)
		}
	}
	return nil
}

func (c *networkCmd) doNetworkList(conf *config.Config, args []string) error {
	var remote string
	var err error

	if len(args) > 1 {
		var name string
		remote, name, err = conf.ParseRemote(args[1])
		if err != nil {
			return err
		}

		if name != "" {
			return fmt.Errorf(i18n.G("Filtering isn't supported yet"))
		}
	} else {
		remote = conf.DefaultRemote
	}

	client, err := conf.GetContainerServer(remote)
	if err != nil {
		return err
	}

	networks, err := client.GetNetworks()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, network := range networks {
		if shared.StringInSlice(network.Type, []string{"loopback", "unknown"}) {
			continue
		}

		strManaged := i18n.G("NO")
		if network.Managed {
			strManaged = i18n.G("YES")
		}

		strUsedBy := fmt.Sprintf("%d", len(network.UsedBy))
		data = append(data, []string{network.Name, network.Type, strManaged, network.Description, strUsedBy})
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetAutoWrapText(false)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetRowLine(true)
	table.SetHeader([]string{
		i18n.G("NAME"),
		i18n.G("TYPE"),
		i18n.G("MANAGED"),
		i18n.G("DESCRIPTION"),
		i18n.G("USED BY")})
	sort.Sort(byName(data))
	table.AppendBulk(data)
	table.Render()

	return nil
}

func (c *networkCmd) doNetworkSet(client lxd.ContainerServer, name string, args []string) error {
	// we shifted @args so so it should read "<key> [<value>]"
	if len(args) < 1 {
		return errArgs
	}

	network, etag, err := client.GetNetwork(name)
	if err != nil {
		return err
	}

	if !network.Managed {
		return fmt.Errorf(i18n.G("Only managed networks can be modified."))
	}

	key := args[0]
	var value string
	if len(args) < 2 {
		value = ""
	} else {
		value = args[1]
	}

	if !termios.IsTerminal(int(syscall.Stdin)) && value == "-" {
		buf, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf(i18n.G("Can't read from stdin: %s"), err)
		}
		value = string(buf[:])
	}

	network.Config[key] = value

	return client.UpdateNetwork(name, network.Writable(), etag)
}

func (c *networkCmd) doNetworkShow(client lxd.ContainerServer, name string) error {
	if name == "" {
		return errArgs
	}

	network, _, err := client.GetNetwork(name)
	if err != nil {
		return err
	}

	sort.Strings(network.UsedBy)

	data, err := yaml.Marshal(&network)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}
