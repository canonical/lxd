package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"

	lxd "github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/termios"
)

type cmdExec struct {
	global *cmdGlobal

	flagMode                string
	flagEnvironment         []string
	flagForceInteractive    bool
	flagForceNonInteractive bool
	flagDisableStdin        bool
	flagUser                uint32
	flagGroup               uint32
	flagCwd                 string

	interactive bool
}

func (c *cmdExec) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("exec", i18n.G("[<remote>:]<instance> [flags] [--] <command line>"))
	cmd.Short = i18n.G("Execute commands in instances")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Execute commands in instances

The command is executed directly using exec, so there is no shell and
shell patterns (variables, file redirects, ...) won't be understood.
If you need a shell environment you need to execute the shell
executable, passing the shell commands as arguments, for example:

  lxc exec <instance> -- sh -c "cd /tmp && pwd"

For interactive sessions, a convenient 'shell' alias is provided to
spawn a login shell inside the instance:

  lxc shell <instance>

This 'shell' alias is a shorthand for:

  lxc exec <instance> -- su -l

Note: due to using 'su -l', most environment variables will be reset.

Mode defaults to non-interactive, interactive mode is selected if both stdin AND stdout are terminals (stderr is ignored).`))

	cmd.RunE = c.run
	cmd.Flags().StringArrayVar(&c.flagEnvironment, "env", nil, i18n.G("Environment variable to set (e.g. HOME=/home/foo)")+"``")
	cmd.Flags().StringVar(&c.flagMode, "mode", "auto", i18n.G("Override the terminal mode (auto, interactive or non-interactive)")+"``")
	cmd.Flags().BoolVarP(&c.flagForceInteractive, "force-interactive", "t", false, i18n.G("Force pseudo-terminal allocation"))
	cmd.Flags().BoolVarP(&c.flagForceNonInteractive, "force-noninteractive", "T", false, i18n.G("Disable pseudo-terminal allocation"))
	cmd.Flags().BoolVarP(&c.flagDisableStdin, "disable-stdin", "n", false, i18n.G("Disable stdin (reads from /dev/null)"))
	cmd.Flags().Uint32Var(&c.flagUser, "user", 0, i18n.G("User ID to run the command as (default 0)")+"``")
	cmd.Flags().Uint32Var(&c.flagGroup, "group", 0, i18n.G("Group ID to run the command as (default 0)")+"``")
	cmd.Flags().StringVar(&c.flagCwd, "cwd", "", i18n.G("Directory to run the command in (default /root)")+"``")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstancesAction(toComplete, "exec", false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdExec) sendTermSize(control *websocket.Conn) error {
	width, height, err := termios.GetSize(getStdoutFd())
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

func (c *cmdExec) run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, -1)
	if exit {
		return err
	}

	if c.flagForceInteractive && c.flagForceNonInteractive {
		return errors.New(i18n.G("You can't pass -t and -T at the same time"))
	}

	if c.flagMode != "auto" && (c.flagForceInteractive || c.flagForceNonInteractive) {
		return errors.New(i18n.G("You can't pass -t or -T at the same time as --mode"))
	}

	// Connect to the daemon
	remote, name, err := conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	// Check security privilege of instance, warn about privileged instance
	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}
	resource := resources[0]
	resp, _, err := resource.server.GetInstance(name)
	if err != nil {
		return err
	}
	value := resp.Config["security.privileged"]
	if value == "true" {
		logger.Warnf("%s is a privileged instance (security.privileged: true), so snapd/systemd won't work on noble. "+
			"To fix this, please consider using LXD VMs if you need privileged security.", name)
	}

	d, err := conf.GetInstanceServer(remote)
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
		variable, value, found := strings.Cut(arg, "=")
		if found {
			env[variable] = value
		}
	}

	// Configure the terminal
	stdinFd := getStdinFd()
	stdoutFd := getStdoutFd()

	stdinTerminal := termios.IsTerminal(stdinFd)
	stdoutTerminal := termios.IsTerminal(stdoutFd)

	// Determine interaction mode
	if c.flagDisableStdin {
		c.interactive = false
	} else if c.flagMode == "interactive" || c.flagForceInteractive {
		c.interactive = true
	} else if c.flagMode == "non-interactive" || c.flagForceNonInteractive {
		c.interactive = false
	} else {
		c.interactive = stdinTerminal && stdoutTerminal
	}

	// Record terminal state
	var oldttystate *termios.State
	if c.interactive && stdinTerminal {
		oldttystate, err = termios.MakeRaw(stdinFd)
		if err != nil {
			return err
		}

		defer func() { _ = termios.Restore(stdinFd, oldttystate) }()
	}

	// Setup interactive console handler
	handler := c.controlSocketHandler

	// Grab current terminal dimensions
	var width, height int
	if stdoutTerminal {
		width, height, err = termios.GetSize(getStdoutFd())
		if err != nil {
			return err
		}
	}

	var stdin io.Reader
	stdin = os.Stdin
	if c.flagDisableStdin {
		stdin = bytes.NewReader(nil)
	}

	stdout := getStdout()

	// Prepare the command
	req := api.InstanceExecPost{
		Command:     args[1:],
		WaitForWS:   true,
		Interactive: c.interactive,
		Environment: env,
		Width:       width,
		Height:      height,
		User:        c.flagUser,
		Group:       c.flagGroup,
		Cwd:         c.flagCwd,
	}

	execArgs := lxd.InstanceExecArgs{
		Stdin:    stdin,
		Stdout:   stdout,
		Stderr:   os.Stderr,
		Control:  handler,
		DataDone: make(chan bool),
	}

	// Run the command in the instance
	op, err := d.ExecInstance(name, req, &execArgs)
	if err != nil {
		return err
	}

	// Wait for the operation to complete
	err = op.Wait()
	opAPI := op.Get()
	if opAPI.Metadata != nil {
		exitStatusRaw, ok := opAPI.Metadata["return"].(float64)
		if ok {
			c.global.ret = int(exitStatusRaw)
		}
	}

	if err != nil {
		return err
	}

	// Wait for any remaining I/O to be flushed
	<-execArgs.DataDone

	return nil
}
