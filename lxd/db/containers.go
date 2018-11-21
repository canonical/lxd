package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/lxd/types"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
	"github.com/pkg/errors"

	log "github.com/lxc/lxd/shared/log15"
)

// ContainerArgs is a value object holding all db-related details about a
// container.
type ContainerArgs struct {
	// Don't set manually
	ID    int
	Node  string
	Ctype ContainerType

	// Creation only
	BaseImage    string
	CreationDate time.Time

	Architecture int
	Config       map[string]string
	Description  string
	Devices      types.Devices
	Ephemeral    bool
	LastUsedDate time.Time
	Name         string
	Profiles     []string
	Stateful     bool
}

// ContainerType encodes the type of container (either regular or snapshot).
type ContainerType int

// Numerical codes for container types.
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
		return "", ErrNoSuchObject
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

// ContainerID returns the ID of the container with the given name.
func (c *ClusterTx) ContainerID(name string) (int64, error) {
	stmt := "SELECT id FROM containers WHERE name=?"
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
		return -1, fmt.Errorf("more than one container has the given name")
	}
}

// SnapshotIDsAndNames returns a map of snapshot IDs to snapshot names for the
// container with the given name.
func (c *ClusterTx) SnapshotIDsAndNames(name string) (map[int]string, error) {
	prefix := name + shared.SnapshotDelimiter
	length := len(prefix)
	objects := make([]struct {
		ID   int
		Name string
	}, 0)
	dest := func(i int) []interface{} {
		objects = append(objects, struct {
			ID   int
			Name string
		}{})
		return []interface{}{&objects[i].ID, &objects[i].Name}
	}
	stmt, err := c.tx.Prepare("SELECT id, name FROM containers WHERE SUBSTR(name,1,?)=? AND type=?")
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	err = query.SelectObjects(stmt, dest, length, prefix, CTypeSnapshot)
	if err != nil {
		return nil, err
	}
	result := make(map[int]string)
	for i := range objects {
		result[objects[i].ID] = strings.Split(objects[i].Name, shared.SnapshotDelimiter)[1]
	}
	return result, nil
}

// ContainerNodeMove changes the node associated with a container.
//
// It's meant to be used when moving a non-running container backed by ceph
// from one cluster node to another.
func (c *ClusterTx) ContainerNodeMove(oldName, newName, newNode string) error {
	// First check that the container to be moved is backed by a ceph
	// volume.
	poolName, err := c.ContainerPool(oldName)
	if err != nil {
		return errors.Wrap(err, "failed to get container's storage pool name")
	}
	poolID, err := c.StoragePoolID(poolName)
	if err != nil {
		return errors.Wrap(err, "failed to get container's storage pool ID")
	}
	poolDriver, err := c.StoragePoolDriver(poolID)
	if err != nil {
		return errors.Wrap(err, "failed to get container's storage pool driver")
	}
	if poolDriver != "ceph" {
		return fmt.Errorf("container's storage pool is not of type ceph")
	}

	// Update the name of the container and of its snapshots, and the node
	// ID they are associated with.
	containerID, err := c.ContainerID(oldName)
	if err != nil {
		return errors.Wrap(err, "failed to get container's ID")
	}
	snapshots, err := c.SnapshotIDsAndNames(oldName)
	if err != nil {
		return errors.Wrap(err, "failed to get container's snapshots")
	}
	node, err := c.NodeByName(newNode)
	if err != nil {
		return errors.Wrap(err, "failed to get new node's info")
	}
	stmt := "UPDATE containers SET node_id=?, name=? WHERE id=?"
	result, err := c.tx.Exec(stmt, node.ID, newName, containerID)
	if err != nil {
		return errors.Wrap(err, "failed to update container's name and node ID")
	}
	n, err := result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "failed to get rows affected by container update")
	}
	if n != 1 {
		return fmt.Errorf("unexpected number of updated rows in containers table: %d", n)
	}
	for snapshotID, snapshotName := range snapshots {
		newSnapshotName := newName + shared.SnapshotDelimiter + snapshotName
		stmt := "UPDATE containers SET node_id=?, name=? WHERE id=?"
		result, err := c.tx.Exec(stmt, node.ID, newSnapshotName, snapshotID)
		if err != nil {
			return errors.Wrap(err, "failed to update snapshot's name and node ID")
		}
		n, err := result.RowsAffected()
		if err != nil {
			return errors.Wrap(err, "failed to get rows affected by snapshot update")
		}
		if n != 1 {
			return fmt.Errorf("unexpected number of updated snapshot rows: %d", n)
		}
	}

	// No need to update storage_volumes if the name is identical
	if newName == oldName {
		return nil
	}

	// Update the container's and snapshots' storage volume name (since this is ceph,
	// there's a clone of the volume for each node).
	count, err := c.NodesCount()
	if err != nil {
		return errors.Wrap(err, "failed to get node's count")
	}
	stmt = "UPDATE storage_volumes SET name=? WHERE name=? AND storage_pool_id=? AND type=?"
	result, err = c.tx.Exec(stmt, newName, oldName, poolID, StoragePoolVolumeTypeContainer)
	if err != nil {
		return errors.Wrap(err, "failed to update container's volume name")
	}
	n, err = result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "failed to get rows affected by container volume update")
	}
	if n != int64(count) {
		return fmt.Errorf("unexpected number of updated rows in volumes table: %d", n)
	}
	for _, snapshotName := range snapshots {
		oldSnapshotName := oldName + shared.SnapshotDelimiter + snapshotName
		newSnapshotName := newName + shared.SnapshotDelimiter + snapshotName
		stmt := "UPDATE storage_volumes SET name=? WHERE name=? AND storage_pool_id=? AND type=?"
		result, err := c.tx.Exec(
			stmt, newSnapshotName, oldSnapshotName, poolID, StoragePoolVolumeTypeContainer)
		if err != nil {
			return errors.Wrap(err, "failed to update snapshot volume")
		}
		n, err = result.RowsAffected()
		if err != nil {
			return errors.Wrap(err, "failed to get rows affected by snapshot volume update")
		}
		if n != int64(count) {
			return fmt.Errorf("unexpected number of updated snapshots in volumes table: %d", n)
		}
	}

	return nil
}

