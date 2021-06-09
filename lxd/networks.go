package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/gorilla/mux"
	"github.com/pkg/errors"

	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/cluster/request"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/device/nictype"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/network"
	"github.com/lxc/lxd/lxd/network/openvswitch"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/resources"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"
)

// Lock to prevent concurent networks creation
var networkCreateLock sync.Mutex

var networksCmd = APIEndpoint{
	Path: "networks",

	Get:  APIEndpointAction{Handler: networksGet, AccessHandler: allowProjectPermission("networks", "view")},
	Post: APIEndpointAction{Handler: networksPost, AccessHandler: allowProjectPermission("networks", "manage-networks")},
}

var networkCmd = APIEndpoint{
	Path: "networks/{name}",

	Delete: APIEndpointAction{Handler: networkDelete, AccessHandler: allowProjectPermission("networks", "manage-networks")},
	Get:    APIEndpointAction{Handler: networkGet, AccessHandler: allowProjectPermission("networks", "view")},
	Patch:  APIEndpointAction{Handler: networkPatch, AccessHandler: allowProjectPermission("networks", "manage-networks")},
	Post:   APIEndpointAction{Handler: networkPost, AccessHandler: allowProjectPermission("networks", "manage-networks")},
	Put:    APIEndpointAction{Handler: networkPut, AccessHandler: allowProjectPermission("networks", "manage-networks")},
}

var networkLeasesCmd = APIEndpoint{
	Path: "networks/{name}/leases",

	Get: APIEndpointAction{Handler: networkLeasesGet, AccessHandler: allowProjectPermission("networks", "view")},
}

var networkStateCmd = APIEndpoint{
	Path: "networks/{name}/state",

	Get: APIEndpointAction{Handler: networkStateGet, AccessHandler: allowProjectPermission("networks", "view")},
}

// API endpoints

// swagger:operation GET /1.0/networks networks networks_get
//
// Get the networks
//
// Returns a list of networks (URLs).
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
//               "/1.0/networks/lxdbr0",
//               "/1.0/networks/lxdbr1"
//             ]
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/networks?recursion=1 networks networks_get_recursion1
//
// Get the networks
//
// Returns a list of networks (structs).
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
//           description: List of networks
//           items:
//             $ref: "#/definitions/Network"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func networksGet(d *Daemon, r *http.Request) response.Response {
	projectName, _, err := project.NetworkProject(d.State().Cluster, projectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	recursion := util.IsRecursionRequest(r)

	// Get list of managed networks (that may or may not have network interfaces on the host).
	networks, err := d.cluster.GetNetworks(projectName)
	if err != nil {
		return response.InternalError(err)
	}

	// Get list of actual network interfaces on the host as well if the network's project is Default.
	if projectName == project.Default {
		ifaces, err := net.Interfaces()
		if err != nil {
			return response.InternalError(err)
		}

		for _, iface := range ifaces {
			// Ignore veth pairs (for performance reasons).
			if strings.HasPrefix(iface.Name, "veth") {
				continue
			}

			// Append to the list of networks if a managed network of same name doesn't exist.
			if !shared.StringInSlice(iface.Name, networks) {
				networks = append(networks, iface.Name)
			}
		}
	}

	resultString := []string{}
	resultMap := []api.Network{}
	for _, network := range networks {
		if !recursion {
			resultString = append(resultString, fmt.Sprintf("/%s/networks/%s", version.APIVersion, network))
		} else {
			net, err := doNetworkGet(d, r, projectName, network)
			if err != nil {
				continue
			}
			resultMap = append(resultMap, net)
		}
	}

	if !recursion {
		return response.SyncResponse(true, resultString)
	}

	return response.SyncResponse(true, resultMap)
}

