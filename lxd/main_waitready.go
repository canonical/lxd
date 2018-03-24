package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/lxc/lxd/client"
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
	go func() {
		for {
			d, err := lxd.ConnectLXDUnix("", nil)
			if err != nil {
				time.Sleep(500 * time.Millisecond)
				continue
			}

			_, _, err = d.RawQuery("GET", "/internal/ready", nil, "")
			if err != nil {
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
			return fmt.Errorf("LXD still not running after %ds timeout", c.flagTimeout)
		}
	} else {
		<-finger
	}

	return nil
}
