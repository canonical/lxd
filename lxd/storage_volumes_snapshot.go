package main

import (
	"context"
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

	Get:  APIEndpointAction{Handler: storagePoolVolumeSnapshotsTypeGetHandler, AccessHandler: allowProjectResourceList},
	Post: APIEndpointAction{Handler: storagePoolVolumeSnapshotsTypePostHandler, AccessHandler: storagePoolVolumeTypeAccessHandler(entity.TypeStorageVolume, auth.EntitlementCanManageSnapshots)},
}

var storagePoolVolumeSnapshotTypeCmd = APIEndpoint{
	Path:        "storage-pools/{poolName}/volumes/{type}/{volumeName}/snapshots/{snapshotName}",
	MetricsType: entity.TypeStoragePool,

	Delete: APIEndpointAction{Handler: storagePoolVolumeSnapshotTypeDeleteHandler, AccessHandler: storagePoolVolumeTypeAccessHandler(entity.TypeStorageVolumeSnapshot, auth.EntitlementCanDelete)},
	Get:    APIEndpointAction{Handler: storagePoolVolumeSnapshotTypeGetHandler, AccessHandler: storagePoolVolumeTypeAccessHandler(entity.TypeStorageVolumeSnapshot, auth.EntitlementCanView)},
	Post:   APIEndpointAction{Handler: storagePoolVolumeSnapshotTypePostHandler, AccessHandler: storagePoolVolumeTypeAccessHandler(entity.TypeStorageVolumeSnapshot, auth.EntitlementCanEdit)},
	Patch:  APIEndpointAction{Handler: storagePoolVolumeSnapshotTypePatchHandler, AccessHandler: storagePoolVolumeTypeAccessHandler(entity.TypeStorageVolumeSnapshot, auth.EntitlementCanEdit)},
	Put:    APIEndpointAction{Handler: storagePoolVolumeSnapshotTypePutHandler, AccessHandler: storagePoolVolumeTypeAccessHandler(entity.TypeStorageVolumeSnapshot, auth.EntitlementCanEdit)},
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
func storagePoolVolumeSnapshotsTypePostHandler(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Parse the request.
	req := api.StorageVolumeSnapshotsPost{}
	err := request.DecodeAndRestoreJSONBody(r, &req)
	if err != nil {
		return response.SmartError(err)
	}

	reqProjectName := request.ProjectParam(r)
	target := request.QueryParam(r, "target")

	op, err := storagePoolVolumeSnapshotsTypePost(r.Context(), s, req, reqProjectName, target)
	if err != nil {
		return response.SmartError(err)
	}

	return operations.OperationResponse(op)
}

func storagePoolVolumeSnapshotsTypePost(reqContext context.Context, s *state.State, req api.StorageVolumeSnapshotsPost, reqProjectName string, target string) (*operations.Operation, error) {
	details, err := request.GetCtxValue[storageVolumeDetails](reqContext, ctxStorageVolumeDetails)
	if err != nil {
		return nil, err
	}

	// Check that the storage volume type is valid.
	if details.volumeType != dbCluster.StoragePoolVolumeTypeCustom {
		return nil, api.StatusErrorf(http.StatusBadRequest, "Invalid storage volume type %q", details.volumeTypeName)
	}

	effectiveProjectName, err := request.GetCtxValue[string](reqContext, request.CtxEffectiveProjectName)
	if err != nil {
		return nil, err
	}

	// Forward if needed.
	err = forwardIfTargetIsRemote(reqContext, s, target)
	if err != nil {
		return nil, err
	}

	err = forwardIfVolumeIsRemote(reqContext, s)
	if err != nil {
		return nil, err
	}

	err = s.DB.Cluster.Transaction(reqContext, func(ctx context.Context, tx *db.ClusterTx) error {
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
		return nil, err
	}

	// Check that this isn't a restricted volume
	used, err := storagePools.VolumeUsedByDaemon(s, details.pool.Name(), details.volumeName)
	if err != nil {
		return nil, err
	}

	if used {
		return nil, api.NewStatusError(http.StatusBadRequest, "Volumes used by LXD itself cannot have snapshots")
	}

	var parentDBVolume *db.StorageVolume
	var parentVolumeArgs db.StorageVolumeArgs
	err = s.DB.Cluster.Transaction(reqContext, func(ctx context.Context, tx *db.ClusterTx) error {
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
		return nil, err
	}

	if req.Name == "" {
		snapName, err := storagePools.VolumeDetermineNextSnapshotName(reqContext, s, parentVolumeArgs.PoolName, parentVolumeArgs.Name, parentVolumeArgs.Config)
		if err != nil {
			return nil, err
		}

		req.Name = snapName
	}

	// Validate the snapshot name using same rule as pool name.
	err = details.pool.ValidateName(req.Name)
	if err != nil {
		return nil, err
	}

	err = s.DB.Cluster.Transaction(reqContext, func(ctx context.Context, tx *db.ClusterTx) error {
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
		return nil, err
	}

	// Create the snapshot.
	snapshot := func(op *operations.Operation) error {
		return details.pool.CreateCustomVolumeSnapshot(effectiveProjectName, details.volumeName, req.Name, req.Description, req.ExpiresAt, op)
	}

	resources := map[string][]api.URL{}
	resources["storage_volumes"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", details.pool.Name(), "volumes", details.volumeTypeName, details.volumeName)}
	resources["storage_volume_snapshots"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", details.pool.Name(), "volumes", details.volumeTypeName, details.volumeName, "snapshots", req.Name)}

	return operations.OperationCreate(reqContext, s, reqProjectName, operations.OperationClassTask, operationtype.VolumeSnapshotCreate, resources, nil, snapshot, nil, nil)
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
func storagePoolVolumeSnapshotsTypeGetHandler(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	err := addStoragePoolVolumeDetailsToRequestContext(s, r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := request.ProjectParam(r)
	target := request.QueryParam(r, "target")

	if util.IsRecursionRequest(r) {
		snapshots, err := storagePoolVolumeSnapshotsTypeGet(r.Context(), s, projectName, target)
		if err != nil {
			return response.SmartError(err)
		}

		return response.SyncResponse(true, snapshots)
	}

	urls, err := storagePoolVolumeSnapshotsTypeURLGet(r.Context(), s, projectName, target)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, urls)
}

func storagePoolVolumeSnapshotsTypeGet(reqContext context.Context, s *state.State, projectName string, target string) ([]*api.StorageVolumeSnapshot, error) {
	details, err := request.GetCtxValue[storageVolumeDetails](reqContext, ctxStorageVolumeDetails)
	if err != nil {
		return nil, err
	}

	// Check that the storage volume type is valid.
	if !shared.ValueInSlice(details.volumeType, supportedVolumeTypes) {
		return nil, api.StatusErrorf(http.StatusBadRequest, "Invalid storage volume type %q", details.volumeTypeName)
	}

	effectiveProjectName, err := request.GetCtxValue[string](reqContext, request.CtxEffectiveProjectName)
	if err != nil {
		return nil, err
	}

	// Forward if needed.
	err = forwardIfTargetIsRemote(reqContext, s, target)
	if err != nil {
		return nil, err
	}

	volumes, err := getDBStoragePoolVolumeSnapshots(reqContext, s, effectiveProjectName, details)
	if err != nil {
		return nil, err
	}

	canView, err := s.Authorizer.GetPermissionChecker(reqContext, auth.EntitlementCanView, entity.TypeStorageVolumeSnapshot)
	if err != nil {
		return nil, err
	}

	// Prepare the response.
	snapshots := []*api.StorageVolumeSnapshot{}
	for _, volume := range volumes {
		_, snapshotName, _ := api.GetParentAndSnapshotName(volume.Name)

		if !canView(entity.StorageVolumeSnapshotURL(projectName, details.location, details.pool.Name(), details.volumeTypeName, details.volumeName, snapshotName)) {
			continue
		}

		var vol *db.StorageVolume
		err = s.DB.Cluster.Transaction(reqContext, func(ctx context.Context, tx *db.ClusterTx) error {
			vol, err = tx.GetStoragePoolVolume(ctx, details.pool.ID(), effectiveProjectName, details.volumeType, volume.Name, true)
			return err
		})
		if err != nil {
			return nil, err
		}

		volumeUsedBy, err := storagePoolVolumeUsedByGet(s, effectiveProjectName, vol)
		if err != nil {
			return nil, err
		}

		vol.UsedBy = project.FilterUsedBy(reqContext, s.Authorizer, volumeUsedBy)

		snap := &api.StorageVolumeSnapshot{}
		snap.Config = vol.Config
		snap.Description = vol.Description
		snap.Name = vol.Name
		snap.CreatedAt = vol.CreatedAt
		snap.ExpiresAt = &volume.ExpiryDate

		snapshots = append(snapshots, snap)
	}

	return snapshots, nil
}

func storagePoolVolumeSnapshotsTypeURLGet(reqContext context.Context, s *state.State, projectName string, target string) ([]string, error) {
	details, err := request.GetCtxValue[storageVolumeDetails](reqContext, ctxStorageVolumeDetails)
	if err != nil {
		return nil, err
	}

	// Check that the storage volume type is valid.
	if !shared.ValueInSlice(details.volumeType, supportedVolumeTypes) {
		return nil, api.StatusErrorf(http.StatusBadRequest, "Invalid storage volume type %q", details.volumeTypeName)
	}

	effectiveProjectName, err := request.GetCtxValue[string](reqContext, request.CtxEffectiveProjectName)
	if err != nil {
		return nil, err
	}

	// Forward if needed.
	err = forwardIfTargetIsRemote(reqContext, s, target)
	if err != nil {
		return nil, err
	}

	volumes, err := getDBStoragePoolVolumeSnapshots(reqContext, s, effectiveProjectName, details)
	if err != nil {
		return nil, err
	}

	canView, err := s.Authorizer.GetPermissionChecker(reqContext, auth.EntitlementCanView, entity.TypeStorageVolumeSnapshot)
	if err != nil {
		return nil, err
	}

	// Prepare the response.
	result := []string{}
	for _, volume := range volumes {
		_, snapshotName, _ := api.GetParentAndSnapshotName(volume.Name)

		if !canView(entity.StorageVolumeSnapshotURL(projectName, details.location, details.pool.Name(), details.volumeTypeName, details.volumeName, snapshotName)) {
			continue
		}

		result = append(result, fmt.Sprintf("/%s/storage-pools/%s/volumes/%s/%s/snapshots/%s", version.APIVersion, details.pool.Name(), details.volumeTypeName, details.volumeName, snapshotName))
	}

	return result, nil
}

func getDBStoragePoolVolumeSnapshots(ctx context.Context, s *state.State, projectName string, details storageVolumeDetails) ([]db.StorageVolumeArgs, error) {
	var volumes []db.StorageVolumeArgs
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		// Get the names of all storage volume snapshots of a given volume.
		volumes, err = tx.GetLocalStoragePoolVolumeSnapshotsWithType(ctx, projectName, details.volumeName, details.volumeType, details.pool.ID())
		if err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return volumes, nil
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
func storagePoolVolumeSnapshotTypePostHandler(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get the name of the storage volume.
	snapshotName, err := url.PathUnescape(mux.Vars(r)["snapshotName"])
	if err != nil {
		return response.SmartError(err)
	}

	reqProjectName := request.ProjectParam(r)
	target := request.QueryParam(r, "target")

	// Parse the request.
	req := api.StorageVolumeSnapshotPost{}
	err = request.DecodeAndRestoreJSONBody(r, &req)
	if err != nil {
		return response.SmartError(err)
	}

	op, err := storagePoolVolumeSnapshotTypePost(r.Context(), s, snapshotName, req, reqProjectName, target)
	if err != nil {
		return response.SmartError(err)
	}

	return operations.OperationResponse(op)
}

func storagePoolVolumeSnapshotTypePost(reqContext context.Context, s *state.State, snapshotName string, req api.StorageVolumeSnapshotPost, reqProjectName string, target string) (*operations.Operation, error) {
	details, err := request.GetCtxValue[storageVolumeDetails](reqContext, ctxStorageVolumeDetails)
	if err != nil {
		return nil, err
	}

	fullSnapshotName := fmt.Sprintf("%s/%s", details.volumeName, snapshotName)

	// Check that the storage volume type is valid.
	if details.volumeType != dbCluster.StoragePoolVolumeTypeCustom {
		return nil, api.StatusErrorf(http.StatusBadRequest, "Invalid storage volume type %q", details.volumeTypeName)
	}

	effectiveProjectName, err := request.GetCtxValue[string](reqContext, request.CtxEffectiveProjectName)
	if err != nil {
		return nil, err
	}

	// Check new volume name is valid.
	err = storagePools.ValidVolumeName(req.Name)
	if err != nil {
		return nil, api.NewStatusError(http.StatusBadRequest, err.Error())
	}

	// Forward if needed.
	err = forwardIfTargetIsRemote(reqContext, s, target)
	if err != nil {
		return nil, err
	}

	err = forwardIfVolumeIsRemote(reqContext, s)
	if err != nil {
		return nil, err
	}

	// This is a migration request so send back requested secrets.
	if req.Migration {
		req := api.StorageVolumePost{
			Name:   req.Name,
			Target: req.Target,
		}

		return storagePoolVolumeTypePostMigration(reqContext, s, reqProjectName, effectiveProjectName, details.pool.Name(), fullSnapshotName, req)
	}

	// Rename the snapshot.
	snapshotRename := func(op *operations.Operation) error {
		return details.pool.RenameCustomVolumeSnapshot(effectiveProjectName, fullSnapshotName, req.Name, op)
	}

	resources := map[string][]api.URL{}
	resources["storage_volume_snapshots"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", details.pool.Name(), "volumes", details.volumeTypeName, details.volumeName, "snapshots", snapshotName)}

	return operations.OperationCreate(reqContext, s, reqProjectName, operations.OperationClassTask, operationtype.VolumeSnapshotRename, resources, nil, snapshotRename, nil, nil)
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
func storagePoolVolumeSnapshotTypeGetHandler(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get the name of the storage volume.
	snapshotName, err := url.PathUnescape(mux.Vars(r)["snapshotName"])
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

	snapshot, etag, err := storagePoolVolumeSnapshotTypeGet(r.Context(), s, snapshotName)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponseETag(true, snapshot, etag)
}

func storagePoolVolumeSnapshotTypeGet(reqContext context.Context, s *state.State, snapshotName string) (snapshot *api.StorageVolumeSnapshot, etag any, err error) {
	details, err := request.GetCtxValue[storageVolumeDetails](reqContext, ctxStorageVolumeDetails)
	if err != nil {
		return nil, nil, err
	}

	effectiveProjectName, err := request.GetCtxValue[string](reqContext, request.CtxEffectiveProjectName)
	if err != nil {
		return nil, nil, err
	}

	fullSnapshotName := fmt.Sprintf("%s/%s", details.volumeName, snapshotName)

	var dbVolume *db.StorageVolume
	var expiry time.Time

	err = s.DB.Cluster.Transaction(reqContext, func(ctx context.Context, tx *db.ClusterTx) error {
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
		return nil, nil, err
	}

	snapshot = &api.StorageVolumeSnapshot{}
	snapshot.Config = dbVolume.Config
	snapshot.Description = dbVolume.Description
	snapshot.Name = snapshotName
	snapshot.ExpiresAt = &expiry
	snapshot.ContentType = dbVolume.ContentType
	snapshot.CreatedAt = dbVolume.CreatedAt

	etag = []any{snapshot.Description, expiry}

	return snapshot, etag, nil
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
func storagePoolVolumeSnapshotTypePutHandler(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get the name of the storage volume.
	snapshotName, err := url.PathUnescape(mux.Vars(r)["snapshotName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Parse the request.
	req := api.StorageVolumeSnapshotPut{}
	err = request.DecodeAndRestoreJSONBody(r, &req)
	if err != nil {
		return response.SmartError(err)
	}

	etag := r.Header.Get("If-Match")
	target := request.QueryParam(r, "target")

	err = storagePoolVolumeSnapshotTypePut(r.Context(), s, snapshotName, req, etag, target)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

func storagePoolVolumeSnapshotTypePut(reqContext context.Context, s *state.State, snapshotName string, req api.StorageVolumeSnapshotPut, reqETag string, target string) error {
	details, err := request.GetCtxValue[storageVolumeDetails](reqContext, ctxStorageVolumeDetails)
	if err != nil {
		return err
	}

	effectiveProjectName, err := request.GetCtxValue[string](reqContext, request.CtxEffectiveProjectName)
	if err != nil {
		return err
	}

	// Forward if needed.
	err = forwardIfTargetIsRemote(reqContext, s, target)
	if err != nil {
		return err
	}

	err = forwardIfVolumeIsRemote(reqContext, s)
	if err != nil {
		return err
	}

	fullSnapshotName := fmt.Sprintf("%s/%s", details.volumeName, snapshotName)

	var dbVolume *db.StorageVolume
	var expiry time.Time

	err = s.DB.Cluster.Transaction(reqContext, func(ctx context.Context, tx *db.ClusterTx) error {
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
		return err
	}

	// Validate the ETag
	etag := []any{dbVolume.Description, expiry}
	err = util.EtagCheckString(reqETag, etag)
	if err != nil {
		return api.NewStatusError(http.StatusPreconditionFailed, err.Error())
	}

	return doStoragePoolVolumeSnapshotUpdate(reqContext, s, effectiveProjectName, dbVolume.Name, details.volumeType, req)
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
func storagePoolVolumeSnapshotTypePatchHandler(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get the name of the storage volume.
	snapshotName, err := url.PathUnescape(mux.Vars(r)["snapshotName"])
	if err != nil {
		return response.SmartError(err)
	}

	req := api.StorageVolumeSnapshotPut{}
	err = request.DecodeAndRestoreJSONBody(r, &req)
	if err != nil {
		return response.SmartError(err)
	}

	etag := r.Header.Get("If-Match")
	target := request.QueryParam(r, "target")

	err = storagePoolVolumeSnapshotTypePatch(r.Context(), s, snapshotName, req, etag, target)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

func storagePoolVolumeSnapshotTypePatch(reqContext context.Context, s *state.State, snapshotName string, req api.StorageVolumeSnapshotPut, reqEtag string, target string) error {
	details, err := request.GetCtxValue[storageVolumeDetails](reqContext, ctxStorageVolumeDetails)
	if err != nil {
		return err
	}

	effectiveProjectName, err := request.GetCtxValue[string](reqContext, request.CtxEffectiveProjectName)
	if err != nil {
		return err
	}

	// Forward if needed.
	err = forwardIfTargetIsRemote(reqContext, s, target)
	if err != nil {
		return err
	}

	err = forwardIfVolumeIsRemote(reqContext, s)
	if err != nil {
		return err
	}

	fullSnapshotName := fmt.Sprintf("%s/%s", details.volumeName, snapshotName)

	var dbVolume *db.StorageVolume
	var expiry time.Time

	err = s.DB.Cluster.Transaction(reqContext, func(ctx context.Context, tx *db.ClusterTx) error {
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
		return err
	}

	// Validate the ETag
	etag := []any{dbVolume.Description, expiry}
	err = util.EtagCheckString(reqEtag, etag)
	if err != nil {
		return api.NewStatusError(http.StatusPreconditionFailed, err.Error())
	}

	req.Description = dbVolume.Description
	req.ExpiresAt = &expiry

	return doStoragePoolVolumeSnapshotUpdate(reqContext, s, effectiveProjectName, dbVolume.Name, details.volumeType, req)
}

func doStoragePoolVolumeSnapshotUpdate(reqContext context.Context, s *state.State, projectName string, volName string, volumeType dbCluster.StoragePoolVolumeType, req api.StorageVolumeSnapshotPut) error {
	details, err := request.GetCtxValue[storageVolumeDetails](reqContext, ctxStorageVolumeDetails)
	if err != nil {
		return err
	}

	expiry := time.Time{}
	if req.ExpiresAt != nil {
		expiry = *req.ExpiresAt
	}

	// Use an empty operation for this sync response to pass the requestor
	op := &operations.Operation{}
	op.SetRequestor(reqContext)

	// Update the database.
	if volumeType == dbCluster.StoragePoolVolumeTypeCustom {
		err = details.pool.UpdateCustomVolumeSnapshot(projectName, volName, req.Description, nil, expiry, op)
		if err != nil {
			return err
		}
	} else {
		inst, err := instance.LoadByProjectAndName(s, projectName, volName)
		if err != nil {
			return err
		}

		err = details.pool.UpdateInstanceSnapshot(inst, req.Description, nil, op)
		if err != nil {
			return err
		}
	}

	return nil
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
func storagePoolVolumeSnapshotTypeDeleteHandler(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get the name of the storage volume.
	snapshotName, err := url.PathUnescape(mux.Vars(r)["snapshotName"])
	if err != nil {
		return response.SmartError(err)
	}

	reqProjectName := request.ProjectParam(r)
	target := request.QueryParam(r, "target")

	op, err := storagePoolVolumeSnapshotTypeDelete(r.Context(), s, snapshotName, reqProjectName, target)
	if err != nil {
		return response.SmartError(err)
	}

	return operations.OperationResponse(op)
}

func storagePoolVolumeSnapshotTypeDelete(reqContext context.Context, s *state.State, snapshotName string, reqProjectName string, target string) (*operations.Operation, error) {
	details, err := request.GetCtxValue[storageVolumeDetails](reqContext, ctxStorageVolumeDetails)
	if err != nil {
		return nil, err
	}

	// Check that the storage volume type is valid.
	if details.volumeType != dbCluster.StoragePoolVolumeTypeCustom {
		return nil, api.StatusErrorf(http.StatusBadRequest, "Invalid storage volume type %q", details.volumeTypeName)
	}

	effectiveProjectName, err := request.GetCtxValue[string](reqContext, request.CtxEffectiveProjectName)
	if err != nil {
		return nil, err
	}

	// Forward if needed.
	err = forwardIfTargetIsRemote(reqContext, s, target)
	if err != nil {
		return nil, err
	}

	err = forwardIfVolumeIsRemote(reqContext, s)
	if err != nil {
		return nil, err
	}

	fullSnapshotName := fmt.Sprintf("%s/%s", details.volumeName, snapshotName)

	snapshotDelete := func(op *operations.Operation) error {
		return details.pool.DeleteCustomVolumeSnapshot(effectiveProjectName, fullSnapshotName, op)
	}

	resources := map[string][]api.URL{}
	resources["storage_volume_snapshots"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", details.pool.Name(), "volumes", details.volumeTypeName, details.volumeName, "snapshots", snapshotName)}

	return operations.OperationCreate(reqContext, s, reqProjectName, operations.OperationClassTask, operationtype.VolumeSnapshotDelete, resources, nil, snapshotDelete, nil, nil)
}

func pruneExpiredAndAutoCreateCustomVolumeSnapshotsTask(stateFunc func() *state.State) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		s := stateFunc()

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

			op, err := operations.OperationCreate(context.Background(), s, "", operations.OperationClassTask, operationtype.CustomVolumeSnapshotsExpire, nil, nil, opRun, nil, nil)
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

			op, err := operations.OperationCreate(context.Background(), s, "", operations.OperationClassTask, operationtype.VolumeSnapshotCreate, nil, nil, opRun, nil, nil)
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

		pool, err := storagePools.LoadByName(s, v.PoolName)
		if err != nil {
			return fmt.Errorf("Error loading pool for volume %q (project %q, pool %q): %w", v.Name, v.ProjectName, v.PoolName, err)
		}

		err = pool.CreateCustomVolumeSnapshot(v.ProjectName, v.Name, snapshotName, v.Description, nil, nil)
		if err != nil {
			return fmt.Errorf("Error creating snapshot for volume %q (project %q, pool %q): %w", v.Name, v.ProjectName, v.PoolName, err)
		}
	}

	return nil
}
