//go:build linux && cgo && !agent

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/version"
)

// GetStoragePoolVolumesWithType return a list of all volumes of the given type.
// If memberSpecific is true, then the search is restricted to volumes that belong to this member or belong to
// all members.
func (c *ClusterTx) GetStoragePoolVolumesWithType(ctx context.Context, volumeType cluster.StoragePoolVolumeType, memberSpecific bool) ([]StorageVolumeArgs, error) {
	var q strings.Builder
	q.WriteString(`
SELECT
	storage_volumes.id,
	storage_volumes.name,
	storage_volumes.description,
	storage_volumes.creation_date,
	storage_pools.name,
	projects.name,
	IFNULL(storage_volumes.node_id, -1)
FROM storage_volumes
JOIN storage_pools ON storage_pools.id = storage_volumes.storage_pool_id
JOIN projects ON projects.id = storage_volumes.project_id
WHERE storage_volumes.type = ?
`)

	args := []any{volumeType}

	if memberSpecific {
		q.WriteString("AND (storage_volumes.node_id = ? OR storage_volumes.node_id IS NULL) ")
		args = append(args, c.nodeID)
	}

	result := []StorageVolumeArgs{}
	err := query.Scan(ctx, c.Tx(), q.String(), func(scan func(dest ...any) error) error {
		entry := StorageVolumeArgs{}

		err := scan(&entry.ID, &entry.Name, &entry.Description, &entry.CreationDate, &entry.PoolName, &entry.ProjectName, &entry.NodeID)
		if err != nil {
			return err
		}

		result = append(result, entry)
		return nil
	}, args...)
	if err != nil {
		return nil, err
	}

	for i := range result {
		result[i].Config, err = c.storageVolumeConfigGet(ctx, result[i].ID, false)
		if err != nil {
			return nil, err
		}
	}

	return result, nil
}

// GetStoragePoolVolumeWithID returns the volume with the given ID.
func (c *ClusterTx) GetStoragePoolVolumeWithID(ctx context.Context, volumeID int) (StorageVolumeArgs, error) {
	var response StorageVolumeArgs
	var rawVolumeType = int(-1)

	stmt := `
SELECT
	storage_volumes.id,
	storage_volumes.name,
	storage_volumes.description,
	storage_volumes.creation_date,
	storage_volumes.type,
	IFNULL(storage_volumes.node_id, -1),
	storage_pools.name,
	projects.name
FROM storage_volumes
JOIN storage_pools ON storage_pools.id = storage_volumes.storage_pool_id
JOIN projects ON projects.id = storage_volumes.project_id
LEFT JOIN nodes ON nodes.id = storage_volumes.node_id
WHERE storage_volumes.id = ?
`

	err := c.tx.QueryRowContext(ctx, stmt, volumeID).Scan(&response.ID, &response.Name, &response.Description, &response.CreationDate, &rawVolumeType, &response.NodeID, &response.PoolName, &response.ProjectName)
	if err != nil {
		if err == sql.ErrNoRows {
			return StorageVolumeArgs{}, api.StatusErrorf(http.StatusNotFound, "Storage volume not found")
		}

		return StorageVolumeArgs{}, err
	}

	response.Config, err = c.storageVolumeConfigGet(ctx, response.ID, false)
	if err != nil {
		return StorageVolumeArgs{}, err
	}

	response.Type, err = cluster.StoragePoolVolumeTypeFromInt(rawVolumeType)
	if err != nil {
		return StorageVolumeArgs{}, err
	}

	response.TypeName = response.Type.String()

	return response, nil
}

