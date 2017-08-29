package main

import (
	"encoding/json"
	"os"
	"strings"
	"syscall"

	"gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd/shared"
)

/*
 * This is called by lxd when called as "lxd forkexec <container>"
 */
func cmdForkExec(args *Args) error {
	if len(args.Params) < 3 {
		return SubCommandErrorf(-1, "Bad params: %q", args.Params)
	}
	if len(args.Extra) < 1 {
		return SubCommandErrorf(-1, "Bad extra: %q", args.Extra)
	}

	name := args.Params[0]
	lxcpath := args.Params[1]
	configPath := args.Params[2]

	c, err := lxc.NewContainer(name, lxcpath)
	if err != nil {
		return SubCommandErrorf(-1, "Error initializing container for start: %q", err)
	}

	err = c.LoadConfigFile(configPath)
	if err != nil {
		return SubCommandErrorf(-1, "Error opening startup config file: %q", err)
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
	cmd := []string{}

	section := ""
	for _, arg := range args.Extra {
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
			cmd = append(cmd, arg)
		} else {
			return SubCommandErrorf(-1, "Invalid exec section: %s", section)
		}
	}

	opts.Env = env

	status, err := c.RunCommandNoWait(cmd, opts)
	if err != nil {
		return SubCommandErrorf(-1, "Failed running command: %q", err)
	}
	// Send the PID of the executing process.
	w := os.NewFile(uintptr(3), "attachedPid")
	defer w.Close()

	err = json.NewEncoder(w).Encode(status)
	if err != nil {
		return SubCommandErrorf(-1, "Failed sending PID of executing command: %q", err)
	}

	var ws syscall.WaitStatus
	wpid, err := syscall.Wait4(status, &ws, 0, nil)
	if err != nil || wpid != status {
		return SubCommandErrorf(-1, "Failed finding process: %q", err)
	}

	if ws.Exited() {
		return SubCommandErrorf(ws.ExitStatus(), "")
	}

	if ws.Signaled() {
		// 128 + n == Fatal error signal "n"
		return SubCommandErrorf(128+int(ws.Signal()), "")
	}

	return SubCommandErrorf(-1, "Command failed")
}
