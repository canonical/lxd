package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/lxc/lxd/client"
)

func cmdShutdown(args *Args) error {
	connArgs := &lxd.ConnectionArgs{
		SkipGetServer: true,
	}
	c, err := lxd.ConnectLXDUnix("", connArgs)
	if err != nil {
		return err
	}

	_, _, err = c.RawQuery("PUT", "/internal/shutdown", nil, "")
	if err != nil && !strings.HasSuffix(err.Error(), ": EOF") {
		// NOTE: if we got an EOF error here it means that the daemon
		// has shutdown so quickly that it already closed the unix
		// socket. We consider the daemon dead in this case.
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