// ContainerArgsList returns all container objects alll node.
func (c *ClusterTx) ContainerArgsList() ([]ContainerArgs, error) {
	return c.containerArgsList(false)
}

// ContainerArgsNodeList returns all container objects on the local node.
func (c *ClusterTx) ContainerArgsNodeList() ([]ContainerArgs, error) {
	return c.containerArgsList(true)
}

func (c *ClusterTx) containerArgsList(local bool) ([]ContainerArgs, error) {
	// First query the containers table.
	sql := `
SELECT containers.id, nodes.name, type, creation_date, architecture,
       coalesce(containers.description, ''), ephemeral, last_use_date,
       containers.name, stateful
  FROM containers
  JOIN nodes ON containers.node_id=nodes.id
  WHERE type=0
`
	if local {
		sql += " AND nodes.id=?"
	}

	sql += `
ORDER BY containers.name
`

	containers := make([]ContainerArgs, 0)

	dest := func(i int) []interface{} {
		containers = append(containers, ContainerArgs{})
		return []interface{}{
			&containers[i].ID,
			&containers[i].Node,
			&containers[i].Ctype,
			&containers[i].CreationDate,
			&containers[i].Architecture,
			&containers[i].Description,
			&containers[i].Ephemeral,
			&containers[i].LastUsedDate,
			&containers[i].Name,
			&containers[i].Stateful,
		}
	}

	args := make([]interface{}, 0)
	if local {
		args = append(args, c.nodeID)
	}

	stmt, err := c.tx.Prepare(sql)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	err = query.SelectObjects(stmt, dest, args...)
	if err != nil {
		return nil, errors.Wrap(err, "failed to query containers")
	}

	// Make an index to populate configs and devices.
	index := make(map[int]*ContainerArgs, len(containers))
	for i := range containers {
		index[containers[i].ID] = &containers[i]
		containers[i].Config = map[string]string{}
		containers[i].Devices = types.Devices{}
		containers[i].Profiles = make([]string, 0)
	}

	// Query the containers_config table.
	sql = `
SELECT container_id, key, value
  FROM containers_config
  JOIN containers ON containers.id=container_id
`
	if local {
		sql += `
  JOIN nodes ON nodes.id=containers.node_id
  WHERE nodes.id=? AND containers.type=0
`
	} else {
		sql += `
  WHERE containers.type=0
`
	}

	configs := make([]struct {
		ContainerID int64
		Key         string
		Value       string
	}, 0)

	dest = func(i int) []interface{} {
		configs = append(configs, struct {
			ContainerID int64
			Key         string
			Value       string
		}{})

		return []interface{}{
			&configs[i].ContainerID,
			&configs[i].Key,
			&configs[i].Value,
		}
	}

	stmt, err = c.tx.Prepare(sql)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	err = query.SelectObjects(stmt, dest, args...)
	if err != nil {
		return nil, errors.Wrap(err, "failed to query containers config")
	}

	for _, config := range configs {
		index[int(config.ContainerID)].Config[config.Key] = config.Value
	}

	// Query the containers_devices/containers_devices_config tables.
	sql = `
SELECT container_id, containers_devices.name, containers_devices.type,
       coalesce(containers_devices_config.key, ''), coalesce(containers_devices_config.value, '')
  FROM containers_devices
  LEFT OUTER JOIN containers_devices_config ON containers_devices_config.container_device_id=containers_devices.id
  JOIN containers ON containers.id=container_id
`
	if local {
		sql += `
  JOIN nodes ON nodes.id=containers.node_id
  WHERE nodes.id=? AND containers.type=0
`
	} else {
		sql += `
  WHERE containers.type=0
`
	}

	devices := make([]struct {
		ContainerID int64
		Name        string
		Type        int64
		Key         string
		Value       string
	}, 0)

	dest = func(i int) []interface{} {
		devices = append(devices, struct {
			ContainerID int64
			Name        string
			Type        int64
			Key         string
			Value       string
		}{})

		return []interface{}{
			&devices[i].ContainerID,
			&devices[i].Name,
			&devices[i].Type,
			&devices[i].Key,
			&devices[i].Value,
		}
	}

	stmt, err = c.tx.Prepare(sql)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	err = query.SelectObjects(stmt, dest, args...)
	if err != nil {
		return nil, errors.Wrap(err, "failed to query containers devices")
	}

	for _, device := range devices {
		cid := int(device.ContainerID)
		_, ok := index[cid].Devices[device.Name]
		if !ok {
			// First time we see this device, let's int the config
			// and add the type.
			index[cid].Devices[device.Name] = make(map[string]string)

			typ, err := dbDeviceTypeToString(int(device.Type))
			if err != nil {
				return nil, errors.Wrapf(err, "unexpected device type code '%d'", device.Type)
			}
			index[cid].Devices[device.Name]["type"] = typ
		}

		if device.Key != "" {
			index[cid].Devices[device.Name][device.Key] = device.Value
		}

	}

	// Query the profiles table
	sql = `
SELECT container_id, profiles.name FROM containers_profiles
  JOIN profiles ON containers_profiles.profile_id=profiles.id
  JOIN containers ON containers.id=container_id
`

	if local {
		sql += `
  JOIN nodes ON nodes.id=containers.node_id
  WHERE nodes.id=? AND containers.type=0
`
	} else {
		sql += `
  WHERE containers.type=0
`
	}

	sql += `
ORDER BY containers_profiles.apply_order
`

	profiles := make([]struct {
		ContainerID int64
		Name        string
	}, 0)

	dest = func(i int) []interface{} {
		profiles = append(profiles, struct {
			ContainerID int64
			Name        string
		}{})

		return []interface{}{
			&profiles[i].ContainerID,
			&profiles[i].Name,
		}
	}

	stmt, err = c.tx.Prepare(sql)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	err = query.SelectObjects(stmt, dest, args...)
	if err != nil {
		return nil, errors.Wrap(err, "failed to query containers profiles")
	}

	for _, profile := range profiles {
		id := int(profile.ContainerID)
		index[id].Profiles = append(index[id].Profiles, profile.Name)
	}

	return containers, nil
}

