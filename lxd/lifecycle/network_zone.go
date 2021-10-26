package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/shared/api"
)

// Internal copy of the network zone interface.
type networkZone interface {
	Info() *api.NetworkZone
	Project() string
}

// NetworkZoneAction represents a lifecycle event action for network zones.
type NetworkZoneAction string

// All supported lifecycle events for network zones.
const (
	NetworkZoneCreated = NetworkZoneAction("created")
	NetworkZoneDeleted = NetworkZoneAction("deleted")
	NetworkZoneUpdated = NetworkZoneAction("updated")
)

// Event creates the lifecycle event for an action on a network zone.
func (a NetworkZoneAction) Event(n networkZone, requestor *api.EventLifecycleRequestor, ctx map[string]interface{}) api.EventLifecycle {
	eventType := fmt.Sprintf("network-zone-%s", a)

	u := fmt.Sprintf("/1.0/network-zones/%s", url.PathEscape(n.Info().Name))
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
