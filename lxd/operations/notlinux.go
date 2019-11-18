// +build !linux !cgo agent

package operations

import (
	"fmt"

	"github.com/lxc/lxd/lxd/db"
)

func registerDBOperation(op *Operation, opType db.OperationType) error {
	if op.state != nil {
		return fmt.Errorf("registerDBOperation not supported on this platform")
	}

	return nil
}

func removeDBOperation(op *Operation) error {
	if op.state != nil {
		return fmt.Errorf("registerDBOperation not supported on this platform")
	}

	return nil
}

func getServerName(op *Operation) (string, error) {
	if op.state != nil {
		return "", fmt.Errorf("registerDBOperation not supported on this platform")
	}

	return "", nil
}

func (op *Operation) sendEvent(eventMessage interface{}) {
	if op.events == nil {
		return
	}

	op.events.Send(op.project, "operation", eventMessage)
}
