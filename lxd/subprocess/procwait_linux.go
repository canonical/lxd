//go:build linux

package subprocess

import (
	"context"
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// pidfdInfo mirrors the kernel struct pidfd_info used by the PIDFD_GET_INFO ioctl.
type pidfdInfo struct {
	mask           uint64
	cgroupid       uint64
	pid            uint32
	tgid           uint32
	ppid           uint32
	ruid           uint32
	rgid           uint32
	euid           uint32
	egid           uint32
	suid           uint32
	sgid           uint32
	fsuid          uint32
	fsgid          uint32
	exitCode       int32
	coredumpMask   uint32
	coredumpSignal uint32
	coredumpCode   uint32
	coredumpPad    uint32
	supportedMask  uint64
}

const (
	// pidfdInfoExit requests exit information from PIDFD_GET_INFO (Linux 6.15+).
	pidfdInfoExit = 1 << 3

	// pidfsIoctlMagic and iocDirWriteRead build the PIDFD_GET_INFO request number
	// (_IOWR(0xFF, 11, struct pidfd_info)).
	pidfsIoctlMagic = 0xff
	iocDirWriteRead = 3
)

// waitProcess blocks until the process identified by pid has exited, or ctx is cancelled.
// It uses a pidfd so it can wait on a process that is not a child of the current process
// (for example one imported from a PID file). The startTime is used to guard against PID reuse.
// On kernels that support PIDFD_GET_INFO it returns the process exit code, otherwise -1.
func waitProcess(ctx context.Context, pid int, startTime int64) (int64, error) {
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
			return pidfdExitCode(pidFd), nil
		}

		if ctx.Err() != nil {
			return -1, ctx.Err()
		}
	}
}

// pidfdExitCode returns the exit code of the exited process referenced by pidFd using the
// PIDFD_GET_INFO ioctl, or -1 if the kernel does not support it or the process was signalled.
func pidfdExitCode(pidFd int) int64 {
	info := pidfdInfo{mask: pidfdInfoExit}
	req := (uintptr(iocDirWriteRead) << 30) | (unsafe.Sizeof(info) << 16) | (uintptr(pidfsIoctlMagic) << 8) | 11

	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(pidFd), req, uintptr(unsafe.Pointer(&info)))
	if errno != 0 {
		return -1
	}

	// The kernel clears the requested bit if it could not provide the information.
	if info.mask&pidfdInfoExit == 0 {
		return -1
	}

	waitStatus := syscall.WaitStatus(info.exitCode)
	if !waitStatus.Exited() {
		return -1
	}

	return int64(waitStatus.ExitStatus())
}
