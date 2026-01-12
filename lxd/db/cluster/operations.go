//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strings"
	"time"

	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t operations.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e operation objects
//go:generate mapper stmt -e operation objects-by-ConflictReference
//go:generate mapper stmt -e operation objects-by-NodeID
//go:generate mapper stmt -e operation objects-by-NodeID-and-Class
//go:generate mapper stmt -e operation objects-by-Class
//go:generate mapper stmt -e operation objects-by-ID
//go:generate mapper stmt -e operation objects-by-UUID
//go:generate mapper stmt -e operation objects-by-ProjectID
//go:generate mapper stmt -e operation create
//go:generate mapper stmt -e operation create-or-replace
//go:generate mapper stmt -e operation delete-by-UUID
//
//go:generate mapper method -i -e operation GetMany
//go:generate mapper method -i -e operation Create
//go:generate mapper method -i -e operation CreateOrReplace
//go:generate mapper method -i -e operation DeleteOne-by-UUID
//go:generate goimports -w operations.mapper.go
//go:generate goimports -w operations.interface.mapper.go

// Operation holds information about a single LXD operation running on a node
// in the cluster.
type Operation struct {
	ID                  int64              `db:"primary=yes"`                                                      // Stable database identifier
	UUID                string             `db:"primary=yes"`                                                      // User-visible identifier, such as uuid
	NodeAddress         string             `db:"coalesce=''&leftjoin=nodes.address&omit=create,create-or-replace"` // Address of the node the operation is running on
	ProjectID           *int64             // ID of the project for the operation.
	NodeID              int64              // ID of the node the operation is running on
	Type                operationtype.Type // Type of the operation
	RequestorProtocol   *RequestorProtocol // Protocol from the operation requestor
	RequestorIdentityID *int64             // Identity ID from the operation requestor
	EntityID            int                // ID of the entity the operation acts upon
	Metadata            string             // JSON encoded metadata for the operation
	Class               int64              // Class of the operation
	CreatedAt           time.Time          // Time the operation was created
	UpdatedAt           time.Time          // Time when the state or the metadata of the operation were last updated
	Inputs              string             // JSON encoded inputs for the operation
	Status              int64              `db:"sql=operations.status_code"` // Status code of the operation
	ConflictReference   string             // All operations which cannot run concurrently share the same conflict reference
	Error               string             // Error message if the operation failed
	Parent              *int64             // Parent operation ID. This is used for sub-operations of other operations.
	Stage               int64              // Stage of the operation
}

// OperationFilter specifies potential query parameter fields.
type OperationFilter struct {
	ID                *int64
	ProjectID         *int64
	NodeID            *int64
	UUID              *string
	ConflictReference *string
	Class             *int64
}

// RequestorProtocol is the database representation of the Requestor Protocol.
//
// RequestorProtocol is defined on string so that constants can be converted by casting. The [sql.Scanner] and
// [driver.Valuer] interfaces are implemented on this type such that the string constants are converted into their int64
// counterparts as they are written to the database, or converted back into an [RequestorProtocol] as they are read from the
// database. It is not possible to read/write an invalid requestor protocol from/to the database when using this type.
type RequestorProtocol string

const (
	requestorProtocolNone int64 = iota
	requestorProtocolCluster
	requestorProtocolUnix
	requestorProtocolPKI
	requestorProtocolDevLXD
	requestorProtocolTLS
	requestorProtocolOIDC
	requestorProtocolBearer
)

// requestorProtocolTextToCode maps RequestorProtocol string constants to their int64 database representation.
var requestorProtocolCodeToText = map[int64]string{
	requestorProtocolNone:    "",
	requestorProtocolCluster: request.ProtocolCluster,
	requestorProtocolUnix:    request.ProtocolUnix,
	requestorProtocolPKI:     request.ProtocolPKI,
	requestorProtocolDevLXD:  request.ProtocolDevLXD,
	requestorProtocolTLS:     api.AuthenticationMethodTLS,
	requestorProtocolOIDC:    api.AuthenticationMethodOIDC,
	requestorProtocolBearer:  api.AuthenticationMethodBearer,
}

// ScanInteger implements [query.IntegerScanner] for [RequestorProtocol]. This simplifies the Scan implementation.
func (r *RequestorProtocol) ScanInteger(requestorProtocolCode int64) error {
	text, ok := requestorProtocolCodeToText[requestorProtocolCode]
	if !ok {
		return fmt.Errorf("Unknown requestor protocol `%d`", requestorProtocolCode)
	}

	*r = RequestorProtocol(text)
	return nil
}

// Scan implements [sql.Scanner] for [RequestorProtocol]. This converts the integer value back into the correct constant or
// returns an error.
func (r *RequestorProtocol) Scan(value any) error {
	return query.ScanValue(value, r, false)
}

// Value implements [driver.Valuer] for [RequestorProtocol]. This converts the API constant into an integer or throws an error.
func (r *RequestorProtocol) Value() (driver.Value, error) {
	if r == nil {
		return nil, nil
	}

	switch *r {
	case "":
		return requestorProtocolNone, nil
	case RequestorProtocol(request.ProtocolCluster):
		return requestorProtocolCluster, nil
	case RequestorProtocol(request.ProtocolUnix):
		return requestorProtocolUnix, nil
	case RequestorProtocol(request.ProtocolPKI):
		return requestorProtocolPKI, nil
	case RequestorProtocol(request.ProtocolDevLXD):
		return requestorProtocolDevLXD, nil
	case RequestorProtocol(api.AuthenticationMethodTLS):
		return requestorProtocolTLS, nil
	case RequestorProtocol(api.AuthenticationMethodOIDC):
		return requestorProtocolOIDC, nil
	case RequestorProtocol(api.AuthenticationMethodBearer):
		return requestorProtocolBearer, nil
	}

	return nil, fmt.Errorf("Invalid requestor protocol %q", *r)
}