// GetStoragePoolVolumeWithUUID returns the volume with the given UUID.
func (c *ClusterTx) GetStoragePoolVolumeWithUUID(ctx context.Context, volumeUUID string) (StorageVolumeArgs, error) {
	var response StorageVolumeArgs
	var rawVolumeType = int(-1)

	stmt := `
SELECT
	storage_volumes.id,
	storage_volumes.name,
	storage_volumes.description,
	storage_volumes.creation_date,
	storage_volumes.type,
	IFNULL(storage_volumes.node_id, -1),
	storage_pools.name,
	projects.name
FROM storage_volumes
JOIN storage_volumes_config ON storage_volumes.id = storage_volumes_config.storage_volume_id
JOIN storage_pools ON storage_pools.id = storage_volumes.storage_pool_id
JOIN projects ON projects.id = storage_volumes.project_id
LEFT JOIN nodes ON nodes.id = storage_volumes.node_id
WHERE storage_volumes_config.key = "volatile.uuid" AND storage_volumes_config.value = ?
`

	err := c.tx.QueryRowContext(ctx, stmt, volumeUUID).Scan(&response.ID, &response.Name, &response.Description, &response.CreationDate, &rawVolumeType, &response.NodeID, &response.PoolName, &response.ProjectName)
	if err != nil {
		if err == sql.ErrNoRows {
			return StorageVolumeArgs{}, api.StatusErrorf(http.StatusNotFound, "Storage volume not found")
		}

		return StorageVolumeArgs{}, err
	}

	response.Config, err = c.storageVolumeConfigGet(ctx, response.ID, false)
	if err != nil {
		return StorageVolumeArgs{}, err
	}

	response.Type, err = cluster.StoragePoolVolumeTypeFromInt(rawVolumeType)
	if err != nil {
		return StorageVolumeArgs{}, err
	}

	response.TypeName = response.Type.String()

	return response, nil
}

// StorageVolumeFilter used for filtering storage volumes with GetStorageVolumes().
type StorageVolumeFilter struct {
	Type    *cluster.StoragePoolVolumeType
	Project *string
	Name    *string
	PoolID  *int64
}

// StorageVolume represents a database storage volume record.
type StorageVolume struct {
	api.StorageVolume

	ID int64
}

// GetStorageVolumes returns all storage volumes.
// If there are no volumes, it returns an empty list and no error.
// Accepts filters for narrowing down the results returned. If memberSpecific is true, then the search is
// restricted to volumes that belong to this member or belong to all members.
func (c *ClusterTx) GetStorageVolumes(ctx context.Context, memberSpecific bool, filters ...StorageVolumeFilter) ([]*StorageVolume, error) {
	var q = &strings.Builder{}
	args := []any{}

	q.WriteString(`
		SELECT
			projects.name as project,
			storage_volumes_all.id,
			storage_volumes_all.name,
			IFNULL(nodes.name, "") as location,
			storage_volumes_all.type,
			storage_volumes_all.content_type,
			storage_volumes_all.description,
			storage_volumes_all.creation_date,
			storage_pools.name as pool
		FROM storage_volumes_all
		JOIN projects ON projects.id = storage_volumes_all.project_id
		LEFT JOIN nodes ON nodes.id = storage_volumes_all.node_id
		JOIN storage_pools ON storage_pools.id = storage_volumes_all.storage_pool_id
	`)

	if len(filters) > 0 {
		q.WriteString("WHERE (")

		for i, filter := range filters {
			// Validate filter.
			if filter.Name != nil && filter.Type == nil {
				return nil, errors.New("Cannot filter by volume name if volume type not specified")
			}

			if filter.Name != nil && filter.Project == nil {
				return nil, errors.New("Cannot filter by volume name if volume project not specified")
			}

			var qFilters []string

			if filter.Type != nil {
				qFilters = append(qFilters, "storage_volumes_all.type = ?")
				args = append(args, *filter.Type)
			}

			if filter.PoolID != nil {
				qFilters = append(qFilters, "storage_volumes_all.storage_pool_id = ?")
				args = append(args, *filter.PoolID)
			}

			if filter.Project != nil {
				qFilters = append(qFilters, "projects.name = ?")
				args = append(args, *filter.Project)
			}

			if filter.Name != nil {
				qFilters = append(qFilters, "storage_volumes_all.name = ?")
				args = append(args, *filter.Name)
			}

			if qFilters == nil {
				return nil, errors.New("Invalid storage volume filter")
			}

			if i > 0 {
				q.WriteString(" OR ")
			}

			q.WriteString("(" + strings.Join(qFilters, " AND ") + ")")
		}

		q.WriteString(")")

		if memberSpecific {
			if len(filters) > 0 {
				q.WriteString("AND (storage_volumes_all.node_id = ? OR storage_volumes_all.node_id IS NULL) ")
			} else {
				q.WriteString("WHERE (storage_volumes_all.node_id = ? OR storage_volumes_all.node_id IS NULL) ")
			}

			args = append(args, c.nodeID)
		}
	}

	var err error
	var volumes []*StorageVolume

	err = query.Scan(ctx, c.Tx(), q.String(), func(scan func(dest ...any) error) error {
		var rawVolumeType = int(-1)
		var rawContentType = int(-1)
		var vol StorageVolume

		err := scan(&vol.Project, &vol.ID, &vol.Name, &vol.Location, &rawVolumeType, &rawContentType, &vol.Description, &vol.CreatedAt, &vol.Pool)
		if err != nil {
			return err
		}

		volumeType, err := cluster.StoragePoolVolumeTypeFromInt(rawVolumeType)
		if err != nil {
			return err
		}

		contentType, err := cluster.StoragePoolVolumeContentTypeFromInt(rawContentType)
		if err != nil {
			return err
		}

		vol.Type = volumeType.String()
		vol.ContentType = contentType.String()

		volumes = append(volumes, &vol)

		return nil
	}, args...)
	if err != nil {
		return nil, err
	}

	// Populate config.
	for _, volume := range volumes {
		volume.Config, err = c.storageVolumeConfigGet(ctx, volume.ID, shared.IsSnapshot(volume.Name))
		if err != nil {
			return nil, fmt.Errorf("Failed loading volume config for %q: %w", volume.Name, err)
		}
	}

	return volumes, nil
}

