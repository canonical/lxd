package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/CanonicalLtd/go-dqlite"
	"github.com/gorilla/mux"
	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/node"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"
	"github.com/pkg/errors"
)

var clusterCmd = Command{
	name: "cluster",
	get:  clusterGet,
	put:  clusterPut,
}

var clusterNodesCmd = Command{
	name: "cluster/members",
	get:  clusterNodesGet,
}

var clusterNodeCmd = Command{
	name:   "cluster/members/{name}",
	get:    clusterNodeGet,
	post:   clusterNodePost,
	delete: clusterNodeDelete,
}

var internalClusterAcceptCmd = Command{
	name: "cluster/accept",
	post: internalClusterPostAccept,
}

var internalClusterRebalanceCmd = Command{
	name: "cluster/rebalance",
	post: internalClusterPostRebalance,
}

var internalClusterPromoteCmd = Command{
	name: "cluster/promote",
	post: internalClusterPostPromote,
}

// Return information about the cluster.
func clusterGet(d *Daemon, r *http.Request) Response {
	name := ""
	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		name, err = tx.NodeName()
		return err
	})
	if err != nil {
		return SmartError(err)
	}

	// If the name is set to the hard-coded default node name, then
	// clustering is not enabled.
	if name == "none" {
		name = ""
	}

	memberConfig, err := clusterGetMemberConfig(d.cluster)
	if err != nil {
		return SmartError(err)
	}

	cluster := api.Cluster{
		ServerName:   name,
		Enabled:      name != "",
		MemberConfig: memberConfig,
	}

	return SyncResponseETag(true, cluster, cluster)
}

// Fetch information about all node-specific configuration keys set on the
// storage pools and networks of this cluster.
func clusterGetMemberConfig(cluster *db.Cluster) ([]api.ClusterMemberConfigKey, error) {
	var pools map[string]map[string]string
	var networks map[string]map[string]string

	keys := []api.ClusterMemberConfigKey{}

	err := cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error

		pools, err = tx.StoragePoolsNodeConfig()
		if err != nil {
			return errors.Wrapf(err, "Failed to fetch storage pools configuration")
		}

		networks, err = tx.NetworksNodeConfig()
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
func clusterPut(d *Daemon, r *http.Request) Response {
	req := api.ClusterPut{}

	// Parse the request
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return BadRequest(err)
	}

	// Sanity checks
	if req.ServerName == "" && req.Enabled {
		return BadRequest(fmt.Errorf("ServerName is required when enabling clustering"))
	}
	if req.ServerName != "" && !req.Enabled {
		return BadRequest(fmt.Errorf("ServerName must be empty when disabling clustering"))
	}

	// Disable clustering.
	if !req.Enabled {
		return clusterPutDisable(d)
	}

	// Depending on the provided parameters we either bootstrap a brand new
	// cluster with this node as first node, or perform a request to join a
	// given cluster.
	if req.ClusterAddress == "" {
		return clusterPutBootstrap(d, req)
	}

	return clusterPutJoin(d, req)
}

