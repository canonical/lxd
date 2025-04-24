package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"sync"
	"syscall"

	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/termios"
)

type cmdConsole struct {
	global *cmdGlobal

	flagShowLog bool
	flagType    string
}

func (c *cmdConsole) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("console", i18n.G("[<remote>:]<instance>"))
	cmd.Short = i18n.G("Attach to instance consoles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Attach to instance consoles

This command allows you to interact with the boot console of an instance
as well as retrieve past log entries from it.`))

	cmd.RunE = c.run
	cmd.Flags().BoolVar(&c.flagShowLog, "show-log", false, i18n.G("Retrieve the container's console log"))
	cmd.Flags().StringVarP(&c.flagType, "type", "t", "console", i18n.G("Type of connection to establish: 'console' for serial console, 'vga' for SPICE graphical output")+"``")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return c.global.cmpTopLevelResource("instance", toComplete)
	}

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
	consoleDisconnect chan struct{}
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
		close(er.consoleDisconnect)
		return 0, err
	}

	*er.foundEscape = false
	return n, err
}

func (c *cmdConsole) run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Validate flags.
	if !shared.ValueInSlice(c.flagType, []string{"console", "vga"}) {
		return fmt.Errorf(i18n.G("Unknown output type %q"), c.flagType)
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
			return errors.New(i18n.G("The --show-log flag is only supported for by 'console' output type"))
		}

		console := &lxd.InstanceConsoleLogArgs{}
		log, err := d.GetInstanceConsoleLog(name, console)
		if err != nil {
			return err
		}

		stuff, err := io.ReadAll(log)
		if err != nil {
			return err
		}

		if len(stuff) > 0 {
			fmt.Printf("%s\n", string(stuff))
		} else {
			fmt.Println("No new messages")
		}

		return nil
	}

	return c.runConsole(d, name)
}

func (c *cmdConsole) runConsole(d lxd.InstanceServer, name string) error {
	if c.flagType == "" {
		c.flagType = "console"
	}

	switch c.flagType {
	case "console":
		return c.console(d, name)
	case "vga":
		return c.vga(d, name)
	}

	return fmt.Errorf(i18n.G("Unknown console type %q"), c.flagType)
}

func (c *cmdConsole) console(d lxd.InstanceServer, name string) error {
	// Configure the terminal
	cfd := int(os.Stdin.Fd())

	oldTTYstate, err := termios.MakeRaw(cfd)
	if err != nil {
		return err
	}

	defer func() { _ = termios.Restore(cfd, oldTTYstate) }()

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
	manualDisconnect := make(chan struct{})

	sendDisconnect := make(chan struct{})
	defer close(sendDisconnect)

	consoleArgs := lxd.InstanceConsoleArgs{
		Terminal: &readWriteCloser{stdinMirror{os.Stdin,
			manualDisconnect, new(bool)}, os.Stdout},
		Control:           handler,
		ConsoleDisconnect: consoleDisconnect,
	}

	go func() {
		select {
		case <-sendDisconnect:
		case <-manualDisconnect:
		}

		close(consoleDisconnect)
	}()

	fmt.Printf("%s\n\r", i18n.G("To detach from the console, press: <ctrl>+a q"))

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

	// Create a context that is canceled on signal reception.
	// This is used to enable the function to execute any cleanup defer statements
	// before exiting.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	// We currently use the control websocket just to abort in case of errors.
	controlDone := make(chan struct{}, 1)
	handler := func(control *websocket.Conn) {
		<-controlDone
		closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
		_ = control.WriteMessage(websocket.CloseMessage, closeMsg)
	}

	// Prepare the remote console.
	req := api.InstanceConsolePost{
		Type: "vga",
	}

	chDisconnect := make(chan bool)
	chViewer := make(chan struct{})

	consoleArgs := lxd.InstanceConsoleArgs{
		Control:           handler,
		ConsoleDisconnect: chDisconnect,
	}

	// Setup local socket.
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
		path, err := os.CreateTemp(conf.ConfigPath("sockets"), "*.spice")
		if err != nil {
			return err
		}

		_ = path.Close()

		err = os.Remove(path.Name())
		if err != nil {
			return err
		}

		// Listen on the socket.
		lc := net.ListenConfig{}
		listener, err = lc.Listen(ctx, "unix", path.Name())
		if err != nil {
			return err
		}

		defer func() { _ = os.Remove(path.Name()) }()

		socket = "spice+unix://" + path.Name()
	} else {
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return err
		}

		addr, ok := listener.Addr().(*net.TCPAddr)
		if !ok {
			return errors.New("Failed to get TCP listen address")
		}

		socket = "spice://127.0.0.1:" + fmt.Sprint(addr.Port)
	}

	// Clean everything up when the viewer is done.
	go func() {
		<-chViewer
		_ = listener.Close()
		close(chDisconnect)
	}()

	// Spawn the remote console.
	op, connect, err := d.ConsoleInstanceDynamic(name, req, &consoleArgs)
	if err != nil {
		close(chViewer)
		return err
	}

	// Handle connections to the socket.
	wgConnections := sync.WaitGroup{}
	chConnected := make(chan struct{})
	go func() {
		hasConnected := false

		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			if !hasConnected {
				hasConnected = true
				close(chConnected)
			}

			wgConnections.Add(1)

			go func(conn io.ReadWriteCloser) {
				defer wgConnections.Done()

				err = connect(conn)
				if err != nil {
					return
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
			cmd = exec.Command(spicy, "--uri="+socket)
		}

		// Start the command.
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err := cmd.Start()
		if err != nil {
			return fmt.Errorf(i18n.G("Failed starting command: %w"), err)
		}

		// Handle the command exiting.
		go func() {
			_ = cmd.Wait()
			close(chViewer)
		}()

		// Kill the viewer on remote disconnection.
		go func() {
			<-chConnected
			wgConnections.Wait()

			if cmd.Process == nil {
				return
			}

			_ = cmd.Process.Kill()
		}()
	} else {
		fmt.Println(i18n.G("LXD automatically uses either spicy or remote-viewer when present."))
		fmt.Println(i18n.G("As neither could be found, the raw SPICE socket can be found at:"))
		fmt.Printf("  %s\n", socket)

		// Wait for all connections to complete.
		<-chConnected
		wgConnections.Wait()
		close(chViewer)
	}

	// Wait for the operation to complete.
	err = op.Wait()
	if err != nil {
		return err
	}

	return nil
}
