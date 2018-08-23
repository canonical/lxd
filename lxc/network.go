package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"strings"
	"syscall"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/termios"
)

type cmdNetwork struct {
	global *cmdGlobal

	flagTarget string
}

func (c *cmdNetwork) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("network")
	cmd.Short = i18n.G("Manage and attach containers to networks")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage and attach containers to networks`))

	// Attach
	networkAttachCmd := cmdNetworkAttach{global: c.global, network: c}
	cmd.AddCommand(networkAttachCmd.Command())

	// Attach profile
	networkAttachProfileCmd := cmdNetworkAttachProfile{global: c.global, network: c}
	cmd.AddCommand(networkAttachProfileCmd.Command())

	// Create
	networkCreateCmd := cmdNetworkCreate{global: c.global, network: c}
	cmd.AddCommand(networkCreateCmd.Command())

	// Delete
	networkDeleteCmd := cmdNetworkDelete{global: c.global, network: c}
	cmd.AddCommand(networkDeleteCmd.Command())

	// Detach
	networkDetachCmd := cmdNetworkDetach{global: c.global, network: c}
	cmd.AddCommand(networkDetachCmd.Command())

	// Detach profile
	networkDetachProfileCmd := cmdNetworkDetachProfile{global: c.global, network: c}
	cmd.AddCommand(networkDetachProfileCmd.Command())

	// Edit
	networkEditCmd := cmdNetworkEdit{global: c.global, network: c}
	cmd.AddCommand(networkEditCmd.Command())

	// Get
	networkGetCmd := cmdNetworkGet{global: c.global, network: c}
	cmd.AddCommand(networkGetCmd.Command())

	// Info
	networkInfoCmd := cmdNetworkInfo{global: c.global, network: c}
	cmd.AddCommand(networkInfoCmd.Command())

	// List
	networkListCmd := cmdNetworkList{global: c.global, network: c}
	cmd.AddCommand(networkListCmd.Command())

	// List leases
	networkListLeasesCmd := cmdNetworkListLeases{global: c.global, network: c}
	cmd.AddCommand(networkListLeasesCmd.Command())

	// Rename
	networkRenameCmd := cmdNetworkRename{global: c.global, network: c}
	cmd.AddCommand(networkRenameCmd.Command())

	// Set
	networkSetCmd := cmdNetworkSet{global: c.global, network: c}
	cmd.AddCommand(networkSetCmd.Command())

	// Show
	networkShowCmd := cmdNetworkShow{global: c.global, network: c}
	cmd.AddCommand(networkShowCmd.Command())

	// Unset
	networkUnsetCmd := cmdNetworkUnset{global: c.global, network: c, networkSet: &networkSetCmd}
	cmd.AddCommand(networkUnsetCmd.Command())

	return cmd
}

// Attach
type cmdNetworkAttach struct {
	global  *cmdGlobal
	network *cmdNetwork
}

func (c *cmdNetworkAttach) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("attach [<remote>:]<network> <container> [<device name>] [<interface name>]")
	cmd.Short = i18n.G("Attach network interfaces to containers")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Attach new network interfaces to containers`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdNetworkAttach) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 2, 4)
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
		return fmt.Errorf(i18n.G("Missing network name"))
	}

	// Default name is same as network
	devName := resource.name
	if len(args) > 2 {
		devName = args[2]
	}

	// Get the network entry
	network, _, err := resource.server.GetNetwork(resource.name)
	if err != nil {
		return err
	}

	// Prepare the container's device entry
	device := map[string]string{
		"type":    "nic",
		"nictype": "macvlan",
		"parent":  resource.name,
	}

	if network.Type == "bridge" {
		device["nictype"] = "bridged"
	}

	if len(args) > 3 {
		device["name"] = args[3]
	}

	// Add the device to the container
	err = containerDeviceAdd(resource.server, args[1], devName, device)
	if err != nil {
		return err
	}

	return nil
}

// Attach profile
type cmdNetworkAttachProfile struct {
	global  *cmdGlobal
	network *cmdNetwork
}

func (c *cmdNetworkAttachProfile) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("attach-profile [<remote>:]<network> <profile> [<device name>] [<interface name>]")
	cmd.Short = i18n.G("Attach network interfaces to profiles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Attach network interfaces to profiles`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdNetworkAttachProfile) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 2, 4)
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
		return fmt.Errorf(i18n.G("Missing network name"))
	}

	// Default name is same as network
	devName := resource.name
	if len(args) > 2 {
		devName = args[2]
	}

	// Get the network entry
	network, _, err := resource.server.GetNetwork(resource.name)
	if err != nil {
		return err
	}

	// Prepare the profile's device entry
	device := map[string]string{
		"type":    "nic",
		"nictype": "macvlan",
		"parent":  resource.name,
	}

	if network.Type == "bridge" {
		device["nictype"] = "bridged"
	}

	if len(args) > 3 {
		device["name"] = args[3]
	}

	// Add the device to the profile
	err = profileDeviceAdd(resource.server, args[1], devName, device)
	if err != nil {
		return err
	}

	return nil
}

