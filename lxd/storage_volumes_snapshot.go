package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/project/limits"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	storagePools "github.com/canonical/lxd/lxd/storage"
	"github.com/canonical/lxd/lxd/task"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/version"
)

var storagePoolVolumeSnapshotsTypeCmd = APIEndpoint{
	Path:        "storage-pools/{poolName}/volumes/{type}/{volumeName}/snapshots",
	MetricsType: entity.TypeStoragePool,

	Get:  APIEndpointAction{Handler: storagePoolVolumeSnapshotsTypeGet, AccessHandler: allowProjectResourceList},
	Post: APIEndpointAction{Handler: storagePoolVolumeSnapshotsTypePost, AccessHandler: storagePoolVolumeTypeAccessHandler(entity.TypeStorageVolume, auth.EntitlementCanManageSnapshots)},
}

var storagePoolVolumeSnapshotTypeCmd = APIEndpoint{
	Path:        "storage-pools/{poolName}/volumes/{type}/{volumeName}/snapshots/{snapshotName}",
	MetricsType: entity.TypeStoragePool,

	Delete: APIEndpointAction{Handler: storagePoolVolumeSnapshotTypeDelete, AccessHandler: storagePoolVolumeTypeAccessHandler(entity.TypeStorageVolumeSnapshot, auth.EntitlementCanDelete)},
	Get:    APIEndpointAction{Handler: storagePoolVolumeSnapshotTypeGet, AccessHandler: storagePoolVolumeTypeAccessHandler(entity.TypeStorageVolumeSnapshot, auth.EntitlementCanView)},
	Post:   APIEndpointAction{Handler: storagePoolVolumeSnapshotTypePost, AccessHandler: storagePoolVolumeTypeAccessHandler(entity.TypeStorageVolumeSnapshot, auth.EntitlementCanEdit)},
	Patch:  APIEndpointAction{Handler: storagePoolVolumeSnapshotTypePatch, AccessHandler: storagePoolVolumeTypeAccessHandler(entity.TypeStorageVolumeSnapshot, auth.EntitlementCanEdit)},
	Put:    APIEndpointAction{Handler: storagePoolVolumeSnapshotTypePut, AccessHandler: storagePoolVolumeTypeAccessHandler(entity.TypeStorageVolumeSnapshot, auth.EntitlementCanEdit)},
}

