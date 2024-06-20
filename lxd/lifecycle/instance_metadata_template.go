package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/shared/api"
)

// InstanceMetadataTemplateAction represents a lifecycle event action for instance metadata templates.
type InstanceMetadataTemplateAction string

// All supported lifecycle events for instance metadata templates.
const (
	InstanceMetadataTemplateDeleted   = InstanceMetadataTemplateAction("deleted")
	InstanceMetadataTemplateCreated   = InstanceMetadataTemplateAction("created")
	InstanceMetadataTemplateRetrieved = InstanceMetadataTemplateAction("retrieved")
)

// Event creates the lifecycle event for an action on instance metadata templates.
func (a InstanceMetadataTemplateAction) Event(inst instance, requestor *api.EventLifecycleRequestor, ctx map[string]interface{}) api.EventLifecycle {
	eventType := fmt.Sprintf("instance-metadata-template-%s", a)
	u := fmt.Sprintf("/1.0/instances/%s/metadata/templates", url.PathEscape(inst.Name()))

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
