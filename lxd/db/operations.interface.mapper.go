//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db

// OperationGenerated is an interface of generated methods for Operation
type OperationGenerated interface {
	// GetOperations returns all available operations.
	// generator: operation GetMany
	GetOperations(filter OperationFilter) ([]Operation, error)

	// CreateOrReplaceOperation adds a new operation to the database.
	// generator: operation CreateOrReplace
	CreateOrReplaceOperation(object Operation) (int64, error)

	// DeleteOperation deletes the operation matching the given key parameters.
	// generator: operation DeleteOne
	DeleteOperation(filter OperationFilter) error

	// DeleteOperations deletes the operation matching the given key parameters.
	// generator: operation DeleteMany
	DeleteOperations(filter OperationFilter) error
}
