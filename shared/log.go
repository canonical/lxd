package shared

var Log func(string, string, ...interface{})
var Debugf func(string, ...interface{})
var Logf func(string, ...interface{})

type Ctx map[string]interface{}

func init() {
	Log = func(string, string, ...interface{}) {}
	Debugf = func(string, ...interface{}) {}
	Logf = func(string, ...interface{}) {}
}
