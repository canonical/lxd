package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	storagePools "github.com/canonical/lxd/lxd/storage"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/version"
)

var storagePoolVolumeTypeBitmapsCmd = APIEndpoint{
	Path:            "storage-pools/{poolName}/volumes/{type}/{volumeName}/bitmaps",
	MetricsType:     entity.TypeStoragePool,
	ProjectSpecific: true,

	Get:  APIEndpointAction{Handler: storagePoolVolumeTypeBitmapsGet, AccessHandler: storagePoolVolumeTypeAccessHandler(auth.EntitlementCanView)},
	Post: APIEndpointAction{Handler: storagePoolVolumeTypeBitmapsPost, AccessHandler: storagePoolVolumeTypeAccessHandler(auth.EntitlementCanEdit)},
}

var storagePoolVolumeTypeBitmapCmd = APIEndpoint{
	Path:            "storage-pools/{poolName}/volumes/{type}/{volumeName}/bitmaps/{bitmapName}",
	MetricsType:     entity.TypeStoragePool,
	ProjectSpecific: true,

	Get:    APIEndpointAction{Handler: storagePoolVolumeTypeBitmapGet, AccessHandler: storagePoolVolumeTypeAccessHandler(auth.EntitlementCanView)},
	Delete: APIEndpointAction{Handler: storagePoolVolumeTypeBitmapDelete, AccessHandler: storagePoolVolumeTypeAccessHandler(auth.EntitlementCanEdit)},
}

// storagePoolVolumeTypeBitmapInstance applies the preconditions shared by every bitmap endpoint and returns the
// running virtual machine that holds the volume together with the disk device name the volume is attached through,
// the volume details from the request context and the effective project of the volume. The request is forwarded
// when the volume or the instance using it lives on another cluster member. When the returned response is non-nil,
// the caller should return it immediately. operation names the caller's action in the precondition errors.
func storagePoolVolumeTypeBitmapInstance(s *state.State, r *http.Request, operation string) (inst instance.Instance, deviceName string, details storageVolumeDetails, effectiveProjectName string, resp response.Response) {
	details, err := request.GetContextValue[storageVolumeDetails](r.Context(), ctxStorageVolumeDetails)
	if err != nil {
		return nil, "", details, "", response.SmartError(err)
	}

	// Check that the storage volume type is valid.
	if !slices.Contains([]cluster.StoragePoolVolumeType{cluster.StoragePoolVolumeTypeVM, cluster.StoragePoolVolumeTypeCustom}, details.volumeType) {
		return nil, "", details, "", response.BadRequest(fmt.Errorf("Invalid storage volume type %q", details.volumeTypeName))
	}

	effectiveProjectName, err = request.GetContextValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return nil, "", details, "", response.SmartError(err)
	}

	// Forward if needed.
	target := request.QueryParam(r, "target")
	resp = forwardedResponseToNode(r.Context(), s, target)
	if resp != nil {
		return nil, "", details, "", resp
	}

	resp = forwardedResponseIfVolumeIsRemote(r.Context(), s)
	if resp != nil {
		return nil, "", details, "", resp
	}

	dbVolume, err := storagePools.VolumeDBGet(details.pool, effectiveProjectName, details.volumeName, storagePools.VolumeDBTypeToType(details.volumeType))
	if err != nil {
		return nil, "", details, "", response.SmartError(err)
	}

	if dbVolume.ContentType != cluster.StoragePoolVolumeContentTypeNameBlock {
		return nil, "", details, "", response.BadRequest(fmt.Errorf("Invalid storage volume content type %q, bitmaps are only supported on block volumes", dbVolume.ContentType))
	}

	inst, deviceName, err = storagePools.InstanceByVolumeName(s, details.pool.Name(), effectiveProjectName, details.volumeName, details.volumeType)
	if err != nil {
		if errors.Is(err, storagePools.ErrVolumeNotAttached) {
			return nil, "", details, "", response.BadRequest(errors.New("Volume must be attached to a running virtual machine"))
		}

		return nil, "", details, "", response.SmartError(err)
	}

	// IsRunning only knows about the local member, so the member running the instance serves the request.
	// A targeted or forwarded request is not forwarded again, as the forwarded response keeps the caller's
	// target and would bounce between the two members.
	if inst.Location() != s.ServerName {
		requestor, err := request.GetRequestor(r.Context())
		if err != nil {
			return nil, "", details, "", response.SmartError(err)
		}

		if target != "" || requestor.IsForwarded() {
			return nil, "", details, "", response.BadRequest(fmt.Errorf("Instance %q is located on cluster member %q", inst.Name(), inst.Location()))
		}

		resp = forwardedResponseToNode(r.Context(), s, inst.Location())
		if resp != nil {
			return nil, "", details, "", resp
		}
	}

	if inst.Type() != instancetype.VM {
		return nil, "", details, "", response.BadRequest(fmt.Errorf("%s requires the volume to be attached to a virtual machine", operation))
	}

	if !inst.IsRunning() {
		return nil, "", details, "", response.BadRequest(fmt.Errorf("%s requires the instance to be running", operation))
	}

	return inst, deviceName, details, effectiveProjectName, nil
}

