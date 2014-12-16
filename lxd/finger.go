package main

import (
	"net/http"

	"github.com/lxc/lxd"
)

func fingerGet(d *Daemon, r *http.Request) Response {
	remoteAddr := r.RemoteAddr
	if remoteAddr == "@" {
		remoteAddr = "unix socket"
	}
	lxd.Debugf("responding to finger from %s", remoteAddr)

	resp := lxd.Jmap{"auth": "guest", "api_compat": lxd.APICompat}

	if d.isTrustedClient(r) {
		resp["auth"] = "trusted"
		resp["version"] = lxd.Version
	} else {
		resp["auth"] = "untrusted"
	}

	return SyncResponse(true, resp)
}

var fingerCmd = Command{"finger", true, false, fingerGet, nil, nil, nil}
