// +build gccgo

package main

// These are stubs since gccgo doesn't have GetsockoptUcred yet (this was added
// in golang 1.4, so presumably when gccgo catches up we can get rid of this hack).

import (
	"net"
	"net/http"

	"gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd/shared"
)

func setupDevLxdMount(c *lxc.Container) error {
	return nil
}

func createAndBindDevLxd() (*net.UnixListener, error) {
	shared.Logf("WARNING: devlxd not supported under gccgo")
	return nil, nil
}

func devLxdServer(d *Daemon) http.Server {
	return http.Server{}
}