// swagger:operation POST /1.0/networks networks networks_post
//
// Add a network
//
// Creates a new network.
// When clustered, most network types require individual POST for each cluster member prior to a global POST.
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
//   - in: query
//     name: target
//     description: Cluster member name
//     type: string
//     example: lxd01
//   - in: body
//     name: network
//     description: Network
//     required: true
//     schema:
//       $ref: "#/definitions/NetworksPost"
// responses:
//   "200":
//     $ref: "#/responses/EmptySyncResponse"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func networksPost(d *Daemon, r *http.Request) response.Response {
	projectName, projectConfig, err := project.NetworkProject(d.State().Cluster, projectParam(r))
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
		return response.BadRequest(fmt.Errorf("No name provided"))
	}

	if req.Type == "" {
		if projectName != project.Default {
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
	if projectName != project.Default && !netTypeInfo.Projects {
		return response.BadRequest(fmt.Errorf("Network type does not support non-default projects"))
	}

	// Check if project has limits.network and if so check we are allowed to create another network.
	if projectName != project.Default && projectConfig != nil && projectConfig["limits.networks"] != "" {
		networksLimit, err := strconv.Atoi(projectConfig["limits.networks"])
		if err != nil {
			return response.InternalError(errors.Wrapf(err, "Invalid project limits.network value"))
		}

		networks, err := d.cluster.GetNetworks(projectName)
		if err != nil {
			return response.InternalError(errors.Wrapf(err, "Failed loading project's networks for limits check"))
		}

		// Only check network limits if the new network name doesn't exist already in networks list.
		// If it does then this create request will either be for adding a target node to an existing
		// pending network or it will fail anyway as it is a duplicate.
		if !shared.StringInSlice(req.Name, networks) && len(networks) >= networksLimit {
			return response.BadRequest(fmt.Errorf("Networks limit has been reached for project"))
		}
	}

	url := fmt.Sprintf("/%s/networks/%s", version.APIVersion, req.Name)
	resp := response.SyncResponseLocation(true, nil, url)

	clientType := request.UserAgentClientType(r.Header.Get("User-Agent"))

	if isClusterNotification(r) {
		n, err := network.LoadByName(d.State(), projectName, req.Name)
		if err != nil {
			return response.SmartError(err)
		}

		// This is an internal request which triggers the actual creation of the network across all nodes
		// after they have been previously defined.
		err = doNetworksCreate(d, n, clientType)
		if err != nil {
			return response.SmartError(err)
		}
		return resp
	}

	targetNode := queryParam(r, "target")
	if targetNode != "" {
		if !netTypeInfo.NodeSpecificConfig {
			return response.BadRequest(fmt.Errorf("Network type %q does not support node specific config", netType.Type()))
		}

		// A targetNode was specified, let's just define the node's network without actually creating it.
		// Check that only NodeSpecificNetworkConfig keys are specified.
		for key := range req.Config {
			if !shared.StringInSlice(key, db.NodeSpecificNetworkConfig) {
				return response.BadRequest(fmt.Errorf("Config key %q may not be used as node-specific key", key))
			}
		}

		err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
			return tx.CreatePendingNetwork(targetNode, projectName, req.Name, netType.DBType(), req.Config)
		})
		if err != nil {
			if err == db.ErrAlreadyDefined {
				return response.BadRequest(fmt.Errorf("The network is already defined on node %q", targetNode))
			}
			return response.SmartError(err)
		}

		return resp
	}

	// Load existing pool if exists, if not don't fail.
	_, netInfo, _, err := d.cluster.GetNetworkInAnyState(projectName, req.Name)
	if err != nil && err != db.ErrNoSuchObject {
		return response.InternalError(err)
	}

	// Check if we're clustered.
	count, err := cluster.Count(d.State())
	if err != nil {
		return response.SmartError(err)
	}

	// No targetNode was specified and we're clustered or there is an existing partially created single node
	// network, either way finalize the config in the db and actually create the network on all cluster nodes.
	if count > 1 || (netInfo != nil && netInfo.Status != api.NetworkStatusCreated) {
		// Simulate adding pending node network config when the driver doesn't support per-node config.
		if !netTypeInfo.NodeSpecificConfig && clientType != request.ClientTypeJoiner {
			// Create pending entry for each node.
			err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
				nodes, err := tx.GetNodes()
				if err != nil {
					return err
				}

				for _, node := range nodes {
					// Don't pass in any config, as these nodes don't have any node-specific
					// config and we don't want to create duplicate global config.
					err = tx.CreatePendingNetwork(node.Name, projectName, req.Name, netType.DBType(), nil)
					if err != nil && errors.Cause(err) != db.ErrAlreadyDefined {
						return errors.Wrapf(err, "Failed creating pending network for node %q", node.Name)
					}
				}

				return nil
			})
			if err != nil {
				return response.SmartError(err)
			}
		}

		err = networksPostCluster(d, projectName, netInfo, req, clientType, netType)
		if err != nil {
			return response.SmartError(err)
		}

		return resp
	}

	// Non-clustered network creation.
	if netInfo != nil {
		return response.BadRequest(fmt.Errorf("The network already exists"))
	}

	revert := revert.New()
	defer revert.Fail()

	// Populate default config.
	err = netType.FillConfig(req.Config)
	if err != nil {
		return response.SmartError(err)
	}

	// Create the database entry.
	_, err = d.cluster.CreateNetwork(projectName, req.Name, req.Description, netType.DBType(), req.Config)
	if err != nil {
		return response.SmartError(errors.Wrapf(err, "Error inserting %q into database", req.Name))
	}
	revert.Add(func() { d.cluster.DeleteNetwork(projectName, req.Name) })

	n, err := network.LoadByName(d.State(), projectName, req.Name)
	if err != nil {
		return response.SmartError(err)
	}

	err = doNetworksCreate(d, n, clientType)
	if err != nil {
		return response.SmartError(err)
	}

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
		if !shared.StringInSlice(key, db.NodeSpecificNetworkConfig) {
			return true
		}
	}

	return false
}

