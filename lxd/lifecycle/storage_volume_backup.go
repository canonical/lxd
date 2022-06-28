package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

// StorageVolumeBackupAction represents a lifecycle event action for storage volume backups.
type StorageVolumeBackupAction string

// All supported lifecycle events for storage volume backups.
const (
	StorageVolumeBackupCreated   = StorageVolumeBackupAction(api.EventLifecycleStorageVolumeBackupCreated)
	StorageVolumeBackupDeleted   = StorageVolumeBackupAction(api.EventLifecycleStorageVolumeBackupDeleted)
	StorageVolumeBackupRetrieved = StorageVolumeBackupAction(api.EventLifecycleStorageVolumeBackupRetrieved)
	StorageVolumeBackupRenamed   = StorageVolumeBackupAction(api.EventLifecycleStorageVolumeBackupRenamed)
)

// Event creates the lifecycle event for an action on a storage volume backup.
func (a StorageVolumeBackupAction) Event(poolName string, volumeType string, volumeName string, projectName string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	parentName, backupName, _ := shared.InstanceGetParentAndSnapshotName(volumeName)

	u := fmt.Sprintf("/1.0/storage-pools/%s/volumes/%s/%s/backups", url.PathEscape(poolName), url.PathEscape(volumeType), url.PathEscape(parentName))
	if backupName != "" {
		u = fmt.Sprintf("%s/%s", u, backupName)
	}

	if projectName != project.Default {
		u = fmt.Sprintf("%s?project=%s", u, url.QueryEscape(projectName))
	}

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u,
		Context:   ctx,
		Requestor: requestor,
	}
}