func clusterPutBootstrap(d *Daemon, req api.ClusterPut) Response {
	run := func(op *operation) error {
		// The default timeout when non-clustered is one minute, let's
		// lower it down now that we'll likely have to make requests
		// over the network.
		//
		// FIXME: this is a workaround for #5234.
		d.cluster.SetDefaultTimeout(5 * time.Second)

		// Start clustering tasks
		d.startClusterTasks()

		err := cluster.Bootstrap(d.State(), d.gateway, req.ServerName)
		if err != nil {
			d.cluster.SetDefaultTimeout(time.Minute)
			d.stopClusterTasks()
			return err
		}

		return nil
	}
	resources := map[string][]string{}
	resources["cluster"] = []string{}

	// If there's no cluster.https_address set, but core.https_address is,
	// let's default to it.
	d.db.Transaction(func(tx *db.NodeTx) error {
		config, err := node.ConfigLoad(tx)
		if err != nil {
			return errors.Wrap(err, "Fetch node configuration")
		}

		clusterAddress := config.ClusterAddress()
		if clusterAddress != "" {
			return nil
		}

		address := config.HTTPSAddress()

		_, err = config.Patch(map[string]interface{}{
			"cluster.https_address": address,
		})
		if err != nil {
			return errors.Wrap(err, "Copy core.https_address to cluster.https_address")
		}

		return nil
	})

	op, err := operationCreate(d.cluster, "", operationClassTask, db.OperationClusterBootstrap, resources, nil, run, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	// Add the cluster flag from the agent
	version.UserAgentFeatures([]string{"cluster"})

	return OperationResponse(op)
}

func clusterPutJoin(d *Daemon, req api.ClusterPut) Response {
	// Make sure basic pre-conditions are met.
	if len(req.ClusterCertificate) == 0 {
		return BadRequest(fmt.Errorf("No target cluster node certificate provided"))
	}

	clusterAddress, err := node.ClusterAddress(d.db)
	if err != nil {
		return SmartError(err)
	}
	if clusterAddress != "" {
		return BadRequest(fmt.Errorf("This server is already clustered"))
	}

	address, err := node.HTTPSAddress(d.db)
	if err != nil {
		return SmartError(err)
	}

	if address == "" {
		if req.ServerAddress == "" {
			return BadRequest(fmt.Errorf("No core.https_address config key is set on this node"))
		}

		// The user has provided a server address, and no networking
		// was setup on this node, let's do the job and open the
		// port. We'll use the same address both for the REST API and
		// for clustering.

		// First try to listen to the provided address. If we fail, we
		// won't actually update the database config.
		err = d.endpoints.NetworkUpdateAddress(req.ServerAddress)
		if err != nil {
			return SmartError(err)
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
			return SmartError(err)
		}

		address = req.ServerAddress
	} else {
		if req.ServerAddress != "" {
			// The user has previously set core.https_address and
			// is now providing a cluster address as well. If they
			// differ we need to listen to it.
			if !util.IsAddressCovered(req.ServerAddress, address) {
				err := d.endpoints.ClusterUpdateAddress(req.ServerAddress)
				if err != nil {
					return SmartError(err)
				}
				address = req.ServerAddress
			}
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
			return SmartError(err)
		}
	}

	// Client parameters to connect to the target cluster node.
	cert := d.endpoints.NetworkCert()
	args := &lxd.ConnectionArgs{
		TLSClientCert: string(cert.PublicKey()),
		TLSClientKey:  string(cert.PrivateKey()),
		TLSServerCert: string(req.ClusterCertificate),
	}
	fingerprint := cert.Fingerprint()

	// Asynchronously join the cluster.
	run := func(op *operation) error {
		logger.Debug("Running cluster join operation")

		// If the user has provided a cluster password, setup the trust
		// relationship by adding our own certificate to the cluster.
		if req.ClusterPassword != "" {
			err = cluster.SetupTrust(string(cert.PublicKey()), req.ClusterAddress,
				string(req.ClusterCertificate), req.ClusterPassword)
			if err != nil {
				return errors.Wrap(err, "Failed to setup cluster trust")
			}
		}

		// Connect to the target cluster node.
		client, err := lxd.ConnectLXD(fmt.Sprintf("https://%s", req.ClusterAddress), args)
		if err != nil {
			return err
		}

		// If the ServerAddress field is set it means that we're using
		// the new join API introduced with the 'clustering_join'
		// extension.
		if req.ServerAddress != "" {
			// Connect to ourselves to initialize storage pools and
			// networks using the API.
			d, err := lxd.ConnectLXDUnix(d.UnixSocket(), nil)
			if err != nil {
				return errors.Wrap(err, "Failed to connect to local LXD")
			}

			err = clusterInitMember(d, client, req.MemberConfig)
			if err != nil {
				return errors.Wrap(err, "Failed to initialize member")
			}
		}

		// Get all defined storage pools and networks, so they can be compared
		// to the ones in the cluster.
		pools := []api.StoragePool{}
		poolNames, err := d.cluster.StoragePools()
		if err != nil && err != db.ErrNoSuchObject {
			return err
		}

		for _, name := range poolNames {
			_, pool, err := d.cluster.StoragePoolGet(name)
			if err != nil {
				return err
			}
			pools = append(pools, *pool)
		}

		networks := []api.Network{}
		networkNames, err := d.cluster.Networks()
		if err != nil && err != db.ErrNoSuchObject {
			return err
		}

		for _, name := range networkNames {
			_, network, err := d.cluster.NetworkGet(name)
			if err != nil {
				return err
			}
			networks = append(networks, *network)
		}

		// Now request for this node to be added to the list of cluster nodes.
		info, err := clusterAcceptMember(
			client, req.ServerName, address, cluster.SchemaVersion,
			version.APIExtensionsCount(), pools, networks)
		if err != nil {
			return errors.Wrap(err, "failed to request to add node")
		}

		// Update our TLS configuration using the returned cluster certificate.
		err = util.WriteCert(d.os.VarDir, "cluster", []byte(req.ClusterCertificate), info.PrivateKey, nil)
		if err != nil {
			return errors.Wrap(err, "failed to save cluster certificate")
		}
		cert, err := util.LoadCert(d.os.VarDir)
		if err != nil {
			return errors.Wrap(err, "failed to parse cluster certificate")
		}
		d.endpoints.NetworkUpdateCert(cert)

		// Update local setup and possibly join the raft dqlite
		// cluster.
		nodes := make([]db.RaftNode, len(info.RaftNodes))
		for i, node := range info.RaftNodes {
			nodes[i].ID = node.ID
			nodes[i].Address = node.Address
		}

		// The default timeout when non-clustered is one minute, let's
		// lower it down now that we'll likely have to make requests
		// over the network.
		//
		// FIXME: this is a workaround for #5234.
		d.cluster.SetDefaultTimeout(5 * time.Second)

		// Start clustering tasks
		d.startClusterTasks()

		err = cluster.Join(d.State(), d.gateway, cert, req.ServerName, nodes)
		if err != nil {
			d.cluster.SetDefaultTimeout(time.Minute)
			d.stopClusterTasks()
			return err
		}

		// Remove the our old server certificate from the trust store,
		// since it's not needed anymore.
		_, err = d.cluster.CertificateGet(fingerprint)
		if err != db.ErrNoSuchObject {
			if err != nil {
				return err
			}

			err := d.cluster.CertDelete(fingerprint)
			if err != nil {
				return errors.Wrap(err, "failed to delete joining node's certificate")
			}
		}

		// For ceph pools we have to create the mount points too.
		poolNames, err = d.cluster.StoragePools()
		if err != nil && err != db.ErrNoSuchObject {
			return err
		}
		for _, name := range poolNames {
			_, pool, err := d.cluster.StoragePoolGet(name)
			if err != nil {
				return err
			}
			if pool.Driver != "ceph" {
				continue
			}
			storage, err := storagePoolInit(d.State(), name)
			if err != nil {
				return errors.Wrap(err, "failed to init ceph pool for joining node")
			}
			volumeMntPoint := getStoragePoolVolumeMountPoint(
				name, storage.(*storageCeph).volume.Name)
			err = os.MkdirAll(volumeMntPoint, 0711)
			if err != nil {
				return errors.Wrap(err, "failed to create ceph pool mount point")
			}
		}

		// FIXME: special case handling MAAS connection if the config
		// in the cluster is different than what we had locally before
		// joining. Ideally this should be something transparent or
		// more generic, perhaps triggering some parts of Daemon.Init.
		var clusterConfig *cluster.Config
		err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
			var err error
			clusterConfig, err = cluster.ConfigLoad(tx)
			return err
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
		url, key := clusterConfig.MAASController()
		machine := nodeConfig.MAASMachine()
		err = d.setupMAASController(url, key, machine)
		if err != nil {
			return err
		}

		// Add the cluster flag from the agent
		version.UserAgentFeatures([]string{"cluster"})

		return nil
	}

	resources := map[string][]string{}
	resources["cluster"] = []string{}

	op, err := operationCreate(d.cluster, "", operationClassTask, db.OperationClusterJoin, resources, nil, run, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

// Disable clustering on a node.
func clusterPutDisable(d *Daemon) Response {
	// Close the cluster database
	err := d.cluster.Close()
	if err != nil {
		return SmartError(err)
	}

	// Update our TLS configuration using our original certificate.
	for _, suffix := range []string{"crt", "key", "ca"} {
		path := filepath.Join(d.os.VarDir, "cluster."+suffix)
		if !shared.PathExists(path) {
			continue
		}
		err := os.Remove(path)
		if err != nil {
			return InternalError(err)
		}
	}
	cert, err := util.LoadCert(d.os.VarDir)
	if err != nil {
		return InternalError(errors.Wrap(err, "failed to parse node certificate"))
	}

	// Reset the cluster database and make it local to this node.
	d.endpoints.NetworkUpdateCert(cert)
	err = d.gateway.Reset(cert)
	if err != nil {
		return SmartError(err)
	}

	// Re-open the cluster database
	address, err := node.HTTPSAddress(d.db)
	if err != nil {
		return SmartError(err)
	}
	store := d.gateway.ServerStore()
	d.cluster, err = db.OpenCluster(
		"db.bin", store, address, "/unused/db/dir",
		d.config.DqliteSetupTimeout,
		dqlite.WithDialFunc(d.gateway.DialFunc()),
		dqlite.WithContext(d.gateway.Context()),
	)
	if err != nil {
		return SmartError(err)
	}

	// Stop the clustering tasks
	d.stopClusterTasks()

	// Remove the cluster flag from the agent
	version.UserAgentFeatures(nil)

	return EmptySyncResponse
}

// Initialize storage pools and networks on this node.
//
// We pass to LXD client instances, one connected to ourselves (the joining
// node) and one connected to the target cluster node to join.
func clusterInitMember(d, client lxd.ContainerServer, memberConfig []api.ClusterMemberConfigKey) error {
	data := initDataNode{}

	// Fetch all pools currently defined in the cluster.
	pools, err := client.GetStoragePools()
	if err != nil {
		return errors.Wrap(err, "Failed to fetch information about cluster storage pools")
	}

	// Merge the returned storage pools configs with the node-specific
	// configs provided by the user.
	for _, pool := range pools {
		// Skip pending pools.
		if pool.Status == "Pending" {
			continue
		}

		// Skip ceph pools since they have no node-specific key and
		// don't need to be defined on joining nodes.
		if pool.Driver == "ceph" {
			continue
		}

		logger.Debugf("Populating init data for storage pool %s", pool.Name)

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
				logger.Warnf("Ignoring config key %s for storage pool %s", config.Key, config.Name)
				continue
			}

			post.Config[config.Key] = config.Value
		}

		data.StoragePools = append(data.StoragePools, post)
	}

	// Fetch all networks currently defined in the cluster.
	networks, err := client.GetNetworks()
	if err != nil {
		return errors.Wrap(err, "Failed to fetch information about cluster networks")
	}

	// Merge the returned storage networks configs with the node-specific
	// configs provided by the user.
	for _, network := range networks {
		// Skip not-managed or pending networks
		if !network.Managed || network.Status == "Pending" {
			continue
		}

		post := api.NetworksPost{
			NetworkPut: network.NetworkPut,
			Managed:    true,
			Name:       network.Name,
			Type:       network.Type,
		}

		// Apply the node-specific config supplied by the user.
		for _, config := range memberConfig {
			if config.Entity != "network" {
				continue
			}

			if config.Name != network.Name {
				continue
			}

			if !shared.StringInSlice(config.Key, db.NetworkNodeConfigKeys) {
				logger.Warnf("Ignoring config key %s for network %s", config.Key, config.Name)
				continue
			}

			post.Config[config.Key] = config.Value
		}

		data.Networks = append(data.Networks, post)
	}

	revert, err := initDataNodeApply(d, data)
	if err != nil {
		revert()
		return errors.Wrap(err, "Failed to initialize storage pools and networks")
	}

	return nil
}

// Perform a request to the /internal/cluster/accept endpoint to check if a new
// mode can be accepted into the cluster and obtain joining information such as
// the cluster private certificate.
func clusterAcceptMember(
	client lxd.ContainerServer,
	name, address string, schema, apiExt int,
	pools []api.StoragePool, networks []api.Network) (*internalClusterPostAcceptResponse, error) {

	req := internalClusterPostAcceptRequest{
		Name:         name,
		Address:      address,
		Schema:       schema,
		API:          apiExt,
		StoragePools: pools,
		Networks:     networks,
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

func clusterNodesGet(d *Daemon, r *http.Request) Response {
	recursion := util.IsRecursionRequest(r)

	nodes, err := cluster.List(d.State())
	if err != nil {
		return SmartError(err)
	}

	var result interface{}
	if recursion {
		result = nodes
	} else {
		urls := []string{}
		for _, node := range nodes {
			url := fmt.Sprintf("/%s/cluster/members/%s", version.APIVersion, node.ServerName)
			urls = append(urls, url)
		}
		result = urls
	}

	return SyncResponse(true, result)
}

func clusterNodeGet(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	nodes, err := cluster.List(d.State())
	if err != nil {
		return SmartError(err)
	}

	for _, node := range nodes {
		if node.ServerName == name {
			return SyncResponseETag(true, node, node)
		}
	}

	return NotFound(fmt.Errorf("Node '%s' not found", name))
}

func clusterNodePost(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	req := api.ClusterMemberPost{}

	// Parse the request
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return BadRequest(err)
	}

	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		return tx.NodeRename(name, req.ServerName)
	})
	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}

