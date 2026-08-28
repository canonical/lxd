package storage

import (
	"context"
	"fmt"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/storage/drivers"
	"github.com/canonical/lxd/shared/api"
)

// replicaPool is a mirrored pool's record, carried out of the transaction so that the pool can be
// instantiated without querying again for what was already read.
type replicaPool struct {
	id      int64
	info    api.StoragePool
	members map[int64]db.StoragePoolNode
}

// HoldsCephReplicas reports whether the volumes a project keeps on a pool are mirrors that Ceph
// owns rather than images LXD may write to, which is the case for a standby project on a pool
// carrying its `ceph.replicator.<project>` key.
// Both halves are needed. A leader writes to its own images even while it replicates them, and a
// standby whose pool is not mirrored holds ordinary copies.
func HoldsCephReplicas(pool Pool, proj api.Project) bool {
	if proj.ReplicaMode != api.ReplicatorProjectModeStandby {
		return false
	}

	return poolMirrorsProject(pool.ToAPI().Config, proj.Name)
}

// poolMirrorsProject reports whether a pool carries a project's `ceph.replicator.<project>` key.
func poolMirrorsProject(poolConfig map[string]string, projectName string) bool {
	_, mirrored := poolConfig[drivers.CephReplicatorPoolKey(projectName)]

	return mirrored
}

// PromoteProjectVolumes makes the volumes a project holds on its mirrored pools writable.
// Ceph promotes individual images and knows nothing of projects, so the pools a project is
// mirrored from have to be found in LXD before the driver can act on them.
func PromoteProjectVolumes(ctx context.Context, s *state.State, projectName string, force bool) error {
	pools, err := cephReplicaPools(ctx, s, projectName)
	if err != nil {
		return err
	}

	for _, pool := range pools {
		err := pool.PromoteProjectVolumes(ctx, projectName, force)
		if err != nil {
			return fmt.Errorf("Failed promoting the volumes of storage pool %q: %w", pool.Name(), err)
		}
	}

	return nil
}

// cephReplicaPools returns the storage pools carrying a project's `ceph.replicator.<project>` key.
func cephReplicaPools(ctx context.Context, s *state.State, projectName string) ([]Pool, error) {
	var replicaPools []replicaPool

	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		poolRecords, poolMembers, err := tx.GetStoragePools(ctx, nil)
		if err != nil {
			return fmt.Errorf("Failed loading storage pools: %w", err)
		}

		for poolID, poolRecord := range poolRecords {
			if !poolMirrorsProject(poolRecord.Config, projectName) {
				continue
			}

			replicaPools = append(replicaPools, replicaPool{
				id:      poolID,
				info:    poolRecord,
				members: poolMembers[poolID],
			})
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	pools := make([]Pool, 0, len(replicaPools))

	for _, poolRecord := range replicaPools {
		pool, err := LoadByRecord(s, poolRecord.id, poolRecord.info, poolRecord.members)
		if err != nil {
			return nil, fmt.Errorf("Failed loading storage pool %q: %w", poolRecord.info.Name, err)
		}

		pools = append(pools, pool)
	}

	return pools, nil
}
