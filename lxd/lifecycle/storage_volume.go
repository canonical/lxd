package lifecycle

import (
	"context"

	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/version"
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
	StorageVolumeCreated  = StorageVolumeAction(api.EventLifecycleStorageVolumeCreated)
	StorageVolumeDeleted  = StorageVolumeAction(api.EventLifecycleStorageVolumeDeleted)
	StorageVolumeUpdated  = StorageVolumeAction(api.EventLifecycleStorageVolumeUpdated)
	StorageVolumeRenamed  = StorageVolumeAction(api.EventLifecycleStorageVolumeRenamed)
	StorageVolumeRestored = StorageVolumeAction(api.EventLifecycleStorageVolumeRestored)
)

// Event creates the lifecycle event for an action on a storage volume.
func (a StorageVolumeAction) Event(ctx context.Context, v volume, volumeType string, projectName string, eventCtx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "storage-pools", v.Pool(), "volumes", volumeType, v.Name()).Project(projectName)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   eventCtx,
		Requestor: request.CreateRequestor(ctx),
	}
}
