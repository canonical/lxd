package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/backup"
	"github.com/canonical/lxd/lxd/backup/config"
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
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/validate"
	"github.com/canonical/lxd/shared/version"
)

var storagePoolVolumeTypeCustomBackupsCmd = APIEndpoint{
	Path:        "storage-pools/{poolName}/volumes/{type}/{volumeName}/backups",
	MetricsType: entity.TypeStoragePool,

	Get:  APIEndpointAction{Handler: storagePoolVolumeTypeCustomBackupsGet, AccessHandler: allowProjectResourceList(false)},
	Post: APIEndpointAction{Handler: storagePoolVolumeTypeCustomBackupsPost, AccessHandler: storagePoolVolumeTypeAccessHandler(entity.TypeStorageVolume, auth.EntitlementCanManageBackups)},
}

var storagePoolVolumeTypeCustomBackupCmd = APIEndpoint{
	Path:        "storage-pools/{poolName}/volumes/{type}/{volumeName}/backups/{backupName}",
	MetricsType: entity.TypeStoragePool,

	Get:    APIEndpointAction{Handler: storagePoolVolumeTypeCustomBackupGet, AccessHandler: storagePoolVolumeTypeAccessHandler(entity.TypeStorageVolumeBackup, auth.EntitlementCanView)},
	Post:   APIEndpointAction{Handler: storagePoolVolumeTypeCustomBackupPost, AccessHandler: storagePoolVolumeTypeAccessHandler(entity.TypeStorageVolumeBackup, auth.EntitlementCanEdit)},
	Delete: APIEndpointAction{Handler: storagePoolVolumeTypeCustomBackupDelete, AccessHandler: storagePoolVolumeTypeAccessHandler(entity.TypeStorageVolumeBackup, auth.EntitlementCanDelete)},
}

