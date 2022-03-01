//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lxc/lxd/shared"
)

// CreateStorageVolumeSnapshot creates a new storage volume snapshot attached to a given
// storage pool.
func (c *Cluster) CreateStorageVolumeSnapshot(project, volumeName, volumeDescription string, volumeType int, poolID int64, volumeConfig map[string]string, expiryDate time.Time) (int64, error) {
	var volumeID int64

	var snapshotName string
	parts := strings.Split(volumeName, shared.SnapshotDelimiter)
	volumeName = parts[0]
	snapshotName = parts[1]

	err := c.Transaction(func(tx *ClusterTx) error {
		// If we are creating a snapshot, figure out the volume
		// ID of the parent.
		parentID, err := tx.storagePoolVolumeGetTypeID(
			project, volumeName, volumeType, poolID, c.nodeID)
		if err != nil {
			return fmt.Errorf("Find parent volume: %w", err)
		}

		_, err = tx.tx.Exec("UPDATE sqlite_sequence SET seq = seq + 1 WHERE name = 'storage_volumes'")
		if err != nil {
			return fmt.Errorf("Increment storage volumes sequence: %w", err)
		}

		row := tx.tx.QueryRow("SELECT seq FROM sqlite_sequence WHERE name = 'storage_volumes' LIMIT 1")
		err = row.Scan(&volumeID)
		if err != nil {
			return err
		}

		_, err = tx.tx.Exec(
			"INSERT INTO storage_volumes_snapshots (id, storage_volume_id, name, description, expiry_date) VALUES (?, ?, ?, ?, ?)",
			volumeID, parentID, snapshotName, volumeDescription, expiryDate)
		if err != nil {
			return fmt.Errorf("Insert volume snapshot: %w", err)
		}

		err = storageVolumeConfigAdd(tx.tx, volumeID, volumeConfig, true)
		if err != nil {
			return fmt.Errorf("Insert storage volume configuration: %w", err)
		}

		return nil
	})
	if err != nil {
		volumeID = -1
	}

	return volumeID, err
}

// UpdateStorageVolumeSnapshot updates the storage volume snapshot attached to a given storage pool.
func (c *Cluster) UpdateStorageVolumeSnapshot(project, volumeName string, volumeType int, poolID int64, volumeDescription string, volumeConfig map[string]string, expiryDate time.Time) error {
	volumeID, _, err := c.GetLocalStoragePoolVolume(project, volumeName, volumeType, poolID)
	if err != nil {
		return err
	}

	if !strings.Contains(volumeName, shared.SnapshotDelimiter) {
		return fmt.Errorf("Volume is not a snapshot")
	}

	err = c.Transaction(func(tx *ClusterTx) error {
		err = storagePoolVolumeReplicateIfCeph(tx.tx, volumeID, project, volumeName, volumeType, poolID, func(volumeID int64) error {
			err = storageVolumeConfigClear(tx.tx, volumeID, true)
			if err != nil {
				return err
			}

			err = storageVolumeConfigAdd(tx.tx, volumeID, volumeConfig, true)
			if err != nil {
				return err
			}

			err = storageVolumeDescriptionUpdate(tx.tx, volumeID, volumeDescription, true)
			if err != nil {
				return err
			}

			return storageVolumeSnapshotExpiryDateUpdate(tx.tx, volumeID, expiryDate)
		})
		if err != nil {
			return err
		}
		return nil
	})

	return err
}