// swagger:operation GET /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/bitmaps storage storage_pool_volumes_type_bitmaps_get
//
//	Get the storage volume bitmaps
//
//	Returns a list of storage volume bitmaps (URLs).
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
//	          description: List of endpoints
//	          items:
//	            type: string
//	          example: |-
//	            [
//	              "/1.0/storage-pools/local/volumes/virtual-machine/v1/bitmaps/bitmap0",
//	              "/1.0/storage-pools/local/volumes/virtual-machine/v1/bitmaps/bitmap1"
//	            ]
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/bitmaps?recursion=1 storage storage_pool_volumes_type_bitmaps_get_recursion1
//
//	Get the storage volume bitmaps
//
//	Returns a list of storage volume bitmaps (structs).
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
//	          description: List of storage volume bitmaps
//	          items:
//	            $ref: "#/definitions/StorageVolumeBitmap"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumeTypeBitmapsGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	inst, deviceName, details, _, resp := storagePoolVolumeTypeBitmapInstance(s, r, "Listing bitmaps")
	if resp != nil {
		return resp
	}

	bitmaps, err := inst.GetBitmaps(deviceName)
	if err != nil {
		return response.SmartError(err)
	}

	recursion, _ := util.IsRecursionRequest(r)
	if recursion != 0 {
		return response.SyncResponse(true, bitmaps)
	}

	resultString := make([]string, 0, len(bitmaps))
	for _, bitmap := range bitmaps {
		resultString = append(resultString, api.NewURL().Path(version.APIVersion, "storage-pools", details.pool.Name(), "volumes", details.volumeTypeName, details.volumeName, "bitmaps", bitmap.Name).String())
	}

	return response.SyncResponse(true, resultString)
}

// swagger:operation POST /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/bitmaps storage storage_pool_volumes_type_bitmaps_post
//
//	Create a storage volume bitmap
//
//	Creates a new dirty bitmap on the storage volume.
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
//	    name: bitmap
//	    description: Storage volume bitmap
//	    required: true
//	    schema:
//	      $ref: "#/definitions/StorageVolumeBitmapsPost"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumeTypeBitmapsPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	inst, deviceName, details, effectiveProjectName, resp := storagePoolVolumeTypeBitmapInstance(s, r, "Creating a bitmap")
	if resp != nil {
		return resp
	}

	req := api.StorageVolumeBitmapsPost{}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if req.Name == "" {
		return response.BadRequest(errors.New("Bitmap name is required"))
	}

	run := func(_ context.Context, _ *operations.Operation) error {
		return inst.CreateBitmap([]string{deviceName}, req)
	}

	args := operations.OperationArgs{
		ProjectName: request.ProjectParam(r),
		EntityURL:   entity.StorageVolumeURL(effectiveProjectName, details.location, details.pool.Name(), details.volumeTypeName, details.volumeName),
		Type:        operationtype.VolumeBitmapCreate,
		Class:       operationtype.OperationClassTask,
		RunHook:     run,
	}

	op, err := operations.ScheduleUserOperationFromRequest(s, r, args)
	if err != nil {
		return response.InternalError(err)
	}

	return response.OperationResponse(op)
}

// swagger:operation GET /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/bitmaps/{bitmapName} storage storage_pool_volumes_type_bitmap_get
//
//	Get the storage volume bitmap
//
//	Gets a specific storage volume bitmap.
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
//	    description: Storage volume bitmap
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
//	          $ref: "#/definitions/StorageVolumeBitmap"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumeTypeBitmapGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	bitmapName := r.PathValue("bitmapName")

	inst, deviceName, _, _, resp := storagePoolVolumeTypeBitmapInstance(s, r, "Getting a bitmap")
	if resp != nil {
		return resp
	}

	bitmaps, err := inst.GetBitmaps(deviceName)
	if err != nil {
		return response.SmartError(err)
	}

	for _, bitmap := range bitmaps {
		if bitmap.Name == bitmapName {
			return response.SyncResponse(true, bitmap)
		}
	}

	return response.NotFound(errors.New("Bitmap not found"))
}

// swagger:operation DELETE /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/bitmaps/{bitmapName} storage storage_pool_volumes_type_bitmap_delete
//
//	Delete a storage volume bitmap
//
//	Deletes a storage volume bitmap.
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
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumeTypeBitmapDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	bitmapName := r.PathValue("bitmapName")

	inst, deviceName, details, effectiveProjectName, resp := storagePoolVolumeTypeBitmapInstance(s, r, "Deleting a bitmap")
	if resp != nil {
		return resp
	}

	bitmaps, err := inst.GetBitmaps(deviceName)
	if err != nil {
		return response.SmartError(err)
	}

	if !slices.ContainsFunc(bitmaps, func(bitmap api.StorageVolumeBitmap) bool { return bitmap.Name == bitmapName }) {
		return response.NotFound(errors.New("Bitmap not found"))
	}

	run := func(_ context.Context, _ *operations.Operation) error {
		return inst.DeleteBitmap(deviceName, bitmapName)
	}

	args := operations.OperationArgs{
		ProjectName: request.ProjectParam(r),
		EntityURL:   entity.StorageVolumeURL(effectiveProjectName, details.location, details.pool.Name(), details.volumeTypeName, details.volumeName),
		Type:        operationtype.VolumeBitmapDelete,
		Class:       operationtype.OperationClassTask,
		RunHook:     run,
	}

	op, err := operations.ScheduleUserOperationFromRequest(s, r, args)
	if err != nil {
		return response.InternalError(err)
	}

	return response.OperationResponse(op)
}
