package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/db"
	storagePools "github.com/lxc/lxd/lxd/storage"
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

		// Handle shutdown (unix.SIGPWR) and reload (unix.SIGTERM) signals.
		if sig == unix.SIGPWR || sig == unix.SIGTERM {
			// waitForOperations will block until all operations are done, or it's forced to shut down.
			// For the latter case, we re-use the shutdown channel which is filled when a shutdown is
			// initiated using `lxd shutdown`.
			logger.Info("Waiting for operations to finish")
			waitForOperations(s, d.shutdownChan)

			// Unmount daemon image and backup volumes if set.
			logger.Info("Stopping daemon storage volumes")
			done := make(chan struct{})
			go func() {
				err := daemonStorageVolumesUnmount(s)
				if err != nil {
					logger.Warn("Failed to unmount image and backup volumes", log.Ctx{"err": err})
				}

				done <- struct{}{}
			}()

			// Only wait 60 seconds in case the storage backend is unreachable.
			select {
			case <-time.After(time.Minute):
				logger.Warn("Timed out waiting for image and backup volume")
			case <-done:
			}

			// Full shutdown requested.
			if sig == unix.SIGPWR {
				logger.Info("Stopping instances")
				instancesShutdown(s)

				logger.Info("Stopping networks")
				networkShutdown(s)

				// Unmount storage pools after instances stopped.
				logger.Info("Stopping storage pools")
				pools, err := s.Cluster.GetStoragePoolNames()
				if err != nil && err != db.ErrNoSuchObject {
					logger.Error("Failed to get storage pools", log.Ctx{"err": err})
				}

				for _, poolName := range pools {
					pool, err := storagePools.GetPoolByName(s, poolName)
					if err != nil {
						logger.Error("Failed to get storage pool", log.Ctx{"pool": poolName, "err": err})
						continue
					}

					if pool.Driver().Info().Name == "lvm" {
						continue // TODO figure out the intermittent issue with LVM tests when this is removed.
					}

					_, err = pool.Unmount()
					if err != nil {
						logger.Error("Unable to unmount storage pool", log.Ctx{"pool": poolName, "err": err})
						continue
					}
				}
			}
		}

		d.Kill()
	}

	select {
	case sig := <-ch:
		logger.Info("Received signal", log.Ctx{"signal": sig})
		stop(sig)
	case <-d.shutdownChan:
		logger.Info("Asked to shutdown by API")
		stop(unix.SIGPWR)
	}

	return d.Stop()
}
