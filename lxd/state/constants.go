package state

import (
	"os"
)

// LogFilePermissions defines the permissions for log files created by LXD.
// Log files are only read by the LXD daemon running as root, so no group or other access is needed.
const LogFilePermissions os.FileMode = 0600