// ContainerConfigInsert inserts a new config for the container with the given ID.
func (c *ClusterTx) ContainerConfigInsert(id int, config map[string]string) error {
	return ContainerConfigInsert(c.tx, id, config)
}

// ContainerRemove removes the container with the given name from the database.
func (c *Cluster) ContainerRemove(name string) error {
	id, err := c.ContainerID(name)
	if err != nil {
		return err
	}

	err = exec(c.db, "DELETE FROM containers WHERE id=?", id)
	if err != nil {
		return err
	}

	return nil
}

// ContainerName returns the name of the container with the given ID.
func (c *Cluster) ContainerName(id int) (string, error) {
	q := "SELECT name FROM containers WHERE id=?"
	name := ""
	arg1 := []interface{}{id}
	arg2 := []interface{}{&name}
	err := dbQueryRowScan(c.db, q, arg1, arg2)
	if err == sql.ErrNoRows {
		return "", ErrNoSuchObject
	}

	return name, err
}

// ContainerID returns the ID of the container with the given name.
func (c *Cluster) ContainerID(name string) (int, error) {
	q := "SELECT id FROM containers WHERE name=?"
	id := -1
	arg1 := []interface{}{name}
	arg2 := []interface{}{&id}
	err := dbQueryRowScan(c.db, q, arg1, arg2)
	if err == sql.ErrNoRows {
		return -1, ErrNoSuchObject
	}

	return id, err
}

