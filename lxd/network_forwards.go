package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"

	clusterRequest "github.com/lxc/lxd/lxd/cluster/request"
	"github.com/lxc/lxd/lxd/lifecycle"
	"github.com/lxc/lxd/lxd/network"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/request"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
)

var networkForwardsCmd = APIEndpoint{
	Path: "networks/{networkName}/forwards",

	Get:  APIEndpointAction{Handler: networkForwardsGet, AccessHandler: allowProjectPermission("networks", "view")},
	Post: APIEndpointAction{Handler: networkForwardsPost, AccessHandler: allowProjectPermission("networks", "manage-networks")},
}

var networkForwardCmd = APIEndpoint{
	Path: "networks/{networkName}/forwards/{listenAddress}",

	Delete: APIEndpointAction{Handler: networkForwardDelete, AccessHandler: allowProjectPermission("networks", "manage-networks")},
	Get:    APIEndpointAction{Handler: networkForwardGet, AccessHandler: allowProjectPermission("networks", "view")},
	Put:    APIEndpointAction{Handler: networkForwardPut, AccessHandler: allowProjectPermission("networks", "manage-networks")},
	Patch:  APIEndpointAction{Handler: networkForwardPut, AccessHandler: allowProjectPermission("networks", "manage-networks")},
}

// API endpoints

// swagger:operation GET /1.0/networks/{networkName}/forwards network-forwards network_forwards_get
//
// Get the network forwards
//
// Returns a list of network forwards (URLs).
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
//               "/1.0/networks/lxdbr0/forwards/192.0.2.1",
//               "/1.0/networks/lxdbr0/forwards/192.0.2.2"
//             ]
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/networks/{networkName}/forwards?recursion=1 network-forwards network_forward_get_recursion1
//
// Get the networks
//
// Returns a list of network forwards (structs).
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
//           type: integer
//           description: Status code
//           example: 200
//         metadata:
//           type: array
//           description: List of network forwards
//           items:
//             $ref: "#/definitions/NetworkForward"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func networkForwardsGet(d *Daemon, r *http.Request) response.Response {
	projectName, _, err := project.NetworkProject(d.State().Cluster, projectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	n, err := network.LoadByName(d.State(), projectName, mux.Vars(r)["networkName"])
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading network: %w", err))
	}

	if !n.Info().AddressForwards {
		return response.BadRequest(fmt.Errorf("Network driver %q does not support forwards", n.Type()))
	}

	memberSpecific := false // Get forwards for all cluster members.

	if util.IsRecursionRequest(r) {
		records, err := d.State().Cluster.GetNetworkForwards(n.ID(), memberSpecific)
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed loading network forwards: %w", err))
		}

		forwards := make([]*api.NetworkForward, 0, len(records))
		for _, record := range records {
			forwards = append(forwards, record)
		}

		return response.SyncResponse(true, forwards)
	}

	listenAddresses, err := d.State().Cluster.GetNetworkForwardListenAddresses(n.ID(), memberSpecific)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading network forwards: %w", err))
	}

	forwardURLs := make([]string, 0, len(listenAddresses))
	for _, listenAddress := range listenAddresses {
		forwardURLs = append(forwardURLs, fmt.Sprintf("/%s/networks/%s/forwards/%s", version.APIVersion, url.PathEscape(n.Name()), url.PathEscape(listenAddress)))
	}

	return response.SyncResponse(true, forwardURLs)
}

// swagger:operation POST /1.0/networks/{networkName}/forwards network-forwards network_forwards_post
//
// Add a network address forward
//
// Creates a new network address forward.
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
//     name: forward
//     description: Forward
//     required: true
//     schema:
//       $ref: "#/definitions/NetworkForwardsPost"
// responses:
//   "200":
//     $ref: "#/responses/EmptySyncResponse"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func networkForwardsPost(d *Daemon, r *http.Request) response.Response {
	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	projectName, _, err := project.NetworkProject(d.State().Cluster, projectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	// Parse the request into a record.
	req := api.NetworkForwardsPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	req.Normalise() // So we handle the request in normalised/canonical form.

	n, err := network.LoadByName(d.State(), projectName, mux.Vars(r)["networkName"])
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading network: %w", err))
	}

	if !n.Info().AddressForwards {
		return response.BadRequest(fmt.Errorf("Network driver %q does not support forwards", n.Type()))
	}

	targetMember := queryParam(r, "target")
	memberSpecific := targetMember != ""

	// Check if there is an existing forward using the same listen address.
	_, _, err = d.State().Cluster.GetNetworkForward(n.ID(), memberSpecific, req.ListenAddress)
	if err == nil {
		return response.SmartError(api.StatusErrorf(http.StatusConflict, "A forward for that listen address already exists"))
	} else if statusCode, found := api.StatusErrorMatch(err); found && statusCode != http.StatusNotFound {
		return response.SmartError(err)
	}

	clientType := clusterRequest.UserAgentClientType(r.Header.Get("User-Agent"))

	err = n.ForwardCreate(req, clientType)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed creating forward: %w", err))
	}

	d.State().Events.SendLifecycle(projectName, lifecycle.NetworkForwardCreated.Event(n, req.ListenAddress, request.CreateRequestor(r), nil))

	url := fmt.Sprintf("/%s/networks/%s/forwards/%s", version.APIVersion, url.PathEscape(n.Name()), url.PathEscape(req.ListenAddress))
	return response.SyncResponseLocation(true, nil, url)
}

