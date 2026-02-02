package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/lxd/sys"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/logger"
)

type cmdDaemon struct {
	global *cmdGlobal

	// Common options
	flagGroup string
}

func (c *cmdDaemon) command() *cobra.Command {
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
	cmd.RunE = c.run
	cmd.Flags().StringVar(&c.flagGroup, "group", "", cli.FormatStringFlagLabel("The group of users that will be allowed to talk to LXD"))

	return cmd
}

func (c *cmdDaemon) run(cmd *cobra.Command, args []string) error {
	if len(args) > 1 || (len(args) == 1 && args[0] != "daemon" && args[0] != "") {
		return fmt.Errorf("unknown command \"%s\" for \"%s\"", args[0], cmd.CommandPath())
	}

	// Only root should run this
	if os.Geteuid() != 0 {
		return errors.New("This must be run as root")
	}

	neededPrograms := []string{"ip", "rsync", "setfattr", "tar", "unsquashfs", "xz"}
	for _, p := range neededPrograms {
		_, err := exec.LookPath(p)
		if err != nil {
			return err
		}
	}

	defer logger.Info("Daemon stopped")

	conf := defaultDaemonConfig()
	conf.Group = c.flagGroup
	conf.Trace = c.global.flagLogTrace
	d := newDaemon(conf, sys.DefaultOS())

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, unix.SIGPWR)
	signal.Notify(sigCh, unix.SIGINT)
	signal.Notify(sigCh, unix.SIGQUIT)
	signal.Notify(sigCh, unix.SIGTERM)

	chIgnore := make(chan os.Signal, 1)
	signal.Notify(chIgnore, unix.SIGHUP)

	go func() {
		for {
			sig := <-sigCh
			logger.Info("Received signal", logger.Ctx{"signal": sig})
			if d.shutdownCtx.Err() != nil {
				logger.Warn("Ignoring signal, shutdown already in progress", logger.Ctx{"signal": sig})
			} else {
				go func() {
					_ = d.Stop(context.Background(), sig)
					d.shutdownDoneCh <- nil // Send nil error to cmdDaemon to ensure LXD isn't restarted by systemd.
				}()
			}
		}
	}()

	err := d.Init()
	if err != nil {
		logger.Error("Failed starting daemon", logger.Ctx{"err": err})

		// Only trigger d.Stop() and return start up error if manual shutdown hasn't been requested.
		// This avoids calling d.Stop() twice and ensures start up errors don't trigger systemd restart.
		if d.shutdownCtx.Err() == nil {
			// If an error occurred while starting up, try to cleanup any setup done so far.
			// Return the error from the failed d.Init() call so the original start up error can be
			// translated into an exit status and considered by systemd (which may restart).
			_ = d.Stop(context.Background(), unix.SIGINT)
			return err
		}
	}

	return <-d.shutdownDoneCh
}
