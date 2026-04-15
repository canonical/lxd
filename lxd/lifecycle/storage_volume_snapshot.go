package lifecycle

import (
	"context"

	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/version"
)

// StorageVolumeSnapshotAction represents a lifecycle event action for storage volume snapshots.
type StorageVolumeSnapshotAction string

// All supported lifecycle events for storage volume snapshots.
const (
	StorageVolumeSnapshotCreated = StorageVolumeSnapshotAction(api.EventLifecycleStorageVolumeSnapshotCreated)
	StorageVolumeSnapshotDeleted = StorageVolumeSnapshotAction(api.EventLifecycleStorageVolumeSnapshotDeleted)
	StorageVolumeSnapshotUpdated = StorageVolumeSnapshotAction(api.EventLifecycleStorageVolumeSnapshotUpdated)
	StorageVolumeSnapshotRenamed = StorageVolumeSnapshotAction(api.EventLifecycleStorageVolumeSnapshotRenamed)
)

// Event creates the lifecycle event for an action on a storage volume snapshot.
func (a StorageVolumeSnapshotAction) Event(ctx context.Context, v volume, volumeType string, projectName string, eventCtx map[string]any) api.EventLifecycle {
	parentName, snapshotName, _ := api.GetParentAndSnapshotName(v.Name())

	u := api.NewURL().Path(version.APIVersion, "storage-pools", v.Pool(), "volumes", volumeType, parentName, "snapshots", snapshotName).Project(projectName)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   eventCtx,
		Requestor: request.CreateRequestor(ctx),
	}
}
