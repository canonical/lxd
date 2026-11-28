//go:build !windows

package subprocess

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"go.yaml.in/yaml/v2"

	"github.com/canonical/lxd/lxd/linux"
	"github.com/canonical/lxd/shared"
)

// Process struct. Has ability to set runtime arguments.
type Process struct {
	exitCode int64
	exitErr  error

	chExit           chan struct{}
	hasMonitor       bool
	closeFds         bool
	verifiedIdentity bool
	proc             *os.Process

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
	Dir       string `yaml:"dir"`
	StartTime uint64 `yaml:"start_time"`

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

const (
	stopWaitTimeout  = 5 * time.Second
	stopWaitInterval = 10 * time.Millisecond
)

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

func isDeadProcState(state string) bool {
	return state == "Z" || state == "X" || state == "x"
}

// processExited reports whether pid has exited (gone or zombie whose FDs are already closed).
func processExited(pid int) bool {
	fields, err := linux.ProcStatFields("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return errors.Is(err, os.ErrNotExist)
	}

	return len(fields) >= 3 && isDeadProcState(fields[2])
}

// processGroupHasLiveMembers reports whether process group pgid still has any non-zombie member.
func processGroupHasLiveMembers(pgid int) (bool, error) {
	err := syscall.Kill(-pgid, syscall.Signal(0))
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	// kill(-pgid, 0) still succeeds when only unreaped zombies remain in the group.
	// Inspect /proc to distinguish live members from zombies whose FDs are already closed.
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return true, nil
	}

	pgidStr := strconv.Itoa(pgid)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		name := entry.Name()
		if name == "" || name[0] < '0' || name[0] > '9' {
			continue
		}

		fields, err := linux.ProcStatFields("/proc/" + name + "/stat")
		if err != nil || len(fields) < 5 {
			continue
		}

		if fields[4] == pgidStr && !isDeadProcState(fields[2]) {
			return true, nil
		}
	}

	return false, nil
}

func (p *Process) waitForExit() error {
	deadline := time.Now().Add(stopWaitTimeout)
	for {
		err := p.proc.Signal(syscall.Signal(0))
		if errors.Is(err, os.ErrProcessDone) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("Failed checking process exit: %w", err)
		}

		if p.PID > 0 && processExited(p.PID) {
			return nil
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("Timed out waiting for process %d to exit", p.PID)
		}

		time.Sleep(stopWaitInterval)
	}
}

func waitForProcessGroupExit(pgid int) error {
	deadline := time.Now().Add(stopWaitTimeout)
	for {
		alive, err := processGroupHasLiveMembers(pgid)
		if err != nil {
			return fmt.Errorf("Failed checking process group %d exit: %w", pgid, err)
		}

		if !alive {
			return nil
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("Timed out waiting for process group %d to exit", pgid)
		}

		time.Sleep(stopWaitInterval)
	}
}

// Stop will stop the given process object and its process group.
func (p *Process) Stop() error {
	if p.proc == nil {
		return ErrNotRunning
	}

	// Verify the process is still running via its handle before inspecting or
	// signaling its process group, avoiding signaling a recycled PGID.
	err := p.proc.Signal(syscall.Signal(0))
	if err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			p.finish()

			return ErrNotRunning
		}

		return fmt.Errorf("Could not kill process: %w", err)
	}

	// Processes launched via Start run in their own session and process group
	// (Setsid=true), making p.PID the process group leader. When process identity
	// is verified, kill the entire process group so forked children (such as
	// dnsmasq TCP helpers) do not survive and hold inherited sockets open.
	// Legacy imports without verified StartTime/BootID fall back to signaling
	// only the single process to avoid expanding PID-reuse blast radius.
	if p.verifiedIdentity && p.PID > 0 {
		pgid, pgidErr := syscall.Getpgid(p.PID)
		if pgidErr == nil && pgid == p.PID {
			err = syscall.Kill(-pgid, syscall.SIGKILL)
			if err == nil {
				p.finish()

				return waitForProcessGroupExit(pgid)
			}

			if !errors.Is(err, syscall.ESRCH) {
				return fmt.Errorf("Could not kill process group: %w", err)
			}
		}
	}

	err = p.proc.Signal(syscall.SIGKILL)
	if err == nil {
		if p.hasMonitor {
			p.finish()

			return nil
		}

		waitErr := p.waitForExit()
		p.release()

		return waitErr
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
	cmd.Dir = p.Dir
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
	p.verifiedIdentity = true

	starttime, err := linux.ProcessStartTime(p.PID)
	if err == nil {
		p.StartTime = starttime
	}

	bootID, err := linux.CurrentBootID()
	if err == nil {
		p.BootID = bootID
	}
	// Reset exitCode/exitErr
	p.exitCode = 0
	p.exitErr = nil

	// Spawn a goroutine waiting for it to exit.
	p.chExit = make(chan struct{})
	p.hasMonitor = true
	go func() {
		defer close(p.chExit)

		err := cmd.Wait()

		if cmd.ProcessState != nil {
			p.exitCode = int64(cmd.ProcessState.ExitCode())
		} else {
			p.exitCode = -1
		}

		if err != nil {
			p.exitErr = err

			return
		}

		if p.exitCode != 0 {
			p.exitErr = fmt.Errorf("Process exited with non-zero value %d", p.exitCode)
		}
	}()

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
	dat, err := yaml.Marshal(p)
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
