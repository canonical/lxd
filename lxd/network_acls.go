package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/lxd/auth"
	clusterRequest "github.com/canonical/lxd/lxd/cluster/request"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/network/acl"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/version"
)

var networkACLsCmd = APIEndpoint{
	Path:        "network-acls",
	MetricsType: entity.TypeNetwork,

	Get:  APIEndpointAction{Handler: networkACLsGet, AccessHandler: allowProjectResourceList},
	Post: APIEndpointAction{Handler: networkACLsPost, AccessHandler: allowPermission(entity.TypeProject, auth.EntitlementCanCreateNetworkACLs)},
}

var networkACLCmd = APIEndpoint{
	Path:        "network-acls/{name}",
	MetricsType: entity.TypeNetwork,

	Delete: APIEndpointAction{Handler: networkACLDelete, AccessHandler: allowPermission(entity.TypeNetworkACL, auth.EntitlementCanDelete, "name")},
	Get:    APIEndpointAction{Handler: networkACLGet, AccessHandler: allowPermission(entity.TypeNetworkACL, auth.EntitlementCanView, "name")},
	Put:    APIEndpointAction{Handler: networkACLPut, AccessHandler: allowPermission(entity.TypeNetworkACL, auth.EntitlementCanEdit, "name")},
	Patch:  APIEndpointAction{Handler: networkACLPut, AccessHandler: allowPermission(entity.TypeNetworkACL, auth.EntitlementCanEdit, "name")},
	Post:   APIEndpointAction{Handler: networkACLPost, AccessHandler: allowPermission(entity.TypeNetworkACL, auth.EntitlementCanEdit, "name")},
}

var networkACLLogCmd = APIEndpoint{
	Path:        "network-acls/{name}/log",
	MetricsType: entity.TypeNetwork,

	Get: APIEndpointAction{Handler: networkACLLogGet, AccessHandler: allowPermission(entity.TypeNetworkACL, auth.EntitlementCanView, "name")},
}

// API endpoints.

// swagger:operation GET /1.0/network-acls network-acls network_acls_get
//
//  Get the network ACLs
//
//  Returns a list of network ACLs (URLs).
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
//                "/1.0/network-acls/foo",
//                "/1.0/network-acls/bar"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/network-acls?recursion=1 network-acls network_acls_get_recursion1
//
//	Get the network ACLs
//
//	Returns a list of network ACLs (structs).
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
//	          description: List of network ACLs
//	          items:
//	            $ref: "#/definitions/NetworkACL"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkACLsGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	requestProjectName := request.ProjectParam(r)
	effectiveProjectName, _, err := project.NetworkProject(s.DB.Cluster, requestProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	recursion := util.IsRecursionRequest(r)
	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeNetworkACL, true)
	if err != nil {
		return response.SmartError(err)
	}

	var aclNames []string

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		// Get list of Network ACLs.
		aclNames, err = tx.GetNetworkACLs(ctx, effectiveProjectName)

		return err
	})
	if err != nil {
		return response.InternalError(err)
	}

	request.SetCtxValue(r, request.CtxEffectiveProjectName, effectiveProjectName)
	userHasPermission, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeNetworkACL)
	if err != nil {
		return response.SmartError(err)
	}

	resultString := []string{}
	resultMap := []*api.NetworkACL{}
	urlToNetworkACL := make(map[*api.URL]auth.EntitlementReporter)
	for _, aclName := range aclNames {
		if !userHasPermission(entity.NetworkACLURL(requestProjectName, aclName)) {
			continue
		}

		if !recursion {
			resultString = append(resultString, fmt.Sprintf("/%s/network-acls/%s", version.APIVersion, aclName))
		} else {
			netACL, err := acl.LoadByName(s, effectiveProjectName, aclName)
			if err != nil {
				continue
			}

			netACLInfo := netACL.Info()
			netACLInfo.UsedBy, _ = netACL.UsedBy() // Ignore errors in UsedBy, will return nil.
			netACLInfo.UsedBy = project.FilterUsedBy(s.Authorizer, r, netACLInfo.UsedBy)

			resultMap = append(resultMap, netACLInfo)
			urlToNetworkACL[entity.NetworkACLURL(requestProjectName, aclName)] = netACLInfo
		}
	}

	if !recursion {
		return response.SyncResponse(true, resultString)
	}

	if len(withEntitlements) > 0 {
		err = reportEntitlements(r.Context(), s.Authorizer, s.IdentityCache, entity.TypeNetworkACL, withEntitlements, urlToNetworkACL)
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.SyncResponse(true, resultMap)
}

