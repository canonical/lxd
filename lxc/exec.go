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

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/termios"
)

type envList []string

func (f *envList) String() string {
	return fmt.Sprint(*f)
}

func (f *envList) Set(value string) error {
	if f == nil {
		*f = make(envList, 1)
	} else {
		*f = append(*f, value)
	}
	return nil
}

type execCmd struct {
	modeFlag            string
	envArgs             envList
	forceInteractive    bool
	forceNonInteractive bool
	disableStdin        bool
}

func (c *execCmd) showByDefault() bool {
	return true
}

func (c *execCmd) usage() string {
	return i18n.G(
		`Usage: lxc exec [<remote>:]<container> [-t] [-T] [-n] [--mode=auto|interactive|non-interactive] [--env KEY=VALUE...] [--] <command line>

Execute commands in containers.

The command is executed directly using exec, so there is no shell and shell patterns (variables, file redirects, ...)
won't be understood. If you need a shell environment you need to execute the shell executable, passing the shell commands
as arguments, for example:

    lxc exec <container> -- sh -c "cd /tmp && pwd"

Mode defaults to non-interactive, interactive mode is selected if both stdin AND stdout are terminals (stderr is ignored).`)
}

func (c *execCmd) flags() {
	gnuflag.Var(&c.envArgs, "env", i18n.G("Environment variable to set (e.g. HOME=/home/foo)"))
	gnuflag.StringVar(&c.modeFlag, "mode", "auto", i18n.G("Override the terminal mode (auto, interactive or non-interactive)"))
	gnuflag.BoolVar(&c.forceInteractive, "t", false, i18n.G("Force pseudo-terminal allocation"))
	gnuflag.BoolVar(&c.forceNonInteractive, "T", false, i18n.G("Disable pseudo-terminal allocation"))
	gnuflag.BoolVar(&c.disableStdin, "n", false, i18n.G("Disable stdin (reads from /dev/null)"))
}

func (c *execCmd) sendTermSize(control *websocket.Conn) error {
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

func (c *execCmd) forwardSignal(control *websocket.Conn, sig syscall.Signal) error {
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

func (c *execCmd) run(conf *config.Config, args []string) error {
	if len(args) < 2 {
		return errArgs
	}

	if c.forceInteractive && c.forceNonInteractive {
		return fmt.Errorf(i18n.G("You can't pass -t and -T at the same time"))
	}

	if c.modeFlag != "auto" && (c.forceInteractive || c.forceNonInteractive) {
		return fmt.Errorf(i18n.G("You can't pass -t or -T at the same time as --mode"))
	}

	remote, name, err := conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	d, err := conf.GetContainerServer(remote)
	if err != nil {
		return err
	}

	/* FIXME: Default values for HOME and USER are now handled by LXD.
	   This code should be removed after most users upgraded.
	*/
	env := map[string]string{"HOME": "/root", "USER": "root"}
	if myTerm, ok := c.getTERM(); ok {
		env["TERM"] = myTerm
	}

	for _, arg := range c.envArgs {
		pieces := strings.SplitN(arg, "=", 2)
		value := ""
		if len(pieces) > 1 {
			value = pieces[1]
		}
		env[pieces[0]] = value
	}

	cfd := int(syscall.Stdin)

	var interactive bool
	if c.disableStdin {
		interactive = false
	} else if c.modeFlag == "interactive" || c.forceInteractive {
		interactive = true
	} else if c.modeFlag == "non-interactive" || c.forceNonInteractive {
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
	if c.disableStdin {
		stdin = ioutil.NopCloser(bytes.NewReader(nil))
	}

	stdout := c.getStdout()

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

	os.Exit(int(op.Metadata["return"].(float64)))
	return nil
}
