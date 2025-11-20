package lifecycle

import (
	"github.com/canonical/lxd/shared/api"
)

// NetworkPeerAction represents a lifecycle event action for network peers.
type NetworkPeerAction string

// All supported lifecycle events for network peers.
const (
	NetworkPeerCreated = NetworkForwardAction(api.EventLifecycleNetworkPeerCreated)
	NetworkPeerDeleted = NetworkForwardAction(api.EventLifecycleNetworkPeerDeleted)
	NetworkPeerUpdated = NetworkForwardAction(api.EventLifecycleNetworkPeerUpdated)
)
