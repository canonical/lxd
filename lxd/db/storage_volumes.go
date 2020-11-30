// +build linux,cgo,!agent

package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/pkg/errors"
)

// GetStoragePoolVolumesNames gets the names of all storage volumes attached to
// a given storage pool.
func (c *Cluster) GetStoragePoolVolumesNames(poolID int64) ([]string, error) {
	var volumeName string
	query := "SELECT name FROM storage_volumes_all WHERE storage_pool_id=? AND node_id=?"
	inargs := []interface{}{poolID, c.nodeID}
	outargs := []interface{}{volumeName}

	result, err := queryScan(c, query, inargs, outargs)
	if err != nil {
		return []string{}, err
	}

	var out []string

	for _, r := range result {
		out = append(out, r[0].(string))
	}

	return out, nil
}

// GetStoragePoolVolumesWithType return a list of all volumes of the given type.
func (c *Cluster) GetStoragePoolVolumesWithType(volumeType int) ([]StorageVolumeArgs, error) {
	var id int64
	var name string
	var description string
	var poolName string
	var projectName string

	stmt := `
SELECT storage_volumes.id, storage_volumes.name, storage_volumes.description, storage_pools.name, projects.name
FROM storage_volumes
JOIN storage_pools ON storage_pools.id = storage_volumes.storage_pool_id
JOIN projects ON projects.id = storage_volumes.project_id
WHERE storage_volumes.type = ?
`

	inargs := []interface{}{volumeType}
	outargs := []interface{}{id, name, description, poolName, projectName}

	result, err := queryScan(c, stmt, inargs, outargs)
	if err != nil {
		return nil, err
	}

	var response []StorageVolumeArgs

	for _, r := range result {
		args := StorageVolumeArgs{
			ID:          r[0].(int64),
			Name:        r[1].(string),
			Description: r[2].(string),
			PoolName:    r[3].(string),
			ProjectName: r[4].(string),
		}

		args.Config, err = c.storageVolumeConfigGet(args.ID, false)
		if err != nil {
			return nil, err
		}

		response = append(response, args)
	}

	return response, nil
}

// GetStoragePoolVolumes returns all storage volumes attached to a given
// storage pool on any node. If there are no volumes, it returns an
// empty list and no error.
func (c *Cluster) GetStoragePoolVolumes(project string, poolID int64, volumeTypes []int) ([]*api.StorageVolume, error) {
	var nodeIDs []int

	remoteDrivers := StorageRemoteDriverNames()

	err := c.Transaction(func(tx *ClusterTx) error {
		var err error
		// Exclude storage volumes residing on ceph and cephfs as their node ID is null, and SelectIntegers() cannot deal with that.

		s := fmt.Sprintf(`
SELECT DISTINCT node_id
  FROM storage_volumes_all
  JOIN projects ON projects.id = storage_volumes_all.project_id
  JOIN storage_pools ON storage_pools.id = storage_volumes_all.storage_pool_id
 WHERE (projects.name=? OR storage_volumes_all.type=?) AND storage_volumes_all.storage_pool_id=? AND storage_pools.driver NOT IN %s`, query.Params(len(remoteDrivers)))

		args := []interface{}{project, StoragePoolVolumeTypeCustom, poolID}

		for _, driver := range remoteDrivers {
			args = append(args, driver)
		}

		nodeIDs, err = query.SelectIntegers(tx.tx, s, args...)
		return err
	})
	if err != nil {
		return nil, err
	}

	volumes := []*api.StorageVolume{}

	for _, nodeID := range nodeIDs {
		nodeVolumes, err := c.storagePoolVolumesGet(project, poolID, int64(nodeID), volumeTypes)
		if err != nil {
			if err == ErrNoSuchObject {
				continue
			}

			return nil, err
		}

		volumes = append(volumes, nodeVolumes...)
	}

	isRemoteStorage, err := c.isRemoteStorage(poolID)
	if err != nil {
		return nil, err
	}

	if isRemoteStorage {
		nodeVolumes, err := c.storagePoolVolumesGet(project, poolID, c.nodeID, volumeTypes)
		if err != nil && err != ErrNoSuchObject {
			return nil, err
		}

		volumes = append(volumes, nodeVolumes...)
	}

	return volumes, nil
}

