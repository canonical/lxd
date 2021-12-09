package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/lxc/lxd/shared/api"
)

// ClusterGroupAction represents a lifecycle event action for cluster groups.
type ClusterGroupAction string

// All supported lifecycle events for cluster groups.
const (
	ClusterGroupCreated = ClusterGroupAction("created")
	ClusterGroupDeleted = ClusterGroupAction("deleted")
	ClusterGroupUpdated = ClusterGroupAction("updated")
	ClusterGroupRenamed = ClusterGroupAction("renamed")
)

// Event creates the lifecycle event for an action on a cluster group.
func (a ClusterGroupAction) Event(name string, requestor *api.EventLifecycleRequestor, ctx map[string]interface{}) api.EventLifecycle {
	eventType := fmt.Sprintf("cluster-group-%s", a)
	u := fmt.Sprintf("/1.0/cluster/groups/%s", url.PathEscape(name))

	return api.EventLifecycle{
		Action:    eventType,
		Source:    u,
		Context:   ctx,
		Requestor: requestor,
	}
}