func clusterNodeDelete(d *Daemon, r *http.Request) Response {
	force, err := strconv.Atoi(r.FormValue("force"))
	if err != nil {
		force = 0
	}

	name := mux.Vars(r)["name"]
	logger.Debugf("Delete node %s from cluster (force=%d)", name, force)

	// First check that the node is clear from containers and images and
	// make it leave the database cluster, if it's part of it.
	address, err := cluster.Leave(d.State(), d.gateway, name, force == 1)
	if err != nil {
		return SmartError(err)
	}

	if force != 1 {
		// Try to gracefully delete all networks and storage pools on it.
		// Delete all networks on this node
		cert := d.endpoints.NetworkCert()
		client, err := cluster.Connect(address, cert, true)
		if err != nil {
			return SmartError(err)
		}
		networks, err := d.cluster.Networks()
		if err != nil {
			return SmartError(err)
		}
		for _, name := range networks {
			err := client.DeleteNetwork(name)
			if err != nil {
				return SmartError(err)
			}
		}

		// Delete all the pools on this node
		pools, err := d.cluster.StoragePools()
		if err != nil && err != db.ErrNoSuchObject {
			return SmartError(err)
		}
		for _, name := range pools {
			err := client.DeleteStoragePool(name)
			if err != nil {
				return SmartError(err)
			}
		}
	}

	// Remove node from the database
	err = cluster.Purge(d.cluster, name)
	if err != nil {
		return SmartError(errors.Wrap(err, "failed to remove node from database"))
	}
	// Try to notify the leader.
	err = tryClusterRebalance(d)
	if err != nil {
		// This is not a fatal error, so let's just log it.
		logger.Errorf("Failed to rebalance cluster: %v", err)
	}

	if force != 1 {
		// Try to gracefully reset the database on the node.
		cert := d.endpoints.NetworkCert()
		client, err := cluster.Connect(address, cert, false)
		if err != nil {
			return SmartError(err)
		}
		put := api.ClusterPut{}
		put.Enabled = false
		_, err = client.UpdateCluster(put, "")
		if err != nil {
			SmartError(errors.Wrap(err, "failed to cleanup the node"))
		}
	}

	return EmptySyncResponse
}

