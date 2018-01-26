package db

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/lxc/lxd/lxd/types"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"

	log "github.com/lxc/lxd/shared/log15"
)

// ContainerArgs is a value object holding all db-related details about a
// container.
type ContainerArgs struct {
	// Don't set manually
	Id   int
	Node string

	Description  string
	Architecture int
	BaseImage    string
	Config       map[string]string
	CreationDate time.Time
	LastUsedDate time.Time
	Ctype        ContainerType
	Devices      types.Devices
	Ephemeral    bool
	Name         string
	Profiles     []string
	Stateful     bool
}

// ContainerType encodes the type of container (either regular or snapshot).
type ContainerType int

const (
	CTypeRegular  ContainerType = 0
	CTypeSnapshot ContainerType = 1
)

// ContainerNodeAddress returns the address of the node hosting the container
// with the given name.
//
// It returns the empty string if the container is hosted on this node.
func (c *ClusterTx) ContainerNodeAddress(name string) (string, error) {
	stmt := `
SELECT nodes.id, nodes.address
  FROM nodes JOIN containers ON containers.node_id = nodes.id
    WHERE containers.name = ?
`
	var address string
	var id int64
	rows, err := c.tx.Query(stmt, name)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	if !rows.Next() {
		return "", NoSuchObjectError
	}

	err = rows.Scan(&id, &address)
	if err != nil {
		return "", err
	}

	if rows.Next() {
		return "", fmt.Errorf("more than one node associated with container")
	}

	err = rows.Err()
	if err != nil {
		return "", err
	}

	if id == c.nodeID {
		return "", nil
	}

	return address, nil
}