// UpdateOperation updates operation status, metadata and error (if set) in the cluster db.
// This is used to keep DB in sync with the current status of the operation when the operation changes
// its status, or when calls to commit metadata explicitly.
func UpdateOperation(ctx context.Context, tx *sql.Tx, opUUID string,
	updatedAt time.Time, newStatus api.StatusCode, metadata string, opErr error) error {
	stmt := `UPDATE operations SET updated_at = ?, status_code = ?, metadata = ?, error = ? WHERE uuid = ?`

	opErrStr := ""
	if opErr != nil {
		opErrStr = opErr.Error()
	}

	result, err := tx.ExecContext(ctx, stmt, updatedAt, newStatus, metadata, opErrStr, opUUID)
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

// GetOperationResources loads operation resources from the cluster db.
// The entity type is used as the key of the map, as the actual key is not stored in the DB.
func GetOperationResources(ctx context.Context, tx *sql.Tx, opID int64) (map[string][]api.URL, error) {
	stmt := `SELECT entity_id, entity_type FROM operations_resources WHERE operation_id = ?`

	// We cannot call GetEntityURL from within the scan function because it would start a new transaction.
	// So first we read all the entity IDs and types into a slice, then we loop over that slice to get the URLs.
	resources := []*struct {
		EntityID   int
		EntityType EntityType
	}{}
	err := query.Scan(ctx, tx, stmt, func(scan func(dest ...any) error) error {
		r := struct {
			EntityID   int
			EntityType EntityType
		}{}

		err := scan(&r.EntityID, &r.EntityType)
		if err != nil {
			return err
		}

		resources = append(resources, &r)

		return nil
	}, opID)
	if err != nil {
		return nil, fmt.Errorf("Failed reading operation resources: %w", err)
	}

	var result map[string][]api.URL
	for _, r := range resources {
		entityURL, err := GetEntityURL(ctx, tx, entity.Type(r.EntityType), r.EntityID)
		if err != nil {
			return nil, fmt.Errorf("Failed getting resource URL for entity type %q and ID %d: %w", r.EntityType, r.EntityID, err)
		}

		if result == nil {
			result = map[string][]api.URL{}
		}

		_, ok := result[string(r.EntityType)]
		if !ok {
			result[string(r.EntityType)] = []api.URL{}
		}

		// TODO we're using entityType as the key of the map, as the actual key is not stored in the DB.
		// Fix this.
		result[string(r.EntityType)] = append(result[string(r.EntityType)], *entityURL)
	}

	return result, nil
}

// CreateOperationResources registers operation resources in the cluster db.
// Returns entity_id of the first resource registered, or nil if no resources were registered.
func CreateOperationResources(ctx context.Context, tx *sql.Tx, opID int64, resources map[string][]api.URL) error {
	// No resources to register.
	if len(resources) == 0 {
		return nil
	}

	sb := strings.Builder{}
	sb.WriteString(`INSERT INTO operations_resources (operation_id, entity_id, entity_type) VALUES `)
	for _, entityURLs := range resources {
		for _, entityURL := range entityURLs {
			entityReference, err := GetEntityReferenceFromURL(ctx, tx, &entityURL)
			if err != nil {
				return fmt.Errorf("Failed getting entity ID from resource URL %q: %w", entityURL.String(), err)
			}

			entityTypeCode, err := entityReference.EntityType.Value()
			if err != nil {
				return fmt.Errorf("Failed getting entity type code for entity type %q: %w", entityReference.EntityType, err)
			}

			fmt.Fprintf(&sb, "(%d, %d, %d),", opID, entityReference.EntityID, entityTypeCode)
		}
	}

	// Get the final stmt and replace the trailing comma with a semicolon.
	insertStmt := sb.String()
	insertStmt = insertStmt[:len(insertStmt)-1] + ";"

	_, err := tx.ExecContext(ctx, insertStmt)
	if err != nil {
		return fmt.Errorf("Failed inserting operation resources: %w", err)
	}

	return nil
}

// UpdateOperationNodeID updates the node_id field of an existing operation in the cluster db.
func UpdateOperationNodeID(ctx context.Context, tx *sql.Tx, opUUID string, newNodeID int64, updatedAt time.Time) error {
	stmt := `UPDATE operations SET node_id = ?, updated_at = ? WHERE uuid = ?`

	result, err := tx.ExecContext(ctx, stmt, newNodeID, updatedAt, opUUID)
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

// DeleteNonDurableOperations deletes the operations on given node which are not durable.
func DeleteNonDurableOperations(ctx context.Context, tx *sql.Tx, nodeID int64) error {
	stmt := "DELETE FROM operations WHERE node_id = ? AND class != 4"

	_, err := tx.ExecContext(ctx, stmt, nodeID)
	if err != nil {
		return fmt.Errorf("Delete \"operations\": %w", err)
	}

	return nil
}
