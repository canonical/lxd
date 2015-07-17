package main

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/chai2010/gettext-go/gettext"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/internal/gnuflag"
	"github.com/lxc/lxd/shared"
)

func main() {
	if err := run(); err != nil {
		// The action we take depends on the error we get.
		switch t := err.(type) {
		case *url.Error:
			shared.Debugf("url.Error caught in main(). Op: %s, URL: %s, Err: %s\n", t.Op, t.URL, t.Err)
			switch u := t.Err.(type) {
			case *net.OpError:
				shared.Debugf("Inner error type is a net.OpError: Op: %s Net: %s Addr: %s Err: %T", u.Op, u.Net, u.Addr, u.Err)
				if u.Op == "dial" && u.Net == "unix" {
					// The unix socket we are trying to conect to is refusing our connection attempt. Perhaps the server is not running?
					// Let's at least tell the user about it, since it's hard to get information on wether something is actually listening.
					fmt.Fprintf(os.Stderr, fmt.Sprintf(gettext.Gettext("Cannot connect to unix socket at %s Is the server running?\n"), u.Addr))
					os.Exit(1)
				}
			default:
				shared.Debugf("url.Error's inner Err type is %T", u)
			}
		default:
			shared.Debugf("Error caught in main: %T\n", t)
		}

		fmt.Fprintf(os.Stderr, gettext.Gettext("error: %v\n"), err)
		os.Exit(1)
	}
}

func run() error {
	gettext.BindTextdomain("lxd", "", nil)
	gettext.Textdomain("lxd")

	verbose := gnuflag.Bool("v", false, gettext.Gettext("Enables verbose mode."))
	debug := gnuflag.Bool("debug", false, gettext.Gettext("Enables debug mode."))

	gnuflag.StringVar(&lxd.ConfigDir, "config", lxd.ConfigDir, gettext.Gettext("Alternate config directory."))

	if len(os.Args) >= 3 && os.Args[1] == "config" && os.Args[2] == "profile" {
		fmt.Fprintf(os.Stderr, "`lxc config profile` is deprecated, please use `lxc profile`\n")
		os.Args = append(os.Args[:1], os.Args[2:]...)
	}

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

	shared.SetLogger("", "", *verbose, *debug)

	config, err := lxd.LoadConfig()
	if err != nil {
		return err
	}

	certf := lxd.ConfigPath("client.crt")
	keyf := lxd.ConfigPath("client.key")

	if os.Args[0] != "help" && os.Args[0] != "version" && (!shared.PathExists(certf) || !shared.PathExists(keyf)) {
		fmt.Fprintf(os.Stderr, gettext.Gettext("Generating a client certificate. This may take a minute...\n"))

		err = shared.FindOrGenCert(certf, keyf)
		if err != nil {
			return err
		}

		fmt.Fprintf(os.Stderr, gettext.Gettext("If this is your first run, you will need to import images using the 'lxd-images' script.\n"))
		fmt.Fprintf(os.Stderr, gettext.Gettext("For example: 'lxd-images import lxc ubuntu trusty amd64 --alias ubuntu/trusty'.\n"))
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
	"copy":     &copyCmd{},
	"delete":   &deleteCmd{},
	"exec":     &execCmd{},
	"file":     &fileCmd{},
	"finger":   &fingerCmd{},
	"help":     &helpCmd{},
	"image":    &imageCmd{},
	"info":     &infoCmd{},
	"init":     &initCmd{},
	"launch":   &launchCmd{},
	"list":     &listCmd{},
	"move":     &moveCmd{},
	"profile":  &profileCmd{},
	"publish":  &publishCmd{},
	"remote":   &remoteCmd{},
	"restart":  &actionCmd{shared.Restart, true},
	"restore":  &restoreCmd{},
	"snapshot": &snapshotCmd{},
	"start":    &actionCmd{shared.Start, false},
	"stop":     &actionCmd{shared.Stop, true},
	"version":  &versionCmd{},
}

var errArgs = fmt.Errorf(gettext.Gettext("wrong number of subcommand arguments"))
