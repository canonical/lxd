package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/termios"
)

type envFlag []string

func (f *envFlag) String() string {
	return fmt.Sprint(*f)
}

func (f *envFlag) Set(value string) error {
	if f == nil {
		*f = make(envFlag, 1)
	} else {
		*f = append(*f, value)
	}
	return nil
}

type execCmd struct {
	modeFlag string
	envArgs  envFlag
}

func (c *execCmd) showByDefault() bool {
	return true
}

func (c *execCmd) usage() string {
	return i18n.G(
		`Execute the specified command in a container.

lxc exec [remote:]container [--mode=auto|interactive|non-interactive] [--env EDITOR=/usr/bin/vim]... <command>`)
}

func (c *execCmd) flags() {
	gnuflag.Var(&c.envArgs, "env", i18n.G("An environment variable of the form HOME=/home/foo"))
	gnuflag.StringVar(&c.modeFlag, "mode", "auto", i18n.G("Override the terminal mode (auto, interactive or non-interactive)"))
}

func (c *execCmd) sendTermSize(control *websocket.Conn) error {
	width, height, err := termios.GetSize(int(syscall.Stdout))
	if err != nil {
		return err
	}

	shared.Debugf("Window size is now: %dx%d", width, height)

	w, err := control.NextWriter(websocket.TextMessage)
	if err != nil {
		return err
	}

	msg := shared.ContainerExecControl{}
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

func (c *execCmd) run(config *lxd.Config, args []string) error {
	if len(args) < 2 {
		return errArgs
	}

	remote, name := config.ParseRemoteAndContainer(args[0])
	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	env := map[string]string{"HOME": "/root", "USER": "root"}
	myEnv := os.Environ()
	for _, ent := range myEnv {
		if strings.HasPrefix(ent, "TERM=") {
			env["TERM"] = ent[len("TERM="):]
		}
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
	if c.modeFlag == "interactive" {
		interactive = true
	} else if c.modeFlag == "non-interactive" {
		interactive = false
	} else {
		interactive = termios.IsTerminal(cfd)
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

	stdout := c.getStdout()
	ret, err := d.Exec(name, args[1:], env, os.Stdin, stdout, os.Stderr, handler)
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

	/* we get the result of waitpid() here so we need to transform it */
	os.Exit(ret >> 8)
	return fmt.Errorf(i18n.G("unreachable return reached"))
}
