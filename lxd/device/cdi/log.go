package cdi

import (
	"fmt"

	"github.com/canonical/lxd/shared/logger"
)

// CDILogger reuses LXD's shared logger to log the internal operations of the CDI spec generator.
type CDILogger struct {
	lxdLogger logger.Logger
}

// NewCDILogger creates a new CDI logger from a LXD logger instance.
func NewCDILogger(l logger.Logger) *CDILogger {
	return &CDILogger{lxdLogger: l}
}

// Info logs a message (with optional context) at the INFO log level.
func (l *CDILogger) Info(args ...any) {
	l.lxdLogger.Info(fmt.Sprint(args...))
}

// Infof logs at the INFO log level using a standard printf format string.
func (l *CDILogger) Infof(format string, args ...any) {
	l.lxdLogger.Info(fmt.Sprintf(format, args...))
}

// Warning logs a message (with optional context) at the WARNING log level.
func (l *CDILogger) Warning(args ...any) {
	l.lxdLogger.Warn(fmt.Sprint(args...))
}

// Warningf logs at the WARNING log level using a standard printf format string.
func (l *CDILogger) Warningf(format string, args ...any) {
	l.lxdLogger.Warn(fmt.Sprintf(format, args...))
}

// Errorf logs at the ERROR log level using a standard printf format string.
func (l *CDILogger) Errorf(format string, args ...any) {
	l.lxdLogger.Error(fmt.Sprintf(format, args...))
}

// Debugf logs at the DEBUG log level using a standard printf format string.
func (l *CDILogger) Debugf(format string, args ...any) {
	l.lxdLogger.Debug(fmt.Sprintf(format, args...))
}

// Tracef logs at the TRACE log level using a standard printf format string.
func (l *CDILogger) Tracef(format string, args ...any) {
	l.lxdLogger.Trace(fmt.Sprintf(format, args...))
}
