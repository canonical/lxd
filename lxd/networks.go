package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/warningtype"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/network"
	"github.com/canonical/lxd/lxd/network/openvswitch"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/resources"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/lxd/warnings"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/version"
)

// Lock to prevent concurent networks creation.
var networkCreateLock sync.Mutex

var networksCmd = APIEndpoint{
	Path:        "networks",
	MetricsType: entity.TypeNetwork,

	Get:  APIEndpointAction{Handler: networksGet, AccessHandler: allowProjectResourceList(false)},
	Post: APIEndpointAction{Handler: networksPost, AccessHandler: allowPermission(entity.TypeProject, auth.EntitlementCanCreateNetworks)},
}

var networkCmd = APIEndpoint{
	Path:        "networks/{networkName}",
	MetricsType: entity.TypeNetwork,

	Delete: APIEndpointAction{Handler: networkDelete, AccessHandler: networkAccessHandler(auth.EntitlementCanDelete)},
	Get:    APIEndpointAction{Handler: networkGet, AccessHandler: networkAccessHandler(auth.EntitlementCanView)},
	Patch:  APIEndpointAction{Handler: networkPatch, AccessHandler: networkAccessHandler(auth.EntitlementCanEdit)},
	Post:   APIEndpointAction{Handler: networkPost, AccessHandler: networkAccessHandler(auth.EntitlementCanEdit)},
	Put:    APIEndpointAction{Handler: networkPut, AccessHandler: networkAccessHandler(auth.EntitlementCanEdit)},
}

var networkLeasesCmd = APIEndpoint{
	Path:        "networks/{networkName}/leases",
	MetricsType: entity.TypeNetwork,

	Get: APIEndpointAction{Handler: networkLeasesGet, AccessHandler: networkAccessHandler(auth.EntitlementCanView)},
}

var networkStateCmd = APIEndpoint{
	Path:        "networks/{networkName}/state",
	MetricsType: entity.TypeNetwork,

	Get: APIEndpointAction{Handler: networkStateGet, AccessHandler: networkAccessHandler(auth.EntitlementCanView)},
}

// ctxNetworkDetails should be used only for getting/setting networkDetails in the request context.
const ctxNetworkDetails request.CtxKey = "network-details"

// networkDetails contains fields that are determined prior to the access check. This is set in the request context when
// addNetworkDetailsToRequestContext is called.
type networkDetails struct {
	networkName    string
	requestProject api.Project
}

// addNetworkDetailsToRequestContext sets the effective project on the request.Info and sets ctxNetworkDetails (networkDetails)
// in the request context.
func addNetworkDetailsToRequestContext(s *state.State, r *http.Request) error {
	networkName, err := url.PathUnescape(mux.Vars(r)["networkName"])
	if err != nil {
		return err
	}

	requestProjectName := request.ProjectParam(r)
	effectiveProjectName, requestProject, err := project.NetworkProject(s.DB.Cluster, requestProjectName)
	if err != nil {
		return fmt.Errorf("Failed to check project %q network feature: %w", requestProjectName, err)
	}

	request.SetContextValue(r, request.CtxEffectiveProjectName, effectiveProjectName)
	request.SetContextValue(r, ctxNetworkDetails, networkDetails{
		networkName:    networkName,
		requestProject: *requestProject,
	})

	return nil
}

// profileAccessHandler calls addProfileDetailsToRequestContext, then uses the details to perform an access check with
// the given auth.Entitlement.
func networkAccessHandler(entitlement auth.Entitlement) func(d *Daemon, r *http.Request) response.Response {
	return func(d *Daemon, r *http.Request) response.Response {
		s := d.State()
		err := addNetworkDetailsToRequestContext(s, r)
		if err != nil {
			return response.SmartError(err)
		}

		details, err := request.GetContextValue[networkDetails](r.Context(), ctxNetworkDetails)
		if err != nil {
			return response.SmartError(err)
		}

		err = s.Authorizer.CheckPermission(r.Context(), entity.NetworkURL(details.requestProject.Name, details.networkName), entitlement)
		if err != nil {
			return response.SmartError(err)
		}

		return response.EmptySyncResponse
	}
}

// API endpoints