// GetStoragePoolVolume returns the storage volume attached to a given storage pool.
func (c *ClusterTx) GetStoragePoolVolume(ctx context.Context, poolID int64, projectName string, volumeType cluster.StoragePoolVolumeType, volumeName string, memberSpecific bool) (*StorageVolume, error) {
	filters := []StorageVolumeFilter{{
		Project: &projectName,
		Type:    &volumeType,
		Name:    &volumeName,
		PoolID:  &poolID,
	}}

	volumes, err := c.GetStorageVolumes(ctx, memberSpecific, filters...)
	volumesLen := len(volumes)
	if (err == nil && volumesLen <= 0) || errors.Is(err, sql.ErrNoRows) {
		return nil, api.StatusErrorf(http.StatusNotFound, "Storage volume not found")
	} else if err == nil && volumesLen > 1 {
		return nil, api.StatusErrorf(http.StatusConflict, "Storage volume found on more than one cluster member. Please target a specific member")
	} else if err != nil {
		return nil, err
	}

	return volumes[0], nil
}

// GetLocalStoragePoolVolumeSnapshotsWithType get all snapshots of a storage volume
// attached to a given storage pool of a given volume type, on the local member.
// Returns snapshots slice ordered by when they were created, oldest first.
func (c *ClusterTx) GetLocalStoragePoolVolumeSnapshotsWithType(ctx context.Context, projectName string, volumeName string, volumeType cluster.StoragePoolVolumeType, poolID int64) ([]StorageVolumeArgs, error) {
	remoteDrivers := StorageRemoteDriverNames()

	// ORDER BY creation_date and then id is important here as the users of this function can expect that the
	// results will be returned in the order that the snapshots were created. This is specifically used
	// during migration to ensure that the storage engines can re-create snapshots using the
	// correct deltas.
	queryStr := `
  SELECT
    storage_volumes_snapshots.id, storage_volumes_snapshots.name, storage_volumes_snapshots.description,
    storage_volumes_snapshots.creation_date, storage_volumes_snapshots.expiry_date,
    storage_volumes.content_type
  FROM storage_volumes_snapshots
  JOIN storage_volumes ON storage_volumes_snapshots.storage_volume_id = storage_volumes.id
  JOIN projects ON projects.id=storage_volumes.project_id
  JOIN storage_pools ON storage_pools.id=storage_volumes.storage_pool_id
  WHERE storage_volumes.storage_pool_id=?
    AND storage_volumes.type=?
    AND storage_volumes.name=?
    AND projects.name=?
    AND (storage_volumes.node_id=? OR storage_volumes.node_id IS NULL AND storage_pools.driver IN ` + query.Params(len(remoteDrivers)) + `)
  ORDER BY storage_volumes_snapshots.creation_date, storage_volumes_snapshots.id`

	args := []any{poolID, volumeType, volumeName, projectName, c.nodeID}
	for _, driver := range remoteDrivers {
		args = append(args, driver)
	}

	var snapshots []StorageVolumeArgs

	err := query.Scan(ctx, c.Tx(), queryStr, func(scan func(dest ...any) error) error {
		var s StorageVolumeArgs
		var snapName string
		var expiryDate sql.NullTime
		var rawContentType = int(-1)

		err := scan(&s.ID, &snapName, &s.Description, &s.CreationDate, &expiryDate, &rawContentType)
		if err != nil {
			return err
		}

		s.Name = volumeName + shared.SnapshotDelimiter + snapName
		s.PoolID = poolID
		s.ProjectName = projectName
		s.Snapshot = true
		s.ExpiryDate = expiryDate.Time // Convert null to zero.

		contentType, err := cluster.StoragePoolVolumeContentTypeFromInt(rawContentType)
		if err != nil {
			return err
		}

		s.ContentType = contentType.String()

		snapshots = append(snapshots, s)

		return nil
	}, args...)
	if err != nil {
		return nil, err
	}

	// Populate config.
	for i := range snapshots {
		err := storageVolumeSnapshotConfig(ctx, c, snapshots[i].ID, &snapshots[i])
		if err != nil {
			return nil, err
		}
	}

	return snapshots, nil
}

