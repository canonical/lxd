package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/shared/api"
)

// ImageAction represents a lifecycle event action for images.
type ImageAction string

// All supported lifecycle events for images.
const (
	ImageCreated       = ImageAction("created")
	ImageDeleted       = ImageAction("deleted")
	ImageUpdated       = ImageAction("updated")
	ImageRetrieved     = ImageAction("retrieved")
	ImageRefreshed     = ImageAction("refreshed")
	ImageSecretCreated = ImageAction("secret-created")
)

// Event creates the lifecycle event for an action on an image.
func (a ImageAction) Event(image string, projectName string, requestor *api.EventLifecycleRequestor, ctx map[string]interface{}) api.EventLifecycle {
	eventType := fmt.Sprintf("image-%s", a)
	u := fmt.Sprintf("/1.0/images/%s", url.PathEscape(image))
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
