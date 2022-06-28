package lifecycle

import (
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
)

// NetworkLoadBalancerAction represents a lifecycle event action for network load balancers.
type NetworkLoadBalancerAction string

// All supported lifecycle events for network forwards.
const (
	NetworkLoadBalancerCreated = NetworkForwardAction(api.EventLifecycleNetworkLoadBalancerCreated)
	NetworkLoadBalancerDeleted = NetworkForwardAction(api.EventLifecycleNetworkLoadBalancerDeleted)
	NetworkLoadBalancerUpdated = NetworkForwardAction(api.EventLifecycleNetworkLoadBalancerUpdated)
)

// Event creates the lifecycle event for an action on a network forward.
func (a NetworkLoadBalancerAction) Event(n network, listenAddress string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "networks", n.Name(), "load-balancers", listenAddress).Project(n.Project())
	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
