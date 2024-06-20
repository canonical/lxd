package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/shared/api"
)

// StoragePoolAction represents a lifecycle event action for storage pools.
type StoragePoolAction string

// All supported lifecycle events for storage pools.
const (
	StoragePoolCreated = StoragePoolAction("created")
	StoragePoolDeleted = StoragePoolAction("deleted")
	StoragePoolUpdated = StoragePoolAction("updated")
)

// Event creates the lifecycle event for an action on an storage pool.
func (a StoragePoolAction) Event(name string, projectName string, requestor *api.EventLifecycleRequestor, ctx map[string]interface{}) api.EventLifecycle {
	eventType := fmt.Sprintf("storage-pool-%s", a)
	u := fmt.Sprintf("/1.0/storage-pools/%s", url.PathEscape(name))

	if projectName != project.Default {
		u = fmt.Sprintf("%s?project=%s", u, url.QueryEscape(projectName))
	}

	return api.EventLifecycle{
		Action:    eventType,
		Source:    u,
		Context:   ctx,
		Requestor: requestor,
	}
}
