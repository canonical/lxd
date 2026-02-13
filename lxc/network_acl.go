package main

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v2"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/termios"
)

type cmdNetworkACL struct {
	global *cmdGlobal
}

func (c *cmdNetworkACL) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("acl")
	cmd.Short = "Manage network ACLs"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	// List.
	networkACLListCmd := cmdNetworkACLList{global: c.global, networkACL: c}
	cmd.AddCommand(networkACLListCmd.command())

	// Show.
	networkACLShowCmd := cmdNetworkACLShow{global: c.global, networkACL: c}
	cmd.AddCommand(networkACLShowCmd.command())

	// Show log.
	networkACLShowLogCmd := cmdNetworkACLShowLog{global: c.global, networkACL: c}
	cmd.AddCommand(networkACLShowLogCmd.command())

	// Get.
	networkACLGetCmd := cmdNetworkACLGet{global: c.global, networkACL: c}
	cmd.AddCommand(networkACLGetCmd.command())

	// Create.
	networkACLCreateCmd := cmdNetworkACLCreate{global: c.global, networkACL: c}
	cmd.AddCommand(networkACLCreateCmd.command())

	// Set.
	networkACLSetCmd := cmdNetworkACLSet{global: c.global, networkACL: c}
	cmd.AddCommand(networkACLSetCmd.command())

	// Unset.
	networkACLUnsetCmd := cmdNetworkACLUnset{global: c.global, networkACL: c, networkACLSet: &networkACLSetCmd}
	cmd.AddCommand(networkACLUnsetCmd.command())

	// Edit.
	networkACLEditCmd := cmdNetworkACLEdit{global: c.global, networkACL: c}
	cmd.AddCommand(networkACLEditCmd.command())

	// Rename.
	networkACLRenameCmd := cmdNetworkACLRename{global: c.global, networkACL: c}
	cmd.AddCommand(networkACLRenameCmd.command())

	// Delete.
	networkACLDeleteCmd := cmdNetworkACLDelete{global: c.global, networkACL: c}
	cmd.AddCommand(networkACLDeleteCmd.command())

	// Rule.
	networkACLRuleCmd := cmdNetworkACLRule{global: c.global, networkACL: c}
	cmd.AddCommand(networkACLRuleCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// List.
type cmdNetworkACLList struct {
	global     *cmdGlobal
	networkACL *cmdNetworkACL

	flagFormat      string
	flagAllProjects bool
}

func (c *cmdNetworkACLList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", "[<remote>:]")
	cmd.Aliases = []string{"ls"}
	cmd.Short = "List network ACLs"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.RunE = c.run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", cli.FormatStringFlagLabel("Format (csv|json|table|yaml|compact)"))
	cmd.Flags().BoolVar(&c.flagAllProjects, "all-projects", false, "Display network ACLs from all projects")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(toComplete, ":", true, instanceServerRemoteCompletionFilters(*c.global.conf)...)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkACLList) run(cmd *cobra.Command, args []string) error {
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
		return errors.New("Filtering isn't supported yet")
	}

	var acls []api.NetworkACL
	if c.flagAllProjects {
		acls, err = resource.server.GetNetworkACLsAllProjects()
		if err != nil {
			return err
		}
	} else {
		acls, err = resource.server.GetNetworkACLs()
		if err != nil {
			return err
		}
	}

	data := [][]string{}
	for _, acl := range acls {
		strUsedBy := strconv.Itoa(len(acl.UsedBy))
		details := []string{
			acl.Name,
			acl.Description,
			strUsedBy,
		}

		if c.flagAllProjects {
			details = append([]string{acl.Project}, details...)
		}

		data = append(data, details)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		"NAME",
		"DESCRIPTION",
		"USED BY",
	}

	if c.flagAllProjects {
		header = append([]string{"PROJECT"}, header...)
	}

	return cli.RenderTable(c.flagFormat, header, data, acls)
}

// Show.
type cmdNetworkACLShow struct {
	global     *cmdGlobal
	networkACL *cmdNetworkACL
}

