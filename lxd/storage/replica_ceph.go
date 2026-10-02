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

// DemoteProjectVolumes makes the volumes a project holds on its mirrored pools read-only, so that
// the site taking over can promote its own copies.
func DemoteProjectVolumes(ctx context.Context, s *state.State, projectName string) error {
	pools, err := cephReplicaPools(ctx, s, projectName)
	if err != nil {
		return err
	}

	for _, pool := range pools {
		err := pool.DemoteProjectVolumes(ctx, projectName)
		if err != nil {
			return fmt.Errorf("Failed demoting the volumes of storage pool %q: %w", pool.Name(), err)
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

// validateCephReplicatorProjects checks that every `ceph.replicator.<project>` key of a pool config
// names a project that exists. A key naming no project would be picked up by whichever project is
// later created under that name, which would then be mirrored without anyone having asked for it.
func validateCephReplicatorProjects(ctx context.Context, s *state.State, poolConfig map[string]string) error {
	var keys []string

	for key, value := range poolConfig {
		_, isReplicatorKey := drivers.CephReplicatorPoolKeyProject(key)

		// An empty value is how the key is unset.
		if isReplicatorKey && value != "" {
			keys = append(keys, key)
		}
	}

	// Most pools carry no such key, and they are spared the query.
	if len(keys) == 0 {
		return nil
	}

	var projectNames []string

	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		projectNames, err = cluster.GetProjectNames(ctx, tx.Tx())

		return err
	})
	if err != nil {
		return fmt.Errorf("Failed loading project names: %w", err)
	}

	// The keys come out of a map, so order them to report the same one each time.
	slices.Sort(keys)

	for _, key := range keys {
		projectName, _ := drivers.CephReplicatorPoolKeyProject(key)
		if !slices.Contains(projectNames, projectName) {
			return fmt.Errorf("Invalid option %q, project %q does not exist", key, projectName)
		}
	}

	return nil
}

// RemoveCephReplicatorPoolKey removes a project's `ceph.replicator.<project>` key from every pool
// carrying it. It takes the transaction so that the key can go together with the project's own
// record, which leaves no moment where a pool names a project that is gone.
func RemoveCephReplicatorPoolKey(ctx context.Context, tx *db.ClusterTx, projectName string) error {
	poolRecords, _, err := tx.GetStoragePools(ctx, nil)
	if err != nil {
		return fmt.Errorf("Failed loading storage pools: %w", err)
	}

	for _, poolRecord := range poolRecords {
		if !poolMirrorsProject(poolRecord.Config, projectName) {
			continue
		}

		delete(poolRecord.Config, drivers.CephReplicatorPoolKey(projectName))

		err := tx.UpdateStoragePool(ctx, poolRecord.Name, poolRecord.Description, poolRecord.Config)
		if err != nil {
			return fmt.Errorf("Failed updating storage pool %q: %w", poolRecord.Name, err)
		}
	}

	return nil
}
