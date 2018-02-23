package db

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

// StoragePoolConfigs returns a map associating each storage pool name to its
// config values.
//
// The config values are the ones defined for the node this function is run
// on. They are used by cluster.Join when a new node joins the cluster and its
// configuration needs to be migrated to the cluster database.
func (c *ClusterTx) StoragePoolConfigs() (map[string]map[string]string, error) {
	names, err := query.SelectStrings(c.tx, "SELECT name FROM storage_pools")
	if err != nil {
		return nil, err
	}
	pools := make(map[string]map[string]string, len(names))
	for _, name := range names {
		table := `
storage_pools_config JOIN storage_pools ON storage_pools.id=storage_pools_config.storage_pool_id
`
		config, err := query.SelectConfig(
			c.tx, table, "storage_pools.name=? AND storage_pools_config.storage_pool_id=?",
			name, c.nodeID)
		if err != nil {
			return nil, err
		}
		pools[name] = config
	}
	return pools, nil
}

// StoragePoolID returns the ID of the pool with the given name.
func (c *ClusterTx) StoragePoolID(name string) (int64, error) {
	stmt := "SELECT id FROM storage_pools WHERE name=?"
	ids, err := query.SelectIntegers(c.tx, stmt, name)
	if err != nil {
		return -1, err
	}
	switch len(ids) {
	case 0:
		return -1, NoSuchObjectError
	case 1:
		return int64(ids[0]), nil
	default:
		return -1, fmt.Errorf("more than one pool has the given name")
	}
}

// StoragePoolIDsNotPending returns a map associating each storage pool name to its ID.
//
// Pending storage pools are skipped.
func (c *ClusterTx) StoragePoolIDsNotPending() (map[string]int64, error) {
	pools := []struct {
		id   int64
		name string
	}{}
	dest := func(i int) []interface{} {
		pools = append(pools, struct {
			id   int64
			name string
		}{})
		return []interface{}{&pools[i].id, &pools[i].name}

	}
	stmt := "SELECT id, name FROM storage_pools WHERE NOT state=?"
	err := query.SelectObjects(c.tx, dest, stmt, storagePoolPending)
	if err != nil {
		return nil, err
	}
	ids := map[string]int64{}
	for _, pool := range pools {
		ids[pool.name] = pool.id
	}
	return ids, nil
}

// StoragePoolNodeJoin adds a new entry in the storage_pools_nodes table.
//
// It should only be used when a new node joins the cluster, when it's safe to
// assume that the relevant pool has already been created on the joining node,
// and we just need to track it.
func (c *ClusterTx) StoragePoolNodeJoin(poolID, nodeID int64) error {
	columns := []string{"storage_pool_id", "node_id"}
	values := []interface{}{poolID, nodeID}
	_, err := query.UpsertObject(c.tx, "storage_pools_nodes", columns, values)
	return err
}

// StoragePoolConfigAdd adds a new entry in the storage_pools_config table
func (c *ClusterTx) StoragePoolConfigAdd(poolID, nodeID int64, config map[string]string) error {
	return storagePoolConfigAdd(c.tx, poolID, nodeID, config)
}

// Storage pools state.
const (
	storagePoolPending int = iota // Storage pool defined but not yet created.
	storagePoolCreated            // Storage pool created on all nodes.
	storagePoolErrored            // Storage pool creation failed on some nodes
)

