package main

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

// ContainerLXCCmd represents a running command for an LXC container.
type ContainerLXCCmd struct {
	attachedChildPid int
	cmd              *exec.Cmd
}

// PID returns the attached child's process ID.
func (c *ContainerLXCCmd) PID() int {
	return c.attachedChildPid
}

// Signal sends a signal to the command.
func (c *ContainerLXCCmd) Signal(s unix.Signal) error {
	err := unix.Kill(c.attachedChildPid, s)
	if err != nil {
		return err
	}

	return nil
}

// Wait for the command to end and returns its exit code and any error.
func (c *ContainerLXCCmd) Wait() (int, error) {
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
