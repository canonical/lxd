package lifecycle

import (
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/version"
)

// SiteAction represents a lifecycle event action for a site.
type SiteAction string

// All supported lifecycle events for profiles.
const (
	SiteCreated = SiteAction(api.EventLifecycleSiteCreated)
	SiteDeleted = SiteAction(api.EventLifecycleSiteDeleted)
	SiteUpdated = SiteAction(api.EventLifecycleSiteUpdated)
)

// Event creates the lifecycle event for an action on a site.
func (a SiteAction) Event(name string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "sites", name)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
