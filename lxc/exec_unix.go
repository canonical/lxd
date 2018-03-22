// +build !windows

package main

import (
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/shared/logger"
)

func (c *cmdExec) getStdout() io.WriteCloser {
	return os.Stdout
}

func (c *cmdExec) getTERM() (string, bool) {
	return os.LookupEnv("TERM")
}

func (c *cmdExec) controlSocketHandler(control *websocket.Conn) {
	ch := make(chan os.Signal, 10)
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
			logger.Debugf("Received '%s signal', updating window geometry.", sig)
			err := c.sendTermSize(control)
			if err != nil {
				logger.Debugf("error setting term size %s", err)
				return
			}
		case syscall.SIGTERM:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, syscall.SIGTERM)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", syscall.SIGTERM)
				return
			}
		case syscall.SIGHUP:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, syscall.SIGHUP)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", syscall.SIGHUP)
				return
			}
		case syscall.SIGINT:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, syscall.SIGINT)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", syscall.SIGINT)
				return
			}
		case syscall.SIGQUIT:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, syscall.SIGQUIT)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", syscall.SIGQUIT)
				return
			}
		case syscall.SIGABRT:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, syscall.SIGABRT)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", syscall.SIGABRT)
				return
			}
		case syscall.SIGTSTP:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, syscall.SIGTSTP)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", syscall.SIGTSTP)
				return
			}
		case syscall.SIGTTIN:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, syscall.SIGTTIN)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", syscall.SIGTTIN)
				return
			}
		case syscall.SIGTTOU:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, syscall.SIGTTOU)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", syscall.SIGTTOU)
				return
			}
		case syscall.SIGUSR1:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, syscall.SIGUSR1)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", syscall.SIGUSR1)
				return
			}
		case syscall.SIGUSR2:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, syscall.SIGUSR2)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", syscall.SIGUSR2)
				return
			}
		case syscall.SIGSEGV:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, syscall.SIGSEGV)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", syscall.SIGSEGV)
				return
			}
		case syscall.SIGCONT:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, syscall.SIGCONT)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", syscall.SIGCONT)
				return
			}
		default:
			break
		}
	}
}