// Create
type cmdNetworkCreate struct {
	global  *cmdGlobal
	network *cmdNetwork
}

func (c *cmdNetworkCreate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("create [<remote>:]<network> [key=value...]")
	cmd.Short = i18n.G("Create new networks")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Create new networks`))

	cmd.Flags().StringVar(&c.network.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdNetworkCreate) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 1, -1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]
	client := resource.server

	// Create the network
	network := api.NetworksPost{}
	network.Name = resource.name
	network.Config = map[string]string{}

	for i := 1; i < len(args); i++ {
		entry := strings.SplitN(args[i], "=", 2)
		if len(entry) < 2 {
			return fmt.Errorf(i18n.G("Bad key/value pair: %s"), args[i])
		}

		network.Config[entry[0]] = entry[1]
	}

	// If a target member was specified the API won't actually create the
	// network, but only define it as pending in the database.
	if c.network.flagTarget != "" {
		client = client.UseTarget(c.network.flagTarget)
	}

	err = client.CreateNetwork(network)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		if c.network.flagTarget != "" {
			fmt.Printf(i18n.G("Network %s pending on member %s")+"\n", resource.name, c.network.flagTarget)
		} else {
			fmt.Printf(i18n.G("Network %s created")+"\n", resource.name)
		}
	}

	return nil
}

// Delete
type cmdNetworkDelete struct {
	global  *cmdGlobal
	network *cmdNetwork
}

func (c *cmdNetworkDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("delete [<remote>:]<network>")
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete networks")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Delete networks`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdNetworkDelete) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing network name"))
	}

	// Delete the network
	err = resource.server.DeleteNetwork(resource.name)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network %s deleted")+"\n", resource.name)
	}

	return nil
}

// Detach
type cmdNetworkDetach struct {
	global  *cmdGlobal
	network *cmdNetwork
}

