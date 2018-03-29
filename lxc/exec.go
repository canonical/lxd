package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/shared/api"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/termios"
)

type cmdExec struct {
	global *cmdGlobal

	flagMode                string
	flagEnvironment         []string
	flagForceInteractive    bool
	flagForceNonInteractive bool
	flagDisableStdin        bool
}

func (c *cmdExec) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("exec [<remote>:]<container> [flags] [--] <command line>")
	cmd.Short = i18n.G("Execute commands in containers")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Execute commands in containers

The command is executed directly using exec, so there is no shell and
shell patterns (variables, file redirects, ...) won't be understood.
If you need a shell environment you need to execute the shell
executable, passing the shell commands as arguments, for example:

  lxc exec <container> -- sh -c "cd /tmp && pwd"

Mode defaults to non-interactive, interactive mode is selected if both stdin AND stdout are terminals (stderr is ignored).`))

	cmd.RunE = c.Run
	cmd.Flags().StringArrayVar(&c.flagEnvironment, "env", nil, i18n.G("Environment variable to set (e.g. HOME=/home/foo)")+"``")
	cmd.Flags().StringVar(&c.flagMode, "mode", "auto", i18n.G("Override the terminal mode (auto, interactive or non-interactive)")+"``")
	cmd.Flags().BoolVarP(&c.flagForceInteractive, "force-interactive", "t", false, i18n.G("Force pseudo-terminal allocation"))
	cmd.Flags().BoolVarP(&c.flagForceNonInteractive, "force-noninteractive", "T", false, i18n.G("Disable pseudo-terminal allocation"))
	cmd.Flags().BoolVarP(&c.flagDisableStdin, "disable-stdin", "n", false, i18n.G("Disable stdin (reads from /dev/null)"))

	return cmd
}

func (c *cmdExec) sendTermSize(control *websocket.Conn) error {
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

func (c *cmdExec) forwardSignal(control *websocket.Conn, sig syscall.Signal) error {
	logger.Debugf("Forwarding signal: %s", sig)

	w, err := control.NextWriter(websocket.TextMessage)
	if err != nil {
		return err
	}

	msg := api.ContainerExecControl{}
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

func (c *cmdExec) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 2, -1)
	if exit {
		return err
	}

	if c.flagForceInteractive && c.flagForceNonInteractive {
		return fmt.Errorf(i18n.G("You can't pass -t and -T at the same time"))
	}

	if c.flagMode != "auto" && (c.flagForceInteractive || c.flagForceNonInteractive) {
		return fmt.Errorf(i18n.G("You can't pass -t or -T at the same time as --mode"))
	}

	// Connect to the daemon
	remote, name, err := conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	d, err := conf.GetContainerServer(remote)
	if err != nil {
		return err
	}

	// Set the environment
	env := map[string]string{}
	myTerm, ok := c.getTERM()
	if ok {
		env["TERM"] = myTerm
	}

	for _, arg := range c.flagEnvironment {
		pieces := strings.SplitN(arg, "=", 2)
		value := ""
		if len(pieces) > 1 {
			value = pieces[1]
		}
		env[pieces[0]] = value
	}

	// Configure the terminal
	cfd := int(syscall.Stdin)

	var interactive bool
	if c.flagDisableStdin {
		interactive = false
	} else if c.flagMode == "interactive" || c.flagForceInteractive {
		interactive = true
	} else if c.flagMode == "non-interactive" || c.flagForceNonInteractive {
		interactive = false
	} else {
		interactive = termios.IsTerminal(cfd) && termios.IsTerminal(int(syscall.Stdout))
	}

	var oldttystate *termios.State
	if interactive {
		oldttystate, err = termios.MakeRaw(cfd)
		if err != nil {
			return err
		}
		defer termios.Restore(cfd, oldttystate)
	}

	handler := c.controlSocketHandler
	if !interactive {
		handler = nil
	}

	var width, height int
	if interactive {
		width, height, err = termios.GetSize(int(syscall.Stdout))
		if err != nil {
			return err
		}
	}

	var stdin io.ReadCloser
	stdin = os.Stdin
	if c.flagDisableStdin {
		stdin = ioutil.NopCloser(bytes.NewReader(nil))
	}

	stdout := c.getStdout()

	// Prepare the command
	req := api.ContainerExecPost{
		Command:     args[1:],
		WaitForWS:   true,
		Interactive: interactive,
		Environment: env,
		Width:       width,
		Height:      height,
	}

	execArgs := lxd.ContainerExecArgs{
		Stdin:    stdin,
		Stdout:   stdout,
		Stderr:   os.Stderr,
		Control:  handler,
		DataDone: make(chan bool),
	}

	// Run the command in the container
	op, err := d.ExecContainer(name, req, &execArgs)
	if err != nil {
		return err
	}

	// Wait for the operation to complete
	err = op.Wait()
	if err != nil {
		return err
	}
	opAPI := op.Get()

	// Wait for any remaining I/O to be flushed
	<-execArgs.DataDone

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

	os.Exit(int(opAPI.Metadata["return"].(float64)))
	return nil
}
