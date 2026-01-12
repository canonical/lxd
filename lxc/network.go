package main

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v2"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/termios"
	"github.com/canonical/lxd/shared/units"
)

type cmdNetwork struct {
	global *cmdGlobal

	flagTarget string
	flagType   string
}

func (c *cmdNetwork) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("network")
	cmd.Short = "Manage and attach instances to networks"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	// Attach
	networkAttachCmd := cmdNetworkAttach{global: c.global, network: c}
	cmd.AddCommand(networkAttachCmd.command())

	// Attach profile
	networkAttachProfileCmd := cmdNetworkAttachProfile{global: c.global, network: c}
	cmd.AddCommand(networkAttachProfileCmd.command())

	// Create
	networkCreateCmd := cmdNetworkCreate{global: c.global, network: c}
	cmd.AddCommand(networkCreateCmd.command())

	// Delete
	networkDeleteCmd := cmdNetworkDelete{global: c.global, network: c}
	cmd.AddCommand(networkDeleteCmd.command())

	// Detach
	networkDetachCmd := cmdNetworkDetach{global: c.global, network: c}
	cmd.AddCommand(networkDetachCmd.command())

	// Detach profile
	networkDetachProfileCmd := cmdNetworkDetachProfile{global: c.global, network: c}
	cmd.AddCommand(networkDetachProfileCmd.command())

	// Edit
	networkEditCmd := cmdNetworkEdit{global: c.global, network: c}
	cmd.AddCommand(networkEditCmd.command())

	// Get
	networkGetCmd := cmdNetworkGet{global: c.global, network: c}
	cmd.AddCommand(networkGetCmd.command())

	// Info
	networkInfoCmd := cmdNetworkInfo{global: c.global, network: c}
	cmd.AddCommand(networkInfoCmd.command())

	// List
	networkListCmd := cmdNetworkList{global: c.global, network: c}
	cmd.AddCommand(networkListCmd.command())

	// List allocations
	networkListAllocationsCmd := cmdNetworkListAllocations{global: c.global, network: c}
	cmd.AddCommand(networkListAllocationsCmd.command())

	// List leases
	networkListLeasesCmd := cmdNetworkListLeases{global: c.global, network: c}
	cmd.AddCommand(networkListLeasesCmd.command())

	// Rename
	networkRenameCmd := cmdNetworkRename{global: c.global, network: c}
	cmd.AddCommand(networkRenameCmd.command())

	// Set
	networkSetCmd := cmdNetworkSet{global: c.global, network: c}
	cmd.AddCommand(networkSetCmd.command())

	// Show
	networkShowCmd := cmdNetworkShow{global: c.global, network: c}
	cmd.AddCommand(networkShowCmd.command())

	// Unset
	networkUnsetCmd := cmdNetworkUnset{global: c.global, network: c, networkSet: &networkSetCmd}
	cmd.AddCommand(networkUnsetCmd.command())

	// ACL
	networkACLCmd := cmdNetworkACL{global: c.global}
	cmd.AddCommand(networkACLCmd.command())

	// Forward
	networkForwardCmd := cmdNetworkForward{global: c.global}
	cmd.AddCommand(networkForwardCmd.command())

	// Load Balancer
	networkLoadBalancerCmd := cmdNetworkLoadBalancer{global: c.global}
	cmd.AddCommand(networkLoadBalancerCmd.command())

	// Peer
	networkPeerCmd := cmdNetworkPeer{global: c.global}
	cmd.AddCommand(networkPeerCmd.command())

	// Zone
	networkZoneCmd := cmdNetworkZone{global: c.global}
	cmd.AddCommand(networkZoneCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// Attach.
type cmdNetworkAttach struct {
	global  *cmdGlobal
	network *cmdNetwork
}

func (c *cmdNetworkAttach) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("attach", "[<remote>:]<network> <instance> [<device name>] [<interface name>]")
	cmd.Short = "Attach network interfaces to instances"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network", toComplete)
		}

		remote, _, err := c.global.conf.ParseRemote(args[0])
		if err != nil {
			return handleCompletionError(err)
		}

		if len(args) == 1 {
			return c.global.cmpTopLevelResourceInRemote(remote, "instance", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkAttach) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
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
		return errors.New("Missing network name")
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

	// Prepare the instance's device entry
	var device map[string]string
	if network.Managed && resource.server.HasExtension("instance_nic_network") {
		// If network is managed, use the network property rather than nictype, so that the network's
		// inherited properties are loaded into the NIC when started.
		device = map[string]string{
			"type":    "nic",
			"network": network.Name,
		}
	} else {
		// If network is unmanaged default to using a macvlan connected to the specified interface.
		device = map[string]string{
			"type":    "nic",
			"nictype": "macvlan",
			"parent":  resource.name,
		}

		if network.Type == "bridge" {
			// If the network type is an unmanaged bridge, use bridged NIC type.
			device["nictype"] = "bridged"
		}
	}

	if len(args) > 3 {
		device["name"] = args[3]
	}

	// Add the device to the instance
	err = instanceDeviceAdd(resource.server, args[1], devName, device)
	if err != nil {
		return err
	}

	return nil
}

