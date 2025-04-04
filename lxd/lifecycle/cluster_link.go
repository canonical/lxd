package lifecycle

import (
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/version"
)

// ClusterLinkAction represents a lifecycle event action for cluster links.
type ClusterLinkAction string

// All supported lifecycle events for cluster links.
const (
	ClusterLinkCreated = ClusterLinkAction(api.EventLifecycleClusterLinkCreated)
	ClusterLinkAdded   = ClusterLinkAction(api.EventLifecycleClusterLinkAdded)
	ClusterLinkRemoved = ClusterLinkAction(api.EventLifecycleClusterLinkRemoved)
	ClusterLinkUpdated = ClusterLinkAction(api.EventLifecycleClusterLinkUpdated)
	ClusterLinkRenamed = ClusterLinkAction(api.EventLifecycleClusterLinkRenamed)
)

// Event creates the lifecycle event for an action on a cluster link.
func (a ClusterLinkAction) Event(name string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "clusters", name)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
