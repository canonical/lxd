package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/shared/api"
)

// ProfileAction represents a lifecycle event action for profiles.
type ProfileAction string

// All supported lifecycle events for profiles.
const (
	ProfileCreated = ProfileAction(api.EventLifecycleProfileCreated)
	ProfileDeleted = ProfileAction(api.EventLifecycleProfileDeleted)
	ProfileUpdated = ProfileAction(api.EventLifecycleProfileUpdated)
	ProfileRenamed = ProfileAction(api.EventLifecycleProfileRenamed)
)

// Event creates the lifecycle event for an action on a profile.
func (a ProfileAction) Event(name string, projectName string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := fmt.Sprintf("/1.0/profiles/%s", url.PathEscape(name))
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
