package shared

import (
	"runtime"
)

var Logfunc func(string, string, ...interface{})
var Debugf func(string, ...interface{})
var Logf func(string, ...interface{})

var Log logger

type logger struct{}

func (l *logger) Debug(msg string, ctx ...interface{}) {
	Logfunc("debug", msg, ctx)
}
func (l *logger) Info(msg string, ctx ...interface{}) {
	Logfunc("info", msg, ctx)
}
func (l *logger) Warn(msg string, ctx ...interface{}) {
	Logfunc("warn", msg, ctx)
}
func (l *logger) Error(msg string, ctx ...interface{}) {
	Logfunc("error", msg, ctx)
}
func (l *logger) Crit(msg string, ctx ...interface{}) {
	Logfunc("crit", msg, ctx)
}

func init() {
	Logfunc = func(string, string, ...interface{}) {}
	Debugf = func(string, ...interface{}) {}
	Logf = func(string, ...interface{}) {}
}

func PrintStack() {
	buf := make([]byte, 1<<16)
	runtime.Stack(buf, true)
	Debugf("%s", buf)
}
