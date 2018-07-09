package main

import (
	"net/http"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared/api"
)

var serverResourceCmd = Command{
	name: "resources",
	get:  serverResourcesGet,
}

var storagePoolResourcesCmd = Command{
	name: "storage-pools/{name}/resources",
	get:  storagePoolResourcesGet,
}

// /1.0/resources
// Get system resources
func serverResourcesGet(d *Daemon, r *http.Request) Response {
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
	poolName := mux.Vars(r)["name"]

	// Get the existing storage pool.
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
