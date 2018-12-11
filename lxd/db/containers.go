package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/lxd/types"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/pkg/errors"

	log "github.com/lxc/lxd/shared/log15"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t containers.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -p db -e container objects
//go:generate mapper stmt -p db -e container objects-by-Type
//go:generate mapper stmt -p db -e container objects-by-Project-and-Type
//go:generate mapper stmt -p db -e container objects-by-Node-and-Type
//go:generate mapper stmt -p db -e container objects-by-Project-and-Node-and-Type
//go:generate mapper stmt -p db -e container objects-by-Project-and-Name
//go:generate mapper stmt -p db -e container objects-by-Project-and-Name-and-Type
//go:generate mapper stmt -p db -e container profiles-ref
//go:generate mapper stmt -p db -e container profiles-ref-by-Project
//go:generate mapper stmt -p db -e container profiles-ref-by-Node
//go:generate mapper stmt -p db -e container profiles-ref-by-Project-and-Node
//go:generate mapper stmt -p db -e container profiles-ref-by-Project-and-Name
//go:generate mapper stmt -p db -e container config-ref
//go:generate mapper stmt -p db -e container config-ref-by-Project
//go:generate mapper stmt -p db -e container config-ref-by-Node
//go:generate mapper stmt -p db -e container config-ref-by-Project-and-Node
//go:generate mapper stmt -p db -e container config-ref-by-Project-and-Name
//go:generate mapper stmt -p db -e container devices-ref
//go:generate mapper stmt -p db -e container devices-ref-by-Project
//go:generate mapper stmt -p db -e container devices-ref-by-Node
//go:generate mapper stmt -p db -e container devices-ref-by-Project-and-Node
//go:generate mapper stmt -p db -e container devices-ref-by-Project-and-Name
//go:generate mapper stmt -p db -e container id
//go:generate mapper stmt -p db -e container create struct=Container
//go:generate mapper stmt -p db -e container create-config-ref
//go:generate mapper stmt -p db -e container create-devices-ref
//go:generate mapper stmt -p db -e container rename
//go:generate mapper stmt -p db -e container delete
//
//go:generate mapper method -p db -e container List
//go:generate mapper method -p db -e container Get
//go:generate mapper method -p db -e container ID struct=Container
//go:generate mapper method -p db -e container Exists struct=Container
//go:generate mapper method -p db -e container Create struct=Container
//go:generate mapper method -p db -e container ProfilesRef
//go:generate mapper method -p db -e container ConfigRef
//go:generate mapper method -p db -e container DevicesRef
//go:generate mapper method -p db -e container Rename
//go:generate mapper method -p db -e container Delete

// Container is a value object holding db-related details about a container.
type Container struct {
	ID           int
	Project      string `db:"primary=yes&join=projects.name"`
	Name         string `db:"primary=yes"`
	Node         string `db:"join=nodes.name"`
	Type         int
	Architecture int
	Ephemeral    bool
	CreationDate time.Time
	Stateful     bool
	LastUseDate  time.Time
	Description  string `db:"coalesce=''"`
	Config       map[string]string
	Devices      map[string]map[string]string
	Profiles     []string
}

// ContainerFilter can be used to filter results yielded by ContainerList.
type ContainerFilter struct {
	Project string
	Name    string
	Node    string
	Type    int
}

// ContainerToArgs is a convenience to convert the new Container db struct into
// the legacy ContainerArgs.
func ContainerToArgs(container *Container) ContainerArgs {
	args := ContainerArgs{
		ID:           container.ID,
		Project:      container.Project,
		Name:         container.Name,
		Node:         container.Node,
		Ctype:        ContainerType(container.Type),
		Architecture: container.Architecture,
		Ephemeral:    container.Ephemeral,
		CreationDate: container.CreationDate,
		Stateful:     container.Stateful,
		LastUsedDate: container.LastUseDate,
		Description:  container.Description,
		Config:       container.Config,
		Devices:      container.Devices,
		Profiles:     container.Profiles,
	}

	if args.Devices == nil {
		args.Devices = types.Devices{}
	}

	return args
}

