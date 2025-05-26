package main

import (
	"net/http"
	"net/url"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	storageDrivers "github.com/canonical/lxd/lxd/storage/drivers"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

var devLXDStoragePoolEndpoint = APIEndpoint{
	Path: "storage-pools/{poolName}",
	Get:  APIEndpointAction{Handler: devLXDStoragePoolGetHandler, AccessHandler: allowDevLXDAuthenticated},
}

var devLXDStoragePoolVolumesEndpoint = APIEndpoint{
	Path: "storage-pools/{poolName}/volumes",
	Get:  APIEndpointAction{Handler: devLXDStoragePoolVolumesGetHandler, AccessHandler: allowDevLXDAuthenticated},
}

var devLXDStoragePoolVolumesTypeEndpoint = APIEndpoint{
	Path: "storage-pools/{poolName}/volumes/{type}",
	Get:  APIEndpointAction{Handler: devLXDStoragePoolVolumesGetHandler, AccessHandler: allowDevLXDAuthenticated},
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

// devLXDStoragePoolVolumesGetHandler retrieves all custom storage volumes in the specified pool
// that are owned by the caller.
func devLXDStoragePoolVolumesGetHandler(d *Daemon, r *http.Request) response.Response {
	inst, err := getInstanceFromContextAndCheckSecurityFlags(r.Context(), devLXDSecurityKey, devLXDSecurityManagementVolumesKey)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	projectName := inst.Project().Name
	pathVars := mux.Vars(r)

	poolName, err := url.PathUnescape(pathVars["poolName"])
	if err != nil {
		return response.DevLXDErrorResponse(api.NewGenericStatusError(http.StatusBadRequest))
	}

	volType, err := url.PathUnescape(pathVars["type"])
	if err != nil {
		return response.DevLXDErrorResponse(api.NewGenericStatusError(http.StatusBadRequest))
	}

	// Get identity from the request context.
	identity, err := request.GetCallerIdentityFromContext(r.Context())
	if identity == nil {
		return response.DevLXDErrorResponse(err)
	}

	// Reject non-recursive requests.
	if !util.IsRecursionRequest(r) {
		return response.DevLXDErrorResponse(api.NewStatusError(http.StatusNotImplemented, "Only recursive requests are currently supported"))
	}

	// Reject non-custom volume types, if the type is specified.
	if volType != "" && storageDrivers.VolumeType(volType) != storageDrivers.VolumeTypeCustom {
		return response.DevLXDErrorResponse(api.NewStatusError(http.StatusBadRequest, "Only custom storage volume requests are allowed"))
	}

	// Get storage volumes.
	vols := []api.StorageVolume{}

	url := api.NewURL().Path("1.0", "storage-pools", poolName, "volumes", volType).Project(projectName).WithQuery("recursion", "1")
	target := r.URL.Query().Get("target")
	if target != "" {
		url = url.WithQuery("target", target)
	}

	// Ensure only custom volumes are returned, if the volume type is not provided.
	if volType == "" {
		url = url.WithQuery("filter", "type eq custom")
	}

	req, err := lxd.NewRequestWithContext(r.Context(), http.MethodGet, url.String(), nil, "")
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	resp := storagePoolVolumesGet(d, req)
	_, err = response.NewResponseCapture(req).RenderToStruct(resp, &vols)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	respVols := make([]api.DevLXDStorageVolume, 0, len(vols))
	for _, vol := range vols {
		if !isDevLXDVolumeOwner(vol.Config, identity.Identifier) {
			// Skip volumes not owned by the caller.
			continue
		}

		respVols = append(respVols, api.DevLXDStorageVolume{
			Name:        vol.Name,
			Description: vol.Description,
			Pool:        vol.Pool,
			Type:        vol.Type,
			Config:      vol.Config,
			Location:    vol.Location,
		})
	}

	return response.DevLXDResponse(http.StatusOK, respVols, "json")
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

// devLXDStoragePoolVolumeTypeAccessHandler returns an access handler which checks the given entitlement
// on a storage volume and ensures cross-project access is not allowed.
func devLXDStoragePoolVolumeTypeAccessHandler(entityType entity.Type, entitlement auth.Entitlement) func(d *Daemon, r *http.Request) response.Response {
	return func(d *Daemon, r *http.Request) response.Response {
		s := d.State()

		// Disallow cross-project access and ensure project query parameter is set.
		err := enforceDevLXDProject(r)
		if err != nil {
			return response.DevLXDErrorResponse(err)
		}

		err = checkStoragePoolVolumeTypeAccess(s, r, entityType, entitlement)
		if err != nil {
			return response.DevLXDErrorResponse(err)
		}

		return response.EmptySyncResponse
	}
}
