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

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/backup"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/project/limits"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/version"
)

var storagePoolVolumeTypeCustomBackupsCmd = APIEndpoint{
	Path: "storage-pools/{poolName}/volumes/{type}/{volumeName}/backups",

	Get:  APIEndpointAction{Handler: storagePoolVolumeTypeCustomBackupsGet, AccessHandler: storagePoolVolumeTypeAccessHandler(auth.EntitlementCanView)},
	Post: APIEndpointAction{Handler: storagePoolVolumeTypeCustomBackupsPost, AccessHandler: storagePoolVolumeTypeAccessHandler(auth.EntitlementCanManageBackups)},
}

var storagePoolVolumeTypeCustomBackupCmd = APIEndpoint{
	Path: "storage-pools/{poolName}/volumes/{type}/{volumeName}/backups/{backupName}",

	Get:    APIEndpointAction{Handler: storagePoolVolumeTypeCustomBackupGet, AccessHandler: storagePoolVolumeTypeAccessHandler(auth.EntitlementCanView)},
	Post:   APIEndpointAction{Handler: storagePoolVolumeTypeCustomBackupPost, AccessHandler: storagePoolVolumeTypeAccessHandler(auth.EntitlementCanManageBackups)},
	Delete: APIEndpointAction{Handler: storagePoolVolumeTypeCustomBackupDelete, AccessHandler: storagePoolVolumeTypeAccessHandler(auth.EntitlementCanManageBackups)},
}

var storagePoolVolumeTypeCustomBackupExportCmd = APIEndpoint{
	Path: "storage-pools/{poolName}/volumes/{type}/{volumeName}/backups/{backupName}/export",

	Get: APIEndpointAction{Handler: storagePoolVolumeTypeCustomBackupExportGet, AccessHandler: storagePoolVolumeTypeAccessHandler(auth.EntitlementCanView)},
}

// swagger:operation GET /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/backups storage storage_pool_volumes_type_backups_get
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

