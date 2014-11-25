package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"syscall"

	"github.com/lxc/lxd"
	"golang.org/x/crypto/ssh/terminal"
)

type shellCmd struct{}

const shellUsage = `
Start a shell or specified command (NOT IMPLEMENTED) in a container.

lxc shell container [command]
`

func (c *shellCmd) usage() string {
	return shellUsage
}

func (c *shellCmd) flags() {}

func (c *shellCmd) run(config *lxd.Config, args []string) error {
	if len(args) > 1 {
		return errArgs
	}

	d, name, err := lxd.NewClient(config, args[0])
	if err != nil {
		return err
	}

	// TODO FIXME - random value in place of 5
	secret := "5"

	// TODO - accept arguments
	l, err := d.Shell(name, "/bin/bash", secret)
	if err != nil {
		return err
	}

	cfd := syscall.Stdout
	if terminal.IsTerminal(cfd) {
		oldttystate, err := terminal.MakeRaw(cfd)
		if err != nil {
			return err
		}
		defer terminal.Restore(cfd, oldttystate)
	}

	// open a connection to l and connect stdin/stdout to it

	// connect
	conn, err := net.Dial("tcp", l)
	if err != nil {
		return err
	}
	_, err = conn.Write([]byte(secret))
	if err != nil {
		return err
	}

	go func() {
		_, err := io.Copy(conn, os.Stdin)
		if err != nil {
			fmt.Printf("Stdin read error: %s\n", err.Error())
			return
		}
	}()
	_, err = io.Copy(os.Stdout, conn)
	if err != nil {
		fmt.Printf("Connection read error: %s\n", err.Error())
		return err
	}

	return nil
}
