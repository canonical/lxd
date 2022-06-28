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
	ClusterMemberAdded   = ClusterMemberAction(api.EventLifecycleClusterMemberAdded)
	ClusterMemberRemoved = ClusterMemberAction(api.EventLifecycleClusterMemberRemoved)
	ClusterMemberUpdated = ClusterMemberAction(api.EventLifecycleClusterMemberUpdated)
	ClusterMemberRenamed = ClusterMemberAction(api.EventLifecycleClusterMemberRenamed)
)

// Event creates the lifecycle event for an action on a cluster member.
func (a ClusterMemberAction) Event(name string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := "/1.0/cluster/members"
	if name != "" {
		u = fmt.Sprintf("%s/%s", u, url.PathEscape(name))
	}

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u,
		Context:   ctx,
		Requestor: requestor,
	}
}
