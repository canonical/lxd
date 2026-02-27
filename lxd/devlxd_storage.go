package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

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
	MetricsType: entity.TypeStoragePool,
	Path:        "storage-pools/{poolName}",
	Get:         APIEndpointAction{Handler: devLXDStoragePoolGetHandler, AccessHandler: allowDevLXDAuthenticated},
}

var devLXDStoragePoolVolumesEndpoint = APIEndpoint{
	MetricsType: entity.TypeStoragePool,
	Path:        "storage-pools/{poolName}/volumes",
	Get:         APIEndpointAction{Handler: devLXDStoragePoolVolumesGetHandler, AccessHandler: allowDevLXDAuthenticated},
	Post:        APIEndpointAction{Handler: devLXDStoragePoolVolumesPostHandler, AccessHandler: allowDevLXDPermission(entity.TypeProject, auth.EntitlementCanCreateStorageVolumes)},
}

var devLXDStoragePoolVolumesTypeEndpoint = APIEndpoint{
	MetricsType: entity.TypeStoragePool,
	Path:        "storage-pools/{poolName}/volumes/{type}",
	Get:         APIEndpointAction{Handler: devLXDStoragePoolVolumesGetHandler, AccessHandler: allowDevLXDAuthenticated},
	Post:        APIEndpointAction{Handler: devLXDStoragePoolVolumesPostHandler, AccessHandler: allowDevLXDPermission(entity.TypeProject, auth.EntitlementCanCreateStorageVolumes)},
}

var devLXDStoragePoolVolumeTypeEndpoint = APIEndpoint{
	MetricsType: entity.TypeStoragePool,
	Path:        "storage-pools/{poolName}/volumes/{type}/{volumeName}",
	Get:         APIEndpointAction{Handler: devLXDStoragePoolVolumeGetHandler, AccessHandler: devLXDStoragePoolVolumeTypeAccessHandler(entity.TypeStorageVolume, auth.EntitlementCanView)},
	Put:         APIEndpointAction{Handler: devLXDStoragePoolVolumePutHandler, AccessHandler: devLXDStoragePoolVolumeTypeAccessHandler(entity.TypeStorageVolume, auth.EntitlementCanEdit)},
	Patch:       APIEndpointAction{Handler: devLXDStoragePoolVolumePutHandler, AccessHandler: devLXDStoragePoolVolumeTypeAccessHandler(entity.TypeStorageVolume, auth.EntitlementCanEdit)},
	Delete:      APIEndpointAction{Handler: devLXDStoragePoolVolumeDeleteHandler, AccessHandler: devLXDStoragePoolVolumeTypeAccessHandler(entity.TypeStorageVolume, auth.EntitlementCanDelete)},
}

var devLXDStoragePoolVolumeSnapshotsEndpoint = APIEndpoint{
	MetricsType: entity.TypeStoragePool,
	Path:        "storage-pools/{poolName}/volumes/{type}/{volumeName}/snapshots",
	Get:         APIEndpointAction{Handler: devLXDStoragePoolVolumeSnapshotsGetHandler, AccessHandler: devLXDStoragePoolVolumeTypeAccessHandler(entity.TypeStorageVolume, auth.EntitlementCanView)},
	Post:        APIEndpointAction{Handler: devLXDStoragePoolVolumeSnapshotsPostHandler, AccessHandler: devLXDStoragePoolVolumeTypeAccessHandler(entity.TypeStorageVolume, auth.EntitlementCanManageSnapshots)},
}