// storageVolumeSnapshotConfig populates the config map of the Storage Volume Snapshot with the given ID.
func storageVolumeSnapshotConfig(ctx context.Context, tx *ClusterTx, volumeSnapshotID int64, volume *StorageVolumeArgs) error {
	q := "SELECT key, value FROM storage_volumes_snapshots_config WHERE storage_volume_snapshot_id = ?"

	volume.Config = make(map[string]string)
	return query.Scan(ctx, tx.Tx(), q, func(scan func(dest ...any) error) error {
		var key, value string

		err := scan(&key, &value)
		if err != nil {
			return err
		}

		_, found := volume.Config[key]
		if found {
			return fmt.Errorf("Duplicate config row found for key %q for storage volume snapshot ID %d", key, volumeSnapshotID)
		}

		volume.Config[key] = value

		return nil
	}, volumeSnapshotID)
}

// UpdateStoragePoolVolume updates the storage volume attached to a given storage pool.
func (c *ClusterTx) UpdateStoragePoolVolume(ctx context.Context, projectName string, volumeName string, volumeType cluster.StoragePoolVolumeType, poolID int64, volumeDescription string, volumeConfig map[string]string) error {
	isSnapshot := strings.Contains(volumeName, shared.SnapshotDelimiter)

	volume, err := c.GetStoragePoolVolume(ctx, poolID, projectName, volumeType, volumeName, true)
	if err != nil {
		return err
	}

	err = storageVolumeConfigClear(c.tx, volume.ID, isSnapshot)
	if err != nil {
		return err
	}

	err = storageVolumeConfigAdd(c.tx, volume.ID, volumeConfig, isSnapshot)
	if err != nil {
		return err
	}

	err = storageVolumeDescriptionUpdate(c.tx, volume.ID, volumeDescription, isSnapshot)
	if err != nil {
		return err
	}

	return nil
}

// RemoveStoragePoolVolume deletes the storage volume attached to a given storage
// pool.
func (c *ClusterTx) RemoveStoragePoolVolume(ctx context.Context, projectName string, volumeName string, volumeType cluster.StoragePoolVolumeType, poolID int64) error {
	isSnapshot := strings.Contains(volumeName, shared.SnapshotDelimiter)
	var stmt string
	if isSnapshot {
		stmt = "DELETE FROM storage_volumes_snapshots WHERE id=?"
	} else {
		stmt = "DELETE FROM storage_volumes WHERE id=?"
	}

	volume, err := c.GetStoragePoolVolume(ctx, poolID, projectName, volumeType, volumeName, true)
	if err != nil {
		return err
	}

	_, err = c.tx.ExecContext(ctx, stmt, volume.ID)
	if err != nil {
		return err
	}

	return nil
}

// RenameStoragePoolVolume renames the storage volume attached to a given storage pool.
func (c *ClusterTx) RenameStoragePoolVolume(ctx context.Context, projectName string, oldVolumeName string, newVolumeName string, volumeType cluster.StoragePoolVolumeType, poolID int64) error {
	isSnapshot := strings.Contains(oldVolumeName, shared.SnapshotDelimiter)
	var stmt string
	if isSnapshot {
		_, newVolumeName, _ = strings.Cut(newVolumeName, shared.SnapshotDelimiter)
		stmt = "UPDATE storage_volumes_snapshots SET name=? WHERE id=?"
	} else {
		stmt = "UPDATE storage_volumes SET name=? WHERE id=?"
	}

	volume, err := c.GetStoragePoolVolume(ctx, poolID, projectName, volumeType, oldVolumeName, true)
	if err != nil {
		return err
	}

	_, err = c.tx.ExecContext(ctx, stmt, newVolumeName, volume.ID)
	if err != nil {
		return err
	}

	return nil
}

