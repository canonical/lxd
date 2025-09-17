package main

import (
	"context"
	"encoding/json"
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
	Post: APIEndpointAction{Handler: devLXDStoragePoolVolumesPostHandler, AccessHandler: allowDevLXDPermission(entity.TypeProject, auth.EntitlementCanCreateStorageVolumes)},
}

var devLXDStoragePoolVolumesTypeEndpoint = APIEndpoint{
	Path: "storage-pools/{poolName}/volumes/{type}",
	Get:  APIEndpointAction{Handler: devLXDStoragePoolVolumesGetHandler, AccessHandler: allowDevLXDAuthenticated},
	Post: APIEndpointAction{Handler: devLXDStoragePoolVolumesPostHandler, AccessHandler: allowDevLXDPermission(entity.TypeProject, auth.EntitlementCanCreateStorageVolumes)},
}

var devLXDStoragePoolVolumeTypeEndpoint = APIEndpoint{
	Path:   "storage-pools/{poolName}/volumes/{type}/{volumeName}",
	Get:    APIEndpointAction{Handler: devLXDStoragePoolVolumeGetHandler, AccessHandler: devLXDStoragePoolVolumeTypeAccessHandler(entity.TypeStorageVolume, auth.EntitlementCanView)},
	Put:    APIEndpointAction{Handler: devLXDStoragePoolVolumePutHandler, AccessHandler: devLXDStoragePoolVolumeTypeAccessHandler(entity.TypeStorageVolume, auth.EntitlementCanEdit)},
	Patch:  APIEndpointAction{Handler: devLXDStoragePoolVolumePutHandler, AccessHandler: devLXDStoragePoolVolumeTypeAccessHandler(entity.TypeStorageVolume, auth.EntitlementCanEdit)},
	Delete: APIEndpointAction{Handler: devLXDStoragePoolVolumeDeleteHandler, AccessHandler: devLXDStoragePoolVolumeTypeAccessHandler(entity.TypeStorageVolume, auth.EntitlementCanDelete)},
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

// devLXDStoragePoolVolumesPostHandler creates a new custom storage volume in the specified pool
// and sets the caller as the owner of the volume.
func devLXDStoragePoolVolumesPostHandler(d *Daemon, r *http.Request) response.Response {
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

	// Decode the request body.
	vol := api.DevLXDStorageVolumesPost{}
	err = json.NewDecoder(r.Body).Decode(&vol)
	if err != nil {
		return response.DevLXDErrorResponse(api.StatusErrorf(http.StatusInternalServerError, "Failed decoding request body: %w", err))
	}

	if volType == "" {
		volType = "custom"
	}

	// Reject non-custom volume type.
	if storageDrivers.VolumeType(volType) != storageDrivers.VolumeTypeCustom {
		return response.DevLXDErrorResponse(api.NewStatusError(http.StatusBadRequest, "Only custom storage volumes can be created"))
	}

	if vol.Type != "" && vol.Type != volType {
		return response.DevLXDErrorResponse(api.NewStatusError(http.StatusBadRequest, "URL volume type does not match the volume type in body"))
	}

	if vol.Config == nil {
		vol.Config = make(map[string]string)
	}

	// Set the caller's identity ID as the volume owner, as volume updates or removal through DevLXD
	// are only allowed for volumes owned by the caller.
	vol.Config["volatile.devlxd.owner"] = identity.Identifier

	// Create storage volume.
	reqBody := api.StorageVolumesPost{
		Name:        vol.Name,
		Type:        volType,
		ContentType: vol.ContentType,
		StorageVolumePut: api.StorageVolumePut{
			Config:      vol.Config,
			Description: vol.Description,
		},
	}

	url := api.NewURL().Path("1.0", "storage-pools", poolName, "volumes", volType).Project(projectName).WithQuery("recursion", "1")
	target := r.URL.Query().Get("target")
	if target != "" {
		url = url.WithQuery("target", target)
	}

	req, err := lxd.NewRequestWithContext(r.Context(), http.MethodPost, url.String(), reqBody, "")
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	resp := storagePoolVolumesPost(d, req)
	err = response.NewResponseCapture(req).Render(resp)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	return response.DevLXDResponse(http.StatusOK, "", "raw")
}

// devLXDStoragePoolVolumeGet is a helper function that retrieves information about the specified
// custom storage volume if it is owned by the caller.
// If the volume is not found or not owned by the caller, it returns a generic not found error.
func devLXDStoragePoolVolumeGet(ctx context.Context, d *Daemon, target string, project string, poolName string, volName string, volType string) (*api.DevLXDStorageVolume, string, error) {
	// Get identity from the request context.
	identity, err := request.GetCallerIdentityFromContext(ctx)
	if identity == nil {
		return nil, "", err
	}

	// Restrict access to custom volumes.
	if storageDrivers.VolumeType(volType) != storageDrivers.VolumeTypeCustom {
		return nil, "", api.NewStatusError(http.StatusBadRequest, "Only custom storage volume requests are allowed")
	}

	// Get storage volumes.
	vol := api.StorageVolume{}

	url := api.NewURL().Path("1.0", "storage-pools", poolName, "volumes", "custom", volName).Project(project)
	if target != "" {
		url = url.WithQuery("target", target)
	}

	req, err := lxd.NewRequestWithContext(ctx, http.MethodGet, url.String(), nil, "")
	if err != nil {
		return nil, "", err
	}

	resp := storagePoolVolumeGet(d, req)
	etag, err := response.NewResponseCapture(req).RenderToStruct(resp, &vol)
	if err != nil {
		return nil, "", err
	}

	// If the volume does not belong to the caller, return not found.
	if !isDevLXDVolumeOwner(vol.Config, identity.Identifier) {
		return nil, "", api.NewGenericStatusError(http.StatusNotFound)
	}

	respVol := &api.DevLXDStorageVolume{
		Name:        vol.Name,
		Description: vol.Description,
		Pool:        vol.Pool,
		Type:        vol.Type,
		Config:      vol.Config,
		Location:    vol.Location,
	}

	return respVol, etag, nil
}

// devLXDStoragePoolVolumeGetHandler retrieves information about the specified storage volume.
// If the volume is not found or not owned by the caller, it returns a generic not found error.
func devLXDStoragePoolVolumeGetHandler(d *Daemon, r *http.Request) response.Response {
	inst, err := getInstanceFromContextAndCheckSecurityFlags(r.Context(), devLXDSecurityKey, devLXDSecurityManagementVolumesKey)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	projectName := inst.Project().Name

	poolName, volType, volName, err := extractVolumeParams(r)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	vol, etag, err := devLXDStoragePoolVolumeGet(r.Context(), d, r.URL.Query().Get("target"), projectName, poolName, volName, volType)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	return response.DevLXDResponseETag(http.StatusOK, vol, "json", etag)
}

// devLXDStoragePoolVolumePutHandler updates the specified custom storage volume if it is owned by the caller.
// If the volume is not found or not owned by the caller, it returns a generic not found error.
func devLXDStoragePoolVolumePutHandler(d *Daemon, r *http.Request) response.Response {
	inst, err := getInstanceFromContextAndCheckSecurityFlags(r.Context(), devLXDSecurityKey, devLXDSecurityManagementVolumesKey)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	projectName := inst.Project().Name
	target := r.URL.Query().Get("target")

	poolName, volType, volName, err := extractVolumeParams(r)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	// Retrieve the volume first to ensure the caller owns it.
	_, _, err = devLXDStoragePoolVolumeGet(r.Context(), d, target, inst.Project().Name, poolName, volName, volType)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	// Get identity from the request context.
	identity, err := request.GetCallerIdentityFromContext(r.Context())
	if identity == nil {
		return response.DevLXDErrorResponse(err)
	}

	// Decode the request body.
	vol := api.DevLXDStorageVolumePut{}
	err = json.NewDecoder(r.Body).Decode(&vol)
	if err != nil {
		return response.DevLXDErrorResponse(api.StatusErrorf(http.StatusInternalServerError, "Failed decoding request body: %w", err))
	}

	if vol.Config == nil {
		vol.Config = make(map[string]string)
	}

	// Ensure the volume owner cannot be changed.
	_, ok := vol.Config["volatile.devlxd.owner"]
	if ok && !isDevLXDVolumeOwner(vol.Config, identity.Identifier) {
		return response.DevLXDErrorResponse(api.NewStatusError(http.StatusBadRequest, "Volume owner cannot be changed"))
	}

	// Ensure caller's identity ID is retained as the volume owner.
	vol.Config["volatile.devlxd.owner"] = identity.Identifier

	//nolint:staticcheck // Explicitly copying fields to avoid future issues if the types diverge.
	reqBody := api.StorageVolumePut{
		Config:      vol.Config,
		Description: vol.Description,
	}

	etag := r.Header.Get("If-Match")

	url := api.NewURL().Path("1.0", "storage-pools", poolName, "volumes", "custom", volName).Project(projectName)
	if target != "" {
		url = url.WithQuery("target", target)
	}

	req, err := lxd.NewRequestWithContext(r.Context(), http.MethodPut, url.String(), reqBody, etag)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	var resp response.Response
	if r.Method == http.MethodPatch {
		resp = storagePoolVolumePatch(d, req)
	} else {
		resp = storagePoolVolumePut(d, req)
	}

	err = response.NewResponseCapture(req).Render(resp)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	return response.DevLXDResponse(http.StatusOK, "", "raw")
}

// devLXDStoragePoolVolumeDeleteHandler deletes the specified custom storage volume if it is owned by the caller.
// If the volume is not found or not owned by the caller, it returns a generic not found error.
func devLXDStoragePoolVolumeDeleteHandler(d *Daemon, r *http.Request) response.Response {
	inst, err := getInstanceFromContextAndCheckSecurityFlags(r.Context(), devLXDSecurityKey, devLXDSecurityManagementVolumesKey)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	projectName := inst.Project().Name
	target := r.URL.Query().Get("target")

	poolName, volType, volName, err := extractVolumeParams(r)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	// Retrieve the volume first to ensure the caller owns it.
	_, _, err = devLXDStoragePoolVolumeGet(r.Context(), d, target, inst.Project().Name, poolName, volName, volType)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	// Delete storage volume.
	url := api.NewURL().Path("1.0", "storage-pools", poolName, "volumes", "custom", volName).Project(projectName)
	if target != "" {
		url = url.WithQuery("target", target)
	}

	req, err := lxd.NewRequestWithContext(r.Context(), http.MethodDelete, url.String(), nil, "")
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	resp := storagePoolVolumeDelete(d, req)
	err = response.NewResponseCapture(req).Render(resp)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	return response.DevLXDResponse(http.StatusOK, "", "raw")
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

// extractVolumeParams extracts the pool name, volume type and volume name from the request URL.
func extractVolumeParams(r *http.Request) (poolName string, volType string, volName string, err error) {
	pathVars := mux.Vars(r)

	poolName, err = url.PathUnescape(pathVars["poolName"])
	if err != nil {
		return "", "", "", api.NewGenericStatusError(http.StatusBadRequest)
	}

	volType, err = url.PathUnescape(pathVars["type"])
	if err != nil {
		return "", "", "", api.NewGenericStatusError(http.StatusBadRequest)
	}

	volName, err = url.PathUnescape(pathVars["volumeName"])
	if err != nil {
		return "", "", "", api.NewGenericStatusError(http.StatusBadRequest)
	}

	return poolName, volType, volName, nil
}
