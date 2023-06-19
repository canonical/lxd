//go:build linux && cgo && !agent

package db

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

// CreateStorageVolumeSnapshot creates a new storage volume snapshot attached to a given
// storage pool.
func (c *Cluster) CreateStorageVolumeSnapshot(projectName string, volumeName string, volumeDescription string, volumeType int, poolID int64, volumeConfig map[string]string, creationDate time.Time, expiryDate time.Time) (int64, error) {
	var volumeID int64

	var snapshotName string
	parts := strings.Split(volumeName, shared.SnapshotDelimiter)
	volumeName = parts[0]
	snapshotName = parts[1]

	err := c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		// Figure out the volume ID of the parent.
		parentID, err := tx.storagePoolVolumeGetTypeID(ctx, projectName, volumeName, volumeType, poolID, c.nodeID)
		if err != nil {
			return fmt.Errorf("Failed finding parent volume record for snapshot: %w", err)
		}

		_, err = tx.tx.Exec("UPDATE sqlite_sequence SET seq = seq + 1 WHERE name = 'storage_volumes'")
		if err != nil {
			return fmt.Errorf("Failed incrementing storage volumes sequence: %w", err)
		}

		row := tx.tx.QueryRowContext(ctx, "SELECT seq FROM sqlite_sequence WHERE name = 'storage_volumes' LIMIT 1")
		err = row.Scan(&volumeID)
		if err != nil {
			return fmt.Errorf("Failed getting storage volumes sequence: %w", err)
		}

		_, err = tx.tx.Exec("INSERT INTO storage_volumes_snapshots (id, storage_volume_id, name, description, creation_date, expiry_date) VALUES (?, ?, ?, ?, ?, ?)", volumeID, parentID, snapshotName, volumeDescription, creationDate, expiryDate)
		if err != nil {
			return fmt.Errorf("Failed creating volume snapshot record: %w", err)
		}

		err = storageVolumeConfigAdd(tx.tx, volumeID, volumeConfig, true)
		if err != nil {
			return fmt.Errorf("Failed inserting storage volume snapshot record configuration: %w", err)
		}

		return nil
	})
	if err != nil {
		volumeID = -1
	}

	return volumeID, err
}

// UpdateStorageVolumeSnapshot updates the storage volume snapshot attached to a given storage pool.
func (c *Cluster) UpdateStorageVolumeSnapshot(projectName string, volumeName string, volumeType int, poolID int64, volumeDescription string, volumeConfig map[string]string, expiryDate time.Time) error {
	var err error

	if !strings.Contains(volumeName, shared.SnapshotDelimiter) {
		return fmt.Errorf("Volume is not a snapshot")
	}

	err = c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		volume, err := tx.GetStoragePoolVolume(ctx, poolID, projectName, volumeType, volumeName, true)
		if err != nil {
			return err
		}

		err = storageVolumeConfigClear(tx.tx, volume.ID, true)
		if err != nil {
			return err
		}

		err = storageVolumeConfigAdd(tx.tx, volume.ID, volumeConfig, true)
		if err != nil {
			return err
		}

		err = storageVolumeDescriptionUpdate(tx.tx, volume.ID, volumeDescription, true)
		if err != nil {
			return err
		}

		err = storageVolumeSnapshotExpiryDateUpdate(tx.tx, volume.ID, expiryDate)
		if err != nil {
			return err
		}

		return nil
	})

	return err
}

// GetStorageVolumeSnapshotWithID returns the volume snapshot with the given ID.
func (c *Cluster) GetStorageVolumeSnapshotWithID(snapshotID int) (StorageVolumeArgs, error) {
	args := StorageVolumeArgs{}
	q := `
SELECT
	volumes.id,
	volumes.name,
	volumes.creation_date,
	storage_pools.name,
	volumes.type,
	projects.name
FROM storage_volumes_all AS volumes
JOIN projects ON projects.id=volumes.project_id
JOIN storage_pools ON storage_pools.id=volumes.storage_pool_id
WHERE volumes.id=?
`
	arg1 := []any{snapshotID}
	outfmt := []any{&args.ID, &args.Name, &args.CreationDate, &args.PoolName, &args.Type, &args.ProjectName}
	err := dbQueryRowScan(c, q, arg1, outfmt)
	if err != nil {
		if err == sql.ErrNoRows {
			return args, api.StatusErrorf(http.StatusNotFound, "Storage pool volume snapshot not found")
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
	inargs := []any{volumeID}
	outargs := []any{&expiry}

	err := dbQueryRowScan(c, query, inargs, outargs)
	if err != nil {
		if err == sql.ErrNoRows {
			return expiry, api.StatusErrorf(http.StatusNotFound, "Storage pool volume snapshot not found")
		}

		return expiry, err
	}

	return expiry, nil
}

// GetExpiredStorageVolumeSnapshots returns a list of expired volume snapshots.
func (c *Cluster) GetExpiredStorageVolumeSnapshots() ([]StorageVolumeArgs, error) {
	q := `
	SELECT
		storage_volumes_snapshots.id,
		storage_volumes.name,
		storage_volumes_snapshots.name,
		storage_volumes_snapshots.creation_date,
		storage_volumes_snapshots.expiry_date,
		storage_pools.name,
		projects.name,
		IFNULL(storage_volumes.node_id, -1)
	FROM storage_volumes_snapshots
	JOIN storage_volumes ON storage_volumes_snapshots.storage_volume_id = storage_volumes.id
	JOIN storage_pools ON storage_volumes.storage_pool_id = storage_pools.id
	JOIN projects ON storage_volumes.project_id = projects.id
	WHERE storage_volumes.type = ? AND storage_volumes_snapshots.expiry_date != '0001-01-01T00:00:00Z'`

	var snapshots []StorageVolumeArgs

	err := c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		return query.Scan(ctx, tx.Tx(), q, func(scan func(dest ...any) error) error {
			var snap StorageVolumeArgs
			var snapName string
			var volName string
			var expiryTime sql.NullTime

			err := scan(&snap.ID, &volName, &snapName, &snap.CreationDate, &expiryTime, &snap.PoolName, &snap.ProjectName, &snap.NodeID)
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
	stmt := "UPDATE storage_volumes_snapshots SET expiry_date=? WHERE id=?"
	_, err := tx.Exec(stmt, expiryDate, volumeID)
	return err
}
