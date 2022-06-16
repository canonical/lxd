package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/lxc/lxd/shared/api"
)

// ClusterMemberAction represents a lifecycle event action for cluster members.
type ClusterMemberAction string

// All supported lifecycle events for cluster members.
const (
	ClusterMemberAdded   = ClusterMemberAction("added")
	ClusterMemberRemoved = ClusterMemberAction("removed")
	ClusterMemberUpdated = ClusterMemberAction("updated")
	ClusterMemberRenamed = ClusterMemberAction("renamed")
)

// Event creates the lifecycle event for an action on a cluster member.
func (a ClusterMemberAction) Event(name string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	eventType := fmt.Sprintf("cluster-member-%s", a)
	u := "/1.0/cluster/members"
	if name != "" {
		u = fmt.Sprintf("%s/%s", u, url.PathEscape(name))
	}

	return api.EventLifecycle{
		Action:    eventType,
		Source:    u,
		Context:   ctx,
		Requestor: requestor,
	}
}