var storagePoolVolumeTypeCustomBackupExportCmd = APIEndpoint{
	Path:        "storage-pools/{poolName}/volumes/{type}/{volumeName}/backups/{backupName}/export",
	MetricsType: entity.TypeStoragePool,

	Get: APIEndpointAction{Handler: storagePoolVolumeTypeCustomBackupExportGet, AccessHandler: storagePoolVolumeTypeAccessHandler(entity.TypeStorageVolumeBackup, auth.EntitlementCanView)},
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

	err := addStoragePoolVolumeDetailsToRequestContext(s, r)
	if err != nil {
		return response.SmartError(err)
	}

	effectiveProjectName, err := request.GetContextValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	details, err := request.GetContextValue[storageVolumeDetails](r.Context(), ctxStorageVolumeDetails)
	if err != nil {
		return response.SmartError(err)
	}

	// Check that the storage volume type is valid.
	if details.volumeType != cluster.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", details.volumeTypeName))
	}

	target := request.QueryParam(r, "target")
	resp := forwardedResponseToNode(r.Context(), s, target)
	if resp != nil {
		return resp
	}

	// Handle requests targeted to a volume on a different node
	resp = forwardedResponseIfVolumeIsRemote(r.Context(), s)
	if resp != nil {
		return resp
	}

	recursion, _ := util.IsRecursionRequest(r)

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

	canView, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeStorageVolumeBackup)
	if err != nil {
		return response.SmartError(err)
	}

	for _, backup := range backups {
		_, backupName, ok := strings.Cut(backup.Name(), "/")
		if !ok {
			// Not adding the name to the error response here because we were unable to check if the caller is allowed to view it.
			return response.InternalError(errors.New("Storage volume backup has invalid name"))
		}

		if !canView(entity.StorageVolumeBackupURL(request.ProjectParam(r), details.location, details.pool.Name(), details.volumeTypeName, details.volumeName, backupName)) {
			continue
		}

		if recursion == 0 {
			url := api.NewURL().Path(version.APIVersion, "storage-pools", details.pool.Name(), "volumes", "custom", details.volumeName, "backups", backupName).String()
			resultString = append(resultString, url)
		} else {
			render := backup.Render()
			resultMap = append(resultMap, render)
		}
	}

	if recursion == 0 {
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

	details, err := request.GetContextValue[storageVolumeDetails](r.Context(), ctxStorageVolumeDetails)
	if err != nil {
		return response.SmartError(err)
	}

	// Check that the storage volume type is valid.
	if details.volumeType != cluster.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", details.volumeTypeName))
	}

	requestProjectName := request.ProjectParam(r)
	effectiveProjectName, err := request.GetContextValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	target := request.QueryParam(r, "target")
	resp := forwardedResponseToNode(r.Context(), s, target)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(r.Context(), s)
	if resp != nil {
		return resp
	}

	var dbVolume *db.StorageVolume
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		err := limits.AllowBackupCreation(tx, effectiveProjectName)
		if err != nil {
			return err
		}

		dbVolume, err = tx.GetStoragePoolVolume(ctx, details.pool.ID(), effectiveProjectName, details.volumeType, details.volumeName, true)
		if err != nil {
			return err
		}

		return nil
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

	if req.CompressionAlgorithm != "" {
		err = validate.IsCompressionAlgorithm(req.CompressionAlgorithm)
		if err != nil {
			return response.BadRequest(err)
		}
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
		backupNo := 0

		// Iterate over previous backups to autoincrement the backup number.
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

			if num >= backupNo {
				backupNo = num + 1
			}
		}

		req.Name = fmt.Sprintf("backup%d", backupNo)
	}

	// In case no version was selected for the backup format use the globally set format by default.
	// This allows staying backwards compatible with older CLIs which don't yet support
	// sending this field.
	if req.Version == 0 {
		req.Version = config.DefaultMetadataVersion
	} else if req.Version > config.MaxMetadataVersion {
		return response.BadRequest(fmt.Errorf("Invalid backup format version %d", req.Version))
	}

	// Validate the name.
	backupName, err := backup.ValidateBackupName(req.Name)
	if err != nil {
		return response.BadRequest(err)
	}

	fullName := details.volumeName + shared.SnapshotDelimiter + backupName
	volumeOnly := req.VolumeOnly

	backup := func(ctx context.Context, op *operations.Operation) error {
		args := db.StoragePoolVolumeBackup{
			Name:                 fullName,
			VolumeID:             dbVolume.ID,
			CreationDate:         time.Now(),
			ExpiryDate:           req.ExpiresAt,
			VolumeOnly:           volumeOnly,
			OptimizedStorage:     req.OptimizedStorage,
			CompressionAlgorithm: req.CompressionAlgorithm,
		}

		err := volumeBackupCreate(s, args, effectiveProjectName, details.pool.Name(), details.volumeName, req.Version)
		if err != nil {
			return fmt.Errorf("Create volume backup: %w", err)
		}

		s.Events.SendLifecycle(effectiveProjectName, lifecycle.StorageVolumeBackupCreated.Event(details.pool.Name(), details.volumeTypeName, args.Name, effectiveProjectName, op.EventLifecycleRequestor(), logger.Ctx{"type": details.volumeTypeName}))

		return nil
	}

	volumeURL := api.NewURL().Path(version.APIVersion, "storage-pools", details.pool.Name(), "volumes", details.volumeTypeName, details.volumeName).Project(requestProjectName)
	args := operations.OperationArgs{
		ProjectName: requestProjectName,
		EntityURL:   volumeURL,
		Type:        operationtype.CustomVolumeBackupCreate,
		Class:       operations.OperationClassTask,
		RunHook:     backup,
		Resources: map[entity.Type][]api.URL{
			entity.TypeStorageVolume: {*volumeURL},
		},
		Metadata: map[string]any{
			operations.EntityURL: api.NewURL().Path(version.APIVersion, "storage-pools", details.pool.Name(), "volumes", details.volumeTypeName, details.volumeName, "backups", backupName).Project(requestProjectName).String(),
		},
	}

	op, err := operations.ScheduleUserOperationFromRequest(s, r, args)
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

	details, err := request.GetContextValue[storageVolumeDetails](r.Context(), ctxStorageVolumeDetails)
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

	effectiveProjectName, err := request.GetContextValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	target := request.QueryParam(r, "target")
	resp := forwardedResponseToNode(r.Context(), s, target)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(r.Context(), s)
	if resp != nil {
		return resp
	}

	fullName := details.volumeName + shared.SnapshotDelimiter + backupName

	backup, err := storagePoolVolumeBackupLoadByName(r.Context(), s, effectiveProjectName, details.pool.Name(), fullName)
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

	details, err := request.GetContextValue[storageVolumeDetails](r.Context(), ctxStorageVolumeDetails)
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
	effectiveProjectName, err := request.GetContextValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	target := request.QueryParam(r, "target")
	resp := forwardedResponseToNode(r.Context(), s, target)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(r.Context(), s)
	if resp != nil {
		return resp
	}

	req := api.StoragePoolVolumeBackupPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Validate the name.
	newBackupName, err := backup.ValidateBackupName(req.Name)
	if err != nil {
		return response.BadRequest(err)
	}

	oldName := details.volumeName + shared.SnapshotDelimiter + backupName

	backup, err := storagePoolVolumeBackupLoadByName(r.Context(), s, effectiveProjectName, details.pool.Name(), oldName)
	if err != nil {
		return response.SmartError(err)
	}

	newName := details.volumeName + shared.SnapshotDelimiter + newBackupName

	rename := func(ctx context.Context, op *operations.Operation) error {
		err := backup.Rename(newName)
		if err != nil {
			return err
		}

		s.Events.SendLifecycle(effectiveProjectName, lifecycle.StorageVolumeBackupRenamed.Event(details.pool.Name(), details.volumeTypeName, newName, effectiveProjectName, op.EventLifecycleRequestor(), logger.Ctx{"old_name": oldName}))

		return nil
	}

	backupURL := api.NewURL().Path(version.APIVersion, "storage-pools", details.pool.Name(), "volumes", details.volumeTypeName, details.volumeName, "backups", backupName)
	args := operations.OperationArgs{
		ProjectName: requestProjectName,
		EntityURL:   backupURL,
		Type:        operationtype.CustomVolumeBackupRename,
		Class:       operations.OperationClassTask,
		RunHook:     rename,
		Resources: map[entity.Type][]api.URL{
			entity.TypeStorageVolumeBackup: {*backupURL},
		},
	}

	op, err := operations.ScheduleUserOperationFromRequest(s, r, args)
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

	details, err := request.GetContextValue[storageVolumeDetails](r.Context(), ctxStorageVolumeDetails)
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
	effectiveProjectName, err := request.GetContextValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	target := request.QueryParam(r, "target")
	resp := forwardedResponseToNode(r.Context(), s, target)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(r.Context(), s)
	if resp != nil {
		return resp
	}

	fullName := details.volumeName + shared.SnapshotDelimiter + backupName

	backup, err := storagePoolVolumeBackupLoadByName(r.Context(), s, effectiveProjectName, details.pool.Name(), fullName)
	if err != nil {
		return response.SmartError(err)
	}

	remove := func(ctx context.Context, op *operations.Operation) error {
		err := backup.Delete()
		if err != nil {
			return err
		}

		s.Events.SendLifecycle(effectiveProjectName, lifecycle.StorageVolumeBackupDeleted.Event(details.pool.Name(), details.volumeTypeName, fullName, effectiveProjectName, op.EventLifecycleRequestor(), nil))

		return nil
	}

	backupURL := api.NewURL().Path(version.APIVersion, "storage-pools", details.pool.Name(), "volumes", details.volumeTypeName, details.volumeName, "backups", backupName).Project(effectiveProjectName)
	args := operations.OperationArgs{
		ProjectName: requestProjectName,
		EntityURL:   backupURL,
		Type:        operationtype.CustomVolumeBackupRemove,
		Class:       operations.OperationClassTask,
		RunHook:     remove,
		Resources: map[entity.Type][]api.URL{
			entity.TypeStorageVolumeBackup: {*backupURL},
		},
	}

	op, err := operations.ScheduleUserOperationFromRequest(s, r, args)
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

	details, err := request.GetContextValue[storageVolumeDetails](r.Context(), ctxStorageVolumeDetails)
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

	effectiveProjectName, err := request.GetContextValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	target := request.QueryParam(r, "target")
	resp := forwardedResponseToNode(r.Context(), s, target)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(r.Context(), s)
	if resp != nil {
		return resp
	}

	fullName := details.volumeName + shared.SnapshotDelimiter + backupName

	// Ensure the volume exists
	_, err = storagePoolVolumeBackupLoadByName(r.Context(), s, effectiveProjectName, details.pool.Name(), fullName)
	if err != nil {
		return response.SmartError(err)
	}

	ent := response.FileResponseEntry{
		Path: filepath.Join(s.BackupsStoragePath(effectiveProjectName), "custom", details.pool.Name(), project.StorageVolume(effectiveProjectName, fullName)),
	}

	s.Events.SendLifecycle(effectiveProjectName, lifecycle.StorageVolumeBackupRetrieved.Event(details.pool.Name(), details.volumeTypeName, fullName, effectiveProjectName, request.CreateRequestor(r.Context()), nil))

	return response.FileResponse([]response.FileResponseEntry{ent}, nil)
}
