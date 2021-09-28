package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/lxc/lxd/client"
)

type cmdShutdown struct {
	global *cmdGlobal

	flagForce   bool
	flagTimeout int
}

func (c *cmdShutdown) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "shutdown"
	cmd.Short = "Tell LXD to shutdown all containers and exit"
	cmd.Long = `Description:
  Tell LXD to shutdown all containers and exit

  This will tell LXD to start a clean shutdown of all containers,
  followed by having itself shutdown and exit.

  This can take quite a while as containers can take a long time to
  shutdown, especially if a non-standard timeout was configured for them.
`
	cmd.RunE = c.Run
	cmd.Flags().IntVarP(&c.flagTimeout, "timeout", "t", 0, "Number of seconds to wait before giving up"+"``")
	cmd.Flags().BoolVarP(&c.flagForce, "force", "f", false, "Force shutdown instead of waiting for running operations to finish"+"``")

	return cmd
}

func (c *cmdShutdown) Run(cmd *cobra.Command, args []string) error {
	connArgs := &lxd.ConnectionArgs{
		SkipGetServer: true,
	}

	d, err := lxd.ConnectLXDUnix("", connArgs)
	if err != nil {
		return err
	}

	v := url.Values{}
	v.Set("force", strconv.FormatBool(c.flagForce))

	chResult := make(chan error, 1)
	go func() {
		defer close(chResult)

		// Request shutdown, this shouldn't return until daemon has stopped.
		_, _, err = d.RawQuery("PUT", fmt.Sprintf("/internal/shutdown?%s", v.Encode()), nil, "")
		if err != nil && !strings.HasSuffix(err.Error(), ": EOF") {
			// NOTE: if we got an EOF error here it means that the daemon has shutdown so quickly that
			// it already closed the unix socket. We consider the daemon dead in this case so no need
			// to return the error.
			chResult <- err
			return
		}

		// Try connecting to events endpoint to check the daemon has really shutdown.
		monitor, err := d.GetEvents()
		if err != nil {
			return
		}

		monitor.Wait()
	}()

	if c.flagTimeout > 0 {
		select {
		case err = <-chResult:
			return err
		case <-time.After(time.Second * time.Duration(c.flagTimeout)):
			return fmt.Errorf("LXD still running after %ds timeout", c.flagTimeout)
		}
	}

	return <-chResult
}