// ContainersListByNodeAddress returns the names of all containers grouped by
// cluster node address.
//
// The node address of containers running on the local node is set to the empty
// string, to distinguish it from remote nodes.
//
// Containers whose node is down are addeded to the special address "0.0.0.0".
func (c *ClusterTx) ContainersListByNodeAddress() (map[string][]string, error) {
	offlineThreshold, err := c.NodeOfflineThreshold()
	if err != nil {
		return nil, err
	}

	stmt := `
SELECT containers.name, nodes.id, nodes.address, nodes.heartbeat
  FROM containers JOIN nodes ON nodes.id = containers.node_id
  WHERE containers.type=?
  ORDER BY containers.id
`
	rows, err := c.tx.Query(stmt, CTypeRegular)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := map[string][]string{}

	for i := 0; rows.Next(); i++ {
		var name string
		var nodeAddress string
		var nodeID int64
		var nodeHeartbeat time.Time
		err := rows.Scan(&name, &nodeID, &nodeAddress, &nodeHeartbeat)
		if err != nil {
			return nil, err
		}
		if nodeID == c.nodeID {
			nodeAddress = ""
		} else if nodeIsOffline(offlineThreshold, nodeHeartbeat) {
			nodeAddress = "0.0.0.0"
		}
		result[nodeAddress] = append(result[nodeAddress], name)
	}

	err = rows.Err()
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ContainersByNodeName returns a map associating each container to the name of
// its node.
func (c *ClusterTx) ContainersByNodeName() (map[string]string, error) {
	stmt := `
SELECT containers.name, nodes.name
  FROM containers JOIN nodes ON nodes.id = containers.node_id
  WHERE containers.type=?
`
	rows, err := c.tx.Query(stmt, CTypeRegular)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := map[string]string{}

	for i := 0; rows.Next(); i++ {
		var name string
		var nodeName string
		err := rows.Scan(&name, &nodeName)
		if err != nil {
			return nil, err
		}
		result[name] = nodeName
	}

	err = rows.Err()
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Cluster) ContainerRemove(name string) error {
	id, err := c.ContainerId(name)
	if err != nil {
		return err
	}

	_, err = exec(c.db, "DELETE FROM containers WHERE id=?", id)
	if err != nil {
		return err
	}

	return nil
}

func (c *Cluster) ContainerName(id int) (string, error) {
	q := "SELECT name FROM containers WHERE id=?"
	name := ""
	arg1 := []interface{}{id}
	arg2 := []interface{}{&name}
	err := dbQueryRowScan(c.db, q, arg1, arg2)
	return name, err
}

func (c *Cluster) ContainerId(name string) (int, error) {
	q := "SELECT id FROM containers WHERE name=?"
	id := -1
	arg1 := []interface{}{name}
	arg2 := []interface{}{&id}
	err := dbQueryRowScan(c.db, q, arg1, arg2)
	return id, err
}

func (c *Cluster) ContainerGet(name string) (ContainerArgs, error) {
	var used *time.Time    // Hold the db-returned time
	var nodeAddress string // Hold the db-returned node address
	description := sql.NullString{}

	args := ContainerArgs{}
	args.Name = name

	ephemInt := -1
	statefulInt := -1
	q := `
SELECT containers.id, containers.description, architecture, type, ephemeral, stateful,
       creation_date, last_use_date, nodes.name, nodes.address
  FROM containers JOIN nodes ON node_id = nodes.id
  WHERE containers.name=?
`
	arg1 := []interface{}{name}
	arg2 := []interface{}{&args.Id, &description, &args.Architecture, &args.Ctype, &ephemInt, &statefulInt, &args.CreationDate, &used, &args.Node, &nodeAddress}
	err := dbQueryRowScan(c.db, q, arg1, arg2)
	if err != nil {
		return args, err
	}

	args.Description = description.String

	if args.Id == -1 {
		return args, fmt.Errorf("Unknown container")
	}

	if ephemInt == 1 {
		args.Ephemeral = true
	}

	if statefulInt == 1 {
		args.Stateful = true
	}

	if used != nil {
		args.LastUsedDate = *used
	} else {
		args.LastUsedDate = time.Unix(0, 0).UTC()
	}

	config, err := c.ContainerConfig(args.Id)
	if err != nil {
		return args, err
	}
	args.Config = config

	profiles, err := c.ContainerProfiles(args.Id)
	if err != nil {
		return args, err
	}
	args.Profiles = profiles

	/* get container_devices */
	args.Devices = types.Devices{}
	newdevs, err := c.Devices(name, false)
	if err != nil {
		return args, err
	}

	for k, v := range newdevs {
		args.Devices[k] = v
	}

	if nodeAddress == "0.0.0.0" {
		// This means we're not clustered, so omit the node name
		args.Node = ""
	}

	return args, nil
}

func (c *Cluster) ContainerCreate(args ContainerArgs) (int, error) {
	_, err := c.ContainerId(args.Name)
	if err == nil {
		return 0, DbErrAlreadyDefined
	}

	tx, err := begin(c.db)
	if err != nil {
		return 0, err
	}

	ephemInt := 0
	if args.Ephemeral == true {
		ephemInt = 1
	}

	statefulInt := 0
	if args.Stateful == true {
		statefulInt = 1
	}

	args.CreationDate = time.Now().UTC()
	args.LastUsedDate = time.Unix(0, 0).UTC()

	str := fmt.Sprintf("INSERT INTO containers (node_id, name, architecture, type, ephemeral, creation_date, last_use_date, stateful) VALUES (?, ?, ?, ?, ?, ?, ?, ?)")
	stmt, err := tx.Prepare(str)
	if err != nil {
		tx.Rollback()
		return 0, err
	}
	defer stmt.Close()
	result, err := stmt.Exec(c.nodeID, args.Name, args.Architecture, args.Ctype, ephemInt, args.CreationDate.Unix(), args.LastUsedDate.Unix(), statefulInt)
	if err != nil {
		tx.Rollback()
		return 0, err
	}

	id64, err := result.LastInsertId()
	if err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("Error inserting %s into database", args.Name)
	}
	// TODO: is this really int64? we should fix it everywhere if so
	id := int(id64)
	if err := ContainerConfigInsert(tx, id, args.Config); err != nil {
		tx.Rollback()
		return 0, err
	}

	if err := ContainerProfilesInsert(tx, id, args.Profiles); err != nil {
		tx.Rollback()
		return 0, err
	}

	if err := DevicesAdd(tx, "container", int64(id), args.Devices); err != nil {
		tx.Rollback()
		return 0, err
	}

	return id, TxCommit(tx)
}

func ContainerConfigClear(tx *sql.Tx, id int) error {
	_, err := tx.Exec("DELETE FROM containers_config WHERE container_id=?", id)
	if err != nil {
		return err
	}
	_, err = tx.Exec("DELETE FROM containers_profiles WHERE container_id=?", id)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`DELETE FROM containers_devices_config WHERE id IN
		(SELECT containers_devices_config.id
		 FROM containers_devices_config JOIN containers_devices
		 ON containers_devices_config.container_device_id=containers_devices.id
		 WHERE containers_devices.container_id=?)`, id)
	if err != nil {
		return err
	}
	_, err = tx.Exec("DELETE FROM containers_devices WHERE container_id=?", id)
	return err
}

