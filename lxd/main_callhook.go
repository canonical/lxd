package main

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/lxd-user/callhook"
	"github.com/canonical/lxd/lxd/device/cdi"
)

type cmdCallhook struct {
	global            *cmdGlobal
	devicesRootFolder string
}

// Command returns a cobra command for `lxd callhook`.
func (c *cmdCallhook) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "callhook <path> [<instance id>|<instance project> <instance name>] <hook>"
	cmd.Short = "Call container lifecycle hook in LXD"
	cmd.Long = `Description:
  Call container lifecycle hook in LXD

  This internal command notifies LXD about a container lifecycle event
  (start, startmountns, stopns, stop, restart) and blocks until LXD has processed it.
`
	cmd.RunE = c.Run
	cmd.Hidden = true

	// devicesRootFolder is used to specify where to look for CDI config device files.
	cmd.Flags().StringVar(&c.devicesRootFolder, "devicesRootFolder", "", "Root folder for CDI devices")

	return cmd
}

// Run executes the `lxd callhook` command.
func (c *cmdCallhook) Run(cmd *cobra.Command, args []string) error {
	// Only root should run this.
	if os.Geteuid() != 0 {
		return errors.New("This must be run as root")
	}

	// Parse request.
	lxdPath, projectName, instanceRef, hook, cdiHooksFiles, err := callhook.ParseArgs(args)
	if err != nil {
		_ = cmd.Help()
		if len(args) == 0 {
			return nil
		}

		return err
	}

	// Handle startmountns hook.
	if hook == "startmountns" {
		if len(cdiHooksFiles) == 0 {
			return errors.New("Missing required CDI hooks files argument")
		}

		if c.devicesRootFolder == "" {
			return errors.New("Missing required --devicesRootFolder <directory> flag")
		}

		containerRootFSMount := os.Getenv("LXC_ROOTFS_MOUNT")
		if containerRootFSMount == "" {
			return errors.New("LXC_ROOTFS_MOUNT is empty")
		}

		var err error
		for _, cdiHooksFile := range cdiHooksFiles {
			cdiHookPath := filepath.Join(c.devicesRootFolder, cdiHooksFile)
			err = cdi.ApplyHooksToContainer(cdiHookPath, containerRootFSMount)
			if err != nil {
				return err
			}
		}

		return nil
	}

	// Handle all other hook types.
	return callhook.HandleContainerHook(lxdPath, projectName, instanceRef, hook)
}