// swagger:operation POST /1.0/network-acls network-acls network_acls_post
//
//	Add a network ACL
//
//	Creates a new network ACL.
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
//	    name: acl
//	    description: ACL
//	    required: true
//	    schema:
//	      $ref: "#/definitions/NetworkACLsPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkACLsPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, _, err := project.NetworkProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	req := api.NetworkACLsPost{}

	// Parse the request into a record.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	_, err = acl.LoadByName(s, projectName, req.Name)
	if err == nil {
		return response.BadRequest(fmt.Errorf("The network ACL already exists"))
	}

	err = acl.Create(s, projectName, &req)
	if err != nil {
		return response.SmartError(err)
	}

	netACL, err := acl.LoadByName(s, projectName, req.Name)
	if err != nil {
		return response.BadRequest(err)
	}

	lc := lifecycle.NetworkACLCreated.Event(netACL, request.CreateRequestor(r), nil)
	s.Events.SendLifecycle(projectName, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// swagger:operation DELETE /1.0/network-acls/{name} network-acls network_acl_delete
//
//	Delete the network ACL
//
//	Removes the network ACL.
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
func networkACLDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, _, err := project.NetworkProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	aclName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	netACL, err := acl.LoadByName(s, projectName, aclName)
	if err != nil {
		return response.SmartError(err)
	}

	err = netACL.Delete()
	if err != nil {
		return response.SmartError(err)
	}

	s.Events.SendLifecycle(projectName, lifecycle.NetworkACLDeleted.Event(netACL, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}

// swagger:operation GET /1.0/network-acls/{name} network-acls network_acl_get
//
//	Get the network ACL
//
//	Gets a specific network ACL.
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
//	    description: ACL
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
//	          $ref: "#/definitions/NetworkACL"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkACLGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, _, err := project.NetworkProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	aclName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeNetworkACL, false)
	if err != nil {
		return response.SmartError(err)
	}

	netACL, err := acl.LoadByName(s, projectName, aclName)
	if err != nil {
		return response.SmartError(err)
	}

	info := netACL.Info()
	info.UsedBy, err = netACL.UsedBy()
	if err != nil {
		return response.SmartError(err)
	}

	info.UsedBy = project.FilterUsedBy(s.Authorizer, r, info.UsedBy)
	if len(withEntitlements) > 0 {
		err = reportEntitlements(r.Context(), s.Authorizer, s.IdentityCache, entity.TypeNetworkACL, withEntitlements, map[*api.URL]auth.EntitlementReporter{entity.NetworkACLURL(projectName, aclName): info})
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.SyncResponseETag(true, info, netACL.Etag())
}

// swagger:operation PATCH /1.0/network-acls/{name} network-acls network_acl_patch
//
//  Partially update the network ACL
//
//  Updates a subset of the network ACL configuration.
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
//      name: acl
//      description: ACL configuration
//      required: true
//      schema:
//        $ref: "#/definitions/NetworkACLPut"
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

// swagger:operation PUT /1.0/network-acls/{name} network-acls network_acl_put
//
//	Update the network ACL
//
//	Updates the entire network ACL configuration.
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
//	    name: acl
//	    description: ACL configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/NetworkACLPut"
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
func networkACLPut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, _, err := project.NetworkProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	aclName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the existing Network ACL.
	netACL, err := acl.LoadByName(s, projectName, aclName)
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag.
	err = util.EtagCheck(r, netACL.Etag())
	if err != nil {
		return response.PreconditionFailed(err)
	}

	req := api.NetworkACLPut{}

	// Decode the request.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if r.Method == http.MethodPatch {
		// If config being updated via "patch" method, then merge all existing config with the keys that
		// are present in the request config.
		for k, v := range netACL.Info().Config {
			_, ok := req.Config[k]
			if !ok {
				req.Config[k] = v
			}
		}
	}

	clientType := clusterRequest.UserAgentClientType(r.Header.Get("User-Agent"))

	err = netACL.Update(&req, clientType)
	if err != nil {
		return response.SmartError(err)
	}

	s.Events.SendLifecycle(projectName, lifecycle.NetworkACLUpdated.Event(netACL, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}

// swagger:operation POST /1.0/network-acls/{name} network-acls network_acl_post
//
//	Rename the network ACL
//
//	Renames an existing network ACL.
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
//	    name: acl
//	    description: ACL rename request
//	    required: true
//	    schema:
//	      $ref: "#/definitions/NetworkACLPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkACLPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	aclName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	projectName, _, err := project.NetworkProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	req := api.NetworkACLPost{}

	// Parse the request.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Get the existing Network ACL.
	netACL, err := acl.LoadByName(s, projectName, aclName)
	if err != nil {
		return response.SmartError(err)
	}

	err = netACL.Rename(req.Name)
	if err != nil {
		return response.SmartError(err)
	}

	lc := lifecycle.NetworkACLRenamed.Event(netACL, request.CreateRequestor(r), logger.Ctx{"old_name": aclName})
	s.Events.SendLifecycle(projectName, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// swagger:operation GET /1.0/network-acls/{name}/log network-acls network_acl_log_get
//
//	Get the network ACL log
//
//	Gets a specific network ACL log entries.
//
//	---
//	produces:
//	  - application/octet-stream
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "200":
//	     description: Raw log file
//	     content:
//	       application/octet-stream:
//	         schema:
//	           type: string
//	           example: LOG-ENTRY
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkACLLogGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, _, err := project.NetworkProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	aclName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	netACL, err := acl.LoadByName(s, projectName, aclName)
	if err != nil {
		return response.SmartError(err)
	}

	clientType := clusterRequest.UserAgentClientType(r.Header.Get("User-Agent"))
	log, err := netACL.GetLog(r.Context(), clientType)
	if err != nil {
		return response.SmartError(err)
	}

	ent := response.FileResponseEntry{}
	ent.File = bytes.NewReader([]byte(log))
	ent.FileModified = time.Now()
	ent.FileSize = int64(len(log))

	return response.FileResponse([]response.FileResponseEntry{ent}, nil)
}
