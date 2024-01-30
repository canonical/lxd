package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
	"github.com/canonical/lxd/shared/version"
)

type cmdVersion struct {
	global *cmdGlobal
}

func (c *cmdVersion) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("version", i18n.G("[<remote>:]"))
	cmd.Short = i18n.G("Show local and remote versions")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show local and remote versions`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdVersion) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	// Client version
	clientVersion := version.Version
	if version.IsLTSVersion {
		clientVersion = fmt.Sprintf("%s LTS", clientVersion)
	}

	// Remote version
	remote := ""
	if len(args) == 1 {
		remote = args[0]
		if !strings.HasSuffix(remote, ":") {
			remote = remote + ":"
		}
	}

	serverVersion := i18n.G("unreachable")
	resources, err := c.global.ParseServers(remote)
	if err == nil {
		resource := resources[0]
		info, _, err := resource.server.GetServer()
		if err == nil {
			serverVersion = info.Environment.ServerVersion
			if info.Environment.ServerLTS {
				serverVersion = fmt.Sprintf("%s LTS", serverVersion)
			}
		}
	}

	fmt.Printf(i18n.G("Client version: %s\n"), clientVersion)
	fmt.Printf(i18n.G("Server version: %s\n"), serverVersion)

	return nil
}
