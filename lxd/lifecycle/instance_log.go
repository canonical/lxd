package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/shared/api"
)

// InstanceLogAction represents a lifecycle event action for instance logs.
type InstanceLogAction string

// All supported lifecycle events for instance logs.
const (
	InstanceLogRetrieved = InstanceLogAction("retrieved")
	InstanceLogDeleted   = InstanceLogAction("deleted")
)

// Event creates the lifecycle event for an action on an instance log.
func (a InstanceLogAction) Event(file string, inst instance, requestor *api.EventLifecycleRequestor, ctx map[string]interface{}) api.EventLifecycle {
	eventType := fmt.Sprintf("instance-log-%s", a)
	u := fmt.Sprintf("/1.0/instance/%s/logs/%s", url.PathEscape(inst.Name()), file)

	if inst.Project() != project.Default {
		u = fmt.Sprintf("%s?project=%s", u, url.QueryEscape(inst.Project()))
	}

	return api.EventLifecycle{
		Action:    eventType,
		Source:    u,
		Context:   ctx,
		Requestor: requestor,
	}
}
