package main

import (
	"net/http"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/resources"
	"github.com/lxc/lxd/lxd/response"
	storagePools "github.com/lxc/lxd/lxd/storage"
	storageDrivers "github.com/lxc/lxd/lxd/storage/drivers"
	"github.com/lxc/lxd/shared/api"
)

var api10ResourcesCmd = APIEndpoint{
	Path: "resources",

	Get: APIEndpointAction{Handler: api10ResourcesGet, AccessHandler: AllowAuthenticated},
}

var storagePoolResourcesCmd = APIEndpoint{
	Path: "storage-pools/{name}/resources",

	Get: APIEndpointAction{Handler: storagePoolResourcesGet, AccessHandler: AllowAuthenticated},
}

// /1.0/resources
// Get system resources
func api10ResourcesGet(d *Daemon, r *http.Request) response.Response {
	// If a target was specified, forward the request to the relevant node.
	resp := ForwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	// Get the local resource usage
	res, err := resources.GetResources()
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, res)
}

// /1.0/storage-pools/{name}/resources
// Get resources for a specific storage pool
func storagePoolResourcesGet(d *Daemon, r *http.Request) response.Response {
	// If a target was specified, forward the request to the relevant node.
	resp := ForwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	// Get the existing storage pool
	poolName := mux.Vars(r)["name"]
	var res *api.ResourcesStoragePool

	// Check if we can load new storage layer for pool driver type.
	pool, err := storagePools.GetPoolByName(d.State(), poolName)
	if err != storageDrivers.ErrUnknownDriver {
		if err != nil {
			return response.InternalError(err)
		}

		res, err = pool.GetResources()
		if err != nil {
			return response.InternalError(err)
		}
	} else { // Fallback to old storage layer.
		s, err := storagePoolInit(d.State(), poolName)
		if err != nil {
			return response.InternalError(err)
		}

		err = s.StoragePoolCheck()
		if err != nil {
			return response.InternalError(err)
		}

		res, err = s.StoragePoolResources()
		if err != nil {
			return response.InternalError(err)
		}
	}

	return response.SyncResponse(true, res)
}
