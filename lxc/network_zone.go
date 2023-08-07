package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
	"github.com/canonical/lxd/shared/termios"
)

type cmdNetworkZone struct {
	global *cmdGlobal
}

// Command() initializes a new Cobra command for managing network zones, adding related subcommands for specific operations.
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

	// Record.
	networkZoneRecordCmd := cmdNetworkZoneRecord{global: c.global, networkZone: c}
	cmd.AddCommand(networkZoneRecordCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// List.
type cmdNetworkZoneList struct {
	global      *cmdGlobal
	networkZone *cmdNetworkZone

	flagFormat string
}

// Command() initializes a new Cobra command to list the available network zones with customizable output format options.
func (c *cmdNetworkZoneList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List available network zoneS")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("List available network zone"))

	cmd.RunE = c.Run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")

	return cmd
}

// Run() executes the Cobra command to list network zones, fetching the zones from the server and rendering them in the requested format.
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

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		i18n.G("NAME"),
		i18n.G("DESCRIPTION"),
		i18n.G("USED BY"),
	}

	return cli.RenderTable(c.flagFormat, header, data, zones)
}

// Show.
type cmdNetworkZoneShow struct {
	global      *cmdGlobal
	networkZone *cmdNetworkZone
}

// Command() initializes a new Cobra command to display the configurations of a specified network zone.
func (c *cmdNetworkZoneShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<Zone>"))
	cmd.Short = i18n.G("Show network zone configurations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Show network zone configurations"))
	cmd.RunE = c.Run

	return cmd
}

// Run() executes the Cobra command to display the configurations of a specified network zone, fetching details from the server and printing them in YAML format.
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

	flagIsProperty bool
}

// Command() initializes a new Cobra command to retrieve the values of specified configuration keys for a given network zone.
func (c *cmdNetworkZoneGet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get", i18n.G("[<remote>:]<Zone> <key>"))
	cmd.Short = i18n.G("Get values for network zone configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Get values for network zone configuration keys"))
	cmd.RunE = c.Run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a network zone property"))
	return cmd
}

