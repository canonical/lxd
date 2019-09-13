// +build !windows

package main

import (
	"encoding/json"
	"os"
	"os/signal"

	"github.com/gorilla/websocket"
	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

func (c *cmdExec) getTERM() (string, bool) {
	return os.LookupEnv("TERM")
}

func (c *cmdExec) controlSocketHandler(control *websocket.Conn) {
	ch := make(chan os.Signal, 10)
	signal.Notify(ch,
		unix.SIGWINCH,
		unix.SIGTERM,
		unix.SIGHUP,
		unix.SIGINT,
		unix.SIGQUIT,
		unix.SIGABRT,
		unix.SIGTSTP,
		unix.SIGTTIN,
		unix.SIGTTOU,
		unix.SIGUSR1,
		unix.SIGUSR2,
		unix.SIGSEGV,
		unix.SIGCONT)

	closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
	defer control.WriteMessage(websocket.CloseMessage, closeMsg)

	for {
		sig := <-ch
		switch sig {
		case unix.SIGWINCH:
			logger.Debugf("Received '%s signal', updating window geometry.", sig)
			err := c.sendTermSize(control)
			if err != nil {
				logger.Debugf("error setting term size %s", err)
				return
			}
		case unix.SIGTERM:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, unix.SIGTERM)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", unix.SIGTERM)
				return
			}
		case unix.SIGHUP:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, unix.SIGHUP)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", unix.SIGHUP)
				return
			}
		case unix.SIGINT:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, unix.SIGINT)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", unix.SIGINT)
				return
			}
		case unix.SIGQUIT:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, unix.SIGQUIT)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", unix.SIGQUIT)
				return
			}
		case unix.SIGABRT:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, unix.SIGABRT)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", unix.SIGABRT)
				return
			}
		case unix.SIGTSTP:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, unix.SIGTSTP)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", unix.SIGTSTP)
				return
			}
		case unix.SIGTTIN:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, unix.SIGTTIN)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", unix.SIGTTIN)
				return
			}
		case unix.SIGTTOU:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, unix.SIGTTOU)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", unix.SIGTTOU)
				return
			}
		case unix.SIGUSR1:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, unix.SIGUSR1)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", unix.SIGUSR1)
				return
			}
		case unix.SIGUSR2:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, unix.SIGUSR2)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", unix.SIGUSR2)
				return
			}
		case unix.SIGSEGV:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, unix.SIGSEGV)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", unix.SIGSEGV)
				return
			}
		case unix.SIGCONT:
			logger.Debugf("Received '%s signal', forwarding to executing program.", sig)
			err := c.forwardSignal(control, unix.SIGCONT)
			if err != nil {
				logger.Debugf("Failed to forward signal '%s'.", unix.SIGCONT)
				return
			}
		default:
			break
		}
	}
}

func (c *cmdExec) forwardSignal(control *websocket.Conn, sig unix.Signal) error {
	logger.Debugf("Forwarding signal: %s", sig)

	w, err := control.NextWriter(websocket.TextMessage)
	if err != nil {
		return err
	}

	msg := api.InstanceExecControl{}
	msg.Command = "signal"
	msg.Signal = int(sig)

	buf, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = w.Write(buf)

	w.Close()
	return err
}
