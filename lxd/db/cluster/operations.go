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
	"github.com/canonical/lxd/shared/entity"
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
//go:generate mapper stmt -e operation objects-by-UUID
//go:generate mapper stmt -e operation create-or-replace
//go:generate mapper stmt -e operation delete-by-UUID
//go:generate mapper stmt -e operation delete-by-NodeID
//
//go:generate mapper method -i -e operation GetMany
//go:generate mapper method -i -e operation CreateOrReplace
//go:generate mapper method -i -e operation DeleteOne-by-UUID
//go:generate mapper method -i -e operation DeleteMany-by-NodeID
//go:generate goimports -w operations.mapper.go
//go:generate goimports -w operations.interface.mapper.go

// Operation holds information about a single LXD operation running on a node
// in the cluster.
type Operation struct {
	ID                int64              `db:"primary=yes"`                               // Stable database identifier
	UUID              string             `db:"primary=yes"`                               // User-visible identifier
	NodeAddress       string             `db:"join=nodes.address&omit=create-or-replace"` // Address of the node the operation is running on
	ProjectID         *int64             // ID of the project for the operation.
	NodeID            int64              // ID of the node the operation is running on
	Type              operationtype.Type // Type of the operation
	Description       string             // Description of the operation
	RequestorProtocol string             // Protocol from the operation requestor
	IdentityID        *int64             // Identity ID
	Class             int64              // Class of the operation
	CreatedAt         time.Time          // Time the operation was created
	Status            int64              // Status code of the operation
}

// OperationFilter specifies potential query parameter fields.
type OperationFilter struct {
	ID     *int64
	NodeID *int64
	UUID   *string
	Class  *int64
}

// UpdateOperationNodeID updates the node_id field of an existing operation in the cluster db.
func UpdateOperationNodeID(ctx context.Context, tx *sql.Tx, opUUID string, newNodeID int64) error {
	stmt := `UPDATE operations SET node_id = ? WHERE uuid = ?`

	result, err := tx.ExecContext(ctx, stmt, newNodeID, opUUID)
	if err != nil {
		return fmt.Errorf("Failed to update operation node ID: %w", err)
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

	rows, err := tx.QueryContext(ctx, stmt, opID)
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()

	values := map[string]string{}
	for rows.Next() {
		var key string
		var value string

		err := rows.Scan(&key, &value)
		if err != nil {
			return nil, err
		}

		values[key] = value
	}

	err = rows.Err()
	if err != nil {
		return nil, err
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
			return fmt.Errorf("Failed to write operation metadata: %w", err)
		}
	}

	return nil
}

// GetDurableOperationResources retrieves resources associated with a durable operation from the cluster db.
func GetDurableOperationResources(ctx context.Context, tx *sql.Tx, opID int64) (map[string][]api.URL, error) {
	stmt := `SELECT resource, entity_type, entity_id FROM operations_resources WHERE operation_id = ?`

	dbResources := make(map[string][]EntityRef)
	dest := func(scan func(dest ...any) error) error {
		entityRef := EntityRef{}
		var resourceType string

		err := scan(&resourceType, &entityRef.EntityType, &entityRef.EntityID)
		if err != nil {
			return err
		}

		_, ok := dbResources[resourceType]
		if !ok {
			dbResources[resourceType] = make([]EntityRef, 0)
		}

		dbResources[resourceType] = append(dbResources[resourceType], entityRef)
		return nil
	}

	err := query.Scan(ctx, tx, stmt, dest, opID)
	if err != nil {
		return nil, fmt.Errorf("Failed to read operation resources: %w", err)
	}

	result := make(map[string][]api.URL)
	for resourceType, entityRefs := range dbResources {
		result[resourceType] = make([]api.URL, 0)
		for _, entityRef := range entityRefs {
			entityURL, err := GetEntityURL(ctx, tx, entity.Type(entityRef.EntityType), entityRef.EntityID)
			if err != nil {
				return nil, err
			}

			result[resourceType] = append(result[resourceType], *entityURL)
		}
	}

	return result, nil
}

// CreateOrInsertDurableOperationResources inserts resources associated with a durable operation in the cluster db.
func CreateOrInsertDurableOperationResources(ctx context.Context, tx *sql.Tx, opID int64, resources map[string][]api.URL) error {
	// No resources to register.
	if len(resources) == 0 {
		return nil
	}

	for resourceType, entityURLs := range resources {
		entityReferences := make(map[*api.URL]*EntityRef, len(entityURLs))
		for _, entityURL := range entityURLs {
			entityReferences[&entityURL] = &EntityRef{}
		}

		err := PopulateEntityReferencesFromURLs(ctx, tx, entityReferences)
		if err != nil {
			return fmt.Errorf("Failed to populate entity references from URLs: %w", err)
		}

		for _, entityRef := range entityReferences {
			stmt := `INSERT OR REPLACE INTO operations_resources (operation_id, resource, entity_type, entity_id) VALUES (?, ?, ?, ?)`
			_, err := tx.ExecContext(ctx, stmt, opID, resourceType, entityRef.EntityType, entityRef.EntityID)
			if err != nil {
				return fmt.Errorf("Failed to write operation metadata: %w", err)
			}
		}
	}

	return nil
}
