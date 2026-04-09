package main

import (
	"errors"
	"net/http"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/version"
)

var internalInstanceBitmapsCmd = APIEndpoint{
	Path: "instances/{name}/bitmaps",

	Get: APIEndpointAction{Handler: instanceBitmapsGet, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

var internalInstanceBitmapCmd = APIEndpoint{
	Path: "instances/{name}/bitmaps/{bitmapName}",

	Get: APIEndpointAction{Handler: instanceBitmapGet, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

var instanceSnapshotBitmapsCmd = APIEndpoint{
	Path:            "instances/{name}/snapshots/{snapshotName}/bitmaps",
	MetricsType:     entity.TypeInstance,
	ProjectSpecific: true,

	Get: APIEndpointAction{Handler: instanceBitmapsGet, AccessHandler: allowPermission(entity.TypeInstanceSnapshot, auth.EntitlementCanView, "name", "snapshotName")},
}

var instanceSnapshotBitmapCmd = APIEndpoint{
	Path:            "instances/{name}/snapshots/{snapshotName}/bitmaps/{bitmapName}",
	MetricsType:     entity.TypeInstance,
	ProjectSpecific: true,

	Get: APIEndpointAction{Handler: instanceBitmapGet, AccessHandler: allowPermission(entity.TypeInstanceSnapshot, auth.EntitlementCanView, "name", "snapshotName")},
}

// instanceBitmapsLoad loads the instance, or the instance snapshot when the request names one,
// whose bitmaps the request is about, and returns the URL of its bitmaps.
// The bitmaps of an instance are served under the internal API, for debugging and testing.
// When the returned response is non-nil, the caller should return it immediately, as the request
// was forwarded to the member of the instance or failed.
func instanceBitmapsLoad(s *state.State, r *http.Request) (inst instance.Instance, bitmapsURL *api.URL, resp response.Response) {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return nil, nil, response.SmartError(err)
	}

	projectName := request.ProjectParam(r)
	instName := r.PathValue("name")
	if shared.IsSnapshot(instName) {
		return nil, nil, response.BadRequest(errors.New("Invalid instance name"))
	}

	resp, err = forwardedResponseIfInstanceIsRemote(r.Context(), s, projectName, instName, instanceType)
	if err != nil {
		return nil, nil, response.SmartError(err)
	}

	if resp != nil {
		return nil, nil, resp
	}

	name := instName
	bitmapsURL = api.NewURL().Path("internal", "instances", instName, "bitmaps").Project(projectName)

	snapshotName := r.PathValue("snapshotName")
	if snapshotName != "" {
		name = instName + shared.SnapshotDelimiter + snapshotName
		bitmapsURL = api.NewURL().Path(version.APIVersion, "instances", instName, "snapshots", snapshotName, "bitmaps").Project(projectName)
	}

	inst, err = instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return nil, nil, response.SmartError(err)
	}

	return inst, bitmapsURL, nil
}

// swagger:operation GET /1.0/instances/{name}/snapshots/{snapshotName}/bitmaps instances instance_snapshot_bitmaps_get
//
//	Get the bitmaps
//
//	Returns a list of bitmaps of the instance snapshot (URLs).
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
//	              "/1.0/instances/foo/snapshots/snap1/bitmaps/snap0"
//	            ]
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/instances/{name}/snapshots/{snapshotName}/bitmaps?recursion=1 instances instance_snapshot_bitmaps_get_recursion1
//
//	Get the bitmaps
//
//	Returns a list of bitmaps of the instance snapshot (structs), grouped by name with one entry per volume.
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
//	          description: List of bitmaps
//	          items:
//	            $ref: "#/definitions/InstanceBitmap"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceBitmapsGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	inst, bitmapsURL, resp := instanceBitmapsLoad(s, r)
	if resp != nil {
		return resp
	}

	bitmaps, err := inst.Bitmaps()
	if err != nil {
		return response.SmartError(err)
	}

	recursion, _ := util.IsRecursionRequest(r)
	if recursion > 0 {
		return response.SyncResponse(true, bitmaps)
	}

	urls := make([]string, 0, len(bitmaps))
	for _, bitmap := range bitmaps {
		urls = append(urls, bitmapsURL.Path(bitmap.Name).String())
	}

	return response.SyncResponse(true, urls)
}

// swagger:operation GET /1.0/instances/{name}/snapshots/{snapshotName}/bitmaps/{bitmapName} instances instance_snapshot_bitmap_get
//
//	Get the bitmap
//
//	Gets a specific bitmap of the instance snapshot.
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
//	responses:
//	  "200":
//	    description: Bitmap
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
//	          $ref: "#/definitions/InstanceBitmap"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceBitmapGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	inst, _, resp := instanceBitmapsLoad(s, r)
	if resp != nil {
		return resp
	}

	bitmapName := r.PathValue("bitmapName")

	bitmaps, err := inst.Bitmaps()
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
