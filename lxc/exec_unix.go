// +build !windows

package main

import (
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
)

func (c *execCmd) getStdout() io.WriteCloser {
	return os.Stdout
}

func (c *execCmd) controlSocketHandler(d *lxd.Client, control *websocket.Conn) {
	ch := make(chan os.Signal)
	signal.Notify(ch, syscall.SIGWINCH)

	for {
		sig := <-ch

		shared.LogDebugf("Received '%s signal', updating window geometry.", sig)

		err := c.sendTermSize(control)
		if err != nil {
			shared.LogDebugf("error setting term size %s", err)
			break
		}
	}

	closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
	control.WriteMessage(websocket.CloseMessage, closeMsg)
}
