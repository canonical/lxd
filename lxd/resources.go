package main

import (
	"net/http"

	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared/api"
)

// /1.0/resources
// Get system resources
func serverResourcesGet(d *Daemon, r *http.Request) Response {
	res := api.Resources{}

	cpu, err := util.CpuResource()
	if err != nil {
		return SmartError(err)
	}

	mem, err := util.MemoryResource()
	if err != nil {
		return SmartError(err)
	}

	res.CPU = cpu
	res.Memory = mem

	return SyncResponse(true, res)
}

var serverResourceCmd = Command{name: "resources", get: serverResourcesGet}