func ContainerConfigInsert(tx *sql.Tx, id int, config map[string]string) error {
	str := "INSERT INTO containers_config (container_id, key, value) values (?, ?, ?)"
	stmt, err := tx.Prepare(str)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for k, v := range config {
		_, err := stmt.Exec(id, k, v)
		if err != nil {
			logger.Debugf("Error adding configuration item %s = %s to container %d",
				k, v, id)
			return err
		}
	}

	return nil
}

func (c *Cluster) ContainerConfigGet(id int, key string) (string, error) {
	q := "SELECT value FROM containers_config WHERE container_id=? AND key=?"
	value := ""
	arg1 := []interface{}{id, key}
	arg2 := []interface{}{&value}
	err := dbQueryRowScan(c.db, q, arg1, arg2)
	return value, err
}

func (c *Cluster) ContainerConfigRemove(id int, name string) error {
	_, err := exec(c.db, "DELETE FROM containers_config WHERE key=? AND container_id=?", name, id)
	return err
}

func (c *Cluster) ContainerSetStateful(id int, stateful bool) error {
	statefulInt := 0
	if stateful {
		statefulInt = 1
	}

	_, err := exec(c.db, "UPDATE containers SET stateful=? WHERE id=?", statefulInt, id)
	return err
}

func ContainerProfilesInsert(tx *sql.Tx, id int, profiles []string) error {
	applyOrder := 1
	str := `INSERT INTO containers_profiles (container_id, profile_id, apply_order) VALUES
		(?, (SELECT id FROM profiles WHERE name=?), ?);`
	stmt, err := tx.Prepare(str)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, p := range profiles {
		_, err = stmt.Exec(id, p, applyOrder)
		if err != nil {
			logger.Debugf("Error adding profile %s to container: %s",
				p, err)
			return err
		}
		applyOrder = applyOrder + 1
	}

	return nil
}

// Get a list of profiles for a given container id.
func (c *Cluster) ContainerProfiles(containerId int) ([]string, error) {
	var name string
	var profiles []string

	query := `
        SELECT name FROM containers_profiles
        JOIN profiles ON containers_profiles.profile_id=profiles.id
		WHERE container_id=?
        ORDER BY containers_profiles.apply_order`
	inargs := []interface{}{containerId}
	outfmt := []interface{}{name}

	results, err := queryScan(c.db, query, inargs, outfmt)
	if err != nil {
		return nil, err
	}

	for _, r := range results {
		name = r[0].(string)

		profiles = append(profiles, name)
	}

	return profiles, nil
}

// ContainerConfig gets the container configuration map from the DB
func (c *Cluster) ContainerConfig(containerId int) (map[string]string, error) {
	var key, value string
	q := `SELECT key, value FROM containers_config WHERE container_id=?`

	inargs := []interface{}{containerId}
	outfmt := []interface{}{key, value}

	// Results is already a slice here, not db Rows anymore.
	results, err := queryScan(c.db, q, inargs, outfmt)
	if err != nil {
		return nil, err //SmartError will wrap this and make "not found" errors pretty
	}

	config := map[string]string{}

	for _, r := range results {
		key = r[0].(string)
		value = r[1].(string)

		config[key] = value
	}

	return config, nil
}

func (c *Cluster) ContainersList(cType ContainerType) ([]string, error) {
	q := fmt.Sprintf("SELECT name FROM containers WHERE type=? ORDER BY name")
	inargs := []interface{}{cType}
	var container string
	outfmt := []interface{}{container}
	result, err := queryScan(c.db, q, inargs, outfmt)
	if err != nil {
		return nil, err
	}

	var ret []string
	for _, container := range result {
		ret = append(ret, container[0].(string))
	}

	return ret, nil
}

func (c *Cluster) ContainersResetState() error {
	// Reset all container states
	_, err := exec(c.db, "DELETE FROM containers_config WHERE key='volatile.last_state.power'")
	return err
}

