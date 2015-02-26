package main

import (
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/gosexy/gettext"
	"github.com/lxc/lxd"
	"github.com/lxc/lxd/internal/gnuflag"
	"golang.org/x/crypto/ssh/terminal"
)

type execCmd struct{}

func (c *execCmd) showByDefault() bool {
	return true
}

func (c *execCmd) usage() string {
	return gettext.Gettext(
		"Execute the specified command in a container.\n" +
			"\n" +
			"lxc exec container [--env EDITOR=/usr/bin/vim]... <command>\n")
}

type envFlag []string

func (f *envFlag) String() string {
	return fmt.Sprint(*f)
}

func (f *envFlag) Set(value string) error {
	if f == nil {
		*f = make(envFlag, 1)
	} else {
		*f = append(*f, value)
	}
	return nil
}

var envArgs envFlag

func (c *execCmd) flags() {
	gnuflag.Var(&envArgs, "env", "An environment variable of the form HOME=/home/foo")
}

func (c *execCmd) run(config *lxd.Config, args []string) error {
	if len(args) < 2 {
		return errArgs
	}

	remote, name := config.ParseRemoteAndContainer(args[0])
	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	env := map[string]string{"HOME": "/root"}
	myEnv := os.Environ()
	for _, ent := range myEnv {
		if strings.HasPrefix(ent, "TERM=") {
			env["TERM"] = ent[len("TERM="):]
		}
	}

	for _, arg := range envArgs {
		pieces := strings.SplitN(arg, "=", 2)
		value := ""
		if len(pieces) > 1 {
			value = pieces[1]
		}
		env[pieces[0]] = value
	}

	cfd := syscall.Stdout
	var oldttystate *terminal.State
	if terminal.IsTerminal(cfd) {
		oldttystate, err = terminal.MakeRaw(cfd)
		if err != nil {
			return err
		}
		defer terminal.Restore(cfd, oldttystate)
	}

	ret, err := d.Exec(name, args[1:], env, os.Stdin, os.Stdout, os.Stderr)
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
