package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/canonical/lxd/shared/api"
)

// ProjectAction represents a lifecycle event action for projects.
type ProjectAction string

// All supported lifecycle events for projects.
const (
	ProjectCreated = ProjectAction("created")
	ProjectDeleted = ProjectAction("deleted")
	ProjectUpdated = ProjectAction("updated")
	ProjectRenamed = ProjectAction("renamed")
)

// Event creates the lifecycle event for an action on a project.
func (a ProjectAction) Event(name string, requestor *api.EventLifecycleRequestor, ctx map[string]interface{}) api.EventLifecycle {
	eventType := fmt.Sprintf("project-%s", a)
	u := fmt.Sprintf("/1.0/projects/%s", url.PathEscape(name))

	return api.EventLifecycle{
		Action:    eventType,
		Source:    u,
		Context:   ctx,
		Requestor: requestor,
	}
}
