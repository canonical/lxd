package subprocess

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"gopkg.in/yaml.v2"
)

// Process struct. Has ability to set runtime arguments
type Process struct {
	pid    int64
	Name   string   `yaml:"name"`
	Args   []string `yaml:"args,flow"`
	Stdin  string   `yaml:"stdin"`
	Stdout string   `yaml:"stdout"`
	Stderr string   `yaml:"stderr"`
}

// Pid returns the pid for the given process object
func (p *Process) Pid() (int64, error) {
	pr, _ := os.FindProcess(int(p.pid))
	err := pr.Signal(syscall.Signal(0))
	if err == nil {
		return p.pid, nil
	}

	return 0, fmt.Errorf("Process not running, cannot retrieve PID")
}

// Stop will stop the given process object
func (p *Process) Stop() error {
	pr, _ := os.FindProcess(int(p.pid))
	err := pr.Signal(syscall.Signal(0))
	if err == nil {
		err = pr.Kill()
		if err != nil {
			return fmt.Errorf("Could not kill process: %s", err)
		}

		return nil
	} else if err == syscall.ESRCH { //ESRCH error is if process could not be found
		return fmt.Errorf("Process is not running. Could not kill process")
	}

	return fmt.Errorf("Could not kill process: %s", err)
}

// Start will start the given process object
func (p *Process) Start() error {
	args := strings.Join(p.Args, " ")
	cmd := exec.Command(p.Name, args)

	if p.Stdout != "" {
		out, err := os.Create(p.Stdout)
		if err != nil {
			return fmt.Errorf("Unable to open stdout file: %s", err)
		}
		defer out.Close()
		cmd.Stdout = out
	}

	if p.Stdin != "" {
		in, err := os.Open(p.Stdin)
		if err != nil {
			return fmt.Errorf("Unable to open stdin file: %s", err)
		}
		defer in.Close()
		cmd.Stdin = in
	}

	if p.Stderr != "" {
		sterr, err := os.Create(p.Stderr)
		if err != nil {
			return fmt.Errorf("Unable to open stderr file: %s", err)
		}
		defer sterr.Close()
		cmd.Stderr = sterr
	}

	err := cmd.Start()
	if err != nil {
		return fmt.Errorf("Unable to start process: %s", err)
	}

	p.pid = int64(cmd.Process.Pid)
	return nil
}

// Restart stop and starts the given process object
func (p *Process) Restart() error {
	err := p.Stop()
	if err != nil {
		return fmt.Errorf("Unable to stop process: %s", err)
	}

	err = p.Start()
	if err != nil {
		return fmt.Errorf("Unable to start process: %s", err)
	}

	return nil
}

// Reload sends the SIGHUP signal to the given process object
func (p *Process) Reload() error {
	pr, _ := os.FindProcess(int(p.pid))
	err := pr.Signal(syscall.Signal(0))
	if err == nil {
		err = pr.Signal(syscall.SIGHUP)
		if err != nil {
			return fmt.Errorf("Could not reload process: %s", err)
		}
		return nil
	} else if err == syscall.ESRCH { //ESRCH error is if process could not be found
		return fmt.Errorf("Process is not running. Could not reload process")
	}

	return fmt.Errorf("Could not reload process: %s", err)
}

// Save will save the given process object to a yaml file. Can be imported at a later point.
func (p *Process) Save(path string) error {
	dat, err := yaml.Marshal(p)
	if err != nil {
		return fmt.Errorf("Unable to serialize process struct to YAML: %s", err)
	}

	err = ioutil.WriteFile(path, dat, 0644)
	if err != nil {
		return fmt.Errorf("Unable to write to file %s: %s", path, err)
	}

	return nil
}

// Signal will send a signal to the given process object given a signal value
func (p *Process) Signal(signal int64) error {
	pr, _ := os.FindProcess(int(p.pid))
	err := pr.Signal(syscall.Signal(0))
	if err == nil {
		err = pr.Signal(syscall.Signal(signal))
		if err != nil {
			return fmt.Errorf("Could not signal process: %s", err)
		}
		return nil
	} else if err == syscall.ESRCH { //ESRCH error is if process could not be found
		return fmt.Errorf("Process is not running. Could not signal process")
	}

	return fmt.Errorf("Could not signal process: %s", err)
}

// Wait will wait for the given process object exit code
func (p *Process) Wait() (int64, error) {
	pr, _ := os.FindProcess(int(p.pid))
	err := pr.Signal(syscall.Signal(0))
	if err == nil {
		procstate, err := pr.Wait()
		if err != nil {
			return -1, fmt.Errorf("Could not wait on process: %s", err)
		}
		exitcode := int64(procstate.Sys().(syscall.WaitStatus).ExitStatus())
		return exitcode, nil
	}

	return 0, fmt.Errorf("Process is not running. Could not wait")
}
