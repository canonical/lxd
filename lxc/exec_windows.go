// +build windows

package main

import (
	"io"
	"os"

	"github.com/gorilla/websocket"
	"github.com/shiena/ansicolor"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
)

func getStdout() io.Writer {
	return ansicolor.NewAnsiColorWriter(os.Stdout)
}

func controlSocketHandler(c *lxd.Client, control *websocket.Conn) {
	// TODO: figure out what the equivalent of signal.SIGWINCH is on
	// windows and use that; for now if you resize your terminal it just
	// won't work quite correctly.
	err := sendTermSize(control)
	if err != nil {
		shared.Debugf("error setting term size %s", err)
	}
}
