package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/shared/api"
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
	StorageVolumeCreated  = StorageVolumeAction("created")
	StorageVolumeDeleted  = StorageVolumeAction("deleted")
	StorageVolumeUpdated  = StorageVolumeAction("updated")
	StorageVolumeRenamed  = StorageVolumeAction("renamed")
	StorageVolumeRestored = StorageVolumeAction("restored")
)

// Event creates the lifecycle event for an action on a storage volume.
func (a StorageVolumeAction) Event(v volume, volumeType string, projectName string, op *operations.Operation, ctx map[string]interface{}) api.EventLifecycle {
	eventType := fmt.Sprintf("storage-volume-%s", a)
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
		Action:    eventType,
		Source:    u,
		Context:   ctx,
		Requestor: requestor,
	}
}
