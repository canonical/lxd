package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/shared/api"
)

// ImageAliasAction represents a lifecycle event action for image aliases.
type ImageAliasAction string

// All supported lifecycle events for image aliases.
const (
	ImageAliasCreated = ImageAliasAction("created")
	ImageAliasDeleted = ImageAliasAction("deleted")
	ImageAliasUpdated = ImageAliasAction("updated")
	ImageAliasRenamed = ImageAliasAction("renamed")
)

// Event creates the lifecycle event for an action on an image alias.
func (a ImageAliasAction) Event(image string, projectName string, requestor *api.EventLifecycleRequestor, ctx map[string]interface{}) api.EventLifecycle {
	eventType := fmt.Sprintf("image-alias-%s", a)
	u := fmt.Sprintf("/1.0/images/aliases/%s", url.PathEscape(image))
	if projectName != project.Default {
		u = fmt.Sprintf("%s?project=%s", u, url.QueryEscape(projectName))
	}
	return api.EventLifecycle{
		Action:    eventType,
		Source:    u,
		Context:   ctx,
		Requestor: requestor,
	}
}
