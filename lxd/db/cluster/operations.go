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
//go:generate mapper stmt -e operation objects-by-NodeID-and-Class
//go:generate mapper stmt -e operation objects-by-ID
//go:generate mapper stmt -e operation objects-by-Reference
//go:generate mapper stmt -e operation create-or-replace
//go:generate mapper stmt -e operation delete-by-Reference
//go:generate mapper stmt -e operation delete-by-NodeID
//
//go:generate mapper method -i -e operation GetMany
//go:generate mapper method -i -e operation CreateOrReplace
//go:generate mapper method -i -e operation DeleteOne-by-Reference
//go:generate mapper method -i -e operation DeleteMany-by-NodeID
//go:generate goimports -w operations.mapper.go
//go:generate goimports -w operations.interface.mapper.go

// Operation holds information about a single LXD operation running on a node
// in the cluster.
type Operation struct {
	ID                  int64              `db:"primary=yes"`                               // Stable database identifier
	Reference           string             `db:"primary=yes"`                               // User-visible identifier, such as uuid
	NodeAddress         string             `db:"join=nodes.address&omit=create-or-replace"` // Address of the node the operation is running on
	ProjectID           *int64             // ID of the project for the operation.
	NodeID              int64              // ID of the node the operation is running on
	Type                operationtype.Type // Type of the operation
	Description         string             // Description of the operation
	RequestorProtocol   string             // Protocol from the operation requestor
	RequestorIdentityID *int64             // Identity ID from the operation requestor
	EntityID            *int               // ID of the entity the operation acts upon
	Class               int64              // Class of the operation
	CreatedAt           time.Time          // Time the operation was created
	Inputs              string             // JSON encoded inputs for the operation
	Status              int64              // Status code of the operation
	Error               string             // Error message if the operation failed
	Stage               *int64             // Stage of the operation
}

// OperationFilter specifies potential query parameter fields.
type OperationFilter struct {
	ID        *int64
	NodeID    *int64
	Reference *string
	Class     *int64
}

// GetDurableOperationMetadata retrieves metadata key/value pairs for a durable operation from the cluster db.
func GetDurableOperationMetadata(ctx context.Context, tx *sql.Tx, opID int64) (map[string]string, error) {
	stmt := `SELECT key, value FROM operations_metadata WHERE operation_id = ?`

	values := map[string]string{}
	err := query.Scan(ctx, tx, stmt, func(scan func(dest ...any) error) error {
		var key string
		var value string

		err := scan(&key, &value)
		if err != nil {
			return err
		}

		values[key] = value
		return nil
	}, opID)
	if err != nil {
		return nil, fmt.Errorf("Failed reading operation metadata: %w", err)
	}

	return values, nil
}

// CreateOrInsertDurableOperationMetadata inserts metadata key/value pairs for a durable operation in the cluster db.
// This is needed so that the durable operation can be restarted on a different node in case of failure.
func CreateOrInsertDurableOperationMetadata(ctx context.Context, tx *sql.Tx, opID int64, metadata map[string]any) error {
	// No metadata to register.
	if len(metadata) == 0 {
		return nil
	}

	for key, value := range metadata {
		stmt := `INSERT OR REPLACE INTO operations_metadata (operation_id, key, value) VALUES (?, ?, ?)`

		_, err := tx.ExecContext(ctx, stmt, opID, key, value)
		if err != nil {
			return fmt.Errorf("Failed writing operation metadata: %w", err)
		}
	}

	return nil
}
