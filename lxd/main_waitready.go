package main

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

type cmdWaitready struct {
	global *cmdGlobal

	flagTimeout uint64
	flagNetwork bool
	flagStorage bool
}

// Command returns a cobra.Command object representing the "waitready" command.
func (c *cmdWaitready) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "waitready"
	cmd.Short = "Wait for LXD to be ready to process requests"
	cmd.Long = `Description:
  Wait for LXD to be ready to process requests

  This command will block until LXD is reachable over its REST API and
  is done with early start tasks like re-starting previously started
  containers.

  Optional flags can be set to wait for additional resources to be ready.
`
	cmd.RunE = c.Run
	cmd.Flags().Uint64VarP(&c.flagTimeout, "timeout", "t", 0, "Number of seconds to wait before giving up")
	cmd.Flags().BoolVar(&c.flagNetwork, "network", false, "Whether to wait for all networks to be ready")
	cmd.Flags().BoolVar(&c.flagStorage, "storage", false, "Whether to wait for all storage pools to be ready")

	return cmd
}

// Run executes the "waitready" command.
func (c *cmdWaitready) Run(cmd *cobra.Command, args []string) error {
	ctx := context.Background() // Default to no timeout.
	if c.flagTimeout > 0 {
		var cancel context.CancelFunc

		// Add 100ms longer than timeout to allow receipt of response message from server so we can
		// differentiate between the server being unreachable or just not ready yet.
		ctx, cancel = context.WithTimeout(ctx, (time.Duration(c.flagTimeout)*time.Second)+(time.Millisecond*100))
		defer cancel()
	}

	log := func(i int, format string, args ...any) {
		// Start logging only after the 10'th attempt (about 5seconds).
		// Then after the 30'th attempt (about 15 seconds), log only only one attempt every 10 attempts
		// (about 5 seconds), to avoid being too verbose.
		doLog := false
		if i > 10 {
			doLog = i < 30 || ((i % 10) == 0)
		}

		if doLog {
			logger.Debugf(format, args...)
		}
	}

	for i := 0; ; i++ {
		if ctx.Err() != nil {
			return fmt.Errorf("LXD daemon not reachable after %ds timeout", c.flagTimeout)
		}

		if i > 0 {
			time.Sleep(time.Second)
		}

		log(i, "Connecting to LXD daemon (attempt %d)", i)

		var d lxd.InstanceServer
		d, err := lxd.ConnectLXDUnixWithContext(ctx, "", &lxd.ConnectionArgs{
			SkipGetServer: true,
		})
		if err != nil {
			log(i, "Failed connecting to LXD daemon (attempt %d): %v", i, err)
			continue
		}

		log(i, "Checking if LXD daemon is ready (attempt %d)", i)

		u := api.NewURL().Path("internal", "ready").WithQuery("timeout", strconv.FormatUint(c.flagTimeout, 10))

		// Add the network query parameter to the URL to wait for all networks to be ready.
		if c.flagNetwork {
			u.WithQuery("network", "1")
		}

		// Add the storage query parameter to the URL to wait for all storage pools to be ready.
		if c.flagStorage {
			u.WithQuery("storage", "1")
		}

		_, _, err = d.RawQuery(http.MethodGet, u.String(), nil, "")
		if err != nil {
			// LXD is reachable but is internally reporting as not ready after the specified timeout.
			if api.StatusErrorCheck(err, http.StatusServiceUnavailable) {
				return fmt.Errorf("%s after %ds timeout", err.Error(), c.flagTimeout)
			}

			log(i, "Failed to check if LXD daemon is ready (attempt %d): %v", i, err)
			continue
		}

		return nil // LXD is ready.
	}
}
