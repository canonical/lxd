package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/gosexy/gettext"
	"github.com/lxc/lxd"
	"github.com/lxc/lxd/internal/gnuflag"
	"github.com/lxc/lxd/shared"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, gettext.Gettext("error: %v\n"), err)
		os.Exit(1)
	}
}

func run() error {
	gettext.BindTextdomain("lxd", "")
	gettext.Textdomain("lxd")

	verbose := gnuflag.Bool("v", false, gettext.Gettext("Enables verbose mode."))
	debug := gnuflag.Bool("debug", false, gettext.Gettext("Enables debug mode."))

	gnuflag.StringVar(&lxd.ConfigDir, "config", lxd.ConfigDir, gettext.Gettext("Alternate config directory."))

	if len(os.Args) == 2 && (os.Args[1] == "-h" || os.Args[1] == "--help") {
		os.Args[1] = "help"
	}
	if len(os.Args) < 2 {
		commands["help"].run(nil, nil)
		os.Exit(1)
	}
	name := os.Args[1]
	cmd, ok := commands[name]
	if !ok {
		fmt.Fprintf(os.Stderr, gettext.Gettext("error: unknown command: %s\n"), name)
		commands["help"].run(nil, nil)
		os.Exit(1)
	}
	cmd.flags()
	gnuflag.Usage = func() {
		fmt.Fprintf(os.Stderr, gettext.Gettext("Usage: %s\n\nOptions:\n\n"), strings.TrimSpace(cmd.usage()))
		gnuflag.PrintDefaults()
	}

	os.Args = os.Args[1:]
	gnuflag.Parse(true)

	if *verbose || *debug {
		shared.SetLogger(log.New(os.Stderr, "", log.LstdFlags))
		shared.SetDebug(*debug)
	}

	config, err := lxd.LoadConfig()
	if err != nil {
		return err
	}

	err = cmd.run(config, gnuflag.Args())
	if err == errArgs {
		fmt.Fprintf(os.Stderr, gettext.Gettext("error: %v\n%s"), err, cmd.usage())
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
	"stop":     &actionCmd{shared.Stop, true},
	"start":    &actionCmd{shared.Start, false},
	"restart":  &actionCmd{shared.Restart, true},
	"freeze":   &actionCmd{shared.Freeze, false},
	"unfreeze": &actionCmd{shared.Unfreeze, false},
	"delete":   &deleteCmd{},
	"file":     &fileCmd{},
	"snapshot": &snapshotCmd{},
	"exec":     &execCmd{},
}

var errArgs = fmt.Errorf(gettext.Gettext("wrong number of subcommand arguments"))
