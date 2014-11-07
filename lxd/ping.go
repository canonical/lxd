package main

import (
	"net/http"

	"github.com/lxc/lxd"
)

func (d *Daemon) servePing(w http.ResponseWriter, r *http.Request) {
	remoteAddr := r.RemoteAddr
	if remoteAddr == "@" {
		remoteAddr = "unix socket"
	}
	lxd.Debugf("responding to ping from %s", remoteAddr)
	w.Write([]byte(lxd.Version))
}
