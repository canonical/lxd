package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/lxc/lxd/shared/api"
)

// ClusterAction represents a lifecycle event action for clusters.
type ClusterAction string

// All supported lifecycle events for clusters.
const (
	ClusterEnabled            = ClusterAction(api.EventLifecycleClusterEnabled)
	ClusterDisabled           = ClusterAction(api.EventLifecycleClusterDisabled)
	ClusterCertificateUpdated = ClusterAction(api.EventLifecycleClusterCertificateUpdated)
	ClusterTokenCreated       = ClusterAction(api.EventLifecycleClusterTokenCreated)
)

// Event creates the lifecycle event for an action on a cluster.
func (a ClusterAction) Event(name string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := fmt.Sprintf("/1.0/cluster/%s", url.PathEscape(name))

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u,
		Context:   ctx,
		Requestor: requestor,
	}
}
