package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/shared/api"
)

// InstanceLogAction represents a lifecycle event action for instance logs.
type InstanceLogAction string

// All supported lifecycle events for instance logs.
const (
	InstanceLogRetrieved = InstanceLogAction(api.EventLifecycleInstanceLogRetrieved)
	InstanceLogDeleted   = InstanceLogAction(api.EventLifecycleInstanceLogDeleted)
)

// Event creates the lifecycle event for an action on an instance log.
func (a InstanceLogAction) Event(file string, inst instance, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := fmt.Sprintf("/1.0/instance/%s/logs/%s", url.PathEscape(inst.Name()), file)
	if inst.Project() != project.Default {
		u = fmt.Sprintf("%s?project=%s", u, url.QueryEscape(inst.Project()))
	}

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u,
		Context:   ctx,
		Requestor: requestor,
	}
}