// swagger:operation POST /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/snapshots storage storage_pool_volumes_type_snapshots_post
//
//	Create a storage volume snapshot
//
//	Creates a new storage volume snapshot.
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
//	    description: Storage volume snapshot
//	    required: true
//	    schema:
//	      $ref: "#/definitions/StorageVolumeSnapshotsPost"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumeSnapshotsTypePost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	details, err := request.GetCtxValue[storageVolumeDetails](r.Context(), ctxStorageVolumeDetails)
	if err != nil {
		return response.SmartError(err)
	}

	// Check that the storage volume type is valid.
	if details.volumeType != dbCluster.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", details.volumeTypeName))
	}

	requestProjectName := request.ProjectParam(r)
	effectiveProjectName, err := request.GetCtxValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbProject, err := dbCluster.GetProject(context.Background(), tx.Tx(), effectiveProjectName)
		if err != nil {
			return err
		}

		p, err := dbProject.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		err = limits.AllowSnapshotCreation(p)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Forward if needed.
	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(s, r)
	if resp != nil {
		return resp
	}

	// Parse the request.
	req := api.StorageVolumeSnapshotsPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that this isn't a restricted volume
	used, err := storagePools.VolumeUsedByDaemon(s, details.pool.Name(), details.volumeName)
	if err != nil {
		return response.InternalError(err)
	}

	if used {
		return response.BadRequest(fmt.Errorf("Volumes used by LXD itself cannot have snapshots"))
	}

	var parentDBVolume *db.StorageVolume
	var parentVolumeArgs db.StorageVolumeArgs
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get the parent volume so we can get the config.
		parentDBVolume, err = tx.GetStoragePoolVolume(ctx, details.pool.ID(), effectiveProjectName, details.volumeType, details.volumeName, true)
		if err != nil {
			return err
		}

		// We will need the parent volume config to determine the snapshot name.
		if req.Name == "" {
			parentVolumeArgs, err = tx.GetStoragePoolVolumeWithID(ctx, int(parentDBVolume.ID))
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if req.Name == "" {
		snapName, err := storagePools.VolumeDetermineNextSnapshotName(r.Context(), s, parentVolumeArgs.PoolName, parentVolumeArgs.Name, parentVolumeArgs.Config)
		if err != nil {
			return response.SmartError(err)
		}

		req.Name = snapName
	}

	// Validate the snapshot name using same rule as pool name.
	err = details.pool.ValidateName(req.Name)
	if err != nil {
		return response.BadRequest(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Ensure that the snapshot doesn't already exist.
		snapDBVolume, err := tx.GetStoragePoolVolume(ctx, details.pool.ID(), effectiveProjectName, details.volumeType, fmt.Sprintf("%s/%s", details.volumeName, req.Name), true)
		if err != nil && !response.IsNotFoundError(err) {
			return err
		} else if snapDBVolume != nil {
			return api.StatusErrorf(http.StatusConflict, "Snapshot %q already in use", req.Name)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Fill in the expiry.
	var expiry time.Time
	if req.ExpiresAt != nil {
		expiry = *req.ExpiresAt
	} else {
		expiry, err = shared.GetExpiry(time.Now(), parentDBVolume.Config["snapshots.expiry"])
		if err != nil {
			return response.BadRequest(err)
		}
	}

	// Create the snapshot.
	snapshot := func(op *operations.Operation) error {
		return details.pool.CreateCustomVolumeSnapshot(effectiveProjectName, details.volumeName, req.Name, req.Description, expiry, op)
	}

	resources := map[string][]api.URL{}
	resources["storage_volumes"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", details.pool.Name(), "volumes", details.volumeTypeName, details.volumeName)}
	resources["storage_volume_snapshots"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", details.pool.Name(), "volumes", details.volumeTypeName, details.volumeName, "snapshots", req.Name)}

	op, err := operations.OperationCreate(s, requestProjectName, operations.OperationClassTask, operationtype.VolumeSnapshotCreate, resources, nil, snapshot, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// swagger:operation GET /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/snapshots storage storage_pool_volumes_type_snapshots_get
//
//  Get the storage volume snapshots
//
//  Returns a list of storage volume snapshots (URLs).
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
//                "/1.0/storage-pools/local/volumes/custom/foo/snapshots/snap0",
//                "/1.0/storage-pools/local/volumes/custom/foo/snapshots/snap1"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/snapshots?recursion=1 storage storage_pool_volumes_type_snapshots_get_recursion1
//
//	Get the storage volume snapshots
//
//	Returns a list of storage volume snapshots (structs).
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
//	          description: List of storage volume snapshots
//	          items:
//	            $ref: "#/definitions/StorageVolumeSnapshot"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumeSnapshotsTypeGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	err := addStoragePoolVolumeDetailsToRequestContext(s, r)
	if err != nil {
		return response.SmartError(err)
	}

	details, err := request.GetCtxValue[storageVolumeDetails](r.Context(), ctxStorageVolumeDetails)
	if err != nil {
		return response.SmartError(err)
	}

	recursion := util.IsRecursionRequest(r)

	// Check that the storage volume type is valid.
	if !shared.ValueInSlice(details.volumeType, supportedVolumeTypes) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", details.volumeTypeName))
	}

	effectiveProjectName, err := request.GetCtxValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	var volumes []db.StorageVolumeArgs

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		// Get the names of all storage volume snapshots of a given volume.
		volumes, err = tx.GetLocalStoragePoolVolumeSnapshotsWithType(ctx, effectiveProjectName, details.volumeName, details.volumeType, details.pool.ID())
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	canView, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeStorageVolumeSnapshot)
	if err != nil {
		return response.SmartError(err)
	}

	// Prepare the response.
	resultString := []string{}
	resultMap := []*api.StorageVolumeSnapshot{}
	for _, volume := range volumes {
		_, snapshotName, _ := api.GetParentAndSnapshotName(volume.Name)

		if !canView(entity.StorageVolumeSnapshotURL(request.ProjectParam(r), details.location, details.pool.Name(), details.volumeTypeName, details.volumeName, snapshotName)) {
			continue
		}

		if !recursion {
			resultString = append(resultString, fmt.Sprintf("/%s/storage-pools/%s/volumes/%s/%s/snapshots/%s", version.APIVersion, details.pool.Name(), details.volumeTypeName, details.volumeName, snapshotName))
		} else {
			var vol *db.StorageVolume
			err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
				vol, err = tx.GetStoragePoolVolume(ctx, details.pool.ID(), effectiveProjectName, details.volumeType, volume.Name, true)
				return err
			})
			if err != nil {
				return response.SmartError(err)
			}

			volumeUsedBy, err := storagePoolVolumeUsedByGet(s, effectiveProjectName, vol)
			if err != nil {
				return response.SmartError(err)
			}

			vol.UsedBy = project.FilterUsedBy(s.Authorizer, r, volumeUsedBy)

			snap := &api.StorageVolumeSnapshot{}
			snap.Config = vol.Config
			snap.Description = vol.Description
			snap.Name = vol.Name
			snap.CreatedAt = vol.CreatedAt

			expiryDate := volume.ExpiryDate
			if expiryDate.Unix() > 0 {
				snap.ExpiresAt = &expiryDate
			}

			resultMap = append(resultMap, snap)
		}
	}

	if !recursion {
		return response.SyncResponse(true, resultString)
	}

	return response.SyncResponse(true, resultMap)
}

// swagger:operation POST /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/snapshots/{snapshotName} storage storage_pool_volumes_type_snapshot_post
//
//	Rename a storage volume snapshot
//
//	Renames a storage volume snapshot.
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
//	    description: Storage volume snapshot
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
func storagePoolVolumeSnapshotTypePost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	details, err := request.GetCtxValue[storageVolumeDetails](r.Context(), ctxStorageVolumeDetails)
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the storage volume.
	snapshotName, err := url.PathUnescape(mux.Vars(r)["snapshotName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Check that the storage volume type is valid.
	if details.volumeType != dbCluster.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", details.volumeTypeName))
	}

	requestProjectName := request.ProjectParam(r)
	effectiveProjectName, err := request.GetCtxValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	// Forward if needed.
	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(s, r)
	if resp != nil {
		return resp
	}

	fullSnapshotName := fmt.Sprintf("%s/%s", details.volumeName, snapshotName)

	// Parse the request.
	req := api.StorageVolumeSnapshotPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check new volume name is valid.
	err = storagePools.ValidVolumeName(req.Name)
	if err != nil {
		return response.BadRequest(err)
	}

	// This is a migration request so send back requested secrets.
	if req.Migration {
		req := api.StorageVolumePost{
			Name:   req.Name,
			Target: req.Target,
		}

		return storagePoolVolumeTypePostMigration(s, r, requestProjectName, effectiveProjectName, details.pool.Name(), fullSnapshotName, req)
	}

	// Rename the snapshot.
	snapshotRename := func(op *operations.Operation) error {
		return details.pool.RenameCustomVolumeSnapshot(effectiveProjectName, fullSnapshotName, req.Name, op)
	}

	resources := map[string][]api.URL{}
	resources["storage_volume_snapshots"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", details.pool.Name(), "volumes", details.volumeTypeName, details.volumeName, "snapshots", snapshotName)}

	op, err := operations.OperationCreate(s, requestProjectName, operations.OperationClassTask, operationtype.VolumeSnapshotRename, resources, nil, snapshotRename, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// swagger:operation GET /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/snapshots/{snapshotName} storage storage_pool_volumes_type_snapshot_get
//
//	Get the storage volume snapshot
//
//	Gets a specific storage volume snapshot.
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
//	    description: Storage volume snapshot
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
//	          $ref: "#/definitions/StorageVolumeSnapshot"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumeSnapshotTypeGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	details, err := request.GetCtxValue[storageVolumeDetails](r.Context(), ctxStorageVolumeDetails)
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the storage volume.
	snapshotName, err := url.PathUnescape(mux.Vars(r)["snapshotName"])
	if err != nil {
		return response.SmartError(err)
	}

	effectiveProjectName, err := request.GetCtxValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	// Forward if needed.
	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(s, r)
	if resp != nil {
		return resp
	}

	fullSnapshotName := fmt.Sprintf("%s/%s", details.volumeName, snapshotName)

	var dbVolume *db.StorageVolume
	var expiry time.Time

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbVolume, err = tx.GetStoragePoolVolume(ctx, details.pool.ID(), effectiveProjectName, details.volumeType, fullSnapshotName, true)
		if err != nil {
			return err
		}

		expiry, err = tx.GetStorageVolumeSnapshotExpiry(ctx, dbVolume.ID)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	snapshot := &api.StorageVolumeSnapshot{}
	snapshot.Config = dbVolume.Config
	snapshot.Description = dbVolume.Description
	snapshot.Name = snapshotName
	snapshot.ExpiresAt = &expiry
	snapshot.ContentType = dbVolume.ContentType
	snapshot.CreatedAt = dbVolume.CreatedAt

	etag := []any{snapshot.Description, expiry}
	return response.SyncResponseETag(true, snapshot, etag)
}

// swagger:operation PUT /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/snapshots/{snapshotName} storage storage_pool_volumes_type_snapshot_put
//
//	Update the storage volume snapshot
//
//	Updates the entire storage volume snapshot configuration.
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
//	    name: storage volume snapshot
//	    description: Storage volume snapshot configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/StorageVolumeSnapshotPut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumeSnapshotTypePut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	details, err := request.GetCtxValue[storageVolumeDetails](r.Context(), ctxStorageVolumeDetails)
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the storage volume.
	snapshotName, err := url.PathUnescape(mux.Vars(r)["snapshotName"])
	if err != nil {
		return response.SmartError(err)
	}

	effectiveProjectName, err := request.GetCtxValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	// Forward if needed.
	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(s, r)
	if resp != nil {
		return resp
	}

	fullSnapshotName := fmt.Sprintf("%s/%s", details.volumeName, snapshotName)

	var dbVolume *db.StorageVolume
	var expiry time.Time

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbVolume, err = tx.GetStoragePoolVolume(ctx, details.pool.ID(), effectiveProjectName, details.volumeType, fullSnapshotName, true)
		if err != nil {
			return err
		}

		expiry, err = tx.GetStorageVolumeSnapshotExpiry(ctx, dbVolume.ID)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag
	etag := []any{dbVolume.Description, expiry}
	err = util.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	req := api.StorageVolumeSnapshotPut{}

	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	return doStoragePoolVolumeSnapshotUpdate(s, r, effectiveProjectName, dbVolume.Name, details.volumeType, req)
}

// swagger:operation PATCH /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/snapshots/{snapshotName} storage storage_pool_volumes_type_snapshot_patch
//
//	Partially update the storage volume snapshot
//
//	Updates a subset of the storage volume snapshot configuration.
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
//	    name: storage volume snapshot
//	    description: Storage volume snapshot configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/StorageVolumeSnapshotPut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumeSnapshotTypePatch(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	details, err := request.GetCtxValue[storageVolumeDetails](r.Context(), ctxStorageVolumeDetails)
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the storage volume.
	snapshotName, err := url.PathUnescape(mux.Vars(r)["snapshotName"])
	if err != nil {
		return response.SmartError(err)
	}

	effectiveProjectName, err := request.GetCtxValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	// Forward if needed.
	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(s, r)
	if resp != nil {
		return resp
	}

	fullSnapshotName := fmt.Sprintf("%s/%s", details.volumeName, snapshotName)

	var dbVolume *db.StorageVolume
	var expiry time.Time

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbVolume, err = tx.GetStoragePoolVolume(ctx, details.pool.ID(), effectiveProjectName, details.volumeType, fullSnapshotName, true)
		if err != nil {
			return err
		}

		expiry, err = tx.GetStorageVolumeSnapshotExpiry(ctx, dbVolume.ID)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag
	etag := []any{dbVolume.Description, expiry}
	err = util.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	req := api.StorageVolumeSnapshotPut{
		Description: dbVolume.Description,
		ExpiresAt:   &expiry,
	}

	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	return doStoragePoolVolumeSnapshotUpdate(s, r, effectiveProjectName, dbVolume.Name, details.volumeType, req)
}

func doStoragePoolVolumeSnapshotUpdate(s *state.State, r *http.Request, projectName string, volName string, volumeType dbCluster.StoragePoolVolumeType, req api.StorageVolumeSnapshotPut) response.Response {
	expiry := time.Time{}
	if req.ExpiresAt != nil {
		expiry = *req.ExpiresAt
	}

	details, err := request.GetCtxValue[storageVolumeDetails](r.Context(), ctxStorageVolumeDetails)
	if err != nil {
		return response.SmartError(err)
	}

	// Use an empty operation for this sync response to pass the requestor
	op := &operations.Operation{}
	op.SetRequestor(r)

	// Update the database.
	if volumeType == dbCluster.StoragePoolVolumeTypeCustom {
		err = details.pool.UpdateCustomVolumeSnapshot(projectName, volName, req.Description, nil, expiry, op)
		if err != nil {
			return response.SmartError(err)
		}
	} else {
		inst, err := instance.LoadByProjectAndName(s, projectName, volName)
		if err != nil {
			return response.SmartError(err)
		}

		err = details.pool.UpdateInstanceSnapshot(inst, req.Description, nil, op)
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.EmptySyncResponse
}

// swagger:operation DELETE /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/snapshots/{snapshotName} storage storage_pool_volumes_type_snapshot_delete
//
//	Delete a storage volume snapshot
//
//	Deletes a new storage volume snapshot.
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
func storagePoolVolumeSnapshotTypeDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	details, err := request.GetCtxValue[storageVolumeDetails](r.Context(), ctxStorageVolumeDetails)
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the storage volume.
	snapshotName, err := url.PathUnescape(mux.Vars(r)["snapshotName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Check that the storage volume type is valid.
	if details.volumeType != dbCluster.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", details.volumeTypeName))
	}

	requestProjectName := request.ProjectParam(r)
	effectiveProjectName, err := request.GetCtxValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	// Forward if needed.
	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(s, r)
	if resp != nil {
		return resp
	}

	fullSnapshotName := fmt.Sprintf("%s/%s", details.volumeName, snapshotName)

	snapshotDelete := func(op *operations.Operation) error {
		return details.pool.DeleteCustomVolumeSnapshot(effectiveProjectName, fullSnapshotName, op)
	}

	resources := map[string][]api.URL{}
	resources["storage_volume_snapshots"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", details.pool.Name(), "volumes", details.volumeTypeName, details.volumeName, "snapshots", snapshotName)}

	op, err := operations.OperationCreate(s, requestProjectName, operations.OperationClassTask, operationtype.VolumeSnapshotDelete, resources, nil, snapshotDelete, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func pruneExpiredAndAutoCreateCustomVolumeSnapshotsTask(d *Daemon) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		s := d.State()
		var volumes, remoteVolumes, expiredSnapshots, expiredRemoteSnapshots []db.StorageVolumeArgs
		var memberCount int
		var onlineMemberIDs []int64

		err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			// Get the list of expired custom volume snapshots for this member (or remote).
			allExpiredSnapshots, err := tx.GetExpiredStorageVolumeSnapshots(ctx, true)
			if err != nil {
				return fmt.Errorf("Failed getting expired custom volume snapshots: %w", err)
			}

			for _, v := range allExpiredSnapshots {
				if v.NodeID < 0 {
					// Keep a separate list of remote volumes in order to select a member to
					// perform the snapshot expiry on later.
					expiredRemoteSnapshots = append(expiredRemoteSnapshots, v)
				} else {
					logger.Debug("Scheduling local custom volume snapshot expiry", logger.Ctx{"volName": v.Name, "project": v.ProjectName, "pool": v.PoolName})
					expiredSnapshots = append(expiredSnapshots, v) // Always include local volumes.
				}
			}

			projs, err := dbCluster.GetProjects(ctx, tx.Tx())
			if err != nil {
				return fmt.Errorf("Failed loading projects: %w", err)
			}

			// Key by project name for lookup later.
			projects := make(map[string]*api.Project, len(projs))
			for _, p := range projs {
				projects[p.Name], err = p.ToAPI(ctx, tx.Tx())
				if err != nil {
					return fmt.Errorf("Failed loading project %q: %w", p.Name, err)
				}
			}

			allVolumes, err := tx.GetStoragePoolVolumesWithType(ctx, dbCluster.StoragePoolVolumeTypeCustom, true)
			if err != nil {
				return fmt.Errorf("Failed getting volumes for auto custom volume snapshot task: %w", err)
			}

			for _, v := range allVolumes {
				err = limits.AllowSnapshotCreation(projects[v.ProjectName])
				if err != nil {
					continue
				}

				schedule, ok := v.Config["snapshots.schedule"]
				if !ok || schedule == "" {
					continue
				}

				// Check if snapshot is scheduled.
				if !snapshotIsScheduledNow(schedule, v.ID) {
					continue
				}

				if v.NodeID < 0 {
					// Keep a separate list of remote volumes in order to select a member to
					// perform the snapshot later.
					remoteVolumes = append(remoteVolumes, v)
				} else {
					logger.Debug("Scheduling local auto custom volume snapshot", logger.Ctx{"volName": v.Name, "project": v.ProjectName, "pool": v.PoolName})
					volumes = append(volumes, v) // Always include local volumes.
				}
			}

			if len(remoteVolumes) > 0 || len(expiredRemoteSnapshots) > 0 {
				// Get list of cluster members.
				members, err := tx.GetNodes(ctx)
				if err != nil {
					return fmt.Errorf("Failed getting cluster members: %w", err)
				}

				memberCount = len(members)

				// Filter to online members.
				for _, member := range members {
					if member.IsOffline(s.GlobalConfig.OfflineThreshold()) {
						continue
					}

					onlineMemberIDs = append(onlineMemberIDs, member.ID)
				}

				return nil
			}

			return nil
		})
		if err != nil {
			logger.Error("Failed getting custom volume info", logger.Ctx{"err": err})
			return
		}

		localMemberID := s.DB.Cluster.GetNodeID()

		if len(expiredRemoteSnapshots) > 0 {
			// Skip expiring remote custom volume snapshots if there are no online members, as we can't
			// be sure that the cluster isn't partitioned and we may end up attempting to expire
			// snapshot on multiple members.
			if memberCount > 1 && len(onlineMemberIDs) <= 0 {
				logger.Error("Skipping remote volumes for expire custom volume snapshot task due to no online members")
			} else {
				for _, v := range expiredRemoteSnapshots {
					// If there are multiple cluster members, a stable random member is chosen
					// to perform the snapshot expiry. This avoids expiring the snapshot on
					// every member and spreads the load across the online cluster members.
					if memberCount > 1 {
						selectedMemberID, err := util.GetStableRandomInt64FromList(int64(v.ID), onlineMemberIDs)
						if err != nil {
							logger.Error("Failed scheduling remote expire custom volume snapshot task", logger.Ctx{"volName": v.Name, "project": v.ProjectName, "pool": v.PoolName, "err": err})
							continue
						}

						// Don't snapshot, if we're not the chosen one.
						if localMemberID != selectedMemberID {
							continue
						}
					}

					logger.Debug("Scheduling remote custom volume snapshot expiry", logger.Ctx{"volName": v.Name, "project": v.ProjectName, "pool": v.PoolName})
					expiredSnapshots = append(expiredSnapshots, v)
				}
			}
		}

		if len(remoteVolumes) > 0 {
			// Skip snapshotting remote custom volumes if there are no online members, as we can't be
			// sure that the cluster isn't partitioned and we may end up attempting the snapshot on
			// multiple members.
			if memberCount > 1 && len(onlineMemberIDs) <= 0 {
				logger.Error("Skipping remote volumes for auto custom volume snapshot task due to no online members")
			} else {
				for _, v := range remoteVolumes {
					// If there are multiple cluster members, a stable random member is chosen
					// to perform the snapshot from. This avoids taking the snapshot on every
					// member and spreads the load taking the snapshots across the online
					// cluster members.
					if memberCount > 1 {
						selectedNodeID, err := util.GetStableRandomInt64FromList(int64(v.ID), onlineMemberIDs)
						if err != nil {
							logger.Error("Failed scheduling remote auto custom volume snapshot task", logger.Ctx{"volName": v.Name, "project": v.ProjectName, "pool": v.PoolName, "err": err})
							continue
						}

						// Don't snapshot, if we're not the chosen one.
						if localMemberID != selectedNodeID {
							continue
						}
					}

					logger.Debug("Scheduling remote auto custom volume snapshot", logger.Ctx{"volName": v.Name, "project": v.ProjectName, "pool": v.PoolName})
					volumes = append(volumes, v)
				}
			}
		}

		// Handle snapshot expiry first before creating new ones to reduce the chances of running out of
		// disk space.
		if len(expiredSnapshots) > 0 {
			opRun := func(op *operations.Operation) error {
				return pruneExpiredCustomVolumeSnapshots(ctx, s, expiredSnapshots)
			}

			op, err := operations.OperationCreate(s, "", operations.OperationClassTask, operationtype.CustomVolumeSnapshotsExpire, nil, nil, opRun, nil, nil, nil)
			if err != nil {
				logger.Error("Failed creating expired custom volume snapshots prune operation", logger.Ctx{"err": err})
			} else {
				logger.Info("Pruning expired custom volume snapshots")
				err = op.Start()
				if err != nil {
					logger.Error("Failed starting expired custom volume snapshots prune operation", logger.Ctx{"err": err})
				} else {
					err = op.Wait(ctx)
					if err != nil {
						logger.Error("Failed pruning expired custom volume snapshots", logger.Ctx{"err": err})
					} else {
						logger.Info("Done pruning expired custom volume snapshots")
					}
				}
			}
		}

		// Handle snapshot auto creation.
		if len(volumes) > 0 {
			opRun := func(op *operations.Operation) error {
				return autoCreateCustomVolumeSnapshots(ctx, s, volumes)
			}

			op, err := operations.OperationCreate(s, "", operations.OperationClassTask, operationtype.VolumeSnapshotCreate, nil, nil, opRun, nil, nil, nil)
			if err != nil {
				logger.Error("Failed creating scheduled volume snapshot operation", logger.Ctx{"err": err})
			} else {
				logger.Info("Creating scheduled volume snapshots")
				err = op.Start()
				if err != nil {
					logger.Error("Failed starting scheduled volume snapshot operation", logger.Ctx{"err": err})
				} else {
					err = op.Wait(ctx)
					if err != nil {
						logger.Error("Failed scheduled custom volume snapshots", logger.Ctx{"err": err})
					} else {
						logger.Info("Done creating scheduled volume snapshots")
					}
				}
			}
		}
	}

	first := true
	schedule := func() (time.Duration, error) {
		interval := time.Minute

		if first {
			first = false
			return interval, task.ErrSkip
		}

		return interval, nil
	}

	return f, schedule
}

var customVolSnapshotsPruneRunning = sync.Map{}

func pruneExpiredCustomVolumeSnapshots(ctx context.Context, s *state.State, expiredSnapshots []db.StorageVolumeArgs) error {
	for _, v := range expiredSnapshots {
		err := ctx.Err()
		if err != nil {
			return err // Stop if context is cancelled.
		}

		_, loaded := customVolSnapshotsPruneRunning.LoadOrStore(v.ID, struct{}{})
		if loaded {
			continue // Deletion of this snapshot is already running, skip.
		}

		pool, err := storagePools.LoadByName(s, v.PoolName)
		if err != nil {
			customVolSnapshotsPruneRunning.Delete(v.ID)
			return fmt.Errorf("Error loading pool for volume snapshot %q (project %q, pool %q): %w", v.Name, v.ProjectName, v.PoolName, err)
		}

		err = pool.DeleteCustomVolumeSnapshot(v.ProjectName, v.Name, nil)
		customVolSnapshotsPruneRunning.Delete(v.ID)
		if err != nil {
			return fmt.Errorf("Error deleting custom volume snapshot %q (project %q, pool %q): %w", v.Name, v.ProjectName, v.PoolName, err)
		}
	}

	return nil
}

func autoCreateCustomVolumeSnapshots(ctx context.Context, s *state.State, volumes []db.StorageVolumeArgs) error {
	// Make the snapshots sequentially.
	for _, v := range volumes {
		err := ctx.Err()
		if err != nil {
			return err // Stop if context is cancelled.
		}

		snapshotName, err := storagePools.VolumeDetermineNextSnapshotName(ctx, s, v.PoolName, v.Name, v.Config)
		if err != nil {
			return fmt.Errorf("Error retrieving next snapshot name for volume %q (project %q, pool %q): %w", v.Name, v.ProjectName, v.PoolName, err)
		}

		expiry, err := shared.GetExpiry(time.Now(), v.Config["snapshots.expiry"])
		if err != nil {
			return fmt.Errorf("Error getting snapshot expiry for volume %q (project %q, pool %q): %w", v.Name, v.ProjectName, v.PoolName, err)
		}

		pool, err := storagePools.LoadByName(s, v.PoolName)
		if err != nil {
			return fmt.Errorf("Error loading pool for volume %q (project %q, pool %q): %w", v.Name, v.ProjectName, v.PoolName, err)
		}

		err = pool.CreateCustomVolumeSnapshot(v.ProjectName, v.Name, snapshotName, v.Description, expiry, nil)
		if err != nil {
			return fmt.Errorf("Error creating snapshot for volume %q (project %q, pool %q): %w", v.Name, v.ProjectName, v.PoolName, err)
		}
	}

	return nil
}
