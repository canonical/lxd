package logger

import (
	"github.com/sirupsen/logrus"
)

// ctxLogger returns a logger target with all provided ctx applied.
func (lw *logWrapper) ctxLogger(ctx ...Ctx) targetLogger {
	logger := lw.target
	for _, c := range ctx {
		logger = logger.WithFields(logrus.Fields(c))
	}

	return logger
}

func newWrapper(target targetLogger) Logger {
	return &logWrapper{target}
}

type logWrapper struct {
	target targetLogger
}

// Panic logs a panic level error message.
func (lw *logWrapper) Panic(msg string, ctx ...Ctx) {
	lw.ctxLogger(ctx...).Panic(msg)
}

// Fatal logs a fatal error message.
func (lw *logWrapper) Fatal(msg string, ctx ...Ctx) {
	lw.ctxLogger(ctx...).Fatal(msg)
}

// Error logs an error message.
func (lw *logWrapper) Error(msg string, ctx ...Ctx) {
	lw.ctxLogger(ctx...).Error(msg)
}

// Warn logs a warning message.
func (lw *logWrapper) Warn(msg string, ctx ...Ctx) {
	lw.ctxLogger(ctx...).Warn(msg)
}

// Info logs an informational message.
func (lw *logWrapper) Info(msg string, ctx ...Ctx) {
	lw.ctxLogger(ctx...).Info(msg)
}

// Debug logs a debug message.
func (lw *logWrapper) Debug(msg string, ctx ...Ctx) {
	lw.ctxLogger(ctx...).Debug(msg)
}

// Trace logs a trace message.
func (lw *logWrapper) Trace(msg string, ctx ...Ctx) {
	lw.ctxLogger(ctx...).Trace(msg)
}

// AddContext returns a new Logger with the provided context added.
func (lw *logWrapper) AddContext(ctx Ctx) Logger {
	return &logWrapper{lw.ctxLogger(ctx)}
}
