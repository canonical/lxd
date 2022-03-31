package logger

import (
	"github.com/sirupsen/logrus"
)

// Ctx is the logging context.
type Ctx logrus.Fields

// Log contains the logger used by all the logging functions.
var Log *logrus.Logger

// Logger is the main logging interface.
type Logger interface {
	Debug(args ...interface{})
	Info(args ...interface{})
	Warn(args ...interface{})
	Error(args ...interface{})
	WithFields(fields logrus.Fields) *logrus.Entry
}
