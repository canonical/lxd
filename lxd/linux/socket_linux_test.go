package linux

import (
	"errors"
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestPidfdGetExitInfo(t *testing.T) {
	// Fork a child process that exits immediately with status 42
	proc, err := os.StartProcess("/bin/sh", []string{"sh", "-c", "exit 42"}, &os.ProcAttr{})
	require.NoError(t, err)

	pidfd, err := PidFdOpen(proc.Pid, 0)
	if err != nil {
		t.Skipf("PidFdOpen not supported: %v", err)
	}

	defer func() { _ = pidfd.Close() }()

	// Wait for child to exit
	state, err := proc.Wait()
	require.NoError(t, err)
	assert.Equal(t, 42, state.ExitCode())

	// Call PidfdGetExitInfo
	exitCode, hasExitCode, err := PidfdGetExitInfo(int(pidfd.Fd()))
	if err != nil {
		if errors.Is(err, unix.ENOTTY) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOSYS) {
			t.Skipf("PIDFD_GET_INFO ioctl not supported by running kernel: %v", err)
		}

		t.Fatalf("Unexpected error from PidfdGetExitInfo: %v", err)
	}

	assert.True(t, hasExitCode)
	waitStatus := syscall.WaitStatus(exitCode)
	assert.True(t, waitStatus.Exited())
	assert.Equal(t, 42, waitStatus.ExitStatus())
}