// StoragePoolCreatePending creates a new pending storage pool on the node with
// the given name.
func (c *ClusterTx) StoragePoolCreatePending(node, name, driver string, conf map[string]string) error {
	// First check if a storage pool with the given name exists, and, if
	// so, that it has a matching driver and it's in the pending state.
	pool := struct {
		id     int64
		driver string
		state  int
	}{}

	var errConsistency error
	dest := func(i int) []interface{} {
		// Sanity check that there is at most one pool with the given name.
		if i != 0 {
			errConsistency = fmt.Errorf("more than one pool exists with the given name")
		}
		return []interface{}{&pool.id, &pool.driver, &pool.state}
	}
	stmt := "SELECT id, driver, state FROM storage_pools WHERE name=?"
	err := query.SelectObjects(c.tx, dest, stmt, name)
	if err != nil {
		return err
	}
	if errConsistency != nil {
		return errConsistency
	}

	var poolID = pool.id
	if poolID == 0 {
		// No existing pool with the given name was found, let's create
		// one.
		columns := []string{"name", "driver"}
		values := []interface{}{name, driver}
		poolID, err = query.UpsertObject(c.tx, "storage_pools", columns, values)
		if err != nil {
			return err
		}
	} else {
		// Check that the existing pools matches the given driver and
		// is in the pending state.
		if pool.driver != driver {
			return fmt.Errorf("pool already exists with a different driver")
		}
		if pool.state != storagePoolPending {
			return fmt.Errorf("pool is not in pending state")
		}
	}

	// Get the ID of the node with the given name.
	nodeInfo, err := c.NodeByName(node)
	if err != nil {
		return err
	}

	// Check that no storage_pool entry of this node and pool exists yet.
	count, err := query.Count(
		c.tx, "storage_pools_nodes", "storage_pool_id=? AND node_id=?", poolID, nodeInfo.ID)
	if err != nil {
		return err
	}
	if count != 0 {
		return DbErrAlreadyDefined
	}

	// Insert the node-specific configuration.
	columns := []string{"storage_pool_id", "node_id"}
	values := []interface{}{poolID, nodeInfo.ID}
	_, err = query.UpsertObject(c.tx, "storage_pools_nodes", columns, values)
	if err != nil {
		return err
	}
	err = c.StoragePoolConfigAdd(poolID, nodeInfo.ID, conf)
	if err != nil {
		return err
	}

	return nil
}

// StoragePoolCreated sets the state of the given pool to "Created".
func (c *ClusterTx) StoragePoolCreated(name string) error {
	return c.storagePoolState(name, storagePoolCreated)
}

// StoragePoolErrored sets the state of the given pool to "Errored".
func (c *ClusterTx) StoragePoolErrored(name string) error {
	return c.storagePoolState(name, storagePoolErrored)
}

func (c *ClusterTx) storagePoolState(name string, state int) error {
	stmt := "UPDATE storage_pools SET state=? WHERE name=?"
	result, err := c.tx.Exec(stmt, state, name)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return NoSuchObjectError
	}
	return nil
}

// StoragePoolNodeConfigs returns the node-specific configuration of all
// nodes grouped by node name, for the given poolID.
//
// If the storage pool is not defined on all nodes, an error is returned.
func (c *ClusterTx) StoragePoolNodeConfigs(poolID int64) (map[string]map[string]string, error) {
	// Fetch all nodes.
	nodes, err := c.Nodes()
	if err != nil {
		return nil, err
	}

	// Fetch the names of the nodes where the storage pool is defined.
	stmt := `
SELECT nodes.name FROM nodes
  LEFT JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id
  LEFT JOIN storage_pools ON storage_pools_nodes.storage_pool_id = storage_pools.id
WHERE storage_pools.id = ? AND storage_pools.state = ?
`
	defined, err := query.SelectStrings(c.tx, stmt, poolID, storagePoolPending)
	if err != nil {
		return nil, err
	}

	// Figure which nodes are missing
	missing := []string{}
	for _, node := range nodes {
		if !shared.StringInSlice(node.Name, defined) {
			missing = append(missing, node.Name)
		}
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("Pool not defined on nodes: %s", strings.Join(missing, ", "))
	}

	configs := map[string]map[string]string{}
	for _, node := range nodes {
		config, err := query.SelectConfig(c.tx, "storage_pools_config", "node_id=?", node.ID)
		if err != nil {
			return nil, err
		}
		configs[node.Name] = config
	}

	return configs, nil
}

