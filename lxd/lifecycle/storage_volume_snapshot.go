package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

// StorageVolumeSnapshotAction represents a lifecycle event action for storage volume snapshots.
type StorageVolumeSnapshotAction string

// All supported lifecycle events for storage volume snapshots.
const (
	StorageVolumeSnapshotCreated = StorageVolumeSnapshotAction("created")
	StorageVolumeSnapshotDeleted = StorageVolumeSnapshotAction("deleted")
	StorageVolumeSnapshotUpdated = StorageVolumeSnapshotAction("updated")
	StorageVolumeSnapshotRenamed = StorageVolumeSnapshotAction("renamed")
)

// Event creates the lifecycle event for an action on a storage volume snapshot.
func (a StorageVolumeSnapshotAction) Event(v volume, volumeType string, projectName string, op *operations.Operation, ctx map[string]interface{}) api.EventLifecycle {
	eventType := fmt.Sprintf("storage-volume-snapshot-%s", a)
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
		Action:    eventType,
		Source:    u,
		Context:   ctx,
		Requestor: requestor,
	}
}
