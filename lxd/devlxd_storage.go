package main

import (
	"net/http"
	"net/url"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared/api"
)

var devLXDStoragePoolEndpoint = APIEndpoint{
	Path: "storage-pools/{poolName}",
	Get:  APIEndpointAction{Handler: devLXDStoragePoolGetHandler, AccessHandler: allowDevLXDAuthenticated},
}

// devLXDStoragePoolGetHandler retrieves information about the specified storage pool.
func devLXDStoragePoolGetHandler(d *Daemon, r *http.Request) response.Response {
	inst, err := getInstanceFromContextAndCheckSecurityFlags(r.Context(), devLXDSecurityKey, devLXDSecurityManagementVolumesKey)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.DevLXDErrorResponse(api.NewGenericStatusError(http.StatusBadRequest))
	}

	// Get storage pool.
	projectName := inst.Project().Name
	pool := api.StoragePool{}

	url := api.NewURL().Path("1.0", "storage-pools", poolName).Project(projectName)
	req, err := lxd.NewRequestWithContext(r.Context(), http.MethodGet, url.String(), nil, "")
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	resp := storagePoolGet(d, req)
	etag, err := response.NewResponseCapture(req).RenderToStruct(resp, &pool)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	// Convert to devLXD response.
	respPool := api.DevLXDStoragePool{
		Name:   pool.Name,
		Driver: pool.Driver,
		Status: pool.Status,
	}

	return response.DevLXDResponseETag(http.StatusOK, respPool, "json", etag)
}

// isDevLXDVolumeOwner checks whether the given storage volume is owned by the specified identity ID.
// The volume is owned if it has a config key "volatile.devlxd.owner" set to the identity ID.
func isDevLXDVolumeOwner(volConfig map[string]string, identityID string) bool {
	owner, ok := volConfig["volatile.devlxd.owner"]
	if !ok {
		// Missing owner key means the volume is not owned by devLXD.
		return false
	}

	return owner == identityID
}
