package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/version"
)

type cmdVersion struct {
	global *cmdGlobal
}

func (c *cmdVersion) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("version", "[<remote>:]")
	cmd.Short = "Show local and remote versions"
	cmd.Long = cli.FormatSection("Description", `Show local and remote versions`)

	cmd.RunE = c.run

	return cmd
}

func (c *cmdVersion) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	// Client version
	clientVersion := version.Version
	if version.IsLTSVersion {
		clientVersion = clientVersion + " LTS"
	}

	// Remote version
	remote := ""
	if len(args) == 1 {
		remote = args[0]
		if !strings.HasSuffix(remote, ":") {
			remote = remote + ":"
		}
	}

	serverVersion := "unreachable"
	resources, err := c.global.ParseServers(remote)
	if err == nil {
		resource := resources[0]
		info, _, err := resource.server.GetServer()
		if err == nil {
			serverVersion = info.Environment.ServerVersion
			if info.Environment.ServerLTS {
				serverVersion = serverVersion + " LTS"
			}
		}
	}

	fmt.Printf("Client version: %s\n", clientVersion)
	fmt.Printf("Server version: %s\n", serverVersion)

	return nil
}