var devLXDStoragePoolVolumeSnapshotEndpoint = APIEndpoint{
	MetricsType: entity.TypeStoragePool,
	Path:        "storage-pools/{poolName}/volumes/{type}/{volumeName}/snapshots/{snapshotName}",
	Get:         APIEndpointAction{Handler: devLXDStoragePoolVolumeSnapshotGetHandler, AccessHandler: devLXDStoragePoolVolumeTypeAccessHandler(entity.TypeStorageVolumeSnapshot, auth.EntitlementCanView)},
	Delete:      APIEndpointAction{Handler: devLXDStoragePoolVolumeSnapshotDeleteHandler, AccessHandler: devLXDStoragePoolVolumeTypeAccessHandler(entity.TypeStorageVolumeSnapshot, auth.EntitlementCanDelete)},
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
	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	// Reject non-recursive requests.
	recursion, _ := util.IsRecursionRequest(r)
	if recursion == 0 {
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
		if !isDevLXDVolumeOwner(vol.Config, requestor.CallerUsername()) {
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
	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
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
	vol.Config["volatile.devlxd.owner"] = requestor.CallerUsername()

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

	// Configure volume source, if provided in the request.
	if vol.Source.Type != "" {
		// Validate source type.
		if vol.Source.Type != api.SourceTypeCopy {
			return response.DevLXDErrorResponse(api.StatusErrorf(http.StatusBadRequest, "Invalid source type %q: Only source type %q is supported", vol.Source.Type, api.SourceTypeCopy))
		}

		// Extract source volume name.
		// If snapshot is provided as source, we ensure the snapshot's volume is owned by the caller.
		sourceVolName, _, _ := strings.Cut(vol.Source.Name, "/")
		if sourceVolName == "" {
			return response.DevLXDErrorResponse(api.StatusErrorf(http.StatusBadRequest, "Source volume name must be provided when source is configured"))
		}

		// Fetch a source volume.
		sourceVol := api.StorageVolume{}

		sourceURL := api.NewURL().Path("1.0", "storage-pools", vol.Source.Pool, "volumes", "custom", sourceVolName).Project(projectName)
		if vol.Source.Location != "" {
			sourceURL = sourceURL.WithQuery("target", vol.Source.Location)
		}

		req, err := lxd.NewRequestWithContext(r.Context(), http.MethodGet, sourceURL.String(), nil, "")
		if err != nil {
			return response.DevLXDErrorResponse(err)
		}

		// Set path variables for the request, required when populating the request using volume details.
		// Source volume is not part of the original request URL.
		req = mux.SetURLVars(req, map[string]string{
			"volumeName": sourceVolName,
			"poolName":   vol.Source.Pool,
			"type":       "custom",
		})

		// Populate request context with source volume details.
		err = addStoragePoolVolumeDetailsToRequestContext(d.State(), req)
		if err != nil {
			if api.StatusErrorCheck(err, http.StatusNotFound) {
				return response.DevLXDErrorResponse(api.NewStatusError(http.StatusNotFound, "Source volume not found"))
			}

			return response.DevLXDErrorResponse(err)
		}

		resp := storagePoolVolumeGet(d, req)
		_, err = response.NewResponseCapture(req).RenderToStruct(resp, &sourceVol)
		if err != nil {
			return response.DevLXDErrorResponse(err)
		}

		// Ensure the source volume is owned by the caller.
		if !isDevLXDVolumeOwner(sourceVol.Config, requestor.CallerUsername()) {
			return response.DevLXDErrorResponse(api.NewStatusError(http.StatusNotFound, "Source volume not found"))
		}

		// Configure source for the new volume.
		reqBody.Source = api.StorageVolumeSource{
			Name:     vol.Source.Name,
			Type:     vol.Source.Type,
			Pool:     vol.Source.Pool,
			Location: vol.Source.Location,
			// Always use instance project because cross-project volume copies are not allowed.
			Project: projectName,
		}
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
	op, err := response.NewResponseCapture(req).RenderToOperation(resp)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	return response.DevLXDOperationResponse(*op)
}

// devLXDStoragePoolVolumeGet is a helper function that retrieves information about the specified
// custom storage volume if it is owned by the caller.
// If the volume is not found or not owned by the caller, it returns a generic not found error.
func devLXDStoragePoolVolumeGet(ctx context.Context, d *Daemon, target string, project string, poolName string, volName string, volType string) (*api.DevLXDStorageVolume, string, error) {
	// Get identity from the request context.
	requestor, err := request.GetRequestor(ctx)
	if err != nil {
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
	if !isDevLXDVolumeOwner(vol.Config, requestor.CallerUsername()) {
		return nil, "", api.NewGenericStatusError(http.StatusNotFound)
	}

	respVol := &api.DevLXDStorageVolume{
		Name:        vol.Name,
		Description: vol.Description,
		Pool:        vol.Pool,
		Type:        vol.Type,
		ContentType: vol.ContentType,
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
	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
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
	if ok && !isDevLXDVolumeOwner(vol.Config, requestor.CallerUsername()) {
		return response.DevLXDErrorResponse(api.NewStatusError(http.StatusBadRequest, "Volume owner cannot be changed"))
	}

	// Ensure caller's identity ID is retained as the volume owner.
	vol.Config["volatile.devlxd.owner"] = requestor.CallerUsername()

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

	op, err := response.NewResponseCapture(req).RenderToOperation(resp)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	return response.DevLXDOperationResponse(*op)
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
	op, err := response.NewResponseCapture(req).RenderToOperation(resp)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	return response.DevLXDOperationResponse(*op)
}

// devLXDStoragePoolVolumeSnapshotsGetHandler retrieves all snapshots for the given volume.
// If the specified storage volume is not owned by the caller, a generic not found error is returned.
func devLXDStoragePoolVolumeSnapshotsGetHandler(d *Daemon, r *http.Request) response.Response {
	inst, err := getInstanceFromContextAndCheckSecurityFlags(r.Context(), devLXDSecurityKey, devLXDSecurityManagementVolumesKey)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	poolName, volType, volName, err := extractVolumeParams(r)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	projectName := inst.Project().Name
	target := r.URL.Query().Get("target")

	// Restrict access to custom volumes.
	if volType != "custom" {
		return response.DevLXDErrorResponse(api.NewStatusError(http.StatusBadRequest, "Only snapshots from custom storage volumes can be retrieved"))
	}

	// Non-recursive requests are currently not supported.
	recursion, _ := util.IsRecursionRequest(r)
	if recursion == 0 {
		return response.DevLXDErrorResponse(api.NewStatusError(http.StatusNotImplemented, "Only recursive requests are currently supported"))
	}

	// Retrieve the parent volume first to ensure the caller owns it.
	_, _, err = devLXDStoragePoolVolumeGet(r.Context(), d, target, projectName, poolName, volName, volType)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	// Get storage volume snapshots.
	url := api.NewURL().Path("1.0", "storage-pools", poolName, "volumes", volType, volName, "snapshots").Project(projectName).WithQuery("recursion", "1")
	if target != "" {
		url = url.WithQuery("target", target)
	}

	req, err := lxd.NewRequestWithContext(r.Context(), http.MethodGet, url.String(), nil, "")
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	var snapshots []api.StorageVolumeSnapshot

	resp := storagePoolVolumeSnapshotsTypeGet(d, req)
	etag, err := response.NewResponseCapture(req).RenderToStruct(resp, &snapshots)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	// Map to devLXD response.
	respSnapshots := make([]api.DevLXDStorageVolumeSnapshot, 0, len(snapshots))
	for _, snap := range snapshots {
		respSnapshots = append(respSnapshots, api.DevLXDStorageVolumeSnapshot{
			Name:        snap.Name,
			Description: snap.Description,
			ContentType: snap.ContentType,
			Config:      snap.Config,
		})
	}

	return response.DevLXDResponseETag(http.StatusOK, respSnapshots, "json", etag)
}

// devLXDStoragePoolVolumeSnapshotsPostHandler creates a new snapshot for the specified storage volume.
// If the storage volume is not owned by the caller, a generic not found error is returned.
func devLXDStoragePoolVolumeSnapshotsPostHandler(d *Daemon, r *http.Request) response.Response {
	inst, err := getInstanceFromContextAndCheckSecurityFlags(r.Context(), devLXDSecurityKey, devLXDSecurityManagementVolumesKey)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	poolName, volType, volName, err := extractVolumeParams(r)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	projectName := inst.Project().Name
	target := r.URL.Query().Get("target")

	// Restrict access to custom volumes.
	if volType != "custom" {
		return response.DevLXDErrorResponse(api.NewStatusError(http.StatusBadRequest, "Only snapshots for custom storage volumes can be created"))
	}

	// Retrieve the parent volume first to ensure the caller owns it.
	_, _, err = devLXDStoragePoolVolumeGet(r.Context(), d, target, projectName, poolName, volName, volType)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	// Decode the request body.
	snap := api.DevLXDStorageVolumeSnapshotsPost{}
	err = json.NewDecoder(r.Body).Decode(&snap)
	if err != nil {
		return response.DevLXDErrorResponse(api.StatusErrorf(http.StatusInternalServerError, "Failed decoding request body: %w", err))
	}

	reqBody := api.StorageVolumeSnapshotsPost{
		Name:        snap.Name,
		Description: snap.Description,
	}

	// Create storage volume snapshot.
	url := api.NewURL().Path("1.0", "storage-pools", poolName, "volumes", volType, volName, "snapshots").Project(projectName)
	if target != "" {
		url = url.WithQuery("target", target)
	}

	req, err := lxd.NewRequestWithContext(r.Context(), http.MethodPost, url.String(), reqBody, "")
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	resp := storagePoolVolumeSnapshotsTypePost(d, req)
	op, err := response.NewResponseCapture(req).RenderToOperation(resp)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	return response.DevLXDOperationResponse(*op)
}

// devLXDStoragePoolVolumeSnapshotGetHandler retrieves information about the specified storage volume snapshot.
// If the snapshot is not found or the parent volume is not owned by the caller, a generic not found error is returned.
func devLXDStoragePoolVolumeSnapshotGetHandler(d *Daemon, r *http.Request) response.Response {
	inst, err := getInstanceFromContextAndCheckSecurityFlags(r.Context(), devLXDSecurityKey, devLXDSecurityManagementVolumesKey)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	poolName, volType, volName, err := extractVolumeParams(r)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	snapName, err := url.PathUnescape(mux.Vars(r)["snapshotName"])
	if err != nil {
		return response.DevLXDErrorResponse(api.NewGenericStatusError(http.StatusBadRequest))
	}

	projectName := inst.Project().Name
	target := r.URL.Query().Get("target")

	// Restrict access to custom volumes.
	if volType != "custom" {
		return response.DevLXDErrorResponse(api.NewStatusError(http.StatusBadRequest, "Only snapshots from custom storage volumes can be retrieved"))
	}

	// Retrieve the parent volume first to ensure the caller owns it.
	_, _, err = devLXDStoragePoolVolumeGet(r.Context(), d, target, projectName, poolName, volName, volType)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	// Get storage volume snapshot.
	url := api.NewURL().Path("1.0", "storage-pools", poolName, "volumes", volType, volName, "snapshots", snapName).Project(projectName)
	if target != "" {
		url = url.WithQuery("target", target)
	}

	req, err := lxd.NewRequestWithContext(r.Context(), http.MethodGet, url.String(), nil, "")
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	var snapshot api.StorageVolumeSnapshot

	resp := storagePoolVolumeSnapshotTypeGet(d, req)
	etag, err := response.NewResponseCapture(req).RenderToStruct(resp, &snapshot)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	// Map to devLXD response.
	respSnapshot := api.DevLXDStorageVolumeSnapshot{
		Name:        snapshot.Name,
		Description: snapshot.Description,
		ContentType: snapshot.ContentType,
		Config:      snapshot.Config,
	}

	return response.DevLXDResponseETag(http.StatusOK, respSnapshot, "json", etag)
}

// devLXDStoragePoolVolumeSnapshotDeleteHandler deletes the specified storage volume snapshot.
// If the snapshot is not found or the parent volume is not owned by the caller, a generic not found error is returned.
func devLXDStoragePoolVolumeSnapshotDeleteHandler(d *Daemon, r *http.Request) response.Response {
	inst, err := getInstanceFromContextAndCheckSecurityFlags(r.Context(), devLXDSecurityKey, devLXDSecurityManagementVolumesKey)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	poolName, volType, volName, err := extractVolumeParams(r)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	snapName, err := url.PathUnescape(mux.Vars(r)["snapshotName"])
	if err != nil {
		return response.DevLXDErrorResponse(api.NewGenericStatusError(http.StatusBadRequest))
	}

	projectName := inst.Project().Name
	target := r.URL.Query().Get("target")

	// Restrict access to custom volumes.
	if volType != "custom" {
		return response.DevLXDErrorResponse(api.NewStatusError(http.StatusBadRequest, "Only snapshots from custom storage volumes can be deleted"))
	}

	// Retrieve the parent volume first to ensure the caller owns it.
	_, _, err = devLXDStoragePoolVolumeGet(r.Context(), d, target, projectName, poolName, volName, volType)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	// Delete storage volume snapshot.
	url := api.NewURL().Path("1.0", "storage-pools", poolName, "volumes", volType, volName, "snapshots", snapName).Project(projectName)
	if target != "" {
		url = url.WithQuery("target", target)
	}

	req, err := lxd.NewRequestWithContext(r.Context(), http.MethodDelete, url.String(), nil, "")
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	resp := storagePoolVolumeSnapshotTypeDelete(d, req)
	op, err := response.NewResponseCapture(req).RenderToOperation(resp)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	return response.DevLXDOperationResponse(*op)
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
