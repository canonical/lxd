package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd/shared"
)

type cmdForkexec struct {
	global *cmdGlobal
}

func (c *cmdForkexec) Command() *cobra.Command {
	// Main subcommand
	cmd := &cobra.Command{}
	cmd.Use = "forkexec <container name> <containers path> <config> -- env [key=value...] -- cmd <args...>"
	cmd.Short = "Execute a task inside the container"
	cmd.Long = `Description:
  Execute a task inside the container

  This internal command is used to spawn a task inside the container and
  allow LXD to interact with it.
`
	cmd.RunE = c.Run
	cmd.Hidden = true

	return cmd
}

func (c *cmdForkexec) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	if len(args) < 4 {
		cmd.Help()

		if len(args) == 0 {
			return nil
		}

		return fmt.Errorf("Missing required arguments")
	}

	// Only root should run this
	if os.Geteuid() != 0 {
		return fmt.Errorf("This must be run as root")
	}

	name := args[0]
	lxcpath := args[1]
	configPath := args[2]

	d, err := lxc.NewContainer(name, lxcpath)
	if err != nil {
		return fmt.Errorf("Error initializing container for start: %q", err)
	}

	err = d.LoadConfigFile(configPath)
	if err != nil {
		return fmt.Errorf("Error opening startup config file: %q", err)
	}

	syscall.Dup3(int(os.Stdin.Fd()), 200, 0)
	syscall.Dup3(int(os.Stdout.Fd()), 201, 0)
	syscall.Dup3(int(os.Stderr.Fd()), 202, 0)

	syscall.Close(int(os.Stdin.Fd()))
	syscall.Close(int(os.Stdout.Fd()))
	syscall.Close(int(os.Stderr.Fd()))

	opts := lxc.DefaultAttachOptions
	opts.ClearEnv = true
	opts.StdinFd = 200
	opts.StdoutFd = 201
	opts.StderrFd = 202

	logPath := shared.LogPath(name, "forkexec.log")
	if shared.PathExists(logPath) {
		os.Remove(logPath)
	}

	logFile, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_SYNC, 0644)
	if err == nil {
		syscall.Dup3(int(logFile.Fd()), 1, 0)
		syscall.Dup3(int(logFile.Fd()), 2, 0)
	}

	env := []string{}
	command := []string{}

	section := ""
	for _, arg := range args[3:] {
		// The "cmd" section must come last as it may contain a --
		if arg == "--" && section != "cmd" {
			section = ""
			continue
		}

		if section == "" {
			section = arg
			continue
		}

		if section == "env" {
			fields := strings.SplitN(arg, "=", 2)
			if len(fields) == 2 && fields[0] == "HOME" {
				opts.Cwd = fields[1]
			}
			env = append(env, arg)
		} else if section == "cmd" {
			command = append(command, arg)
		} else {
			return fmt.Errorf("Invalid exec section: %s", section)
		}
	}

	opts.Env = env

	status, err := d.RunCommandNoWait(command, opts)
	if err != nil {
		return fmt.Errorf("Failed running command: %q", err)
	}
	// Send the PID of the executing process.
	w := os.NewFile(uintptr(3), "attachedPid")
	defer w.Close()

	err = json.NewEncoder(w).Encode(status)
	if err != nil {
		return fmt.Errorf("Failed sending PID of executing command: %q", err)
	}

	var ws syscall.WaitStatus
	wpid, err := syscall.Wait4(status, &ws, 0, nil)
	if err != nil || wpid != status {
		return fmt.Errorf("Failed finding process: %q", err)
	}

	if ws.Exited() {
		os.Exit(ws.ExitStatus())
	}

	if ws.Signaled() {
		// 128 + n == Fatal error signal "n"
		os.Exit(128 + int(ws.Signal()))
	}

	return fmt.Errorf("Command failed")
}
