package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
)

// /1.0/storage-pools/{name}/volumes
// List all storage volumes attached to a given storage pool.
func storagePoolVolumesGet(d *Daemon, r *http.Request) Response {
	poolName := mux.Vars(r)["name"]

	recursionStr := r.FormValue("recursion")
	recursion, err := strconv.Atoi(recursionStr)
	if err != nil {
		recursion = 0
	}

	// Retrieve ID of the storage pool (and check if the storage pool
	// exists).
	poolID, err := d.db.StoragePoolGetID(poolName)
	if err != nil {
		return SmartError(err)
	}

	// Get all volumes currently attached to the storage pool by ID of the
	// pool.
	volumes, err := d.db.StoragePoolVolumesGet(poolID, supportedVolumeTypes)
	if err != nil && err != db.NoSuchObjectError {
		return SmartError(err)
	}

	resultString := []string{}
	for _, volume := range volumes {
		apiEndpoint, err := storagePoolVolumeTypeNameToAPIEndpoint(volume.Type)
		if err != nil {
			return InternalError(err)
		}

		if recursion == 0 {
			resultString = append(resultString, fmt.Sprintf("/%s/storage-pools/%s/volumes/%s/%s", version.APIVersion, poolName, apiEndpoint, volume.Name))
		} else {
			volumeUsedBy, err := storagePoolVolumeUsedByGet(d.State(), volume.Name, volume.Type)
			if err != nil {
				return InternalError(err)
			}
			volume.UsedBy = volumeUsedBy
		}
	}

	if recursion == 0 {
		return SyncResponse(true, resultString)
	}

	return SyncResponse(true, volumes)
}

var storagePoolVolumesCmd = Command{name: "storage-pools/{name}/volumes", get: storagePoolVolumesGet}

// /1.0/storage-pools/{name}/volumes/{type}
// List all storage volumes of a given volume type for a given storage pool.
func storagePoolVolumesTypeGet(d *Daemon, r *http.Request) Response {
	// Get the name of the pool the storage volume is supposed to be
	// attached to.
	poolName := mux.Vars(r)["name"]

	recursionStr := r.FormValue("recursion")
	recursion, err := strconv.Atoi(recursionStr)
	if err != nil {
		recursion = 0
	}

	// Get the name of the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePoolVolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return BadRequest(err)
	}
	// Check that the storage volume type is valid.
	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return BadRequest(fmt.Errorf("invalid storage volume type %s", volumeTypeName))
	}

	// Retrieve ID of the storage pool (and check if the storage pool
	// exists).
	poolID, err := d.db.StoragePoolGetID(poolName)
	if err != nil {
		return SmartError(err)
	}

	// Get the names of all storage volumes of a given volume type currently
	// attached to the storage pool.
	volumes, err := d.db.StoragePoolVolumesGetType(volumeType, poolID)
	if err != nil {
		return SmartError(err)
	}

	resultString := []string{}
	resultMap := []*api.StorageVolume{}
	for _, volume := range volumes {
		if recursion == 0 {
			apiEndpoint, err := storagePoolVolumeTypeToAPIEndpoint(volumeType)
			if err != nil {
				return InternalError(err)
			}
			resultString = append(resultString, fmt.Sprintf("/%s/storage-pools/%s/volumes/%s/%s", version.APIVersion, poolName, apiEndpoint, volume))
		} else {
			_, vol, err := d.db.StoragePoolVolumeGetType(volume, volumeType, poolID)
			if err != nil {
				continue
			}

			volumeUsedBy, err := storagePoolVolumeUsedByGet(d.State(), vol.Name, vol.Type)
			if err != nil {
				return SmartError(err)
			}
			vol.UsedBy = volumeUsedBy

			resultMap = append(resultMap, vol)
		}
	}

	if recursion == 0 {
		return SyncResponse(true, resultString)
	}

	return SyncResponse(true, resultMap)
}

// /1.0/storage-pools/{name}/volumes/{type}
// Create a storage volume of a given volume type in a given storage pool.
func storagePoolVolumesTypePost(d *Daemon, r *http.Request) Response {
	req := api.StorageVolumesPost{}

	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return BadRequest(err)
	}

	// Sanity checks.
	if req.Name == "" {
		return BadRequest(fmt.Errorf("No name provided"))
	}

	if strings.Contains(req.Name, "/") {
		return BadRequest(fmt.Errorf("Storage volume names may not contain slashes"))
	}

	// Check that the user gave use a storage volume type for the storage
	// volume we are about to create.
	if req.Type == "" {
		return BadRequest(fmt.Errorf("you must provide a storage volume type of the storage volume"))
	}

	// Check if the user gave us a valid pool name in which the new storage
	// volume is supposed to be created.
	poolName := mux.Vars(r)["name"]

	// We currently only allow to create storage volumes of type
	// storagePoolVolumeTypeCustom. So check, that nothing else was
	// requested.
	if req.Type != storagePoolVolumeTypeNameCustom {
		return BadRequest(fmt.Errorf(`Currently not allowed to create `+
			`storage volumes of type %s`, req.Type))
	}

	err = storagePoolVolumeCreateInternal(d.State(), poolName, req.Name, req.Description, req.Type, req.Config)
	if err != nil {
		return InternalError(err)
	}

	apiEndpoint, err := storagePoolVolumeTypeNameToAPIEndpoint(req.Type)
	if err != nil {
		return InternalError(err)
	}

	return SyncResponseLocation(true, nil, fmt.Sprintf("/%s/storage-pools/%s/volumes/%s", version.APIVersion, poolName, apiEndpoint))
}