// swagger:operation GET /1.0/networks networks networks_get
//
//  Get the networks
//
//  Returns a list of networks (URLs).
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
//      description: Retrieve networks from all projects
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
//                "/1.0/networks/lxdbr0",
//                "/1.0/networks/lxdbr1"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/networks?recursion=1 networks networks_get_recursion1
//
//	Get the networks
//
//	Returns a list of networks (structs).
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
//	  - in: query
//	    name: all-projects
//	    description: Retrieve networks from all projects
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
//	          description: List of networks
//	          items:
//	            $ref: "#/definitions/Network"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networksGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// If a target was specified, forward the request to the relevant node.
	target := request.QueryParam(r, "target")
	resp := forwardedResponseToNode(r.Context(), s, target)
	if resp != nil {
		return resp
	}

	requestProjectName, allProjects, err := request.ProjectParams(r)
	if err != nil {
		return response.SmartError(err)
	}

	var effectiveProjectName string
	var reqProject *api.Project
	if !allProjects {
		// Project specific requests require an effective project, when "features.networks" is enabled this is the requested project, otherwise it is the default project.
		effectiveProjectName, reqProject, err = project.NetworkProject(s.DB.Cluster, requestProjectName)
		if err != nil {
			return response.SmartError(err)
		}

		request.SetContextValue(r, request.CtxEffectiveProjectName, effectiveProjectName)
	}

	recursion, _ := util.IsRecursionRequest(r)
	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeNetwork, true)
	if err != nil {
		return response.SmartError(err)
	}

	// networks holds the network names of the managed and unmanaged networks. They are in two different slices so that
	// we can perform access control checks differently.
	var networks [2]map[string][]string
	const (
		managed = iota
		unmanaged
	)

	projectConfigs := make(map[string]map[string]string)
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		networks[managed] = map[string][]string{}
		networks[unmanaged] = map[string][]string{}

		if allProjects {
			// Get list of managed networks from all projects.
			networks[managed], err = tx.GetNetworksAllProjects(ctx)
			if err != nil {
				return err
			}

			projectConfigs, err = dbCluster.GetAllProjectsConfig(ctx, tx.Tx())
			if err != nil {
				return err
			}
		} else {
			// Get list of managed networks (that may or may not have network interfaces on the host).
			networkNames, err := tx.GetNetworks(ctx, effectiveProjectName)
			if err != nil {
				return err
			}

			networks[managed][requestProjectName] = networkNames
		}

		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Get list of actual network interfaces on the host if the effective project is default and the caller has permission.
	var getUnmanagedNetworks bool
	if effectiveProjectName == api.ProjectDefaultName || allProjects {
		err := s.Authorizer.CheckPermission(r.Context(), entity.ServerURL(), auth.EntitlementCanViewUnmanagedNetworks)
		if err == nil {
			getUnmanagedNetworks = true
		} else if !auth.IsDeniedError(err) {
			return response.SmartError(err)
		}
	}

	if getUnmanagedNetworks {
		ifaces, err := net.Interfaces()
		if err != nil {
			return response.InternalError(err)
		}

		for _, iface := range ifaces {
			// Ignore veth pairs (for performance reasons).
			if strings.HasPrefix(iface.Name, "veth") {
				continue
			}

			unmanagedNetworkProject := requestProjectName
			if allProjects {
				unmanagedNetworkProject = api.ProjectDefaultName
			}

			// Append to the list of networks if a managed network of same name doesn't exist.
			if !slices.Contains(networks[managed][unmanagedNetworkProject], iface.Name) {
				networks[unmanaged][unmanagedNetworkProject] = append(networks[unmanaged][unmanagedNetworkProject], iface.Name)
			}
		}
	}

	// Permission checker works for managed networks only, since they are present in the database.
	userHasPermission, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeNetwork)
	if err != nil {
		return response.InternalError(err)
	}

	resultString := []string{}
	resultMap := []*api.Network{}
	urlToNetwork := make(map[*api.URL]auth.EntitlementReporter)
	for kind, projectNetworks := range networks {
		for projectName, networkNames := range projectNetworks {
			for _, networkName := range networkNames {
				// Filter out managed networks that the caller doesn't have permission to view.
				if kind == managed && !userHasPermission(entity.NetworkURL(projectName, networkName)) {
					continue
				}

				if recursion == 0 {
					resultString = append(resultString, api.NewURL().Path(version.APIVersion, "networks", networkName).String())
				} else {
					var projectConfig map[string]string
					if allProjects {
						projectConfig = projectConfigs[projectName]
					} else {
						projectConfig = reqProject.Config
					}

					net, err := doNetworkGet(s, r, s.ServerClustered, projectName, projectConfig, networkName)
					if err != nil {
						continue
					}

					resultMap = append(resultMap, &net)
					urlToNetwork[entity.NetworkURL(projectName, networkName)] = &net
				}
			}
		}
	}

	if recursion == 0 {
		return response.SyncResponse(true, resultString)
	}

	if len(withEntitlements) > 0 {
		err = reportEntitlements(r.Context(), s.Authorizer, entity.TypeNetwork, withEntitlements, urlToNetwork)
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.SyncResponse(true, resultMap)
}

// swagger:operation POST /1.0/networks networks networks_post
//
//	Add a network
//
//	Creates a new network.
//	When clustered, most network types require individual POST for each cluster member prior to a global POST.
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
//	    name: network
//	    description: Network
//	    required: true
//	    schema:
//	      $ref: "#/definitions/NetworksPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networksPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, reqProject, err := project.NetworkProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	networkCreateLock.Lock()
	defer networkCreateLock.Unlock()

	req := api.NetworksPost{}

	// Parse the request.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
	if req.Name == "" {
		return response.BadRequest(errors.New("No name provided"))
	}

	// Check if project allows access to network.
	if !project.NetworkAllowed(reqProject.Config, req.Name, true) {
		return response.SmartError(api.StatusErrorf(http.StatusForbidden, "Network not allowed in project"))
	}

	if req.Type == "" {
		if projectName != api.ProjectDefaultName {
			req.Type = "ovn" // Only OVN networks are allowed inside network enabled projects.
		} else {
			req.Type = "bridge" // Default to bridge for non-network enabled projects.
		}
	}

	if req.Config == nil {
		req.Config = map[string]string{}
	}

	netType, err := network.LoadByType(req.Type)
	if err != nil {
		return response.BadRequest(err)
	}

	err = netType.ValidateName(req.Name)
	if err != nil {
		return response.BadRequest(err)
	}

	netTypeInfo := netType.Info()
	if projectName != api.ProjectDefaultName && !netTypeInfo.Projects {
		return response.BadRequest(errors.New("Network type does not support non-default projects"))
	}

	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
		return response.SmartError(err)
	}

	// Check if project has limits.network and if so check we are allowed to create another network.
	if projectName != api.ProjectDefaultName && reqProject.Config != nil && reqProject.Config["limits.networks"] != "" {
		networksLimit, err := strconv.Atoi(reqProject.Config["limits.networks"])
		if err != nil {
			return response.InternalError(fmt.Errorf("Invalid project limits.network value: %w", err))
		}

		var networks []string

		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			networks, err = tx.GetNetworks(ctx, projectName)

			return err
		})
		if err != nil {
			return response.InternalError(fmt.Errorf("Failed loading project's networks for limits check: %w", err))
		}

		// Only check network limits if the new network name doesn't exist already in networks list.
		// If it does then this create request will either be for adding a target node to an existing
		// pending network or it will fail anyway as it is a duplicate.
		if !slices.Contains(networks, req.Name) && len(networks) >= networksLimit {
			return response.BadRequest(errors.New("Networks limit has been reached for project"))
		}
	}

	u := api.NewURL().Path(version.APIVersion, "networks", req.Name).Project(projectName)

	resp := response.SyncResponseLocation(true, nil, u.String())

	clientType := requestor.ClientType()

	if requestor.IsClusterNotification() {
		n, err := network.LoadByName(s, projectName, req.Name)
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed loading network: %w", err))
		}

		// This is an internal request which triggers the actual creation of the network across all nodes
		// after they have been previously defined.
		err = doNetworksCreate(r.Context(), s, n, clientType)
		if err != nil {
			return response.SmartError(err)
		}

		return resp
	}

	targetNode := request.QueryParam(r, "target")
	if targetNode != "" {
		if !netTypeInfo.NodeSpecificConfig {
			return response.BadRequest(fmt.Errorf("Network type %q does not support member specific config", netType.Type()))
		}

		// A targetNode was specified, let's just define the node's network without actually creating it.
		// Check that only NodeSpecificNetworkConfig keys are specified.
		for key := range req.Config {
			if !slices.Contains(db.NodeSpecificNetworkConfig, key) {
				return response.BadRequest(fmt.Errorf("Config key %q may not be used as member-specific key", key))
			}
		}

		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.CreatePendingNetwork(ctx, targetNode, projectName, req.Name, netType.DBType(), req.Config)
		})
		if err != nil {
			if api.StatusErrorCheck(err, http.StatusConflict) {
				return response.BadRequest(fmt.Errorf("The network is already defined on member %q", targetNode))
			}

			return response.SmartError(err)
		}

		return resp
	}

	var netInfo *api.Network

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Load existing network if exists, if not don't fail.
		_, netInfo, _, err = tx.GetNetworkInAnyState(ctx, projectName, req.Name)

		return err
	})
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return response.InternalError(err)
	}

	// Check if we're clustered.
	count, err := cluster.Count(s)
	if err != nil {
		return response.SmartError(err)
	}

	// No targetNode was specified and we're clustered or there is an existing partially created single node
	// network, either way finalize the config in the db and actually create the network on all cluster nodes.
	if count > 1 || (netInfo != nil && netInfo.Status != api.NetworkStatusCreated) {
		// Simulate adding pending node network config when the driver doesn't support per-node config.
		if !netTypeInfo.NodeSpecificConfig && clientType != request.ClientTypeJoiner {
			// Create pending entry for each node.
			err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
				members, err := tx.GetNodes(ctx)
				if err != nil {
					return fmt.Errorf("Failed getting cluster members: %w", err)
				}

				for _, member := range members {
					// Don't pass in any config, as these nodes don't have any node-specific
					// config and we don't want to create duplicate global config.
					err = tx.CreatePendingNetwork(ctx, member.Name, projectName, req.Name, netType.DBType(), nil)
					if err != nil && !api.StatusErrorCheck(err, http.StatusConflict) {
						return fmt.Errorf("Failed creating pending network for member %q: %w", member.Name, err)
					}
				}

				return nil
			})
			if err != nil {
				return response.SmartError(err)
			}

			n, err := network.LoadByName(s, projectName, req.Name)
			if err != nil {
				return response.SmartError(fmt.Errorf("Failed loading network: %w", err))
			}

			s.Events.SendLifecycle(projectName, lifecycle.NetworkCreated.Event(n, requestor.EventLifecycleRequestor(), nil))
		}

		err = networksPostCluster(r.Context(), s, projectName, netInfo, req, clientType, netType)
		if err != nil {
			return response.SmartError(err)
		}

		return resp
	}

	// Non-clustered network creation.
	if netInfo != nil {
		return response.BadRequest(errors.New("The network already exists"))
	}

	revert := revert.New()
	defer revert.Fail()

	// Populate default config unless joining a cluster.
	if clientType != request.ClientTypeJoiner {
		err = netType.FillConfig(req.Config)
		if err != nil {
			return response.SmartError(err)
		}
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Create the database entry.
		_, err = tx.CreateNetwork(ctx, projectName, req.Name, req.Description, netType.DBType(), req.Config)

		return err
	})
	if err != nil {
		return response.SmartError(fmt.Errorf("Error inserting %q into database: %w", req.Name, err))
	}

	revert.Add(func() {
		_ = s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.DeleteNetwork(ctx, projectName, req.Name)
		})
	})

	n, err := network.LoadByName(s, projectName, req.Name)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading network: %w", err))
	}

	err = doNetworksCreate(r.Context(), s, n, clientType)
	if err != nil {
		return response.SmartError(err)
	}

	s.Events.SendLifecycle(projectName, lifecycle.NetworkCreated.Event(n, requestor.EventLifecycleRequestor(), nil))

	revert.Success()
	return resp
}