func (c *cmdNetworkACLShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", "[<remote>:]<ACL>")
	cmd.Short = "Show network ACL configurations"
	cmd.Long = cli.FormatSection("Description", cmd.Short)
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network_acl", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkACLShow) run(cmd *cobra.Command, args []string) error {
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
		return errors.New("Missing network ACL name")
	}

	// Show the network ACL config.
	netACL, _, err := resource.server.GetNetworkACL(resource.name)
	if err != nil {
		return err
	}

	sort.Strings(netACL.UsedBy)

	data, err := yaml.Marshal(&netACL)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Show log.
type cmdNetworkACLShowLog struct {
	global     *cmdGlobal
	networkACL *cmdNetworkACL
}

func (c *cmdNetworkACLShowLog) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show-log", "[<remote>:]<ACL>")
	cmd.Short = "Show network ACL log"
	cmd.Long = cli.FormatSection("Description", cmd.Short)
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network_acl", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkACLShowLog) run(cmd *cobra.Command, args []string) error {
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
		return errors.New("Missing network ACL name")
	}

	// Get the ACL log.
	log, err := resource.server.GetNetworkACLLogfile(resource.name)
	if err != nil {
		return err
	}

	_, err = io.Copy(os.Stdout, log)
	_ = log.Close()

	return err
}

// Get.
type cmdNetworkACLGet struct {
	global     *cmdGlobal
	networkACL *cmdNetworkACL

	flagIsProperty bool
}

func (c *cmdNetworkACLGet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get", "[<remote>:]<ACL> <key>")
	cmd.Short = "Get value for network ACL configuration key"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, "Get the key as a network ACL property")
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network_acl", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkACLConfigs(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkACLGet) run(cmd *cobra.Command, args []string) error {
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
		return errors.New("Missing network ACL name")
	}

	resp, _, err := resource.server.GetNetworkACL(resource.name)
	if err != nil {
		return err
	}

	if c.flagIsProperty {
		w := resp.Writable()
		res, err := getFieldByJSONTag(&w, args[1])
		if err != nil {
			return fmt.Errorf("The property %q does not exist on the network ACL %q: %v", args[1], resource.name, err)
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
type cmdNetworkACLCreate struct {
	global     *cmdGlobal
	networkACL *cmdNetworkACL
}

func (c *cmdNetworkACLCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", "[<remote>:]<ACL> [key=value...]")
	cmd.Short = "Create new network ACL"
	cmd.Long = cli.FormatSection("Description", cmd.Short)
	cmd.Example = cli.FormatSection("", `lxc network acl create a1

lxc network acl create a1 < config.yaml
    Create network acl with configuration from config.yaml`)

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network_acl", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkACLCreate) run(cmd *cobra.Command, args []string) error {
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
		return errors.New("Missing network ACL name")
	}

	// If stdin isn't a terminal, read yaml from it.
	var aclPut api.NetworkACLPut
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		err = yaml.UnmarshalStrict(contents, &aclPut)
		if err != nil {
			return err
		}
	}

	// Create the network ACL.
	acl := api.NetworkACLsPost{
		NetworkACLPost: api.NetworkACLPost{
			Name: resource.name,
		},
		NetworkACLPut: aclPut,
	}

	if acl.Config == nil {
		acl.Config = map[string]string{}
	}

	for i := 1; i < len(args); i++ {
		entry := strings.SplitN(args[i], "=", 2)
		if len(entry) < 2 {
			return fmt.Errorf("Bad key/value pair: %s", args[i])
		}

		acl.Config[entry[0]] = entry[1]
	}

	err = resource.server.CreateNetworkACL(acl)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf("Network ACL %s created\n", resource.name)
	}

	return nil
}

// Set.
type cmdNetworkACLSet struct {
	global     *cmdGlobal
	networkACL *cmdNetworkACL

	flagIsProperty bool
}

