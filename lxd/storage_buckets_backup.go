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
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/project/limits"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	storagePools "github.com/canonical/lxd/lxd/storage"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/version"
)

var storagePoolBucketBackupsCmd = APIEndpoint{
	Path: "storage-pools/{poolName}/buckets/{bucketName}/backups",

	Get:  APIEndpointAction{Handler: storagePoolBucketBackupsGet, AccessHandler: allowPermission(entity.TypeStorageBucket, auth.EntitlementCanView, "poolName", "bucketName", "location")},
	Post: APIEndpointAction{Handler: storagePoolBucketBackupsPost, AccessHandler: allowPermission(entity.TypeStorageBucket, auth.EntitlementCanManageBackups, "poolName", "bucketName", "location")},
}

var storagePoolBucketBackupCmd = APIEndpoint{
	Path: "storage-pools/{poolName}/buckets/{bucketName}/backups/{backupName}",

	Get:    APIEndpointAction{Handler: storagePoolBucketBackupGet, AccessHandler: allowPermission(entity.TypeStorageBucket, auth.EntitlementCanView, "poolName", "bucketName", "location")},
	Post:   APIEndpointAction{Handler: storagePoolBucketBackupPost, AccessHandler: allowPermission(entity.TypeStorageBucket, auth.EntitlementCanManageBackups, "poolName", "bucketName", "location")},
	Delete: APIEndpointAction{Handler: storagePoolBucketBackupDelete, AccessHandler: allowPermission(entity.TypeStorageBucket, auth.EntitlementCanManageBackups, "poolName", "bucketName", "location")},
}

var storagePoolBucketBackupsExportCmd = APIEndpoint{
	Path: "storage-pools/{poolName}/buckets/{bucketName}/backups/{backupName}/export",

	Get: APIEndpointAction{Handler: storagePoolBucketBackupExportGet, AccessHandler: allowPermission(entity.TypeStorageBucket, auth.EntitlementCanView, "poolName", "bucketName", "location")},
}

// swagger:operation GET /1.0/storage-pools/{poolName}/buckets/{bucketName}/backups storage storage_pool_buckets_backups_get
//
//  Get the storage bucket backups
//
//  Returns a list of storage bucket backups (URLs).
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
//      example: server01
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
//                "/1.0/storage-pools/local/buckets/foo/backups/backup0",
//                "/1.0/storage-pools/local/buckets/foo/backups/backup1"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/storage-pools/{poolName}/buckets/{bucketName}/backups?recursion=1 storage storage_pool_buckets_backups_get_recursion1
//
//	Get the storage bucket backups
//
//	Returns a list of storage bucket backups (structs).
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
//	    example: server01
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
//	          description: List of storage bucket backups
//	          items:
//	            $ref: "#/definitions/StoragePoolBucketBackup"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolBucketBackupsGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	bucketProjectName, err := project.StorageBucketProject(r.Context(), s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading storage pool: %w", err))
	}

	if !pool.Driver().Info().Buckets {
		return response.BadRequest(fmt.Errorf("Storage pool does not support buckets"))
	}

	bucketName, err := url.PathUnescape(mux.Vars(r)["bucketName"])
	if err != nil {
		return response.SmartError(err)
	}

	targetMember := request.QueryParam(r, "target")
	memberSpecific := targetMember != ""

	var bucket *db.StorageBucket
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		bucket, err = tx.GetStoragePoolBucket(ctx, pool.ID(), bucketProjectName, memberSpecific, bucketName)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	u := pool.GetBucketURL(bucket.Name)
	if u != nil {
		bucket.S3URL = u.String()
	}

	return response.SyncResponseETag(true, bucket, bucket.Etag())
}

