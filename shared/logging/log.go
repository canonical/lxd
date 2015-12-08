package shared

import (
	"fmt"
	"path/filepath"
	"runtime"

	log "gopkg.in/inconshreveable/log15.v2"
)

// Logger is the log15 Logger we use everywhere.
var LxdLog log.Logger
var debug bool

func Log(lvl string, msg string, ctx ...interface{}) {
	switch lvl {
	case "debug":
		LxdLog.Debug(msg, ctx)
	case "info":
		LxdLog.Info(msg, ctx)
	case "warn":
		LxdLog.Warn(msg, ctx)
	case "error":
		LxdLog.Error(msg, ctx)
	case "crit":
		LxdLog.Crit(msg, ctx)
	}
}

// SetLogger defines the *log.Logger where log messages are sent to.
func SetLogger(syslog string, logfile string, verbose bool, debug bool, customHandler log.Handler) error {
	LxdLog = log.New()

	var handlers []log.Handler

	var syshandler log.Handler

	// System specific handler
	syshandler = getSystemHandler(syslog, debug)
	if syshandler != nil {
		handlers = append(handlers, syshandler)
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

	if customHandler != nil {
		handlers = append(handlers, customHandler)
	}

	Log.SetHandler(log.MultiHandler(handlers...))

	return nil
}

// Logf sends to the logger registered via SetLogger the string resulting
// from running format and args through Sprintf.
func Logf(format string, args ...interface{}) {
	if Log != nil {
		Log.Info(fmt.Sprintf(format, args...))
	}
}

// Debugf sends to the logger registered via SetLogger the string resulting
// from running format and args through Sprintf, but only if debugging was
// enabled via SetDebug.
func Debugf(format string, args ...interface{}) {
	if Log != nil {
		Log.Debug(fmt.Sprintf(format, args...))
	}
}

func PrintStack() {
	buf := make([]byte, 1<<16)
	runtime.Stack(buf, true)
	Debugf("%s", buf)
}