var storagePoolVolumesTypeCmd = Command{name: "storage-pools/{name}/volumes/{type}", get: storagePoolVolumesTypeGet, post: storagePoolVolumesTypePost}

// /1.0/storage-pools/{name}/volumes/{type}/{name}
// Rename a storage volume of a given volume type in a given storage pool.
func storagePoolVolumeTypePost(d *Daemon, r *http.Request) Response {
	// Get the name of the storage volume.
	volumeName := mux.Vars(r)["name"]

	// Get the name of the storage pool the volume is supposed to be
	// attached to.
	poolName := mux.Vars(r)["pool"]

	// Get the name of the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	req := api.StorageVolumePost{}

	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return BadRequest(err)
	}

	// Sanity checks.
	if req.Name == "" {
		return BadRequest(fmt.Errorf("No name provided"))
	}

	if strings.Contains(req.Name, "/") {
		return BadRequest(fmt.Errorf("Storage volume names may not contain slashes"))
	}

	// We currently only allow to create storage volumes of type
	// storagePoolVolumeTypeCustom. So check, that nothing else was
	// requested.
	if volumeTypeName != storagePoolVolumeTypeNameCustom {
		return BadRequest(fmt.Errorf("Renaming storage volumes of type %s is not allowed", volumeTypeName))
	}

	// Retrieve ID of the storage pool (and check if the storage pool
	// exists).
	poolID, err := d.db.StoragePoolGetID(poolName)
	if err != nil {
		return SmartError(err)
	}

	// Check that the name isn't already in use.
	_, err = d.db.StoragePoolVolumeGetTypeID(req.Name,
		storagePoolVolumeTypeCustom, poolID)
	if err == nil || err != nil && err != db.NoSuchObjectError {
		return Conflict
	}

	s, err := storagePoolVolumeInit(d.State(), poolName, volumeName, storagePoolVolumeTypeCustom)
	if err != nil {
		return SmartError(err)
	}

	err = s.StoragePoolVolumeRename(req.Name)
	if err != nil {
		return InternalError(err)
	}

	return EmptySyncResponse
}

// /1.0/storage-pools/{pool}/volumes/{type}/{name}
// Get storage volume of a given volume type on a given storage pool.
func storagePoolVolumeTypeGet(d *Daemon, r *http.Request) Response {
	// Get the name of the storage volume.
	volumeName := mux.Vars(r)["name"]

	// Get the name of the storage pool the volume is supposed to be
	// attached to.
	poolName := mux.Vars(r)["pool"]

	// Get the name of the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePoolVolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return BadRequest(err)
	}
	// Check that the storage volume type is valid.
	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return BadRequest(fmt.Errorf("invalid storage volume type %s", volumeTypeName))
	}

	// Get the ID of the storage pool the storage volume is supposed to be
	// attached to.
	poolID, err := d.db.StoragePoolGetID(poolName)
	if err != nil {
		return SmartError(err)
	}

	// Get the storage volume.
	_, volume, err := d.db.StoragePoolVolumeGetType(volumeName, volumeType, poolID)
	if err != nil {
		return SmartError(err)
	}

	volumeUsedBy, err := storagePoolVolumeUsedByGet(d.State(), volume.Name, volume.Type)
	if err != nil {
		return SmartError(err)
	}
	volume.UsedBy = volumeUsedBy

	etag := []interface{}{volume.Name, volume.Type, volume.Config}

	return SyncResponseETag(true, volume, etag)
}

