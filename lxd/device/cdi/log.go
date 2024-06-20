package cdi

import (
	"fmt"

	"github.com/canonical/lxd/shared/logger"
)

// CDILogger reuses LXD's shared logger to log the internal operations of the CDI spec generator.
type CDILogger struct{}

// Info logs a message (with optional context) at the INFO log level.
func (l *CDILogger) Info(args ...any) {
	logger.Log.Info(fmt.Sprint(args...))
}

// Infof logs at the INFO log level using a standard printf format string.
func (l *CDILogger) Infof(format string, args ...any) {
	logger.Log.Info(fmt.Sprintf(format, args...))
}

// Warning logs a message (with optional context) at the WARNING log level.
func (l *CDILogger) Warning(args ...any) {
	logger.Log.Warn(fmt.Sprint(args...))
}

// Warningf logs at the WARNING log level using a standard printf format string.
func (l *CDILogger) Warningf(format string, args ...any) {
	logger.Log.Warn(fmt.Sprintf(format, args...))
}

// Errorf logs at the ERROR log level using a standard printf format string.
func (l *CDILogger) Errorf(format string, args ...any) {
	logger.Log.Error(fmt.Sprintf(format, args...))
}

// Debugf logs at the DEBUG log level using a standard printf format string.
func (l *CDILogger) Debugf(format string, args ...any) {
	logger.Log.Debug(fmt.Sprintf(format, args...))
}
