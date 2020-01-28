package subprocess

import (
	"fmt"
	"io/ioutil"

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
		return nil, fmt.Errorf("Unable to read file %s: %s", path, err)
	}

	proc := Process{}
	err = yaml.Unmarshal(dat, &proc)
	if err != nil {
		return nil, fmt.Errorf("Unable to parse Process YAML: %s", err)
	}

	return &proc, nil
}
