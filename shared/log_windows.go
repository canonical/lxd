// +build windows

package shared

import (
	log "gopkg.in/inconshreveable/log15.v2"
)

// GetSystemHandler on Windows does nothing.
func GetSystemHandler(syslog string, debug bool) (log.Handler) {
	return nil
}
