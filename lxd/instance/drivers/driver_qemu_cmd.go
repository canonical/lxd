package drivers

import (
	"strconv"

	"golang.org/x/sys/unix"

	lxdClient "github.com/grant-he/lxd/client"
	"github.com/grant-he/lxd/shared/api"
	"github.com/grant-he/lxd/shared/logger"
)

// Cmd represents a running command for an Qemu VM.
type qemuCmd struct {
	attachedChildPid int
	cmd              lxdClient.Operation
	dataDone         chan bool
	controlSendCh    chan api.InstanceExecControl
	controlResCh     chan error
	cleanupFunc      func()
}

// PID returns the attached child's process ID.
func (c *qemuCmd) PID() int {
	return c.attachedChildPid
}

// Signal sends a signal to the command.
func (c *qemuCmd) Signal(sig unix.Signal) error {
	command := api.InstanceExecControl{
		Command: "signal",
		Signal:  int(sig),
	}

	c.controlSendCh <- command
	err := <-c.controlResCh
	if err != nil {
		return err
	}

	logger.Debugf(`Forwarded signal "%d" to lxd-agent`, sig)
	return nil
}

// Wait for the command to end and returns its exit code and any error.
func (c *qemuCmd) Wait() (int, error) {
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

// WindowResize resizes the running command's window.
func (c *qemuCmd) WindowResize(fd, winchWidth, winchHeight int) error {
	command := api.InstanceExecControl{
		Command: "window-resize",
		Args: map[string]string{
			"width":  strconv.Itoa(winchWidth),
			"height": strconv.Itoa(winchHeight),
		},
	}

	c.controlSendCh <- command
	err := <-c.controlResCh
	if err != nil {
		return err
	}
	logger.Debugf(`Forwarded window resize "%dx%d" to lxd-agent`, winchWidth, winchHeight)
	return nil
}
