// +build windows

package main

import (
	"io"
	"os"

	"github.com/gorilla/websocket"
	"github.com/mattn/go-colorable"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
)

// Windows doesn't process ANSI sequences natively, so we wrap
// os.Stdout for improved user experience for Windows client
type wrappedWriteCloser struct {
	io.Closer
	wrapper io.Writer
}

func (wwc *wrappedWriteCloser) Write(p []byte) (int, error) {
	return wwc.wrapper.Write(p)
}

func (c *execCmd) getStdout() io.WriteCloser {
	return &wrappedWriteCloser{os.Stdout, colorable.NewColorableStdout()}
}

func (c *execCmd) controlSocketHandler(d *lxd.Client, control *websocket.Conn) {
	// TODO: figure out what the equivalent of signal.SIGWINCH is on
	// windows and use that; for now if you resize your terminal it just
	// won't work quite correctly.
	err := c.sendTermSize(control)
	if err != nil {
		shared.Debugf("error setting term size %s", err)
	}
}
