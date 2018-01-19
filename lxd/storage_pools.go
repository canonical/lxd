package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/gorilla/mux"
	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
)

// Lock to prevent concurent storage pools creation
var storagePoolCreateLock sync.Mutex

// /1.0/storage-pools
// List all storage pools.
func storagePoolsGet(d *Daemon, r *http.Request) Response {
	recursionStr := r.FormValue("recursion")
	recursion, err := strconv.Atoi(recursionStr)
	if err != nil {
		recursion = 0
	}

	pools, err := d.cluster.StoragePools()
	if err != nil && err != db.NoSuchObjectError {
		return SmartError(err)
	}

	resultString := []string{}
	resultMap := []api.StoragePool{}
	for _, pool := range pools {
		if recursion == 0 {
			resultString = append(resultString, fmt.Sprintf("/%s/storage-pools/%s", version.APIVersion, pool))
		} else {
			plID, pl, err := d.cluster.StoragePoolGet(pool)
			if err != nil {
				continue
			}

			// Get all users of the storage pool.
			poolUsedBy, err := storagePoolUsedByGet(d.State(), plID, pool)
			if err != nil {
				return SmartError(err)
			}
			pl.UsedBy = poolUsedBy

			resultMap = append(resultMap, *pl)
		}
	}

	if recursion == 0 {
		return SyncResponse(true, resultString)
	}

	return SyncResponse(true, resultMap)
}

// /1.0/storage-pools
// Create a storage pool.
func storagePoolsPost(d *Daemon, r *http.Request) Response {
	storagePoolCreateLock.Lock()
	defer storagePoolCreateLock.Unlock()

	req := api.StoragePoolsPost{}

	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return BadRequest(err)
	}

	// Sanity checks.
	if req.Name == "" {
		return BadRequest(fmt.Errorf("No name provided"))
	}

	if strings.Contains(req.Name, "/") {
		return BadRequest(fmt.Errorf("Storage pool names may not contain slashes"))
	}

	if req.Driver == "" {
		return BadRequest(fmt.Errorf("No driver provided"))
	}

	url := fmt.Sprintf("/%s/storage-pools/%s", version.APIVersion, req.Name)
	response := SyncResponseLocation(true, nil, url)

	if isClusterNotification(r) {
		// This is an internal request which triggers the actual
		// creation of the pool across all nodes, after they have been
		// previously defined.
		err = doStoragePoolCreateInternal(
			d.State(), req.Name, req.Description, req.Driver, req.Config)
		if err != nil {
			return SmartError(err)
		}
		return response
	}

	targetNode := r.FormValue("targetNode")
	if targetNode == "" {
		count, err := cluster.Count(d.State())
		if err != nil {
			return SmartError(err)
		}

		if count == 1 {
			// No targetNode was specified and we're either a single-node
			// cluster or not clustered at all, so create the storage
			// pool immediately.
			err = storagePoolCreateInternal(
				d.State(), req.Name, req.Description, req.Driver, req.Config)
		} else {
			// No targetNode was specified and we're clustered, so finalize the
			// config in the db and actually create the pool on all nodes.
			err = storagePoolsPostCluster(d, req)
		}
		if err != nil {
			return InternalError(err)
		}
		return response

	}

	// A targetNode was specified, let's just define the node's storage
	// without actually creating it. The only legal key values for the
	// storage config are the ones in StoragePoolNodeConfigKeys.
	for key := range req.Config {
		if !shared.StringInSlice(key, db.StoragePoolNodeConfigKeys) {
			return SmartError(fmt.Errorf("Invalid config key '%s'", key))
		}
	}
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		return tx.StoragePoolCreatePending(targetNode, req.Name, req.Driver, req.Config)
	})
	if err != nil {
		if err == db.DbErrAlreadyDefined {
			return BadRequest(
				fmt.Errorf("The storage pool already defined on node %s", targetNode))
		}
		return SmartError(err)
	}

	return response
}

