package main

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/pkg/errors"
	log "gopkg.in/inconshreveable/log15.v2"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/cluster"
	clusterRequest "github.com/lxc/lxd/lxd/cluster/request"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/drivers"
	"github.com/lxc/lxd/lxd/lifecycle"
	"github.com/lxc/lxd/lxd/node"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/request"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/osarch"
	"github.com/lxc/lxd/shared/validate"
	"github.com/lxc/lxd/shared/version"
)

var targetGroupPrefix = "@"

var clusterCmd = APIEndpoint{
	Path: "cluster",

	Get: APIEndpointAction{Handler: clusterGet, AccessHandler: allowAuthenticated},
	Put: APIEndpointAction{Handler: clusterPut},
}

var clusterNodesCmd = APIEndpoint{
	Path: "cluster/members",

	Get:  APIEndpointAction{Handler: clusterNodesGet, AccessHandler: allowAuthenticated},
	Post: APIEndpointAction{Handler: clusterNodesPost},
}

var clusterNodeCmd = APIEndpoint{
	Path: "cluster/members/{name}",

	Delete: APIEndpointAction{Handler: clusterNodeDelete},
	Get:    APIEndpointAction{Handler: clusterNodeGet, AccessHandler: allowAuthenticated},
	Patch:  APIEndpointAction{Handler: clusterNodePatch},
	Put:    APIEndpointAction{Handler: clusterNodePut},
	Post:   APIEndpointAction{Handler: clusterNodePost},
}

var clusterNodeStateCmd = APIEndpoint{
	Path: "cluster/members/{name}/state",

	Post: APIEndpointAction{Handler: clusterNodeStatePost},
}

var clusterCertificateCmd = APIEndpoint{
	Path: "cluster/certificate",

	Put: APIEndpointAction{Handler: clusterCertificatePut},
}

var clusterGroupsCmd = APIEndpoint{
	Path: "cluster/groups",

	Get:  APIEndpointAction{Handler: clusterGroupsGet, AccessHandler: allowAuthenticated},
	Post: APIEndpointAction{Handler: clusterGroupsPost, AccessHandler: allowAuthenticated},
}

var clusterGroupCmd = APIEndpoint{
	Path: "cluster/groups/{name}",

	Get:    APIEndpointAction{Handler: clusterGroupGet, AccessHandler: allowAuthenticated},
	Post:   APIEndpointAction{Handler: clusterGroupPost},
	Put:    APIEndpointAction{Handler: clusterGroupPut},
	Patch:  APIEndpointAction{Handler: clusterGroupPatch},
	Delete: APIEndpointAction{Handler: clusterGroupDelete},
}

var internalClusterAcceptCmd = APIEndpoint{
	Path: "cluster/accept",

	Post: APIEndpointAction{Handler: internalClusterPostAccept},
}

var internalClusterRebalanceCmd = APIEndpoint{
	Path: "cluster/rebalance",

	Post: APIEndpointAction{Handler: internalClusterPostRebalance},
}

var internalClusterAssignCmd = APIEndpoint{
	Path: "cluster/assign",

	Post: APIEndpointAction{Handler: internalClusterPostAssign},
}

var internalClusterHandoverCmd = APIEndpoint{
	Path: "cluster/handover",

	Post: APIEndpointAction{Handler: internalClusterPostHandover},
}

var internalClusterRaftNodeCmd = APIEndpoint{
	Path: "cluster/raft-node/{address}",

	Delete: APIEndpointAction{Handler: internalClusterRaftNodeDelete},
}

// swagger:operation GET /1.0/cluster cluster cluster_get
//
// Get the cluster configuration
//
// Gets the current cluster configuration.
//
// ---
// produces:
//   - application/json
// responses:
//   "200":
//     description: Cluster configuration
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
//           $ref: "#/definitions/Cluster"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func clusterGet(d *Daemon, r *http.Request) response.Response {
	name := ""
	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		name, err = tx.GetLocalNodeName()
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	// If the name is set to the hard-coded default node name, then
	// clustering is not enabled.
	if name == "none" {
		name = ""
	}

	memberConfig, err := clusterGetMemberConfig(d.cluster)
	if err != nil {
		return response.SmartError(err)
	}

	cluster := api.Cluster{
		ServerName:   name,
		Enabled:      name != "",
		MemberConfig: memberConfig,
	}

	return response.SyncResponseETag(true, cluster, cluster)
}

