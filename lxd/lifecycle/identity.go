package lifecycle

import (
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/version"
)

// IdentityAction represents a lifecycle event action for identities.
type IdentityAction string

// All supported lifecycle events for identities.
const (
	IdentityCreated = IdentityAction(api.EventLifecycleIdentityCreated)
	IdentityUpdated = IdentityAction(api.EventLifecycleIdentityUpdated)
)

// Event creates the lifecycle event for an action on a Certificate.
func (a IdentityAction) Event(authenticationMethod string, identifier string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := api.NewURL().Path(version.APIVersion, "auth", "identities", authenticationMethod, identifier)

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u.String(),
		Context:   ctx,
		Requestor: requestor,
	}
}
