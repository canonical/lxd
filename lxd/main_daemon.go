package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime/pprof"
	"sync"
	"syscall"
	"time"

	"github.com/lxc/lxd/shared"
)

func cmdDaemon() error {
	if *argCPUProfile != "" {
		f, err := os.Create(*argCPUProfile)
		if err != nil {
			fmt.Printf("Error opening cpu profile file: %s\n", err)
			return nil
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	if *argMemProfile != "" {
		go memProfiler(*argMemProfile)
	}

	neededPrograms := []string{"setfacl", "rsync", "tar", "unsquashfs", "xz"}
	for _, p := range neededPrograms {
		_, err := exec.LookPath(p)
		if err != nil {
			return err
		}
	}

	if *argPrintGoroutinesEvery > 0 {
		go func() {
			for {
				time.Sleep(time.Duration(*argPrintGoroutinesEvery) * time.Second)
				shared.PrintStack()
			}
		}()
	}

	d := &Daemon{
		group:     *argGroup,
		SetupMode: shared.PathExists(shared.VarPath(".setup_mode"))}
	err := d.Init()
	if err != nil {
		if d != nil && d.db != nil {
			d.db.Close()
		}
		return err
	}

	var ret error
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		ch := make(chan os.Signal)
		signal.Notify(ch, syscall.SIGPWR)
		sig := <-ch

		shared.LogInfof("Received '%s signal', shutting down containers.", sig)

		containersShutdown(d)

		ret = d.Stop()
		wg.Done()
	}()

	go func() {
		<-d.shutdownChan

		shared.LogInfof("Asked to shutdown by API, shutting down containers.")

		containersShutdown(d)

		ret = d.Stop()
		wg.Done()
	}()

	go func() {
		ch := make(chan os.Signal)
		signal.Notify(ch, syscall.SIGINT)
		signal.Notify(ch, syscall.SIGQUIT)
		signal.Notify(ch, syscall.SIGTERM)
		sig := <-ch

		shared.LogInfof("Received '%s signal', exiting.", sig)
		ret = d.Stop()
		wg.Done()
	}()

	wg.Wait()
	return ret
}
