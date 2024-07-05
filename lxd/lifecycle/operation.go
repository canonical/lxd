package lifecycle

import (
	"fmt"
	"net/url"

	"github.com/canonical/lxd/shared/api"
)

// Internal copy of the operation interface.
type operation interface {
	ID() string
}

// OperationAction represents a lifecycle event action for operations.
type OperationAction string

// All supported lifecycle events for operations.
const (
	OperationCancelled = OperationAction("cancelled")
)

// Event creates the lifecycle event for an action on an operation.
func (a OperationAction) Event(op operation, requestor *api.EventLifecycleRequestor, ctx map[string]interface{}) api.EventLifecycle {
	eventType := fmt.Sprintf("operation-%s", a)
	u := fmt.Sprintf("/1.0/operations/%s", url.PathEscape(op.ID()))

	return api.EventLifecycle{
		Action:    eventType,
		Source:    u,
		Context:   ctx,
		Requestor: requestor,
	}
}
