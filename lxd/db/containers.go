package db

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/lxd/device/config"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/pkg/errors"

	log "github.com/lxc/lxd/shared/log15"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t instances.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -p db -e instance objects
//go:generate mapper stmt -p db -e instance objects-by-Type
//go:generate mapper stmt -p db -e instance objects-by-Project-and-Type
//go:generate mapper stmt -p db -e instance objects-by-Node-and-Type
//go:generate mapper stmt -p db -e instance objects-by-Project-and-Node-and-Type
//go:generate mapper stmt -p db -e instance objects-by-Project-and-Name
//go:generate mapper stmt -p db -e instance objects-by-Project-and-Name-and-Type
//go:generate mapper stmt -p db -e instance profiles-ref
//go:generate mapper stmt -p db -e instance profiles-ref-by-Project
//go:generate mapper stmt -p db -e instance profiles-ref-by-Node
//go:generate mapper stmt -p db -e instance profiles-ref-by-Project-and-Node
//go:generate mapper stmt -p db -e instance profiles-ref-by-Project-and-Name
//go:generate mapper stmt -p db -e instance config-ref
//go:generate mapper stmt -p db -e instance config-ref-by-Project
//go:generate mapper stmt -p db -e instance config-ref-by-Node
//go:generate mapper stmt -p db -e instance config-ref-by-Project-and-Node
//go:generate mapper stmt -p db -e instance config-ref-by-Project-and-Name
//go:generate mapper stmt -p db -e instance devices-ref
//go:generate mapper stmt -p db -e instance devices-ref-by-Project
//go:generate mapper stmt -p db -e instance devices-ref-by-Node
//go:generate mapper stmt -p db -e instance devices-ref-by-Project-and-Node
//go:generate mapper stmt -p db -e instance devices-ref-by-Project-and-Name
//go:generate mapper stmt -p db -e instance id
//go:generate mapper stmt -p db -e instance create struct=Instance
//go:generate mapper stmt -p db -e instance create-config-ref
//go:generate mapper stmt -p db -e instance create-devices-ref
//go:generate mapper stmt -p db -e instance rename
//go:generate mapper stmt -p db -e instance delete
//
//go:generate mapper method -p db -e instance List
//go:generate mapper method -p db -e instance Get
//go:generate mapper method -p db -e instance ID struct=Instance
//go:generate mapper method -p db -e instance Exists struct=Instance
//go:generate mapper method -p db -e instance Create struct=Instance
//go:generate mapper method -p db -e instance ProfilesRef
//go:generate mapper method -p db -e instance ConfigRef
//go:generate mapper method -p db -e instance DevicesRef
//go:generate mapper method -p db -e instance Rename
//go:generate mapper method -p db -e instance Delete

// Instance is a value object holding db-related details about a container.
type Instance struct {
	ID           int
	Project      string `db:"primary=yes&join=projects.name"`
	Name         string `db:"primary=yes"`
	Node         string `db:"join=nodes.name"`
	Type         instancetype.Type
	Snapshot     bool `db:"ignore"`
	Architecture int
	Ephemeral    bool
	CreationDate time.Time
	Stateful     bool
	LastUseDate  time.Time
	Description  string `db:"coalesce=''"`
	Config       map[string]string
	Devices      map[string]map[string]string
	Profiles     []string
	ExpiryDate   time.Time
}

// InstanceFilter can be used to filter results yielded by InstanceList.
type InstanceFilter struct {
	Project string
	Name    string
	Node    string
	Type    instancetype.Type
}

// ContainerToArgs is a convenience to convert the new Container db struct into
// the legacy InstanceArgs.
func ContainerToArgs(container *Instance) InstanceArgs {
	args := InstanceArgs{
		ID:           container.ID,
		Project:      container.Project,
		Name:         container.Name,
		Node:         container.Node,
		Type:         container.Type,
		Snapshot:     container.Snapshot,
		Architecture: container.Architecture,
		Ephemeral:    container.Ephemeral,
		CreationDate: container.CreationDate,
		Stateful:     container.Stateful,
		LastUsedDate: container.LastUseDate,
		Description:  container.Description,
		Config:       container.Config,
		Devices:      deviceConfig.NewDevices(container.Devices),
		Profiles:     container.Profiles,
		ExpiryDate:   container.ExpiryDate,
	}

	if args.Devices == nil {
		args.Devices = config.Devices{}
	}

	return args
}

