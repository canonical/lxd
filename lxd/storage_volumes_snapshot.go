package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
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

	// Get a snapshot name.
	if req.Name == "" {
		// i := d.cluster.ContainerNextSnapshot(volumeName)
		i := 0
		req.Name = fmt.Sprintf("snap%d", i)
	}

	// Validate the name
	err = storageValidName(req.Name)
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
	storage, err := storagePoolVolumeInit(d.State(), poolName, volumeName, volumeType)
	if err != nil {
		return SmartError(err)
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
			Kind:        db.StorageVolumeKindSnapshot,
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

	op, err := operationCreate(d.cluster, operationClassTask, db.OperationVolumeSnapshotCreate, resources, nil, snapshot, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

func storagePoolVolumeSnapshotsTypeGet(d *Daemon, r *http.Request) Response {
	return NotImplemented(fmt.Errorf("Retrieving storage pool volume snapshots is not implemented"))
}

func storagePoolVolumeSnapshotTypePost(d *Daemon, r *http.Request) Response {
	return NotImplemented(fmt.Errorf("Updating storage pool volume snapshots is not implemented"))
}

func storagePoolVolumeSnapshotTypeGet(d *Daemon, r *http.Request) Response {
	return NotImplemented(fmt.Errorf("Retrieving a storage pool volume snapshot is not implemented"))
}

func storagePoolVolumeSnapshotTypeDelete(d *Daemon, r *http.Request) Response {
	return NotImplemented(fmt.Errorf("Deleting storage pool volume snapshots is not implemented"))
}
