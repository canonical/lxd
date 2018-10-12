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

// StoragePoolsNodeConfig returns a map associating each storage pool name to
// its node-specific config values (i.e. the ones where node_id is not NULL).
func (c *ClusterTx) StoragePoolsNodeConfig() (map[string]map[string]string, error) {
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
			c.tx, table, "storage_pools.name=? AND storage_pools_config.node_id=?",
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
		return -1, ErrNoSuchObject
	case 1:
		return int64(ids[0]), nil
	default:
		return -1, fmt.Errorf("more than one pool has the given name")
	}
}

// StoragePoolDriver returns the driver of the pool with the given ID.
func (c *ClusterTx) StoragePoolDriver(id int64) (string, error) {
	stmt := "SELECT driver FROM storage_pools WHERE id=?"
	drivers, err := query.SelectStrings(c.tx, stmt, id)
	if err != nil {
		return "", err
	}
	switch len(drivers) {
	case 0:
		return "", ErrNoSuchObject
	case 1:
		return drivers[0], nil
	default:
		return "", fmt.Errorf("more than one pool has the given id")
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
	stmt, err := c.tx.Prepare("SELECT id, name FROM storage_pools WHERE NOT state=?")
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	err = query.SelectObjects(stmt, dest, storagePoolPending)
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
	if err != nil {
		return errors.Wrap(err, "failed to add storage pools node entry")
	}

	return nil
}

// StoragePoolNodeJoinCeph updates internal state to reflect that nodeID is
// joining a cluster where poolID is a ceph pool.
func (c *ClusterTx) StoragePoolNodeJoinCeph(poolID, nodeID int64) error {
	// Get the IDs of the other nodes (they should be all linked to
	// the pool).
	stmt := "SELECT node_id FROM storage_pools_nodes WHERE storage_pool_id=?"
	nodeIDs, err := query.SelectIntegers(c.tx, stmt, poolID)
	if err != nil {
		return errors.Wrap(err, "failed to fetch IDs of nodes with ceph pool")
	}
	if len(nodeIDs) == 0 {
		return fmt.Errorf("ceph pool is not linked to any node")
	}
	otherNodeID := nodeIDs[0]

	// Create entries of all the ceph volumes for the new node.
	_, err = c.tx.Exec(`
INSERT INTO storage_volumes(name, storage_pool_id, node_id, type, description, project_id)
  SELECT name, storage_pool_id, ?, type, description, 1
    FROM storage_volumes WHERE storage_pool_id=? AND node_id=?
`, nodeID, poolID, otherNodeID)
	if err != nil {
		return errors.Wrap(err, "failed to create node ceph volumes")
	}

	// Create entries of all the ceph volumes configs for the new node.
	stmt = `
SELECT id FROM storage_volumes WHERE storage_pool_id=? AND node_id=?
  ORDER BY name, type
`
	volumeIDs, err := query.SelectIntegers(c.tx, stmt, poolID, nodeID)
	if err != nil {
		return errors.Wrap(err, "failed to get joining node's ceph volume IDs")
	}
	otherVolumeIDs, err := query.SelectIntegers(c.tx, stmt, poolID, otherNodeID)
	if err != nil {
		return errors.Wrap(err, "failed to get other node's ceph volume IDs")
	}
	if len(volumeIDs) != len(otherVolumeIDs) { // Sanity check
		return fmt.Errorf("not all ceph volumes were copied")
	}
	for i, otherVolumeID := range otherVolumeIDs {
		config, err := query.SelectConfig(
			c.tx, "storage_volumes_config", "storage_volume_id=?", otherVolumeID)
		if err != nil {
			return errors.Wrap(err, "failed to get storage volume config")
		}
		for key, value := range config {
			_, err := c.tx.Exec(`
INSERT INTO storage_volumes_config(storage_volume_id, key, value) VALUES(?, ?, ?)
`, volumeIDs[i], key, value)
			if err != nil {
				return errors.Wrap(err, "failed to copy volume config")
			}
		}
	}

	return nil
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
	stmt, err := c.tx.Prepare("SELECT id, driver, state FROM storage_pools WHERE name=?")
	if err != nil {
		return err
	}
	defer stmt.Close()
	err = query.SelectObjects(stmt, dest, name)
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
		return ErrAlreadyDefined
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
		return ErrNoSuchObject
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
		config, err := query.SelectConfig(
			c.tx, "storage_pools_config", "storage_pool_id=? AND node_id=?", poolID, node.ID)
		if err != nil {
			return nil, err
		}
		configs[node.Name] = config
	}

	return configs, nil
}

