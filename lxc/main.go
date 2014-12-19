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
	gnuflag.StringVar(&lxd.ConfigDir, "config", lxd.ConfigDir, "Alternate config directory.")

	if len(os.Args) == 2 && (os.Args[1] == "-h" || os.Args[1] == "--help") {
		os.Args[1] = "help"
	}
	if len(os.Args) < 2 {
		os.Args = append(os.Args, "help")
	}
	name := os.Args[1]
	cmd, ok := commands[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "error: unknown command: %s\n", name)
		commands["help"].run(nil, nil)
		os.Exit(1)
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

	config, err := lxd.LoadConfig()
	if err != nil {
		return err
	}

	err = cmd.run(config, gnuflag.Args())
	if err == errArgs {
		fmt.Fprintf(os.Stderr, "error: %v\n%s", err, cmd.usage())
		os.Exit(1)
	}
	return err
}

type command interface {
	usage() string
	flags()
	run(config *lxd.Config, args []string) error
}

var commands = map[string]command{
	"version":  &versionCmd{},
	"help":     &helpCmd{},
	"finger":   &fingerCmd{},
	"config":   &configCmd{},
	"create":   &createCmd{},
	"list":     &listCmd{},
	"remote":   &remoteCmd{},
	"stop":     &actionCmd{lxd.Stop},
	"start":    &actionCmd{lxd.Start},
	"restart":  &actionCmd{lxd.Restart},
	"freeze":   &actionCmd{lxd.Freeze},
	"unfreeze": &actionCmd{lxd.Unfreeze},
	"delete":   &deleteCmd{},
	"file":     &fileCmd{},
	"snapshot": &snapshotCmd{},
	"exec":     &execCmd{},
}

var errArgs = fmt.Errorf("wrong number of subcommand arguments")