// Fetch information about all node-specific configuration keys set on the
// storage pools and networks of this cluster.
func clusterGetMemberConfig(cluster *db.Cluster) ([]api.ClusterMemberConfigKey, error) {
	var pools map[string]map[string]string
	var networks map[string]map[string]string

	keys := []api.ClusterMemberConfigKey{}

	err := cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error

		pools, err = tx.GetStoragePoolsLocalConfig()
		if err != nil {
			return errors.Wrapf(err, "Failed to fetch storage pools configuration")
		}

		networks, err = tx.GetNetworksLocalConfig()
		if err != nil {
			return errors.Wrapf(err, "Failed to fetch networks configuration")
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	for pool, config := range pools {
		for key := range config {
			if strings.HasPrefix(key, shared.ConfigVolatilePrefix) {
				continue
			}

			key := api.ClusterMemberConfigKey{
				Entity:      "storage-pool",
				Name:        pool,
				Key:         key,
				Description: fmt.Sprintf("\"%s\" property for storage pool \"%s\"", key, pool),
			}
			keys = append(keys, key)
		}
	}

	for network, config := range networks {
		for key := range config {
			if strings.HasPrefix(key, shared.ConfigVolatilePrefix) {
				continue
			}

			key := api.ClusterMemberConfigKey{
				Entity:      "network",
				Name:        network,
				Key:         key,
				Description: fmt.Sprintf("\"%s\" property for network \"%s\"", key, network),
			}
			keys = append(keys, key)
		}
	}

	return keys, nil
}

// Depending on the parameters passed and on local state this endpoint will
// either:
//
// - bootstrap a new cluster (if this node is not clustered yet)
// - request to join an existing cluster
// - disable clustering on a node
//
// The client is required to be trusted.

// swagger:operation PUT /1.0/cluster cluster cluster_put
//
// Update the cluster configuration
//
// Updates the entire cluster configuration.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: body
//     name: cluster
//     description: Cluster configuration
//     required: true
//     schema:
//       $ref: "#/definitions/ClusterPut"
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
func clusterPut(d *Daemon, r *http.Request) response.Response {
	req := api.ClusterPut{}

	// Parse the request
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
	if req.ServerName == "" && req.Enabled {
		return response.BadRequest(fmt.Errorf("ServerName is required when enabling clustering"))
	}
	if req.ServerName != "" && !req.Enabled {
		return response.BadRequest(fmt.Errorf("ServerName must be empty when disabling clustering"))
	}

	if req.ServerName != "" && strings.HasPrefix(req.ServerName, targetGroupPrefix) {
		return response.BadRequest(fmt.Errorf("ServerName may not start with %q", targetGroupPrefix))
	}

	// Disable clustering.
	if !req.Enabled {
		return clusterPutDisable(d, r, req)
	}

	// Depending on the provided parameters we either bootstrap a brand new
	// cluster with this node as first node, or perform a request to join a
	// given cluster.
	if req.ClusterAddress == "" {
		return clusterPutBootstrap(d, r, req)
	}

	return clusterPutJoin(d, r, req)
}

func clusterPutBootstrap(d *Daemon, r *http.Request, req api.ClusterPut) response.Response {
	logger.Info("Bootstrapping cluster", log.Ctx{"serverName": req.ServerName})

	run := func(op *operations.Operation) error {
		// Start clustering tasks
		d.startClusterTasks()

		err := cluster.Bootstrap(d.State(), d.gateway, req.ServerName)
		if err != nil {
			d.stopClusterTasks()
			return err
		}

		d.State().Events.SendLifecycle(projectParam(r), lifecycle.ClusterEnabled.Event(req.ServerName, op.Requestor(), nil))

		return nil
	}
	resources := map[string][]string{}
	resources["cluster"] = []string{}

	// If there's no cluster.https_address set, but core.https_address is,
	// let's default to it.
	err := d.db.Transaction(func(tx *db.NodeTx) error {
		config, err := node.ConfigLoad(tx)
		if err != nil {
			return errors.Wrap(err, "Failed to fetch member configuration")
		}

		clusterAddress := config.ClusterAddress()
		if clusterAddress != "" {
			return nil
		}

		address := config.HTTPSAddress()

		if util.IsWildCardAddress(address) {
			return fmt.Errorf("Cannot use wildcard core.https_address %q for cluster.https_address. Please specify a new cluster.https_address or core.https_address", address)
		}

		_, err = config.Patch(map[string]interface{}{
			"cluster.https_address": address,
		})
		if err != nil {
			return errors.Wrap(err, "Copy core.https_address to cluster.https_address")
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	op, err := operations.OperationCreate(d.State(), "", operations.OperationClassTask, db.OperationClusterBootstrap, resources, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	// Add the cluster flag from the agent
	version.UserAgentFeatures([]string{"cluster"})

	return operations.OperationResponse(op)
}

func clusterPutJoin(d *Daemon, r *http.Request, req api.ClusterPut) response.Response {
	logger.Info("Joining cluster", log.Ctx{"serverName": req.ServerName})

	// Make sure basic pre-conditions are met.
	if len(req.ClusterCertificate) == 0 {
		return response.BadRequest(fmt.Errorf("No target cluster member certificate provided"))
	}

	clustered, err := cluster.Enabled(d.db)
	if err != nil {
		return response.SmartError(err)
	}

	if clustered {
		return response.BadRequest(fmt.Errorf("This server is already clustered"))
	}

	// The old pre 'clustering_join' join API approach is no longer supported.
	if req.ServerAddress == "" {
		return response.BadRequest(fmt.Errorf("No server address provided for this member"))
	}

	address, err := node.HTTPSAddress(d.db)
	if err != nil {
		return response.SmartError(err)
	}

	if address == "" {
		// As the user always provides a server address, but no networking
		// was setup on this node, let's do the job and open the
		// port. We'll use the same address both for the REST API and
		// for clustering.

		// First try to listen to the provided address. If we fail, we
		// won't actually update the database config.
		err = d.endpoints.NetworkUpdateAddress(req.ServerAddress)
		if err != nil {
			return response.SmartError(err)
		}

		err := d.db.Transaction(func(tx *db.NodeTx) error {
			config, err := node.ConfigLoad(tx)
			if err != nil {
				return errors.Wrap(err, "Failed to load cluster config")
			}

			_, err = config.Patch(map[string]interface{}{
				"core.https_address":    req.ServerAddress,
				"cluster.https_address": req.ServerAddress,
			})
			return err
		})
		if err != nil {
			return response.SmartError(err)
		}

		address = req.ServerAddress
	} else {
		// The user has previously set core.https_address and
		// is now providing a cluster address as well. If they
		// differ we need to listen to it.
		if !util.IsAddressCovered(req.ServerAddress, address) {
			err := d.endpoints.ClusterUpdateAddress(req.ServerAddress)
			if err != nil {
				return response.SmartError(err)
			}
			address = req.ServerAddress
		}

		// Update the cluster.https_address config key.
		err := d.db.Transaction(func(tx *db.NodeTx) error {
			config, err := node.ConfigLoad(tx)
			if err != nil {
				return errors.Wrap(err, "Failed to load cluster config")
			}
			_, err = config.Patch(map[string]interface{}{
				"cluster.https_address": address,
			})
			return err
		})
		if err != nil {
			return response.SmartError(err)
		}
	}

	// Client parameters to connect to the target cluster node.
	serverCert := d.serverCert()
	args := &lxd.ConnectionArgs{
		TLSClientCert: string(serverCert.PublicKey()),
		TLSClientKey:  string(serverCert.PrivateKey()),
		TLSServerCert: string(req.ClusterCertificate),
		UserAgent:     version.UserAgent,
	}

	// Asynchronously join the cluster.
	run := func(op *operations.Operation) error {
		logger.Debug("Running cluster join operation")

		// If the user has provided a cluster password, setup the trust
		// relationship by adding our own certificate to the cluster.
		if req.ClusterPassword != "" {
			err = cluster.SetupTrust(serverCert, req.ServerName, req.ClusterAddress, req.ClusterCertificate, req.ClusterPassword)
			if err != nil {
				return errors.Wrap(err, "Failed to setup cluster trust")
			}
		}

		// Now we are in the remote trust store, ensure our name and type are correct to allow the cluster
		// to associate our member name to the server certificate.
		err = cluster.UpdateTrust(serverCert, req.ServerName, req.ClusterAddress, req.ClusterCertificate)
		if err != nil {
			return errors.Wrap(err, "Failed to update cluster trust")
		}

		// Connect to the target cluster node.
		client, err := lxd.ConnectLXD(fmt.Sprintf("https://%s", req.ClusterAddress), args)
		if err != nil {
			return err
		}

		// As ServerAddress field is required to be set it means that we're using the new join API
		// introduced with the 'clustering_join' extension.
		// Connect to ourselves to initialize storage pools and networks using the API.
		localClient, err := lxd.ConnectLXDUnix(d.UnixSocket(), &lxd.ConnectionArgs{UserAgent: clusterRequest.UserAgentJoiner})
		if err != nil {
			return errors.Wrap(err, "Failed to connect to local LXD")
		}

		revert := revert.New()
		defer revert.Fail()

		localRevert, err := clusterInitMember(localClient, client, req.MemberConfig)
		if err != nil {
			return errors.Wrap(err, "Failed to initialize member")
		}
		revert.Add(localRevert)

		// Get all defined storage pools and networks, so they can be compared to the ones in the cluster.
		pools := []api.StoragePool{}
		poolNames, err := d.cluster.GetStoragePoolNames()
		if err != nil && err != db.ErrNoSuchObject {
			return err
		}

		for _, name := range poolNames {
			_, pool, _, err := d.cluster.GetStoragePoolInAnyState(name)
			if err != nil {
				return err
			}
			pools = append(pools, *pool)
		}

		// Get a list of projects for networks.
		var projects []db.Project

		err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
			projects, err = tx.GetProjects(db.ProjectFilter{})
			return err
		})
		if err != nil {
			return errors.Wrapf(err, "Failed to load projects for networks")
		}

		networks := []internalClusterPostNetwork{}
		for _, p := range projects {
			networkNames, err := d.cluster.GetNetworks(p.Name)
			if err != nil && err != db.ErrNoSuchObject {
				return err
			}

			for _, name := range networkNames {
				_, network, _, err := d.cluster.GetNetworkInAnyState(p.Name, name)
				if err != nil {
					return err
				}

				internalNetwork := internalClusterPostNetwork{
					NetworksPost: api.NetworksPost{
						NetworkPut: network.NetworkPut,
						Name:       network.Name,
						Type:       network.Type,
					},
					Project: p.Name,
				}

				networks = append(networks, internalNetwork)
			}
		}

		// Now request for this node to be added to the list of cluster nodes.
		info, err := clusterAcceptMember(client, req.ServerName, address, cluster.SchemaVersion, version.APIExtensionsCount(), pools, networks)
		if err != nil {
			return errors.Wrap(err, "Failed request to add member")
		}

		// Update our TLS configuration using the returned cluster certificate.
		err = util.WriteCert(d.os.VarDir, "cluster", []byte(req.ClusterCertificate), info.PrivateKey, nil)
		if err != nil {
			return errors.Wrap(err, "Failed to save cluster certificate")
		}

		networkCert, err := util.LoadClusterCert(d.os.VarDir)
		if err != nil {
			return errors.Wrap(err, "Failed to parse cluster certificate")
		}

		d.endpoints.NetworkUpdateCert(networkCert)

		// Add trusted certificates of other members to local trust store.
		trustedCerts, err := client.GetCertificates()
		if err != nil {
			return errors.Wrap(err, "Failed to get trusted certificates")
		}

		for _, trustedCert := range trustedCerts {
			if trustedCert.Type == api.CertificateTypeServer {
				dbType, err := db.CertificateAPITypeToDBType(trustedCert.Type)
				if err != nil {
					return err
				}

				// Store the certificate in the local database.
				dbCert := db.Certificate{
					Fingerprint: trustedCert.Fingerprint,
					Type:        dbType,
					Name:        trustedCert.Name,
					Certificate: trustedCert.Certificate,
					Restricted:  trustedCert.Restricted,
				}

				logger.Debugf("Adding certificate %q (%s) to local trust store", trustedCert.Name, trustedCert.Fingerprint)

				err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
					_, err = tx.CreateCertificate(dbCert)
					if err != nil {
						return err
					}

					projects := make([]db.Project, len(trustedCert.Projects))

					for i, p := range trustedCert.Projects {
						project, err := tx.GetProject(p)
						if err != nil {
							return err
						}

						projects[i] = *project
					}
					err = tx.UpdateCertificateProjects(dbCert, projects)
					if err != nil {
						return err
					}

					return nil
				})
				if err != nil && err.Error() != "This certificate already exists" {
					return errors.Wrapf(err, "Failed adding local trusted certificate %q (%s)", trustedCert.Name, trustedCert.Fingerprint)
				}
			}
		}

		// Update cached trusted certificates.
		updateCertificateCache(d)

		// Update local setup and possibly join the raft dqlite cluster.
		nodes := make([]db.RaftNode, len(info.RaftNodes))
		for i, node := range info.RaftNodes {
			nodes[i].ID = node.ID
			nodes[i].Address = node.Address
			nodes[i].Role = db.RaftRole(node.Role)
		}

		err = cluster.Join(d.State(), d.gateway, networkCert, serverCert, req.ServerName, nodes)
		if err != nil {
			return err
		}

		// Start clustering tasks.
		d.startClusterTasks()
		revert.Add(func() { d.stopClusterTasks() })

		// Handle optional service integration on cluster join
		var clusterConfig *cluster.Config

		err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
			var err error

			clusterConfig, err = cluster.ConfigLoad(tx)
			if err != nil {
				return err
			}

			// Add the new node to the default cluster group.
			err = tx.AddNodeToClusterGroup("default", req.ServerName)
			if err != nil {
				return fmt.Errorf("Failed to add new member to the default cluster group: %w", err)
			}

			return nil
		})
		if err != nil {
			return err
		}

		var nodeConfig *node.Config
		err = d.db.Transaction(func(tx *db.NodeTx) error {
			var err error
			nodeConfig, err = node.ConfigLoad(tx)
			return err
		})
		if err != nil {
			return err
		}

		// Connect to MAAS
		url, key := clusterConfig.MAASController()
		machine := nodeConfig.MAASMachine()
		err = d.setupMAASController(url, key, machine)
		if err != nil {
			return err
		}

		// Handle external authentication/RBAC
		candidAPIURL, candidAPIKey, candidExpiry, candidDomains := clusterConfig.CandidServer()
		rbacAPIURL, rbacAPIKey, rbacExpiry, rbacAgentURL, rbacAgentUsername, rbacAgentPrivateKey, rbacAgentPublicKey := clusterConfig.RBACServer()

		if rbacAPIURL != "" {
			err = d.setupRBACServer(rbacAPIURL, rbacAPIKey, rbacExpiry, rbacAgentURL, rbacAgentUsername, rbacAgentPrivateKey, rbacAgentPublicKey)
			if err != nil {
				return err
			}
		}

		if candidAPIURL != "" {
			err = d.setupExternalAuthentication(candidAPIURL, candidAPIKey, candidExpiry, candidDomains)
			if err != nil {
				return err
			}
		}

		// Start up networks so any post-join changes can be applied now that we have a Node ID.
		logger.Debug("Starting networks after cluster join")
		err = networkStartup(d.State())
		if err != nil {
			logger.Errorf("Failed starting networks: %v", err)
		}

		client, err = cluster.Connect(req.ClusterAddress, d.endpoints.NetworkCert(), serverCert, r, true)
		if err != nil {
			return err
		}

		// Add the cluster flag from the agent
		version.UserAgentFeatures([]string{"cluster"})

		// Notify the leader of successful join, possibly triggering
		// role changes.
		_, _, err = client.RawQuery("POST", "/internal/cluster/rebalance", nil, "")
		if err != nil {
			logger.Warnf("Failed to trigger cluster rebalance: %v", err)
		}

		// Ensure all images are available after this node has joined.
		err = autoSyncImages(d.shutdownCtx, d)
		if err != nil {
			logger.Warn("Failed to sync images")
		}

		d.State().Events.SendLifecycle(projectParam(r), lifecycle.ClusterMemberAdded.Event(req.ServerName, op.Requestor(), nil))

		revert.Success()
		return nil
	}

	resources := map[string][]string{}
	resources["cluster"] = []string{}

	op, err := operations.OperationCreate(d.State(), "", operations.OperationClassTask, db.OperationClusterJoin, resources, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// clusterPutDisableMu is used to prevent the LXD process from being replaced/stopped during removal from the
// cluster until such time as the request that initiated the removal has finished. This allows for self removal
// from the cluster when not the leader.
var clusterPutDisableMu sync.Mutex

// Disable clustering on a node.
func clusterPutDisable(d *Daemon, r *http.Request, req api.ClusterPut) response.Response {
	logger.Info("Disabling clustering", log.Ctx{"serverName": req.ServerName})

	// Close the cluster database
	err := d.cluster.Close()
	if err != nil {
		return response.SmartError(err)
	}

	// Update our TLS configuration using our original certificate.
	for _, suffix := range []string{"crt", "key", "ca"} {
		path := filepath.Join(d.os.VarDir, "cluster."+suffix)
		if !shared.PathExists(path) {
			continue
		}
		err := os.Remove(path)
		if err != nil {
			return response.InternalError(err)
		}
	}

	networkCert, err := util.LoadCert(d.os.VarDir)
	if err != nil {
		return response.InternalError(errors.Wrap(err, "Failed to parse member certificate"))
	}

	// Reset the cluster database and make it local to this node.
	err = d.gateway.Reset(networkCert)
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r)
	d.State().Events.SendLifecycle(projectParam(r), lifecycle.ClusterDisabled.Event(req.ServerName, requestor, nil))

	// Stop database cluster connection.
	d.gateway.Kill()

	go func() {
		<-r.Context().Done() // Wait until request has finished.

		// Wait until we can acquire the lock. This way if another request is holding the lock we won't
		// replace/stop the LXD daemon until that request has finished.
		clusterPutDisableMu.Lock()
		defer clusterPutDisableMu.Unlock()

		if d.systemdSocketActivated {
			logger.Info("Exiting LXD daemon following removal from cluster")
			os.Exit(0)
		} else {
			logger.Info("Restarting LXD daemon following removal from cluster")
			err = util.ReplaceDaemon()
			if err != nil {
				logger.Error("Failed restarting LXD daemon", log.Ctx{"err": err})
			}
		}
	}()

	return response.ManualResponse(func(w http.ResponseWriter) error {
		err := response.EmptySyncResponse.Render(w)
		if err != nil {
			return err
		}

		// Send the response before replacing the LXD daemon process.
		f, ok := w.(http.Flusher)
		if ok {
			f.Flush()
		} else {
			return fmt.Errorf("http.ResponseWriter is not type http.Flusher")
		}

		return nil
	})
}

