package main

import (
	"net/http"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared/api"
)

var api10ResourcesCmd = Command{
	name: "resources",
	get:  api10ResourcesGet,
}

var storagePoolResourcesCmd = Command{
	name: "storage-pools/{name}/resources",
	get:  storagePoolResourcesGet,
}

// /1.0/resources
// Get system resources
func api10ResourcesGet(d *Daemon, r *http.Request) Response {
	// If a target was specified, forward the request to the relevant node.
	response := ForwardedResponseIfTargetIsRemote(d, r)
	if response != nil {
		return response
	}

	// Get the local resource usage
	res := api.Resources{}

	cpu, err := util.CPUResource()
	if err != nil {
		return SmartError(err)
	}

	mem, err := util.MemoryResource()
	if err != nil {
		return SmartError(err)
	}

	res.CPU = *cpu
	res.Memory = *mem

	return SyncResponse(true, res)
}

// /1.0/storage-pools/{name}/resources
// Get resources for a specific storage pool
func storagePoolResourcesGet(d *Daemon, r *http.Request) Response {
	// If a target was specified, forward the request to the relevant node.
	response := ForwardedResponseIfTargetIsRemote(d, r)
	if response != nil {
		return response
	}

	// Get the existing storage pool
	poolName := mux.Vars(r)["name"]
	s, err := storagePoolInit(d.State(), poolName)
	if err != nil {
		return InternalError(err)
	}

	err = s.StoragePoolCheck()
	if err != nil {
		return InternalError(err)
	}

	res, err := s.StoragePoolResources()
	if err != nil {
		return InternalError(err)
	}

	return SyncResponse(true, &res)
}
