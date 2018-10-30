package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/log15/term"

	"github.com/lxc/lxd/shared/logger"
)

// GetLogger returns a logger suitable for using as logger.Log.
func GetLogger(syslog string, logfile string, verbose bool, debug bool, customHandler log.Handler) (logger.Logger, error) {
	Log := log.New()

	var handlers []log.Handler
	var syshandler log.Handler

	// System specific handler
	syshandler = getSystemHandler(syslog, debug, LogfmtFormat())
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
					log.Must.FileHandler(logfile, LogfmtFormat()),
				),
			)
		} else {
			handlers = append(handlers, log.Must.FileHandler(logfile, LogfmtFormat()))
		}
	}

	// StderrHandler
	format := LogfmtFormat()
	if term.IsTty(os.Stderr.Fd()) {
		format = TerminalFormat()
	}

	if verbose || debug {
		if !debug {
			handlers = append(
				handlers,
				log.LvlFilterHandler(
					log.LvlInfo,
					log.StreamHandler(os.Stderr, format),
				),
			)
		} else {
			handlers = append(handlers, log.StreamHandler(os.Stderr, format))
		}
	} else {
		handlers = append(
			handlers,
			log.LvlFilterHandler(
				log.LvlWarn,
				log.StreamHandler(os.Stderr, format),
			),
		)
	}

	if customHandler != nil {
		handlers = append(handlers, customHandler)
	}

	Log.SetHandler(log.MultiHandler(handlers...))

	return Log, nil
}

// SetLogger installs the given logger as global logger. It returns a function
// that can be used to restore whatever logger was installed beforehand.
func SetLogger(newLogger logger.Logger) func() {
	origLog := logger.Log
	logger.Log = newLogger
	return func() {
		logger.Log = origLog
	}
}

// WaitRecord blocks until a log.Record is received on the given channel. It
// returns the emitted record, or nil if no record was received within the
// given timeout. Useful in conjunction with log.ChannelHandler, for
// asynchronous testing.
func WaitRecord(ch chan *log.Record, timeout time.Duration) *log.Record {
	select {
	case record := <-ch:
		return record
	case <-time.After(timeout):
		return nil
	}
}

// AddContext will return a copy of the logger with extra context added
func AddContext(logger logger.Logger, ctx log.Ctx) logger.Logger {
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
