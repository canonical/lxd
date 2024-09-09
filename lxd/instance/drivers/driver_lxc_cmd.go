package drivers

import (
	"os/exec"

	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
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
	exitStatus, err := shared.ExitStatus(c.cmd.Wait())

	// Convert special exit statuses into errors.
	switch exitStatus {
	case 127:
		err = ErrExecCommandNotFound
	case 126:
		err = ErrExecCommandNotExecutable
	case 129, 137, 143:
		// Container disconnections can result in different exit codes depending
		// on what signal was used internally.
		// 129 for SIGHUP, 137 for SIGKILL and 143 for SIGTERM are all possible.
		err = ErrExecDisconnected
	}

	return exitStatus, err
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