// StoragePools returns the names of all storage pools.
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
		return []string{}, ErrNoSuchObject
	}

	pools := []string{}
	for _, r := range result {
		pools = append(pools, r[0].(string))
	}

	return pools, nil
}

// StoragePoolsGetDrivers returns the names of all storage volumes attached to
// a given storage pool.
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
		return []string{}, ErrNoSuchObject
	}

	drivers := []string{}
	for _, driver := range result {
		drivers = append(drivers, driver[0].(string))
	}

	return drivers, nil
}

// StoragePoolGetID returns the id of a single storage pool.
func (c *Cluster) StoragePoolGetID(poolName string) (int64, error) {
	poolID := int64(-1)
	query := "SELECT id FROM storage_pools WHERE name=?"
	inargs := []interface{}{poolName}
	outargs := []interface{}{&poolID}

	err := dbQueryRowScan(c.db, query, inargs, outargs)
	if err != nil {
		if err == sql.ErrNoRows {
			return -1, ErrNoSuchObject
		}
	}

	return poolID, nil
}

// StoragePoolGet returns a single storage pool.
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
			return -1, nil, ErrNoSuchObject
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

// StoragePoolConfigGet returns the config of a storage pool.
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

// StoragePoolCreate creates new storage pool.
func (c *Cluster) StoragePoolCreate(poolName string, poolDescription string, poolDriver string, poolConfig map[string]string) (int64, error) {
	var id int64
	err := c.Transaction(func(tx *ClusterTx) error {
		result, err := tx.tx.Exec("INSERT INTO storage_pools (name, description, driver, state) VALUES (?, ?, ?, ?)", poolName, poolDescription, poolDriver, storagePoolCreated)
		if err != nil {
			return err
		}

		id, err = result.LastInsertId()
		if err != nil {
			return err
		}

		// Insert a node-specific entry pointing to ourselves.
		columns := []string{"storage_pool_id", "node_id"}
		values := []interface{}{id, c.nodeID}
		_, err = query.UpsertObject(tx.tx, "storage_pools_nodes", columns, values)
		if err != nil {
			return err
		}

		err = storagePoolConfigAdd(tx.tx, id, c.nodeID, poolConfig)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		id = -1
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

// StoragePoolDriver returns the driver of the pool with the given ID.
func storagePoolDriverGet(tx *sql.Tx, id int64) (string, error) {
	stmt := "SELECT driver FROM storage_pools WHERE id=?"
	drivers, err := query.SelectStrings(tx, stmt, id)
	if err != nil {
		return "", err
	}
	switch len(drivers) {
	case 0:
		return "", ErrNoSuchObject
	case 1:
		return drivers[0], nil
	default:
		return "", fmt.Errorf("more than one pool has the given ID")
	}
}

// StoragePoolUpdate updates a storage pool.
func (c *Cluster) StoragePoolUpdate(poolName, description string, poolConfig map[string]string) error {
	poolID, _, err := c.StoragePoolGet(poolName)
	if err != nil {
		return err
	}

	err = c.Transaction(func(tx *ClusterTx) error {
		err = StoragePoolUpdateDescription(tx.tx, poolID, description)
		if err != nil {
			return err
		}

		err = StoragePoolConfigClear(tx.tx, poolID, c.nodeID)
		if err != nil {
			return err
		}

		err = storagePoolConfigAdd(tx.tx, poolID, c.nodeID, poolConfig)
		if err != nil {
			return err
		}
		return nil
	})

	return err
}

// StoragePoolUpdateDescription updates the storage pool description.
func StoragePoolUpdateDescription(tx *sql.Tx, id int64, description string) error {
	_, err := tx.Exec("UPDATE storage_pools SET description=? WHERE id=?", description, id)
	return err
}

// StoragePoolConfigClear deletes the storage pool config.
func StoragePoolConfigClear(tx *sql.Tx, poolID, nodeID int64) error {
	_, err := tx.Exec("DELETE FROM storage_pools_config WHERE storage_pool_id=? AND (node_id=? OR node_id IS NULL)", poolID, nodeID)
	if err != nil {
		return err
	}

	return nil
}

// StoragePoolDelete deletes storage pool.
func (c *Cluster) StoragePoolDelete(poolName string) (*api.StoragePool, error) {
	poolID, pool, err := c.StoragePoolGet(poolName)
	if err != nil {
		return nil, err
	}

	err = exec(c.db, "DELETE FROM storage_pools WHERE id=?", poolID)
	if err != nil {
		return nil, err
	}

	return pool, nil
}

// StoragePoolVolumesGetNames gets the names of all storage volumes attached to
// a given storage pool.
func (c *Cluster) StoragePoolVolumesGetNames(poolID int64) ([]string, error) {
	var volumeName string
	query := "SELECT name FROM storage_volumes WHERE storage_pool_id=? AND node_id=?"
	inargs := []interface{}{poolID, c.nodeID}
	outargs := []interface{}{volumeName}

	result, err := queryScan(c.db, query, inargs, outargs)
	if err != nil {
		return []string{}, err
	}

	var out []string

	for _, r := range result {
		out = append(out, r[0].(string))
	}

	return out, nil
}

// StoragePoolVolumesGet returns all storage volumes attached to a given
// storage pool on any node.
func (c *Cluster) StoragePoolVolumesGet(project string, poolID int64, volumeTypes []int) ([]*api.StorageVolume, error) {
	var nodeIDs []int

	err := c.Transaction(func(tx *ClusterTx) error {
		var err error
		nodeIDs, err = query.SelectIntegers(tx.tx, `
SELECT DISTINCT node_id
  FROM storage_volumes
  JOIN projects ON projects.id = storage_volumes.project_id
 WHERE (projects.name=? OR storage_volumes.type=?) AND storage_pool_id=?
`, project, StoragePoolVolumeTypeCustom, poolID)
		return err
	})
	if err != nil {
		return nil, err
	}
	volumes := []*api.StorageVolume{}

	for _, nodeID := range nodeIDs {
		nodeVolumes, err := c.storagePoolVolumesGet(project, poolID, int64(nodeID), volumeTypes)
		if err != nil {
			return nil, err
		}
		volumes = append(volumes, nodeVolumes...)
	}
	return volumes, nil
}

// StoragePoolNodeVolumesGet returns all storage volumes attached to a given
// storage pool on the current node.
func (c *Cluster) StoragePoolNodeVolumesGet(poolID int64, volumeTypes []int) ([]*api.StorageVolume, error) {
	return c.storagePoolVolumesGet("default", poolID, c.nodeID, volumeTypes)
}

// Returns all storage volumes attached to a given storage pool on the given
// node.
func (c *Cluster) storagePoolVolumesGet(project string, poolID, nodeID int64, volumeTypes []int) ([]*api.StorageVolume, error) {
	// Get all storage volumes of all types attached to a given storage
	// pool.
	result := []*api.StorageVolume{}
	for _, volumeType := range volumeTypes {
		volumeNames, err := c.StoragePoolVolumesGetType(project, volumeType, poolID, nodeID)
		if err != nil && err != sql.ErrNoRows {
			return nil, errors.Wrap(err, "failed to fetch volume types")
		}
		for _, volumeName := range volumeNames {
			_, volume, err := c.StoragePoolVolumeGetType(project, volumeName, volumeType, poolID, nodeID)
			if err != nil {
				return nil, errors.Wrap(err, "failed to fetch volume type")
			}
			result = append(result, volume)
		}
	}

	if len(result) == 0 {
		return result, ErrNoSuchObject
	}

	return result, nil
}

// StoragePoolVolumesGetType get all storage volumes attached to a given
// storage pool of a given volume type, on the given node.
func (c *Cluster) StoragePoolVolumesGetType(project string, volumeType int, poolID, nodeID int64) ([]string, error) {
	var poolName string
	query := `
SELECT storage_volumes.name
  FROM storage_volumes
  JOIN projects ON projects.id=storage_volumes.project_id
 WHERE (projects.name=? OR storage_volumes.type=?) AND storage_pool_id=? AND node_id=? AND type=?
`
	inargs := []interface{}{project, StoragePoolVolumeTypeCustom, poolID, nodeID, volumeType}
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

// StoragePoolVolumeSnapshotsGetType get all snapshots of a storage volume
// attached to a given storage pool of a given volume type, on the given node.
func (c *Cluster) StoragePoolVolumeSnapshotsGetType(volumeName string, volumeType int, poolID int64) ([]string, error) {
	result := []string{}
	regexp := volumeName + shared.SnapshotDelimiter
	length := len(regexp)

	query := "SELECT name FROM storage_volumes WHERE storage_pool_id=? AND node_id=? AND type=? AND snapshot=? AND SUBSTR(name,1,?)=?"
	inargs := []interface{}{poolID, c.nodeID, volumeType, true, length, regexp}
	outfmt := []interface{}{volumeName}

	dbResults, err := queryScan(c.db, query, inargs, outfmt)
	if err != nil {
		return result, err
	}

	for _, r := range dbResults {
		result = append(result, r[0].(string))
	}

	return result, nil
}

// StoragePoolNodeVolumesGetType returns all storage volumes attached to a
// given storage pool of a given volume type, on the current node.
func (c *Cluster) StoragePoolNodeVolumesGetType(volumeType int, poolID int64) ([]string, error) {
	return c.StoragePoolVolumesGetType("default", volumeType, poolID, c.nodeID)
}

// StoragePoolVolumeGetType returns a single storage volume attached to a
// given storage pool of a given type, on the node with the given ID.
func (c *Cluster) StoragePoolVolumeGetType(project string, volumeName string, volumeType int, poolID, nodeID int64) (int64, *api.StorageVolume, error) {
	// Custom volumes are "global", i.e. they are associated with the
	// default project.
	if volumeType == StoragePoolVolumeTypeCustom {
		project = "default"
	}

	volumeID, err := c.StoragePoolVolumeGetTypeID(project, volumeName, volumeType, poolID, nodeID)
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

// StoragePoolNodeVolumeGetType gets a single storage volume attached to a
// given storage pool of a given type, on the current node.
func (c *Cluster) StoragePoolNodeVolumeGetType(volumeName string, volumeType int, poolID int64) (int64, *api.StorageVolume, error) {
	return c.StoragePoolNodeVolumeGetTypeByProject("default", volumeName, volumeType, poolID)
}

// StoragePoolNodeVolumeGetTypeByProject gets a single storage volume attached to a
// given storage pool of a given type, on the current node in the given project.
func (c *Cluster) StoragePoolNodeVolumeGetTypeByProject(project, volumeName string, volumeType int, poolID int64) (int64, *api.StorageVolume, error) {
	return c.StoragePoolVolumeGetType(project, volumeName, volumeType, poolID, c.nodeID)
}

// StoragePoolVolumeUpdate updates the storage volume attached to a given storage
// pool.
func (c *Cluster) StoragePoolVolumeUpdate(volumeName string, volumeType int, poolID int64, volumeDescription string, volumeConfig map[string]string) error {
	volumeID, _, err := c.StoragePoolNodeVolumeGetType(volumeName, volumeType, poolID)
	if err != nil {
		return err
	}

	err = c.Transaction(func(tx *ClusterTx) error {
		err = storagePoolVolumeReplicateIfCeph(tx.tx, volumeID, "default", volumeName, volumeType, poolID, func(volumeID int64) error {
			err = StorageVolumeConfigClear(tx.tx, volumeID)
			if err != nil {
				return err
			}

			err = StorageVolumeConfigAdd(tx.tx, volumeID, volumeConfig)
			if err != nil {
				return err
			}

			return StorageVolumeDescriptionUpdate(tx.tx, volumeID, volumeDescription)
		})
		if err != nil {
			return err
		}
		return nil
	})

	return err
}

// StoragePoolVolumeDelete deletes the storage volume attached to a given storage
// pool.
func (c *Cluster) StoragePoolVolumeDelete(project, volumeName string, volumeType int, poolID int64) error {
	volumeID, _, err := c.StoragePoolNodeVolumeGetTypeByProject(project, volumeName, volumeType, poolID)
	if err != nil {
		return err
	}

	err = c.Transaction(func(tx *ClusterTx) error {
		err := storagePoolVolumeReplicateIfCeph(tx.tx, volumeID, project, volumeName, volumeType, poolID, func(volumeID int64) error {
			_, err := tx.tx.Exec("DELETE FROM storage_volumes WHERE id=?", volumeID)
			return err
		})
		return err
	})

	return err
}

// StoragePoolVolumeRename renames the storage volume attached to a given storage pool.
func (c *Cluster) StoragePoolVolumeRename(project, oldVolumeName string, newVolumeName string, volumeType int, poolID int64) error {
	volumeID, _, err := c.StoragePoolNodeVolumeGetTypeByProject(project, oldVolumeName, volumeType, poolID)
	if err != nil {
		return err
	}

	err = c.Transaction(func(tx *ClusterTx) error {
		err := storagePoolVolumeReplicateIfCeph(tx.tx, volumeID, project, oldVolumeName, volumeType, poolID, func(volumeID int64) error {
			_, err := tx.tx.Exec("UPDATE storage_volumes SET name=? WHERE id=? AND type=?", newVolumeName, volumeID, volumeType)
			return err
		})
		return err
	})

	return err
}

// This a convenience to replicate a certain volume change to all nodes if the
// underlying driver is ceph.
func storagePoolVolumeReplicateIfCeph(tx *sql.Tx, volumeID int64, project, volumeName string, volumeType int, poolID int64, f func(int64) error) error {
	driver, err := storagePoolDriverGet(tx, poolID)
	if err != nil {
		return err
	}
	volumeIDs := []int64{volumeID}

	// If this is a ceph volume, we want to duplicate the change across the
	// the rows for all other nodes.
	if driver == "ceph" {
		volumeIDs, err = storageVolumeIDsGet(tx, project, volumeName, volumeType, poolID)
		if err != nil {
			return err
		}
	}

	for _, volumeID := range volumeIDs {
		err := f(volumeID)
		if err != nil {
			return err
		}
	}

	return nil
}

// StoragePoolVolumeCreate creates a new storage volume attached to a given
// storage pool.
func (c *Cluster) StoragePoolVolumeCreate(project, volumeName, volumeDescription string, volumeType int, snapshot bool, poolID int64, volumeConfig map[string]string) (int64, error) {
	var thisVolumeID int64

	err := c.Transaction(func(tx *ClusterTx) error {
		nodeIDs := []int{int(c.nodeID)}
		driver, err := storagePoolDriverGet(tx.tx, poolID)
		if err != nil {
			return err
		}
		// If the driver is ceph, create a volume entry for each node.
		if driver == "ceph" {
			nodeIDs, err = query.SelectIntegers(tx.tx, "SELECT id FROM nodes")
			if err != nil {
				return err
			}
		}

		for _, nodeID := range nodeIDs {
			result, err := tx.tx.Exec(`
INSERT INTO storage_volumes (storage_pool_id, node_id, type, snapshot, name, description, project_id) VALUES (?, ?, ?, ?, ?, ?, (SELECT id FROM projects WHERE name = ?))
`,
				poolID, nodeID, volumeType, snapshot, volumeName, volumeDescription, project)
			if err != nil {
				return err
			}

			volumeID, err := result.LastInsertId()
			if err != nil {
				return err
			}
			if int64(nodeID) == c.nodeID {
				// Return the ID of the volume created on this node.
				thisVolumeID = volumeID
			}

			err = StorageVolumeConfigAdd(tx.tx, volumeID, volumeConfig)
			if err != nil {
				tx.tx.Rollback()
				return err
			}
		}
		return nil
	})
	if err != nil {
		thisVolumeID = -1
	}

	return thisVolumeID, err
}

// StoragePoolVolumeGetTypeID returns the ID of a storage volume on a given
// storage pool of a given storage volume type, on the given node.
func (c *Cluster) StoragePoolVolumeGetTypeID(project string, volumeName string, volumeType int, poolID, nodeID int64) (int64, error) {
	volumeID := int64(-1)
	query := `SELECT storage_volumes.id
FROM storage_volumes
JOIN storage_pools ON storage_volumes.storage_pool_id = storage_pools.id
JOIN projects ON storage_volumes.project_id = projects.id
WHERE projects.name=? AND storage_volumes.storage_pool_id=? AND storage_volumes.node_id=?
AND storage_volumes.name=? AND storage_volumes.type=?`
	inargs := []interface{}{project, poolID, nodeID, volumeName, volumeType}
	outargs := []interface{}{&volumeID}

	err := dbQueryRowScan(c.db, query, inargs, outargs)
	if err != nil {
		if err == sql.ErrNoRows {
			return -1, ErrNoSuchObject
		}
		return -1, err
	}

	return volumeID, nil
}

// StoragePoolNodeVolumeGetTypeID get the ID of a storage volume on a given
// storage pool of a given storage volume type, on the current node.
func (c *Cluster) StoragePoolNodeVolumeGetTypeID(volumeName string, volumeType int, poolID int64) (int64, error) {
	return c.StoragePoolVolumeGetTypeID("default", volumeName, volumeType, poolID, c.nodeID)
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
	"zfs.pool_name",
	"lvm.thinpool",
	"lvm.vg_name",
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

// StoragePoolInsertZfsDriver replaces the driver of all storage pools without
// a driver, setting it to 'zfs'.
func (c *Cluster) StoragePoolInsertZfsDriver() error {
	err := exec(c.db, "UPDATE storage_pools SET driver='zfs', description='' WHERE driver=''")
	return err
}