// This function is used to notify the leader that a node was removed, it will
// decide whether to promote a new node as database node.
func tryClusterRebalance(d *Daemon) error {
	leader, err := d.gateway.LeaderAddress()
	if err != nil {
		// This is not a fatal error, so let's just log it.
		return errors.Wrap(err, "failed to get current leader node")
	}
	cert := d.endpoints.NetworkCert()
	client, err := cluster.Connect(leader, cert, true)
	if err != nil {
		return errors.Wrap(err, "failed to connect to leader node")
	}
	_, _, err = client.RawQuery("POST", "/internal/cluster/rebalance", nil, "")
	if err != nil {
		return errors.Wrap(err, "request to rebalance cluster failed")
	}
	return nil
}

func internalClusterPostAccept(d *Daemon, r *http.Request) Response {
	req := internalClusterPostAcceptRequest{}

	// Parse the request
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return BadRequest(err)
	}

	// Sanity checks
	if req.Name == "" {
		return BadRequest(fmt.Errorf("No name provided"))
	}

	// Redirect all requests to the leader, which is the one with
	// knowning what nodes are part of the raft cluster.
	address, err := node.ClusterAddress(d.db)
	if err != nil {
		return SmartError(err)
	}
	leader, err := d.gateway.LeaderAddress()
	if err != nil {
		return InternalError(err)
	}
	if address != leader {
		logger.Debugf("Redirect node accept request to %s", leader)
		url := &url.URL{
			Scheme: "https",
			Path:   "/internal/cluster/accept",
			Host:   leader,
		}
		return SyncResponseRedirect(url.String())
	}

	// Check that the pools and networks provided by the joining node have
	// configs that match the cluster ones.
	err = clusterCheckStoragePoolsMatch(d.cluster, req.StoragePools)
	if err != nil {
		return SmartError(err)
	}
	err = clusterCheckNetworksMatch(d.cluster, req.Networks)
	if err != nil {
		return SmartError(err)
	}

	nodes, err := cluster.Accept(d.State(), d.gateway, req.Name, req.Address, req.Schema, req.API)
	if err != nil {
		return BadRequest(err)
	}
	accepted := internalClusterPostAcceptResponse{
		RaftNodes:  make([]internalRaftNode, len(nodes)),
		PrivateKey: d.endpoints.NetworkPrivateKey(),
	}
	for i, node := range nodes {
		accepted.RaftNodes[i].ID = node.ID
		accepted.RaftNodes[i].Address = node.Address
	}
	return SyncResponse(true, accepted)
}

