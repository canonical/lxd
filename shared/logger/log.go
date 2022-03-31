package logger

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"

	"github.com/sirupsen/logrus"
	lWriter "github.com/sirupsen/logrus/hooks/writer"

	"github.com/lxc/lxd/shared/termios"
)

// Setup a basic empty logger on init.
func init() {
	logger := logrus.StandardLogger()
	logger.SetOutput(ioutil.Discard)

	Log = logger
}

// InitLogger intializes a full logging instance.
func InitLogger(filepath string, syslogName string, verbose bool, debug bool, hook logrus.Hook) error {
	logger := logrus.StandardLogger()
	logger.Level = logrus.DebugLevel
	logger.SetOutput(io.Discard)

	// Setup the formatter.
	logger.Formatter = &logrus.TextFormatter{FullTimestamp: true, ForceColors: termios.IsTerminal(int(os.Stderr.Fd()))}

	// Setup log level.
	levels := []logrus.Level{logrus.PanicLevel, logrus.FatalLevel, logrus.ErrorLevel, logrus.WarnLevel}
	if debug {
		levels = append(levels, logrus.InfoLevel, logrus.DebugLevel)
	} else if verbose {
		levels = append(levels, logrus.InfoLevel)
	}

	// Setup writers.
	writers := []io.Writer{os.Stderr}

	if filepath != "" {
		f, err := os.OpenFile(filepath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
		if err != nil {
			return err
		}

		writers = append(writers, f)
	}

	logger.AddHook(&lWriter.Hook{
		Writer:    io.MultiWriter(writers...),
		LogLevels: levels,
	})

	// Setup syslog.
	if syslogName != "" {
		err := setupSyslog(logger, syslogName)
		if err != nil {
			return err
		}
	}

	// Add hooks.
	if hook != nil {
		logger.AddHook(hook)
	}

	// Set the logger.
	Log = logger

	return nil
}

// Debug logs a message (with optional context) at the DEBUG log level
func Debug(msg string, ctx ...Ctx) {
	var logCtx Ctx
	if len(ctx) > 0 {
		logCtx = ctx[0]
	}

	Log.Debug(msg, logCtx)
}

// Info logs a message (with optional context) at the INFO log level
func Info(msg string, ctx ...Ctx) {
	var logCtx Ctx
	if len(ctx) > 0 {
		logCtx = ctx[0]
	}

	Log.Info(msg, logCtx)
}

// Warn logs a message (with optional context) at the WARNING log level
func Warn(msg string, ctx ...Ctx) {
	var logCtx Ctx
	if len(ctx) > 0 {
		logCtx = ctx[0]
	}

	Log.Warn(msg, logCtx)
}

// Error logs a message (with optional context) at the ERROR log level
func Error(msg string, ctx ...Ctx) {
	var logCtx Ctx
	if len(ctx) > 0 {
		logCtx = ctx[0]
	}

	Log.Error(msg, logCtx)
}

// Infof logs at the INFO log level using a standard printf format string
func Infof(format string, args ...any) {
	if Log != nil {
		Log.Info(fmt.Sprintf(format, args...))
	}
}

// Debugf logs at the DEBUG log level using a standard printf format string
func Debugf(format string, args ...any) {
	if Log != nil {
		Log.Debug(fmt.Sprintf(format, args...))
	}
}

// Warnf logs at the WARNING log level using a standard printf format string
func Warnf(format string, args ...any) {
	if Log != nil {
		Log.Warn(fmt.Sprintf(format, args...))
	}
}

// Errorf logs at the ERROR log level using a standard printf format string
func Errorf(format string, args ...any) {
	if Log != nil {
		Log.Error(fmt.Sprintf(format, args...))
	}
}

// AddContext returns a new logger with the context added.
func AddContext(logger Logger, ctx Ctx) *logrus.Entry {
	return logger.WithFields(logrus.Fields(ctx))
}
