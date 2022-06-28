package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/lxc/lxd/shared/api"
)

// Internal copy of the operation interface.
type operation interface {
	ID() string
}

// OperationAction represents a lifecycle event action for operations.
type OperationAction string

// All supported lifecycle events for operations.
const (
	OperationCancelled = OperationAction(api.EventLifecycleOperationCancelled)
)

// Event creates the lifecycle event for an action on an operation.
func (a OperationAction) Event(op operation, requestor *api.EventLifecycleRequestor, ctx map[string]any) api.EventLifecycle {
	u := fmt.Sprintf("/1.0/operations/%s", url.PathEscape(op.ID()))

	return api.EventLifecycle{
		Action:    string(a),
		Source:    u,
		Context:   ctx,
		Requestor: requestor,
	}
}
