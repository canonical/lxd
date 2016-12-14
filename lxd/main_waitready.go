package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/lxc/lxd"
)

func cmdWaitReady() error {
	var timeout int

	if *argTimeout == -1 {
		timeout = 15
	} else {
		timeout = *argTimeout
	}

	finger := make(chan error, 1)
	go func() {
		for {
			c, err := lxd.NewClient(&lxd.DefaultConfig, "local")
			if err != nil {
				time.Sleep(500 * time.Millisecond)
				continue
			}

			req, err := http.NewRequest("GET", c.BaseURL+"/internal/ready", nil)
			if err != nil {
				time.Sleep(500 * time.Millisecond)
				continue
			}

			raw, err := c.Http.Do(req)
			if err != nil {
				time.Sleep(500 * time.Millisecond)
				continue
			}

			_, err = lxd.HoistResponse(raw, lxd.Sync)
			if err != nil {
				time.Sleep(500 * time.Millisecond)
				continue
			}

			finger <- nil
			return
		}
	}()

	select {
	case <-finger:
		break
	case <-time.After(time.Second * time.Duration(timeout)):
		return fmt.Errorf("LXD still not running after %ds timeout.", timeout)
	}

	return nil
}