// CreateStoragePoolVolume creates a new storage volume attached to a given storage pool.
func (c *ClusterTx) CreateStoragePoolVolume(ctx context.Context, projectName string, volumeName string, volumeDescription string, volumeType cluster.StoragePoolVolumeType, poolID int64, volumeConfig map[string]string, contentType cluster.StoragePoolVolumeContentType, creationDate time.Time) (int64, error) {
	var volumeID int64

	if shared.IsSnapshot(volumeName) {
		return -1, errors.New("Volume name may not be a snapshot")
	}

	remoteDrivers := StorageRemoteDriverNames()

	driver, err := c.GetStoragePoolDriver(ctx, poolID)
	if err != nil {
		return -1, err
	}

	var result sql.Result

	if slices.Contains(remoteDrivers, driver) {
		result, err = c.tx.ExecContext(ctx, `
INSERT INTO storage_volumes (storage_pool_id, type, name, description, project_id, content_type, creation_date)
 VALUES (?, ?, ?, ?, (SELECT id FROM projects WHERE name = ?), ?, ?)
`,
			poolID, volumeType, volumeName, volumeDescription, projectName, contentType, creationDate)
	} else {
		result, err = c.tx.ExecContext(ctx, `
INSERT INTO storage_volumes (storage_pool_id, node_id, type, name, description, project_id, content_type, creation_date)
 VALUES (?, ?, ?, ?, ?, (SELECT id FROM projects WHERE name = ?), ?, ?)
`,
			poolID, c.nodeID, volumeType, volumeName, volumeDescription, projectName, contentType, creationDate)
	}

	if err != nil {
		return -1, err
	}

	volumeID, err = result.LastInsertId()
	if err != nil {
		return -1, err
	}

	err = storageVolumeConfigAdd(c.tx, volumeID, volumeConfig, false)
	if err != nil {
		return -1, fmt.Errorf("Failed inserting storage volume record configuration: %w", err)
	}

	return volumeID, err
}

// Return the ID of a storage volume on a given storage pool of a given storage
// volume type, on the given node.
func (c *ClusterTx) storagePoolVolumeGetTypeID(ctx context.Context, project string, volumeName string, volumeType cluster.StoragePoolVolumeType, poolID, nodeID int64) (int64, error) {
	remoteDrivers := StorageRemoteDriverNames()

	s := `
SELECT storage_volumes_all.id
  FROM storage_volumes_all
  JOIN storage_pools ON storage_volumes_all.storage_pool_id = storage_pools.id
  JOIN projects ON storage_volumes_all.project_id = projects.id
  WHERE projects.name=?
    AND storage_volumes_all.storage_pool_id=?
    AND storage_volumes_all.name=?
	AND storage_volumes_all.type=?
	AND (storage_volumes_all.node_id=? OR storage_volumes_all.node_id IS NULL AND storage_pools.driver IN ` + query.Params(len(remoteDrivers)) + `)`

	args := []any{project, poolID, volumeName, volumeType, nodeID}

	for _, driver := range remoteDrivers {
		args = append(args, driver)
	}

	result, err := query.SelectIntegers(ctx, c.tx, s, args...)
	if err != nil {
		return -1, err
	}

	if len(result) == 0 {
		return -1, api.StatusErrorf(http.StatusNotFound, "Storage volume not found")
	}

	return int64(result[0]), nil
}

// GetStoragePoolNodeVolumeID gets the ID of a storage volume on a given storage pool
// of a given storage volume type and project, on the current node.
func (c *ClusterTx) GetStoragePoolNodeVolumeID(ctx context.Context, projectName string, volumeName string, volumeType cluster.StoragePoolVolumeType, poolID int64) (int64, error) {
	return c.storagePoolVolumeGetTypeID(ctx, projectName, volumeName, volumeType, poolID, c.nodeID)
}

// StorageVolumeArgs is a value object holding all db-related details about a
// storage volume.
type StorageVolumeArgs struct {
	ID   int64
	Name string

	// At least one of Type or TypeName must be set.
	Type     cluster.StoragePoolVolumeType
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

	NodeID int64
}

