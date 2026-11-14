package lifecycle

import (
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
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
func (a ImageAliasAction) Event(name string, projectName string, imageType string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := entity.ImageAliasURL(projectName, imageType, name)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
