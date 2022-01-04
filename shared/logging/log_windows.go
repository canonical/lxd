//go:build windows
// +build windows

package logging

import (
	log "gopkg.in/inconshreveable/log15.v2"
)

// getSystemHandler on Windows does nothing.
func getSystemHandler(syslog string, debug bool, format log.Format) log.Handler {
	return nil
}