// Get all storage pools.
func (c *Cluster) StoragePools() ([]string, error) {
	return c.storagePools("")
}

// StoragePoolsNotPending returns the names of all storage pools that are not
// pending.
func (c *Cluster) StoragePoolsNotPending() ([]string, error) {
	return c.storagePools("NOT state=?", storagePoolPending)
}

// Get all storage pools matching the given WHERE filter (if given).
func (c *Cluster) storagePools(where string, args ...interface{}) ([]string, error) {
	var name string
	stmt := "SELECT name FROM storage_pools"
	inargs := []interface{}{}
	outargs := []interface{}{name}

	if where != "" {
		stmt += fmt.Sprintf(" WHERE %s", where)
		for _, arg := range args {
			inargs = append(inargs, arg)
		}
	}

	result, err := queryScan(c.db, stmt, inargs, outargs)
	if err != nil {
		return []string{}, err
	}

	if len(result) == 0 {
		return []string{}, NoSuchObjectError
	}

	pools := []string{}
	for _, r := range result {
		pools = append(pools, r[0].(string))
	}

	return pools, nil
}

// Get the names of all storage volumes attached to a given storage pool.
func (c *Cluster) StoragePoolsGetDrivers() ([]string, error) {
	var poolDriver string
	query := "SELECT DISTINCT driver FROM storage_pools"
	inargs := []interface{}{}
	outargs := []interface{}{poolDriver}

	result, err := queryScan(c.db, query, inargs, outargs)
	if err != nil {
		return []string{}, err
	}

	if len(result) == 0 {
		return []string{}, NoSuchObjectError
	}

	drivers := []string{}
	for _, driver := range result {
		drivers = append(drivers, driver[0].(string))
	}

	return drivers, nil
}

// Get id of a single storage pool.
func (c *Cluster) StoragePoolGetID(poolName string) (int64, error) {
	poolID := int64(-1)
	query := "SELECT id FROM storage_pools WHERE name=?"
	inargs := []interface{}{poolName}
	outargs := []interface{}{&poolID}

	err := dbQueryRowScan(c.db, query, inargs, outargs)
	if err != nil {
		if err == sql.ErrNoRows {
			return -1, NoSuchObjectError
		}
	}

	return poolID, nil
}

// Get a single storage pool.
func (c *Cluster) StoragePoolGet(poolName string) (int64, *api.StoragePool, error) {
	var poolDriver string
	poolID := int64(-1)
	description := sql.NullString{}
	var state int

	query := "SELECT id, driver, description, state FROM storage_pools WHERE name=?"
	inargs := []interface{}{poolName}
	outargs := []interface{}{&poolID, &poolDriver, &description, &state}

	err := dbQueryRowScan(c.db, query, inargs, outargs)
	if err != nil {
		if err == sql.ErrNoRows {
			return -1, nil, NoSuchObjectError
		}
		return -1, nil, err
	}

	config, err := c.StoragePoolConfigGet(poolID)
	if err != nil {
		return -1, nil, err
	}

	storagePool := api.StoragePool{
		Name:   poolName,
		Driver: poolDriver,
	}
	storagePool.Description = description.String
	storagePool.Config = config

	switch state {
	case storagePoolPending:
		storagePool.Status = "Pending"
	case storagePoolCreated:
		storagePool.Status = "Created"
	default:
		storagePool.Status = "Unknown"
	}

	nodes, err := c.storagePoolNodes(poolID)
	if err != nil {
		return -1, nil, err
	}
	storagePool.Locations = nodes

	return poolID, &storagePool, nil
}

