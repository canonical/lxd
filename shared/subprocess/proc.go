package subprocess

import (
	"io/ioutil"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"
)

// Process struct. Has ability to set runtime arguments
type Process struct {
	exitCode int64         `yaml:"-"`
	exitErr  error         `yaml:"-"`
	chExit   chan struct{} `yaml:"-"`

	Name   string   `yaml:"name"`
	Args   []string `yaml:"args,flow"`
	Pid    int64    `yaml:"pid"`
	Stdout string   `yaml:"stdout"`
	Stderr string   `yaml:"stderr"`
}

// GetPid returns the pid for the given process object
func (p *Process) GetPid() (int64, error) {
	pr, _ := os.FindProcess(int(p.Pid))
	err := pr.Signal(syscall.Signal(0))
	if err == nil {
		return p.Pid, nil
	}

	return 0, ErrNotRunning
}

// Stop will stop the given process object
func (p *Process) Stop() error {
	pr, _ := os.FindProcess(int(p.Pid))

	// Check if process exists.
	err := pr.Signal(syscall.Signal(0))
	if err == nil {
		err = pr.Kill()
		if err == nil {
			return nil // Killed successfully.
		}
	}

	// Wait for any background goroutine to be done.
	<-p.chExit

	// Check if either the existence check or the kill resulted in an already finished error.
	if strings.Contains(err.Error(), "process already finished") {
		return ErrNotRunning
	}

	return errors.Wrapf(err, "Could not kill process")
}

// Start will start the given process object
func (p *Process) Start() error {
	cmd := exec.Command(p.Name, p.Args...)
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{}
	cmd.SysProcAttr.Setsid = true

	// Setup output capture.
	if p.Stdout != "" {
		out, err := os.Create(p.Stdout)
		if err != nil {
			return errors.Wrapf(err, "Unable to open stdout file")
		}
		defer out.Close()
		cmd.Stdout = out
	}

	if p.Stderr == p.Stdout {
		cmd.Stderr = cmd.Stdout
	} else if p.Stderr != "" {
		out, err := os.Create(p.Stderr)
		if err != nil {
			return errors.Wrapf(err, "Unable to open stderr file")
		}
		defer out.Close()
		cmd.Stderr = out
	}

	// Start the process.
	err := cmd.Start()
	if err != nil {
		return errors.Wrapf(err, "Unable to start process")
	}

	p.Pid = int64(cmd.Process.Pid)
	p.chExit = make(chan struct{})

	// Spawn a goroutine waiting for it to exit.
	go func() {
		procstate, err := cmd.Process.Wait()
		if err != nil {
			p.exitCode = -1
			p.exitErr = err
			close(p.chExit)
			return
		}

		p.exitCode = int64(procstate.ExitCode())
		close(p.chExit)
	}()

	return nil
}

// Restart stop and starts the given process object
func (p *Process) Restart() error {
	err := p.Stop()
	if err != nil {
		return errors.Wrapf(err, "Unable to stop process")
	}

	err = p.Start()
	if err != nil {
		return errors.Wrapf(err, "Unable to start process")
	}

	return nil
}

// Reload sends the SIGHUP signal to the given process object
func (p *Process) Reload() error {
	pr, _ := os.FindProcess(int(p.Pid))
	err := pr.Signal(syscall.Signal(0))
	if err == nil {
		err = pr.Signal(syscall.SIGHUP)
		if err != nil {
			return errors.Wrapf(err, "Could not reload process")
		}
		return nil
	} else if strings.Contains(err.Error(), "process already finished") {
		return ErrNotRunning
	}

	return errors.Wrapf(err, "Could not reload process")
}

// Save will save the given process object to a YAML file. Can be imported at a later point.
func (p *Process) Save(path string) error {
	dat, err := yaml.Marshal(p)
	if err != nil {
		return errors.Wrapf(err, "Unable to serialize process struct to YAML")
	}

	err = ioutil.WriteFile(path, dat, 0644)
	if err != nil {
		return errors.Wrapf(err, "Unable to write to file '%s'", path)
	}

	return nil
}

// Signal will send a signal to the given process object given a signal value
func (p *Process) Signal(signal int64) error {
	pr, _ := os.FindProcess(int(p.Pid))
	err := pr.Signal(syscall.Signal(0))
	if err == nil {
		err = pr.Signal(syscall.Signal(signal))
		if err != nil {
			return errors.Wrapf(err, "Could not signal process")
		}
		return nil
	} else if strings.Contains(err.Error(), "process already finished") {
		return ErrNotRunning
	}

	return errors.Wrapf(err, "Could not signal process")
}

// Wait will wait for the given process object exit code
func (p *Process) Wait() (int64, error) {
	<-p.chExit
	return p.exitCode, p.exitErr
}
