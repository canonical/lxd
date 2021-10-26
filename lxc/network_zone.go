package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"strings"

	"github.com/lxc/lxd/lxc/utils"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/termios"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/shared/api"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
)

type cmdNetworkZone struct {
	global *cmdGlobal
}

func (c *cmdNetworkZone) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("zone")
	cmd.Short = i18n.G("Manage network zones")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Manage network zones"))

	// List.
	networkZoneListCmd := cmdNetworkZoneList{global: c.global, networkZone: c}
	cmd.AddCommand(networkZoneListCmd.Command())

	// Show.
	networkZoneShowCmd := cmdNetworkZoneShow{global: c.global, networkZone: c}
	cmd.AddCommand(networkZoneShowCmd.Command())

	// Get.
	networkZoneGetCmd := cmdNetworkZoneGet{global: c.global, networkZone: c}
	cmd.AddCommand(networkZoneGetCmd.Command())

	// Create.
	networkZoneCreateCmd := cmdNetworkZoneCreate{global: c.global, networkZone: c}
	cmd.AddCommand(networkZoneCreateCmd.Command())

	// Set.
	networkZoneSetCmd := cmdNetworkZoneSet{global: c.global, networkZone: c}
	cmd.AddCommand(networkZoneSetCmd.Command())

	// Unset.
	networkZoneUnsetCmd := cmdNetworkZoneUnset{global: c.global, networkZone: c, networkZoneSet: &networkZoneSetCmd}
	cmd.AddCommand(networkZoneUnsetCmd.Command())

	// Edit.
	networkZoneEditCmd := cmdNetworkZoneEdit{global: c.global, networkZone: c}
	cmd.AddCommand(networkZoneEditCmd.Command())

	// Delete.
	networkZoneDeleteCmd := cmdNetworkZoneDelete{global: c.global, networkZone: c}
	cmd.AddCommand(networkZoneDeleteCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { cmd.Usage() }
	return cmd
}

// List.
type cmdNetworkZoneList struct {
	global      *cmdGlobal
	networkZone *cmdNetworkZone

	flagFormat string
}

func (c *cmdNetworkZoneList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List available network zoneS")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("List available network zone"))

	cmd.RunE = c.Run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml)")+"``")

	return cmd
}

func (c *cmdNetworkZoneList) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 1)
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

	// List the networks.
	if resource.name != "" {
		return fmt.Errorf(i18n.G("Filtering isn't supported yet"))
	}

	zones, err := resource.server.GetNetworkZones()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, zone := range zones {
		strUsedBy := fmt.Sprintf("%d", len(zone.UsedBy))
		details := []string{
			zone.Name,
			zone.Description,
			strUsedBy,
		}

		data = append(data, details)
	}
	sort.Sort(byName(data))

	header := []string{
		i18n.G("NAME"),
		i18n.G("DESCRIPTION"),
		i18n.G("USED BY"),
	}

	return utils.RenderTable(c.flagFormat, header, data, zones)
}

// Show.
type cmdNetworkZoneShow struct {
	global      *cmdGlobal
	networkZone *cmdNetworkZone
}

func (c *cmdNetworkZoneShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<Zone>"))
	cmd.Short = i18n.G("Show network zone configurations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Show network zone configurations"))
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdNetworkZoneShow) Run(cmd *cobra.Command, args []string) error {
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

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing network zone name"))
	}

	// Show the network zone config.
	netZone, _, err := resource.server.GetNetworkZone(resource.name)
	if err != nil {
		return err
	}

	sort.Strings(netZone.UsedBy)

	data, err := yaml.Marshal(&netZone)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Get.
type cmdNetworkZoneGet struct {
	global      *cmdGlobal
	networkZone *cmdNetworkZone
}

func (c *cmdNetworkZoneGet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get", i18n.G("[<remote>:]<Zone> <key>"))
	cmd.Short = i18n.G("Get values for network zone configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Get values for network zone configuration keys"))
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdNetworkZoneGet) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing network zone name"))
	}

	resp, _, err := resource.server.GetNetworkZone(resource.name)
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

// Create.
type cmdNetworkZoneCreate struct {
	global      *cmdGlobal
	networkZone *cmdNetworkZone
}

