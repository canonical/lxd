// +build !windows

package subprocess

import (
	"io/ioutil"

	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"
)

// NewProcess is a constructor for a process object. Represents a process with argument config. Returns an address to process
func NewProcess(name string, args []string, stdoutPath string, stderrPath string) (*Process, error) {
	proc := Process{
		Name:   name,
		Args:   args,
		Stdout: stdoutPath,
		Stderr: stderrPath,
	}

	return &proc, nil
}

// ImportProcess imports a saved process into a subprocess object.
func ImportProcess(path string) (*Process, error) {
	dat, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, errors.Wrapf(err, "Unable to read file '%s'", path)
	}

	proc := Process{}
	err = yaml.Unmarshal(dat, &proc)
	if err != nil {
		return nil, errors.Wrapf(err, "Unable to parse Process YAML")
	}

	return &proc, nil
}