// networksPostCluster checks that there is a pending network in the database and then attempts to setup the
// network on each node. If all nodes are successfully setup then the network's state is set to created.
// Accepts an optional existing network record, which will exist when performing subsequent re-create attempts.
func networksPostCluster(d *Daemon, projectName string, netInfo *api.Network, req api.NetworksPost, clientType request.ClientType, netType network.Type) error {
	// Check that no node-specific config key has been supplied in request.
	for key := range req.Config {
		if shared.StringInSlice(key, db.NodeSpecificNetworkConfig) {
			return fmt.Errorf("Config key %q is node-specific", key)
		}
	}

	// If network already exists, perform quick checks.
	if netInfo != nil {
		// Check network isn't already created.
		if netInfo.Status == api.NetworkStatusCreated {
			return fmt.Errorf("The network is already created")
		}

		// Check the requested network type matches the type created when adding the local node config.
		if req.Type != netInfo.Type {
			return fmt.Errorf("Requested network type %q doesn't match type in existing database record %q", req.Type, netInfo.Type)
		}
	}

	// Check that the network is properly defined, get the node-specific configs and merge with global config.
	var nodeConfigs map[string]map[string]string
	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		// Check if any global config exists already, if so we should not create global config again.
		if netInfo != nil && networkPartiallyCreated(netInfo) {
			if len(req.Config) > 0 {
				return fmt.Errorf("Network already partially created. Please do not specify any global config when re-running create")
			}

			logger.Debug("Skipping global network create as global config already partially created", log.Ctx{"project": projectName, "network": req.Name})
			return nil
		}

		// Fetch the network ID.
		networkID, err := tx.GetNetworkID(projectName, req.Name)
		if err != nil {
			return err
		}

		// Fetch the node-specific configs and check the network is defined for all nodes.
		nodeConfigs, err = tx.NetworkNodeConfigs(networkID)
		if err != nil {
			return err
		}

		// Add default values if we are inserting global config for first time.
		err = netType.FillConfig(req.Config)
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
		if err == db.ErrNoSuchObject {
			return fmt.Errorf("Network not pending on any node (use --target <node> first)")
		}

		return err
	}

	// Create notifier for other nodes to create the network.
	notifier, err := cluster.NewNotifier(d.State(), d.endpoints.NetworkCert(), d.serverCert(), cluster.NotifyAll)
	if err != nil {
		return err
	}

	// Load the network from the database for the local node.
	n, err := network.LoadByName(d.State(), projectName, req.Name)
	if err != nil {
		return err
	}

	err = doNetworksCreate(d, n, clientType)
	if err != nil {
		return err
	}
	logger.Debug("Created network on local cluster member", log.Ctx{"project": projectName, "network": req.Name})

	// Notify other nodes to create the network.
	err = notifier(func(client lxd.InstanceServer) error {
		server, _, err := client.GetServer()
		if err != nil {
			return err
		}

		// Create fresh request based on existing network to send to node.
		nodeReq := api.NetworksPost{
			NetworkPut: api.NetworkPut{
				Config:      n.Config(),
				Description: n.Description(),
			},
			Name: n.Name(),
			Type: n.Type(),
		}

		// Remove node-specific config keys.
		for _, key := range db.NodeSpecificNetworkConfig {
			delete(nodeReq.Config, key)
		}

		// Merge node specific config items into global config.
		for key, value := range nodeConfigs[server.Environment.ServerName] {
			nodeReq.Config[key] = value
		}

		err = client.UseProject(n.Project()).CreateNetwork(nodeReq)
		if err != nil {
			return err
		}
		logger.Debug("Created network on cluster member", log.Ctx{"project": n.Project(), "network": n.Name(), "member": server.Environment.ServerName, "config": nodeReq.Config})

		return nil
	})
	if err != nil {
		return err
	}

	// Mark network global status as networkCreated now that all nodes have succeeded.
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		return tx.NetworkCreated(projectName, req.Name)
	})
	if err != nil {
		return err
	}
	logger.Debug("Marked network global status as created", log.Ctx{"project": projectName, "network": req.Name})

	return nil
}