// swagger:operation DELETE /1.0/networks/{networkName}/forwards/{listenAddress} network-forwards network_forward_delete
//
// Delete the network forward
//
// Removes the network forward.
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
func networkForwardDelete(d *Daemon, r *http.Request) response.Response {
	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	projectName, _, err := project.NetworkProject(d.State().Cluster, projectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	n, err := network.LoadByName(d.State(), projectName, mux.Vars(r)["networkName"])
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading network: %w", err))
	}

	if !n.Info().AddressForwards {
		return response.BadRequest(fmt.Errorf("Network driver %q does not support forwards", n.Type()))
	}

	listenAddress := mux.Vars(r)["listenAddress"]

	clientType := clusterRequest.UserAgentClientType(r.Header.Get("User-Agent"))

	err = n.ForwardDelete(listenAddress, clientType)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed deleting forward: %w", err))
	}

	d.State().Events.SendLifecycle(projectName, lifecycle.NetworkForwardDeleted.Event(n, listenAddress, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}

// swagger:operation GET /1.0/networks/{networkName}/forwards/{listenAddress} network-forwards network_forward_get
//
// Get the network forward
//
// Gets a specific network forward.
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
//     description: Forward
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
//           $ref: "#/definitions/NetworkForward"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func networkForwardGet(d *Daemon, r *http.Request) response.Response {
	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	projectName, _, err := project.NetworkProject(d.State().Cluster, projectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	n, err := network.LoadByName(d.State(), projectName, mux.Vars(r)["networkName"])
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading network: %w", err))
	}

	if !n.Info().AddressForwards {
		return response.BadRequest(fmt.Errorf("Network driver %q does not support forwards", n.Type()))
	}

	listenAddress := mux.Vars(r)["listenAddress"]
	targetMember := queryParam(r, "target")
	memberSpecific := targetMember != ""

	_, forward, err := d.State().Cluster.GetNetworkForward(n.ID(), memberSpecific, listenAddress)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponseETag(true, forward, forward.Etag())
}

// swagger:operation PATCH /1.0/networks/{networkName}/forwards/{listenAddress} network-forwards network_forward_patch
//
// Partially update the network forward
//
// Updates a subset of the network forward configuration.
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
//     name: forward
//     description: Forward configuration
//     required: true
//     schema:
//       $ref: "#/definitions/NetworkForwardPut"
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

// swagger:operation PUT /1.0/networks/{networkName}/forwards/{listenAddress} network-forwards network_forward_put
//
// Update the network forward
//
// Updates the entire network forward configuration.
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
//     name: forward
//     description: Forward configuration
//     required: true
//     schema:
//       $ref: "#/definitions/NetworkForwardPut"
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
func networkForwardPut(d *Daemon, r *http.Request) response.Response {
	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	projectName, _, err := project.NetworkProject(d.State().Cluster, projectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	n, err := network.LoadByName(d.State(), projectName, mux.Vars(r)["networkName"])
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading network: %w", err))
	}

	if !n.Info().AddressForwards {
		return response.BadRequest(fmt.Errorf("Network driver %q does not support forwards", n.Type()))
	}

	listenAddress := mux.Vars(r)["listenAddress"]

	// Decode the request.
	req := api.NetworkForwardPut{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	targetMember := queryParam(r, "target")
	memberSpecific := targetMember != ""

	if r.Method == http.MethodPatch {
		_, forward, err := d.State().Cluster.GetNetworkForward(n.ID(), memberSpecific, listenAddress)
		if err != nil {
			return response.SmartError(err)
		}

		// If forward being updated via "patch" method and ports not specified, then merge existing ports
		// into forward.
		if req.Ports == nil {
			req.Ports = forward.Ports
		}
	}

	req.Normalise() // So we handle the request in normalised/canonical form.

	clientType := clusterRequest.UserAgentClientType(r.Header.Get("User-Agent"))

	err = n.ForwardUpdate(listenAddress, req, clientType)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed updating forward: %w", err))
	}

	d.State().Events.SendLifecycle(projectName, lifecycle.NetworkForwardUpdated.Event(n, listenAddress, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}