// Attach profile.
type cmdNetworkAttachProfile struct {
	global  *cmdGlobal
	network *cmdNetwork
}

func (c *cmdNetworkAttachProfile) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("attach-profile", "[<remote>:]<network> <profile> [<device name>] [<interface name>]")
	cmd.Short = "Attach network interfaces to profiles"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 1 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network", toComplete)
		}

		remote, _, err := c.global.conf.ParseRemote(args[0])
		if err != nil {
			return handleCompletionError(err)
		}

		return c.global.cmpTopLevelResourceInRemote(remote, "profile", toComplete)
	}

	return cmd
}

func (c *cmdNetworkAttachProfile) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
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
		return errors.New("Missing network name")
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

// Create.
type cmdNetworkCreate struct {
	global  *cmdGlobal
	network *cmdNetwork
}

func (c *cmdNetworkCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", "[<remote>:]<network> [key=value...]")
	cmd.Short = "Create new networks"
	cmd.Long = cli.FormatSection("Description", cmd.Short)
	cmd.Example = cli.FormatSection("", `lxc network create foo
    Create a new network called foo

lxc network create bar network=baz --type ovn
    Create a new OVN network called bar using baz as its uplink network`)

	cmd.Flags().StringVar(&c.network.flagTarget, "target", "",  cli.FormatStringFlagLabel("Cluster member name"))
	cmd.Flags().StringVarP(&c.network.flagType, "type", "t", "", cli.FormatStringFlagLabel("Network type"))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpRemotes(toComplete, ":", true, instanceServerRemoteCompletionFilters(*c.global.conf)...)
	}

	return cmd
}

func (c *cmdNetworkCreate) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
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
	network.Type = c.network.flagType

	for i := 1; i < len(args); i++ {
		entry := strings.SplitN(args[i], "=", 2)
		if len(entry) < 2 {
			return fmt.Errorf("Bad key/value pair: %s", args[i])
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
			fmt.Printf("Network %s pending on member %s\n", resource.name, c.network.flagTarget)
		} else {
			fmt.Printf("Network %s created\n", resource.name)
		}
	}

	return nil
}

// Delete.
type cmdNetworkDelete struct {
	global  *cmdGlobal
	network *cmdNetwork
}

func (c *cmdNetworkDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", "[<remote>:]<network>")
	cmd.Aliases = []string{"rm"}
	cmd.Short = "Delete networks"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpTopLevelResource("network", toComplete)
	}

	return cmd
}

func (c *cmdNetworkDelete) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
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
		return errors.New("Missing network name")
	}

	// Delete the network
	err = resource.server.DeleteNetwork(resource.name)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf("Network %s deleted\n", resource.name)
	}

	return nil
}

// Detach.
type cmdNetworkDetach struct {
	global  *cmdGlobal
	network *cmdNetwork
}

