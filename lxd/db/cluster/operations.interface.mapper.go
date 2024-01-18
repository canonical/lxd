//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
)

// OperationGenerated is an interface of generated methods for Operation.
type OperationGenerated interface {
	// GetOperations returns all available operations.
	// generator: operation GetMany
	GetOperations(ctx context.Context, tx *sql.Tx, filters ...OperationFilter) ([]Operation, error)

	// CreateOrReplaceOperation adds a new operation to the database.
	// generator: operation CreateOrReplace
	CreateOrReplaceOperation(ctx context.Context, tx *sql.Tx, object Operation) (int64, error)

	// GetOperationID return the ID of the operation with the given key.
	// generator: operation ID
	GetOperationID(ctx context.Context, tx *sql.Tx, uuid string) (int64, error)

	// OperationExists checks if a operation with the given key exists.
	// generator: operation Exists
	OperationExists(ctx context.Context, tx *sql.Tx, uuid string) (bool, error)

	// CreateOperation adds a new operation to the database.
	// generator: operation Create
	CreateOperation(ctx context.Context, tx *sql.Tx, object Operation) (int64, error)

	// DeleteOperation deletes the operation matching the given key parameters.
	// generator: operation DeleteOne-by-UUID
	DeleteOperation(ctx context.Context, tx *sql.Tx, uuid string) error

	// DeleteOperations deletes the operation matching the given key parameters.
	// generator: operation DeleteMany-by-NodeID
	DeleteOperations(ctx context.Context, tx *sql.Tx, nodeID int64) error
}
