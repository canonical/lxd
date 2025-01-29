package lifecycle

import (
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/version"
)

// ServiceAction represents a lifecycle event action for a service.
type ServiceAction string

// All supported lifecycle events for services.
const (
	ServiceCreated = ServiceAction(api.EventLifecycleServiceCreated)
	ServiceDeleted = ServiceAction(api.EventLifecycleServiceDeleted)
	ServiceUpdated = ServiceAction(api.EventLifecycleServiceUpdated)
)

// Event creates the lifecycle event for an action on a service.
func (a ServiceAction) Event(name string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "services", name)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
