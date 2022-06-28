package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/shared/api"
)

// InstanceMetadataAction represents a lifecycle event action for instance metadata.
type InstanceMetadataAction string

// All supported lifecycle events for instance metadata.
const (
	InstanceMetadataUpdated   = InstanceMetadataAction(api.EventLifecycleInstanceMetadataUpdated)
	InstanceMetadataRetrieved = InstanceMetadataAction(api.EventLifecycleInstanceMetadataRetrieved)
)

// Event creates the lifecycle event for an action on instance metadata.
func (a InstanceMetadataAction) Event(inst instance, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := fmt.Sprintf("/1.0/instances/%s/metadata", url.PathEscape(inst.Name()))
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
