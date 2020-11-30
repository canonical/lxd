package subprocess

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/shared"
)

// Process struct. Has ability to set runtime arguments
type Process struct {
	exitCode int64 `yaml:"-"`
	exitErr  error `yaml:"-"`

	chExit     chan struct{} `yaml:"-"`
	hasMonitor bool          `yaml:"-"`

	Name     string   `yaml:"name"`
	Args     []string `yaml:"args,flow"`
	Apparmor string   `yaml:"apparmor"`
	PID      int64    `yaml:"pid"`
	Stdout   string   `yaml:"stdout"`
	Stderr   string   `yaml:"stderr"`

	UID       uint32 `yaml:"uid"`
	GID       uint32 `yaml:"gid"`
	SetGroups bool   `yaml:"set_groups"`
}

func (p *Process) hasApparmor() bool {
	_, err := exec.LookPath("aa-exec")
	if err != nil {
		return false
	}

	if !shared.PathExists("/sys/kernel/security/apparmor") {
		return false
	}

	return true
}

// GetPid returns the pid for the given process object
func (p *Process) GetPid() (int64, error) {
	pr, _ := os.FindProcess(int(p.PID))
	err := pr.Signal(syscall.Signal(0))
	if err == nil {
		return p.PID, nil
	}

	return 0, ErrNotRunning
}

// SetApparmor allows setting the AppArmor profile.
func (p *Process) SetApparmor(profile string) {
	p.Apparmor = profile
}

// SetCreds allows setting process credentials.
func (p *Process) SetCreds(uid uint32, gid uint32) {
	p.UID = uid
	p.GID = gid
}

// Stop will stop the given process object
func (p *Process) Stop() error {
	pr, _ := os.FindProcess(int(p.PID))

	// Check if process exists.
	err := pr.Signal(syscall.Signal(0))
	if err == nil {
		err = pr.Kill()
		if err == nil {
			if p.hasMonitor {
				<-p.chExit
			}

			return nil // Killed successfully.
		}
	}

	// Check if either the existence check or the kill resulted in an already finished error.
	if strings.Contains(err.Error(), "process already finished") {
		if p.hasMonitor {
			<-p.chExit
		}

		return ErrNotRunning
	}

	return errors.Wrapf(err, "Could not kill process")
}

// Start will start the given process object
func (p *Process) Start() error {
	return p.start(nil)
}

// StartWithFiles will start the given process object with extra file descriptors
func (p *Process) StartWithFiles(fds []*os.File) error {
	return p.start(fds)
}

func (p *Process) start(fds []*os.File) error {
	var cmd *exec.Cmd

	if p.Apparmor != "" && p.hasApparmor() {
		cmd = exec.Command("aa-exec", append([]string{"-p", p.Apparmor, p.Name}, p.Args...)...)
	} else {
		cmd = exec.Command(p.Name, p.Args...)
	}
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{}
	cmd.SysProcAttr.Setsid = true

	if p.UID != 0 || p.GID != 0 {
		cmd.SysProcAttr.Credential = &syscall.Credential{}
		cmd.SysProcAttr.Credential.Uid = p.UID
		cmd.SysProcAttr.Credential.Gid = p.GID
	}

	if fds != nil {
		cmd.ExtraFiles = fds
	}

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

	p.PID = int64(cmd.Process.Pid)

	// Reset exitCode/exitErr
	p.exitCode = 0
	p.exitErr = nil

	// Spawn a goroutine waiting for it to exit.
	p.chExit = make(chan struct{})
	p.hasMonitor = true
	go func() {
		procstate, err := cmd.Process.Wait()
		if err != nil {
			p.exitCode = -1
			p.exitErr = err
			close(p.chExit)
			return
		}

		exitcode := int64(procstate.ExitCode())
		p.exitCode = exitcode
		if p.exitCode != 0 {
			p.exitErr = fmt.Errorf("Process exited with a non-zero value")
		}
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
	pr, _ := os.FindProcess(int(p.PID))
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
	pr, _ := os.FindProcess(int(p.PID))
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
	if !p.hasMonitor {
		return -1, fmt.Errorf("Unable to wait on process we didn't spawn")
	}

	<-p.chExit
	return p.exitCode, p.exitErr
}
