//go:build !windows

package main

import (
	"os"
	"os/exec"
	"os/signal"

	"github.com/gorilla/websocket"
	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/shared/logger"
)

// controlSocketHandler listens for terminal window size changes and updates the terminal geometry accordingly, then closes the websocket connection.
func (c *cmdConsole) controlSocketHandler(control *websocket.Conn) {
	ch := make(chan os.Signal, 10)
	signal.Notify(ch, unix.SIGWINCH)

	for {
		sig := <-ch
		logger.Debugf("Received '%s signal', updating window geometry.", sig)
		err := c.sendTermSize(control)
		if err != nil {
			logger.Debugf("error setting term size %s", err)
			break
		}
	}

	closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
	_ = control.WriteMessage(websocket.CloseMessage, closeMsg)
}

// findCommand returns the path of the specified command if it exists in the system's PATH.
func (c *cmdConsole) findCommand(name string) string {
	path, _ := exec.LookPath(name)
	return path
}
