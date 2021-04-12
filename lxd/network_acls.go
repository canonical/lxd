package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/cluster/request"
	"github.com/lxc/lxd/lxd/network/acl"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
)

var networkACLsCmd = APIEndpoint{
	Path: "network-acls",

	Get:  APIEndpointAction{Handler: networkACLsGet, AccessHandler: allowProjectPermission("networks", "view")},
	Post: APIEndpointAction{Handler: networkACLsPost, AccessHandler: allowProjectPermission("networks", "manage-networks")},
}

var networkACLCmd = APIEndpoint{
	Path: "network-acls/{name}",

	Delete: APIEndpointAction{Handler: networkACLDelete, AccessHandler: allowProjectPermission("networks", "manage-networks")},
	Get:    APIEndpointAction{Handler: networkACLGet, AccessHandler: allowProjectPermission("networks", "view")},
	Put:    APIEndpointAction{Handler: networkACLPut, AccessHandler: allowProjectPermission("networks", "manage-networks")},
	Patch:  APIEndpointAction{Handler: networkACLPut, AccessHandler: allowProjectPermission("networks", "manage-networks")},
	Post:   APIEndpointAction{Handler: networkACLPost, AccessHandler: allowProjectPermission("networks", "manage-networks")},
}

// API endpoints.

// swagger:operation GET /1.0/network-acls network-acls network_acls_get
//
// Get the network ACLs
//
// Returns a list of network ACLs (URLs).
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
//           type: int
//           description: Status code
//           example: 200
//         metadata:
//           type: array
//           description: List of endpoints
//           items:
//             type: string
//           example: |-
//             [
//               "/1.0/network-acls/foo",
//               "/1.0/network-acls/bar"
//             ]
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/network-acls?recursion=1 network-acls network_acls_get_recursion1
//
// Get the network ACLs
//
// Returns a list of network ACLs (structs).
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
//           type: int
//           description: Status code
//           example: 200
//         metadata:
//           type: array
//           description: List of network ACLs
//           items:
//             $ref: "#/definitions/NetworkACL"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func networkACLsGet(d *Daemon, r *http.Request) response.Response {
	projectName, _, err := project.NetworkProject(d.State().Cluster, projectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	recursion := util.IsRecursionRequest(r)

	// Get list of Network ACLs.
	aclNames, err := d.cluster.GetNetworkACLs(projectName)
	if err != nil {
		return response.InternalError(err)
	}

	resultString := []string{}
	resultMap := []api.NetworkACL{}
	for _, aclName := range aclNames {
		if !recursion {
			resultString = append(resultString, fmt.Sprintf("/%s/network-acls/%s", version.APIVersion, aclName))
		} else {
			netACL, err := acl.LoadByName(d.State(), projectName, aclName)
			if err != nil {
				continue
			}

			netACLInfo := netACL.Info()
			netACLInfo.UsedBy, _ = netACL.UsedBy() // Ignore errors in UsedBy, will return nil.

			resultMap = append(resultMap, *netACLInfo)
		}
	}

	if !recursion {
		return response.SyncResponse(true, resultString)
	}

	return response.SyncResponse(true, resultMap)
}

// swagger:operation POST /1.0/network-acls network-acls network_acls_post
//
// Add a network ACL
//
// Creates a new network ACL.
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
//   - in: body
//     name: acl
//     description: ACL
//     required: true
//     schema:
//       $ref: "#/definitions/NetworkACLsPost"
// responses:
//   "200":
//     $ref: "#/responses/EmptySyncResponse"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func networkACLsPost(d *Daemon, r *http.Request) response.Response {
	projectName, _, err := project.NetworkProject(d.State().Cluster, projectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	req := api.NetworkACLsPost{}

	// Parse the request into a record.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	_, err = acl.LoadByName(d.State(), projectName, req.Name)
	if err == nil {
		return response.BadRequest(fmt.Errorf("The network ACL already exists"))
	}

	err = acl.Create(d.State(), projectName, &req)
	if err != nil {
		return response.SmartError(err)
	}

	url := fmt.Sprintf("/%s/network-acls/%s", version.APIVersion, req.Name)
	return response.SyncResponseLocation(true, nil, url)
}

// swagger:operation DELETE /1.0/network-acls/{name} network-acls network_acls_delete
//
// Delete the network ACL
//
// Removes the network ACL.
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
// responses:
//   "200":
//     $ref: "#/responses/EmptySyncResponse"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func networkACLDelete(d *Daemon, r *http.Request) response.Response {
	projectName, _, err := project.NetworkProject(d.State().Cluster, projectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	netACL, err := acl.LoadByName(d.State(), projectName, mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	err = netACL.Delete()
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

// swagger:operation GET /1.0/network-acls/{name} network-acls network_acls_get
//
// Get the network ACL
//
// Gets a specific network ACL.
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
// responses:
//   "200":
//     description: ACL
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
//           type: int
//           description: Status code
//           example: 200
//         metadata:
//           $ref: "#/definitions/NetworkACL"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func networkACLGet(d *Daemon, r *http.Request) response.Response {
	projectName, _, err := project.NetworkProject(d.State().Cluster, projectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	netACL, err := acl.LoadByName(d.State(), projectName, mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	info := netACL.Info()
	info.UsedBy, err = netACL.UsedBy()
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponseETag(true, info, netACL.Etag())
}

// swagger:operation PATCH /1.0/network-acls/{name} network-acls network_acls_patch
//
// Partially update the network ACL
//
// Updates a subset of the network ACL configuration.
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
//   - in: body
//     name: acl
//     description: ACL configuration
//     required: true
//     schema:
//       $ref: "#/definitions/NetworkACLPut"
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

// swagger:operation PUT /1.0/network-acls/{name} network-acls network_acls_put
//
// Update the network ACL
//
// Updates the entire network ACL configuration.
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
//   - in: body
//     name: acl
//     description: ACL configuration
//     required: true
//     schema:
//       $ref: "#/definitions/NetworkACLPut"
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
func networkACLPut(d *Daemon, r *http.Request) response.Response {
	projectName, _, err := project.NetworkProject(d.State().Cluster, projectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	// Get the existing Network ACL.
	netACL, err := acl.LoadByName(d.State(), projectName, mux.Vars(r)["name"])
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

	clientType := request.UserAgentClientType(r.Header.Get("User-Agent"))

	err = netACL.Update(&req, clientType)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

// swagger:operation POST /1.0/network-acls/{name} network-acls network_acls_post
//
// Rename the network ACL
//
// Renames an existing network ACL.
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
//   - in: body
//     name: acl
//     description: ACL rename request
//     required: true
//     schema:
//       $ref: "#/definitions/NetworkACLPost"
// responses:
//   "200":
//     $ref: "#/responses/EmptySyncResponse"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func networkACLPost(d *Daemon, r *http.Request) response.Response {
	projectName, _, err := project.NetworkProject(d.State().Cluster, projectParam(r))
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
	netACL, err := acl.LoadByName(d.State(), projectName, mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	err = netACL.Rename(req.Name)
	if err != nil {
		return response.SmartError(err)
	}

	url := fmt.Sprintf("/%s/network-acls/%s", version.APIVersion, req.Name)
	return response.SyncResponseLocation(true, nil, url)
}
