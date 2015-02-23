package main

import (
	"net/http"

	"gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd/shared"
)

func listGet(d *Daemon, r *http.Request) Response {
	shared.Debugf("responding to list")

	var result []string

	containers := lxc.DefinedContainers(d.lxcpath)
	for i := range containers {
		result = append(result, containers[i].Name())
	}

	return SyncResponse(true, result)
}

var listCmd = Command{name: "list", get: listGet}