// swagger:operation POST /1.0/storage-pools/{poolName}/buckets/{bucketName}/backups storage storage_pool_buckets_backups_post
//
//	Create a storage bucket backup
//
//	Creates a new storage bucket backup.
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
//	    example: server01
//	  - in: body
//	    name: bucket
//	    description: Storage bucket backup
//	    required: true
//	    schema:
//	      $ref: "#/definitions/StoragePoolBucketBackupsPost"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolBucketBackupsPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	projectName, err := project.StorageBucketProject(r.Context(), s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading storage pool: %w", err))
	}

	if !pool.Driver().Info().Buckets {
		return response.BadRequest(fmt.Errorf("Storage pool does not support buckets"))
	}

	bucketName, err := url.PathUnescape(mux.Vars(r)["bucketName"])
	if err != nil {
		return response.SmartError(err)
	}

	targetMember := request.QueryParam(r, "target")
	memberSpecific := targetMember != ""

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		err := limits.AllowBackupCreation(tx, projectName)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	var bucket *db.StorageBucket
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		bucket, err = tx.GetStoragePoolBucket(ctx, pool.ID(), projectName, memberSpecific, bucketName)
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

	body, err := json.Marshal(rj)
	if err != nil {
		return response.InternalError(err)
	}

	req := api.StorageBucketBackupsPost{}

	err = json.Unmarshal(body, &req)
	if err != nil {
		return response.BadRequest(err)
	}

	if req.Name == "" {
		var backups []string

		// come up with a name.
		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			backups, err = tx.GetStoragePoolBucketBackupsName(ctx, projectName, bucket.Name)
			return err
		})
		if err != nil {
			return response.BadRequest(err)
		}

		base := bucket.Name + shared.SnapshotDelimiter + "backup"
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
	backupName, err := backup.ValidateBackupName(req.Name)
	if err != nil {
		return response.BadRequest(err)
	}

	fullName := bucket.Name + shared.SnapshotDelimiter + backupName

	backup := func(op *operations.Operation) error {
		args := db.StoragePoolBucketBackup{
			Name:         fullName,
			BucketID:     bucket.ID,
			BucketName:   bucket.Name,
			CreationDate: time.Now(),
			ExpiryDate:   req.ExpiresAt,
		}

		err := bucketBackupCreate(s, args, projectName, poolName, bucket.Name)
		if err != nil {
			return fmt.Errorf("Create bucket backup: %w", err)
		}

		s.Events.SendLifecycle(projectName, lifecycle.StorageBucketBackupCreated.Event(poolName, args.Name, projectName, op.Requestor(), nil))

		return nil
	}

	resources := map[string][]api.URL{}
	resources["storage_buckets"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", poolName, "buckets", bucket.Name)}
	resources["backups"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", poolName, "buckets", bucket.Name, "backups", backupName)}

	op, err := operations.OperationCreate(s, request.ProjectParam(r), operations.OperationClassTask, operationtype.BucketBackupCreate, resources, nil, backup, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// swagger:operation GET /1.0/storage-pools/{poolName}/buckets/{bucketName}/backups/{backupName} storage storage_pool_buckets_backup_get
//
//	Get the storage bucket backup
//
//	Gets a specific storage bucket backup.
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
//	    example: server01
//	responses:
//	  "200":
//	    description: Storage bucket backup
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
//	          $ref: "#/definitions/StoragePoolBucketBackup"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolBucketBackupGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	projectName, err := project.StorageBucketProject(r.Context(), s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading storage pool: %w", err))
	}

	if !pool.Driver().Info().Buckets {
		return response.BadRequest(fmt.Errorf("Storage pool does not support buckets"))
	}

	bucketName, err := url.PathUnescape(mux.Vars(r)["bucketName"])
	if err != nil {
		return response.SmartError(err)
	}

	backupName, err := url.PathUnescape(mux.Vars(r)["backupName"])
	if err != nil {
		return response.SmartError(err)
	}

	fullName := bucketName + shared.SnapshotDelimiter + backupName

	backup, err := storagePoolBucketBackupLoadByName(s, projectName, poolName, fullName)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, backup.Render())
}

