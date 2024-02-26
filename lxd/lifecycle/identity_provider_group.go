package lifecycle

import (
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

// IdentityProviderGroupAction represents a lifecycle event action for auth groups.
type IdentityProviderGroupAction string

// All supported lifecycle events for identities.
const (
	IdentityProviderGroupCreated = IdentityProviderGroupAction(api.EventLifecycleIdentityProviderGroupCreated)
	IdentityProviderGroupUpdated = IdentityProviderGroupAction(api.EventLifecycleIdentityProviderGroupUpdated)
	IdentityProviderGroupRenamed = IdentityProviderGroupAction(api.EventLifecycleIdentityProviderGroupRenamed)
	IdentityProviderGroupDeleted = IdentityProviderGroupAction(api.EventLifecycleIdentityProviderGroupDeleted)
)

// Event creates the lifecycle event for an action on a Certificate.
func (a IdentityProviderGroupAction) Event(identityProviderGroupName string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := entity.IdentityProviderGroupURL(identityProviderGroupName)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
