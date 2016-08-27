package main

import (
	"fmt"
	"net"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared/i18n"
)

type teleportCmd struct {
}

func (c *teleportCmd) showByDefault() bool {
	return true
}

func (c *teleportCmd) usage() string {
	return i18n.G(
		`Make port from inside container available on local interface.

lxd teleport [remote:]container [there=:<port> here=<host>:<port>]
`)
}

func (c *teleportCmd) flags() {
}

func (c *teleportCmd) forward(conn net.Conn, client *lxd.Client) {
	fmt.Printf("New connection from: %s\n", conn.RemoteAddr())
	defer conn.Close()

	// [ ] if no signal, write debug message
	conn.Write([]byte("No signal from remote\n"))
}

func (c *teleportCmd) run(config *lxd.Config, args []string) error {
	// [ ] param parsing
	if len(args) < 1 {
		return errArgs
	}

	remote, name := config.ParseRemoteAndContainer(args[0])

	// create new client to get websocket connection
	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}
	fmt.Println(`New client: ` + d.Name)
	fmt.Println("Container: " + name)

	// creating local server for listening on specified port
	// [ ] no hardcoded value
	listenon := "localhost:1337"
	fmt.Println("Listening on: " + listenon)
	acceptor, err := net.Listen("tcp", listenon)
	if err != nil {
		return err
	}
	for {
		conn, err := acceptor.Accept()
		if err != nil {
			// [ ] doesn't seem to be the right strategy
			return err
		}
		// [ ] go handle forward request
		go c.forward(conn, d)
	}

	return nil
}
