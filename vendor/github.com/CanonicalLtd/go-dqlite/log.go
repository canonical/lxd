package dqlite

import (
	"fmt"
	"log"
	"os"

	"github.com/CanonicalLtd/go-dqlite/internal/logging"
)

// LogFunc is a function that can be used for logging.
type LogFunc = logging.Func

// LogLevel defines the logging level.
type LogLevel = logging.Level

// Available logging levels.
const (
	LogDebug = logging.Debug
	LogInfo  = logging.Info
	LogWarn  = logging.Warn
	LogError = logging.Error
)

// Create a LogFunc with reasonable defaults.
func defaultLogFunc() LogFunc {
	logger := log.New(os.Stdout, "", log.LstdFlags|log.Lmicroseconds)

	return func(l LogLevel, format string, a ...interface{}) {
		format = fmt.Sprintf("[%s]: %s", l.String(), format)
		logger.Printf(format, a...)
	}
}
