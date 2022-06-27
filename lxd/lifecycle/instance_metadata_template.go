package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/shared/api"
)

// InstanceMetadataTemplateAction represents a lifecycle event action for instance metadata templates.
type InstanceMetadataTemplateAction string

// All supported lifecycle events for instance metadata templates.
const (
	InstanceMetadataTemplateDeleted   = InstanceMetadataTemplateAction(api.EventLifecycleInstanceMetadataTemplateDeleted)
	InstanceMetadataTemplateCreated   = InstanceMetadataTemplateAction(api.EventLifecycleInstanceMetadataTemplateCreated)
	InstanceMetadataTemplateRetrieved = InstanceMetadataTemplateAction(api.EventLifecycleInstanceMetadataTemplateRetrieved)
)

// Event creates the lifecycle event for an action on instance metadata templates.
func (a InstanceMetadataTemplateAction) Event(inst instance, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := fmt.Sprintf("/1.0/instances/%s/metadata/templates", url.PathEscape(inst.Name()))
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
