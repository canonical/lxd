package lifecycle

import (
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/version"
)

// ImageRegistryAction represents a lifecycle event action for image registries.
type ImageRegistryAction string

// All supported lifecycle events for image registries.
const (
	ImageRegistryCreated = ImageRegistryAction(api.EventLifecycleImageRegistryCreated)
	ImageRegistryDeleted = ImageRegistryAction(api.EventLifecycleImageRegistryDeleted)
	ImageRegistryRenamed = ImageRegistryAction(api.EventLifecycleImageRegistryRenamed)
	ImageRegistryUpdated = ImageRegistryAction(api.EventLifecycleImageRegistryUpdated)
)

// Event creates the lifecycle event for an action on an image registry.
func (a ImageRegistryAction) Event(name string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "image-registries", name)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
