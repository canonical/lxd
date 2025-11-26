package operationtype

import (
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
	// OperationClassDurable represents the Durable OperationClass.
	OperationClassDurable Class = 4
)

func (t Class) String() string {
	return map[Class]string{
		OperationClassTask:      api.OperationClassTask,
		OperationClassWebsocket: api.OperationClassWebsocket,
		OperationClassToken:     api.OperationClassToken,
		OperationClassDurable:   api.OperationClassDurable,
	}[t]
}