// Run() executes the Cobra command to retrieve the value of a specified configuration key for a given network zone from the server and prints it.
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

	if c.flagIsProperty {
		w := resp.Writable()
		res, err := getFieldByJsonTag(&w, args[1])
		if err != nil {
			return fmt.Errorf(i18n.G("The property %q does not exist on the network zone %q: %v"), args[1], resource.name, err)
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

// Create.
type cmdNetworkZoneCreate struct {
	global      *cmdGlobal
	networkZone *cmdNetworkZone
}

// Command() initializes a new Cobra command to create a new network zone with specified configurations.
func (c *cmdNetworkZoneCreate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", i18n.G("[<remote>:]<Zone> [key=value...]"))
	cmd.Short = i18n.G("Create new network zones")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Create new network zones"))

	cmd.RunE = c.Run

	return cmd
}

// Run() executes the Cobra command to create a new network zone, taking in configurations from arguments or stdin, and sending a creation request to the server.
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
		contents, err := io.ReadAll(os.Stdin)
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

	flagIsProperty bool
}

// Command() initializes a new Cobra command to set or update configuration keys for a specified network zone.
func (c *cmdNetworkZoneSet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set", i18n.G("[<remote>:]<Zone> <key>=<value>..."))
	cmd.Short = i18n.G("Set network zone configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Set network zone configuration keys

For backward compatibility, a single configuration key may still be set with:
    lxc network set [<remote>:]<Zone> <key> <value>`))

	cmd.RunE = c.Run
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a network zone property"))

	return cmd
}

// Run() executes the Cobra command to set or update configuration keys for a specified network zone,
// fetching the zone details and sending the updated configuration to the server.
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

	writable := netZone.Writable()
	if c.flagIsProperty {
		if cmd.Name() == "unset" {
			for k := range keys {
				err := unsetFieldByJsonTag(&writable, k)
				if err != nil {
					return fmt.Errorf(i18n.G("Error unsetting property: %v"), err)
				}
			}
		} else {
			err := unpackKVToWritable(&writable, keys)
			if err != nil {
				return fmt.Errorf(i18n.G("Error setting properties: %v"), err)
			}
		}
	} else {
		for k, v := range keys {
			writable.Config[k] = v
		}
	}

	return resource.server.UpdateNetworkZone(resource.name, writable, etag)
}

// Unset.
type cmdNetworkZoneUnset struct {
	global         *cmdGlobal
	networkZone    *cmdNetworkZone
	networkZoneSet *cmdNetworkZoneSet

	flagIsProperty bool
}

// Command() initializes a new Cobra command to unset or remove specified configuration keys from a network zone.
func (c *cmdNetworkZoneUnset) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset", i18n.G("[<remote>:]<Zone> <key>"))
	cmd.Short = i18n.G("Unset network zone configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Unset network zone configuration keys"))
	cmd.RunE = c.Run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a network zone property"))

	return cmd
}

// Run() removes the specified configuration key from a network zone by appending an empty string to the key and invoking networkZoneSet's Run() function.
func (c *cmdNetworkZoneUnset) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	c.networkZoneSet.flagIsProperty = c.flagIsProperty

	args = append(args, "")
	return c.networkZoneSet.Run(cmd, args)
}

// Edit.
type cmdNetworkZoneEdit struct {
	global      *cmdGlobal
	networkZone *cmdNetworkZone
}

// Command() initializes a new cobra command for editing network zone configurations using YAML format.
func (c *cmdNetworkZoneEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<Zone>"))
	cmd.Short = i18n.G("Edit network zone configurations as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Edit network zone configurations as YAML"))

	cmd.RunE = c.Run

	return cmd
}

// helpTemplate() provides a help text in YAML format for configuring a network zone.
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

// Run() lets user edit the configuration of a specified network zone using a text editor, validating and applying changes.
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
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		// Allow output of `lxc network zone show` command to be passed in here, but only take the contents
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

// Command() sets up the command to delete specified network zones from the remote.
func (c *cmdNetworkZoneDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<Zone>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete network zones")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Delete network zones"))
	cmd.RunE = c.Run

	return cmd
}

// Run() executes the deletion of a specific network zone, printing a success message unless quiet mode is active.
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

// Add/Remove Rule.
type cmdNetworkZoneRecord struct {
	global      *cmdGlobal
	networkZone *cmdNetworkZone
}

// Command() sets up the main network zone record command and its subcommands,
// which include functions to list, show, get, create, set, unset, edit, and delete network zone records.
func (c *cmdNetworkZoneRecord) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("record")
	cmd.Short = i18n.G("Manage network zone records")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Manage network zone records"))

	// List.
	networkZoneRecordListCmd := cmdNetworkZoneRecordList{global: c.global, networkZoneRecord: c}
	cmd.AddCommand(networkZoneRecordListCmd.Command())

	// Show.
	networkZoneRecordShowCmd := cmdNetworkZoneRecordShow{global: c.global, networkZoneRecord: c}
	cmd.AddCommand(networkZoneRecordShowCmd.Command())

	// Get.
	networkZoneRecordGetCmd := cmdNetworkZoneRecordGet{global: c.global, networkZoneRecord: c}
	cmd.AddCommand(networkZoneRecordGetCmd.Command())

	// Create.
	networkZoneRecordCreateCmd := cmdNetworkZoneRecordCreate{global: c.global, networkZoneRecord: c}
	cmd.AddCommand(networkZoneRecordCreateCmd.Command())

	// Set.
	networkZoneRecordSetCmd := cmdNetworkZoneRecordSet{global: c.global, networkZoneRecord: c}
	cmd.AddCommand(networkZoneRecordSetCmd.Command())

	// Unset.
	networkZoneRecordUnsetCmd := cmdNetworkZoneRecordUnset{global: c.global, networkZoneRecord: c, networkZoneRecordSet: &networkZoneRecordSetCmd}
	cmd.AddCommand(networkZoneRecordUnsetCmd.Command())

	// Edit.
	networkZoneRecordEditCmd := cmdNetworkZoneRecordEdit{global: c.global, networkZoneRecord: c}
	cmd.AddCommand(networkZoneRecordEditCmd.Command())

	// Delete.
	networkZoneRecordDeleteCmd := cmdNetworkZoneRecordDelete{global: c.global, networkZoneRecord: c}
	cmd.AddCommand(networkZoneRecordDeleteCmd.Command())

	// Entry.
	networkZoneRecordEntryCmd := cmdNetworkZoneRecordEntry{global: c.global, networkZoneRecord: c}
	cmd.AddCommand(networkZoneRecordEntryCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// List.
type cmdNetworkZoneRecordList struct {
	global            *cmdGlobal
	networkZoneRecord *cmdNetworkZoneRecord

	flagFormat string
}

// Command() sets up the 'list' subcommand to display network zone records in various formats such as table, csv, json, yaml, or compact.
func (c *cmdNetworkZoneRecordList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]<zone>"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List available network zone records")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("List available network zone records"))

	cmd.RunE = c.Run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")

	return cmd
}

// Run() retrieves and lists the network zone records for a specified network zone in a user-specified format.
func (c *cmdNetworkZoneRecordList) Run(cmd *cobra.Command, args []string) error {
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

	// List the records.
	records, err := resource.server.GetNetworkZoneRecords(resource.name)
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, record := range records {
		entries := []string{}

		for _, entry := range record.Entries {
			entries = append(entries, fmt.Sprintf("%s %s", entry.Type, entry.Value))
		}

		details := []string{
			record.Name,
			record.Description,
			strings.Join(entries, "\n"),
		}

		data = append(data, details)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		i18n.G("NAME"),
		i18n.G("DESCRIPTION"),
		i18n.G("ENTRIES"),
	}

	return cli.RenderTable(c.flagFormat, header, data, records)
}

// Show.
type cmdNetworkZoneRecordShow struct {
	global            *cmdGlobal
	networkZoneRecord *cmdNetworkZoneRecord
}

// Command() returns a command that shows the configuration of a specific network zone record.
func (c *cmdNetworkZoneRecordShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<zone> <record>"))
	cmd.Short = i18n.G("Show network zone record configuration")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Show network zone record configurations"))
	cmd.RunE = c.Run

	return cmd
}

// Run() retrieves and displays the configuration of a specified network zone record in YAML format.
func (c *cmdNetworkZoneRecordShow) Run(cmd *cobra.Command, args []string) error {
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

	// Show the network zone config.
	netRecord, _, err := resource.server.GetNetworkZoneRecord(resource.name, args[1])
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&netRecord)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Get.
type cmdNetworkZoneRecordGet struct {
	global            *cmdGlobal
	networkZoneRecord *cmdNetworkZoneRecord

	flagIsProperty bool
}

// Command() sets up the 'get' command to fetch configuration key values from a specified network zone record.
func (c *cmdNetworkZoneRecordGet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get", i18n.G("[<remote>:]<zone> <record> <key>"))
	cmd.Short = i18n.G("Get values for network zone record configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Get values for network zone record configuration keys"))
	cmd.RunE = c.Run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a network zone record property"))
	return cmd
}

// Run() fetches and prints a specific configuration key's value from a given network zone record.
func (c *cmdNetworkZoneRecordGet) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, 3)
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
		return fmt.Errorf(i18n.G("Missing network zone record name"))
	}

	resp, _, err := resource.server.GetNetworkZoneRecord(resource.name, args[1])
	if err != nil {
		return err
	}

	if c.flagIsProperty {
		w := resp.Writable()
		res, err := getFieldByJsonTag(&w, args[2])
		if err != nil {
			return fmt.Errorf(i18n.G("The property %q does not exist on the network zone record %q: %v"), args[2], resource.name, err)
		}

		fmt.Printf("%v\n", res)
	} else {
		for k, v := range resp.Config {
			if k == args[2] {
				fmt.Printf("%s\n", v)
			}
		}
	}

	return nil
}

// Create.
type cmdNetworkZoneRecordCreate struct {
	global            *cmdGlobal
	networkZoneRecord *cmdNetworkZoneRecord
}

// Command() constructs a new command for creating a network zone record with optional key-value configurations.
func (c *cmdNetworkZoneRecordCreate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", i18n.G("[<remote>:]<zone> <record> [key=value...]"))
	cmd.Short = i18n.G("Create new network zone record")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Create new network zone record"))

	cmd.RunE = c.Run

	return cmd
}

// Run() executes the command to create a network zone record, handling input from terminal or stdin, and optional key-value pairs.
func (c *cmdNetworkZoneRecordCreate) Run(cmd *cobra.Command, args []string) error {
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

	// If stdin isn't a terminal, read yaml from it.
	var recordPut api.NetworkZoneRecordPut
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		err = yaml.UnmarshalStrict(contents, &recordPut)
		if err != nil {
			return err
		}
	}

	// Create the network zone.
	record := api.NetworkZoneRecordsPost{
		Name:                 args[1],
		NetworkZoneRecordPut: recordPut,
	}

	if record.Config == nil {
		record.Config = map[string]string{}
	}

	for i := 2; i < len(args); i++ {
		entry := strings.SplitN(args[i], "=", 2)
		if len(entry) < 2 {
			return fmt.Errorf(i18n.G("Bad key/value pair: %s"), args[i])
		}

		record.Config[entry[0]] = entry[1]
	}

	err = resource.server.CreateNetworkZoneRecord(resource.name, record)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network zone record %s created")+"\n", args[1])
	}

	return nil
}

// Set.
type cmdNetworkZoneRecordSet struct {
	global            *cmdGlobal
	networkZoneRecord *cmdNetworkZoneRecord

	flagIsProperty bool
}

// Command() returns a cobra.Command object configured to set configuration keys for a network zone record.
func (c *cmdNetworkZoneRecordSet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set", i18n.G("[<remote>:]<zone> <record> <key>=<value>..."))
	cmd.Short = i18n.G("Set network zone record configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Set network zone record configuration keys`))

	cmd.RunE = c.Run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a network zone record property"))
	return cmd
}

