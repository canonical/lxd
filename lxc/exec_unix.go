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

func getStdout() io.WriteCloser {
	return os.Stdout
}

func controlSocketHandler(c *lxd.Client, control *websocket.Conn) {
	ch := make(chan os.Signal)
	signal.Notify(ch, syscall.SIGWINCH)

	for {
		err := sendTermSize(control)
		if err != nil {
			shared.Debugf("error setting term size %s", err)
			break
		}

		sig := <-ch

		shared.Debugf("Received '%s signal', updating window geometry.", sig)
	}

	closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
	control.WriteMessage(websocket.CloseMessage, closeMsg)
}