func storagePoolsPostCluster(d *Daemon, req api.StoragePoolsPost) error {
	// Check that no node-specific config key has been defined.
	for key := range req.Config {
		if shared.StringInSlice(key, db.StoragePoolNodeConfigKeys) {
			return fmt.Errorf("Config key '%s' is node-specific", key)
		}
	}

	// Check that the pool is properly defined, fetch the node-specific
	// configs and insert the global config.
	var configs map[string]map[string]string
	var nodeName string
	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		// Check that the pool was defined at all.
		poolID, err := tx.StoragePoolID(req.Name)
		if err != nil {
			return err
		}

		// Fetch the node-specific configs.
		configs, err = tx.StoragePoolNodeConfigs(poolID)
		if err != nil {
			return err
		}

		// Take note of the name of this node
		nodeName, err = tx.NodeName()
		if err != nil {
			return err
		}

		// Insert the global config keys.
		return tx.StoragePoolConfigAdd(poolID, 0, req.Config)
	})
	if err != nil {
		if err == db.NoSuchObjectError {
			return fmt.Errorf("Pool not pending on any node (use --target <node> first)")
		}
		return err
	}

	// Create the pool on this node.
	nodeReq := req
	for key, value := range configs[nodeName] {
		nodeReq.Config[key] = value
	}
	err = doStoragePoolCreateInternal(
		d.State(), req.Name, req.Description, req.Driver, req.Config)
	if err != nil {
		return err
	}

	// Notify all other nodes to create the pool.
	notifier, err := cluster.NewNotifier(d.State(), d.endpoints.NetworkCert(), cluster.NotifyAll)
	if err != nil {
		return err
	}
	notifyErr := notifier(func(client lxd.ContainerServer) error {
		_, _, err := client.GetServer()
		if err != nil {
			return err
		}
		nodeReq := req
		for key, value := range configs[client.ClusterNodeName()] {
			nodeReq.Config[key] = value
		}
		return client.CreateStoragePool(nodeReq)
	})

	errored := notifyErr != nil

	// Finally update the storage pool state.
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		if errored {
			return tx.StoragePoolErrored(req.Name)
		}
		return tx.StoragePoolCreated(req.Name)
	})
	if err != nil {
		return err
	}

	return notifyErr
}

var storagePoolsCmd = Command{name: "storage-pools", get: storagePoolsGet, post: storagePoolsPost}

// /1.0/storage-pools/{name}
// Get a single storage pool.
func storagePoolGet(d *Daemon, r *http.Request) Response {
	poolName := mux.Vars(r)["name"]

	// Get the existing storage pool.
	poolID, pool, err := d.cluster.StoragePoolGet(poolName)
	if err != nil {
		return SmartError(err)
	}

	// Get all users of the storage pool.
	poolUsedBy, err := storagePoolUsedByGet(d.State(), poolID, poolName)
	if err != nil && err != db.NoSuchObjectError {
		return SmartError(err)
	}
	pool.UsedBy = poolUsedBy

	targetNode := r.FormValue("targetNode")

	clustered, err := cluster.Enabled(d.db)
	if err != nil {
		return SmartError(err)
	}

	// If no target node is specified and the client is clustered, we omit
	// the node-specific fields.
	if targetNode == "" && clustered {
		for _, key := range db.StoragePoolNodeConfigKeys {
			delete(pool.Config, key)
		}
	}

	// If a target was specified, forward the request to the relevant node.
	if targetNode != "" {
		address, err := cluster.ResolveTarget(d.cluster, targetNode)
		if err != nil {
			return SmartError(err)
		}
		if address != "" {
			cert := d.endpoints.NetworkCert()
			client, err := cluster.Connect(address, cert, true)
			if err != nil {
				return SmartError(err)
			}
			client = client.ClusterTargetNode(targetNode)
			pool, _, err = client.GetStoragePool(poolName)
			if err != nil {
				return SmartError(err)
			}
		}
	}

	etag := []interface{}{pool.Name, pool.Driver, pool.Config}

	return SyncResponseETag(true, &pool, etag)
}

