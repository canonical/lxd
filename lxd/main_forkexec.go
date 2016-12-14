package main

import (
	"fmt"
	"os"
	"strings"
	"syscall"

	"gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd/shared"
)

/*
 * This is called by lxd when called as "lxd forkexec <container>"
 */
func cmdForkExec(args []string) (int, error) {
	if len(args) < 6 {
		return -1, fmt.Errorf("Bad arguments: %q", args)
	}

	name := args[1]
	lxcpath := args[2]
	configPath := args[3]

	c, err := lxc.NewContainer(name, lxcpath)
	if err != nil {
		return -1, fmt.Errorf("Error initializing container for start: %q", err)
	}

	err = c.LoadConfigFile(configPath)
	if err != nil {
		return -1, fmt.Errorf("Error opening startup config file: %q", err)
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
	for _, arg := range args[5:len(args)] {
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
			return -1, fmt.Errorf("Invalid exec section: %s", section)
		}
	}

	opts.Env = env

	status, err := c.RunCommandStatus(cmd, opts)
	if err != nil {
		return -1, fmt.Errorf("Failed running command: %q", err)
	}

	return status >> 8, nil
}