// Run() method executes the 'set' command, updating the configuration of a specified network zone record.
func (c *cmdNetworkZoneRecordSet) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing network zone name"))
	}

	// Get the network zone.
	netRecord, etag, err := resource.server.GetNetworkZoneRecord(resource.name, args[1])
	if err != nil {
		return err
	}

	// Set the keys.
	keys, err := getConfig(args[2:]...)
	if err != nil {
		return err
	}

	writable := netRecord.Writable()
	if c.flagIsProperty {
		if cmd.Name() == "unset" {
			for k := range keys {
				err := unsetFieldByJsonTag(&writable, k)
				if err != nil {
					return fmt.Errorf(i18n.G("Error unsetting property: %v"), err)
				}
			}
		} else {
			err := unpackKVToWritable(&writable, keys)
			if err != nil {
				return fmt.Errorf(i18n.G("Error setting properties: %v"), err)
			}
		}
	} else {
		for k, v := range keys {
			writable.Config[k] = v
		}
	}

	return resource.server.UpdateNetworkZoneRecord(resource.name, args[1], writable, etag)
}

// Unset.
type cmdNetworkZoneRecordUnset struct {
	global               *cmdGlobal
	networkZoneRecord    *cmdNetworkZoneRecord
	networkZoneRecordSet *cmdNetworkZoneRecordSet

	flagIsProperty bool
}

