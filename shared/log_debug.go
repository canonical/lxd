// +build logdebug

package shared

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
func LogDebug(msg string, ctx interface{}) {
	if Log != nil {
		pc, fn, line, _ := runtime.Caller(1)
		msg := fmt.Sprintf("%s: %d: %s: %s", fn, line, runtime.FuncForPC(pc).Name(), msg)
		Log.Debug(msg, ctx)
	}
}

func LogInfo(msg string, ctx interface{}) {
	if Log != nil {
		pc, fn, line, _ := runtime.Caller(1)
		msg := fmt.Sprintf("%s: %d: %s: %s", fn, line, runtime.FuncForPC(pc).Name(), msg)
		Log.Info(msg, ctx)
	}
}

func LogWarn(msg string, ctx interface{}) {
	if Log != nil {
		pc, fn, line, _ := runtime.Caller(1)
		msg := fmt.Sprintf("%s: %d: %s: %s", fn, line, runtime.FuncForPC(pc).Name(), msg)
		Log.Warn(msg, ctx)
	}
}

func LogError(msg string, ctx interface{}) {
	if Log != nil {
		pc, fn, line, _ := runtime.Caller(1)
		msg := fmt.Sprintf("%s: %d: %s: %s", fn, line, runtime.FuncForPC(pc).Name(), msg)
		Log.Error(msg, ctx)
	}
}

func LogCrit(msg string, ctx interface{}) {
	if Log != nil {
		pc, fn, line, _ := runtime.Caller(1)
		msg := fmt.Sprintf("%s: %d: %s: %s", fn, line, runtime.FuncForPC(pc).Name(), msg)
		Log.Crit(msg, ctx)
	}
}

// Wrappers around Logger interface functions that send a string to the Logger
// by running it through fmt.Sprintf().
func LogInfof(format string, args ...interface{}) {
	if Log != nil {
		msg := fmt.Sprintf(format, args...)
		pc, fn, line, _ := runtime.Caller(1)
		msg = fmt.Sprintf("%s: %d: %s: %s", fn, line, runtime.FuncForPC(pc).Name(), msg)
		Log.Info(msg)
	}
}

func LogDebugf(format string, args ...interface{}) {
	if Log != nil {
		msg := fmt.Sprintf(format, args...)
		pc, fn, line, _ := runtime.Caller(1)
		msg = fmt.Sprintf("%s: %d: %s: %s", fn, line, runtime.FuncForPC(pc).Name(), msg)
		Log.Debug(msg)
	}
}

func LogWarnf(format string, args ...interface{}) {
	if Log != nil {
		msg := fmt.Sprintf(format, args...)
		pc, fn, line, _ := runtime.Caller(1)
		msg = fmt.Sprintf("%s: %d: %s: %s", fn, line, runtime.FuncForPC(pc).Name(), msg)
		Log.Warn(msg)
	}
}

func LogErrorf(format string, args ...interface{}) {
	if Log != nil {
		msg := fmt.Sprintf(format, args...)
		pc, fn, line, _ := runtime.Caller(1)
		msg = fmt.Sprintf("%s: %d: %s: %s", fn, line, runtime.FuncForPC(pc).Name(), msg)
		Log.Error(msg)
	}
}

func LogCritf(format string, args ...interface{}) {
	if Log != nil {
		msg := fmt.Sprintf(format, args...)
		pc, fn, line, _ := runtime.Caller(1)
		msg = fmt.Sprintf("%s: %d: %s: %s", fn, line, runtime.FuncForPC(pc).Name(), msg)
		Log.Crit(msg)
	}
}

func PrintStack() {
	buf := make([]byte, 1<<16)
	runtime.Stack(buf, true)
	LogErrorf("%s", buf)
}
