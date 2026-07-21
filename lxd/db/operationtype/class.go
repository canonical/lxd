package operationtype

import (
	"fmt"
	"slices"

	"github.com/canonical/lxd/shared/api"
)

// Class represents an operation Class.
type Class int

const (
	// OperationClassTask represents the Task Class.
	OperationClassTask Class = 1
	// OperationClassWebsocket represents the Websocket Class.
	OperationClassWebsocket Class = 2
	// OperationClassToken represents the Token Class.
	OperationClassToken Class = 3
	// OperationClassDurable represents the Durable Class.
	OperationClassDurable Class = 4
)

// String implements [fmt.Stringer] for [Class].
func (t Class) String() string {
	return map[Class]string{
		OperationClassTask:      api.OperationClassTask,
		OperationClassWebsocket: api.OperationClassWebsocket,
		OperationClassToken:     api.OperationClassToken,
		OperationClassDurable:   api.OperationClassDurable,
	}[t]
}

// Validate returns an error if the [Class] is not known.
func (t Class) Validate() error {
	if t.String() == "" {
		return fmt.Errorf(`Unknown operation class "%d"`, t)
	}

	return nil
}

// SupportsBulkOperations returns true if operations of this class may be a parent or child operation.
func (t Class) SupportsBulkOperations() bool {
	return slices.Contains([]Class{OperationClassTask, OperationClassDurable}, t)
}
