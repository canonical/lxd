package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
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
func (a StorageVolumeSnapshotAction) Event(v volume, volumeType string, projectName string, op *operations.Operation, ctx map[string]any) api.EventLifecycle {
	parentName, snapshotName, _ := shared.InstanceGetParentAndSnapshotName(v.Name())

	u := fmt.Sprintf("/1.0/storage-pools/%s/volumes/%s/%s/snapshots", url.PathEscape(v.Pool()), url.PathEscape(volumeType), url.PathEscape(parentName))
	if snapshotName != "" {
		u = fmt.Sprintf("%s/%s", u, snapshotName)
	}

	if projectName != project.Default {
		u = fmt.Sprintf("%s?project=%s", u, url.QueryEscape(projectName))
	}

	var requestor *api.EventLifecycleRequestor
	if op != nil {
		requestor = op.Requestor()
	}

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u,
		Context:   ctx,
		Requestor: requestor,
	}
}
