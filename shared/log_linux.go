// +build linux

package shared

import (
	log "gopkg.in/inconshreveable/log15.v2"
)

// SetLogger defines the *log.Logger where log messages are sent to.
func SetLogger(syslog string, logfile string, verbose bool, debug bool) error {
	Log = log.New()

	var handlers []log.Handler

	// SyslogHandler
	if syslog != "" {
		if !debug {
			handlers = append(
				handlers,
				log.LvlFilterHandler(
					log.LvlInfo,
					log.Must.SyslogHandler(syslog, log.LogfmtFormat()),
				),
			)
		} else {
			handlers = append(handlers, log.Must.SyslogHandler(syslog, log.LogfmtFormat()))
		}
	}

	// FileHandler
	if logfile != "" {
		if !PathExists(filepath.Dir(logfile)) {
			return fmt.Errorf("Log file path doesn't exist: %s", filepath.Dir(logfile))
		}

		if !debug {
			handlers = append(
				handlers,
				log.LvlFilterHandler(
					log.LvlInfo,
					log.Must.FileHandler(logfile, log.LogfmtFormat()),
				),
			)
		} else {
			handlers = append(handlers, log.Must.FileHandler(logfile, log.LogfmtFormat()))
		}
	}

	// StderrHandler
	if verbose || debug {
		if !debug {
			handlers = append(
				handlers,
				log.LvlFilterHandler(
					log.LvlInfo,
					log.StderrHandler,
				),
			)
		} else {
			handlers = append(handlers, log.StderrHandler)
		}
	} else {
		handlers = append(
			handlers,
			log.LvlFilterHandler(
				log.LvlWarn,
				log.StderrHandler,
			),
		)
	}

	Log.SetHandler(log.MultiHandler(handlers...))

	return nil
}
