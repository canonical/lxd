package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	dqlite "github.com/canonical/go-dqlite/v3/client"
	"github.com/gorilla/mux"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/certificate"
	"github.com/canonical/lxd/lxd/cluster"
	clusterConfig "github.com/canonical/lxd/lxd/cluster/config"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/node"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	storagePools "github.com/canonical/lxd/lxd/storage"
	"github.com/canonical/lxd/lxd/task"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/osarch"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/version"
)

var clusterCmd = APIEndpoint{
	Path:        "cluster",
	MetricsType: entity.TypeClusterMember,

	Get: APIEndpointAction{Handler: clusterGet, AccessHandler: allowAuthenticated},
	Put: APIEndpointAction{Handler: clusterPut, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

var internalClusterAcceptCmd = APIEndpoint{
	Path:        "cluster/accept",
	MetricsType: entity.TypeClusterMember,

	Post: APIEndpointAction{Handler: internalClusterPostAccept, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

var internalClusterRebalanceCmd = APIEndpoint{
	Path:        "cluster/rebalance",
	MetricsType: entity.TypeClusterMember,

	Post: APIEndpointAction{Handler: internalClusterPostRebalance, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

var internalClusterAssignCmd = APIEndpoint{
	Path:        "cluster/assign",
	MetricsType: entity.TypeClusterMember,

	Post: APIEndpointAction{Handler: internalClusterPostAssign, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

var internalClusterHandoverCmd = APIEndpoint{
	Path:        "cluster/handover",
	MetricsType: entity.TypeClusterMember,

	Post: APIEndpointAction{Handler: internalClusterPostHandover, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

var internalClusterRaftNodeCmd = APIEndpoint{
	Path:        "cluster/raft-node/{address}",
	MetricsType: entity.TypeClusterMember,

	Delete: APIEndpointAction{Handler: internalClusterRaftNodeDelete, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

// swagger:operation GET /1.0/cluster cluster cluster_get
//
//	Get the cluster configuration
//
//	Gets the current cluster configuration.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: Cluster configuration
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
//	          $ref: "#/definitions/Cluster"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func clusterGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()
	serverName := s.ServerName

	// If the name is set to the hard-coded default node name, then
	// clustering is not enabled.
	if serverName == "none" {
		serverName = ""
	}

	memberConfig, err := clusterGetMemberConfig(r.Context(), s.DB.Cluster)
	if err != nil {
		return response.SmartError(err)
	}

	// Sort the member config.
	sort.Slice(memberConfig, func(i, j int) bool {
		left := memberConfig[i]
		right := memberConfig[j]

		if left.Entity != right.Entity {
			return left.Entity < right.Entity
		}

		if left.Name != right.Name {
			return left.Name < right.Name
		}

		if left.Key != right.Key {
			return left.Key < right.Key
		}

		return left.Description < right.Description
	})

	cluster := api.Cluster{
		ServerName:   serverName,
		Enabled:      serverName != "",
		MemberConfig: memberConfig,
	}

	return response.SyncResponseETag(true, cluster, cluster)
}

// Fetch information about all node-specific configuration keys set on the
// storage pools and networks of this cluster.
func clusterGetMemberConfig(ctx context.Context, cluster *db.Cluster) ([]api.ClusterMemberConfigKey, error) {
	var pools map[string]map[string]string
	var networks map[string]map[string]string

	keys := []api.ClusterMemberConfigKey{}

	err := cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		pools, err = tx.GetStoragePoolsLocalConfig(ctx)
		if err != nil {
			return fmt.Errorf("Failed fetching storage pools configuration: %w", err)
		}

		networks, err = tx.GetNetworksLocalConfig(ctx)
		if err != nil {
			return fmt.Errorf("Failed fetching networks configuration: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	for pool, config := range pools {
		for key, value := range config {
			if strings.HasPrefix(key, instancetype.ConfigVolatilePrefix) {
				continue
			}

			key := api.ClusterMemberConfigKey{
				Entity:      "storage-pool",
				Name:        pool,
				Key:         key,
				Description: fmt.Sprintf("%q property for storage pool %q", key, pool),
				Value:       value,
			}

			keys = append(keys, key)
		}
	}

	for network, config := range networks {
		for key, value := range config {
			if strings.HasPrefix(key, instancetype.ConfigVolatilePrefix) {
				continue
			}

			key := api.ClusterMemberConfigKey{
				Entity:      "network",
				Name:        network,
				Key:         key,
				Description: fmt.Sprintf("%q property for network %q", key, network),
				Value:       value,
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
//	Update the cluster configuration
//
//	Updates the entire cluster configuration.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: cluster
//	    description: Cluster configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ClusterPut"
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
func clusterPut(d *Daemon, r *http.Request) response.Response {
	req := api.ClusterPut{}

	// Parse the request
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
	if req.ServerName == "" && req.Enabled {
		return response.BadRequest(errors.New("ServerName is required when enabling clustering"))
	}

	if req.ServerName != "" && !req.Enabled {
		return response.BadRequest(errors.New("ServerName must be empty when disabling clustering"))
	}

	if req.ServerName != "" && strings.HasPrefix(req.ServerName, instancetype.TargetClusterGroupPrefix) {
		return response.BadRequest(fmt.Errorf("ServerName may not start with %q", instancetype.TargetClusterGroupPrefix))
	}

	if req.ServerName == "none" {
		return response.BadRequest(fmt.Errorf("ServerName cannot be %q", req.ServerName))
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
	s := d.State()
	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
		return response.SmartError(err)
	}

	logger.Info("Bootstrapping cluster", logger.Ctx{"serverName": req.ServerName})

	run := func(ctx context.Context, op *operations.Operation) error {
		// Update server name.
		d.globalConfigMu.Lock()
		d.serverName = req.ServerName
		d.serverClustered = true
		d.globalConfigMu.Unlock()

		d.events.SetLocalLocation(d.serverName)

		// Refresh the state.
		s = d.State()

		// Start clustering tasks
		d.startClusterTasks()

		err := cluster.Bootstrap(s, d.gateway, req.ServerName)
		if err != nil {
			d.stopClusterTasks()
			return err
		}

		// Restart the networks (to pickup forkdns and the like).
		err = networkStartup(d.State, false)
		if err != nil {
			return err
		}

		s.Events.SendLifecycle(request.ProjectParam(r), lifecycle.ClusterEnabled.Event(req.ServerName, op.EventLifecycleRequestor(), nil))

		return nil
	}

	// If there's no cluster.https_address set, but core.https_address is,
	// let's default to it.
	var config *node.Config
	err = s.DB.Node.Transaction(r.Context(), func(ctx context.Context, tx *db.NodeTx) error {
		config, err = node.ConfigLoad(ctx, tx)
		if err != nil {
			return fmt.Errorf("Failed fetching member configuration: %w", err)
		}

		localClusterAddress := config.ClusterAddress()
		if localClusterAddress != "" {
			return nil
		}

		localHTTPSAddress := config.HTTPSAddress()

		if util.IsWildCardAddress(localHTTPSAddress) {
			return fmt.Errorf("Cannot use wildcard core.https_address %q for cluster.https_address. Please specify a new cluster.https_address or core.https_address", localClusterAddress)
		}

		_, err = config.Patch(map[string]string{
			"cluster.https_address": localHTTPSAddress,
		})
		if err != nil {
			return fmt.Errorf("Copy core.https_address to cluster.https_address: %w", err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Update local config cache.
	d.globalConfigMu.Lock()
	d.localConfig = config
	d.globalConfigMu.Unlock()

	args := operations.OperationArgs{
		Type:    operationtype.ClusterBootstrap,
		Class:   operations.OperationClassTask,
		RunHook: run,
	}

	op, err := operations.CreateUserOperation(s, requestor, args)
	if err != nil {
		return response.InternalError(err)
	}

	// Add the cluster flag from the agent
	features := []string{"cluster"}
	err = version.UserAgentFeatures(features)
	if err != nil {
		logger.Warn("Failed configuring LXD user agent", logger.Ctx{"err": err, "features": features})
	}

	return operations.OperationResponse(op)
}

func clusterPutJoin(d *Daemon, r *http.Request, req api.ClusterPut) response.Response {
	s := d.State()
	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
		return response.SmartError(err)
	}

	logger.Info("Joining cluster", logger.Ctx{"serverName": req.ServerName})

	// Make sure basic pre-conditions are met.
	if len(req.ClusterCertificate) == 0 {
		return response.BadRequest(errors.New("No target cluster member certificate provided"))
	}

	if s.ServerClustered {
		return response.BadRequest(errors.New("This server is already clustered"))
	}

	// The old pre 'clustering_join' join API approach is no longer supported.
	if req.ServerAddress == "" {
		return response.BadRequest(errors.New("No server address provided for this member"))
	}

	localHTTPSAddress := s.LocalConfig.HTTPSAddress()

	var config *node.Config

	if localHTTPSAddress == "" {
		// As the user always provides a server address, but no networking
		// was setup on this node, let's do the job and open the
		// port. We'll use the same address both for the REST API and
		// for clustering.

		// First try to listen to the provided address. If we fail, we
		// won't actually update the database config.
		err := s.Endpoints.NetworkUpdateAddress(req.ServerAddress)
		if err != nil {
			return response.SmartError(err)
		}

		err = s.DB.Node.Transaction(r.Context(), func(ctx context.Context, tx *db.NodeTx) error {
			config, err = node.ConfigLoad(ctx, tx)
			if err != nil {
				return fmt.Errorf("Failed loading cluster config: %w", err)
			}

			_, err = config.Patch(map[string]string{
				"core.https_address":    req.ServerAddress,
				"cluster.https_address": req.ServerAddress,
			})
			return err
		})
		if err != nil {
			return response.SmartError(err)
		}

		localHTTPSAddress = req.ServerAddress
	} else {
		// The user has previously set core.https_address and
		// is now providing a cluster address as well. If they
		// differ we need to listen to it.
		if !util.IsAddressCovered(req.ServerAddress, localHTTPSAddress) {
			err := s.Endpoints.ClusterUpdateAddress(req.ServerAddress)
			if err != nil {
				return response.SmartError(err)
			}

			localHTTPSAddress = req.ServerAddress
		} else if util.IsWildCardAddress(localHTTPSAddress) {
			// Clustering requires an explicit address,
			// so if core.https_address is a wildcard, we should still use the explicitly defined address.
			localHTTPSAddress = req.ServerAddress
		}

		// Update the cluster.https_address config key.
		err := s.DB.Node.Transaction(r.Context(), func(ctx context.Context, tx *db.NodeTx) error {
			var err error

			config, err = node.ConfigLoad(ctx, tx)
			if err != nil {
				return fmt.Errorf("Failed loading cluster config: %w", err)
			}

			_, err = config.Patch(map[string]string{
				"cluster.https_address": localHTTPSAddress,
			})
			return err
		})
		if err != nil {
			return response.SmartError(err)
		}
	}

	// Update local config cache.
	d.globalConfigMu.Lock()
	d.localConfig = config
	d.globalConfigMu.Unlock()

	// Client parameters to connect to the target cluster node.
	serverCert := s.ServerCert()
	args := &lxd.ConnectionArgs{
		TLSClientCert: string(serverCert.PublicKey()),
		TLSClientKey:  string(serverCert.PrivateKey()),
		TLSServerCert: string(req.ClusterCertificate),
		UserAgent:     version.UserAgent,
	}

	// Asynchronously join the cluster.
	run := func(ctx context.Context, op *operations.Operation) error {
		logger.Debug("Running cluster join operation")

		// If the user has provided a join token, setup the trust
		// relationship by adding our own certificate to the cluster.
		if req.ClusterToken != "" {
			err := cluster.SetupTrust(serverCert, req)
			if err != nil {
				return fmt.Errorf("Failed setting up cluster trust: %w", err)
			}
		}

		// Now we are in the remote trust store, ensure our name and type are correct to allow the cluster
		// to associate our member name to the server certificate.
		err := cluster.UpdateTrust(serverCert, req.ServerName, req.ClusterAddress, req.ClusterCertificate)
		if err != nil {
			return fmt.Errorf("Failed updating cluster trust: %w", err)
		}

		// Connect to the target cluster node.
		client, err := lxd.ConnectLXD("https://"+req.ClusterAddress, args)
		if err != nil {
			return err
		}

		// As ServerAddress field is required to be set it means that we're using the new join API
		// introduced with the 'clustering_join' extension.
		// Connect to ourselves to initialize storage pools and networks using the API.
		localClient, err := lxd.ConnectLXDUnix(d.os.GetUnixSocket(), &lxd.ConnectionArgs{UserAgent: request.UserAgentJoiner})
		if err != nil {
			return fmt.Errorf("Failed connecting to local LXD: %w", err)
		}

		revert := revert.New()
		defer revert.Fail()

		// Update server name.
		oldServerName := d.serverName
		d.globalConfigMu.Lock()
		d.serverName = req.ServerName
		d.serverClustered = true
		d.globalConfigMu.Unlock()
		revert.Add(func() {
			d.globalConfigMu.Lock()
			d.serverName = oldServerName
			d.serverClustered = false
			d.globalConfigMu.Unlock()

			d.events.SetLocalLocation(d.serverName)
		})

		d.events.SetLocalLocation(d.serverName)
		localRevert, err := clusterInitMember(localClient, client, req.MemberConfig)
		if err != nil {
			return fmt.Errorf("Failed initializing member: %w", err)
		}

		revert.Add(localRevert)

		// Get all defined storage pools and networks, so they can be compared to the ones in the cluster.
		pools := []api.StoragePool{}
		networks := []api.InitNetworksProjectPost{}

		err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			poolNames, err := tx.GetStoragePoolNames(ctx)
			if err != nil && !response.IsNotFoundError(err) {
				return err
			}

			for _, name := range poolNames {
				_, pool, _, err := tx.GetStoragePoolInAnyState(ctx, name)
				if err != nil {
					return err
				}

				pools = append(pools, *pool)
			}

			// Get a list of projects for networks.
			var projects []dbCluster.Project

			projects, err = dbCluster.GetProjects(ctx, tx.Tx())
			if err != nil {
				return fmt.Errorf("Failed loading projects for networks: %w", err)
			}

			for _, p := range projects {
				networkNames, err := tx.GetNetworks(ctx, p.Name)
				if err != nil && !response.IsNotFoundError(err) {
					return err
				}

				for _, name := range networkNames {
					_, network, _, err := tx.GetNetworkInAnyState(ctx, p.Name, name)
					if err != nil {
						return err
					}

					internalNetwork := api.InitNetworksProjectPost{
						NetworksPost: api.NetworksPost{
							NetworkPut: network.Writable(),
							Name:       network.Name,
							Type:       network.Type,
						},
						Project: p.Name,
					}

					networks = append(networks, internalNetwork)
				}
			}

			return nil
		})
		if err != nil {
			return err
		}

		// Now request for this node to be added to the list of cluster nodes.
		info, err := clusterAcceptMember(client, req.ServerName, localHTTPSAddress, cluster.SchemaVersion, version.APIExtensionsCount(), pools, networks)
		if err != nil {
			return fmt.Errorf("Failed request to add member: %w", err)
		}

		// Update our TLS configuration using the returned cluster certificate.
		err = util.WriteCert(s.OS.VarDir, "cluster", []byte(req.ClusterCertificate), info.PrivateKey, nil)
		if err != nil {
			return fmt.Errorf("Failed saving cluster certificate: %w", err)
		}

		networkCert, err := util.LoadClusterCert(s.OS.VarDir)
		if err != nil {
			return fmt.Errorf("Failed parsing cluster certificate: %w", err)
		}

		s.Endpoints.NetworkUpdateCert(networkCert)

		// Add trusted certificates of other members to local trust store.
		trustedCerts, err := client.GetCertificates()
		if err != nil {
			return fmt.Errorf("Failed getting trusted certificates: %w", err)
		}

		for _, trustedCert := range trustedCerts {
			if trustedCert.Type == api.CertificateTypeServer {
				dbType, err := certificate.FromAPIType(trustedCert.Type)
				if err != nil {
					return err
				}

				// Store the certificate in the local database.
				dbCert := dbCluster.Certificate{
					Fingerprint: trustedCert.Fingerprint,
					Type:        dbType,
					Name:        trustedCert.Name,
					Certificate: trustedCert.Certificate,
					Restricted:  trustedCert.Restricted,
				}

				logger.Debugf("Adding certificate %q (%s) to local trust store", trustedCert.Name, trustedCert.Fingerprint)

				err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					id, err := dbCluster.CreateCertificate(ctx, tx.Tx(), dbCert)
					if err != nil {
						return err
					}

					err = dbCluster.UpdateCertificateProjects(ctx, tx.Tx(), int(id), trustedCert.Projects)
					if err != nil {
						return err
					}

					return nil
				})
				if err != nil && !api.StatusErrorCheck(err, http.StatusConflict) {
					return fmt.Errorf("Failed adding local trusted certificate %q (%s): %w", trustedCert.Name, trustedCert.Fingerprint, err)
				}
			}
		}

		// Update cached trusted certificates (this adds the server certificates we collected above) so that we are able to join.
		// Client and metric type certificates from the cluster we are joining will not be added until later.
		s.UpdateIdentityCache()

		// Update local setup and possibly join the raft dqlite cluster.
		nodes := make([]db.RaftNode, 0, len(info.RaftNodes))
		for _, node := range info.RaftNodes {
			nodes = append(nodes, db.RaftNode{
				NodeInfo: dqlite.NodeInfo{
					ID:      node.ID,
					Address: node.Address,
					Role:    db.RaftRole(node.Role),
				},
			})
		}

		err = cluster.Join(s, d.gateway, networkCert, serverCert, req.ServerName, nodes)
		if err != nil {
			return err
		}

		// Start clustering tasks.
		d.startClusterTasks()
		revert.Add(func() { d.stopClusterTasks() })

		// Handle optional service integration on cluster join
		err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			// Add the new node to the default cluster group.
			err := tx.AddNodeToClusterGroup(ctx, "default", req.ServerName)
			if err != nil {
				return fmt.Errorf("Failed adding new member to the default cluster group: %w", err)
			}

			return nil
		})
		if err != nil {
			return err
		}

		var nodeConfig *node.Config
		err = s.DB.Node.Transaction(ctx, func(ctx context.Context, tx *db.NodeTx) error {
			var err error
			nodeConfig, err = node.ConfigLoad(ctx, tx)
			return err
		})
		if err != nil {
			return err
		}

		// Get the current (updated) config.
		var currentClusterConfig *clusterConfig.Config
		err = d.db.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			currentClusterConfig, err = clusterConfig.Load(ctx, tx)
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return err
		}

		d.globalConfigMu.Lock()
		d.localConfig = nodeConfig
		d.globalConfig = currentClusterConfig
		d.globalConfigMu.Unlock()

		existingConfigDump := currentClusterConfig.Dump()
		changes := make(map[string]string, len(existingConfigDump))
		maps.Copy(changes, existingConfigDump)

		// Copy the old config so that the update triggers have access to it.
		// In this case it will not be used as we are not changing any node values.
		oldNodeConfig := make(map[string]string)
		maps.Copy(oldNodeConfig, s.LocalConfig.Dump())

		err = doAPI10UpdateTriggers(d, nil, changes, oldNodeConfig, nodeConfig, currentClusterConfig)
		if err != nil {
			return err
		}

		// Refresh the state.
		s = d.State()

		// Start up networks so any post-join changes can be applied now that we have a Node ID.
		logger.Debug("Starting networks after cluster join")
		err = networkStartup(d.State, false)
		if err != nil {
			logger.Errorf("Failed starting networks: %v", err)
		}

		client, err = cluster.Connect(ctx, req.ClusterAddress, s.Endpoints.NetworkCert(), serverCert, true)
		if err != nil {
			return err
		}

		// Add the cluster flag from the agent
		features := []string{"cluster"}
		err = version.UserAgentFeatures(features)
		if err != nil {
			logger.Warn("Failed configuring LXD user agent", logger.Ctx{"err": err, "features": features})
		}

		// Notify the leader of successful join, possibly triggering
		// role changes.
		_, _, err = client.RawQuery(http.MethodPost, "/internal/cluster/rebalance", nil, "")
		if err != nil {
			logger.Warnf("Failed triggering cluster rebalance: %v", err)
		}

		// Ensure all images are available after this node has joined.
		err = autoSyncImages(ctx, s)
		if err != nil {
			logger.Warn("Failed syncing images")
		}

		// Update the identity cache again to add identities from the cluster we're joining..
		s.UpdateIdentityCache()

		s.Events.SendLifecycle(request.ProjectParam(r), lifecycle.ClusterMemberAdded.Event(req.ServerName, op.EventLifecycleRequestor(), nil))

		revert.Success()
		return nil
	}

	opArgs := operations.OperationArgs{
		Type:    operationtype.ClusterJoin,
		Class:   operations.OperationClassTask,
		RunHook: run,
	}

	op, err := operations.CreateUserOperation(s, requestor, opArgs)
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
	s := d.State()

	logger.Info("Disabling clustering", logger.Ctx{"serverName": req.ServerName})

	// Close the cluster database
	err := s.DB.Cluster.Close()
	if err != nil {
		return response.SmartError(err)
	}

	// Update our TLS configuration using our original certificate.
	for _, suffix := range []string{"crt", "key", "ca"} {
		path := filepath.Join(s.OS.VarDir, "cluster."+suffix)
		if !shared.PathExists(path) {
			continue
		}

		err := os.Remove(path)
		if err != nil {
			return response.InternalError(err)
		}
	}

	networkCert, err := util.LoadCert(s.OS.VarDir)
	if err != nil {
		return response.InternalError(fmt.Errorf("Failed parsing member certificate: %w", err))
	}

	// Reset the cluster database and make it local to this node.
	err = d.gateway.Reset(networkCert)
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r.Context())
	s.Events.SendLifecycle(request.ProjectParam(r), lifecycle.ClusterDisabled.Event(req.ServerName, requestor, nil))

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
			// When removing a node from a cluster, we try to re-exec its daemon to clear its state,
			// but if LXD is using systemd socket activation then we just want to call os.Exit() directly.
			// In this case the socket FDs and environment vars may be different, so we can't re-exec.
			os.Exit(0) //nolint:revive
		}

		logger.Info("Restarting LXD daemon following removal from cluster")
		err = util.ReplaceDaemon()
		if err != nil {
			logger.Error("Failed restarting LXD daemon", logger.Ctx{"err": err})
		}
	}()

	return response.ManualResponse(func(w http.ResponseWriter) error {
		err := response.EmptySyncResponse.Render(w, r)
		if err != nil {
			return err
		}

		// Send the response before replacing the LXD daemon process.
		f, ok := w.(http.Flusher)
		if !ok {
			return errors.New("http.ResponseWriter is not type http.Flusher")
		}

		f.Flush()

		return nil
	})
}

// clusterInitMember initialises storage pools and networks on this member. We pass two LXD client instances, one
// connected to ourselves (the joining member) and one connected to the target cluster member to join.
// Returns a revert fail function that can be used to undo this function if a subsequent step fails.
func clusterInitMember(d lxd.InstanceServer, client lxd.InstanceServer, memberConfig []api.ClusterMemberConfigKey) (revert.Hook, error) {
	data := api.InitLocalPreseed{}

	// Fetch all pools currently defined in the cluster.
	pools, err := client.GetStoragePools()
	if err != nil {
		return nil, fmt.Errorf("Failed fetching information about cluster storage pools: %w", err)
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
			StoragePoolPut: pool.Writable(),
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

			if !slices.Contains(db.NodeSpecificStorageConfig, config.Key) {
				logger.Warnf("Ignoring config key %q for storage pool %q", config.Key, config.Name)
				continue
			}

			post.Config[config.Key] = config.Value
		}

		data.StoragePools = append(data.StoragePools, post)
	}

	projects, err := client.GetProjects()
	if err != nil {
		return nil, fmt.Errorf("Failed fetching project information about cluster networks: %w", err)
	}

	for _, p := range projects {
		if shared.IsFalseOrEmpty(p.Config["features.networks"]) && p.Name != api.ProjectDefaultName {
			// Skip non-default projects that can't have their own networks so we don't try
			// and add the same default project networks twice.
			continue
		}

		// Request that the project be created first before the project specific networks.
		data.Projects = append(data.Projects, api.ProjectsPost{
			Name: p.Name,
			ProjectPut: api.ProjectPut{
				Description: p.Description,
				Config:      p.Config,
			},
		})

		// Fetch all project specific networks currently defined in the cluster for the project.
		networks, err := client.UseProject(p.Name).GetNetworks()
		if err != nil {
			return nil, fmt.Errorf("Failed fetching network information about cluster networks in project %q: %w", p.Name, err)
		}

		// Merge the returned networks configs with the node-specific configs provided by the user.
		for _, network := range networks {
			// Skip unmanaged or pending networks.
			if !network.Managed || network.Status != api.NetworkStatusCreated {
				continue
			}

			post := api.InitNetworksProjectPost{
				NetworksPost: api.NetworksPost{
					NetworkPut: network.Writable(),
					Name:       network.Name,
					Type:       network.Type,
				},
				Project: p.Name,
			}

			// Apply the node-specific config supplied by the user for networks in the default project.
			// At this time project specific networks don't have node specific config options.
			if p.Name == api.ProjectDefaultName {
				for _, config := range memberConfig {
					if config.Entity != "network" {
						continue
					}

					if config.Name != network.Name {
						continue
					}

					if !slices.Contains(db.NodeSpecificNetworkConfig, config.Key) {
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
		return nil, fmt.Errorf("Failed initializing storage pools and networks: %w", err)
	}

	return revert, nil
}

// Perform a request to the /internal/cluster/accept endpoint to check if a new
// node can be accepted into the cluster and obtain joining information such as
// the cluster private certificate.
func clusterAcceptMember(client lxd.InstanceServer, name string, address string, schema int, apiExt int, pools []api.StoragePool, networks []api.InitNetworksProjectPost) (*internalClusterPostAcceptResponse, error) {
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
	resp, _, err := client.RawQuery(http.MethodPost, "/internal/cluster/accept", req, "")
	if err != nil {
		return nil, err
	}

	err = resp.MetadataAsStruct(&info)
	if err != nil {
		return nil, err
	}

	return info, nil
}

func internalClusterPostAccept(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	leaderInfo, err := s.LeaderInfo()
	if err != nil {
		return response.InternalError(err)
	}

	if !leaderInfo.Clustered {
		return response.InternalError(cluster.ErrNodeIsNotClustered)
	}

	if !leaderInfo.Leader {
		logger.Debug("Redirect member accept request", logger.Ctx{"leader": leaderInfo.Address})
		url := &url.URL{
			Scheme: "https",
			Path:   "/internal/cluster/accept",
			Host:   leaderInfo.Address,
		}

		return response.SyncResponseRedirect(url.String())
	}

	req := internalClusterPostAcceptRequest{}

	// Parse the request
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
	if req.Name == "" {
		return response.BadRequest(errors.New("No name provided"))
	}

	// Get lock now we are on leader.
	d.clusterMembershipMutex.Lock()
	defer d.clusterMembershipMutex.Unlock()

	// Check that the pools and networks provided by the joining node have
	// configs that match the cluster ones.
	err = clusterCheckStoragePoolsMatch(r.Context(), s.DB.Cluster, req.StoragePools)
	if err != nil {
		return response.SmartError(err)
	}

	err = clusterCheckNetworksMatch(r.Context(), s.DB.Cluster, req.Networks)
	if err != nil {
		return response.SmartError(err)
	}

	nodes, err := cluster.Accept(s, d.gateway, req.Name, req.Address, req.Schema, req.API, req.Architecture)
	if err != nil {
		return response.BadRequest(err)
	}

	accepted := internalClusterPostAcceptResponse{
		RaftNodes:  make([]internalRaftNode, 0, len(nodes)),
		PrivateKey: s.Endpoints.NetworkPrivateKey(),
	}

	for _, node := range nodes {
		accepted.RaftNodes = append(accepted.RaftNodes, internalRaftNode{
			ID:      node.ID,
			Address: node.Address,
			Role:    int(node.Role),
		})
	}

	return response.SyncResponse(true, accepted)
}

// A request for the /internal/cluster/accept endpoint.
type internalClusterPostAcceptRequest struct {
	Name         string                        `json:"name" yaml:"name"`
	Address      string                        `json:"address" yaml:"address"`
	Schema       int                           `json:"schema" yaml:"schema"`
	API          int                           `json:"api" yaml:"api"`
	StoragePools []api.StoragePool             `json:"storage_pools" yaml:"storage_pools"`
	Networks     []api.InitNetworksProjectPost `json:"networks" yaml:"networks"`
	Architecture int                           `json:"architecture" yaml:"architecture"`
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
	s := d.State()

	leaderInfo, err := s.LeaderInfo()
	if err != nil {
		return response.InternalError(err)
	}

	if !leaderInfo.Clustered {
		return response.InternalError(cluster.ErrNodeIsNotClustered)
	}

	if !leaderInfo.Leader {
		logger.Debug("Redirect cluster rebalance request", logger.Ctx{"leader": leaderInfo.Address})
		url := &url.URL{
			Scheme: "https",
			Path:   "/internal/cluster/rebalance",
			Host:   leaderInfo.Address,
		}

		return response.SyncResponseRedirect(url.String())
	}

	// Get lock now we are on leader.
	d.clusterMembershipMutex.Lock()
	defer d.clusterMembershipMutex.Unlock()

	err = rebalanceMemberRoles(r.Context(), s, d.gateway, nil)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, nil)
}

// Check if there's a dqlite node whose role should be changed, and post a
// change role request if so.
func rebalanceMemberRoles(ctx context.Context, s *state.State, gateway *cluster.Gateway, unavailableMembers []string) error {
	if s.ShutdownCtx.Err() != nil {
		return nil
	}

	for {
		address, nodes, connectivity, err := cluster.GetNextRoleChange(s, gateway, unavailableMembers)
		if err != nil {
			return err
		}

		if address == "" {
			// Nothing to do.
			return nil
		}

		// Process demotions of offline nodes immediately.
		demoted := false
		for _, node := range nodes {
			if node.Address != address || node.Role != db.RaftSpare {
				continue
			}

			if connectivity[address] {
				break
			}

			logger.Info("Demoting offline member during rebalance", logger.Ctx{"candidateAddress": node.Address})
			err := gateway.DemoteOfflineNode(node.ID)
			if err != nil {
				return fmt.Errorf("Demote offline node %s: %w", node.Address, err)
			}

			demoted = true
			break
		}

		if demoted {
			continue
		}

		// Tell the node to promote itself.
		logger.Info("Promoting member during rebalance", logger.Ctx{"candidateAddress": address})
		err = changeMemberRole(ctx, s, address, nodes)
		if err != nil {
			return err
		}
	}
}

// Check if there are nodes not part of the raft configuration and add them in
// case.
func upgradeNodesWithoutRaftRole(s *state.State, gateway *cluster.Gateway) error {
	if s.ShutdownCtx.Err() != nil {
		return nil
	}

	var members []db.NodeInfo
	err := s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		members, err = tx.GetNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed getting cluster members: %w", err)
		}

		return nil
	})
	if err != nil {
		return err
	}

	return cluster.UpgradeMembersWithoutRole(gateway, members)
}

// Post a change role request to the member with the given address. The nodes
// slice contains details about all members, including the one being changed.
func changeMemberRole(ctx context.Context, s *state.State, address string, nodes []db.RaftNode) error {
	post := &internalClusterPostAssignRequest{}
	for _, node := range nodes {
		post.RaftNodes = append(post.RaftNodes, internalRaftNode{
			ID:      node.ID,
			Address: node.Address,
			Role:    int(node.Role),
			Name:    node.Name,
		})
	}

	client, err := cluster.Connect(ctx, address, s.Endpoints.NetworkCert(), s.ServerCert(), true)
	if err != nil {
		return err
	}

	_, _, err = client.RawQuery(http.MethodPost, "/internal/cluster/assign", post, "")
	if err != nil {
		return err
	}

	return nil
}

// Try to handover the role of this member to another one.
func handoverMemberRole(s *state.State, gateway *cluster.Gateway) error {
	// If we aren't clustered, there's nothing to do.
	if !s.ServerClustered {
		return nil
	}

	// Figure out our own cluster address.
	localClusterAddress := s.LocalConfig.ClusterAddress()

	post := &internalClusterPostHandoverRequest{
		Address: localClusterAddress,
	}

	logCtx := logger.Ctx{"address": localClusterAddress}

	// Find the cluster leader.
findLeader:
	leaderInfo, err := s.LeaderInfo()
	if err != nil {
		return err
	}

	if leaderInfo.Leader {
		logger.Info("Transferring leadership", logCtx)
		err := gateway.TransferLeadership()
		if err != nil {
			return fmt.Errorf("Failed transferring leadership: %w", err)
		}

		goto findLeader
	}

	logger.Info("Handing over cluster member role", logCtx)
	client, err := cluster.Connect(context.Background(), leaderInfo.Address, s.Endpoints.NetworkCert(), s.ServerCert(), true)
	if err != nil {
		return fmt.Errorf("Failed connecting to leader to hand over cluster member role: %w", err)
	}

	_, _, err = client.RawQuery(http.MethodPost, "/internal/cluster/handover", post, "")
	if err != nil {
		return fmt.Errorf("Failed requesting cluster member role handover: %w", err)
	}

	return nil
}

// Used to assign a new role to a the local dqlite node.
func internalClusterPostAssign(d *Daemon, r *http.Request) response.Response {
	s := d.State()
	req := internalClusterPostAssignRequest{}

	// Parse the request
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
	if len(req.RaftNodes) == 0 {
		return response.BadRequest(errors.New("No raft members provided"))
	}

	nodes := make([]db.RaftNode, 0, len(req.RaftNodes))
	for _, node := range req.RaftNodes {
		nodes = append(nodes, db.RaftNode{
			NodeInfo: dqlite.NodeInfo{
				ID:      node.ID,
				Address: node.Address,
				Role:    db.RaftRole(node.Role),
			},
			Name: node.Name,
		})
	}

	err = cluster.Assign(s, d.gateway, nodes)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, nil)
}

// A request for the /internal/cluster/assign endpoint.
type internalClusterPostAssignRequest struct {
	RaftNodes []internalRaftNode `json:"raft_nodes" yaml:"raft_nodes"`
}

// Used to to transfer the responsibilities of a member to another one.
func internalClusterPostHandover(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	leaderInfo, err := s.LeaderInfo()
	if err != nil {
		return response.InternalError(err)
	}

	if !leaderInfo.Clustered {
		return response.InternalError(cluster.ErrNodeIsNotClustered)
	}

	if !leaderInfo.Leader {
		logger.Debug("Redirect handover request", logger.Ctx{"leader": leaderInfo.Address})
		url := &url.URL{
			Scheme: "https",
			Path:   "/internal/cluster/handover",
			Host:   leaderInfo.Address,
		}

		return response.SyncResponseRedirect(url.String())
	}

	req := internalClusterPostHandoverRequest{}

	// Parse the request
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
	if req.Address == "" {
		return response.BadRequest(errors.New("No ID provided"))
	}

	localClusterAddress := s.LocalConfig.ClusterAddress()

	// Get lock now we are on leader.
	d.clusterMembershipMutex.Lock()
	defer d.clusterMembershipMutex.Unlock()

	target, nodes, err := cluster.Handover(s, d.gateway, req.Address)
	if err != nil {
		return response.SmartError(err)
	}

	// If there's no other member we can promote, there's nothing we can do, just return.
	if target == "" {
		logger.Warn("No other cluster member to handover to", logger.Ctx{"losingAddress": req.Address})
		goto out
	}

	logger.Info("Promoting member during handover", logger.Ctx{"address": localClusterAddress, "losingAddress": req.Address, "candidateAddress": target})
	err = changeMemberRole(r.Context(), s, target, nodes)
	if err != nil {
		return response.SmartError(err)
	}

	// Demote the member that is handing over.
	for i, node := range nodes {
		if node.Address == req.Address {
			nodes[i].Role = db.RaftSpare
		}
	}

	logger.Info("Demoting member during handover", logger.Ctx{"address": localClusterAddress, "losingAddress": req.Address})
	err = changeMemberRole(r.Context(), s, req.Address, nodes)
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

func clusterCheckStoragePoolsMatch(ctx context.Context, cluster *db.Cluster, reqPools []api.StoragePool) error {
	return cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		poolNames, err := tx.GetCreatedStoragePoolNames(ctx)
		if err != nil && !response.IsNotFoundError(err) {
			return err
		}

		for _, name := range poolNames {
			found := false
			for _, reqPool := range reqPools {
				if reqPool.Name != name {
					continue
				}

				found = true

				var pool *api.StoragePool

				_, pool, _, err = tx.GetStoragePoolInAnyState(ctx, name)
				if err != nil {
					return err
				}

				if pool.Driver != reqPool.Driver {
					return fmt.Errorf("Mismatching driver for storage pool %s", name)
				}
				// Exclude the keys which are node-specific.
				exclude := db.NodeSpecificStorageConfig
				err = util.CompareConfigs(pool.Config, reqPool.Config, exclude)
				if err != nil {
					return fmt.Errorf("Mismatching config for storage pool %s: %w", name, err)
				}

				break
			}

			if !found {
				return fmt.Errorf("Missing storage pool %s", name)
			}
		}

		return nil
	})
}

func clusterCheckNetworksMatch(ctx context.Context, cluster *db.Cluster, reqNetworks []api.InitNetworksProjectPost) error {
	return cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		// Get a list of projects for networks.
		networkProjectNames, err := dbCluster.GetProjectNames(ctx, tx.Tx())
		if err != nil {
			return fmt.Errorf("Failed loading projects for networks: %w", err)
		}

		for _, networkProjectName := range networkProjectNames {
			networkNames, err := tx.GetCreatedNetworkNamesByProject(ctx, networkProjectName)
			if err != nil && !response.IsNotFoundError(err) {
				return err
			}

			for _, networkName := range networkNames {
				found := false

				for _, reqNetwork := range reqNetworks {
					if reqNetwork.Name != networkName || reqNetwork.Project != networkProjectName {
						continue
					}

					found = true

					_, network, _, err := tx.GetNetworkInAnyState(ctx, networkProjectName, networkName)
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
						return fmt.Errorf("Mismatching config for network %q in project %q: %w", network.Name, networkProjectName, err)
					}

					break
				}

				if !found {
					return fmt.Errorf("Missing network %q in project %q", networkName, networkProjectName)
				}
			}
		}

		return nil
	})
}

// Used as low-level recovering helper.
func internalClusterRaftNodeDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	address, err := url.PathUnescape(mux.Vars(r)["address"])
	if err != nil {
		return response.SmartError(err)
	}

	err = cluster.RemoveRaftNode(d.gateway, address)
	if err != nil {
		return response.SmartError(err)
	}

	err = rebalanceMemberRoles(r.Context(), s, d.gateway, nil)
	if err != nil && !errors.Is(err, cluster.ErrNotLeader) {
		logger.Warn("Could not rebalance cluster member roles after raft member removal", logger.Ctx{"err": err})
	}

	return response.SyncResponse(true, nil)
}

func autoHealClusterTask(stateFunc func() *state.State, gateway *cluster.Gateway) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		op, err := autoHealCluster(ctx, stateFunc(), gateway)
		if err != nil {
			return
		}

		err = op.Start()
		if err != nil {
			logger.Error("Failed starting cluster instances heal operation", logger.Ctx{"err": err})
			return
		}

		err = op.Wait(ctx)
		if err != nil {
			logger.Error("Failed healing cluster instances", logger.Ctx{"err": err})
			return
		}
	}

	return f, task.Every(time.Minute)
}

