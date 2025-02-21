package main

import (
	"errors"
	"fmt"
	"io"
	"net"
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

type cmdService struct {
	global *cmdGlobal
}

func (c *cmdService) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("service")
	cmd.Short = i18n.G("Manage services")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage services`))

	// Join
	serviceAddCmd := cmdServiceAdd{global: c.global, service: c}
	cmd.AddCommand(serviceAddCmd.command())

	// List
	serviceListCmd := cmdServiceList{global: c.global, service: c}
	cmd.AddCommand(serviceListCmd.command())

	// Remove
	serviceRemoveCmd := cmdServiceRemove{global: c.global, service: c}
	cmd.AddCommand(serviceRemoveCmd.command())

	// Edit
	serviceEditCmd := cmdServiceEdit{global: c.global, service: c}
	cmd.AddCommand(serviceEditCmd.command())

	// Get
	serviceGetCmd := cmdServiceGet{global: c.global, service: c}
	cmd.AddCommand(serviceGetCmd.command())

	// Set
	serviceSetCmd := cmdServiceSet{global: c.global, service: c}
	cmd.AddCommand(serviceSetCmd.command())

	// Show
	serviceShowCmd := cmdServiceShow{global: c.global, service: c}
	cmd.AddCommand(serviceShowCmd.command())

	// Unset
	serviceUnsetCmd := cmdServiceUnset{global: c.global, service: c}
	cmd.AddCommand(serviceUnsetCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// Join.
type cmdServiceAdd struct {
	global  *cmdGlobal
	service *cmdService

	flagToken       string
	flagAddress     string
	flagIdentity    string
	flagDescription string
}

func (c *cmdServiceAdd) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("join", i18n.G("<name> <type> [--token <trust_token>] [--address <ip_address>] [--identity <identity_name>]"))
	cmd.Short = i18n.G("Join a service")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Join a service`))
	cmd.Flags().StringVarP(&c.flagIdentity, "token", "t", "", "Trust token to use when adding lxd service")
	cmd.Flags().StringVarP(&c.flagIdentity, "address", "a", "", "Optional IP to override addresses inside token")
	cmd.Flags().StringVarP(&c.flagIdentity, "identity", "i", "", "Pending identity to use for joining service")
	_ = cmd.MarkFlagRequired("identity")
	cmd.Flags().StringVarP(&c.flagDescription, "description", "d", "", "Service description")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		// TODO: Fetch types from metadata API config keys.
		if len(args) == 1 {
			return []string{"lxd", "image-server", "s3", "cluster-manager"}, cobra.ShellCompDirectiveNoFileComp
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	// Conditionally mark "token" and "identity" as required if type is "lxd".
	cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		if args[1] == "lxd" {
			_ = cmd.MarkFlagRequired("token")
			_ = cmd.MarkFlagRequired("identity")
		}

		return nil
	}

	cmd.RunE = c.run

	return cmd
}

func (c *cmdServiceAdd) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	if len(args) == 1 {
		return errors.New(i18n.G("Missing service type"))
	}

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
	client := resource.server

	service := api.ServicePost{
		Name: args[0],
		Type: args[1],
	}

	if args[1] == "lxd" {
		service.TrustToken = c.flagToken
		service.IdentityName = c.flagIdentity

		if len(args) == 3 {
			if net.ParseIP(c.flagAddress) == nil {
				return fmt.Errorf(i18n.G("Invalid IP address: %s"), c.flagAddress)
			}

			service.Address = c.flagAddress
		}
	}

	if c.flagDescription != "" {
		service.Description = c.flagDescription
	}

	if service.Config == nil {
		service.Config = map[string]string{}
	}

	for i := 2; i < len(args); i++ {
		entry := strings.SplitN(args[i], "=", 2)
		if len(entry) < 2 {
			return fmt.Errorf(i18n.G("Bad key=value pair: %s"), entry)
		}

		service.Config[entry[0]] = entry[1]
	}

	err = client.AddService(service)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Service %s joined")+"\n", args[0])
	}

	return nil
}

// List.
type cmdServiceList struct {
	global  *cmdGlobal
	service *cmdService

	flagFormat string
}

func (c *cmdServiceList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List service")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List service`))

	cmd.RunE = c.run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpRemotes(toComplete, false)
	}

	return cmd
}

func (c *cmdServiceList) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 1)
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

	services, err := client.GetServices()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, service := range services {
		details := []string{
			service.Name,
			service.Type.String(),
			strings.Join(service.Addresses, ","),
			service.Description,
		}

		data = append(data, details)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		i18n.G("NAME"),
		i18n.G("TYPE"),
		i18n.G("ADDRESSES"),
		i18n.G("DESCRIPTION"),
	}

	return cli.RenderTable(c.flagFormat, header, data, services)
}

