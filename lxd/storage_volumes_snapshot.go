package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/flosch/pongo2"
	"github.com/gorilla/mux"
	"github.com/pkg/errors"
	log "gopkg.in/inconshreveable/log15.v2"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/response"
	storagePools "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/lxd/task"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"
)

var storagePoolVolumeSnapshotsTypeCmd = APIEndpoint{
	Path: "storage-pools/{pool}/volumes/{type}/{name}/snapshots",

	Get:  APIEndpointAction{Handler: storagePoolVolumeSnapshotsTypeGet, AccessHandler: allowProjectPermission("storage-volumes", "view")},
	Post: APIEndpointAction{Handler: storagePoolVolumeSnapshotsTypePost, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
}

var storagePoolVolumeSnapshotTypeCmd = APIEndpoint{
	Path: "storage-pools/{pool}/volumes/{type}/{name}/snapshots/{snapshotName}",

	Delete: APIEndpointAction{Handler: storagePoolVolumeSnapshotTypeDelete, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Get:    APIEndpointAction{Handler: storagePoolVolumeSnapshotTypeGet, AccessHandler: allowProjectPermission("storage-volumes", "view")},
	Post:   APIEndpointAction{Handler: storagePoolVolumeSnapshotTypePost, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Patch:  APIEndpointAction{Handler: storagePoolVolumeSnapshotTypePatch, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Put:    APIEndpointAction{Handler: storagePoolVolumeSnapshotTypePut, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
}

// swagger:operation POST /1.0/storage-pools/{name}/volumes/{type}/{volume}/snapshots storage storage_pool_volumes_type_snapshots_post
//
// Create a storage volume snapshot
//
// Creates a new storage volume snapshot.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
//   - in: query
//     name: target
//     description: Cluster member name
//     type: string
//     example: lxd01
//   - in: body
//     name: volume
//     description: Storage volume snapshot
//     required: true
//     schema:
//       $ref: "#/definitions/StorageVolumeSnapshotsPost"
// responses:
//   "202":
//     $ref: "#/responses/Operation"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func storagePoolVolumeSnapshotsTypePost(d *Daemon, r *http.Request) response.Response {
	// Get the name of the pool.
	poolName := mux.Vars(r)["pool"]

	// Get the name of the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	// Get the name of the volume.
	volumeName := mux.Vars(r)["name"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if volumeType != db.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	// Get the project name.
	projectName, err := project.StorageVolumeProject(d.State().Cluster, projectParam(r), volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	// Forward if needed.
	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(d, r, poolName, projectName, volumeName, volumeType)
	if resp != nil {
		return resp
	}

	// Parse the request.
	req := api.StorageVolumeSnapshotsPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Get a snapshot name.
	if req.Name == "" {
		i := d.cluster.GetNextStorageVolumeSnapshotIndex(poolName, volumeName, volumeType, "snap%d")
		req.Name = fmt.Sprintf("snap%d", i)
	}

	// Validate the name
	err = storagePools.ValidName(req.Name)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that this isn't a restricted volume
	used, err := storagePools.VolumeUsedByDaemon(d.State(), poolName, volumeName)
	if err != nil {
		return response.InternalError(err)
	}

	if used {
		return response.BadRequest(fmt.Errorf("Volumes used by LXD itself cannot have snapshots"))
	}

	// Retrieve ID of the storage pool (and check if the storage pool exists).
	poolID, err := d.cluster.GetStoragePoolID(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Ensure that the snapshot doesn't already exist.
	_, _, err = d.cluster.GetLocalStoragePoolVolume(projectName, fmt.Sprintf("%s/%s", volumeName, req.Name), volumeType, poolID)
	if err != db.ErrNoSuchObject {
		if err != nil {
			return response.SmartError(err)
		}

		return response.Conflict(fmt.Errorf("Snapshot '%s' already in use", req.Name))
	}

	// Get the parent volume so we can get the config.
	_, vol, err := d.cluster.GetLocalStoragePoolVolume(projectName, volumeName, volumeType, poolID)
	if err != nil {
		return response.SmartError(err)
	}

	// Fill in the expiry.
	var expiry time.Time
	if req.ExpiresAt != nil {
		expiry = *req.ExpiresAt
	} else {
		expiry, err = shared.GetSnapshotExpiry(time.Now(), vol.Config["snapshots.expiry"])
		if err != nil {
			return response.BadRequest(err)
		}
	}

	// Create the snapshot.
	snapshot := func(op *operations.Operation) error {
		pool, err := storagePools.GetPoolByName(d.State(), poolName)
		if err != nil {
			return err
		}

		return pool.CreateCustomVolumeSnapshot(projectName, volumeName, req.Name, expiry, op)
	}

	resources := map[string][]string{}
	resources["storage_volumes"] = []string{volumeName}

	op, err := operations.OperationCreate(d.State(), projectParam(r), operations.OperationClassTask, db.OperationVolumeSnapshotCreate, resources, nil, snapshot, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// swagger:operation GET /1.0/storage-pools/{name}/volumes/{type}/{volume}/snapshots storage storage_pool_volumes_type_snapshots_get
//
// Get the storage volume snapshots
//
// Returns a list of storage volume snapshots (URLs).
//
// ---
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
//   - in: query
//     name: target
//     description: Cluster member name
//     type: string
//     example: lxd01
// responses:
//   "200":
//     description: API endpoints
//     schema:
//       type: object
//       description: Sync response
//       properties:
//         type:
//           type: string
//           description: Response type
//           example: sync
//         status:
//           type: string
//           description: Status description
//           example: Success
//         status_code:
//           type: integer
//           description: Status code
//           example: 200
//         metadata:
//           type: array
//           description: List of endpoints
//           items:
//             type: string
//           example: |-
//             [
//               "/1.0/storage-pools/local/volumes/custom/foo/snapshots/snap0",
//               "/1.0/storage-pools/local/volumes/custom/foo/snapshots/snap1"
//             ]
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/storage-pools/{name}/volumes/{type}/{volume}/snapshots?recursion=1 storage storage_pool_volumes_type_snapshots_get_recursion1
//
// Get the storage volume snapshots
//
// Returns a list of storage volume snapshots (structs).
//
// ---
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
//   - in: query
//     name: target
//     description: Cluster member name
//     type: string
//     example: lxd01
// responses:
//   "200":
//     description: API endpoints
//     schema:
//       type: object
//       description: Sync response
//       properties:
//         type:
//           type: string
//           description: Response type
//           example: sync
//         status:
//           type: string
//           description: Status description
//           example: Success
//         status_code:
//           type: integer
//           description: Status code
//           example: 200
//         metadata:
//           type: array
//           description: List of storage volume snapshots
//           items:
//             $ref: "#/definitions/StorageVolumeSnapshot"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func storagePoolVolumeSnapshotsTypeGet(d *Daemon, r *http.Request) response.Response {
	// Get the name of the pool the storage volume is supposed to be attached to.
	poolName := mux.Vars(r)["pool"]

	recursion := util.IsRecursionRequest(r)

	// Get the name of the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	// Get the name of the volume type.
	volumeName := mux.Vars(r)["name"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	projectName, err := project.StorageVolumeProject(d.State().Cluster, projectParam(r), volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	// Retrieve ID of the storage pool (and check if the storage pool exists).
	poolID, err := d.cluster.GetStoragePoolID(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Get the names of all storage volume snapshots of a given volume.
	volumes, err := d.cluster.GetLocalStoragePoolVolumeSnapshotsWithType(projectName, volumeName, volumeType, poolID)
	if err != nil {
		return response.SmartError(err)
	}

	// Prepare the response.
	resultString := []string{}
	resultMap := []*api.StorageVolumeSnapshot{}
	for _, volume := range volumes {
		_, snapshotName, _ := shared.InstanceGetParentAndSnapshotName(volume.Name)

		if !recursion {
			resultString = append(resultString, fmt.Sprintf("/%s/storage-pools/%s/volumes/%s/%s/snapshots/%s", version.APIVersion, poolName, volumeTypeName, volumeName, snapshotName))
		} else {
			_, vol, err := d.cluster.GetLocalStoragePoolVolume(projectName, volume.Name, volumeType, poolID)
			if err != nil {
				continue
			}

			volumeUsedBy, err := storagePoolVolumeUsedByGet(d.State(), projectName, poolName, vol)
			if err != nil {
				return response.SmartError(err)
			}
			vol.UsedBy = project.FilterUsedBy(r, volumeUsedBy)

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

// swagger:operation POST /1.0/storage-pools/{name}/volumes/{type}/{volume}/snapshots/{snapshot} storage storage_pool_volumes_type_snapshot_post
//
// Rename a storage volume snapshot
//
// Renames a storage volume snapshot.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
//   - in: query
//     name: target
//     description: Cluster member name
//     type: string
//     example: lxd01
//   - in: body
//     name: volume rename
//     description: Storage volume snapshot
//     required: true
//     schema:
//       $ref: "#/definitions/StorageVolumeSnapshotPost"
// responses:
//   "202":
//     $ref: "#/responses/Operation"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func storagePoolVolumeSnapshotTypePost(d *Daemon, r *http.Request) response.Response {
	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName := mux.Vars(r)["pool"]

	// Get the name of the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	// Get the name of the storage volume.
	volumeName := mux.Vars(r)["name"]

	// Get the name of the storage volume.
	snapshotName := mux.Vars(r)["snapshotName"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if volumeType != db.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	// Get the project name.
	projectName, err := project.StorageVolumeProject(d.State().Cluster, projectParam(r), volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	// Forward if needed.
	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	fullSnapshotName := fmt.Sprintf("%s/%s", volumeName, snapshotName)
	resp = forwardedResponseIfVolumeIsRemote(d, r, poolName, projectName, volumeName, volumeType)
	if resp != nil {
		return resp
	}

	// Parse the request.
	req := api.StorageVolumeSnapshotPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
	if req.Name == "" {
		return response.BadRequest(fmt.Errorf("No name provided"))
	}

	if strings.Contains(req.Name, "/") {
		return response.BadRequest(fmt.Errorf("Storage volume names may not contain slashes"))
	}

	// Rename the snapshot.
	snapshotRename := func(op *operations.Operation) error {
		pool, err := storagePools.GetPoolByName(d.State(), poolName)
		if err != nil {
			return err
		}

		return pool.RenameCustomVolumeSnapshot(projectName, fullSnapshotName, req.Name, op)
	}

	resources := map[string][]string{}
	resources["storage_volume_snapshots"] = []string{volumeName}

	op, err := operations.OperationCreate(d.State(), projectParam(r), operations.OperationClassTask, db.OperationVolumeSnapshotRename, resources, nil, snapshotRename, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// swagger:operation GET /1.0/storage-pools/{name}/volumes/{type}/{volume}/snapshots/{snapshot} storage storage_pool_volumes_type_snapshot_get
//
// Get the storage volume snapshot
//
// Gets a specific storage volume snapshot.
//
// ---
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
//   - in: query
//     name: target
//     description: Cluster member name
//     type: string
//     example: lxd01
// responses:
//   "200":
//     description: Storage volume snapshot
//     schema:
//       type: object
//       description: Sync response
//       properties:
//         type:
//           type: string
//           description: Response type
//           example: sync
//         status:
//           type: string
//           description: Status description
//           example: Success
//         status_code:
//           type: integer
//           description: Status code
//           example: 200
//         metadata:
//           $ref: "#/definitions/StorageVolumeSnapshot"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
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
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Get the project name.
	projectName, err := project.StorageVolumeProject(d.State().Cluster, projectParam(r), volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	// Forward if needed.
	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	fullSnapshotName := fmt.Sprintf("%s/%s", volumeName, snapshotName)
	resp = forwardedResponseIfVolumeIsRemote(d, r, poolName, projectName, fullSnapshotName, volumeType)
	if resp != nil {
		return resp
	}

	// Get the snapshot.
	poolID, _, _, err := d.cluster.GetStoragePool(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	volID, volume, err := d.cluster.GetLocalStoragePoolVolume(projectName, fullSnapshotName, volumeType, poolID)
	if err != nil {
		return response.SmartError(err)
	}

	expiry, err := d.cluster.GetStorageVolumeSnapshotExpiry(volID)
	if err != nil {
		return response.SmartError(err)
	}

	snapshot := api.StorageVolumeSnapshot{}
	snapshot.Config = volume.Config
	snapshot.Description = volume.Description
	snapshot.Name = snapshotName
	snapshot.ExpiresAt = &expiry

	etag := []interface{}{snapshot.Description, expiry}
	return response.SyncResponseETag(true, &snapshot, etag)
}

// swagger:operation PUT /1.0/storage-pools/{name}/volumes/{type}/{volume}/snapshots/{snapshot} storage storage_pool_volumes_type_snapshot_put
//
// Update the storage volume snapshot
//
// Updates the entire storage volume snapshot configuration.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
//   - in: query
//     name: target
//     description: Cluster member name
//     type: string
//     example: lxd01
//   - in: body
//     name: storage volume snapshot
//     description: Storage volume snapshot configuration
//     required: true
//     schema:
//       $ref: "#/definitions/StorageVolumeSnapshotPut"
// responses:
//   "200":
//     $ref: "#/responses/EmptySyncResponse"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "412":
//     $ref: "#/responses/PreconditionFailed"
//   "500":
//     $ref: "#/responses/InternalServerError"
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
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Get the project name.
	projectName, err := project.StorageVolumeProject(d.State().Cluster, projectParam(r), volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	// Forward if needed.
	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	fullSnapshotName := fmt.Sprintf("%s/%s", volumeName, snapshotName)
	resp = forwardedResponseIfVolumeIsRemote(d, r, poolName, projectName, fullSnapshotName, volumeType)
	if resp != nil {
		return resp
	}

	// Get the snapshot.
	poolID, _, _, err := d.cluster.GetStoragePool(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	volID, vol, err := d.cluster.GetLocalStoragePoolVolume(projectName, fullSnapshotName, volumeType, poolID)
	if err != nil {
		return response.SmartError(err)
	}

	expiry, err := d.cluster.GetStorageVolumeSnapshotExpiry(volID)
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag
	etag := []interface{}{vol.Description, expiry}
	err = util.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	req := api.StorageVolumeSnapshotPut{}

	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	return doStoragePoolVolumeSnapshotUpdate(d, r, poolName, projectName, vol.Name, volumeType, req)
}

// swagger:operation PATCH /1.0/storage-pools/{name}/volumes/{type}/{volume}/snapshots/{snapshot} storage storage_pool_volumes_type_snapshot_patch
//
// Partially update the storage volume snapshot
//
// Updates a subset of the storage volume snapshot configuration.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
//   - in: query
//     name: target
//     description: Cluster member name
//     type: string
//     example: lxd01
//   - in: body
//     name: storage volume snapshot
//     description: Storage volume snapshot configuration
//     required: true
//     schema:
//       $ref: "#/definitions/StorageVolumeSnapshotPut"
// responses:
//   "200":
//     $ref: "#/responses/EmptySyncResponse"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "412":
//     $ref: "#/responses/PreconditionFailed"
//   "500":
//     $ref: "#/responses/InternalServerError"
func storagePoolVolumeSnapshotTypePatch(d *Daemon, r *http.Request) response.Response {
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
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Get the project name.
	projectName, err := project.StorageVolumeProject(d.State().Cluster, projectParam(r), volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	// Forward if needed.
	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	fullSnapshotName := fmt.Sprintf("%s/%s", volumeName, snapshotName)
	resp = forwardedResponseIfVolumeIsRemote(d, r, poolName, projectName, fullSnapshotName, volumeType)
	if resp != nil {
		return resp
	}

	// Get the snapshot.
	poolID, _, _, err := d.cluster.GetStoragePool(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	volID, vol, err := d.cluster.GetLocalStoragePoolVolume(projectName, fullSnapshotName, volumeType, poolID)
	if err != nil {
		return response.SmartError(err)
	}

	expiry, err := d.cluster.GetStorageVolumeSnapshotExpiry(volID)
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag
	etag := []interface{}{vol.Description, expiry}
	err = util.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	req := api.StorageVolumeSnapshotPut{
		Description: vol.Description,
		ExpiresAt:   &expiry,
	}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	return doStoragePoolVolumeSnapshotUpdate(d, r, poolName, projectName, vol.Name, volumeType, req)
}

func doStoragePoolVolumeSnapshotUpdate(d *Daemon, r *http.Request, poolName string, projectName string, volName string, volumeType int, req api.StorageVolumeSnapshotPut) response.Response {
	expiry := time.Time{}
	if req.ExpiresAt != nil {
		expiry = *req.ExpiresAt
	}

	pool, err := storagePools.GetPoolByName(d.State(), poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Use an empty operation for this sync response to pass the requestor
	op := &operations.Operation{}
	op.SetRequestor(r)

	// Update the database.
	if volumeType == db.StoragePoolVolumeTypeCustom {
		err = pool.UpdateCustomVolumeSnapshot(projectName, volName, req.Description, nil, expiry, op)
		if err != nil {
			return response.SmartError(err)
		}
	} else {
		inst, err := instance.LoadByProjectAndName(d.State(), projectName, volName)
		if err != nil {
			return response.NotFound(err)
		}

		err = pool.UpdateInstanceSnapshot(inst, req.Description, nil, op)
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.EmptySyncResponse
}

// swagger:operation DELETE /1.0/storage-pools/{name}/volumes/{type}/{volume}/snapshots/{snapshot} storage storage_pool_volumes_type_snapshot_delete
//
// Delete a storage volume snapshot
//
// Deletes a new storage volume snapshot.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
//   - in: query
//     name: target
//     description: Cluster member name
//     type: string
//     example: lxd01
// responses:
//   "202":
//     $ref: "#/responses/Operation"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func storagePoolVolumeSnapshotTypeDelete(d *Daemon, r *http.Request) response.Response {
	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName := mux.Vars(r)["pool"]

	// Get the name of the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	// Get the name of the storage volume.
	volumeName := mux.Vars(r)["name"]

	// Get the name of the storage volume.
	snapshotName := mux.Vars(r)["snapshotName"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if volumeType != db.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	// Get the project name.
	projectName, err := project.StorageVolumeProject(d.State().Cluster, projectParam(r), volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	// Forward if needed.
	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	fullSnapshotName := fmt.Sprintf("%s/%s", volumeName, snapshotName)
	resp = forwardedResponseIfVolumeIsRemote(d, r, poolName, projectName, fullSnapshotName, volumeType)
	if resp != nil {
		return resp
	}

	snapshotDelete := func(op *operations.Operation) error {
		pool, err := storagePools.GetPoolByName(d.State(), poolName)
		if err != nil {
			return err
		}

		return pool.DeleteCustomVolumeSnapshot(projectName, fullSnapshotName, op)
	}

	resources := map[string][]string{}
	resources["storage_volume_snapshots"] = []string{volumeName}

	op, err := operations.OperationCreate(d.State(), projectParam(r), operations.OperationClassTask, db.OperationVolumeSnapshotDelete, resources, nil, snapshotDelete, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func pruneExpireCustomVolumeSnapshotsTask(d *Daemon) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		// Get the list of expired custom volume snapshots.
		expiredSnapshots, err := d.cluster.GetExpiredStorageVolumeSnapshots()
		if err != nil {
			logger.Error("Unable to retrieve the list of expired custom volume snapshots", log.Ctx{"err": err})
			return
		}

		if len(expiredSnapshots) == 0 {
			return
		}

		opRun := func(op *operations.Operation) error {
			return pruneExpiredCustomVolumeSnapshots(ctx, d, expiredSnapshots)
		}

		op, err := operations.OperationCreate(d.State(), "", operations.OperationClassTask, db.OperationCustomVolumeSnapshotsExpire, nil, nil, opRun, nil, nil, nil)
		if err != nil {
			logger.Error("Failed to start expired custom volume snapshots operation", log.Ctx{"err": err})
			return
		}

		logger.Info("Pruning expired custom volume snapshots")
		_, err = op.Run()
		if err != nil {
			logger.Error("Failed to expire backups", log.Ctx{"err": err})
		}
		logger.Info("Done pruning expired custom volume snapshots")
	}

	f(context.Background())

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

func pruneExpiredCustomVolumeSnapshots(ctx context.Context, d *Daemon, expiredSnapshots []db.StorageVolumeArgs) error {
	for _, s := range expiredSnapshots {
		pool, err := storagePools.GetPoolByName(d.State(), s.PoolName)
		if err != nil {
			return errors.Wrapf(err, "Failed to get pool %q", s.PoolName)
		}

		err = pool.DeleteCustomVolumeSnapshot(s.ProjectName, s.Name, nil)
		if err != nil {
			return errors.Wrapf(err, "Error deleting custom volume snapshot %s", s.Name)
		}
	}

	return nil
}

func autoCreateCustomVolumeSnapshotsTask(d *Daemon) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		allVolumes, err := d.cluster.GetStoragePoolVolumesWithType(db.StoragePoolVolumeTypeCustom)
		if err != nil {
			logger.Error("Failed getting volumes for auto custom volume snapshot task", log.Ctx{"err": err})
			return
		}

		localNodeID := d.cluster.GetNodeID()

		var volumes, remoteVolumes []db.StorageVolumeArgs
		for _, v := range allVolumes {
			schedule, ok := v.Config["snapshots.schedule"]
			if !ok || schedule == "" {
				continue
			}

			// Check if snapshot is scheduled.
			if !snapshotIsScheduledNow(schedule, v.ID) {
				continue
			}

			if v.NodeID == localNodeID {
				// Always include local volumes.
				volumes = append(volumes, v)
				logger.Debug("Scheduling local auto custom volume snapshot", log.Ctx{"vol": v.Name, "project": v.ProjectName, "pool": v.PoolName})
			} else if v.NodeID < 0 {
				// Keep a separate list of remote volumes in order to select a member to perform
				// the snapshot later.
				remoteVolumes = append(remoteVolumes, v)
			}
		}

		if len(remoteVolumes) > 0 {
			// Get list of cluster members.
			var nodeCount int
			var onlineNodeIDs []int64
			err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
				// Get the offline threshold.
				config, err := cluster.ConfigLoad(tx)
				if err != nil {
					return errors.Wrap(err, "Failed to load LXD config")
				}

				// Get all the members.
				nodes, err := tx.GetNodes()
				if err != nil {
					return err
				}

				nodeCount = len(nodes)

				// Filter to online members.
				for _, node := range nodes {
					if node.IsOffline(config.OfflineThreshold()) {
						continue
					}

					onlineNodeIDs = append(onlineNodeIDs, node.ID)
				}

				return nil
			})
			if err != nil {
				logger.Error("Failed getting online cluster members for auto custom volume snapshot task", log.Ctx{"err": err})
				return
			}

			// Skip snapshotting remote custom volumes if there are no online members, as we can't be
			// sure that the cluster isn't partitioned and we may end up attempting the snapshot on
			// multiple members.
			if nodeCount > 1 && len(onlineNodeIDs) <= 0 {
				logger.Error("Skipping remote volumes for auto custom volume snapshot task due to no online members")
			} else {
				for _, v := range remoteVolumes {
					// If there are multiple cluster members, a stable random member is chosen
					// to perform the snapshot from. This avoids taking the snapshot on every
					// member and spreads the load taking the snapshots across the online
					// cluster members.
					if nodeCount > 1 {
						selectedNodeID, err := util.GetStableRandomInt64FromList(int64(v.ID), onlineNodeIDs)
						if err != nil {
							logger.Error("Failed scheduling remote auto custom volume snapshot task", log.Ctx{"vol": v.Name, "project": v.ProjectName, "pool": v.PoolName, "err": err})
							continue
						}

						// Don't snapshot, if we're not the chosen one.
						if d.cluster.GetNodeID() != selectedNodeID {
							continue
						}
					}

					logger.Debug("Scheduling remote auto custom volume snapshot", log.Ctx{"vol": v.Name, "project": v.ProjectName, "pool": v.PoolName})
					volumes = append(volumes, v)
				}
			}
		}

		if len(volumes) == 0 {
			return
		}

		opRun := func(op *operations.Operation) error {
			autoCreateCustomVolumeSnapshots(ctx, d, volumes)
			return nil
		}

		op, err := operations.OperationCreate(d.State(), "", operations.OperationClassTask, db.OperationVolumeSnapshotCreate, nil, nil, opRun, nil, nil, nil)
		if err != nil {
			logger.Error("Failed to start create volume snapshot operation", log.Ctx{"err": err})
			return
		}

		logger.Info("Creating scheduled volume snapshots")

		_, err = op.Run()
		if err != nil {
			logger.Error("Failed to create scheduled volume snapshots", log.Ctx{"err": err})
		}

		logger.Info("Done creating scheduled volume snapshots")
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

func autoCreateCustomVolumeSnapshots(ctx context.Context, d *Daemon, volumes []db.StorageVolumeArgs) {
	// Make the snapshots sequentially.
	for _, v := range volumes {
		// Run snapshot process in a go routine then collect the result, to allow context cancellation.
		ch := make(chan struct{})
		go func() {
			snapshotName, err := volumeDetermineNextSnapshotName(d, v, "snap%d")
			if err != nil {
				logger.Error("Error retrieving next snapshot name", log.Ctx{"err": err, "volume": v})
				ch <- struct{}{}
				return
			}

			expiry, err := shared.GetSnapshotExpiry(time.Now(), v.Config["snapshots.expiry"])
			if err != nil {
				logger.Error("Error getting expiry date", log.Ctx{"err": err, "volume": v})
				ch <- struct{}{}
				return
			}

			pool, err := storagePools.GetPoolByName(d.State(), v.PoolName)
			if err != nil {
				logger.Error("Error retrieving pool", log.Ctx{"err": err, "pool": v.PoolName})
				ch <- struct{}{}
				return
			}

			err = pool.CreateCustomVolumeSnapshot(v.ProjectName, v.Name, snapshotName, expiry, nil)
			if err != nil {
				logger.Error("Error creating volume snapshot", log.Ctx{"err": err, "volume": v})
			}

			ch <- struct{}{}
		}()
		select {
		case <-ctx.Done():
			return
		case <-ch:
		}
	}
}

func volumeDetermineNextSnapshotName(d *Daemon, volume db.StorageVolumeArgs, defaultPattern string) (string, error) {
	var err error

	pattern, ok := volume.Config["snapshots.pattern"]
	if !ok {
		pattern = defaultPattern
	}

	pattern, err = shared.RenderTemplate(pattern, pongo2.Context{
		"creation_date": time.Now(),
	})
	if err != nil {
		return "", err
	}

	count := strings.Count(pattern, "%d")
	if count > 1 {
		return "", fmt.Errorf("Snapshot pattern may contain '%%d' only once")
	} else if count == 1 {
		i := d.cluster.GetNextStorageVolumeSnapshotIndex(volume.PoolName, volume.Name, db.StoragePoolVolumeTypeCustom, pattern)
		return strings.Replace(pattern, "%d", strconv.Itoa(i), 1), nil
	}

	snapshotExists := false

	var snapshots []db.StorageVolumeArgs
	var projects []string

	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		projects, err = tx.GetProjectNames()
		return err
	})
	if err != nil {
		return "", err
	}

	pools, err := d.cluster.GetStoragePoolNames()
	if err != nil {
		return "", err
	}

	for _, pool := range pools {
		poolID, err := d.cluster.GetStoragePoolID(pool)
		if err != nil {
			return "", err
		}

		for _, project := range projects {
			snaps, err := d.cluster.GetLocalStoragePoolVolumeSnapshotsWithType(project, volume.Name, db.StoragePoolVolumeTypeCustom, poolID)
			if err != nil {
				return "", err
			}

			snapshots = append(snapshots, snaps...)
		}
	}

	for _, snap := range snapshots {
		_, snapOnlyName, _ := shared.InstanceGetParentAndSnapshotName(snap.Name)

		if snapOnlyName == pattern {
			snapshotExists = true
			break
		}
	}

	if snapshotExists {
		i := d.cluster.GetNextStorageVolumeSnapshotIndex(volume.PoolName, volume.Name, db.StoragePoolVolumeTypeCustom, pattern)
		return strings.Replace(pattern, "%d", strconv.Itoa(i), 1), nil
	}

	return pattern, nil
}
