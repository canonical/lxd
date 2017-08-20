package main

import (
	"fmt"
	"os"
	"time"

	"github.com/lxc/lxd/client"
)

func cmdCallHook(args *Args) error {
	// Parse the arguments
	if len(args.Params) < 3 {
		return fmt.Errorf("Invalid arguments")
	}

	path := args.Params[0]
	id := args.Params[1]
	state := args.Params[2]
	target := ""

	// Connect to LXD
	c, err := lxd.ConnectLXDUnix(fmt.Sprintf("%s/unix.socket", path), nil)
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
		_, _, err := c.RawQuery("GET", url, nil, "")
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
		return fmt.Errorf("Reboot must be handled by LXD.")
	}

	return nil
}