// networkPartiallyCreated returns true of supplied network has properties that indicate it has had previous
// create attempts run on it but failed on one or more nodes.
func networkPartiallyCreated(netInfo *api.Network) bool {
	// If the network status is NetworkStatusErrored, this means create has been run in the past and has
	// failed on one or more nodes. Hence it is partially created.
	if netInfo.Status == api.NetworkStatusErrored {
		return true
	}

	// If the network has global config keys, then it has previously been created by having its global config
	// inserted, and this means it is partialled created.
	for key := range netInfo.Config {
		if !slices.Contains(db.NodeSpecificNetworkConfig, key) {
			return true
		}
	}

	return false
}

// networksPostCluster checks that there is a pending network in the database and then attempts to setup the
// network on each node. If all nodes are successfully setup then the network's state is set to created.
// Accepts an optional existing network record, which will exist when performing subsequent re-create attempts.
func networksPostCluster(ctx context.Context, s *state.State, projectName string, netInfo *api.Network, req api.NetworksPost, clientType request.ClientType, netType network.Type) error {
	// Check that no node-specific config key has been supplied in request.
	for key := range req.Config {
		if slices.Contains(db.NodeSpecificNetworkConfig, key) {
			return fmt.Errorf("Config key %q is cluster member specific", key)
		}
	}

	// If network already exists, perform quick checks.
	if netInfo != nil {
		// Check network isn't already created.
		if netInfo.Status == api.NetworkStatusCreated {
			return errors.New("The network is already created")
		}

		// Check the requested network type matches the type created when adding the local member config.
		if req.Type != netInfo.Type {
			return fmt.Errorf("Requested network type %q doesn't match type in existing database record %q", req.Type, netInfo.Type)
		}
	}

	// Check that the network is properly defined, get the node-specific configs and merge with global config.
	var nodeConfigs map[string]map[string]string
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		// Check if any global config exists already, if so we should not create global config again.
		if netInfo != nil && networkPartiallyCreated(netInfo) {
			if len(req.Config) > 0 {
				return errors.New("Network already partially created. Please do not specify any global config when re-running create")
			}

			logger.Debug("Skipping global network create as global config already partially created", logger.Ctx{"project": projectName, "network": req.Name})
			return nil
		}

		// Fetch the network ID.
		networkID, err := tx.GetNetworkID(ctx, projectName, req.Name)
		if err != nil {
			return err
		}

		// Fetch the node-specific configs and check the network is defined for all nodes.
		nodeConfigs, err = tx.NetworkNodeConfigs(ctx, networkID)
		if err != nil {
			return err
		}

		// Add default values if we are inserting global config for first time.
		err = netType.FillConfig(req.Config)
		if err != nil {
			return err
		}

		// Update only the description of the network.
		err = tx.UpdateNetworkDescription(networkID, req.Description)
		if err != nil {
			return err
		}

		// Insert the global config keys.
		err = tx.CreateNetworkConfig(networkID, 0, req.Config)
		if err != nil {
			return err
		}

		// Assume failure unless we succeed later on.
		return tx.NetworkErrored(projectName, req.Name)
	})
	if err != nil {
		if response.IsNotFoundError(err) {
			return errors.New("Network not pending on any node (use --target <node> first)")
		}

		return err
	}

	// Create notifier for other nodes to create the network.
	notifier, err := cluster.NewNotifier(s, s.Endpoints.NetworkCert(), s.ServerCert(), cluster.NotifyAll)
	if err != nil {
		return err
	}

	// Load the network from the database for the local member.
	n, err := network.LoadByName(s, projectName, req.Name)
	if err != nil {
		return fmt.Errorf("Failed loading network: %w", err)
	}

	netConfig := n.Config()

	err = doNetworksCreate(ctx, s, n, clientType)
	if err != nil {
		return err
	}

	logger.Debug("Created network on local cluster member", logger.Ctx{"project": projectName, "network": req.Name, "config": netConfig})

	// Remove this node's node specific config keys.
	for _, key := range db.NodeSpecificNetworkConfig {
		delete(netConfig, key)
	}

	// Notify other nodes to create the network.
	err = notifier(func(member db.NodeInfo, client lxd.InstanceServer) error {
		// Clone the network config for this node so we don't modify it and potentially end up sending
		// this node's config to another node.
		nodeConfig := make(map[string]string, len(netConfig))
		maps.Copy(nodeConfig, netConfig)

		// Merge node specific config items into global config.
		maps.Copy(nodeConfig, nodeConfigs[member.Name])

		// Create fresh request based on existing network to send to node.
		nodeReq := api.NetworksPost{
			NetworkPut: api.NetworkPut{
				Config:      nodeConfig,
				Description: n.Description(),
			},
			Name: n.Name(),
			Type: n.Type(),
		}

		err = client.UseProject(n.Project()).CreateNetwork(nodeReq)
		if err != nil {
			return err
		}

		logger.Debug("Created network on cluster member", logger.Ctx{"project": n.Project(), "network": n.Name(), "member": member.Name, "config": nodeReq.Config})

		return nil
	})
	if err != nil {
		return err
	}

	// Mark network global status as networkCreated now that all nodes have succeeded.
	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.NetworkCreated(projectName, req.Name)
	})
	if err != nil {
		return err
	}

	logger.Debug("Marked network global status as created", logger.Ctx{"project": projectName, "network": req.Name})

	return nil
}

