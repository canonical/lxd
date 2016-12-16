package state

import (
	"os"
)

// Global variables
var Debug bool
var Verbose bool
var ExecPath string

func init() {
	absPath, err := os.Readlink("/proc/self/exe")
	if err != nil {
		absPath = "bad-exec-path"
	}
	ExecPath = absPath
}
