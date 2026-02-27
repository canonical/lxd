package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/network/zone"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/version"
)

var networkZonesCmd = APIEndpoint{
	Path:        "network-zones",
	MetricsType: entity.TypeNetwork,

	Get:  APIEndpointAction{Handler: networkZonesGet, AccessHandler: allowProjectResourceList(false)},
	Post: APIEndpointAction{Handler: networkZonesPost, AccessHandler: allowPermission(entity.TypeProject, auth.EntitlementCanCreateNetworkZones)},
}

var networkZoneCmd = APIEndpoint{
	Path:        "network-zones/{zone}",
	MetricsType: entity.TypeNetwork,

	Delete: APIEndpointAction{Handler: networkZoneDelete, AccessHandler: networkZoneAccessHandler(auth.EntitlementCanDelete)},
	Get:    APIEndpointAction{Handler: networkZoneGet, AccessHandler: networkZoneAccessHandler(auth.EntitlementCanView)},
	Put:    APIEndpointAction{Handler: networkZonePut, AccessHandler: networkZoneAccessHandler(auth.EntitlementCanEdit)},
	Patch:  APIEndpointAction{Handler: networkZonePut, AccessHandler: networkZoneAccessHandler(auth.EntitlementCanEdit)},
}

// ctxNetworkZoneDetails should be used only for getting/setting networkZoneDetails in the request context.
const ctxNetworkZoneDetails request.CtxKey = "network-zone-details"

// networkZoneDetails contains fields that are determined prior to the access check. This is set in the request context when
// addNetworkZoneDetailsToRequestContext is called.
type networkZoneDetails struct {
	zoneName       string
	requestProject api.Project
}

// addNetworkZoneDetailsToRequestContext sets the effective project in the request.Info and sets ctxNetworkZoneDetails (networkZoneDetails)
// in the request context.
func addNetworkZoneDetailsToRequestContext(s *state.State, r *http.Request) error {
	zoneName, err := url.PathUnescape(mux.Vars(r)["zone"])
	if err != nil {
		return err
	}

	requestProjectName := request.ProjectParam(r)
	effectiveProjectName, requestProject, err := project.NetworkZoneProject(s.DB.Cluster, requestProjectName)
	if err != nil {
		return fmt.Errorf("Failed to check project %q network feature: %w", requestProjectName, err)
	}

	request.SetContextValue(r, request.CtxEffectiveProjectName, effectiveProjectName)
	request.SetContextValue(r, ctxNetworkZoneDetails, networkZoneDetails{
		zoneName:       zoneName,
		requestProject: *requestProject,
	})

	return nil
}

