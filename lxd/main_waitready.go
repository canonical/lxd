package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/shared/logger"
)

type cmdWaitready struct {
	global *cmdGlobal

	flagTimeout int
}

func (c *cmdWaitready) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "waitready"
	cmd.Short = "Wait for LXD to be ready to process requests"
	cmd.Long = `Description:
  Wait for LXD to be ready to process requests

  This command will block until LXD is reachable over its REST API and
  is done with early start tasks like re-starting previously started
  containers.
`
	cmd.RunE = c.Run
	cmd.Flags().IntVarP(&c.flagTimeout, "timeout", "t", 0, "Number of seconds to wait before giving up"+"``")

	return cmd
}

func (c *cmdWaitready) Run(cmd *cobra.Command, args []string) error {
	finger := make(chan error, 1)
	var errLast error
	go func() {
		for i := 0; ; i++ {
			// Start logging only after the 10'th attempt (about 5
			// seconds). Then after the 30'th attempt (about 15
			// seconds), log only only one attempt every 10
			// attempts (about 5 seconds), to avoid being too
			// verbose.
			doLog := false
			if i > 10 {
				doLog = i < 30 || ((i % 10) == 0)
			}

			if doLog {
				logger.Debugf("Connecting to LXD daemon (attempt %d)", i)
			}
			d, err := lxd.ConnectLXDUnix("", nil)
			if err != nil {
				errLast = err
				if doLog {
					logger.Debugf("Failed connecting to LXD daemon (attempt %d): %v", i, err)
				}
				time.Sleep(500 * time.Millisecond)
				continue
			}

			if doLog {
				logger.Debugf("Checking if LXD daemon is ready (attempt %d)", i)
			}
			_, _, err = d.RawQuery("GET", "/internal/ready", nil, "")
			if err != nil {
				errLast = err
				if doLog {
					logger.Debugf("Failed to check if LXD daemon is ready (attempt %d): %v", i, err)
				}
				time.Sleep(500 * time.Millisecond)
				continue
			}

			finger <- nil
			return
		}
	}()

	if c.flagTimeout > 0 {
		select {
		case <-finger:
			break
		case <-time.After(time.Second * time.Duration(c.flagTimeout)):
			return fmt.Errorf("LXD still not running after %ds timeout (%v)", c.flagTimeout, errLast)
		}
	} else {
		<-finger
	}

	return nil
}
