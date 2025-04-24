package main

import (
	"errors"
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

func (c *cmdNetworkZone) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("zone")
	cmd.Short = i18n.G("Manage network zones")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Manage network zones"))

	// List.
	networkZoneListCmd := cmdNetworkZoneList{global: c.global, networkZone: c}
	cmd.AddCommand(networkZoneListCmd.command())

	// Show.
	networkZoneShowCmd := cmdNetworkZoneShow{global: c.global, networkZone: c}
	cmd.AddCommand(networkZoneShowCmd.command())

	// Get.
	networkZoneGetCmd := cmdNetworkZoneGet{global: c.global, networkZone: c}
	cmd.AddCommand(networkZoneGetCmd.command())

	// Create.
	networkZoneCreateCmd := cmdNetworkZoneCreate{global: c.global, networkZone: c}
	cmd.AddCommand(networkZoneCreateCmd.command())

	// Set.
	networkZoneSetCmd := cmdNetworkZoneSet{global: c.global, networkZone: c}
	cmd.AddCommand(networkZoneSetCmd.command())

	// Unset.
	networkZoneUnsetCmd := cmdNetworkZoneUnset{global: c.global, networkZone: c, networkZoneSet: &networkZoneSetCmd}
	cmd.AddCommand(networkZoneUnsetCmd.command())

	// Edit.
	networkZoneEditCmd := cmdNetworkZoneEdit{global: c.global, networkZone: c}
	cmd.AddCommand(networkZoneEditCmd.command())

	// Delete.
	networkZoneDeleteCmd := cmdNetworkZoneDelete{global: c.global, networkZone: c}
	cmd.AddCommand(networkZoneDeleteCmd.command())

	// Record.
	networkZoneRecordCmd := cmdNetworkZoneRecord{global: c.global, networkZone: c}
	cmd.AddCommand(networkZoneRecordCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// List.
type cmdNetworkZoneList struct {
	global      *cmdGlobal
	networkZone *cmdNetworkZone

	flagFormat      string
	flagAllProjects bool
}

func (c *cmdNetworkZoneList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List available network zoneS")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("List available network zone"))

	cmd.RunE = c.run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")
	cmd.Flags().BoolVar(&c.flagAllProjects, "all-projects", false, i18n.G("Display network zones from all projects"))

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(toComplete, ":", true, instanceServerRemoteCompletionFilters(*c.global.conf)...)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkZoneList) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Filtering isn't supported yet"))
	}

	var zones []api.NetworkZone
	if c.flagAllProjects {
		zones, err = resource.server.GetNetworkZonesAllProjects()
		if err != nil {
			return err
		}
	} else {
		zones, err = resource.server.GetNetworkZones()
		if err != nil {
			return err
		}
	}

	data := [][]string{}
	for _, zone := range zones {
		strUsedBy := fmt.Sprint(len(zone.UsedBy))
		details := []string{
			zone.Name,
			zone.Description,
			strUsedBy,
		}

		if c.flagAllProjects {
			details = append([]string{zone.Project}, details...)
		}

		data = append(data, details)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		i18n.G("NAME"),
		i18n.G("DESCRIPTION"),
		i18n.G("USED BY"),
	}

	if c.flagAllProjects {
		header = append([]string{i18n.G("PROJECT")}, header...)
	}

	return cli.RenderTable(c.flagFormat, header, data, zones)
}

// Show.
type cmdNetworkZoneShow struct {
	global      *cmdGlobal
	networkZone *cmdNetworkZone
}

func (c *cmdNetworkZoneShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<Zone>"))
	cmd.Short = i18n.G("Show network zone configurations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Show network zone configurations"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network_zone", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkZoneShow) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing network zone name"))
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

func (c *cmdNetworkZoneGet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get", i18n.G("[<remote>:]<Zone> <key>"))
	cmd.Short = i18n.G("Get values for network zone configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Get values for network zone configuration keys"))
	cmd.RunE = c.run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a network zone property"))

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network_zone", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkZoneConfigs(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkZoneGet) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing network zone name"))
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

