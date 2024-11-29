package main

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
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
		return response.BadRequest(fmt.Errorf("Invalid instance name"))
	}

	// Parse the recursion field
	recursionStr := r.FormValue("recursion")

	recursion, err := strconv.Atoi(recursionStr)
	if err != nil {
		recursion = 0
	}

	// Detect if we want to also returns entitlements for each instance.
	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeInstance)
	if err != nil {
		return response.SmartError(err)
	}

	// Handle requests targeted to a container on a different node
	resp, err := forwardedResponseIfInstanceIsRemote(s, r, projectName, name, instanceType)
	if err != nil {
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
	if recursion == 0 {
		state, etag, err = c.Render()
	} else {
		hostInterfaces, _ := net.Interfaces()
		state, etag, err = c.RenderFull(hostInterfaces)
	}

	if err != nil {
		return response.SmartError(err)
	}

	if len(withEntitlements) > 0 {
		if recursion == 0 {
			err = d.authorizer.AddEntitlements(r.Context(), &entity.EntityWithEntitlementsAndURL{Entity: state.(*api.Instance), EntityType: entity.TypeProject, EntityURL: entity.InstanceURL(c.Project().Name, c.Name())}, withEntitlements)
			if err != nil {
				return response.SmartError(err)
			}
		} else {
			err = d.authorizer.AddEntitlements(r.Context(), &entity.EntityWithEntitlementsAndURL{Entity: state.(*api.InstanceFull), EntityType: entity.TypeProject, EntityURL: entity.InstanceURL(c.Project().Name, c.Name())}, withEntitlements)
			if err != nil {
				return response.SmartError(err)
			}
		}
	}

	return response.SyncResponseETag(true, state, etag)
}