// Create the network on the system. The clusterNotification flag is used to indicate whether creation request
// is coming from a cluster notification (and if so we should not delete the database record on error).
func doNetworksCreate(ctx context.Context, s *state.State, n network.Network, clientType request.ClientType) error {
	revert := revert.New()
	defer revert.Fail()

	// Don't validate network config during pre-cluster-join phase, as if network has ACLs they won't exist
	// in the local database yet. Once cluster join is completed, network will be restarted to give chance for
	// ACL firewall config to be applied.
	if clientType != request.ClientTypeJoiner {
		// Validate so that when run on a cluster node the full config (including node specific config)
		// is checked.
		err := n.Validate(n.Config())
		if err != nil {
			return err
		}
	}

	if n.LocalStatus() == api.NetworkStatusCreated {
		logger.Debug("Skipping local network create as already created", logger.Ctx{"project": n.Project(), "network": n.Name()})
		return nil
	}

	// Run initial creation setup for the network driver.
	err := n.Create(clientType)
	if err != nil {
		return err
	}

	revert.Add(func() { _ = n.Delete(clientType) })

	// Only start networks when not doing a cluster pre-join phase (this ensures that networks are only started
	// once the node has fully joined the clustered database and has consistent config with rest of the nodes).
	if clientType != request.ClientTypeJoiner {
		err = n.Start()
		if err != nil {
			return fmt.Errorf("Failed starting network: %w", err)
		}
	}

	// Mark local as status as networkCreated.
	err = s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.NetworkNodeCreated(n.ID())
	})
	if err != nil {
		return err
	}

	logger.Debug("Marked network local status as created", logger.Ctx{"project": n.Project(), "network": n.Name()})

	revert.Success()
	return nil
}

// swagger:operation GET /1.0/networks/{name} networks network_get
//
//	Get the network
//
//	Gets a specific network.
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
//	    description: Network
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
//	          $ref: "#/definitions/Network"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// If a target was specified, forward the request to the relevant node.
	target := request.QueryParam(r, "target")
	resp := forwardedResponseToNode(r.Context(), s, target)
	if resp != nil {
		return resp
	}

	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeNetwork, false)
	if err != nil {
		return response.SmartError(err)
	}

	details, err := request.GetContextValue[networkDetails](r.Context(), ctxNetworkDetails)
	if err != nil {
		return response.SmartError(err)
	}

	allNodes := s.ServerClustered && request.QueryParam(r, "target") == ""

	n, err := doNetworkGet(s, r, allNodes, details.requestProject.Name, details.requestProject.Config, details.networkName)
	if err != nil {
		return response.SmartError(err)
	}

	if len(withEntitlements) > 0 {
		err = reportEntitlements(r.Context(), s.Authorizer, entity.TypeNetwork, withEntitlements, map[*api.URL]auth.EntitlementReporter{entity.NetworkURL(details.requestProject.Name, details.networkName): &n})
		if err != nil {
			return response.SmartError(err)
		}
	}

	etag := []any{n.Name, n.Managed, n.Type, n.Description, n.Config}

	return response.SyncResponseETag(true, &n, etag)
}

