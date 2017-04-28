// +build linux darwin

package logging

import (
	log "gopkg.in/inconshreveable/log15.v2"
)

// getSystemHandler on Linux writes messages to syslog.
func getSystemHandler(syslog string, debug bool, format log.Format) log.Handler {
	// SyslogHandler
	if syslog != "" {
		if !debug {
			return log.LvlFilterHandler(
				log.LvlInfo,
				log.Must.SyslogHandler(syslog, format),
			)
		}

		return log.Must.SyslogHandler(syslog, format)
	}

	return nil
}
