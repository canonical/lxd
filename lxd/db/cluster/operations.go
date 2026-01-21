//go:build linux && cgo && !agent

package cluster

import (
	"time"

	"github.com/canonical/lxd/lxd/db/operationtype"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t operations.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e operation objects
//go:generate mapper stmt -e operation objects-by-Type
//go:generate mapper stmt -e operation objects-by-Type-and-EntityID
//go:generate mapper stmt -e operation objects-by-NodeID
//go:generate mapper stmt -e operation objects-by-ID
//go:generate mapper stmt -e operation objects-by-Reference
//go:generate mapper stmt -e operation create
//go:generate mapper stmt -e operation create-or-replace
//go:generate mapper stmt -e operation delete-by-Reference
//go:generate mapper stmt -e operation delete-by-NodeID
//
//go:generate mapper method -i -e operation GetMany
//go:generate mapper method -i -e operation Create
//go:generate mapper method -i -e operation CreateOrReplace
//go:generate mapper method -i -e operation DeleteOne-by-Reference
//go:generate mapper method -i -e operation DeleteMany-by-NodeID
//go:generate goimports -w operations.mapper.go
//go:generate goimports -w operations.interface.mapper.go

// Operation holds information about a single LXD operation running on a node
// in the cluster.
type Operation struct {
	ID                  int64              `db:"primary=yes"`                                                      // Stable database identifier
	Reference           string             `db:"primary=yes"`                                                      // User-visible identifier, such as uuid
	NodeAddress         string             `db:"coalesce=''&leftjoin=nodes.address&omit=create,create-or-replace"` // Address of the node the operation is running on
	ProjectID           *int64             // ID of the project for the operation.
	NodeID              int64              // ID of the node the operation is running on
	Type                operationtype.Type // Type of the operation
	RequestorProtocol   string             // Protocol from the operation requestor
	RequestorIdentityID *int64             // Identity ID from the operation requestor
	EntityID            *int               // ID of the entity the operation acts upon
	Class               int64              // Class of the operation
	CreatedAt           time.Time          // Time the operation was created
	UpdatedAt           time.Time          // Time when the state or the metadata of the operation were last updated
	Inputs              string             // JSON encoded inputs for the operation
	Status              int64              // Status code of the operation
	Error               *string            // Error message if the operation failed
	Parent              *int64             // Parent operation ID. This is used for sub-operations of other operations.
	Stage               *int64             // Stage of the operation
}

// OperationFilter specifies potential query parameter fields.
type OperationFilter struct {
	ID        *int64
	NodeID    *int64
	Reference *string
	Type      *operationtype.Type
	EntityID  *int
}
