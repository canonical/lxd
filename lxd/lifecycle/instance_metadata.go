package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/shared/api"
)

// InstanceMetadataAction represents a lifecycle event action for instance metadata.
type InstanceMetadataAction string

// All supported lifecycle events for instance metadata.
const (
	InstanceMetadataUpdated   = InstanceMetadataAction("updated")
	InstanceMetadataRetrieved = InstanceMetadataAction("retrieved")
)

// Event creates the lifecycle event for an action on instance metadata.
func (a InstanceMetadataAction) Event(inst instance, requestor *api.EventLifecycleRequestor, ctx map[string]interface{}) api.EventLifecycle {
	eventType := fmt.Sprintf("instance-metadata-%s", a)
	u := fmt.Sprintf("/1.0/instances/%s/metadata", url.PathEscape(inst.Name()))

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
