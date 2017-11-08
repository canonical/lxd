package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strconv"
	"syscall"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/termios"
)

type consoleCmd struct {
	showLog bool
}

func (c *consoleCmd) showByDefault() bool {
	return true
}

func (c *consoleCmd) usage() string {
	return i18n.G(
		`Usage: lxc console [<remote>:]<container> [-l]

Interact with the container's console device and log.`)
}

func (c *consoleCmd) flags() {
	gnuflag.BoolVar(&c.showLog, "show-log", false, i18n.G("Retrieve the container's console log"))
}

func (c *consoleCmd) sendTermSize(control *websocket.Conn) error {
	width, height, err := termios.GetSize(int(syscall.Stdout))
	if err != nil {
		return err
	}

	logger.Debugf("Window size is now: %dx%d", width, height)

	w, err := control.NextWriter(websocket.TextMessage)
	if err != nil {
		return err
	}

	msg := api.ContainerExecControl{}
	msg.Command = "window-resize"
	msg.Args = make(map[string]string)
	msg.Args["width"] = strconv.Itoa(width)
	msg.Args["height"] = strconv.Itoa(height)

	buf, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = w.Write(buf)

	w.Close()
	return err
}

type ReadWriteCloser struct {
	io.Reader
	io.WriteCloser
}

func (c *consoleCmd) run(conf *config.Config, args []string) error {
	if len(args) < 1 {
		return errArgs
	}

	remote, name, err := conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	d, err := conf.GetContainerServer(remote)
	if err != nil {
		return err
	}

	if c.showLog {
		console := &lxd.ContainerConsoleLogArgs{}
		log, err := d.GetContainerConsoleLog(name, console)
		if err != nil {
			return err
		}

		stuff, err := ioutil.ReadAll(log)
		if err != nil {
			return err
		}

		fmt.Printf("\n"+i18n.G("Console log:")+"\n\n%s\n", string(stuff))
		return nil
	}

	cfd := int(syscall.Stdin)

	var oldttystate *termios.State
	oldttystate, err = termios.MakeRaw(cfd)
	if err != nil {
		return err
	}
	defer termios.Restore(cfd, oldttystate)

	handler := c.controlSocketHandler

	var width, height int
	width, height, err = termios.GetSize(int(syscall.Stdout))
	if err != nil {
		return err
	}

	req := api.ContainerConsolePost{
		Width:  width,
		Height: height,
	}

	consoleArgs := lxd.ContainerConsoleArgs{
		Terminal: &ReadWriteCloser{os.Stdin, os.Stdout},
		Control:  handler,
	}

	// Run the command in the container
	op, err := d.ConsoleContainer(name, req, &consoleArgs)
	if err != nil {
		return err
	}

	// Wait for the operation to complete
	err = op.Wait()
	if err != nil {
		return err
	}

	if oldttystate != nil {
		/* A bit of a special case here: we want to exit with the same code as
		 * the process inside the container, so we explicitly exit here
		 * instead of returning an error.
		 *
		 * Additionally, since os.Exit() exits without running deferred
		 * functions, we restore the terminal explicitly.
		 */
		termios.Restore(cfd, oldttystate)
	}

	return nil
}
