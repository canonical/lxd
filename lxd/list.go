package main

import (
	"fmt"
	"net/http"

	"gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd"
)

func (d *Daemon) serveList(w http.ResponseWriter, r *http.Request) {
	lxd.Debugf("responding to list")
	if !d.isTrustedClient(r) {
		lxd.Debugf("List request from untrusted client")
		return
	}

	c := lxc.DefinedContainers(d.lxcpath)
	for i := range c {
		fmt.Fprintf(w, "%d: %s (%s)\n", i, c[i].Name(), c[i].State())
	}
}