// Create the network on the system. The clusterNotification flag is used to indicate whether creation request
// is coming from a cluster notification (and if so we should not delete the database record on error).
func doNetworksCreate(d *Daemon, n network.Network, clientType request.ClientType) error {
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
		logger.Debug("Skipping local network create as already created", log.Ctx{"project": n.Project(), "network": n.Name()})
		return nil
	}

	// Run initial creation setup for the network driver.
	err := n.Create(clientType)
	if err != nil {
		return err
	}

	revert.Add(func() { n.Delete(clientType) })

	// Only start networks when not doing a cluster pre-join phase (this ensures that networks are only started
	// once the node has fully joined the clustered database and has consistent config with rest of the nodes).
	if clientType != request.ClientTypeJoiner {
		err = n.Start()
		if err != nil {
			return err
		}
	}

	// Mark local as status as networkCreated.
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		return tx.NetworkNodeCreated(n.ID())
	})
	if err != nil {
		return err
	}
	logger.Debug("Marked network local status as created", log.Ctx{"project": n.Project(), "network": n.Name()})

	revert.Success()
	return nil
}

// swagger:operation GET /1.0/networks/{name} networks network_get
//
// Get the network
//
// Gets a specific network.
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
//   - in: query
//     name: target
//     description: Cluster member name
//     type: string
//     example: lxd01
// responses:
//   "200":
//     description: Network
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
//           $ref: "#/definitions/Network"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func networkGet(d *Daemon, r *http.Request) response.Response {
	// If a target was specified, forward the request to the relevant node.
	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	projectName, _, err := project.NetworkProject(d.State().Cluster, projectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	name := mux.Vars(r)["name"]

	n, err := doNetworkGet(d, r, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	targetNode := queryParam(r, "target")
	clustered, err := cluster.Enabled(d.db)
	if err != nil {
		return response.SmartError(err)
	}

	// If no target node is specified and the daemon is clustered, we omit the node-specific fields.
	if targetNode == "" && clustered {
		for _, key := range db.NodeSpecificNetworkConfig {
			delete(n.Config, key)
		}
	}

	etag := []interface{}{n.Name, n.Managed, n.Type, n.Description, n.Config}

	return response.SyncResponseETag(true, &n, etag)
}

func doNetworkGet(d *Daemon, r *http.Request, projectName string, name string) (api.Network, error) {
	// Ignore veth pairs (for performance reasons).
	if strings.HasPrefix(name, "veth") {
		return api.Network{}, os.ErrNotExist
	}

	// Get some information.
	_, dbInfo, _, _ := d.cluster.GetNetworkInAnyState(projectName, name)

	// Don't allow retrieving info about the local node interfaces when not using default project.
	if projectName != project.Default && dbInfo == nil {
		return api.Network{}, os.ErrNotExist
	}

	osInfo, _ := net.InterfaceByName(name)

	// Quick check.
	if osInfo == nil && dbInfo == nil {
		return api.Network{}, os.ErrNotExist
	}

	// Prepare the response.
	n := api.Network{}
	n.Name = name
	n.UsedBy = []string{}
	n.Config = map[string]string{}

	// Set the device type as needed.
	if dbInfo != nil {
		n.Managed = true
		n.Description = dbInfo.Description
		n.Config = dbInfo.Config
		n.Type = dbInfo.Type
	} else if osInfo != nil && shared.IsLoopback(osInfo) {
		n.Type = "loopback"
	} else if shared.PathExists(fmt.Sprintf("/sys/class/net/%s/bridge", n.Name)) {
		n.Type = "bridge"
	} else if shared.PathExists(fmt.Sprintf("/proc/net/vlan/%s", n.Name)) {
		n.Type = "vlan"
	} else if shared.PathExists(fmt.Sprintf("/sys/class/net/%s/device", n.Name)) {
		n.Type = "physical"
	} else if shared.PathExists(fmt.Sprintf("/sys/class/net/%s/bonding", n.Name)) {
		n.Type = "bond"
	} else {
		ovs := openvswitch.NewOVS()
		if exists, _ := ovs.BridgeExists(n.Name); exists {
			n.Type = "bridge"
		} else {
			n.Type = "unknown"
		}
	}

	// Look for instances using the interface.
	if n.Type != "loopback" {
		usedBy, err := network.UsedBy(d.State(), projectName, n.Name, false)
		if err != nil {
			return api.Network{}, err
		}

		n.UsedBy = project.FilterUsedBy(r, usedBy)
	}

	if dbInfo != nil {
		n.Status = dbInfo.Status
		n.Locations = dbInfo.Locations
	}

	return n, nil
}

// swagger:operation DELETE /1.0/networks/{name} networks network_delete
//
// Delete the network
//
// Removes the network.
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
func networkDelete(d *Daemon, r *http.Request) response.Response {
	projectName, _, err := project.NetworkProject(d.State().Cluster, projectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	name := mux.Vars(r)["name"]
	state := d.State()

	// Get the existing network.
	n, err := network.LoadByName(state, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	clientType := request.UserAgentClientType(r.Header.Get("User-Agent"))

	clusterNotification := isClusterNotification(r)
	if !clusterNotification {
		// Quick checks.
		inUse, err := n.IsUsed()
		if err != nil {
			return response.SmartError(err)
		}

		if inUse {
			return response.BadRequest(fmt.Errorf("The network is currently in use"))
		}
	}

	if n.LocalStatus() != api.NetworkStatusPending {
		err = n.Delete(clientType)
		if err != nil {
			return response.InternalError(err)
		}
	}

	// If this is a cluster notification, we're done, any database work will be done by the node that is
	// originally serving the request.
	if clusterNotification {
		return response.EmptySyncResponse
	}

	// If we are clustered, also notify all other nodes, if any.
	clustered, err := cluster.Enabled(d.db)
	if err != nil {
		return response.SmartError(err)
	}
	if clustered {
		notifier, err := cluster.NewNotifier(d.State(), d.endpoints.NetworkCert(), d.serverCert(), cluster.NotifyAll)
		if err != nil {
			return response.SmartError(err)
		}
		err = notifier(func(client lxd.InstanceServer) error {
			return client.UseProject(n.Project()).DeleteNetwork(n.Name())
		})
		if err != nil {
			return response.SmartError(err)
		}
	}

	// Remove the network from the database.
	err = d.State().Cluster.DeleteNetwork(n.Project(), n.Name())
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

// swagger:operation POST /1.0/networks/{name} networks network_post
//
// Rename the network
//
// Renames an existing network.
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
//     name: network
//     description: Network rename request
//     required: true
//     schema:
//       $ref: "#/definitions/NetworkPost"
// responses:
//   "200":
//     $ref: "#/responses/EmptySyncResponse"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func networkPost(d *Daemon, r *http.Request) response.Response {
	// FIXME: renaming a network is currently not supported in clustering
	//        mode. The difficulty is that network.Start() depends on the
	//        network having already been renamed in the database, which is
	//        a chicken-and-egg problem for cluster notifications (the
	//        serving node should typically do the database job, so the
	//        network is not yet renamed inthe db when the notified node
	//        runs network.Start).
	clustered, err := cluster.Enabled(d.db)
	if err != nil {
		return response.SmartError(err)
	}
	if clustered {
		return response.BadRequest(fmt.Errorf("Renaming clustered network not supported"))
	}

	name := mux.Vars(r)["name"]
	req := api.NetworkPost{}
	state := d.State()

	// Parse the request.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	projectName, _, err := project.NetworkProject(d.State().Cluster, projectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	// Get the existing network.
	n, err := network.LoadByName(state, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	if n.Status() != api.NetworkStatusCreated {
		return response.BadRequest(fmt.Errorf("Cannot rename network when not in created state"))
	}

	// Ensure new name is supplied.
	if req.Name == "" {
		return response.BadRequest(fmt.Errorf("New network name not provided"))
	}

	err = n.ValidateName(req.Name)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check network isn't in use.
	inUse, err := n.IsUsed()
	if err != nil {
		return response.InternalError(errors.Wrapf(err, "Failed checking network in use"))
	}

	if inUse {
		return response.BadRequest(fmt.Errorf("Network is currently in use"))
	}

	// Check that the name isn't already in used by an existing managed network.
	networks, err := d.cluster.GetNetworks(projectName)
	if err != nil {
		return response.InternalError(err)
	}

	if shared.StringInSlice(req.Name, networks) {
		return response.Conflict(fmt.Errorf("Network %q already exists", req.Name))
	}

	// Rename it.
	err = n.Rename(req.Name)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponseLocation(true, nil, fmt.Sprintf("/%s/networks/%s", version.APIVersion, req.Name))
}

// swagger:operation PUT /1.0/networks/{name} networks network_put
//
// Update the network
//
// Updates the entire network configuration.
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
//   - in: query
//     name: target
//     description: Cluster member name
//     type: string
//     example: lxd01
//   - in: body
//     name: network
//     description: Network configuration
//     required: true
//     schema:
//       $ref: "#/definitions/NetworkPut"
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
func networkPut(d *Daemon, r *http.Request) response.Response {
	// If a target was specified, forward the request to the relevant node.
	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	projectName, _, err := project.NetworkProject(d.State().Cluster, projectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	name := mux.Vars(r)["name"]

	// Get the existing network.
	n, err := network.LoadByName(d.State(), projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	targetNode := queryParam(r, "target")
	clustered, err := cluster.Enabled(d.db)
	if err != nil {
		return response.SmartError(err)
	}

	if targetNode == "" && n.Status() != api.NetworkStatusCreated {
		return response.BadRequest(fmt.Errorf("Cannot update network global config when not in created state"))
	}

	// Duplicate config for etag modification and generation.
	etagConfig := util.CopyConfig(n.Config())

	// If no target node is specified and the daemon is clustered, we omit the node-specific fields so that
	// the e-tag can be generated correctly. This is because the GET request used to populate the request
	// will also remove node-specific keys when no target is specified.
	if targetNode == "" && clustered {
		for _, key := range db.NodeSpecificNetworkConfig {
			delete(etagConfig, key)
		}
	}

	// Validate the ETag.
	etag := []interface{}{n.Name(), n.IsManaged(), n.Type(), n.Description(), etagConfig}
	err = util.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	// Decode the request.
	req := api.NetworkPut{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return response.BadRequest(err)
	}

	// In clustered mode, we differentiate between node specific and non-node specific config keys based on
	// whether the user has specified a target to apply the config to.
	if clustered {
		if targetNode == "" {
			// If no target is specified, then ensure only non-node-specific config keys are changed.
			for k := range req.Config {
				if shared.StringInSlice(k, db.NodeSpecificNetworkConfig) {
					return response.BadRequest(fmt.Errorf("Config key %q is node-specific", k))
				}
			}
		} else {
			curConfig := n.Config()

			// If a target is specified, then ensure only node-specific config keys are changed.
			for k, v := range req.Config {
				if !shared.StringInSlice(k, db.NodeSpecificNetworkConfig) && curConfig[k] != v {
					return response.BadRequest(fmt.Errorf("Config key %q may not be used as node-specific key", k))
				}
			}
		}
	}

	clientType := request.UserAgentClientType(r.Header.Get("User-Agent"))

	return doNetworkUpdate(d, projectName, n, req, targetNode, clientType, r.Method, clustered)
}

// swagger:operation PATCH /1.0/networks/{name} networks network_patch
//
// Partially update the network
//
// Updates a subset of the network configuration.
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
//   - in: query
//     name: target
//     description: Cluster member name
//     type: string
//     example: lxd01
//   - in: body
//     name: network
//     description: Network configuration
//     required: true
//     schema:
//       $ref: "#/definitions/NetworkPut"
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
func networkPatch(d *Daemon, r *http.Request) response.Response {
	return networkPut(d, r)
}

// doNetworkUpdate loads the current local network config, merges with the requested network config, validates
// and applies the changes. Will also notify other cluster nodes of non-node specific config if needed.
func doNetworkUpdate(d *Daemon, projectName string, n network.Network, req api.NetworkPut, targetNode string, clientType request.ClientType, httpMethod string, clustered bool) response.Response {
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
			if shared.StringInSlice(k, db.NodeSpecificNetworkConfig) {
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
// Get the DHCP leases
//
// Returns a list of DHCP leases for the network.
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
//   - in: query
//     name: target
//     description: Cluster member name
//     type: string
//     example: lxd01
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
//           description: List of DHCP leases
//           items:
//             $ref: "#/definitions/NetworkLease"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func networkLeasesGet(d *Daemon, r *http.Request) response.Response {
	// The project we are filtering the instance leases by.
	instProjectName := projectParam(r)

	// The project we should use the load the network.
	networkProjectName, _, err := project.NetworkProject(d.State().Cluster, instProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	name := mux.Vars(r)["name"]

	// Try to get the network.
	n, err := doNetworkGet(d, r, networkProjectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	// Validate that we do have leases for it.
	if !n.Managed || n.Type != "bridge" {
		return response.NotFound(errors.New("Leases not found"))
	}

	leases := []api.NetworkLease{}
	projectMacs := []string{}

	// Get all static leases.
	if !isClusterNotification(r) {
		// Get all the instances.
		instances, err := instance.LoadByProject(d.State(), instProjectName)
		if err != nil {
			return response.SmartError(err)
		}

		for _, inst := range instances {
			// Go through all its devices (including profiles).
			for k, dev := range inst.ExpandedDevices() {
				// Skip uninteresting entries.
				if dev["type"] != "nic" {
					continue
				}

				nicType, err := nictype.NICType(d.State(), inst.Project(), dev)
				if err != nil || nicType != "bridged" {
					continue
				}

				// Temporarily populate parent from network setting if used.
				if dev["network"] != "" {
					dev["parent"] = dev["network"]
				}

				if dev["parent"] != name {
					continue
				}

				// Fill in the hwaddr from volatile.
				if dev["hwaddr"] == "" {
					dev["hwaddr"] = inst.LocalConfig()[fmt.Sprintf("volatile.%s.hwaddr", k)]
				}

				// Record the MAC.
				if dev["hwaddr"] != "" {
					projectMacs = append(projectMacs, dev["hwaddr"])
				}

				// Add the lease.
				if dev["ipv4.address"] != "" {
					leases = append(leases, api.NetworkLease{
						Hostname: inst.Name(),
						Address:  dev["ipv4.address"],
						Hwaddr:   dev["hwaddr"],
						Type:     "static",
						Location: inst.Location(),
					})
				}

				if dev["ipv6.address"] != "" {
					leases = append(leases, api.NetworkLease{
						Hostname: inst.Name(),
						Address:  dev["ipv6.address"],
						Hwaddr:   dev["hwaddr"],
						Type:     "static",
						Location: inst.Location(),
					})
				}
			}
		}
	}

	// Local server name.
	var serverName string
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		serverName, err = tx.GetLocalNodeName()
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Get dynamic leases.
	leaseFile := shared.VarPath("networks", name, "dnsmasq.leases")
	if !shared.PathExists(leaseFile) {
		return response.SyncResponse(true, leases)
	}

	content, err := ioutil.ReadFile(leaseFile)
	if err != nil {
		return response.SmartError(err)
	}

	for _, lease := range strings.Split(string(content), "\n") {
		fields := strings.Fields(lease)
		if len(fields) >= 5 {
			// Parse the MAC.
			mac := network.GetMACSlice(fields[1])
			macStr := strings.Join(mac, ":")

			if len(macStr) < 17 && fields[4] != "" {
				macStr = fields[4][len(fields[4])-17:]
			}

			// Look for an existing static entry.
			found := false
			for _, entry := range leases {
				if entry.Hwaddr == macStr && entry.Address == fields[2] {
					found = true
					break
				}
			}

			if found {
				continue
			}

			// Add the lease to the list.
			leases = append(leases, api.NetworkLease{
				Hostname: fields[3],
				Address:  fields[2],
				Hwaddr:   macStr,
				Type:     "dynamic",
				Location: serverName,
			})
		}
	}

	// Collect leases from other servers.
	if !isClusterNotification(r) {
		notifier, err := cluster.NewNotifier(d.State(), d.endpoints.NetworkCert(), d.serverCert(), cluster.NotifyAlive)
		if err != nil {
			return response.SmartError(err)
		}

		err = notifier(func(client lxd.InstanceServer) error {
			memberLeases, err := client.GetNetworkLeases(name)
			if err != nil {
				return err
			}

			leases = append(leases, memberLeases...)
			return nil
		})
		if err != nil {
			return response.SmartError(err)
		}

		// Filter based on project.
		filteredLeases := []api.NetworkLease{}
		for _, lease := range leases {
			if !shared.StringInSlice(lease.Hwaddr, projectMacs) {
				continue
			}

			filteredLeases = append(filteredLeases, lease)
		}

		leases = filteredLeases
	}

	return response.SyncResponse(true, leases)
}

func networkStartup(s *state.State) error {
	var err error

	// Get a list of projects.
	var projectNames []string

	err = s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		projectNames, err = tx.GetProjectNames()
		return err
	})
	if err != nil {
		return errors.Wrapf(err, "Failed to load projects")
	}

	// Record of networks that need to be started later keyed on project name.
	deferredNetworks := make(map[string][]network.Network)

	for _, projectName := range projectNames {
		deferredNetworks[projectName] = make([]network.Network, 0)

		// Get a list of managed networks.
		networks, err := s.Cluster.GetCreatedNetworks(projectName)
		if err != nil {
			return errors.Wrapf(err, "Failed to load networks for project %q", projectName)
		}

		// Bring them all up.
		for _, name := range networks {
			n, err := network.LoadByName(s, projectName, name)
			if err != nil {
				return errors.Wrapf(err, "Failed to load network %q in project %q", name, projectName)
			}

			netConfig := n.Config()
			err = n.Validate(netConfig)
			if err != nil {
				// Don't cause LXD to fail to start entirely on network start up failure.
				logger.Error("Failed to validate network", log.Ctx{"err": err, "project": projectName, "name": name})
				continue
			}

			// Defer network start until after non-dependent networks.
			if netConfig["network"] != "" {
				deferredNetworks[projectName] = append(deferredNetworks[projectName], n)
				continue
			}

			err = n.Start()
			if err != nil {
				// Don't cause LXD to fail to start entirely on network start up failure.
				logger.Error("Failed to bring up network", log.Ctx{"err": err, "project": projectName, "name": name})
				continue
			}
		}
	}

	// Bring up deferred networks after non-dependent networks have been started.
	for projectName, networks := range deferredNetworks {
		for _, n := range networks {
			err = n.Start()
			if err != nil {
				// Don't cause LXD to fail to start entirely on network start up failure.
				logger.Error("Failed to bring up network", log.Ctx{"err": err, "project": projectName, "name": n.Name()})
				continue
			}
		}
	}

	return nil
}

func networkShutdown(s *state.State) error {
	var err error

	// Get a list of projects.
	var projectNames []string

	err = s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		projectNames, err = tx.GetProjectNames()
		return err
	})
	if err != nil {
		return errors.Wrapf(err, "Failed to load projects")
	}

	for _, projectName := range projectNames {
		// Get a list of managed networks.
		networks, err := s.Cluster.GetNetworks(projectName)
		if err != nil {
			return errors.Wrapf(err, "Failed to load networks for project %q", projectName)
		}

		// Bring them all down.
		for _, name := range networks {
			n, err := network.LoadByName(s, projectName, name)
			if err != nil {
				return errors.Wrapf(err, "Failed to load network %q in project %q", name, projectName)
			}

			err = n.Stop()
			if err != nil {
				logger.Error("Failed to bring down network", log.Ctx{"err": err, "project": projectName, "name": name})
			}
		}
	}

	return nil
}

// swagger:operation GET /1.0/networks/{name}/state networks networks_state_get
//
// Get the network state
//
// Returns the current network state information.
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
//   - in: query
//     name: target
//     description: Cluster member name
//     type: string
//     example: lxd01
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
//           $ref: "#/definitions/NetworkState"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"

func networkStateGet(d *Daemon, r *http.Request) response.Response {
	// If a target was specified, forward the request to the relevant node.
	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	name := mux.Vars(r)["name"]

	state, err := resources.GetNetworkState(name)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, state)
}