// swagger:operation POST /1.0/storage-pools/{poolName}/buckets/{bucketName}/backups/{backupName} storage storage_pool_buckets_backup_post
//
//	Rename a storage bucket backup
//
//	Renames a storage bucket backup.
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
//	    example: server01
//	  - in: body
//	    name: bucket rename
//	    description: Storage bucket backup
//	    required: true
//	    schema:
//	      $ref: "#/definitions/StorageBucketBackupPost"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolBucketBackupPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	projectName, err := project.StorageBucketProject(r.Context(), s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading storage pool: %w", err))
	}

	if !pool.Driver().Info().Buckets {
		return response.BadRequest(fmt.Errorf("Storage pool does not support buckets"))
	}

	bucketName, err := url.PathUnescape(mux.Vars(r)["bucketName"])
	if err != nil {
		return response.SmartError(err)
	}

	backupName, err := url.PathUnescape(mux.Vars(r)["backupName"])
	if err != nil {
		return response.SmartError(err)
	}

	req := api.StorageBucketBackupPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Validate the name.
	newBackupName, err := backup.ValidateBackupName(req.Name)
	if err != nil {
		return response.BadRequest(err)
	}

	oldName := bucketName + shared.SnapshotDelimiter + backupName

	backup, err := storagePoolBucketBackupLoadByName(s, projectName, pool.Name(), oldName)
	if err != nil {
		return response.SmartError(err)
	}

	newName := backup.BucketName() + shared.SnapshotDelimiter + newBackupName

	rename := func(op *operations.Operation) error {
		err := backup.Rename(newName)
		if err != nil {
			return err
		}

		s.Events.SendLifecycle(projectName, lifecycle.StorageBucketBackupRenamed.Event(pool.Name(), newName, projectName, op.Requestor(), nil))

		return nil
	}

	resources := map[string][]api.URL{}
	resources["storage_buckets"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", pool.Name(), "buckets", bucketName)}
	resources["backups"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", pool.Name(), "buckets", bucketName, "backups", backupName)}

	op, err := operations.OperationCreate(s, request.ProjectParam(r), operations.OperationClassTask, operationtype.BucketBackupRemove, resources, nil, rename, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// swagger:operation DELETE /1.0/storage-pools/{poolName}/buckets/{bucketName}/backups/{backupName} storage storage_pool_buckets_backup_delete
//
//	Delete a storage bucket backup
//
//	Deletes a new storage bucket backup.
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
//	    example: server01
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolBucketBackupDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	projectName, err := project.StorageBucketProject(r.Context(), s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading storage pool: %w", err))
	}

	if !pool.Driver().Info().Buckets {
		return response.BadRequest(fmt.Errorf("Storage pool does not support buckets"))
	}

	bucketName, err := url.PathUnescape(mux.Vars(r)["bucketName"])
	if err != nil {
		return response.SmartError(err)
	}

	backupName, err := url.PathUnescape(mux.Vars(r)["backupName"])
	if err != nil {
		return response.SmartError(err)
	}

	fullName := bucketName + shared.SnapshotDelimiter + backupName

	backup, err := storagePoolBucketBackupLoadByName(s, projectName, pool.Name(), fullName)
	if err != nil {
		return response.SmartError(err)
	}

	remove := func(op *operations.Operation) error {
		err := backup.Delete()
		if err != nil {
			return err
		}

		s.Events.SendLifecycle(projectName, lifecycle.StorageBucketBackupDeleted.Event(pool.Name(), fullName, projectName, op.Requestor(), nil))

		return nil
	}

	resources := map[string][]api.URL{}
	resources["storage_buckets"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", pool.Name(), "buckets", bucketName)}
	resources["backups"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", pool.Name(), "buckets", bucketName, "backups", backupName)}

	op, err := operations.OperationCreate(s, request.ProjectParam(r), operations.OperationClassTask, operationtype.BucketBackupRemove, resources, nil, remove, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// swagger:operation GET /1.0/storage-pools/{poolName}/buckets/{bucketName}/backups/{backupName}/export storage storage_pool_buckets_backup_export_get
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
//	    example: server01
//	responses:
//	  "200":
//	    description: Raw backup data
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolBucketBackupExportGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	projectName, err := project.StorageBucketProject(r.Context(), s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading storage pool: %w", err))
	}

	if !pool.Driver().Info().Buckets {
		return response.BadRequest(fmt.Errorf("Storage pool does not support buckets"))
	}

	bucketName, err := url.PathUnescape(mux.Vars(r)["bucketName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get backup name.
	backupName, err := url.PathUnescape(mux.Vars(r)["backupName"])
	if err != nil {
		return response.SmartError(err)
	}

	targetMember := request.QueryParam(r, "target")
	memberSpecific := targetMember != ""

	fullName := bucketName + shared.SnapshotDelimiter + backupName

	// Ensure the bucket exists
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		_, err = tx.GetStoragePoolBucket(ctx, pool.ID(), projectName, memberSpecific, bucketName)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	ent := response.FileResponseEntry{
		Path: shared.VarPath("backups", "buckets", poolName, project.StorageBucket(projectName, fullName)),
	}

	s.Events.SendLifecycle(projectName, lifecycle.StorageBucketBackupRetrieved.Event(poolName, fullName, projectName, request.CreateRequestor(r), nil))

	return response.FileResponse([]response.FileResponseEntry{ent}, nil)
}

func storagePoolBucketBackupLoadByName(s *state.State, projectName, poolName, backupName string) (*backup.BucketBackup, error) {
	var b db.StoragePoolBucketBackup

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		b, err = tx.GetStoragePoolBucketBackup(ctx, projectName, poolName, backupName)
		return err
	})
	if err != nil {
		return nil, err
	}

	backup := backup.NewBucketBackup(s, projectName, poolName, b.BucketName, b.ID, b.Name, b.CreationDate, b.ExpiryDate)

	return backup, nil
}
