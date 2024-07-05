package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/canonical/lxd/shared/api"
)

// ClusterAction represents a lifecycle event action for clusters.
type ClusterAction string

// All supported lifecycle events for clusters.
const (
	ClusterEnabled            = ClusterAction("enabled")
	ClusterDisabled           = ClusterAction("disabled")
	ClusterCertificateUpdated = ClusterAction("certificate-updated")
	ClusterTokenCreated       = ClusterAction("token-created")
)

// Event creates the lifecycle event for an action on a cluster.
func (a ClusterAction) Event(name string, requestor *api.EventLifecycleRequestor, ctx map[string]interface{}) api.EventLifecycle {
	eventType := fmt.Sprintf("cluster-%s", a)
	u := fmt.Sprintf("/1.0/cluster/%s", url.PathEscape(name))

	return api.EventLifecycle{
		Action:    eventType,
		Source:    u,
		Context:   ctx,
		Requestor: requestor,
	}
}