// Remove.
type cmdServiceRemove struct {
	global  *cmdGlobal
	service *cmdService
}

func (c *cmdServiceRemove) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("remove", i18n.G("<service>"))
	cmd.Short = i18n.G("Remove services")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Remove services`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpServices(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdServiceRemove) run(cmd *cobra.Command, args []string) error {
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

	err = client.DeleteService(args[0])
	if err != nil {
		return err
	}

	return nil
}

// Edit.
type cmdServiceEdit struct {
	global  *cmdGlobal
	service *cmdService
}

func (c *cmdServiceEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<service>"))
	cmd.Short = i18n.G("Edit service configurations as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit service configurations as YAML`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc service edit [<remote>:]<service> < service.yaml
    Update a service using the content of service.yaml.`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpServices(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdServiceEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of a service.
### Any line starting with a '#' will be ignored.
###
### A service consists of a set of configuration items.
###
### An example would look like:
### description: backup cluster
### addresses: [10.0.0.1:8443, 10.0.0.2:8443]
### config:
###   `)
}

func (c *cmdServiceEdit) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing service name"))
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.ServicePut{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}

		return resource.server.UpdateService(resource.name, newdata, "")
	}

	// Extract the current value
	service, etag, err := resource.server.GetService(resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&service)
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
		newdata := api.ServicePut{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = resource.server.UpdateService(resource.name, newdata, etag)
		}

		// Respawn the editor
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

// Get.
type cmdServiceGet struct {
	global  *cmdGlobal
	service *cmdService

	flagIsProperty bool
}

func (c *cmdServiceGet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get", i18n.G("[<remote>:]<service> <key>"))
	cmd.Short = i18n.G("Get values for service configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Get values for service configuration keys`))

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a service property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpServices(toComplete)
		}

		// TODO: Service config completions.
		// if len(args) == 1 {
		// 	return c.global.cmpServiceSetConfigs(args[0])
		// }

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdServiceGet) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing service name"))
	}

	resp, _, err := resource.server.GetService(resource.name)
	if err != nil {
		return err
	}

	if c.flagIsProperty {
		w := resp.Writable()
		res, err := getFieldByJsonTag(&w, args[1])
		if err != nil {
			return fmt.Errorf(i18n.G("The property %q does not exist for the service %q: %v"), args[1], resource.name, err)
		}

		fmt.Printf("%v\n", res)
	} else {
		v, ok := resp.Config[args[1]]
		if ok {
			fmt.Println(v)
		}
	}

	return nil
}

// Set.
type cmdServiceSet struct {
	global  *cmdGlobal
	service *cmdService

	flagIsProperty bool
}

func (c *cmdServiceSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set", i18n.G("[<remote>:]<service> <key> <value>"))
	cmd.Short = i18n.G("Set service configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Set service configuration keys`))

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a service property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpServices(toComplete)
		}

		// TODO:
		// if len(args) == 1 {
		// 	return c.global.cmpServiceAllConfigs(args[0])
		// }

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdServiceSet) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, -1)
	if exit {
		return err
	}

	// Parse remote.
	remote := args[0]

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]
	if resource.name == "" {
		return errors.New(i18n.G("Missing service name"))
	}

	client := resource.server

	// Get service.
	service, etag, err := client.GetService(resource.name)
	if err != nil {
		return err
	}

	// Parse key/values.
	keys, err := getConfig(args[1:]...)
	if err != nil {
		return err
	}

	writable := service.Writable()
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
		if writable.Config == nil {
			writable.Config = make(map[string]string)
		}

		// Update the volume config keys.
		for k, v := range keys {
			writable.Config[k] = v
		}
	}

	err = client.UpdateService(resource.name, writable, etag)
	if err != nil {
		return err
	}

	return nil
}

// Show.
type cmdServiceShow struct {
	global  *cmdGlobal
	service *cmdService
}

func (c *cmdServiceShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<service>"))
	cmd.Short = i18n.G("Show service configurations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show service configurations`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpServices(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdServiceShow) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
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
	client := resource.server

	if resource.name == "" {
		return errors.New(i18n.G("Missing service name"))
	}

	service, _, err := client.GetService(resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&service)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Unset.
type cmdServiceUnset struct {
	global     *cmdGlobal
	service    *cmdService
	serviceSet *cmdServiceSet

	flagIsProperty bool
}

func (c *cmdServiceUnset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset", i18n.G("[<remote>:]<service> <key>"))
	cmd.Short = i18n.G("Unset service configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Unset service configuration keys`))

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a service property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpServices(toComplete)
		}

		// TODO:
		// if len(args) == 1 {
		// 	return c.global.cmpServiceSetConfigs(args[0])
		// }

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdServiceUnset) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	c.serviceSet.flagIsProperty = c.flagIsProperty

	args = append(args, "")
	return c.serviceSet.run(cmd, args)
}
