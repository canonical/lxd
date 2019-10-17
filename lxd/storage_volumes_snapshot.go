package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/response"
	storagePools "github.com/lxc/lxd/lxd/storage"
	storageDrivers "github.com/lxc/lxd/lxd/storage/drivers"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
)

var storagePoolVolumeSnapshotsTypeCmd = APIEndpoint{
	Path: "storage-pools/{pool}/volumes/{type}/{name}/snapshots",

	Get:  APIEndpointAction{Handler: storagePoolVolumeSnapshotsTypeGet, AccessHandler: AllowAuthenticated},
	Post: APIEndpointAction{Handler: storagePoolVolumeSnapshotsTypePost},
}

var storagePoolVolumeSnapshotTypeCmd = APIEndpoint{
	Path: "storage-pools/{pool}/volumes/{type}/{name}/snapshots/{snapshotName}",

	Delete: APIEndpointAction{Handler: storagePoolVolumeSnapshotTypeDelete},
	Get:    APIEndpointAction{Handler: storagePoolVolumeSnapshotTypeGet, AccessHandler: AllowAuthenticated},
	Post:   APIEndpointAction{Handler: storagePoolVolumeSnapshotTypePost},
	Put:    APIEndpointAction{Handler: storagePoolVolumeSnapshotTypePut},
}

