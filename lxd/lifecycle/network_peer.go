package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/shared/api"
)

// NetworkPeerAction represents a lifecycle event action for network peers.
type NetworkPeerAction string

// All supported lifecycle events for network peers.
const (
	NetworkPeerCreated = NetworkForwardAction(api.EventLifecycleNetworkPeerCreated)
	NetworkPeerDeleted = NetworkForwardAction(api.EventLifecycleNetworkPeerDeleted)
	NetworkPeerUpdated = NetworkForwardAction(api.EventLifecycleNetworkPeerUpdated)
)

// Event creates the lifecycle event for an action on a network forward.
func (a NetworkPeerAction) Event(n network, peerName string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := fmt.Sprintf("/1.0/networks/%s/peers/%s", url.PathEscape(n.Name()), url.PathEscape(peerName))
	if n.Project() != project.Default {
		u = fmt.Sprintf("%s?project=%s", u, url.QueryEscape(n.Project()))
	}

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u,
		Context:   ctx,
		Requestor: requestor,
	}
}
