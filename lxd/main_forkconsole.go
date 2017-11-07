package main

import (
	"os"
	"strconv"

	"gopkg.in/lxc/go-lxc.v2"
)

/*
 * This is called by lxd when called as "lxd forkconsole <container> <path> <conf> <ttynum> <escape>"
 */
func cmdForkConsole(args *Args) error {
	if len(args.Params) != 5 {
		return SubCommandErrorf(-1, "Bad params: %q", args.Params)
	}

	name := args.Params[0]
	lxcpath := args.Params[1]
	configPath := args.Params[2]
	ttynum, err := strconv.Atoi(args.Params[3])
	if err != nil {
		return SubCommandErrorf(-1, "Failed to retrieve tty number: %q", err)
	}
	escape, err := strconv.Atoi(args.Params[4])
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
	opts.Tty = ttynum
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