func storagePoolVolumeSnapshotsTypePost(d *Daemon, r *http.Request) response.Response {
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
		return response.BadRequest(err)
	}

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type \"%d\"", volumeType))
	}

	// Get a snapshot name.
	if req.Name == "" {
		i := d.cluster.StorageVolumeNextSnapshot(volumeName, volumeType)
		req.Name = fmt.Sprintf("snap%d", i)
	}

	// Validate the name
	err = storagePools.ValidName(req.Name)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that this isn't a restricted volume
	used, err := daemonStorageUsed(d.State(), poolName, volumeName)
	if err != nil {
		return response.InternalError(err)
	}

	if used {
		return response.BadRequest(fmt.Errorf("Volumes used by LXD itself cannot have snapshots"))
	}

	// Retrieve ID of the storage pool (and check if the storage pool
	// exists).
	poolID, err := d.cluster.StoragePoolGetID(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	resp := ForwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	resp = ForwardedResponseIfVolumeIsRemote(d, r, poolID, volumeName, volumeType)
	if resp != nil {
		return resp
	}

	// Ensure that the storage volume exists.
	storage, err := storagePoolVolumeInit(d.State(), "default", poolName, volumeName, volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	// Ensure that the snapshot doens't already exist
	_, _, err = d.cluster.StoragePoolNodeVolumeGetType(fmt.Sprintf("%s/%s", volumeName, req.Name), volumeType, poolID)
	if err != db.ErrNoSuchObject {
		if err != nil {
			return response.SmartError(err)
		}

		return response.Conflict(fmt.Errorf("Snapshot '%s' already in use", req.Name))
	}

	// Start the storage.
	ourMount, err := storage.StoragePoolVolumeMount()
	if err != nil {
		return response.SmartError(err)
	}
	if ourMount {
		defer storage.StoragePoolVolumeUmount()
	}

	volWritable := storage.GetStoragePoolVolumeWritable()
	fullSnapName := fmt.Sprintf("%s%s%s", volumeName, shared.SnapshotDelimiter, req.Name)
	req.Name = fullSnapName
	snapshot := func(op *operations.Operation) error {
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

	op, err := operations.OperationCreate(d.State(), "", operations.OperationClassTask, db.OperationVolumeSnapshotCreate, resources, nil, snapshot, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func storagePoolVolumeSnapshotsTypeGet(d *Daemon, r *http.Request) response.Response {
	// Get the name of the pool the storage volume is supposed to be
	// attached to.
	poolName := mux.Vars(r)["pool"]

	recursion := util.IsRecursionRequest(r)

	// Get the name of the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	// Get the name of the volume type.
	volumeName := mux.Vars(r)["name"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}
	// Check that the storage volume type is valid.
	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return response.BadRequest(fmt.Errorf("invalid storage volume type %s", volumeTypeName))
	}

	// Retrieve ID of the storage pool (and check if the storage pool
	// exists).
	poolID, err := d.cluster.StoragePoolGetID(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Get the names of all storage volume snapshots of a given volume
	volumes, err := d.cluster.StoragePoolVolumeSnapshotsGetType(volumeName, volumeType, poolID)
	if err != nil {
		return response.SmartError(err)
	}

	resultString := []string{}
	resultMap := []*api.StorageVolumeSnapshot{}
	for _, volume := range volumes {
		_, snapshotName, _ := shared.ContainerGetParentAndSnapshotName(volume.Name)

		if !recursion {
			apiEndpoint, err := storagePoolVolumeTypeToAPIEndpoint(volumeType)
			if err != nil {
				return response.InternalError(err)
			}
			resultString = append(resultString, fmt.Sprintf("/%s/storage-pools/%s/volumes/%s/%s/snapshots/%s", version.APIVersion, poolName, apiEndpoint, volumeName, snapshotName))
		} else {
			_, vol, err := d.cluster.StoragePoolNodeVolumeGetType(volume.Name, volumeType, poolID)
			if err != nil {
				continue
			}

			volumeUsedBy, err := storagePoolVolumeUsedByGet(d.State(), "default", poolName, vol.Name, vol.Type)
			if err != nil {
				return response.SmartError(err)
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
		return response.SyncResponse(true, resultString)
	}

	return response.SyncResponse(true, resultMap)
}

func storagePoolVolumeSnapshotTypePost(d *Daemon, r *http.Request) response.Response {
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
		return response.BadRequest(err)
	}

	// Sanity checks.
	if req.Name == "" {
		return response.BadRequest(fmt.Errorf("No name provided"))
	}

	if strings.Contains(req.Name, "/") {
		return response.BadRequest(fmt.Errorf("Storage volume names may not contain slashes"))
	}

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if volumeType != storagePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("invalid storage volume type %s", volumeTypeName))
	}

	resp := ForwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	poolID, _, err := d.cluster.StoragePoolGet(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	fullSnapshotName := fmt.Sprintf("%s/%s", volumeName, snapshotName)
	resp = ForwardedResponseIfVolumeIsRemote(d, r, poolID, fullSnapshotName, volumeType)
	if resp != nil {
		return resp
	}

	s, err := storagePoolVolumeInit(d.State(), "default", poolName, fullSnapshotName, volumeType)
	if err != nil {
		return response.NotFound(err)
	}

	snapshotRename := func(op *operations.Operation) error {
		// Check if we can load new storage layer for pool driver type.
		pool, err := storagePools.GetPoolByName(d.State(), poolName)
		if err != storageDrivers.ErrUnknownDriver {
			if err != nil {
				return err
			}

			err = pool.RenameCustomVolumeSnapshot(fullSnapshotName, req.Name, op)
		} else {
			err = s.StoragePoolVolumeSnapshotRename(req.Name)
		}

		return err
	}

	resources := map[string][]string{}
	resources["storage_volume_snapshots"] = []string{volumeName}

	op, err := operations.OperationCreate(d.State(), "", operations.OperationClassTask, db.OperationVolumeSnapshotDelete, resources, nil, snapshotRename, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func storagePoolVolumeSnapshotTypeGet(d *Daemon, r *http.Request) response.Response {
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
	volumeType, err := storagePools.VolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if volumeType != storagePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("invalid storage volume type %s", volumeTypeName))
	}

	resp := ForwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	poolID, _, err := d.cluster.StoragePoolGet(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	fullSnapshotName := fmt.Sprintf("%s/%s", volumeName, snapshotName)
	resp = ForwardedResponseIfVolumeIsRemote(d, r, poolID, fullSnapshotName, volumeType)
	if resp != nil {
		return resp
	}

	_, volume, err := d.cluster.StoragePoolNodeVolumeGetType(fullSnapshotName, volumeType, poolID)
	if err != nil {
		return response.SmartError(err)
	}

	snapshot := api.StorageVolumeSnapshot{}
	snapshot.Config = volume.Config
	snapshot.Description = volume.Description
	snapshot.Name = snapshotName

	etag := []interface{}{snapshot.Name, snapshot.Description, snapshot.Config}

	return response.SyncResponseETag(true, &snapshot, etag)
}

func storagePoolVolumeSnapshotTypePut(d *Daemon, r *http.Request) response.Response {
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
	volumeType, err := storagePools.VolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if volumeType != storagePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("invalid storage volume type %s", volumeTypeName))
	}

	resp := ForwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	poolID, _, err := d.cluster.StoragePoolGet(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	fullSnapshotName := fmt.Sprintf("%s/%s", volumeName, snapshotName)
	resp = ForwardedResponseIfVolumeIsRemote(d, r, poolID, fullSnapshotName, volumeType)
	if resp != nil {
		return resp
	}

	_, volume, err := d.cluster.StoragePoolNodeVolumeGetType(fullSnapshotName, volumeType, poolID)
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag
	etag := []interface{}{snapshotName, volume.Description, volume.Config}
	err = util.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	req := api.StorageVolumeSnapshotPut{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	var do func(*operations.Operation) error
	var opDescription db.OperationType
	do = func(op *operations.Operation) error {
		err = storagePoolVolumeSnapshotUpdate(d.State(), poolName, volume.Name, volumeType, req.Description)
		if err != nil {
			return err
		}

		opDescription = db.OperationVolumeSnapshotDelete
		return nil
	}

	resources := map[string][]string{}
	resources["storage_volume_snapshots"] = []string{volumeName}

	op, err := operations.OperationCreate(d.State(), "", operations.OperationClassTask, opDescription, resources, nil, do, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func storagePoolVolumeSnapshotTypeDelete(d *Daemon, r *http.Request) response.Response {
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
	volumeType, err := storagePools.VolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if volumeType != storagePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("invalid storage volume type %s", volumeTypeName))
	}

	resp := ForwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	poolID, _, err := d.cluster.StoragePoolGet(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	fullSnapshotName := fmt.Sprintf("%s/%s", volumeName, snapshotName)
	resp = ForwardedResponseIfVolumeIsRemote(d, r, poolID, fullSnapshotName, volumeType)
	if resp != nil {
		return resp
	}

	s, err := storagePoolVolumeInit(d.State(), "default", poolName, fullSnapshotName, volumeType)
	if err != nil {
		return response.NotFound(err)
	}

	snapshotDelete := func(op *operations.Operation) error {
		// Check if we can load new storage layer for pool driver type.
		pool, err := storagePools.GetPoolByName(d.State(), poolName)
		if err != storageDrivers.ErrUnknownDriver {
			if err != nil {
				return err
			}

			err = pool.DeleteCustomVolumeSnapshot(fullSnapshotName, op)
		} else {
			err = s.StoragePoolVolumeSnapshotDelete()
		}

		return err
	}

	resources := map[string][]string{}
	resources["storage_volume_snapshots"] = []string{volumeName}

	op, err := operations.OperationCreate(d.State(), "", operations.OperationClassTask, db.OperationVolumeSnapshotDelete, resources, nil, snapshotDelete, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}