// clusterInitMember initialises storage pools and networks on this node. We pass two LXD client instances, one
// connected to ourselves (the joining node) and one connected to the target cluster node to join.
// Returns a revert function that can be used to undo the setup if a subsequent step fails.
func clusterInitMember(d lxd.InstanceServer, client lxd.InstanceServer, memberConfig []api.ClusterMemberConfigKey) (func(), error) {
	data := initDataNode{}

	// Fetch all pools currently defined in the cluster.
	pools, err := client.GetStoragePools()
	if err != nil {
		return nil, errors.Wrap(err, "Failed to fetch information about cluster storage pools")
	}

	// Merge the returned storage pools configs with the node-specific
	// configs provided by the user.
	for _, pool := range pools {
		// Skip pending pools.
		if pool.Status == "Pending" {
			continue
		}

		logger.Debugf("Populating init data for storage pool %q", pool.Name)

		post := api.StoragePoolsPost{
			StoragePoolPut: pool.StoragePoolPut,
			Driver:         pool.Driver,
			Name:           pool.Name,
		}

		// Delete config keys that are automatically populated by LXD
		delete(post.Config, "volatile.initial_source")
		delete(post.Config, "zfs.pool_name")

		// Apply the node-specific config supplied by the user.
		for _, config := range memberConfig {
			if config.Entity != "storage-pool" {
				continue
			}

			if config.Name != pool.Name {
				continue
			}

			if !shared.StringInSlice(config.Key, db.StoragePoolNodeConfigKeys) {
				logger.Warnf("Ignoring config key %q for storage pool %q", config.Key, config.Name)
				continue
			}

			post.Config[config.Key] = config.Value
		}

		data.StoragePools = append(data.StoragePools, post)
	}

	projects, err := client.GetProjects()
	if err != nil {
		return nil, errors.Wrap(err, "Failed to fetch project information about cluster networks")
	}

	for _, p := range projects {
		if !shared.IsTrue(p.Config["features.networks"]) && p.Name != project.Default {
			// Skip non-default projects that can't have their own networks so we don't try
			// and add the same default project networks twice.
			continue
		}

		// Fetch all networks currently defined in the cluster for the project.
		networks, err := client.UseProject(p.Name).GetNetworks()
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to fetch network information about cluster networks in project %q", p.Name)
		}

		if len(networks) > 0 {
			// Ensure project exists locally and has same config as cluster project.
			_, localProjectEtag, err := d.GetProject(p.Name)
			if err != nil {
				err = d.CreateProject(api.ProjectsPost{
					Name: p.Name,
					ProjectPut: api.ProjectPut{
						Description: p.Description,
						Config:      p.Config,
					},
				})
				if err != nil {
					return nil, errors.Wrapf(err, "Failed to create local node project %q", p.Name)
				}
			} else if p.Name != project.Default {
				// Update project features if not default project.
				err = d.UpdateProject(p.Name, api.ProjectPut{
					Description: p.Description,
					Config:      p.Config,
				}, localProjectEtag)
				if err != nil {
					return nil, errors.Wrapf(err, "Failed to update local node project %q", p.Name)
				}
			}
		}

		// Merge the returned networks configs with the node-specific configs provided by the user.
		for _, network := range networks {
			// Skip unmanaged or pending networks.
			if !network.Managed || network.Status != api.NetworkStatusCreated {
				continue
			}

			post := internalClusterPostNetwork{
				NetworksPost: api.NetworksPost{
					NetworkPut: network.NetworkPut,
					Name:       network.Name,
					Type:       network.Type,
				},
				Project: p.Name,
			}

			// Apply the node-specific config supplied by the user for networks in the default project.
			// At this time project specific networks don't have node specific config options.
			if p.Name == project.Default {
				for _, config := range memberConfig {
					if config.Entity != "network" {
						continue
					}

					if config.Name != network.Name {
						continue
					}

					if !shared.StringInSlice(config.Key, db.NodeSpecificNetworkConfig) {
						logger.Warnf("Ignoring config key %q for network %q in project %q", config.Key, config.Name, p.Name)
						continue
					}

					post.Config[config.Key] = config.Value
				}
			}

			data.Networks = append(data.Networks, post)
		}
	}

	revert, err := initDataNodeApply(d, data)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to initialize storage pools and networks")
	}

	return revert, nil
}

// Perform a request to the /internal/cluster/accept endpoint to check if a new
// node can be accepted into the cluster and obtain joining information such as
// the cluster private certificate.
func clusterAcceptMember(client lxd.InstanceServer, name string, address string, schema int, apiExt int, pools []api.StoragePool, networks []internalClusterPostNetwork) (*internalClusterPostAcceptResponse, error) {
	architecture, err := osarch.ArchitectureGetLocalID()
	if err != nil {
		return nil, err
	}

	req := internalClusterPostAcceptRequest{
		Name:         name,
		Address:      address,
		Schema:       schema,
		API:          apiExt,
		StoragePools: pools,
		Networks:     networks,
		Architecture: architecture,
	}
	info := &internalClusterPostAcceptResponse{}
	resp, _, err := client.RawQuery("POST", "/internal/cluster/accept", req, "")
	if err != nil {
		return nil, err
	}

	err = resp.MetadataAsStruct(&info)
	if err != nil {
		return nil, err
	}

	return info, nil
}

// swagger:operation GET /1.0/cluster/members cluster cluster_members_get
//
// Get the cluster members
//
// Returns a list of cluster members (URLs).
//
// ---
// produces:
//   - application/json
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
//               "/1.0/cluster/members/lxd01",
//               "/1.0/cluster/members/lxd02"
//             ]
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/cluster/members?recursion=1 cluster cluster_members_get_recursion1
//
// Get the cluster members
//
// Returns a list of cluster members (structs).
//
// ---
// produces:
//   - application/json
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
//           description: List of cluster members
//           items:
//             $ref: "#/definitions/ClusterMember"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func clusterNodesGet(d *Daemon, r *http.Request) response.Response {
	recursion := util.IsRecursionRequest(r)
	state := d.State()

	var err error
	var nodes []db.NodeInfo
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		// Get the nodes.
		nodes, err = tx.GetNodes()
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	leader, err := d.gateway.LeaderAddress()
	if err != nil {
		return response.InternalError(err)
	}

	if recursion {
		result := []api.ClusterMember{}
		for _, node := range nodes {
			member, err := node.ToAPI(state.Cluster, state.Node, leader)
			if err != nil {
				return response.InternalError(err)
			}

			result = append(result, *member)
		}

		return response.SyncResponse(true, result)
	}

	urls := []string{}
	for _, node := range nodes {
		url := fmt.Sprintf("/%s/cluster/members/%s", version.APIVersion, node.Name)
		urls = append(urls, url)
	}

	return response.SyncResponse(true, urls)
}

var clusterNodesPostMu sync.Mutex // Used to prevent races when creating cluster join tokens.

// swagger:operation POST /1.0/cluster/members cluster cluster_members_post
//
// Request a join token
//
// Requests a join token to add a cluster member.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: body
//     name: cluster
//     description: Cluster member add request
//     required: true
//     schema:
//       $ref: "#/definitions/ClusterMembersPost"
// responses:
//   "202":
//     $ref: "#/responses/Operation"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func clusterNodesPost(d *Daemon, r *http.Request) response.Response {
	req := api.ClusterMembersPost{}

	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	clustered, err := cluster.Enabled(d.db)
	if err != nil {
		return response.SmartError(err)
	}

	if !clustered {
		return response.BadRequest(fmt.Errorf("This server is not clustered"))
	}

	// Get target addresses for existing online members, so that it can be encoded into the join token so that
	// the joining member will not have to specify a joining address during the join process.
	// Use anonymous interface type to align with how the API response will be returned for consistency when
	// retrieving remote operations.
	onlineNodeAddresses := make([]interface{}, 0)

	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		// Get the offline threshold.
		config, err := cluster.ConfigLoad(tx)
		if err != nil {
			return errors.Wrap(err, "Failed to load LXD config")
		}

		// Get the nodes.
		nodes, err := tx.GetNodes()
		if err != nil {
			return err
		}

		// Filter to online members.
		for _, node := range nodes {
			if node.State == db.ClusterMemberStateEvacuated || node.IsOffline(config.OfflineThreshold()) {
				continue
			}

			onlineNodeAddresses = append(onlineNodeAddresses, node.Address)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if len(onlineNodeAddresses) < 1 {
		return response.InternalError(fmt.Errorf("There are no online cluster members"))
	}

	// Lock to prevent concurrent requests racing the operationsGetByType function and creating duplicates.
	// We have to do this because collecting all of the operations from existing cluster members can take time.
	clusterNodesPostMu.Lock()
	defer clusterNodesPostMu.Unlock()

	// Remove any existing join tokens for the requested cluster member, this way we only ever have one active
	// join token for each potential new member, and it has the most recent active members list for joining.
	// This also ensures any historically unused (but potentially published) join tokens are removed.
	ops, err := operationsGetByType(d, r, project.Default, db.OperationClusterJoinToken)
	if err != nil {
		return response.InternalError(errors.Wrapf(err, "Failed getting cluster join token operations"))
	}

	for _, op := range ops {
		if op.StatusCode != api.Running {
			continue // Tokens are single use, so if cancelled but not deleted yet its not available.
		}

		opServerName, ok := op.Metadata["serverName"]
		if !ok {
			continue
		}

		if opServerName == req.ServerName {
			// Join token operation matches requested server name, so lets cancel it.
			logger.Warn("Cancelling duplicate join token operation", log.Ctx{"operation": op.ID, "serverName": opServerName})
			err = operationCancel(d, r, project.Default, op)
			if err != nil {
				return response.InternalError(errors.Wrapf(err, "Failed to cancel operation %q", op.ID))
			}
		}
	}

	// Generate join secret for new member. This will be stored inside the join token operation and will be
	// supplied by the joining member (encoded inside the join token) which will allow us to lookup the correct
	// operation in order to validate the requested joining server name is correct and authorised.
	joinSecret, err := shared.RandomCryptoString()
	if err != nil {
		return response.InternalError(err)
	}

	// Generate fingerprint of network certificate so joining member can automatically trust the correct
	// certificate when it is presented during the join process.
	fingerprint, err := shared.CertFingerprintStr(string(d.endpoints.NetworkPublicKey()))
	if err != nil {
		return response.InternalError(err)
	}

	meta := map[string]interface{}{
		"serverName":  req.ServerName, // Add server name to allow validation of name during join process.
		"secret":      joinSecret,
		"fingerprint": fingerprint,
		"addresses":   onlineNodeAddresses,
	}

	resources := map[string][]string{}
	resources["cluster"] = []string{}

	op, err := operations.OperationCreate(d.State(), project.Default, operations.OperationClassToken, db.OperationClusterJoinToken, resources, meta, nil, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	d.State().Events.SendLifecycle(projectParam(r), lifecycle.ClusterTokenCreated.Event("members", op.Requestor(), nil))

	return operations.OperationResponse(op)
}

// swagger:operation GET /1.0/cluster/members/{name} cluster cluster_member_get
//
// Get the cluster member
//
// Gets a specific cluster member.
//
// ---
// produces:
//   - application/json
// responses:
//   "200":
//     description: Profile
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
//           $ref: "#/definitions/ClusterMember"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func clusterNodeGet(d *Daemon, r *http.Request) response.Response {
	name := mux.Vars(r)["name"]
	state := d.State()

	var err error
	var nodes []db.NodeInfo
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		// Get the node.
		nodes, err = tx.GetNodes()
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	leader, err := d.gateway.LeaderAddress()
	if err != nil {
		return response.InternalError(err)
	}

	for _, node := range nodes {
		if node.Name != name {
			continue
		}

		member, err := node.ToAPI(state.Cluster, state.Node, leader)
		if err != nil {
			return response.InternalError(err)
		}

		return response.SyncResponseETag(true, member, member.ClusterMemberPut)
	}

	return response.NotFound(fmt.Errorf("Member '%s' not found", name))
}

