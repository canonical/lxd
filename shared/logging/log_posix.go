// +build linux darwin freebsd

package logging

import (
	log "github.com/lxc/lxd/shared/log15"
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
