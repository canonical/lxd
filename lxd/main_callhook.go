package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/lxc/lxd/client"
)

type cmdCallhook struct {
	global *cmdGlobal
}

func (c *cmdCallhook) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "callhook <path> <id> <state>"
	cmd.Short = "Call container lifecycle hook in LXD"
	cmd.Long = `Description:
  Call container lifecycle hook in LXD

  This internal command notifies LXD about a container lifecycle event
  (start, stop, restart) and blocks until LXD has processed it.

`
	cmd.RunE = c.Run
	cmd.Hidden = true

	return cmd
}

func (c *cmdCallhook) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	if len(args) < 2 {
		cmd.Help()

		if len(args) == 0 {
			return nil
		}

		return fmt.Errorf("Missing required arguments")
	}

	path := args[0]
	id := args[1]
	state := args[2]
	target := ""

	// Only root should run this
	if os.Geteuid() != 0 {
		return fmt.Errorf("This must be run as root")
	}

	// Connect to LXD
	socket := os.Getenv("LXD_SOCKET")
	if socket == "" {
		socket = filepath.Join(path, "unix.socket")
	}
	d, err := lxd.ConnectLXDUnix(socket, nil)
	if err != nil {
		return err
	}

	// Prepare the request URL
	url := fmt.Sprintf("/internal/containers/%s/on%s", id, state)
	if state == "stop" {
		target = os.Getenv("LXC_TARGET")
		if target == "" {
			target = "unknown"
		}
		url = fmt.Sprintf("%s?target=%s", url, target)
	}

	// Setup the request
	hook := make(chan error, 1)
	go func() {
		_, _, err := d.RawQuery("GET", url, nil, "")
		if err != nil {
			hook <- err
			return
		}

		hook <- nil
	}()

	// Handle the timeout
	select {
	case err := <-hook:
		if err != nil {
			return err
		}
		break
	case <-time.After(30 * time.Second):
		return fmt.Errorf("Hook didn't finish within 30s")
	}

	if target == "reboot" {
		return fmt.Errorf("Reboot must be handled by LXD")
	}

	return nil
}
