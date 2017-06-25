// +build windows

package main

import (
	"io"
	"os"

	"github.com/gorilla/websocket"
	"github.com/mattn/go-colorable"

	"github.com/lxc/lxd/shared/logger"
)

// Windows doesn't process ANSI sequences natively, so we wrap
// os.Stdout for improved user experience for Windows client
type WrappedWriteCloser struct {
	io.Closer
	wrapper io.Writer
}

func (wwc *WrappedWriteCloser) Write(p []byte) (int, error) {
	return wwc.wrapper.Write(p)
}

func (c *execCmd) getStdout() io.WriteCloser {
	return &WrappedWriteCloser{os.Stdout, colorable.NewColorableStdout()}
}

func (c *execCmd) getTERM() (string, bool) {
	return "dumb", true
}

func (c *execCmd) controlSocketHandler(control *websocket.Conn) {
	// TODO: figure out what the equivalent of signal.SIGWINCH is on
	// windows and use that; for now if you resize your terminal it just
	// won't work quite correctly.
	err := c.sendTermSize(control)
	if err != nil {
		logger.Debugf("error setting term size %s", err)
	}
}
