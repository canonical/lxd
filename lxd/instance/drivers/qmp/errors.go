package qmp

import (
	"errors"
)

// ErrMonitorDisconnect is returned when interacting with a disconnected Monitor.
var ErrMonitorDisconnect = errors.New("Monitor is disconnected")

// ErrMonitorBadConsole is retuned when the requested console doesn't exist.
var ErrMonitorBadConsole = errors.New("Requested console couldn't be found")
