//go:build windows

package main

import (
	"io"
	"os"
	"os/signal"

	"github.com/gorilla/websocket"
	"golang.org/x/sys/windows"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

// Windows doesn't process ANSI sequences natively, so we wrap
// os.Stdout for improved user experience for Windows client
type WrappedWriteCloser struct {
	io.Closer
	wrapper io.Writer
}

// Writes the given byte slice to the underlying WrappedWriteCloser's wrapper.
func (wwc *WrappedWriteCloser) Write(p []byte) (int, error) {
	return wwc.wrapper.Write(p)
}

// Returns a hard-coded terminal type ("dumb") and a boolean value indicating the value exists.
func (c *cmdExec) getTERM() (string, bool) {
	return "dumb", true
}

// Handles interruption signals on a WebSocket control connection and forwards them to the executing program.
func (c *cmdExec) controlSocketHandler(control *websocket.Conn) {
	ch := make(chan os.Signal, 10)
	signal.Notify(ch, os.Interrupt)

	closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
	defer control.WriteMessage(websocket.CloseMessage, closeMsg)

	for {
		sig := <-ch
		switch sig {
		case os.Interrupt:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, windows.SIGINT)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", windows.SIGINT)
				return
			}

		default:
			break
		}
	}
}

// Forwards a given signal from the executing program to the control WebSocket as a JSON message.
func (c *cmdExec) forwardSignal(control *websocket.Conn, sig windows.Signal) error {
	logger.Debugf("Forwarding signal: %s", sig)

	msg := api.InstanceExecControl{}
	msg.Command = "signal"
	msg.Signal = int(sig)

	return control.WriteJSON(msg)
}
