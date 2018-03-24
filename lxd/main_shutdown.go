package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/lxc/lxd/client"
)

type cmdShutdown struct {
	global *cmdGlobal

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

	_, _, err = d.RawQuery("PUT", "/internal/shutdown", nil, "")
	if err != nil && !strings.HasSuffix(err.Error(), ": EOF") {
		// NOTE: if we got an EOF error here it means that the daemon
		// has shutdown so quickly that it already closed the unix
		// socket. We consider the daemon dead in this case.
		return err
	}

	chMonitor := make(chan bool, 1)
	go func() {
		monitor, err := d.GetEvents()
		if err != nil {
			close(chMonitor)
			return
		}

		monitor.Wait()
		close(chMonitor)
	}()

	if c.flagTimeout > 0 {
		select {
		case <-chMonitor:
			break
		case <-time.After(time.Second * time.Duration(c.flagTimeout)):
			return fmt.Errorf("LXD still running after %ds timeout", c.flagTimeout)
		}
	} else {
		<-chMonitor
	}

	return nil
}
