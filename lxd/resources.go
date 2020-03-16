package main

import (
	"net/http"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/resources"
	"github.com/lxc/lxd/lxd/response"
	storagePools "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/shared/api"
)

var api10ResourcesCmd = APIEndpoint{
	Path: "resources",

	Get: APIEndpointAction{Handler: api10ResourcesGet, AccessHandler: allowAuthenticated},
}

var storagePoolResourcesCmd = APIEndpoint{
	Path: "storage-pools/{name}/resources",

	Get: APIEndpointAction{Handler: storagePoolResourcesGet, AccessHandler: allowAuthenticated},
}

// /1.0/resources
// Get system resources
func api10ResourcesGet(d *Daemon, r *http.Request) response.Response {
	// If a target was specified, forward the request to the relevant node.
	resp := forwardedResponseIfTargetIsRemote(d, r)
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
	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	// Get the existing storage pool
	poolName := mux.Vars(r)["name"]
	var res *api.ResourcesStoragePool

	pool, err := storagePools.GetPoolByName(d.State(), poolName)
	if err != nil {
		return response.InternalError(err)
	}

	res, err = pool.GetResources()
	if err != nil {
		return response.InternalError(err)
	}

	return response.SyncResponse(true, res)
}
