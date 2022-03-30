package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/shared/api"
)

// NetworkForwardAction represents a lifecycle event action for network forwards.
type NetworkForwardAction string

// All supported lifecycle events for network forwards.
const (
	NetworkForwardCreated = NetworkForwardAction("created")
	NetworkForwardDeleted = NetworkForwardAction("deleted")
	NetworkForwardUpdated = NetworkForwardAction("updated")
)

// Event creates the lifecycle event for an action on a network forward.
func (a NetworkForwardAction) Event(n network, listenAddress string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	eventType := fmt.Sprintf("network-%s", a)
	u := fmt.Sprintf("/1.0/networks/%s/forwards/%s", url.PathEscape(n.Name()), url.PathEscape(listenAddress))

	if n.Project() != project.Default {
		u = fmt.Sprintf("%s?project=%s", u, url.QueryEscape(n.Project()))
	}

	return api.EventLifecycle{
		Action:    eventType,
		Source:    u,
		Context:   ctx,
		Requestor: requestor,
	}
}