// Command() method sets up the 'unset' command which removes specified keys from the network zone record configuration.
func (c *cmdNetworkZoneRecordUnset) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset", i18n.G("[<remote>:]<zone> <record> <key>"))
	cmd.Short = i18n.G("Unset network zone record configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Unset network zone record configuration keys"))
	cmd.RunE = c.Run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a network zone record property"))
	return cmd
}

// Run() method for the 'unset' command which delegates to 'set' command to remove a specified configuration key from a network zone record.
func (c *cmdNetworkZoneRecordUnset) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, 3)
	if exit {
		return err
	}

	c.networkZoneRecordSet.flagIsProperty = c.flagIsProperty

	args = append(args, "")
	return c.networkZoneRecordSet.Run(cmd, args)
}

// Edit.
type cmdNetworkZoneRecordEdit struct {
	global            *cmdGlobal
	networkZoneRecord *cmdNetworkZoneRecord
}

// Generates the 'edit' command to facilitate editing of network zone record configurations using YAML format.
func (c *cmdNetworkZoneRecordEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<zone> <record>"))
	cmd.Short = i18n.G("Edit network zone record configurations as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Edit network zone record configurations as YAML"))

	cmd.RunE = c.Run

	return cmd
}

// Returns a template string to assist the user in creating or editing a network zone record in YAML format.
func (c *cmdNetworkZoneRecordEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the network zone record.
### Any line starting with a '# will be ignored.
###
### A network zone consists of a set of rules and configuration items.
###
### An example would look like:
### name: foo
### description: SPF record
### config:
###  user.foo: bah
`)
}

// Edits an existing network zone record configuration using an interactive text editor, with changes applied upon editor exit.
func (c *cmdNetworkZoneRecordEdit) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing network zone record name"))
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		// Allow output of `lxc network zone show` command to be passed in here, but only take the contents
		// of the NetworkZonePut fields when updating the Zone. The other fields are silently discarded.
		newdata := api.NetworkZoneRecord{}
		err = yaml.UnmarshalStrict(contents, &newdata)
		if err != nil {
			return err
		}

		return resource.server.UpdateNetworkZoneRecord(resource.name, args[1], newdata.NetworkZoneRecordPut, "")
	}

	// Get the current config.
	netRecord, etag, err := resource.server.GetNetworkZoneRecord(resource.name, args[1])
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(netRecord.Writable())
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
		newdata := api.NetworkZoneRecord{} // We show the full Zone info, but only send the writable fields.
		err = yaml.UnmarshalStrict(content, &newdata)
		if err == nil {
			err = resource.server.UpdateNetworkZoneRecord(resource.name, args[1], newdata.Writable(), etag)
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
type cmdNetworkZoneRecordDelete struct {
	global            *cmdGlobal
	networkZoneRecord *cmdNetworkZoneRecord
}

// Provides a command to delete a specified network zone record from a given zone.
func (c *cmdNetworkZoneRecordDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<zone> <record>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete network zone record")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Delete network zone record"))
	cmd.RunE = c.Run

	return cmd
}

// Executes the removal of a specific network zone record from a specified zone.
func (c *cmdNetworkZoneRecordDelete) Run(cmd *cobra.Command, args []string) error {
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

	// Delete the network zone.
	err = resource.server.DeleteNetworkZoneRecord(resource.name, args[1])
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network zone record %s deleted")+"\n", args[1])
	}

	return nil
}

// Add/Remove Rule.
type cmdNetworkZoneRecordEntry struct {
	global            *cmdGlobal
	networkZoneRecord *cmdNetworkZoneRecord

	flagTTL uint64
}

// Provides a command to manage (add or remove) entries in a network zone record.
func (c *cmdNetworkZoneRecordEntry) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("entry")
	cmd.Short = i18n.G("Manage network zone record entries")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Manage network zone record entries"))

	// Rule Add.
	cmd.AddCommand(c.CommandAdd())

	// Rule Remove.
	cmd.AddCommand(c.CommandRemove())

	return cmd
}

// Provides a command to add an entry to a network zone record with optional TTL.
func (c *cmdNetworkZoneRecordEntry) CommandAdd() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("add", i18n.G("[<remote>:]<zone> <record> <type> <value>"))
	cmd.Short = i18n.G("Add a network zone record entry")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Add entries to a network zone record"))
	cmd.RunE = c.RunAdd
	cmd.Flags().Uint64Var(&c.flagTTL, "ttl", 0, i18n.G("Entry TTL")+"``")

	return cmd
}

// Implements the action of adding a new entry to a network zone record with the provided values.
func (c *cmdNetworkZoneRecordEntry) RunAdd(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 4, 4)
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

	// Get the network record.
	netRecord, etag, err := resource.server.GetNetworkZoneRecord(resource.name, args[1])
	if err != nil {
		return err
	}

	// Add the entry.
	entry := api.NetworkZoneRecordEntry{
		Type:  args[2],
		TTL:   c.flagTTL,
		Value: args[3],
	}

	netRecord.Entries = append(netRecord.Entries, entry)
	return resource.server.UpdateNetworkZoneRecord(resource.name, args[1], netRecord.Writable(), etag)
}

// Provides a command to remove an entry from a specified network zone record by given type and value.
func (c *cmdNetworkZoneRecordEntry) CommandRemove() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("remove", i18n.G("[<remote>:]<zone> <record> <type> <value>"))
	cmd.Short = i18n.G("Remove a network zone record entry")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Remove entries from a network zone record"))
	cmd.RunE = c.RunRemove

	return cmd
}

// Implements the operation to remove a specific entry from a network zone record, identified by type and value.
func (c *cmdNetworkZoneRecordEntry) RunRemove(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 4, 4)
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

	// Get the network zone record.
	netRecord, etag, err := resource.server.GetNetworkZoneRecord(resource.name, args[1])
	if err != nil {
		return err
	}

	found := false
	for i, entry := range netRecord.Entries {
		if entry.Type != args[2] || entry.Value != args[3] {
			continue
		}

		found = true
		netRecord.Entries = append(netRecord.Entries[:i], netRecord.Entries[i+1:]...)
	}

	if !found {
		return fmt.Errorf(i18n.G("Couldn't find a matching entry"))
	}

	return resource.server.UpdateNetworkZoneRecord(resource.name, args[1], netRecord.Writable(), etag)
}