// ContainerArgs is a value object holding all db-related details about a
// container.
type ContainerArgs struct {
	// Don't set manually
	ID    int
	Node  string
	Ctype ContainerType

	// Creation only
	Project      string
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

// ContainerBackupArgs is a value object holding all db-related details
// about a backup.
type ContainerBackupArgs struct {
	// Don't set manually
	ID int

	ContainerID      int
	Name             string
	CreationDate     time.Time
	ExpiryDate       time.Time
	ContainerOnly    bool
	OptimizedStorage bool
}

// ContainerType encodes the type of container (either regular or snapshot).
type ContainerType int

// Numerical codes for container types.
const (
	CTypeRegular  ContainerType = 0
	CTypeSnapshot ContainerType = 1
)

// ContainerNames returns the names of all containers the given project.
func (c *ClusterTx) ContainerNames(project string) ([]string, error) {
	stmt := `
SELECT containers.name FROM containers
  JOIN projects ON projects.id = containers.project_id
  WHERE projects.name = ? AND containers.type = ?
`
	return query.SelectStrings(c.tx, stmt, project, CTypeRegular)
}

// ContainerNodeAddress returns the address of the node hosting the container
// with the given name in the given project.
//
// It returns the empty string if the container is hosted on this node.
func (c *ClusterTx) ContainerNodeAddress(project string, name string) (string, error) {
	stmt := `
SELECT nodes.id, nodes.address
  FROM nodes
  JOIN containers ON containers.node_id = nodes.id
  JOIN projects ON projects.id = containers.project_id
 WHERE projects.name = ? AND containers.name = ?
`
	var address string
	var id int64
	rows, err := c.tx.Query(stmt, project, name)
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
func (c *ClusterTx) ContainersListByNodeAddress(project string) (map[string][]string, error) {
	offlineThreshold, err := c.NodeOfflineThreshold()
	if err != nil {
		return nil, err
	}

	stmt := `
SELECT containers.name, nodes.id, nodes.address, nodes.heartbeat
  FROM containers
  JOIN nodes ON nodes.id = containers.node_id
  JOIN projects ON projects.id = containers.project_id
  WHERE containers.type=?
    AND projects.name = ?
  ORDER BY containers.id
`
	rows, err := c.tx.Query(stmt, CTypeRegular, project)
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

// ContainerListExpanded loads all containers across all projects and expands
// their config and devices using the profiles they are associated to.
func (c *ClusterTx) ContainerListExpanded() ([]Container, error) {
	containers, err := c.ContainerList(ContainerFilter{})
	if err != nil {
		return nil, errors.Wrap(err, "Load containers")
	}

	profiles, err := c.ProfileList(ProfileFilter{})
	if err != nil {
		return nil, errors.Wrap(err, "Load profiles")
	}

	// Index of all profiles by project and name.
	profilesByProjectAndName := map[string]map[string]Profile{}
	for _, profile := range profiles {
		profilesByName, ok := profilesByProjectAndName[profile.Project]
		if !ok {
			profilesByName = map[string]Profile{}
			profilesByProjectAndName[profile.Project] = profilesByName
		}
		profilesByName[profile.Name] = profile
	}

	for i, container := range containers {
		profiles := make([]api.Profile, len(container.Profiles))
		for j, name := range container.Profiles {
			profile := profilesByProjectAndName[container.Project][name]
			profiles[j] = *ProfileToAPI(&profile)
		}

		containers[i].Config = ProfilesExpandConfig(container.Config, profiles)
		containers[i].Devices = ProfilesExpandDevices(container.Devices, profiles)
	}

	return containers, nil
}

// ContainersByNodeName returns a map associating each container to the name of
// its node.
func (c *ClusterTx) ContainersByNodeName(project string) (map[string]string, error) {
	stmt := `
SELECT containers.name, nodes.name
  FROM containers
  JOIN nodes ON nodes.id = containers.node_id
  JOIN projects ON projects.id = containers.project_id
  WHERE containers.type=?
    AND projects.name = ?
`
	rows, err := c.tx.Query(stmt, CTypeRegular, project)
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
	poolName, err := c.ContainerPool("default", oldName)
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
	containerID, err := c.ContainerID("default", oldName)
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

// ContainerNodeList returns all container objects on the local node.
func (c *ClusterTx) ContainerNodeList() ([]Container, error) {
	node, err := c.NodeName()
	if err != nil {
		return nil, errors.Wrap(err, "Local node name")
	}
	filter := ContainerFilter{
		Node: node,
		Type: int(CTypeRegular),
	}

	return c.ContainerList(filter)
}

// ContainerNodeProjectList returns all container objects on the local node within the given project.
func (c *ClusterTx) ContainerNodeProjectList(project string) ([]Container, error) {
	node, err := c.NodeName()
	if err != nil {
		return nil, errors.Wrap(err, "Local node name")
	}
	filter := ContainerFilter{
		Project: project,
		Node:    node,
		Type:    int(CTypeRegular),
	}

	return c.ContainerList(filter)
}

// ContainerConfigInsert inserts a new config for the container with the given ID.
func (c *ClusterTx) ContainerConfigInsert(id int, config map[string]string) error {
	return ContainerConfigInsert(c.tx, id, config)
}

// ContainerRemove removes the container with the given name from the database.
func (c *Cluster) ContainerRemove(project, name string) error {
	return c.Transaction(func(tx *ClusterTx) error {
		return tx.ContainerDelete(project, name)
	})
}

// ContainerProjectAndName returns the project and the name of the container
// with the given ID.
func (c *Cluster) ContainerProjectAndName(id int) (string, string, error) {
	q := `
SELECT projects.name, containers.name
  FROM containers
  JOIN projects ON projects.id = containers.project_id
WHERE containers.id=?
`
	project := ""
	name := ""
	arg1 := []interface{}{id}
	arg2 := []interface{}{&project, &name}
	err := dbQueryRowScan(c.db, q, arg1, arg2)
	if err == sql.ErrNoRows {
		return "", "", ErrNoSuchObject
	}

	return project, name, err
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
// profiles with the given names in the given project.
func ContainerProfilesInsert(tx *sql.Tx, id int, project string, profiles []string) error {
	enabled, err := projectHasProfiles(tx, project)
	if err != nil {
		return errors.Wrap(err, "Check if project has profiles")
	}
	if !enabled {
		project = "default"
	}

	applyOrder := 1
	str := `
INSERT INTO containers_profiles (container_id, profile_id, apply_order)
  VALUES (
    ?,
    (SELECT profiles.id
     FROM profiles
     JOIN projects ON projects.id=profiles.project_id
     WHERE projects.name=? AND profiles.name=?),
    ?
  )
`
	stmt, err := tx.Prepare(str)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, profile := range profiles {
		_, err = stmt.Exec(id, project, profile, applyOrder)
		if err != nil {
			logger.Debugf("Error adding profile %s to container: %s",
				profile, err)
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

// LegacyContainersList returns the names of all the containers of the given type.
//
// NOTE: this is a pre-projects legacy API that is used only by patches. Don't
// use it for new code.
func (c *Cluster) LegacyContainersList(cType ContainerType) ([]string, error) {
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
		// Set the new value
		str := fmt.Sprintf("INSERT OR REPLACE INTO containers_config (container_id, key, value) VALUES (?, 'volatile.last_state.power', ?)")
		stmt, err := tx.tx.Prepare(str)
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

// ContainerCreationUpdate updates the cration_date field of the container
// with the given ID.
func (c *Cluster) ContainerCreationUpdate(id int, date time.Time) error {
	stmt := `UPDATE containers SET creation_date=? WHERE id=?`
	err := exec(c.db, stmt, date, id)
	return err
}

// ContainerLastUsedUpdate updates the last_use_date field of the container
// with the given ID.
func (c *Cluster) ContainerLastUsedUpdate(id int, date time.Time) error {
	stmt := `UPDATE containers SET last_use_date=? WHERE id=?`
	err := exec(c.db, stmt, date, id)
	return err
}

// ContainerGetSnapshots returns the names of all snapshots of the container
// in the given project with the given name.
func (c *Cluster) ContainerGetSnapshots(project, name string) ([]string, error) {
	result := []string{}

	regexp := name + shared.SnapshotDelimiter
	length := len(regexp)
	q := `
SELECT containers.name
  FROM containers
  JOIN projects ON projects.id = containers.project_id
WHERE projects.name=? AND containers.type=? AND SUBSTR(containers.name,1,?)=?
`
	inargs := []interface{}{project, CTypeSnapshot, length, regexp}
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
// with the given name and pattern should have.
func (c *Cluster) ContainerNextSnapshot(project string, name string, pattern string) int {
	base := name + shared.SnapshotDelimiter
	length := len(base)
	q := `
SELECT containers.name
  FROM containers
  JOIN projects ON projects.id = containers.project_id
 WHERE projects.name=? AND containers.type=? AND SUBSTR(containers.name,1,?)=?`
	var numstr string
	inargs := []interface{}{project, CTypeSnapshot, length, base}
	outfmt := []interface{}{numstr}
	results, err := queryScan(c.db, q, inargs, outfmt)
	if err != nil {
		return 0
	}
	max := 0

	for _, r := range results {
		snapOnlyName := strings.SplitN(r[0].(string), shared.SnapshotDelimiter, 2)[1]
		fields := strings.SplitN(pattern, "%d", 2)

		var num int
		count, err := fmt.Sscanf(snapOnlyName, fmt.Sprintf("%s%%d%s", fields[0], fields[1]), &num)
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
func (c *Cluster) ContainerPool(project, containerName string) (string, error) {
	var poolName string
	err := c.Transaction(func(tx *ClusterTx) error {
		var err error
		poolName, err = tx.ContainerPool(project, containerName)
		return err
	})
	return poolName, err
}

// ContainerPool returns the storage pool of a given container.
func (c *ClusterTx) ContainerPool(project, containerName string) (string, error) {
	// Get container storage volume. Since container names are globally
	// unique, and their storage volumes carry the same name, their storage
	// volumes are unique too.
	poolName := ""
	query := `
SELECT storage_pools.name FROM storage_pools
  JOIN storage_volumes ON storage_pools.id=storage_volumes.storage_pool_id
  JOIN containers ON containers.name=storage_volumes.name
  JOIN projects ON projects.id=containers.project_id
 WHERE projects.name=? AND storage_volumes.node_id=? AND storage_volumes.name=? AND storage_volumes.type=?
`
	inargs := []interface{}{project, c.nodeID, containerName, StoragePoolVolumeTypeContainer}
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

// ContainerBackupID returns the ID of the container backup with the given name.
func (c *Cluster) ContainerBackupID(name string) (int, error) {
	q := "SELECT id FROM containers_backups WHERE name=?"
	id := -1
	arg1 := []interface{}{name}
	arg2 := []interface{}{&id}
	err := dbQueryRowScan(c.db, q, arg1, arg2)
	if err == sql.ErrNoRows {
		return -1, ErrNoSuchObject
	}

	return id, err
}

// ContainerGetBackup returns the backup with the given name.
func (c *Cluster) ContainerGetBackup(project, name string) (ContainerBackupArgs, error) {
	args := ContainerBackupArgs{}
	args.Name = name

	containerOnlyInt := -1
	optimizedStorageInt := -1
	q := `
SELECT containers_backups.id, containers_backups.container_id,
       containers_backups.creation_date, containers_backups.expiry_date,
       containers_backups.container_only, containers_backups.optimized_storage
    FROM containers_backups
    JOIN containers ON containers.id=containers_backups.container_id
    JOIN projects ON projects.id=containers.project_id
    WHERE projects.name=? AND containers_backups.name=?
`
	arg1 := []interface{}{project, name}
	arg2 := []interface{}{&args.ID, &args.ContainerID, &args.CreationDate,
		&args.ExpiryDate, &containerOnlyInt, &optimizedStorageInt}
	err := dbQueryRowScan(c.db, q, arg1, arg2)
	if err != nil {
		if err == sql.ErrNoRows {
			return args, ErrNoSuchObject
		}

		return args, err
	}

	if containerOnlyInt == 1 {
		args.ContainerOnly = true
	}

	if optimizedStorageInt == 1 {
		args.OptimizedStorage = true
	}

	return args, nil
}

// ContainerGetBackups returns the names of all backups of the container
// with the given name.
func (c *Cluster) ContainerGetBackups(project, name string) ([]string, error) {
	var result []string

	q := `SELECT containers_backups.name FROM containers_backups
JOIN containers ON containers_backups.container_id=containers.id
JOIN projects ON projects.id=containers.project_id
WHERE projects.name=? AND containers.name=?`
	inargs := []interface{}{project, name}
	outfmt := []interface{}{name}
	dbResults, err := queryScan(c.db, q, inargs, outfmt)
	if err != nil {
		return nil, err
	}

	for _, r := range dbResults {
		result = append(result, r[0].(string))
	}

	return result, nil
}

// ContainerBackupCreate creates a new backup
func (c *Cluster) ContainerBackupCreate(args ContainerBackupArgs) error {
	_, err := c.ContainerBackupID(args.Name)
	if err == nil {
		return ErrAlreadyDefined
	}

	err = c.Transaction(func(tx *ClusterTx) error {
		containerOnlyInt := 0
		if args.ContainerOnly {
			containerOnlyInt = 1
		}

		optimizedStorageInt := 0
		if args.OptimizedStorage {
			optimizedStorageInt = 1
		}

		str := fmt.Sprintf("INSERT INTO containers_backups (container_id, name, creation_date, expiry_date, container_only, optimized_storage) VALUES (?, ?, ?, ?, ?, ?)")
		stmt, err := tx.tx.Prepare(str)
		if err != nil {
			return err
		}
		defer stmt.Close()
		result, err := stmt.Exec(args.ContainerID, args.Name,
			args.CreationDate.Unix(), args.ExpiryDate.Unix(), containerOnlyInt,
			optimizedStorageInt)
		if err != nil {
			return err
		}

		_, err = result.LastInsertId()
		if err != nil {
			return fmt.Errorf("Error inserting %s into database", args.Name)
		}

		return nil
	})

	return err
}

// ContainerBackupRemove removes the container backup with the given name from
// the database.
func (c *Cluster) ContainerBackupRemove(name string) error {
	id, err := c.ContainerBackupID(name)
	if err != nil {
		return err
	}

	err = exec(c.db, "DELETE FROM containers_backups WHERE id=?", id)
	if err != nil {
		return err
	}

	return nil
}

// ContainerBackupRename renames a container backup from the given current name
// to the new one.
func (c *Cluster) ContainerBackupRename(oldName, newName string) error {
	err := c.Transaction(func(tx *ClusterTx) error {
		str := fmt.Sprintf("UPDATE containers_backups SET name = ? WHERE name = ?")
		stmt, err := tx.tx.Prepare(str)
		if err != nil {
			return err
		}
		defer stmt.Close()

		logger.Debug(
			"Calling SQL Query",
			log.Ctx{
				"query":   "UPDATE containers_backups SET name = ? WHERE name = ?",
				"oldName": oldName,
				"newName": newName})
		if _, err := stmt.Exec(newName, oldName); err != nil {
			return err
		}

		return nil
	})
	return err
}

// ContainerBackupsGetExpired returns a list of expired container backups.
func (c *Cluster) ContainerBackupsGetExpired() ([]string, error) {
	var result []string
	var name string
	var expiryDate string

	q := `SELECT containers_backups.name, containers_backups.expiry_date FROM containers_backups`
	outfmt := []interface{}{name, expiryDate}
	dbResults, err := queryScan(c.db, q, nil, outfmt)
	if err != nil {
		return nil, err
	}

	for _, r := range dbResults {
		timestamp := r[1]

		var backupExpiry time.Time
		err = backupExpiry.UnmarshalText([]byte(timestamp.(string)))
		if err != nil {
			return []string{}, err
		}

		if backupExpiry.IsZero() {
			// Backup doesn't expire
			continue
		}

		// Backup has expired
		if time.Now().Unix()-backupExpiry.Unix() >= 0 {
			result = append(result, r[0].(string))
		}
	}

	return result, nil
}