// profileAccessHandler calls addNetworkZoneDetailsToRequestContext, then uses the details to perform an access check with
// the given auth.Entitlement.
func networkZoneAccessHandler(entitlement auth.Entitlement) func(d *Daemon, r *http.Request) response.Response {
	return func(d *Daemon, r *http.Request) response.Response {
		s := d.State()
		err := addNetworkZoneDetailsToRequestContext(s, r)
		if err != nil {
			return response.SmartError(err)
		}

		details, err := request.GetContextValue[networkZoneDetails](r.Context(), ctxNetworkZoneDetails)
		if err != nil {
			return response.SmartError(err)
		}

		err = s.Authorizer.CheckPermission(r.Context(), entity.NetworkZoneURL(details.requestProject.Name, details.zoneName), entitlement)
		if err != nil {
			return response.SmartError(err)
		}

		return response.EmptySyncResponse
	}
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
//    - in: query
//      name: all-projects
//      description: Retrieve network zones from all projects
//      type: boolean
//      example: true
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
//	  - in: query
//	    name: all-projects
//	    description: Retrieve network zones from all projects
//	    type: boolean
//	    example: true
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

	requestProjectName, allProjects, err := request.ProjectParams(r)
	if err != nil {
		return response.SmartError(err)
	}

	var effectiveProjectName string
	if !allProjects {
		// Project specific requests require an effective project, when "features.networks.zones" is enabled this is the requested project, otherwise it is the default project.
		effectiveProjectName, _, err = project.NetworkZoneProject(s.DB.Cluster, requestProjectName)
		if err != nil {
			return response.SmartError(err)
		}

		// If the request is project specific, then set effective project name in the request context so that the authorizer can generate the correct URL.
		request.SetContextValue(r, request.CtxEffectiveProjectName, effectiveProjectName)
	}

	recursion, _ := util.IsRecursionRequest(r)
	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeNetworkZone, true)
	if err != nil {
		return response.SmartError(err)
	}

	var zoneNamesMap map[string]string
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		if allProjects {
			zoneNamesMap, err = tx.GetNetworkZones(ctx)
		} else {
			// Get list of Network zones.
			zoneNames, err := tx.GetNetworkZonesByProject(ctx, effectiveProjectName)
			if err != nil {
				return err
			}

			// Network zones should be mapped to the requested project for project specific requests.
			zoneNamesMap = make(map[string]string, len(zoneNames))
			for _, zoneName := range zoneNames {
				zoneNamesMap[zoneName] = requestProjectName
			}
		}

		return err
	})
	if err != nil {
		return response.InternalError(err)
	}

	userHasPermission, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeNetworkZone)
	if err != nil {
		return response.InternalError(err)
	}

	resultString := []string{}
	resultMap := []*api.NetworkZone{}
	urlToNetworkZone := make(map[*api.URL]auth.EntitlementReporter)
	for zoneName, projectName := range zoneNamesMap {
		// Check permission for each network zone against the requested project.
		if !userHasPermission(entity.NetworkZoneURL(projectName, zoneName)) {
			continue
		}

		if recursion == 0 {
			resultString = append(resultString, api.NewURL().Path(version.APIVersion, "network-zones", zoneName).String())
		} else {
			var netzone zone.NetworkZone
			if !allProjects {
				netzone, err = zone.LoadByNameAndProject(r.Context(), s, effectiveProjectName, zoneName)
			} else {
				netzone, err = zone.LoadByNameAndProject(r.Context(), s, projectName, zoneName)
			}

			if err != nil {
				return response.SmartError(err)
			}

			netzoneInfo := netzone.Info()
			netzoneInfo.UsedBy, _ = netzone.UsedBy(r.Context()) // Ignore errors in UsedBy, will return nil.
			netzoneInfo.UsedBy = project.FilterUsedBy(r.Context(), s.Authorizer, netzoneInfo.UsedBy)
			netzoneInfo.Project = projectName

			resultMap = append(resultMap, netzoneInfo)
			urlToNetworkZone[entity.NetworkZoneURL(projectName, zoneName)] = netzoneInfo
		}
	}

	if recursion == 0 {
		return response.SyncResponse(true, resultString)
	}

	if len(withEntitlements) > 0 {
		err = reportEntitlements(r.Context(), s.Authorizer, entity.TypeNetworkZone, withEntitlements, urlToNetworkZone)
		if err != nil {
			return response.SmartError(err)
		}
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
	err = zone.Exists(r.Context(), s, req.Name)
	if err == nil {
		return response.BadRequest(errors.New("The network zone already exists"))
	}

	err = zone.Create(r.Context(), s, projectName, &req)
	if err != nil {
		return response.SmartError(err)
	}

	netzone, err := zone.LoadByNameAndProject(r.Context(), s, projectName, req.Name)
	if err != nil {
		return response.BadRequest(err)
	}

	lc := lifecycle.NetworkZoneCreated.Event(netzone, request.CreateRequestor(r.Context()), nil)
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

	effectiveProjectName, err := request.GetContextValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	details, err := request.GetContextValue[networkZoneDetails](r.Context(), ctxNetworkZoneDetails)
	if err != nil {
		return response.SmartError(err)
	}

	err = doNetworkZoneDelete(r.Context(), s, details.zoneName, effectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

// doNetworkZoneDelete deletes the named network zone in the given project.
func doNetworkZoneDelete(ctx context.Context, s *state.State, zoneName string, projectName string) error {
	netzone, err := zone.LoadByNameAndProject(ctx, s, projectName, zoneName)
	if err != nil {
		return err
	}

	err = netzone.Delete(ctx)
	if err != nil {
		return fmt.Errorf("Failed deleting network zone %q: %w", zoneName, err)
	}

	s.Events.SendLifecycle(projectName, lifecycle.NetworkZoneDeleted.Event(netzone, request.CreateRequestor(ctx), nil))

	return nil
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

	effectiveProjectName, err := request.GetContextValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	details, err := request.GetContextValue[networkZoneDetails](r.Context(), ctxNetworkZoneDetails)
	if err != nil {
		return response.SmartError(err)
	}

	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeNetworkZone, false)
	if err != nil {
		return response.SmartError(err)
	}

	netzone, err := zone.LoadByNameAndProject(r.Context(), s, effectiveProjectName, details.zoneName)
	if err != nil {
		return response.SmartError(err)
	}

	info := netzone.Info()
	info.UsedBy, err = netzone.UsedBy(r.Context())
	if err != nil {
		return response.SmartError(err)
	}

	info.UsedBy = project.FilterUsedBy(r.Context(), s.Authorizer, info.UsedBy)

	if len(withEntitlements) > 0 {
		err = reportEntitlements(r.Context(), s.Authorizer, entity.TypeNetworkZone, withEntitlements, map[*api.URL]auth.EntitlementReporter{entity.NetworkZoneURL(effectiveProjectName, details.zoneName): info})
		if err != nil {
			return response.SmartError(err)
		}
	}

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

	effectiveProjectName, err := request.GetContextValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	details, err := request.GetContextValue[networkZoneDetails](r.Context(), ctxNetworkZoneDetails)
	if err != nil {
		return response.SmartError(err)
	}

	// Get the existing Network zone.
	netzone, err := zone.LoadByNameAndProject(r.Context(), s, effectiveProjectName, details.zoneName)
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

	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
		return response.SmartError(err)
	}

	err = netzone.Update(&req, requestor.ClientType())
	if err != nil {
		return response.SmartError(err)
	}

	s.Events.SendLifecycle(effectiveProjectName, lifecycle.NetworkZoneUpdated.Event(netzone, requestor.EventLifecycleRequestor(), nil))

	return response.EmptySyncResponse
}
