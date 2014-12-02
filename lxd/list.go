package main

import (
	"net/http"

	"gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd"
)

func listGet(d *Daemon, w http.ResponseWriter, r *http.Request) {
	lxd.Debugf("responding to list")

	result := make([]string, 0)

	containers := lxc.DefinedContainers(d.lxcpath)
	for i := range containers {
		result = append(result, containers[i].Name())
	}

	SyncResponse(true, result, w)
}

var listCmd = Command{"list", false, listGet, nil, nil, nil}