// A request for the /internal/cluster/accept endpoint.
type internalClusterPostAcceptRequest struct {
	Name         string            `json:"name" yaml:"name"`
	Address      string            `json:"address" yaml:"address"`
	Schema       int               `json:"schema" yaml:"schema"`
	API          int               `json:"api" yaml:"api"`
	StoragePools []api.StoragePool `json:"storage_pools" yaml:"storage_pools"`
	Networks     []api.Network     `json:"networks" yaml:"networks"`
}

// A Response for the /internal/cluster/accept endpoint.
type internalClusterPostAcceptResponse struct {
	RaftNodes  []internalRaftNode `json:"raft_nodes" yaml:"raft_nodes"`
	PrivateKey []byte             `json:"private_key" yaml:"private_key"`
}

// Represent a LXD node that is part of the dqlite raft cluster.
type internalRaftNode struct {
	ID      int64  `json:"id" yaml:"id"`
	Address string `json:"address" yaml:"address"`
}

// Used to update the cluster after a database node has been removed, and
// possibly promote another one as database node.
func internalClusterPostRebalance(d *Daemon, r *http.Request) Response {
	// Redirect all requests to the leader, which is the one with with
	// up-to-date knowledge of what nodes are part of the raft cluster.
	localAddress, err := node.ClusterAddress(d.db)
	if err != nil {
		return SmartError(err)
	}
	leader, err := d.gateway.LeaderAddress()
	if err != nil {
		return InternalError(err)
	}
	if localAddress != leader {
		logger.Debugf("Redirect cluster rebalance request to %s", leader)
		url := &url.URL{
			Scheme: "https",
			Path:   "/internal/cluster/rebalance",
			Host:   leader,
		}
		return SyncResponseRedirect(url.String())
	}

	logger.Debugf("Rebalance cluster")

	// Check if we have a spare node to promote.
	address, nodes, err := cluster.Rebalance(d.State(), d.gateway)
	if err != nil {
		return SmartError(err)
	}
	if address == "" {
		return SyncResponse(true, nil) // Nothing to change
	}

	// Tell the node to promote itself.
	post := &internalClusterPostPromoteRequest{}
	for _, node := range nodes {
		post.RaftNodes = append(post.RaftNodes, internalRaftNode{
			ID:      node.ID,
			Address: node.Address,
		})
	}

	cert := d.endpoints.NetworkCert()
	client, err := cluster.Connect(address, cert, false)
	if err != nil {
		return SmartError(err)
	}
	_, _, err = client.RawQuery("POST", "/internal/cluster/promote", post, "")
	if err != nil {
		return SmartError(err)
	}

	return SyncResponse(true, nil)
}

