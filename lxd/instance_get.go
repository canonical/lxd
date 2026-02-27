package main

import (
	"errors"
	"net"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

// swagger:operation GET /1.0/instances/{name} instances instance_get
//
//  Get the instance
//
//  Gets a specific instance (basic struct).
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
//  responses:
//    "200":
//      description: Instance
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
//            $ref: "#/definitions/Instance"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/instances/{name}?recursion=1 instances instance_get_recursion1
//
//	Get the instance
//
//	Gets a specific instance (full struct).
//
//	recursion=1 also includes information about state, snapshots and backups.
//
//	Selective recursion is supported using semicolon-separated syntax in the recursion parameter:
//	recursion=2;fields=state.disk,state.network to fetch only specific state fields.
//	Valid fields are: state.disk, state.network.
//	Use recursion=2;fields= to fetch instance data without any expensive state fields.
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
//	    name: recursion
//	    description: Recursion level (0, 1, 2) or selective recursion (2;fields=state.disk,state.network)
//	    type: string
//	    example: 2;fields=state.disk
//	responses:
//	  "200":
//	    description: Instance
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
//	          $ref: "#/definitions/Instance"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := request.ProjectParam(r)
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if shared.IsSnapshot(name) {
		return response.BadRequest(errors.New("Invalid instance name"))
	}

	recursion, fields := util.IsRecursionRequest(r)

	stateOpts, err := instance.ParseRecursionFields(fields)
	if err != nil {
		return response.BadRequest(err)
	}

	recursive := recursion > 0

	// Detect if we want to also return entitlements for each instance.
	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeInstance, false)
	if err != nil {
		return response.SmartError(err)
	}

	// Handle requests targeted to a container on a different node
	resp, err := forwardedResponseIfInstanceIsRemote(r.Context(), s, projectName, name, instanceType)

	// If the instance's node is not reachable and the request is not recursive, proceed getting
	// the instance info from the database.
	// The instance state will show as Error since we can't determine the state of an instance on another node.
	// If request is recursive, the additional information on instance state will be out of reach since
	// we can't reach the node that is running the instance.
	if err != nil && (!api.StatusErrorCheck(err, http.StatusServiceUnavailable) || recursive) {
		return response.SmartError(err)
	}

	if resp != nil {
		return resp
	}

	c, err := instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	var state any
	var etag any
	if !recursive {
		state, etag, err = c.Render()
	} else {
		hostInterfaces, _ := net.Interfaces()
		state, etag, err = c.RenderFull(hostInterfaces, stateOpts)
	}

	if err != nil {
		return response.SmartError(err)
	}

	if len(withEntitlements) > 0 {
		err = reportEntitlements(r.Context(), s.Authorizer, entity.TypeInstance, withEntitlements, map[*api.URL]auth.EntitlementReporter{entity.InstanceURL(c.Project().Name, c.Name()): state.(auth.EntitlementReporter)})
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.SyncResponseETag(true, state, etag)
}