// swagger:operation PATCH /1.0/cluster/members/{name} cluster cluster_member_patch
//
// Partially update the cluster member
//
// Updates a subset of the cluster member configuration.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: body
//     name: cluster
//     description: Cluster member configuration
//     required: true
//     schema:
//       $ref: "#/definitions/ClusterMemberPut"
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
func clusterNodePatch(d *Daemon, r *http.Request) response.Response {
	return updateClusterNode(d, r, true)
}

// swagger:operation PUT /1.0/cluster/members/{name} cluster cluster_member_put
//
// Update the cluster member
//
// Updates the entire cluster member configuration.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: body
//     name: cluster
//     description: Cluster member configuration
//     required: true
//     schema:
//       $ref: "#/definitions/ClusterMemberPut"
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
func clusterNodePut(d *Daemon, r *http.Request) response.Response {
	return updateClusterNode(d, r, false)
}

// updateClusterNode is shared between clusterNodePut and clusterNodePatch.
func updateClusterNode(d *Daemon, r *http.Request, isPatch bool) response.Response {
	name := mux.Vars(r)["name"]
	state := d.State()

	var err error
	var node db.NodeInfo
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		// Get the node.
		node, err = tx.GetNodeByName(name)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	leader, err := d.gateway.LeaderAddress()
	if err != nil {
		return response.InternalError(err)
	}

	member, err := node.ToAPI(state.Cluster, state.Node, leader)
	if err != nil {
		return response.InternalError(err)
	}

	// Validate the request is fine
	err = util.EtagCheck(r, member.ClusterMemberPut)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	// Parse the request
	req := api.ClusterMemberPut{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Validate the request
	if shared.StringInSlice(string(db.ClusterRoleDatabase), member.Roles) && !shared.StringInSlice(string(db.ClusterRoleDatabase), req.Roles) {
		return response.BadRequest(fmt.Errorf("The '%s' role cannot be dropped at this time", db.ClusterRoleDatabase))
	}

	if !shared.StringInSlice(string(db.ClusterRoleDatabase), member.Roles) && shared.StringInSlice(string(db.ClusterRoleDatabase), req.Roles) {
		return response.BadRequest(fmt.Errorf("The '%s' role cannot be added at this time", db.ClusterRoleDatabase))
	}

	// Nodes must belong to at least one group.
	if len(req.Groups) == 0 {
		return response.BadRequest(fmt.Errorf("Cluster members need to belong to at least one group"))
	}

	// Update the database
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		nodeInfo, err := tx.GetNodeByName(name)
		if err != nil {
			return errors.Wrap(err, "Loading node information")
		}

		err = clusterValidateConfig(req.Config)
		if err != nil {
			return err
		}

		if isPatch {
			// Populate request config with current values.
			if req.Config == nil {
				req.Config = nodeInfo.Config
			} else {
				for k, v := range nodeInfo.Config {
					if _, ok := req.Config[k]; !ok {
						req.Config[k] = v
					}
				}
			}
		}

		// Update node config.
		err = tx.UpdateNodeConfig(nodeInfo.ID, req.Config)
		if err != nil {
			return fmt.Errorf("Failed to update cluster member config: %w", err)
		}

		// Update the description.
		if req.Description != member.Description {
			err = tx.SetDescription(nodeInfo.ID, req.Description)
			if err != nil {
				return errors.Wrap(err, "Update description")
			}
		}

		// Update the roles.
		dbRoles := []db.ClusterRole{}
		for _, role := range req.Roles {
			dbRoles = append(dbRoles, db.ClusterRole(role))
		}

		err = tx.UpdateNodeRoles(nodeInfo.ID, dbRoles)
		if err != nil {
			return errors.Wrap(err, "Update roles")
		}

		err = tx.UpdateNodeFailureDomain(nodeInfo.ID, req.FailureDomain)
		if err != nil {
			return errors.Wrap(err, "Update failure domain")
		}

		// Update the cluster groups.
		err = tx.UpdateNodeClusterGroups(nodeInfo.ID, req.Groups)
		if err != nil {
			return fmt.Errorf("Update cluster groups: %w", err)
		}
		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r)
	d.State().Events.SendLifecycle(projectParam(r), lifecycle.ClusterMemberUpdated.Event(name, requestor, nil))

	return response.EmptySyncResponse
}

// clusterValidateConfig validates the configuration keys/values for cluster members.
func clusterValidateConfig(config map[string]string) error {
	clusterConfigKeys := map[string]func(value string) error{
		"scheduler.instance": validate.Optional(validate.IsOneOf("all", "group", "manual")),
	}

	for k, v := range config {
		// User keys are free for all.
		if strings.HasPrefix(k, "user.") {
			continue
		}

		validator, ok := clusterConfigKeys[k]
		if !ok {
			return fmt.Errorf("Invalid cluster configuration key %q", k)
		}

		err := validator(v)
		if err != nil {
			return fmt.Errorf("Invalid cluster configuration key %q value", k)
		}
	}

	return nil
}

// swagger:operation POST /1.0/cluster/members/{name} cluster cluster_member_post
//
// Rename the cluster member
//
// Renames an existing cluster member.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: body
//     name: cluster
//     description: Cluster member rename request
//     required: true
//     schema:
//       $ref: "#/definitions/ClusterMemberPost"
// responses:
//   "200":
//     $ref: "#/responses/EmptySyncResponse"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func clusterNodePost(d *Daemon, r *http.Request) response.Response {
	name := mux.Vars(r)["name"]

	req := api.ClusterMemberPost{}

	// Parse the request
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		return tx.RenameNode(name, req.ServerName)
	})
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r)
	d.State().Events.SendLifecycle(projectParam(r), lifecycle.ClusterMemberRenamed.Event(req.ServerName, requestor, log.Ctx{"old_name": name}))

	return response.EmptySyncResponse
}

// swagger:operation DELETE /1.0/cluster/members/{name} cluster cluster_member_delete
//
// Delete the cluster member
//
// Removes the member from the cluster.
//
// ---
// produces:
//   - application/json
// responses:
//   "200":
//     $ref: "#/responses/EmptySyncResponse"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func clusterNodeDelete(d *Daemon, r *http.Request) response.Response {
	force, err := strconv.Atoi(r.FormValue("force"))
	if err != nil {
		force = 0
	}

	name := mux.Vars(r)["name"]

	// Redirect all requests to the leader, which is the one with
	// knowning what nodes are part of the raft cluster.
	localAddress, err := node.ClusterAddress(d.db)
	if err != nil {
		return response.SmartError(err)
	}

	leader, err := d.gateway.LeaderAddress()
	if err != nil {
		return response.InternalError(err)
	}

	var localInfo, leaderInfo db.NodeInfo
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		localInfo, err = tx.GetNodeByAddress(localAddress)
		if err != nil {
			return fmt.Errorf("Failed loading local member info %q: %w", localAddress, err)
		}

		leaderInfo, err = tx.GetNodeByAddress(leader)
		if err != nil {
			return fmt.Errorf("Failed loading leader member info %q: %w", leader, err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Get information about the cluster.
	var nodes []db.RaftNode
	err = d.db.Transaction(func(tx *db.NodeTx) error {
		var err error
		nodes, err = tx.GetRaftNodes()
		return err
	})
	if err != nil {
		return response.SmartError(errors.Wrap(err, "Unable to get raft nodes"))
	}

	if localAddress != leader {
		if localInfo.Name == name {
			// If the member being removed is ourselves and we are not the leader, then lock the
			// clusterPutDisableMu before we forward the request to the leader, so that when the leader
			// goes on to request clusterPutDisable back to ourselves it won't be actioned until we
			// have returned this request back to the original client.
			clusterPutDisableMu.Lock()
			logger.Info("Acquired cluster self removal lock", log.Ctx{"member": localInfo.Name})

			go func() {
				<-r.Context().Done() // Wait until request is finished.

				logger.Info("Releasing cluster self removal lock", log.Ctx{"member": localInfo.Name})
				clusterPutDisableMu.Unlock()
			}()
		}

		logger.Debugf("Redirect member delete request to %s", leader)
		client, err := cluster.Connect(leader, d.endpoints.NetworkCert(), d.serverCert(), r, false)
		if err != nil {
			return response.SmartError(err)
		}
		err = client.DeleteClusterMember(name, force == 1)
		if err != nil {
			return response.SmartError(err)
		}

		// If we are the only remaining node, wait until promotion to leader,
		// then update cluster certs.
		if name == leaderInfo.Name && len(nodes) == 2 {
			err = d.gateway.WaitLeadership()
			if err != nil {
				return response.SmartError(err)
			}

			updateCertificateCache(d)
		}

		return response.ManualResponse(func(w http.ResponseWriter) error {
			err := response.EmptySyncResponse.Render(w)
			if err != nil {
				return err
			}

			// Send the response before replacing the LXD daemon process.
			f, ok := w.(http.Flusher)
			if ok {
				f.Flush()
			} else {
				return fmt.Errorf("http.ResponseWriter is not type http.Flusher")
			}

			return nil
		})
	}

	// Get lock now we are on leader.
	d.clusterMembershipMutex.Lock()
	defer d.clusterMembershipMutex.Unlock()

	// If we are removing the leader of a 2 node cluster, ensure the other node can be a leader.
	if name == leaderInfo.Name && len(nodes) == 2 {
		for i := range nodes {
			if nodes[i].Address != leader && nodes[i].Role == db.RaftStandBy {
				// Promote the remaining node.
				nodes[i].Role = db.RaftVoter
				err := changeMemberRole(d, r, nodes[i].Address, nodes)
				if err != nil {
					return response.SmartError(errors.Wrap(err, "Unable to promote remaining cluster member to leader"))
				}

				break
			}
		}
	}

	logger.Info("Deleting member from cluster", log.Ctx{"name": name, "force": force})

	err = autoSyncImages(d.shutdownCtx, d)
	if err != nil {
		if force == 0 {
			return response.SmartError(errors.Wrap(err, "Failed to sync images"))
		}

		// If force is set, only show a warning instead of returning an error.
		logger.Warn("Failed to sync images")
	}

	// First check that the node is clear from containers and images and
	// make it leave the database cluster, if it's part of it.
	address, err := cluster.Leave(d.State(), d.gateway, name, force == 1)
	if err != nil {
		return response.SmartError(err)
	}

	if force != 1 {
		// Try to gracefully delete all networks and storage pools on it.
		// Delete all networks on this node
		client, err := cluster.Connect(address, d.endpoints.NetworkCert(), d.serverCert(), r, true)
		if err != nil {
			return response.SmartError(err)
		}

		// Get a list of projects for networks.
		var networkProjectNames []string

		err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
			networkProjectNames, err = tx.GetProjectNames()
			return err
		})
		if err != nil {
			return response.SmartError(errors.Wrapf(err, "Failed to load projects for networks"))
		}

		for _, networkProjectName := range networkProjectNames {
			networks, err := d.cluster.GetNetworks(networkProjectName)
			if err != nil {
				return response.SmartError(err)
			}

			for _, name := range networks {
				err := client.UseProject(networkProjectName).DeleteNetwork(name)
				if err != nil {
					return response.SmartError(err)
				}
			}
		}

		// Delete all the pools on this node
		pools, err := d.cluster.GetStoragePoolNames()
		if err != nil && err != db.ErrNoSuchObject {
			return response.SmartError(err)
		}

		for _, name := range pools {
			err := client.DeleteStoragePool(name)
			if err != nil {
				return response.SmartError(err)
			}
		}
	}

	// Remove node from the database
	err = cluster.Purge(d.cluster, name)
	if err != nil {
		return response.SmartError(errors.Wrap(err, "Failed to remove member from database"))
	}

	err = rebalanceMemberRoles(d, r, nil)
	if err != nil {
		logger.Warnf("Failed to rebalance dqlite nodes: %v", err)
	}

	// If this leader node removed itself, just disable clustering.
	if address == localAddress {
		return clusterPutDisable(d, r, api.ClusterPut{})
	} else if force != 1 {
		// Try to gracefully reset the database on the node.
		client, err := cluster.Connect(address, d.endpoints.NetworkCert(), d.serverCert(), r, true)
		if err != nil {
			return response.SmartError(err)
		}

		put := api.ClusterPut{}
		put.Enabled = false
		_, err = client.UpdateCluster(put, "")
		if err != nil {
			return response.SmartError(errors.Wrap(err, "Failed to cleanup the member"))
		}
	}

	// Refresh the trusted certificate cache now that the member certificate has been removed.
	// We do not need to notify the other members here because the next heartbeat will trigger member change
	// detection and updateCertificateCache is called as part of that.
	updateCertificateCache(d)

	// Ensure all images are available after this node has been deleted.
	err = autoSyncImages(d.shutdownCtx, d)
	if err != nil {
		logger.Warn("Failed to sync images")
	}

	requestor := request.CreateRequestor(r)
	d.State().Events.SendLifecycle(projectParam(r), lifecycle.ClusterMemberRemoved.Event(name, requestor, nil))

	return response.EmptySyncResponse
}

