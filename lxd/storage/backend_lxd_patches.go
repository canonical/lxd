package storage

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/db/cluster"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/storage/drivers"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

var lxdEarlyPatches = map[string]func(b *lxdBackend) error{
	"storage_missing_snapshot_records":    patchMissingSnapshotRecords,
	"storage_delete_old_snapshot_records": patchDeleteOldSnapshotRecords,
}

var lxdLatePatches = map[string]func(b *lxdBackend) error{}

// Patches start here.

// patchMissingSnapshotRecords creates any missing storage volume records for instance volume snapshots.
// This is needed because it seems that in 2019 some instance snapshots did not have their associated volume DB
// records created. This later caused problems when we started validating that the instance snapshot DB record
// count matched the volume snapshot DB record count.
func patchMissingSnapshotRecords(b *lxdBackend) error {
	var err error
	var localNode string

	err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		localNode, err = tx.GetLocalNodeName(ctx)
		if err != nil {
			return fmt.Errorf("Failed to get local member name: %w", err)
		}

		return err
	})
	if err != nil {
		return err
	}

	// Get instances on this local server (as the DB helper functions return volumes on local server), also
	// avoids running the same queries on every cluster member for instances on shared storage.
	filter := cluster.InstanceFilter{Node: &localNode}
	err = b.state.DB.Cluster.InstanceList(context.TODO(), func(inst db.InstanceArgs, p api.Project) error {
		// Check we can convert the instance to the volume type needed.
		volType, err := InstanceTypeToVolumeType(inst.Type)
		if err != nil {
			return err
		}

		contentType := drivers.ContentTypeFS
		if inst.Type == instancetype.VM {
			contentType = drivers.ContentTypeBlock
		}

		// Get all the instance snapshot DB records.
		var instPoolName string
		var snapshots []cluster.Instance
		err = b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			instPoolName, err = tx.GetInstancePool(ctx, p.Name, inst.Name)
			if err != nil {
				if api.StatusErrorCheck(err, http.StatusNotFound) {
					// If the instance cannot be associated to a pool its got bigger problems
					// outside the scope of this patch. Will skip due to empty instPoolName.
					return nil
				}

				return fmt.Errorf("Failed finding pool for instance %q in project %q: %w", inst.Name, p.Name, err)
			}

			snapshots, err = tx.GetInstanceSnapshotsWithName(ctx, p.Name, inst.Name)
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return err
		}

		if instPoolName != b.Name() {
			return nil // This instance isn't hosted on this storage pool, skip.
		}

		dbVol, err := VolumeDBGet(b, p.Name, inst.Name, volType)
		if err != nil {
			return fmt.Errorf("Failed loading storage volume record %q: %w", inst.Name, err)
		}

		// Get all the instance volume snapshot DB records.
		dbVolSnaps, err := VolumeDBSnapshotsGet(b, p.Name, inst.Name, volType)
		if err != nil {
			return fmt.Errorf("Failed loading storage volume snapshot records %q: %w", inst.Name, err)
		}

		for i := range snapshots {
			foundVolumeSnapshot := false
			for _, dbVolSnap := range dbVolSnaps {
				if dbVolSnap.Name == snapshots[i].Name {
					foundVolumeSnapshot = true
					break
				}
			}

			if !foundVolumeSnapshot {
				b.logger.Info("Creating missing volume snapshot record", logger.Ctx{"project": snapshots[i].Project, "instance": snapshots[i].Name})
				err = VolumeDBCreate(b, snapshots[i].Project, snapshots[i].Name, "Auto repaired", volType, true, dbVol.Config, snapshots[i].CreationDate, time.Time{}, contentType, false, true)
				if err != nil {
					return err
				}
			}
		}

		return nil
	}, filter)
	if err != nil {
		return err
	}

	return nil
}

// patchDeleteOldSnapshotRecords deletes the remaining snapshot records in storage_volumes
// (a previous patch would have already moved them into storage_volume_snapshots).
func patchDeleteOldSnapshotRecords(b *lxdBackend) error {
	err := b.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		nodeID := tx.GetNodeID()
		_, err := tx.Tx().Exec(`
DELETE FROM storage_volumes WHERE id IN (
	SELECT id FROM storage_volumes
	JOIN (
		/* Create a two column intermediary table containing the container name and its snapshot name */
		SELECT sv.name inst_name, svs.name snap_name FROM storage_volumes AS sv
		JOIN storage_volumes_snapshots AS svs ON sv.id = svs.storage_volume_id AND node_id=? AND type=?
	) j1 ON name=printf("%s/%s", j1.inst_name, j1.snap_name)
	/* Only keep the records with a matching 'name' pattern, 'node_id' and 'type' */
);
`, nodeID, db.StoragePoolVolumeTypeContainer)
		if err != nil {
			return fmt.Errorf("Failed to delete remaining instance snapshot records in the `storage_volumes` table: %w", err)
		}

		return nil
	})

	if err != nil {
		return err
	}

	return nil
}