// doNetworkGet returns information about the specified network.
// If the network being requested is a managed network and allNodes is true then node specific config is removed.
// Otherwise if allNodes is false then the network's local status is returned.
func doNetworkGet(s *state.State, r *http.Request, allNodes bool, requestProjectName string, reqProjectConfig map[string]string, networkName string) (api.Network, error) {
	effectiveProjectName, err := request.GetContextValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		// Effective project may not be set if getting networks for all projects.
		effectiveProjectName = requestProjectName
	}

	// Ignore veth pairs (for performance reasons).
	if strings.HasPrefix(networkName, "veth") {
		return api.Network{}, api.StatusErrorf(http.StatusNotFound, "Network not found")
	}

	// Get some information.
	n, err := network.LoadByName(s, effectiveProjectName, networkName)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return api.Network{}, fmt.Errorf("Failed loading network: %w", err)
	}

	// Don't allow retrieving info about the local server interfaces when not using default project.
	if effectiveProjectName != api.ProjectDefaultName && n == nil {
		return api.Network{}, api.StatusErrorf(http.StatusNotFound, "Network not found")
	}

	// Check if project allows access to network.
	if !project.NetworkAllowed(reqProjectConfig, networkName, n != nil && n.IsManaged()) {
		return api.Network{}, api.StatusErrorf(http.StatusNotFound, "Network not found")
	}

	osInfo, _ := net.InterfaceByName(networkName)

	// Quick check.
	if osInfo == nil && n == nil {
		return api.Network{}, api.StatusErrorf(http.StatusNotFound, "Network not found")
	}

	// Prepare the response.
	apiNet := api.Network{}
	apiNet.Name = networkName
	apiNet.UsedBy = []string{}
	apiNet.Config = map[string]string{}
	apiNet.Project = requestProjectName

	// Set the device type as needed.
	if n != nil {
		apiNet.Managed = true
		apiNet.Description = n.Description()
		apiNet.Type = n.Type()

		err = s.Authorizer.CheckPermission(r.Context(), entity.NetworkURL(requestProjectName, networkName), auth.EntitlementCanEdit)
		if err != nil && !auth.IsDeniedError(err) {
			return api.Network{}, err
		} else if err == nil {
			// Only allow users that can edit network config to view it as sensitive info can be stored there.
			apiNet.Config = n.Config()
		}

		// If no member is specified, we omit the node-specific fields.
		if allNodes {
			for _, key := range db.NodeSpecificNetworkConfig {
				delete(apiNet.Config, key)
			}
		}
	} else if osInfo != nil && shared.IsLoopback(osInfo) {
		apiNet.Type = "loopback"
	} else if shared.PathExists("/sys/class/net/" + apiNet.Name + "/bridge") {
		apiNet.Type = "bridge"
	} else if shared.PathExists("/proc/net/vlan/" + apiNet.Name) {
		apiNet.Type = "vlan"
	} else if shared.PathExists("/sys/class/net/" + apiNet.Name + "/device") {
		apiNet.Type = "physical"
	} else if shared.PathExists("/sys/class/net/" + apiNet.Name + "/bonding") {
		apiNet.Type = "bond"
	} else {
		ovs := openvswitch.NewOVS()
		exists, _ := ovs.BridgeExists(apiNet.Name)
		if exists {
			apiNet.Type = "bridge"
		} else {
			apiNet.Type = "unknown"
		}
	}

	// Look for instances using the interface.
	if apiNet.Type != "loopback" {
		var networkID int64
		if n != nil {
			networkID = n.ID()
		}

		usedBy, err := network.UsedBy(s, effectiveProjectName, networkID, apiNet.Name, apiNet.Type, false)
		if err != nil {
			return api.Network{}, err
		}

		apiNet.UsedBy = project.FilterUsedBy(r.Context(), s.Authorizer, usedBy)
	}

	if n != nil {
		if allNodes {
			apiNet.Status = n.Status()
		} else {
			apiNet.Status = n.LocalStatus()
		}

		apiNet.Locations = n.Locations()
	}

	return apiNet, nil
}

// swagger:operation DELETE /1.0/networks/{name} networks network_delete
//
//	Delete the network
//
//	Removes the network.
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
func networkDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	effectiveProjectName, err := request.GetContextValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	details, err := request.GetContextValue[networkDetails](r.Context(), ctxNetworkDetails)
	if err != nil {
		return response.SmartError(err)
	}

	err = doNetworkDelete(r.Context(), s, details.networkName, effectiveProjectName, details.requestProject.Config)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

// doNetworkDelete deletes the named network in the given project.
func doNetworkDelete(ctx context.Context, s *state.State, name string, effectiveProjectName string, requestProjectConfig map[string]string) error {
	// Get the existing network.
	n, err := network.LoadByName(s, effectiveProjectName, name)
	if err != nil {
		return fmt.Errorf("Failed loading network: %w", err)
	}

	// Check if project allows access to network.
	if !project.NetworkAllowed(requestProjectConfig, name, n.IsManaged()) {
		return api.NewStatusError(http.StatusNotFound, "Network not found")
	}

	requestor, err := request.GetRequestor(ctx)
	if err != nil {
		return err
	}

	clusterNotification := requestor.IsClusterNotification()
	if !clusterNotification {
		// Quick checks.
		inUse, err := n.IsUsed()
		if err != nil {
			return err
		}

		if inUse {
			return api.NewStatusError(http.StatusBadRequest, "The network is currently in use")
		}
	}

	if n.LocalStatus() != api.NetworkStatusPending {
		err = n.Delete(requestor.ClientType())
		if err != nil {
			return fmt.Errorf("Failed to delete network: %w", err)
		}
	}

	// If this is a cluster notification, we're done, any database work will be done by the node that is
	// originally serving the request.
	if clusterNotification {
		return nil
	}

	// If we are clustered, also notify all other nodes, if any.
	if s.ServerClustered {
		notifier, err := cluster.NewNotifier(s, s.Endpoints.NetworkCert(), s.ServerCert(), cluster.NotifyAll)
		if err != nil {
			return err
		}

		err = notifier(func(member db.NodeInfo, client lxd.InstanceServer) error {
			return client.UseProject(n.Project()).DeleteNetwork(n.Name())
		})
		if err != nil {
			return err
		}
	}

	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		// Remove the network from the database.
		err = tx.DeleteNetwork(ctx, n.Project(), n.Name())
		return err
	})
	if err != nil {
		return err
	}

	s.Events.SendLifecycle(effectiveProjectName, lifecycle.NetworkDeleted.Event(n, requestor.EventLifecycleRequestor(), nil))

	return nil
}

