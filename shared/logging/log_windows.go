// +build windows

package logging

import (
	log "github.com/lxc/lxd/shared/log15"
)

// getSystemHandler on Windows does nothing.
func getSystemHandler(syslog string, debug bool, format log.Format) log.Handler {
	return nil
}
