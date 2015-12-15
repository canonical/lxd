package logging

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/lxc/lxd/shared"

	log "gopkg.in/inconshreveable/log15.v2"
)

// GetLogger returns a logger suitable for using as shared.Log.
func GetLogger(syslog string, logfile string, verbose bool, debug bool, customHandler log.Handler) (shared.Logger, error) {
	Log := log.New()

	var handlers []log.Handler

	var syshandler log.Handler

	// System specific handler
	syshandler = getSystemHandler(syslog, debug)
	if syshandler != nil {
		handlers = append(handlers, syshandler)
	}

	// FileHandler
	if logfile != "" {
		if !pathExists(filepath.Dir(logfile)) {
			return nil, fmt.Errorf("Log file path doesn't exist: %s", filepath.Dir(logfile))
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

	if customHandler != nil {
		handlers = append(handlers, customHandler)
	}

	Log.SetHandler(log.MultiHandler(handlers...))

	return Log, nil
}

func AddContext(logger shared.Logger, ctx log.Ctx) shared.Logger {
	log15logger, ok := logger.(log.Logger)
	if !ok {
		logger.Error("couldn't downcast logger to add context", log.Ctx{"logger": log15logger, "ctx": ctx})
		return logger
	}

	return log15logger.New(ctx)
}

func pathExists(name string) bool {
	_, err := os.Lstat(name)
	if err != nil && os.IsNotExist(err) {
		return false
	}
	return true
}
