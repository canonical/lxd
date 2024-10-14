package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/lxd-user/callhook"
)

type cmdCallhook struct {
	global *cmdGlobal
}

// Command returns a cobra command for `lxd callhook`.
func (c *cmdCallhook) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "callhook <path> [<instance id>|<instance project> <instance name>] <hook>"
	cmd.Short = "Call container lifecycle hook in LXD"
	cmd.Long = `Description:
  Call container lifecycle hook in LXD

  This internal command notifies LXD about a container lifecycle event
  (stopns, stop) and blocks until LXD has processed it.
`
	cmd.RunE = c.Run
	cmd.Hidden = true

	return cmd
}

// Run executes the `lxd callhook` command.
func (c *cmdCallhook) Run(cmd *cobra.Command, args []string) error {
	// Only root should run this.
	if os.Geteuid() != 0 {
		return fmt.Errorf("This must be run as root")
	}

	// Parse request.
	lxdPath, projectName, instanceRef, hook, _, err := callhook.ParseArgs(args)
	if err != nil {
		_ = cmd.Help()
		if len(args) == 0 {
			return nil
		}

		return err
	}

	// Handle stop hooks.
	return callhook.HandleContainerHook(lxdPath, projectName, instanceRef, hook)
}