// swagger:operation POST /1.0/networks/{name} networks network_post
//
//	Rename the network
//
//	Renames an existing network.
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
//	    name: network
//	    description: Network rename request
//	    required: true
//	    schema:
//	      $ref: "#/definitions/NetworkPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// FIXME: renaming a network is currently not supported in clustering
	//        mode. The difficulty is that network.Start() depends on the
	//        network having already been renamed in the database, which is
	//        a chicken-and-egg problem for cluster notifications (the
	//        serving node should typically do the database job, so the
	//        network is not yet renamed inthe db when the notified node
	//        runs network.Start).
	if s.ServerClustered {
		return response.BadRequest(errors.New("Renaming clustered network not supported"))
	}

	effectiveProjectName, err := request.GetContextValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	details, err := request.GetContextValue[networkDetails](r.Context(), ctxNetworkDetails)
	if err != nil {
		return response.SmartError(err)
	}

	req := api.NetworkPost{}

	// Parse the request.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Get the existing network.
	n, err := network.LoadByName(s, effectiveProjectName, details.networkName)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading network: %w", err))
	}

	// Check if project allows access to network.
	if !project.NetworkAllowed(details.requestProject.Config, details.networkName, n.IsManaged()) {
		return response.SmartError(api.StatusErrorf(http.StatusNotFound, "Network not found"))
	}

	if n.Status() != api.NetworkStatusCreated {
		return response.BadRequest(errors.New("Cannot rename network when not in created state"))
	}

	// Ensure new name is supplied.
	if req.Name == "" {
		return response.BadRequest(errors.New("New network name not provided"))
	}

	err = n.ValidateName(req.Name)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check network isn't in use.
	inUse, err := n.IsUsed()
	if err != nil {
		return response.InternalError(fmt.Errorf("Failed checking network in use: %w", err))
	}

	if inUse {
		return response.BadRequest(errors.New("Network is currently in use"))
	}

	var networks []string

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Check that the name isn't already in used by an existing managed network.
		networks, err = tx.GetNetworks(ctx, effectiveProjectName)

		return err
	})
	if err != nil {
		return response.InternalError(err)
	}

	if slices.Contains(networks, req.Name) {
		return response.Conflict(fmt.Errorf("Network %q already exists", req.Name))
	}

	// Rename it.
	err = n.Rename(req.Name)
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r.Context())
	lc := lifecycle.NetworkRenamed.Event(n, requestor, map[string]any{"old_name": details.networkName})
	s.Events.SendLifecycle(effectiveProjectName, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// swagger:operation PUT /1.0/networks/{name} networks network_put
//
//	Update the network
//
//	Updates the entire network configuration.
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
//	    name: network
//	    description: Network configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/NetworkPut"
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
func networkPut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// If a target was specified, forward the request to the relevant node.
	target := request.QueryParam(r, "target")
	resp := forwardedResponseToNode(r.Context(), s, target)
	if resp != nil {
		return resp
	}

	effectiveProjectName, err := request.GetContextValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	details, err := request.GetContextValue[networkDetails](r.Context(), ctxNetworkDetails)
	if err != nil {
		return response.SmartError(err)
	}

	// Get the existing network.
	n, err := network.LoadByName(s, effectiveProjectName, details.networkName)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading network: %w", err))
	}

	// Check if project allows access to network.
	if !project.NetworkAllowed(details.requestProject.Config, details.networkName, n.IsManaged()) {
		return response.SmartError(api.StatusErrorf(http.StatusNotFound, "Network not found"))
	}

	targetNode := request.QueryParam(r, "target")

	if targetNode == "" && n.Status() != api.NetworkStatusCreated {
		return response.BadRequest(errors.New("Cannot update network global config when not in created state"))
	}

	// Duplicate config for etag modification and generation.
	etagConfig := util.CopyConfig(n.Config())

	// If no target node is specified and the daemon is clustered, we omit the node-specific fields so that
	// the e-tag can be generated correctly. This is because the GET request used to populate the request
	// will also remove node-specific keys when no target is specified.
	if targetNode == "" && s.ServerClustered {
		for _, key := range db.NodeSpecificNetworkConfig {
			delete(etagConfig, key)
		}
	}

	// Validate the ETag.
	etag := []any{n.Name(), n.IsManaged(), n.Type(), n.Description(), etagConfig}
	err = util.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	// Decode the request.
	req := api.NetworkPut{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
		return response.SmartError(err)
	}

	// In clustered mode, we differentiate between node specific and non-node specific config keys based on
	// whether the user has specified a target to apply the config to.
	if s.ServerClustered {
		if targetNode == "" {
			// If no target is specified, then ensure only non-node-specific config keys are changed.
			for k := range req.Config {
				if slices.Contains(db.NodeSpecificNetworkConfig, k) {
					return response.BadRequest(fmt.Errorf("Config key %q is cluster member specific", k))
				}
			}
		} else {
			curConfig := n.Config()

			// If a target is specified, then ensure only node-specific config keys are changed.
			for k, v := range req.Config {
				if !slices.Contains(db.NodeSpecificNetworkConfig, k) && curConfig[k] != v {
					return response.BadRequest(fmt.Errorf("Config key %q may not be used as member-specific key", k))
				}
			}
		}
	}

	response := doNetworkUpdate(n, req, targetNode, requestor.ClientType(), r.Method, s.ServerClustered)

	s.Events.SendLifecycle(effectiveProjectName, lifecycle.NetworkUpdated.Event(n, requestor.EventLifecycleRequestor(), nil))

	return response
}

// swagger:operation PATCH /1.0/networks/{name} networks network_patch
//
//	Partially update the network
//
//	Updates a subset of the network configuration.
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
//	    name: network
//	    description: Network configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/NetworkPut"
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
func networkPatch(d *Daemon, r *http.Request) response.Response {
	return networkPut(d, r)
}

// doNetworkUpdate loads the current local network config, merges with the requested network config, validates
// and applies the changes. Will also notify other cluster nodes of non-node specific config if needed.
func doNetworkUpdate(n network.Network, req api.NetworkPut, targetNode string, clientType request.ClientType, httpMethod string, clustered bool) response.Response {
	if req.Config == nil {
		req.Config = map[string]string{}
	}

	// Normally a "put" request will replace all existing config, however when clustered, we need to account
	// for the node specific config keys and not replace them when the request doesn't specify a specific node.
	if targetNode == "" && httpMethod != http.MethodPatch && clustered {
		// If non-node specific config being updated via "put" method in cluster, then merge the current
		// node-specific network config with the submitted config to allow validation.
		// This allows removal of non-node specific keys when they are absent from request config.
		for k, v := range n.Config() {
			if slices.Contains(db.NodeSpecificNetworkConfig, k) {
				req.Config[k] = v
			}
		}
	} else if httpMethod == http.MethodPatch {
		// If config being updated via "patch" method, then merge all existing config with the keys that
		// are present in the request config.
		for k, v := range n.Config() {
			_, ok := req.Config[k]
			if !ok {
				req.Config[k] = v
			}
		}
	}

	// Validate the merged configuration.
	err := n.Validate(req.Config)
	if err != nil {
		return response.BadRequest(err)
	}

	// Apply the new configuration (will also notify other cluster nodes if needed).
	err = n.Update(req, targetNode, clientType)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

// swagger:operation GET /1.0/networks/{name}/leases networks networks_leases_get
//
//	Get the DHCP leases
//
//	Returns a list of DHCP leases for the network.
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
//	          description: List of DHCP leases
//	          items:
//	            $ref: "#/definitions/NetworkLease"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkLeasesGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, reqProject, err := project.NetworkProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	networkName, err := url.PathUnescape(mux.Vars(r)["networkName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Attempt to load the network.
	n, err := network.LoadByName(s, projectName, networkName)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading network: %w", err))
	}

	// Check if project allows access to network.
	if !project.NetworkAllowed(reqProject.Config, networkName, n.IsManaged()) {
		return response.SmartError(api.StatusErrorf(http.StatusNotFound, "Network not found"))
	}

	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
		return response.SmartError(err)
	}

	leases, err := n.Leases(reqProject.Name, requestor.ClientType())
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, leases)
}

