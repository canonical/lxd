//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lxc/lxd/lxd/db/query"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/pkg/errors"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t instances.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -p db -e instance objects
//go:generate mapper stmt -p db -e instance objects-by-ID
//go:generate mapper stmt -p db -e instance objects-by-Project
//go:generate mapper stmt -p db -e instance objects-by-Project-and-Type
//go:generate mapper stmt -p db -e instance objects-by-Project-and-Type-and-Node
//go:generate mapper stmt -p db -e instance objects-by-Project-and-Type-and-Node-and-Name
//go:generate mapper stmt -p db -e instance objects-by-Project-and-Type-and-Name
//go:generate mapper stmt -p db -e instance objects-by-Project-and-Name
//go:generate mapper stmt -p db -e instance objects-by-Project-and-Name-and-Node
//go:generate mapper stmt -p db -e instance objects-by-Project-and-Node
//go:generate mapper stmt -p db -e instance objects-by-Type
//go:generate mapper stmt -p db -e instance objects-by-Type-and-Name
//go:generate mapper stmt -p db -e instance objects-by-Type-and-Name-and-Node
//go:generate mapper stmt -p db -e instance objects-by-Type-and-Node
//go:generate mapper stmt -p db -e instance objects-by-Node
//go:generate mapper stmt -p db -e instance objects-by-Node-and-Name
//go:generate mapper stmt -p db -e instance objects-by-Name
//go:generate mapper stmt -p db -e instance id
//go:generate mapper stmt -p db -e instance create struct=Instance
//go:generate mapper stmt -p db -e instance rename
//go:generate mapper stmt -p db -e instance delete-by-Project-and-Name
//go:generate mapper stmt -p db -e instance update struct=Instance
//
//go:generate mapper method -p db -e instance GetMany references=Device,Config
//go:generate mapper method -p db -e instance GetOne
//go:generate mapper method -p db -e instance URIs
//go:generate mapper method -p db -e instance ID struct=Instance
//go:generate mapper method -p db -e instance Exists struct=Instance
//go:generate mapper method -p db -e instance Create references=Device,Config
//go:generate mapper method -p db -e instance Rename
//go:generate mapper method -p db -e instance DeleteOne-by-Project-and-Name
//go:generate mapper method -p db -e instance Update references=Device,Config

// Instance is a value object holding db-related details about an instance.
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
	LastUseDate  sql.NullTime
	Description  string `db:"coalesce=''"`
	Profiles     []string
	ExpiryDate   sql.NullTime
}

// InstanceFilter specifies potential query parameter fields.
type InstanceFilter struct {
	ID      *int
	Project *string
	Name    *string
	Node    *string
	Type    *instancetype.Type
}

// InstanceToArgs is a convenience to convert an Instance db struct into the legacy InstanceArgs.
func InstanceToArgs(inst *Instance) InstanceArgs {
	args := InstanceArgs{
		ID:           inst.ID,
		Project:      inst.Project,
		Name:         inst.Name,
		Node:         inst.Node,
		Type:         inst.Type,
		Snapshot:     inst.Snapshot,
		Architecture: inst.Architecture,
		Ephemeral:    inst.Ephemeral,
		CreationDate: inst.CreationDate,
		Stateful:     inst.Stateful,
		LastUsedDate: inst.LastUseDate.Time,
		Description:  inst.Description,
		Profiles:     inst.Profiles,
		ExpiryDate:   inst.ExpiryDate.Time,
	}

	// TODO: fetch instance devices, config, and profiles, and handle errors if necessary.
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
	Devices      deviceConfig.Devices
	Ephemeral    bool
	LastUsedDate time.Time
	Name         string
	Profiles     []string
	Stateful     bool
	ExpiryDate   time.Time
}

// InstanceTypeFilter returns an InstanceFilter populated with a valid instance type,
// or an empty filter if instance type is 'Any'.
func InstanceTypeFilter(instanceType instancetype.Type) InstanceFilter {
	if instanceType != instancetype.Any {
		return InstanceFilter{Type: &instanceType}
	}
	return InstanceFilter{}
}

// GetInstanceNames returns the names of all containers the given project.
func (c *ClusterTx) GetInstanceNames(project string) ([]string, error) {
	stmt := `
SELECT instances.name FROM instances
  JOIN projects ON projects.id = instances.project_id
  WHERE projects.name = ? AND instances.type = ?
`
	return query.SelectStrings(c.tx, stmt, project, instancetype.Any)
}

