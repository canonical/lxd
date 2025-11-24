//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/db/query"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t operations.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e operation objects
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
	ID                  int64              `db:"primary=yes"`                           // Stable database identifier
	Reference           string             `db:"primary=yes"`                           // User-visible identifier, such as uuid
	NodeAddress         string             `db:"omit=objects,create,create-or-replace"` // Address of the node the operation is running on
	ProjectID           *int64             // ID of the project for the operation.
	NodeID              *int64             // ID of the node the operation is running on
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
	Stage               *int64             // Stage of the operation
}

// OperationFilter specifies potential query parameter fields.
type OperationFilter struct {
	ID        *int64
	NodeID    *int64
	Reference *string
}

// GetOperationsWithAddress returns a list of operations matching the provided filters,
// including the address of the node each operation is running on.
func GetOperationsWithAddress(ctx context.Context, tx *sql.Tx, filters ...OperationFilter) ([]Operation, error) {
	ops, err := GetOperations(ctx, tx, filters...)
	if err != nil {
		return nil, err
	}

	stmt := `SELECT id, address FROM nodes`
	nodes := make(map[int64]string)

	dest := func(scan func(dest ...any) error) error {
		var id int64
		var address string
		err := scan(&id, &address)
		if err != nil {
			return err
		}

		nodes[id] = address
		return nil
	}

	err = query.Scan(ctx, tx, stmt, dest)
	if err != nil {
		return nil, err
	}

	for i := range ops {
		if ops[i].NodeID != nil {
			address, ok := nodes[*ops[i].NodeID]
			if !ok {
				return nil, fmt.Errorf("Failed to find node address for node ID %d", *ops[i].NodeID)
			}

			ops[i].NodeAddress = address
		}
	}

	return ops, nil
}
