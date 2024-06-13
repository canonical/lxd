package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/lxd/auth"
	clusterRequest "github.com/canonical/lxd/lxd/cluster/request"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/network/zone"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/version"
)

var networkZonesCmd = APIEndpoint{
	Path: "network-zones",

	Get:  APIEndpointAction{Handler: networkZonesGet, AccessHandler: allowAuthenticated},
	Post: APIEndpointAction{Handler: networkZonesPost, AccessHandler: allowPermission(entity.TypeProject, auth.EntitlementCanCreateNetworkZones)},
}

var networkZoneCmd = APIEndpoint{
	Path: "network-zones/{zone}",

	Delete: APIEndpointAction{Handler: networkZoneDelete, AccessHandler: allowPermission(entity.TypeNetworkZone, auth.EntitlementCanDelete, "zone")},
	Get:    APIEndpointAction{Handler: networkZoneGet, AccessHandler: allowPermission(entity.TypeNetworkZone, auth.EntitlementCanView, "zone")},
	Put:    APIEndpointAction{Handler: networkZonePut, AccessHandler: allowPermission(entity.TypeNetworkZone, auth.EntitlementCanEdit, "zone")},
	Patch:  APIEndpointAction{Handler: networkZonePut, AccessHandler: allowPermission(entity.TypeNetworkZone, auth.EntitlementCanEdit, "zone")},
}

// API endpoints.

// swagger:operation GET /1.0/network-zones network-zones network_zones_get
//
//  Get the network zones
//
//  Returns a list of network zones (URLs).
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
//                "/1.0/network-zones/example.net",
//                "/1.0/network-zones/example.com"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/network-zones?recursion=1 network-zones network_zones_get_recursion1
//
//	Get the network zones
//
//	Returns a list of network zones (structs).
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
//	          description: List of network zones
//	          items:
//	            $ref: "#/definitions/NetworkZone"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkZonesGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, _, err := project.NetworkZoneProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	recursion := util.IsRecursionRequest(r)

	var zoneNames []string

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get list of Network zones.
		zoneNames, err = tx.GetNetworkZonesByProject(ctx, projectName)

		return err
	})
	if err != nil {
		return response.InternalError(err)
	}

	request.SetCtxValue(r, request.CtxEffectiveProjectName, projectName)
	userHasPermission, err := s.Authorizer.GetPermissionChecker(r.Context(), r, auth.EntitlementCanView, entity.TypeNetworkZone)
	if err != nil {
		return response.InternalError(err)
	}

	resultString := []string{}
	resultMap := []api.NetworkZone{}
	for _, zoneName := range zoneNames {
		if !userHasPermission(entity.TypeNetworkZone.URL(projectName, zoneName)) {
			continue
		}

		if !recursion {
			resultString = append(resultString, api.NewURL().Path(version.APIVersion, "network-zones", zoneName).String())
		} else {
			netzone, err := zone.LoadByNameAndProject(s, projectName, zoneName)
			if err != nil {
				continue
			}

			netzoneInfo := netzone.Info()
			netzoneInfo.UsedBy, _ = netzone.UsedBy() // Ignore errors in UsedBy, will return nil.
			netzoneInfo.UsedBy = project.FilterUsedBy(s.Authorizer, r, netzoneInfo.UsedBy)

			resultMap = append(resultMap, *netzoneInfo)
		}
	}

	if !recursion {
		return response.SyncResponse(true, resultString)
	}

	return response.SyncResponse(true, resultMap)
}