// swagger:operation PUT /1.0/cluster/certificate cluster clustering_update_cert
//
// Update the certificate for the cluster
//
// Replaces existing cluster certificate and reloads LXD on each cluster
// member.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: body
//     name: cluster
//     description: Cluster certificate replace request
//     required: true
//     schema:
//       $ref: "#/definitions/ClusterCertificatePut"
// responses:
//   "200":
//     $ref: "#/responses/EmptySyncResponse"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func clusterCertificatePut(d *Daemon, r *http.Request) response.Response {
	req := api.ClusterCertificatePut{}

	// Parse the request
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	certBytes := []byte(req.ClusterCertificate)
	keyBytes := []byte(req.ClusterCertificateKey)

	certBlock, _ := pem.Decode(certBytes)
	if certBlock == nil {
		return response.BadRequest(fmt.Errorf("Certificate must be base64 encoded PEM certificate: %v", err))
	}

	keyBlock, _ := pem.Decode(keyBytes)
	if keyBlock == nil {
		return response.BadRequest(fmt.Errorf("Private key must be base64 encoded PEM key: %v", err))
	}

	// First node forwards request to all other cluster nodes
	if !isClusterNotification(r) {
		servers, err := d.gateway.NodeStore().Get(context.Background())
		if err != nil {
			return response.SmartError(err)
		}

		localAddress, err := node.ClusterAddress(d.db)
		if err != nil {
			return response.SmartError(err)
		}

		for _, server := range servers {
			if server.Address == localAddress {
				continue
			}

			client, err := cluster.Connect(server.Address, d.endpoints.NetworkCert(), d.serverCert(), r, true)
			if err != nil {
				return response.SmartError(err)
			}

			err = client.UpdateClusterCertificate(req, "")
			if err != nil {
				return response.SmartError(err)
			}
		}
	}

	err = util.WriteCert(d.os.VarDir, "cluster", certBytes, keyBytes, nil)
	if err != nil {
		return response.SmartError(err)
	}

	// Get the new cluster certificate struct
	cert, err := util.LoadClusterCert(d.os.VarDir)
	if err != nil {
		return response.SmartError(err)
	}

	// Update the certificate on the network endpoint and gateway
	d.endpoints.NetworkUpdateCert(cert)
	d.gateway.NetworkUpdateCert(cert)

	requestor := request.CreateRequestor(r)
	d.State().Events.SendLifecycle(projectParam(r), lifecycle.ClusterCertificateUpdated.Event("certificate", requestor, nil))

	return response.EmptySyncResponse
}

func internalClusterPostAccept(d *Daemon, r *http.Request) response.Response {
	req := internalClusterPostAcceptRequest{}

	// Parse the request
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
	if req.Name == "" {
		return response.BadRequest(fmt.Errorf("No name provided"))
	}

	// Redirect all requests to the leader, which is the one with
	// knowning what nodes are part of the raft cluster.
	address, err := node.ClusterAddress(d.db)
	if err != nil {
		return response.SmartError(err)
	}
	leader, err := d.gateway.LeaderAddress()
	if err != nil {
		return response.InternalError(err)
	}
	if address != leader {
		logger.Debugf("Redirect member accept request to %s", leader)

		if leader == "" {
			return response.SmartError(fmt.Errorf("Unable to find leader address"))
		}

		url := &url.URL{
			Scheme: "https",
			Path:   "/internal/cluster/accept",
			Host:   leader,
		}
		return response.SyncResponseRedirect(url.String())
	}

	// Get lock now we are on leader.
	d.clusterMembershipMutex.Lock()
	defer d.clusterMembershipMutex.Unlock()

	// Check that the pools and networks provided by the joining node have
	// configs that match the cluster ones.
	err = clusterCheckStoragePoolsMatch(d.cluster, req.StoragePools)
	if err != nil {
		return response.SmartError(err)
	}
	err = clusterCheckNetworksMatch(d.cluster, req.Networks)
	if err != nil {
		return response.SmartError(err)
	}

	nodes, err := cluster.Accept(d.State(), d.gateway, req.Name, req.Address, req.Schema, req.API, req.Architecture)
	if err != nil {
		return response.BadRequest(err)
	}
	accepted := internalClusterPostAcceptResponse{
		RaftNodes:  make([]internalRaftNode, len(nodes)),
		PrivateKey: d.endpoints.NetworkPrivateKey(),
	}
	for i, node := range nodes {
		accepted.RaftNodes[i].ID = node.ID
		accepted.RaftNodes[i].Address = node.Address
		accepted.RaftNodes[i].Role = int(node.Role)
	}
	return response.SyncResponse(true, accepted)
}

// A request for the /internal/cluster/accept endpoint.
type internalClusterPostAcceptRequest struct {
	Name         string                       `json:"name" yaml:"name"`
	Address      string                       `json:"address" yaml:"address"`
	Schema       int                          `json:"schema" yaml:"schema"`
	API          int                          `json:"api" yaml:"api"`
	StoragePools []api.StoragePool            `json:"storage_pools" yaml:"storage_pools"`
	Networks     []internalClusterPostNetwork `json:"networks" yaml:"networks"`
	Architecture int                          `json:"architecture" yaml:"architecture"`
}

type internalClusterPostNetwork struct {
	api.NetworksPost `yaml:",inline"`

	Project string
}

// A Response for the /internal/cluster/accept endpoint.
type internalClusterPostAcceptResponse struct {
	RaftNodes  []internalRaftNode `json:"raft_nodes" yaml:"raft_nodes"`
	PrivateKey []byte             `json:"private_key" yaml:"private_key"`
}

// Represent a LXD node that is part of the dqlite raft cluster.
type internalRaftNode struct {
	ID      uint64 `json:"id" yaml:"id"`
	Address string `json:"address" yaml:"address"`
	Role    int    `json:"role" yaml:"role"`
	Name    string `json:"name" yaml:"name"`
}

// Used to update the cluster after a database node has been removed, and
// possibly promote another one as database node.
func internalClusterPostRebalance(d *Daemon, r *http.Request) response.Response {
	// Redirect all requests to the leader, which is the one with with
	// up-to-date knowledge of what nodes are part of the raft cluster.
	localAddress, err := node.ClusterAddress(d.db)
	if err != nil {
		return response.SmartError(err)
	}
	leader, err := d.gateway.LeaderAddress()
	if err != nil {
		return response.InternalError(err)
	}
	if localAddress != leader {
		logger.Debugf("Redirect cluster rebalance request to %s", leader)
		url := &url.URL{
			Scheme: "https",
			Path:   "/internal/cluster/rebalance",
			Host:   leader,
		}
		return response.SyncResponseRedirect(url.String())
	}

	// Get lock now we are on leader.
	d.clusterMembershipMutex.Lock()
	defer d.clusterMembershipMutex.Unlock()

	err = rebalanceMemberRoles(d, r, nil)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, nil)
}

// Check if there's a dqlite node whose role should be changed, and post a
// change role request if so.
func rebalanceMemberRoles(d *Daemon, r *http.Request, unavailableMembers []string) error {
	if d.shutdownCtx.Err() != nil {
		return nil
	}

again:
	address, nodes, err := cluster.Rebalance(d.State(), d.gateway, unavailableMembers)
	if err != nil {
		return err
	}

	if address == "" {
		// Nothing to do.
		return nil
	}

	// Process demotions of offline nodes immediately.
	for _, node := range nodes {
		if node.Address != address || node.Role != db.RaftSpare {
			continue
		}

		if cluster.HasConnectivity(d.endpoints.NetworkCert(), d.serverCert(), address) {
			break
		}

		logger.Info("Demoting offline member during rebalance", log.Ctx{"candidateAddress": node.Address})
		err := d.gateway.DemoteOfflineNode(node.ID)
		if err != nil {
			return errors.Wrapf(err, "Demote offline node %s", node.Address)
		}

		goto again
	}

	// Tell the node to promote itself.
	logger.Info("Promoting member during rebalance", log.Ctx{"candidateAddress": address})
	err = changeMemberRole(d, r, address, nodes)
	if err != nil {
		return err
	}

	goto again
}

// Check if there are nodes not part of the raft configuration and add them in
// case.
func upgradeNodesWithoutRaftRole(d *Daemon) error {
	if d.shutdownCtx.Err() != nil {
		return nil
	}

	var allNodes []db.NodeInfo
	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		allNodes, err = tx.GetNodes()
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return errors.Wrap(err, "Failed to get current cluster nodes")

	}
	return cluster.UpgradeMembersWithoutRole(d.gateway, allNodes)
}