// GetStorageVolumeNodes returns the node info of all nodes on which the volume with the given name is defined.
// The volume name can be either a regular name or a volume snapshot name.
// If the volume is defined, but without a specific node, then the ErrNoClusterMember error is returned.
// If the volume is not found then an api.StatusError with code set to http.StatusNotFound is returned.
func (c *ClusterTx) GetStorageVolumeNodes(ctx context.Context, poolID int64, projectName string, volumeName string, volumeType cluster.StoragePoolVolumeType) ([]NodeInfo, error) {
	nodes := []NodeInfo{}

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

	err := query.Scan(ctx, c.tx, sql, func(scan func(dest ...any) error) error {
		node := NodeInfo{}
		err := scan(&node.ID, &node.Address, &node.Name)
		if err != nil {
			return err
		}

		nodes = append(nodes, node)

		return nil
	}, poolID, projectName, volumeName, volumeType)
	if err != nil {
		return nil, err
	}

	for _, node := range nodes {
		// Volume is defined without a cluster member.
		if node.ID == 0 {
			return nil, ErrNoClusterMember
		}
	}

	nodeCount := len(nodes)
	if nodeCount == 0 {
		return nil, api.StatusErrorf(http.StatusNotFound, "Storage volume not found")
	} else if nodeCount > 1 {
		driver, err := c.GetStoragePoolDriver(ctx, poolID)
		if err != nil {
			return nil, err
		}

		// Earlier schema versions created a volume DB record for each cluster member for remote storage
		// pools, so if the storage driver is one of those remote pools and the addressCount is >1 then we
		// take this to mean that the volume doesn't have an explicit cluster member and is therefore
		// equivalent to db.ErrNoClusterMember that is used in newer schemas where a single remote volume
		// DB record is created that is not associated to any single member.
		if StorageRemoteDriverNames == nil {
			return nil, errors.New("No remote storage drivers function defined")
		}

		remoteDrivers := StorageRemoteDriverNames()
		if slices.Contains(remoteDrivers, driver) {
			return nil, ErrNoClusterMember
		}
	}

	return nodes, nil
}

// Get the config of a storage volume.
func (c *ClusterTx) storageVolumeConfigGet(ctx context.Context, volumeID int64, isSnapshot bool) (map[string]string, error) {
	var queryStr string
	if isSnapshot {
		queryStr = "SELECT key, value FROM storage_volumes_snapshots_config WHERE storage_volume_snapshot_id=?"
	} else {
		queryStr = "SELECT key, value FROM storage_volumes_config WHERE storage_volume_id=?"
	}

	config := map[string]string{}
	err := query.Scan(ctx, c.Tx(), queryStr, func(scan func(dest ...any) error) error {
		var key string
		var value string

		err := scan(&key, &value)
		if err != nil {
			return err
		}

		config[key] = value

		return nil
	}, volumeID)
	if err != nil {
		return nil, err
	}

	return config, nil
}

// GetNextStorageVolumeSnapshotIndex returns the index of the next snapshot of the storage
// volume with the given name should have.
//
// Note, the code below doesn't deal with snapshots of snapshots.
// To do that, we'll need to weed out based on # slashes in names.
func (c *ClusterTx) GetNextStorageVolumeSnapshotIndex(ctx context.Context, pool, name string, typ cluster.StoragePoolVolumeType, pattern string) (nextIndex int) {
	remoteDrivers := StorageRemoteDriverNames()

	q := `
SELECT storage_volumes_snapshots.name FROM storage_volumes_snapshots
  JOIN storage_volumes ON storage_volumes_snapshots.storage_volume_id=storage_volumes.id
  JOIN storage_pools ON storage_volumes.storage_pool_id=storage_pools.id
 WHERE storage_volumes.type=?
   AND storage_volumes.name=?
   AND storage_pools.name=?
   AND (storage_volumes.node_id=? OR storage_volumes.node_id IS NULL AND storage_pools.driver IN ` + query.Params(len(remoteDrivers)) + `)`

	inargs := []any{typ, name, pool, c.nodeID}
	for _, driver := range remoteDrivers {
		inargs = append(inargs, driver)
	}

	if !strings.Contains(pattern, "%d") {
		return 0
	}

	err := query.Scan(ctx, c.tx, q, func(scan func(dest ...any) error) error {
		var substr string
		err := scan(&substr)
		if err != nil {
			return err
		}

		var num int
		count, err := fmt.Sscanf(substr, pattern, &num)
		if err != nil || count != 1 {
			return nil
		}

		if num >= nextIndex {
			nextIndex = num + 1
		}

		return nil
	}, inargs...)
	if err != nil {
		return 0
	}

	return nextIndex
}