// GetNodeAddressOfInstance returns the address of the node hosting the
// instance with the given name in the given project.
//
// It returns the empty string if the container is hosted on this node.
func (c *ClusterTx) GetNodeAddressOfInstance(project string, name string, filter InstanceFilter) (string, error) {
	var stmt string

	args := make([]interface{}, 0, 4) // Expect up to 4 filters.
	var filters strings.Builder

	// Project filter.
	filters.WriteString("projects.name = ?")
	args = append(args, project)

	// Instance type filter.
	if filter.Type != nil {
		filters.WriteString(" AND instances.type = ?")
		args = append(args, *filter.Type)
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
		return "", fmt.Errorf("More than one node associated with instance")
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

// GetProjectAndInstanceNamesByNodeAddress returns the project and name of all instances grouped by
// cluster node address. Each node address has a slice of instances, where each instance is represented
// as an array of length 2 in which element 0 is the project and element 1 is the instance name.
//
// The node address of instances running on the local node is set to the empty
// string, to distinguish it from remote nodes.
//
// Instances whose node is down are added to the special address "0.0.0.0".
func (c *ClusterTx) GetProjectAndInstanceNamesByNodeAddress(projects []string, filter InstanceFilter) (map[string][][2]string, error) {
	offlineThreshold, err := c.GetNodeOfflineThreshold()
	if err != nil {
		return nil, err
	}

	args := make([]interface{}, 0, 2) // Expect up to 2 filters.
	var filters strings.Builder

	// Project filter.
	filters.WriteString(fmt.Sprintf("projects.name IN (%s)", generateInClauseParams(len(projects))))
	for _, project := range projects {
		args = append(args, project)
	}

	// Instance type filter.
	if filter.Type != nil {
		filters.WriteString(" AND instances.type = ?")
		args = append(args, *filter.Type)
	}

	stmt := fmt.Sprintf(`
SELECT instances.name, nodes.id, nodes.address, nodes.heartbeat, projects.name
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

	result := map[string][][2]string{}

	for i := 0; rows.Next(); i++ {
		var instanceName string
		var nodeAddress string
		var nodeID int64
		var nodeHeartbeat time.Time
		var projectName string
		err := rows.Scan(&instanceName, &nodeID, &nodeAddress, &nodeHeartbeat, &projectName)
		if err != nil {
			return nil, err
		}
		if nodeID == c.nodeID {
			nodeAddress = ""
		} else if nodeIsOffline(offlineThreshold, nodeHeartbeat) {
			nodeAddress = "0.0.0.0"
		}
		result[nodeAddress] = append(result[nodeAddress], [2]string{projectName, instanceName})
	}

	err = rows.Err()
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ErrInstanceListStop used as return value from InstanceList's instanceFunc when prematurely stopping the search.
var ErrInstanceListStop = fmt.Errorf("search stopped")

// InstanceList loads all instances across all projects and for each instance runs the instanceFunc passing in the
// instance and it's project and profiles. Accepts optional filter argument to specify a subset of instances.
func (c *Cluster) InstanceList(filter *InstanceFilter, instanceFunc func(inst Instance, project Project, profiles []api.Profile) error) error {
	var instances []Instance
	projectMap := map[string]Project{}
	projectHasProfiles := map[string]bool{}
	profilesByProjectAndName := map[string]map[string]Profile{}

	if filter == nil {
		filter = &InstanceFilter{}
	}

	// Retrieve required info from the database in single transaction for performance.
	err := c.Transaction(func(tx *ClusterTx) error {
		var err error
		instances, err = tx.GetInstances(*filter)
		if err != nil {
			return errors.Wrap(err, "Failed loading instances")
		}

		projects, err := tx.GetProjects(ProjectFilter{})
		if err != nil {
			return errors.Wrap(err, "Failed loading projects")
		}

		// Index of all projects by name and record which projects have the profiles feature.
		for i, project := range projects {
			projectMap[project.Name] = projects[i]
			projectHasProfiles[project.Name] = shared.IsTrue(project.Config["features.profiles"])
		}

		profiles, err := tx.GetProfiles(ProfileFilter{})
		if err != nil {
			return errors.Wrap(err, "Failed loading profiles")
		}

		// Index of all profiles by project and name.
		for _, profile := range profiles {
			profilesByName, ok := profilesByProjectAndName[profile.Project]
			if !ok {
				profilesByName = map[string]Profile{}
				profilesByProjectAndName[profile.Project] = profilesByName
			}
			profilesByName[profile.Name] = profile
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Call the instanceFunc provided for each instance after the transaction has ended, as we don't know if
	// the instanceFunc will be slow or may need to make additional DB queries.
	for _, instance := range instances {
		profiles := make([]api.Profile, len(instance.Profiles))

		// If the instance's project does not have the profiles feature enabled,
		// we fall back to the default project.
		profilesProject := instance.Project
		if !projectHasProfiles[profilesProject] {
			profilesProject = "default" // Equivalent to project.Default constant.
		}

		for j, name := range instance.Profiles {
			profile := profilesByProjectAndName[profilesProject][name]
			profiles[j] = *ProfileToAPI(&profile)
		}

		err = instanceFunc(instance, projectMap[instance.Project], profiles)
		if err != nil {
			return err
		}
	}

	return nil
}

// GetProjectInstanceToNodeMap returns a map associating the project (key element 0) and name (key element 1) of each
// instance in the given projects to the name of the node hosting the instance.
func (c *ClusterTx) GetProjectInstanceToNodeMap(projects []string, filter InstanceFilter) (map[[2]string]string, error) {
	args := make([]interface{}, 0, 2) // Expect up to 2 filters.
	var filters strings.Builder

	// Project filter.
	filters.WriteString(fmt.Sprintf("projects.name IN (%s)", generateInClauseParams(len(projects))))
	for _, project := range projects {
		args = append(args, project)
	}

	// Instance type filter.
	if filter.Type != nil {
		filters.WriteString(" AND instances.type = ?")
		args = append(args, *filter.Type)
	}

	stmt := fmt.Sprintf(`
SELECT instances.name, nodes.name, projects.name
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

	result := map[[2]string]string{}

	for i := 0; rows.Next(); i++ {
		var instanceName string
		var nodeName string
		var projectName string
		err := rows.Scan(&instanceName, &nodeName, &projectName)
		if err != nil {
			return nil, err
		}
		result[[2]string{projectName, instanceName}] = nodeName
	}

	err = rows.Err()
	if err != nil {
		return nil, err
	}
	return result, nil
}

// UpdateInstanceNode changes the name of an instance and the cluster member hosting it.
// It's meant to be used when moving a non-running instance backed by ceph from one cluster node to another.
func (c *ClusterTx) UpdateInstanceNode(project, oldName string, newName string, newNode string, volumeType int) error {
	// First check that the container to be moved is backed by a ceph
	// volume.
	poolName, err := c.GetInstancePool(project, oldName)
	if err != nil {
		return errors.Wrap(err, "Failed to get instance's storage pool name")
	}

	poolID, err := c.GetStoragePoolID(poolName)
	if err != nil {
		return errors.Wrap(err, "Failed to get instance's storage pool ID")
	}

	poolDriver, err := c.GetStoragePoolDriver(poolID)
	if err != nil {
		return errors.Wrap(err, "Failed to get instance's storage pool driver")
	}

	if poolDriver != "ceph" {
		return fmt.Errorf("Instance's storage pool is not of type ceph")
	}

	// Update the name of the container and of its snapshots, and the node
	// ID they are associated with.
	containerID, err := c.GetInstanceID(project, oldName)
	if err != nil {
		return errors.Wrap(err, "Failed to get instance's ID")
	}

	node, err := c.GetNodeByName(newNode)
	if err != nil {
		return errors.Wrap(err, "Failed to get new node's info")
	}

	stmt := "UPDATE instances SET node_id=?, name=? WHERE id=?"
	result, err := c.tx.Exec(stmt, node.ID, newName, containerID)
	if err != nil {
		return errors.Wrap(err, "Failed to update instance's name and node ID")
	}

	n, err := result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "Failed to get rows affected by instance update")
	}

	if n != 1 {
		return fmt.Errorf("Unexpected number of updated rows in instances table: %d", n)
	}

	// No need to update storage_volumes if the name is identical
	if newName == oldName {
		return nil
	}

	stmt = "UPDATE storage_volumes SET name=? WHERE name=? AND storage_pool_id=? AND type=?"
	result, err = c.tx.Exec(stmt, newName, oldName, poolID, volumeType)
	if err != nil {
		return errors.Wrap(err, "Failed to update instance's volume name")
	}

	n, err = result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "Failed to get rows affected by instance volume update")
	}

	if n != 1 {
		return fmt.Errorf("Unexpected number of updated rows in volumes table: %d", n)
	}

	return nil
}