// swagger:operation GET /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/backups?recursion=1 storage storage_pool_volumes_type_backups_get_recursion1
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
	s := d.State()

	effectiveProjectName, err := request.GetCtxValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	details, err := request.GetCtxValue[storageVolumeDetails](r.Context(), ctxStorageVolumeDetails)
	if err != nil {
		return response.SmartError(err)
	}

	// Check that the storage volume type is valid.
	if details.volumeType != cluster.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", details.volumeTypeName))
	}

	// Handle requests targeted to a volume on a different node
	resp := forwardedResponseIfVolumeIsRemote(s, r)
	if resp != nil {
		return resp
	}

	recursion := util.IsRecursionRequest(r)

	var volumeBackups []db.StoragePoolVolumeBackup

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		volumeBackups, err = tx.GetStoragePoolVolumeBackups(ctx, effectiveProjectName, details.volumeName, details.pool.ID())
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	backups := make([]*backup.VolumeBackup, len(volumeBackups))

	for i, b := range volumeBackups {
		backups[i] = backup.NewVolumeBackup(s, effectiveProjectName, details.pool.Name(), details.volumeName, b.ID, b.Name, b.CreationDate, b.ExpiryDate, b.VolumeOnly, b.OptimizedStorage)
	}

	resultString := []string{}
	resultMap := []*api.StoragePoolVolumeBackup{}

	for _, backup := range backups {
		if !recursion {
			url := api.NewURL().Path(version.APIVersion, "storage-pools", details.pool.Name(), "volumes", "custom", details.volumeName, "backups", strings.Split(backup.Name(), "/")[1]).String()
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

// swagger:operation POST /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/backups storage storage_pool_volumes_type_backups_post
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

	details, err := request.GetCtxValue[storageVolumeDetails](r.Context(), ctxStorageVolumeDetails)
	if err != nil {
		return response.SmartError(err)
	}

	// Check that the storage volume type is valid.
	if details.volumeType != cluster.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", details.volumeTypeName))
	}

	requestProjectName := request.ProjectParam(r)
	effectiveProjectName, err := request.GetCtxValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		err := limits.AllowBackupCreation(tx, effectiveProjectName)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(s, r)
	if resp != nil {
		return resp
	}

	var dbVolume *db.StorageVolume
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbVolume, err = tx.GetStoragePoolVolume(ctx, details.pool.ID(), effectiveProjectName, details.volumeType, details.volumeName, true)
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
		var backups []string

		// come up with a name.
		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			backups, err = tx.GetStoragePoolVolumeBackupsNames(ctx, effectiveProjectName, details.volumeName, details.pool.ID())
			return err
		})
		if err != nil {
			return response.BadRequest(err)
		}

		base := details.volumeName + shared.SnapshotDelimiter + "backup"
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

	fullName := details.volumeName + shared.SnapshotDelimiter + req.Name
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

		err := volumeBackupCreate(s, args, effectiveProjectName, details.pool.Name(), details.volumeName)
		if err != nil {
			return fmt.Errorf("Create volume backup: %w", err)
		}

		s.Events.SendLifecycle(effectiveProjectName, lifecycle.StorageVolumeBackupCreated.Event(details.pool.Name(), details.volumeTypeName, args.Name, effectiveProjectName, op.Requestor(), logger.Ctx{"type": details.volumeTypeName}))

		return nil
	}

	resources := map[string][]api.URL{}
	resources["storage_volumes"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", details.pool.Name(), "volumes", details.volumeTypeName, details.volumeName)}
	resources["backups"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", details.pool.Name(), "volumes", details.volumeTypeName, details.volumeName, "backups", req.Name)}

	op, err := operations.OperationCreate(s, requestProjectName, operations.OperationClassTask, operationtype.CustomVolumeBackupCreate, resources, nil, backup, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// swagger:operation GET /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/backups/{backupName} storage storage_pool_volumes_type_backup_get
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

	details, err := request.GetCtxValue[storageVolumeDetails](r.Context(), ctxStorageVolumeDetails)
	if err != nil {
		return response.SmartError(err)
	}

	// Get backup name.
	backupName, err := url.PathUnescape(mux.Vars(r)["backupName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Check that the storage volume type is valid.
	if details.volumeType != cluster.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", details.volumeTypeName))
	}

	effectiveProjectName, err := request.GetCtxValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(s, r)
	if resp != nil {
		return resp
	}

	fullName := details.volumeName + shared.SnapshotDelimiter + backupName

	backup, err := storagePoolVolumeBackupLoadByName(s, effectiveProjectName, details.pool.Name(), fullName)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, backup.Render())
}

// swagger:operation POST /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/backups/{backupName} storage storage_pool_volumes_type_backup_post
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

	details, err := request.GetCtxValue[storageVolumeDetails](r.Context(), ctxStorageVolumeDetails)
	if err != nil {
		return response.SmartError(err)
	}

	// Get backup name.
	backupName, err := url.PathUnescape(mux.Vars(r)["backupName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Check that the storage volume type is valid.
	if details.volumeType != cluster.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", details.volumeTypeName))
	}

	requestProjectName := request.ProjectParam(r)
	effectiveProjectName, err := request.GetCtxValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(s, r)
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

	oldName := details.volumeName + shared.SnapshotDelimiter + backupName

	backup, err := storagePoolVolumeBackupLoadByName(s, effectiveProjectName, details.pool.Name(), oldName)
	if err != nil {
		return response.SmartError(err)
	}

	newName := details.volumeName + shared.SnapshotDelimiter + req.Name

	rename := func(op *operations.Operation) error {
		err := backup.Rename(newName)
		if err != nil {
			return err
		}

		s.Events.SendLifecycle(effectiveProjectName, lifecycle.StorageVolumeBackupRenamed.Event(details.pool.Name(), details.volumeTypeName, newName, effectiveProjectName, op.Requestor(), logger.Ctx{"old_name": oldName}))

		return nil
	}

	resources := map[string][]api.URL{}
	resources["storage_volumes"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", details.pool.Name(), "volumes", details.volumeTypeName, details.volumeName)}
	resources["backups"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", details.pool.Name(), "volumes", details.volumeTypeName, details.volumeName, "backups", oldName)}

	op, err := operations.OperationCreate(s, requestProjectName, operations.OperationClassTask, operationtype.CustomVolumeBackupRename, resources, nil, rename, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// swagger:operation DELETE /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/backups/{backupName} storage storage_pool_volumes_type_backup_delete
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

	details, err := request.GetCtxValue[storageVolumeDetails](r.Context(), ctxStorageVolumeDetails)
	if err != nil {
		return response.SmartError(err)
	}

	// Get backup name.
	backupName, err := url.PathUnescape(mux.Vars(r)["backupName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Check that the storage volume type is valid.
	if details.volumeType != cluster.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", details.volumeTypeName))
	}

	requestProjectName := request.ProjectParam(r)
	effectiveProjectName, err := request.GetCtxValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(s, r)
	if resp != nil {
		return resp
	}

	fullName := details.volumeName + shared.SnapshotDelimiter + backupName

	backup, err := storagePoolVolumeBackupLoadByName(s, effectiveProjectName, details.pool.Name(), fullName)
	if err != nil {
		return response.SmartError(err)
	}

	remove := func(op *operations.Operation) error {
		err := backup.Delete()
		if err != nil {
			return err
		}

		s.Events.SendLifecycle(effectiveProjectName, lifecycle.StorageVolumeBackupDeleted.Event(details.pool.Name(), details.volumeTypeName, fullName, effectiveProjectName, op.Requestor(), nil))

		return nil
	}

	resources := map[string][]api.URL{}
	resources["storage_volumes"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", details.pool.Name(), "volumes", details.volumeTypeName, details.volumeName)}
	resources["backups"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", details.pool.Name(), "volumes", details.volumeTypeName, details.volumeName, "backups", backupName)}

	op, err := operations.OperationCreate(s, requestProjectName, operations.OperationClassTask, operationtype.CustomVolumeBackupRemove, resources, nil, remove, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// swagger:operation GET /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/backups/{backupName}/export storage storage_pool_volumes_type_backup_export_get
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

	details, err := request.GetCtxValue[storageVolumeDetails](r.Context(), ctxStorageVolumeDetails)
	if err != nil {
		return response.SmartError(err)
	}

	// Get backup name.
	backupName, err := url.PathUnescape(mux.Vars(r)["backupName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Check that the storage volume type is valid.
	if details.volumeType != cluster.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", details.volumeTypeName))
	}

	effectiveProjectName, err := request.GetCtxValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(s, r)
	if resp != nil {
		return resp
	}

	fullName := details.volumeName + shared.SnapshotDelimiter + backupName

	// Ensure the volume exists
	_, err = storagePoolVolumeBackupLoadByName(s, effectiveProjectName, details.pool.Name(), fullName)
	if err != nil {
		return response.SmartError(err)
	}

	ent := response.FileResponseEntry{
		Path: shared.VarPath("backups", "custom", details.pool.Name(), project.StorageVolume(effectiveProjectName, fullName)),
	}

	s.Events.SendLifecycle(effectiveProjectName, lifecycle.StorageVolumeBackupRetrieved.Event(details.pool.Name(), details.volumeTypeName, fullName, effectiveProjectName, request.CreateRequestor(r), nil))

	return response.FileResponse([]response.FileResponseEntry{ent}, nil)
}
