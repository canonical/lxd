package logger

import (
	"os"
	"sync"

	"github.com/sirupsen/logrus"
)

// SafeLogger is a thread-safe logger
type SafeLogger struct {
	logger *logrus.Logger
	mu     sync.Mutex
}

// NewSafeLogger creates a new thread-safe logger
func NewSafeLogger(filename string) (*SafeLogger, error) {
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return nil, err
	}

	logger := logrus.New()
	logger.SetOutput(file)
	logger.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})

	return &SafeLogger{
		logger: logger,
	}, nil
}

// Log logs a message with the given level and fields
func (sl *SafeLogger) Log(level logrus.Level, msg string, fields logrus.Fields) {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	entry := sl.logger.WithFields(fields)
	switch level {
	case logrus.DebugLevel:
		entry.Debug(msg)
	case logrus.InfoLevel:
		entry.Info(msg)
	case logrus.WarnLevel:
		entry.Warn(msg)
	case logrus.ErrorLevel:
		entry.Error(msg)
	case logrus.FatalLevel:
		entry.Fatal(msg)
	case logrus.PanicLevel:
		entry.Panic(msg)
	}
}

// Helper methods for different log levels
func (sl *SafeLogger) Debug(msg string, fields logrus.Fields) {
	sl.Log(logrus.DebugLevel, msg, fields)
}

func (sl *SafeLogger) Info(msg string, fields logrus.Fields) {
	sl.Log(logrus.InfoLevel, msg, fields)
}

func (sl *SafeLogger) Warn(msg string, fields logrus.Fields) {
	sl.Log(logrus.WarnLevel, msg, fields)
}

func (sl *SafeLogger) Error(msg string, fields logrus.Fields) {
	sl.Log(logrus.ErrorLevel, msg, fields)
}
