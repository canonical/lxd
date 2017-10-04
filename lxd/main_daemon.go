package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime/pprof"
	"syscall"
	"time"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
)

func cmdDaemon(args *Args) error {
	// Only root should run this
	if os.Geteuid() != 0 {
		return fmt.Errorf("This must be run as root")
	}

	if args.CPUProfile != "" {
		f, err := os.Create(args.CPUProfile)
		if err != nil {
			fmt.Printf("Error opening cpu profile file: %s\n", err)
			return nil
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	if args.MemProfile != "" {
		go memProfiler(args.MemProfile)
	}

	neededPrograms := []string{"setfacl", "rsync", "tar", "unsquashfs", "xz"}
	for _, p := range neededPrograms {
		_, err := exec.LookPath(p)
		if err != nil {
			return err
		}
	}

	if args.PrintGoroutinesEvery > 0 {
		go func() {
			for {
				time.Sleep(time.Duration(args.PrintGoroutinesEvery) * time.Second)
				logger.Debugf(logger.GetStack())
			}
		}()
	}

	d := NewDaemon()
	d.group = args.Group
	d.SetupMode = shared.PathExists(shared.VarPath(".setup_mode"))
	err := d.Init()
	if err != nil {
		if d != nil && d.db != nil {
			d.db.Close()
		}
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
			containersShutdown(s, d.Storage)
		} else {
			logger.Infof("Received '%s signal', exiting.", sig)
		}

	case <-d.shutdownChan:
		logger.Infof("Asked to shutdown by API, shutting down containers.")
		containersShutdown(s, d.Storage)
	}

	return d.Stop()
}