// Post a change role request to the member with the given address. The nodes
// slice contains details about all members, including the one being changed.
func changeMemberRole(d *Daemon, r *http.Request, address string, nodes []db.RaftNode) error {
	post := &internalClusterPostAssignRequest{}
	for _, node := range nodes {
		post.RaftNodes = append(post.RaftNodes, internalRaftNode{
			ID:      node.ID,
			Address: node.Address,
			Role:    int(node.Role),
			Name:    node.Name,
		})
	}

	client, err := cluster.Connect(address, d.endpoints.NetworkCert(), d.serverCert(), r, true)
	if err != nil {
		return err
	}

	_, _, err = client.RawQuery("POST", "/internal/cluster/assign", post, "")
	if err != nil {
		return err
	}

	return nil
}

// Try to handover the role of this member to another one.
func handoverMemberRole(d *Daemon) error {
	// If we aren't clustered, there's nothing to do.
	clustered, err := cluster.Enabled(d.db)
	if err != nil {
		return err
	}
	if !clustered {
		return nil
	}

	// Figure out our own cluster address.
	address, err := node.ClusterAddress(d.db)
	if err != nil {
		return err
	}

	post := &internalClusterPostHandoverRequest{
		Address: address,
	}

	logCtx := log.Ctx{"address": address}

	// Find the cluster leader.
findLeader:
	leader, err := d.gateway.LeaderAddress()
	if err != nil {
		return err
	}
	if leader == "" {
		return fmt.Errorf("No leader address found")
	}

	if leader == address {
		logger.Info("Transferring leadership", logCtx)
		err := d.gateway.TransferLeadership()
		if err != nil {
			return errors.Wrapf(err, "Failed to transfer leadership")
		}
		goto findLeader
	}

	logger.Info("Handing over cluster member role", logCtx)
	client, err := cluster.Connect(leader, d.endpoints.NetworkCert(), d.serverCert(), nil, true)
	if err != nil {
		return errors.Wrapf(err, "Failed handing over cluster member role")
	}

	_, _, err = client.RawQuery("POST", "/internal/cluster/handover", post, "")
	if err != nil {
		return err
	}

	return nil
}

// Used to assign a new role to a the local dqlite node.
func internalClusterPostAssign(d *Daemon, r *http.Request) response.Response {
	req := internalClusterPostAssignRequest{}

	// Parse the request
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
	if len(req.RaftNodes) == 0 {
		return response.BadRequest(fmt.Errorf("No raft members provided"))
	}

	nodes := make([]db.RaftNode, len(req.RaftNodes))
	for i, node := range req.RaftNodes {
		nodes[i].ID = node.ID
		nodes[i].Address = node.Address
		nodes[i].Role = db.RaftRole(node.Role)
		nodes[i].Name = node.Name
	}
	err = cluster.Assign(d.State(), d.gateway, nodes)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, nil)
}

// A request for the /internal/cluster/assign endpoint.
type internalClusterPostAssignRequest struct {
	RaftNodes []internalRaftNode `json:"raft_nodes" yaml:"raft_nodes"`
}

// Used to to transfer the responsibilities of a member to another one
func internalClusterPostHandover(d *Daemon, r *http.Request) response.Response {
	req := internalClusterPostHandoverRequest{}

	// Parse the request
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
	if req.Address == "" {
		return response.BadRequest(fmt.Errorf("No id provided"))
	}

	// Redirect all requests to the leader, which is the one with
	// authoritative knowledge of the current raft configuration.
	address, err := node.ClusterAddress(d.db)
	if err != nil {
		return response.SmartError(err)
	}
	leader, err := d.gateway.LeaderAddress()
	if err != nil {
		return response.InternalError(err)
	}

	if leader == "" {
		return response.SmartError(fmt.Errorf("No leader address found"))
	}

	if address != leader {
		logger.Debugf("Redirect handover request to %s", leader)
		url := &url.URL{
			Scheme: "https",
			Path:   "/internal/cluster/handover",
			Host:   leader,
		}
		return response.SyncResponseRedirect(url.String())
	}

	// Get lock now we are on leader.
	d.clusterMembershipMutex.Lock()
	defer d.clusterMembershipMutex.Unlock()

	target, nodes, err := cluster.Handover(d.State(), d.gateway, req.Address)
	if err != nil {
		return response.SmartError(err)
	}

	// If there's no other member we can promote, there's nothing we can
	// do, just return.
	if target == "" {
		goto out
	}

	logger.Info("Promoting member during handover", log.Ctx{"address": address, "losingAddress": req.Address, "candidateAddress": target})
	err = changeMemberRole(d, r, target, nodes)
	if err != nil {
		return response.SmartError(err)
	}

	// Demote the member that is handing over.
	for i, node := range nodes {
		if node.Address == req.Address {
			nodes[i].Role = db.RaftSpare
		}
	}

	logger.Info("Demoting member during handover", log.Ctx{"address": address, "losingAddress": req.Address})
	err = changeMemberRole(d, r, req.Address, nodes)
	if err != nil {
		return response.SmartError(err)
	}

out:
	return response.SyncResponse(true, nil)
}

// A request for the /internal/cluster/handover endpoint.
type internalClusterPostHandoverRequest struct {
	// Address of the server whose role should be transferred.
	Address string `json:"address" yaml:"address"`
}

func clusterCheckStoragePoolsMatch(cluster *db.Cluster, reqPools []api.StoragePool) error {
	poolNames, err := cluster.GetCreatedStoragePoolNames()
	if err != nil && err != db.ErrNoSuchObject {
		return err
	}
	for _, name := range poolNames {
		found := false
		for _, reqPool := range reqPools {
			if reqPool.Name != name {
				continue
			}
			found = true
			_, pool, _, err := cluster.GetStoragePoolInAnyState(name)
			if err != nil {
				return err
			}
			if pool.Driver != reqPool.Driver {
				return fmt.Errorf("Mismatching driver for storage pool %s", name)
			}
			// Exclude the keys which are node-specific.
			exclude := db.StoragePoolNodeConfigKeys
			err = util.CompareConfigs(pool.Config, reqPool.Config, exclude)
			if err != nil {
				return fmt.Errorf("Mismatching config for storage pool %s: %v", name, err)
			}
			break
		}
		if !found {
			return fmt.Errorf("Missing storage pool %s", name)
		}
	}
	return nil
}

func clusterCheckNetworksMatch(cluster *db.Cluster, reqNetworks []internalClusterPostNetwork) error {
	var err error

	// Get a list of projects for networks.
	var networkProjectNames []string

	err = cluster.Transaction(func(tx *db.ClusterTx) error {
		networkProjectNames, err = tx.GetProjectNames()
		return err
	})
	if err != nil {
		return errors.Wrapf(err, "Failed to load projects for networks")
	}

	for _, networkProjectName := range networkProjectNames {
		networkNames, err := cluster.GetCreatedNetworks(networkProjectName)
		if err != nil && err != db.ErrNoSuchObject {
			return err
		}

		for _, networkName := range networkNames {
			found := false

			for _, reqNetwork := range reqNetworks {
				if reqNetwork.Name != networkName || reqNetwork.Project != networkProjectName {
					continue
				}

				found = true

				_, network, _, err := cluster.GetNetworkInAnyState(networkProjectName, networkName)
				if err != nil {
					return err
				}

				if reqNetwork.Type != network.Type {
					return fmt.Errorf("Mismatching type for network %q in project %q", networkName, networkProjectName)
				}

				// Exclude the keys which are node-specific.
				exclude := db.NodeSpecificNetworkConfig
				err = util.CompareConfigs(network.Config, reqNetwork.Config, exclude)
				if err != nil {
					return errors.Wrapf(err, "Mismatching config for network %q in project %q", networkName, networkProjectName)
				}

				break
			}

			if !found {
				return fmt.Errorf("Missing network %q in project %q", networkName, networkProjectName)
			}
		}
	}

	return nil
}

// Used as low-level recovering helper.
func internalClusterRaftNodeDelete(d *Daemon, r *http.Request) response.Response {
	address := mux.Vars(r)["address"]
	err := cluster.RemoveRaftNode(d.gateway, address)
	if err != nil {
		return response.SmartError(err)
	}

	err = rebalanceMemberRoles(d, r, nil)
	if err != nil && errors.Cause(err) != cluster.ErrNotLeader {
		logger.Warn("Could not rebalance cluster member roles after raft member removal", log.Ctx{"err": err})
	}

	return response.SyncResponse(true, nil)
}

// swagger:operation POST /1.0/cluster/members/{name}/state cluster cluster_member_state_post
//
// Evacuate or restore a cluster member
//
// Evacuates or restores a cluster member.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: body
//     name: cluster
//     description: Cluster member state
//     required: true
//     schema:
//       $ref: "#/definitions/ClusterMemberStatePost"
// responses:
//   "202":
//     $ref: "#/responses/Operation"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func clusterNodeStatePost(d *Daemon, r *http.Request) response.Response {
	name := mux.Vars(r)["name"]

	// Forward request
	resp := forwardedResponseToNode(d, r, name)
	if resp != nil {
		return resp
	}

	// Parse the request
	req := api.ClusterMemberStatePost{}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if req.Action == "evacuate" {
		return evacuateClusterMember(d, r)
	} else if req.Action == "restore" {
		return restoreClusterMember(d, r)
	}

	return response.BadRequest(fmt.Errorf("Unknown action %q", req.Action))
}