func autoHealCluster(ctx context.Context, s *state.State, gateway *cluster.Gateway) (*operations.Operation, error) {
	healingThreshold := s.GlobalConfig.ClusterHealingThreshold()
	if healingThreshold == 0 {
		return nil, errors.New("Skipping healing cluster instances as cluster healing is disabled")
	}

	leaderInfo, err := s.LeaderInfo()
	if err != nil {
		return nil, fmt.Errorf("Failed determining cluster leader: %w", err)
	}

	if !leaderInfo.Clustered || !leaderInfo.Leader {
		return nil, errors.New("Skipping healing cluster instances on non-leader member")
	}

	var offlineMembers []db.NodeInfo
	{
		var members []db.NodeInfo
		err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			members, err = tx.GetNodes(ctx)
			if err != nil {
				return fmt.Errorf("Failed getting cluster members: %w", err)
			}

			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("Failed healing cluster instances: %w", err)
		}

		for _, member := range members {
			// Ignore members which have been evacuated, and those which haven't exceeded the
			// healing offline trigger threshold.
			if member.State == db.ClusterMemberStateEvacuated || !member.IsOffline(healingThreshold) {
				continue
			}

			offlineMembers = append(offlineMembers, member)
		}
	}

	if len(offlineMembers) == 0 {
		return nil, errors.New("Skipping healing cluster instances as there are no cluster members to evacuate")
	}

	opRun := func(ctx context.Context, op *operations.Operation) error {
		for _, member := range offlineMembers {
			err := healClusterMember(s, gateway, op, member.Name)
			if err != nil {
				logger.Error("Failed healing cluster instances", logger.Ctx{"err": err})
				return err
			}
		}

		return nil
	}

	args := operations.OperationArgs{
		Type:    operationtype.ClusterHeal,
		Class:   operations.OperationClassTask,
		RunHook: opRun,
	}

	op, err := operations.CreateServerOperation(s, args)
	if err != nil {
		return nil, fmt.Errorf("Failed creating cluster instances heal operation: %w", err)
	}

	return op, nil
}