// GetLocalStoragePoolVolumes returns all storage volumes attached to a given
// storage pool on the current node. If there are no volumes, it returns an
// empty list as well as ErrNoSuchObject.
func (c *Cluster) GetLocalStoragePoolVolumes(project string, poolID int64, volumeTypes []int) ([]*api.StorageVolume, error) {
	return c.storagePoolVolumesGet(project, poolID, c.nodeID, volumeTypes)
}

// Returns all storage volumes attached to a given storage pool on the given
// node. If there are no volumes, it returns an empty list as well as
// ErrNoSuchObject.
func (c *Cluster) storagePoolVolumesGet(project string, poolID, nodeID int64, volumeTypes []int) ([]*api.StorageVolume, error) {
	// Get all storage volumes of all types attached to a given storage pool.
	result := []*api.StorageVolume{}
	for _, volumeType := range volumeTypes {
		volumeNames, err := c.storagePoolVolumesGetType(project, volumeType, poolID, nodeID)
		if err != nil && err != sql.ErrNoRows {
			return nil, errors.Wrap(err, "Failed to fetch volume types")
		}

		for _, volumeName := range volumeNames {
			_, volume, err := c.storagePoolVolumeGetType(project, volumeName, volumeType, poolID, nodeID)
			if err != nil {
				return nil, errors.Wrap(err, "Failed to fetch volume type")
			}
			result = append(result, volume)
		}
	}

	if len(result) == 0 {
		return result, ErrNoSuchObject
	}

	return result, nil
}

// Get all storage volumes attached to a given storage pool of a given volume
// type, on the given node.
func (c *Cluster) storagePoolVolumesGetType(project string, volumeType int, poolID, nodeID int64) ([]string, error) {
	var poolName string

	remoteDrivers := StorageRemoteDriverNames()

	query := fmt.Sprintf(`
SELECT storage_volumes_all.name
  FROM storage_volumes_all
  JOIN projects ON projects.id=storage_volumes_all.project_id
  join storage_pools ON storage_pools.id=storage_volumes_all.storage_pool_id
 WHERE projects.name=?
   AND storage_volumes_all.storage_pool_id=?
   AND storage_volumes_all.type=?
   AND (storage_volumes_all.node_id=? OR storage_volumes_all.node_id IS NULL AND storage_pools.driver IN %s)`, query.Params(len(remoteDrivers)))
	inargs := []interface{}{project, poolID, volumeType, nodeID}
	outargs := []interface{}{poolName}

	for _, driver := range remoteDrivers {
		inargs = append(inargs, driver)
	}

	result, err := queryScan(c, query, inargs, outargs)
	if err != nil {
		return []string{}, err
	}

	response := []string{}
	for _, r := range result {
		response = append(response, r[0].(string))
	}

	return response, nil
}

// GetLocalStoragePoolVolumeSnapshotsWithType get all snapshots of a storage volume
// attached to a given storage pool of a given volume type, on the local node.
// Returns snapshots slice ordered by when they were created, oldest first.
func (c *Cluster) GetLocalStoragePoolVolumeSnapshotsWithType(projectName string, volumeName string, volumeType int, poolID int64) ([]StorageVolumeArgs, error) {
	result := []StorageVolumeArgs{}
	remoteDrivers := StorageRemoteDriverNames()

	// ORDER BY id is important here as the users of this function can expect that the results
	// will be returned in the order that the snapshots were created. This is specifically used
	// during migration to ensure that the storage engines can re-create snapshots using the
	// correct deltas.
	query := fmt.Sprintf(`
SELECT storage_volumes_snapshots.name, storage_volumes_snapshots.description FROM storage_volumes_snapshots
  JOIN storage_volumes ON storage_volumes_snapshots.storage_volume_id = storage_volumes.id
  JOIN projects ON projects.id=storage_volumes.project_id
  JOIN storage_pools ON storage_pools.id=storage_volumes.storage_pool_id
  WHERE storage_volumes.storage_pool_id=?
    AND storage_volumes.type=?
    AND storage_volumes.name=?
    AND projects.name=?
    AND (storage_volumes.node_id=? OR storage_volumes.node_id IS NULL AND storage_pools.driver IN %s)
  ORDER BY storage_volumes_snapshots.id`, query.Params(len(remoteDrivers)))
	inargs := []interface{}{poolID, volumeType, volumeName, projectName, c.nodeID}

	for _, driver := range remoteDrivers {
		inargs = append(inargs, driver)
	}

	typeGuide := StorageVolumeArgs{} // StorageVolume struct used to guide the types expected.
	outfmt := []interface{}{typeGuide.Name, typeGuide.Description}
	dbResults, err := queryScan(c, query, inargs, outfmt)
	if err != nil {
		return result, err
	}

	for _, r := range dbResults {
		row := StorageVolumeArgs{
			Name:        volumeName + shared.SnapshotDelimiter + r[0].(string),
			Description: r[1].(string),
			Snapshot:    true,
		}
		result = append(result, row)
	}

	return result, nil
}

