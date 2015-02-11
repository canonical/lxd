package main

import (
	"fmt"
	"os"
	"syscall"

	"github.com/gosexy/gettext"
	"github.com/lxc/lxd"
	"golang.org/x/crypto/ssh/terminal"
)

type execCmd struct{}

func (c *execCmd) showByDefault() bool {
	return true
}

func (c *execCmd) usage() string {
	return gettext.Gettext(
		"exec specified command in a container.\n" +
			"\n" +
			"lxc exec container [command]\n")
}

func (c *execCmd) flags() {}

func (c *execCmd) run(config *lxd.Config, args []string) error {
	if len(args) < 2 {
		return errArgs
	}

	remote, name := config.ParseRemoteAndContainer(args[0])
	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	cfd := syscall.Stdout
	var oldttystate *terminal.State = nil
	if terminal.IsTerminal(cfd) {
		oldttystate, err = terminal.MakeRaw(cfd)
		if err != nil {
			return err
		}
		defer terminal.Restore(cfd, oldttystate)
	}

	ret, err := d.Exec(name, args[1:], os.Stdin, os.Stdout, os.Stderr)
	if err != nil {
		return err
	}

	if oldttystate != nil {
		/* A bit of a special case here: we want to exit with the same code as
		 * the process inside the container, so we explicitly exit here
		 * instead of returning an error.
		 *
		 * Additionally, since os.Exit() exits without running deferred
		 * functions, we restore the terminal explicitly.
		 */
		terminal.Restore(cfd, oldttystate)
	}

	/* we get the result of waitpid() here so we need to transform it */
	os.Exit(ret >> 8)
	return fmt.Errorf(gettext.Gettext("unreachable return reached"))
}
