package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/grant-he/lxd/lxd/backup"
	"github.com/grant-he/lxd/lxd/db"
	"github.com/grant-he/lxd/lxd/operations"
	"github.com/grant-he/lxd/lxd/project"
	"github.com/grant-he/lxd/lxd/response"
	storagePools "github.com/grant-he/lxd/lxd/storage"
	"github.com/grant-he/lxd/lxd/util"
	"github.com/grant-he/lxd/shared"
	"github.com/grant-he/lxd/shared/api"
	"github.com/grant-he/lxd/shared/version"
	"github.com/pkg/errors"
)

var storagePoolVolumeTypeCustomBackupsCmd = APIEndpoint{
	Path: "storage-pools/{pool}/volumes/{type}/{name}/backups",

	Get:  APIEndpointAction{Handler: storagePoolVolumeTypeCustomBackupsGet, AccessHandler: allowProjectPermission("storage-volumes", "view")},
	Post: APIEndpointAction{Handler: storagePoolVolumeTypeCustomBackupsPost, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
}

var storagePoolVolumeTypeCustomBackupCmd = APIEndpoint{
	Path: "storage-pools/{pool}/volumes/{type}/{name}/backups/{backupName}",

	Get:    APIEndpointAction{Handler: storagePoolVolumeTypeCustomBackupGet, AccessHandler: allowProjectPermission("storage-volumes", "view")},
	Post:   APIEndpointAction{Handler: storagePoolVolumeTypeCustomBackupPost, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Delete: APIEndpointAction{Handler: storagePoolVolumeTypeCustomBackupDelete, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
}

var storagePoolVolumeTypeCustomBackupExportCmd = APIEndpoint{
	Path: "storage-pools/{pool}/volumes/{type}/{name}/backups/{backupName}/export",

	Get: APIEndpointAction{Handler: storagePoolVolumeTypeCustomBackupExportGet, AccessHandler: allowProjectPermission("storage-volumes", "view")},
}

func storagePoolVolumeTypeCustomBackupsGet(d *Daemon, r *http.Request) response.Response {
	projectName, err := project.StorageVolumeProject(d.State().Cluster, projectParam(r), db.StoragePoolVolumeTypeCustom)
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the storage volume.
	volumeName := mux.Vars(r)["name"]
	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName := mux.Vars(r)["pool"]
	// Get the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if volumeType != db.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	poolID, _, err := d.cluster.GetStoragePool(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Handle requests targeted to a volume on a different node
	resp := forwardedResponseIfVolumeIsRemote(d, r, poolName, projectName, volumeName, db.StoragePoolVolumeTypeCustom)
	if resp != nil {
		return resp
	}

	recursion := util.IsRecursionRequest(r)

	volumeBackups, err := d.State().Cluster.GetStoragePoolVolumeBackups(projectName, volumeName, poolID)
	if err != nil {
		return response.SmartError(err)
	}

	backups := make([]*backup.VolumeBackup, len(volumeBackups))

	for i, b := range volumeBackups {
		backups[i] = backup.NewVolumeBackup(d.State(), projectName, poolName, volumeName, b.ID, b.Name, b.CreationDate, b.ExpiryDate, b.VolumeOnly, b.OptimizedStorage)
	}

	resultString := []string{}
	resultMap := []*api.StoragePoolVolumeBackup{}

	for _, backup := range backups {
		if !recursion {
			url := fmt.Sprintf("/%s/storage-pools/%s/volumes/custom/%s/backups/%s",
				version.APIVersion, poolName, volumeName, strings.Split(backup.Name(), "/")[1])
			resultString = append(resultString, url)
		} else {
			render := backup.Render()
			resultMap = append(resultMap, render)
		}
	}

	if !recursion {
		return response.SyncResponse(true, resultString)
	}

	return response.SyncResponse(true, resultMap)
}

func storagePoolVolumeTypeCustomBackupsPost(d *Daemon, r *http.Request) response.Response {
	// Get the name of the storage volume.
	volumeName := mux.Vars(r)["name"]
	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName := mux.Vars(r)["pool"]
	// Get the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if volumeType != db.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	projectName, err := project.StorageVolumeProject(d.State().Cluster, projectParam(r), db.StoragePoolVolumeTypeCustom)
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	poolID, _, err := d.cluster.GetStoragePool(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	resp = forwardedResponseIfVolumeIsRemote(d, r, poolName, projectName, volumeName, db.StoragePoolVolumeTypeCustom)
	if resp != nil {
		return resp
	}

	volumeID, _, err := d.cluster.GetLocalStoragePoolVolume(projectName, volumeName, db.StoragePoolVolumeTypeCustom, poolID)
	if err != nil {
		return response.SmartError(err)
	}

	rj := shared.Jmap{}
	err = json.NewDecoder(r.Body).Decode(&rj)
	if err != nil {
		return response.InternalError(err)
	}

	expiry, _ := rj.GetString("expires_at")
	if expiry == "" {
		// Disable expiration by setting it to zero time.
		rj["expires_at"] = time.Date(1, time.January, 1, 0, 0, 0, 0, time.UTC)
	}

	// Create body with correct expiry.
	body, err := json.Marshal(rj)
	if err != nil {
		return response.InternalError(err)
	}

	req := api.StoragePoolVolumeBackupsPost{}

	err = json.Unmarshal(body, &req)
	if err != nil {
		return response.BadRequest(err)
	}

	if req.Name == "" {
		// come up with a name.
		backups, err := d.cluster.GetStoragePoolVolumeBackupsNames(projectName, volumeName, poolID)
		if err != nil {
			return response.BadRequest(err)
		}

		base := volumeName + shared.SnapshotDelimiter + "backup"
		length := len(base)
		max := 0

		for _, backup := range backups {
			// Ignore backups not containing base.
			if !strings.HasPrefix(backup, base) {
				continue
			}

			substr := backup[length:]
			var num int
			count, err := fmt.Sscanf(substr, "%d", &num)
			if err != nil || count != 1 {
				continue
			}
			if num >= max {
				max = num + 1
			}
		}

		req.Name = fmt.Sprintf("backup%d", max)
	}

	// Validate the name.
	if strings.Contains(req.Name, "/") {
		return response.BadRequest(fmt.Errorf("Backup names may not contain slashes"))
	}

	fullName := volumeName + shared.SnapshotDelimiter + req.Name
	volumeOnly := req.VolumeOnly

	backup := func(op *operations.Operation) error {
		args := db.StoragePoolVolumeBackup{
			Name:                 fullName,
			VolumeID:             volumeID,
			CreationDate:         time.Now(),
			ExpiryDate:           req.ExpiresAt,
			VolumeOnly:           volumeOnly,
			OptimizedStorage:     req.OptimizedStorage,
			CompressionAlgorithm: req.CompressionAlgorithm,
		}

		err := volumeBackupCreate(d.State(), args, projectName, poolName, volumeName)
		if err != nil {
			return errors.Wrap(err, "Create volume backup")
		}

		return nil
	}

	resources := map[string][]string{}
	resources["storage_volumes"] = []string{volumeName}
	resources["backups"] = []string{req.Name}

	op, err := operations.OperationCreate(d.State(), projectName, operations.OperationClassTask,
		db.OperationCustomVolumeBackupCreate, resources, nil, backup, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func storagePoolVolumeTypeCustomBackupGet(d *Daemon, r *http.Request) response.Response {
	// Get the name of the storage volume.
	volumeName := mux.Vars(r)["name"]
	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName := mux.Vars(r)["pool"]
	// Get the volume type.
	volumeTypeName := mux.Vars(r)["type"]
	// Get backup name.
	backupName := mux.Vars(r)["backupName"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if volumeType != db.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	projectName, err := project.StorageVolumeProject(d.State().Cluster, projectParam(r), db.StoragePoolVolumeTypeCustom)
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(d, r, poolName, projectName, volumeName, db.StoragePoolVolumeTypeCustom)
	if resp != nil {
		return resp
	}

	fullName := volumeName + shared.SnapshotDelimiter + backupName

	backup, err := storagePoolVolumeBackupLoadByName(d.State(), projectName, poolName, fullName)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, backup.Render())
}

func storagePoolVolumeTypeCustomBackupPost(d *Daemon, r *http.Request) response.Response {
	// Get the name of the storage volume.
	volumeName := mux.Vars(r)["name"]
	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName := mux.Vars(r)["pool"]
	// Get the volume type.
	volumeTypeName := mux.Vars(r)["type"]
	// Get backup name.
	backupName := mux.Vars(r)["backupName"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if volumeType != db.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	projectName, err := project.StorageVolumeProject(d.State().Cluster, projectParam(r), db.StoragePoolVolumeTypeCustom)
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(d, r, poolName, projectName, volumeName, db.StoragePoolVolumeTypeCustom)
	if resp != nil {
		return resp
	}

	req := api.StoragePoolVolumeBackupPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Validate the name
	if strings.Contains(req.Name, "/") {
		return response.BadRequest(fmt.Errorf("Backup names may not contain slashes"))
	}

	oldName := volumeName + shared.SnapshotDelimiter + backupName

	backup, err := storagePoolVolumeBackupLoadByName(d.State(), projectName, poolName, oldName)
	if err != nil {
		return response.SmartError(err)
	}

	newName := volumeName + shared.SnapshotDelimiter + req.Name

	rename := func(op *operations.Operation) error {
		err := backup.Rename(newName)
		if err != nil {
			return err
		}

		return nil
	}

	resources := map[string][]string{}
	resources["volume"] = []string{volumeName}

	op, err := operations.OperationCreate(d.State(), projectName, operations.OperationClassTask,
		db.OperationCustomVolumeBackupRename, resources, nil, rename, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func storagePoolVolumeTypeCustomBackupDelete(d *Daemon, r *http.Request) response.Response {
	// Get the name of the storage volume.
	volumeName := mux.Vars(r)["name"]
	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName := mux.Vars(r)["pool"]
	// Get the volume type.
	volumeTypeName := mux.Vars(r)["type"]
	// Get backup name.
	backupName := mux.Vars(r)["backupName"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if volumeType != db.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	projectName, err := project.StorageVolumeProject(d.State().Cluster, projectParam(r), db.StoragePoolVolumeTypeCustom)
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(d, r, poolName, projectName, volumeName, db.StoragePoolVolumeTypeCustom)
	if resp != nil {
		return resp
	}

	fullName := volumeName + shared.SnapshotDelimiter + backupName

	backup, err := storagePoolVolumeBackupLoadByName(d.State(), projectName, poolName, fullName)
	if err != nil {
		return response.SmartError(err)
	}

	remove := func(op *operations.Operation) error {
		err := backup.Delete()
		if err != nil {
			return err
		}

		return nil
	}

	resources := map[string][]string{}
	resources["volume"] = []string{volumeName}

	op, err := operations.OperationCreate(d.State(), projectName, operations.OperationClassTask,
		db.OperationCustomVolumeBackupRemove, resources, nil, remove, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func storagePoolVolumeTypeCustomBackupExportGet(d *Daemon, r *http.Request) response.Response {
	// Get the name of the storage volume.
	volumeName := mux.Vars(r)["name"]
	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName := mux.Vars(r)["pool"]
	// Get the volume type.
	volumeTypeName := mux.Vars(r)["type"]
	// Get backup name.
	backupName := mux.Vars(r)["backupName"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if volumeType != db.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	projectName, err := project.StorageVolumeProject(d.State().Cluster, projectParam(r), db.StoragePoolVolumeTypeCustom)
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(d, r, poolName, projectName, volumeName, db.StoragePoolVolumeTypeCustom)
	if resp != nil {
		return resp
	}

	fullName := volumeName + shared.SnapshotDelimiter + backupName

	// Ensure the volume exists
	_, err = storagePoolVolumeBackupLoadByName(d.State(), projectName, poolName, fullName)
	if err != nil {
		return response.SmartError(err)
	}

	ent := response.FileResponseEntry{
		Path: shared.VarPath("backups", "custom", poolName, project.StorageVolume(projectName, fullName)),
	}

	return response.FileResponse(r, []response.FileResponseEntry{ent}, nil, false)
}
