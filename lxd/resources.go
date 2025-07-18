package main

import (
	"net/http"
	"net/url"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/resources"
	"github.com/canonical/lxd/lxd/response"
	storagePools "github.com/canonical/lxd/lxd/storage"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

var api10ResourcesCmd = APIEndpoint{
	Path:        "resources",
	MetricsType: entity.TypeServer,

	Get: APIEndpointAction{Handler: api10ResourcesGet, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanViewResources)},
}

var storagePoolResourcesCmd = APIEndpoint{
	Path:        "storage-pools/{name}/resources",
	MetricsType: entity.TypeStoragePool,

	Get: APIEndpointAction{Handler: storagePoolResourcesGet, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanViewResources)},
}

// swagger:operation GET /1.0/resources server resources_get
//
//	Get system resources information
//
//	Gets the hardware information profile of the LXD server.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: target
//	    description: Cluster member name
//	    type: string
//	    example: lxd01
//	responses:
//	  "200":
//	    description: Hardware resources
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
//	          $ref: "#/definitions/Resources"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func api10ResourcesGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// If a target was specified, forward the request to the relevant node.
	target := request.QueryParam(r, "target")
	resp := forwardedResponseToNode(r.Context(), s, target)
	if resp != nil {
		return resp
	}

	// Get the local resource usage
	res, err := resources.GetResources()
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, res)
}

// swagger:operation GET /1.0/storage-pools/{name}/resources storage storage_pool_resources
//
//	Get storage pool resources information
//
//	Gets the usage information for the storage pool.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: target
//	    description: Cluster member name
//	    type: string
//	    example: lxd01
//	responses:
//	  "200":
//	    description: Hardware resources
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
//	          $ref: "#/definitions/ResourcesStoragePool"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolResourcesGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// If a target was specified, forward the request to the relevant node.
	target := request.QueryParam(r, "target")
	resp := forwardedResponseToNode(r.Context(), s, target)
	if resp != nil {
		return resp
	}

	// Get the existing storage pool
	poolName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	var res *api.ResourcesStoragePool

	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.InternalError(err)
	}

	res, err = pool.GetResources()
	if err != nil {
		return response.InternalError(err)
	}

	return response.SyncResponse(true, res)
}
