package api

import (
	"time"
)

// OperationClassTask represents the Task OperationClass
const OperationClassTask = "task"

// OperationClassWebsocket represents the Websocket OperationClass
const OperationClassWebsocket = "websocket"

// OperationClassToken represents the Token OperationClass
const OperationClassToken = "token"

// Operation represents a LXD background operation
//
// swagger:model
type Operation struct {
	// UUID of the operation
	// Example: 6916c8a6-9b7d-4abd-90b3-aedfec7ec7da
	ID string `json:"id" yaml:"id"`

	// Type of operation (task, token or websocket)
	// Example: websocket
	Class string `json:"class" yaml:"class"`

	// Description of the operation
	// Example: Executing command
	Description string `json:"description" yaml:"description"`

	// Operation creation time
	// Example: 2021-03-23T17:38:37.753398689-04:00
	CreatedAt time.Time `json:"created_at" yaml:"created_at"`

	// Operation last change
	// Example: 2021-03-23T17:38:37.753398689-04:00
	UpdatedAt time.Time `json:"updated_at" yaml:"updated_at"`

	// Status name
	// Example: Running
	Status string `json:"status" yaml:"status"`

	// Status code
	// Example: 103
	StatusCode StatusCode `json:"status_code" yaml:"status_code"`

	// Affected resourcs
	// Example: {"containers": ["/1.0/containers/foo"], "instances": ["/1.0/instances/foo"]}
	Resources map[string][]string `json:"resources" yaml:"resources"`

	// Operation specific metadata
	// Example: {"command": ["bash"], "environment": {"HOME": "/root", "LANG": "C.UTF-8", "PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "TERM": "xterm", "USER": "root"}, "fds": {"0": "da3046cf02c0116febf4ef3fe4eaecdf308e720c05e5a9c730ce1a6f15417f66", "1": "05896879d8692607bd6e4a09475667da3b5f6714418ab0ee0e5720b4c57f754b"}, "interactive": true}
	Metadata map[string]interface{} `json:"metadata" yaml:"metadata"`

	// Whether the operation can be canceled
	// Example: false
	MayCancel bool `json:"may_cancel" yaml:"may_cancel"`

	// Operation error mesage
	// Example: Some error message
	Err string `json:"err" yaml:"err"`

	// What cluster member this record was found on
	// Example: lxd01
	//
	// API extension: operation_location
	Location string `json:"location" yaml:"location"`
}
