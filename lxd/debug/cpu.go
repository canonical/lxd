package debug

import (
	"fmt"
	"os"
	"runtime/pprof"

	"golang.org/x/net/context"
)

// CPU starts the Go CPU profiler, dumping to the given file.
func CPU(filename string) Activity {
	return func() (activityFunc, error) {
		if filename == "" {
			return nil, nil
		}

		file, err := os.Create(filename)
		if err != nil {
			return nil, fmt.Errorf("Error opening cpu profile file: %v", err)
		}

		pprof.StartCPUProfile(file)
		f := func(ctx context.Context) {
			<-ctx.Done()
			pprof.StopCPUProfile()
		}
		return f, nil
	}
}
