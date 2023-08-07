//go:build !linux || !cgo || agent

package operations

import (
	"fmt"

	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/shared/api"
)

// Checks if an operation registration is supported on the platform; returns an error if not.
func registerDBOperation(op *Operation, opType operationtype.Type) error {
	if op.state != nil {
		return fmt.Errorf("registerDBOperation not supported on this platform")
	}

	return nil
}

// Checks if operation removal is supported on the platform; returns an error if not.
func removeDBOperation(op *Operation) error {
	if op.state != nil {
		return fmt.Errorf("registerDBOperation not supported on this platform")
	}

	return nil
}

// Dispatches an event message associated with the operation, if event dispatching is enabled.
func (op *Operation) sendEvent(eventMessage any) {
	if op.events == nil {
		return
	}

	op.events.Send(op.projectName, api.EventTypeOperation, eventMessage)
}