// GetLocalStoragePoolVolumesWithType returns all storage volumes attached to a
// given storage pool of a given volume type, on the current node.
func (c *Cluster) GetLocalStoragePoolVolumesWithType(projectName string, volumeType int, poolID int64) ([]string, error) {
	return c.storagePoolVolumesGetType(projectName, volumeType, poolID, c.nodeID)
}

// Return a single storage volume attached to a given storage pool of a given
// type, on the node with the given ID.
func (c *Cluster) storagePoolVolumeGetType(project string, volumeName string, volumeType int, poolID, nodeID int64) (int64, *api.StorageVolume, error) {
	isSnapshot := strings.Contains(volumeName, shared.SnapshotDelimiter)

	volumeID, err := c.storagePoolVolumeGetTypeID(project, volumeName, volumeType, poolID, nodeID)
	if err != nil {
		return -1, nil, err
	}

	isRemoteStorage, err := c.isRemoteStorage(poolID)
	if err != nil {
		return -1, nil, err
	}

	volumeNode := ""

	if !isRemoteStorage {
		volumeNode, err = c.storageVolumeNodeGet(volumeID)
		if err != nil {
			return -1, nil, err
		}
	}

	volumeConfig, err := c.storageVolumeConfigGet(volumeID, isSnapshot)
	if err != nil {
		return -1, nil, err
	}

	volumeDescription, err := c.GetStorageVolumeDescription(volumeID)
	if err != nil {
		return -1, nil, err
	}

	volumeContentType, err := c.getStorageVolumeContentType(volumeID)
	if err != nil {
		return -1, nil, err
	}

	volumeTypeName, err := storagePoolVolumeTypeToName(volumeType)
	if err != nil {
		return -1, nil, err
	}

	volumeContentTypeName, err := storagePoolVolumeContentTypeToName(volumeContentType)
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
	storageVolume.ContentType = volumeContentTypeName

	return volumeID, &storageVolume, nil
}

// GetLocalStoragePoolVolume gets a single storage volume attached to a
// given storage pool of a given type, on the current node in the given project.
func (c *Cluster) GetLocalStoragePoolVolume(project, volumeName string, volumeType int, poolID int64) (int64, *api.StorageVolume, error) {
	return c.storagePoolVolumeGetType(project, volumeName, volumeType, poolID, c.nodeID)
}

// UpdateStoragePoolVolume updates the storage volume attached to a given storage pool.
func (c *Cluster) UpdateStoragePoolVolume(project, volumeName string, volumeType int, poolID int64, volumeDescription string, volumeConfig map[string]string) error {
	volumeID, _, err := c.GetLocalStoragePoolVolume(project, volumeName, volumeType, poolID)
	if err != nil {
		return err
	}

	isSnapshot := strings.Contains(volumeName, shared.SnapshotDelimiter)

	err = c.Transaction(func(tx *ClusterTx) error {
		err = storagePoolVolumeReplicateIfCeph(tx.tx, volumeID, project, volumeName, volumeType, poolID, func(volumeID int64) error {
			err = storageVolumeConfigClear(tx.tx, volumeID, isSnapshot)
			if err != nil {
				return err
			}

			err = storageVolumeConfigAdd(tx.tx, volumeID, volumeConfig, isSnapshot)
			if err != nil {
				return err
			}

			return storageVolumeDescriptionUpdate(tx.tx, volumeID, volumeDescription, isSnapshot)
		})
		if err != nil {
			return err
		}
		return nil
	})

	return err
}

