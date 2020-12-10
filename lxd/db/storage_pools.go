// +build linux,cgo,!agent

package db

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

// GetStoragePoolsLocalConfig returns a map associating each storage pool name to
// its node-specific config values (i.e. the ones where node_id is not NULL).
func (c *ClusterTx) GetStoragePoolsLocalConfig() (map[string]map[string]string, error) {
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

// GetStoragePoolUsedBy looks up all users of a storage pool.
func (c *ClusterTx) GetStoragePoolUsedBy(name string) ([]string, error) {
	usedby := []string{}

	// Get the pool ID.
	id, err := c.GetStoragePoolID(name)
	if err != nil {
		return nil, err
	}

	// Get the cluster nodes.
	nodes, err := c.GetNodes()
	if err != nil {
		return nil, err
	}
	nodesName := map[int64]string{}

	for _, node := range nodes {
		nodesName[node.ID] = node.Name
	}

	// Get all the storage volumes on this node.
	vols := []struct {
		volName     string
		volType     int64
		projectName string
		nodeID      int64
	}{}
	dest := func(i int) []interface{} {
		vols = append(vols, struct {
			volName     string
			volType     int64
			projectName string
			nodeID      int64
		}{})

		return []interface{}{&vols[i].volName, &vols[i].volType, &vols[i].projectName, &vols[i].nodeID}
	}

	stmt, err := c.tx.Prepare("SELECT storage_volumes.name, storage_volumes.type, projects.name, storage_volumes.node_id FROM storage_volumes JOIN projects ON projects.id=storage_volumes.project_id WHERE storage_pool_id=? AND (node_id=? OR storage_volumes.type == 2) ORDER BY storage_volumes.type ASC, projects.name ASC, storage_volumes.name ASC, storage_volumes.node_id ASC")
	if err != nil {
		return nil, err
	}

	err = query.SelectObjects(stmt, dest, id, c.nodeID)
	if err != nil {
		return nil, err
	}

	for _, r := range vols {
		// Handle instances.
		if r.volType == StoragePoolVolumeTypeContainer || r.volType == StoragePoolVolumeTypeVM {
			if r.projectName == "default" {
				usedby = append(usedby, fmt.Sprintf("/1.0/instances/%s", r.volName))
			} else {
				usedby = append(usedby, fmt.Sprintf("/1.0/instances/%s?project=%s", r.volName, r.projectName))
			}
		}

		// Handle images.
		if r.volType == StoragePoolVolumeTypeImage {
			// Get the projects using an image.
			stmt := "SELECT projects.name FROM images JOIN projects ON projects.id=images.project_id WHERE fingerprint=?"
			projects, err := query.SelectStrings(c.tx, stmt, r.volName)
			if err != nil {
				return nil, err
			}

			for _, project := range projects {
				if project == "default" {
					usedby = append(usedby, fmt.Sprintf("/1.0/images/%s", r.volName))
				} else {
					usedby = append(usedby, fmt.Sprintf("/1.0/images/%s?project=%s", r.volName, project))
				}
			}
		}

		// Handle custom storage volumes.
		if r.volType == StoragePoolVolumeTypeCustom {
			if nodesName[r.nodeID] != "none" {
				if r.projectName == "default" {
					usedby = append(usedby, fmt.Sprintf("/1.0/storage-pools/%s/volumes/custom/%s?target=%s", name, r.volName, nodesName[r.nodeID]))
				} else {
					usedby = append(usedby, fmt.Sprintf("/1.0/storage-pools/%s/volumes/custom/%s?project=%s&target=%s", name, r.volName, r.projectName, nodesName[r.nodeID]))
				}
			} else {
				if r.projectName == "default" {
					usedby = append(usedby, fmt.Sprintf("/1.0/storage-pools/%s/volumes/custom/%s", name, r.volName))
				} else {
					usedby = append(usedby, fmt.Sprintf("/1.0/storage-pools/%s/volumes/custom/%s?project=%s", name, r.volName, r.projectName))
				}
			}
		}
	}

	// Get all the profiles using the storage pool.
	profiles, err := c.GetProfiles(ProfileFilter{})
	if err != nil {
		return nil, err
	}

	for _, profile := range profiles {
		for _, v := range profile.Devices {
			if v["type"] != "disk" {
				continue
			}

			if v["pool"] != name {
				continue
			}

			if profile.Project == "default" {
				usedby = append(usedby, fmt.Sprintf("/1.0/profiles/%s", profile.Name))
			} else {
				usedby = append(usedby, fmt.Sprintf("/1.0/profiles/%s?project=%s", profile.Name, profile.Project))
			}
		}
	}

	// Sort the output.
	sort.Strings(usedby)

	return usedby, nil
}

// GetStoragePoolID returns the ID of the pool with the given name.
func (c *ClusterTx) GetStoragePoolID(name string) (int64, error) {
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

// GetStoragePoolDriver returns the driver of the pool with the given ID.
func (c *ClusterTx) GetStoragePoolDriver(id int64) (string, error) {
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

// GetNonPendingStoragePoolsNamesToIDs returns a map associating each storage pool name to its ID.
//
// Pending storage pools are skipped.
func (c *ClusterTx) GetNonPendingStoragePoolsNamesToIDs() (map[string]int64, error) {
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

// UpdateStoragePoolAfterNodeJoin adds a new entry in the storage_pools_nodes table.
//
// It should only be used when a new node joins the cluster, when it's safe to
// assume that the relevant pool has already been created on the joining node,
// and we just need to track it.
func (c *ClusterTx) UpdateStoragePoolAfterNodeJoin(poolID, nodeID int64) error {
	columns := []string{"storage_pool_id", "node_id"}
	values := []interface{}{poolID, nodeID}
	_, err := query.UpsertObject(c.tx, "storage_pools_nodes", columns, values)
	if err != nil {
		return errors.Wrap(err, "failed to add storage pools node entry")
	}

	return nil
}

// UpdateCephStoragePoolAfterNodeJoin updates internal state to reflect that nodeID is
// joining a cluster where poolID is a ceph pool.
func (c *ClusterTx) UpdateCephStoragePoolAfterNodeJoin(poolID, nodeID int64) error {
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
		volumeID := volumeIDs[i]
		config, err := query.SelectConfig(
			c.tx, "storage_volumes_config", "storage_volume_id=?", otherVolumeID)
		if err != nil {
			return errors.Wrap(err, "failed to get storage volume config")
		}
		for key, value := range config {
			_, err := c.tx.Exec(`
INSERT INTO storage_volumes_config(storage_volume_id, key, value) VALUES(?, ?, ?)
`, volumeID, key, value)
			if err != nil {
				return errors.Wrap(err, "failed to copy volume config")
			}
		}

		// Copy volume snapshots as well.
		otherSnapshotIDs, err := query.SelectIntegers(c.tx,
			"SELECT id FROM storage_volumes_snapshots WHERE storage_volume_id = ?",
			otherVolumeID)
		if err != nil {
			return err
		}

		for _, otherSnapshotID := range otherSnapshotIDs {
			var snapshotID int64
			_, err := c.tx.Exec("UPDATE sqlite_sequence SET seq = seq + 1 WHERE name = 'storage_volumes'")
			if err != nil {
				return errors.Wrap(err, "Increment storage volumes sequence")
			}
			row := c.tx.QueryRow("SELECT seq FROM sqlite_sequence WHERE name = 'storage_volumes' LIMIT 1")
			err = row.Scan(&snapshotID)
			if err != nil {
				return errors.Wrap(err, "Fetch next storage volume ID")
			}

			_, err = c.tx.Exec(`
INSERT INTO storage_volumes_snapshots (id, storage_volume_id, name, description)
SELECT ?, ?, name, description
  FROM storage_volumes_snapshots WHERE id=?
`, snapshotID, volumeID, otherSnapshotID)
			if err != nil {
				return errors.Wrap(err, "Copy volume snapshot")
			}

			_, err = c.tx.Exec(`
INSERT INTO storage_volumes_snapshots_config (storage_volume_snapshot_id, key, value)
SELECT ?, key, value
  FROM storage_volumes_snapshots_config
 WHERE storage_volume_snapshot_id=?
`, snapshotID, otherSnapshotID)
			if err != nil {
				return errors.Wrap(err, "Copy volume snapshot config")
			}
		}
	}

	return nil
}

// CreateStoragePoolConfig adds a new entry in the storage_pools_config table
func (c *ClusterTx) CreateStoragePoolConfig(poolID, nodeID int64, config map[string]string) error {
	return storagePoolConfigAdd(c.tx, poolID, nodeID, config)
}

// StoragePoolState indicates the state of the storage pool or storage pool node.
type StoragePoolState int

// Storage pools state.
const (
	storagePoolPending StoragePoolState = iota // Storage pool defined but not yet created globally or on specific node.
	storagePoolCreated                         // Storage pool created globally or on specific node.
	storagePoolErrored                         // Deprecated (should no longer occur).
)

// StoragePoolNode represents a storage pool node.
type StoragePoolNode struct {
	ID    int64
	Name  string
	State StoragePoolState
}

// CreatePendingStoragePool creates a new pending storage pool on the node with
// the given name.
func (c *ClusterTx) CreatePendingStoragePool(node, name, driver string, conf map[string]string) error {
	// First check if a storage pool with the given name exists, and, if
	// so, that it has a matching driver and it's in the pending state.
	pool := struct {
		id     int64
		driver string
		state  StoragePoolState
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
	nodeInfo, err := c.GetNodeByName(node)
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
	err = c.CreateStoragePoolConfig(poolID, nodeInfo.ID, conf)
	if err != nil {
		return err
	}

	return nil
}

// StoragePoolCreated sets the state of the given pool to storagePoolCreated.
func (c *ClusterTx) StoragePoolCreated(name string) error {
	return c.storagePoolState(name, storagePoolCreated)
}

func (c *ClusterTx) storagePoolState(name string, state StoragePoolState) error {
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

// storagePoolNodes returns the nodes keyed by node ID that the given storage pool is defined on.
func (c *ClusterTx) storagePoolNodes(poolID int64) (map[int64]StoragePoolNode, error) {
	nodes := []StoragePoolNode{}
	dest := func(i int) []interface{} {
		nodes = append(nodes, StoragePoolNode{})
		return []interface{}{&nodes[i].ID, &nodes[i].Name}
	}

	stmt, err := c.tx.Prepare(`
		SELECT nodes.id, nodes.name FROM nodes
		JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id
		WHERE storage_pools_nodes.storage_pool_id = ?
	`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	err = query.SelectObjects(stmt, dest, poolID)
	if err != nil {
		return nil, err
	}

	poolNodes := map[int64]StoragePoolNode{}
	for _, node := range nodes {
		node.State = -1
		poolNodes[node.ID] = node
	}

	return poolNodes, nil
}

// GetStoragePoolNodeConfigs returns the node-specific configuration of all
// nodes grouped by node name, for the given poolID.
//
// If the storage pool is not defined on all nodes, an error is returned.
func (c *ClusterTx) GetStoragePoolNodeConfigs(poolID int64) (map[string]map[string]string, error) {
	// Fetch all nodes.
	nodes, err := c.GetNodes()
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

// GetStoragePoolNames returns the names of all storage pools.
func (c *Cluster) GetStoragePoolNames() ([]string, error) {
	return c.storagePools("")
}

// GetNonPendingStoragePoolNames returns the names of all storage pools that are not
// pending.
func (c *Cluster) GetNonPendingStoragePoolNames() ([]string, error) {
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

	result, err := queryScan(c, stmt, inargs, outargs)
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

// GetStoragePoolDrivers returns the names of all storage drivers currently
// being used by at least one storage pool.
func (c *Cluster) GetStoragePoolDrivers() ([]string, error) {
	var poolDriver string
	query := "SELECT DISTINCT driver FROM storage_pools"
	inargs := []interface{}{}
	outargs := []interface{}{poolDriver}

	result, err := queryScan(c, query, inargs, outargs)
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

// GetStoragePoolID returns the id of a single storage pool.
func (c *Cluster) GetStoragePoolID(poolName string) (int64, error) {
	poolID := int64(-1)
	query := "SELECT id FROM storage_pools WHERE name=?"
	inargs := []interface{}{poolName}
	outargs := []interface{}{&poolID}

	err := dbQueryRowScan(c, query, inargs, outargs)
	if err != nil {
		if err == sql.ErrNoRows {
			return -1, ErrNoSuchObject
		}
	}

	return poolID, nil
}

// GetStoragePool returns a single storage pool.
//
// The pool must be in the created stated, not pending.
func (c *Cluster) GetStoragePool(poolName string) (int64, *api.StoragePool, map[int64]StoragePoolNode, error) {
	return c.getStoragePool(poolName, true)
}

// GetStoragePoolInAnyState returns the storage pool with the given name.
//
// The pool can be in any state.
func (c *Cluster) GetStoragePoolInAnyState(name string) (int64, *api.StoragePool, map[int64]StoragePoolNode, error) {
	return c.getStoragePool(name, false)
}

// GetStoragePool returns a single storage pool.
func (c *Cluster) getStoragePool(poolName string, onlyCreated bool) (int64, *api.StoragePool, map[int64]StoragePoolNode, error) {
	var poolDriver string
	poolID := int64(-1)
	description := sql.NullString{}
	var state StoragePoolState

	query := "SELECT id, driver, description, state FROM storage_pools WHERE name=?"
	inargs := []interface{}{poolName}
	outargs := []interface{}{&poolID, &poolDriver, &description, &state}
	if onlyCreated {
		query += " AND state=?"
		inargs = append(inargs, storagePoolCreated)
	}

	err := dbQueryRowScan(c, query, inargs, outargs)
	if err != nil {
		if err == sql.ErrNoRows {
			return -1, nil, nil, ErrNoSuchObject
		}
		return -1, nil, nil, err
	}

	config, err := c.getStoragePoolConfig(poolID)
	if err != nil {
		return -1, nil, nil, err
	}

	storagePool := api.StoragePool{
		Name:   poolName,
		Driver: poolDriver,
	}
	storagePool.Description = description.String
	storagePool.Config = config
	storagePool.Status = StoragePoolStateToAPIStatus(state)

	nodes, err := c.storagePoolNodes(poolID)
	if err != nil {
		return -1, nil, nil, err
	}

	for _, node := range nodes {
		storagePool.Locations = append(storagePool.Locations, node.Name)
	}

	return poolID, &storagePool, nodes, nil
}

// StoragePoolStateToAPIStatus converts DB StoragePoolState to API status string.
func StoragePoolStateToAPIStatus(state StoragePoolState) string {
	switch state {
	case storagePoolPending:
		return api.StoragePoolStatusPending
	case storagePoolCreated:
		return api.StoragePoolStatusCreated
	case storagePoolErrored:
		return api.StoragePoolStatusErrored
	default:
		return api.StoragePoolStatusUnknown
	}
}

// storagePoolNodes returns the nodes keyed by node ID that the given storage pool is defined on.
func (c *Cluster) storagePoolNodes(poolID int64) (map[int64]StoragePoolNode, error) {
	var nodes map[int64]StoragePoolNode
	var err error

	err = c.Transaction(func(tx *ClusterTx) error {
		nodes, err = tx.storagePoolNodes(poolID)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return nodes, nil
}

// Return the config of a storage pool.
func (c *Cluster) getStoragePoolConfig(poolID int64) (map[string]string, error) {
	var key, value string
	query := "SELECT key, value FROM storage_pools_config WHERE storage_pool_id=? AND (node_id=? OR node_id IS NULL)"
	inargs := []interface{}{poolID, c.nodeID}
	outargs := []interface{}{key, value}

	results, err := queryScan(c, query, inargs, outargs)
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

// CreateStoragePool creates new storage pool.
func (c *Cluster) CreateStoragePool(poolName string, poolDescription string, poolDriver string, poolConfig map[string]string) (int64, error) {
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

// UpdateStoragePool updates a storage pool.
func (c *Cluster) UpdateStoragePool(poolName, description string, poolConfig map[string]string) error {
	poolID, _, _, err := c.GetStoragePoolInAnyState(poolName)
	if err != nil {
		return err
	}

	err = c.Transaction(func(tx *ClusterTx) error {
		err = updateStoragePoolDescription(tx.tx, poolID, description)
		if err != nil {
			return err
		}

		err = clearStoragePoolConfig(tx.tx, poolID, c.nodeID)
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

// Uupdate the storage pool description.
func updateStoragePoolDescription(tx *sql.Tx, id int64, description string) error {
	_, err := tx.Exec("UPDATE storage_pools SET description=? WHERE id=?", description, id)
	return err
}

// Delete the storage pool config.
func clearStoragePoolConfig(tx *sql.Tx, poolID, nodeID int64) error {
	_, err := tx.Exec("DELETE FROM storage_pools_config WHERE storage_pool_id=? AND (node_id=? OR node_id IS NULL)", poolID, nodeID)
	if err != nil {
		return err
	}

	return nil
}

// RemoveStoragePool deletes storage pool.
func (c *Cluster) RemoveStoragePool(poolName string) (*api.StoragePool, error) {
	poolID, pool, _, err := c.GetStoragePoolInAnyState(poolName)
	if err != nil {
		return nil, err
	}

	err = exec(c, "DELETE FROM storage_pools WHERE id=?", poolID)
	if err != nil {
		return nil, err
	}

	return pool, nil
}

// FillMissingStoragePoolDriver fills the driver of all storage pools without a
// driver, setting it to 'zfs'.
func (c *Cluster) FillMissingStoragePoolDriver() error {
	err := exec(c, "UPDATE storage_pools SET driver='zfs', description='' WHERE driver=''")
	return err
}

// StoragePoolNodeConfigKeys lists all storage pool config keys which are
// node-specific.
var StoragePoolNodeConfigKeys = []string{
	"size",
	"source",
	"volatile.initial_source",
	"zfs.pool_name",
	"lvm.thinpool_name",
	"lvm.vg_name",
}

func (c *Cluster) isRemoteStorage(poolID int64) (bool, error) {
	isRemoteStorage := false

	err := c.Transaction(func(tx *ClusterTx) error {
		driver, err := tx.GetStoragePoolDriver(poolID)
		if err != nil {
			return err
		}

		isRemoteStorage = shared.StringInSlice(driver, StorageRemoteDriverNames())

		return nil
	})

	return isRemoteStorage, err
}
