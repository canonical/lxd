//go:build !windows

package subprocess

import (
	"fmt"
	"io"
	"os"

	"go.yaml.in/yaml/v2"

	"github.com/canonical/lxd/lxd/util"
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
			return nil, fmt.Errorf("Cannot open stdout file %q: %w", stdoutPath, err)
		}
	}
	if stderrPath == stdoutPath {
		stderr = stdout
	} else if stderrPath != "" {
		stderr, err = os.Create(stderrPath)
		if err != nil {
			return nil, fmt.Errorf("Cannot open stderr file %q: %w", stderrPath, err)
		}
	}

	p := NewProcessWithFds(name, args, nil, stdout, stderr)
	p.closeFds = true

	return p, nil
}

// NewProcessWithFds is a constructor for a process object. Represents a process with argument config. Returns an address to process.
func NewProcessWithFds(name string, args []string, stdin io.ReadCloser, stdout io.WriteCloser, stderr io.WriteCloser) *Process {
	proc := Process{
		Name:   name,
		Args:   args,
		stdin:  stdin,
		stdout: stdout,
		stderr: stderr,
	}

	return &proc
}

// ImportProcess imports a saved process into a subprocess object.
func ImportProcess(path string) (*Process, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("Cannot read PID file %q: %w", path, err)
	}

	defer func() { _ = file.Close() }()

	proc := Process{}
	err = yaml.NewDecoder(util.MaxBytesReader(file, util.MaxYAMLFileBytes)).Decode(&proc)
	if err != nil {
		return nil, fmt.Errorf("Cannot parse YAML in PID file %q: %w", path, err)
	}

	if proc.PID <= 0 {
		return nil, fmt.Errorf("%w %d in PID file %q", ErrBadPID, proc.PID, path)
	}

	if proc.BootID != "" {
		bootID, err := util.CurrentBootID()
		if err == nil {
			if bootID != proc.BootID {
				return &proc, nil
			}
		}
	}

	// On unix, FindProcess always returns successfully (with a 'done' process if pidfd_open
	// returned with ESRCH).
	proc.proc, _ = os.FindProcess(proc.PID)
	if proc.StartTime != 0 {
		starttime, err := util.ProcessStartTime(proc.PID)
		if err == nil {
			if proc.StartTime != starttime {
				_ = proc.proc.Release()
				proc.proc = nil
				return &proc, nil
			}
		}
	}

	return &proc, nil
}