func (c *cmdNetworkDetach) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("detach", "[<remote>:]<network> <instance> [<device name>]")
	cmd.Short = "Detach network interfaces from instances"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkInstances(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkDetach) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
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
		return errors.New("Missing network name")
	}

	// Default name is same as network
	devName := ""
	if len(args) > 2 {
		devName = args[2]
	}

	// Get the instance entry
	inst, etag, err := resource.server.GetInstance(args[1])
	if err != nil {
		return err
	}

	// Find the device
	if devName == "" {
		for n, d := range inst.Devices {
			if d["type"] == "nic" && (d["parent"] == resource.name || d["network"] == resource.name) {
				if devName != "" {
					return errors.New("More than one device matches, specify the device name")
				}

				devName = n
			}
		}
	}

	if devName == "" {
		return errors.New("No device found for this network")
	}

	device, ok := inst.Devices[devName]
	if !ok {
		return errors.New("The specified device doesn't exist")
	}

	if device["type"] != "nic" || (device["parent"] != resource.name && device["network"] != resource.name) {
		return errors.New("The specified device doesn't match the network")
	}

	// Remove the device
	delete(inst.Devices, devName)
	op, err := resource.server.UpdateInstance(args[1], inst.Writable(), etag)
	if err != nil {
		return err
	}

	return op.Wait()
}

// Detach profile.
type cmdNetworkDetachProfile struct {
	global  *cmdGlobal
	network *cmdNetwork
}

func (c *cmdNetworkDetachProfile) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("detach-profile", "[<remote>:]<network> <profile> [<device name>]")
	cmd.Short = "Detach network interfaces from profiles"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkProfiles(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkDetachProfile) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
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
		return errors.New("Missing network name")
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
			if d["type"] == "nic" && (d["parent"] == resource.name || d["network"] == resource.name) {
				if devName != "" {
					return errors.New("More than one device matches, specify the device name")
				}

				devName = n
			}
		}
	}

	if devName == "" {
		return errors.New("No device found for this network")
	}

	device, ok := profile.Devices[devName]
	if !ok {
		return errors.New("The specified device doesn't exist")
	}

	if device["type"] != "nic" || (device["parent"] != resource.name && device["network"] != resource.name) {
		return errors.New("The specified device doesn't match the network")
	}

	// Remove the device
	delete(profile.Devices, devName)
	op, err := resource.server.UpdateProfile(args[1], profile.Writable(), etag)
	if err != nil {
		return err
	}

	return op.Wait()
}

// Edit.
type cmdNetworkEdit struct {
	global  *cmdGlobal
	network *cmdNetwork
}

func (c *cmdNetworkEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", "[<remote>:]<network>")
	cmd.Short = "Edit network configurations as YAML"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpTopLevelResource("network", toComplete)
	}

	return cmd
}

func (c *cmdNetworkEdit) helpTemplate() string {
	return `### This is a YAML representation of the network.
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
### Note that only the configuration can be changed.`
}

func (c *cmdNetworkEdit) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
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
		return errors.New("Missing network name")
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
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
		return errors.New("Only managed networks can be modified")
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
			fmt.Fprintf(os.Stderr, "Config parsing error: %s\n", err)
			fmt.Println("Press enter to open the editor again or ctrl+c to abort change")

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

// Get.
type cmdNetworkGet struct {
	global  *cmdGlobal
	network *cmdNetwork

	flagIsProperty bool
}

