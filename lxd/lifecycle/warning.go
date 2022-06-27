package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/lxc/lxd/shared/api"
)

// WarningAction represents a lifecycle event action for warnings.
type WarningAction string

// All supported lifecycle events for warnings.
const (
	WarningAcknowledged = WarningAction(api.EventLifecycleWarningAcknowledged)
	WarningReset        = WarningAction(api.EventLifecycleWarningReset)
	WarningDeleted      = WarningAction(api.EventLifecycleWarningDeleted)
)

// Event creates the lifecycle event for an action on a warning.
func (a WarningAction) Event(id string, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := fmt.Sprintf("/1.0/warnings/%s", url.PathEscape(id))

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u,
		Context:   ctx,
		Requestor: requestor,
	}
}
