package main

import (
	"net/http"

	"github.com/lxc/lxd"
)

func pingGet(d *Daemon, w http.ResponseWriter, r *http.Request) {
	remoteAddr := r.RemoteAddr
	if remoteAddr == "@" {
		remoteAddr = "unix socket"
	}
	lxd.Debugf("responding to ping from %s", remoteAddr)

	resp := lxd.Jmap{"auth": "guest", "api_compat": lxd.ApiVersion}

	if d.isTrustedClient(r) {
		resp["auth"] = "trusted"
		resp["version"] = lxd.Version
	} else {
		resp["auth"] = "untrusted"
	}

	SyncResponse(true, resp, w)
}

var pingCmd = Command{"ping", pingGet, nil, nil, nil}