// swagger:operation POST /1.0/network-zones network-zones network_zones_post
//
//	Add a network zone
//
//	Creates a new network zone.
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
//	  - in: body
//	    name: zone
//	    description: zone
//	    required: true
//	    schema:
//	      $ref: "#/definitions/NetworkZonesPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkZonesPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, _, err := project.NetworkZoneProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	req := api.NetworkZonesPost{}

	// Parse the request into a record.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Create the zone.
	err = zone.Exists(s, req.Name)
	if err == nil {
		return response.BadRequest(fmt.Errorf("The network zone already exists"))
	}

	err = zone.Create(s, projectName, &req)
	if err != nil {
		return response.SmartError(err)
	}

	netzone, err := zone.LoadByNameAndProject(s, projectName, req.Name)
	if err != nil {
		return response.BadRequest(err)
	}

	lc := lifecycle.NetworkZoneCreated.Event(netzone, request.CreateRequestor(r), nil)
	s.Events.SendLifecycle(projectName, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// swagger:operation DELETE /1.0/network-zones/{zone} network-zones network_zone_delete
//
//	Delete the network zone
//
//	Removes the network zone.
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
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkZoneDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, _, err := project.NetworkZoneProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	zoneName, err := url.PathUnescape(mux.Vars(r)["zone"])
	if err != nil {
		return response.SmartError(err)
	}

	netzone, err := zone.LoadByNameAndProject(s, projectName, zoneName)
	if err != nil {
		return response.SmartError(err)
	}

	err = netzone.Delete()
	if err != nil {
		return response.SmartError(err)
	}

	s.Events.SendLifecycle(projectName, lifecycle.NetworkZoneDeleted.Event(netzone, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}

// swagger:operation GET /1.0/network-zones/{zone} network-zones network_zone_get
//
//	Get the network zone
//
//	Gets a specific network zone.
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
//	    description: zone
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
//	          $ref: "#/definitions/NetworkZone"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkZoneGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, _, err := project.NetworkZoneProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	zoneName, err := url.PathUnescape(mux.Vars(r)["zone"])
	if err != nil {
		return response.SmartError(err)
	}

	netzone, err := zone.LoadByNameAndProject(s, projectName, zoneName)
	if err != nil {
		return response.SmartError(err)
	}

	info := netzone.Info()
	info.UsedBy, err = netzone.UsedBy()
	if err != nil {
		return response.SmartError(err)
	}

	info.UsedBy = project.FilterUsedBy(s.Authorizer, r, info.UsedBy)

	return response.SyncResponseETag(true, info, netzone.Etag())
}

// swagger:operation PATCH /1.0/network-zones/{zone} network-zones network_zone_patch
//
//  Partially update the network zone
//
//  Updates a subset of the network zone configuration.
//
//  ---
//  consumes:
//    - application/json
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
//    - in: body
//      name: zone
//      description: zone configuration
//      required: true
//      schema:
//        $ref: "#/definitions/NetworkZonePut"
//  responses:
//    "200":
//      $ref: "#/responses/EmptySyncResponse"
//    "400":
//      $ref: "#/responses/BadRequest"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "412":
//      $ref: "#/responses/PreconditionFailed"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation PUT /1.0/network-zones/{zone} network-zones network_zone_put
//
//	Update the network zone
//
//	Updates the entire network zone configuration.
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
//	  - in: body
//	    name: zone
//	    description: zone configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/NetworkZonePut"
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
func networkZonePut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, _, err := project.NetworkZoneProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	zoneName, err := url.PathUnescape(mux.Vars(r)["zone"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the existing Network zone.
	netzone, err := zone.LoadByNameAndProject(s, projectName, zoneName)
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag.
	err = util.EtagCheck(r, netzone.Etag())
	if err != nil {
		return response.PreconditionFailed(err)
	}

	req := api.NetworkZonePut{}

	// Decode the request.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if r.Method == http.MethodPatch {
		// If config being updated via "patch" method, then merge all existing config with the keys that
		// are present in the request config.
		for k, v := range netzone.Info().Config {
			_, ok := req.Config[k]
			if !ok {
				req.Config[k] = v
			}
		}
	}

	clientType := clusterRequest.UserAgentClientType(r.Header.Get("User-Agent"))

	err = netzone.Update(&req, clientType)
	if err != nil {
		return response.SmartError(err)
	}

	s.Events.SendLifecycle(projectName, lifecycle.NetworkZoneUpdated.Event(netzone, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}
