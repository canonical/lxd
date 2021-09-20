package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/sys"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
)

type cmdDaemon struct {
	global *cmdGlobal

	// Common options
	flagGroup string
}

func (c *cmdDaemon) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "lxd"
	cmd.Short = "The LXD container manager (daemon)"
	cmd.Long = `Description:
  The LXD container manager (daemon)

  This is the LXD daemon command line. It's typically started directly by your
  init system and interacted with through a tool like ` + "`lxc`" + `.

  There are however a number of subcommands that let you interact directly with
  the local LXD daemon and which may not be performed through the REST API alone.
`
	cmd.RunE = c.Run
	cmd.Flags().StringVar(&c.flagGroup, "group", "", "The group of users that will be allowed to talk to LXD"+"``")

	return cmd
}

func (c *cmdDaemon) Run(cmd *cobra.Command, args []string) error {
	if len(args) > 1 || (len(args) == 1 && args[0] != "daemon" && args[0] != "") {
		return fmt.Errorf("unknown command \"%s\" for \"%s\"", args[0], cmd.CommandPath())
	}

	// Only root should run this
	if os.Geteuid() != 0 {
		return fmt.Errorf("This must be run as root")
	}

	neededPrograms := []string{"ip", "rsync", "setfattr", "tar", "unsquashfs", "xz"}
	for _, p := range neededPrograms {
		_, err := exec.LookPath(p)
		if err != nil {
			return err
		}
	}

	conf := defaultDaemonConfig()
	conf.Group = c.flagGroup
	conf.Trace = c.global.flagLogTrace
	d := newDaemon(conf, sys.DefaultOS())

	err := d.Init()
	if err != nil {
		return err
	}

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, unix.SIGPWR)
	signal.Notify(ch, unix.SIGINT)
	signal.Notify(ch, unix.SIGQUIT)
	signal.Notify(ch, unix.SIGTERM)

	chIgnore := make(chan os.Signal, 1)
	signal.Notify(chIgnore, unix.SIGHUP)

	s := d.State()

	stop := func(sig os.Signal) {
		// Cancelling the context will make everyone aware that we're shutting down.
		d.cancel()

		// Wait for ongoing operations to finish if requested.
		if sig == unix.SIGPWR || sig == unix.SIGTERM {
			dbAvailable := true
			var shutdownTimeout time.Duration

			dbCtx, dbCtxCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer dbCtxCancel()

			err = s.Cluster.TransactionContext(dbCtx, func(tx *db.ClusterTx) error {
				config, err := cluster.ConfigLoad(tx)
				if err != nil {
					return err
				}

				shutdownTimeout = config.ShutdownTimeout()

				return nil
			})
			if err != nil {
				logger.Error("Database is not available, using default shutdown timeout", log.Ctx{"err": err})
				shutdownTimeout = 5 * time.Minute
				dbAvailable = false
			}

			// waitForOperations will block until all operations are done, or it's forced to shut down.
			// For the latter case, we re-use the shutdown channel which is filled when a shutdown is
			// initiated using `lxd shutdown`.
			// We wait up to 5 minutes for exec/console operations to finish. If there are still
			// running operations, we shut down the instances which will terminate the operations.
			logger.Info("Waiting for all operations to finish", log.Ctx{"timeout": shutdownTimeout})
			opCtx, opCtxCancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer opCtxCancel()

			waitForOperations(opCtx, s, d.shutdownChan)

			// Clean up instance, networks and storage pools if full clean shutdown requested.
			if sig == unix.SIGPWR {
				instancesShutdown(s, dbAvailable)

				if dbAvailable {
					networkShutdown(s)
					daemonStorageUnmount(s)
				} else {
					logger.Error("Skipping network and storage shutdown as database not available")
				}
			}
		} else {
			logger.Info("Exiting") // Just exit for all other signals.
		}
	}

	select {
	case sig := <-ch:
		logger.Info("Received signal", log.Ctx{"signal": sig})
		stop(sig)
	case <-d.shutdownChan:
		logger.Info("Asked to shutdown by API")
		stop(unix.SIGPWR)
	}

	d.Kill()
	err = d.Stop()

	logger.Info("Stopped")
	return err
}
