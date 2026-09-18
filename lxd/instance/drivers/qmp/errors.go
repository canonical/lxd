package qmp

import (
	"errors"
)

// ErrMonitorDisconnect is returned when interacting with a disconnected Monitor.
var ErrMonitorDisconnect = errors.New("Monitor is disconnected")

// ErrMonitorTimeout is returned when a command gets no reply in time (e.g. unresponsive QEMU).
var ErrMonitorTimeout = errors.New("Monitor command timed out")

// ErrMonitorBadConsole is retuned when the requested console doesn't exist.
var ErrMonitorBadConsole = errors.New("Requested console could not be found")
