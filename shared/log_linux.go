// +build linux

package shared

import (
	log "gopkg.in/inconshreveable/log15.v2"
)

// GetSystemHandler on Linux writes messages to syslog.
func GetSystemHandler(syslog string, debug bool) (log.Handler) {
	// SyslogHandler
	if syslog != "" {
		if !debug {
			return log.LvlFilterHandler(
				log.LvlInfo,
				log.Must.SyslogHandler(syslog, log.LogfmtFormat()),
			)
		} else {
			return log.Must.SyslogHandler(syslog, log.LogfmtFormat())
		}
	}

	return nil
}
