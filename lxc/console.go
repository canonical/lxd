package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strconv"

	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/shared/api"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/termios"
)

type cmdConsole struct {
	global *cmdGlobal

	flagShowLog bool
}

func (c *cmdConsole) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("console [<remote>:]<container>")
	cmd.Short = i18n.G("Attach to container consoles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Attach to container consoles

This command allows you to interact with the boot console of a container
as well as retrieve past log entries from it.`))

	cmd.RunE = c.Run
	cmd.Flags().BoolVar(&c.flagShowLog, "show-log", false, i18n.G("Retrieve the container's console log"))

	return cmd
}

func (c *cmdConsole) sendTermSize(control *websocket.Conn) error {
	width, height, err := termios.GetSize(int(os.Stdout.Fd()))
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

type readWriteCloser struct {
	io.Reader
	io.WriteCloser
}

type stdinMirror struct {
	r                 io.Reader
	consoleDisconnect chan<- bool
	foundEscape       *bool
}

// The pty has been switched to raw mode so we will only ever read a single
// byte. The buffer size is therefore uninteresting to us.
func (er stdinMirror) Read(p []byte) (int, error) {
	n, err := er.r.Read(p)

	v := rune(p[0])
	if v == '\u0001' && !*er.foundEscape {
		*er.foundEscape = true
		return 0, err
	}

	if v == 'q' && *er.foundEscape {
		select {
		case er.consoleDisconnect <- true:
			return 0, err
		default:
			return 0, err
		}
	}

	*er.foundEscape = false
	return n, err
}

func (c *cmdConsole) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Connect to LXD
	remote, name, err := conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	d, err := conf.GetContainerServer(remote)
	if err != nil {
		return err
	}

	// Show the current log if requested
	if c.flagShowLog {
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

	// Configure the terminal
	cfd := int(os.Stdin.Fd())

	var oldttystate *termios.State
	oldttystate, err = termios.MakeRaw(cfd)
	if err != nil {
		return err
	}
	defer termios.Restore(cfd, oldttystate)

	handler := c.controlSocketHandler

	var width, height int
	width, height, err = termios.GetSize(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}

	// Prepare the remote console
	req := api.ContainerConsolePost{
		Width:  width,
		Height: height,
	}

	consoleDisconnect := make(chan bool)
	sendDisconnect := make(chan bool)
	defer close(sendDisconnect)

	consoleArgs := lxd.ContainerConsoleArgs{
		Terminal: &readWriteCloser{stdinMirror{os.Stdin,
			sendDisconnect, new(bool)}, os.Stdout},
		Control:           handler,
		ConsoleDisconnect: consoleDisconnect,
	}

	go func() {
		<-sendDisconnect
		close(consoleDisconnect)
	}()

	fmt.Printf(i18n.G("To detach from the console, press: <ctrl>+a q") + "\n\r")

	// Attach to the container console
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