func networkStartup(stateFunc func() *state.State, restoreOnly bool) error {
	var err error

	// Build a list of networks to start, keyed by project and network name.
	const networkPriorityStandalone = 0 // Start networks not dependent on any other network first.
	const networkPriorityPhysical = 1   // Start networks dependent on physical interfaces second.
	const networkPriorityLogical = 2    // Start networks dependent logical networks third.
	startNetworks := []map[network.ProjectNetwork]struct{}{
		networkPriorityStandalone: make(map[network.ProjectNetwork]struct{}),
		networkPriorityPhysical:   make(map[network.ProjectNetwork]struct{}),
		networkPriorityLogical:    make(map[network.ProjectNetwork]struct{}),
	}

	loadedNetworks := make(map[network.ProjectNetwork]network.Network)

	initNetwork := func(s *state.State, n network.Network, priority int) error {
		err = n.Start()
		if err != nil {
			err = fmt.Errorf("Failed starting network: %w", err)

			_ = s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.UpsertWarningLocalNode(ctx, n.Project(), entity.TypeNetwork, int(n.ID()), warningtype.NetworkUnvailable, err.Error())
			})

			return err
		}

		logger.Info("Initialized network", logger.Ctx{"project": n.Project(), "name": n.Name()})

		// Network initialized successfully so remove it from the list so its not retried.
		pn := network.ProjectNetwork{
			ProjectName: n.Project(),
			NetworkName: n.Name(),
		}

		delete(startNetworks[priority], pn)

		_ = warnings.ResolveWarningsByLocalNodeAndProjectAndTypeAndEntity(s.DB.Cluster, n.Project(), warningtype.NetworkUnvailable, entity.TypeNetwork, int(n.ID()))

		return nil
	}

	restoreNetwork := func(n network.Network, priority int) error {
		if n.LocalStatus() != api.NetworkStatusCreated {
			return fmt.Errorf("Cannot restore network %q when not in created state", n.Name())
		}

		err = n.Restore()
		if err != nil {
			return fmt.Errorf("Failed restoring network: %w", err)
		}

		// Network restored successfully so remove it from the list.
		// Otherwise the network startup might enter a retry loop which is not desired when restoring a network.
		pn := network.ProjectNetwork{
			ProjectName: n.Project(),
			NetworkName: n.Name(),
		}

		delete(startNetworks[priority], pn)
		return nil
	}

	loadAndStartupNetwork := func(s *state.State, pn network.ProjectNetwork, priority int, firstPass bool, restoreOnly bool) error {
		var err error
		var n network.Network

		if firstPass && loadedNetworks[pn] != nil {
			// Check if network already loaded from during first pass phase.
			n = loadedNetworks[pn]
		} else {
			n, err = network.LoadByName(s, pn.ProjectName, pn.NetworkName)
			if err != nil {
				if api.StatusErrorCheck(err, http.StatusNotFound) {
					// Network has been deleted since we began trying to start it so delete
					// entry.
					delete(startNetworks[priority], pn)

					return nil
				}

				return fmt.Errorf("Failed loading: %w", err)
			}
		}

		netConfig := n.Config()
		err = n.Validate(netConfig)
		if err != nil {
			return fmt.Errorf("Failed validating: %w", err)
		}

		// Update network start priority based on dependencies.
		if netConfig["parent"] != "" && priority != networkPriorityPhysical {
			// Start networks that depend on physical interfaces existing after
			// non-dependent networks.
			delete(startNetworks[priority], pn)
			startNetworks[networkPriorityPhysical][pn] = struct{}{}

			return nil
		} else if (netConfig["network"] != "" || netConfig["bridge.external_interfaces"] != "") && priority != networkPriorityLogical {
			// Start networks that depend on other logical networks after
			// non-dependent networks and networks that depend on physical interfaces.
			delete(startNetworks[priority], pn)
			startNetworks[networkPriorityLogical][pn] = struct{}{}

			return nil
		}

		// When restoring a network don't enter the initNetwork function and simply run the network's Restore.
		// The init takes care of e.g. clearing warnings related to the overall start of the network.
		if restoreOnly {
			return restoreNetwork(n, priority)
		}

		return initNetwork(s, n, priority)
	}

	remainingNetworksCount := func() int {
		remainingNetworks := 0
		for _, projectNetworks := range startNetworks {
			remainingNetworks += len(projectNetworks)
		}

		return remainingNetworks
	}

	{
		// Perform first pass to start networks.
		// Local scope for state variable during initial pass of setting up networks.
		s := stateFunc()
		err = s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
			projectNames, err := dbCluster.GetProjectNames(ctx, tx.Tx())
			if err != nil {
				return fmt.Errorf("Failed loading projects: %w", err)
			}

			for _, projectName := range projectNames {
				networkNames, err := tx.GetCreatedNetworkNamesByProject(ctx, projectName)
				if err != nil {
					return fmt.Errorf("Failed to load networks for project %q: %w", projectName, err)
				}

				for _, networkName := range networkNames {
					pn := network.ProjectNetwork{
						ProjectName: projectName,
						NetworkName: networkName,
					}

					// Assume all networks are networkPriorityStandalone initially.
					startNetworks[networkPriorityStandalone][pn] = struct{}{}
				}
			}

			return nil
		})
		if err != nil {
			return err
		}

		// Try initializing networks in priority order.
		for priority := range startNetworks {
			for pn := range startNetworks[priority] {
				err := loadAndStartupNetwork(s, pn, priority, true, restoreOnly)
				if err != nil {
					// When restoring a network the operation is not allowed to fail.
					// The network is already started at this stage which might have taken multiple retries.
					if restoreOnly {
						return err
					}

					logger.Error("Failed initializing network", logger.Ctx{"project": pn.ProjectName, "network": pn.NetworkName, "err": err})

					continue
				}
			}
		}

		loadedNetworks = nil // Don't store loaded networks after first pass.
	}

	// For any remaining networks that were not successfully initialised, we now start a go routine to
	// periodically try to initialize them again in the background.
	if remainingNetworksCount() > 0 {
		go func() {
			for {
				t := time.NewTimer(time.Minute)
				s := stateFunc() // Get fresh state in case global config has been updated.

				select {
				case <-s.ShutdownCtx.Done():
					t.Stop()
					return
				case <-t.C:
					t.Stop()

					tryInstancesStart := false

					// Try initializing networks in priority order.
					for priority := range startNetworks {
						for pn := range startNetworks[priority] {
							err := loadAndStartupNetwork(s, pn, priority, false, restoreOnly)
							if err != nil {
								logger.Error("Failed initializing network", logger.Ctx{"project": pn.ProjectName, "network": pn.NetworkName, "err": err})

								continue
							}

							tryInstancesStart = true // We initialized at least one network.
						}
					}

					remainingNetworks := remainingNetworksCount()
					if remainingNetworks <= 0 {
						logger.Info("All networks initialized")
					}

					// At least one remaining network was initialized, check if any instances
					// can now start.
					if tryInstancesStart {
						instances, err := instance.LoadNodeAll(s, instancetype.Any)
						if err != nil {
							logger.Warn("Failed loading instances to start", logger.Ctx{"err": err})
						} else {
							instancesStart(s, instances)
						}
					}

					if remainingNetworks <= 0 {
						// All networks are ready now after performing some retries.
						// This unblocks any waitready caller using the --network flag.
						s.NetworkReady.Cancel()

						return // Our job here is done.
					}
				}
			}
		}()
	} else {
		// All networks are ready.
		// This unblocks any waitready caller using the --network flag.
		// In case there aren't any networks, this just cancels the canceller.
		stateFunc().NetworkReady.Cancel()

		logger.Info("All networks started")
	}

	return nil
}

