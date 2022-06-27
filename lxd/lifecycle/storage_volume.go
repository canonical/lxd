package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/shared/api"
)

// Internal copy of the volume interface.
type volume interface {
	Name() string
	Pool() string
}

// StorageVolumeAction represents a lifecycle event action for storage volumes.
type StorageVolumeAction string

// All supported lifecycle events for storage volumes.
const (
	StorageVolumeCreated  = StorageVolumeAction(api.EventLifecycleStorageVolumCreated)
	StorageVolumeDeleted  = StorageVolumeAction(api.EventLifecycleStorageVolumeDeleted)
	StorageVolumeUpdated  = StorageVolumeAction(api.EventLifecycleStorageVolumeUpdated)
	StorageVolumeRenamed  = StorageVolumeAction(api.EventLifecycleStorageVolumeRenamed)
	StorageVolumeRestored = StorageVolumeAction(api.EventLifecycleStorageVolumeRestored)
)

// Event creates the lifecycle event for an action on a storage volume.
func (a StorageVolumeAction) Event(v volume, volumeType string, projectName string, op *operations.Operation, ctx map[string]any) api.EventLifecycle {
	u := fmt.Sprintf("/1.0/storage-pools/%s/volumes", url.PathEscape(v.Pool()))
	if volumeType != "" {
		u = fmt.Sprintf("%s/%s", u, url.PathEscape(volumeType))
	}

	if v.Name() != "" {
		u = fmt.Sprintf("%s/%s", u, url.PathEscape(v.Name()))
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
