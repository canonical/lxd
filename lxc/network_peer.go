package main

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v2"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/termios"
)

type cmdNetworkPeer struct {
	global *cmdGlobal
}

func (c *cmdNetworkPeer) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("peer")
	cmd.Short = "Manage network peerings"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	// List.
	networkPeerListCmd := cmdNetworkPeerList{global: c.global, networkPeer: c}
	cmd.AddCommand(networkPeerListCmd.command())

	// Show.
	networkPeerShowCmd := cmdNetworkPeerShow{global: c.global, networkPeer: c}
	cmd.AddCommand(networkPeerShowCmd.command())

	// Create.
	networkPeerCreateCmd := cmdNetworkPeerCreate{global: c.global, networkPeer: c}
	cmd.AddCommand(networkPeerCreateCmd.command())

	// Get,
	networkPeerGetCmd := cmdNetworkPeerGet{global: c.global, networkPeer: c}
	cmd.AddCommand(networkPeerGetCmd.command())

	// Set.
	networkPeerSetCmd := cmdNetworkPeerSet{global: c.global, networkPeer: c}
	cmd.AddCommand(networkPeerSetCmd.command())

	// Unset.
	networkPeerUnsetCmd := cmdNetworkPeerUnset{global: c.global, networkPeer: c, networkPeerSet: &networkPeerSetCmd}
	cmd.AddCommand(networkPeerUnsetCmd.command())

	// Edit.
	networkPeerEditCmd := cmdNetworkPeerEdit{global: c.global, networkPeer: c}
	cmd.AddCommand(networkPeerEditCmd.command())

	// Delete.
	networkPeerDeleteCmd := cmdNetworkPeerDelete{global: c.global, networkPeer: c}
	cmd.AddCommand(networkPeerDeleteCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// List.
type cmdNetworkPeerList struct {
	global      *cmdGlobal
	networkPeer *cmdNetworkPeer

	flagFormat string
}

func (c *cmdNetworkPeerList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", "[<remote>:]<network>")
	cmd.Aliases = []string{"ls"}
	cmd.Short = "List available network peers"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.RunE = c.run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", cli.FormatStringFlagLabel("Format (csv|json|table|yaml|compact"))

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkPeerList) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote.
	remote := ""
	if len(args) > 0 {
		remote = args[0]
	}

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New("Missing network name")
	}

	peers, err := resource.server.GetNetworkPeers(resource.name)
	if err != nil {
		return err
	}

	data := make([][]string, 0, len(peers))
	for _, peer := range peers {
		targetPeer := "Unknown"

		if peer.TargetProject != "" && peer.TargetNetwork != "" {
			targetPeer = peer.TargetProject + "/" + peer.TargetNetwork
		}

		details := []string{
			peer.Name,
			peer.Description,
			targetPeer,
			strings.ToUpper(peer.Status),
		}

		data = append(data, details)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		"NAME",
		"DESCRIPTION",
		"PEER",
		"STATE",
	}

	return cli.RenderTable(c.flagFormat, header, data, peers)
}

// Show.
type cmdNetworkPeerShow struct {
	global      *cmdGlobal
	networkPeer *cmdNetworkPeer
}

func (c *cmdNetworkPeerShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", "[<remote>:]<network> <peer name>")
	cmd.Short = "Show network peer configurations"
	cmd.Long = cli.FormatSection("Description", cmd.Short)
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkPeers(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkPeerShow) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New("Missing network name")
	}

	if args[1] == "" {
		return errors.New("Missing peer name")
	}

	client := resource.server

	// Show the network peer config.
	peer, _, err := client.GetNetworkPeer(resource.name, args[1])
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&peer)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Create.
type cmdNetworkPeerCreate struct {
	global      *cmdGlobal
	networkPeer *cmdNetworkPeer
}

