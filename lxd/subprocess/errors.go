//go:build !windows

package subprocess

import (
	"errors"
)

// ErrNotRunning is returned when performing an action against a stopped process.
var ErrNotRunning = errors.New("The process is not running")

// ErrBadPID is returned when an import file contains a facially incorrect/dangerous PID <= 0.
var ErrBadPID = errors.New("Invalid PID")

// ErrNoExitStatus is returned by Wait when an imported (non-child) process has exited but its
// exit status cannot be retrieved, as only the process's parent can reap it.
var ErrNoExitStatus = errors.New("Exit status unavailable for imported process")
