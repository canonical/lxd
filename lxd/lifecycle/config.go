package lifecycle

import (
	"fmt"

	"github.com/lxc/lxd/shared/api"
)

// ConfigAction represents a lifecycle event action for the server configuration
type ConfigAction string

// All supported lifecycle events for the server configuration
const (
	ConfigUpdated = ConfigAction("updated")
)

// Event creates the lifecycle event for an action on the server configuration
func (a ConfigAction) Event(requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	eventType := fmt.Sprintf("config-%s", a)
	u := "/1.0"

	return api.EventLifecycle{
		Action:    eventType,
		Source:    u,
		Context:   ctx,
		Requestor: requestor,
	}
}