// /1.0/storage-pools/{name}
// Replace pool properties.
func storagePoolPut(d *Daemon, r *http.Request) Response {
	poolName := mux.Vars(r)["name"]

	// Get the existing storage pool.
	_, dbInfo, err := d.cluster.StoragePoolGet(poolName)
	if err != nil {
		return SmartError(err)
	}

	// Validate the ETag
	etag := []interface{}{dbInfo.Name, dbInfo.Driver, dbInfo.Config}

	err = util.EtagCheck(r, etag)
	if err != nil {
		return PreconditionFailed(err)
	}

	req := api.StoragePoolPut{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	// Validate the configuration
	err = storagePoolValidateConfig(poolName, dbInfo.Driver, req.Config, dbInfo.Config)
	if err != nil {
		return BadRequest(err)
	}

	err = storagePoolUpdate(d.State(), poolName, req.Description, req.Config)
	if err != nil {
		return InternalError(err)
	}

	return EmptySyncResponse
}

// /1.0/storage-pools/{name}
// Change pool properties.
func storagePoolPatch(d *Daemon, r *http.Request) Response {
	poolName := mux.Vars(r)["name"]

	// Get the existing network
	_, dbInfo, err := d.cluster.StoragePoolGet(poolName)
	if dbInfo != nil {
		return SmartError(err)
	}

	// Validate the ETag
	etag := []interface{}{dbInfo.Name, dbInfo.Driver, dbInfo.Config}

	err = util.EtagCheck(r, etag)
	if err != nil {
		return PreconditionFailed(err)
	}

	req := api.StoragePoolPut{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	// Config stacking
	if req.Config == nil {
		req.Config = map[string]string{}
	}

	for k, v := range dbInfo.Config {
		_, ok := req.Config[k]
		if !ok {
			req.Config[k] = v
		}
	}

	// Validate the configuration
	err = storagePoolValidateConfig(poolName, dbInfo.Driver, req.Config, dbInfo.Config)
	if err != nil {
		return BadRequest(err)
	}

	err = storagePoolUpdate(d.State(), poolName, req.Description, req.Config)
	if err != nil {
		return InternalError(err)
	}

	return EmptySyncResponse
}

// /1.0/storage-pools/{name}
// Delete storage pool.
func storagePoolDelete(d *Daemon, r *http.Request) Response {
	poolName := mux.Vars(r)["name"]

	poolID, err := d.cluster.StoragePoolGetID(poolName)
	if err != nil {
		return NotFound
	}

	// If this is not an internal cluster request, check if the storage
	// pool has any volumes associated with it, if so error out.
	if !isClusterNotification(r) {
		response := storagePoolDeleteCheckPreconditions(d.cluster, poolName, poolID)
		if response != nil {
			return response
		}
	}

	s, err := storagePoolInit(d.State(), poolName)
	if err != nil {
		return InternalError(err)
	}

	err = s.StoragePoolDelete()
	if err != nil {
		return InternalError(err)
	}

	// If this is a cluster notification, we're done, any database work
	// will be done by the node that is originally serving the request.
	if isClusterNotification(r) {
		return EmptySyncResponse
	}

	// If we are clustered, also notify all other nodes, if any.
	clustered, err := cluster.Enabled(d.db)
	if err != nil {
		return SmartError(err)
	}
	if clustered {
		notifier, err := cluster.NewNotifier(d.State(), d.endpoints.NetworkCert(), cluster.NotifyAll)
		if err != nil {
			return SmartError(err)
		}
		err = notifier(func(client lxd.ContainerServer) error {
			_, _, err := client.GetServer()
			if err != nil {
				return err
			}
			return client.DeleteStoragePool(poolName)
		})
		if err != nil {
			return SmartError(err)
		}
	}

	err = dbStoragePoolDeleteAndUpdateCache(d.cluster, poolName)
	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}

func storagePoolDeleteCheckPreconditions(cluster *db.Cluster, poolName string, poolID int64) Response {
	volumeCount, err := cluster.StoragePoolVolumesGetNames(poolID)
	if err != nil {
		return InternalError(err)
	}

	if volumeCount > 0 {
		return BadRequest(fmt.Errorf("storage pool \"%s\" has volumes attached to it", poolName))
	}

	// Check if the storage pool is still referenced in any profiles.
	profiles, err := profilesUsingPoolGetNames(cluster, poolName)
	if err != nil {
		return SmartError(err)
	}

	if len(profiles) > 0 {
		return BadRequest(fmt.Errorf("Storage pool \"%s\" has profiles using it:\n%s", poolName, strings.Join(profiles, "\n")))
	}

	return nil
}

var storagePoolCmd = Command{name: "storage-pools/{name}", get: storagePoolGet, put: storagePoolPut, patch: storagePoolPatch, delete: storagePoolDelete}
