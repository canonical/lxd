package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/lxc/lxd"
)

func cmdShutdown() error {
	var timeout int

	if *argTimeout == -1 {
		timeout = 60
	} else {
		timeout = *argTimeout
	}

	c, err := lxd.NewClient(&lxd.DefaultConfig, "local")
	if err != nil {
		return err
	}

	req, err := http.NewRequest("PUT", c.BaseURL+"/internal/shutdown", nil)
	if err != nil {
		return err
	}

	_, err = c.Http.Do(req)
	if err != nil {
		return err
	}

	monitor := make(chan error, 1)
	go func() {
		monitor <- c.Monitor(nil, func(m interface{}) {}, nil)
	}()

	select {
	case <-monitor:
		break
	case <-time.After(time.Second * time.Duration(timeout)):
		return fmt.Errorf("LXD still running after %ds timeout.", timeout)
	}

	return nil
}
