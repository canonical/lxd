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

// NetworkZoneRecordAction represents a lifecycle event action for network zone records.
type NetworkZoneRecordAction string

// All supported lifecycle events for network zones.
const (
	NetworkZoneCreated = NetworkZoneAction("created")
	NetworkZoneDeleted = NetworkZoneAction("deleted")
	NetworkZoneUpdated = NetworkZoneAction("updated")

	NetworkZoneRecordCreated = NetworkZoneRecordAction("created")
	NetworkZoneRecordDeleted = NetworkZoneRecordAction("deleted")
	NetworkZoneRecordUpdated = NetworkZoneRecordAction("updated")
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

// Event creates the lifecycle event for an action on a network zone record.
func (a NetworkZoneRecordAction) Event(n networkZone, name string, requestor *api.EventLifecycleRequestor, ctx map[string]interface{}) api.EventLifecycle {
	eventType := fmt.Sprintf("network-zone-record-%s", a)

	u := fmt.Sprintf("/1.0/network-zones/%s/records/%s", url.PathEscape(n.Info().Name), url.PathEscape(name))
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
