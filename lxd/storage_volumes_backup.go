package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/backup"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/db/operationtype"
	"github.com/lxc/lxd/lxd/lifecycle"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/request"
	"github.com/lxc/lxd/lxd/response"
	storagePools "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"
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

// swagger:operation GET /1.0/storage-pools/{name}/volumes/{type}/{volume}/backups storage storage_pool_volumes_type_backups_get
//
//  Get the storage volume backups
//
//  Returns a list of storage volume backups (URLs).
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
//    - in: query
//      name: target
//      description: Cluster member name
//      type: string
//      example: lxd01
//  responses:
//    "200":
//      description: API endpoints
//      schema:
//        type: object
//        description: Sync response
//        properties:
//          type:
//            type: string
//            description: Response type
//            example: sync
//          status:
//            type: string
//            description: Status description
//            example: Success
//          status_code:
//            type: integer
//            description: Status code
//            example: 200
//          metadata:
//            type: array
//            description: List of endpoints
//            items:
//              type: string
//            example: |-
//              [
//                "/1.0/storage-pools/local/volumes/custom/foo/backups/backup0",
//                "/1.0/storage-pools/local/volumes/custom/foo/backups/backup1"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/storage-pools/{name}/volumes/{type}/{volume}/backups?recursion=1 storage storage_pool_volumes_type_backups_get_recursion1
//
//	Get the storage volume backups
//
//	Returns a list of storage volume backups (structs).
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: query
//	    name: target
//	    description: Cluster member name
//	    type: string
//	    example: lxd01
//	responses:
//	  "200":
//	    description: API endpoints
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          type: array
//	          description: List of storage volume backups
//	          items:
//	            $ref: "#/definitions/StoragePoolVolumeBackup"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumeTypeCustomBackupsGet(d *Daemon, r *http.Request) response.Response {
	projectName, err := project.StorageVolumeProject(d.State().DB.Cluster, projectParam(r), db.StoragePoolVolumeTypeCustom)
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the storage volume.
	volumeName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName, err := url.PathUnescape(mux.Vars(r)["pool"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the volume type.
	volumeTypeName, err := url.PathUnescape(mux.Vars(r)["type"])
	if err != nil {
		return response.SmartError(err)
	}

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if volumeType != db.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	poolID, _, _, err := d.db.Cluster.GetStoragePool(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Handle requests targeted to a volume on a different node
	resp := forwardedResponseIfVolumeIsRemote(d.State(), r, poolName, projectName, volumeName, db.StoragePoolVolumeTypeCustom)
	if resp != nil {
		return resp
	}

	recursion := util.IsRecursionRequest(r)

	volumeBackups, err := d.State().DB.Cluster.GetStoragePoolVolumeBackups(projectName, volumeName, poolID)
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

// swagger:operation POST /1.0/storage-pools/{name}/volumes/{type}/{volume}/backups storage storage_pool_volumes_type_backups_post
//
//	Create a storage volume backup
//
//	Creates a new storage volume backup.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: query
//	    name: target
//	    description: Cluster member name
//	    type: string
//	    example: lxd01
//	  - in: body
//	    name: volume
//	    description: Storage volume backup
//	    required: true
//	    schema:
//	      $ref: "#/definitions/StoragePoolVolumeBackupsPost"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumeTypeCustomBackupsPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get the name of the storage volume.
	volumeName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName, err := url.PathUnescape(mux.Vars(r)["pool"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the volume type.
	volumeTypeName, err := url.PathUnescape(mux.Vars(r)["type"])
	if err != nil {
		return response.SmartError(err)
	}

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if volumeType != db.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	projectName, err := project.StorageVolumeProject(s.DB.Cluster, projectParam(r), db.StoragePoolVolumeTypeCustom)
	if err != nil {
		return response.SmartError(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		err := project.AllowBackupCreation(tx, projectName)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	poolID, _, _, err := d.db.Cluster.GetStoragePool(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	resp = forwardedResponseIfVolumeIsRemote(s, r, poolName, projectName, volumeName, db.StoragePoolVolumeTypeCustom)
	if resp != nil {
		return resp
	}

	var dbVolume *db.StorageVolume
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbVolume, err = tx.GetStoragePoolVolume(ctx, poolID, projectName, volumeType, volumeName, true)
		return err
	})
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
		backups, err := s.DB.Cluster.GetStoragePoolVolumeBackupsNames(projectName, volumeName, poolID)
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
			VolumeID:             dbVolume.ID,
			CreationDate:         time.Now(),
			ExpiryDate:           req.ExpiresAt,
			VolumeOnly:           volumeOnly,
			OptimizedStorage:     req.OptimizedStorage,
			CompressionAlgorithm: req.CompressionAlgorithm,
		}

		err := volumeBackupCreate(s, args, projectName, poolName, volumeName)
		if err != nil {
			return fmt.Errorf("Create volume backup: %w", err)
		}

		s.Events.SendLifecycle(projectName, lifecycle.StorageVolumeBackupCreated.Event(poolName, volumeTypeName, args.Name, projectName, op.Requestor(), logger.Ctx{"type": volumeTypeName}))

		return nil
	}

	resources := map[string][]string{}
	resources["storage_volumes"] = []string{volumeName}
	resources["backups"] = []string{req.Name}

	op, err := operations.OperationCreate(s, projectParam(r), operations.OperationClassTask, operationtype.CustomVolumeBackupCreate, resources, nil, backup, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// swagger:operation GET /1.0/storage-pools/{name}/volumes/{type}/{volume}/backups/{backup} storage storage_pool_volumes_type_backup_get
//
//	Get the storage volume backup
//
//	Gets a specific storage volume backup.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: query
//	    name: target
//	    description: Cluster member name
//	    type: string
//	    example: lxd01
//	responses:
//	  "200":
//	    description: Storage volume backup
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          $ref: "#/definitions/StoragePoolVolumeBackup"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumeTypeCustomBackupGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get the name of the storage volume.
	volumeName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName, err := url.PathUnescape(mux.Vars(r)["pool"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the volume type.
	volumeTypeName, err := url.PathUnescape(mux.Vars(r)["type"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get backup name.
	backupName, err := url.PathUnescape(mux.Vars(r)["backupName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if volumeType != db.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	projectName, err := project.StorageVolumeProject(s.DB.Cluster, projectParam(r), db.StoragePoolVolumeTypeCustom)
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(s, r, poolName, projectName, volumeName, db.StoragePoolVolumeTypeCustom)
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

// swagger:operation POST /1.0/storage-pools/{name}/volumes/{type}/{volume}/backups/{backup} storage storage_pool_volumes_type_backup_post
//
//	Rename a storage volume backup
//
//	Renames a storage volume backup.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: query
//	    name: target
//	    description: Cluster member name
//	    type: string
//	    example: lxd01
//	  - in: body
//	    name: volume rename
//	    description: Storage volume backup
//	    required: true
//	    schema:
//	      $ref: "#/definitions/StorageVolumeSnapshotPost"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumeTypeCustomBackupPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get the name of the storage volume.
	volumeName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName, err := url.PathUnescape(mux.Vars(r)["pool"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the volume type.
	volumeTypeName, err := url.PathUnescape(mux.Vars(r)["type"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get backup name.
	backupName, err := url.PathUnescape(mux.Vars(r)["backupName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if volumeType != db.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	projectName, err := project.StorageVolumeProject(s.DB.Cluster, projectParam(r), db.StoragePoolVolumeTypeCustom)
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(s, r, poolName, projectName, volumeName, db.StoragePoolVolumeTypeCustom)
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

	backup, err := storagePoolVolumeBackupLoadByName(s, projectName, poolName, oldName)
	if err != nil {
		return response.SmartError(err)
	}

	newName := volumeName + shared.SnapshotDelimiter + req.Name

	rename := func(op *operations.Operation) error {
		err := backup.Rename(newName)
		if err != nil {
			return err
		}

		s.Events.SendLifecycle(projectName, lifecycle.StorageVolumeBackupRenamed.Event(poolName, volumeTypeName, newName, projectName, op.Requestor(), logger.Ctx{"old_name": oldName}))

		return nil
	}

	resources := map[string][]string{}
	resources["volume"] = []string{volumeName}

	op, err := operations.OperationCreate(d.State(), projectParam(r), operations.OperationClassTask, operationtype.CustomVolumeBackupRename, resources, nil, rename, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// swagger:operation DELETE /1.0/storage-pools/{name}/volumes/{type}/{volume}/backups/{backup} storage storage_pool_volumes_type_backup_delete
//
//	Delete a storage volume backup
//
//	Deletes a new storage volume backup.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: query
//	    name: target
//	    description: Cluster member name
//	    type: string
//	    example: lxd01
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumeTypeCustomBackupDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get the name of the storage volume.
	volumeName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName, err := url.PathUnescape(mux.Vars(r)["pool"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the volume type.
	volumeTypeName, err := url.PathUnescape(mux.Vars(r)["type"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get backup name.
	backupName, err := url.PathUnescape(mux.Vars(r)["backupName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if volumeType != db.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	projectName, err := project.StorageVolumeProject(s.DB.Cluster, projectParam(r), db.StoragePoolVolumeTypeCustom)
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(s, r, poolName, projectName, volumeName, db.StoragePoolVolumeTypeCustom)
	if resp != nil {
		return resp
	}

	fullName := volumeName + shared.SnapshotDelimiter + backupName

	backup, err := storagePoolVolumeBackupLoadByName(s, projectName, poolName, fullName)
	if err != nil {
		return response.SmartError(err)
	}

	remove := func(op *operations.Operation) error {
		err := backup.Delete()
		if err != nil {
			return err
		}

		s.Events.SendLifecycle(projectName, lifecycle.StorageVolumeBackupDeleted.Event(poolName, volumeTypeName, fullName, projectName, op.Requestor(), nil))

		return nil
	}

	resources := map[string][]string{}
	resources["volume"] = []string{volumeName}

	op, err := operations.OperationCreate(s, projectParam(r), operations.OperationClassTask, operationtype.CustomVolumeBackupRemove, resources, nil, remove, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// swagger:operation GET /1.0/storage-pools/{name}/volumes/{type}/{volume}/backups/{backup}/export storage storage_pool_volumes_type_backup_export_get
//
//	Get the raw backup file
//
//	Download the raw backup file from the server.
//
//	---
//	produces:
//	  - application/octet-stream
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: query
//	    name: target
//	    description: Cluster member name
//	    type: string
//	    example: lxd01
//	responses:
//	  "200":
//	    description: Raw backup data
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumeTypeCustomBackupExportGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get the name of the storage volume.
	volumeName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName, err := url.PathUnescape(mux.Vars(r)["pool"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the volume type.
	volumeTypeName, err := url.PathUnescape(mux.Vars(r)["type"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get backup name.
	backupName, err := url.PathUnescape(mux.Vars(r)["backupName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if volumeType != db.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	projectName, err := project.StorageVolumeProject(s.DB.Cluster, projectParam(r), db.StoragePoolVolumeTypeCustom)
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(s, r, poolName, projectName, volumeName, db.StoragePoolVolumeTypeCustom)
	if resp != nil {
		return resp
	}

	fullName := volumeName + shared.SnapshotDelimiter + backupName

	// Ensure the volume exists
	_, err = storagePoolVolumeBackupLoadByName(s, projectName, poolName, fullName)
	if err != nil {
		return response.SmartError(err)
	}

	ent := response.FileResponseEntry{
		Path: shared.VarPath("backups", "custom", poolName, project.StorageVolume(projectName, fullName)),
	}

	s.Events.SendLifecycle(projectName, lifecycle.StorageVolumeBackupRetrieved.Event(poolName, volumeTypeName, fullName, projectName, request.CreateRequestor(r), nil))

	return response.FileResponse(r, []response.FileResponseEntry{ent}, nil)
}
