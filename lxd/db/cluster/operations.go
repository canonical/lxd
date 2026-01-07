//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared/api"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t operations.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e operation objects
//go:generate mapper stmt -e operation objects-by-NodeID
//go:generate mapper stmt -e operation objects-by-NodeID-and-Class
//go:generate mapper stmt -e operation objects-by-Class
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
	Class     *int64
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

// UpdateOperationNodeID updates the node_id field of an existing operation in the cluster db.
func UpdateOperationNodeID(ctx context.Context, tx *sql.Tx, opReference string, newNodeID int64, updatedAt time.Time) error {
	stmt := `UPDATE operations SET node_id = ?, updated_at = ? WHERE reference = ?`

	result, err := tx.ExecContext(ctx, stmt, newNodeID, updatedAt, opReference)
	if err != nil {
		return fmt.Errorf("Failed updating operation node ID: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("Fetch affected rows: %w", err)
	}

	if n != 1 {
		return fmt.Errorf("Query updated %d rows instead of 1", n)
	}

	return nil
}

// UpdateOperationStatus updates the status field of an existing operation in the cluster db.
func UpdateOperationStatus(ctx context.Context, tx *sql.Tx, opReference string, newStatus api.StatusCode, updatedAt time.Time, opErr *string) error {
	stmt := `UPDATE operations SET status = ?, updated_at = ?, error = ? WHERE reference = ?`
	if newStatus.IsFinal() {
		// If we are moving to final status, clear node_id.
		stmt = `UPDATE operations SET status = ?, updated_at = ?, error = ?, node_id = NULL WHERE reference = ?`
	}

	result, err := tx.ExecContext(ctx, stmt, newStatus, updatedAt, opErr, opReference)
	if err != nil {
		return fmt.Errorf("Failed updating operation status: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("Fetch affected rows: %w", err)
	}

	if n != 1 {
		return fmt.Errorf("Query updated %d rows instead of 1", n)
	}

	return nil
}

// UpdateOperationUpdatedAt updates only the updatedAt timestamp. This is used when operation metadata changes.
func UpdateOperationUpdatedAt(ctx context.Context, tx *sql.Tx, opReference string, updatedAt time.Time) error {
	stmt := `UPDATE operations SET updated_at = ? WHERE reference = ?`

	result, err := tx.ExecContext(ctx, stmt, updatedAt, opReference)
	if err != nil {
		return fmt.Errorf("Failed updating operation updated_at: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("Fetch affected rows: %w", err)
	}

	if n != 1 {
		return fmt.Errorf("Query updated %d rows instead of 1", n)
	}

	return nil
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
