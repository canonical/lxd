package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/shared/api"
)

// StoragePoolAction represents a lifecycle event action for storage pools.
type StoragePoolAction string

// All supported lifecycle events for storage pools.
const (
	StoragePoolCreated = StoragePoolAction(api.EventLifecycleStoragePoolCreated)
	StoragePoolDeleted = StoragePoolAction(api.EventLifecycleStoragePoolDeleted)
	StoragePoolUpdated = StoragePoolAction(api.EventLifecycleStoragePoolUpdated)
)

// Event creates the lifecycle event for an action on an storage pool.
func (a StoragePoolAction) Event(name string, projectName string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := fmt.Sprintf("/1.0/storage-pools/%s", url.PathEscape(name))
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
