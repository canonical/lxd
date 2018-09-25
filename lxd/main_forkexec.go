package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"gopkg.in/lxc/go-lxc.v2"
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

	name := args[0]
	lxcpath := args[1]
	configPath := args[2]

	// Only root should run this
	if os.Geteuid() != 0 {
		return fmt.Errorf("This must be run as root")
	}

	// Get the status
	fdStatus := os.NewFile(uintptr(6), "attachedPid")
	defer fdStatus.Close()

	// Load the container
	d, err := lxc.NewContainer(name, lxcpath)
	if err != nil {
		return fmt.Errorf("Error initializing container for start: %q", err)
	}

	err = d.LoadConfigFile(configPath)
	if err != nil {
		return fmt.Errorf("Error opening startup config file: %q", err)
	}

	// Setup attach arguments
	opts := lxc.DefaultAttachOptions
	opts.ClearEnv = true
	opts.StdinFd = 3
	opts.StdoutFd = 4
	opts.StderrFd = 5

	// Parse the command line
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

	// Exec the command
	status, err := d.RunCommandNoWait(command, opts)
	if err != nil {
		return fmt.Errorf("Failed running command: %q", err)
	}

	// Send the PID of the executing process.
	err = json.NewEncoder(fdStatus).Encode(status)
	if err != nil {
		return fmt.Errorf("Failed sending PID of executing command: %q", err)
	}

	// Handle exit code
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
