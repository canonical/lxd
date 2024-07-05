package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/shared/api"
)

// ProfileAction represents a lifecycle event action for profiles.
type ProfileAction string

// All supported lifecycle events for profiles.
const (
	ProfileCreated = ProfileAction("created")
	ProfileDeleted = ProfileAction("deleted")
	ProfileUpdated = ProfileAction("updated")
	ProfileRenamed = ProfileAction("renamed")
)

// Event creates the lifecycle event for an action on a profile.
func (a ProfileAction) Event(name string, projectName string, requestor *api.EventLifecycleRequestor, ctx map[string]interface{}) api.EventLifecycle {
	eventType := fmt.Sprintf("profile-%s", a)
	u := fmt.Sprintf("/1.0/profiles/%s", url.PathEscape(name))
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
