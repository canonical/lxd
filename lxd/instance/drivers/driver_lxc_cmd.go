package drivers

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/grant-he/lxd/shared"
	"github.com/grant-he/lxd/shared/logger"
)

// lxcCmd represents a running command for an LXC container.
type lxcCmd struct {
	attachedChildPid int
	cmd              *exec.Cmd
}

// PID returns the attached child's process ID.
func (c *lxcCmd) PID() int {
	return c.attachedChildPid
}

// Signal sends a signal to the command.
func (c *lxcCmd) Signal(sig unix.Signal) error {
	err := unix.Kill(c.attachedChildPid, sig)
	if err != nil {
		return err
	}

	logger.Debugf(`Forwarded signal "%d" to PID "%d"`, sig, c.PID())
	return nil
}

// Wait for the command to end and returns its exit code and any error.
func (c *lxcCmd) Wait() (int, error) {
	err := c.cmd.Wait()
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if ok {
			status, ok := exitErr.Sys().(syscall.WaitStatus)
			if ok {
				return status.ExitStatus(), nil
			}

			if status.Signaled() {
				// 128 + n == Fatal error signal "n"
				return 128 + int(status.Signal()), nil
			}
		}

		return -1, err
	}

	return 0, nil
}

// WindowResize resizes the running command's window.
func (c *lxcCmd) WindowResize(fd, winchWidth, winchHeight int) error {
	err := shared.SetSize(fd, winchWidth, winchHeight)
	if err != nil {
		return err
	}

	logger.Debugf(`Set window size "%dx%d" of PID "%d"`, winchWidth, winchHeight, c.PID())
	return nil
}
