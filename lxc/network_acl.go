package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"strings"

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

	// Create.
	networkACLCreateCmd := cmdNetworkACLCreate{global: c.global, networkACL: c}
	cmd.AddCommand(networkACLCreateCmd.Command())

	return cmd
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
