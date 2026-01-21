//go:build !linux || !cgo || agent

package operations

import (
	"fmt"

	"github.com/canonical/lxd/shared/api"
)

func registerDBOperation(op *Operation) error {
	if op.state != nil {
		return fmt.Errorf("registerDBOperation not supported on this platform")
	}

	return nil
}

func updateDBOperationStatus(op *Operation) error {
	if op.state != nil {
		return fmt.Errorf("updateDBOperationStatus not supported on this platform")
	}

	return nil
}

func removeDBOperation(op *Operation) error {
	if op.state != nil {
		return fmt.Errorf("removeDBOperation not supported on this platform")
	}

	return nil
}

func conflictingOperationExists(op *Operation, constraint OperationUniquenessConstraint) (bool, error) {
	if op.state != nil {
		return false, fmt.Errorf("conflictingOperationExists not supported on this platform")
	}

	return false, nil
}

func (op *Operation) sendEvent(eventMessage any) {
	if op.events == nil {
		return
	}

	op.events.Send(op.projectName, api.EventTypeOperation, eventMessage)
}
