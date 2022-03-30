package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/shared/api"
)

// Internal copy of the network acl interface.
type networkACL interface {
	Info() *api.NetworkACL
	Project() string
}

// NetworkACLAction represents a lifecycle event action for network acls.
type NetworkACLAction string

// All supported lifecycle events for network acls.
const (
	NetworkACLCreated = NetworkACLAction("created")
	NetworkACLDeleted = NetworkACLAction("deleted")
	NetworkACLUpdated = NetworkACLAction("updated")
	NetworkACLRenamed = NetworkACLAction("renamed")
)

// Event creates the lifecycle event for an action on a network acl.
func (a NetworkACLAction) Event(n networkACL, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	eventType := fmt.Sprintf("network-acl-%s", a)

	u := fmt.Sprintf("/1.0/network-acls/%s", url.PathEscape(n.Info().Name))
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