func (c *cmdNetworkGet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get", "[<remote>:]<network> <key>")
	cmd.Short = "Get values for network configuration keys"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.Flags().StringVar(&c.network.flagTarget, "target", "", cli.FormatStringFlagLabel("Cluster member name"))
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, "Get the key as a network property")
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkConfigs(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkGet) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
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
		return errors.New("Missing network name")
	}

	// Get the network key
	if c.network.flagTarget != "" {
		client = client.UseTarget(c.network.flagTarget)
	}

	resp, _, err := client.GetNetwork(resource.name)
	if err != nil {
		return err
	}

	if c.flagIsProperty {
		w := resp.Writable()
		res, err := getFieldByJSONTag(&w, args[1])
		if err != nil {
			return fmt.Errorf("The property %q does not exist on the network %q: %v", args[1], resource.name, err)
		}

		fmt.Printf("%v\n", res)
	} else {
		for k, v := range resp.Config {
			if k == args[1] {
				fmt.Printf("%s\n", v)
			}
		}
	}

	return nil
}

// Info.
type cmdNetworkInfo struct {
	global  *cmdGlobal
	network *cmdNetwork
}

func (c *cmdNetworkInfo) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("info", "[<remote>:]<network>")
	cmd.Short = "Get runtime information on networks"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.Flags().StringVar(&c.network.flagTarget, "target", "", cli.FormatStringFlagLabel("Cluster member name"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpTopLevelResource("network", toComplete)
	}

	return cmd
}

func (c *cmdNetworkInfo) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]
	client := resource.server

	if resource.name == "" {
		return errors.New("Missing network name")
	}

	// Targeting.
	if c.network.flagTarget != "" {
		if !client.IsClustered() {
			return errors.New("To use --target, the destination remote must be a cluster")
		}

		client = client.UseTarget(c.network.flagTarget)
	}

	state, err := client.GetNetworkState(resource.name)
	if err != nil {
		return err
	}

	// Interface information.
	fmt.Printf("Name: %s\n", resource.name)
	fmt.Printf("MAC address: %s\n", state.Hwaddr)
	fmt.Printf("MTU: %d\n", state.Mtu)
	fmt.Printf("State: %s\n", state.State)
	fmt.Printf("Type: %s\n", state.Type)

	// IP addresses.
	if len(state.Addresses) > 0 {
		fmt.Println("")
		fmt.Println("IP addresses:")
		for _, addr := range state.Addresses {
			fmt.Printf("  %s\t%s/%s (%s)\n", addr.Family, addr.Address, addr.Netmask, addr.Scope)
		}
	}

	// Network usage.
	fmt.Println("")
	fmt.Println("Network usage:")
	fmt.Printf("  %s: %s\n", "Bytes received", units.GetByteSizeString(state.Counters.BytesReceived, 2))
	fmt.Printf("  %s: %s\n", "Bytes sent", units.GetByteSizeString(state.Counters.BytesSent, 2))
	fmt.Printf("  %s: %d\n", "Packets received", state.Counters.PacketsReceived)
	fmt.Printf("  %s: %d\n", "Packets sent", state.Counters.PacketsSent)

	// Bond information.
	if state.Bond != nil {
		fmt.Println("")
		fmt.Println("Bond:")
		fmt.Printf("  %s: %s\n", "Mode", state.Bond.Mode)
		fmt.Printf("  %s: %s\n", "Transmit policy", state.Bond.TransmitPolicy)
		fmt.Printf("  %s: %d\n", "Up delay", state.Bond.UpDelay)
		fmt.Printf("  %s: %d\n", "Down delay", state.Bond.DownDelay)
		fmt.Printf("  %s: %d\n", "MII Frequency", state.Bond.MIIFrequency)
		fmt.Printf("  %s: %s\n", "MII state", state.Bond.MIIState)
		fmt.Printf("  %s: %s\n", "Lower devices", strings.Join(state.Bond.LowerDevices, ", "))
	}

	// Bridge information.
	if state.Bridge != nil {
		fmt.Println("")
		fmt.Println("Bridge:")
		fmt.Printf("  %s: %s\n", "ID", state.Bridge.ID)
		fmt.Printf("  %s: %v\n", "STP", state.Bridge.STP)
		fmt.Printf("  %s: %d\n", "Forward delay", state.Bridge.ForwardDelay)
		fmt.Printf("  %s: %d\n", "Default VLAN ID", state.Bridge.VLANDefault)
		fmt.Printf("  %s: %v\n", "VLAN filtering", state.Bridge.VLANFiltering)
		fmt.Printf("  %s: %s\n", "Upper devices", strings.Join(state.Bridge.UpperDevices, ", "))
	}

	// VLAN information.
	if state.VLAN != nil {
		fmt.Println("")
		fmt.Println("VLAN:")
		fmt.Printf("  %s: %s\n", "Lower device", state.VLAN.LowerDevice)
		fmt.Printf("  %s: %d\n", "VLAN ID", state.VLAN.VID)
	}

	// OVN information.
	if state.OVN != nil {
		fmt.Println("")
		fmt.Println("OVN:")
		fmt.Printf("  %s: %s\n", "Chassis", state.OVN.Chassis)
	}

	return nil
}

