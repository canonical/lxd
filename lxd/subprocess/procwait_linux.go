//go:build linux && cgo

package subprocess

/*
#include <linux/ioctl.h>
#include <stdint.h>

// pidfd_info_ioctl mirrors the kernel struct pidfd_info used by the
// PIDFD_GET_INFO ioctl. It is defined here so the ioctl request number can be
// built with the architecture-specific _IOWR macro, avoiding the asm-generic
// shift assumptions that break on PowerPC and MIPS.
struct pidfd_info_ioctl {
	uint64_t mask;
	uint64_t cgroupid;
	uint32_t pid;
	uint32_t tgid;
	uint32_t ppid;
	uint32_t ruid;
	uint32_t rgid;
	uint32_t euid;
	uint32_t egid;
	uint32_t suid;
	uint32_t sgid;
	uint32_t fsuid;
	uint32_t fsgid;
	int32_t  exit_code;
	uint32_t coredump_mask;
	uint32_t coredump_signal;
	uint32_t coredump_code;
	uint32_t coredump_pad;
	uint64_t supported_mask;
};

#define PIDFD_GET_INFO_IOCTL _IOWR(0xFF, 11, struct pidfd_info_ioctl)
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	// pidfdInfoExit requests exit information from PIDFD_GET_INFO (Linux 6.15+).
	pidfdInfoExit = 1 << 3
)

// waitProcess blocks until the process identified by pid has exited, or ctx is cancelled.
// It uses a pidfd so it can wait on a process that is not a child of the current process
// (for example one imported from a PID file). The startTime is used to guard against PID reuse.
// On kernels that support PIDFD_GET_INFO it returns the process exit code, otherwise -1.
//
// If pidfd monitoring is unavailable or fails, waitProcess falls back to polling the process
// with signal 0 so that callers still do not return until the process has exited.
func waitProcess(ctx context.Context, pid int, startTime int64) (int64, error) {
	code, err := waitProcessPidfd(ctx, pid, startTime)
	if err == nil {
		return code, nil
	}

	// pidfd_open or pidfd polling failed; preserve the wait guarantee by falling
	// back to signal-0 polling.
	return waitProcessSignal0(ctx, pid, startTime)
}

// waitProcessPidfd waits for the process using a pidfd and PIDFD_GET_INFO.
func waitProcessPidfd(ctx context.Context, pid int, startTime int64) (int64, error) {
	pidFd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		// ESRCH means the process is already gone.
		if errors.Is(err, unix.ESRCH) {
			return -1, nil
		}

		return -1, fmt.Errorf("Failed opening pidfd for PID %d: %w", pid, err)
	}

	defer func() { _ = unix.Close(pidFd) }()

	// Guard against PID reuse: if the recorded start time no longer matches, the original
	// process has already exited and a new one has reused its PID.
	if startTime != 0 {
		currentStartTime, err := processStartTime(pid)
		if err != nil || currentStartTime != startTime {
			return -1, nil
		}
	}

	// The pidfd becomes readable once the process exits.
	pollFds := []unix.PollFd{{Fd: int32(pidFd), Events: unix.POLLIN}}
	for {
		// Poll with a bounded timeout so context cancellation can be observed.
		n, err := unix.Poll(pollFds, 100)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}

			return -1, fmt.Errorf("Failed polling pidfd for PID %d: %w", pid, err)
		}

		if n > 0 {
			revents := pollFds[0].Revents
			if revents&(unix.POLLERR|unix.POLLNVAL) != 0 {
				return -1, fmt.Errorf("pidfd for PID %d reported error events: %d", pid, revents)
			}

			if revents&unix.POLLIN != 0 {
				return pidfdExitCode(pidFd), nil
			}

			// POLLHUP or other unexpected events are ignored and polling continues.
			continue
		}

		if ctx.Err() != nil {
			return -1, ctx.Err()
		}
	}
}

// waitProcessSignal0 waits for the process by polling with signal 0.
func waitProcessSignal0(ctx context.Context, pid int, startTime int64) (int64, error) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return -1, nil
	}

	for {
		err := proc.Signal(syscall.Signal(0))
		if err != nil {
			return -1, nil
		}

		// Guard against PID reuse while polling.
		if startTime != 0 {
			currentStartTime, err := processStartTime(pid)
			if err != nil || currentStartTime != startTime {
				return -1, nil
			}
		}

		select {
		case <-ctx.Done():
			return -1, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// pidfdExitCode returns the exit code of the exited process referenced by pidFd using the
// PIDFD_GET_INFO ioctl, or -1 if the kernel does not support it or the process was signalled.
func pidfdExitCode(pidFd int) int64 {
	var info C.struct_pidfd_info_ioctl
	info.mask = C.uint64_t(pidfdInfoExit)

	req := uintptr(C.PIDFD_GET_INFO_IOCTL)
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(pidFd), req, uintptr(unsafe.Pointer(&info)))
	if errno != 0 {
		return -1
	}

	// The kernel clears the requested bit if it could not provide the information.
	if info.mask&C.uint64_t(pidfdInfoExit) == 0 {
		return -1
	}

	waitStatus := syscall.WaitStatus(uint32(info.exit_code))
	if !waitStatus.Exited() {
		return -1
	}

	return int64(waitStatus.ExitStatus())
}