// /1.0/storage-pools/{pool}/volumes/{type}/{name}
func storagePoolVolumeTypePut(d *Daemon, r *http.Request) Response {
	// Get the name of the storage volume.
	volumeName := mux.Vars(r)["name"]

	// Get the name of the storage pool the volume is supposed to be
	// attached to.
	poolName := mux.Vars(r)["pool"]

	// Get the name of the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePoolVolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return BadRequest(err)
	}
	// Check that the storage volume type is valid.
	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return BadRequest(fmt.Errorf("invalid storage volume type %s", volumeTypeName))
	}

	poolID, pool, err := d.db.StoragePoolGet(poolName)
	if err != nil {
		return SmartError(err)
	}

	// Get the existing storage volume.
	_, volume, err := d.db.StoragePoolVolumeGetType(volumeName, volumeType, poolID)
	if err != nil {
		return SmartError(err)
	}

	// Validate the ETag
	etag := []interface{}{volume.Name, volume.Type, volume.Config}

	err = util.EtagCheck(r, etag)
	if err != nil {
		return PreconditionFailed(err)
	}

	req := api.StorageVolumePut{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	// Validate the configuration
	err = storageVolumeValidateConfig(volumeName, req.Config, pool)
	if err != nil {
		return BadRequest(err)
	}

	err = storagePoolVolumeUpdate(d.State(), poolName, volumeName, volumeType, req.Description, req.Config)
	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}

// /1.0/storage-pools/{pool}/volumes/{type}/{name}
func storagePoolVolumeTypePatch(d *Daemon, r *http.Request) Response {
	// Get the name of the storage volume.
	volumeName := mux.Vars(r)["name"]

	// Get the name of the storage pool the volume is supposed to be
	// attached to.
	poolName := mux.Vars(r)["pool"]

	// Get the name of the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePoolVolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return BadRequest(err)
	}
	// Check that the storage volume type is valid.
	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return BadRequest(fmt.Errorf("invalid storage volume type %s", volumeTypeName))
	}

	// Get the ID of the storage pool the storage volume is supposed to be
	// attached to.
	poolID, pool, err := d.db.StoragePoolGet(poolName)
	if err != nil {
		return SmartError(err)
	}

	// Get the existing storage volume.
	_, volume, err := d.db.StoragePoolVolumeGetType(volumeName, volumeType, poolID)
	if err != nil {
		return SmartError(err)
	}

	// Validate the ETag
	etag := []interface{}{volume.Name, volume.Type, volume.Config}

	err = util.EtagCheck(r, etag)
	if err != nil {
		return PreconditionFailed(err)
	}

	req := api.StorageVolumePut{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	if req.Config == nil {
		req.Config = map[string]string{}
	}

	for k, v := range volume.Config {
		_, ok := req.Config[k]
		if !ok {
			req.Config[k] = v
		}
	}

	// Validate the configuration
	err = storageVolumeValidateConfig(volumeName, req.Config, pool)
	if err != nil {
		return BadRequest(err)
	}

	err = storagePoolVolumeUpdate(d.State(), poolName, volumeName, volumeType, req.Description, req.Config)
	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}

// /1.0/storage-pools/{pool}/volumes/{type}/{name}
func storagePoolVolumeTypeDelete(d *Daemon, r *http.Request) Response {
	// Get the name of the storage volume.
	volumeName := mux.Vars(r)["name"]

	// Get the name of the storage pool the volume is supposed to be
	// attached to.
	poolName := mux.Vars(r)["pool"]

	// Get the name of the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePoolVolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return BadRequest(err)
	}
	// Check that the storage volume type is valid.
	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return BadRequest(fmt.Errorf("invalid storage volume type %s", volumeTypeName))
	}

	switch volumeType {
	case storagePoolVolumeTypeCustom:
		// allowed
	case storagePoolVolumeTypeImage:
		// allowed
	default:
		return BadRequest(fmt.Errorf("storage volumes of type \"%s\" cannot be deleted with the storage api", volumeTypeName))
	}

	volumeUsedBy, err := storagePoolVolumeUsedByGet(d.State(), volumeName, volumeTypeName)
	if err != nil {
		return SmartError(err)
	}

	if len(volumeUsedBy) > 0 {
		if len(volumeUsedBy) != 1 ||
			volumeType != storagePoolVolumeTypeImage ||
			volumeUsedBy[0] != fmt.Sprintf(
				"/%s/images/%s",
				version.APIVersion,
				volumeName) {
			return BadRequest(fmt.Errorf(`The storage volume is ` +
				`still in use by containers or profiles`))
		}
	}

	s, err := storagePoolVolumeInit(d.State(), poolName, volumeName, volumeType)
	if err != nil {
		return NotFound
	}

	switch volumeType {
	case storagePoolVolumeTypeCustom:
		err = s.StoragePoolVolumeDelete()
	case storagePoolVolumeTypeImage:
		err = s.ImageDelete(volumeName)
	default:
		return BadRequest(fmt.Errorf(`Storage volumes of type "%s" `+
			`cannot be deleted with the storage api`,
			volumeTypeName))
	}
	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}

var storagePoolVolumeTypeCmd = Command{name: "storage-pools/{pool}/volumes/{type}/{name:.*}", post: storagePoolVolumeTypePost, get: storagePoolVolumeTypeGet, put: storagePoolVolumeTypePut, patch: storagePoolVolumeTypePatch, delete: storagePoolVolumeTypeDelete}