// InstanceArgs is a value object holding all db-related details about an instance.
type InstanceArgs struct {
	// Don't set manually
	ID       int
	Node     string
	Type     instancetype.Type
	Snapshot bool

	// Creation only
	Project      string
	BaseImage    string
	CreationDate time.Time

	Architecture int
	Config       map[string]string
	Description  string
	Devices      config.Devices
	Ephemeral    bool
	LastUsedDate time.Time
	Name         string
	Profiles     []string
	Stateful     bool
	ExpiryDate   time.Time
}

// InstanceBackupArgs is a value object holding all db-related details about a backup.
type InstanceBackupArgs struct {
	// Don't set manually
	ID int

	InstanceID       int
	Name             string
	CreationDate     time.Time
	ExpiryDate       time.Time
	InstanceOnly     bool
	OptimizedStorage bool
}

// ContainerNames returns the names of all containers the given project.
func (c *ClusterTx) ContainerNames(project string) ([]string, error) {
	stmt := `
SELECT instances.name FROM instances
  JOIN projects ON projects.id = instances.project_id
  WHERE projects.name = ? AND instances.type = ?
`
	return query.SelectStrings(c.tx, stmt, project, instancetype.Container)
}

// ContainerNodeAddress returns the address of the node hosting the container
// with the given name in the given project.
//
// It returns the empty string if the container is hosted on this node.
func (c *ClusterTx) ContainerNodeAddress(project string, name string, instanceType instancetype.Type) (string, error) {
	var stmt string

	args := make([]interface{}, 0, 4) // Expect up to 4 filters.
	var filters strings.Builder

	// Project filter.
	filters.WriteString("projects.name = ?")
	args = append(args, project)

	// Instance type filter.
	if instanceType != instancetype.Any {
		filters.WriteString(" AND instances.type = ?")
		args = append(args, instanceType)
	}

	if strings.Contains(name, shared.SnapshotDelimiter) {
		parts := strings.SplitN(name, shared.SnapshotDelimiter, 2)

		// Instance name filter.
		filters.WriteString(" AND instances.name = ?")
		args = append(args, parts[0])

		// Snapshot name filter.
		filters.WriteString(" AND instances_snapshots.name = ?")
		args = append(args, parts[1])

		stmt = fmt.Sprintf(`
SELECT nodes.id, nodes.address
  FROM nodes
  JOIN instances ON instances.node_id = nodes.id
  JOIN projects ON projects.id = instances.project_id
  JOIN instances_snapshots ON instances_snapshots.instance_id = instances.id
 WHERE %s
`, filters.String())
	} else {
		// Instance name filter.
		filters.WriteString(" AND instances.name = ?")
		args = append(args, name)

		stmt = fmt.Sprintf(`
SELECT nodes.id, nodes.address
  FROM nodes
  JOIN instances ON instances.node_id = nodes.id
  JOIN projects ON projects.id = instances.project_id
 WHERE %s
`, filters.String())
	}

	var address string
	var id int64
	rows, err := c.tx.Query(stmt, args...)
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
func (c *ClusterTx) ContainersListByNodeAddress(project string, instanceType instancetype.Type) (map[string][]string, error) {
	offlineThreshold, err := c.NodeOfflineThreshold()
	if err != nil {
		return nil, err
	}

	args := make([]interface{}, 0, 2) // Expect up to 2 filters.
	var filters strings.Builder

	// Project filter.
	filters.WriteString("projects.name = ?")
	args = append(args, project)

	// Instance type filter.
	if instanceType != instancetype.Any {
		filters.WriteString(" AND instances.type = ?")
		args = append(args, instanceType)
	}

	stmt := fmt.Sprintf(`
SELECT instances.name, nodes.id, nodes.address, nodes.heartbeat
  FROM instances
  JOIN nodes ON nodes.id = instances.node_id
  JOIN projects ON projects.id = instances.project_id
  WHERE %s
  ORDER BY instances.id
`, filters.String())

	rows, err := c.tx.Query(stmt, args...)
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
func (c *ClusterTx) ContainerListExpanded() ([]Instance, error) {
	instances, err := c.InstanceList(InstanceFilter{})
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

	for i, instance := range instances {
		profiles := make([]api.Profile, len(instance.Profiles))
		for j, name := range instance.Profiles {
			profile := profilesByProjectAndName[instance.Project][name]
			profiles[j] = *ProfileToAPI(&profile)
		}

		instances[i].Config = ProfilesExpandConfig(instance.Config, profiles)
		instances[i].Devices = ProfilesExpandDevices(deviceConfig.NewDevices(instance.Devices), profiles).CloneNative()
	}

	return instances, nil
}

// ContainersByNodeName returns a map associating each container to the name of
// its node.
func (c *ClusterTx) ContainersByNodeName(project string, instanceType instancetype.Type) (map[string]string, error) {
	args := make([]interface{}, 0, 2) // Expect up to 2 filters.
	var filters strings.Builder

	// Project filter.
	filters.WriteString("projects.name = ?")
	args = append(args, project)

	// Instance type filter.
	if instanceType != instancetype.Any {
		filters.WriteString(" AND instances.type = ?")
		args = append(args, instanceType)
	}

	stmt := fmt.Sprintf(`
SELECT instances.name, nodes.name
  FROM instances
  JOIN nodes ON nodes.id = instances.node_id
  JOIN projects ON projects.id = instances.project_id
  WHERE %s
`, filters.String())

	rows, err := c.tx.Query(stmt, args...)
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

// Returns a map of snapshot IDs to snapshot names for the
// container with the given name.
func (c *ClusterTx) snapshotIDsAndNames(project, name string) (map[int]string, error) {
	filter := InstanceSnapshotFilter{
		Project:  project,
		Instance: name,
	}
	objects, err := c.InstanceSnapshotList(filter)
	if err != nil {
		return nil, err
	}

	result := make(map[int]string)
	for i := range objects {
		result[objects[i].ID] = objects[i].Name
	}
	return result, nil
}

// ContainerNodeMove changes the node associated with a container.
//
// It's meant to be used when moving a non-running container backed by ceph
// from one cluster node to another.
func (c *ClusterTx) ContainerNodeMove(project, oldName, newName, newNode string) error {
	// First check that the container to be moved is backed by a ceph
	// volume.
	poolName, err := c.ContainerPool(project, oldName)
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
	containerID, err := c.InstanceID(project, oldName)
	if err != nil {
		return errors.Wrap(err, "failed to get container's ID")
	}
	snapshots, err := c.snapshotIDsAndNames(project, oldName)
	if err != nil {
		return errors.Wrap(err, "failed to get container's snapshots")
	}
	node, err := c.NodeByName(newNode)
	if err != nil {
		return errors.Wrap(err, "failed to get new node's info")
	}
	stmt := "UPDATE instances SET node_id=?, name=? WHERE id=?"
	result, err := c.tx.Exec(stmt, node.ID, newName, containerID)
	if err != nil {
		return errors.Wrap(err, "failed to update container's name and node ID")
	}
	n, err := result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "failed to get rows affected by container update")
	}
	if n != 1 {
		return fmt.Errorf("unexpected number of updated rows in instances table: %d", n)
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
func (c *ClusterTx) ContainerNodeList() ([]Instance, error) {
	node, err := c.NodeName()
	if err != nil {
		return nil, errors.Wrap(err, "Local node name")
	}
	filter := InstanceFilter{
		Node: node,
		Type: instancetype.Container,
	}

	return c.InstanceList(filter)
}

// ContainerNodeProjectList returns all container objects on the local node within the given project.
func (c *ClusterTx) ContainerNodeProjectList(project string, instanceType instancetype.Type) ([]Instance, error) {
	node, err := c.NodeName()
	if err != nil {
		return nil, errors.Wrap(err, "Local node name")
	}
	filter := InstanceFilter{
		Project: project,
		Node:    node,
		Type:    instanceType,
	}

	return c.InstanceList(filter)
}

// ContainerConfigInsert inserts a new config for the container with the given ID.
func (c *ClusterTx) ContainerConfigInsert(id int, config map[string]string) error {
	return ContainerConfigInsert(c.tx, id, config)
}

// ContainerConfigUpdate inserts/updates/deletes the provided keys
func (c *ClusterTx) ContainerConfigUpdate(id int, values map[string]string) error {
	insertSQL := fmt.Sprintf("INSERT OR REPLACE INTO instances_config (instance_id, key, value) VALUES")
	deleteSQL := "DELETE FROM instances_config WHERE key IN %s AND instance_id=?"
	return c.configUpdate(id, values, insertSQL, deleteSQL)
}

func (c *ClusterTx) configUpdate(id int, values map[string]string, insertSQL, deleteSQL string) error {
	changes := map[string]string{}
	deletes := []string{}

	// Figure out which key to set/unset
	for key, value := range values {
		if value == "" {
			deletes = append(deletes, key)
			continue
		}
		changes[key] = value
	}

	// Insert/update keys
	if len(changes) > 0 {
		query := insertSQL
		exprs := []string{}
		params := []interface{}{}
		for key, value := range changes {
			exprs = append(exprs, "(?, ?, ?)")
			params = append(params, []interface{}{id, key, value}...)
		}

		query += strings.Join(exprs, ",")
		_, err := c.tx.Exec(query, params...)
		if err != nil {
			return err
		}
	}

	// Delete keys
	if len(deletes) > 0 {
		query := fmt.Sprintf(deleteSQL, query.Params(len(deletes)))
		params := []interface{}{}
		for _, key := range deletes {
			params = append(params, key)
		}

		params = append(params, id)
		_, err := c.tx.Exec(query, params...)
		if err != nil {
			return err
		}
	}

	return nil
}

// ContainerRemove removes the container with the given name from the database.
func (c *Cluster) ContainerRemove(project, name string) error {
	if strings.Contains(name, shared.SnapshotDelimiter) {
		parts := strings.SplitN(name, shared.SnapshotDelimiter, 2)
		return c.Transaction(func(tx *ClusterTx) error {
			return tx.InstanceSnapshotDelete(project, parts[0], parts[1])
		})
	}
	return c.Transaction(func(tx *ClusterTx) error {
		return tx.InstanceDelete(project, name)
	})
}

// ContainerProjectAndName returns the project and the name of the container
// with the given ID.
func (c *Cluster) ContainerProjectAndName(id int) (string, string, error) {
	q := `
SELECT projects.name, instances.name
  FROM instances
  JOIN projects ON projects.id = instances.project_id
WHERE instances.id=?
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
func (c *Cluster) ContainerID(project, name string) (int, error) {
	var id int64
	err := c.Transaction(func(tx *ClusterTx) error {
		var err error
		id, err = tx.InstanceID(project, name)
		return err
	})
	return int(id), err
}

// ContainerConfigClear removes any config associated with the container with
// the given ID.
func ContainerConfigClear(tx *sql.Tx, id int) error {
	_, err := tx.Exec("DELETE FROM instances_config WHERE instance_id=?", id)
	if err != nil {
		return err
	}
	_, err = tx.Exec("DELETE FROM instances_profiles WHERE instance_id=?", id)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`DELETE FROM instances_devices_config WHERE id IN
		(SELECT instances_devices_config.id
		 FROM instances_devices_config JOIN instances_devices
		 ON instances_devices_config.instance_device_id=instances_devices.id
		 WHERE instances_devices.instance_id=?)`, id)
	if err != nil {
		return err
	}
	_, err = tx.Exec("DELETE FROM instances_devices WHERE instance_id=?", id)
	return err
}

// ContainerConfigInsert inserts a new config for the container with the given ID.
func ContainerConfigInsert(tx *sql.Tx, id int, config map[string]string) error {
	stmt, err := tx.Prepare("INSERT INTO instances_config (instance_id, key, value) values (?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for k, v := range config {
		if v == "" {
			continue
		}

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
	q := "SELECT value FROM instances_config WHERE instance_id=? AND key=?"
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
	err := exec(c.db, "DELETE FROM instances_config WHERE key=? AND instance_id=?", key, id)
	return err
}

// ContainerSetStateful toggles the stateful flag of the container with the
// given ID.
func (c *Cluster) ContainerSetStateful(id int, stateful bool) error {
	statefulInt := 0
	if stateful {
		statefulInt = 1
	}

	err := exec(c.db, "UPDATE instances SET stateful=? WHERE id=?", statefulInt, id)
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
INSERT INTO instances_profiles (instance_id, profile_id, apply_order)
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
        SELECT name FROM instances_profiles
        JOIN profiles ON instances_profiles.profile_id=profiles.id
		WHERE instance_id=?
        ORDER BY instances_profiles.apply_order`
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
	q := `SELECT key, value FROM instances_config WHERE instance_id=?`

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

// LegacyContainersList returns the names of all the containers.
//
// NOTE: this is a pre-projects legacy API that is used only by patches. Don't
// use it for new code.
func (c *Cluster) LegacyContainersList() ([]string, error) {
	q := fmt.Sprintf("SELECT name FROM instances WHERE type=? ORDER BY name")
	inargs := []interface{}{instancetype.Container}
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

// LegacySnapshotsList returns the names of all the snapshots.
//
// NOTE: this is a pre-projects legacy API that is used only by patches. Don't
// use it for new code.
func (c *Cluster) LegacySnapshotsList() ([]string, error) {
	q := fmt.Sprintf(`
SELECT instances.name, instances_snapshots.name
FROM instances_snapshots
JOIN instances ON instances.id = instances_snapshots.instance_id
WHERE type=? ORDER BY instances.name, instances_snapshots.name
`)
	inargs := []interface{}{instancetype.Container}
	var container string
	var snapshot string
	outfmt := []interface{}{container, snapshot}
	result, err := queryScan(c.db, q, inargs, outfmt)
	if err != nil {
		return nil, err
	}

	var ret []string
	for _, r := range result {
		ret = append(ret, r[0].(string)+shared.SnapshotDelimiter+r[1].(string))
	}

	return ret, nil
}

// ContainersNodeList returns the names of all the containers of the given type
// running on the local node.
func (c *Cluster) ContainersNodeList(instanceType instancetype.Type) ([]string, error) {
	q := fmt.Sprintf("SELECT name FROM instances WHERE type=? AND node_id=? ORDER BY name")
	inargs := []interface{}{instanceType, c.nodeID}
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
	err := exec(c.db, "DELETE FROM instances_config WHERE key='volatile.last_state.power'")
	return err
}

// ContainerSetState sets the the power state of the container with the given ID.
func (c *Cluster) ContainerSetState(id int, state string) error {
	err := c.Transaction(func(tx *ClusterTx) error {
		return tx.ContainerSetState(id, state)
	})
	return err
}

// ContainerSetState sets the the power state of the container with the given ID.
func (c *ClusterTx) ContainerSetState(id int, state string) error {
	// Set the new value
	str := fmt.Sprintf("INSERT OR REPLACE INTO instances_config (instance_id, key, value) VALUES (?, 'volatile.last_state.power', ?)")
	stmt, err := c.tx.Prepare(str)
	if err != nil {
		return err
	}
	defer stmt.Close()

	_, err = stmt.Exec(id, state)
	if err != nil {
		return err
	}

	return nil
}

// ContainerUpdate updates the description, architecture and ephemeral flag of
// the container with the given ID.
func ContainerUpdate(tx *sql.Tx, id int, description string, architecture int, ephemeral bool,
	expiryDate time.Time) error {
	str := fmt.Sprintf("UPDATE instances SET description=?, architecture=?, ephemeral=?, expiry_date=? WHERE id=?")
	stmt, err := tx.Prepare(str)
	if err != nil {
		return err
	}
	defer stmt.Close()

	ephemeralInt := 0
	if ephemeral {
		ephemeralInt = 1
	}

	if expiryDate.IsZero() {
		_, err = stmt.Exec(description, architecture, ephemeralInt, "", id)
	} else {
		_, err = stmt.Exec(description, architecture, ephemeralInt, expiryDate, id)
	}
	if err != nil {
		return err
	}

	return nil
}

// ContainerCreationUpdate updates the cration_date field of the container
// with the given ID.
func (c *Cluster) ContainerCreationUpdate(id int, date time.Time) error {
	stmt := `UPDATE instances SET creation_date=? WHERE id=?`
	err := exec(c.db, stmt, date, id)
	return err
}

// ContainerLastUsedUpdate updates the last_use_date field of the container
// with the given ID.
func (c *ClusterTx) ContainerLastUsedUpdate(id int, date time.Time) error {
	str := `UPDATE instances SET last_use_date=? WHERE id=?`
	stmt, err := c.tx.Prepare(str)
	if err != nil {
		return err
	}
	defer stmt.Close()

	_, err = stmt.Exec(date, id)
	if err != nil {
		return err
	}

	return nil
}

// ContainerGetSnapshots returns the names of all snapshots of the container
// in the given project with the given name.
func (c *Cluster) ContainerGetSnapshots(project, name string) ([]string, error) {
	result := []string{}

	q := `
SELECT instances_snapshots.name
  FROM instances_snapshots
  JOIN instances ON instances.id = instances_snapshots.instance_id
  JOIN projects ON projects.id = instances.project_id
WHERE projects.name=? AND instances.name=?
ORDER BY date(instances_snapshots.creation_date)
`
	inargs := []interface{}{project, name}
	outfmt := []interface{}{name}
	dbResults, err := queryScan(c.db, q, inargs, outfmt)
	if err != nil {
		return result, err
	}

	for _, r := range dbResults {
		result = append(result, name+shared.SnapshotDelimiter+r[0].(string))
	}

	return result, nil
}

// ContainerGetSnapshotsFull returns all container objects for snapshots of a given container
func (c *ClusterTx) ContainerGetSnapshotsFull(project string, name string) ([]Instance, error) {
	instance, err := c.InstanceGet(project, name)
	if err != nil {
		return nil, err
	}
	filter := InstanceSnapshotFilter{
		Project:  project,
		Instance: name,
	}

	snapshots, err := c.InstanceSnapshotList(filter)
	if err != nil {
		return nil, err
	}

	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i].CreationDate.Before(snapshots[j].CreationDate) })

	instances := make([]Instance, len(snapshots))
	for i, snapshot := range snapshots {
		instances[i] = InstanceSnapshotToInstance(instance, &snapshot)
	}

	return instances, nil
}

// ContainerNextSnapshot returns the index the next snapshot of the container
// with the given name and pattern should have.
func (c *Cluster) ContainerNextSnapshot(project string, name string, pattern string) int {
	q := `
SELECT instances_snapshots.name
  FROM instances_snapshots
  JOIN instances ON instances.id = instances_snapshots.instance_id
  JOIN projects ON projects.id = instances.project_id
WHERE projects.name=? AND instances.name=?`
	var numstr string
	inargs := []interface{}{project, name}
	outfmt := []interface{}{numstr}
	results, err := queryScan(c.db, q, inargs, outfmt)
	if err != nil {
		return 0
	}
	max := 0

	for _, r := range results {
		snapOnlyName := r[0].(string)
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
	if strings.Contains(containerName, shared.SnapshotDelimiter) {
		return c.containerPoolSnapshot(project, containerName)
	}

	// Get container storage volume. Since container names are globally
	// unique, and their storage volumes carry the same name, their storage
	// volumes are unique too.
	poolName := ""
	query := `
SELECT storage_pools.name FROM storage_pools
  JOIN storage_volumes ON storage_pools.id=storage_volumes.storage_pool_id
  JOIN instances ON instances.name=storage_volumes.name
  JOIN projects ON projects.id=instances.project_id
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

func (c *ClusterTx) containerPoolSnapshot(project, fullName string) (string, error) {
	poolName := ""
	query := `
SELECT storage_pools.name FROM storage_pools
  JOIN storage_volumes ON storage_pools.id=storage_volumes.storage_pool_id
  JOIN projects ON projects.id=storage_volumes.project_id
 WHERE projects.name=? AND storage_volumes.node_id=? AND storage_volumes.name=? AND storage_volumes.type=?
`
	inargs := []interface{}{project, c.nodeID, fullName, StoragePoolVolumeTypeContainer}
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
	q := "SELECT id FROM instances_backups WHERE name=?"
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

	instanceOnlyInt := -1
	optimizedStorageInt := -1
	q := `
SELECT instances_backups.id, instances_backups.instance_id,
       instances_backups.creation_date, instances_backups.expiry_date,
       instances_backups.container_only, instances_backups.optimized_storage
    FROM instances_backups
    JOIN instances ON instances.id=instances_backups.instance_id
    JOIN projects ON projects.id=instances.project_id
    WHERE projects.name=? AND instances_backups.name=?
`
	arg1 := []interface{}{project, name}
	arg2 := []interface{}{&args.ID, &args.ContainerID, &args.CreationDate,
		&args.ExpiryDate, &instanceOnlyInt, &optimizedStorageInt}
	err := dbQueryRowScan(c.db, q, arg1, arg2)
	if err != nil {
		if err == sql.ErrNoRows {
			return args, ErrNoSuchObject
		}

		return args, err
	}

	if instanceOnlyInt == 1 {
		args.InstanceOnly = true
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

	q := `SELECT instances_backups.name FROM instances_backups
JOIN instances ON instances_backups.instance_id=instances.id
JOIN projects ON projects.id=instances.project_id
WHERE projects.name=? AND instances.name=?`
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
		instanceOnlyInt := 0
		if args.InstanceOnly {
			instanceOnlyInt = 1
		}

		optimizedStorageInt := 0
		if args.OptimizedStorage {
			optimizedStorageInt = 1
		}

		str := fmt.Sprintf("INSERT INTO instances_backups (instance_id, name, creation_date, expiry_date, container_only, optimized_storage) VALUES (?, ?, ?, ?, ?, ?)")
		stmt, err := tx.tx.Prepare(str)
		if err != nil {
			return err
		}
		defer stmt.Close()
		result, err := stmt.Exec(args.ContainerID, args.Name,
			args.CreationDate.Unix(), args.ExpiryDate.Unix(), instanceOnlyInt,
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

	err = exec(c.db, "DELETE FROM instances_backups WHERE id=?", id)
	if err != nil {
		return err
	}

	return nil
}

// ContainerBackupRename renames a container backup from the given current name
// to the new one.
func (c *Cluster) ContainerBackupRename(oldName, newName string) error {
	err := c.Transaction(func(tx *ClusterTx) error {
		str := fmt.Sprintf("UPDATE instances_backups SET name = ? WHERE name = ?")
		stmt, err := tx.tx.Prepare(str)
		if err != nil {
			return err
		}
		defer stmt.Close()

		logger.Debug(
			"Calling SQL Query",
			log.Ctx{
				"query":   "UPDATE instances_backups SET name = ? WHERE name = ?",
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

	q := `SELECT instances_backups.name, instances_backups.expiry_date FROM instances_backups`
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