// Return the names of the nodes the given pool is defined on.
func (c *Cluster) storagePoolNodes(poolID int64) ([]string, error) {
	stmt := `
SELECT nodes.name FROM nodes
  JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id
  WHERE storage_pools_nodes.storage_pool_id = ?
`
	var nodes []string
	err := c.Transaction(func(tx *ClusterTx) error {
		var err error
		nodes, err = query.SelectStrings(tx.tx, stmt, poolID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return nodes, nil
}

// Get config of a storage pool.
func (c *Cluster) StoragePoolConfigGet(poolID int64) (map[string]string, error) {
	var key, value string
	query := "SELECT key, value FROM storage_pools_config WHERE storage_pool_id=? AND (node_id=? OR node_id IS NULL)"
	inargs := []interface{}{poolID, c.nodeID}
	outargs := []interface{}{key, value}

	results, err := queryScan(c.db, query, inargs, outargs)
	if err != nil {
		return nil, err
	}

	config := map[string]string{}

	for _, r := range results {
		key = r[0].(string)
		value = r[1].(string)

		config[key] = value
	}

	return config, nil
}

// Create new storage pool.
func (c *Cluster) StoragePoolCreate(poolName string, poolDescription string, poolDriver string, poolConfig map[string]string) (int64, error) {
	tx, err := begin(c.db)
	if err != nil {
		return -1, err
	}

	result, err := tx.Exec("INSERT INTO storage_pools (name, description, driver, state) VALUES (?, ?, ?, ?)", poolName, poolDescription, poolDriver, storagePoolCreated)
	if err != nil {
		tx.Rollback()
		return -1, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		tx.Rollback()
		return -1, err
	}

	// Insert a node-specific entry pointing to ourselves.
	columns := []string{"storage_pool_id", "node_id"}
	values := []interface{}{id, c.nodeID}
	_, err = query.UpsertObject(tx, "storage_pools_nodes", columns, values)
	if err != nil {
		tx.Rollback()
		return -1, err
	}

	err = storagePoolConfigAdd(tx, id, c.nodeID, poolConfig)
	if err != nil {
		tx.Rollback()
		return -1, err
	}

	err = TxCommit(tx)
	if err != nil {
		return -1, err
	}

	return id, nil
}

// Add new storage pool config.
func storagePoolConfigAdd(tx *sql.Tx, poolID, nodeID int64, poolConfig map[string]string) error {
	str := "INSERT INTO storage_pools_config (storage_pool_id, node_id, key, value) VALUES(?, ?, ?, ?)"
	stmt, err := tx.Prepare(str)
	defer stmt.Close()
	if err != nil {
		return err
	}

	for k, v := range poolConfig {
		if v == "" {
			continue
		}
		var nodeIDValue interface{}
		if !shared.StringInSlice(k, StoragePoolNodeConfigKeys) {
			nodeIDValue = nil
		} else {
			nodeIDValue = nodeID
		}

		_, err = stmt.Exec(poolID, nodeIDValue, k, v)
		if err != nil {
			return err
		}
	}

	return nil
}

// Update storage pool.
func (c *Cluster) StoragePoolUpdate(poolName, description string, poolConfig map[string]string) error {
	poolID, _, err := c.StoragePoolGet(poolName)
	if err != nil {
		return err
	}

	tx, err := begin(c.db)
	if err != nil {
		return err
	}

	err = StoragePoolUpdateDescription(tx, poolID, description)
	if err != nil {
		tx.Rollback()
		return err
	}

	err = StoragePoolConfigClear(tx, poolID, c.nodeID)
	if err != nil {
		tx.Rollback()
		return err
	}

	err = storagePoolConfigAdd(tx, poolID, c.nodeID, poolConfig)
	if err != nil {
		tx.Rollback()
		return err
	}

	return TxCommit(tx)
}

// Update the storage pool description.
func StoragePoolUpdateDescription(tx *sql.Tx, id int64, description string) error {
	_, err := tx.Exec("UPDATE storage_pools SET description=? WHERE id=?", description, id)
	return err
}

// Delete storage pool config.
func StoragePoolConfigClear(tx *sql.Tx, poolID, nodeID int64) error {
	_, err := tx.Exec("DELETE FROM storage_pools_config WHERE storage_pool_id=? AND (node_id=? OR node_id IS NULL)", poolID, nodeID)
	if err != nil {
		return err
	}

	return nil
}

// Delete storage pool.
func (c *Cluster) StoragePoolDelete(poolName string) (*api.StoragePool, error) {
	poolID, pool, err := c.StoragePoolGet(poolName)
	if err != nil {
		return nil, err
	}

	_, err = exec(c.db, "DELETE FROM storage_pools WHERE id=?", poolID)
	if err != nil {
		return nil, err
	}

	return pool, nil
}

// Get the names of all storage volumes attached to a given storage pool.
func (c *Cluster) StoragePoolVolumesGetNames(poolID int64) (int, error) {
	var volumeName string
	query := "SELECT name FROM storage_volumes WHERE storage_pool_id=? AND node_id=?"
	inargs := []interface{}{poolID, c.nodeID}
	outargs := []interface{}{volumeName}

	result, err := queryScan(c.db, query, inargs, outargs)
	if err != nil {
		return -1, err
	}

	if len(result) == 0 {
		return 0, nil
	}

	return len(result), nil
}

// StoragePoolVolumesGet returns all storage volumes attached to a given
// storage pool on any node.
func (c *Cluster) StoragePoolVolumesGet(poolID int64, volumeTypes []int) ([]*api.StorageVolume, error) {
	var nodeIDs []int

	err := c.Transaction(func(tx *ClusterTx) error {
		var err error
		nodeIDs, err = query.SelectIntegers(tx.tx, "SELECT DISTINCT node_id FROM storage_volumes WHERE storage_pool_id=?", poolID)
		return err
	})
	if err != nil {
		return nil, err
	}
	volumes := []*api.StorageVolume{}

	for _, nodeID := range nodeIDs {
		nodeVolumes, err := c.storagePoolVolumesGet(poolID, int64(nodeID), volumeTypes)
		if err != nil {
			return nil, err
		}
		volumes = append(volumes, nodeVolumes...)
	}
	return volumes, nil
}

// Get all storage volumes attached to a given storage pool on the current
// node.
func (c *Cluster) StoragePoolNodeVolumesGet(poolID int64, volumeTypes []int) ([]*api.StorageVolume, error) {
	return c.storagePoolVolumesGet(poolID, c.nodeID, volumeTypes)
}

// Returns all storage volumes attached to a given storage pool on the given
// node.
func (c *Cluster) storagePoolVolumesGet(poolID, nodeID int64, volumeTypes []int) ([]*api.StorageVolume, error) {
	// Get all storage volumes of all types attached to a given storage
	// pool.
	result := []*api.StorageVolume{}
	for _, volumeType := range volumeTypes {
		volumeNames, err := c.StoragePoolVolumesGetType(volumeType, poolID, nodeID)
		if err != nil && err != sql.ErrNoRows {
			return nil, errors.Wrap(err, "failed to fetch volume types")
		}
		for _, volumeName := range volumeNames {
			_, volume, err := c.StoragePoolVolumeGetType(volumeName, volumeType, poolID, nodeID)
			if err != nil {
				return nil, errors.Wrap(err, "failed to fetch volume type")
			}
			result = append(result, volume)
		}
	}

	if len(result) == 0 {
		return result, NoSuchObjectError
	}

	return result, nil
}

// StoragePoolVolumesGetType get all storage volumes attached to a given
// storage pool of a given volume type, on the given node.
func (c *Cluster) StoragePoolVolumesGetType(volumeType int, poolID, nodeID int64) ([]string, error) {
	var poolName string
	query := "SELECT name FROM storage_volumes WHERE storage_pool_id=? AND node_id=? AND type=?"
	inargs := []interface{}{poolID, nodeID, volumeType}
	outargs := []interface{}{poolName}

	result, err := queryScan(c.db, query, inargs, outargs)
	if err != nil {
		return []string{}, err
	}

	response := []string{}
	for _, r := range result {
		response = append(response, r[0].(string))
	}

	return response, nil
}

// Get all storage volumes attached to a given storage pool of a given volume
// type, on the current node.
func (c *Cluster) StoragePoolNodeVolumesGetType(volumeType int, poolID int64) ([]string, error) {
	return c.StoragePoolVolumesGetType(volumeType, poolID, c.nodeID)
}

// StoragePoolVolumeGetType returns a single storage volume attached to a
// given storage pool of a given type, on the node with the given ID.
func (c *Cluster) StoragePoolVolumeGetType(volumeName string, volumeType int, poolID, nodeID int64) (int64, *api.StorageVolume, error) {
	volumeID, err := c.StoragePoolVolumeGetTypeID(volumeName, volumeType, poolID, nodeID)
	if err != nil {
		return -1, nil, err
	}

	volumeNode, err := c.StorageVolumeNodeGet(volumeID)
	if err != nil {
		return -1, nil, err
	}

	volumeConfig, err := c.StorageVolumeConfigGet(volumeID)
	if err != nil {
		return -1, nil, err
	}

	volumeDescription, err := c.StorageVolumeDescriptionGet(volumeID)
	if err != nil {
		return -1, nil, err
	}

	volumeTypeName, err := StoragePoolVolumeTypeToName(volumeType)
	if err != nil {
		return -1, nil, err
	}

	storageVolume := api.StorageVolume{
		Type: volumeTypeName,
	}
	storageVolume.Name = volumeName
	storageVolume.Description = volumeDescription
	storageVolume.Config = volumeConfig
	storageVolume.Location = volumeNode

	return volumeID, &storageVolume, nil
}

// Get a single storage volume attached to a given storage pool of a given
// type, on the current node.
func (c *Cluster) StoragePoolNodeVolumeGetType(volumeName string, volumeType int, poolID int64) (int64, *api.StorageVolume, error) {
	return c.StoragePoolVolumeGetType(volumeName, volumeType, poolID, c.nodeID)
}

// Update storage volume attached to a given storage pool.
func (c *Cluster) StoragePoolVolumeUpdate(volumeName string, volumeType int, poolID int64, volumeDescription string, volumeConfig map[string]string) error {
	volumeID, _, err := c.StoragePoolNodeVolumeGetType(volumeName, volumeType, poolID)
	if err != nil {
		return err
	}

	tx, err := begin(c.db)
	if err != nil {
		return err
	}

	err = StorageVolumeConfigClear(tx, volumeID)
	if err != nil {
		tx.Rollback()
		return err
	}

	err = StorageVolumeConfigAdd(tx, volumeID, volumeConfig)
	if err != nil {
		tx.Rollback()
		return err
	}

	err = StorageVolumeDescriptionUpdate(tx, volumeID, volumeDescription)
	if err != nil {
		tx.Rollback()
		return err
	}

	return TxCommit(tx)
}

// Delete storage volume attached to a given storage pool.
func (c *Cluster) StoragePoolVolumeDelete(volumeName string, volumeType int, poolID int64) error {
	volumeID, _, err := c.StoragePoolNodeVolumeGetType(volumeName, volumeType, poolID)
	if err != nil {
		return err
	}

	_, err = exec(c.db, "DELETE FROM storage_volumes WHERE id=?", volumeID)
	if err != nil {
		return err
	}

	return nil
}

// Rename storage volume attached to a given storage pool.
func (c *Cluster) StoragePoolVolumeRename(oldVolumeName string, newVolumeName string, volumeType int, poolID int64) error {
	volumeID, _, err := c.StoragePoolNodeVolumeGetType(oldVolumeName, volumeType, poolID)
	if err != nil {
		return err
	}

	tx, err := begin(c.db)
	if err != nil {
		return err
	}

	_, err = tx.Exec("UPDATE storage_volumes SET name=? WHERE id=? AND type=?", newVolumeName, volumeID, volumeType)
	if err != nil {
		tx.Rollback()
		return err
	}

	return TxCommit(tx)
}

// Create new storage volume attached to a given storage pool.
func (c *Cluster) StoragePoolVolumeCreate(volumeName, volumeDescription string, volumeType int, poolID int64, volumeConfig map[string]string) (int64, error) {
	tx, err := begin(c.db)
	if err != nil {
		return -1, err
	}

	result, err := tx.Exec("INSERT INTO storage_volumes (storage_pool_id, node_id, type, name, description) VALUES (?, ?, ?, ?, ?)",
		poolID, c.nodeID, volumeType, volumeName, volumeDescription)
	if err != nil {
		tx.Rollback()
		return -1, err
	}

	volumeID, err := result.LastInsertId()
	if err != nil {
		tx.Rollback()
		return -1, err
	}

	err = StorageVolumeConfigAdd(tx, volumeID, volumeConfig)
	if err != nil {
		tx.Rollback()
		return -1, err
	}

	err = TxCommit(tx)
	if err != nil {
		return -1, err
	}

	return volumeID, nil
}

// StoragePoolVolumeGetTypeID returns the ID of a storage volume on a given
// storage pool of a given storage volume type, on the given node.
func (c *Cluster) StoragePoolVolumeGetTypeID(volumeName string, volumeType int, poolID, nodeID int64) (int64, error) {
	volumeID := int64(-1)
	query := `SELECT storage_volumes.id
FROM storage_volumes
JOIN storage_pools
ON storage_volumes.storage_pool_id = storage_pools.id
WHERE storage_volumes.storage_pool_id=? AND storage_volumes.node_id=?
AND storage_volumes.name=? AND storage_volumes.type=?`
	inargs := []interface{}{poolID, nodeID, volumeName, volumeType}
	outargs := []interface{}{&volumeID}

	err := dbQueryRowScan(c.db, query, inargs, outargs)
	if err != nil {
		return -1, NoSuchObjectError
	}

	return volumeID, nil
}

// Get ID of a storage volume on a given storage pool of a given storage volume
// type, on the current node.
func (c *Cluster) StoragePoolNodeVolumeGetTypeID(volumeName string, volumeType int, poolID int64) (int64, error) {
	return c.StoragePoolVolumeGetTypeID(volumeName, volumeType, poolID, c.nodeID)
}

// XXX: this was extracted from lxd/storage_volume_utils.go, we find a way to
//      factor it independently from both the db and main packages.
const (
	StoragePoolVolumeTypeContainer = iota
	StoragePoolVolumeTypeImage
	StoragePoolVolumeTypeCustom
)

// Leave the string type in here! This guarantees that go treats this is as a
// typed string constant. Removing it causes go to treat these as untyped string
// constants which is not what we want.
const (
	StoragePoolVolumeTypeNameContainer string = "container"
	StoragePoolVolumeTypeNameImage     string = "image"
	StoragePoolVolumeTypeNameCustom    string = "custom"
)

// StoragePoolNodeConfigKeys lists all storage pool config keys which are
// node-specific.
var StoragePoolNodeConfigKeys = []string{
	"size",
	"source",
	"volatile.initial_source",
}

// StoragePoolVolumeTypeToName converts a volume integer type code to its
// human-readable name.
func StoragePoolVolumeTypeToName(volumeType int) (string, error) {
	switch volumeType {
	case StoragePoolVolumeTypeContainer:
		return StoragePoolVolumeTypeNameContainer, nil
	case StoragePoolVolumeTypeImage:
		return StoragePoolVolumeTypeNameImage, nil
	case StoragePoolVolumeTypeCustom:
		return StoragePoolVolumeTypeNameCustom, nil
	}

	return "", fmt.Errorf("invalid storage volume type")
}

func (c *Cluster) StoragePoolInsertZfsDriver() error {
	_, err := exec(c.db, "UPDATE storage_pools SET driver='zfs', description='' WHERE driver=''")
	return err
}
