package main

import (
	"os"
	"strconv"
	"strings"

	"gopkg.in/lxc/go-lxc.v2"
)

/*
 * This is called by lxd when called as "lxd forkconsole <container> <path> <conf> <tty=<n>> <escape=<n>>"
 */
func cmdForkConsole(args *Args) error {
	if len(args.Params) != 5 {
		return SubCommandErrorf(-1, "Bad params: %q", args.Params)
	}

	name := args.Params[0]
	lxcpath := args.Params[1]
	configPath := args.Params[2]

	ttyNum := strings.TrimPrefix(args.Params[3], "tty=")
	tty, err := strconv.Atoi(ttyNum)
	if err != nil {
		return SubCommandErrorf(-1, "Failed to retrieve tty number: %q", err)
	}

	escapeNum := strings.TrimPrefix(args.Params[4], "escape=")
	escape, err := strconv.Atoi(escapeNum)
	if err != nil {
		return SubCommandErrorf(-1, "Failed to retrieve escape character: %q", err)
	}

	c, err := lxc.NewContainer(name, lxcpath)
	if err != nil {
		return SubCommandErrorf(-1, "Error initializing container: %q", err)
	}

	err = c.LoadConfigFile(configPath)
	if err != nil {
		return SubCommandErrorf(-1, "Error opening config file: %q", err)
	}

	opts := lxc.ConsoleOptions{}
	opts.Tty = tty
	opts.StdinFd = uintptr(os.Stdin.Fd())
	opts.StdoutFd = uintptr(os.Stdout.Fd())
	opts.StderrFd = uintptr(os.Stderr.Fd())
	opts.EscapeCharacter = rune(escape)

	err = c.Console(opts)
	if err != nil {
		return SubCommandErrorf(-1, "Failed running forkconsole: %q", err)
	}

	return nil
}