func (c *cmdNetworkZoneCreate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", i18n.G("[<remote>:]<Zone> [key=value...]"))
	cmd.Short = i18n.G("Create new network zones")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Create new network zones"))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdNetworkZoneCreate) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, -1)
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
		return fmt.Errorf(i18n.G("Missing network zone name"))
	}

	// If stdin isn't a terminal, read yaml from it.
	var zonePut api.NetworkZonePut
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		err = yaml.UnmarshalStrict(contents, &zonePut)
		if err != nil {
			return err
		}
	}

	// Create the network zone.
	zone := api.NetworkZonesPost{
		Name:           resource.name,
		NetworkZonePut: zonePut,
	}

	if zone.Config == nil {
		zone.Config = map[string]string{}
	}

	for i := 1; i < len(args); i++ {
		entry := strings.SplitN(args[i], "=", 2)
		if len(entry) < 2 {
			return fmt.Errorf(i18n.G("Bad key/value pair: %s"), args[i])
		}

		zone.Config[entry[0]] = entry[1]
	}

	err = resource.server.CreateNetworkZone(zone)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network Zone %s created")+"\n", resource.name)
	}

	return nil
}

// Set.
type cmdNetworkZoneSet struct {
	global      *cmdGlobal
	networkZone *cmdNetworkZone
}

func (c *cmdNetworkZoneSet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set", i18n.G("[<remote>:]<Zone> <key>=<value>..."))
	cmd.Short = i18n.G("Set network zone configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Set network zone configuration keys

For backward compatibility, a single configuration key may still be set with:
    lxc network set [<remote>:]<Zone> <key> <value>`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdNetworkZoneSet) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, -1)
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
		return fmt.Errorf(i18n.G("Missing network zone name"))
	}

	// Get the network zone.
	netZone, etag, err := resource.server.GetNetworkZone(resource.name)
	if err != nil {
		return err
	}

	// Set the keys.
	keys, err := getConfig(args[1:]...)
	if err != nil {
		return err
	}

	for k, v := range keys {
		netZone.Config[k] = v
	}

	return resource.server.UpdateNetworkZone(resource.name, netZone.Writable(), etag)
}

// Unset.
type cmdNetworkZoneUnset struct {
	global         *cmdGlobal
	networkZone    *cmdNetworkZone
	networkZoneSet *cmdNetworkZoneSet
}

func (c *cmdNetworkZoneUnset) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset", i18n.G("[<remote>:]<Zone> <key>"))
	cmd.Short = i18n.G("Unset network zone configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Unset network zone configuration keys"))
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdNetworkZoneUnset) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	args = append(args, "")
	return c.networkZoneSet.Run(cmd, args)
}

// Edit.
type cmdNetworkZoneEdit struct {
	global      *cmdGlobal
	networkZone *cmdNetworkZone
}

func (c *cmdNetworkZoneEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<Zone>"))
	cmd.Short = i18n.G("Edit network zone configurations as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Edit network zone configurations as YAML"))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdNetworkZoneEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the network zone.
### Any line starting with a '# will be ignored.
###
### A network zone consists of a set of rules and configuration items.
###
### An example would look like:
### name: example.net
### description: Internal domain
### config:
###  user.foo: bah
`)
}

func (c *cmdNetworkZoneEdit) Run(cmd *cobra.Command, args []string) error {
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

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing network zone name"))
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		// Allow output of `lxc network zone show` command to passed in here, but only take the contents
		// of the NetworkZonePut fields when updating the Zone. The other fields are silently discarded.
		newdata := api.NetworkZone{}
		err = yaml.UnmarshalStrict(contents, &newdata)
		if err != nil {
			return err
		}

		return resource.server.UpdateNetworkZone(resource.name, newdata.NetworkZonePut, "")
	}

	// Get the current config.
	netZone, etag, err := resource.server.GetNetworkZone(resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&netZone)
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
		newdata := api.NetworkZone{} // We show the full Zone info, but only send the writable fields.
		err = yaml.UnmarshalStrict(content, &newdata)
		if err == nil {
			err = resource.server.UpdateNetworkZone(resource.name, newdata.Writable(), etag)
		}

		// Respawn the editor.
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.G("Config parsing error: %s")+"\n", err)
			fmt.Println(i18n.G("Press enter to open the editor again or ctrl+c to abort change"))

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
type cmdNetworkZoneDelete struct {
	global      *cmdGlobal
	networkZone *cmdNetworkZone
}

func (c *cmdNetworkZoneDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<Zone>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete network zones")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Delete network zones"))
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdNetworkZoneDelete) Run(cmd *cobra.Command, args []string) error {
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

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing network zone name"))
	}

	// Delete the network zone.
	err = resource.server.DeleteNetworkZone(resource.name)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network Zone %s deleted")+"\n", resource.name)
	}

	return nil
}