// ContainerGet returns the container with the given name.
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
	arg2 := []interface{}{&args.ID, &description, &args.Architecture, &args.Ctype, &ephemInt, &statefulInt, &args.CreationDate, &used, &args.Node, &nodeAddress}
	err := dbQueryRowScan(c.db, q, arg1, arg2)
	if err != nil {
		if err == sql.ErrNoRows {
			return args, ErrNoSuchObject
		}

		return args, err
	}

	args.Description = description.String

	if args.ID == -1 {
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

	config, err := c.ContainerConfig(args.ID)
	if err != nil {
		return args, err
	}
	args.Config = config

	profiles, err := c.ContainerProfiles(args.ID)
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

// ContainerCreate creates a new container and returns its ID.
func (c *Cluster) ContainerCreate(args ContainerArgs) (int, error) {
	_, err := c.ContainerID(args.Name)
	if err == nil {
		return 0, ErrAlreadyDefined
	}

	var id int
	err = c.Transaction(func(tx *ClusterTx) error {
		ephemInt := 0
		if args.Ephemeral == true {
			ephemInt = 1
		}

		statefulInt := 0
		if args.Stateful == true {
			statefulInt = 1
		}

		if args.CreationDate.IsZero() {
			args.CreationDate = time.Now().UTC()
		}

		if args.LastUsedDate.IsZero() {
			args.LastUsedDate = time.Unix(0, 0).UTC()
		}

		str := fmt.Sprintf("INSERT INTO containers (node_id, name, architecture, type, ephemeral, creation_date, last_use_date, stateful) VALUES (?, ?, ?, ?, ?, ?, ?, ?)")
		stmt, err := tx.tx.Prepare(str)
		if err != nil {
			return err
		}
		defer stmt.Close()
		result, err := stmt.Exec(c.nodeID, args.Name, args.Architecture, args.Ctype, ephemInt, args.CreationDate.Unix(), args.LastUsedDate.Unix(), statefulInt)
		if err != nil {
			return err
		}

		id64, err := result.LastInsertId()
		if err != nil {
			return fmt.Errorf("Error inserting %s into database", args.Name)
		}
		// TODO: is this really int64? we should fix it everywhere if so
		id = int(id64)
		if err := ContainerConfigInsert(tx.tx, id, args.Config); err != nil {
			return err
		}

		if err := ContainerProfilesInsert(tx.tx, id, args.Profiles); err != nil {
			return err
		}

		if err := DevicesAdd(tx.tx, "container", int64(id), args.Devices); err != nil {
			return err
		}

		return nil
	})

	return id, err
}

// ContainerConfigClear removes any config associated with the container with
// the given ID.
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

// ContainerConfigInsert inserts a new config for the container with the given ID.
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

// ContainerConfigGet returns the value of the given key in the configuration
// of the container with the given ID.
func (c *Cluster) ContainerConfigGet(id int, key string) (string, error) {
	q := "SELECT value FROM containers_config WHERE container_id=? AND key=?"
	value := ""
	arg1 := []interface{}{id, key}
	arg2 := []interface{}{&value}
	err := dbQueryRowScan(c.db, q, arg1, arg2)
	if err == sql.ErrNoRows {
		return "", ErrNoSuchObject
	}

	return value, err
}

// ContainerConfigRemove removes the given key from the config of the container
// with the given ID.
func (c *Cluster) ContainerConfigRemove(id int, key string) error {
	err := exec(c.db, "DELETE FROM containers_config WHERE key=? AND container_id=?", key, id)
	return err
}

// ContainerSetStateful toggles the stateful flag of the container with the
// given ID.
func (c *Cluster) ContainerSetStateful(id int, stateful bool) error {
	statefulInt := 0
	if stateful {
		statefulInt = 1
	}

	err := exec(c.db, "UPDATE containers SET stateful=? WHERE id=?", statefulInt, id)
	return err
}

// ContainerProfilesInsert associates the container with the given ID with the
// profiles with the given names.
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

// ContainerProfiles returns a list of profiles for a given container ID.
func (c *Cluster) ContainerProfiles(id int) ([]string, error) {
	var name string
	var profiles []string

	query := `
        SELECT name FROM containers_profiles
        JOIN profiles ON containers_profiles.profile_id=profiles.id
		WHERE container_id=?
        ORDER BY containers_profiles.apply_order`
	inargs := []interface{}{id}
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
func (c *Cluster) ContainerConfig(id int) (map[string]string, error) {
	var key, value string
	q := `SELECT key, value FROM containers_config WHERE container_id=?`

	inargs := []interface{}{id}
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

// ContainersList returns the names of all the containers of the given type.
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

// ContainersNodeList returns the names of all the containers of the given type
// running on the local node.
func (c *Cluster) ContainersNodeList(cType ContainerType) ([]string, error) {
	q := fmt.Sprintf("SELECT name FROM containers WHERE type=? AND node_id=? ORDER BY name")
	inargs := []interface{}{cType, c.nodeID}
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

// ContainersResetState resets the power state of all containers.
func (c *Cluster) ContainersResetState() error {
	// Reset all container states
	err := exec(c.db, "DELETE FROM containers_config WHERE key='volatile.last_state.power'")
	return err
}

// ContainerSetState sets the the power state of the container with the given ID.
func (c *Cluster) ContainerSetState(id int, state string) error {
	err := c.Transaction(func(tx *ClusterTx) error {
		// Clear any existing entry
		str := fmt.Sprintf("DELETE FROM containers_config WHERE container_id = ? AND key = 'volatile.last_state.power'")
		stmt, err := tx.tx.Prepare(str)
		if err != nil {
			return err
		}
		defer stmt.Close()

		if _, err := stmt.Exec(id); err != nil {
			return err
		}

		// Insert the new one
		str = fmt.Sprintf("INSERT INTO containers_config (container_id, key, value) VALUES (?, 'volatile.last_state.power', ?)")
		stmt, err = tx.tx.Prepare(str)
		if err != nil {
			return err
		}
		defer stmt.Close()

		if _, err = stmt.Exec(id, state); err != nil {
			return err
		}
		return nil
	})
	return err
}

// ContainerRename renames a container from the given current name to the new
// one.
func (c *Cluster) ContainerRename(oldName string, newName string) error {
	err := c.Transaction(func(tx *ClusterTx) error {
		str := fmt.Sprintf("UPDATE containers SET name = ? WHERE name = ?")
		stmt, err := tx.tx.Prepare(str)
		if err != nil {
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
			return err
		}

		return nil
	})
	return err
}

// ContainerUpdate updates the description, architecture and ephemeral flag of
// the container with the given ID.
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

// ContainerLastUsedUpdate updates the last_use_date field of the container
// with the given ID.
func (c *Cluster) ContainerLastUsedUpdate(id int, date time.Time) error {
	stmt := `UPDATE containers SET last_use_date=? WHERE id=?`
	err := exec(c.db, stmt, date, id)
	return err
}

// ContainerGetSnapshots returns the names of all snapshots of the container
// with the given name.
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

// ContainerNextSnapshot returns the index the next snapshot of the container
// with the given name should have.
//
// Note, the code below doesn't deal with snapshots of snapshots.
// To do that, we'll need to weed out based on # slashes in names
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

// ContainerPool returns the storage pool of a given container.
//
// This is a non-transactional variant of ClusterTx.ContainerPool().
func (c *Cluster) ContainerPool(containerName string) (string, error) {
	var poolName string
	err := c.Transaction(func(tx *ClusterTx) error {
		var err error
		poolName, err = tx.ContainerPool(containerName)
		return err
	})
	return poolName, err
}

// ContainerPool returns the storage pool of a given container.
func (c *ClusterTx) ContainerPool(containerName string) (string, error) {
	// Get container storage volume. Since container names are globally
	// unique, and their storage volumes carry the same name, their storage
	// volumes are unique too.
	poolName := ""
	query := `SELECT storage_pools.name FROM storage_pools
JOIN storage_volumes ON storage_pools.id=storage_volumes.storage_pool_id
WHERE storage_volumes.node_id=? AND storage_volumes.name=? AND storage_volumes.type=?`
	inargs := []interface{}{c.nodeID, containerName, StoragePoolVolumeTypeContainer}
	outargs := []interface{}{&poolName}

	err := c.tx.QueryRow(query, inargs...).Scan(outargs...)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", ErrNoSuchObject
		}

		return "", err
	}

	return poolName, nil
}
