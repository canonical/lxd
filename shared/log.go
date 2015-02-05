package shared

import (
	"fmt"
)

// Logger is implemented by the standard *log.Logger.
type Logger interface {
	Output(calldepth int, s string) error
}

var logger Logger
var debug bool

// SetLogger defines the *log.Logger where log messages are sent to.
func SetLogger(l Logger) {
	logger = l
}

// SetDebug defines whether debugging is enabled or not.
func SetDebug(enabled bool) {
	debug = enabled
}

// Logf sends to the logger registered via SetLogger the string resulting
// from running format and args through Sprintf.
func Logf(format string, args ...interface{}) {
	if logger != nil {
		logger.Output(2, fmt.Sprintf(format, args...))
	}
}

// Debugf sends to the logger registered via SetLogger the string resulting
// from running format and args through Sprintf, but only if debugging was
// enabled via SetDebug.
func Debugf(format string, args ...interface{}) {
	if debug && logger != nil {
		logger.Output(2, fmt.Sprintf(format, args...))
	}
}