func evacuateClusterMember(d *Daemon, r *http.Request) response.Response {
	var err error
	var node db.NodeInfo

	nodeName := mux.Vars(r)["name"]

	// Set node status to EVACUATED
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		// Get the node.
		node, err = tx.GetNodeByName(nodeName)
		if err != nil {
			return errors.Wrap(err, "Failed to get cluster member by name")
		}

		if node.State == db.ClusterMemberStatePending {
			return fmt.Errorf("Cannot evacuate or restore a pending cluster member")
		}

		// Set node status to EVACUATED to prevent instances from being created.
		err = tx.UpdateNodeStatus(node.ID, db.ClusterMemberStateEvacuated)
		if err != nil {
			return errors.Wrap(err, "Failed to update cluster member status")
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Do nothing if the node is already evacuated.
	if node.State == db.ClusterMemberStateEvacuated {
		return response.SmartError(fmt.Errorf("Cluster member is already evacuated"))
	}

	reverter := revert.New()
	defer reverter.Fail()

	// Ensure node is put into its previous state if anything fails.
	reverter.Add(func() {
		d.cluster.Transaction(func(tx *db.ClusterTx) error {
			tx.UpdateNodeStatus(node.ID, db.ClusterMemberStateCreated)

			return nil
		})
	})

	// The instances are retrieved in a separate transaction, after the node is in EVACUATED state.
	var dbInstances []db.Instance

	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		// If evacuating, consider only the instances on the node which needs to be evacuated.
		dbInstances, err = tx.GetInstances(db.InstanceFilter{Node: &nodeName})
		if err != nil {
			return errors.Wrap(err, "Failed to get instances")
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	instances := make([]instance.Instance, len(dbInstances))

	for i, dbInst := range dbInstances {
		inst, err := instance.LoadByProjectAndName(d.State(), dbInst.Project, dbInst.Name)
		if err != nil {
			return response.SmartError(errors.Wrap(err, "Failed to load instance"))
		}

		instances[i] = inst
	}

	var targetNodeName string
	var targetNode db.NodeInfo

	run := func(op *operations.Operation) error {
		metadata := make(map[string]interface{})

		for _, inst := range instances {
			// Stop the instance if needed.
			isRunning := inst.IsRunning()
			if isRunning {
				metadata["evacuation_progress"] = fmt.Sprintf("Stopping %q in project %q", inst.Name(), inst.Project())
				op.UpdateMetadata(metadata)

				// Get the shutdown timeout for the instance.
				timeout := inst.ExpandedConfig()["boot.host_shutdown_timeout"]
				val, err := strconv.Atoi(timeout)
				if err != nil {
					val = 30
				}

				// Start with a clean shutdown.
				err = inst.Shutdown(time.Duration(val) * time.Second)
				if err != nil {
					// Fallback to forced stop.
					err = inst.Stop(false)
					if err != nil && errors.Cause(err) != drivers.ErrInstanceIsStopped {
						return errors.Wrapf(err, "Failed to stop instance %q", inst.Name())
					}
				}

				// Mark the instance as RUNNING in volatile so its state can be properly restored.
				inst.VolatileSet(map[string]string{"volatile.last_state.power": "RUNNING"})
			}

			// If not migratable, the instance is just stopped.
			if !inst.CanMigrate() {
				continue
			}

			// Find the least loaded cluster member which supports the architecture.
			err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
				targetNodeName, err = tx.GetNodeWithLeastInstances([]int{node.Architecture}, -1, "", nil)
				if err != nil {
					return err
				}

				if targetNodeName == "" {
					// No migration target found.
					return nil
				}

				targetNode, err = tx.GetNodeByName(targetNodeName)
				if err != nil {
					return err
				}

				return nil
			})
			if err != nil {
				return err
			}

			// Skip migration if no target available.
			if targetNodeName == "" {
				logger.Warn("No migration target available for instance", log.Ctx{"name": inst.Name(), "project": inst.Project()})
				continue
			}

			// Start migrating the instance.
			metadata["evacuation_progress"] = fmt.Sprintf("Migrating %q in project %q to %q", inst.Name(), inst.Project(), targetNodeName)
			op.UpdateMetadata(metadata)
			inst.VolatileSet(map[string]string{"volatile.evacuate.origin": nodeName})

			// Migrate the instance.
			req := api.InstancePost{
				Name: inst.Name(),
			}

			err = migrateInstance(d, r, inst, targetNodeName, false, req, op)
			if err != nil {
				return errors.Wrap(err, "Failed to migrate instance")
			}

			if !isRunning {
				continue
			}

			// Start it back up on target.
			dest, err := cluster.Connect(targetNode.Address, d.endpoints.NetworkCert(), d.serverCert(), r, true)
			if err != nil {
				return errors.Wrap(err, "Failed to connect to destination")
			}
			dest = dest.UseProject(inst.Project())

			metadata["evacuation_progress"] = fmt.Sprintf("Starting %q in project %q", inst.Name(), inst.Project())
			op.UpdateMetadata(metadata)

			startOp, err := dest.UpdateInstanceState(inst.Name(), api.InstanceStatePut{Action: "start"}, "")
			if err != nil {
				return err
			}

			err = startOp.Wait()
			if err != nil {
				return err
			}
		}

		return nil
	}

	op, err := operations.OperationCreate(d.State(), "", operations.OperationClassTask, db.OperationClusterMemberEvacuate, nil, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	reverter.Success()
	return operations.OperationResponse(op)
}

func restoreClusterMember(d *Daemon, r *http.Request) response.Response {
	originName := mux.Vars(r)["name"]

	var node db.NodeInfo
	var err error

	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		// Get the node.
		node, err = tx.GetNodeByName(originName)
		if err != nil {
			return errors.Wrap(err, "Failed to get cluster member by name")
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if node.State == db.ClusterMemberStatePending {
		return response.SmartError(fmt.Errorf("Cannot restore or restore a pending cluster member"))
	}

	if node.State == db.ClusterMemberStateCreated {
		return response.SmartError(fmt.Errorf("Cluster member not evacuated"))
	}

	var dbInstances []db.Instance
	var instanceConfig []map[string]string
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		dbInstances, err = tx.GetInstances(db.InstanceFilter{})
		if err != nil {
			return errors.Wrap(err, "Failed to get instances")
		}

		instanceConfig = make([]map[string]string, len(dbInstances))
		for i, inst := range dbInstances {
			instanceConfig[i], err = tx.GetInstanceConfig(inst.ID)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	instances := make([]instance.Instance, 0)
	localInstances := make([]instance.Instance, 0)

	for i, dbInst := range dbInstances {
		if dbInst.Node == node.Name {
			inst, err := instance.LoadByProjectAndName(d.State(), dbInst.Project, dbInst.Name)
			if err != nil {
				return response.SmartError(errors.Wrap(err, "Failed to load instance"))
			}

			localInstances = append(localInstances, inst)
			continue
		}

		// Only consider instances where volatile.evacuate.origin is set to the node which needs to be restored.
		val, ok := instanceConfig[i]["volatile.evacuate.origin"]
		if !ok || val != node.Name {
			continue
		}

		inst, err := instance.LoadByProjectAndName(d.State(), dbInst.Project, dbInst.Name)
		if err != nil {
			return response.SmartError(errors.Wrap(err, "Failed to load instance"))
		}

		instances = append(instances, inst)
	}

	run := func(op *operations.Operation) error {
		var source lxd.InstanceServer
		var sourceNode db.NodeInfo

		metadata := make(map[string]interface{})

		// Restart the local instances.
		for _, inst := range localInstances {
			// Don't start instances which were stopped by the user.
			if inst.LocalConfig()["volatile.last_state.power"] != "RUNNING" {
				continue
			}

			// Don't attempt to start instances which are already running.
			if inst.IsRunning() {
				continue
			}

			// Start the instance.
			metadata["evacuation_progress"] = fmt.Sprintf("Starting %q in project %q", inst.Name(), inst.Project())
			op.UpdateMetadata(metadata)

			err = inst.Start(false)
			if err != nil {
				return errors.Wrapf(err, "Failed to start instance %q", inst.Name())
			}
		}

		// Migrate back the remote instances.
		for _, inst := range instances {
			metadata["evacuation_progress"] = fmt.Sprintf("Migrating %q in project %q from %q", inst.Name(), inst.Project(), inst.Location())
			op.UpdateMetadata(metadata)

			err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
				sourceNode, err = tx.GetNodeByName(inst.Location())
				if err != nil {
					return errors.Wrapf(err, "Failed to get node %q", inst.Location())
				}

				return nil
			})
			if err != nil {
				return errors.Wrap(err, "Failed to get node")
			}

			source, err = cluster.Connect(sourceNode.Address, d.endpoints.NetworkCert(), d.serverCert(), r, true)
			if err != nil {
				return errors.Wrap(err, "Failed to connect to source")
			}

			source = source.UseProject(inst.Project())

			apiInst, _, err := source.GetInstance(inst.Name())
			if err != nil {
				return errors.Wrapf(err, "Failed to get instance %q", inst.Name())
			}

			isRunning := apiInst.StatusCode == api.Running

			if isRunning {
				metadata["evacuation_progress"] = fmt.Sprintf("Stopping %q in project %q", inst.Name(), inst.Project())
				op.UpdateMetadata(metadata)

				timeout := inst.ExpandedConfig()["boot.host_shutdown_timeout"]
				val, err := strconv.Atoi(timeout)
				if err != nil {
					val = 30
				}

				// Attempt a clean stop.
				stopOp, err := source.UpdateInstanceState(inst.Name(), api.InstanceStatePut{Action: "stop", Force: false, Timeout: val}, "")
				if err != nil {
					return errors.Wrapf(err, "Failed to stop instance %q", inst.Name())
				}

				// Wait for the stop operation to complete or timeout.
				err = stopOp.Wait()
				if err != nil {
					// On failure, attempt a forceful stop.
					stopOp, err = source.UpdateInstanceState(inst.Name(), api.InstanceStatePut{Action: "stop", Force: true}, "")
					if err != nil {
						// If this fails too, fail the whole operation.
						return errors.Wrapf(err, "Failed to stop instance %q", inst.Name())
					}

					// Wait for the forceful stop to complete.
					err = stopOp.Wait()
					if err != nil {
						return errors.Wrapf(err, "Failed to stop instance %q", inst.Name())
					}
				}
			}

			req := api.InstancePost{
				Name:      inst.Name(),
				Migration: true,
			}

			source = source.UseTarget(originName)

			migrationOp, err := source.MigrateInstance(inst.Name(), req)
			if err != nil {
				return errors.Wrap(err, "Migration API failure")
			}

			err = migrationOp.Wait()
			if err != nil {
				return errors.Wrap(err, "Failed to wait for migration to finish")
			}

			// Reload the instance after migration.
			inst, err := instance.LoadByProjectAndName(d.State(), inst.Project(), inst.Name())
			if err != nil {
				return errors.Wrap(err, "Failed to load instance")
			}

			config := inst.LocalConfig()
			delete(config, "volatile.evacuate.origin")

			args := db.InstanceArgs{
				Architecture: inst.Architecture(),
				Config:       config,
				Description:  inst.Description(),
				Devices:      inst.LocalDevices(),
				Ephemeral:    inst.IsEphemeral(),
				Profiles:     inst.Profiles(),
				Project:      inst.Project(),
				ExpiryDate:   inst.ExpiryDate(),
			}

			err = inst.Update(args, false)
			if err != nil {
				return errors.Wrapf(err, "Failed to update instance %q", inst.Name())
			}

			if !isRunning {
				continue
			}

			metadata["evacuation_progress"] = fmt.Sprintf("Starting %q in project %q", inst.Name(), inst.Project())
			op.UpdateMetadata(metadata)

			err = inst.Start(false)
			if err != nil {
				return errors.Wrapf(err, "Failed to start instance %q", inst.Name())
			}
		}

		err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
			err = tx.UpdateNodeStatus(node.ID, db.ClusterMemberStateCreated)
			if err != nil {
				return errors.Wrap(err, "Failed to update cluster member status")
			}

			return nil
		})
		if err != nil {
			return err
		}

		return nil
	}

	op, err := operations.OperationCreate(d.State(), "", operations.OperationClassTask, db.OperationClusterMemberRestore, nil, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// swagger:operation POST /1.0/cluster/groups cluster cluster_groups_post
//
// Create a cluster group.
//
// Creates a new cluster group.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: body
//     name: cluster
//     description: Cluster group to create
//     required: true
//     schema:
//       $ref: "#/definitions/ClusterGroupsPost"
// responses:
//   "200":
//     $ref: "#/responses/EmptySyncResponse"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func clusterGroupsPost(d *Daemon, r *http.Request) response.Response {
	clustered, err := cluster.Enabled(d.db)
	if err != nil {
		return response.SmartError(err)
	}

	if !clustered {
		return response.BadRequest(fmt.Errorf("This server is not clustered"))
	}

	req := api.ClusterGroupsPost{}

	// Parse the request.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
	err = clusterGroupValidateName(req.Name)
	if err != nil {
		return response.BadRequest(err)
	}

	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		obj := db.ClusterGroup{
			Name:        req.Name,
			Description: req.Description,
			Nodes:       req.Members,
		}

		_, err := tx.CreateClusterGroup(obj)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r)
	d.State().Events.SendLifecycle(project.Default, lifecycle.ClusterGroupCreated.Event(req.Name, requestor, nil))

	return response.SyncResponseLocation(true, nil, fmt.Sprintf("/%s/cluster/groups/%s", version.APIVersion, req.Name))
}

