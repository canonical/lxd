package main

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/lxc/lxd"
)

func cmdCallHook(args []string) error {
	if len(args) < 4 {
		return fmt.Errorf("Invalid arguments")
	}

	path := args[1]
	id := args[2]
	state := args[3]
	target := ""

	err := os.Setenv("LXD_DIR", path)
	if err != nil {
		return err
	}

	c, err := lxd.NewClient(&lxd.DefaultConfig, "local")
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/internal/containers/%s/on%s", c.BaseURL, id, state)

	if state == "stop" {
		target = os.Getenv("LXC_TARGET")
		if target == "" {
			target = "unknown"
		}
		url = fmt.Sprintf("%s?target=%s", url, target)
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}

	hook := make(chan error, 1)
	go func() {
		raw, err := c.Http.Do(req)
		if err != nil {
			hook <- err
			return
		}

		_, err = lxd.HoistResponse(raw, lxd.Sync)
		if err != nil {
			hook <- err
			return
		}

		hook <- nil
	}()

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
