package main

import (
	"os"
	"os/signal"
	"runtime/pprof"
	"syscall"

	"github.com/lxc/lxd/shared"
)

func doMemDump(memProfile string) {
	f, err := os.Create(memProfile)
	if err != nil {
		shared.Debugf("Error opening memory profile file '%s': %s", err)
		return
	}
	pprof.WriteHeapProfile(f)
	f.Close()
}

func memProfiler(memProfile string) {
	ch := make(chan os.Signal)
	signal.Notify(ch, syscall.SIGUSR1)
	for {
		sig := <-ch
		shared.Debugf("Received '%s signal', dumping memory.", sig)
		doMemDump(memProfile)
	}
}
