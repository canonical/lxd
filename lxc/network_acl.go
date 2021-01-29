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

type cmdNetworkACL struct {
	global *cmdGlobal
}

func (c *cmdNetworkACL) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("acl")
	cmd.Short = i18n.G("Manage network ACLs")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Manage network ACLs"))

	// List.
	networkACLListCmd := cmdNetworkACLList{global: c.global, networkACL: c}
	cmd.AddCommand(networkACLListCmd.Command())

	// Show.
	networkACLShowCmd := cmdNetworkACLShow{global: c.global, networkACL: c}
	cmd.AddCommand(networkACLShowCmd.Command())

	// Get.
	networkACLGetCmd := cmdNetworkACLGet{global: c.global, networkACL: c}
	cmd.AddCommand(networkACLGetCmd.Command())

	// Create.
	networkACLCreateCmd := cmdNetworkACLCreate{global: c.global, networkACL: c}
	cmd.AddCommand(networkACLCreateCmd.Command())

	// Set.
	networkACLSetCmd := cmdNetworkACLSet{global: c.global, networkACL: c}
	cmd.AddCommand(networkACLSetCmd.Command())

	// Unset.
	networkACLUnsetCmd := cmdNetworkACLUnset{global: c.global, networkACL: c, networkACLSet: &networkACLSetCmd}
	cmd.AddCommand(networkACLUnsetCmd.Command())

	// Edit.
	networkACLEditCmd := cmdNetworkACLEdit{global: c.global, networkACL: c}
	cmd.AddCommand(networkACLEditCmd.Command())

	// Rename.
	networkACLRenameCmd := cmdNetworkACLRename{global: c.global, networkACL: c}
	cmd.AddCommand(networkACLRenameCmd.Command())

	// Delete.
	networkACLDeleteCmd := cmdNetworkACLDelete{global: c.global, networkACL: c}
	cmd.AddCommand(networkACLDeleteCmd.Command())

	return cmd
}

// List.
type cmdNetworkACLList struct {
	global     *cmdGlobal
	networkACL *cmdNetworkACL

	flagFormat string
}

func (c *cmdNetworkACLList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List available network ACLS")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("List available network ACL"))

	cmd.RunE = c.Run
	cmd.Flags().StringVar(&c.flagFormat, "format", "table", i18n.G("Format (csv|json|table|yaml)")+"``")

	return cmd
}

func (c *cmdNetworkACLList) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks.
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

	acls, err := resource.server.GetNetworkACLs()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, acl := range acls {
		strUsedBy := fmt.Sprintf("%d", len(acl.UsedBy))
		details := []string{
			acl.Name,
			acl.Description,
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

	return utils.RenderTable(c.flagFormat, header, data, acls)
}

// Show.
type cmdNetworkACLShow struct {
	global     *cmdGlobal
	networkACL *cmdNetworkACL
}

func (c *cmdNetworkACLShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<ACL>"))
	cmd.Short = i18n.G("Show network ACL configurations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Show network ACL configurations"))
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdNetworkACLShow) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks.
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
		return fmt.Errorf(i18n.G("Missing network ACL name"))
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

// Get.
type cmdNetworkACLGet struct {
	global     *cmdGlobal
	networkACL *cmdNetworkACL
}

func (c *cmdNetworkACLGet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get", i18n.G("[<remote>:]<ACL> <key>"))
	cmd.Short = i18n.G("Get values for network ACL configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Get values for network ACL configuration keys"))
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdNetworkACLGet) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks.
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
		return fmt.Errorf(i18n.G("Missing network ACL name"))
	}

	resp, _, err := resource.server.GetNetworkACL(resource.name)
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
type cmdNetworkACLCreate struct {
	global     *cmdGlobal
	networkACL *cmdNetworkACL
}

