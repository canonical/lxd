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
	signal.Notify(ch,
		syscall.SIGWINCH,
		syscall.SIGTERM,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGQUIT,
		syscall.SIGABRT,
		syscall.SIGTSTP,
		syscall.SIGTTIN,
		syscall.SIGTTOU,
		syscall.SIGUSR1,
		syscall.SIGUSR2,
		syscall.SIGSEGV,
		syscall.SIGCONT)

	closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
	defer control.WriteMessage(websocket.CloseMessage, closeMsg)

	for {
		sig := <-ch
		switch sig {
		case syscall.SIGWINCH:
			shared.LogDebugf("Received '%s signal', updating window geometry.", sig)
			err := c.sendTermSize(control)
			if err != nil {
				shared.LogDebugf("error setting term size %s", err)
				return
			}
		case syscall.SIGTERM:
			shared.LogDebugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, syscall.SIGTERM)
			if err != nil {
				shared.LogDebugf("Failed to forward signal '%s'.", syscall.SIGTERM)
				return
			}
		case syscall.SIGHUP:
			shared.LogDebugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, syscall.SIGHUP)
			if err != nil {
				shared.LogDebugf("Failed to forward signal '%s'.", syscall.SIGHUP)
				return
			}
		case syscall.SIGINT:
			shared.LogDebugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, syscall.SIGINT)
			if err != nil {
				shared.LogDebugf("Failed to forward signal '%s'.", syscall.SIGINT)
				return
			}
		case syscall.SIGQUIT:
			shared.LogDebugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, syscall.SIGQUIT)
			if err != nil {
				shared.LogDebugf("Failed to forward signal '%s'.", syscall.SIGQUIT)
				return
			}
		case syscall.SIGABRT:
			shared.LogDebugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, syscall.SIGABRT)
			if err != nil {
				shared.LogDebugf("Failed to forward signal '%s'.", syscall.SIGABRT)
				return
			}
		case syscall.SIGTSTP:
			shared.LogDebugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, syscall.SIGTSTP)
			if err != nil {
				shared.LogDebugf("Failed to forward signal '%s'.", syscall.SIGTSTP)
				return
			}
		case syscall.SIGTTIN:
			shared.LogDebugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, syscall.SIGTTIN)
			if err != nil {
				shared.LogDebugf("Failed to forward signal '%s'.", syscall.SIGTTIN)
				return
			}
		case syscall.SIGTTOU:
			shared.LogDebugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, syscall.SIGTTOU)
			if err != nil {
				shared.LogDebugf("Failed to forward signal '%s'.", syscall.SIGTTOU)
				return
			}
		case syscall.SIGUSR1:
			shared.LogDebugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, syscall.SIGUSR1)
			if err != nil {
				shared.LogDebugf("Failed to forward signal '%s'.", syscall.SIGUSR1)
				return
			}
		case syscall.SIGUSR2:
			shared.LogDebugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, syscall.SIGUSR2)
			if err != nil {
				shared.LogDebugf("Failed to forward signal '%s'.", syscall.SIGUSR2)
				return
			}
		case syscall.SIGSEGV:
			shared.LogDebugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, syscall.SIGSEGV)
			if err != nil {
				shared.LogDebugf("Failed to forward signal '%s'.", syscall.SIGSEGV)
				return
			}
		case syscall.SIGCONT:
			shared.LogDebugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, syscall.SIGCONT)
			if err != nil {
				shared.LogDebugf("Failed to forward signal '%s'.", syscall.SIGCONT)
				return
			}
		default:
			break
		}
	}
}