// List.
type cmdNetworkList struct {
	global  *cmdGlobal
	network *cmdNetwork

	flagFormat      string
	flagTarget      string
	flagAllProjects bool
}

func (c *cmdNetworkList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", "[<remote>:]")
	cmd.Aliases = []string{"ls"}
	cmd.Short = "List available networks"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.RunE = c.run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", cli.FormatStringFlagLabel("Format (csv|json|table|yaml|compact)"))
	cmd.Flags().StringVar(&c.flagTarget, "target", "", cli.FormatStringFlagLabel("Cluster member name"))
	cmd.Flags().BoolVar(&c.flagAllProjects, "all-projects", false, "Display networks from all projects")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpRemotes(toComplete, ":", true, instanceServerRemoteCompletionFilters(*c.global.conf)...)
	}

	return cmd
}

func (c *cmdNetworkList) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
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
		return errors.New("Filtering isn't supported yet")
	}

	client := resource.server

	// Targeting.
	if c.flagTarget != "" {
		client = client.UseTarget(c.flagTarget)
	}

	var networks []api.Network
	if c.flagAllProjects {
		networks, err = client.GetNetworksAllProjects()
		if err != nil {
			return err
		}
	} else {
		networks, err = client.GetNetworks()
		if err != nil {
			return err
		}
	}

	data := [][]string{}
	for _, network := range networks {
		if slices.Contains([]string{"loopback", "unknown"}, network.Type) {
			continue
		}

		strManaged := "NO"
		if network.Managed {
			strManaged = "YES"
		}

		strUsedBy := strconv.Itoa(len(network.UsedBy))
		details := []string{
			network.Name,
			network.Type,
			strManaged,
			network.Config["ipv4.address"],
			network.Config["ipv6.address"],
			network.Description,
			strUsedBy,
			strings.ToUpper(network.Status),
		}

		if c.flagAllProjects {
			details = append([]string{network.Project}, details...)
		}

		data = append(data, details)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		"NAME",
		"TYPE",
		"MANAGED",
		"IPV4",
		"IPV6",
		"DESCRIPTION",
		"USED BY",
		"STATE",
	}

	if c.flagAllProjects {
		header = append([]string{"PROJECT"}, header...)
	}

	return cli.RenderTable(c.flagFormat, header, data, networks)
}

// List leases.
type cmdNetworkListLeases struct {
	global  *cmdGlobal
	network *cmdNetwork

	flagFormat string
}

func (c *cmdNetworkListLeases) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list-leases", "[<remote>:]<network>")
	cmd.Short = "List DHCP leases"
	cmd.Long = cli.FormatSection("Description", cmd.Short)
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", cli.FormatStringFlagLabel("Format (csv|json|table|yaml|compact)"))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpTopLevelResource("network", toComplete)
	}

	return cmd
}

func (c *cmdNetworkListLeases) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
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
		return errors.New("Missing network name")
	}

	// List DHCP leases
	leases, err := resource.server.GetNetworkLeases(resource.name)
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, lease := range leases {
		entry := []string{lease.Hostname, lease.Hwaddr, lease.Address, strings.ToUpper(lease.Type)}
		if resource.server.IsClustered() {
			entry = append(entry, lease.Location)
		}

		data = append(data, entry)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		"HOSTNAME",
		"MAC ADDRESS",
		"IP ADDRESS",
		"TYPE",
	}

	if resource.server.IsClustered() {
		header = append(header, "LOCATION")
	}

	return cli.RenderTable(c.flagFormat, header, data, leases)
}

