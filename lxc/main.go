package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/internal/gnuflag"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

var verbose = gnuflag.Bool("v", false, "Enables verbose mode.")
var debug = gnuflag.Bool("debug", false, "Enables debug mode.")

func run() error {
	if len(os.Args) == 2 && (os.Args[1] == "-h" || os.Args[1] == "--help") {
		os.Args[1] = "help"
	}
	if len(os.Args) < 2 || os.Args[1] == "" || os.Args[1][0] == '-' {
		return fmt.Errorf("missing subcommand")
	}
	name := os.Args[1]
	cmd, ok := commands[name]
	if !ok {
		return fmt.Errorf("unknown command: %s", name)
	}
	cmd.flags()
	gnuflag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s\n\nOptions:\n\n", strings.TrimSpace(cmd.usage()))
		gnuflag.PrintDefaults()
	}

	os.Args = os.Args[1:]
	gnuflag.Parse(true)

	if *verbose || *debug {
		lxd.SetLogger(log.New(os.Stderr, "", log.LstdFlags))
		lxd.SetDebug(*debug)
	}
	return cmd.run(gnuflag.Args())
}

type command interface {
	usage() string
	flags()
	run(args []string) error
}

var commands = map[string]command{
	"version": &versionCmd{},
	"help":    &helpCmd{},
	"ping":    &pingCmd{},
	"create":  &createCmd{},
	"start": &byNameCmd{
		"start",
		func(c *lxd.Client, name string) (string, error) { return c.Start(name) },
	},
	"stop": &byNameCmd{
		"stop",
		func(c *lxd.Client, name string) (string, error) { return c.Stop(name) },
	},
	"delete": &byNameCmd{
		"delete",
		func(c *lxd.Client, name string) (string, error) { return c.Delete(name) },
	},
}

var errArgs = fmt.Errorf("too many subcommand arguments")