func (c *cmdNetworkACLSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set", "[<remote>:]<ACL> <key>=<value>...")
	cmd.Short = "Set network ACL configuration keys"
	cmd.Long = cli.FormatSection("Description", cmd.Short+`

For backward compatibility, a single configuration key may still be set with:
    lxc network set [<remote>:]<ACL> <key> <value>`)

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, "Set the key as a network ACL property")
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network_acl", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkACLSet) run(cmd *cobra.Command, args []string) error {
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
		return errors.New("Missing network ACL name")
	}

	// Get the network ACL.
	netACL, etag, err := resource.server.GetNetworkACL(resource.name)
	if err != nil {
		return err
	}

	// Set the keys.
	keys, err := getConfig(args[1:]...)
	if err != nil {
		return err
	}

	writable := netACL.Writable()
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

	return resource.server.UpdateNetworkACL(resource.name, writable, etag)
}

// Unset.
type cmdNetworkACLUnset struct {
	global        *cmdGlobal
	networkACL    *cmdNetworkACL
	networkACLSet *cmdNetworkACLSet

	flagIsProperty bool
}

func (c *cmdNetworkACLUnset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset", "[<remote>:]<ACL> <key>")
	cmd.Short = "Unset network ACL configuration key"
	cmd.Long = cli.FormatSection("Description", cmd.Short)
	cmd.RunE = c.run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, "Unset the key as a network ACL property")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network_acl", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpNetworkACLConfigs(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkACLUnset) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	c.networkACLSet.flagIsProperty = c.flagIsProperty

	args = append(args, "")
	return c.networkACLSet.run(cmd, args)
}

// Edit.
type cmdNetworkACLEdit struct {
	global     *cmdGlobal
	networkACL *cmdNetworkACL
}

func (c *cmdNetworkACLEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", "[<remote>:]<ACL>")
	cmd.Short = "Edit network ACL configurations as YAML"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network_acl", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkACLEdit) helpTemplate() string {
	return `### This is a YAML representation of the network ACL.
### Any line starting with a '# will be ignored.
###
### A network ACL consists of a set of rules and configuration items.
###
### An example would look like:
### name: allow-all-inbound
### description: test desc
### egress: []
### ingress:
### - action: allow
###   state: enabled
###   protocol: ""
###   source: ""
###   source_port: ""
###   destination: ""
###   destination_port: ""
###   icmp_type: ""
###   icmp_code: ""
### config:
###  user.foo: bah
###
### Note that only the ingress and egress rules, description and configuration keys can be changed.`
}

func (c *cmdNetworkACLEdit) run(cmd *cobra.Command, args []string) error {
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
		return errors.New("Missing network ACL name")
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		// Allow output of `lxc network acl show` command to be passed in here, but only take the contents
		// of the NetworkACLPut fields when updating the ACL. The other fields are silently discarded.
		newdata := api.NetworkACL{}
		err = yaml.UnmarshalStrict(contents, &newdata)
		if err != nil {
			return err
		}

		return resource.server.UpdateNetworkACL(resource.name, newdata.Writable(), "")
	}

	// Get the current config.
	netACL, etag, err := resource.server.GetNetworkACL(resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&netACL)
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
		newdata := api.NetworkACL{} // We show the full ACL info, but only send the writable fields.
		err = yaml.UnmarshalStrict(content, &newdata)
		if err == nil {
			err = resource.server.UpdateNetworkACL(resource.name, newdata.Writable(), etag)
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

// Rename.
type cmdNetworkACLRename struct {
	global     *cmdGlobal
	networkACL *cmdNetworkACL
}

func (c *cmdNetworkACLRename) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rename", "[<remote>:]<ACL> <new-name>")
	cmd.Aliases = []string{"mv"}
	cmd.Short = "Rename network ACL"
	cmd.Long = cli.FormatSection("Description", cmd.Short)
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network_acl", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkACLRename) run(cmd *cobra.Command, args []string) error {
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
		return errors.New("Missing network ACL name")
	}

	// Rename the network.
	err = resource.server.RenameNetworkACL(resource.name, api.NetworkACLPost{Name: args[1]})
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf("Network ACL %s renamed to %s\n", resource.name, args[1])
	}

	return nil
}