func (c *Cluster) ContainerSetState(id int, state string) error {
	tx, err := begin(c.db)
	if err != nil {
		return err
	}

	// Clear any existing entry
	str := fmt.Sprintf("DELETE FROM containers_config WHERE container_id = ? AND key = 'volatile.last_state.power'")
	stmt, err := tx.Prepare(str)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	if _, err := stmt.Exec(id); err != nil {
		tx.Rollback()
		return err
	}

	// Insert the new one
	str = fmt.Sprintf("INSERT INTO containers_config (container_id, key, value) VALUES (?, 'volatile.last_state.power', ?)")
	stmt, err = tx.Prepare(str)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	if _, err = stmt.Exec(id, state); err != nil {
		tx.Rollback()
		return err
	}

	return TxCommit(tx)
}

func (c *Cluster) ContainerRename(oldName string, newName string) error {
	tx, err := begin(c.db)
	if err != nil {
		return err
	}

	str := fmt.Sprintf("UPDATE containers SET name = ? WHERE name = ?")
	stmt, err := tx.Prepare(str)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	logger.Debug(
		"Calling SQL Query",
		log.Ctx{
			"query":   "UPDATE containers SET name = ? WHERE name = ?",
			"oldName": oldName,
			"newName": newName})
	if _, err := stmt.Exec(newName, oldName); err != nil {
		tx.Rollback()
		return err
	}

	return TxCommit(tx)
}

func ContainerUpdate(tx *sql.Tx, id int, description string, architecture int, ephemeral bool) error {
	str := fmt.Sprintf("UPDATE containers SET description=?, architecture=?, ephemeral=? WHERE id=?")
	stmt, err := tx.Prepare(str)
	if err != nil {
		return err
	}
	defer stmt.Close()

	ephemeralInt := 0
	if ephemeral {
		ephemeralInt = 1
	}

	if _, err := stmt.Exec(description, architecture, ephemeralInt, id); err != nil {
		return err
	}

	return nil
}

func (c *Cluster) ContainerLastUsedUpdate(id int, date time.Time) error {
	stmt := `UPDATE containers SET last_use_date=? WHERE id=?`
	_, err := exec(c.db, stmt, date, id)
	return err
}

func (c *Cluster) ContainerGetSnapshots(name string) ([]string, error) {
	result := []string{}

	regexp := name + shared.SnapshotDelimiter
	length := len(regexp)
	q := "SELECT name FROM containers WHERE type=? AND SUBSTR(name,1,?)=?"
	inargs := []interface{}{CTypeSnapshot, length, regexp}
	outfmt := []interface{}{name}
	dbResults, err := queryScan(c.db, q, inargs, outfmt)
	if err != nil {
		return result, err
	}

	for _, r := range dbResults {
		result = append(result, r[0].(string))
	}

	return result, nil
}

/*
 * Note, the code below doesn't deal with snapshots of snapshots.
 * To do that, we'll need to weed out based on # slashes in names
 */
func (c *Cluster) ContainerNextSnapshot(name string) int {
	base := name + shared.SnapshotDelimiter + "snap"
	length := len(base)
	q := fmt.Sprintf("SELECT name FROM containers WHERE type=? AND SUBSTR(name,1,?)=?")
	var numstr string
	inargs := []interface{}{CTypeSnapshot, length, base}
	outfmt := []interface{}{numstr}
	results, err := queryScan(c.db, q, inargs, outfmt)
	if err != nil {
		return 0
	}
	max := 0

	for _, r := range results {
		numstr = r[0].(string)
		if len(numstr) <= length {
			continue
		}
		substr := numstr[length:]
		var num int
		count, err := fmt.Sscanf(substr, "%d", &num)
		if err != nil || count != 1 {
			continue
		}
		if num >= max {
			max = num + 1
		}
	}

	return max
}

// Get the storage pool of a given container.
func (c *Cluster) ContainerPool(containerName string) (string, error) {
	// Get container storage volume. Since container names are globally
	// unique, and their storage volumes carry the same name, their storage
	// volumes are unique too.
	poolName := ""
	query := `SELECT storage_pools.name FROM storage_pools
JOIN storage_volumes ON storage_pools.id=storage_volumes.storage_pool_id
WHERE storage_volumes.node_id=? AND storage_volumes.name=? AND storage_volumes.type=?`
	inargs := []interface{}{c.nodeID, containerName, StoragePoolVolumeTypeContainer}
	outargs := []interface{}{&poolName}

	err := dbQueryRowScan(c.db, query, inargs, outargs)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", NoSuchObjectError
		}

		return "", err
	}

	return poolName, nil
}
