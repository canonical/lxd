package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/shared/api"
)

// ImageAction represents a lifecycle event action for images.
type ImageAction string

// All supported lifecycle events for images.
const (
	ImageCreated       = ImageAction(api.EventLifecycleImageCreated)
	ImageDeleted       = ImageAction(api.EventLifecycleImageDeleted)
	ImageUpdated       = ImageAction(api.EventLifecycleImageUpdated)
	ImageRetrieved     = ImageAction(api.EventLifecycleImageRetrieved)
	ImageRefreshed     = ImageAction(api.EventLifecycleImageRefreshed)
	ImageSecretCreated = ImageAction(api.EventLifecycleImageSecretCreated)
)

// Event creates the lifecycle event for an action on an image.
func (a ImageAction) Event(image string, projectName string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := fmt.Sprintf("/1.0/images/%s", url.PathEscape(image))
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
