// +build windows

package main

import (
	"io"
	"os"
	"os/signal"

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
	ch := make(chan os.Signal, 10)
	signal.Notify(ch, os.Interrupt)

	closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
	defer control.WriteMessage(websocket.CloseMessage, closeMsg)

	for {
		sig := <-ch

		logger.Debugf("Received '%s signal', updating window geometry.", sig)
	}
}