// GetStorageVolumeSnapshotsNames gets the snapshot names of a storage volume.
func (c *Cluster) GetStorageVolumeSnapshotsNames(volumeID int64) ([]string, error) {
	var snapshotName string
	query := "SELECT name FROM storage_volumes_snapshots WHERE storage_volume_id=?"
	inargs := []interface{}{volumeID}
	outargs := []interface{}{snapshotName}

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

// GetStorageVolumeSnapshotWithID returns the volume snapshot with the given ID.
func (c *Cluster) GetStorageVolumeSnapshotWithID(snapshotID int) (StorageVolumeArgs, error) {
	args := StorageVolumeArgs{}
	q := `
SELECT
	volumes.id,
	volumes.name,
	storage_pools.name,
	volumes.type,
	projects.name
FROM storage_volumes_all AS volumes
JOIN projects ON projects.id=volumes.project_id
JOIN storage_pools ON storage_pools.id=volumes.storage_pool_id
WHERE volumes.id=?
`
	arg1 := []interface{}{snapshotID}
	outfmt := []interface{}{&args.ID, &args.Name, &args.PoolName, &args.Type, &args.ProjectName}
	err := dbQueryRowScan(c, q, arg1, outfmt)
	if err != nil {
		if err == sql.ErrNoRows {
			return args, ErrNoSuchObject
		}

		return args, err
	}

	if !strings.Contains(args.Name, shared.SnapshotDelimiter) {
		return args, fmt.Errorf("Volume is not a snapshot")
	}

	args.TypeName = StoragePoolVolumeTypeNames[args.Type]

	return args, nil
}

// GetStorageVolumeSnapshotExpiry gets the expiry date of a storage volume snapshot.
func (c *Cluster) GetStorageVolumeSnapshotExpiry(volumeID int64) (time.Time, error) {
	var expiry time.Time

	query := "SELECT expiry_date FROM storage_volumes_snapshots WHERE id=?"
	inargs := []interface{}{volumeID}
	outargs := []interface{}{&expiry}

	err := dbQueryRowScan(c, query, inargs, outargs)
	if err != nil {
		if err == sql.ErrNoRows {
			return expiry, ErrNoSuchObject
		}
		return expiry, err
	}

	return expiry, nil
}

// GetExpiredStorageVolumeSnapshots returns a list of expired volume snapshots.
func (c *Cluster) GetExpiredStorageVolumeSnapshots() ([]StorageVolumeArgs, error) {
	q := `
	SELECT storage_volumes.name, storage_volumes_snapshots.name, storage_volumes_snapshots.expiry_date, storage_pools.name, projects.name
	FROM storage_volumes_snapshots
	JOIN storage_volumes ON storage_volumes_snapshots.storage_volume_id = storage_volumes.id
	JOIN storage_pools ON storage_volumes.storage_pool_id = storage_pools.id
	JOIN projects ON storage_volumes.project_id = projects.id
	WHERE storage_volumes.type = ?`

	var snapshots []StorageVolumeArgs

	err := c.Transaction(func(tx *ClusterTx) error {
		return tx.QueryScan(q, func(scan func(dest ...interface{}) error) error {
			var snap StorageVolumeArgs
			var snapName string
			var volName string
			var expiryTime sql.NullTime

			err := scan(&volName, &snapName, &expiryTime, &snap.PoolName, &snap.ProjectName)
			if err != nil {
				return err
			}

			snap.Name = volName + shared.SnapshotDelimiter + snapName
			snap.ExpiryDate = expiryTime.Time // Convert nulls to zero.

			// Since zero time causes some issues due to timezones, we check the
			// unix timestamp instead of IsZero().
			if snap.ExpiryDate.Unix() <= 0 {
				return nil // Backup doesn't expire.
			}

			// Check if snapshot has expired.
			if time.Now().Unix()-snap.ExpiryDate.Unix() >= 0 {
				snapshots = append(snapshots, snap)
			}

			return nil
		}, StoragePoolVolumeTypeCustom)
	})
	if err != nil {
		return nil, err
	}

	return snapshots, nil
}

// Updates the expiry date of a storage volume snapshot.
func storageVolumeSnapshotExpiryDateUpdate(tx *sql.Tx, volumeID int64, expiryDate time.Time) error {
	stmt := fmt.Sprintf("UPDATE storage_volumes_snapshots SET expiry_date=? WHERE id=?")
	_, err := tx.Exec(stmt, expiryDate, volumeID)
	return err
}
