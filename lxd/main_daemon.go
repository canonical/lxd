package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	dbg "github.com/lxc/lxd/lxd/debug"
	"github.com/lxc/lxd/lxd/sys"
	"github.com/lxc/lxd/shared/logger"
)

func cmdDaemon(args *Args) error {
	// Only root should run this
	if os.Geteuid() != 0 {
		return fmt.Errorf("This must be run as root")
	}

	// Start debug activities as per command line flags, if any.
	stop, err := dbg.Start(
		dbg.CPU(args.CPUProfile),
		dbg.Memory(args.MemProfile),
		dbg.Goroutines(args.PrintGoroutinesEvery),
	)
	defer stop()
	if err != nil {
		fmt.Printf("%v\n", err)
		return nil
	}

	neededPrograms := []string{"setfacl", "rsync", "tar", "unsquashfs", "xz"}
	for _, p := range neededPrograms {
		_, err := exec.LookPath(p)
		if err != nil {
			return err
		}

	}
	c := &DaemonConfig{
		Group: args.Group,
	}
	d := NewDaemon(c, sys.DefaultOS())
	err = d.Init()
	if err != nil {
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
		containersShutdown(s)
		networkShutdown(s)
	}

	return d.Stop()
}
