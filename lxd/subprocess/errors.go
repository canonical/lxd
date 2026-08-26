//go:build !windows

package subprocess

import (
	"errors"
)

// ErrNotRunning is returned when performing an action against a stopped process.
var ErrNotRunning = errors.New("The process is not running")

// ErrAlreadyRunning is returned when trying to start a process that is already running.
var ErrAlreadyRunning = errors.New("The process is already running")

// ErrBadPID is returned when an import file contains a facially incorrect/dangerous PID <= 0.
var ErrBadPID = errors.New("Invalid PID")
