package main

import (
	"fmt"
	"time"

	"github.com/lxc/lxd/client"
)

func cmdShutdown(args *Args) error {
	c, err := lxd.ConnectLXDUnix("", nil)
	if err != nil {
		return err
	}

	_, _, err = c.RawQuery("PUT", "/internal/shutdown", nil, "")
	if err != nil {
		return err
	}

	chMonitor := make(chan bool, 1)
	go func() {
		monitor, err := c.GetEvents()
		if err != nil {
			close(chMonitor)
			return
		}

		monitor.Wait()
		close(chMonitor)
	}()

	if args.Timeout > 0 {
		select {
		case <-chMonitor:
			break
		case <-time.After(time.Second * time.Duration(args.Timeout)):
			return fmt.Errorf("LXD still running after %ds timeout.", args.Timeout)
		}
	} else {
		<-chMonitor
	}

	return nil
}
