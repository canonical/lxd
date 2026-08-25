//go:build !windows

package subprocess

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"

	"go.yaml.in/yaml/v2"

	"github.com/canonical/lxd/shared"
)

// Process struct. Has ability to set runtime arguments.
type Process struct {
	exitCode int64
	exitErr  error

	chExit     chan struct{}
	hasMonitor bool
	closeFds   bool
	proc       *os.Process

	Name     string   `yaml:"name"`
	Args     []string `yaml:"args,flow"`
	Apparmor string   `yaml:"apparmor"`
	PID      int      `yaml:"pid"`
	BootID   string   `yaml:"boot_id"`
	stdin    io.ReadCloser
	stdout   io.WriteCloser
	stderr   io.WriteCloser

	UID       uint32 `yaml:"uid"`
	GID       uint32 `yaml:"gid"`
	SetGroups bool   `yaml:"set_groups"`
	StartTime int64  `yaml:"start_time"`

	SysProcAttr *syscall.SysProcAttr
}

func (p *Process) hasApparmor() bool {
	if shared.IsFalse(os.Getenv("LXD_SECURITY_APPARMOR")) {
		return false
	}

	if !shared.PathExists("/sys/kernel/security/apparmor") {
		return false
	}

	_, err := exec.LookPath("aa-exec")
	return err == nil
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

func (p *Process) release() {
	if p.proc == nil {
		return
	}

	_ = p.proc.Release()
	p.proc = nil
}

func (p *Process) finish() {
	if p.hasMonitor {
		<-p.chExit
		return
	}

	p.release()
}

// startMonitor spawns a goroutine that waits for the process to exit using the supplied wait
// function, records the resulting exit code and error, and closes chExit. It is shared by spawned
// processes (which reap via the child handle) and imported processes (which wait via a pidfd).
func (p *Process) startMonitor(wait func() (int64, error)) {
	p.exitCode = -1
	p.exitErr = nil
	chExit := make(chan struct{})
	p.chExit = chExit
	p.hasMonitor = true

	go func() {
		defer close(chExit)

		p.exitCode, p.exitErr = wait()
	}()
}

// monitorImported starts a monitor for an imported (non-child) process, waiting on it via a pidfd.
// On kernels that support PIDFD_GET_INFO the exit code is recorded, otherwise it is unknown.
func (p *Process) monitorImported() {
	// Capture the identity locally so the wait does not touch p, which may be reused (e.g. via
	// Start or Restart) while this monitor is still running.
	pid := p.PID
	startTime := p.StartTime
	p.startMonitor(func() (int64, error) {
		code, err := waitProcess(context.Background(), pid, startTime)
		if err != nil {
			return -1, nil
		}

		if code > 0 {
			return code, fmt.Errorf("Process exited with non-zero value %d", code)
		}

		return code, nil
	})
}

// Stop will stop the given process object.
func (p *Process) Stop() error {
	if p.proc == nil {
		return ErrNotRunning
	}

	err := p.proc.Signal(syscall.SIGKILL)
	if err == nil {
		p.finish()

		return nil
	}

	if errors.Is(err, os.ErrProcessDone) {
		p.finish()

		return ErrNotRunning
	}

	return fmt.Errorf("Could not kill process: %w", err)
}

// Start will start the given process object.
func (p *Process) Start(ctx context.Context) error {
	return p.start(ctx, nil)
}

// StartWithFiles will start the given process object with extra file descriptors.
func (p *Process) StartWithFiles(ctx context.Context, fds []*os.File) error {
	return p.start(ctx, fds)
}

func (p *Process) start(ctx context.Context, fds []*os.File) error {
	var cmd *exec.Cmd

	if p.Apparmor != "" && p.hasApparmor() {
		cmd = exec.CommandContext(ctx, "aa-exec", append([]string{"-p", p.Apparmor, p.Name}, p.Args...)...)
	} else {
		cmd = exec.CommandContext(ctx, p.Name, p.Args...)
	}

	cmd.Stdout = p.stdout
	cmd.Stderr = p.stderr
	cmd.Stdin = p.stdin
	cmd.SysProcAttr = p.SysProcAttr
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}

	cmd.SysProcAttr.Setsid = true

	if p.UID != 0 || p.GID != 0 {
		cmd.SysProcAttr.Credential = &syscall.Credential{}
		cmd.SysProcAttr.Credential.Uid = p.UID
		cmd.SysProcAttr.Credential.Gid = p.GID
	}

	if fds != nil {
		cmd.ExtraFiles = fds
	}

	if p.stdout != nil && p.closeFds {
		defer func() { _ = p.stdout.Close() }()
	}

	if p.stderr != nil && p.stderr != p.stdout && p.closeFds {
		defer func() { _ = p.stderr.Close() }()
	}

	// Start the process.
	err := cmd.Start()
	if err != nil {
		return fmt.Errorf("Cannot start process: %w", err)
	}

	p.BootID = ""
	p.StartTime = 0
	p.proc = cmd.Process
	p.PID = cmd.Process.Pid

	starttime, err := processStartTime(p.PID)
	if err == nil {
		p.StartTime = starttime
	}

	bootID, err := currentBootID()
	if err == nil {
		p.BootID = bootID
	}

	p.startMonitor(func() (int64, error) {
		err := cmd.Wait()

		code := int64(-1)
		if cmd.ProcessState != nil {
			code = int64(cmd.ProcessState.ExitCode())
		}

		if err != nil {
			return code, err
		}

		if code != 0 {
			return code, fmt.Errorf("Process exited with non-zero value %d", code)
		}

		return code, nil
	})

	return nil
}

