package main

import (
	"fmt"
	"time"

	"github.com/lxc/lxd/client"
)

func cmdWaitReady(args *Args) error {
	var timeout int

	if args.Timeout == -1 {
		timeout = 15
	} else {
		timeout = args.Timeout
	}

	finger := make(chan error, 1)
	go func() {
		for {
			c, err := lxd.ConnectLXDUnix("", nil)
			if err != nil {
				time.Sleep(500 * time.Millisecond)
				continue
			}

			_, _, err = c.RawQuery("GET", "/internal/ready", nil, "")
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
