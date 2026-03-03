//go:build !windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"

	"github.com/gorilla/websocket"
	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/shared/logger"
)

func (c *cmdConsole) controlSocketHandler(control *websocket.Conn) {
	ch := make(chan os.Signal, 10)
	signal.Notify(ch, unix.SIGWINCH)

	for {
		sig := <-ch
		logger.Debugf("Received '%s signal', updating window geometry.", sig)
		err := c.sendTermSize(control)
		if err != nil {
			logger.Debugf("error setting term size: %s", err)
			break
		}
	}

	closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
	err := control.WriteMessage(websocket.CloseMessage, closeMsg)
	if err != nil {
		logger.Debugf("error sending close message: %s", err)
	}
}

func (c *cmdConsole) findCommand(name string) string {
	// Look for the command in the PATH but ignore "not found" errors as multiple candidates may be tried.
	path, err := exec.LookPath(name)
	if err != nil && !errors.Is(err, exec.ErrNotFound) {
		logger.Debugf("error looking for command '%s': %s", name, err)
	}

	return path
}