func (c *cmdNetworkZoneCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", i18n.G("[<remote>:]<Zone> [key=value...]"))
	cmd.Short = i18n.G("Create new network zones")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Create new network zones"))
	cmd.Example = cli.FormatSection("", i18n.G(`lxc network zone create z1

lxc network zone create z1 < config.yaml
    Create network zone z1 with configuration from config.yaml`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network_zone", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkZoneCreate) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing network zone name"))
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

func (c *cmdNetworkZoneSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set", i18n.G("[<remote>:]<Zone> <key>=<value>..."))
	cmd.Short = i18n.G("Set network zone configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Set network zone configuration keys

For backward compatibility, a single configuration key may still be set with:
    lxc network set [<remote>:]<Zone> <key> <value>`))

	cmd.RunE = c.run
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a network zone property"))

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network_zone", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkZoneSet) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing network zone name"))
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

func (c *cmdNetworkZoneUnset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset", i18n.G("[<remote>:]<Zone> <key>"))
	cmd.Short = i18n.G("Unset network zone configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Unset network zone configuration keys"))
	cmd.RunE = c.run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a network zone property"))

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network_zone", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkZoneConfigs(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkZoneUnset) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	c.networkZoneSet.flagIsProperty = c.flagIsProperty

	args = append(args, "")
	return c.networkZoneSet.run(cmd, args)
}

// Edit.
type cmdNetworkZoneEdit struct {
	global      *cmdGlobal
	networkZone *cmdNetworkZone
}

func (c *cmdNetworkZoneEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<Zone>"))
	cmd.Short = i18n.G("Edit network zone configurations as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Edit network zone configurations as YAML"))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network_zone", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

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

func (c *cmdNetworkZoneEdit) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing network zone name"))
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

		return resource.server.UpdateNetworkZone(resource.name, newdata.Writable(), "")
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

func (c *cmdNetworkZoneDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<Zone>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete network zones")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Delete network zones"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network_zone", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkZoneDelete) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing network zone name"))
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

func (c *cmdNetworkZoneRecord) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("record")
	cmd.Short = i18n.G("Manage network zone records")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Manage network zone records"))

	// List.
	networkZoneRecordListCmd := cmdNetworkZoneRecordList{global: c.global, networkZoneRecord: c}
	cmd.AddCommand(networkZoneRecordListCmd.command())

	// Show.
	networkZoneRecordShowCmd := cmdNetworkZoneRecordShow{global: c.global, networkZoneRecord: c}
	cmd.AddCommand(networkZoneRecordShowCmd.command())

	// Get.
	networkZoneRecordGetCmd := cmdNetworkZoneRecordGet{global: c.global, networkZoneRecord: c}
	cmd.AddCommand(networkZoneRecordGetCmd.command())

	// Create.
	networkZoneRecordCreateCmd := cmdNetworkZoneRecordCreate{global: c.global, networkZoneRecord: c}
	cmd.AddCommand(networkZoneRecordCreateCmd.command())

	// Set.
	networkZoneRecordSetCmd := cmdNetworkZoneRecordSet{global: c.global, networkZoneRecord: c}
	cmd.AddCommand(networkZoneRecordSetCmd.command())

	// Unset.
	networkZoneRecordUnsetCmd := cmdNetworkZoneRecordUnset{global: c.global, networkZoneRecord: c, networkZoneRecordSet: &networkZoneRecordSetCmd}
	cmd.AddCommand(networkZoneRecordUnsetCmd.command())

	// Edit.
	networkZoneRecordEditCmd := cmdNetworkZoneRecordEdit{global: c.global, networkZoneRecord: c}
	cmd.AddCommand(networkZoneRecordEditCmd.command())

	// Delete.
	networkZoneRecordDeleteCmd := cmdNetworkZoneRecordDelete{global: c.global, networkZoneRecord: c}
	cmd.AddCommand(networkZoneRecordDeleteCmd.command())

	// Entry.
	networkZoneRecordEntryCmd := cmdNetworkZoneRecordEntry{global: c.global, networkZoneRecord: c}
	cmd.AddCommand(networkZoneRecordEntryCmd.command())

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

func (c *cmdNetworkZoneRecordList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]<zone>"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List available network zone records")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("List available network zone records"))

	cmd.RunE = c.run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network_zone", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkZoneRecordList) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing network zone name"))
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
			entries = append(entries, entry.Type+" "+entry.Value)
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

func (c *cmdNetworkZoneRecordShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<zone> <record>"))
	cmd.Short = i18n.G("Show network zone record configuration")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Show network zone record configurations"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network_zone", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkZoneRecords(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkZoneRecordShow) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing network zone name"))
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

func (c *cmdNetworkZoneRecordGet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get", i18n.G("[<remote>:]<zone> <record> <key>"))
	cmd.Short = i18n.G("Get values for network zone record configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Get values for network zone record configuration keys"))
	cmd.RunE = c.run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a network zone record property"))

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network_zone", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkZoneRecords(args[0])
		}

		if len(args) == 2 {
			return c.global.cmpNetworkZoneRecordConfigs(args[0], args[1])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkZoneRecordGet) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing network zone record name"))
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

func (c *cmdNetworkZoneRecordCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", i18n.G("[<remote>:]<zone> <record> [key=value...]"))
	cmd.Short = i18n.G("Create new network zone record")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Create new network zone record"))
	cmd.Example = cli.FormatSection("", i18n.G(`lxc network zone record create z1 r1

lxc network zone record create z1 r1 < config.yaml
    Create record r1 for zone z1 with configuration from config.yaml`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network_zone", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkZoneRecords(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkZoneRecordCreate) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing network zone name"))
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

func (c *cmdNetworkZoneRecordSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set", i18n.G("[<remote>:]<zone> <record> <key>=<value>..."))
	cmd.Short = i18n.G("Set network zone record configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Set network zone record configuration keys`))

	cmd.RunE = c.run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a network zone record property"))

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network_zone", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkZoneRecords(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkZoneRecordSet) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing network zone name"))
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