// Used to promote the local non-database node to be a database one.
func internalClusterPostPromote(d *Daemon, r *http.Request) Response {
	req := internalClusterPostPromoteRequest{}

	// Parse the request
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return BadRequest(err)
	}

	// Sanity checks
	if len(req.RaftNodes) == 0 {
		return BadRequest(fmt.Errorf("No raft nodes provided"))
	}

	nodes := make([]db.RaftNode, len(req.RaftNodes))
	for i, node := range req.RaftNodes {
		nodes[i].ID = node.ID
		nodes[i].Address = node.Address
	}
	err = cluster.Promote(d.State(), d.gateway, nodes)
	if err != nil {
		return SmartError(err)
	}

	return SyncResponse(true, nil)
}

// A request for the /internal/cluster/promote endpoint.
type internalClusterPostPromoteRequest struct {
	RaftNodes []internalRaftNode `json:"raft_nodes" yaml:"raft_nodes"`
}

func clusterCheckStoragePoolsMatch(cluster *db.Cluster, reqPools []api.StoragePool) error {
	poolNames, err := cluster.StoragePoolsNotPending()
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
			_, pool, err := cluster.StoragePoolGet(name)
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
			_, pool, err := cluster.StoragePoolGet(name)
			if err != nil {
				return err
			}
			// Ignore missing ceph pools, since they'll be shared
			// and we don't require them to be defined on the
			// joining node.
			if pool.Driver == "ceph" {
				continue
			}
			return fmt.Errorf("Missing storage pool %s", name)
		}
	}
	return nil
}

func clusterCheckNetworksMatch(cluster *db.Cluster, reqNetworks []api.Network) error {
	networkNames, err := cluster.NetworksNotPending()
	if err != nil && err != db.ErrNoSuchObject {
		return err
	}
	for _, name := range networkNames {
		found := false
		for _, reqNetwork := range reqNetworks {
			if reqNetwork.Name != name {
				continue
			}
			found = true
			_, network, err := cluster.NetworkGet(name)
			if err != nil {
				return err
			}
			// Exclude the keys which are node-specific.
			exclude := db.NetworkNodeConfigKeys
			err = util.CompareConfigs(network.Config, reqNetwork.Config, exclude)
			if err != nil {
				return fmt.Errorf("Mismatching config for network %s: %v", name, err)
			}
			break
		}
		if !found {
			return fmt.Errorf("Missing network %s", name)
		}
	}
	return nil
}
