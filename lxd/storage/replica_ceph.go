package storage

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
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

// HoldsCephReplicasByName is HoldsCephReplicas for callers that only have the project's name. The
// pool key is checked first so that the project record is only loaded when the pool is mirrored,
// which keeps the ordinary paths free of the extra query.
func HoldsCephReplicasByName(ctx context.Context, s *state.State, pool Pool, projectName string) (bool, error) {
	if !poolMirrorsProject(pool.ToAPI().Config, projectName) {
		return false, nil
	}

	var proj *api.Project
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		dbProject, err := cluster.GetProject(ctx, tx.Tx(), projectName)
		if err != nil {
			return err
		}

		proj, err = dbProject.ToAPI(ctx, tx.Tx())
		return err
	})
	if err != nil {
		return false, fmt.Errorf("Failed loading project %q: %w", projectName, err)
	}

	return HoldsCephReplicas(pool, *proj), nil
}

// poolMirrorsProject reports whether a pool carries a project's `ceph.replicator.<project>` key.
func poolMirrorsProject(poolConfig map[string]string, projectName string) bool {
	_, mirrored := poolConfig[drivers.CephReplicatorPoolKey(projectName)]

	return mirrored
}

// ProjectMirrorsToCeph reports whether any pool carries the project's `ceph.replicator.<project>`
// key.
func ProjectMirrorsToCeph(ctx context.Context, s *state.State, projectName string) (bool, error) {
	names, err := CephReplicaPoolNames(ctx, s, projectName)
	if err != nil {
		return false, err
	}

	return len(names) > 0, nil
}

// CephReplicaPoolNames returns the names of the pools carrying a project's `ceph.replicator.<project>`
// key.
func CephReplicaPoolNames(ctx context.Context, s *state.State, projectName string) ([]string, error) {
	records, err := cephReplicaPoolRecords(ctx, s, projectName)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(records))
	for _, record := range records {
		names = append(names, record.info.Name)
	}

	return names, nil
}

// MirrorProjectVolumes enrolls the volumes a project holds on its mirrored pools into replication
// and sends their current state to the peer.
func MirrorProjectVolumes(ctx context.Context, s *state.State, projectName string) error {
	pools, err := cephReplicaPools(ctx, s, projectName)
	if err != nil {
		return err
	}

	for _, pool := range pools {
		err := pool.MirrorProjectVolumes(ctx, projectName)
		if err != nil {
			return fmt.Errorf("Failed mirroring the volumes of storage pool %q: %w", pool.Name(), err)
		}
	}

	return nil
}

// ConfirmProjectVolumeMirrors returns, as "pool/volume", the volumes on the project's mirrored pools
// that the peer has not replayed yet.
func ConfirmProjectVolumeMirrors(ctx context.Context, s *state.State, projectName string) ([]string, error) {
	pools, err := cephReplicaPools(ctx, s, projectName)
	if err != nil {
		return nil, err
	}

	var pending []string

	for _, pool := range pools {
		poolPending, err := pool.ConfirmProjectVolumeMirrors(ctx, projectName)
		if err != nil {
			return nil, fmt.Errorf("Failed confirming the volumes of storage pool %q: %w", pool.Name(), err)
		}

		for _, volName := range poolPending {
			pending = append(pending, pool.Name()+"/"+volName)
		}
	}

	return pending, nil
}

// cephReplicaPools returns the storage pools carrying a project's `ceph.replicator.<project>` key.
func cephReplicaPools(ctx context.Context, s *state.State, projectName string) ([]Pool, error) {
	replicaPools, err := cephReplicaPoolRecords(ctx, s, projectName)
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

// cephReplicaPoolRecords returns the records of the pools carrying a project's
// `ceph.replicator.<project>` key.
func cephReplicaPoolRecords(ctx context.Context, s *state.State, projectName string) ([]replicaPool, error) {
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

	// The records come out of a map, so order them by name to keep the pending list and the error
	// messages built from it the same from one run to the next.
	slices.SortFunc(replicaPools, func(a replicaPool, b replicaPool) int {
		return cmp.Compare(a.info.Name, b.info.Name)
	})

	return replicaPools, nil
}
