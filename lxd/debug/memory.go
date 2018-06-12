package debug

import (
	"os"
	"os/signal"
	"runtime/pprof"
	"syscall"

	"github.com/lxc/lxd/shared/logger"
	"golang.org/x/net/context"
)

// Memory is a debug activity that perpetually watches for SIGUSR1 signals and
// dumps the memory to the given file whenever the signal is received.
//
// If the given filename is the empty string, no profiler is started.
func Memory(filename string) Activity {
	return func() (activityFunc, error) {
		if filename == "" {
			return nil, nil
		}

		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGUSR1)

		f := func(ctx context.Context) {
			memoryWatcher(ctx, signals, filename)
			signal.Stop(signals)
		}

		return f, nil
	}
}

// Watch for SIGUSR1 and trigger memoryDump().
func memoryWatcher(ctx context.Context, signals <-chan os.Signal, filename string) {
	for {
		select {
		case sig := <-signals:
			logger.Debugf("Received '%s signal', dumping memory.", sig)
			memoryDump(filename)
		case <-ctx.Done():
			logger.Debugf("Shutdown memory profiler.")
			return
		}
	}
}

// Dump the current memory info to the given file.
func memoryDump(filename string) {
	f, err := os.Create(filename)
	if err != nil {
		logger.Debugf("Error opening memory profile file '%s': %s", filename, err)
		return
	}
	pprof.WriteHeapProfile(f)
	f.Close()
}