// Updates the description of a storage volume.
func storageVolumeDescriptionUpdate(tx *sql.Tx, volumeID int64, description string, isSnapshot bool) error {
	var table string
	if isSnapshot {
		table = "storage_volumes_snapshots"
	} else {
		table = "storage_volumes"
	}

	stmt := "UPDATE " + table + " SET description=? WHERE id=?"
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
	if err != nil {
		return err
	}

	defer func() { _ = stmt.Close() }()

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

// GetCustomVolumesInProject returns all custom volumes in the given project.
func (c *ClusterTx) GetCustomVolumesInProject(ctx context.Context, project string) ([]StorageVolumeArgs, error) {
	sql := `
SELECT
	storage_volumes.id,
	storage_volumes.name,
	storage_volumes.creation_date,
	storage_pools.name,
	IFNULL(storage_volumes.node_id, -1)
FROM storage_volumes
JOIN storage_pools ON storage_pools.id = storage_volumes.storage_pool_id
JOIN projects ON projects.id = storage_volumes.project_id
WHERE storage_volumes.type = ? AND projects.name = ?
`

	volumes := []StorageVolumeArgs{}
	err := query.Scan(ctx, c.tx, sql, func(scan func(dest ...any) error) error {
		volume := StorageVolumeArgs{}
		err := scan(&volume.ID, &volume.Name, &volume.CreationDate, &volume.PoolName, &volume.NodeID)
		if err != nil {
			return err
		}

		volumes = append(volumes, volume)

		return nil
	}, cluster.StoragePoolVolumeTypeCustom, project)
	if err != nil {
		return nil, fmt.Errorf("Fetch custom volumes: %w", err)
	}

	for i, volume := range volumes {
		config, err := query.SelectConfig(ctx, c.tx, "storage_volumes_config", "storage_volume_id=?", volume.ID)
		if err != nil {
			return nil, fmt.Errorf("Fetch custom volume config: %w", err)
		}

		volumes[i].Config = config
	}

	return volumes, nil
}

// GetStorageVolumeURIs returns the URIs of the storage volumes, specifying
// target node if applicable.
func (c *ClusterTx) GetStorageVolumeURIs(ctx context.Context, project string) ([]string, error) {
	volInfo, err := c.GetCustomVolumesInProject(ctx, project)
	if err != nil {
		return nil, err
	}

	uris := []string{}
	for _, info := range volInfo {
		uri := api.NewURL().Path(version.APIVersion, "storage-pools", info.PoolName, "volumes", "custom", info.Name).Project(project)

		// Skip checking nodes if node_id is NULL.
		if info.NodeID != -1 {
			nodeInfo, err := c.GetNodes(ctx)
			if err != nil {
				return nil, err
			}

			for _, node := range nodeInfo {
				if node.ID == info.NodeID {
					uri.Target(node.Name)
					break
				}
			}
		}

		uris = append(uris, uri.String())
	}

	return uris, nil
}

// UpdateStorageVolumeNode changes the name of a storage volume and the cluster member hosting it.
// It's meant to be used when moving a storage volume backed by ceph from one cluster node to another.
func (c *ClusterTx) UpdateStorageVolumeNode(ctx context.Context, projectName string, oldName string, newName string, newMemberName string, poolID int64, volumeType cluster.StoragePoolVolumeType) error {
	volume, err := c.GetStoragePoolVolume(ctx, poolID, projectName, volumeType, oldName, false)
	if err != nil {
		return err
	}

	member, err := c.GetNodeByName(ctx, newMemberName)
	if err != nil {
		return fmt.Errorf("Failed to get new member %q info: %w", newMemberName, err)
	}

	stmt := "UPDATE storage_volumes SET node_id=?, name=? WHERE id=?"
	result, err := c.tx.Exec(stmt, member.ID, newName, volume.ID)
	if err != nil {
		return fmt.Errorf("Failed to update volumes's name and member ID: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("Failed to get rows affected by volume update: %w", err)
	}

	if n != 1 {
		return fmt.Errorf("Unexpected number of updated rows in storage_volumes table: %d", n)
	}

	return nil
}
