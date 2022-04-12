//go:build !windows

package subprocess

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"

	"gopkg.in/yaml.v2"
)

// NewProcess is a constructor for a process object. Represents a process with argument config.
// stdoutPath and stderrPath arguments are optional. Returns an address to process.
func NewProcess(name string, args []string, stdoutPath string, stderrPath string) (*Process, error) {
	var stdout, stderr io.WriteCloser
	var err error
	// Setup output capture.
	if stdoutPath != "" {
		stdout, err = os.Create(stdoutPath)
		if err != nil {
			return nil, fmt.Errorf("Unable to open stdout file %q: %w", stdoutPath, err)
		}
	}
	if stderrPath == stdoutPath {
		stderr = stdout
	} else if stderrPath != "" {
		stderr, err = os.Create(stderrPath)
		if err != nil {
			return nil, fmt.Errorf("Unable to open stderr file %q: %w", stderrPath, err)
		}
	}

	p, err := NewProcessWithFds(name, args, nil, stdout, stderr)
	if err != nil {
		return nil, fmt.Errorf("Error when creating process object: %w", err)
	}
	p.closeFds = true

	return p, nil
}

// NewProcessWithFds is a constructor for a process object. Represents a process with argument config. Returns an address to process
func NewProcessWithFds(name string, args []string, stdin io.ReadCloser, stdout io.WriteCloser, stderr io.WriteCloser) (*Process, error) {
	proc := Process{
		Name:   name,
		Args:   args,
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
	}

	return &proc, nil
}

// ImportProcess imports a saved process into a subprocess object.
func ImportProcess(path string) (*Process, error) {
	dat, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("Unable to read file '%s': %w", path, err)
	}

	proc := Process{}
	err = yaml.Unmarshal(dat, &proc)
	if err != nil {
		return nil, fmt.Errorf("Unable to parse Process YAML: %w", err)
	}

	return &proc, nil
}