// GetLocalInstancesInProject retuurns all instances of the given type on the local node within the given project.
// If projectName is empty then all instances in all projects are returned.
func (c *ClusterTx) GetLocalInstancesInProject(filter InstanceFilter) ([]Instance, error) {
	node, err := c.GetLocalNodeName()
	if err != nil {
		return nil, errors.Wrap(err, "Local node name")
	}

	if node != "" {
		filter.Node = &node
	}

	return c.GetInstances(filter)
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

// DeleteInstanceConfigKey removes the given key from the config of the instance
// with the given ID.
func (c *ClusterTx) DeleteInstanceConfigKey(id int64, key string) error {
	q := "DELETE FROM instances_config WHERE key=? AND instance_id=?"
	_, err := c.tx.Exec(q, key, id)
	return err
}

// UpdateInstancePowerState sets the the power state of the container with the given ID.
func (c *ClusterTx) UpdateInstancePowerState(id int, state string) error {
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

// UpdateInstanceLastUsedDate updates the last_use_date field of the instance
// with the given ID.
func (c *ClusterTx) UpdateInstanceLastUsedDate(id int, date time.Time) error {
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

// GetInstanceSnapshotsWithName returns all snapshots of a given instance in date created order, oldest first.
func (c *ClusterTx) GetInstanceSnapshotsWithName(project string, name string) ([]Instance, error) {
	instance, err := c.GetInstance(project, name)
	if err != nil {
		return nil, err
	}
	filter := InstanceSnapshotFilter{
		Project:  &project,
		Instance: &name,
	}

	snapshots, err := c.GetInstanceSnapshots(filter)
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

// GetInstancePool returns the storage pool of a given instance (or snapshot).
func (c *ClusterTx) GetInstancePool(projectName string, instanceName string) (string, error) {
	// Strip snapshot name if supplied in instanceName, and lookup the storage pool of the parent instance
	// as that must always be the same as the snapshot's storage pool.
	instanceName, _, _ = shared.InstanceGetParentAndSnapshotName(instanceName)

	remoteDrivers := StorageRemoteDriverNames()

	// Get container storage volume. Since container names are globally
	// unique, and their storage volumes carry the same name, their storage
	// volumes are unique too.
	poolName := ""
	query := fmt.Sprintf(`
SELECT storage_pools.name FROM storage_pools
  JOIN storage_volumes_all ON storage_pools.id=storage_volumes_all.storage_pool_id
  JOIN instances ON instances.name=storage_volumes_all.name
  JOIN projects ON projects.id=instances.project_id
 WHERE projects.name=?
   AND storage_volumes_all.name=?
   AND storage_volumes_all.type IN(?,?)
   AND storage_volumes_all.project_id = instances.project_id
   AND (storage_volumes_all.node_id=? OR storage_volumes_all.node_id IS NULL AND storage_pools.driver IN %s)`, query.Params(len(remoteDrivers)))
	inargs := []interface{}{projectName, instanceName, StoragePoolVolumeTypeContainer, StoragePoolVolumeTypeVM, c.nodeID}
	outargs := []interface{}{&poolName}

	for _, driver := range remoteDrivers {
		inargs = append(inargs, driver)
	}

	err := c.tx.QueryRow(query, inargs...).Scan(outargs...)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", ErrNoSuchObject
		}

		return "", err
	}

	return poolName, nil
}

// DeleteInstance removes the instance with the given name from the database.
func (c *Cluster) DeleteInstance(project, name string) error {
	if strings.Contains(name, shared.SnapshotDelimiter) {
		parts := strings.SplitN(name, shared.SnapshotDelimiter, 2)
		return c.Transaction(func(tx *ClusterTx) error {
			return tx.DeleteInstanceSnapshot(project, parts[0], parts[1])
		})
	}
	return c.Transaction(func(tx *ClusterTx) error {
		return tx.DeleteInstance(project, name)
	})
}

// GetInstanceProjectAndName returns the project and the name of the instance
// with the given ID.
func (c *Cluster) GetInstanceProjectAndName(id int) (string, string, error) {
	var project string
	var name string
	q := `
SELECT projects.name, instances.name
  FROM instances
  JOIN projects ON projects.id = instances.project_id
WHERE instances.id=?
`
	err := c.Transaction(func(tx *ClusterTx) error {
		return tx.tx.QueryRow(q, id).Scan(&project, &name)

	})

	if err == sql.ErrNoRows {
		return "", "", ErrNoSuchObject
	}

	return project, name, err
}

// GetInstanceID returns the ID of the instance with the given name.
func (c *Cluster) GetInstanceID(project, name string) (int, error) {
	var id int64
	err := c.Transaction(func(tx *ClusterTx) error {
		var err error
		id, err = tx.GetInstanceID(project, name)
		return err
	})
	return int(id), err
}

// GetInstanceConfig returns the value of the given key in the configuration
// of the instance with the given ID.
func (c *Cluster) GetInstanceConfig(id int, key string) (string, error) {
	q := "SELECT value FROM instances_config WHERE instance_id=? AND key=?"
	value := ""
	err := c.Transaction(func(tx *ClusterTx) error {
		return tx.tx.QueryRow(q, id, key).Scan(&value)
	})
	if err == sql.ErrNoRows {
		return "", ErrNoSuchObject
	}

	return value, err
}

// DeleteInstanceConfigKey removes the given key from the config of the instance
// with the given ID.
func (c *Cluster) DeleteInstanceConfigKey(id int, key string) error {
	return c.Transaction(func(tx *ClusterTx) error {
		return tx.DeleteInstanceConfigKey(int64(id), key)
	})
}

// UpdateInstanceStatefulFlag toggles the stateful flag of the instance with
// the given ID.
func (c *Cluster) UpdateInstanceStatefulFlag(id int, stateful bool) error {
	statefulInt := 0
	if stateful {
		statefulInt = 1
	}
	return c.Transaction(func(tx *ClusterTx) error {
		_, err := tx.tx.Exec("UPDATE instances SET stateful=? WHERE id=?", statefulInt, id)
		return err
	})
}

// LegacyContainersList returns the names of all the containers.
//
// NOTE: this is a pre-projects legacy API that is used only by patches. Don't
// use it for new code.
func (c *Cluster) LegacyContainersList() ([]string, error) {
	q := fmt.Sprintf("SELECT name FROM instances WHERE type=? ORDER BY name")

	var ret []string

	err := c.Transaction(func(tx *ClusterTx) error {
		var err error
		ret, err = query.SelectStrings(tx.tx, q, instancetype.Container)
		return err
	})
	if err != nil {
		return nil, err
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
	result, err := queryScan(c, q, inargs, outfmt)
	if err != nil {
		return nil, err
	}

	var ret []string
	for _, r := range result {
		ret = append(ret, r[0].(string)+shared.SnapshotDelimiter+r[1].(string))
	}

	return ret, nil
}

// UpdateInstanceSnapshotCreationDate updates the creation_date field of the instance snapshot with ID.
func (c *Cluster) UpdateInstanceSnapshotCreationDate(instanceID int, date time.Time) error {
	stmt := `UPDATE instances_snapshots SET creation_date=? WHERE id=?`
	err := exec(c, stmt, date, instanceID)
	return err
}

// GetInstanceSnapshotsNames returns the names of all snapshots of the instance
// in the given project with the given name.
func (c *Cluster) GetInstanceSnapshotsNames(project, name string) ([]string, error) {
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
	dbResults, err := queryScan(c, q, inargs, outfmt)
	if err != nil {
		return result, err
	}

	for _, r := range dbResults {
		result = append(result, name+shared.SnapshotDelimiter+r[0].(string))
	}

	return result, nil
}

// GetNextInstanceSnapshotIndex returns the index that the next snapshot of the
// instance with the given name and pattern should have.
func (c *Cluster) GetNextInstanceSnapshotIndex(project string, name string, pattern string) int {
	q := `
SELECT instances_snapshots.name
  FROM instances_snapshots
  JOIN instances ON instances.id = instances_snapshots.instance_id
  JOIN projects ON projects.id = instances.project_id
WHERE projects.name=? AND instances.name=?`
	var numstr string
	inargs := []interface{}{project, name}
	outfmt := []interface{}{numstr}
	results, err := queryScan(c, q, inargs, outfmt)
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

// GetInstancePool returns the storage pool of a given instance.
//
// This is a non-transactional variant of ClusterTx.GetInstancePool().
func (c *Cluster) GetInstancePool(project, instanceName string) (string, error) {
	var poolName string
	err := c.Transaction(func(tx *ClusterTx) error {
		var err error
		poolName, err = tx.GetInstancePool(project, instanceName)
		return err
	})
	return poolName, err
}

// UpdateInstance updates the description, architecture and ephemeral flag of
// the instance with the given ID.
func UpdateInstance(tx *sql.Tx, id int, description string, architecture int, ephemeral bool,
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

// Generates '?' signs for sql IN clause.
func generateInClauseParams(length int) string {
	result := []string{}
	for i := 0; i < length; i++ {
		result = append(result, "?")
	}
	return strings.Join(result, ",")
}