// Restart stop and starts the given process object.
func (p *Process) Restart(ctx context.Context) error {
	err := p.Stop()
	if err != nil {
		return fmt.Errorf("Cannot stop process: %w", err)
	}

	err = p.Start(ctx)
	if err != nil {
		return fmt.Errorf("Cannot start process: %w", err)
	}

	return nil
}

// Reload sends the SIGHUP signal to the given process object.
func (p *Process) Reload() error {
	if p.proc == nil {
		return ErrNotRunning
	}

	err := p.proc.Signal(syscall.SIGHUP)
	if err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			p.finish()
			return ErrNotRunning
		}

		return fmt.Errorf("Could not reload process: %w", err)
	}

	return nil
}

// Save will save the given process object to a YAML file. Can be imported at a later point.
func (p *Process) Save(path string) error {
	// Marshal a copy of only the persisted fields. Marshalling the live process would have yaml
	// read the whole struct (via reflect) and race with the monitor goroutine updating the
	// exit state. The excluded fields are not persisted anyway.
	saved := Process{
		Name:        p.Name,
		Args:        p.Args,
		Apparmor:    p.Apparmor,
		PID:         p.PID,
		BootID:      p.BootID,
		UID:         p.UID,
		GID:         p.GID,
		SetGroups:   p.SetGroups,
		StartTime:   p.StartTime,
		SysProcAttr: p.SysProcAttr,
	}

	dat, err := yaml.Marshal(&saved)
	if err != nil {
		return fmt.Errorf("Cannot serialize process struct to YAML: %w", err)
	}

	err = os.WriteFile(path, dat, 0600)
	if err != nil {
		return fmt.Errorf("Cannot write to file %q: %w", path, err)
	}

	return nil
}

// Signal will send a signal to the given process object given a signal value.
func (p *Process) Signal(signal int64) error {
	if p.proc == nil {
		return ErrNotRunning
	}

	err := p.proc.Signal(syscall.Signal(signal))
	if err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			p.finish()
			return ErrNotRunning
		}

		return fmt.Errorf("Could not signal process: %w", err)
	}

	return nil
}

// Wait will wait for the given process object exit code.
func (p *Process) Wait(ctx context.Context) (int64, error) {
	if !p.hasMonitor {
		return -1, errors.New("Cannot wait on process we did not spawn")
	}

	select {
	case <-p.chExit:
		return p.exitCode, p.exitErr
	case <-ctx.Done():
		return -1, ctx.Err()
	}
}