// Delete.
type cmdNetworkACLDelete struct {
	global     *cmdGlobal
	networkACL *cmdNetworkACL
}

func (c *cmdNetworkACLDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", "[<remote>:]<ACL>")
	cmd.Aliases = []string{"rm"}
	cmd.Short = "Delete network ACL"
	cmd.Long = cli.FormatSection("Description", cmd.Short)
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network_acl", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkACLDelete) run(cmd *cobra.Command, args []string) error {
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
		return errors.New("Missing network ACL name")
	}

	// Delete the network ACL.
	err = resource.server.DeleteNetworkACL(resource.name)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf("Network ACL %s deleted\n", resource.name)
	}

	return nil
}

// Add/Remove Rule.
type cmdNetworkACLRule struct {
	global          *cmdGlobal
	networkACL      *cmdNetworkACL
	flagRemoveForce bool
}

func (c *cmdNetworkACLRule) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rule")
	cmd.Short = "Manage network ACL rules"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	// Rule Add.
	cmd.AddCommand(c.commandAdd())

	// Rule Remove.
	cmd.AddCommand(c.commandRemove())

	return cmd
}

func (c *cmdNetworkACLRule) commandAdd() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("add", "[<remote>:]<ACL> <direction> <key>=<value>...")
	cmd.Short = "Add rule to an ACL"
	cmd.Long = cli.FormatSection("Description", cmd.Short)
	cmd.RunE = c.runAdd

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network_acl", toComplete)
		}

		if len(args) == 1 {
			return []string{"ingress", "egress"}, cobra.ShellCompDirectiveNoFileComp
		}

		if len(args) == 2 {
			return c.global.cmpNetworkACLRuleProperties()
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// networkACLRuleJSONStructFieldMap returns a map of JSON tag names to struct field indices for api.NetworkACLRule.
func networkACLRuleJSONStructFieldMap() map[string]int {
	// Use reflect to get field names in rule from json tags.
	ruleType := reflect.TypeOf(api.NetworkACLRule{})
	allowedKeys := make(map[string]int, ruleType.NumField())

	for i := range ruleType.NumField() {
		field := ruleType.Field(i)
		if field.PkgPath != "" {
			continue // Skip unexported fields. It is empty for upper case (exported) field names.
		}

		if field.Type.Name() != "string" {
			continue // Skip non-string fields.
		}

		// Split the json tag into its name and options (e.g. json:"action,omitempty").
		tagParts := strings.SplitN(string(field.Tag.Get(("json"))), ",", 2)
		fieldName := tagParts[0]

		if fieldName == "" {
			continue // Skip fields with no tagged field name.
		}

		allowedKeys[fieldName] = i // Add the name to allowed keys and record field index.
	}

	return allowedKeys
}

// parseConfigKeysToRule converts a map of key/value pairs into an api.NetworkACLRule using reflection.
func (c *cmdNetworkACLRule) parseConfigToRule(config map[string]string) (*api.NetworkACLRule, error) {
	// Use reflect to get struct field indices in NetworkACLRule for json tags.
	allowedKeys := networkACLRuleJSONStructFieldMap()

	// Initialise new rule.
	rule := api.NetworkACLRule{}
	ruleValue := reflect.ValueOf(&rule).Elem()

	for k, v := range config {
		fieldIndex, found := allowedKeys[k]
		if !found {
			return nil, fmt.Errorf("Unknown key: %s", k)
		}

		fieldValue := ruleValue.Field(fieldIndex)
		if !fieldValue.CanSet() {
			return nil, fmt.Errorf("Cannot set key: %s", k)
		}

		fieldValue.SetString(v) // Set the value into the struct field.
	}

	return &rule, nil
}

func (c *cmdNetworkACLRule) runAdd(cmd *cobra.Command, args []string) error {
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
		return errors.New("Missing network ACL name")
	}

	// Get config keys from arguments.
	keys, err := getConfig(args[2:]...)
	if err != nil {
		return err
	}

	// Get the network ACL.
	netACL, etag, err := resource.server.GetNetworkACL(resource.name)
	if err != nil {
		return err
	}

	rule, err := c.parseConfigToRule(keys)
	if err != nil {
		return err
	}

	rule.Normalise() // Strip space.

	// Default to enabled if not specified.
	if rule.State == "" {
		rule.State = "enabled"
	}

	// Add rule to the requested direction (if direction valid).
	switch args[1] {
	case "ingress":
		netACL.Ingress = append(netACL.Ingress, *rule)
	case "egress":
		netACL.Egress = append(netACL.Egress, *rule)
	default:
		return errors.New("The direction argument must be one of: ingress, egress")
	}

	return resource.server.UpdateNetworkACL(resource.name, netACL.Writable(), etag)
}