func (c *cmdNetworkDetach) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("detach [<remote>:]<network> <container> [<device name>]")
	cmd.Short = i18n.G("Detach network interfaces from containers")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Detach network interfaces from containers`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdNetworkDetach) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 2, 3)
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
		return fmt.Errorf(i18n.G("Missing network name"))
	}

	// Default name is same as network
	devName := ""
	if len(args) > 2 {
		devName = args[2]
	}

	// Get the container entry
	container, etag, err := resource.server.GetContainer(args[1])
	if err != nil {
		return err
	}

	// Find the device
	if devName == "" {
		for n, d := range container.Devices {
			if d["type"] == "nic" && d["parent"] == resource.name {
				if devName != "" {
					return fmt.Errorf(i18n.G("More than one device matches, specify the device name"))
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

	if device["type"] != "nic" || device["parent"] != resource.name {
		return fmt.Errorf(i18n.G("The specified device doesn't match the network"))
	}

	// Remove the device
	delete(container.Devices, devName)
	op, err := resource.server.UpdateContainer(args[1], container.Writable(), etag)
	if err != nil {
		return err
	}

	return op.Wait()
}

// Detach profile
type cmdNetworkDetachProfile struct {
	global  *cmdGlobal
	network *cmdNetwork
}

func (c *cmdNetworkDetachProfile) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("detach-profile [<remote>:]<network> <container> [<device name>]")
	cmd.Short = i18n.G("Detach network interfaces from profiles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Detach network interfaces from profiles`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdNetworkDetachProfile) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 2, 3)
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
		return fmt.Errorf(i18n.G("Missing network name"))
	}

	// Default name is same as network
	devName := ""
	if len(args) > 2 {
		devName = args[2]
	}

	// Get the profile entry
	profile, etag, err := resource.server.GetProfile(args[1])
	if err != nil {
		return err
	}

	// Find the device
	if devName == "" {
		for n, d := range profile.Devices {
			if d["type"] == "nic" && d["parent"] == resource.name {
				if devName != "" {
					return fmt.Errorf(i18n.G("More than one device matches, specify the device name"))
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

	if device["type"] != "nic" || device["parent"] != resource.name {
		return fmt.Errorf(i18n.G("The specified device doesn't match the network"))
	}

	// Remove the device
	delete(profile.Devices, devName)
	err = resource.server.UpdateProfile(args[1], profile.Writable(), etag)
	if err != nil {
		return err
	}

	return nil
}

// Edit
type cmdNetworkEdit struct {
	global  *cmdGlobal
	network *cmdNetwork
}

func (c *cmdNetworkEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("edit [<remote>:]<network>")
	cmd.Short = i18n.G("Edit network configurations as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit network configurations as YAML`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdNetworkEdit) helpTemplate() string {
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

func (c *cmdNetworkEdit) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing network name"))
	}

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

		return resource.server.UpdateNetwork(resource.name, newdata, "")
	}

	// Extract the current value
	network, etag, err := resource.server.GetNetwork(resource.name)
	if err != nil {
		return err
	}

	if !network.Managed {
		return fmt.Errorf(i18n.G("Only managed networks can be modified"))
	}

	data, err := yaml.Marshal(&network)
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
		newdata := api.NetworkPut{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = resource.server.UpdateNetwork(resource.name, newdata, etag)
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

// Get
type cmdNetworkGet struct {
	global  *cmdGlobal
	network *cmdNetwork
}

func (c *cmdNetworkGet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("get [<remote>:]<network> <key>")
	cmd.Short = i18n.G("Get values for network configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Get values for network configuration keys`))

	cmd.Flags().StringVar(&c.network.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdNetworkGet) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]
	client := resource.server

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing network name"))
	}

	// Get the network key
	if c.network.flagTarget != "" {
		client = client.UseTarget(c.network.flagTarget)
	}

	resp, _, err := client.GetNetwork(resource.name)
	if err != nil {
		return err
	}

	for k, v := range resp.Config {
		if k == args[1] {
			fmt.Printf("%s\n", v)
		}
	}

	return nil
}

// Info
type cmdNetworkInfo struct {
	global  *cmdGlobal
	network *cmdNetwork
}

func (c *cmdNetworkInfo) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("info [<remote>:]<network>")
	cmd.Short = i18n.G("Get runtime information on networks")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Get runtime information on networks`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdNetworkInfo) Run(cmd *cobra.Command, args []string) error {
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
	client := resource.server

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing network name"))
	}

	state, err := client.GetNetworkState(resource.name)
	if err != nil {
		return err
	}

	// Interface information
	fmt.Printf(i18n.G("Name: %s")+"\n", resource.name)
	fmt.Printf(i18n.G("MAC address: %s")+"\n", state.Hwaddr)
	fmt.Printf(i18n.G("MTU: %d")+"\n", state.Mtu)
	fmt.Printf(i18n.G("State: %s")+"\n", state.State)

	// IP addresses
	fmt.Println("")
	fmt.Println(i18n.G("Ips:"))
	for _, addr := range state.Addresses {
		fmt.Printf("  %s\t%s\n", addr.Family, addr.Address)
	}

	// Network usage
	fmt.Println("")
	fmt.Println(i18n.G("Network usage:"))
	fmt.Printf("  %s: %s\n", i18n.G("Bytes received"), shared.GetByteSizeString(state.Counters.BytesReceived, 2))
	fmt.Printf("  %s: %s\n", i18n.G("Bytes sent"), shared.GetByteSizeString(state.Counters.BytesSent, 2))
	fmt.Printf("  %s: %d\n", i18n.G("Packets received"), state.Counters.PacketsReceived)
	fmt.Printf("  %s: %d\n", i18n.G("Packets sent"), state.Counters.PacketsSent)

	return nil
}

// List
type cmdNetworkList struct {
	global  *cmdGlobal
	network *cmdNetwork

	flagFormat string
}

func (c *cmdNetworkList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("list [<remote>:]")
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List available networks")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List available networks`))

	cmd.RunE = c.Run
	cmd.Flags().StringVar(&c.flagFormat, "format", "table", i18n.G("Format (csv|json|table|yaml)")+"``")

	return cmd
}