// Rename.
type cmdNetworkRename struct {
	global  *cmdGlobal
	network *cmdNetwork
}

func (c *cmdNetworkRename) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rename", "[<remote>:]<network> <new-name>")
	cmd.Aliases = []string{"mv"}
	cmd.Short = "Rename networks"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpTopLevelResource("network", toComplete)
	}

	return cmd
}

func (c *cmdNetworkRename) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
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
		return errors.New("Missing network name")
	}

	// Rename the network
	err = resource.server.RenameNetwork(resource.name, api.NetworkPost{Name: args[1]})
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf("Network %s renamed to %s\n", resource.name, args[1])
	}

	return nil
}

// Set.
type cmdNetworkSet struct {
	global  *cmdGlobal
	network *cmdNetwork

	flagIsProperty bool
}

func (c *cmdNetworkSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set", "[<remote>:]<network> <key>=<value>...")
	cmd.Short = "Set network configuration keys"
	cmd.Long = cli.FormatSection("Description", `Set network configuration keys

For backward compatibility, a single configuration key may still be set with:
    lxc network set [<remote>:]<network> <key> <value>`)

	cmd.Flags().StringVar(&c.network.flagTarget, "target", "", cli.FormatStringFlagLabel("Cluster member name"))
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, "Set the key as a network property")
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpTopLevelResource("network", toComplete)
	}

	return cmd
}

func (c *cmdNetworkSet) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, -1)
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
		return errors.New("Missing network name")
	}

	// Handle targeting
	if c.network.flagTarget != "" {
		client = client.UseTarget(c.network.flagTarget)
	}

	// Get the network
	network, etag, err := client.GetNetwork(resource.name)
	if err != nil {
		return err
	}

	if !network.Managed {
		return errors.New("Only managed networks can be modified")
	}

	// Set the keys
	keys, err := getConfig(args[1:]...)
	if err != nil {
		return err
	}

	writable := network.Writable()
	if c.flagIsProperty {
		if cmd.Name() == "unset" {
			for k := range keys {
				err := unsetFieldByJSONTag(&writable, k)
				if err != nil {
					return fmt.Errorf("Error unsetting property: %v", err)
				}
			}
		} else {
			err := unpackKVToWritable(&writable, keys)
			if err != nil {
				return fmt.Errorf("Error setting properties: %v", err)
			}
		}
	} else {
		maps.Copy(writable.Config, keys)
	}

	return client.UpdateNetwork(resource.name, writable, etag)
}

// Show.
type cmdNetworkShow struct {
	global  *cmdGlobal
	network *cmdNetwork
}

func (c *cmdNetworkShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", "[<remote>:]<network>")
	cmd.Short = "Show network configurations"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.Flags().StringVar(&c.network.flagTarget, "target", "", cli.FormatStringFlagLabel("Cluster member name"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpTopLevelResource("network", toComplete)
	}

	return cmd
}

func (c *cmdNetworkShow) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
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
		return errors.New("Missing network name")
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

// Unset.
type cmdNetworkUnset struct {
	global     *cmdGlobal
	network    *cmdNetwork
	networkSet *cmdNetworkSet

	flagIsProperty bool
}

func (c *cmdNetworkUnset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset", "[<remote>:]<network> <key>")
	cmd.Short = "Unset network configuration keys"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.Flags().StringVar(&c.network.flagTarget, "target", "", cli.FormatStringFlagLabel("Cluster member name"))
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, "Unset the key as a network property")
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkConfigs(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkUnset) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	c.networkSet.flagIsProperty = c.flagIsProperty

	args = append(args, "")
	return c.networkSet.run(cmd, args)
}
