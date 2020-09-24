// +build linux,cgo,!agent

package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/shared"
)

// CreateStorageVolumeSnapshot creates a new storage volume snapshot attached to a given
// storage pool.
func (c *Cluster) CreateStorageVolumeSnapshot(project, volumeName, volumeDescription string, volumeType int, poolID int64, volumeConfig map[string]string, expiryDate time.Time) (int64, error) {
	var thisVolumeID int64

	var snapshotName string
	parts := strings.Split(volumeName, shared.SnapshotDelimiter)
	volumeName = parts[0]
	snapshotName = parts[1]

	err := c.Transaction(func(tx *ClusterTx) error {
		nodeIDs := []int{int(c.nodeID)}
		driver, err := storagePoolDriverGet(tx.tx, poolID)
		if err != nil {
			return err
		}

		// If the driver is ceph, create a volume entry for each node.
		if driver == "ceph" || driver == "cephfs" {
			nodeIDs, err = query.SelectIntegers(tx.tx, "SELECT id FROM nodes")
			if err != nil {
				return err
			}
		}

		for _, nodeID := range nodeIDs {
			var volumeID int64

			// If we are creating a snapshot, figure out the volume
			// ID of the parent.
			parentID, err := tx.storagePoolVolumeGetTypeID(
				project, volumeName, volumeType, poolID, int64(nodeID))
			if err != nil {
				return errors.Wrap(err, "Find parent volume")
			}

			_, err = tx.tx.Exec("UPDATE sqlite_sequence SET seq = seq + 1 WHERE name = 'storage_volumes'")
			if err != nil {
				return errors.Wrap(err, "Increment storage volumes sequence")
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
				return errors.Wrap(err, "Insert volume snapshot")
			}

			if int64(nodeID) == c.nodeID {
				// Return the ID of the volume created on this node.
				thisVolumeID = volumeID
			}

			err = storageVolumeConfigAdd(tx.tx, volumeID, volumeConfig, true)
			if err != nil {
				return errors.Wrap(err, "Insert storage volume configuration")
			}
		}
		return nil
	})
	if err != nil {
		thisVolumeID = -1
	}

	return thisVolumeID, err
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
	var result []StorageVolumeArgs
	var volumeName string
	var snapshotName string
	var expiryDate string
	var poolName string
	var projectName string

	q := `
SELECT storage_volumes.name, storage_volumes_snapshots.name, storage_volumes_snapshots.expiry_date, storage_pools.name, projects.name
FROM storage_volumes_snapshots
JOIN storage_volumes ON storage_volumes_snapshots.storage_volume_id = storage_volumes.id
JOIN storage_pools ON storage_volumes.storage_pool_id = storage_pools.id
JOIN projects ON storage_volumes.project_id = projects.id
WHERE storage_volumes.type = ?`
	infmt := []interface{}{StoragePoolVolumeTypeCustom}
	outfmt := []interface{}{volumeName, snapshotName, expiryDate, poolName, projectName}
	dbResults, err := queryScan(c, q, infmt, outfmt)
	if err != nil {
		return nil, err
	}

	for _, r := range dbResults {
		timestamp := r[2]

		var snapshotExpiry time.Time
		err = snapshotExpiry.UnmarshalText([]byte(timestamp.(string)))
		if err != nil {
			return []StorageVolumeArgs{}, err
		}

		// Since zero time causes some issues due to timezones, we check the
		// unix timestamp instead of IsZero().
		if snapshotExpiry.Unix() <= 0 {
			// Backup doesn't expire
			continue
		}

		// Snapshot has expired
		if time.Now().Unix()-snapshotExpiry.Unix() >= 0 {
			result = append(result, StorageVolumeArgs{
				ProjectName: r[4].(string),
				Name:        r[0].(string) + shared.SnapshotDelimiter + r[1].(string),
				PoolName:    r[3].(string),
				ExpiryDate:  snapshotExpiry,
			})
		}
	}

	return result, nil
}

// Updates the expiry date of a storage volume snapshot.
func storageVolumeSnapshotExpiryDateUpdate(tx *sql.Tx, volumeID int64, expiryDate time.Time) error {
	stmt := fmt.Sprintf("UPDATE storage_volumes_snapshots SET expiry_date=? WHERE id=?")
	_, err := tx.Exec(stmt, expiryDate, volumeID)
	return err
}
