package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/lxc/lxd/shared/api"
)

// ProjectAction represents a lifecycle event action for projects.
type ProjectAction string

// All supported lifecycle events for projects.
const (
	ProjectCreated = ProjectAction(api.EventLifecycleProjectCreated)
	ProjectDeleted = ProjectAction(api.EventLifecycleProjectDeleted)
	ProjectUpdated = ProjectAction(api.EventLifecycleProjectUpdated)
	ProjectRenamed = ProjectAction(api.EventLifecycleProjectRenamed)
)

// Event creates the lifecycle event for an action on a project.
func (a ProjectAction) Event(name string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := fmt.Sprintf("/1.0/projects/%s", url.PathEscape(name))

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u,
		Context:   ctx,
		Requestor: requestor,
	}
}
