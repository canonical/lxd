// +build !logdebug

package logger

import (
	"fmt"
	"runtime"
)

type Logger interface {
	Debug(msg string, ctx ...interface{})
	Info(msg string, ctx ...interface{})
	Warn(msg string, ctx ...interface{})
	Error(msg string, ctx ...interface{})
	Crit(msg string, ctx ...interface{})
}

var Log Logger

type nullLogger struct{}

func (nl nullLogger) Debug(msg string, ctx ...interface{}) {}
func (nl nullLogger) Info(msg string, ctx ...interface{})  {}
func (nl nullLogger) Warn(msg string, ctx ...interface{})  {}
func (nl nullLogger) Error(msg string, ctx ...interface{}) {}
func (nl nullLogger) Crit(msg string, ctx ...interface{})  {}

func init() {
	Log = nullLogger{}
}

// General wrappers around Logger interface functions.
func Debug(msg string, ctx ...interface{}) {
	if Log != nil {
		Log.Debug(msg, ctx...)
	}
}

func Info(msg string, ctx ...interface{}) {
	if Log != nil {
		Log.Info(msg, ctx...)
	}
}

func Warn(msg string, ctx ...interface{}) {
	if Log != nil {
		Log.Warn(msg, ctx...)
	}
}

func Error(msg string, ctx ...interface{}) {
	if Log != nil {
		Log.Error(msg, ctx...)
	}
}

func Crit(msg string, ctx ...interface{}) {
	if Log != nil {
		Log.Crit(msg, ctx...)
	}
}

// Wrappers around Logger interface functions that send a string to the Logger
// by running it through fmt.Sprintf().
func Infof(format string, args ...interface{}) {
	if Log != nil {
		Log.Info(fmt.Sprintf(format, args...))
	}
}

func Debugf(format string, args ...interface{}) {
	if Log != nil {
		Log.Debug(fmt.Sprintf(format, args...))
	}
}

func Warnf(format string, args ...interface{}) {
	if Log != nil {
		Log.Warn(fmt.Sprintf(format, args...))
	}
}

func Errorf(format string, args ...interface{}) {
	if Log != nil {
		Log.Error(fmt.Sprintf(format, args...))
	}
}

func Critf(format string, args ...interface{}) {
	if Log != nil {
		Log.Crit(fmt.Sprintf(format, args...))
	}
}

func PrintStack() {
	buf := make([]byte, 1<<16)
	runtime.Stack(buf, true)
	Errorf("%s", buf)
}
