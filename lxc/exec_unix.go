//go:build !windows

package main

import (
	"os"
	"os/signal"

	"github.com/gorilla/websocket"
	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

func (c *cmdExec) getTERM() (string, bool) {
	return os.LookupEnv("TERM")
}

func (c *cmdExec) controlSocketHandler(control *websocket.Conn) {
	signals := []os.Signal{
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
		unix.SIGCONT,
	}

	ch := make(chan os.Signal, len(signals))
	signal.Notify(ch, signals...)
	defer signal.Stop(ch)

	closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
	defer func() { _ = control.WriteMessage(websocket.CloseMessage, closeMsg) }()

	for {
		sig := <-ch

		switch sig {
		case unix.SIGWINCH:
			if !c.interactive {
				// Don't send SIGWINCH to non-interactive, this can lead to console corruption/crashes.
				continue
			}

			err := c.sendTermSize(control)
			if err != nil {
				logger.Debugf("Error setting term size: %v", err)
				return
			}

		case unix.SIGHUP:
			file, err := os.OpenFile("/dev/tty", os.O_RDONLY|unix.O_NOCTTY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0666)
			if err == nil {
				_ = file.Close()
				err = c.forwardSignal(control, unix.SIGHUP)
			} else {
				err = c.forwardSignal(control, unix.SIGTERM)
				sig = unix.SIGTERM
			}

			if err != nil {
				logger.Debugf("Failed forwarding signal %q: %v", sig, err)
				return
			}

		default:
			err := c.forwardSignal(control, sig.(unix.Signal))
			if err != nil {
				logger.Debugf("Failed forwarding signal %q: %v", sig, err)
				return
			}
		}
	}
}

func (c *cmdExec) forwardSignal(control *websocket.Conn, sig unix.Signal) error {
	msg := api.InstanceExecControl{}
	msg.Command = "signal"
	msg.Signal = int(sig)

	return control.WriteJSON(msg)
}