func (c *cmdNetworkList) Run(cmd *cobra.Command, args []string) error {
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

	// List the networks
	if resource.name != "" {
		return fmt.Errorf(i18n.G("Filtering isn't supported yet"))
	}

	networks, err := resource.server.GetNetworks()
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
		details := []string{network.Name, network.Type, strManaged, network.Description, strUsedBy}
		if resource.server.IsClustered() {
			details = append(details, strings.ToUpper(network.Status))
		}
		data = append(data, details)
	}

	header := []string{
		i18n.G("NAME"),
		i18n.G("TYPE"),
		i18n.G("MANAGED"),
		i18n.G("DESCRIPTION"),
		i18n.G("USED BY"),
	}
	if resource.server.IsClustered() {
		header = append(header, i18n.G("STATE"))
	}

	switch c.flagFormat {
	case listFormatTable:
		table := tablewriter.NewWriter(os.Stdout)
		table.SetAutoWrapText(false)
		table.SetAlignment(tablewriter.ALIGN_LEFT)
		table.SetRowLine(true)
		table.SetHeader(header)
		sort.Sort(byName(data))
		table.AppendBulk(data)
		table.Render()
	case listFormatCSV:
		sort.Sort(byName(data))
		data = append(data, []string{})
		copy(data[1:], data[0:])
		data[0] = header
		w := csv.NewWriter(os.Stdout)
		w.WriteAll(data)
		if err := w.Error(); err != nil {
			return err
		}
	case listFormatJSON:
		data := networks
		enc := json.NewEncoder(os.Stdout)
		err := enc.Encode(data)
		if err != nil {
			return err
		}
	case listFormatYAML:
		data := networks
		out, err := yaml.Marshal(data)
		if err != nil {
			return err
		}
		fmt.Printf("%s", out)
	default:
		return fmt.Errorf(i18n.G("Invalid format %q"), c.flagFormat)
	}

	return nil
}

// List leases
type cmdNetworkListLeases struct {
	global  *cmdGlobal
	network *cmdNetwork
}

func (c *cmdNetworkListLeases) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("list-leases [<remote>:]<network>")
	cmd.Short = i18n.G("List DHCP leases")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List DHCP leases`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdNetworkListLeases) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing network name"))
	}

	// List DHCP leases
	leases, err := resource.server.GetNetworkLeases(resource.name)
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, lease := range leases {
		data = append(data, []string{lease.Hostname, lease.Hwaddr, lease.Address, strings.ToUpper(lease.Type)})
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetAutoWrapText(false)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetRowLine(true)
	table.SetHeader([]string{
		i18n.G("HOSTNAME"),
		i18n.G("MAC ADDRESS"),
		i18n.G("IP ADDRESS"),
		i18n.G("TYPE")})
	sort.Sort(byName(data))
	table.AppendBulk(data)
	table.Render()

	return nil
}

// Rename
type cmdNetworkRename struct {
	global  *cmdGlobal
	network *cmdNetwork
}

func (c *cmdNetworkRename) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("rename [<remote>:]<network> <new-name>")
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename networks")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Rename networks`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdNetworkRename) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
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
		return fmt.Errorf(i18n.G("Missing network name"))
	}

	// Rename the network
	err = resource.server.RenameNetwork(resource.name, api.NetworkPost{Name: args[1]})
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network %s renamed to %s")+"\n", resource.name, args[1])
	}

	return nil
}

// Set
type cmdNetworkSet struct {
	global  *cmdGlobal
	network *cmdNetwork
}

func (c *cmdNetworkSet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("set [<remote>:]<network> <key> <value>")
	cmd.Short = i18n.G("Set network configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Set network configuration keys`))

	cmd.Flags().StringVar(&c.network.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdNetworkSet) Run(cmd *cobra.Command, args []string) error {
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
	client := resource.server

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing network name"))
	}

	// Set the config key
	if c.network.flagTarget != "" {
		client = client.UseTarget(c.network.flagTarget)
	}

	network, etag, err := client.GetNetwork(resource.name)
	if err != nil {
		return err
	}

	if !network.Managed {
		return fmt.Errorf(i18n.G("Only managed networks can be modified"))
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

	network.Config[key] = value

	return client.UpdateNetwork(resource.name, network.Writable(), etag)
}

// Show
type cmdNetworkShow struct {
	global  *cmdGlobal
	network *cmdNetwork
}

func (c *cmdNetworkShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("show [<remote>:]<network>")
	cmd.Short = i18n.G("Show network configurations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show network configurations`))

	cmd.Flags().StringVar(&c.network.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdNetworkShow) Run(cmd *cobra.Command, args []string) error {
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
	client := resource.server

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing network name"))
	}

	// Show the network config
	if c.network.flagTarget != "" {
		client = client.UseTarget(c.network.flagTarget)
	}

	network, _, err := client.GetNetwork(resource.name)
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

// Unset
type cmdNetworkUnset struct {
	global     *cmdGlobal
	network    *cmdNetwork
	networkSet *cmdNetworkSet
}

func (c *cmdNetworkUnset) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("unset [<remote>:]<network> <key>")
	cmd.Short = i18n.G("Unset network configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Unset network configuration keys`))

	cmd.Flags().StringVar(&c.network.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdNetworkUnset) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	args = append(args, "")
	return c.networkSet.Run(cmd, args)
}