// swagger:operation GET /1.0/cluster/groups cluster-groups cluster_groups_get
//
// Get the cluster groups
//
// Returns a list of cluster groups (URLs).
//
// ---
// produces:
//   - application/json
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
//               "/1.0/cluster/groups/lxd01",
//               "/1.0/cluster/groups/lxd02"
//             ]
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/cluster/groups?recursion=1 cluster-groups cluster_groups_get_recursion1
//
// Get the cluster groups
//
// Returns a list of cluster groups (structs).
//
// ---
// produces:
//   - application/json
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
//           description: List of cluster groups
//           items:
//             $ref: "#/definitions/ClusterGroup"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func clusterGroupsGet(d *Daemon, r *http.Request) response.Response {
	clustered, err := cluster.Enabled(d.db)
	if err != nil {
		return response.SmartError(err)
	}

	if !clustered {
		return response.BadRequest(fmt.Errorf("This server is not clustered"))
	}

	recursion := util.IsRecursionRequest(r)

	var result interface{}

	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		if recursion {
			clusterGroups, err := tx.GetClusterGroups(db.ClusterGroupFilter{})
			if err != nil {
				return err
			}

			apiClusterGroups := make([]*api.ClusterGroup, len(clusterGroups))
			for i, clusterGroup := range clusterGroups {
				members, err := tx.GetClusterGroupNodes(clusterGroup.Name)
				if err != nil {
					return err
				}

				apiClusterGroups[i] = db.ClusterGroupToAPI(&clusterGroup, members)
			}

			result = apiClusterGroups
		} else {
			result, err = tx.GetClusterGroupURIs(db.ClusterGroupFilter{})
		}
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, result)
}

// swagger:operation GET /1.0/cluster/groups/{name} cluster-groups cluster_group_get
//
// Get the cluster group
//
// Gets a specific cluster group.
//
// ---
// produces:
//   - application/json
// responses:
//   "200":
//     description: Cluster group
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
//           $ref: "#/definitions/ClusterGroup"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func clusterGroupGet(d *Daemon, r *http.Request) response.Response {
	name := mux.Vars(r)["name"]

	clustered, err := cluster.Enabled(d.db)
	if err != nil {
		return response.SmartError(err)
	}

	if !clustered {
		return response.BadRequest(fmt.Errorf("This server is not clustered"))
	}

	var group *db.ClusterGroup

	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		// Get the cluster group.
		group, err = tx.GetClusterGroup(name)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		if errors.Is(err, db.ErrNoSuchObject) {
			return response.NotFound(fmt.Errorf("Cluster group %q not found", name))
		}

		return response.SmartError(err)
	}

	apiGroup, err := group.ToAPI()
	if err != nil {
		return response.InternalError(err)
	}

	return response.SyncResponseETag(true, apiGroup, apiGroup.ClusterGroupPut)
}

// swagger:operation POST /1.0/cluster/groups/{name} cluster-groups cluster_group_post
//
// Rename the cluster group
//
// Renames an existing cluster group.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: body
//     name: name
//     description: Cluster group rename request
//     required: true
//     schema:
//       $ref: "#/definitions/ClusterGroupPost"
// responses:
//   "200":
//     $ref: "#/responses/EmptySyncResponse"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func clusterGroupPost(d *Daemon, r *http.Request) response.Response {
	name := mux.Vars(r)["name"]
	if name == "default" {
		return response.Forbidden(errors.New(`The "default" group cannot be renamed`))
	}

	clustered, err := cluster.Enabled(d.db)
	if err != nil {
		return response.SmartError(err)
	}

	if !clustered {
		return response.BadRequest(fmt.Errorf("This server is not clustered"))
	}

	req := api.ClusterGroupPost{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
	err = clusterGroupValidateName(name)
	if err != nil {
		return response.BadRequest(err)
	}

	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		// Check that the name isn't already in use.
		_, err = tx.GetClusterGroup(req.Name)
		if err == nil {
			return fmt.Errorf("Name %q already in use", req.Name)
		}

		// Rename the cluster group.
		err = tx.RenameClusterGroup(name, req.Name)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		if errors.Is(err, db.ErrNoSuchObject) {
			return response.NotFound(fmt.Errorf("Cluster group %q not found", name))
		}

		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r)
	d.State().Events.SendLifecycle(project.Default, lifecycle.ClusterGroupRenamed.Event(req.Name, requestor, log.Ctx{"old_name": name}))

	return response.SyncResponseLocation(true, nil, fmt.Sprintf("/%s/cluster/groups/%s", version.APIVersion, req.Name))
}

// swagger:operation PUT /1.0/cluster/groups/{name} cluster-groups cluster_group_put
//
// Update the cluster group
//
// Updates the entire cluster group configuration.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: body
//     name: cluster group
//     description: cluster group configuration
//     required: true
//     schema:
//       $ref: "#/definitions/ClusterGroupPut"
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
func clusterGroupPut(d *Daemon, r *http.Request) response.Response {
	name := mux.Vars(r)["name"]

	clustered, err := cluster.Enabled(d.db)
	if err != nil {
		return response.SmartError(err)
	}

	if !clustered {
		return response.BadRequest(fmt.Errorf("This server is not clustered"))
	}

	req := api.ClusterGroupPut{}

	// Parse the request.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		group, err := tx.GetClusterGroup(name)
		if err != nil {
			return err
		}

		obj := db.ClusterGroup{
			Name:        group.Name,
			Description: req.Description,
		}

		err = tx.UpdateClusterGroup(name, obj)
		if err != nil {
			return err
		}

		members, err := tx.GetClusterGroupNodes(name)
		if err != nil {
			return err
		}

		// skipMembers is a list of members which already belong to the group.
		skipMembers := []string{}

		for _, oldMember := range members {
			if !shared.StringInSlice(oldMember, req.Members) {
				// Get all cluster groups this member belongs to.
				groups, err := tx.GetClusterGroupsWithNode(oldMember)
				if err != nil {
					return err
				}

				// Note that members who only belong to this group will not be removed from it.
				// That is because each member needs to belong to at least one group.
				if len(groups) > 1 {
					// Remove member from this group as it belongs to at least one other group.
					err = tx.RemoveNodeFromClusterGroup(name, oldMember)
					if err != nil {
						return err
					}
				}
			} else {
				skipMembers = append(skipMembers, oldMember)
			}
		}

		for _, member := range req.Members {
			// Skip these members as they already belong to this group.
			if shared.StringInSlice(member, skipMembers) {
				continue
			}

			// Add new members to the group.
			err = tx.AddNodeToClusterGroup(name, member)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r)
	d.State().Events.SendLifecycle(project.Default, lifecycle.ClusterGroupUpdated.Event(name, requestor, log.Ctx{"description": req.Description, "members": req.Members}))

	return response.EmptySyncResponse
}

// swagger:operation PATCH /1.0/cluster/groups/{name} cluster-groups cluster_group_patch
//
// Update the cluster group
//
// Updates the cluster group configuration.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: body
//     name: cluster group
//     description: cluster group configuration
//     required: true
//     schema:
//       $ref: "#/definitions/ClusterGroupPut"
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
func clusterGroupPatch(d *Daemon, r *http.Request) response.Response {
	name := mux.Vars(r)["name"]

	clustered, err := cluster.Enabled(d.db)
	if err != nil {
		return response.SmartError(err)
	}

	if !clustered {
		return response.BadRequest(fmt.Errorf("This server is not clustered"))
	}

	var clusterGroup *api.ClusterGroup
	var dbClusterGroup *db.ClusterGroup

	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		dbClusterGroup, err = tx.GetClusterGroup(name)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	clusterGroup, err = dbClusterGroup.ToAPI()
	if err != nil {
		return response.SmartError(err)
	}

	req := clusterGroup.Writable()

	// Validate the ETag.
	etag := []interface{}{clusterGroup.Description, clusterGroup.Members}
	err = util.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	// Parse the request.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if req.Members == nil {
		req.Members = clusterGroup.Members
	}

	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		obj := db.ClusterGroup{
			Name:        dbClusterGroup.Name,
			Description: req.Description,
		}

		err = tx.UpdateClusterGroup(name, obj)
		if err != nil {
			return err
		}

		members, err := tx.GetClusterGroupNodes(name)
		if err != nil {
			return err
		}

		// skipMembers is a list of members which already belong to the group.
		skipMembers := []string{}

		for _, oldMember := range members {
			if !shared.StringInSlice(oldMember, req.Members) {
				// Get all cluster groups this member belongs to.
				groups, err := tx.GetClusterGroupsWithNode(oldMember)
				if err != nil {
					return err
				}

				// Cluster member cannot be removed from the group as it doesn't belong to any other.
				if len(groups) == 1 {
					return fmt.Errorf("Cannot remove %s from group as member needs to belong to at least one group", oldMember)
				}

				// Remove member from this group as it belongs to at least one other group.
				err = tx.RemoveNodeFromClusterGroup(name, oldMember)
				if err != nil {
					return err
				}
			} else {
				skipMembers = append(skipMembers, oldMember)
			}
		}

		for _, member := range req.Members {
			// Skip these members as they already belong to this group.
			if shared.StringInSlice(member, skipMembers) {
				continue
			}

			// Add new members to the group.
			err = tx.AddNodeToClusterGroup(name, member)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r)
	d.State().Events.SendLifecycle(project.Default, lifecycle.ClusterGroupUpdated.Event(name, requestor, log.Ctx{"description": req.Description, "members": req.Members}))

	return response.EmptySyncResponse
}

// swagger:operation DELETE /1.0/cluster/groups/{name} cluster-groups cluster_group_delete
//
// Delete the cluster group.
//
// Removes the cluster group.
//
// ---
// produces:
//   - application/json
// responses:
//   "200":
//     $ref: "#/responses/EmptySyncResponse"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func clusterGroupDelete(d *Daemon, r *http.Request) response.Response {
	name := mux.Vars(r)["name"]

	// Quick checks.
	if name == "default" {
		return response.Forbidden(fmt.Errorf("The 'default' cluster group cannot be deleted"))
	}

	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		members, err := tx.GetClusterGroupNodes(name)
		if err != nil {
			return err
		}

		if len(members) > 0 {
			return fmt.Errorf("Only empty cluster groups can be removed")

		}

		return tx.DeleteClusterGroup(name)
	})

	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r)
	d.State().Events.SendLifecycle(name, lifecycle.ClusterGroupDeleted.Event(name, requestor, nil))

	return response.EmptySyncResponse
}

func clusterGroupValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("No name provided")
	}

	if strings.Contains(name, "/") {
		return fmt.Errorf("Cluster group names may not contain slashes")
	}

	if strings.Contains(name, " ") {
		return fmt.Errorf("Cluster group names may not contain spaces")
	}

	if strings.Contains(name, "_") {
		return fmt.Errorf("Cluster group names may not contain underscores")
	}

	if strings.Contains(name, "'") || strings.Contains(name, `"`) {
		return fmt.Errorf("Cluster group names may not contain quotes")
	}

	if name == "*" {
		return fmt.Errorf("Reserved cluster group name")
	}

	if shared.StringInSlice(name, []string{".", ".."}) {
		return fmt.Errorf("Invalid cluster group name %q", name)
	}

	return nil
}
