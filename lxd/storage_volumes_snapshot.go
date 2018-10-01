package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
)

var storagePoolVolumeSnapshotsTypeCmd = Command{
	name: "storage-pools/{pool}/volumes/{type}/{name}/snapshots",
	post: storagePoolVolumeSnapshotsTypePost,
	get:  storagePoolVolumeSnapshotsTypeGet,
}

var storagePoolVolumeSnapshotTypeCmd = Command{
	name:   "storage-pools/{pool}/volumes/{type}/{name}/snapshots/{snapshotName}",
	post:   storagePoolVolumeSnapshotTypePost,
	get:    storagePoolVolumeSnapshotTypeGet,
	put:    storagePoolVolumeSnapshotTypePut,
	delete: storagePoolVolumeSnapshotTypeDelete,
}

func storagePoolVolumeSnapshotsTypePost(d *Daemon, r *http.Request) Response {
	// Get the name of the pool.
	poolName := mux.Vars(r)["pool"]

	// Get the name of the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	// Get the name of the volume.
	volumeName := mux.Vars(r)["name"]

	// Parse the request.
	req := api.StorageVolumeSnapshotsPost{}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return BadRequest(err)
	}

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePoolVolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return BadRequest(fmt.Errorf("invalid storage volume type \"%d\"", volumeType))
	}

	// Get a snapshot name.
	if req.Name == "" {
		i := d.cluster.StorageVolumeNextSnapshot(volumeName, volumeType)
		req.Name = fmt.Sprintf("snap%d", i)
	}

	// Validate the name
	err = storageValidName(req.Name)
	if err != nil {
		return BadRequest(err)
	}

	// Retrieve ID of the storage pool (and check if the storage pool
	// exists).
	poolID, err := d.cluster.StoragePoolGetID(poolName)
	if err != nil {
		return SmartError(err)
	}

	response := ForwardedResponseIfTargetIsRemote(d, r)
	if response != nil {
		return response
	}

	response = ForwardedResponseIfVolumeIsRemote(d, r, poolID, volumeName, volumeType)
	if response != nil {
		return response
	}

	// Ensure that the storage volume exists.
	storage, err := storagePoolVolumeInit(d.State(), "default", poolName, volumeName, volumeType)
	if err != nil {
		return SmartError(err)
	}

	// Ensure that it doens't already fucking exist
	_, _, err = d.cluster.StoragePoolNodeVolumeGetType(fmt.Sprintf("%s/%s", volumeName, req.Name), volumeType, poolID)
	if err != db.ErrNoSuchObject {
		if err != nil {
			return SmartError(err)
		}

		return Conflict(fmt.Errorf("Snapshot '%s' already in use", req.Name))
	}

	// Start the storage.
	ourMount, err := storage.StoragePoolVolumeMount()
	if err != nil {
		return SmartError(err)
	}
	if ourMount {
		defer storage.StoragePoolVolumeUmount()
	}

	volWritable := storage.GetStoragePoolVolumeWritable()
	fullSnapName := fmt.Sprintf("%s%s%s", volumeName, shared.SnapshotDelimiter, req.Name)
	req.Name = fullSnapName
	snapshot := func(op *operation) error {
		dbArgs := &db.StorageVolumeArgs{
			Name:        fullSnapName,
			PoolName:    poolName,
			TypeName:    volumeTypeName,
			Snapshot:    true,
			Config:      volWritable.Config,
			Description: volWritable.Description,
		}

		err = storage.StoragePoolVolumeSnapshotCreate(&req)
		if err != nil {
			return err
		}

		_, err = storagePoolVolumeSnapshotDBCreateInternal(d.State(), dbArgs)
		if err != nil {
			return err
		}
		return nil
	}

	resources := map[string][]string{}
	resources["storage_volumes"] = []string{volumeName}

	op, err := operationCreate(d.cluster, "", operationClassTask, db.OperationVolumeSnapshotCreate, resources, nil, snapshot, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

func storagePoolVolumeSnapshotsTypeGet(d *Daemon, r *http.Request) Response {
	// Get the name of the pool the storage volume is supposed to be
	// attached to.
	poolName := mux.Vars(r)["pool"]

	recursion := util.IsRecursionRequest(r)

	// Get the name of the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	// Get the name of the volume type.
	volumeName := mux.Vars(r)["name"]

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
	poolID, err := d.cluster.StoragePoolGetID(poolName)
	if err != nil {
		return SmartError(err)
	}

	// Get the names of all storage volume snapshots of a given volume
	volumes, err := d.cluster.StoragePoolVolumeSnapshotsGetType(volumeName, volumeType, poolID)
	if err != nil {
		return SmartError(err)
	}

	resultString := []string{}
	resultMap := []*api.StorageVolumeSnapshot{}
	for _, volume := range volumes {
		_, snapshotName, _ := containerGetParentAndSnapshotName(volume)

		if !recursion {
			apiEndpoint, err := storagePoolVolumeTypeToAPIEndpoint(volumeType)
			if err != nil {
				return InternalError(err)
			}
			resultString = append(resultString, fmt.Sprintf("/%s/storage-pools/%s/volumes/%s/%s/snapshots/%s", version.APIVersion, poolName, apiEndpoint, volumeName, snapshotName))
		} else {
			_, vol, err := d.cluster.StoragePoolNodeVolumeGetType(volume, volumeType, poolID)
			if err != nil {
				continue
			}

			volumeUsedBy, err := storagePoolVolumeUsedByGet(d.State(), "default", vol.Name, vol.Type)
			if err != nil {
				return SmartError(err)
			}
			vol.UsedBy = volumeUsedBy

			tmp := &api.StorageVolumeSnapshot{}
			tmp.Config = vol.Config
			tmp.Description = vol.Description
			tmp.Name = vol.Name

			resultMap = append(resultMap, tmp)
		}
	}

	if !recursion {
		return SyncResponse(true, resultString)
	}

	return SyncResponse(true, resultMap)
}

func storagePoolVolumeSnapshotTypePost(d *Daemon, r *http.Request) Response {
	// Get the name of the storage pool the volume is supposed to be
	// attached to.
	poolName := mux.Vars(r)["pool"]

	// Get the name of the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	// Get the name of the storage volume.
	volumeName := mux.Vars(r)["name"]

	// Get the name of the storage volume.
	snapshotName := mux.Vars(r)["snapshotName"]

	req := api.StorageVolumeSnapshotPost{}

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

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePoolVolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if volumeType != storagePoolVolumeTypeCustom {
		return BadRequest(fmt.Errorf("invalid storage volume type %s", volumeTypeName))
	}

	response := ForwardedResponseIfTargetIsRemote(d, r)
	if response != nil {
		return response
	}

	poolID, _, err := d.cluster.StoragePoolGet(poolName)
	if err != nil {
		return SmartError(err)
	}

	fullSnapshotName := fmt.Sprintf("%s/%s", volumeName, snapshotName)
	response = ForwardedResponseIfVolumeIsRemote(d, r, poolID, fullSnapshotName, volumeType)
	if response != nil {
		return response
	}

	s, err := storagePoolVolumeInit(d.State(), "default", poolName, fullSnapshotName, volumeType)
	if err != nil {
		return NotFound(err)
	}

	snapshotRename := func(op *operation) error {
		err = s.StoragePoolVolumeSnapshotRename(req.Name)
		if err != nil {
			return err
		}

		return nil
	}

	resources := map[string][]string{}
	resources["storage_volume_snapshots"] = []string{volumeName}

	op, err := operationCreate(d.cluster, "", operationClassTask, db.OperationVolumeSnapshotDelete, resources, nil, snapshotRename, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

func storagePoolVolumeSnapshotTypeGet(d *Daemon, r *http.Request) Response {
	// Get the name of the storage pool the volume is supposed to be
	// attached to.
	poolName := mux.Vars(r)["pool"]

	// Get the name of the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	// Get the name of the storage volume.
	volumeName := mux.Vars(r)["name"]

	// Get the name of the storage volume.
	snapshotName := mux.Vars(r)["snapshotName"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePoolVolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if volumeType != storagePoolVolumeTypeCustom {
		return BadRequest(fmt.Errorf("invalid storage volume type %s", volumeTypeName))
	}

	response := ForwardedResponseIfTargetIsRemote(d, r)
	if response != nil {
		return response
	}

	poolID, _, err := d.cluster.StoragePoolGet(poolName)
	if err != nil {
		return SmartError(err)
	}

	fullSnapshotName := fmt.Sprintf("%s/%s", volumeName, snapshotName)
	response = ForwardedResponseIfVolumeIsRemote(d, r, poolID, fullSnapshotName, volumeType)
	if response != nil {
		return response
	}

	_, volume, err := d.cluster.StoragePoolNodeVolumeGetType(fullSnapshotName, volumeType, poolID)
	if err != nil {
		return SmartError(err)
	}

	snapshot := api.StorageVolumeSnapshot{}
	snapshot.Config = volume.Config
	snapshot.Description = volume.Description
	snapshot.Name = snapshotName

	etag := []interface{}{snapshot.Name, snapshot.Description, snapshot.Config}

	return SyncResponseETag(true, &snapshot, etag)
}

func storagePoolVolumeSnapshotTypePut(d *Daemon, r *http.Request) Response {
	// Get the name of the storage pool the volume is supposed to be
	// attached to.
	poolName := mux.Vars(r)["pool"]

	// Get the name of the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	// Get the name of the storage volume.
	volumeName := mux.Vars(r)["name"]

	// Get the name of the storage volume.
	snapshotName := mux.Vars(r)["snapshotName"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePoolVolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if volumeType != storagePoolVolumeTypeCustom {
		return BadRequest(fmt.Errorf("invalid storage volume type %s", volumeTypeName))
	}

	response := ForwardedResponseIfTargetIsRemote(d, r)
	if response != nil {
		return response
	}

	poolID, _, err := d.cluster.StoragePoolGet(poolName)
	if err != nil {
		return SmartError(err)
	}

	fullSnapshotName := fmt.Sprintf("%s/%s", volumeName, snapshotName)
	response = ForwardedResponseIfVolumeIsRemote(d, r, poolID, fullSnapshotName, volumeType)
	if response != nil {
		return response
	}

	_, volume, err := d.cluster.StoragePoolNodeVolumeGetType(fullSnapshotName, volumeType, poolID)
	if err != nil {
		return SmartError(err)
	}

	// Validate the ETag
	etag := []interface{}{snapshotName, volume.Description, volume.Config}
	err = util.EtagCheck(r, etag)
	if err != nil {
		return PreconditionFailed(err)
	}

	req := api.StorageVolumeSnapshotPut{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return BadRequest(err)
	}

	var do func(*operation) error
	var opDescription db.OperationType
	do = func(op *operation) error {
		err = storagePoolVolumeSnapshotUpdate(d.State(), poolName, volume.Name, volumeType, req.Description)
		if err != nil {
			return err
		}

		opDescription = db.OperationVolumeSnapshotDelete
		return nil
	}

	resources := map[string][]string{}
	resources["storage_volume_snapshots"] = []string{volumeName}

	op, err := operationCreate(d.cluster, "", operationClassTask, opDescription, resources, nil, do, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

func storagePoolVolumeSnapshotTypeDelete(d *Daemon, r *http.Request) Response {
	// Get the name of the storage pool the volume is supposed to be
	// attached to.
	poolName := mux.Vars(r)["pool"]

	// Get the name of the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	// Get the name of the storage volume.
	volumeName := mux.Vars(r)["name"]

	// Get the name of the storage volume.
	snapshotName := mux.Vars(r)["snapshotName"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePoolVolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if volumeType != storagePoolVolumeTypeCustom {
		return BadRequest(fmt.Errorf("invalid storage volume type %s", volumeTypeName))
	}

	response := ForwardedResponseIfTargetIsRemote(d, r)
	if response != nil {
		return response
	}

	poolID, _, err := d.cluster.StoragePoolGet(poolName)
	if err != nil {
		return SmartError(err)
	}

	fullSnapshotName := fmt.Sprintf("%s/%s", volumeName, snapshotName)
	response = ForwardedResponseIfVolumeIsRemote(d, r, poolID, fullSnapshotName, volumeType)
	if response != nil {
		return response
	}

	s, err := storagePoolVolumeInit(d.State(), "default", poolName, fullSnapshotName, volumeType)
	if err != nil {
		return NotFound(err)
	}

	snapshotDelete := func(op *operation) error {
		err = s.StoragePoolVolumeSnapshotDelete()
		if err != nil {
			return err
		}

		return nil
	}

	resources := map[string][]string{}
	resources["storage_volume_snapshots"] = []string{volumeName}

	op, err := operationCreate(d.cluster, "", operationClassTask, db.OperationVolumeSnapshotDelete, resources, nil, snapshotDelete, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}
