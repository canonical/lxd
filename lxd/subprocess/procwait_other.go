//go:build !linux && !windows

package subprocess

import (
	"context"
	"os"
	"syscall"
	"time"
)

// waitProcess blocks until the process identified by pid has exited, or ctx is cancelled.
// pidfd is only available on Linux, so this fallback polls the process for liveness and cannot
// determine the exit code, which is reported as -1.
func waitProcess(ctx context.Context, pid int, startTime int64) (int64, error) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return -1, nil
	}

	for {
		// Signal 0 reports whether the process is still present.
		err := proc.Signal(syscall.Signal(0))
		if err != nil {
			return -1, nil
		}

		select {
		case <-ctx.Done():
			return -1, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