func networkStop(s *state.State, evacuateOnly bool) {
	if s.DB.Cluster == nil {
		logger.Warn("Skipping networks stop due to global database not being available")
		return
	}

	logger.Info("Stopping networks")

	var err error

	// Get a list of projects.
	var projectNames []string

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		projectNames, err = dbCluster.GetProjectNames(ctx, tx.Tx())
		return err
	})
	if err != nil {
		logger.Error("Failed shutting down networks, couldn't load projects", logger.Ctx{"err": err})
		return
	}

	for _, projectName := range projectNames {
		var networks []string

		err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Get a list of managed networks.
			networks, err = tx.GetNetworks(ctx, projectName)

			return err
		})
		if err != nil {
			logger.Error("Failed shutting down networks, couldn't load networks for project", logger.Ctx{"project": projectName, "err": err})
			continue
		}

		// Bring them all down.
		for _, name := range networks {
			n, err := network.LoadByName(s, projectName, name)
			if err != nil {
				logger.Error("Failed shutting down network, couldn't load network", logger.Ctx{"network": name, "project": projectName, "err": err})
				continue
			}

			if evacuateOnly {
				if n.LocalStatus() != api.NetworkStatusCreated {
					logger.Error("Failed evacuating network, not in created state", logger.Ctx{"network": name, "project": projectName})
					continue
				}

				err = n.Evacuate()
			} else {
				err = n.Stop()
			}

			if err != nil {
				logger.Error("Failed to bring down network", logger.Ctx{"err": err, "project": projectName, "name": name, "evacuate": evacuateOnly})
			}
		}
	}
}

// networkRestartOVN is used to trigger a restart of all OVN networks.
func networkRestartOVN(s *state.State) error {
	logger.Info("Restarting OVN networks")

	// Get a list of projects.
	var projectNames []string
	var err error
	err = s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		projectNames, err = dbCluster.GetProjectNames(ctx, tx.Tx())
		return err
	})
	if err != nil {
		return fmt.Errorf("Failed loading projects: %w", err)
	}

	// Go over all the networks in every project.
	for _, projectName := range projectNames {
		var networkNames []string

		err := s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
			networkNames, err = tx.GetCreatedNetworkNamesByProject(ctx, projectName)

			return err
		})
		if err != nil {
			return fmt.Errorf("Failed to load networks for project %q: %w", projectName, err)
		}

		for _, networkName := range networkNames {
			// Load the network struct.
			n, err := network.LoadByName(s, projectName, networkName)
			if err != nil {
				return fmt.Errorf("Failed to load network %q in project %q: %w", networkName, projectName, err)
			}

			// Skip non-OVN networks.
			if n.DBType() != db.NetworkTypeOVN {
				continue
			}

			// Restart the network.
			err = n.Start()
			if err != nil {
				return fmt.Errorf("Failed to restart network %q in project %q: %w", networkName, projectName, err)
			}
		}
	}

	return nil
}

// swagger:operation GET /1.0/networks/{name}/state networks networks_state_get
//
//	Get the network state
//
//	Returns the current network state information.
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
//	          $ref: "#/definitions/NetworkState"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkStateGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// If a target was specified, forward the request to the relevant node.
	target := request.QueryParam(r, "target")
	resp := forwardedResponseToNode(r.Context(), s, target)
	if resp != nil {
		return resp
	}

	projectName, reqProject, err := project.NetworkProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	networkName, err := url.PathUnescape(mux.Vars(r)["networkName"])
	if err != nil {
		return response.SmartError(err)
	}

	n, err := network.LoadByName(s, projectName, networkName)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return response.SmartError(fmt.Errorf("Failed loading network: %w", err))
	}

	// Check if project allows access to network.
	if !project.NetworkAllowed(reqProject.Config, networkName, n != nil && n.IsManaged()) {
		return response.SmartError(api.StatusErrorf(http.StatusNotFound, "Network not found"))
	}

	var state *api.NetworkState
	if n != nil {
		state, err = n.State()
		if err != nil {
			return response.SmartError(err)
		}
	} else {
		state, err = resources.GetNetworkState(networkName)
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.SyncResponse(true, state)
}
