//go:build linux || darwin || freebsd
// +build linux darwin freebsd

package logging

import (
	slog "log/syslog"

	log "gopkg.in/inconshreveable/log15.v2"
)

// getSystemHandler on Linux writes messages to syslog.
func getSystemHandler(syslog string, debug bool, format log.Format) log.Handler {
	// SyslogHandler
	if syslog != "" {
		if !debug {
			return log.LvlFilterHandler(
				log.LvlInfo,
				log.Must.SyslogHandler(slog.LOG_INFO, syslog, format),
			)
		}

		return log.Must.SyslogHandler(slog.LOG_INFO, syslog, format)
	}

	return nil
}