func healClusterMember(s *state.State, gateway *cluster.Gateway, op *operations.Operation, name string) error {
	logger.Info("Starting cluster healing", logger.Ctx{"member": name})
	defer logger.Info("Completed cluster healing", logger.Ctx{"member": name})

	migrateFunc := func(ctx context.Context, s *state.State, inst instance.Instance, targetMemberInfo *db.NodeInfo, live bool, startInstance bool, op *operations.Operation) error {
		// This returns an error if the instance's storage pool is local.
		// Since we only care about remote backed instances, this can be ignored and return nil instead.
		poolName, err := inst.StoragePool()
		if err != nil {
			if api.StatusErrorCheck(err, http.StatusNotFound) {
				return nil // We only care about remote backed instances.
			}

			return err
		}

		pool, err := storagePools.LoadByName(s, poolName)
		if err != nil {
			return fmt.Errorf("Failed loading storage pool %q: %w", poolName, err)
		}

		// Ignore instances on local storage pools.
		if !pool.Driver().Info().Remote {
			return nil
		}

		// Migrate the instance.
		req := api.InstancePost{
			Migration: true,
		}

		dest, err := cluster.Connect(ctx, targetMemberInfo.Address, s.Endpoints.NetworkCert(), s.ServerCert(), true)
		if err != nil {
			return err
		}

		dest = dest.UseProject(inst.Project().Name)
		dest = dest.UseTarget(targetMemberInfo.Name)

		migrateOp, err := dest.MigrateInstance(inst.Name(), req)
		if err != nil {
			return err
		}

		err = migrateOp.Wait()
		if err != nil {
			return err
		}

		if !startInstance || live {
			return nil
		}

		// Start it back up on target.
		startOp, err := dest.UpdateInstanceState(inst.Name(), api.InstanceStatePut{Action: "start"}, "")
		if err != nil {
			return err
		}

		err = startOp.Wait()
		if err != nil {
			return err
		}

		return nil
	}

	err := evacuateClusterMember(context.Background(), s, gateway, op, name, api.ClusterEvacuateModeHeal, nil, migrateFunc)
	if err != nil {
		logger.Error("Failed healing cluster member", logger.Ctx{"member": name, "err": err})
		return err
	}

	s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.ClusterMemberHealed.Event(name, op.EventLifecycleRequestor(), nil))
	return nil
}
