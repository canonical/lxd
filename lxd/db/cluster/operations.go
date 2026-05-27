//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
)

// OperationsRow is a row of the operations table.
// db:model operations
type OperationsRow struct {
	ID int64 `db:"id"`

	// db:omit update
	UUID   string `db:"uuid"`
	NodeID int64  `db:"node_id"`

	// db:omit update
	Type operationtype.Type `db:"type"`

	// db:omit update
	ProjectID *int64 `db:"project_id"`

	// db:omit update
	RequestorProtocol *RequestorProtocol `db:"requestor_protocol"`

	// db:omit update
	RequestorIdentityID *int64 `db:"requestor_identity_id"`

	// db:omit update
	EntityID int64  `db:"entity_id"`
	Metadata string `db:"metadata"`

	// db:omit update
	Class int64 `db:"class"`

	// db:omit update
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`

	// db:omit update
	Inputs     string `db:"inputs"`
	StatusCode int64  `db:"status_code"`
	Error      string `db:"error"`

	// db:omit update
	ConflictReference string `db:"conflict_reference"`

	// db:omit update
	Parent *int64 `db:"parent"`

	// db:omit update
	Stage     int64 `db:"stage"`
	ErrorCode int64 `db:"error_code"`
}

// APIName implements [query.APINamer] for [OperationsRow] for API friendly error messages.
func (OperationsRow) APIName() string {
	return "Operation"
}

// Operation enriches an OperationsRow with project, node, and identity information.
// db:model operations
type Operation struct {
	Row OperationsRow

	// db:join LEFT JOIN projects ON operations.project_id = projects.id
	ProjectName string `db:"coalesce(projects.name, '')"`

	// db:join JOIN nodes ON operations.node_id = nodes.id
	NodeAddress string `db:"nodes.address"`
	NodeName    string `db:"nodes.name"`

	// db:join LEFT JOIN identities ON operations.requestor_identity_id = identities.id
	IdentityIdentifier string `db:"coalesce(identities.identifier, '')"`
}

// OperationFilter specifies potential query parameter fields.
type OperationFilter struct {
	ID     *int64
	NodeID *int64
	UUID   *string
	Parent *int64
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

// requestorProtocolCodeToText maps RequestorProtocol int64 database representation to its string constant.
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
		return fmt.Errorf("Unknown requestor protocol %d", requestorProtocolCode)
	}

	*r = RequestorProtocol(text)
	return nil
}

// Scan implements [sql.Scanner] for [RequestorProtocol]. This converts the integer value back into the correct constant or
// returns an error.
func (r *RequestorProtocol) Scan(value any) error {
	// We set allowNull to true here, but it doesn't really have any effect.
	// The SQL layer doesn't call the Scan method if the value is NULL, so we would never get a NULL value here.
	// If the value is NULL, the *RequestorProtocol is just not set and remains nil.
	// This is the desired behavior, as a NULL value in the database corresponds to a missing requestor protocol.
	return query.ScanValue(value, r, true)
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
func UpdateOperation(ctx context.Context, tx *sql.Tx, opUUID string, updatedAt time.Time, newStatus api.StatusCode, metadata string, opErr string, opErrCode int64) error {
	stmt := `UPDATE operations SET updated_at = ?, status_code = ?, metadata = ?, error = ?, error_code = ? WHERE uuid = ?`

	result, err := tx.ExecContext(ctx, stmt, updatedAt, newStatus, metadata, opErr, opErrCode, opUUID)
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
func GetOperationResources(ctx context.Context, tx *sql.Tx, opID int64) (map[entity.Type][]api.URL, error) {
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

	var result map[entity.Type][]api.URL
	for _, r := range resources {
		entityURL, err := GetEntityURL(ctx, tx, entity.Type(r.EntityType), r.EntityID)
		if err != nil {
			// If a delete operation has already deleted its resources, it's possible that some of the resources will not be found.
			// In that case, we just skip those resources and return the ones that are still there.
			if api.StatusErrorCheck(err, http.StatusNotFound) {
				logger.Debug("Failed loading resource URL for operation resource, skipping resource", logger.Ctx{"entity_type": r.EntityType, "entity_id": r.EntityID, "err": err})
				continue
			}

			return nil, fmt.Errorf("Failed loading resource URL for operation resource: %w", err)
		}

		if result == nil {
			result = map[entity.Type][]api.URL{}
		}

		_, ok := result[entity.Type(r.EntityType)]
		if !ok {
			result[entity.Type(r.EntityType)] = []api.URL{}
		}

		result[entity.Type(r.EntityType)] = append(result[entity.Type(r.EntityType)], *entityURL)
	}

	return result, nil
}

// GetParentOperations returns all parent operation, that is all operations that don't have a parent.
func GetParentOperations(ctx context.Context, tx *sql.Tx) ([]Operation, error) {
	stmt := `
SELECT operations.id, operations.uuid, nodes.address AS node_address, nodes.name, operations.project_id, operations.node_id, operations.type, operations.requestor_protocol, operations.requestor_identity_id, operations.entity_id, operations.metadata, operations.class, operations.created_at, operations.updated_at, operations.inputs, operations.status_code, operations.conflict_reference, operations.error, operations.error_code, operations.parent, operations.stage
  FROM operations
  JOIN nodes ON operations.node_id = nodes.id
  WHERE parent IS NULL
  ORDER BY operations.id, operations.uuid
`

	// Result slice.
	objects := make([]Operation, 0)

	dest := func(scan func(dest ...any) error) error {
		o := Operation{}
		err := scan(&o.ID, &o.UUID, &o.NodeAddress, &o.Location, &o.ProjectID, &o.NodeID, &o.Type, &o.RequestorProtocol, &o.RequestorIdentityID, &o.EntityID, &o.Metadata, &o.Class, &o.CreatedAt, &o.UpdatedAt, &o.Inputs, &o.Status, &o.ConflictReference, &o.Error, &o.ErrorCode, &o.Parent, &o.Stage)
		if err != nil {
			return err
		}

		objects = append(objects, o)

		return nil
	}

	err := query.Scan(ctx, tx, stmt, dest)
	if err != nil {
		return nil, fmt.Errorf("Failed fetching from \"operations\" table: %w", err)
	}

	return objects, nil
}

// CreateOperationResources registers operation resources in the cluster db.
func CreateOperationResources(ctx context.Context, tx *sql.Tx, opID int64, resources map[entity.Type][]api.URL) error {
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

// deleteEphemeralOperationsFromNodes deletes ephemeral operations from nodes with the given list of IDs.
// Ephemeral operations are operations which are normally cleared few seconds after they finish. In other words, these are:
// - Operations with class Task, Websocket or Token (class between 1 and 3), and
// - Operations which are not bulk operations (parent is NULL and id not in parent column of any operation).
func deleteEphemeralOperationsFromNodes(ctx context.Context, tx *sql.Tx, nodeIDs ...int64) error {
	stmt := `DELETE FROM operations
WHERE class BETWEEN 1 AND 3
AND parent IS NULL
AND id NOT IN (SELECT parent FROM operations WHERE parent IS NOT NULL)
AND node_id IN ` + query.IntParams(nodeIDs...)

	_, err := tx.ExecContext(ctx, stmt)
	if err != nil {
		return fmt.Errorf("Failed deleting operations from nodes: %w", err)
	}

	return nil
}

// failRunningBulkOperationsFromNodes marks all running bulk operations on target nodes as failed.
// Bulk operations are persisted in the database for 24 hours, thus are not ephemeral. Yet, if a node crashes or shuts down,
// the bulk operations which were not running on the node previously are not going to finish and need to be marked accordingly.
func failRunningBulkOperationsFromNodes(ctx context.Context, tx *sql.Tx, nodeIDs ...int64) error {
	stmt := `UPDATE operations SET updated_at = ?, status_code = ?, error = ?, error_code = ?
WHERE class BETWEEN 1 AND 3
AND status_code < 200
AND (parent IS NOT NULL OR id IN (SELECT parent FROM operations WHERE parent IS NOT NULL))
AND node_id IN ` + query.IntParams(nodeIDs...)

	_, err := tx.ExecContext(ctx, stmt, time.Now(), api.Failure, "Member shut down", http.StatusServiceUnavailable)
	if err != nil {
		return fmt.Errorf("Failed marking bulk operations as failed: %w", err)
	}

	return nil
}

// ClearStaleOperationsFromNodes clears all stale operation records from the database for the given node IDs. This includes:
// - Deleting ephemeral operations, which are operations that are normally cleared few seconds after they finish.
// - Marking running bulk operations as failed.
func ClearStaleOperationsFromNodes(ctx context.Context, tx *sql.Tx, nodeIDs ...int64) error {
	err := deleteEphemeralOperationsFromNodes(ctx, tx, nodeIDs...)
	if err != nil {
		return fmt.Errorf("Failed deleting ephemeral operations from nodes: %w", err)
	}

	err = failRunningBulkOperationsFromNodes(ctx, tx, nodeIDs...)
	if err != nil {
		return fmt.Errorf("Failed failing running bulk operations from nodes: %w", err)
	}

	return nil
}

const operationInfo = `
SELECT 
    operations.id, 
    operations.uuid,
    operations.node_id,
    nodes.address, 
    nodes.name,
    operations.project_id, 
    coalesce(projects.name, '') AS project_name,
    operations.type, 
    operations.requestor_protocol, 
    operations.requestor_identity_id,
    coalesce(identities.identifier, ''),
    operations.entity_id, 
    operations.metadata, 
    operations.class, 
    operations.created_at, 
    operations.updated_at, 
    operations.inputs, 
    operations.status_code, 
    operations.conflict_reference, 
    operations.error, 
    operations.parent, 
    operations.stage
FROM operations
JOIN nodes ON operations.node_id = nodes.id
LEFT JOIN projects ON operations.project_id = projects.id
LEFT JOIN identities ON operations.requestor_identity_id = identities.id
`

// OperationInfo embeds [Operation] to enrich the data via some left joins.
// To be removed when we have replaced the database generator.
type OperationInfo struct {
	Operation
	Location           string
	ProjectName        string
	IdentityIdentifier string
}

// getOperationInfos returns a slice of [OperationInfo] whose elements conform to the given clause.
func getOperationInfos(ctx context.Context, tx *sql.Tx, clause string, args ...any) ([]OperationInfo, error) {
	var ops []OperationInfo
	err := query.Scan(ctx, tx, operationInfo+clause, func(scan func(dest ...any) error) error {
		var op OperationInfo
		err := scan(
			&op.ID,
			&op.UUID,
			&op.NodeID,
			&op.NodeAddress,
			&op.Location,
			&op.ProjectID,
			&op.ProjectName,
			&op.Type,
			&op.RequestorProtocol,
			&op.RequestorIdentityID,
			&op.IdentityIdentifier,
			&op.EntityID,
			&op.Metadata,
			&op.Class,
			&op.CreatedAt,
			&op.UpdatedAt,
			&op.Inputs,
			&op.Status,
			&op.ConflictReference,
			&op.Error,
			&op.Parent,
			&op.Stage,
		)
		if err != nil {
			return err
		}

		ops = append(ops, op)
		return nil
	}, args...)
	if err != nil {
		return nil, err
	}

	return ops, nil
}

// GetOperationInfosByProjectAndType returns a slice of [OperationInfo] with the given project and type.
func GetOperationInfosByProjectAndType(ctx context.Context, tx *sql.Tx, projectName string, opType operationtype.Type) ([]OperationInfo, error) {
	return getOperationInfos(ctx, tx, "WHERE coalesce(projects.name, '') = ? AND operations.type = ?", projectName, opType)
}
