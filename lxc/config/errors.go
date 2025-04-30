package config

import (
	"errors"
)

// ErrNotLinux is returned when attemping to access the "local" remote on non-Linux systems.
var ErrNotLinux = errors.New("Can't connect to a local LXD server on a non-Linux system")
