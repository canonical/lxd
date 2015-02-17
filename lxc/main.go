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

	if len(os.Args) == 2 && os.Args[1] == "--version" {
		os.Args[1] = "version"
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
	showByDefault() bool
	run(config *lxd.Config, args []string) error
}

var commands = map[string]command{
	"config":   &configCmd{},
	"delete":   &deleteCmd{},
	"exec":     &execCmd{},
	"file":     &fileCmd{},
	"finger":   &fingerCmd{},
	"help":     &helpCmd{},
	"init":     &initCmd{},
	"launch":   &launchCmd{},
	"list":     &listCmd{},
	"remote":   &remoteCmd{},
	"restart":  &actionCmd{shared.Restart, true},
	"snapshot": &snapshotCmd{},
	"start":    &actionCmd{shared.Start, false},
	"stop":     &actionCmd{shared.Stop, true},
	"version":  &versionCmd{},
}

var errArgs = fmt.Errorf(gettext.Gettext("wrong number of subcommand arguments"))