func (c *cmdNetworkACLRule) commandRemove() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("remove", "[<remote>:]<ACL> <direction> <key>=<value>...")
	cmd.Short = "Remove rule from an ACL"
	cmd.Long = cli.FormatSection("Description", cmd.Short)
	cmd.Flags().BoolVar(&c.flagRemoveForce, "force", false, "Remove all rules that match")

	cmd.RunE = c.runRemove

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("network_acl", toComplete)
		}

		if len(args) == 1 {
			return []string{"ingress", "egress"}, cobra.ShellCompDirectiveNoFileComp
		}

		if len(args) == 2 {
			return c.global.cmpNetworkACLRuleProperties()
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdNetworkACLRule) runRemove(cmd *cobra.Command, args []string) error {
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
		return errors.New("Missing network ACL name")
	}

	// Get config filters from arguments.
	filters, err := getConfig(args[2:]...)
	if err != nil {
		return err
	}

	// Get the network ACL.
	netACL, etag, err := resource.server.GetNetworkACL(resource.name)
	if err != nil {
		return err
	}

	// Use reflect to get struct field indices in NetworkACLRule for json tags.
	allowedKeys := networkACLRuleJSONStructFieldMap()

	// Check the supplied filters match possible fields.
	for k := range filters {
		_, found := allowedKeys[k]
		if !found {
			return fmt.Errorf("Unknown key: %s", k)
		}
	}

	// isFilterMatch returns whether the supplied rule has matching field values in the filters supplied.
	// If no filters are supplied, then the rule is considered to have matched.
	isFilterMatch := func(rule *api.NetworkACLRule, filters map[string]string) bool {
		ruleValue := reflect.ValueOf(rule).Elem()

		for k, v := range filters {
			fieldIndex, found := allowedKeys[k]
			if !found {
				return false
			}

			fieldValue := ruleValue.Field(fieldIndex)
			if fieldValue.String() != v {
				return false
			}
		}

		return true // Match found as all struct fields match the supplied filter values.
	}

	// removeFromRules removes a single rule that matches the filters supplied. If multiple rules match then
	// an error is returned unless c.flagRemoveForce is true, in which case all matching rules are removed.
	removeFromRules := func(rules []api.NetworkACLRule, filters map[string]string) ([]api.NetworkACLRule, error) {
		removed := false
		newRules := make([]api.NetworkACLRule, 0, len(rules))

		for _, r := range rules {
			if isFilterMatch(&r, filters) {
				if removed && !c.flagRemoveForce {
					return nil, errors.New("Multiple rules match. Use --force to remove them all")
				}

				removed = true
				continue // Don't add removed rule to newRules.
			}

			newRules = append(newRules, r)
		}

		if !removed {
			return nil, errors.New("No matching rule(s) found")
		}

		return newRules, nil
	}

	// Remove matching rule(s) from the requested direction (if direction valid).
	switch args[1] {
	case "ingress":
		rules, err := removeFromRules(netACL.Ingress, filters)
		if err != nil {
			return err
		}

		netACL.Ingress = rules
	case "egress":
		rules, err := removeFromRules(netACL.Egress, filters)
		if err != nil {
			return err
		}

		netACL.Egress = rules
	default:
		return errors.New("The direction argument must be one of: ingress, egress")
	}

	return resource.server.UpdateNetworkACL(resource.name, netACL.Writable(), etag)
}
