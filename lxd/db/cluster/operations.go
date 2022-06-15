//go:build linux && cgo && !agent

package cluster

import (
	"github.com/lxc/lxd/lxd/db/operationtype"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t operations.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e operation objects version=2 version=2
//go:generate mapper stmt -e operation objects-by-NodeID version=2
//go:generate mapper stmt -e operation objects-by-ID version=2
//go:generate mapper stmt -e operation objects-by-UUID version=2
//go:generate mapper stmt -e operation create-or-replace version=2
//go:generate mapper stmt -e operation delete-by-UUID version=2
//go:generate mapper stmt -e operation delete-by-NodeID version=2
//
//go:generate mapper method -i -e operation GetMany version=2
//go:generate mapper method -i -e operation CreateOrReplace version=2
//go:generate mapper method -i -e operation DeleteOne-by-UUID version=2
//go:generate mapper method -i -e operation DeleteMany-by-NodeID version=2

// Operation holds information about a single LXD operation running on a node
// in the cluster.
type Operation struct {
	ID          int64              `db:"primary=yes"`                               // Stable database identifier
	UUID        string             `db:"primary=yes"`                               // User-visible identifier
	NodeAddress string             `db:"join=nodes.address&omit=create-or-replace"` // Address of the node the operation is running on
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
