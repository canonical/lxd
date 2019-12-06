package qemu

import (
	"golang.org/x/sys/unix"

	lxdClient "github.com/lxc/lxd/client"
)

// Cmd represents a running command for an Qemu VM.
type Cmd struct {
	attachedChildPid int
	cmd              lxdClient.Operation
	dataDone         chan bool
	signalSendCh     chan unix.Signal
	signalResCh      chan error
	cleanupFunc      func()
}

// PID returns the attached child's process ID.
func (c *Cmd) PID() int {
	return c.attachedChildPid
}

// Signal sends a signal to the command.
func (c *Cmd) Signal(sig unix.Signal) error {
	c.signalSendCh <- sig
	return <-c.signalResCh
}

// Wait for the command to end and returns its exit code and any error.
func (c *Cmd) Wait() (int, error) {
	if c.cleanupFunc != nil {
		defer c.cleanupFunc()
	}

	err := c.cmd.Wait()
	if err != nil {
		return -1, err
	}

	opAPI := c.cmd.Get()
	<-c.dataDone
	exitCode := int(opAPI.Metadata["return"].(float64))

	return exitCode, nil
}