// RemoveStoragePoolVolume deletes the storage volume attached to a given storage
// pool.
func (c *Cluster) RemoveStoragePoolVolume(project, volumeName string, volumeType int, poolID int64) error {
	volumeID, _, err := c.GetLocalStoragePoolVolume(project, volumeName, volumeType, poolID)
	if err != nil {
		return err
	}

	isSnapshot := strings.Contains(volumeName, shared.SnapshotDelimiter)
	var stmt string
	if isSnapshot {
		stmt = "DELETE FROM storage_volumes_snapshots WHERE id=?"
	} else {
		stmt = "DELETE FROM storage_volumes WHERE id=?"
	}

	err = c.Transaction(func(tx *ClusterTx) error {
		err := storagePoolVolumeReplicateIfCeph(tx.tx, volumeID, project, volumeName, volumeType, poolID, func(volumeID int64) error {
			_, err := tx.tx.Exec(stmt, volumeID)
			return err
		})
		return err
	})

	return err
}

// RenameStoragePoolVolume renames the storage volume attached to a given storage pool.
func (c *Cluster) RenameStoragePoolVolume(project, oldVolumeName string, newVolumeName string, volumeType int, poolID int64) error {
	volumeID, _, err := c.GetLocalStoragePoolVolume(project, oldVolumeName, volumeType, poolID)
	if err != nil {
		return err
	}

	isSnapshot := strings.Contains(oldVolumeName, shared.SnapshotDelimiter)
	var stmt string
	if isSnapshot {
		parts := strings.Split(newVolumeName, shared.SnapshotDelimiter)
		newVolumeName = parts[1]
		stmt = "UPDATE storage_volumes_snapshots SET name=? WHERE id=?"
	} else {
		stmt = "UPDATE storage_volumes SET name=? WHERE id=?"
	}

	err = c.Transaction(func(tx *ClusterTx) error {
		err := storagePoolVolumeReplicateIfCeph(tx.tx, volumeID, project, oldVolumeName, volumeType, poolID, func(volumeID int64) error {
			_, err := tx.tx.Exec(stmt, newVolumeName, volumeID)
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

	remoteDrivers := StorageRemoteDriverNames()

	// If this is a ceph volume, we want to duplicate the change across the
	// the rows for all other nodes.
	if shared.StringInSlice(driver, remoteDrivers) {
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

// CreateStoragePoolVolume creates a new storage volume attached to a given
// storage pool.
func (c *Cluster) CreateStoragePoolVolume(project, volumeName, volumeDescription string, volumeType int, poolID int64, volumeConfig map[string]string, contentType int) (int64, error) {
	var volumeID int64

	if shared.IsSnapshot(volumeName) {
		return -1, fmt.Errorf("Volume name may not be a snapshot")
	}

	remoteDrivers := StorageRemoteDriverNames()

	err := c.Transaction(func(tx *ClusterTx) error {
		driver, err := tx.GetStoragePoolDriver(poolID)
		if err != nil {
			return err
		}

		var result sql.Result

		if shared.StringInSlice(driver, remoteDrivers) {
			result, err = tx.tx.Exec(`
INSERT INTO storage_volumes (storage_pool_id, type, name, description, project_id, content_type)
 VALUES (?, ?, ?, ?, (SELECT id FROM projects WHERE name = ?), ?)
`,
				poolID, volumeType, volumeName, volumeDescription, project, contentType)
		} else {
			result, err = tx.tx.Exec(`
INSERT INTO storage_volumes (storage_pool_id, node_id, type, name, description, project_id, content_type)
 VALUES (?, ?, ?, ?, ?, (SELECT id FROM projects WHERE name = ?), ?)
`,
				poolID, c.nodeID, volumeType, volumeName, volumeDescription, project, contentType)
		}
		if err != nil {
			return err
		}

		volumeID, err = result.LastInsertId()
		if err != nil {
			return err
		}

		err = storageVolumeConfigAdd(tx.tx, volumeID, volumeConfig, false)
		if err != nil {
			return errors.Wrap(err, "Insert storage volume configuration")
		}

		return nil
	})
	if err != nil {
		volumeID = -1
	}

	return volumeID, err
}

// Return the ID of a storage volume on a given storage pool of a given storage
// volume type, on the given node.
func (c *Cluster) storagePoolVolumeGetTypeID(project string, volumeName string, volumeType int, poolID, nodeID int64) (int64, error) {
	var id int64
	err := c.Transaction(func(tx *ClusterTx) error {
		var err error
		id, err = tx.storagePoolVolumeGetTypeID(project, volumeName, volumeType, poolID, nodeID)
		return err
	})
	if err != nil {
		return -1, err
	}
	return id, nil
}

func (c *ClusterTx) storagePoolVolumeGetTypeID(project string, volumeName string, volumeType int, poolID, nodeID int64) (int64, error) {
	remoteDrivers := StorageRemoteDriverNames()

	s := fmt.Sprintf(`
SELECT storage_volumes_all.id
  FROM storage_volumes_all
  JOIN storage_pools ON storage_volumes_all.storage_pool_id = storage_pools.id
  JOIN projects ON storage_volumes_all.project_id = projects.id
  WHERE projects.name=?
    AND storage_volumes_all.storage_pool_id=?
    AND storage_volumes_all.name=?
	AND storage_volumes_all.type=?
	AND (storage_volumes_all.node_id=? OR storage_volumes_all.node_id IS NULL AND storage_pools.driver IN %s)`, query.Params(len(remoteDrivers)))

	args := []interface{}{project, poolID, volumeName, volumeType, nodeID}

	for _, driver := range remoteDrivers {
		args = append(args, driver)
	}

	result, err := query.SelectIntegers(c.tx, s, args...)
	if err != nil {
		return -1, err
	}

	if len(result) == 0 {
		return -1, ErrNoSuchObject
	}

	return int64(result[0]), nil
}

// GetStoragePoolNodeVolumeID gets the ID of a storage volume on a given storage pool
// of a given storage volume type and project, on the current node.
func (c *Cluster) GetStoragePoolNodeVolumeID(projectName string, volumeName string, volumeType int, poolID int64) (int64, error) {
	return c.storagePoolVolumeGetTypeID(projectName, volumeName, volumeType, poolID, c.nodeID)
}

// XXX: this was extracted from lxd/storage_volume_utils.go, we find a way to
//      factor it independently from both the db and main packages.
const (
	StoragePoolVolumeTypeContainer = iota
	StoragePoolVolumeTypeImage
	StoragePoolVolumeTypeCustom
	StoragePoolVolumeTypeVM
)

// Leave the string type in here! This guarantees that go treats this is as a
// typed string constant. Removing it causes go to treat these as untyped string
// constants which is not what we want.
const (
	StoragePoolVolumeTypeNameContainer string = "container"
	StoragePoolVolumeTypeNameVM        string = "virtual-machine"
	StoragePoolVolumeTypeNameImage     string = "image"
	StoragePoolVolumeTypeNameCustom    string = "custom"
)

// Content types.
const (
	StoragePoolVolumeContentTypeFS = iota
	StoragePoolVolumeContentTypeBlock
)

// Content type names.
const (
	StoragePoolVolumeContentTypeNameFS    string = "filesystem"
	StoragePoolVolumeContentTypeNameBlock string = "block"
)

// StorageVolumeArgs is a value object holding all db-related details about a
// storage volume.
type StorageVolumeArgs struct {
	ID   int64
	Name string

	// At least one of Type or TypeName must be set.
	Type     int
	TypeName string

	// At least one of PoolID or PoolName must be set.
	PoolID   int64
	PoolName string

	Snapshot bool

	Config       map[string]string
	Description  string
	CreationDate time.Time
	ExpiryDate   time.Time

	// At least on of ProjectID or ProjectName must be set.
	ProjectID   int64
	ProjectName string

	ContentType string
}

// GetStorageVolumeNodes returns the node info of all nodes on which the volume with the given name is defined.
// The volume name can be either a regular name or a volume snapshot name.
// If the volume is defined, but without a specific node, then the ErrNoClusterMember error is returned.
// If the volume is not found then the ErrNoSuchObject error is returned.
func (c *ClusterTx) GetStorageVolumeNodes(poolID int64, projectName string, volumeName string, volumeType int) ([]NodeInfo, error) {
	nodes := []NodeInfo{}

	dest := func(i int) []interface{} {
		nodes = append(nodes, NodeInfo{})
		return []interface{}{&nodes[i].ID, &nodes[i].Address, &nodes[i].Name}
	}

	sql := `
	SELECT coalesce(nodes.id,0) AS nodeID, coalesce(nodes.address,"") AS nodeAddress, coalesce(nodes.name,"") AS nodeName
	FROM storage_volumes_all
	JOIN projects ON projects.id = storage_volumes_all.project_id
	LEFT JOIN nodes ON storage_volumes_all.node_id=nodes.id
	WHERE storage_volumes_all.storage_pool_id=?
		AND projects.name=?
		AND storage_volumes_all.name=?
		AND storage_volumes_all.type=?
`
	stmt, err := c.tx.Prepare(sql)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	err = query.SelectObjects(stmt, dest, poolID, projectName, volumeName, volumeType)
	if err != nil {
		return nil, err
	}

	if len(nodes) == 0 {
		return nil, ErrNoSuchObject
	}

	for _, node := range nodes {
		// Volume is defined without a cluster member.
		if node.ID == 0 {
			return nil, ErrNoClusterMember
		}
	}

	nodeCount := len(nodes)
	if nodeCount == 0 {
		return nil, ErrNoSuchObject
	} else if nodeCount > 1 {
		driver, err := c.GetStoragePoolDriver(poolID)
		if err != nil {
			return nil, err
		}

		// Earlier schema versions created a volume DB record for each cluster member for remote storage
		// pools, so if the storage driver is one of those remote pools and the addressCount is >1 then we
		// take this to mean that the volume doesn't have an explicit cluster member and is therefore
		// equivalent to db.ErrNoClusterMember that is used in newer schemas where a single remote volume
		// DB record is created that is not associated to any single member.
		if StorageRemoteDriverNames == nil {
			return nil, fmt.Errorf("No remote storage drivers function defined")
		}

		remoteDrivers := StorageRemoteDriverNames()
		if shared.StringInSlice(driver, remoteDrivers) {
			return nil, ErrNoClusterMember
		}
	}

	return nodes, nil
}

// Return the name of the node a storage volume is on.
func (c *Cluster) storageVolumeNodeGet(volumeID int64) (string, error) {
	name := ""
	query := `
SELECT nodes.name FROM storage_volumes_all
  JOIN nodes ON nodes.id=storage_volumes_all.node_id
   WHERE storage_volumes_all.id=?
`
	inargs := []interface{}{volumeID}
	outargs := []interface{}{&name}

	err := dbQueryRowScan(c, query, inargs, outargs)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", ErrNoSuchObject
		}

		return "", err
	}

	return name, nil
}

// Get the config of a storage volume.
func (c *Cluster) storageVolumeConfigGet(volumeID int64, isSnapshot bool) (map[string]string, error) {
	var key, value string
	var query string
	if isSnapshot {
		query = "SELECT key, value FROM storage_volumes_snapshots_config WHERE storage_volume_snapshot_id=?"
	} else {
		query = "SELECT key, value FROM storage_volumes_config WHERE storage_volume_id=?"
	}
	inargs := []interface{}{volumeID}
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

// GetStorageVolumeDescription gets the description of a storage volume.
func (c *Cluster) GetStorageVolumeDescription(volumeID int64) (string, error) {
	description := sql.NullString{}
	query := "SELECT description FROM storage_volumes_all WHERE id=?"
	inargs := []interface{}{volumeID}
	outargs := []interface{}{&description}

	err := dbQueryRowScan(c, query, inargs, outargs)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", ErrNoSuchObject
		}
		return "", err
	}

	return description.String, nil
}

// getStorageVolumeContentType gets the content type of a storage volume.
func (c *Cluster) getStorageVolumeContentType(volumeID int64) (int, error) {
	var contentType int
	query := "SELECT content_type FROM storage_volumes_all WHERE id=?"
	inargs := []interface{}{volumeID}
	outargs := []interface{}{&contentType}

	err := dbQueryRowScan(c, query, inargs, outargs)
	if err != nil {
		if err == sql.ErrNoRows {
			return -1, ErrNoSuchObject
		}
		return -1, err
	}

	return contentType, nil
}

// GetNextStorageVolumeSnapshotIndex returns the index of the next snapshot of the storage
// volume with the given name should have.
//
// Note, the code below doesn't deal with snapshots of snapshots.
// To do that, we'll need to weed out based on # slashes in names
func (c *Cluster) GetNextStorageVolumeSnapshotIndex(pool, name string, typ int, pattern string) int {
	q := fmt.Sprintf(`
SELECT storage_volumes_snapshots.name FROM storage_volumes_snapshots
  JOIN storage_volumes ON storage_volumes_snapshots.storage_volume_id=storage_volumes.id
  JOIN storage_pools ON storage_volumes.storage_pool_id=storage_pools.id
 WHERE storage_volumes.type=?
   AND storage_volumes.name=?
   AND storage_pools.name=?
`)
	var numstr string
	inargs := []interface{}{typ, name, pool}
	outfmt := []interface{}{numstr}
	results, err := queryScan(c, q, inargs, outfmt)
	if err != nil {
		return 0
	}
	max := 0

	for _, r := range results {
		substr := r[0].(string)
		fields := strings.SplitN(pattern, "%d", 2)

		var num int
		count, err := fmt.Sscanf(substr, fmt.Sprintf("%s%%d%s", fields[0], fields[1]), &num)
		if err != nil || count != 1 {
			continue
		}
		if num >= max {
			max = num + 1
		}
	}

	return max
}

// Updates the description of a storage volume.
func storageVolumeDescriptionUpdate(tx *sql.Tx, volumeID int64, description string, isSnapshot bool) error {
	var table string
	if isSnapshot {
		table = "storage_volumes_snapshots"
	} else {
		table = "storage_volumes"
	}
	stmt := fmt.Sprintf("UPDATE %s SET description=? WHERE id=?", table)
	_, err := tx.Exec(stmt, description, volumeID)
	return err
}

// Add a new storage volume config into database.
func storageVolumeConfigAdd(tx *sql.Tx, volumeID int64, volumeConfig map[string]string, isSnapshot bool) error {
	var str string
	if isSnapshot {
		str = "INSERT INTO storage_volumes_snapshots_config (storage_volume_snapshot_id, key, value) VALUES(?, ?, ?)"
	} else {
		str = "INSERT INTO storage_volumes_config (storage_volume_id, key, value) VALUES(?, ?, ?)"
	}
	stmt, err := tx.Prepare(str)
	defer stmt.Close()
	if err != nil {
		return err
	}

	for k, v := range volumeConfig {
		if v == "" {
			continue
		}

		_, err = stmt.Exec(volumeID, k, v)
		if err != nil {
			return err
		}
	}

	return nil
}

// Delete storage volume config.
func storageVolumeConfigClear(tx *sql.Tx, volumeID int64, isSnapshot bool) error {
	var stmt string
	if isSnapshot {
		stmt = "DELETE FROM storage_volumes_snapshots_config WHERE storage_volume_snapshot_id=?"
	} else {
		stmt = "DELETE FROM storage_volumes_config WHERE storage_volume_id=?"
	}
	_, err := tx.Exec(stmt, volumeID)
	if err != nil {
		return err
	}

	return nil
}

// Get the IDs of all volumes with the given name and type associated with the
// given pool, regardless of their node_id column.
func storageVolumeIDsGet(tx *sql.Tx, project, volumeName string, volumeType int, poolID int64) ([]int64, error) {
	ids, err := query.SelectIntegers(tx, `
SELECT storage_volumes_all.id
  FROM storage_volumes_all
  JOIN projects ON projects.id = storage_volumes_all.project_id
 WHERE projects.name=?
   AND storage_volumes_all.name=?
   AND storage_volumes_all.type=?
   AND storage_volumes_all.storage_pool_id=?
`, project, volumeName, volumeType, poolID)
	if err != nil {
		return nil, err
	}
	ids64 := make([]int64, len(ids))
	for i, id := range ids {
		ids64[i] = int64(id)
	}
	return ids64, nil
}

// RemoveStorageVolumeImages removes the volumes associated with the images
// with the given fingerprints.
func (c *Cluster) RemoveStorageVolumeImages(fingerprints []string) error {
	stmt := fmt.Sprintf(
		"DELETE FROM storage_volumes WHERE type=? AND name NOT IN %s",
		query.Params(len(fingerprints)))
	args := []interface{}{StoragePoolVolumeTypeImage}
	for _, fingerprint := range fingerprints {
		args = append(args, fingerprint)
	}
	err := exec(c, stmt, args...)
	return err
}

// UpgradeStorageVolumConfigToLVMThinPoolNameKey upgrades the config keys of LVM
// volumes.
func (c *Cluster) UpgradeStorageVolumConfigToLVMThinPoolNameKey() error {
	err := exec(c, "UPDATE storage_pools_config SET key='lvm.thinpool_name' WHERE key='volume.lvm.thinpool_name';")
	if err != nil {
		return err
	}

	err = exec(c, "DELETE FROM storage_volumes_config WHERE key='lvm.thinpool_name';")
	if err != nil {
		return err
	}
	err = exec(c, "DELETE FROM storage_volumes_snapshots_config WHERE key='lvm.thinpool_name';")
	if err != nil {
		return err
	}

	return nil
}

// Convert a volume integer type code to its human-readable name.
func storagePoolVolumeTypeToName(volumeType int) (string, error) {
	switch volumeType {
	case StoragePoolVolumeTypeContainer:
		return StoragePoolVolumeTypeNameContainer, nil
	case StoragePoolVolumeTypeVM:
		return StoragePoolVolumeTypeNameVM, nil
	case StoragePoolVolumeTypeImage:
		return StoragePoolVolumeTypeNameImage, nil
	case StoragePoolVolumeTypeCustom:
		return StoragePoolVolumeTypeNameCustom, nil
	}

	return "", fmt.Errorf("Invalid storage volume type")
}

// Convert a volume integer content type code to its human-readable name.
func storagePoolVolumeContentTypeToName(contentType int) (string, error) {
	switch contentType {
	case StoragePoolVolumeContentTypeFS:
		return StoragePoolVolumeContentTypeNameFS, nil
	case StoragePoolVolumeContentTypeBlock:
		return StoragePoolVolumeContentTypeNameBlock, nil
	}

	return "", fmt.Errorf("Invalid storage volume content type")
}

// GetCustomVolumesInProject returns all custom volumes in the given project.
func (c *ClusterTx) GetCustomVolumesInProject(project string) ([]StorageVolumeArgs, error) {
	sql := `
SELECT storage_volumes.id, storage_volumes.name, storage_pools.name
FROM storage_volumes
JOIN storage_pools ON storage_pools.id = storage_volumes.storage_pool_id
JOIN projects ON projects.id = storage_volumes.project_id
WHERE storage_volumes.type = ? AND projects.name = ?
`
	stmt, err := c.tx.Prepare(sql)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	volumes := []StorageVolumeArgs{}
	dest := func(i int) []interface{} {
		volumes = append(volumes, StorageVolumeArgs{})
		return []interface{}{
			&volumes[i].ID,
			&volumes[i].Name,
			&volumes[i].PoolName,
		}
	}

	err = query.SelectObjects(stmt, dest, StoragePoolVolumeTypeCustom, project)
	if err != nil {
		return nil, errors.Wrap(err, "Fetch custom volumes")
	}

	for i, volume := range volumes {
		config, err := query.SelectConfig(c.tx, "storage_volumes_config", "storage_volume_id=?", volume.ID)
		if err != nil {
			return nil, errors.Wrap(err, "Fetch custom volume config")
		}
		volumes[i].Config = config
	}

	return volumes, nil
}
