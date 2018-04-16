package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	dbg "github.com/lxc/lxd/lxd/debug"
	"github.com/lxc/lxd/lxd/sys"
	"github.com/lxc/lxd/shared/logger"
)

type cmdDaemon struct {
	global *cmdGlobal

	// Common options
	flagGroup string

	// Debug options
	flagCPUProfile      string
	flagMemoryProfile   string
	flagPrintGoroutines int
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
	cmd.Flags().StringVar(&c.flagCPUProfile, "cpu-profile", "", "Enable CPU profiling, writing into the specified file"+"``")
	cmd.Flags().StringVar(&c.flagMemoryProfile, "memory-profile", "", "Enable memory profiling, writing into the specified file"+"``")
	cmd.Flags().IntVar(&c.flagPrintGoroutines, "print-goroutines", 0, "How often to print all the goroutines"+"``")

	return cmd
}

func (c *cmdDaemon) Run(cmd *cobra.Command, args []string) error {
	// Only root should run this
	if os.Geteuid() != 0 {
		return fmt.Errorf("This must be run as root")
	}

	// Start debug activities as per command line flags, if any.
	stop, err := dbg.Start(
		dbg.CPU(c.flagCPUProfile),
		dbg.Memory(c.flagMemoryProfile),
		dbg.Goroutines(c.flagPrintGoroutines),
	)
	if err != nil {
		return err
	}

	defer stop()

	neededPrograms := []string{"setfacl", "rsync", "tar", "unsquashfs", "xz"}
	for _, p := range neededPrograms {
		_, err := exec.LookPath(p)
		if err != nil {
			return err
		}
	}

	conf := DefaultDaemonConfig()
	conf.Group = c.flagGroup
	conf.Trace = c.global.flagLogTrace
	d := NewDaemon(conf, sys.DefaultOS())

	err = d.Init()
	if err != nil {
		logger.Errorf("Daemon failed to start: %v", err)
		return err
	}

	ch := make(chan os.Signal)
	signal.Notify(ch, syscall.SIGPWR)
	signal.Notify(ch, syscall.SIGINT)
	signal.Notify(ch, syscall.SIGQUIT)
	signal.Notify(ch, syscall.SIGTERM)

	s := d.State()
	select {
	case sig := <-ch:
		if sig == syscall.SIGPWR {
			logger.Infof("Received '%s signal', shutting down containers.", sig)
			containersShutdown(s)
			networkShutdown(s)
		} else {
			logger.Infof("Received '%s signal', exiting.", sig)
		}

	case <-d.shutdownChan:
		logger.Infof("Asked to shutdown by API, shutting down containers.")
		d.Kill()
		containersShutdown(s)
		networkShutdown(s)
	}

	return d.Stop()
}
