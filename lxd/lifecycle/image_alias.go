package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/shared/api"
)

// ImageAliasAction represents a lifecycle event action for image aliases.
type ImageAliasAction string

// All supported lifecycle events for image aliases.
const (
	ImageAliasCreated = ImageAliasAction(api.EventLifecycleImageAliasCreated)
	ImageAliasDeleted = ImageAliasAction(api.EventLifecycleImageAliasDeleted)
	ImageAliasUpdated = ImageAliasAction(api.EventLifecycleImageAliasUpdated)
	ImageAliasRenamed = ImageAliasAction(api.EventLifecycleImageAliasRenamed)
)

// Event creates the lifecycle event for an action on an image alias.
func (a ImageAliasAction) Event(image string, projectName string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := fmt.Sprintf("/1.0/images/aliases/%s", url.PathEscape(image))
	if projectName != project.Default {
		u = fmt.Sprintf("%s?project=%s", u, url.QueryEscape(projectName))
	}

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u,
		Context:   ctx,
		Requestor: requestor,
	}
}