func (c *cmdNetworkPeerCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", "[<remote>:]<network> <peer_name> <[target project/]target_network> [key=value...]")
	cmd.Short = "Create new network peering"
	cmd.Long = cli.FormatSection("Description", cmd.Short)
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkPeerCreate) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, -1)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New("Missing network name")
	}

	if args[1] == "" {
		return errors.New("Missing peer name")
	}

	if args[2] == "" {
		return errors.New("Missing target network")
	}

	targetParts := strings.SplitN(args[2], "/", 2)

	var targetProject, targetNetwork string
	if len(targetParts) == 2 {
		targetProject = targetParts[0]
		targetNetwork = targetParts[1]
	} else {
		targetNetwork = targetParts[0]
	}

	// If stdin isn't a terminal, read yaml from it.
	var peerPut api.NetworkPeerPut
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		err = yaml.UnmarshalStrict(contents, &peerPut)
		if err != nil {
			return err
		}
	}

	if peerPut.Config == nil {
		peerPut.Config = map[string]string{}
	}

	// Get config filters from arguments.
	for i := 3; i < len(args); i++ {
		entry := strings.SplitN(args[i], "=", 2)
		if len(entry) < 2 {
			return fmt.Errorf("Bad key/value pair: %s", args[i])
		}

		peerPut.Config[entry[0]] = entry[1]
	}

	// Create the network peer.
	peer := api.NetworkPeersPost{
		Name:           args[1],
		TargetProject:  targetProject,
		TargetNetwork:  targetNetwork,
		NetworkPeerPut: peerPut,
	}

	client := resource.server

	err = client.CreateNetworkPeer(resource.name, peer)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		createdPeer, _, err := client.GetNetworkPeer(resource.name, peer.Name)
		if err != nil {
			return fmt.Errorf("Failed getting peer's status: %w", err)
		}

		switch createdPeer.Status {
		case api.NetworkStatusCreated:
			fmt.Printf("Network peer %s created\n", peer.Name)
		case api.NetworkStatusPending:
			fmt.Printf("Network peer %s pending (please complete mutual peering on peer network)\n", peer.Name)
		default:
			fmt.Printf("Network peer %s is in unexpected state %q\n", peer.Name, createdPeer.Status)
		}
	}

	return nil
}

// Get.
type cmdNetworkPeerGet struct {
	global      *cmdGlobal
	networkPeer *cmdNetworkPeer

	flagIsProperty bool
}

func (c *cmdNetworkPeerGet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get", "[<remote>:]<network> <peer_name> <key>")
	cmd.Short = "Get value for network peer configuration key"
	cmd.Long = cli.FormatSection("Description", cmd.Short)
	cmd.RunE = c.run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, "Get the key as a network peer property")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkPeers(args[0])
		}

		if len(args) == 2 {
			return c.global.cmpNetworkPeerConfigs(args[0], args[1])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkPeerGet) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
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
		return errors.New("Missing network name")
	}

	if args[1] == "" {
		return errors.New("Missing peer name")
	}

	// Get the current config.
	peer, _, err := client.GetNetworkPeer(resource.name, args[1])
	if err != nil {
		return err
	}

	if c.flagIsProperty {
		w := peer.Writable()
		res, err := getFieldByJSONTag(&w, args[2])
		if err != nil {
			return fmt.Errorf("The property %q does not exist on the network peer %q: %v", args[2], resource.name, err)
		}

		fmt.Printf("%v\n", res)
	} else {
		for k, v := range peer.Config {
			if k == args[2] {
				fmt.Printf("%s\n", v)
			}
		}
	}

	return nil
}

// Set.
type cmdNetworkPeerSet struct {
	global      *cmdGlobal
	networkPeer *cmdNetworkPeer

	flagIsProperty bool
}

