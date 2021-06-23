package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/lxc/lxd/shared/api"
)

// ProjectAction represents a lifecycle event action for project devices.
type ProjectAction string

// All supported lifecycle events for project devices.
const (
	ProjectCreated = ProjectAction("created")
	ProjectDeleted = ProjectAction("deleted")
	ProjectUpdated = ProjectAction("updated")
	ProjectRenamed = ProjectAction("renamed")
)

// Event creates the lifecycle event for an action on a project device.
func (a ProjectAction) Event(name string, ctx map[string]interface{}) api.EventLifecycle {
	eventType := fmt.Sprintf("project-%s", a)
	u := fmt.Sprintf("/1.0/projects/%s", url.PathEscape(name))

	return api.EventLifecycle{
		Action:    eventType,
		Source:    u,
		Context:   ctx,
		Requestor: nil,
	}
}