func (c *cmdNetworkACLCreate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", i18n.G("[<remote>:]<ACL> [key=value...]"))
	cmd.Short = i18n.G("Create new network ACLs")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Create new network ACLs"))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdNetworkACLCreate) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks.
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
		return fmt.Errorf(i18n.G("Missing network ACL name"))
	}

	// If stdin isn't a terminal, read yaml from it.
	var aclPut api.NetworkACLPut
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := ioutil.ReadAll(os.Stdin)
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
			return fmt.Errorf(i18n.G("Bad key/value pair: %s"), args[i])
		}

		acl.Config[entry[0]] = entry[1]
	}

	err = resource.server.CreateNetworkACL(acl)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network ACL %s created")+"\n", resource.name)
	}

	return nil
}

// Set.
type cmdNetworkACLSet struct {
	global     *cmdGlobal
	networkACL *cmdNetworkACL
}

func (c *cmdNetworkACLSet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set", i18n.G("[<remote>:]<ACL> <key>=<value>..."))
	cmd.Short = i18n.G("Set network ACL configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Set network ACL configuration keys

For backward compatibility, a single configuration key may still be set with:
    lxc network set [<remote>:]<ACL> <key> <value>`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdNetworkACLSet) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks.
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
		return fmt.Errorf(i18n.G("Missing network ACL name"))
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

	for k, v := range keys {
		netACL.Config[k] = v
	}

	return resource.server.UpdateNetworkACL(resource.name, netACL.Writable(), etag)
}

// Unset.
type cmdNetworkACLUnset struct {
	global        *cmdGlobal
	networkACL    *cmdNetworkACL
	networkACLSet *cmdNetworkACLSet
}

func (c *cmdNetworkACLUnset) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset", i18n.G("[<remote>:]<ACL> <key>"))
	cmd.Short = i18n.G("Unset network ACL configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Unset network ACL configuration keys"))
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdNetworkACLUnset) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	args = append(args, "")
	return c.networkACLSet.Run(cmd, args)
}

// Edit.
type cmdNetworkACLEdit struct {
	global     *cmdGlobal
	networkACL *cmdNetworkACL
}

func (c *cmdNetworkACLEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<ACL>"))
	cmd.Short = i18n.G("Edit network ACL configurations as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Edit network ACL configurations as YAML"))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdNetworkACLEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the network ACL.
### Any line starting with a '# will be ignored.
###
### A network ACL consists of a set of rules and configuration items.
###
### An example would look like:
### name: allow-all-inbound
### description: test desc
### egress: []
### ingress:
### - action: accept
###   source: ""
###   destination: ""
###   protocol: ""
###   sourceport: ""
###   destinationport: ""
###   icmptype: ""
###   icmpcode: ""
### config:
###  user.foo: bah
###
### Note that only the ingress and egress rules, description and configuration keys can be changed.`)
}

func (c *cmdNetworkACLEdit) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks.
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
		return fmt.Errorf(i18n.G("Missing network ACL name"))
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.NetworkACLPut{}
		err = yaml.UnmarshalStrict(contents, &newdata)
		if err != nil {
			return err
		}

		return resource.server.UpdateNetworkACL(resource.name, newdata, "")
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

// Rename.
type cmdNetworkACLRename struct {
	global     *cmdGlobal
	networkACL *cmdNetworkACL
}

func (c *cmdNetworkACLRename) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rename", i18n.G("[<remote>:]<ACL> <new-name>"))
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename network ACLs")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Rename network ACLs"))
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdNetworkACLRename) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks.
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
		return fmt.Errorf(i18n.G("Missing network ACL name"))
	}

	// Rename the network.
	err = resource.server.RenameNetworkACL(resource.name, api.NetworkACLPost{Name: args[1]})
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network ACL %s renamed to %s")+"\n", resource.name, args[1])
	}

	return nil
}

// Delete.
type cmdNetworkACLDelete struct {
	global     *cmdGlobal
	networkACL *cmdNetworkACL
}

func (c *cmdNetworkACLDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<ACL>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete network ACLs")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Delete network ACLs"))
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdNetworkACLDelete) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks.
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
		return fmt.Errorf(i18n.G("Missing network ACL name"))
	}

	// Delete the network ACL.
	err = resource.server.DeleteNetworkACL(resource.name)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Network ACL %s deleted")+"\n", resource.name)
	}

	return nil
}