func (c *cmdNetworkPeerSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set", "[<remote>:]<network> <peer_name> <key>=<value>...")
	cmd.Short = "Set network peer keys"
	cmd.Long = cli.FormatSection("Description", cmd.Short+`

For backward compatibility, a single configuration key may still be set with:
    lxc network set [<remote>:]<network> <peer_name> <key> <value>`)
	cmd.RunE = c.run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, "Set the key as a network peer property")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkPeers(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkPeerSet) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, -1)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New("Missing network name")
	}

	if args[1] == "" {
		return errors.New("Missing peer name")
	}

	client := resource.server

	// Get the current config.
	peer, etag, err := client.GetNetworkPeer(resource.name, args[1])
	if err != nil {
		return err
	}

	if peer.Config == nil {
		peer.Config = map[string]string{}
	}

	// Set the keys.
	keys, err := getConfig(args[2:]...)
	if err != nil {
		return err
	}

	writable := peer.Writable()
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

	return client.UpdateNetworkPeer(resource.name, peer.Name, writable, etag)
}

// Unset.
type cmdNetworkPeerUnset struct {
	global         *cmdGlobal
	networkPeer    *cmdNetworkPeer
	networkPeerSet *cmdNetworkPeerSet

	flagIsProperty bool
}

func (c *cmdNetworkPeerUnset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset", "[<remote>:]<network> <peer_name> <key>")
	cmd.Short = "Unset network peer configuration key"
	cmd.Long = cli.FormatSection("Description", cmd.Short)
	cmd.RunE = c.run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, "Unset the key as a network peer property")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkPeers(args[0])
		}

		if len(args) == 2 {
			return c.global.cmpNetworkPeerConfigs(args[0], args[1])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkPeerUnset) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, 3)
	if exit {
		return err
	}

	c.networkPeerSet.flagIsProperty = c.flagIsProperty

	args = append(args, "")
	return c.networkPeerSet.run(cmd, args)
}

// Edit.
type cmdNetworkPeerEdit struct {
	global      *cmdGlobal
	networkPeer *cmdNetworkPeer
}

func (c *cmdNetworkPeerEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", "[<remote>:]<network> <peer_name>")
	cmd.Short = "Edit network peer configurations as YAML"
	cmd.Long = cli.FormatSection("Description", cmd.Short)
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkPeers(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkPeerEdit) helpTemplate() string {
	return `### This is a YAML representation of the network peer.
### Any line starting with a '# will be ignored.
###
### An example would look like:
### description: A peering to mynet
### config: {}
### name: mypeer
### target_project: default
### target_network: mynet
### status: Pending
###
### Note that the name, target_project, target_network and status fields cannot be changed.`
}

func (c *cmdNetworkPeerEdit) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New("Missing network name")
	}

	if args[1] == "" {
		return errors.New("Missing peer name")
	}

	client := resource.server

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		// Allow output of `lxc network peer show` command to be passed in here, but only take the contents
		// of the NetworkPeerPut fields when updating. The other fields are silently discarded.
		newData := api.NetworkPeer{}
		err = yaml.UnmarshalStrict(contents, &newData)
		if err != nil {
			return err
		}

		return client.UpdateNetworkPeer(resource.name, args[1], newData.Writable(), "")
	}

	// Get the current config.
	peer, etag, err := client.GetNetworkPeer(resource.name, args[1])
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&peer)
	if err != nil {
		return err
	}

	// Spawn the editor.
	content, err := shared.TextEditor("", []byte(c.helpTemplate()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor.
		newData := api.NetworkPeer{} // We show the full info, but only send the writable fields.
		err = yaml.UnmarshalStrict(content, &newData)
		if err == nil {
			err = client.UpdateNetworkPeer(resource.name, args[1], newData.Writable(), etag)
		}

		// Respawn the editor.
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

// Delete.
type cmdNetworkPeerDelete struct {
	global      *cmdGlobal
	networkPeer *cmdNetworkPeer
}

func (c *cmdNetworkPeerDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", "[<remote>:]<network> <peer_name>")
	cmd.Aliases = []string{"rm"}
	cmd.Short = "Delete network peering"
	cmd.Long = cli.FormatSection("Description", cmd.Short)
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkPeers(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkPeerDelete) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New("Missing network name")
	}

	if args[1] == "" {
		return errors.New("Missing peer name")
	}

	client := resource.server

	// Delete the network peer.
	err = client.DeleteNetworkPeer(resource.name, args[1])
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf("Network peer %s deleted\n", args[1])
	}

	return nil
}
