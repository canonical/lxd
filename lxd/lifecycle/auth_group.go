package lifecycle

import (
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

// AuthGroupAction represents a lifecycle event action for auth groups.
type AuthGroupAction string

// All supported lifecycle events for identities.
const (
	AuthGroupCreated = AuthGroupAction(api.EventLifecycleAuthGroupCreated)
	AuthGroupUpdated = AuthGroupAction(api.EventLifecycleAuthGroupUpdated)
	AuthGroupRenamed = AuthGroupAction(api.EventLifecycleAuthGroupRenamed)
	AuthGroupDeleted = AuthGroupAction(api.EventLifecycleAuthGroupDeleted)
)

// Event creates the lifecycle event for an action on a Certificate.
func (a AuthGroupAction) Event(groupName string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := entity.AuthGroupURL(groupName)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
