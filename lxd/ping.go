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
	// TODO - need to add 'guest' mode
	// (my local copy of the specs don't yet have that)
	if d.isTrustedClient(r) {
		w.Write([]byte(" trusted"))
	} else {
		w.Write([]byte(" untrusted"))
	}
}
