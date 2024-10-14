package main

import (
	"github.com/spf13/cobra"

	"github.com/canonical/lxd/lxd-user/callhook"
)

type cmdCallhook struct {
	global *cmdGlobal
}

func (c *cmdCallhook) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "callhook <path> [<instance id>|<instance project> <instance name>] <hook>"
	cmd.Short = "Call container lifecycle hook in LXD"
	cmd.Long = `Description:
  Call container lifecycle hook in LXD

  This internal command notifies LXD about a container lifecycle event
  (start, stopns, stop, restart) and blocks until LXD has processed it.
`
	cmd.RunE = c.Run
	cmd.Hidden = true

	return cmd
}

func (c *cmdCallhook) Run(cmd *cobra.Command, args []string) error {
	// Parse request.
	lxdPath, projectName, instanceRef, hook, _, err := callhook.ParseArgs(args)
	if err != nil {
		_ = cmd.Help()
		if len(args) == 0 {
			return nil
		}

		return err
	}

	// Handle all other hook types.
	return callhook.HandleContainerHook(lxdPath, projectName, instanceRef, hook)
}
