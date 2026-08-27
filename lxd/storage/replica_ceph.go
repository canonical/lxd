package storage

import (
	"github.com/canonical/lxd/lxd/storage/drivers"
	"github.com/canonical/lxd/shared/api"
)

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
