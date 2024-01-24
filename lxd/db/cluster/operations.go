//go:build linux && cgo && !agent

package cluster

import (
	"github.com/canonical/lxd/lxd/db/operationtype"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t operations.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e operation id
//go:generate mapper stmt -e operation objects
//go:generate mapper stmt -e operation objects-by-NodeID
//go:generate mapper stmt -e operation objects-by-ID
//go:generate mapper stmt -e operation objects-by-UUID
//go:generate mapper stmt -e operation create-or-replace
//go:generate mapper stmt -e operation create
//go:generate mapper stmt -e operation delete-by-UUID
//go:generate mapper stmt -e operation delete-by-NodeID
//
//go:generate mapper method -i -e operation GetMany
//go:generate mapper method -i -e operation CreateOrReplace
//go:generate mapper method -i -e operation ID
//go:generate mapper method -i -e operation Exists
//go:generate mapper method -i -e operation Create
//go:generate mapper method -i -e operation DeleteOne-by-UUID
//go:generate mapper method -i -e operation DeleteMany-by-NodeID

// Operation holds information about a single LXD operation running on a node
// in the cluster.
type Operation struct {
	ID          int64              // Stable database identifier
	UUID        string             `db:"primary=yes"`                                      // User-visible identifier
	NodeAddress string             `db:"join=nodes.address&omit=create-or-replace,create"` // Address of the node the operation is running on
	ProjectID   *int64             // ID of the project for the operation.
	NodeID      int64              // ID of the node the operation is running on
	Type        operationtype.Type // Type of the operation
}

// OperationFilter specifies potential query parameter fields.
type OperationFilter struct {
	ID     *int64
	NodeID *int64
	UUID   *string
}