func (c *cmdNetworkZoneRecordUnset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset", i18n.G("[<remote>:]<zone> <record> <key>"))
	cmd.Short = i18n.G("Unset network zone record configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Unset network zone record configuration keys"))
	cmd.RunE = c.run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a network zone record property"))

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network_zone", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkZoneRecords(args[0])
		}

		if len(args) == 2 {
			return c.global.cmpNetworkZoneRecordConfigs(args[0], args[1])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkZoneRecordUnset) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, 3)
	if exit {
		return err
	}

	c.networkZoneRecordSet.flagIsProperty = c.flagIsProperty

	args = append(args, "")
	return c.networkZoneRecordSet.run(cmd, args)
}

// Edit.
type cmdNetworkZoneRecordEdit struct {
	global            *cmdGlobal
	networkZoneRecord *cmdNetworkZoneRecord
}

func (c *cmdNetworkZoneRecordEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<zone> <record>"))
	cmd.Short = i18n.G("Edit network zone record configurations as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Edit network zone record configurations as YAML"))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network_zone", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkZoneRecords(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

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

func (c *cmdNetworkZoneRecordEdit) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing network zone record name"))
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

		return resource.server.UpdateNetworkZoneRecord(resource.name, args[1], newdata.Writable(), "")
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

func (c *cmdNetworkZoneRecordDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<zone> <record>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete network zone record")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Delete network zone record"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network_zone", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkZoneRecords(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkZoneRecordDelete) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing network zone name"))
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

func (c *cmdNetworkZoneRecordEntry) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("entry")
	cmd.Short = i18n.G("Manage network zone record entries")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Manage network zone record entries"))

	// Rule Add.
	cmd.AddCommand(c.commandAdd())

	// Rule Remove.
	cmd.AddCommand(c.commandRemove())

	return cmd
}

func (c *cmdNetworkZoneRecordEntry) commandAdd() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("add", i18n.G("[<remote>:]<zone> <record> <type> <value>"))
	cmd.Short = i18n.G("Add a network zone record entry")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Add entries to a network zone record"))
	cmd.RunE = c.runAdd
	cmd.Flags().Uint64Var(&c.flagTTL, "ttl", 0, i18n.G("Entry TTL")+"``")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network_zone", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkZoneRecords(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkZoneRecordEntry) runAdd(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing network zone name"))
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

func (c *cmdNetworkZoneRecordEntry) commandRemove() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("remove", i18n.G("[<remote>:]<zone> <record> <type> <value>"))
	cmd.Short = i18n.G("Remove a network zone record entry")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Remove entries from a network zone record"))
	cmd.RunE = c.runRemove

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network_zone", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkZoneRecords(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkZoneRecordEntry) runRemove(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing network zone name"))
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
		return errors.New(i18n.G("Couldn't find a matching entry"))
	}

	return resource.server.UpdateNetworkZoneRecord(resource.name, args[1], netRecord.Writable(), etag)
}
