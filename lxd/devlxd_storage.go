package main

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	storageDrivers "github.com/canonical/lxd/lxd/storage/drivers"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared/api"
)

var devLXDStoragePoolEndpoint = devLXDAPIEndpoint{
	Path: "storage-pools/{poolName}",
	Get:  devLXDAPIEndpointAction{Handler: devLXDStoragePoolGetHandler, AccessHandler: allowDevLXDAuthenticated},
}

func devLXDStoragePoolGetHandler(d *Daemon, r *http.Request) response.Response {
	inst, err := getInstanceFromContextAndCheckSecurityFlags(r.Context(), devLXDSecurityKey, devLXDSecurityMgmtVolumesKey)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	// Get storage pool.
	poolName := mux.Vars(r)["poolName"]
	projectName := inst.Project().Name
	pool := api.StoragePool{}

	url := api.NewURL().Path("1.0", "storage-pools", poolName).Project(projectName)
	req, err := request.NewRequestWithContext(r.Context(), http.MethodGet, url.String(), nil, "")
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	resp := storagePoolGet(d, req)
	etag, err := RenderToStruct(req, resp, &pool)
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

var devLXDStoragePoolVolumesEndpoint = devLXDAPIEndpoint{
	Path: "storage-pools/{poolName}/volumes",
	Get:  devLXDAPIEndpointAction{Handler: devLXDStoragePoolVolumesGetHandler, AccessHandler: allowDevLXDAuthenticated},
	Post: devLXDAPIEndpointAction{Handler: devLXDStoragePoolVolumesPostHandler, AccessHandler: allowDevLXDAuthenticated},
}

var devLXDStoragePoolVolumesTypeEndpoint = devLXDAPIEndpoint{
	Path: "storage-pools/{poolName}/volumes/{type}",
	Get:  devLXDAPIEndpointAction{Handler: devLXDStoragePoolVolumesGetHandler, AccessHandler: allowDevLXDAuthenticated},
	Post: devLXDAPIEndpointAction{Handler: devLXDStoragePoolVolumesPostHandler, AccessHandler: allowDevLXDAuthenticated},
}

func devLXDStoragePoolVolumesGetHandler(d *Daemon, r *http.Request) response.Response {
	inst, err := getInstanceFromContextAndCheckSecurityFlags(r.Context(), devLXDSecurityKey, devLXDSecurityMgmtVolumesKey)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	poolName := mux.Vars(r)["poolName"]
	volType := mux.Vars(r)["type"]
	projectName := inst.Project().Name

	// Get identity from the request context.
	identity, err := getDevLXDIdentity(r.Context())
	if identity == nil {
		return response.DevLXDErrorResponse(err)
	}

	// Reject non-recursive requests.
	if !util.IsRecursionRequest(r) {
		return response.DevLXDErrorResponse(api.NewStatusError(http.StatusNotImplemented, "Only recursive requests are currently supported"))
	}

	// Reject non-custom volume types, if the type is specified.
	if volType != "" && storageDrivers.VolumeType(volType) != storageDrivers.VolumeTypeCustom {
		return response.DevLXDErrorResponse(api.NewStatusError(http.StatusBadRequest, "Only custom storage volumes can be retrieved"))
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

	req, err := request.NewRequestWithContext(r.Context(), http.MethodGet, url.String(), nil, "")
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	resp := storagePoolVolumesGet(d, req)
	_, err = RenderToStruct(req, resp, &vols)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	respVols := make([]api.DevLXDStorageVolume, 0, len(vols))
	for _, vol := range vols {
		if vol.Config["volatile.devlxd.owner"] != identity.Identifier {
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

func devLXDStoragePoolVolumesPostHandler(d *Daemon, r *http.Request) response.Response {
	inst, err := getInstanceFromContextAndCheckSecurityFlags(r.Context(), devLXDSecurityKey, devLXDSecurityMgmtVolumesKey)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	poolName := mux.Vars(r)["poolName"]
	volType := mux.Vars(r)["type"]
	projectName := inst.Project().Name

	// Get identity from the request context.
	identity, err := getDevLXDIdentity(r.Context())
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

	// Set caller's identity ID as the volume owner.
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

	req, err := request.NewRequestWithContext(r.Context(), http.MethodPost, url.String(), reqBody, "")
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	resp := storagePoolVolumesPost(d, req)
	err = Render(req, resp)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	return response.DevLXDResponse(http.StatusOK, "", "raw")
}

var devLXDStoragePoolVolumeTypeEndpoint = devLXDAPIEndpoint{
	Path:   "storage-pools/{poolName}/volumes/{type}/{volumeName}",
	Get:    devLXDAPIEndpointAction{Handler: devLXDStoragePoolVolumeGetHandler, AccessHandler: allowDevLXDAuthenticated},
	Delete: devLXDAPIEndpointAction{Handler: devLXDStoragePoolVolumeDeleteHandler, AccessHandler: allowDevLXDAuthenticated},
}

func devLXDStoragePoolVolumeGet(ctx context.Context, d *Daemon, target string, project string, poolName string, volName string, volType string) (*api.DevLXDStorageVolume, string, error) {
	// Get identity from the request context.
	identity, err := getDevLXDIdentity(ctx)
	if identity == nil {
		return nil, "", err
	}

	// Restrict access to custom volumes.
	if storageDrivers.VolumeType(volType) != storageDrivers.VolumeTypeCustom {
		return nil, "", api.NewStatusError(http.StatusBadRequest, "Only custom storage volumes can be retrieved")
	}

	// Get storage volumes.
	vol := api.StorageVolume{}

	url := api.NewURL().Path("1.0", "storage-pools", poolName, "volumes", "custom", volName).Project(project)
	if target != "" {
		url = url.WithQuery("target", target)
	}

	req, err := request.NewRequestWithContext(ctx, http.MethodGet, url.String(), nil, "")
	if err != nil {
		return nil, "", err
	}

	err = addStoragePoolVolumeDetailsToRequestContext(d.State(), req)
	if err != nil {
		return nil, "", err
	}

	resp := storagePoolVolumeGet(d, req)
	etag, err := RenderToStruct(req, resp, &vol)
	if err != nil {
		return nil, "", err
	}

	// If the volume does not belong to the caller, return not found.
	if vol.Config["volatile.devlxd.owner"] != identity.Identifier {
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

func devLXDStoragePoolVolumeGetHandler(d *Daemon, r *http.Request) response.Response {
	inst, err := getInstanceFromContextAndCheckSecurityFlags(r.Context(), devLXDSecurityKey, devLXDSecurityMgmtVolumesKey)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	poolName := mux.Vars(r)["poolName"]
	volName := mux.Vars(r)["volumeName"]
	volType := mux.Vars(r)["type"]
	projectName := inst.Project().Name

	vol, etag, err := devLXDStoragePoolVolumeGet(r.Context(), d, r.URL.Query().Get("target"), projectName, poolName, volName, volType)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	return response.DevLXDResponseETag(http.StatusOK, vol, "json", etag)
}

func devLXDStoragePoolVolumeDeleteHandler(d *Daemon, r *http.Request) response.Response {
	inst, err := getInstanceFromContextAndCheckSecurityFlags(r.Context(), devLXDSecurityKey, devLXDSecurityMgmtVolumesKey)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	poolName := mux.Vars(r)["poolName"]
	volName := mux.Vars(r)["volumeName"]
	volType := mux.Vars(r)["type"]
	projectName := inst.Project().Name
	target := r.URL.Query().Get("target")

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

	req, err := request.NewRequestWithContext(r.Context(), http.MethodDelete, url.String(), nil, "")
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	err = addStoragePoolVolumeDetailsToRequestContext(d.State(), req)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	resp := storagePoolVolumeDelete(d, req)
	err = Render(req, resp)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	return response.DevLXDResponse(http.StatusOK, "", "raw")
}
