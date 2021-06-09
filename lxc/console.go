package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"

	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/termios"
)

type cmdConsole struct {
	global *cmdGlobal

	flagShowLog bool
	flagType    string
}

func (c *cmdConsole) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("console", i18n.G("[<remote>:]<instance>"))
	cmd.Short = i18n.G("Attach to instance consoles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Attach to instance consoles

This command allows you to interact with the boot console of an instance
as well as retrieve past log entries from it.`))

	cmd.RunE = c.Run
	cmd.Flags().BoolVar(&c.flagShowLog, "show-log", false, i18n.G("Retrieve the instance's console log"))
	cmd.Flags().StringVarP(&c.flagType, "type", "t", "console", i18n.G("Type of connection to establish: 'console' for serial console, 'vga' for SPICE graphical output")+"``")

	return cmd
}

func (c *cmdConsole) sendTermSize(control *websocket.Conn) error {
	width, height, err := termios.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return err
	}

	logger.Debugf("Window size is now: %dx%d", width, height)

	msg := api.InstanceExecControl{}
	msg.Command = "window-resize"
	msg.Args = make(map[string]string)
	msg.Args["width"] = strconv.Itoa(width)
	msg.Args["height"] = strconv.Itoa(height)

	return control.WriteJSON(msg)
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

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Validate flags.
	if !shared.StringInSlice(c.flagType, []string{"console", "vga"}) {
		return fmt.Errorf("Unknown output type %q", c.flagType)
	}

	// Connect to LXD
	remote, name, err := conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	d, err := conf.GetInstanceServer(remote)
	if err != nil {
		return err
	}

	// Show the current log if requested
	if c.flagShowLog {
		if c.flagType != "console" {
			return fmt.Errorf("The --show-log flag is only supported for by 'console' output type")
		}

		console := &lxd.InstanceConsoleLogArgs{}
		log, err := d.GetInstanceConsoleLog(name, console)
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

	return c.Console(d, name)
}

func (c *cmdConsole) Console(d lxd.InstanceServer, name string) error {
	if c.flagType == "" {
		c.flagType = "console"
	}
	switch c.flagType {
	case "console":
		return c.console(d, name)
	case "vga":
		return c.vga(d, name)
	}
	return fmt.Errorf("Unknown console type %q", c.flagType)
}

func (c *cmdConsole) console(d lxd.InstanceServer, name string) error {
	// Configure the terminal
	cfd := int(os.Stdin.Fd())

	oldTTYstate, err := termios.MakeRaw(cfd)
	if err != nil {
		return err
	}
	defer termios.Restore(cfd, oldTTYstate)

	handler := c.controlSocketHandler

	var width, height int
	width, height, err = termios.GetSize(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}

	// Prepare the remote console
	req := api.InstanceConsolePost{
		Width:  width,
		Height: height,
		Type:   "console",
	}

	consoleDisconnect := make(chan bool)
	sendDisconnect := make(chan bool)
	defer close(sendDisconnect)

	consoleArgs := lxd.InstanceConsoleArgs{
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

	// Attach to the instance console
	op, err := d.ConsoleInstance(name, req, &consoleArgs)
	if err != nil {
		return err
	}

	// Wait for the operation to complete
	err = op.Wait()
	if err != nil {
		return err
	}

	return nil
}

func (c *cmdConsole) vga(d lxd.InstanceServer, name string) error {
	var err error
	conf := c.global.conf

	// We currently use the control websocket just to abort in case of errors.
	controlDone := make(chan struct{}, 1)
	handler := func(control *websocket.Conn) {
		<-controlDone
		closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
		control.WriteMessage(websocket.CloseMessage, closeMsg)
	}

	// Prepare the remote console.
	req := api.InstanceConsolePost{
		Type: "vga",
	}

	consoleDisconnect := make(chan bool)
	sendDisconnect := make(chan bool)
	defer close(sendDisconnect)

	consoleArgs := lxd.InstanceConsoleArgs{
		Control:           handler,
		ConsoleDisconnect: consoleDisconnect,
	}

	go func() {
		<-sendDisconnect
		close(consoleDisconnect)
	}()

	var socket string
	var listener net.Listener
	if runtime.GOOS != "windows" {
		// Create a temporary unix socket mirroring the instance's spice socket.
		if !shared.PathExists(conf.ConfigPath("sockets")) {
			err := os.MkdirAll(conf.ConfigPath("sockets"), 0700)
			if err != nil {
				return err
			}
		}

		// Generate a random file name.
		path, err := ioutil.TempFile(conf.ConfigPath("sockets"), "*.spice")
		if err != nil {
			return err
		}
		path.Close()

		err = os.Remove(path.Name())
		if err != nil {
			return err
		}

		// Listen on the socket.
		listener, err = net.Listen("unix", path.Name())
		if err != nil {
			return err
		}
		defer listener.Close()
		defer os.Remove(path.Name())

		socket = fmt.Sprintf("spice+unix://%s", path.Name())
	} else {
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return err
		}
		defer listener.Close()

		addr := listener.Addr().(*net.TCPAddr)
		socket = fmt.Sprintf("spice://127.0.0.1:%d", addr.Port)
	}

	op, connect, err := d.ConsoleInstanceDynamic(name, req, &consoleArgs)
	if err != nil {
		return err
	}

	// Handle connections to the socket.
	go func() {
		count := 0

		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			count++

			go func(conn io.ReadWriteCloser) {
				err = connect(conn)
				if err != nil {
					sendDisconnect <- true
				}

				count--
				if count == 0 {
					sendDisconnect <- true
				}
			}(conn)
		}
	}()

	// Use either spicy or remote-viewer if available.
	remoteViewer := c.findCommand("remote-viewer")
	spicy := c.findCommand("spicy")

	if remoteViewer != "" || spicy != "" {
		var cmd *exec.Cmd
		if remoteViewer != "" {
			cmd = exec.Command(remoteViewer, socket)
		} else {
			cmd = exec.Command(spicy, fmt.Sprintf("--uri=%s", socket))
		}

		// Start the command.
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Start()

		defer func() {
			if cmd.Process == nil {
				return
			}

			cmd.Process.Kill()
		}()
	} else {
		fmt.Println(i18n.G("LXD automatically uses either spicy or remote-viewer when present."))
		fmt.Println(i18n.G("As neither could be found, the raw SPICE socket can be found at:"))
		fmt.Printf("  %s\n", socket)
	}

	// Wait for the operation to complete.
	err = op.Wait()
	if err != nil {
		return err
	}

	return nil
}
